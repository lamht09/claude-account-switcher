package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lamht09/claude-account-switcher/internal/domain"
	"github.com/lamht09/claude-account-switcher/internal/oauth"
)

func setupTestSwitcher(t *testing.T) *Switcher {
	t.Helper()
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sw := NewSwitcher()
	if err := sw.setup(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return sw
}

func TestSwitchMergeOnlyOAuthAccount(t *testing.T) {
	sw := setupTestSwitcher(t)

	liveCfgPath := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	liveCfg := map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "active@example.com",
			"organizationUuid": "org-active",
			"accountUuid":      "acc-active",
		},
		"theme": "dark",
	}
	if err := sw.store.WriteLiveConfig(liveCfgPath, liveCfg); err != nil {
		t.Fatalf("write live config: %v", err)
	}
	if err := sw.store.WriteLiveCredentials(`{"claudeAiOauth":{"accessToken":"active"}}`); err != nil {
		t.Fatalf("write live creds: %v", err)
	}
	targetCfgRaw, _ := json.Marshal(map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "target@example.com",
			"organizationUuid": "org-target",
			"accountUuid":      "acc-target",
		},
		"theme": "light",
	})
	if err := sw.store.WriteAccountBackup("2", "target@example.com", string(targetCfgRaw), `{"claudeAiOauth":{"accessToken":"target"}}`); err != nil {
		t.Fatalf("write target backup: %v", err)
	}
	seq := &domain.SequenceData{
		Sequence:            []int{1, 2},
		ActiveAccountNumber: intPtr(1),
		Accounts: map[string]domain.Account{
			"1": {Email: "active@example.com", OrganizationUUID: "org-active", UUID: "acc-active"},
			"2": {Email: "target@example.com", OrganizationUUID: "org-target", UUID: "acc-target"},
		},
	}
	if err := sw.store.WriteSequence(seq); err != nil {
		t.Fatalf("write sequence: %v", err)
	}

	if err := sw.lock.WithLock(func() error {
		latest, err := sw.readSequence()
		if err != nil {
			return err
		}
		return sw.switchToNumberLocked(2, latest)
	}); err != nil {
		t.Fatalf("switch: %v", err)
	}

	got, _, err := sw.store.ReadLiveConfig()
	if err != nil {
		t.Fatalf("read live config: %v", err)
	}
	if got["theme"] != "dark" {
		t.Fatalf("theme changed unexpectedly: %#v", got["theme"])
	}
	oauthData, _ := got["oauthAccount"].(map[string]any)
	email, _ := oauthData["emailAddress"].(string)
	if email != "target@example.com" {
		t.Fatalf("oauthAccount not switched: %q", email)
	}
}

func TestSwitchUsesCurrentIdentityWhenActiveSlotIsStale(t *testing.T) {
	sw := setupTestSwitcher(t)

	setLiveAccount(t, sw, "a@example.com", "org-a", "Org A", "token-a-live")
	if err := sw.store.WriteAccountBackup("1", "a@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "a@example.com",
			"organizationUuid": "org-a",
			"accountUuid":      "acc-org-a",
		},
	}), `{"claudeAiOauth":{"accessToken":"token-a-backup"}}`); err != nil {
		t.Fatalf("write backup 1: %v", err)
	}
	if err := sw.store.WriteAccountBackup("2", "b@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "b@example.com",
			"organizationUuid": "org-b",
			"accountUuid":      "acc-b",
		},
	}), `{"claudeAiOauth":{"accessToken":"token-b-backup"}}`); err != nil {
		t.Fatalf("write backup 2: %v", err)
	}

	seq := &domain.SequenceData{
		Sequence:            []int{1, 2},
		ActiveAccountNumber: intPtr(2), // stale pointer: live identity is slot 1
		Accounts: map[string]domain.Account{
			"1": {Email: "a@example.com", OrganizationUUID: "org-a", UUID: "acc-org-a"},
			"2": {Email: "b@example.com", OrganizationUUID: "org-b", UUID: "acc-b"},
		},
	}
	if err := sw.store.WriteSequence(seq); err != nil {
		t.Fatalf("write sequence: %v", err)
	}

	if err := sw.Switch(); err != nil {
		t.Fatalf("switch should succeed with stale active slot: %v", err)
	}

	_, updatedCreds, err := sw.store.ReadAccountBackup("1", "a@example.com")
	if err != nil {
		t.Fatalf("read updated backup 1: %v", err)
	}
	if !strings.Contains(updatedCreds, "token-a-live") {
		t.Fatalf("expected slot 1 backup to contain latest live credentials, got: %s", updatedCreds)
	}
}

