package connection

import (
	"os"
	"testing"
)

func TestKeyboardInteractiveOnlyAnswersPasswordPrompts(t *testing.T) {
	ki := keyboardInteractivePassword("pw")
	if a, err := ki("", "", []string{"Password: "}, []bool{false}); err != nil || a[0] != "pw" {
		t.Fatalf("password prompt: %v %v", a, err)
	}
	if a, err := ki("", "", nil, nil); err != nil || len(a) != 0 {
		t.Fatalf("empty challenge: %v %v", a, err)
	}
	for _, c := range []struct {
		q    string
		echo bool
	}{
		{"Verification code: ", false},
		{"Password: ", true}, // echoed: not a secret prompt
		{"Type yes to continue", true},
	} {
		if _, err := ki("", "", []string{c.q}, []bool{c.echo}); err == nil {
			t.Errorf("answered %q (echo %v) with the password", c.q, c.echo)
		}
	}
	if _, err := keyboardInteractivePassword("")("", "", []string{"Password: "}, []bool{false}); err == nil {
		t.Error("answered with an empty password")
	}
}

func TestClampTermSize(t *testing.T) {
	for _, c := range [][4]int{
		{80, 24, 80, 24},
		{0, 0, 0, 0},
		{-5, -1, 0, 0},
		{1 << 20, 1 << 20, maxTermCols, maxTermRows},
	} {
		if cols, rows := clampTermSize(c[0], c[1]); cols != c[2] || rows != c[3] {
			t.Errorf("clampTermSize(%d, %d) = %d, %d", c[0], c[1], cols, rows)
		}
	}
}

func TestUnsafeWindowsName(t *testing.T) {
	for name, want := range map[string]bool{
		"report.txt": false, "con.d": true, "CON": true, "nul.txt": true, "COM1": true, "lpt9.log": true,
		"file.txt:hidden": true, "trailing.": true, "trailing ": true, "console": false, "COM10": false, "..": false,
	} {
		if got := unsafeWindowsName(name); got != want {
			t.Errorf("unsafeWindowsName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDownloadMode(t *testing.T) {
	for _, c := range []struct {
		src  string
		mode os.FileMode
		want os.FileMode
	}{
		{"/home/u/.ssh/config", 0o644, 0o600},
		{"/srv/id_ed25519", 0o644, 0o600},
		{"/srv/id_ed25519.pub", 0o644, 0o644},
		{"/srv/tls/server.key", 0o644, 0o600},
		{"/srv/run.sh", 0o755, 0o755},
		{"/srv/private.txt", 0o400, 0o600},
		{"/srv/plain", 0, 0o644},
		{"/srv/suid", os.ModeSetuid | 0o4755, 0o755},
	} {
		if got := downloadMode(transferItem{src: c.src, mode: c.mode}); got != c.want {
			t.Errorf("downloadMode(%s, %v) = %04o, want %04o", c.src, c.mode, got, c.want)
		}
	}
}
