package oauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	betaHeader      = "oauth-2025-04-20"
	expiryBufferMS  = int64(5 * 60 * 1000)
	clientID        = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	defaultTimeoutS = 10
)

var (
	tokenURL = "https://platform.claude.com/v1/oauth/token"
	usageURL = "https://api.anthropic.com/api/oauth/usage"
)

type oauthEnvelope struct {
	ClaudeAiOauth map[string]any `json:"claudeAiOauth"`
}

func extractOAuthData(credentials string) map[string]any {
	var body oauthEnvelope
	if err := json.Unmarshal([]byte(credentials), &body); err != nil {
		return nil
	}
	return body.ClaudeAiOauth
}

type UsageWindow struct {
	Pct       float64
	Clock     string
	Countdown string
}

type UsageResult struct {
	FiveHour *UsageWindow
	SevenDay *UsageWindow
}

func ExtractAccessToken(credentials string) string {
	var body oauthEnvelope
	if err := json.Unmarshal([]byte(credentials), &body); err != nil {
		return ""
	}
	token, _ := body.ClaudeAiOauth["accessToken"].(string)
	return token
}

func IsExpired(expiresAt any) bool {
	val, ok := expiresAt.(float64)
	if !ok {
		return false
	}
	now := time.Now().UTC().UnixMilli()
	return now+expiryBufferMS >= int64(val)
}

func BuildTokenStatus(credentials string) string {
	var body oauthEnvelope
	if err := json.Unmarshal([]byte(credentials), &body); err != nil {
		return ""
	}
	oauth := body.ClaudeAiOauth
	if oauth == nil {
		return ""
	}
	refreshToken, _ := oauth["refreshToken"].(string)
	hasRefresh := "no"
	if refreshToken != "" {
		hasRefresh = "yes"
	}
	expiresAt, ok := oauth["expiresAt"].(float64)
	if !ok {
		return fmt.Sprintf("oauth: unknown expiry, refresh token %s", hasRefresh)
	}
	expiry := time.UnixMilli(int64(expiresAt)).UTC()
	state := "fresh"
	if IsExpired(expiresAt) {
		state = "expired"
	}
	remaining := time.Until(expiry)
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf(
		"oauth: %s, refresh token %s, expires %s in %s",
		state,
		hasRefresh,
		expiry.Local().Format("Jan 2 15:04"),
		humanDuration(remaining),
	)
}

func RefreshCredentials(credentials string) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(credentials), &payload); err != nil {
		return "", err
	}
	oauth, ok := payload["claudeAiOauth"].(map[string]any)
	if !ok {
		return "", errors.New("missing oauth payload")
	}
	refreshToken, _ := oauth["refreshToken"].(string)
	if refreshToken == "" {
		return "", errors.New("missing refresh token")
	}

	reqBody, _ := json.Marshal(map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     clientID,
	})
	req, _ := http.NewRequest(http.MethodPost, tokenURL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-swap/1.0")
	client := &http.Client{Timeout: defaultTimeoutS * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", errors.New("refresh request failed")
	}
	raw, _ := io.ReadAll(resp.Body)
	var tokenResp map[string]any
	if err := json.Unmarshal(raw, &tokenResp); err != nil {
		return "", err
	}
	oauth["accessToken"], _ = tokenResp["access_token"]
	if expiresIn, ok := tokenResp["expires_in"].(float64); ok {
		oauth["expiresAt"] = time.Now().UTC().UnixMilli() + int64(expiresIn*1000)
	}
	if rt, ok := tokenResp["refresh_token"]; ok {
		oauth["refreshToken"] = rt
	}
	payload["claudeAiOauth"] = oauth
	out, _ := json.Marshal(payload)
	return string(out), nil
}

func RefreshCredentialsIfNeeded(credentials string) (string, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(credentials), &payload); err != nil {
		return "", false, err
	}
	oauth, ok := payload["claudeAiOauth"].(map[string]any)
	if !ok {
		return "", false, errors.New("missing oauth payload")
	}
	if !IsExpired(oauth["expiresAt"]) {
		return credentials, false, nil
	}
	refreshed, err := RefreshCredentials(credentials)
	if err != nil {
		return "", false, err
	}
	return refreshed, true, nil
}

func FetchUsage(accessToken string) (map[string]any, int, error) {
	req, _ := http.NewRequest(http.MethodGet, usageURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", betaHeader)
	req.Header.Set("User-Agent", "claude-swap/1.0")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, resp.StatusCode, errors.New("usage request failed")
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}

func FetchUsageForAccount(credentials string, isActive bool) (*UsageResult, string, bool) {
	workingCreds := credentials
	oauthData := extractOAuthData(workingCreds)

	// Source parity: proactive refresh is only for inactive accounts.
	if !isActive && oauthData != nil {
		if refreshToken, _ := oauthData["refreshToken"].(string); refreshToken != "" && IsExpired(oauthData["expiresAt"]) {
			if refreshed, err := RefreshCredentials(workingCreds); err == nil {
				workingCreds = refreshed
				oauthData = extractOAuthData(workingCreds)
			}
		}
	}

	accessToken := ExtractAccessToken(workingCreds)
	if accessToken == "" {
		return nil, workingCreds, workingCreds != credentials
	}
	raw, code, err := FetchUsage(accessToken)
	if err == nil {
		return buildUsageResult(raw), workingCreds, workingCreds != credentials
	}
	if code == http.StatusUnauthorized && !isActive && oauthData != nil {
		if refreshToken, _ := oauthData["refreshToken"].(string); refreshToken != "" {
			refreshed, refreshErr := RefreshCredentials(workingCreds)
			if refreshErr == nil {
				workingCreds = refreshed
				accessToken = ExtractAccessToken(workingCreds)
				if accessToken != "" {
					raw, _, err = FetchUsage(accessToken)
					if err == nil {
						return buildUsageResult(raw), workingCreds, workingCreds != credentials
					}
				}
			}
		}
	}
	return nil, workingCreds, workingCreds != credentials
}

func humanDuration(d time.Duration) string {
	minutes := int(d.Minutes())
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	mins := minutes % 60
	if hours < 24 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	days := hours / 24
	hrs := hours % 24
	return fmt.Sprintf("%dd %dh", days, hrs)
}

func buildUsageResult(raw map[string]any) *UsageResult {
	if raw == nil {
		return nil
	}
	out := &UsageResult{}
	if w := parseUsageWindow(raw["five_hour"]); w != nil {
		out.FiveHour = w
	}
	if w := parseUsageWindow(raw["seven_day"]); w != nil {
		out.SevenDay = w
	}
	if out.FiveHour == nil && out.SevenDay == nil {
		return nil
	}
	return out
}

func parseUsageWindow(v any) *UsageWindow {
	window, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	utilization, ok := window["utilization"].(float64)
	if !ok {
		return nil
	}
	out := &UsageWindow{Pct: utilization}
	if resetsAt, _ := window["resets_at"].(string); resetsAt != "" {
		if t, err := time.Parse(time.RFC3339, resetsAt); err == nil {
			remaining := time.Until(t)
			if remaining < 0 {
				remaining = 0
			}
			out.Countdown = humanDuration(remaining)
			local := t.Local()
			nowLocal := time.Now().Local()
			if local.Year() == nowLocal.Year() && local.YearDay() == nowLocal.YearDay() {
				out.Clock = local.Format("15:04")
			} else {
				out.Clock = local.Format("Jan 2 15:04")
			}
		}
	}
	return out
}