func TestResolveManagedIdentifierAmbiguousNonTTY(t *testing.T) {
	sw := setupTestSwitcher(t)
	origTTY := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = origTTY })
	seq := &domain.SequenceData{
		Accounts: map[string]domain.Account{
			"1": {Email: "same@example.com", OrganizationUUID: "org-1", UUID: "acc-1"},
			"2": {Email: "same@example.com", OrganizationUUID: "org-2", UUID: "acc-2"},
		},
	}
	_, err := sw.resolveManagedIdentifier(seq, "same@example.com", "switch")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !errors.Is(err, domain.ErrAmbiguousIdentity) {
		t.Fatalf("expected ErrAmbiguousIdentity, got %v", err)
	}
}

func TestRemoveFailsWithoutTTY(t *testing.T) {
	sw := setupTestSwitcher(t)
	seq := &domain.SequenceData{
		Sequence:            []int{1},
		ActiveAccountNumber: intPtr(1),
		Accounts: map[string]domain.Account{
			"1": {Email: "active@example.com", UUID: "acc-active", OrganizationUUID: "org-active"},
		},
	}
	if err := sw.store.WriteSequence(seq); err != nil {
		t.Fatalf("write sequence: %v", err)
	}
	err := sw.Remove("1")
	if err == nil {
		t.Fatal("expected non-tty confirmation error")
	}
	if !strings.Contains(err.Error(), "no TTY") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPurgeFailsWithoutTTY(t *testing.T) {
	sw := setupTestSwitcher(t)
	err := sw.Purge()
	if err == nil {
		t.Fatal("expected non-tty confirmation error")
	}
	if !strings.Contains(err.Error(), "no TTY") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIntegrationAccountLifecycle(t *testing.T) {
	sw := setupTestSwitcher(t)

	origConfirm := confirmPrompt
	origConfirmDefaultYes := confirmPromptDefaultYes
	confirmPrompt = func(string) (bool, error) { return true, nil }
	confirmPromptDefaultYes = func(string) (bool, error) { return true, nil }
	t.Cleanup(func() {
		confirmPrompt = origConfirm
		confirmPromptDefaultYes = origConfirmDefaultYes
	})

	setLiveAccount(t, sw, "a@example.com", "org-a", "Org A", "token-a")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add first account: %v", err)
	}

	setLiveAccount(t, sw, "b@example.com", "org-b", "Org B", "token-b")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add second account: %v", err)
	}

	if err := sw.SwitchTo("1"); err != nil {
		t.Fatalf("switch-to 1: %v", err)
	}
	if err := sw.Switch(); err != nil {
		t.Fatalf("switch next: %v", err)
	}
	if err := sw.Remove("1"); err != nil {
		t.Fatalf("remove 1: %v", err)
	}

	seq, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if len(seq.Accounts) != 1 {
		t.Fatalf("expected one account left, got %d", len(seq.Accounts))
	}

	if err := sw.Purge(); err != nil {
		t.Fatalf("purge: %v", err)
	}
	root, _ := sw.store.Paths()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("expected purge to remove backup root, err=%v", err)
	}
}

func TestResolveManagedIdentifierAmbiguousInteractive(t *testing.T) {
	sw := setupTestSwitcher(t)
	seq := &domain.SequenceData{
		Accounts: map[string]domain.Account{
			"1": {Email: "same@example.com", OrganizationUUID: "org-1", UUID: "acc-1"},
			"2": {Email: "same@example.com", OrganizationUUID: "org-2", UUID: "acc-2"},
		},
	}

	origTTY := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = origTTY })

	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("2\n"); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = origStdin
		_ = r.Close()
	})

	num, err := sw.resolveManagedIdentifier(seq, "same@example.com", "switch")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if num != "2" {
		t.Fatalf("expected selection 2, got %s", num)
	}
}

