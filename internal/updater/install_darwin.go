//go:build darwin

package updater

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const expectedBundleID = "com.wails.terminyxx"

// Stage prepares a downloaded macOS DMG for install: mount it, check the
// bundle ID, copy the .app next to the running bundle, strip quarantine,
// and record a pending swap.
func Stage(dataDir, assetPath, version string) error {
	exe, err := execPath()
	if err != nil {
		return err
	}
	if isEphemeralExe(exe) {
		return ErrNotPackaged
	}
	current, err := appBundle(exe)
	if err != nil {
		return ErrNotPackaged
	}
	parent := filepath.Dir(current)
	if !dirWritable(parent) {
		return ErrNotWritable
	}

	mount, detach, err := attachDMG(assetPath)
	if err != nil {
		return err
	}
	defer detach()

	src, err := findApp(mount)
	if err != nil {
		return err
	}
	want, err := bundleID(current)
	if err != nil {
		want = expectedBundleID
	}
	got, err := bundleID(src)
	if err != nil {
		return fmt.Errorf("new app has no bundle id: %w", err)
	}
	if got != want {
		return fmt.Errorf("bundle id %q does not match %q", got, want)
	}

	staged := current + ".next"
	_ = os.RemoveAll(staged)
	if err := ditto(src, staged); err != nil {
		_ = os.RemoveAll(staged)
		return err
	}
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", staged).Run()

	return SavePending(dataDir, Pending{
		Version: version,
		Staged:  staged,
		Current: current,
		Backup:  current + ".bak",
		Kind:    "app",
	})
}

// ApplyStaged swaps a staged .app into place. If reexec is set, the new
// binary replaces this process and the function does not return on success.
func ApplyStaged(dataDir string, reexec bool) error {
	p, err := LoadPending(dataDir)
	if err != nil || p == nil || p.Kind != "app" {
		return err
	}
	if _, err := os.Stat(p.Staged); err != nil {
		_ = ClearPending(dataDir)
		return fmt.Errorf("staged app missing: %w", err)
	}
	if err := swapApp(p); err != nil {
		return err
	}
	_ = saveBackupPath(dataDir, p.Backup)
	_ = ClearPending(dataDir)
	if !reexec {
		return nil
	}
	newExe := filepath.Join(p.Current, "Contents", "MacOS", "terminyxx")
	if exe, err := execPath(); err == nil {
		newExe = filepath.Join(p.Current, "Contents", "MacOS", filepath.Base(exe))
	}
	_ = syscall.Exec(newExe, os.Args, os.Environ())
	// Exec failed: the swap already happened, so the next launch is new.
	return nil
}

func swapApp(p *Pending) error {
	if p.Backup != "" {
		_ = os.RemoveAll(p.Backup)
	}
	if err := os.Rename(p.Current, p.Backup); err != nil {
		return err
	}
	if err := os.Rename(p.Staged, p.Current); err != nil {
		_ = os.Rename(p.Backup, p.Current)
		return err
	}
	return nil
}

func appBundle(exe string) (string, error) {
	// Terminyxx.app/Contents/MacOS/terminyxx
	macos := filepath.Dir(exe)
	if filepath.Base(macos) != "MacOS" {
		return "", ErrNotPackaged
	}
	contents := filepath.Dir(macos)
	if filepath.Base(contents) != "Contents" {
		return "", ErrNotPackaged
	}
	app := filepath.Dir(contents)
	if !strings.HasSuffix(app, ".app") {
		return "", ErrNotPackaged
	}
	return app, nil
}

func findApp(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".app") {
			return filepath.Join(root, e.Name()), nil
		}
	}
	var found string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if d.IsDir() && strings.HasSuffix(d.Name(), ".app") {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("no .app in the disk image")
	}
	return found, nil
}

func ditto(src, dst string) error {
	cmd := exec.Command("ditto", src, dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ditto: %w (%s)", err, bytes.TrimSpace(out))
	}
	return nil
}

func attachDMG(dmg string) (mount string, detach func(), err error) {
	cmd := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-plist", dmg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("hdiutil attach: %w (%s)", err, bytes.TrimSpace(out))
	}
	mount, err = mountPointFromPlist(out)
	if err != nil {
		_ = exec.Command("hdiutil", "detach", "-quiet", "-force", dmg).Run()
		return "", nil, err
	}
	done := false
	detach = func() {
		if done {
			return
		}
		done = true
		_ = exec.Command("hdiutil", "detach", "-quiet", "-force", mount).Run()
	}
	return mount, detach, nil
}

func bundleID(app string) (string, error) {
	plist := filepath.Join(app, "Contents", "Info.plist")
	if id, err := bundleIDFromXML(plist); err == nil && id != "" {
		return id, nil
	}
	out, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :CFBundleIdentifier", plist).Output()
	if err != nil {
		out, err = exec.Command("plutil", "-extract", "CFBundleIdentifier", "raw", plist).Output()
		if err != nil {
			return "", err
		}
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", fmt.Errorf("empty bundle id")
	}
	return id, nil
}
