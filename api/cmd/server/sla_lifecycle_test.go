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

func TestSLATerminalCreationAndReopening(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	data := domain.Bootstrap{SLARules: []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 60}}}
	issue := domain.Issue{ID: "issue", State: domain.WorkflowState{Type: "completed"}}
	applySLARules(&data, &issue, now)
	if sla := data.IssueSLAs[0]; sla.Status != "completed" || sla.CompletedAt == nil || sla.BreachedAt != nil {
		t.Fatalf("terminal creation left an active timer: %+v", sla)
	}
	applySLARules(&data, &issue, now.Add(2*time.Hour))
	if len(data.SLAEvents) != 2 || data.IssueSLAs[0].BreachedAt != nil {
		t.Fatalf("a met timer later breached: events=%+v sla=%+v", data.SLAEvents, data.IssueSLAs[0])
	}
	issue.State.Type = "started"
	applySLARules(&data, &issue, now.Add(10*time.Minute))
	if sla := data.IssueSLAs[0]; sla.Status != "active" || sla.CompletedAt != nil || !sla.DueAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("reopening did not resume the original clock: %+v", sla)
	}
}

func TestSLALateCompletionRetainsBreachAndOnlyNotifiesOnce(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	team := domain.Team{ID: "team"}
	viewer, watcher := domain.User{ID: "viewer"}, domain.User{ID: "watcher"}
	data := domain.Bootstrap{Viewer: viewer, Users: []domain.User{viewer, watcher}, Teams: []domain.Team{team}, SLARules: []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 60}}}
	issue := domain.Issue{ID: "issue", Team: team, Assignee: &viewer, SubscriberIDs: []string{watcher.ID}, State: domain.WorkflowState{Type: "started"}}
	applySLARules(&data, &issue, now)
	issue.State.Type = "completed"
	applySLARules(&data, &issue, now.Add(time.Hour))
	if sla := data.IssueSLAs[0]; sla.Status != "completed" || sla.BreachedAt == nil || sla.CompletedAt == nil {
		t.Fatalf("late completion erased the breach: %+v", sla)
	}
	for _, id := range []string{viewer.ID, watcher.ID} {
		if !slices.ContainsFunc(data.Notifications, func(notification domain.Notification) bool {
			return notification.RecipientID == id && notification.Actor.ID == "sla-automation" && notification.Category == "reminders" && notification.Payload != nil && notification.Payload.EventType == "issue.sla_breached" && notification.Text == "SLA breached"
		}) {
			t.Fatalf("system breach did not reach %s: %+v", id, data.Notifications)
		}
	}
	if len(data.Activities[issue.ID]) != 3 || len(data.Notifications) != 4 {
		t.Fatalf("breach routing duplicated or lost events: %+v %+v", data.Activities, data.Notifications)
	}
	data.Issues = []domain.Issue{issue}
	for _, notification := range data.Notifications {
		if notification.Payload != nil && notification.Payload.EventType == "issue.sla_breached" {
			message, err := notificationSummary(&data, "https://flow.example/workspace", notification)
			if err != nil || message.Line != "SLA breached" {
				t.Fatalf("breach email lost event text: %+v %v", message, err)
			}
		}
	}
	applySLARules(&data, &issue, now.Add(2*time.Hour))
	if len(data.Activities[issue.ID]) != 3 || len(data.Notifications) != 4 || len(data.SLAEvents) != 4 {
		t.Fatal("repeated completion duplicated the breach")
	}
}

func TestSLABreachHonorsPreferencesAndPrivateTeamAccess(t *testing.T) {
	now := time.Now().UTC()
	team := domain.Team{ID: "private", Private: true}
	owner, outside, muted := domain.User{ID: "owner"}, domain.User{ID: "outside"}, domain.User{ID: "muted"}
	prefs := defaultPreferences(muted.ID)
	prefs.Inbox.Enabled = false
	data := domain.Bootstrap{Viewer: owner, Teams: []domain.Team{team}, Users: []domain.User{owner, outside, muted}, NotificationPreferences: map[string]domain.NotificationPreferences{muted.ID: prefs}, SLARules: []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 1}}}
	issue := domain.Issue{ID: "issue", Creator: owner, Team: team, Assignee: &owner, SubscriberIDs: []string{outside.ID, muted.ID}, State: domain.WorkflowState{Type: "started"}}
	applySLARules(&data, &issue, now)
	applySLARules(&data, &issue, now.Add(time.Minute))
	if len(data.Notifications) != 2 || slices.ContainsFunc(data.Notifications, func(notification domain.Notification) bool { return notification.RecipientID != owner.ID }) {
		t.Fatalf("breach ignored preference or private issue access: %+v", data.Notifications)
	}
}

