package storage

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lamht09/claude-account-switcher/internal/domain"
)

func TestSequenceIsCanonicalStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	s := NewStore()
	if err := s.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	seq := &domain.SequenceData{
		Sequence:            []int{1},
		ActiveAccountNumber: intPtr(1),
		Accounts: map[string]domain.Account{
			"1": {Email: "one@example.com"},
		},
	}
	if err := s.WriteSequence(seq); err != nil {
		t.Fatalf("write sequence: %v", err)
	}
	if _, err := s.ReadSequence(); err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-account-backup", "accounts.json")); !os.IsNotExist(err) {
		t.Fatalf("accounts.json should not be created, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude-account-backup", "slots.json")); !os.IsNotExist(err) {
		t.Fatalf("slots.json should not be created, err=%v", err)
	}
}

func TestWriteAndReadAccountBackupFileBackend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "")

	s := NewStore()
	s.platform = domain.PlatformLinux
	if err := s.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	cfgRaw := `{"oauthAccount":{"emailAddress":"a@example.com","organizationUuid":"org-a","accountUuid":"acc-a"}}`
	if err := s.WriteAccountBackup("1", "a@example.com", cfgRaw, `{"token":"x"}`); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	cfg, creds, err := s.ReadAccountBackup("1", "a@example.com")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if cfg != cfgRaw {
		t.Fatalf("unexpected cfg: %s", cfg)
	}
	if creds != `{"token":"x"}` {
		t.Fatalf("unexpected creds: %s", creds)
	}
}

func TestUsesBackupCredentialsFileForWSL(t *testing.T) {
	s := NewStore()
	s.platform = domain.PlatformWSL
	if !s.usesBackupCredentialsFile() {
		t.Fatal("expected WSL to use file-backed backup credentials")
	}
}

func TestReadAccountBackupMigratesLegacyFileToFingerprintFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))

	s := NewStore()
	s.platform = domain.PlatformLinux
	if err := s.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	cfgRaw := `{"oauthAccount":{"emailAddress":"a@example.com","organizationUuid":"org-a","accountUuid":"acc-a"}}`
	if err := writeAtomic(s.backupConfigPath("1", "a@example.com"), []byte(cfgRaw), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	legacyPath := s.backupCredentialPath("1", "a@example.com")
	legacyEncoded := base64.StdEncoding.EncodeToString([]byte(`{"token":"legacy"}`))
	if err := writeAtomic(legacyPath, []byte(legacyEncoded), 0o600); err != nil {
		t.Fatalf("write legacy credentials: %v", err)
	}
	_, creds, err := s.ReadAccountBackup("1", "a@example.com")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if creds != `{"token":"legacy"}` {
		t.Fatalf("unexpected creds: %s", creds)
	}
	fpPath := s.backupCredentialPathByFingerprint("uuid-org:acc-a|org-a")
	if _, err := os.Stat(fpPath); err != nil {
		t.Fatalf("expected migrated fingerprint file, err=%v", err)
	}
}

func TestReadLiveCredentialsMacOSUsesServiceLookupFirst(t *testing.T) {
	s := NewStore()
	s.platform = domain.PlatformMacOS
	t.Setenv("USER", "alice")
	t.Setenv("CA_FORCE_FILE_BACKUP_CREDENTIALS", "")

	origExecCommand := execCommand
	execCommand = testExecCommandFactory("primary-ok")
	t.Cleanup(func() { execCommand = origExecCommand })

	creds, err := s.ReadLiveCredentials()
	if err != nil {
		t.Fatalf("read live credentials: %v", err)
	}
	if creds != "creds-primary" {
		t.Fatalf("unexpected credentials: %q", creds)
	}
}

func TestReadLiveCredentialsMacOSFallbacksToLegacyAccount(t *testing.T) {
	s := NewStore()
	s.platform = domain.PlatformMacOS
	t.Setenv("USER", "alice")
	t.Setenv("CA_FORCE_FILE_BACKUP_CREDENTIALS", "")

	origExecCommand := execCommand
	execCommand = testExecCommandFactory("fallback-legacy")
	t.Cleanup(func() { execCommand = origExecCommand })

	creds, err := s.ReadLiveCredentials()
	if err != nil {
		t.Fatalf("read live credentials with fallback: %v", err)
	}
	if creds != "creds-legacy" {
		t.Fatalf("unexpected credentials: %q", creds)
	}
}

