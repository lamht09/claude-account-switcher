package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/lamht09/claude-account-switcher/internal/updater"
)

func TestFileContainsAny(t *testing.T) {
	origReadFile := readFile
	readFile = func(string) ([]byte, error) {
		return []byte("12:memory:/docker/abc"), nil
	}
	t.Cleanup(func() { readFile = origReadFile })

	if !fileContainsAny("/proc/1/cgroup", []string{"docker"}) {
		t.Fatal("expected marker to be found")
	}
}

func TestFileContainsAnyReadError(t *testing.T) {
	origReadFile := readFile
	readFile = func(string) ([]byte, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() { readFile = origReadFile })

	if fileContainsAny("/proc/1/cgroup", []string{"docker"}) {
		t.Fatal("expected false on read error")
	}
}

func TestParseCLIArgs(t *testing.T) {
	cfg, err := parseCLIArgs([]string{"list", "--token-status"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.list || !cfg.tokenStatus {
		t.Fatalf("unexpected cfg: %#v", cfg)
	}
}

func TestParseCLIArgsSlotValidation(t *testing.T) {
	if _, err := parseCLIArgs([]string{"list", "--slot", "2"}); err == nil {
		t.Fatal("expected slot validation error")
	}
	cfg, err := parseCLIArgs([]string{"add", "--slot", "2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.addAccount || cfg.slot != 2 {
		t.Fatalf("unexpected cfg: %#v", cfg)
	}
}

func TestParseCLIArgsMutualExclusion(t *testing.T) {
	if _, err := parseCLIArgs([]string{"list", "status"}); err == nil {
		t.Fatal("expected unexpected positional args error")
	}
}

func TestParseCLIArgsVersionOnly(t *testing.T) {
	cfg, err := parseCLIArgs([]string{"--version"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.showVersion {
		t.Fatalf("expected version flag in cfg: %#v", cfg)
	}
}

func TestParseCLIArgsNoCommandShowsHelp(t *testing.T) {
	cfg, err := parseCLIArgs([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.showHelp {
		t.Fatalf("expected showHelp for empty args, got %#v", cfg)
	}
}

func TestParseCLIArgsHelpCommand(t *testing.T) {
	cfg, err := parseCLIArgs([]string{"help"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.showHelp {
		t.Fatalf("expected showHelp for help command, got %#v", cfg)
	}
}

func TestParseCLIArgsRemoveAccountIdentifierValidation(t *testing.T) {
	if _, err := parseCLIArgs([]string{"remove", "not-an-email"}); err == nil {
		t.Fatal("expected validation error for non-numeric, non-email identifier")
	}
	cfg, err := parseCLIArgs([]string{"remove", "2"})
	if err != nil {
		t.Fatalf("unexpected error for numeric identifier: %v", err)
	}
	if cfg.removeAccount != "2" {
		t.Fatalf("unexpected remove account identifier: %#v", cfg)
	}
	cfg, err = parseCLIArgs([]string{"remove", "user@example.com"})
	if err != nil {
		t.Fatalf("unexpected error for email identifier: %v", err)
	}
	if cfg.removeAccount != "user@example.com" {
		t.Fatalf("unexpected remove account identifier: %#v", cfg)
	}
}

func TestParseCLIArgsSwitchToIdentifierValidation(t *testing.T) {
	_, err := parseCLIArgs([]string{"switch-to", "bad-value"})
	if err == nil || !strings.Contains(err.Error(), "invalid email format") {
		t.Fatalf("expected invalid email format error, got: %v", err)
	}
	if _, err := parseCLIArgs([]string{"switch-to", "1"}); err != nil {
		t.Fatalf("unexpected error for numeric switch-to identifier: %v", err)
	}
	if _, err := parseCLIArgs([]string{"switch-to", "person@example.com"}); err != nil {
		t.Fatalf("unexpected error for email switch-to identifier: %v", err)
	}
}

func TestParseCLIArgsRepairAction(t *testing.T) {
	cfg, err := parseCLIArgs([]string{"repair"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.repair {
		t.Fatalf("expected repair action enabled, got %#v", cfg)
	}
}

func TestParseCLIArgsUpdateFlags(t *testing.T) {
	cfg, err := parseCLIArgs([]string{"update", "--to", "v1.2.3", "--check-only", "--force"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.update || cfg.updateTo != "v1.2.3" || !cfg.checkOnly || !cfg.forceUpdate {
		t.Fatalf("unexpected update cfg: %#v", cfg)
	}
}

func TestParseCLIArgsUpdateRejectsPositionalArgs(t *testing.T) {
	_, err := parseCLIArgs([]string{"update", "now"})
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments for update") {
		t.Fatalf("expected update positional args validation error, got %v", err)
	}
}

func TestRunActionUpdateDispatch(t *testing.T) {
	origRunUpdater := runUpdater
	defer func() { runUpdater = origRunUpdater }()

	var called bool
	var got updater.Options
	runUpdater = func(opts updater.Options) error {
		called = true
		got = opts
		return nil
	}

	cfg := cliConfig{
		update:      true,
		updateTo:    "v2.0.0",
		checkOnly:   true,
		forceUpdate: true,
	}
	if err := runAction(cfg, nil); err != nil {
		t.Fatalf("unexpected runAction error: %v", err)
	}
	if !called {
		t.Fatal("expected updater to be called")
	}
	if got.ToVersion != "v2.0.0" || !got.CheckOnly || !got.Force {
		t.Fatalf("unexpected updater options: %#v", got)
	}
	if got.CurrentVersion != version {
		t.Fatalf("unexpected current version: %s", got.CurrentVersion)
	}
	if got.Stdout == nil || got.Stderr == nil {
		t.Fatal("expected stdout/stderr to be set")
	}
}

func TestParseCLIArgsUpdateBlankToVersion(t *testing.T) {
	_, err := parseCLIArgs([]string{"update", "--to", "   "})
	if err == nil || !strings.Contains(err.Error(), "--to cannot be blank") {
		t.Fatalf("expected blank to-version error, got %v", err)
	}
}

func TestRunActionUpdatePropagatesError(t *testing.T) {
	origRunUpdater := runUpdater
	defer func() { runUpdater = origRunUpdater }()

	runUpdater = func(opts updater.Options) error {
		return errors.New("update failed")
	}
	err := runAction(cliConfig{update: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "update failed") {
		t.Fatalf("expected update error to propagate, got %v", err)
	}
}
