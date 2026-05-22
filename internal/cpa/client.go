package cpa

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

type AuthFile struct {
	Name           string         `json:"name"`
	AuthIndex      any            `json:"auth_index"`
	Disabled       bool           `json:"disabled"`
	Status         string         `json:"status"`
	StatusMessage  string         `json:"status_message"`
	Priority       int            `json:"priority"`
	NextRetryAfter *time.Time     `json:"next_retry_after,omitempty"`
	LastRefresh    *time.Time     `json:"last_refresh,omitempty"`
	Account        string         `json:"account,omitempty"`
	AccountType    string         `json:"account_type,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Unavailable    bool           `json:"unavailable,omitempty"`
	Failed         int            `json:"failed,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
	IDToken        map[string]any `json:"id_token,omitempty"`
}

func New(baseURL, key string, timeoutSeconds int) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key, HTTP: &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}}
}

func (c *Client) do(method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

func (c *Client) GetAuthFiles() ([]AuthFile, error) {
	resp, err := c.do(http.MethodGet, "/v0/management/auth-files", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get auth-files failed: %s", string(b))
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	items := make([]AuthFile, 0)
	var rawItems []any
	if raw, ok := data["files"].([]any); ok {
		rawItems = raw
	} else if raw, ok := data["auth_files"].([]any); ok {
		rawItems = raw
	}
	for _, item := range rawItems {
		b, _ := json.Marshal(item)
		var af AuthFile
		if err := json.Unmarshal(b, &af); err == nil {
			items = append(items, af)
		}
	}
	return items, nil
}

func (c *Client) DeleteAuthFile(name string) error {
	q := url.QueryEscape(name)
	resp, err := c.do(http.MethodDelete, "/v0/management/auth-files?name="+q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete auth-file failed: %s", string(b))
	}
	return nil
}

func (c *Client) PatchAuthFileFields(name string, priority *int, note *string) error {
	payload := map[string]any{"name": name}
	if priority != nil {
		payload["priority"] = *priority
	}
	if note != nil {
		payload["note"] = *note
	}
	resp, err := c.do(http.MethodPatch, "/v0/management/auth-files/fields", payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("patch auth-file fields failed: %s", string(b))
	}
	return nil
}

func (c *Client) PatchAuthFileStatus(name string, disabled bool) error {
	payload := map[string]any{"name": name, "disabled": disabled}
	resp, err := c.do(http.MethodPatch, "/v0/management/auth-files/status", payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("patch auth-file status failed: %s", string(b))
	}
	return nil
}

func (c *Client) GetLogs(after int64) ([]string, int64, error) {
	path := "/v0/management/logs"
	if after > 0 {
		path = fmt.Sprintf("%s?after=%d", path, after)
	}
	resp, err := c.do(http.MethodGet, path, nil)
	if err != nil {
		return nil, after, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, after, fmt.Errorf("get logs failed: %s", string(b))
	}
	var data struct {
		Lines           []string `json:"lines"`
		LatestTimestamp int64    `json:"latest-timestamp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, after, err
	}
	return data.Lines, data.LatestTimestamp, nil
}
