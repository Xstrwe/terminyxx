package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.2", "0.1.1", true},
		{"v0.1.2", "0.1.2", false},
		{"0.1.1", "0.1.2", false},
		{"0.2.0", "0.1.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.2", "0.1.2-beta.1", true},
		{"0.1.2-beta.1", "0.1.2", false},
		{"0.1.2-beta.2", "0.1.2-beta.1", true},
		{"junk", "0.1.0", false},
		{"0.1.0", "dev", true},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func serve(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const sample = `{"tag_name":"v0.1.2","html_url":"https://github.com/Xstrwe/terminyxx/releases/tag/v0.1.2","body":"notes",
"assets":[
{"name":"Terminyxx-0.1.2-windows-arm64-setup.exe","browser_download_url":"https://github.com/Xstrwe/terminyxx/releases/download/v0.1.2/Terminyxx-0.1.2-windows-arm64-setup.exe"},
{"name":"Terminyxx-0.1.2-windows-x64-setup.exe","browser_download_url":"https://github.com/Xstrwe/terminyxx/releases/download/v0.1.2/Terminyxx-0.1.2-windows-x64-setup.exe"},
{"name":"Terminyxx-0.1.2-macos-arm64.dmg","browser_download_url":"https://github.com/Xstrwe/terminyxx/releases/download/v0.1.2/Terminyxx-0.1.2-macos-arm64.dmg"},
{"name":"SHA256SUMS.txt","browser_download_url":"https://github.com/Xstrwe/terminyxx/releases/download/v0.1.2/SHA256SUMS.txt"}]}`

func TestCheckAvailable(t *testing.T) {
	r, err := Check(context.Background(), nil, serve(t, 200, sample), "0.1.1", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Available || r.Latest != "0.1.2" || r.Current != "0.1.1" || r.Notes != "notes" {
		t.Fatalf("unexpected result %+v", r)
	}
	if r.DownloadName != "Terminyxx-0.1.2-windows-x64-setup.exe" {
		t.Fatalf("wrong asset %q", r.DownloadName)
	}
	r, _ = Check(context.Background(), nil, serve(t, 200, sample), "0.1.1", "darwin", "arm64")
	if r.DownloadName != "Terminyxx-0.1.2-macos-arm64.dmg" {
		t.Fatalf("wrong mac asset %q", r.DownloadName)
	}
	if r.ChecksumsName != "SHA256SUMS.txt" || r.ChecksumsURL == "" {
		t.Fatalf("missing checksums: %+v", r)
	}
}

func TestCheckUpToDate(t *testing.T) {
	r, err := Check(context.Background(), nil, serve(t, 200, sample), "0.1.2", "windows", "amd64")
	if err != nil || r.Available {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestCheckRejectsForeignURLs(t *testing.T) {
	body := `{"tag_name":"v9.0.0","html_url":"https://evil.example/x","assets":[{"name":"a-windows-x64-setup.exe","browser_download_url":"https://evil.example/a.exe"}]}`
	r, err := Check(context.Background(), nil, serve(t, 200, body), "0.1.1", "windows", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != "https://github.com/Xstrwe/terminyxx/releases/latest" || r.DownloadURL != "" {
		t.Fatalf("foreign URL accepted: %+v", r)
	}
}

func TestCheckErrors(t *testing.T) {
	for _, status := range []int{404, 403, 500} {
		if _, err := Check(context.Background(), nil, serve(t, status, "{}"), "0.1.1", "windows", "amd64"); err == nil {
			t.Errorf("status %d: expected error", status)
		}
	}
	if _, err := Check(context.Background(), nil, serve(t, 200, "not json"), "0.1.1", "windows", "amd64"); err == nil {
		t.Error("expected error for bad JSON")
	}
}
