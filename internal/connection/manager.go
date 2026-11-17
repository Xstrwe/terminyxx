package connection

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"terminyxx/internal/recording"
)

type EventEmitter interface {
	Emit(event string, data ...interface{})
}

var ErrNotFound = errors.New("connection not found")

type ConnectionManager struct {
	mu          sync.RWMutex
	connections map[string]*ManagedConnection
	streams     map[string]*streamConn
	emitter     EventEmitter
	hostKeys    *HostKeyStore
	gate        *OutputGate
	policy      atomic.Pointer[Policy]

	recordingsDir string

	jobsMu sync.Mutex
	jobs   map[string]context.CancelFunc
}

// NewConnectionManager creates a manager. hostKeys may be nil, which
// disables host key verification (used only in tests).
func NewConnectionManager(emitter EventEmitter, hostKeys *HostKeyStore) *ConnectionManager {
	m := &ConnectionManager{
		connections: make(map[string]*ManagedConnection),
		streams:     make(map[string]*streamConn),
		emitter:     emitter,
		hostKeys:    hostKeys,
		gate:        NewOutputGate(),
		jobs:        make(map[string]context.CancelFunc),
	}
	p := DefaultPolicy()
	m.policy.Store(&p)
	return m
}

// SetPolicy applies new security settings. Masking takes effect on the next
// chunk of output; algorithm and agent settings on the next connection.
func (m *ConnectionManager) SetPolicy(p Policy) {
	m.policy.Store(&p)
}

func (m *ConnectionManager) Policy() Policy {
	return *m.policy.Load()
}

func (m *ConnectionManager) maskOptions() MaskOptions {
	p := m.policy.Load()
	return MaskOptions{Enabled: p.MaskOutput, IPs: p.MaskIPs}
}

// ErrLocked is returned for terminal input while the app is locked.
var ErrLocked = errors.New("app is locked")

// SetLocked pauses (true) or resumes terminal output. While locked, output
// is queued in memory and keyboard input is refused; sessions stay open.
func (m *ConnectionManager) SetLocked(locked bool) {
	m.gate.SetLocked(locked)
}

// TrustedHosts lists known_hosts entries (the app's own, then OpenSSH's).
func (m *ConnectionManager) TrustedHosts() ([]TrustedHost, error) {
	if m.hostKeys == nil {
		return []TrustedHost{}, nil
	}
	return m.hostKeys.List()
}

// RemoveTrustedHost revokes one entry of the app's known_hosts.
func (m *ConnectionManager) RemoveTrustedHost(id string) error {
	if m.hostKeys == nil {
		return ErrNotFound
	}
	return m.hostKeys.Remove(id)
}

func (m *ConnectionManager) Connect(req ConnectRequest) error {
	if req.ID == "" {
		return errors.New("connection id is required")
	}
	if req.Host == "" {
		return errors.New("host is required")
	}

	m.emitter.Emit("connection:state:"+req.ID, "connecting")
	// Dial without holding the lock so other tabs stay responsive.
	conn, err := newManagedConnection(req, m)
	if err != nil {
		m.emitter.Emit("connection:state:"+req.ID, "disconnected")
		return err
	}

	m.mu.Lock()
	old := m.connections[req.ID]
	oldStream := m.streams[req.ID]
	delete(m.streams, req.ID)
	m.connections[req.ID] = conn
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if oldStream != nil {
		oldStream.close()
	}

	go func() {
		conn.Wait()
		m.handleClosed(req.ID, conn)
	}()

	m.emitter.Emit("connection:state:"+req.ID, "connected")
	return nil
}

// handleClosed runs when a connection drops for any reason (shell exit,
// network loss, keepalive timeout, explicit disconnect).
func (m *ConnectionManager) handleClosed(id string, conn *ManagedConnection) {
	m.mu.Lock()
	current := m.connections[id] == conn
	if current {
		delete(m.connections, id)
	}
	m.mu.Unlock()
	_ = conn.Close()
	if current {
		m.emitter.Emit("connection:state:"+id, "disconnected")
	}
}

func (m *ConnectionManager) Disconnect(id string) error {
	m.mu.Lock()
	conn, ok := m.connections[id]
	delete(m.connections, id)
	st := m.streams[id]
	delete(m.streams, id)
	m.mu.Unlock()
	if st != nil {
		st.close()
		m.emitter.Emit("connection:state:"+id, "disconnected")
		return nil
	}
	if !ok {
		return nil
	}
	err := conn.Close()
	m.emitter.Emit("connection:state:"+id, "disconnected")
	return err
}

func (m *ConnectionManager) get(id string) (*ManagedConnection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	conn, ok := m.connections[id]
	if !ok {
		return nil, ErrNotFound
	}
	return conn, nil
}

func (m *ConnectionManager) sftp(id string) (*SFTPBridge, error) {
	conn, err := m.get(id)
	if err != nil {
		return nil, err
	}
	return conn.SFTP()
}

func (m *ConnectionManager) StartShell(id string, cols, rows int) error {
	cols, rows = clampTermSize(cols, rows)
	if st := m.stream(id); st != nil {
		st.resize(cols, rows)
		st.start()
		return nil
	}
	conn, err := m.get(id)
	if err != nil {
		return err
	}
	return conn.StartShell(cols, rows)
}

func (m *ConnectionManager) WriteInput(id, data string) error {
	if m.gate.Locked() {
		return ErrLocked
	}
	if st := m.stream(id); st != nil {
		return st.write([]byte(data))
	}
	conn, err := m.get(id)
	if err != nil {
		return err
	}
	return conn.WriteInput(data)
}

