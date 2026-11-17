package connection

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// Tunnel kinds.
const (
	TunnelLocal   = "local"   // -L: listen here, connect to Dest from the server
	TunnelRemote  = "remote"  // -R: listen on the server, connect to Dest from here
	TunnelDynamic = "dynamic" // -D: local SOCKS5 proxy through the server
)

// TunnelSpec describes one port forward.
type TunnelSpec struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	BindHost string `json:"bindHost"` // where to listen (default 127.0.0.1)
	BindPort int    `json:"bindPort"` // 0 = pick a free port (local/dynamic)
	DestHost string `json:"destHost"` // unused for dynamic
	DestPort int    `json:"destPort"`
}

// TunnelInfo is a running tunnel as shown in the UI.
type TunnelInfo struct {
	TunnelSpec
	// ListenAddr is the address actually bound (useful when BindPort was 0).
	ListenAddr string `json:"listenAddr"`
	// Active is the number of connections currently being forwarded.
	Active int64 `json:"active"`
	// Total counts every connection accepted so far.
	Total int64 `json:"total"`
}

// maxTunnelConns caps the connections one tunnel forwards at a time, so a
// flood of local clients cannot exhaust file descriptors or SSH channels.
const maxTunnelConns = 256

// isLoopbackBind reports whether host only accepts connections from this
// computer.
func isLoopbackBind(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type tunnel struct {
	spec     TunnelSpec
	listener net.Listener
	active   atomic.Int64
	total    atomic.Int64
	cancel   context.CancelFunc
}

func (t *tunnel) info() TunnelInfo {
	return TunnelInfo{
		TunnelSpec: t.spec,
		ListenAddr: t.listener.Addr().String(),
		Active:     t.active.Load(),
		Total:      t.total.Load(),
	}
}

// tunnelSet holds the tunnels of one connection.
type tunnelSet struct {
	mu sync.Mutex
	m  map[string]*tunnel
}

func (c *ManagedConnection) tunnels() *tunnelSet {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tunnelSet == nil {
		c.tunnelSet = &tunnelSet{m: map[string]*tunnel{}}
	}
	return c.tunnelSet
}

// StartTunnel opens the listener and starts forwarding.
func (c *ManagedConnection) StartTunnel(spec TunnelSpec) (TunnelInfo, error) {
	client := c.Client()
	if client == nil {
		return TunnelInfo{}, errors.New("not connected")
	}
	if spec.ID == "" {
		return TunnelInfo{}, errors.New("tunnel id is required")
	}
	if spec.BindHost == "" {
		if spec.Kind == TunnelRemote {
			spec.BindHost = "localhost"
		} else {
			spec.BindHost = "127.0.0.1"
		}
	}
	switch spec.Kind {
	case TunnelLocal, TunnelRemote:
		if spec.DestHost == "" || spec.DestPort <= 0 || spec.DestPort > 65535 {
			return TunnelInfo{}, errors.New("destination host and port are required")
		}
	case TunnelDynamic:
	default:
		return TunnelInfo{}, fmt.Errorf("unknown tunnel kind %q", spec.Kind)
	}
	if spec.BindPort < 0 || spec.BindPort > 65535 || (spec.Kind == TunnelRemote && spec.BindPort == 0) {
		return TunnelInfo{}, errors.New("invalid bind port")
	}

	set := c.tunnels()
	set.mu.Lock()
	defer set.mu.Unlock()
	if _, ok := set.m[spec.ID]; ok {
		return TunnelInfo{}, fmt.Errorf("tunnel %s is already running", spec.ID)
	}

	if spec.Kind != TunnelRemote && !isLoopbackBind(spec.BindHost) {
		// Anyone on the network can use this forward (for a SOCKS proxy:
		// reach anything the server can, without authentication).
		log.Printf("tunnel %s: listening on non-loopback address %s, reachable from other computers", spec.ID, spec.BindHost)
	}
	bind := net.JoinHostPort(spec.BindHost, strconv.Itoa(spec.BindPort))
	var ln net.Listener
	var err error
	if spec.Kind == TunnelRemote {
		ln, err = client.Listen("tcp", bind)
	} else {
		ln, err = net.Listen("tcp", bind)
	}
	if err != nil {
		return TunnelInfo{}, fmt.Errorf("listen on %s: %w", bind, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &tunnel{spec: spec, listener: ln, cancel: cancel}
	set.m[spec.ID] = t
	go t.serve(ctx, client)
	return t.info(), nil
}

func (t *tunnel) serve(ctx context.Context, client *ssh.Client) {
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			return // listener closed
		}
		t.total.Add(1)
		if t.active.Load() >= maxTunnelConns {
			_ = conn.Close()
			continue
		}
		t.active.Add(1)
		go func() {
			defer t.active.Add(-1)
			defer conn.Close()
			// Stopping the tunnel closes its open connections too.
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			defer stop()
			switch t.spec.Kind {
			case TunnelLocal:
				t.pipeTo(ctx, conn, func() (net.Conn, error) {
					return client.Dial("tcp", net.JoinHostPort(t.spec.DestHost, strconv.Itoa(t.spec.DestPort)))
				})
			case TunnelRemote:
				t.pipeTo(ctx, conn, func() (net.Conn, error) {
					return net.DialTimeout("tcp", net.JoinHostPort(t.spec.DestHost, strconv.Itoa(t.spec.DestPort)), dialTimeout)
				})
			case TunnelDynamic:
				serveSOCKS5(ctx, conn, client)
			}
		}()
	}
}

func (t *tunnel) pipeTo(ctx context.Context, src net.Conn, dial func() (net.Conn, error)) {
	dst, err := dial()
	if err != nil {
		return
	}
	defer dst.Close()
	pipe(ctx, src, dst)
}

// pipe copies both ways until either side closes or ctx is cancelled.
func pipe(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		// Half-close so the peer sees EOF but the other direction can finish.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	select {
	case <-done:
		// Give the other direction a moment to drain, then tear down.
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
		}
	case <-ctx.Done():
	}
	_ = a.Close()
	_ = b.Close()
}

