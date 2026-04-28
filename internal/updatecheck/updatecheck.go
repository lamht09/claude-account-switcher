package updatecheck

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lamht09/claude-account-switcher/internal/platform"
)

const (
	cacheTTL         = 24 * time.Hour
)

var (
	latestReleaseURL      = "https://api.github.com/repos/lamht09/claude-account-switcher/releases/latest"
	latestReleasePageURL  = "https://github.com/lamht09/claude-account-switcher/releases/latest"
	updateCheckHTTPClient = &http.Client{Timeout: 2 * time.Second}
)

type latestRelease struct {
	TagName string `json:"tag_name"`
}

type cacheBody struct {
	Timestamp float64 `json:"timestamp"`
	Data      string  `json:"data"`
}

func Check(currentVersion string) string {
	latest, cached := readCache()
	if !cached {
		latest = fetchLatest()
		// Source parity: cache both success and failure states after a fresh fetch.
		writeCache(latest)
	}
	if latest == "" {
		return ""
	}
	if isGreaterVersion(normalizeVersion(latest), normalizeVersion(currentVersion)) {
		return "A newer version of claude-account-switcher is available (" + latest + "). You are using " + currentVersion + ". Consider upgrading!"
	}
	return ""
}

func cachePath() string {
	return filepath.Join(platform.BackupRoot(), "cache", "update-check.json")
}

func readCache() (string, bool) {
	raw, err := os.ReadFile(cachePath())
	if err != nil {
		return "", false
	}
	var data cacheBody
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", false
	}
	if time.Since(time.Unix(int64(data.Timestamp), 0)) > cacheTTL {
		return "", false
	}
	return data.Data, true
}

func writeCache(tag string) {
	path := cachePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	body, _ := json.Marshal(cacheBody{
		Timestamp: float64(time.Now().Unix()),
		Data:      tag,
	})
	_ = os.WriteFile(path, body, 0o600)
}

func fetchLatest() string {
	req, err := http.NewRequest(http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "claude-account-switcher/1.0")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := updateCheckHTTPClient.Do(req)
	if err != nil {
		return fetchLatestFromReleasePage()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fetchLatestFromReleasePage()
	}
	var latest latestRelease
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		return fetchLatestFromReleasePage()
	}
	return latest.TagName
}

func fetchLatestFromReleasePage() string {
	req, err := http.NewRequest(http.MethodGet, latestReleasePageURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "claude-account-switcher/1.0")
	resp, err := updateCheckHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ""
	}
	finalURL := resp.Request.URL
	if finalURL == nil {
		return ""
	}
	tag := tagFromReleaseURL(finalURL)
	if tag == "" {
		return ""
	}
	return tag
}

func tagFromReleaseURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	if parts[len(parts)-2] != "tag" {
		return ""
	}
	tag := strings.TrimSpace(parts[len(parts)-1])
	if tag == "" || strings.EqualFold(tag, "latest") {
		return ""
	}
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return tag
}

func isGreaterVersion(a, b string) bool {
	pa := parseVersion(a)
	pb := parseVersion(b)
	maxLen := len(pa)
	if len(pb) > maxLen {
		maxLen = len(pb)
	}
	for i := 0; i < maxLen; i++ {
		av := 0
		bv := 0
		if i < len(pa) {
			av = pa[i]
		}
		if i < len(pb) {
			bv = pb[i]
		}
		if av > bv {
			return true
		}
		if av < bv {
			return false
		}
	}
	return false
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

func parseVersion(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		n := 0
		for _, c := range part {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}