func TestSLARuleTargetEditRetimesWithoutRestartingAndCanceledHasNoSLA(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	data := domain.Bootstrap{SLARules: []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 1440}}}
	issue := domain.Issue{ID: "issue", State: domain.WorkflowState{Type: "started"}}
	applySLARules(&data, &issue, now)
	id := data.IssueSLAs[0].ID
	data.SLARules[0].TargetMinutes = 720
	applySLARules(&data, &issue, now.Add(time.Hour))
	sla := data.IssueSLAs[0]
	if sla.ID != id || !sla.StartedAt.Equal(now) || !sla.DueAt.Equal(now.Add(12*time.Hour)) || sla.TargetMinutes != 720 {
		t.Fatalf("target edit restarted or did not retime SLA: %+v", sla)
	}
	if event := data.Activities[issue.ID][len(data.Activities[issue.ID])-1]; event.Type != "issue.sla_changed" || event.Metadata["previousTargetMinutes"] != "1440" {
		t.Fatalf("target edit activity = %+v", event)
	}
	issue.State.Type = "canceled"
	applySLARules(&data, &issue, now.Add(2*time.Hour))
	if len(activeSLARules(data, issue.ID)) != 0 {
		t.Fatal("canceled issue kept its SLA")
	}
	count := len(data.SLAEvents)
	applySLARules(&data, &issue, now.Add(3*time.Hour))
	if len(data.SLAEvents) != count {
		t.Fatal("repeated cancellation emitted another removal")
	}
}

func TestSLABusinessDayThresholdsSkipWeekendsAndPreserveWallTimeAcrossDST(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 6, 16, 0, 0, 0, zone)
	data := domain.Bootstrap{TeamSettings: map[string]domain.TeamSettings{"team": {Timezone: zone.String()}}, SLARules: []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 1440, BusinessHours: true}}}
	issue := domain.Issue{ID: "issue", Team: domain.Team{ID: "team"}, State: domain.WorkflowState{Type: "started"}}
	applySLARules(&data, &issue, now)
	sla := data.IssueSLAs[0]
	if want := time.Date(2026, 3, 9, 16, 0, 0, 0, zone); !sla.DueAt.Equal(want) {
		t.Fatalf("business day shifted wall clock across DST: %s want %s", sla.DueAt, want)
	}
	if sla.HighRiskAt == nil || !sla.HighRiskAt.Equal(now) || sla.MediumRiskAt == nil || !sla.MediumRiskAt.Equal(time.Date(2026, 2, 26, 16, 0, 0, 0, zone)) {
		t.Fatalf("business-day thresholds = high %v medium %v", sla.HighRiskAt, sla.MediumRiskAt)
	}
}

func TestSLACalendarPausePreservesSubMinuteDuration(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	data := domain.Bootstrap{SLARules: []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 60, PauseStatuses: []string{"backlog"}}}}
	issue := domain.Issue{ID: "issue", State: domain.WorkflowState{Type: "started"}}
	applySLARules(&data, &issue, now)
	issue.State.Type = "backlog"
	applySLARules(&data, &issue, now.Add(15*time.Minute))
	if sla := data.IssueSLAs[0]; sla.Status != "paused" || sla.RemainingMinutes != 45 {
		t.Fatalf("calendar pause did not freeze remaining time: %+v", sla)
	}
	issue.State.Type = "started"
	applySLARules(&data, &issue, now.Add(20*time.Minute+30*time.Second))
	if got := data.IssueSLAs[0].DueAt; !got.Equal(now.Add(65*time.Minute + 30*time.Second)) {
		t.Fatalf("resume lost the partial paused minute: %s", got)
	}
}

func TestSLARuleRejectsInvalidFiltersAndOverflow(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	for _, filters := range []map[string]any{{"prioirty": 1}, {"priority": 1.5}, {"priority": 5}, {"priority": nil}, {"priority": []int{1}}, {"status": "missing"}, {"label": "missing"}, {"project": "missing"}} {
		requestJSON[map[string]any](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Invalid", "filters": filters}, http.StatusBadRequest)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Overflow", "targetMinutes": int64(1 << 62)}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Invalid start", "startMode": "tomorrow"}, http.StatusBadRequest)
	if slaMatches(domain.SLARule{Enabled: true, Filters: map[string]any{"unknown": "value"}}, domain.Issue{}) {
		t.Fatal("unknown persisted filters matched all issues")
	}
}

func TestSLAStartModeUsesIssueCreationAndRebasesOnEdit(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	issue := domain.Issue{ID: "issue", CreatedAt: now.Add(-2 * time.Hour), State: domain.WorkflowState{Type: "started"}}
	data := domain.Bootstrap{SLARules: []domain.SLARule{{ID: "rule", Enabled: true, StartMode: "issueCreation", TargetMinutes: 60}}}
	applySLARules(&data, &issue, now)
	sla := data.IssueSLAs[0]
	if !sla.StartedAt.Equal(issue.CreatedAt) || !sla.DueAt.Equal(issue.CreatedAt.Add(time.Hour)) || sla.Status != "breached" || sla.StartMode != "issueCreation" {
		t.Fatalf("creation-mode SLA ignored issue age: %+v", sla)
	}
	data.SLARules[0].StartMode = "ruleMatch"
	applySLARules(&data, &issue, now.Add(time.Hour))
	if updated := data.IssueSLAs[0]; updated.ID != sla.ID || !updated.StartedAt.Equal(now.Add(time.Hour)) || updated.Status != "active" || updated.BreachedAt != nil || updated.StartMode != "ruleMatch" {
		t.Fatalf("start-mode edit did not rebase the clock: %+v", updated)
	}
}

func TestSLAManualDeadlineOnlyPatchPersistsAndCanClear(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	issue := repository.Bootstrap().Issues[0]
	deadline := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"slaBreachesAt": deadline.Format(time.RFC3339), "slaType": "onlyBusinessDays"}, http.StatusOK)
	if updated.SLABreachesAt == nil || !updated.SLABreachesAt.Equal(deadline) || updated.SLAType != "onlyBusinessDays" {
		t.Fatalf("manual SLA-only patch was discarded: %+v", updated)
	}
	updated = requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"slaBreachesAt": "", "slaType": ""}, http.StatusOK)
	if updated.SLABreachesAt != nil || updated.SLAType != "" {
		t.Fatalf("manual SLA-only clear was discarded: %+v", updated)
	}
}

