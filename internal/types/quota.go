package types

import "time"

type QuotaInfo struct {
	AuthIndex          string     `json:"auth_index,omitempty"`
	AccountID          string     `json:"account_id,omitempty"`
	Email              string     `json:"email,omitempty"`
	PlanType           string     `json:"plan_type,omitempty"`
	Allowed            bool       `json:"allowed"`
	LimitReached       bool       `json:"limit_reached"`
	UsedPercent        int        `json:"used_percent,omitempty"`
	ResetAfterSeconds  int64      `json:"reset_after_seconds,omitempty"`
	RefreshAt          *time.Time `json:"refresh_at,omitempty"`
	RawBody            string     `json:"raw_body,omitempty"`
}
