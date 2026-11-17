package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	lastCheckFile = "update-last-check"
	pendingFile   = "update-pending.json"
	backupFile    = "update-backup-path"
	checkInterval = 24 * time.Hour
)

// LastCheck returns when the last successful GitHub query ran, or zero.
func LastCheck(dataDir string) time.Time {
	b, err := os.ReadFile(filepath.Join(dataDir, lastCheckFile))
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(b)))
	if err != nil {
		return time.Time{}
	}
	return t
}

// Due reports whether a check should run now (never checked, or older than 24h).
func Due(dataDir string, now time.Time) bool {
	last := LastCheck(dataDir)
	if last.IsZero() {
		return true
	}
	return now.Sub(last) >= checkInterval
}

// SaveLastCheck records a successful (or rate-limited) check time.
func SaveLastCheck(dataDir string, now time.Time) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, lastCheckFile), []byte(now.UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// Pending is a staged update waiting to be swapped in on quit or next launch.
type Pending struct {
	Version string `json:"version"`
	Staged  string `json:"staged"`
	Current string `json:"current"`
	Backup  string `json:"backup"`
	// Kind is "app" (macOS bundle swap) or "installer" (Windows setup exe).
	Kind string `json:"kind"`
}

func pendingPath(dataDir string) string {
	return filepath.Join(dataDir, pendingFile)
}

// LoadPending returns a staged update, or nil if there isn't one.
func LoadPending(dataDir string) (*Pending, error) {
	b, err := os.ReadFile(pendingPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if p.Staged == "" || p.Version == "" {
		return nil, nil
	}
	return &p, nil
}

// SavePending records a staged update.
func SavePending(dataDir string, p Pending) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(pendingPath(dataDir), append(b, '\n'), 0o600)
}

// ClearPending removes the staged-update record (not the files themselves).
func ClearPending(dataDir string) error {
	err := os.Remove(pendingPath(dataDir))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func saveBackupPath(dataDir, backup string) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dataDir, backupFile), []byte(backup+"\n"), 0o600)
}

// CleanupBackup deletes the previous app bundle after a successful launch.
func CleanupBackup(dataDir string) {
	path := filepath.Join(dataDir, backupFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	backup := strings.TrimSpace(string(b))
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	_ = os.Remove(path)
}
