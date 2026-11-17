package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"terminyxx/internal/capture"
	"terminyxx/internal/connection"
	"terminyxx/internal/importers"
	"terminyxx/internal/rdp"
	"terminyxx/internal/rdpgw"
	"terminyxx/internal/recording"
	"terminyxx/internal/security"
	"terminyxx/internal/store"
	"terminyxx/internal/syncfile"
)

type WailsEmitter struct {
	ctx context.Context
}

func (e *WailsEmitter) Emit(event string, data ...interface{}) {
	runtime.EventsEmit(e.ctx, event, data...)
}

type App struct {
	ctx     context.Context
	connMgr *connection.ConnectionManager
	data    *store.Store
	rdp     rdp.Launcher
	rdpgw   *rdpgw.Gateway
	stop    chan struct{}
	sec     securityState

	recordingsDir string
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	dataStore, err := store.New(store.NewOSKeyring())
	if err != nil {
		runtime.LogError(ctx, "failed to init store: "+err.Error())
	} else {
		a.data = dataStore
	}

	knownHosts := "known_hosts"
	if dir, err := store.DataDir(); err == nil {
		knownHosts = filepath.Join(dir, "known_hosts")
	}
	hostKeys := connection.NewHostKeyStore(knownHosts, connection.DefaultUserKnownHosts())
	a.connMgr = connection.NewConnectionManager(&WailsEmitter{ctx: ctx}, hostKeys)
	a.recordingsDir = "recordings"
	if dir, err := store.DataDir(); err == nil {
		a.recordingsDir = filepath.Join(dir, "recordings")
	}
	a.connMgr.SetRecordingsDir(a.recordingsDir)
	a.rdp = rdp.NewLauncher()
	rdpCerts := "rdp_known_certs"
	if dir, err := store.DataDir(); err == nil {
		rdpCerts = filepath.Join(dir, "rdp_known_certs")
	}
	a.rdpgw = rdpgw.New(rdpgw.NewTrustStore(rdpCerts))

	a.stop = make(chan struct{})
	a.initSecurity()
	go a.watchScreenSharing()
	a.startAutoUpdate()
}

// watchScreenSharing polls for screen sharing and emits "capture:sharing"
// (a capture.SharingState) when it starts or stops, so the UI can show the
// "hidden from screen sharing" notice only while a share is running.
func (a *App) watchScreenSharing() {
	last := capture.DetectSharing()
	if !last.Supported {
		return
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			cur := capture.DetectSharing()
			if !reflect.DeepEqual(cur, last) {
				last = cur
				runtime.EventsEmit(a.ctx, "capture:sharing", cur)
			}
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	if a.stop != nil {
		close(a.stop)
	}
	applyPendingUpdate(false)
	// Don't leave a copied secret on the clipboard after the app is gone.
	if a.sec.clip != nil {
		a.sec.clip.ClearNow()
	}
	if a.connMgr != nil {
		a.connMgr.CloseAll()
	}
	if a.rdpgw != nil {
		a.rdpgw.Shutdown()
	}
	if a.data != nil {
		_ = a.data.Close()
	}
}

// SSH bindings

func (a *App) SSHConnect(req connection.ConnectRequest) error {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return connection.ErrLocked
	}
	// Saved hosts keep their password in the OS keychain; the frontend never
	// receives it, so resolve it here.
	// The saved password is only ever sent to the saved address: host, port,
	// user and auth settings come from the stored profile, not from the
	// request, so a tampered request cannot redirect the credential.
	if req.Password == "" && req.ProfileID != "" && a.data != nil {
		if p, err := a.data.GetSSHProfile(req.ProfileID); err == nil {
			if pw, err := a.data.SSHPassword(p.ID); err == nil && pw != "" {
				req.Password = pw
				req.Host = p.Host
				req.Port = p.Port
				req.Username = p.Username
				req.AuthType = p.AuthType
				req.KeyPath = p.KeyPath
				req.AllowLegacyAlgorithms = p.AllowLegacyAlgorithms
				req.AgentForwarding = p.AgentForwarding
			}
		}
	}
	if req.JumpFor != "" && a.data != nil && len(req.Jump) == 0 {
		jump, err := a.resolveJumpChain(req.JumpFor)
		if err != nil {
			return err
		}
		req.Jump = jump
	}
	if err := a.connMgr.Connect(req); err != nil {
		return err
	}
	a.autoStartTunnels(req)
	return nil
}

