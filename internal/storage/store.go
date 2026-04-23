package storage

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/lamht09/claude-account-switcher/internal/domain"
	"github.com/lamht09/claude-account-switcher/internal/platform"
)

type Store struct {
	backupRoot     string
	sequencePath   string
	backupCfgDir   string
	backupCredsDir string
	platform       domain.Platform
}

const (
	keychainService       = "Claude Code-credentials"
	legacyKeychainAccount = "claude-account-switcher"
	backupKeyringService  = "claude-code"
	backupKeyringUserBase = "backup-"
	legacyBackupUserBase  = "account-"
)

var execCommand = exec.Command

func NewStore() *Store {
	root := platform.BackupRoot()
	return &Store{
		backupRoot:     root,
		sequencePath:   filepath.Join(root, "sequence.json"),
		backupCfgDir:   filepath.Join(root, "configs"),
		backupCredsDir: filepath.Join(root, "credentials"),
		platform:       domain.DetectPlatform(),
	}
}

func (s *Store) Paths() (backupRoot, sequencePath string) {
	return s.backupRoot, s.sequencePath
}

func (s *Store) EnsureDirs() error {
	for _, dir := range []string{s.backupRoot, s.backupCfgDir, s.backupCredsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ReadSequence() (*domain.SequenceData, error) {
	data, err := os.ReadFile(s.sequencePath)
	if err != nil {
		return nil, err
	}
	var seq domain.SequenceData
	if err := json.Unmarshal(data, &seq); err != nil {
		return nil, err
	}
	if seq.Accounts == nil {
		seq.Accounts = map[string]domain.Account{}
	}
	return &seq, nil
}

func (s *Store) InitSequenceIfMissing() error {
	if _, err := os.Stat(s.sequencePath); err == nil {
		return nil
	}
	seq := domain.SequenceData{
		ActiveAccountNumber: nil,
		LastUpdated:         domain.TimestampNow(),
		Sequence:            []int{},
		Accounts:            map[string]domain.Account{},
	}
	return s.WriteSequence(&seq)
}

func writeAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (s *Store) WriteSequence(seq *domain.SequenceData) error {
	seq.LastUpdated = domain.TimestampNow()
	if seq.Accounts == nil {
		seq.Accounts = map[string]domain.Account{}
	}
	body, err := json.MarshalIndent(seq, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.sequencePath, body, 0o600)
}

func (s *Store) NextAccountNumber(seq *domain.SequenceData) int {
	maxVal := 0
	for key := range seq.Accounts {
		if n, err := strconv.Atoi(key); err == nil && n > maxVal {
			maxVal = n
		}
	}
	return maxVal + 1
}

func (s *Store) ResolveAccount(identifier string, seq *domain.SequenceData) (string, error) {
	if _, err := strconv.Atoi(identifier); err == nil {
		if _, ok := seq.Accounts[identifier]; ok {
			return identifier, nil
		}
		return "", domain.ErrAccountNotFound
	}
	matches := []string{}
	for n, acc := range seq.Accounts {
		if acc.Email == identifier {
			matches = append(matches, n)
		}
	}
	if len(matches) == 0 {
		return "", domain.ErrAccountNotFound
	}
	if len(matches) > 1 {
		return "", domain.ErrAmbiguousIdentity
	}
	return matches[0], nil
}

func (s *Store) ReadLiveConfig() (map[string]any, string, error) {
	path := platform.GlobalConfigPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	return out, path, nil
}

func (s *Store) WriteLiveConfig(path string, data map[string]any) error {
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, body, 0o600)
}

func (s *Store) ReadLiveCredentials() (string, error) {
	if s.platform == domain.PlatformMacOS && !s.forceFileCredentialBackend() {
		account := strings.TrimSpace(os.Getenv("USER"))
		if account == "" {
			account = "user"
		}
		cmd := execCommand("security", "find-generic-password", "-s", keychainService, "-a", account, "-w")
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		// Fallback for environments where the account field is unknown.
		serviceLookup := execCommand("security", "find-generic-password", "-s", keychainService, "-w")
		out, serviceErr := serviceLookup.Output()
		if serviceErr == nil {
			return strings.TrimSpace(string(out)), nil
		}
		// Backward compatibility for legacy Go-specific account naming.
		fallback := execCommand("security", "find-generic-password", "-s", keychainService, "-a", legacyKeychainAccount, "-w")
		out, fallbackErr := fallback.Output()
		if fallbackErr != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
	raw, err := os.ReadFile(platform.LiveCredentialPath())
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (s *Store) WriteLiveCredentials(credentials string) error {
	if s.platform == domain.PlatformMacOS && !s.forceFileCredentialBackend() {
		normalized := strings.TrimSpace(credentials)
		if normalized == "" {
			return fmt.Errorf("credentials cannot be empty")
		}
		account := strings.TrimSpace(os.Getenv("USER"))
		if account == "" {
			account = "user"
		}
		cmd := execCommand(
			"security", "add-generic-password", "-U", "-s", keychainService,
			"-a", account, "-w", normalized,
		)
		return cmd.Run()
	}
	if err := os.MkdirAll(platform.ClaudeConfigHome(), 0o700); err != nil {
		return err
	}
	return writeAtomic(platform.LiveCredentialPath(), []byte(credentials), 0o600)
}

func (s *Store) backupConfigPath(number, email string) string {
	return filepath.Join(s.backupCfgDir, fmt.Sprintf(".claude-config-%s-%s.json", number, email))
}

func (s *Store) backupCredentialPath(number, email string) string {
	return filepath.Join(s.backupCredsDir, fmt.Sprintf(".creds-%s-%s.enc", number, email))
}

func (s *Store) backupCredentialPathByFingerprint(fingerprint string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", "|", "_").Replace(fingerprint)
	return filepath.Join(s.backupCredsDir, fmt.Sprintf(".creds-%s.enc", safe))
}

func (s *Store) WriteAccountBackup(number, email, cfgRaw, credentials string) error {
	cfgPath := s.backupConfigPath(number, email)
	if err := writeAtomic(cfgPath, []byte(cfgRaw), 0o600); err != nil {
		return err
	}
	fingerprint := s.fingerprintFromBackup(cfgRaw, email)
	if fingerprint == "" {
		return fmt.Errorf("%w: cannot determine fingerprint for slot %s", domain.ErrInvalidIdentity, number)
	}
	if s.usesBackupCredentialsFile() {
		enc := base64.StdEncoding.EncodeToString([]byte(credentials))
		return writeAtomic(s.backupCredentialPathByFingerprint(fingerprint), []byte(enc), 0o600)
	}
	return s.writeBackupCredentialToKeyring(fingerprint, credentials)
}

func (s *Store) ReadAccountBackup(number, email string) (cfgRaw, credentials string, err error) {
	cfg, err := os.ReadFile(s.backupConfigPath(number, email))
	if err != nil {
		return "", "", err
	}
	fingerprint := s.fingerprintFromBackup(string(cfg), email)
	if fingerprint == "" {
		return "", "", fmt.Errorf("%w: slot %s has incomplete identity", domain.ErrInvalidIdentity, number)
	}
	if s.usesBackupCredentialsFile() {
		enc, credErr := os.ReadFile(s.backupCredentialPathByFingerprint(fingerprint))
		if credErr == nil {
			plain, decodeErr := base64.StdEncoding.DecodeString(string(enc))
			if decodeErr != nil {
				return "", "", decodeErr
			}
			return string(cfg), string(plain), nil
		}
		enc, credErr = os.ReadFile(s.backupCredentialPath(number, email))
		if credErr != nil {
			return "", "", credErr
		}
		plain, decodeErr := base64.StdEncoding.DecodeString(string(enc))
		if decodeErr != nil {
			return "", "", decodeErr
		}
		decoded := string(plain)
		if decoded != "" {
			_ = writeAtomic(s.backupCredentialPathByFingerprint(fingerprint), []byte(enc), 0o600)
		}
		return string(cfg), string(plain), nil
	}

	creds, credErr := s.readBackupCredentialFromKeyring(fingerprint)
	if credErr == nil && creds != "" {
		return string(cfg), creds, nil
	}
	legacyCreds, legacyErr := s.readLegacyBackupCredentialFromKeyring(number, email)
	if legacyErr == nil && legacyCreds != "" {
		_ = s.writeBackupCredentialToKeyring(fingerprint, legacyCreds)
		return string(cfg), legacyCreds, nil
	}
	enc, fileErr := os.ReadFile(s.backupCredentialPath(number, email))
	if fileErr != nil {
		if credErr != nil {
			return "", "", credErr
		}
		if legacyErr != nil {
			return "", "", legacyErr
		}
		return "", "", fileErr
	}
	plain, err := base64.StdEncoding.DecodeString(string(enc))
	if err != nil {
		return "", "", err
	}
	decoded := string(plain)
	// Best-effort migration: keep old file as rollback source.
	if decoded != "" {
		_ = s.writeBackupCredentialToKeyring(fingerprint, decoded)
	}
	return string(cfg), decoded, nil
}

func (s *Store) DeleteAccountBackup(number, email string, keepCredential bool) {
	cfgRaw := ""
	if raw, err := os.ReadFile(s.backupConfigPath(number, email)); err == nil {
		cfgRaw = string(raw)
	}
	fingerprint := s.fingerprintFromBackup(cfgRaw, email)
	_ = os.Remove(s.backupConfigPath(number, email))
	if keepCredential {
		return
	}
	if fingerprint != "" && s.usesBackupCredentialsFile() {
			_ = os.Remove(s.backupCredentialPathByFingerprint(fingerprint))
		_ = os.Remove(s.backupCredentialPath(number, email))
		return
	}
	if fingerprint != "" {
		_ = s.deleteBackupCredentialFromKeyring(fingerprint)
	}
	_ = s.deleteLegacyBackupCredentialFromKeyring(number, email)
	// Keep file cleanup to remove pre-migration leftovers.
	_ = os.Remove(s.backupCredentialPath(number, email))
}

func (s *Store) Purge() error {
	return os.RemoveAll(s.backupRoot)
}

func (s *Store) SortedSequence(seq *domain.SequenceData) {
	sort.Ints(seq.Sequence)
}

func (s *Store) usesBackupCredentialsFile() bool {
	// Allows CI/tests (especially macOS runners) to bypass interactive keyring backends.
	if s.forceFileCredentialBackend() {
		return true
	}
	return s.platform == domain.PlatformLinux || s.platform == domain.PlatformWSL || s.platform == domain.PlatformUnknown
}

func (s *Store) forceFileCredentialBackend() bool {
	return strings.TrimSpace(os.Getenv("CA_FORCE_FILE_BACKUP_CREDENTIALS")) == "1"
}

func (s *Store) backupCredentialKeyringUser(fingerprint string) string {
	return backupKeyringUserBase + fingerprint
}

func (s *Store) legacyBackupCredentialKeyringUser(number, email string) string {
	return legacyBackupUserBase + number + "-" + email
}

func (s *Store) readBackupCredentialFromKeyring(fingerprint string) (string, error) {
	return keyring.Get(backupKeyringService, s.backupCredentialKeyringUser(fingerprint))
}

func (s *Store) readLegacyBackupCredentialFromKeyring(number, email string) (string, error) {
	return keyring.Get(backupKeyringService, s.legacyBackupCredentialKeyringUser(number, email))
}

func (s *Store) writeBackupCredentialToKeyring(fingerprint, credentials string) error {
	return keyring.Set(backupKeyringService, s.backupCredentialKeyringUser(fingerprint), credentials)
}

func (s *Store) deleteBackupCredentialFromKeyring(fingerprint string) error {
	return keyring.Delete(backupKeyringService, s.backupCredentialKeyringUser(fingerprint))
}

func (s *Store) deleteLegacyBackupCredentialFromKeyring(number, email string) error {
	return keyring.Delete(backupKeyringService, s.legacyBackupCredentialKeyringUser(number, email))
}

func (s *Store) fingerprintFromBackup(cfgRaw, email string) string {
	accountUUID := ""
	orgUUID := ""
	if cfgRaw != "" {
		var cfg map[string]any
		if err := json.Unmarshal([]byte(cfgRaw), &cfg); err == nil {
			if oauthAccount, ok := cfg["oauthAccount"].(map[string]any); ok {
				if v, _ := oauthAccount["accountUuid"].(string); v != "" {
					accountUUID = v
				}
				if v, _ := oauthAccount["organizationUuid"].(string); v != "" {
					orgUUID = v
				}
				if v, _ := oauthAccount["emailAddress"].(string); v != "" {
					email = v
				}
			}
		}
	}
	return domain.AccountFingerprint(accountUUID, orgUUID, email)
}