func TestResolveManagedIdentifierAmbiguousInteractiveInvalidSelection(t *testing.T) {
	sw := setupTestSwitcher(t)
	seq := &domain.SequenceData{
		Accounts: map[string]domain.Account{
			"1": {Email: "same@example.com", OrganizationUUID: "org-1", UUID: "acc-1"},
			"2": {Email: "same@example.com", OrganizationUUID: "org-2", UUID: "acc-2"},
		},
	}

	origTTY := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = origTTY })

	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := w.WriteString("9\n"); err != nil {
		t.Fatalf("write pipe: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = origStdin
		_ = r.Close()
	})

	_, err = sw.resolveManagedIdentifier(seq, "same@example.com", "switch")
	if !errors.Is(err, errSelectionCancelled) {
		t.Fatalf("expected selection cancelled, got %v", err)
	}
}

func TestUsageCacheTTL(t *testing.T) {
	sw := setupTestSwitcher(t)
	cache := map[string]*oauth.UsageResult{
		"1": {FiveHour: &oauth.UsageWindow{Pct: 10}},
	}
	sw.writeUsageCache(cache)

	readBack, ok := sw.readUsageCache()
	if !ok {
		t.Fatal("expected fresh cache")
	}
	if readBack["1"] == nil || readBack["1"].FiveHour == nil || readBack["1"].FiveHour.Pct != 10 {
		t.Fatalf("unexpected cache content: %#v", readBack)
	}

	root, _ := sw.store.Paths()
	cachePath := filepath.Join(root, "cache", "usage.json")
	raw, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	var body usageCacheFile
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal cache: %v", err)
	}
	body.Timestamp = body.Timestamp - int64(usageCacheTTL/time.Second) - 1
	updated, _ := json.Marshal(body)
	if err := os.WriteFile(cachePath, updated, 0o600); err != nil {
		t.Fatalf("write stale cache: %v", err)
	}

	_, fresh := sw.readUsageCache()
	if fresh {
		t.Fatal("expected stale cache to be rejected")
	}
}

