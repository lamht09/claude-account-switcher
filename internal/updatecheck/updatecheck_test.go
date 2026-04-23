package updatecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadWriteCacheRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCache("1.2.3")
	if got := readCache(); got != "1.2.3" {
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
	if got := readCache(); got != "" {
		t.Fatalf("expected stale cache to be ignored, got %q", got)
	}
}

