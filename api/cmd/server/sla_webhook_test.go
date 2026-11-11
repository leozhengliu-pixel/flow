package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestSLAWebhookHighRiskUsesPersistedThresholdEventOnce(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "sla-webhook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer receiver.Close()
	s := server{store: repository}
	data := repository.Bootstrap()
	issue, now := data.Issues[0], time.Now().UTC()
	if err := repository.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.sla_webhook", "", nil, func(current *domain.Bootstrap) error {
		current.Webhooks = []domain.Webhook{{ID: "sla-hook", Enabled: true, URL: receiver.URL, ResourceTypes: []string{"issue_sla"}}}
		high := now.Add(-time.Hour)
		current.IssueSLAs = []domain.IssueSLA{{ID: "sla", IssueID: issue.ID, RuleID: "rule", Status: "active", StartedAt: now.Add(-time.Hour), DueAt: now.Add(23 * time.Hour), HighRiskAt: &high}}
		current.SLAEvents = []domain.SLAEvent{{ID: "high-event", IssueID: issue.ID, SLAID: "sla", Type: "high_risk", CreatedAt: high}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// An old event is outside the replay window; an active short timer alone
	// must not synthesize a different high-risk event on every sweep.
	s.sweepIssueSLAWebhooks(t.Context(), data.Workspace.URLKey, now)
	page, err := repository.WebhookDeliveries(t.Context(), store.WebhookDeliveryQuery{Workspace: data.Workspace.URLKey, WebhookID: "sla-hook"})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("sweep invented a new high-risk event: %+v %v", page.Items, err)
	}
	if err := repository.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.sla_recent_risk", "", nil, func(current *domain.Bootstrap) error {
		current.SLAEvents[0].CreatedAt = now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.sweepIssueSLAWebhooks(t.Context(), data.Workspace.URLKey, now)
	s.sweepIssueSLAWebhooks(t.Context(), data.Workspace.URLKey, now.Add(time.Minute))
	page, err = repository.WebhookDeliveries(t.Context(), store.WebhookDeliveryQuery{Workspace: data.Workspace.URLKey, WebhookID: "sla-hook"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Action != "highRisk" || page.Items[0].EventID != "sla:high-event" {
		t.Fatalf("high-risk event duplicated or changed identity: %+v %v", page.Items, err)
	}
}
