package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestSLAManualOverrideEditsCurrentTimerAndRemovalSuppressesAutomation(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	data := domain.Bootstrap{SLARules: []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 1440, Filters: map[string]any{"priority": 1}}}}
	issue := domain.Issue{ID: "issue", Priority: 1, CreatedAt: now, State: domain.WorkflowState{Type: "started"}}
	applySLARules(&data, &issue, now)
	id := data.IssueSLAs[0].ID
	deadline := now.Add(3 * time.Hour)
	issue.SLABreachesAt = &deadline
	applyManualSLADeadline(&data, &issue, now.Add(time.Hour))
	applySLARules(&data, &issue, now.Add(time.Hour))
	if sla := data.IssueSLAs[0]; !sla.Manual || sla.ID != id || !sla.DueAt.Equal(deadline) {
		t.Fatalf("manual edit was overwritten by automatic target: %+v", sla)
	}
	issue.SLABreachesAt = nil
	applyManualSLADeadline(&data, &issue, now.Add(2*time.Hour))
	applySLARules(&data, &issue, now.Add(2*time.Hour))
	if got := activeSLARules(data, issue.ID); len(got) != 0 {
		t.Fatalf("removing SLA immediately restored automation: %v", got)
	}
	issue.Priority = 3
	applySLARules(&data, &issue, now.Add(3*time.Hour))
	issue.Priority = 1
	applySLARules(&data, &issue, now.Add(4*time.Hour))
	if sla := data.IssueSLAs[0]; sla.Manual || sla.Status != "active" || sla.ID == id {
		t.Fatalf("re-escalation did not restore automation: %+v", sla)
	}
}

func TestSLARemovedStandaloneManualDoesNotBlockLaterMatchingRule(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	data := domain.Bootstrap{}
	issue := domain.Issue{ID: "issue", Priority: 3, State: domain.WorkflowState{Type: "started"}}
	deadline := now.Add(12 * time.Hour)
	issue.SLABreachesAt = &deadline
	applyManualSLADeadline(&data, &issue, now)
	issue.SLABreachesAt = nil
	applyManualSLADeadline(&data, &issue, now.Add(time.Hour))
	data.SLARules = []domain.SLARule{{ID: "urgent", Enabled: true, TargetMinutes: 1440, Filters: map[string]any{"priority": 1}}}
	applySLARules(&data, &issue, now.Add(2*time.Hour))
	if len(activeSLARules(data, issue.ID)) != 0 {
		t.Fatal("nonmatching issue unexpectedly acquired SLA")
	}
	issue.Priority = 1
	applySLARules(&data, &issue, now.Add(3*time.Hour))
	if got := activeSLARules(data, issue.ID); !slices.Equal(got, []string{"urgent"}) {
		t.Fatalf("removed manual SLA blocked later escalation: %v", got)
	}
}

