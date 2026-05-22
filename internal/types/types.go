package types

import "time"

type ActionType string

const (
	ActionDeleteAccount      ActionType = "DELETE_ACCOUNT"
	ActionDisableAccount     ActionType = "DISABLE_ACCOUNT"
	ActionEnableAccount      ActionType = "ENABLE_ACCOUNT"
	ActionReorderPriority    ActionType = "REORDER_PRIORITY"
	ActionMark401Review      ActionType = "MARK_401_REVIEW"
)

type ProposalItem struct {
	AccountID   string         `json:"account_id"`
	AccountName string         `json:"account_name"`
	Action      ActionType     `json:"action"`
	Reason      string         `json:"reason"`
	OldValue    string         `json:"old_value,omitempty"`
	NewValue    string         `json:"new_value,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

type Proposal struct {
	ID        string         `json:"id"`
	Mode      string         `json:"mode"`
	CreatedAt time.Time      `json:"created_at"`
	Items     []ProposalItem `json:"items"`
}

type Issue struct {
	AccountName string         `json:"account_name"`
	Provider    string         `json:"provider,omitempty"`
	Severity    string         `json:"severity"`
	Reason      string         `json:"reason"`
	Disabled    bool           `json:"disabled"`
	Meta        map[string]any `json:"meta,omitempty"`
}

type ManualReview401Item struct {
	AccountName string         `json:"account_name"`
	Provider    string         `json:"provider,omitempty"`
	Disabled    bool           `json:"disabled"`
	Reason      string         `json:"reason"`
	Evidence    map[string]any `json:"evidence,omitempty"`
}
