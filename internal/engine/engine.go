package engine

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"cpa-pool-manager/internal/config"
	"cpa-pool-manager/internal/cpa"
	"cpa-pool-manager/internal/store"
	"cpa-pool-manager/internal/types"
)

type Engine struct {
	Cfg           config.Config
	Store         *store.Store
	CPA           *cpa.Client
	LastLogCursor int64
	progressMu    sync.RWMutex
	fullProgress  ScanProgress
	probeMu       sync.Mutex
	probeTimes    map[string]time.Time
}

func New(cfg config.Config, st *store.Store, client *cpa.Client) *Engine {
	return &Engine{Cfg: cfg, Store: st, CPA: client, probeTimes: map[string]time.Time{}}
}

type authWithQuota struct {
	Auth       cpa.AuthFile
	Quota      *types.QuotaInfo
	QuotaState *store.AccountQuotaState
	WillEnable bool
}

type ScanProgress struct {
	Kind      string `json:"kind"`
	Mode      string `json:"mode,omitempty"`
	Running   bool   `json:"running"`
	Total     int    `json:"total"`
	Done      int    `json:"done"`
	Percent   int    `json:"percent"`
	Current   string `json:"current"`
	Stage     string `json:"stage"`
	UpdatedAt string `json:"updated_at"`
}

func (e *Engine) ScanIssues() ([]types.Issue, error) {
	auths, err := e.CPA.GetAuthFiles()
	if err != nil {
		return nil, err
	}
	issues := make([]types.Issue, 0)
	for _, a := range auths {
		lowerStatus := strings.ToLower(strings.TrimSpace(a.Status))
		lowerMsg := strings.ToLower(strings.TrimSpace(a.StatusMessage))
		if isExplicit401Status(lowerStatus, lowerMsg) {
			issues = append(issues, types.Issue{AccountName: a.Name, Provider: a.Provider, Severity: "warning", Reason: "explicit_401_requires_manual_review", Disabled: a.Disabled, Meta: map[string]any{"status": a.Status, "status_message": a.StatusMessage, "unavailable": a.Unavailable}})
			continue
		}
		if isQuotaStatus(lowerStatus, lowerMsg) && !a.Disabled {
			issues = append(issues, types.Issue{AccountName: a.Name, Provider: a.Provider, Severity: "warning", Reason: "quota_pending_disable", Disabled: a.Disabled, Meta: map[string]any{"status": a.Status, "status_message": a.StatusMessage, "unavailable": a.Unavailable}})
		}
	}
	return issues, nil
}

