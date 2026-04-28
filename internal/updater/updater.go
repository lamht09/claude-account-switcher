package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repoSlug       = "lamht09/claude-account-switcher"
	requestTimeout = 8 * time.Second
)

type Options struct {
	CurrentVersion string
	ToVersion      string
	CheckOnly      bool
	Force          bool
	Stdout         io.Writer
	Stderr         io.Writer
}

type latestRelease struct {
	TagName string `json:"tag_name"`
}

var (
	latestURL    = "https://api.github.com/repos/" + repoSlug + "/releases/latest"
	downloadBase = "https://github.com/" + repoSlug + "/releases/download/"
	httpClient   = &http.Client{Timeout: requestTimeout}
	execPathFunc = os.Executable
)

func Run(opts Options) error {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	target, fromLatest, err := resolveTargetVersion(strings.TrimSpace(opts.ToVersion))
	if err != nil {
		return err
	}
	current := strings.TrimSpace(opts.CurrentVersion)
	targetClean := normalizeVersion(target)
	currentClean := normalizeVersion(current)
	newer := isGreaterVersion(targetClean, currentClean)

	if opts.CheckOnly {
		return printCheckOnlyResult(stdout, current, target, newer, fromLatest)
	}

	if !opts.Force && strings.TrimSpace(opts.ToVersion) == "" && !newer {
		fmt.Fprintf(stdout, "Already up-to-date: current %s, latest %s\n", current, target)
		return nil
	}
	if !opts.Force && strings.TrimSpace(opts.ToVersion) != "" && targetClean == currentClean {
		fmt.Fprintf(stdout, "Already on requested version %s\n", target)
		return nil
	}

	platform, err := currentPlatform()
	if err != nil {
		return err
	}
	assetName, binName, err := platform.assetAndBinary()
	if err != nil {
		return err
	}

	checksumURL := downloadBase + target + "/SHA256SUMS"
	assetURL := downloadBase + target + "/" + assetName
	checksumBody, err := download(checksumURL)
	if err != nil {
		return fmt.Errorf("cannot download checksums for %s: %w", target, err)
	}
	assetBody, err := download(assetURL)
	if err != nil {
		return fmt.Errorf("cannot download asset %s: %w", assetName, err)
	}
	if err := verifyChecksum(checksumBody, assetName, assetBody); err != nil {
		return err
	}

	newBinary, err := extractBinary(platform.os, binName, assetBody)
	if err != nil {
		return err
	}

	execPath, err := execPathFunc()
	if err != nil {
		return fmt.Errorf("cannot resolve current executable path: %w", err)
	}
	resolvedExecPath, err := filepath.EvalSymlinks(execPath)
	if err == nil {
		execPath = resolvedExecPath
	}
	if err := replaceBinary(execPath, newBinary, platform.os == "windows"); err != nil {
		return err
	}
	if err := verifyInstalledVersion(execPath, targetClean); err != nil {
		fmt.Fprintf(stderr, "Warning: updated binary installed but post-check failed: %v\n", err)
	}
	fmt.Fprintf(stdout, "Updated successfully: %s -> %s (%s)\n", current, target, execPath)
	return nil
}

func printCheckOnlyResult(stdout io.Writer, current, target string, newer, fromLatest bool) error {
	if fromLatest {
		if newer {
			fmt.Fprintf(stdout, "Update available: current %s, latest %s\n", current, target)
			return nil
		}
		if normalizeVersion(current) == normalizeVersion(target) {
			fmt.Fprintf(stdout, "You are on the latest version: %s\n", current)
			return nil
		}
		fmt.Fprintf(stdout, "Current version %s is ahead of latest release %s\n", current, target)
		return nil
	}
	if normalizeVersion(current) == normalizeVersion(target) {
		fmt.Fprintf(stdout, "Current version already matches requested target: %s\n", current)
		return nil
	}
	if newer {
		fmt.Fprintf(stdout, "Requested target %s is newer than current %s\n", target, current)
		return nil
	}
	fmt.Fprintf(stdout, "Requested target %s is older than current %s\n", target, current)
	return nil
}

func resolveTargetVersion(to string) (string, bool, error) {
	if to != "" {
		return ensureVersionPrefix(to), false, nil
	}
	tag, err := fetchLatestTag()
	if err != nil {
		return "", true, err
	}
	return tag, true, nil
}