func TestListFetchesUsageInParallel(t *testing.T) {
	sw := setupTestSwitcher(t)
	setLiveAccount(t, sw, "a@example.com", "org-a", "Org A", "token-a")

	if err := sw.store.WriteAccountBackup("1", "a@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "a@example.com",
			"organizationUuid": "org-a",
			"accountUuid":      "acc-a",
		},
	}), `{"claudeAiOauth":{"accessToken":"token-a"}}`); err != nil {
		t.Fatalf("write backup 1: %v", err)
	}
	if err := sw.store.WriteAccountBackup("2", "b@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "b@example.com",
			"organizationUuid": "org-b",
			"accountUuid":      "acc-b",
		},
	}), `{"claudeAiOauth":{"accessToken":"token-b"}}`); err != nil {
		t.Fatalf("write backup 2: %v", err)
	}
	seq := &domain.SequenceData{
		Sequence:            []int{1, 2},
		ActiveAccountNumber: intPtr(1),
		Accounts: map[string]domain.Account{
			"1": {Email: "a@example.com", OrganizationUUID: "org-a", UUID: "acc-a"},
			"2": {Email: "b@example.com", OrganizationUUID: "org-b", UUID: "acc-b"},
		},
	}
	if err := sw.store.WriteSequence(seq); err != nil {
		t.Fatalf("write sequence: %v", err)
	}

	var inFlight int32
	var maxInFlight int32
	origFetch := fetchUsageForAccount
	fetchUsageForAccount = func(credentials string, isActive bool) (*oauth.UsageResult, string, bool) {
		current := atomic.AddInt32(&inFlight, 1)
		for {
			prev := atomic.LoadInt32(&maxInFlight)
			if current <= prev || atomic.CompareAndSwapInt32(&maxInFlight, prev, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return &oauth.UsageResult{FiveHour: &oauth.UsageWindow{Pct: 10}}, credentials, false
	}
	t.Cleanup(func() { fetchUsageForAccount = origFetch })

	if err := sw.List(false); err != nil {
		t.Fatalf("list: %v", err)
	}
	if atomic.LoadInt32(&maxInFlight) < 2 {
		t.Fatalf("expected concurrent usage fetches, max in-flight=%d", maxInFlight)
	}
}

func TestStatusShowsUsageAndOAuthSafeFields(t *testing.T) {
	sw := setupTestSwitcher(t)
	setLiveAccount(t, sw, "status@example.com", "org-status", "Status Org", "token-status")

	if err := sw.store.WriteAccountBackup("1", "status@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "status@example.com",
			"organizationUuid": "org-status",
			"accountUuid":      "acc-org-status",
		},
	}), `{"claudeAiOauth":{"accessToken":"token-status"}}`); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := sw.store.WriteSequence(&domain.SequenceData{
		Sequence:            []int{1},
		ActiveAccountNumber: intPtr(1),
		Accounts: map[string]domain.Account{
			"1": {Email: "status@example.com", OrganizationUUID: "org-status", UUID: "acc-org-status"},
		},
	}); err != nil {
		t.Fatalf("write sequence: %v", err)
	}

	origFetch := fetchUsageForAccount
	fetchUsageForAccount = func(credentials string, isActive bool) (*oauth.UsageResult, string, bool) {
		return &oauth.UsageResult{
			FiveHour: &oauth.UsageWindow{Pct: 42, Clock: "13:00", Countdown: "2h"},
			SevenDay: &oauth.UsageWindow{Pct: 64, Clock: "Mon 10:00", Countdown: "3d"},
		}, credentials, false
	}
	t.Cleanup(func() { fetchUsageForAccount = origFetch })

	out, runErr := captureStdout(func() error { return sw.Status() })
	if runErr != nil {
		t.Fatalf("status: %v", runErr)
	}

	for _, want := range []string{
		"Current profile:",
		"slot 1",
		"Managed slots:",
		"Usage:",
		"5h:  42%",
		"7d:  64%",
		"OAuth:",
		"email=status@example.com",
		"org=org-status",
		"account=acc-org-status",
		"oauth:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected status output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestStatusUsageFallbackWhenCredentialsUnavailable(t *testing.T) {
	sw := setupTestSwitcher(t)
	liveCfgPath := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	if err := sw.store.WriteLiveConfig(liveCfgPath, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "outside@example.com",
			"organizationUuid": "org-outside",
			"accountUuid":      "acc-outside",
		},
	}); err != nil {
		t.Fatalf("write live config: %v", err)
	}

	out, runErr := captureStdout(func() error { return sw.Status() })
	if runErr != nil {
		t.Fatalf("status: %v", runErr)
	}
	for _, want := range []string{
		"outside managed slots",
		"Usage:",
		"usage stats unavailable",
		"OAuth:",
		"email=outside@example.com",
		"org=org-outside",
		"account=acc-outside",
		"oauth: unavailable",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected fallback output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestUsageCacheKeyChangesWhenAccountChanges(t *testing.T) {
	sw := setupTestSwitcher(t)

	oldAccount := domain.Account{
		Email:            "same@example.com",
		OrganizationUUID: "org-a",
		UUID:             "uuid-a",
	}
	newAccount := domain.Account{
		Email:            "same@example.com",
		OrganizationUUID: "org-b",
		UUID:             "uuid-b",
	}

	oldKey := sw.usageCacheKey(oldAccount)
	newKey := sw.usageCacheKey(newAccount)
	if oldKey == newKey {
		t.Fatalf("expected different cache keys for different accounts, got %q", oldKey)
	}
}

func TestUsageCacheKeyFallbackOrder(t *testing.T) {
	sw := setupTestSwitcher(t)

	if key := sw.usageCacheKey(domain.Account{UUID: "acc-uuid", Email: "u@example.com"}); key != "uuid:acc-uuid" {
		t.Fatalf("expected UUID-based key, got %q", key)
	}
	if key := sw.usageCacheKey(domain.Account{UUID: "acc-uuid", OrganizationUUID: "org-1", Email: "u@example.com"}); key != "uuid-org:acc-uuid|org-1" {
		t.Fatalf("expected uuid+org key, got %q", key)
	}
	if key := sw.usageCacheKey(domain.Account{Email: "u@example.com"}); key != "added:" {
		t.Fatalf("expected strict non-identity fallback key, got %q", key)
	}
	if key := sw.usageCacheKey(domain.Account{Added: "2026-01-01T00:00:00Z"}); key != "added:2026-01-01T00:00:00Z" {
		t.Fatalf("expected added timestamp key, got %q", key)
	}
}

func TestSwitchAutoAddsUnmanagedActiveAccount(t *testing.T) {
	sw := setupTestSwitcher(t)

	setLiveAccount(t, sw, "managed@example.com", "org-m", "Managed", "token-m")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add managed account: %v", err)
	}

	// Simulate user currently logged in with an unmanaged account.
	setLiveAccount(t, sw, "new@example.com", "org-n", "New Org", "token-n")

	if err := sw.Switch(); err != nil {
		t.Fatalf("switch should auto-add unmanaged account: %v", err)
	}

	seq, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if len(seq.Accounts) != 2 {
		t.Fatalf("expected 2 accounts after auto-add, got %d", len(seq.Accounts))
	}
	found := false
	for _, acc := range seq.Accounts {
		if acc.Email == "new@example.com" && acc.OrganizationUUID == "org-n" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected unmanaged active account to be auto-added")
	}
}

