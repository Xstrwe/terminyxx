//go:build darwin

package rdp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// microsoftRDCBundleID is shared by Microsoft Remote Desktop and its successor,
// Windows App.
const microsoftRDCBundleID = "com.microsoft.rdc.macos"

type platformLauncher struct{}

func newPlatformLauncher() Launcher {
	return &platformLauncher{}
}

// Launch opens the profile in Windows App (Microsoft Remote Desktop), or in
// whatever app handles .rdp files when that one isn't installed. The client
// has no way to receive a password through the file, so it is ignored here.
func (l *platformLauncher) Launch(profile RDPProfile, _ string) error {
	filePath, err := writeTempRDP(buildRDPFile(profile, false))
	if err != nil {
		return fmt.Errorf("write rdp file: %w", err)
	}

	// `open` returns once Launch Services has handed the file over (or failed
	// to), so run it to completion: a missing client must surface as an error
	// instead of silently doing nothing.
	err = runOpen("-b", microsoftRDCBundleID, filePath)
	if err != nil {
		err = runOpen(filePath)
	}
	if err != nil {
		_ = os.Remove(filePath)
		return fmt.Errorf("no RDP client found; install Windows App from the Mac App Store (%v)", err)
	}
	// The client reads the file asynchronously (a cold start can take a
	// while); remove it afterwards.
	time.AfterFunc(2*time.Minute, func() { _ = os.Remove(filePath) })
	return nil
}

func runOpen(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "open", args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return errors.New(msg)
	}
	return err
}
