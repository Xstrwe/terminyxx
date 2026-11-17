//go:build windows

package updater

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Stage keeps a verified Windows installer to run when Terminyxx quits.
func Stage(dataDir, assetPath, version string) error {
	exe, err := execPath()
	if err != nil {
		return err
	}
	if isEphemeralExe(exe) {
		return ErrNotPackaged
	}
	dir := filepath.Dir(exe)
	if !dirWritable(dir) {
		return ErrNotWritable
	}
	staged := filepath.Join(UpdatesDir(dataDir), "Terminyxx-setup.exe")
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		return err
	}
	_ = os.Remove(staged)
	if err := copyFile(assetPath, staged, 0o700); err != nil {
		return err
	}
	return SavePending(dataDir, Pending{
		Version: version,
		Staged:  staged,
		Current: exe,
		Kind:    "installer",
	})
}

// ApplyStaged starts the silent installer when the app is quitting (reexec
// is ignored: we cannot swap a running Windows exe in place).
func ApplyStaged(dataDir string, reexec bool) error {
	p, err := LoadPending(dataDir)
	if err != nil || p == nil || p.Kind != "installer" {
		return err
	}
	if reexec {
		// Still running: leave the installer for shutdown.
		return nil
	}
	if _, err := os.Stat(p.Staged); err != nil {
		_ = ClearPending(dataDir)
		return fmt.Errorf("staged installer missing: %w", err)
	}
	cmd := exec.Command(p.Staged, "/VERYSILENT", "/NORESTART", "/SUPPRESSMSGBOXES")
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = ClearPending(dataDir)
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		_ = os.Remove(dst)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}
	return nil
}