func TestFirstRunSetupAddsCurrentAccountWhenConfirmed(t *testing.T) {
	sw := setupTestSwitcher(t)
	setLiveAccount(t, sw, "first@example.com", "org-first", "First", "token-first")

	origConfirmDefaultYes := confirmPromptDefaultYes
	confirmPromptDefaultYes = func(string) (bool, error) { return true, nil }
	t.Cleanup(func() { confirmPromptDefaultYes = origConfirmDefaultYes })

	if err := sw.firstRunSetup(); err != nil {
		t.Fatalf("first run setup: %v", err)
	}

	seq, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if len(seq.Accounts) != 1 {
		t.Fatalf("expected 1 account after first run setup, got %d", len(seq.Accounts))
	}
}

func TestAddStoresAccountUUID(t *testing.T) {
	sw := setupTestSwitcher(t)
	setLiveAccount(t, sw, "uuid@example.com", "org-uuid", "Org UUID", "token-uuid")

	if err := sw.Add(0); err != nil {
		t.Fatalf("add account: %v", err)
	}

	seq, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	acc, ok := seq.Accounts["1"]
	if !ok {
		t.Fatal("expected slot 1 to exist")
	}
	if acc.UUID != "acc-org-uuid" {
		t.Fatalf("expected UUID to be persisted, got %q", acc.UUID)
	}
}

// TestSwitchRotatesWhenSequenceOrgMatchesLive checks rotation when live identity
// matches a managed slot on (accountUuid + organizationUuid). Sequence metadata
// must align with the live org; stale org-only drift is no longer treated as the same row.
func TestSwitchRotatesWhenSequenceOrgMatchesLive(t *testing.T) {
	sw := setupTestSwitcher(t)

	liveCfgPath := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	liveCfg := map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "a@example.com",
			"organizationUuid": "org-current",
			"accountUuid":      "acc-stable",
		},
	}
	if err := sw.store.WriteLiveConfig(liveCfgPath, liveCfg); err != nil {
		t.Fatalf("write live config: %v", err)
	}
	if err := sw.store.WriteLiveCredentials(`{"claudeAiOauth":{"accessToken":"a-live"}}`); err != nil {
		t.Fatalf("write live credentials: %v", err)
	}

	if err := sw.store.WriteAccountBackup("1", "a@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "a@example.com",
			"organizationUuid": "org-current",
			"accountUuid":      "acc-stable",
		},
	}), `{"claudeAiOauth":{"accessToken":"a-backup"}}`); err != nil {
		t.Fatalf("write backup 1: %v", err)
	}
	if err := sw.store.WriteAccountBackup("2", "b@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "b@example.com",
			"organizationUuid": "org-b",
			"accountUuid":      "acc-b",
		},
	}), `{"claudeAiOauth":{"accessToken":"b-backup"}}`); err != nil {
		t.Fatalf("write backup 2: %v", err)
	}

	seq := &domain.SequenceData{
		Sequence:            []int{1, 2},
		ActiveAccountNumber: intPtr(2),
		Accounts: map[string]domain.Account{
			"1": {Email: "a@example.com", OrganizationUUID: "org-current", UUID: "acc-stable"},
			"2": {Email: "b@example.com", OrganizationUUID: "org-b", UUID: "acc-b"},
		},
	}
	if err := sw.store.WriteSequence(seq); err != nil {
		t.Fatalf("write sequence: %v", err)
	}

	if err := sw.Switch(); err != nil {
		t.Fatalf("switch should succeed when live matches slot 1 on uuid+org: %v", err)
	}

	cfg, _, err := sw.store.ReadLiveConfig()
	if err != nil {
		t.Fatalf("read live config: %v", err)
	}
	oauthData, _ := cfg["oauthAccount"].(map[string]any)
	email, _ := oauthData["emailAddress"].(string)
	if email != "b@example.com" {
		t.Fatalf("expected switch to rotate to slot 2, got %q", email)
	}
}

