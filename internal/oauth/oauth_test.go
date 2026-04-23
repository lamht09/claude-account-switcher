package oauth

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestExtractAccessToken(t *testing.T) {
	token := ExtractAccessToken(`{"claudeAiOauth":{"accessToken":"abc"}}`)
	if token != "abc" {
		t.Fatalf("unexpected token: %q", token)
	}
}

func TestBuildTokenStatusUnknownExpiry(t *testing.T) {
	status := BuildTokenStatus(`{"claudeAiOauth":{"refreshToken":"r1"}}`)
	if status == "" {
		t.Fatal("expected token status")
	}
}

func TestBuildUsageResult(t *testing.T) {
	raw := map[string]any{
		"five_hour": map[string]any{
			"utilization": 42.0,
			"resets_at":   time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
		"seven_day": map[string]any{
			"utilization": 13.0,
		},
	}
	got := buildUsageResult(raw)
	if got == nil || got.FiveHour == nil || got.SevenDay == nil {
		t.Fatalf("unexpected usage result: %#v", got)
	}
	if got.FiveHour.Pct != 42 {
		t.Fatalf("unexpected 5h pct: %v", got.FiveHour.Pct)
	}
	if got.SevenDay.Pct != 13 {
		t.Fatalf("unexpected 7d pct: %v", got.SevenDay.Pct)
	}
}

func TestHumanDuration(t *testing.T) {
	if humanDuration(30*time.Minute) != "30m" {
		t.Fatalf("unexpected short duration")
	}
	if humanDuration(2*time.Hour+15*time.Minute) != "2h 15m" {
		t.Fatalf("unexpected hour duration")
	}
	if humanDuration(26*time.Hour) != "1d 2h" {
		t.Fatalf("unexpected day duration")
	}
}

func TestFetchUsageForAccountActiveDoesNotRefreshProactively(t *testing.T) {
	var refreshCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			atomic.AddInt32(&refreshCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "new-token",
				"expires_in":    3600,
				"refresh_token": "r2",
			})
		case "/usage":
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "unauthorized"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	origTokenURL := tokenURL
	origUsageURL := usageURL
	tokenURL = server.URL + "/token"
	usageURL = server.URL + "/usage"
	defer func() {
		tokenURL = origTokenURL
		usageURL = origUsageURL
	}()

	creds := `{"claudeAiOauth":{"accessToken":"expired","refreshToken":"r1","expiresAt":1}}`
	usage, updated, changed := FetchUsageForAccount(creds, true)
	if usage != nil {
		t.Fatalf("expected no usage for active account, got %#v", usage)
	}
	if changed {
		t.Fatalf("active account should not refresh proactively; updated=%s", updated)
	}
	if atomic.LoadInt32(&refreshCalls) != 0 {
		t.Fatalf("active account should not hit refresh endpoint, got %d calls", refreshCalls)
	}
}

func TestOAuthRequestsUseSourceUserAgent(t *testing.T) {
	var tokenUA string
	var usageUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenUA = r.Header.Get("User-Agent")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "new-token",
				"expires_in":    3600,
				"refresh_token": "r2",
			})
		case "/usage":
			usageUA = r.Header.Get("User-Agent")
			_, _ = io.WriteString(w, `{"five_hour":{"utilization":1}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	origTokenURL := tokenURL
	origUsageURL := usageURL
	tokenURL = server.URL + "/token"
	usageURL = server.URL + "/usage"
	defer func() {
		tokenURL = origTokenURL
		usageURL = origUsageURL
	}()

	creds := `{"claudeAiOauth":{"accessToken":"expired","refreshToken":"r1","expiresAt":1}}`
	_, _, _ = FetchUsageForAccount(creds, false)
	if tokenUA != "claude-swap/1.0" {
		t.Fatalf("unexpected token request user-agent: %q", tokenUA)
	}
	if usageUA != "claude-swap/1.0" {
		t.Fatalf("unexpected usage request user-agent: %q", usageUA)
	}
}