func (e *Engine) ReconcileIssuesOnly() (types.Proposal, error) {
	proposal := types.Proposal{ID: uuid.NewString(), Mode: e.Cfg.AppMode, CreatedAt: time.Now().UTC(), Items: []types.ProposalItem{}}
	auths, err := e.CPA.GetAuthFiles()
	if err != nil {
		return proposal, err
	}
	quotaExhausted := map[string]bool{}
	explicit401 := map[string]bool{}
	logSignalAvailable := false
	probesUsed := 0
	probeLimit := e.Cfg.ProbeMaxPerRun
	if probeLimit <= 0 {
		probeLimit = 5
	}
	recoveredNames := map[string]types.QuotaInfo{}
	dueRefreshNames := map[string]bool{}
	if dueItems, err := e.Store.ListDueRefreshAccounts(time.Now().UTC(), probeLimit*4); err == nil {
		for _, item := range dueItems {
			dueRefreshNames[item.AccountName] = true
		}
	}
	if e.Cfg.EnableLogSignal {
		lines, cursor, err := e.CPA.GetLogs(e.LastLogCursor)
		if err == nil {
			logSignalAvailable = true
			e.LastLogCursor = cursor
			for _, line := range lines {
				lower := strings.ToLower(line)
				for _, a := range auths {
					if a.Name == "" || !strings.Contains(line, a.Name) {
						continue
					}
					if isExplicit401Text(lower) {
						explicit401[a.Name] = true
					}
					if isQuotaText(lower) {
						quotaExhausted[a.Name] = true
					}
				}
			}
		}
	}
	for _, a := range auths {
		lowerStatus := strings.ToLower(strings.TrimSpace(a.Status))
		lowerMsg := strings.ToLower(strings.TrimSpace(a.StatusMessage))
		if isExplicit401Status(lowerStatus, lowerMsg) {
			explicit401[a.Name] = true
		}
		if isQuotaStatus(lowerStatus, lowerMsg) {
			quotaState, _ := e.Store.LoadAccountQuotaState(a.Name)
			if quotaState != nil && !quotaState.ProbedAt.IsZero() && time.Since(quotaState.ProbedAt.UTC()) <= 6*time.Hour {
				if quotaState.LimitReached {
					quotaExhausted[a.Name] = true
				}
			} else {
				quotaExhausted[a.Name] = true
			}
		}
	}
	for _, a := range auths {
		authIndex := fmt.Sprint(a.AuthIndex)
		accountID := extractAccountID(a)
		if a.Disabled && probesUsed < probeLimit && authIndex != "" && accountID != "" {
			quotaState, _ := e.Store.LoadAccountQuotaState(a.Name)
			if quotaState != nil && quotaState.RefreshAt != nil && !quotaState.RefreshAt.After(time.Now().UTC()) && e.allowProbeNow(a.Name, quotaState) {
				q, err := e.CPA.ProbeWhamUsage(authIndex, accountID)
				probesUsed++
				if err == nil {
					probedAt := time.Now().UTC()
					_ = e.Store.SaveQuotaCache(a.Name, authIndex, accountID, q, probedAt)
					_ = e.Store.SaveAccountQuotaState(a.Name, authIndex, accountID, q, probedAt)
					if !q.LimitReached {
						recoveredNames[a.Name] = q
						proposal.Items = append(proposal.Items, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionEnableAccount, Reason: "quota:wham_recovered", OldValue: "disabled", NewValue: "enabled", Meta: map[string]any{"reset_at": timePtrString(q.RefreshAt), "used_percent": q.UsedPercent, "plan_type": q.PlanType, "auth_index": authIndex, "chatgpt_account": accountID, "allowed": q.Allowed, "limit_reached": q.LimitReached}})
					}
				} else if cpa.IsExplicit401Error(err) {
					explicit401[a.Name] = true
				}
			}
		}
		if !a.Disabled && dueRefreshNames[a.Name] && probesUsed < probeLimit && authIndex != "" && accountID != "" {
			quotaState, _ := e.Store.LoadAccountQuotaState(a.Name)
			if e.allowProbeNow(a.Name, quotaState) {
				q, err := e.CPA.ProbeWhamUsage(authIndex, accountID)
				probesUsed++
				if err == nil {
					probedAt := time.Now().UTC()
					_ = e.Store.SaveQuotaCache(a.Name, authIndex, accountID, q, probedAt)
					_ = e.Store.SaveAccountQuotaState(a.Name, authIndex, accountID, q, probedAt)
				} else if cpa.IsExplicit401Error(err) {
					explicit401[a.Name] = true
				}
			}
		}
		if explicit401[a.Name] {
			meta := map[string]any{
				"status":         a.Status,
				"status_message": a.StatusMessage,
				"provider":       a.Provider,
				"disabled":       a.Disabled,
				"unavailable":    a.Unavailable,
				"log_signal":     logSignalAvailable,
			}
			if !a.Disabled {
				proposal.Items = append(proposal.Items,
					types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionDisableAccount, Reason: "auth:explicit_401_disable_first", OldValue: "enabled", NewValue: "disabled", Meta: meta},
				)
			}
			proposal.Items = append(proposal.Items,
				types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionMark401Review, Reason: "auth:explicit_401_manual_review", Meta: meta},
			)
			continue
		}
		if quotaExhausted[a.Name] && !a.Disabled {
			proposal.Items = append(proposal.Items, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionDisableAccount, Reason: "quota:usage_limit_reached", OldValue: "enabled", NewValue: "disabled", Meta: map[string]any{"status": a.Status, "status_message": a.StatusMessage, "provider": a.Provider, "unavailable": a.Unavailable}})
		}
	}
	if len(recoveredNames) > 0 {
		type candidate struct {
			name  string
			quota types.QuotaInfo
			prio  int
		}
		candidates := make([]candidate, 0, len(auths))
		for _, a := range auths {
			if a.Disabled && recoveredNames[a.Name] == (types.QuotaInfo{}) {
				continue
			}
			if q, ok := recoveredNames[a.Name]; ok {
				candidates = append(candidates, candidate{name: a.Name, quota: q, prio: a.Priority})
				continue
			}
			quotaState, _ := e.Store.LoadAccountQuotaState(a.Name)
			if a.Disabled || quotaState == nil || quotaState.RefreshAt == nil {
				continue
			}
			candidates = append(candidates, candidate{name: a.Name, quota: types.QuotaInfo{RefreshAt: quotaState.RefreshAt, UsedPercent: quotaState.UsedPercent, PlanType: quotaState.PlanType, Allowed: quotaState.Allowed, LimitReached: quotaState.LimitReached}, prio: a.Priority})
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			ai, aj := candidates[i], candidates[j]
			if ai.quota.RefreshAt != nil && aj.quota.RefreshAt != nil && !ai.quota.RefreshAt.Equal(*aj.quota.RefreshAt) {
				return ai.quota.RefreshAt.Before(*aj.quota.RefreshAt)
			}
			if ai.quota.RefreshAt != nil && aj.quota.RefreshAt == nil {
				return true
			}
			if ai.quota.RefreshAt == nil && aj.quota.RefreshAt != nil {
				return false
			}
			return ai.name < aj.name
		})
		for i, item := range candidates {
			q, ok := recoveredNames[item.name]
			if !ok {
				continue
			}
			targetPriority := len(candidates) - i
			if e.Cfg.PriorityOrder == "asc" {
				targetPriority = i + 1
			}
			targetPriority = sparsePriorityAt(i, len(candidates), e.Cfg.PriorityOrder)
			proposal.Items = append(proposal.Items, types.ProposalItem{AccountID: item.name, AccountName: item.name, Action: types.ActionReorderPriority, Reason: "reorder:refresh_at", OldValue: fmt.Sprintf("%d", item.prio), NewValue: fmt.Sprintf("%d", targetPriority), Meta: map[string]any{"refresh_at": timePtrString(q.RefreshAt), "used_percent": q.UsedPercent, "plan_type": q.PlanType, "allowed": q.Allowed, "limit_reached": q.LimitReached}})
		}
	}
	log.Printf("issue-scan stats auths=%d proposal_items=%d", len(auths), len(proposal.Items))
	if err := e.Store.SaveProposal(proposal); err != nil {
		return proposal, err
	}
	if e.Cfg.AppMode == "apply" && len(proposal.Items) > 0 {
		if err := e.ApplyProposal(proposal.ID); err != nil {
			return proposal, err
		}
		log.Printf("issue-scan auto-applied proposal=%s items=%d", proposal.ID, len(proposal.Items))
	}
	return proposal, nil
}

