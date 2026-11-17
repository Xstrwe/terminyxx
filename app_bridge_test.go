package main

import "testing"

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
