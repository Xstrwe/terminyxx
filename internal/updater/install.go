package updater

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrNotWritable is returned when the installed app cannot be replaced
	// (for example /Applications owned by root). The caller should notify
	// the user with a download link instead of failing loudly.
	ErrNotWritable = errors.New("app location is not writable")
	// ErrNotPackaged is returned when Terminyxx is not running from a
	// packaged install (wails dev, go run, tests). Auto-update is skipped.
	ErrNotPackaged = errors.New("not running from a packaged app")
)

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".terminyxx-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func execPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func isEphemeralExe(exe string) bool {
	base := filepath.Base(exe)
	if strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".test.exe") {
		return true
	}
	return strings.Contains(exe, string(filepath.Separator)+"go-build")
}

// UpdatesDir is the scratch folder for downloaded assets.
func UpdatesDir(dataDir string) string {
	return filepath.Join(dataDir, "updates")
}