// resolveJumpChain follows the saved profile's JumpProfileID links and
// returns the hops in connection order (outermost jump host first).
func (a *App) resolveJumpChain(profileID string) ([]connection.ConnectRequest, error) {
	var chain []connection.ConnectRequest
	seen := map[string]bool{profileID: true}
	cur, err := a.data.GetSSHProfile(profileID)
	if err != nil {
		return nil, nil // unsaved or missing profile: no jump
	}
	for cur.JumpProfileID != "" {
		if seen[cur.JumpProfileID] {
			return nil, errors.New("jump host loop: a host cannot be reached through itself")
		}
		if len(chain) >= 5 {
			return nil, errors.New("too many jump hosts (max 5)")
		}
		seen[cur.JumpProfileID] = true
		hop, err := a.data.GetSSHProfile(cur.JumpProfileID)
		if err != nil {
			return nil, fmt.Errorf("jump host not found: %w", err)
		}
		pw, _ := a.data.SSHPassword(hop.ID)
		chain = append([]connection.ConnectRequest{{
			Host: hop.Host, Port: hop.Port, Username: hop.Username, AuthType: hop.AuthType,
			Password: pw, KeyPath: hop.KeyPath, ProfileID: hop.ID,
			AllowLegacyAlgorithms: hop.AllowLegacyAlgorithms,
		}}, chain...)
		cur = hop
	}
	return chain, nil
}

// autoStartTunnels starts the saved forwards marked "start with host".
func (a *App) autoStartTunnels(req connection.ConnectRequest) {
	if req.JumpFor == "" || a.data == nil {
		return
	}
	p, err := a.data.GetSSHProfile(req.JumpFor)
	if err != nil {
		return
	}
	for _, t := range p.Tunnels {
		if !t.AutoStart {
			continue
		}
		spec := connection.TunnelSpec{ID: t.ID, Kind: t.Kind, BindHost: t.BindHost, BindPort: t.BindPort, DestHost: t.DestHost, DestPort: t.DestPort}
		if _, err := a.connMgr.StartTunnel(req.ID, spec); err != nil {
			log.Printf("tunnel %s: %v", t.ID, err)
		}
	}
}

// SFTPReadForEdit loads a remote text file for the built-in editor.
func (a *App) SFTPReadForEdit(id, path string) (connection.EditFile, error) {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return connection.EditFile{}, connection.ErrLocked
	}
	return a.connMgr.ReadForEdit(id, path)
}

// SFTPWriteEdited saves the editor's content. version is the one returned by
// SFTPReadForEdit; a changed file is refused unless force is set.
func (a *App) SFTPWriteEdited(id, path, content, version string, force bool) (string, error) {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return "", connection.ErrLocked
	}
	return a.connMgr.WriteEdited(id, path, content, version, force)
}

// StreamConnect opens a Telnet or serial session. It shows up in the UI like
// an SSH tab (same events), but has no SFTP, tunnels or host monitor.
func (a *App) StreamConnect(req connection.StreamRequest) error {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return connection.ErrLocked
	}
	return a.connMgr.ConnectStream(req)
}

// SerialPorts lists the serial devices on this machine.
func (a *App) SerialPorts() ([]string, error) {
	return a.connMgr.SerialPorts()
}

// ListContainers lists running Docker/Podman containers or Kubernetes pods
// on the server of an SSH connection.
func (a *App) ListContainers(id, runtime string) ([]connection.Container, error) {
	if err := a.requireUnlocked(); err != nil {
		return nil, err
	}
	return a.connMgr.ListContainers(id, runtime)
}

// HostStats reads CPU, memory, disk, load and network of a Linux server
// over a separate SSH exec channel.
func (a *App) HostStats(id string) (connection.HostStats, error) {
	return a.connMgr.HostStats(id)
}

