package cpa

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cpa-pool-manager/internal/types"
)

type apiCallRequest struct {
	AuthIndex string            `json:"authIndex,omitempty"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Header    map[string]string `json:"header,omitempty"`
	Data      string            `json:"data,omitempty"`
}

type apiCallResponse struct {
	StatusCode int                 `json:"status_code"`
	Header     map[string][]string `json:"header"`
	Body       string              `json:"body"`
}

type whamUsageResponse struct {
	AccountID string `json:"account_id"`
	Email     string `json:"email"`
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		Allowed       bool `json:"allowed"`
		LimitReached  bool `json:"limit_reached"`
		PrimaryWindow *struct {
			UsedPercent        int   `json:"used_percent"`
			LimitWindowSeconds int64 `json:"limit_window_seconds"`
			ResetAfterSeconds  int64 `json:"reset_after_seconds"`
			ResetAt            int64 `json:"reset_at"`
		} `json:"primary_window"`
	} `json:"rate_limit"`
}

type Explicit401Error struct {
	StatusCode int
	Code       string
	Message    string
	Body       string
}

func (e *Explicit401Error) Error() string {
	return fmt.Sprintf("explicit_401 status=%d code=%s message=%s", e.StatusCode, e.Code, e.Message)
}

func IsExplicit401Error(err error) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*Explicit401Error)
	return ok
}

func detectExplicit401(wrapped apiCallResponse) *Explicit401Error {
	if wrapped.StatusCode != http.StatusUnauthorized {
		if v := strings.Join(wrapped.Header["X-Openai-Authorization-Error"], ","); !strings.Contains(v, "401") {
			if v2 := strings.Join(wrapped.Header["X-Openai-Ide-Error-Code"], ","); !strings.Contains(strings.ToLower(v2), "token_invalidated") && !strings.Contains(strings.ToLower(wrapped.Body), "\"status\": 401") && !strings.Contains(strings.ToLower(wrapped.Body), "token_invalidated") && !strings.Contains(strings.ToLower(wrapped.Body), "authentication token has been invalidated") {
				return nil
			}
		}
	}
	msg := wrapped.Body
	code := strings.Join(wrapped.Header["X-Openai-Ide-Error-Code"], ",")
	bodyLower := strings.ToLower(wrapped.Body)
	if strings.Contains(bodyLower, "token_invalidated") && code == "" {
		code = "token_invalidated"
	}
	if strings.Contains(bodyLower, "authentication token has been invalidated") {
		msg = "authentication token invalidated"
	}
	return &Explicit401Error{StatusCode: wrapped.StatusCode, Code: code, Message: msg, Body: wrapped.Body}
}

func (c *Client) ProbeWhamUsage(authIndex string, accountID string) (types.QuotaInfo, error) {
	payload := apiCallRequest{
		AuthIndex: authIndex,
		Method:    http.MethodGet,
		URL:       "https://chatgpt.com/backend-api/wham/usage",
		Header: map[string]string{
			"Authorization":      "Bearer $TOKEN$",
			"Content-Type":       "application/json",
			"User-Agent":         "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal",
			"Chatgpt-Account-Id": accountID,
		},
	}
	resp, err := c.do(http.MethodPost, "/v0/management/api-call", payload)
	if err != nil {
		return types.QuotaInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return types.QuotaInfo{}, fmt.Errorf("api-call failed: %s", string(b))
	}
	var wrapped apiCallResponse
	if err := json.NewDecoder(resp.Body).Decode(&wrapped); err != nil {
		return types.QuotaInfo{}, err
	}
	if explicit401 := detectExplicit401(wrapped); explicit401 != nil {
		return types.QuotaInfo{}, explicit401
	}
	if wrapped.StatusCode >= 300 {
		return types.QuotaInfo{}, fmt.Errorf("wham usage upstream failed: status=%d body=%s", wrapped.StatusCode, wrapped.Body)
	}
	var parsed whamUsageResponse
	if err := json.Unmarshal([]byte(wrapped.Body), &parsed); err != nil {
		return types.QuotaInfo{}, err
	}
	out := types.QuotaInfo{
		AuthIndex:    authIndex,
		AccountID:    parsed.AccountID,
		Email:        parsed.Email,
		PlanType:     parsed.PlanType,
		Allowed:      parsed.RateLimit.Allowed,
		LimitReached: parsed.RateLimit.LimitReached,
		RawBody:      wrapped.Body,
	}
	if parsed.RateLimit.PrimaryWindow != nil {
		out.UsedPercent = parsed.RateLimit.PrimaryWindow.UsedPercent
		out.ResetAfterSeconds = parsed.RateLimit.PrimaryWindow.ResetAfterSeconds
		if parsed.RateLimit.PrimaryWindow.ResetAt > 0 {
			t := time.Unix(parsed.RateLimit.PrimaryWindow.ResetAt, 0).UTC()
			out.RefreshAt = &t
		}
	}
	return out, nil
}
