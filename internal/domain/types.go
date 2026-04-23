package domain

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"time"
)

var (
	ErrConfigNotFound    = errors.New("no active Claude account found")
	ErrManagedNotFound   = errors.New("no accounts are managed yet")
	ErrAccountNotFound   = errors.New("account not found")
	ErrAmbiguousIdentity = errors.New("email is ambiguous, use account number")
	ErrInvalidIdentity   = errors.New("account identity is invalid")
)

type Platform string

const (
	PlatformMacOS   Platform = "macos"
	PlatformLinux   Platform = "linux"
	PlatformWSL     Platform = "wsl"
	PlatformWindows Platform = "windows"
	PlatformUnknown Platform = "unknown"
)

var runtimeGOOS = runtime.GOOS
var lookupEnv = os.Getenv
var readFile = os.ReadFile

func DetectPlatform() Platform {
	switch runtimeGOOS {
	case "darwin":
		return PlatformMacOS
	case "linux":
		if lookupEnv("WSL_DISTRO_NAME") != "" || lookupEnv("WSL_INTEROP") != "" {
			return PlatformWSL
		}
		raw, err := readFile("/proc/version")
		if err == nil {
			lowered := strings.ToLower(string(raw))
			if strings.Contains(lowered, "microsoft") || strings.Contains(lowered, "wsl") {
				return PlatformWSL
			}
		}
		return PlatformLinux
	case "windows":
		return PlatformWindows
	default:
		return PlatformUnknown
	}
}

type Account struct {
	Email            string `json:"email"`
	UUID             string `json:"uuid"`
	OrganizationUUID string `json:"organizationUuid"`
	OrganizationName string `json:"organizationName"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	Added            string `json:"added"`
}

type SequenceData struct {
	ActiveAccountNumber *int               `json:"activeAccountNumber"`
	LastUpdated         string             `json:"lastUpdated"`
	Sequence            []int              `json:"sequence"`
	Accounts            map[string]Account `json:"accounts"`
	SlotFingerprints    map[string]string  `json:"slotFingerprints,omitempty"`
}

func TimestampNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func IsValidIdentity(accountUUID, organizationUUID string) bool {
	_ = organizationUUID
	return strings.TrimSpace(accountUUID) != ""
}

func IdentityKey(accountUUID, organizationUUID string) string {
	uuid := strings.TrimSpace(accountUUID)
	org := strings.TrimSpace(organizationUUID)
	if uuid == "" {
		return ""
	}
	if org == "" {
		return "uuid:" + uuid
	}
	return "uuid-org:" + uuid + "|" + org
}

func AccountFingerprint(accountUUID, organizationUUID, _ string) string {
	return IdentityKey(accountUUID, organizationUUID)
}