func fetchLatestTag() (string, error) {
	body, err := download(latestURL)
	if err != nil {
		return "", err
	}
	var rel latestRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("invalid latest release response: %w", err)
	}
	tag := strings.TrimSpace(rel.TagName)
	if tag == "" {
		return "", errors.New("latest release tag_name is empty")
	}
	return tag, nil
}

func download(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "claude-account-switcher-updater/1.0")
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("GitHub API request forbidden (possibly rate-limited); set GITHUB_TOKEN and retry")
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("resource not found at %s", url)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

type runtimePlatform struct {
	os   string
	arch string
}

func currentPlatform() (runtimePlatform, error) {
	osName := runtime.GOOS
	switch osName {
	case "darwin":
		osName = "macos"
	case "linux", "windows":
	default:
		return runtimePlatform{}, fmt.Errorf("unsupported OS %q (supported: linux, macos, windows)", runtime.GOOS)
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64":
	default:
		return runtimePlatform{}, fmt.Errorf("unsupported architecture %q (supported: amd64, arm64)", runtime.GOARCH)
	}
	return runtimePlatform{os: osName, arch: arch}, nil
}

func (p runtimePlatform) assetAndBinary() (string, string, error) {
	if p.os == "windows" {
		return fmt.Sprintf("ca_windows_%s.zip", p.arch), fmt.Sprintf("ca_windows_%s.exe", p.arch), nil
	}
	if p.os == "linux" || p.os == "macos" {
		bin := fmt.Sprintf("ca_%s_%s", p.os, p.arch)
		return bin + ".tar.gz", bin, nil
	}
	return "", "", fmt.Errorf("unsupported platform tuple %s/%s", p.os, p.arch)
}

func verifyChecksum(checksumFile []byte, assetName string, content []byte) error {
	lines := strings.Split(string(checksumFile), "\n")
	expected := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == assetName {
			expected = strings.ToLower(strings.TrimSpace(fields[0]))
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksum entry for %s not found", assetName)
	}
	sum := sha256.Sum256(content)
	actual := hex.EncodeToString(sum[:])
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, expected, actual)
	}
	return nil
}

func extractBinary(osName, binaryName string, archive []byte) ([]byte, error) {
	if osName == "windows" {
		return extractZipEntry(binaryName, archive)
	}
	return extractTarGzEntry(binaryName, archive)
}

func extractTarGzEntry(binaryName string, archive []byte) ([]byte, error) {
	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("cannot read tar.gz: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("cannot read tar entry: %w", err)
		}
		if filepath.Base(header.Name) != binaryName {
			continue
		}
		return io.ReadAll(tr)
	}
	return nil, fmt.Errorf("binary %s not found in archive", binaryName)
}

func extractZipEntry(binaryName string, archive []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("cannot read zip archive: %w", err)
	}
	for _, file := range reader.File {
		if filepath.Base(file.Name) != binaryName {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("cannot open zip entry %s: %w", binaryName, err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}
	return nil, fmt.Errorf("binary %s not found in archive", binaryName)
}

func replaceBinary(execPath string, binary []byte, windows bool) error {
	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, ".ca-update-*")
	if err != nil {
		return fmt.Errorf("cannot create temporary file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write temporary binary: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil && !windows {
		tmp.Close()
		return fmt.Errorf("cannot set executable permission: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot finalize temporary binary: %w", err)
	}

	backupPath := execPath + ".bak"
	_ = os.Remove(backupPath)
	if err := os.Rename(execPath, backupPath); err != nil {
		if windows {
			return fmt.Errorf("cannot replace running executable on Windows (%w); close all `ca` processes and retry", err)
		}
		return fmt.Errorf("cannot prepare binary replacement: %w", err)
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		_ = os.Rename(backupPath, execPath)
		return fmt.Errorf("cannot install updated binary: %w", err)
	}
	_ = os.Remove(backupPath)
	return nil
}

func verifyInstalledVersion(execPath, target string) error {
	out, err := exec.Command(execPath, "--version").CombinedOutput()
	if err != nil {
		return err
	}
	normalized := normalizeVersion(string(out))
	if !strings.Contains(normalized, target) {
		return fmt.Errorf("expected version %s, got output %q", target, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensureVersionPrefix(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

func normalizeVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
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
