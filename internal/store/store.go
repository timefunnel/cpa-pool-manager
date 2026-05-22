package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"cpa-pool-manager/internal/types"
)

type Store struct {
	DB *sql.DB
}

type AccountQuotaState struct {
	AccountName   string
	AuthIndex     string
	AccountID     string
	RefreshAt     *time.Time
	LimitReached  bool
	Allowed       bool
	UsedPercent   int
	PlanType      string
	ProbedAt      time.Time
	UpdatedAt     time.Time
	LastSortKey   string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS proposals (id TEXT PRIMARY KEY, mode TEXT NOT NULL, created_at TEXT NOT NULL, items_json TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS account_snapshots (id TEXT PRIMARY KEY, name TEXT NOT NULL, payload_json TEXT NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS quota_cache (account_name TEXT PRIMARY KEY, auth_index TEXT NOT NULL, account_id TEXT NOT NULL, probed_at TEXT NOT NULL, payload_json TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS account_quota_state (account_name TEXT PRIMARY KEY, auth_index TEXT NOT NULL, account_id TEXT NOT NULL, refresh_at TEXT, limit_reached INTEGER NOT NULL, allowed INTEGER NOT NULL, used_percent INTEGER NOT NULL, plan_type TEXT NOT NULL, probed_at TEXT NOT NULL, updated_at TEXT NOT NULL, last_sort_key TEXT NOT NULL DEFAULT '');`,
	}
	for _, q := range ddl {
		if _, err := s.DB.Exec(q); err != nil {
			return err
		}
	}
	if _, err := s.DB.Exec(`ALTER TABLE account_quota_state ADD COLUMN last_sort_key TEXT NOT NULL DEFAULT '';`); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			return err
		}
	}
	return nil
}

func (s *Store) SaveProposal(p types.Proposal) error {
	b, err := json.Marshal(p.Items)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT OR REPLACE INTO proposals (id, mode, created_at, items_json) VALUES (?, ?, ?, ?)`, p.ID, p.Mode, p.CreatedAt.Format(time.RFC3339), string(b))
	return err
}

func (s *Store) ListProposals(limit, offset int) ([]types.Proposal, error) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.Query(`SELECT id, mode, created_at, items_json FROM proposals WHERE json_array_length(items_json) > 0 ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Proposal
	for rows.Next() {
		var id, mode, createdAt, itemsJSON string
		if err := rows.Scan(&id, &mode, &createdAt, &itemsJSON); err != nil {
			return nil, err
		}
		var items []types.ProposalItem
		_ = json.Unmarshal([]byte(itemsJSON), &items)
		t, _ := time.Parse(time.RFC3339, createdAt)
		out = append(out, types.Proposal{ID: id, Mode: mode, CreatedAt: t, Items: items})
	}
	return out, rows.Err()
}

func (s *Store) CountProposals() (int, error) {
	row := s.DB.QueryRow(`SELECT COUNT(*) FROM proposals WHERE json_array_length(items_json) > 0`)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) SaveQuotaCache(accountName, authIndex, accountID string, quota types.QuotaInfo, probedAt time.Time) error {
	accountName = strings.TrimSpace(accountName)
	authIndex = strings.TrimSpace(authIndex)
	accountID = strings.TrimSpace(accountID)
	if accountName == "" || authIndex == "" || accountID == "" {
		return nil
	}
	b, err := json.Marshal(quota)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`INSERT OR REPLACE INTO quota_cache (account_name, auth_index, account_id, probed_at, payload_json) VALUES (?, ?, ?, ?, ?)`, accountName, authIndex, accountID, probedAt.UTC().Format(time.RFC3339), string(b))
	return err
}

func (s *Store) LoadQuotaCache(accountName string, maxAge time.Duration) (*types.QuotaInfo, *time.Time, error) {
	accountName = strings.TrimSpace(accountName)
	if accountName == "" {
		return nil, nil, nil
	}
	row := s.DB.QueryRow(`SELECT probed_at, payload_json FROM quota_cache WHERE account_name = ?`, accountName)
	var probedAtRaw, payloadJSON string
	if err := row.Scan(&probedAtRaw, &payloadJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	probedAt, err := time.Parse(time.RFC3339, probedAtRaw)
	if err != nil {
		return nil, nil, err
	}
	if maxAge > 0 && time.Since(probedAt.UTC()) > maxAge {
		return nil, &probedAt, nil
	}
	var quota types.QuotaInfo
	if err := json.Unmarshal([]byte(payloadJSON), &quota); err != nil {
		return nil, &probedAt, err
	}
	return &quota, &probedAt, nil
}

func (s *Store) SaveAccountQuotaState(accountName, authIndex, accountID string, quota types.QuotaInfo, probedAt time.Time) error {
	accountName = strings.TrimSpace(accountName)
	authIndex = strings.TrimSpace(authIndex)
	accountID = strings.TrimSpace(accountID)
	if accountName == "" || authIndex == "" || accountID == "" {
		return nil
	}
	refreshAt := ""
	if quota.RefreshAt != nil {
		refreshAt = quota.RefreshAt.UTC().Format(time.RFC3339)
	}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	row := s.DB.QueryRow(`SELECT last_sort_key FROM account_quota_state WHERE account_name = ?`, accountName)
	lastSortKey := ""
	_ = row.Scan(&lastSortKey)
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO account_quota_state (account_name, auth_index, account_id, refresh_at, limit_reached, allowed, used_percent, plan_type, probed_at, updated_at, last_sort_key) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, accountName, authIndex, accountID, refreshAt, boolToInt(quota.LimitReached), boolToInt(quota.Allowed), quota.UsedPercent, quota.PlanType, probedAt.UTC().Format(time.RFC3339), updatedAt, lastSortKey)
	return err
}

func (s *Store) LoadAccountQuotaState(accountName string) (*AccountQuotaState, error) {
	accountName = strings.TrimSpace(accountName)
	if accountName == "" {
		return nil, nil
	}
	row := s.DB.QueryRow(`SELECT account_name, auth_index, account_id, refresh_at, limit_reached, allowed, used_percent, plan_type, probed_at, updated_at, last_sort_key FROM account_quota_state WHERE account_name = ?`, accountName)
	var state AccountQuotaState
	var refreshAtRaw string
	var limitReachedInt, allowedInt int
	var probedAtRaw, updatedAtRaw string
	if err := row.Scan(&state.AccountName, &state.AuthIndex, &state.AccountID, &refreshAtRaw, &limitReachedInt, &allowedInt, &state.UsedPercent, &state.PlanType, &probedAtRaw, &updatedAtRaw, &state.LastSortKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(refreshAtRaw) != "" {
		refreshAt, err := time.Parse(time.RFC3339, refreshAtRaw)
		if err != nil {
			return nil, err
		}
		state.RefreshAt = &refreshAt
	}
	probedAt, err := time.Parse(time.RFC3339, probedAtRaw)
	if err != nil {
		return nil, err
	}
	updatedAt, err := time.Parse(time.RFC3339, updatedAtRaw)
	if err != nil {
		return nil, err
	}
	state.LimitReached = limitReachedInt != 0
	state.Allowed = allowedInt != 0
	state.ProbedAt = probedAt
	state.UpdatedAt = updatedAt
	return &state, nil
}

func (s *Store) GetQuotaStateSummary() (map[string]any, error) {
	rows, err := s.DB.Query(`SELECT account_name, refresh_at, limit_reached, allowed, used_percent, plan_type, probed_at FROM account_quota_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now().UTC()
	total := 0
	availableNow := 0
	limitedNow := 0
	unknownRefresh := 0
	nextRefreshAt := ""
	var nextRefresh time.Time
	peakBucketLabel := ""
	peakBucketCount := 0
	bucketCounts := map[string]int{}
	for rows.Next() {
		var accountName, refreshAtRaw, planType, probedAtRaw string
		var limitReachedInt, allowedInt, usedPercent int
		if err := rows.Scan(&accountName, &refreshAtRaw, &limitReachedInt, &allowedInt, &usedPercent, &planType, &probedAtRaw); err != nil {
			return nil, err
		}
		total++
		limitReached := limitReachedInt != 0
		allowed := allowedInt != 0
		if allowed && !limitReached {
			availableNow++
		}
		if limitReached {
			limitedNow++
		}
		if strings.TrimSpace(refreshAtRaw) == "" {
			unknownRefresh++
			continue
		}
		refreshAt, err := time.Parse(time.RFC3339, refreshAtRaw)
		if err != nil {
			unknownRefresh++
			continue
		}
		if refreshAt.After(now) {
			if nextRefreshAt == "" || refreshAt.Before(nextRefresh) {
				nextRefresh = refreshAt
				nextRefreshAt = refreshAt.UTC().Format(time.RFC3339)
			}
			bucket := refreshAt.UTC().Format("2006-01-02T15") + ":00Z"
			bucketCounts[bucket]++
			if bucketCounts[bucket] > peakBucketCount {
				peakBucketCount = bucketCounts[bucket]
				peakBucketLabel = bucket
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{
		"tracked_accounts": total,
		"available_now_estimate": availableNow,
		"limited_now_estimate": limitedNow,
		"unknown_refresh_accounts": unknownRefresh,
		"next_refresh_at": nextRefreshAt,
		"recovery_peak_bucket": peakBucketLabel,
		"recovery_peak_count": peakBucketCount,
	}, nil
}

func (s *Store) GetRecentActivitySummary(since time.Time) (map[string]int, error) {
	rows, err := s.DB.Query(`SELECT items_json FROM proposals WHERE created_at >= ? ORDER BY created_at DESC`, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summary := map[string]int{
		"auto_disable": 0,
		"auto_enable": 0,
		"auto_reorder": 0,
		"auto_mark_401": 0,
		"proposal_runs": 0,
	}
	for rows.Next() {
		var itemsJSON string
		if err := rows.Scan(&itemsJSON); err != nil {
			return nil, err
		}
		summary["proposal_runs"]++
		var items []types.ProposalItem
		_ = json.Unmarshal([]byte(itemsJSON), &items)
		for _, item := range items {
			switch item.Action {
			case types.ActionDisableAccount:
				summary["auto_disable"]++
			case types.ActionEnableAccount:
				summary["auto_enable"]++
			case types.ActionReorderPriority:
				summary["auto_reorder"]++
			case types.ActionMark401Review:
				summary["auto_mark_401"]++
			}
		}
	}
	return summary, rows.Err()
}

type UpcomingRefreshItem struct {
	AccountName   string `json:"account_name"`
	RefreshAt     string `json:"refresh_at"`
	LimitReached  bool   `json:"limit_reached"`
	Allowed       bool   `json:"allowed"`
	UsedPercent   int    `json:"used_percent"`
	PlanType      string `json:"plan_type"`
	ProbedAt      string `json:"probed_at"`
}

type RecentProbeItem struct {
	AccountName  string `json:"account_name"`
	RefreshAt    string `json:"refresh_at"`
	LimitReached bool   `json:"limit_reached"`
	Allowed      bool   `json:"allowed"`
	UsedPercent  int    `json:"used_percent"`
	PlanType     string `json:"plan_type"`
	ProbedAt     string `json:"probed_at"`
}

func (s *Store) ListUpcomingRefresh(limit int) ([]UpcomingRefreshItem, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.DB.Query(`SELECT account_name, refresh_at, limit_reached, allowed, used_percent, plan_type, probed_at FROM account_quota_state WHERE refresh_at IS NOT NULL AND refresh_at != '' ORDER BY refresh_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UpcomingRefreshItem, 0, limit)
	for rows.Next() {
		var item UpcomingRefreshItem
		var limitReachedInt, allowedInt int
		if err := rows.Scan(&item.AccountName, &item.RefreshAt, &limitReachedInt, &allowedInt, &item.UsedPercent, &item.PlanType, &item.ProbedAt); err != nil {
			return nil, err
		}
		item.LimitReached = limitReachedInt != 0
		item.Allowed = allowedInt != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListDueRefreshAccounts(at time.Time, limit int) ([]UpcomingRefreshItem, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query(`SELECT account_name, refresh_at, limit_reached, allowed, used_percent, plan_type, probed_at FROM account_quota_state WHERE refresh_at IS NOT NULL AND refresh_at != '' AND refresh_at <= ? ORDER BY refresh_at ASC LIMIT ?`, at.UTC().Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UpcomingRefreshItem, 0, limit)
	for rows.Next() {
		var item UpcomingRefreshItem
		var limitReachedInt, allowedInt int
		if err := rows.Scan(&item.AccountName, &item.RefreshAt, &limitReachedInt, &allowedInt, &item.UsedPercent, &item.PlanType, &item.ProbedAt); err != nil {
			return nil, err
		}
		item.LimitReached = limitReachedInt != 0
		item.Allowed = allowedInt != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListRecentProbes(limit int) ([]RecentProbeItem, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.DB.Query(`SELECT account_name, refresh_at, limit_reached, allowed, used_percent, plan_type, probed_at FROM account_quota_state WHERE probed_at IS NOT NULL AND probed_at != '' ORDER BY probed_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RecentProbeItem, 0, limit)
	for rows.Next() {
		var item RecentProbeItem
		var limitReachedInt, allowedInt int
		if err := rows.Scan(&item.AccountName, &item.RefreshAt, &limitReachedInt, &allowedInt, &item.UsedPercent, &item.PlanType, &item.ProbedAt); err != nil {
			return nil, err
		}
		item.LimitReached = limitReachedInt != 0
		item.Allowed = allowedInt != 0
		out = append(out, item)
	}
	return out, rows.Err()
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