func TestSLAEnableReevaluatesExistingIssuesAndDeleteHandsOffToNextRule(t *testing.T) {
	for _, mode := range []string{"scoped", "fallback", "full"} {
		t.Run(mode, func(t *testing.T) {
			restore := store.ForceFullMutationsForTesting(mode == "full")
			defer restore()
			repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repository.Close()
			handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
			data := repository.Bootstrap()
			teamIDs := []string{data.Teams[0].ID}
			if mode == "fallback" {
				teamIDs = []string{}
			}
			requestJSON[map[string]any](t, handler, http.MethodPut, "/api/sla-settings", map[string]any{"enabled": false}, http.StatusOK)
			first := requestJSON[domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "First", "teamIds": teamIDs, "filters": map[string]any{"priority": 1}, "targetMinutes": 1440}, http.StatusCreated)
			issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "SLA enable flow", "teamId": data.Teams[0].ID, "priority": 1}, http.StatusCreated)
			if got := activeSLARules(repository.Bootstrap(), issue.ID); len(got) != 0 {
				t.Fatalf("disabled workspace attached SLA: %v", got)
			}
			requestJSON[map[string]any](t, handler, http.MethodPut, "/api/sla-settings", map[string]any{"enabled": true}, http.StatusOK)
			if got := activeSLARules(repository.Bootstrap(), issue.ID); !slices.Equal(got, []string{first.ID}) {
				t.Fatalf("enabling did not attach to existing issue: %v", got)
			}
			second := requestJSON[domain.SLARule](t, handler, http.MethodPost, "/api/sla-rules", map[string]any{"name": "Fallback", "teamIds": teamIDs, "filters": map[string]any{"priority": 1}, "targetMinutes": 720}, http.StatusCreated)
			requestJSON[map[string]any](t, handler, http.MethodDelete, "/api/sla-rules/"+first.ID, nil, http.StatusNoContent)
			after := repository.Bootstrap()
			if got := activeSLARules(after, issue.ID); !slices.Equal(got, []string{second.ID}) {
				t.Fatalf("delete did not apply fallback: %v", got)
			}
			for _, event := range after.SLAEvents {
				if !slices.ContainsFunc(after.IssueSLAs, func(sla domain.IssueSLA) bool { return sla.ID == event.SLAID }) {
					t.Fatalf("delete left an orphan SLA event: %+v", event)
				}
			}
		})
	}
}

func TestSLASchedulerEmitsBreachWithoutPageLoads(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	data := repository.Bootstrap()
	issue := data.Issues[0]
	now := time.Now().UTC()
	if err := repository.MutateWorkspace(t.Context(), data.Workspace.URLKey, "test.sla_deadline", "", nil, func(current *domain.Bootstrap) error {
		current.SLARules = []domain.SLARule{{ID: "rule", Enabled: true, TargetMinutes: 60}}
		current.IssueSLAs = []domain.IssueSLA{{ID: "sla", IssueID: issue.ID, RuleID: "rule", Status: "active", TargetMinutes: 60, StartedAt: now.Add(-2 * time.Hour), DueAt: now.Add(-time.Hour)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := server{store: repository}
	s.runSLATick(t.Context())
	after := repository.Bootstrap()
	if len(after.IssueSLAs) != 1 || after.IssueSLAs[0].Status != "breached" || after.IssueSLAs[0].BreachedAt == nil || !slaEventRecorded(&after, "sla", "breached") {
		t.Fatalf("idle SLA did not breach: %+v %+v", after.IssueSLAs, after.SLAEvents)
	}
	s.runSLATick(t.Context())
	if again := repository.Bootstrap(); len(again.SLAEvents) != len(after.SLAEvents) || len(again.Notifications) != len(after.Notifications) {
		t.Fatal("repeated scheduler tick duplicated breach events")
	}
}