func (m *ConnectionManager) ResizePTY(id string, cols, rows int) error {
	cols, rows = clampTermSize(cols, rows)
	if st := m.stream(id); st != nil {
		st.resize(cols, rows)
		return nil
	}
	conn, err := m.get(id)
	if err != nil {
		return err
	}
	return conn.ResizePTY(cols, rows)
}

func (m *ConnectionManager) ListDir(id, p string) ([]FileEntry, error) {
	b, err := m.sftp(id)
	if err != nil {
		return nil, err
	}
	return b.ListDir(p)
}

func (m *ConnectionManager) Mkdir(id, p string) error {
	b, err := m.sftp(id)
	if err != nil {
		return err
	}
	return b.Mkdir(p)
}

func (m *ConnectionManager) Remove(id, p string) error {
	b, err := m.sftp(id)
	if err != nil {
		return err
	}
	return b.Remove(p)
}

func (m *ConnectionManager) Rename(id, oldPath, newPath string) error {
	b, err := m.sftp(id)
	if err != nil {
		return err
	}
	return b.Rename(oldPath, newPath)
}

func (m *ConnectionManager) ReadRemoteFile(id, p string) (string, error) {
	b, err := m.sftp(id)
	if err != nil {
		return "", err
	}
	return b.ReadFile(p)
}

func (m *ConnectionManager) GetRemoteHome(id string) (string, error) {
	b, err := m.sftp(id)
	if err != nil {
		return "", err
	}
	return b.Getwd()
}

// Transfer runs a copy/move job; it returns when the job ends. CancelTransfer
// with the same JobID stops it.
func (m *ConnectionManager) Transfer(id string, req TransferRequest) error {
	if req.JobID == "" {
		return errors.New("job id is required")
	}
	b, err := m.sftp(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.jobsMu.Lock()
	if _, exists := m.jobs[req.JobID]; exists {
		m.jobsMu.Unlock()
		cancel()
		return fmt.Errorf("job %s already running", req.JobID)
	}
	m.jobs[req.JobID] = cancel
	m.jobsMu.Unlock()
	defer func() {
		m.jobsMu.Lock()
		delete(m.jobs, req.JobID)
		m.jobsMu.Unlock()
		cancel()
	}()
	return b.Transfer(ctx, req, id, m.emitter)
}

func (m *ConnectionManager) CancelTransfer(jobID string) {
	m.jobsMu.Lock()
	cancel := m.jobs[jobID]
	m.jobsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *ConnectionManager) GetLocalHome() (string, error) {
	return os.UserHomeDir()
}

func (m *ConnectionManager) CloseAll() {
	m.jobsMu.Lock()
	for _, cancel := range m.jobs {
		cancel()
	}
	m.jobsMu.Unlock()

	m.mu.Lock()
	conns := m.connections
	m.connections = make(map[string]*ManagedConnection)
	streams := m.streams
	m.streams = make(map[string]*streamConn)
	m.mu.Unlock()
	for _, st := range streams {
		st.close()
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
}

// StartTunnel starts a port forward on an open connection.
func (m *ConnectionManager) StartTunnel(id string, spec TunnelSpec) (TunnelInfo, error) {
	conn, err := m.get(id)
	if err != nil {
		return TunnelInfo{}, err
	}
	return conn.StartTunnel(spec)
}

func (m *ConnectionManager) StopTunnel(id, tunnelID string) error {
	conn, err := m.get(id)
	if err != nil {
		return err
	}
	return conn.StopTunnel(tunnelID)
}

func (m *ConnectionManager) ListTunnels(id string) ([]TunnelInfo, error) {
	conn, err := m.get(id)
	if err != nil {
		return []TunnelInfo{}, nil
	}
	return conn.Tunnels(), nil
}

// SetRecordingsDir sets where session recordings are written.
func (m *ConnectionManager) SetRecordingsDir(dir string) {
	m.mu.Lock()
	m.recordingsDir = dir
	m.mu.Unlock()
}

// StartRecording records the session of connection id; it returns the file name.
func (m *ConnectionManager) StartRecording(id, title string) (string, error) {
	if st := m.stream(id); st != nil {
		m.mu.RLock()
		dir := m.recordingsDir
		m.mu.RUnlock()
		if dir == "" {
			return "", errors.New("recordings folder is not configured")
		}
		if st.rec.Load() != nil {
			return "", errors.New("already recording")
		}
		r, name, err := recording.Start(dir, title, 120, 30)
		if err != nil {
			return "", err
		}
		st.rec.Store(r)
		return name, nil
	}
	conn, err := m.get(id)
	if err != nil {
		return "", err
	}
	m.mu.RLock()
	dir := m.recordingsDir
	m.mu.RUnlock()
	if dir == "" {
		return "", errors.New("recordings folder is not configured")
	}
	return conn.StartRecording(dir, title)
}

func (m *ConnectionManager) StopRecording(id string) error {
	if st := m.stream(id); st != nil {
		if r := st.rec.Swap(nil); r != nil {
			r.Close()
		}
		return nil
	}
	conn, err := m.get(id)
	if err != nil {
		return err
	}
	conn.StopRecording()
	return nil
}

func (m *ConnectionManager) IsRecording(id string) bool {
	if st := m.stream(id); st != nil {
		return st.rec.Load() != nil
	}
	conn, err := m.get(id)
	return err == nil && conn.Recording()
}