func TestAddRefreshesWhenSameUUIDAndSameOrg(t *testing.T) {
	sw := setupTestSwitcher(t)

	setLiveAccount(t, sw, "same@example.com", "org-a", "Org A", "token-a")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add first account: %v", err)
	}

	setLiveAccount(t, sw, "same@example.com", "org-a", "Org A", "token-refreshed")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add same uuid+org should refresh existing slot: %v", err)
	}

	seq, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if len(seq.Accounts) != 1 {
		t.Fatalf("expected single managed slot, got %d", len(seq.Accounts))
	}
	acc, ok := seq.Accounts["1"]
	if !ok {
		t.Fatalf("expected slot 1, got %+v", seq.Accounts)
	}
	if acc.UUID != "acc-org-a" || acc.OrganizationUUID != "org-a" {
		t.Fatalf("unexpected metadata: %+v", acc)
	}
	_, creds, err := sw.store.ReadAccountBackup("1", "same@example.com")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(creds, "token-refreshed") {
		t.Fatalf("expected refreshed token in backup, got %s", creds)
	}
}

func TestAddSameUUIDDifferentOrgCreatesSecondSlot(t *testing.T) {
	sw := setupTestSwitcher(t)

	setLiveAccount(t, sw, "same@example.com", "org-a", "Org A", "token-a")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add first account: %v", err)
	}

	liveCfgPath := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	cfg := map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "same@example.com",
			"organizationUuid": "org-b",
			"organizationName": "Org B",
			"accountUuid":      "acc-org-a",
		},
		"theme": "dark",
	}
	if err := sw.store.WriteLiveConfig(liveCfgPath, cfg); err != nil {
		t.Fatalf("write updated live config: %v", err)
	}
	if err := sw.store.WriteLiveCredentials(`{"claudeAiOauth":{"accessToken":"token-b","refreshToken":"r","expiresAt":9999999999999}}`); err != nil {
		t.Fatalf("write updated live credentials: %v", err)
	}

	if err := sw.Add(0); err != nil {
		t.Fatalf("add same UUID different org: %v", err)
	}

	seq, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if len(seq.Accounts) != 2 {
		t.Fatalf("expected two managed slots (distinct uuid+org identities), got %d", len(seq.Accounts))
	}
}

func TestAccountNumberByIdentitySameEmailDifferentOrgs(t *testing.T) {
	sw := setupTestSwitcher(t)
	seq := &domain.SequenceData{
		Accounts: map[string]domain.Account{
			"1": {Email: "dup@example.com", OrganizationUUID: "org-1", UUID: "uuid-1"},
			"2": {Email: "dup@example.com", OrganizationUUID: "org-2", UUID: "uuid-2"},
		},
	}
	if got := sw.accountNumberByIdentity(seq, "dup@example.com", "org-2", "uuid-2"); got != "2" {
		t.Fatalf("accountNumberByIdentity for org-2: got %q, want 2", got)
	}
	if got := sw.accountNumberByIdentity(seq, "dup@example.com", "org-1", "uuid-1"); got != "1" {
		t.Fatalf("accountNumberByIdentity for org-1: got %q, want 1", got)
	}
}

func TestAddWithoutSlotRefreshesExistingSlot(t *testing.T) {
	sw := setupTestSwitcher(t)

	setLiveAccount(t, sw, "refresh@example.com", "org-r", "Org R", "token-old")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add first account: %v", err)
	}

	setLiveAccount(t, sw, "refresh@example.com", "org-r", "Org R", "token-new")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add same account should refresh existing slot: %v", err)
	}

	seq, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if len(seq.Accounts) != 1 {
		t.Fatalf("expected 1 managed account, got %d", len(seq.Accounts))
	}
	if len(seq.Sequence) != 1 || seq.Sequence[0] != 1 {
		t.Fatalf("expected sequence to stay on slot 1, got %#v", seq.Sequence)
	}
	if seq.ActiveAccountNumber == nil || *seq.ActiveAccountNumber != 1 {
		t.Fatalf("expected active slot 1, got %#v", seq.ActiveAccountNumber)
	}

	_, creds, err := sw.store.ReadAccountBackup("1", "refresh@example.com")
	if err != nil {
		t.Fatalf("read refreshed backup creds: %v", err)
	}
	if !strings.Contains(creds, "token-new") {
		t.Fatalf("expected refreshed credentials in slot 1 backup, got %s", creds)
	}
}