// Session recording bindings

func (a *App) StartRecording(id, title string) (string, error) {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return "", connection.ErrLocked
	}
	return a.connMgr.StartRecording(id, title)
}

func (a *App) StopRecording(id string) error { return a.connMgr.StopRecording(id) }

func (a *App) IsRecording(id string) bool { return a.connMgr.IsRecording(id) }

func (a *App) ListRecordings() ([]recording.Info, error) {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return nil, connection.ErrLocked
	}
	return recording.List(a.recordingsDir)
}

func (a *App) ReadRecording(name string) (recording.Recording, error) {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return recording.Recording{}, connection.ErrLocked
	}
	return recording.Read(a.recordingsDir, name)
}

func (a *App) DeleteRecording(name string) error {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return connection.ErrLocked
	}
	return recording.Delete(a.recordingsDir, name)
}

func (a *App) SearchRecordings(query string) ([]recording.Hit, error) {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return nil, connection.ErrLocked
	}
	return recording.Search(a.recordingsDir, query, 200)
}

// OpenRecordingsFolder shows the recordings in the file manager.
func (a *App) OpenRecordingsFolder() error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if err := os.MkdirAll(a.recordingsDir, 0o700); err != nil {
		return err
	}
	// BrowserOpenURL only accepts URLs (a plain path is rejected by Wails'
	// URL validator), so hand the folder to the OS file manager instead.
	return openFolder(a.recordingsDir)
}

// Sync file bindings

// ExportSync writes hosts, folders and snippets to a passphrase-encrypted
// file chosen by the user (e.g. inside a cloud-synced folder). It returns the
// path, or "" if the user cancelled.
func (a *App) ExportSync(passphrase string, includePasswords bool) (string, error) {
	if a.data == nil {
		return "", errors.New("storage is unavailable")
	}
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return "", connection.ErrLocked
	}
	if len(passphrase) < 8 {
		return "", errors.New("passphrase must be at least 8 characters")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save encrypted sync file",
		DefaultFilename: "terminyxx.sync",
		Filters:         []runtime.FileFilter{{DisplayName: "Terminyxx sync file", Pattern: "*.sync"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	bundle, err := a.data.ExportBundle(includePasswords)
	if err != nil {
		return "", err
	}
	plain, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	sealed, err := syncfile.Seal(plain, passphrase)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, sealed, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ImportSync merges a sync file into the local data. Items with the same ID
// are replaced; nothing is deleted.
func (a *App) ImportSync(passphrase string) (store.ImportResult, error) {
	var res store.ImportResult
	if a.data == nil {
		return res, errors.New("storage is unavailable")
	}
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return res, connection.ErrLocked
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Open encrypted sync file",
		Filters: []runtime.FileFilter{{DisplayName: "Terminyxx sync file", Pattern: "*.sync"}, {DisplayName: "All files", Pattern: "*.*"}},
	})
	if err != nil || path == "" {
		return res, err
	}
	data, err := readFileLimited(path, maxSyncFileSize)
	if err != nil {
		return res, err
	}
	plain, err := syncfile.Open(data, passphrase)
	if err != nil {
		return res, err
	}
	var bundle store.Bundle
	if err := json.Unmarshal(plain, &bundle); err != nil {
		return res, fmt.Errorf("sync file is damaged: %w", err)
	}
	return a.data.ImportBundle(bundle)
}

// Snippet bindings

func (a *App) ListSnippets() ([]store.Snippet, error) {
	if a.data == nil {
		return []store.Snippet{}, nil
	}
	return a.data.ListSnippets()
}

func (a *App) SaveSnippet(sn store.Snippet) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return nil
	}
	return a.data.SaveSnippet(sn)
}

func (a *App) DeleteSnippet(id string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return nil
	}
	return a.data.DeleteSnippet(id)
}

// Tunnel bindings

func (a *App) StartTunnel(id string, spec connection.TunnelSpec) (connection.TunnelInfo, error) {
	if err := a.requireUnlocked(); err != nil {
		return connection.TunnelInfo{}, err
	}
	return a.connMgr.StartTunnel(id, spec)
}