func (e *Engine) ListManualReview401() ([]types.ManualReview401Item, error) {
	auths, err := e.CPA.GetAuthFiles()
	if err != nil {
		return nil, err
	}
	authByName := make(map[string]cpa.AuthFile, len(auths))
	byAccount := make(map[string]types.ManualReview401Item)
	for _, a := range auths {
		authByName[a.Name] = a
		lowerStatus := strings.ToLower(strings.TrimSpace(a.Status))
		lowerMsg := strings.ToLower(strings.TrimSpace(a.StatusMessage))
		if !isExplicit401Status(lowerStatus, lowerMsg) {
			continue
		}
		byAccount[a.Name] = types.ManualReview401Item{
			AccountName: a.Name,
			Provider:    a.Provider,
			Disabled:    a.Disabled,
			Reason:      "explicit_401_manual_review",
			Evidence: map[string]any{
				"source":         "auth-file",
				"status":         a.Status,
				"status_message": a.StatusMessage,
				"unavailable":    a.Unavailable,
				"failed":         a.Failed,
				"updated_at":     a.UpdatedAt,
			},
		}
	}

	proposals, err := e.Store.ListProposals(200, 0)
	if err != nil {
		return nil, err
	}
	for _, proposal := range proposals {
		for _, item := range proposal.Items {
			if item.Action != types.ActionMark401Review || item.Reason != "auth:explicit_401_manual_review" {
				continue
			}
			name := item.AccountName
			if strings.TrimSpace(name) == "" {
				name = item.AccountID
			}
			if strings.TrimSpace(name) == "" {
				continue
			}
			current, ok := authByName[name]
			if !ok {
				continue
			}
			provider, _ := item.Meta["provider"].(string)
			disabled, _ := item.Meta["disabled"].(bool)
			evidence := map[string]any{
				"source":        "proposal",
				"proposal_id":   proposal.ID,
				"proposal_mode": proposal.Mode,
				"proposal_at":   proposal.CreatedAt.UTC().Format(time.RFC3339),
				"meta":          item.Meta,
			}
			provider = current.Provider
			disabled = current.Disabled
			evidence["current_status"] = current.Status
			evidence["current_status_message"] = current.StatusMessage
			evidence["current_unavailable"] = current.Unavailable
			evidence["current_failed"] = current.Failed
			evidence["current_updated_at"] = current.UpdatedAt
			byAccount[name] = types.ManualReview401Item{
				AccountName: name,
				Provider:    provider,
				Disabled:    disabled,
				Reason:      "explicit_401_manual_review",
				Evidence:    evidence,
			}
		}
	}

	items := make([]types.ManualReview401Item, 0, len(byAccount))
	for _, item := range byAccount {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].AccountName < items[j].AccountName
	})
	return items, nil
}

