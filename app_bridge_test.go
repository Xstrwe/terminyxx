package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckOpenURL(t *testing.T) {
	ok := []string{"https://example.com", "http://example.com/a?b=c", "HTTPS://Example.com/x"}
	for _, u := range ok {
		if _, err := checkOpenURL(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	bad := []string{
		"ms-msdt:/id PCWDiagnostic", "search-ms:query=x", "file:///etc/passwd", "javascript:alert(1)",
		"/Users/me/recordings", "https://", "https://user:pw@example.com", "smb://host/share", "",
	}
	for _, u := range bad {
		if _, err := checkOpenURL(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestReadFileLimited(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, err := readFileLimited(p, 100); err != nil || len(d) != 100 {
		t.Fatalf("at the limit: %d %v", len(d), err)
	}
	if _, err := readFileLimited(p, 99); err == nil {
		t.Fatal("oversized file accepted")
	}
}
