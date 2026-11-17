package rdp

import (
	"fmt"
	"os"
	"strings"
)

type Launcher interface {
	// Launch opens the profile in the OS's RDP client. Password may be empty.
	Launch(profile RDPProfile, password string) error
}

func NewLauncher() Launcher {
	return newPlatformLauncher()
}

// writeTempRDP writes content to a new, uniquely named .rdp file in the temp
// folder. os.CreateTemp uses O_EXCL and mode 0600, so another local user
// cannot pre-create or swap the file (a predictable name allowed that).
func writeTempRDP(content string) (string, error) {
	f, err := os.CreateTemp("", "terminyxx-*.rdp")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// buildRDPFile renders the profile as .rdp file content. Only keys that
// every client understands are written unless windowsOnly is set.
func buildRDPFile(p RDPProfile, windowsOnly bool) string {
	port := defaultInt(p.Port, 3389)
	o := p.Options

	screenMode := 1
	if p.Fullscreen {
		screenMode = 2
	}
	lines := []string{
		fmt.Sprintf("screen mode id:i:%d", screenMode),
		fmt.Sprintf("desktopwidth:i:%d", defaultInt(p.Width, 1920)),
		fmt.Sprintf("desktopheight:i:%d", defaultInt(p.Height, 1080)),
		"session bpp:i:32",
		fmt.Sprintf("full address:s:%s:%d", oneLine(p.Host), port),
		fmt.Sprintf("username:s:%s", oneLine(p.Username)),
		// Prompt about untrusted server certificates instead of silently accepting.
		"authentication level:i:2",
		"prompt for credentials:i:0",
		"negotiate security layer:i:1",
		fmt.Sprintf("redirectclipboard:i:%d", b2i(o.Clipboard)),
		fmt.Sprintf("redirectprinters:i:%d", b2i(o.Printers)),
		fmt.Sprintf("redirectsmartcards:i:%d", b2i(o.SmartCards)),
		fmt.Sprintf("redirectcomports:i:%d", b2i(o.ComPorts)),
		fmt.Sprintf("smart sizing:i:%d", b2i(o.SmartSizing)),
		fmt.Sprintf("use multimon:i:%d", b2i(o.MultiMon)),
		fmt.Sprintf("dynamic resolution:i:%d", b2i(o.DynamicRes)),
		fmt.Sprintf("administrative session:i:%d", b2i(o.Admin)),
	}

	audio := 2 // off
	switch o.Audio {
	case "local", "":
		audio = 0
	case "remote":
		audio = 1
	}
	lines = append(lines,
		fmt.Sprintf("audiomode:i:%d", audio),
		fmt.Sprintf("audiocapturemode:i:%d", b2i(o.Microphone)),
	)

	drives := cleanPaths(o.DrivePaths)
	switch {
	case o.AllDrives:
		lines = append(lines, "redirectdrives:i:1", "drivestoredirect:s:*")
	case o.Drives && len(drives) > 0:
		lines = append(lines, "redirectdrives:i:1", "drivestoredirect:s:"+strings.Join(drives, ";")+";")
	default:
		lines = append(lines, "redirectdrives:i:0")
	}

	if windowsOnly {
		if o.Camera {
			lines = append(lines, "camerastoredirect:s:*")
		}
		if o.Usb {
			lines = append(lines, "usbdevicestoredirect:s:*")
		}
		if o.Gateway != "" {
			lines = append(lines,
				"gatewayhostname:s:"+oneLine(o.Gateway),
				"gatewayusagemethod:i:1",
				"gatewayprofileusagemethod:i:1",
			)
		}
	}
	if p.Domain != "" {
		lines = append(lines, "domain:s:"+oneLine(p.Domain))
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// oneLine strips characters that would let a value inject extra .rdp lines.
func oneLine(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func cleanPaths(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		p = strings.TrimSpace(oneLine(strings.ReplaceAll(p, ";", "")))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
