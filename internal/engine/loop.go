package engine

import (
	"log"
	"time"
)

func (e *Engine) StartBackgroundLoop() {
	issueInterval := time.Duration(e.Cfg.IssueScanIntervalSeconds) * time.Second
	reorderInterval := time.Duration(e.Cfg.AutoReorderIntervalSeconds) * time.Second
	if issueInterval <= 0 && reorderInterval <= 0 {
		return
	}
	go func() {
		lastIssueRun := time.Time{}
		lastReorderRun := time.Time{}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			now := time.Now()
			if e.Cfg.EnableAutoIssueScan && issueInterval > 0 && (lastIssueRun.IsZero() || now.Sub(lastIssueRun) >= issueInterval) {
				proposal, err := e.ReconcileIssuesOnly()
				if err != nil {
					log.Printf("background issue scan failed: %v", err)
				} else {
					log.Printf("background issue scan saved proposal=%s items=%d mode=%s", proposal.ID, len(proposal.Items), proposal.Mode)
				}
				lastIssueRun = now
			}
			if e.Cfg.EnableAutoReorder && reorderInterval > 0 && (lastReorderRun.IsZero() || now.Sub(lastReorderRun) >= reorderInterval) {
				proposal, err := e.ReconcileFull("reorder")
				if err != nil {
					log.Printf("background auto reorder failed: %v", err)
				} else {
					log.Printf("background auto reorder saved proposal=%s items=%d mode=%s", proposal.ID, len(proposal.Items), proposal.Mode)
				}
				lastReorderRun = now
			}
			<-ticker.C
		}
	}()
}
