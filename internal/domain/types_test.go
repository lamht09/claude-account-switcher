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

func TestManagedIdentityMatch(t *testing.T) {
	tests := []struct {
		name     string
		stored   Account
		liveMail string
		liveOrg  string
		liveUUID string
		want     bool
	}{
		{
			name:     "strict_same_uuid_and_org",
			stored:   Account{Email: "a@x.com", UUID: "u1", OrganizationUUID: "o1"},
			liveMail: "a@x.com",
			liveOrg:  "o1",
			liveUUID: "u1",
			want:     true,
		},
		{
			name:     "strict_same_uuid_different_org",
			stored:   Account{Email: "a@x.com", UUID: "u1", OrganizationUUID: "o1"},
			liveMail: "a@x.com",
			liveOrg:  "o2",
			liveUUID: "u1",
			want:     false,
		},
		{
			name:     "strict_email_case_insensitive_irrelevant_when_uuid_matches",
			stored:   Account{Email: "A@X.com", UUID: "u1", OrganizationUUID: "o1"},
			liveMail: "a@x.com",
			liveOrg:  "o1",
			liveUUID: "u1",
			want:     true,
		},
		{
			name:     "legacy_stored_org_empty_same_uuid",
			stored:   Account{Email: "a@x.com", UUID: "u1", OrganizationUUID: ""},
			liveMail: "a@x.com",
			liveOrg:  "o-any",
			liveUUID: "u1",
			want:     true,
		},
		{
			name:     "legacy_stored_org_empty_uuid_mismatch",
			stored:   Account{Email: "a@x.com", UUID: "u1", OrganizationUUID: ""},
			liveMail: "a@x.com",
			liveOrg:  "o1",
			liveUUID: "u2",
			want:     false,
		},
		{
			name:     "fallback_email_org_stored_uuid_empty_live_uuid_present",
			stored:   Account{Email: "User@X.com", UUID: "", OrganizationUUID: "o1"},
			liveMail: "user@x.com",
			liveOrg:  "o1",
			liveUUID: "any-live-uuid",
			want:     true,
		},
		{
			name:     "fallback_email_org_stored_uuid_empty_live_uuid_empty",
			stored:   Account{Email: "user@x.com", UUID: "", OrganizationUUID: "o1"},
			liveMail: "user@x.com",
			liveOrg:  "o1",
			liveUUID: "",
			want:     true,
		},
		{
			name:     "fallback_org_mismatch",
			stored:   Account{Email: "a@x.com", UUID: "", OrganizationUUID: "o1"},
			liveMail: "a@x.com",
			liveOrg:  "o2",
			liveUUID: "",
			want:     false,
		},
		{
			name:     "live_org_empty_stored_org_set_same_uuid",
			stored:   Account{Email: "a@x.com", UUID: "u1", OrganizationUUID: "o1"},
			liveMail: "a@x.com",
			liveOrg:  "",
			liveUUID: "u1",
			want:     false,
		},
		{
			name:     "both_org_empty_same_uuid",
			stored:   Account{Email: "a@x.com", UUID: "u1", OrganizationUUID: ""},
			liveMail: "a@x.com",
			liveOrg:  "",
			liveUUID: "u1",
			want:     true,
		},
		{
			name:     "trim_spaces_uuid_org",
			stored:   Account{Email: "a@x.com", UUID: "  u1  ", OrganizationUUID: "  o1  "},
			liveMail: "a@x.com",
			liveOrg:  "o1",
			liveUUID: "u1",
			want:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ManagedIdentityMatch(tt.stored, tt.liveMail, tt.liveOrg, tt.liveUUID); got != tt.want {
				t.Fatalf("ManagedIdentityMatch(...) = %v, want %v", got, tt.want)
			}
		})
	}
}
