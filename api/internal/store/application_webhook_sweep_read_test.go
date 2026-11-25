package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21ApplicationSweepHooks(data domain.Bootstrap, resource string) []domain.Webhook {
	var result []domain.Webhook
	for _, hook := range data.Webhooks {
		if !hook.Enabled || !slices.Contains(hook.ResourceTypes, resource) ||
			resource == "app_user_notifications" && hook.ManagedBy != "application" ||
			resource == "issue_sla" && hook.AuditLog {
			continue
		}
		result = append(result, domain.Webhook{
			ID: hook.ID, URL: hook.URL, ResourceTypes: []string{resource}, TeamIDs: hook.TeamIDs,
			TeamRestriction: hook.TeamRestriction, Enabled: hook.Enabled, AuditLog: hook.AuditLog,
			ManagedBy: hook.ManagedBy, ApplicationClientID: hook.ApplicationClientID,
		})
	}
	return result
}

func TestScaleP21ApplicationWebhookSweepNative(t *testing.T) {
	now := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("native", 7*3600+123))
	first := domain.IssueSLA{ID: "duplicate", IssueID: "first-issue", RuleID: "first-rule", Status: "active", StartedAt: now.Add(-4 * time.Hour), DueAt: now.Add(time.Minute)}
	last := domain.IssueSLA{ID: "duplicate", IssueID: "last-issue", RuleID: "last-rule", Status: "completed", StartedAt: now.Add(-time.Hour), DueAt: now.Add(time.Hour)}
	empty := domain.IssueSLA{ID: "", IssueID: "empty-issue", RuleID: "empty-rule", Status: "active", StartedAt: now.Add(-5 * time.Hour), DueAt: now.Add(time.Hour)}
	long := domain.IssueSLA{ID: "long", IssueID: "long-issue", RuleID: "long-rule", Status: "active", StartedAt: now.Add(-100 * 24 * time.Hour), DueAt: now.Add(24 * time.Hour)}
	expired := domain.IssueSLA{ID: "expired", Status: "active", DueAt: now}
	breached := domain.IssueSLA{ID: "breached", Status: "active", DueAt: now.Add(time.Minute), BreachedAt: &now}
	wide := strings.Repeat("unrelated body ", 200000)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "organization\x00", URLKey: "logical"},
		Webhooks: []domain.Webhook{
			{ID: "audit-empty", Enabled: true, AuditLog: true, ManagedBy: "application", ApplicationClientID: "client\x00", ResourceTypes: []string{wide, "app_user_notifications", "issue_sla"}, Name: wide, SecretHash: wide},
			{ID: "duplicate", Enabled: true, ManagedBy: "application", URL: "", ResourceTypes: []string{"app_user_notifications", "issue_sla"}, TeamIDs: []string{}, TeamRestriction: "selected"},
			{ID: "duplicate", Enabled: true, URL: "https://example.test", ResourceTypes: []string{"issue_sla"}},
			{ID: "", Enabled: true, ManagedBy: "application", URL: " \x00 ", ResourceTypes: []string{"app_user_notifications"}, TeamIDs: []string{"private"}},
			{ID: "disabled", ResourceTypes: []string{"app_user_notifications", "issue_sla"}, Name: wide},
			{ID: "wrong-managed", Enabled: true, ManagedBy: "Application", ResourceTypes: []string{"app_user_notifications"}},
			{ID: "wrong-resource", Enabled: true, ManagedBy: "application", ResourceTypes: []string{"AppUserNotification", "IssueSLA"}},
		},
		IssueSLAs: []domain.IssueSLA{first, last, empty, long, expired, breached},
		SLAEvents: []domain.SLAEvent{
			{ID: "old", Type: "started", CreatedAt: now.Add(-15*time.Minute - time.Nanosecond)},
			{ID: "set", Type: "started", IssueID: "event-issue", SLAID: "duplicate", CreatedAt: now},
			{ID: "ignored", Type: "completed", CreatedAt: now},
			{ID: "future", Type: "breached", IssueID: "future-issue", SLAID: "", CreatedAt: now.Add(time.Hour)},
			{ID: "missing", Type: "breached", IssueID: "missing-issue", SLAID: "missing", CreatedAt: now.Add(-15 * time.Minute)},
		},
		Users:     []domain.User{{ID: "unused", JobTitle: wide}},
		Documents: []domain.Document{{ID: "unused", Content: wide}},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	view.lastWorkspaceKey = "physical"
	for _, workspace := range []string{"physical", ""} {
		for _, resource := range []string{"app_user_notifications", "issue_sla"} {
			read, found, err := view.BeginApplicationWebhookSweepRead(t.Context(), workspace, resource)
			if err != nil || !found || !reflect.DeepEqual(read.Data.Webhooks, scaleP21ApplicationSweepHooks(data, resource)) || read.Data.Workspace.ID != data.Workspace.ID {
				t.Fatal("sweep hook eligibility/native values changed", workspace, resource, found, err)
			}
			if read.Data.Users != nil || read.Data.Documents != nil || view.metadataResidency.work.used == 0 {
				t.Fatal("read lacks output ownership or loaded unrelated data")
			}
			read.Close()
			read.Close()
		}
		read, found, err := view.BeginApplicationSLAEventRead(t.Context(), workspace, now)
		if err != nil || !found {
			t.Fatal(found, err)
		}
		want := []ApplicationSLAWebhookEvent{
			{Key: "set", Action: "set", IssueID: "event-issue", SLA: last, At: now},
			{Key: "future", Action: "breached", IssueID: "future-issue", SLA: empty, At: now.Add(time.Hour)},
			{Key: "missing", Action: "breached", IssueID: "missing-issue", At: now.Add(-15 * time.Minute)},
		}
		for _, sla := range []domain.IssueSLA{first, empty, long} {
			want = append(want, ApplicationSLAWebhookEvent{Key: sla.ID + ":highRisk:" + sla.DueAt.UTC().Format(time.RFC3339), Action: "highRisk", IssueID: sla.IssueID, SLA: sla, At: now})
		}
		gotJSON, err := json.Marshal(read.Events)
		if err != nil {
			t.Fatal(err)
		}
		wantJSON, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("last-wins SLA index or event/high-risk order changed:\ngot=%+v\nwant=%+v", read.Events, want)
		}
		read.Close()
		read.Close()
		if read.Events != nil || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("sweep read leaked ownership or decoded metadata")
		}
	}
}