func TestAddRejectsDuplicateAccessTokenAcrossSlots(t *testing.T) {
	sw := setupTestSwitcher(t)

	setLiveAccount(t, sw, "first@example.com", "org-first", "First", "token-shared")
	if err := sw.Add(0); err != nil {
		t.Fatalf("add first account: %v", err)
	}

	setLiveAccount(t, sw, "second@example.com", "org-second", "Second", "token-shared")
	err := sw.Add(0)
	if err == nil {
		t.Fatal("expected add to reject duplicate access token")
	}
	if !strings.Contains(err.Error(), "credentials match slot") {
		t.Fatalf("expected duplicate-token error, got %v", err)
	}

	seq, readErr := sw.readSequence()
	if readErr != nil {
		t.Fatalf("read sequence: %v", readErr)
	}
	if len(seq.Accounts) != 1 {
		t.Fatalf("expected second slot to be rejected, got %d accounts", len(seq.Accounts))
	}
}

func TestSwitchFailsWhenCurrentIdentityCannotBeResolvedByUUID(t *testing.T) {
	sw := setupTestSwitcher(t)

	setLiveAccount(t, sw, "unmanaged@example.com", "org-live", "Live", "token-live-unmanaged")
	if err := sw.store.WriteAccountBackup("1", "a@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "a@example.com",
			"organizationUuid": "org-a",
			"accountUuid":      "acc-a",
		},
	}), `{"claudeAiOauth":{"accessToken":"token-a-backup"}}`); err != nil {
		t.Fatalf("write backup 1: %v", err)
	}
	if err := sw.store.WriteAccountBackup("2", "b@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "b@example.com",
			"organizationUuid": "org-b",
			"accountUuid":      "acc-b",
		},
	}), `{"claudeAiOauth":{"accessToken":"token-b-backup"}}`); err != nil {
		t.Fatalf("write backup 2: %v", err)
	}
	seq := &domain.SequenceData{
		Sequence:            []int{1, 2},
		ActiveAccountNumber: intPtr(1), // stale fallback points to slot 1
		Accounts: map[string]domain.Account{
			"1": {Email: "a@example.com", OrganizationUUID: "org-a", UUID: "acc-a"},
			"2": {Email: "b@example.com", OrganizationUUID: "org-b", UUID: "acc-b"},
		},
	}
	if err := sw.store.WriteSequence(seq); err != nil {
		t.Fatalf("write sequence: %v", err)
	}

	err := sw.SwitchTo("2")
	if err == nil {
		t.Fatal("expected switch-to to fail when current identity is not managed by uuid")
	}
	if !strings.Contains(err.Error(), "cannot resolve current managed slot by uuid identity") {
		t.Fatalf("unexpected error: %v", err)
	}

	_, creds, err := sw.store.ReadAccountBackup("1", "a@example.com")
	if err != nil {
		t.Fatalf("read backup 1: %v", err)
	}
	if !strings.Contains(creds, "token-a-backup") {
		t.Fatalf("expected slot 1 backup to stay unchanged, got %s", creds)
	}
}

func TestReadSequenceHydratesFingerprintMappings(t *testing.T) {
	sw := setupTestSwitcher(t)
	seq := &domain.SequenceData{
		Sequence: []int{1},
		Accounts: map[string]domain.Account{
			"1": {Email: "one@example.com", OrganizationUUID: "org-1", UUID: "acc-1"},
		},
	}
	if err := sw.store.WriteSequence(seq); err != nil {
		t.Fatalf("write sequence: %v", err)
	}
	got, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	if got.Accounts["1"].Fingerprint == "" {
		t.Fatal("expected hydrated fingerprint")
	}
	if got.SlotFingerprints["1"] == "" {
		t.Fatal("expected hydrated slot mapping")
	}
}

