package updater

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// maxAssetBytes is an upper bound on a release asset (DMG or installer).
	maxAssetBytes = 256 << 20
	maxSumsBytes  = 1 << 20
)

// allowedDownloadHosts is the set of hosts we will GET over HTTPS.
// objects.githubusercontent.com is what the brief names; the other
// githubusercontent hosts are the CDNs GitHub currently redirects to.
var allowedDownloadHosts = map[string]bool{
	"github.com":                            true,
	"objects.githubusercontent.com":         true,
	"release-assets.githubusercontent.com":  true,
	"github-releases.githubusercontent.com": true,
}

// AllowedURL reports whether raw is an https URL on an allowed GitHub host.
func AllowedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	return allowedDownloadHosts[strings.ToLower(u.Hostname())]
}

func downloadClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL == nil || !AllowedURL(req.URL.String()) {
				return fmt.Errorf("refusing redirect to %s", req.URL)
			}
			return nil
		},
	}
}

// DownloadFile GETs rawURL to dest, refusing non-HTTPS or off-GitHub hosts
// and anything larger than limit bytes. dest is created with 0600.
func DownloadFile(client *http.Client, rawURL, dest string, limit int64) error {
	if !AllowedURL(rawURL) {
		return fmt.Errorf("refusing download from %s", rawURL)
	}
	if client == nil {
		client = downloadClient(5 * time.Minute)
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "Terminyxx-updater")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}
	if resp.ContentLength > limit {
		return fmt.Errorf("download is too large (%d bytes)", resp.ContentLength)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp := dest + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	written, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if written > limit {
		_ = os.Remove(tmp)
		return fmt.Errorf("download is too large")
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