func TestSLAManualStandaloneUsesLifecycleAndSchedulerReminders(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "sla-manual.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := server{store: repository, authDisabled: true, uploadPath: t.TempDir()}
	handler := newHandler(&s)
	data := repository.Bootstrap()
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Manual SLA", "teamId": data.Teams[0].ID, "assigneeId": data.Viewer.ID, "subscriberIds": []string{data.Viewer.ID}}, http.StatusCreated)
	deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"slaBreachesAt": deadline.Format(time.RFC3339)}, http.StatusOK)
	after := repository.Bootstrap()
	index := slices.IndexFunc(after.IssueSLAs, func(sla domain.IssueSLA) bool { return sla.IssueID == issue.ID && sla.Manual })
	if index < 0 || after.IssueSLAs[index].RuleID != "" || !after.IssueSLAs[index].DueAt.Equal(deadline) {
		t.Fatalf("manual SLA did not create a standalone timer: %+v", after.IssueSLAs)
	}
	if err := s.maintainSLADeadlines(t.Context(), data.Workspace.URLKey, deadline); err != nil {
		t.Fatal(err)
	}
	after = repository.Bootstrap()
	if after.IssueSLAs[index].BreachedAt == nil || !slices.ContainsFunc(after.Notifications, func(notification domain.Notification) bool {
		return notification.IssueID == issue.ID && notification.RecipientID == data.Viewer.ID && notification.Payload != nil && notification.Payload.EventType == "issue.sla_breached"
	}) {
		t.Fatalf("manual SLA did not deliver breach reminder: %+v %+v", after.IssueSLAs[index], after.Notifications)
	}
	completed := slices.IndexFunc(after.States, func(state domain.WorkflowState) bool {
		return (state.TeamID == "" || state.TeamID == issue.Team.ID) && state.Type == "completed"
	})
	if completed < 0 {
		t.Fatal("fixture lacks completed state")
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"stateId": after.States[completed].ID}, http.StatusOK)
	if sla := repository.Bootstrap().IssueSLAs[index]; sla.Status != "completed" || sla.CompletedAt == nil {
		t.Fatalf("manual SLA did not complete: %+v", sla)
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"stateId": issue.State.ID}, http.StatusOK)
	if sla := repository.Bootstrap().IssueSLAs[index]; sla.CompletedAt != nil || sla.Status == "completed" {
		t.Fatalf("manual SLA did not reopen: %+v", sla)
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"slaBreachesAt": ""}, http.StatusOK)
	if sla := repository.Bootstrap().IssueSLAs[index]; sla.Status != "removed" {
		t.Fatalf("manual SLA did not clear: %+v", sla)
	}
}

func TestSLAManualClearOfAutomaticTimerWithoutIssueFieldPersists(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "sla-clear-auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	data := repository.Bootstrap()
	requestJSON[domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Automatic", "filters": map[string]any{"priority": 1}, "targetMinutes": 1440}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Clear automated SLA", "teamId": data.Teams[0].ID, "priority": 1}, http.StatusCreated)
	if issue.SLABreachesAt != nil {
		t.Fatal("test requires no explicit deadline field")
	}
	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"slaBreachesAt": ""}, http.StatusOK)
	after := repository.Bootstrap()
	if len(activeSLARules(after, issue.ID)) != 0 || updated.Version <= issue.Version {
		t.Fatal("clear without an issue field was discarded as a no-op")
	}
	if !slices.ContainsFunc(after.Activities[issue.ID], func(event domain.ActivityEvent) bool {
		return event.Type == "issue.updated" && event.Metadata["slaActivityRecorded"] == "true"
	}) {
		t.Fatal("manual SLA update did not mark its dedicated activity")
	}
}

func TestSLAAutomaticTimerPersistsConditionChangesThroughIssueRecordWrites(t *testing.T) {
	for _, prefix := range []string{"/api/issues/", "/api/issue-records/"} {
		t.Run(prefix, func(t *testing.T) {
			repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "sla-record-write.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
			data := repository.Bootstrap()
			requestJSON[map[string]any](t, handler, http.MethodPut, "/api/sla-settings", map[string]any{"enabled": true}, http.StatusOK)
			requestJSON[domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Automatic", "filters": map[string]any{"priority": 1}, "targetMinutes": 1440}, http.StatusCreated)
			issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Condition update", "teamId": data.Teams[0].ID, "priority": 1}, http.StatusCreated)
			if len(activeSLARules(repository.Bootstrap(), issue.ID)) != 1 {
				t.Fatal("creation did not attach SLA")
			}
			requestJSON[domain.Issue](t, handler, http.MethodPatch, prefix+issue.ID, map[string]any{"priority": 3}, http.StatusOK)
			if got := activeSLARules(repository.Bootstrap(), issue.ID); len(got) != 0 {
				t.Fatalf("condition removal metadata was discarded: %v", got)
			}
			requestJSON[domain.Issue](t, handler, http.MethodPatch, prefix+issue.ID, map[string]any{"priority": 1}, http.StatusOK)
			if got := activeSLARules(repository.Bootstrap(), issue.ID); len(got) != 1 {
				t.Fatalf("re-escalation metadata was discarded: %v", got)
			}
		})
	}
}