func (a *App) StopTunnel(id, tunnelID string) error {
	return a.connMgr.StopTunnel(id, tunnelID)
}

func (a *App) ListTunnels(id string) ([]connection.TunnelInfo, error) {
	return a.connMgr.ListTunnels(id)
}

func (a *App) SSHDisconnect(id string) error {
	return a.connMgr.Disconnect(id)
}

// SSHStartShell opens the PTY; the terminal calls it once it is listening
// for output, and again on remount (then it only resizes).
func (a *App) SSHStartShell(id string, cols, rows int) error {
	return a.connMgr.StartShell(id, cols, rows)
}

func (a *App) SSHWriteInput(id, data string) error {
	return a.connMgr.WriteInput(id, data)
}

func (a *App) SSHResizePTY(id string, cols, rows int) error {
	return a.connMgr.ResizePTY(id, cols, rows)
}

// SFTP bindings

func (a *App) SFTPListDir(id, path string) ([]connection.FileEntry, error) {
	if err := a.requireUnlocked(); err != nil {
		return nil, err
	}
	return a.connMgr.ListDir(id, path)
}

func (a *App) SFTPMkdir(id, path string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	return a.connMgr.Mkdir(id, path)
}

func (a *App) SFTPRemove(id, path string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	return a.connMgr.Remove(id, path)
}

func (a *App) SFTPRename(id, oldPath, newPath string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	return a.connMgr.Rename(id, oldPath, newPath)
}

func (a *App) SFTPReadFile(id, path string) (string, error) {
	if err := a.requireUnlocked(); err != nil {
		return "", err
	}
	return a.connMgr.ReadRemoteFile(id, path)
}

func (a *App) SFTPTransfer(id string, req connection.TransferRequest) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	return a.connMgr.Transfer(id, req)
}

func (a *App) SFTPCancelTransfer(jobID string) {
	a.connMgr.CancelTransfer(jobID)
}

func (a *App) GetRemoteHome(id string) (string, error) {
	return a.connMgr.GetRemoteHome(id)
}

// Local filesystem bindings

func (a *App) GetLocalHome() (string, error) {
	return a.connMgr.GetLocalHome()
}

func (a *App) ListLocalDir(path string) ([]connection.FileEntry, error) {
	if err := a.requireUnlocked(); err != nil {
		return nil, err
	}
	return connection.ListLocalDir(path)
}

func (a *App) LocalParentDir(path string) string {
	return connection.LocalParentDir(path)
}

func (a *App) ReadLocalFile(path string) (string, error) {
	if err := a.requireUnlocked(); err != nil {
		return "", err
	}
	return connection.ReadLocalFile(path)
}

func (a *App) LocalMkdir(path string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	return connection.LocalMkdir(path)
}

func (a *App) LocalRemove(path string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if path == "" {
		return errors.New("empty path")
	}
	return connection.LocalRemove(path)
}

func (a *App) LocalRename(oldPath, newPath string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	return connection.LocalRename(oldPath, newPath)
}

// Profile store bindings

func (a *App) ListSSHProfiles() ([]store.SSHProfile, error) {
	if a.data == nil {
		return []store.SSHProfile{}, nil
	}
	return a.data.ListSSHProfiles()
}

func (a *App) SaveSSHProfile(profile store.SSHProfile) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return nil
	}
	return a.data.SaveSSHProfile(profile)
}

func (a *App) DeleteSSHProfile(id string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return nil
	}
	return a.data.DeleteSSHProfile(id)
}

func (a *App) ListRDPProfiles() ([]rdp.RDPProfile, error) {
	if a.data == nil {
		return []rdp.RDPProfile{}, nil
	}
	return a.data.ListRDPProfiles()
}

func (a *App) SaveRDPProfile(profile rdp.RDPProfile) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return nil
	}
	return a.data.SaveRDPProfile(profile)
}

func (a *App) DeleteRDPProfile(id string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return nil
	}
	return a.data.DeleteRDPProfile(id)
}

