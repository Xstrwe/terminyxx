// Package updater asks GitHub whether a newer Terminyxx release exists
// and, for round versions (vX.Y.0), can download, verify and stage it.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrRateLimit is returned when GitHub answers 403 or 429.
var ErrRateLimit = errors.New("GitHub rate limit reached, try again later")

const (
	// Repo is the GitHub repository releases are published to.
	Repo = "Xstrwe/terminyxx"
	// LatestURL returns the newest published, non-draft, non-prerelease release.
	LatestURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

	releasePrefix = "https://github.com/" + Repo + "/"
)

// Result is what the frontend gets (mirrored in wailsjs/go/models.ts).
type Result struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	// URL is the release page; always under github.com/<Repo>/.
	URL   string `json:"url"`
	Notes string `json:"notes"`
	// DownloadURL/DownloadName point at the installer for this OS and CPU, if the release has one.
	DownloadURL  string `json:"downloadUrl"`
	DownloadName string `json:"downloadName"`
	// ChecksumsURL/ChecksumsName point at SHA256SUMS.txt on the same release.
	ChecksumsURL  string `json:"checksumsUrl"`
	ChecksumsName string `json:"checksumsName"`
}

type release struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check queries apiURL (LatestURL in production) and compares with current.
func Check(ctx context.Context, client *http.Client, apiURL, current, goos, goarch string) (Result, error) {
	res := Result{Current: strings.TrimPrefix(current, "v")}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Terminyxx-update-check")
	resp, err := client.Do(req)
	if err != nil {
		return res, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return res, errors.New("no releases have been published yet")
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return res, ErrRateLimit
	case resp.StatusCode != http.StatusOK:
		return res, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return res, fmt.Errorf("unexpected GitHub response: %w", err)
	}
	if _, _, ok := parse(rel.TagName); !ok {
		return res, fmt.Errorf("unrecognised release tag %q", rel.TagName)
	}
	res.Latest = strings.TrimPrefix(rel.TagName, "v")
	if strings.HasPrefix(rel.HTMLURL, releasePrefix) {
		res.URL = rel.HTMLURL
	} else {
		res.URL = releasePrefix + "releases/latest"
	}
	res.Notes = rel.Body
	if rel.Draft || rel.Prerelease {
		return res, nil
	}
	res.Available = Newer(res.Latest, res.Current)
	if want := AssetSuffix(goos, goarch); want != "" {
		for _, a := range rel.Assets {
			if strings.HasSuffix(a.Name, want) && strings.HasPrefix(a.URL, releasePrefix) && AllowedURL(a.URL) {
				res.DownloadName, res.DownloadURL = a.Name, a.URL
				break
			}
		}
	}
	for _, a := range rel.Assets {
		if a.Name == "SHA256SUMS.txt" && strings.HasPrefix(a.URL, releasePrefix) && AllowedURL(a.URL) {
			res.ChecksumsName, res.ChecksumsURL = a.Name, a.URL
			break
		}
	}
	return res, nil
}

// AssetSuffix matches the file names built by .github/workflows/release.yml.
func AssetSuffix(goos, goarch string) string {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" {
		return ""
	}
	switch goos {
	case "windows":
		return "-windows-" + arch + "-setup.exe"
	case "darwin":
		return "-macos-" + arch + ".dmg"
	}
	return ""
}

// Newer reports whether version a is newer than b. A release beats a
// prerelease of the same number; an unparsable b counts as older.
func Newer(a, b string) bool {
	an, apre, aok := parse(a)
	bn, bpre, bok := parse(b)
	if !aok {
		return false
	}
	if !bok {
		return true
	}
	for i := range an {
		if an[i] != bn[i] {
			return an[i] > bn[i]
		}
	}
	if apre == bpre {
		return false
	}
	if apre == "" {
		return true
	}
	if bpre == "" {
		return false
	}
	return apre > bpre
}

func parse(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return nums, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, "", false
		}
		nums[i] = n
	}
	return nums, pre, true
}