func (e *Engine) ReconcileFull(mode string) (types.Proposal, error) {
	proposal := types.Proposal{ID: uuid.NewString(), Mode: e.Cfg.AppMode, CreatedAt: time.Now().UTC(), Items: []types.ProposalItem{}}
	auths, err := e.CPA.GetAuthFiles()
	if err != nil {
		return proposal, err
	}
	stage := "准备全量对账"
	if mode == "quota" {
		stage = "准备额度检查"
	} else if mode == "reorder" {
		stage = "准备优先级重排"
	}
	e.setFullProgress(ScanProgress{Kind: "full", Mode: mode, Running: true, Total: len(auths), Done: 0, Percent: 0, Stage: stage, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
	defer e.setFullProgress(ScanProgress{Kind: "full", Mode: mode, Running: false, Total: len(auths), Done: len(auths), Percent: 100, Stage: "已完成", UpdatedAt: time.Now().UTC().Format(time.RFC3339)})

	items := make([]authWithQuota, len(auths))
	type fullResult struct {
		idx   int
		wrap  authWithQuota
		props []types.ProposalItem
	}
	jobs := make(chan int)
	results := make(chan fullResult, len(auths))
	var doneMu sync.Mutex
	done := 0
	workerCount := 4
	if workerCount > len(auths) {
		workerCount = len(auths)
	}
	if workerCount < 1 {
		workerCount = 1
	}
	var wg sync.WaitGroup
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				a := auths[idx]
				wrapped := authWithQuota{Auth: a}
				localProps := make([]types.ProposalItem, 0)
				lowerStatus := strings.ToLower(strings.TrimSpace(a.Status))
				lowerMsg := strings.ToLower(strings.TrimSpace(a.StatusMessage))

				if mode != "reorder" && isExplicit401Status(lowerStatus, lowerMsg) {
					meta := map[string]any{"status": a.Status, "status_message": a.StatusMessage, "provider": a.Provider, "disabled": a.Disabled, "unavailable": a.Unavailable}
					if !a.Disabled {
						localProps = append(localProps, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionDisableAccount, Reason: "auth:explicit_401_disable_first", OldValue: "enabled", NewValue: "disabled", Meta: meta})
					}
					localProps = append(localProps, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionMark401Review, Reason: "auth:explicit_401_manual_review", Meta: meta})
					results <- fullResult{idx: idx, wrap: wrapped, props: localProps}
					doneMu.Lock()
					done++
					progressStage := "识别 401 / 额度"
					if mode == "quota" {
						progressStage = "额度检查中"
					}
					e.setFullProgress(ScanProgress{Kind: "full", Mode: mode, Running: true, Total: len(auths), Done: done, Percent: calcPercent(done, len(auths)), Current: a.Name, Stage: progressStage, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
					doneMu.Unlock()
					continue
				}

				authIndex := fmt.Sprint(a.AuthIndex)
				accountID := extractAccountID(a)
				shouldProbeExpiredRefresh := false
				if mode == "reorder" {
					cachedQuota, _, err := e.Store.LoadQuotaCache(a.Name, 15*time.Minute)
					if err == nil && cachedQuota != nil {
						wrapped.Quota = cachedQuota
					}
					quotaState, err := e.Store.LoadAccountQuotaState(a.Name)
					if err == nil && quotaState != nil {
						wrapped.QuotaState = quotaState
						if quotaState.RefreshAt != nil && !quotaState.RefreshAt.After(time.Now().UTC()) && authIndex != "" && accountID != "" && e.allowProbeNow(a.Name, quotaState) {
							shouldProbeExpiredRefresh = true
						}
					}
					if wrapped.QuotaState == nil && !a.Disabled && authIndex != "" && accountID != "" && e.allowProbeNow(a.Name, nil) {
						shouldProbeExpiredRefresh = true
					}
				}
				if (mode != "reorder" || shouldProbeExpiredRefresh) && e.Cfg.EnableQuotaPoll && authIndex != "" && accountID != "" {
					q, err := e.CPA.ProbeWhamUsage(authIndex, accountID)
					if err == nil {
						wrapped.Quota = &q
						probedAt := time.Now().UTC()
						_ = e.Store.SaveQuotaCache(a.Name, authIndex, accountID, q, probedAt)
						_ = e.Store.SaveAccountQuotaState(a.Name, authIndex, accountID, q, probedAt)
						if q.LimitReached && !a.Disabled {
							localProps = append(localProps, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionDisableAccount, Reason: "quota:wham_limit_reached", OldValue: "enabled", NewValue: "disabled", Meta: map[string]any{"reset_at": timePtrString(q.RefreshAt), "used_percent": q.UsedPercent, "plan_type": q.PlanType, "auth_index": authIndex, "chatgpt_account": accountID}})
						}
						if !q.LimitReached && a.Disabled {
							wrapped.WillEnable = true
							localProps = append(localProps, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionEnableAccount, Reason: "quota:wham_recovered", OldValue: "disabled", NewValue: "enabled", Meta: map[string]any{"reset_at": timePtrString(q.RefreshAt), "used_percent": q.UsedPercent, "plan_type": q.PlanType, "auth_index": authIndex, "chatgpt_account": accountID, "allowed": q.Allowed, "limit_reached": q.LimitReached}})
						}
					} else if cpa.IsExplicit401Error(err) {
						meta := map[string]any{"provider": a.Provider, "disabled": a.Disabled, "unavailable": a.Unavailable, "auth_index": authIndex, "chatgpt_account": accountID, "probe_error": err.Error()}
						if !a.Disabled {
							localProps = append(localProps, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionDisableAccount, Reason: "auth:explicit_401_disable_first", OldValue: "enabled", NewValue: "disabled", Meta: meta})
						}
						localProps = append(localProps, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionMark401Review, Reason: "auth:explicit_401_manual_review", Meta: meta})
					}
				}

				results <- fullResult{idx: idx, wrap: wrapped, props: localProps}
				doneMu.Lock()
				done++
				progressStage := "识别 401 / 额度"
				if mode == "quota" {
					progressStage = "额度检查中"
				} else if mode == "reorder" {
					progressStage = "优先级重排分析中"
				}
				e.setFullProgress(ScanProgress{Kind: "full", Mode: mode, Running: true, Total: len(auths), Done: done, Percent: calcPercent(done, len(auths)), Current: a.Name, Stage: progressStage, UpdatedAt: time.Now().UTC().Format(time.RFC3339)})
				doneMu.Unlock()
			}
		}()
	}
	for i := range auths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(results)
	for res := range results {
		items[res.idx] = res.wrap
		proposal.Items = append(proposal.Items, res.props...)
	}

	if mode != "quota" {
		sort.SliceStable(items, func(i, j int) bool {
			ai := items[i]
			aj := items[j]
			if ai.Auth.Disabled != aj.Auth.Disabled {
				return !ai.Auth.Disabled && aj.Auth.Disabled
			}
			ati := effectiveRefreshAt(ai)
			atj := effectiveRefreshAt(aj)
			if ati != nil && atj != nil && !ati.Equal(*atj) {
				return ati.Before(*atj)
			}
			if ati != nil && atj == nil {
				return true
			}
			if ati == nil && atj != nil {
				return false
			}
			return ai.Auth.Name < aj.Auth.Name
		})
		enabledItems := make([]authWithQuota, 0, len(items))
		for _, item := range items {
			if item.Auth.Disabled && !item.WillEnable {
				continue
			}
			enabledItems = append(enabledItems, item)
		}
		currentPrios := make([]int, 0, len(enabledItems))
		for _, item := range enabledItems {
			currentPrios = append(currentPrios, item.Auth.Priority)
		}
		for i, item := range enabledItems {
			a := item.Auth
			targetPriority := sparsePriorityAt(i, len(enabledItems), e.Cfg.PriorityOrder)
			if item.WillEnable {
				targetPriority = sparseInsertPriority(i, currentPrios, e.Cfg.PriorityOrder)
			}
			oldPriority := a.Priority
			if item.WillEnable && oldPriority <= 0 {
				oldPriority = 0
			}
			if a.Priority != targetPriority || a.Priority == 0 || item.WillEnable {
				reason := "reorder:refresh_at"
				meta := map[string]any{}
				if item.QuotaState != nil && item.QuotaState.RefreshAt != nil {
					reason = "reorder:stored_refresh_at"
					meta["refresh_at"] = timePtrString(item.QuotaState.RefreshAt)
					meta["probed_at"] = item.QuotaState.ProbedAt.UTC().Format(time.RFC3339)
					meta["plan_type"] = item.QuotaState.PlanType
					meta["used_percent"] = item.QuotaState.UsedPercent
					meta["limit_reached"] = item.QuotaState.LimitReached
					meta["allowed"] = item.QuotaState.Allowed
				} else if item.Quota != nil {
					meta["plan_type"] = item.Quota.PlanType
					meta["used_percent"] = item.Quota.UsedPercent
					meta["limit_reached"] = item.Quota.LimitReached
					meta["refresh_at"] = timePtrString(item.Quota.RefreshAt)
				} else if a.NextRetryAfter != nil {
					reason = "reorder:next_retry_after_fallback"
					meta["refresh_at"] = timePtrString(a.NextRetryAfter)
				} else {
					reason = "reorder:fallback_name"
				}
				proposal.Items = append(proposal.Items, types.ProposalItem{AccountID: a.Name, AccountName: a.Name, Action: types.ActionReorderPriority, Reason: reason, OldValue: fmt.Sprintf("%d", oldPriority), NewValue: fmt.Sprintf("%d", targetPriority), Meta: meta})
			}
		}
	}

	log.Printf("full-reconcile stats mode=%s auths=%d wrapped=%d proposal_items=%d", mode, len(auths), len(items), len(proposal.Items))
	if err := e.Store.SaveProposal(proposal); err != nil {
		return proposal, err
	}
	if e.Cfg.AppMode == "apply" && len(proposal.Items) > 0 {
		if err := e.ApplyProposal(proposal.ID); err != nil {
			return proposal, err
		}
		log.Printf("full-reconcile auto-applied proposal=%s mode=%s items=%d", proposal.ID, mode, len(proposal.Items))
	}
	return proposal, nil
}