func TestWriteLiveCredentialsMacOSUsesUserAccount(t *testing.T) {
	s := NewStore()
	s.platform = domain.PlatformMacOS
	t.Setenv("USER", "alice")
	t.Setenv("CA_FORCE_FILE_BACKUP_CREDENTIALS", "")

	origExecCommand := execCommand
	execCommand = testExecCommandFactory("write")
	t.Cleanup(func() { execCommand = origExecCommand })

	if err := s.WriteLiveCredentials("  token-123  "); err != nil {
		t.Fatalf("write live credentials: %v", err)
	}
}

func TestWriteLiveCredentialsMacOSUsesDefaultUserFallback(t *testing.T) {
	s := NewStore()
	s.platform = domain.PlatformMacOS
	t.Setenv("USER", "")
	t.Setenv("CA_FORCE_FILE_BACKUP_CREDENTIALS", "")

	origExecCommand := execCommand
	execCommand = testExecCommandFactory("write-default")
	t.Cleanup(func() { execCommand = origExecCommand })

	if err := s.WriteLiveCredentials("token-xyz"); err != nil {
		t.Fatalf("write live credentials: %v", err)
	}
}

func testExecCommandFactory(mode string) func(string, ...string) *exec.Cmd {
	return func(command string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestHelperProcessStorage", "--", mode, command}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.Command(os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}
}

func TestHelperProcessStorage(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	sep := 0
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep == 0 || sep+3 > len(args) {
		_, _ = fmt.Fprintln(os.Stderr, "invalid helper invocation")
		os.Exit(2)
	}

	mode := args[sep+1]
	command := args[sep+2]
	cmdArgs := args[sep+3:]
	if command != "security" {
		_, _ = fmt.Fprintf(os.Stderr, "unexpected command %q\n", command)
		os.Exit(2)
	}

	switch mode {
	case "primary-ok":
		expectArgsOrExit(cmdArgs, []string{"find-generic-password", "-s", keychainService, "-a", "alice", "-w"})
		_, _ = fmt.Fprint(os.Stdout, "creds-primary\n")
		os.Exit(0)
	case "fallback-legacy":
		if len(cmdArgs) >= 7 && strings.Join(cmdArgs, " ") == "find-generic-password -s "+keychainService+" -a alice -w" {
			os.Exit(1)
		}
		if len(cmdArgs) >= 5 && strings.Join(cmdArgs, " ") == "find-generic-password -s "+keychainService+" -w" {
			os.Exit(1)
		}
		expectArgsOrExit(cmdArgs, []string{"find-generic-password", "-s", keychainService, "-a", legacyKeychainAccount, "-w"})
		_, _ = fmt.Fprint(os.Stdout, "creds-legacy\n")
		os.Exit(0)
	case "write":
		expectArgsOrExit(cmdArgs, []string{"add-generic-password", "-U", "-s", keychainService, "-a", "alice", "-w", "token-123"})
		os.Exit(0)
	case "write-default":
		expectArgsOrExit(cmdArgs, []string{"add-generic-password", "-U", "-s", keychainService, "-a", "user", "-w", "token-xyz"})
		os.Exit(0)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(2)
	}
}

func expectArgsOrExit(got, want []string) {
	if len(got) != len(want) {
		_, _ = fmt.Fprintf(os.Stderr, "args length mismatch: got=%v want=%v\n", got, want)
		os.Exit(2)
	}
	for i := range want {
		if got[i] != want[i] {
			_, _ = fmt.Fprintf(os.Stderr, "arg mismatch at %d: got=%q want=%q full=%v\n", i, got[i], want[i], got)
			os.Exit(2)
		}
	}
}

func intPtr(v int) *int { return &v }
