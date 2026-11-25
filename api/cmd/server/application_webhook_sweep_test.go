package main

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21ApplicationWebhookSweepQueueDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("app-sweep-")
	now := time.Date(2026, 11, 14, 1, 2, 3, 4, time.UTC)
	var apps []domain.ApplicationInstallation
	for _, name := range []string{"first", "second", "inactive"} {
		app, err := repo.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, domain.ApplicationInstallation{
			ClientID: prefix + name, Name: name, Active: name != "inactive", InstalledBy: f.data.Viewer.ID,
			TeamIDs: []string{f.data.Teams[0].ID}, Scopes: []string{"read", "write"},
		}, "")
		if err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
	}
	teamID, issueID, commentID := f.data.Teams[0].ID, prefix+"issue", prefix+"comment"
	hooks := []domain.Webhook{
		{ID: prefix + "app-audit-empty", Enabled: true, AuditLog: true, ManagedBy: "application", ApplicationClientID: apps[0].ClientID, ResourceTypes: []string{"app_user_notifications"}},
		{ID: prefix + "app-normal", Enabled: true, URL: "https://example.test/first", ManagedBy: "application", ApplicationClientID: apps[0].ClientID, ResourceTypes: []string{"app_user_notifications"}},
		{ID: prefix + "app-second", Enabled: true, URL: "https://example.test/second", ManagedBy: "application", ApplicationClientID: apps[1].ClientID, ResourceTypes: []string{"app_user_notifications"}},
		{ID: prefix + "app-inactive", Enabled: true, ManagedBy: "application", ApplicationClientID: apps[2].ClientID, ResourceTypes: []string{"app_user_notifications"}},
		{ID: prefix + "app-team-blocked", Enabled: true, TeamRestriction: "all", TeamIDs: []string{prefix + "hidden"}, ManagedBy: "application", ApplicationClientID: apps[0].ClientID, ResourceTypes: []string{"app_user_notifications"}},
		{ID: prefix + "app-disabled", ManagedBy: "application", ApplicationClientID: apps[0].ClientID, ResourceTypes: []string{"app_user_notifications"}},
		{ID: prefix + "app-unmanaged", Enabled: true, ApplicationClientID: apps[0].ClientID, ResourceTypes: []string{"app_user_notifications"}},
		{ID: prefix + "sla-empty-url", Enabled: true, ResourceTypes: []string{"issue_sla"}},
		{ID: prefix + "sla-selected-empty", Enabled: true, TeamRestriction: "selected", ResourceTypes: []string{"issue_sla"}},
		{ID: prefix + "sla-allowed", Enabled: true, TeamRestriction: "selected", TeamIDs: []string{teamID}, ResourceTypes: []string{"issue_sla"}},
		{ID: prefix + "sla-blocked", Enabled: true, TeamRestriction: "selected", TeamIDs: []string{prefix + "hidden"}, ResourceTypes: []string{"issue_sla"}},
		{ID: prefix + "sla-audit", Enabled: true, AuditLog: true, ResourceTypes: []string{"issue_sla"}},
	}
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.application_sweep_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Webhooks = hooks
		data.Issues = []domain.Issue{{
			ID: issueID, Identifier: "SWEEP-1", Title: "Application issue", Team: f.data.Teams[0],
			State: domain.WorkflowState{ID: prefix + "state", Type: "backlog"}, Creator: f.data.Viewer,
			Description: "Complete SLA issue body", CreatedAt: now, UpdatedAt: now,
		}}
		data.Comments = map[string][]domain.Comment{issueID: {{ID: commentID, Body: "Application comment", User: f.data.Viewer, CreatedAt: now}}}
		data.Notifications = []domain.Notification{}
		for _, spec := range []struct {
			name, category, kind, issue, comment string
			age                                  time.Duration
		}{
			{"mention", "mentions", "", issueID, commentID, 0},
			{"comment", "", "comment", issueID, "", time.Minute},
			{"reaction", "reactions", "", issueID, prefix + "missing-comment", 2 * time.Minute},
			{"stale", "mentions", "", issueID, "", 15*time.Minute + time.Nanosecond},
			{"assignment", "assignments", "", issueID, "", 0},
			{"unsupported", "other", "", issueID, "", 0},
			{"missing-issue", "mentions", "", prefix + "missing", "", 0},
			{"boundary", "statusChanges", "", issueID, "", 15 * time.Minute},
			{"future", "mentions", "", issueID, "", -time.Hour},
		} {
			data.Notifications = append(data.Notifications, domain.Notification{
				ID: prefix + spec.name, RecipientID: apps[0].UserID, Category: spec.category, Type: spec.kind,
				IssueID: spec.issue, CommentID: spec.comment, Actor: f.data.Viewer, CreatedAt: now, UpdatedAt: now.Add(-spec.age),
			})
		}
		data.NotificationDeliveries = []domain.NotificationDelivery{}
		data.IssueSLAs = []domain.IssueSLA{
			{ID: prefix + "sla", IssueID: issueID, RuleID: "rule", Status: "active", StartedAt: now.Add(-5 * time.Hour), DueAt: now.Add(time.Hour)},
			{ID: prefix + "inactive-sla", IssueID: issueID, Status: "completed", DueAt: now},
		}
		data.SLAEvents = []domain.SLAEvent{
			{ID: prefix + "started", IssueID: issueID, SLAID: prefix + "sla", Type: "started", CreatedAt: now},
			{ID: prefix + "breached", IssueID: issueID, SLAID: prefix + "missing", Type: "breached", CreatedAt: now.Add(-15 * time.Minute)},
			{ID: prefix + "missing-issue-event", IssueID: prefix + "missing", SLAID: prefix + "sla", Type: "started", CreatedAt: now},
			{ID: prefix + "old-event", IssueID: issueID, Type: "breached", CreatedAt: now.Add(-16 * time.Minute)},
			{ID: prefix + "ignored-event", IssueID: issueID, Type: "completed", CreatedAt: now},
		}
		data.Documents = []domain.Document{{ID: prefix + "unrelated-document", Content: strings.Repeat("unrelated document ", 50000)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	clear := func() {
		t.Helper()
		for _, hook := range hooks {
			if err := repo.DeleteWebhookDeliveries(t.Context(), workspace, hook.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for _, hook := range hooks {
			_ = repo.DeleteWebhookDeliveries(context.Background(), workspace, hook.ID)
		}
	})
	type queued struct {
		ID, Hook, EventID, EventType, Action, Status, URL, Secret string
		Attempt, MaxAttempts                                      int
		Payload                                                   json.RawMessage
	}
	collect := func() []queued {
		t.Helper()
		var result []queued
		for _, hook := range hooks {
			page, err := repo.WebhookDeliveries(t.Context(), store.WebhookDeliveryQuery{Workspace: workspace, WebhookID: hook.ID, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				delivery, err := repo.WebhookDelivery(t.Context(), workspace, item.ID)
				if err != nil {
					t.Fatal(err)
				}
				body := delivery.Payload
				if delivery.EventType == "AppUserNotification" {
					var value map[string]json.RawMessage
					if err := json.Unmarshal(body, &value); err != nil {
						t.Fatal(err)
					}
					value["createdAt"] = json.RawMessage(`"generated"`)
					body, err = json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
				}
				result = append(result, queued{delivery.ID, delivery.WebhookID, delivery.EventID, delivery.EventType, delivery.Action, delivery.Status, delivery.URL, delivery.SecretRef, delivery.Attempt, delivery.MaxAttempts, body})
			}
		}
		slices.SortFunc(result, func(a, b queued) int { return strings.Compare(a.ID, b.ID) })
		return result
	}
	for _, name := range []string{"inbox", "assignment", "sla"} {
		t.Run(name, func(t *testing.T) {
			for _, reload := range []bool{false, true} {
				if reload {
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				var want []queued
				for _, bounded := range []bool{false, true} {
					clear()
					source := repo
					if bounded {
						source = view
					}
					service := &server{store: source}
					call := func() {
						switch name {
						case "inbox":
							service.sweepAppUserNotifications(t.Context(), workspace, now)
						case "assignment":
							service.dispatchAppAssignmentNotifications(workspace, domain.DomainEvent{
								ID: prefix + "assigned", Type: "issue.updated", AggregateID: issueID, ActorID: f.data.Viewer.ID, CreatedAt: now,
								PreviousValues: json.RawMessage(`{"assignee":{"id":"` + apps[1].UserID + `"},"delegate":null}`),
							}, map[string]any{"assignee": map[string]any{"id": apps[0].UserID}, "delegate": map[string]any{"id": apps[0].UserID}})
						case "sla":
							service.sweepIssueSLAWebhooks(t.Context(), workspace, now)
						}
					}
					call()
					got := collect()
					call()
					if !reflect.DeepEqual(got, collect()) {
						t.Fatal("repeated sweep changed idempotent queue")
					}
					if !bounded {
						want = got
						expected := map[string]int{"inbox": 13, "assignment": 5, "sla": 9}[name]
						if len(got) != expected {
							t.Fatalf("literal queue fixture changed %s reload=%v got=%d want=%d", name, reload, len(got), expected)
						}
					} else if !reflect.DeepEqual(got, want) || source.WorkspaceMetadataDecodes() != 0 || source.MetadataWorkUsedForTesting() != 0 {
						t.Fatalf("queue differential %s reload=%v got=%d want=%d work=%d:\ngot=%+v\nwant=%+v", name, reload, len(got), len(want), source.MetadataWorkUsedForTesting(), got, want)
					}
				}
			}
		})
	}
}
