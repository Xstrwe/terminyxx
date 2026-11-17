package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	goruntime "runtime"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"terminyxx/internal/connection"
)

// requireUnlocked refuses a call from the frontend while the app is locked.
// The lock screen hides the UI, but the Go bindings stay reachable from the
// WebView, so sensitive methods check the lock here as well.
func (a *App) requireUnlocked() error {
	if a.sec.lock != nil && a.sec.lock.Locked() {
		return connection.ErrLocked
	}
	return nil
}

// errURLNotAllowed is returned for anything but an http(s) URL with a host.
var errURLNotAllowed = errors.New("only http and https links can be opened")

// checkOpenURL accepts only absolute http/https URLs. Other schemes
// (ms-msdt:, search-ms:, file:, custom app handlers…) can start programs.
func checkOpenURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errURLNotAllowed
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", errURLNotAllowed
	}
	if u.Host == "" || u.User != nil {
		return "", errURLNotAllowed
	}
	return u.String(), nil
}

// OpenURL opens an http(s) link in the default browser. The frontend uses it
// instead of the raw Wails BrowserOpenURL so the scheme is checked in Go.
func (a *App) OpenURL(raw string) error {
	u, err := checkOpenURL(raw)
	if err != nil {
		return err
	}
	runtime.BrowserOpenURL(a.ctx, u)
	return nil
}

// openFolder shows a local folder in the OS file manager.
func openFolder(dir string) error {
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "--", dir)
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// explorer.exe exits with 1 even on success; just reap the process.
	go func() { _ = cmd.Wait() }()
	return nil
}

// maxSyncFileSize bounds the sync file ImportSync reads into memory; real
// files (hosts, folders, snippets) are a few hundred KB at most.
const maxSyncFileSize = 64 << 20

// readFileLimited reads a whole file, refusing files larger than limit.
func readFileLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("the file is larger than %d MB", limit>>20)
	}
	return data, nil
}
