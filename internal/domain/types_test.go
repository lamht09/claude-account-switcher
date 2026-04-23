package domain

import (
	"errors"
	"testing"
)

func TestDetectPlatformLinuxVariants(t *testing.T) {
	origGOOS := runtimeGOOS
	origEnv := lookupEnv
	origReadFile := readFile
	t.Cleanup(func() {
		runtimeGOOS = origGOOS
		lookupEnv = origEnv
		readFile = origReadFile
	})

	runtimeGOOS = "linux"

	t.Run("detects wsl via env", func(t *testing.T) {
		lookupEnv = func(key string) string {
			if key == "WSL_DISTRO_NAME" {
				return "Ubuntu"
			}
			return ""
		}
		readFile = func(string) ([]byte, error) { return nil, errors.New("unused") }
		if got := DetectPlatform(); got != PlatformWSL {
			t.Fatalf("expected %q, got %q", PlatformWSL, got)
		}
	})

	t.Run("detects wsl via proc version", func(t *testing.T) {
		lookupEnv = func(string) string { return "" }
		readFile = func(string) ([]byte, error) { return []byte("Linux version ... Microsoft"), nil }
		if got := DetectPlatform(); got != PlatformWSL {
			t.Fatalf("expected %q, got %q", PlatformWSL, got)
		}
	})

	t.Run("falls back to linux", func(t *testing.T) {
		lookupEnv = func(string) string { return "" }
		readFile = func(string) ([]byte, error) { return nil, errors.New("missing") }
		if got := DetectPlatform(); got != PlatformLinux {
			t.Fatalf("expected %q, got %q", PlatformLinux, got)
		}
	})
}