func (e *Engine) ApplyProposal(id string) error {
	proposals, err := e.Store.ListProposals(1000, 0)
	if err != nil {
		return err
	}
	for _, p := range proposals {
		if p.ID != id {
			continue
		}
		for _, item := range p.Items {
			switch item.Action {
			case types.ActionDeleteAccount:
				if err := e.CPA.DeleteAuthFile(item.AccountName); err != nil {
					return err
				}
			case types.ActionDisableAccount:
				if err := e.CPA.PatchAuthFileStatus(item.AccountName, true); err != nil {
					return err
				}
			case types.ActionEnableAccount:
				if err := e.CPA.PatchAuthFileStatus(item.AccountName, false); err != nil {
					return err
				}
			case types.ActionReorderPriority:
				var pr int
				fmt.Sscanf(item.NewValue, "%d", &pr)
				note := "priority updated by cpa-pool-manager"
				if err := e.CPA.PatchAuthFileFields(item.AccountName, &pr, &note); err != nil {
					return err
				}
			case types.ActionMark401Review:
				continue
			}
		}
		return nil
	}
	return fmt.Errorf("proposal not found: %s", id)
}

func extractAccountID(a cpa.AuthFile) string {
	if v, ok := a.IDToken["chatgpt_account_id"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := a.IDToken["account_id"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if strings.TrimSpace(a.Provider) == "codex" {
		return ""
	}
	if strings.TrimSpace(a.Account) != "" {
		return strings.TrimSpace(a.Account)
	}
	return ""
}

func effectiveRefreshAt(a authWithQuota) *time.Time {
	if a.QuotaState != nil && a.QuotaState.RefreshAt != nil {
		if a.QuotaState.RefreshAt.After(time.Now().UTC()) {
			return a.QuotaState.RefreshAt
		}
	}
	if a.Quota != nil && a.Quota.RefreshAt != nil {
		if a.Quota.RefreshAt.After(time.Now().UTC()) {
			return a.Quota.RefreshAt
		}
	}
	if a.Auth.NextRetryAfter != nil {
		return a.Auth.NextRetryAfter
	}
	return nil
}

func (e *Engine) setFullProgress(progress ScanProgress) {
	e.progressMu.Lock()
	defer e.progressMu.Unlock()
	e.fullProgress = progress
}

func (e *Engine) GetFullProgress() ScanProgress {
	e.progressMu.RLock()
	defer e.progressMu.RUnlock()
	return e.fullProgress
}

func calcPercent(done, total int) int {
	if total <= 0 {
		return 0
	}
	if done <= 0 {
		return 0
	}
	if done >= total {
		return 100
	}
	return done * 100 / total
}

func (e *Engine) allowProbeNow(accountName string, quotaState *store.AccountQuotaState) bool {
	cooldown := time.Duration(e.Cfg.ProbeCooldownSeconds) * time.Second
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}
	now := time.Now().UTC()
	e.probeMu.Lock()
	defer e.probeMu.Unlock()
	if last, ok := e.probeTimes[accountName]; ok && now.Sub(last) < cooldown {
		return false
	}
	if quotaState != nil && !quotaState.ProbedAt.IsZero() && now.Sub(quotaState.ProbedAt.UTC()) < cooldown {
		return false
	}
	e.probeTimes[accountName] = now
	return true
}

func sparsePriorityAt(index, total int, order string) int {
	const step = 100
	if total <= 0 {
		return step
	}
	if order == "asc" {
		return (index + 1) * step
	}
	return (total - index) * step
}

func sparseInsertPriority(index int, priorities []int, order string) int {
	fallback := sparsePriorityAt(index, len(priorities), order)
	if len(priorities) == 0 {
		return fallback
	}
	if order == "asc" {
		if index <= 0 {
			if priorities[0] > 1 {
				return priorities[0] / 2
			}
			return fallback
		}
		if index >= len(priorities) {
			return priorities[len(priorities)-1] + 100
		}
		left := priorities[index-1]
		right := priorities[index]
		if right-left > 1 {
			return left + (right-left)/2
		}
		return fallback
	}
	if index <= 0 {
		return priorities[0] + 100
	}
	if index >= len(priorities) {
		if priorities[len(priorities)-1] > 1 {
			return priorities[len(priorities)-1] / 2
		}
		return fallback
	}
	left := priorities[index-1]
	right := priorities[index]
	if left-right > 1 {
		return right + (left-right)/2
	}
	return fallback
}

func timePtrString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func isExplicit401Text(lower string) bool {
	return strings.Contains(lower, " 401") || strings.Contains(lower, "status=401") || strings.Contains(lower, "http 401") || strings.Contains(lower, "\"401\"") || strings.Contains(lower, "[401]")
}

func isExplicit401Status(lowerStatus, lowerMsg string) bool {
	return isExplicit401Text(lowerStatus) || isExplicit401Text(lowerMsg)
}

func isQuotaText(lower string) bool {
	return strings.Contains(lower, "quota exhausted") || strings.Contains(lower, "usage_limit_reached") || strings.Contains(lower, "额度耗尽")
}

func isQuotaStatus(lowerStatus, lowerMsg string) bool {
	if isExplicit401Status(lowerStatus, lowerMsg) {
		return false
	}
	return isQuotaText(lowerStatus) || isQuotaText(lowerMsg)
}
