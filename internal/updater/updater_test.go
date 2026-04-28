package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureVersionPrefix(t *testing.T) {
	if got := ensureVersionPrefix("1.2.3"); got != "v1.2.3" {
		t.Fatalf("unexpected prefixed version: %s", got)
	}
	if got := ensureVersionPrefix("v1.2.3"); got != "v1.2.3" {
		t.Fatalf("unexpected already-prefixed version: %s", got)
	}
}

func TestVerifyChecksum(t *testing.T) {
	content := []byte("payload")
	sum := sha256.Sum256(content)
	checksum := fmt.Sprintf("%s  ca_linux_amd64.tar.gz\n", hex.EncodeToString(sum[:]))
	if err := verifyChecksum([]byte(checksum), "ca_linux_amd64.tar.gz", content); err != nil {
		t.Fatalf("unexpected verify error: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	checksum := "0000000000000000000000000000000000000000000000000000000000000000  ca_linux_amd64.tar.gz\n"
	err := verifyChecksum([]byte(checksum), "ca_linux_amd64.tar.gz", []byte("payload"))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got: %v", err)
	}
}

func TestExtractTarGzEntry(t *testing.T) {
	archive := makeTarGz(t, "ca_linux_amd64", []byte("bin"))
	got, err := extractTarGzEntry("ca_linux_amd64", archive)
	if err != nil {
		t.Fatalf("unexpected extract error: %v", err)
	}
	if string(got) != "bin" {
		t.Fatalf("unexpected extracted content: %s", string(got))
	}
}

func TestExtractZipEntry(t *testing.T) {
	archive := makeZip(t, "ca_windows_amd64.exe", []byte("bin"))
	got, err := extractZipEntry("ca_windows_amd64.exe", archive)
	if err != nil {
		t.Fatalf("unexpected extract error: %v", err)
	}
	if string(got) != "bin" {
		t.Fatalf("unexpected extracted content: %s", string(got))
	}
}

func TestCurrentPlatformTuple(t *testing.T) {
	p, err := currentPlatform()
	if err != nil {
		t.Fatalf("unexpected platform error: %v", err)
	}
	if _, _, err := p.assetAndBinary(); err != nil {
		t.Fatalf("unexpected asset mapping error: %v", err)
	}
}

func TestRunCheckOnlyLatestAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tag_name":"v9.9.9"}`)
	}))
	defer server.Close()

	origClient := httpClient
	origLatestURL := latestURL
	httpClient = server.Client()
	latestURL = server.URL
	t.Cleanup(func() {
		httpClient = origClient
		latestURL = origLatestURL
	})

	var out bytes.Buffer
	err := Run(Options{
		CurrentVersion: "v1.0.0",
		CheckOnly:      true,
		Stdout:         &out,
	})
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	if !strings.Contains(out.String(), "Update available") {
		t.Fatalf("expected update available message, got %q", out.String())
	}
}

func TestRunTargetSameVersionNoForce(t *testing.T) {
	var out bytes.Buffer
	err := Run(Options{
		CurrentVersion: "v1.2.3",
		ToVersion:      "v1.2.3",
		Stdout:         &out,
	})
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	if !strings.Contains(out.String(), "Already on requested version") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRunEndToEndWithMockRelease(t *testing.T) {
	platform, err := currentPlatform()
	if err != nil {
		t.Skipf("unsupported platform for updater test: %v", err)
	}
	asset, binaryName, err := platform.assetAndBinary()
	if err != nil {
		t.Fatalf("asset mapping: %v", err)
	}
	binBody := []byte("new-binary")
	var archive []byte
	if platform.os == "windows" {
		archive = makeZip(t, binaryName, binBody)
	} else {
		archive = makeTarGz(t, binaryName, binBody)
	}
	sum := sha256.Sum256(archive)
	checksum := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_, _ = io.WriteString(w, `{"tag_name":"v1.1.0"}`)
		case "/download/v1.1.0/SHA256SUMS":
			_, _ = io.WriteString(w, checksum)
		case "/download/v1.1.0/" + asset:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	origClient := httpClient
	origLatestURL := latestURL
	origDownloadBase := downloadBase
	origExecPath := execPathFunc
	httpClient = server.Client()
	latestURL = server.URL + "/latest"
	downloadBase = server.URL + "/download/"
	tmpDir := t.TempDir()
	execName := "ca"
	if runtime.GOOS == "windows" {
		execName = "ca.exe"
	}
	execFile := filepath.Join(tmpDir, execName)
	if err := os.WriteFile(execFile, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("seed executable: %v", err)
	}
	execPathFunc = func() (string, error) { return execFile, nil }
	t.Cleanup(func() {
		httpClient = origClient
		latestURL = origLatestURL
		downloadBase = origDownloadBase
		execPathFunc = origExecPath
	})

	var out bytes.Buffer
	var stderr bytes.Buffer
	err = Run(Options{
		CurrentVersion: "v1.0.0",
		Stdout:         &out,
		Stderr:         &stderr,
	})
	if err != nil {
		t.Fatalf("unexpected run error: %v", err)
	}
	updatedBody, err := os.ReadFile(execFile)
	if err != nil {
		t.Fatalf("read updated executable: %v", err)
	}
	if string(updatedBody) != string(binBody) {
		t.Fatalf("binary was not replaced; got %q", string(updatedBody))
	}
	if !strings.Contains(out.String(), "Updated successfully") {
		t.Fatalf("expected success message, got %q", out.String())
	}
}

func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("write zip content: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