func TestRepairRebuildsActiveSlotBackup(t *testing.T) {
	sw := setupTestSwitcher(t)
	setLiveAccount(t, sw, "live@example.com", "org-live", "Live", "token-live")
	if err := sw.store.WriteAccountBackup("1", "live@example.com", mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     "live@example.com",
			"organizationUuid": "org-live",
			"accountUuid":      "acc-org-live",
		},
	}), `{"claudeAiOauth":{"accessToken":"token-old"}}`); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := sw.store.WriteSequence(&domain.SequenceData{
		Sequence:            []int{1},
		ActiveAccountNumber: intPtr(1),
		Accounts: map[string]domain.Account{
			"1": {Email: "live@example.com", OrganizationUUID: "org-live", UUID: "acc-org-live"},
		},
	}); err != nil {
		t.Fatalf("write seq: %v", err)
	}
	if err := sw.Repair(); err != nil {
		t.Fatalf("repair: %v", err)
	}
	_, creds, err := sw.store.ReadAccountBackup("1", "live@example.com")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(creds, "token-live") {
		t.Fatalf("expected repaired live token, got %s", creds)
	}
}

func TestRepairReconcilesFingerprintsToMetadata(t *testing.T) {
	sw := setupTestSwitcher(t)
	const (
		email = "user@example.com"
		org   = "b482de13-49cb-4d81-9510-8b9c89f1948d"
		acc   = "1c5ac151-6d1a-489f-8da1-84ee1514c0eb"
	)
	wrongFP := "uuid-org:" + acc + "|3501f6ef-b332-499f-b151-fbd836cf4b5c"
	wantFP := domain.AccountFingerprint(acc, org, email)

	cfg := mustJSON(t, map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     email,
			"organizationUuid": org,
			"accountUuid":      acc,
		},
	})
	if err := sw.store.WriteAccountBackup("1", email, cfg, `{"claudeAiOauth":{"accessToken":"tok"}}`); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := sw.store.WriteSequence(&domain.SequenceData{
		Sequence:            []int{1},
		ActiveAccountNumber: intPtr(1),
		Accounts: map[string]domain.Account{
			"1": {
				Email:            email,
				UUID:             acc,
				OrganizationUUID: org,
				OrganizationName: "Org",
				Fingerprint:      wrongFP,
				Added:            "2026-01-01T00:00:00Z",
			},
		},
		SlotFingerprints: map[string]string{"1": wrongFP},
	}); err != nil {
		t.Fatalf("write seq: %v", err)
	}

	if err := sw.Repair(); err != nil {
		t.Fatalf("repair: %v", err)
	}
	seq, err := sw.readSequence()
	if err != nil {
		t.Fatalf("read sequence: %v", err)
	}
	acc1 := seq.Accounts["1"]
	if acc1.Fingerprint != wantFP {
		t.Fatalf("account fingerprint: got %q want %q", acc1.Fingerprint, wantFP)
	}
	if seq.SlotFingerprints["1"] != wantFP {
		t.Fatalf("slotFingerprints: got %q want %q", seq.SlotFingerprints["1"], wantFP)
	}
}

func setLiveAccount(t *testing.T, sw *Switcher, email, orgUUID, orgName, accessToken string) {
	t.Helper()
	liveCfgPath := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), ".claude.json")
	cfg := map[string]any{
		"oauthAccount": map[string]any{
			"emailAddress":     email,
			"organizationUuid": orgUUID,
			"organizationName": orgName,
			"accountUuid":      "acc-" + orgUUID,
		},
		"theme": "dark",
	}
	if err := sw.store.WriteLiveConfig(liveCfgPath, cfg); err != nil {
		t.Fatalf("write live config: %v", err)
	}
	creds := `{"claudeAiOauth":{"accessToken":"` + accessToken + `","refreshToken":"r","expiresAt":9999999999999}}`
	if err := sw.store.WriteLiveCredentials(creds); err != nil {
		t.Fatalf("write live credentials: %v", err)
	}
}

func intPtr(v int) *int { return &v }

func mustJSON(t *testing.T, v map[string]any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(out)
}

func captureStdout(run func() error) (string, error) {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	os.Stdout = w
	runErr := run()
	_ = w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	return buf.String(), runErr
}
