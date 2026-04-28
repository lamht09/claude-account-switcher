package updatecheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadWriteCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCache("1.2.3")
	got, ok := readCache()
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got != "1.2.3" {
		t.Fatalf("expected cached value, got %q", got)
	}
}

func TestReadCacheExpires(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, _ := json.Marshal(cacheBody{
		Timestamp: float64(time.Now().Add(-cacheTTL - time.Second).Unix()),
		Data:      "9.9.9",
	})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	got, ok := readCache()
	if ok || got != "" {
		t.Fatalf("expected stale cache to be ignored, got %q", got)
	}
}

func TestCheckReturnsMessageWhenNewVersionAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCache("v1.2.4")
	msg := Check("v1.2.3")
	if msg == "" {
		t.Fatal("expected update message")
	}
	if !strings.Contains(msg, "v1.2.4") {
		t.Fatalf("expected latest version in message, got %q", msg)
	}
}

func TestFetchLatestFallsBackToReleasePageWhenAPIForbidden(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limit", http.StatusForbidden)
	})
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/v9.9.9", http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/v9.9.9", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	origAPI := latestReleaseURL
	origPage := latestReleasePageURL
	origClient := updateCheckHTTPClient
	latestReleaseURL = server.URL + "/api/latest"
	latestReleasePageURL = server.URL + "/releases/latest"
	updateCheckHTTPClient = server.Client()
	t.Cleanup(func() {
		latestReleaseURL = origAPI
		latestReleasePageURL = origPage
		updateCheckHTTPClient = origClient
	})

	if got := fetchLatest(); got != "v9.9.9" {
		t.Fatalf("expected fallback tag, got %q", got)
	}
}

func TestTagFromReleaseURL(t *testing.T) {
	u, err := url.Parse("https://github.com/lamht09/claude-account-switcher/releases/tag/v1.2.3")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if got := tagFromReleaseURL(u); got != "v1.2.3" {
		t.Fatalf("expected v1.2.3, got %q", got)
	}
}

func TestCheckHandlesVersionPrefixesAndSuffixes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCache(" V1.2.4 ")
	msg := Check("v1.2.3-dirty")
	if msg == "" {
		t.Fatal("expected update message for normalized versions")
	}
}

func TestCheckDoesNotRefreshFreshCacheTimestamp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := cachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	initial := cacheBody{
		Timestamp: float64(time.Now().Add(-time.Hour).Unix()),
		Data:      "v0.0.1",
	}
	raw, _ := json.Marshal(initial)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	_ = Check("v9.9.9")

	afterRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var after cacheBody
	if err := json.Unmarshal(afterRaw, &after); err != nil {
		t.Fatalf("unmarshal cache: %v", err)
	}
	if after.Timestamp != initial.Timestamp {
		t.Fatalf("expected timestamp unchanged for fresh cache, before=%v after=%v", initial.Timestamp, after.Timestamp)
	}
	if after.Data != initial.Data {
		t.Fatalf("expected data unchanged for fresh cache, before=%q after=%q", initial.Data, after.Data)
	}
}

