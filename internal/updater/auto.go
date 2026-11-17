package updater

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Outcome is the result of one auto-update attempt.
type Outcome struct {
	// Status is "ok", "ready", "manual", or "skip".
	Status  string
	Version string
	URL     string
	Err     error
}

// AutoCheck queries GitHub for the latest release and, when it is a newer
// round version (vX.Y.0), downloads it, verifies the SHA-256, and stages
// it for the next quit or launch. Errors from the network, rate limits and
// "nothing to do" are quiet: Status is "skip".
func AutoCheck(ctx context.Context, client *http.Client, apiURL, dataDir, current, goos, goarch string) Outcome {
	res, err := Check(ctx, client, apiURL, current, goos, goarch)
	if err != nil {
		return Outcome{Status: "skip", Err: err}
	}
	if !ShouldInstall(res.Latest, res.Current) {
		return Outcome{Status: "ok", Version: res.Latest}
	}
	if res.DownloadURL == "" || res.DownloadName == "" {
		return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: fmt.Errorf("no asset for this platform")}
	}
	if res.ChecksumsURL == "" {
		return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: fmt.Errorf("checksums file is missing")}
	}

	dir := UpdatesDir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Outcome{Status: "skip", Err: err}
	}
	sumsPath := filepath.Join(dir, "SHA256SUMS.txt")
	dlClient := client
	if dlClient == nil {
		dlClient = downloadClient(5 * time.Minute)
	}
	if err := DownloadFile(dlClient, res.ChecksumsURL, sumsPath, maxSumsBytes); err != nil {
		return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: err}
	}
	raw, err := os.ReadFile(sumsPath)
	if err != nil {
		return Outcome{Status: "skip", Err: err}
	}
	sums, err := ParseChecksums(raw)
	if err != nil {
		return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: err}
	}
	want, err := ChecksumFor(sums, res.DownloadName)
	if err != nil {
		return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: err}
	}

	assetPath := filepath.Join(dir, filepath.Base(res.DownloadName))
	if err := DownloadFile(dlClient, res.DownloadURL, assetPath, maxAssetBytes); err != nil {
		return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: err}
	}
	if err := VerifySHA256(assetPath, want); err != nil {
		_ = os.Remove(assetPath)
		return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: err}
	}

	if err := Stage(dataDir, assetPath, res.Latest); err != nil {
		_ = os.Remove(assetPath)
		if errors.Is(err, ErrNotWritable) {
			return Outcome{Status: "manual", Version: res.Latest, URL: firstLink(res), Err: err}
		}
		if errors.Is(err, ErrNotPackaged) {
			return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: err}
		}
		return Outcome{Status: "skip", Version: res.Latest, URL: res.URL, Err: err}
	}
	return Outcome{Status: "ready", Version: res.Latest, URL: res.URL}
}

func firstLink(res Result) string {
	if res.DownloadURL != "" {
		return res.DownloadURL
	}
	return res.URL
}

// ShouldInstall reports whether latest is a strictly newer, non-prerelease
// round version (patch == 0) than current.
func ShouldInstall(latest, current string) bool {
	return IsRound(latest) && Newer(latest, current)
}

// IsRound reports whether v is a final (non-prerelease) X.Y.0 version.
func IsRound(v string) bool {
	nums, pre, ok := parse(v)
	return ok && pre == "" && nums[2] == 0
}

// NormalizeVersion trims a leading "v".
func NormalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