// StopTunnel closes one tunnel.
func (c *ManagedConnection) StopTunnel(id string) error {
	set := c.tunnels()
	set.mu.Lock()
	t, ok := set.m[id]
	delete(set.m, id)
	set.mu.Unlock()
	if !ok {
		return nil
	}
	t.cancel()
	return t.listener.Close()
}

// Tunnels lists the running tunnels.
func (c *ManagedConnection) Tunnels() []TunnelInfo {
	set := c.tunnels()
	set.mu.Lock()
	defer set.mu.Unlock()
	out := make([]TunnelInfo, 0, len(set.m))
	for _, t := range set.m {
		out = append(out, t.info())
	}
	return out
}

func (c *ManagedConnection) closeTunnels() {
	set := c.tunnels()
	set.mu.Lock()
	defer set.mu.Unlock()
	for id, t := range set.m {
		t.cancel()
		_ = t.listener.Close()
		delete(set.m, id)
	}
}

// serveSOCKS5 implements the no-auth CONNECT subset of SOCKS5 (RFC 1928).
func serveSOCKS5(ctx context.Context, conn net.Conn, client *ssh.Client) {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 5 {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	noAuth := false
	for _, m := range methods {
		if m == 0 {
			noAuth = true
		}
	}
	if !noAuth {
		_, _ = conn.Write([]byte{5, 0xff})
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil || req[0] != 5 {
		return
	}
	if req[1] != 1 { // only CONNECT
		socksReply(conn, 7)
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 4:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(conn, l); err != nil {
			return
		}
		name := make([]byte, l[0])
		if _, err := io.ReadFull(conn, name); err != nil {
			return
		}
		host = string(name)
	default:
		socksReply(conn, 8)
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(conn, pb); err != nil {
		return
	}
	port := int(pb[0])<<8 | int(pb[1])

	dst, err := client.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		socksReply(conn, 5) // connection refused
		return
	}
	defer dst.Close()
	socksReply(conn, 0)
	_ = conn.SetDeadline(time.Time{})
	pipe(ctx, conn, dst)
}

func socksReply(conn net.Conn, code byte) {
	_, _ = conn.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
}
