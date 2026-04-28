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

// ManagedIdentityMatch reports whether a stored managed row refers to the same
// logical account as the live Claude identity (email, organizationUuid, accountUuid).
//
// Matching rules (in order of evaluation):
//  1. Strict (uuid + organizationUuid): when both stored and live have non-empty
//     organization UUIDs, require equal account UUIDs and equal organization UUIDs.
//  2. Legacy (stored org missing): when stored organizationUuid is empty but stored
//     account UUID equals live account UUID, treat as the same row (aligns with
//     IdentityKey "uuid:<uuid>" fingerprints from older data).
//  3. Fallback (email + organizationUuid): when stored account UUID is empty,
//     match on NormalizeEmail(stored email) == NormalizeEmail(live email) and equal
//     non-empty organization UUIDs on both sides.
//  4. Otherwise, including same UUID with both orgs set but different values: false.
//     When live organizationUuid is empty but stored org is non-empty, returns false.
func ManagedIdentityMatch(stored Account, liveEmail, liveOrg, liveUUID string) bool {
	su := strings.TrimSpace(stored.UUID)
	so := strings.TrimSpace(stored.OrganizationUUID)
	lu := strings.TrimSpace(liveUUID)
	lo := strings.TrimSpace(liveOrg)

	if su == "" {
		if so == "" || lo == "" {
			return false
		}
		return NormalizeEmail(stored.Email) == NormalizeEmail(liveEmail) && so == lo
	}
	if su != lu {
		return false
	}
	if so == "" {
		return true
	}
	if lo == "" {
		return false
	}
	return so == lo
}
