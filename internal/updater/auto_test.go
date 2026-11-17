package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsRound(t *testing.T) {
	cases := []struct {
		v    string
		want bool
	}{
		{"0.3.0", true},
		{"v0.3.0", true},
		{"1.0.0", true},
		{"v1.2.0", true},
		{"0.3.1", false},
		{"v0.3.1", false},
		{"0.3.0-rc.1", false},
		{"v0.4.0-beta.2", false},
		{"1.2.3", false},
		{"junk", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsRound(c.v); got != c.want {
			t.Errorf("IsRound(%q) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestShouldInstall(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.4.0", "0.3.0", true},
		{"v0.4.0", "0.3.0", true},
		{"0.3.0", "0.3.0", false},
		{"0.3.0", "0.4.0", false},
		{"0.3.1", "0.3.0", false},
		{"0.4.0-rc.1", "0.3.0", false},
		{"0.2.0", "0.3.0", false},
		{"1.0.0", "0.9.9", true},
	}
	for _, c := range cases {
		if got := ShouldInstall(c.latest, c.current); got != c.want {
			t.Errorf("ShouldInstall(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestAssetSuffix(t *testing.T) {
	cases := []struct{ goos, goarch, want string }{
		{"darwin", "amd64", "-macos-x64.dmg"},
		{"darwin", "arm64", "-macos-arm64.dmg"},
		{"windows", "amd64", "-windows-x64-setup.exe"},
		{"windows", "arm64", "-windows-arm64-setup.exe"},
		{"linux", "amd64", ""},
		{"darwin", "386", ""},
	}
	for _, c := range cases {
		if got := AssetSuffix(c.goos, c.goarch); got != c.want {
			t.Errorf("AssetSuffix(%s, %s) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestParseChecksumsAndVerify(t *testing.T) {
	dir := t.TempDir()
	body := []byte("hello terminyxx\n")
	path := filepath.Join(dir, "Terminyxx-0.4.0-macos-x64.dmg")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	hexSum := hex.EncodeToString(sum[:])
	raw := []byte(hexSum + "  Terminyxx-0.4.0-macos-x64.dmg\n" +
		strings.Repeat("ab", 32) + "  other.bin\n")
	sums, err := ParseChecksums(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ChecksumFor(sums, "Terminyxx-0.4.0-macos-x64.dmg")
	if err != nil || got != hexSum {
		t.Fatalf("ChecksumFor: %q %v", got, err)
	}
	if err := VerifySHA256(path, hexSum); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(path, strings.Repeat("00", 32)); err == nil {
		t.Fatal("expected mismatch")
	}
	if _, err := ChecksumFor(sums, "missing.dmg"); err == nil {
		t.Fatal("expected missing checksum")
	}
}

func TestParseChecksumsStarAndErrors(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	sums, err := ParseChecksums([]byte(sum + " *Terminyxx-0.4.0-windows-x64-setup.exe\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ChecksumFor(sums, "Terminyxx-0.4.0-windows-x64-setup.exe"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseChecksums([]byte("not-a-sum  file\n")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ParseChecksums([]byte("")); err == nil {
		t.Fatal("expected empty error")
	}
	if _, err := ParseChecksums(nil); err == nil {
		t.Fatal("expected empty error")
	}
}

func TestAllowedURL(t *testing.T) {
	ok := []string{
		"https://github.com/Xstrwe/terminyxx/releases/download/v0.4.0/app.dmg",
		"https://objects.githubusercontent.com/github-production-release-asset-2e65be/foo",
		"https://release-assets.githubusercontent.com/github-production-release-asset/foo",
		"https://github-releases.githubusercontent.com/foo",
	}
	for _, u := range ok {
		if !AllowedURL(u) {
			t.Errorf("AllowedURL(%q) = false, want true", u)
		}
	}
	bad := []string{
		"http://github.com/Xstrwe/terminyxx/releases/download/v0.4.0/app.dmg",
		"https://evil.example/app.dmg",
		"https://github.com.evil.example/app.dmg",
		"https://user@github.com/foo",
		"ftp://github.com/foo",
		"",
		"not a url",
	}
	for _, u := range bad {
		if AllowedURL(u) {
			t.Errorf("AllowedURL(%q) = true, want false", u)
		}
	}
}

func TestCheckSkipsDraftAndPrerelease(t *testing.T) {
	body := `{"tag_name":"v0.4.0","prerelease":true,"html_url":"https://github.com/Xstrwe/terminyxx/releases/tag/v0.4.0","assets":[]}`
	r, err := Check(t.Context(), nil, serve(t, 200, body), "0.3.0", "darwin", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if r.Available {
		t.Fatalf("prerelease should not be available: %+v", r)
	}
	body = `{"tag_name":"v0.4.0","draft":true,"html_url":"https://github.com/Xstrwe/terminyxx/releases/tag/v0.4.0","assets":[]}`
	r, err = Check(t.Context(), nil, serve(t, 200, body), "0.3.0", "darwin", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if r.Available {
		t.Fatalf("draft should not be available: %+v", r)
	}
}

func TestShouldInstallRejectsNonRoundLatest(t *testing.T) {
	// /releases/latest can be a patch release; auto-install must ignore it.
	if ShouldInstall("0.3.1", "0.3.0") {
		t.Fatal("patch releases must not auto-install")
	}
}

func TestBundleIDFromXML(t *testing.T) {
	dir := t.TempDir()
	plist := filepath.Join(dir, "Info.plist")
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleName</key><string>Terminyxx</string>
  <key>CFBundleIdentifier</key><string>com.wails.terminyxx</string>
</dict></plist>`
	if err := os.WriteFile(plist, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := bundleIDFromXML(plist)
	if err != nil || id != "com.wails.terminyxx" {
		t.Fatalf("got %q %v", id, err)
	}
}
