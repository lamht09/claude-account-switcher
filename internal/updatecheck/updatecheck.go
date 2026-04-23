package updatecheck

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lamht09/claude-account-switcher/internal/platform"
)

const (
	cacheTTL         = 24 * time.Hour
	latestReleaseURL = "https://api.github.com/repos/lamht09/claude-account-switcher/releases/latest"
)

type latestRelease struct {
	TagName string `json:"tag_name"`
}

type cacheBody struct {
	Timestamp float64 `json:"timestamp"`
	Data      string  `json:"data"`
}

func Check(currentVersion string) string {
	latest := readCache()
	if latest == "" {
		latest = fetchLatest()
	}
	// Source parity: cache both success and failure states.
	writeCache(latest)
	if latest == "" {
		return ""
	}
	if isGreaterVersion(strings.TrimPrefix(latest, "v"), strings.TrimPrefix(currentVersion, "v")) {
		return "A newer version of claude-account-switcher is available (" + latest + "). You are using " + currentVersion + ". Consider upgrading!"
	}
	return ""
}

func cachePath() string {
	return filepath.Join(platform.BackupRoot(), "cache", "update-check.json")
}

func readCache() string {
	raw, err := os.ReadFile(cachePath())
	if err != nil {
		return ""
	}
	var data cacheBody
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	if time.Since(time.Unix(int64(data.Timestamp), 0)) > cacheTTL {
		return ""
	}
	return data.Data
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
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return ""
	}
	var latest latestRelease
	if err := json.NewDecoder(resp.Body).Decode(&latest); err != nil {
		return ""
	}
	return latest.TagName
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

func parseVersion(v string) []int {
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, n)
	}
	return out
}