func (a *App) LaunchRDP(profileID string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return nil
	}
	profile, err := a.data.GetRDPProfile(profileID)
	if err != nil {
		return err
	}
	return a.rdp.Launch(profile, profile.Password)
}

// SelectFolder opens a native folder picker (used to pick folders to share
// over RDP). It returns "" if the user cancels.
func (a *App) SelectFolder() (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Select a folder to share"})
}

// SelectKeyFile opens a native file picker for a private key.
func (a *App) SelectKeyFile() (string, error) {
	if err := a.requireUnlocked(); err != nil {
		return "", err
	}
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".ssh")
	}
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Select private key",
		DefaultDirectory: dir,
	})
}

// Folder bindings

// settingsKey holds the frontend's settings JSON (theme, font, highlighting…).
const settingsKey = "app"

// GetSettings returns the saved settings JSON, or "" if none were saved.
func (a *App) GetSettings() (string, error) {
	if a.data == nil {
		return "", nil
	}
	return a.data.GetSetting(settingsKey)
}

// SaveSettings stores the settings JSON written by the frontend and applies
// its security part (masking, algorithms, agent forwarding, clipboard…).
func (a *App) SaveSettings(value string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	a.applySecurity(security.FromSettingsJSON(value))
	if a.data == nil {
		return nil
	}
	return a.data.SetSetting(settingsKey, value)
}

// SetCaptureProtection hides the window from screen capture and screen
// sharing (true) or allows it again (false). The frontend calls it on startup
// and whenever the "Hide from screen sharing" setting changes.
func (a *App) SetCaptureProtection(enabled bool) error {
	return capture.SetProtected(enabled)
}

// GetScreenSharing reports whether another app is capturing the screen now.
func (a *App) GetScreenSharing() capture.SharingState {
	return capture.DetectSharing()
}

func (a *App) ListFolders() ([]store.Folder, error) {
	if a.data == nil {
		return []store.Folder{}, nil
	}
	return a.data.ListFolders()
}

func (a *App) SaveFolder(f store.Folder) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return errors.New("storage unavailable")
	}
	return a.data.SaveFolder(f)
}

func (a *App) DeleteFolder(id string) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return errors.New("storage unavailable")
	}
	return a.data.DeleteFolder(id)
}

// MoveItem moves a folder, SSH or RDP host (kind "folder" | "ssh" | "rdp")
// into parentID at index among its siblings; index -1 appends.
func (a *App) MoveItem(kind, id, parentID string, index int) error {
	if err := a.requireUnlocked(); err != nil {
		return err
	}
	if a.data == nil {
		return errors.New("storage unavailable")
	}
	return a.data.MoveItem(kind, id, parentID, index)
}

// Import bindings

// ListImportSources lists the clients hosts can be imported from on this
// OS, those found on this computer first.
func (a *App) ListImportSources() []importers.SourceInfo {
	return importers.List()
}

// ReadImport reads hosts from a source; empty paths means its detected data.
func (a *App) ReadImport(sourceID string, paths []string) (importers.Result, error) {
	if err := a.requireUnlocked(); err != nil {
		return importers.Result{}, err
	}
	return importers.Read(sourceID, paths)
}

// PickImportPaths shows the file/folder dialog a source needs. It returns
// nothing if the user cancels.
func (a *App) PickImportPaths(sourceID string) ([]string, error) {
	var info *importers.SourceInfo
	for _, s := range importers.List() {
		if s.ID == sourceID {
			info = &s
			break
		}
	}
	if info == nil {
		return nil, errors.New("unknown import source")
	}
	title := "Import from " + info.Name
	filters := []runtime.FileFilter{{DisplayName: info.FilterName, Pattern: info.Filter}, {DisplayName: "All files", Pattern: "*.*"}}
	switch info.Pick {
	case "folder":
		p, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title})
		if err != nil || p == "" {
			return []string{}, err
		}
		return []string{p}, nil
	case "files":
		return runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{Title: title, Filters: filters})
	default:
		p, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title, Filters: filters})
		if err != nil || p == "" {
			return []string{}, err
		}
		return []string{p}, nil
	}
}