func TestScaleP21ApplicationWebhookSweepCapturedAndFailure(t *testing.T) {
	now := time.Date(2026, 11, 14, 1, 2, 3, 4, time.UTC)
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "old"}, SLAEvents: []domain.SLAEvent{{ID: "old", Type: "started", CreatedAt: now}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data.Workspace.ID, data.SLAEvents[0].ID = "new", "new"
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	read, found, err := view.BeginApplicationSLAEventRead(pinned, "physical", now)
	if err != nil || !found || len(read.Events) != 1 || read.Events[0].Key != "new" {
		t.Fatal("second independent lookup reused caller generation", found, err)
	}
	parent.Close()
	if read.Events[0].Key != "new" || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("independent output ownership was borrowed")
	}
	read.Close()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginApplicationSLAEventRead(canceled, "physical", now); !errors.Is(err, context.Canceled) {
		t.Fatal("SLA cancellation ignored", err)
	}
	if _, _, err := view.BeginApplicationWebhookSweepRead(canceled, "physical", "issue_sla"); !errors.Is(err, context.Canceled) {
		t.Fatal("hook cancellation ignored", err)
	}
	if _, found, err := view.BeginApplicationSLAEventRead(t.Context(), "missing", now); err != nil || found {
		t.Fatal("missing source changed", found, err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if _, _, err := view.BeginApplicationSLAEventRead(t.Context(), "physical", now); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("SLA quota ignored", err)
	}
	if _, _, err := view.BeginApplicationWebhookSweepRead(t.Context(), "physical", "issue_sla"); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("hook quota ignored", err)
	}
	view.metadataResidency.work.limit = limit
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			read, _, err := view.BeginApplicationSLAEventRead(t.Context(), "physical", now)
			if err != nil {
				t.Error(err)
				return
			}
			read.Close()
		})
	}
	group.Wait()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("failed/concurrent read leaked work")
	}
}

func TestScaleP21ApplicationWebhookSweepWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	now := time.Date(2026, 11, 14, 1, 2, 3, 4, time.UTC)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	wantWork := map[bool]int{}
	for _, scale := range []int{1, 10} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "webhooks", "issueSlas", "slaEvents", "documents"), workspace, "scale_p21.application_sweep_fixture", "", nil, func(data *domain.Bootstrap) error {
			data.Webhooks = []domain.Webhook{{ID: "sweep-hook", Enabled: true, AuditLog: true, TeamRestriction: "all", ManagedBy: "application", ResourceTypes: []string{"app_user_notifications"}, Name: strings.Repeat("unrelated name ", scale*20000)}}
			data.Documents = []domain.Document{{ID: "sweep-document", Content: strings.Repeat("unrelated body ", scale*20000)}}
			data.IssueSLAs = []domain.IssueSLA{{ID: "sweep-sla", RuleID: "rule", IssueID: "issue", Status: "active", StartedAt: now.Add(-5 * time.Hour), DueAt: now.Add(time.Hour)}}
			data.SLAEvents = []domain.SLAEvent{{ID: "sweep-event", SLAID: "sweep-sla", IssueID: "issue", Type: "started", CreatedAt: now}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			before, _ := f.WorkspaceMetadataFields(workspace, "webhooks", "issueSlas", "slaEvents")
			sql := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { sql++ })
			hooks, found, err := view.BeginApplicationWebhookSweepRead(ctx, workspace, "app_user_notifications")
			if err != nil || !found || !reflect.DeepEqual(hooks.Data.Webhooks, scaleP21ApplicationSweepHooks(before, "app_user_notifications")) {
				t.Fatal("warm/reloaded hook header changed", scale, reload, found, err)
			}
			events, found, err := view.BeginApplicationSLAEventRead(ctx, workspace, now)
			if err != nil || !found || len(events.Events) != 2 || events.Events[0].Key != "sweep-event" || events.Events[1].Action != "highRisk" {
				t.Fatal("warm/reloaded SLA events changed", scale, reload, found, err)
			}
			held := view.MetadataWorkUsedForTesting()
			if scale == 1 {
				wantWork[reload] = held
			} else if held != wantWork[reload] {
				t.Fatal("body10x increased admitted work", reload, held, wantWork[reload])
			}
			hooks.Close()
			events.Close()
			if sql != 0 || view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("unexpected SQL, decode or leaked work", sql)
			}
			t.Logf("scale=%d reload=%v held_work=%d SQL=%d", scale, reload, held, sql)
		}
	}
}
