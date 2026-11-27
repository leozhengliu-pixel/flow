package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The recurring occurrence ticker callback (generateRecurringOccurrence) is
// compared between the literal scoped writer and the bounded scoped image
// writer, warm and freshly reloaded: returned issues and errors, every issue,
// content and metadata row, the issue sequence, domain events, webhooks and
// the ordered realtime publications (the mutation's own event and the
// source/sub-issue entity events).

type recurringOccurrenceCase struct {
	name       string
	sequence   int
	noSequence bool
	// bulk archived sub-issues of the source, imported in batches.
	bulk int
	edit func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time)
	// tick runs the workspace pass instead of one occurrence of the source.
	tick bool
	// want is the number of created issues (instance plus sub-issues); -1
	// for a source-only rewrite and 0 for no write.
	want int
	err  error
}

type recurringOccurrenceOutcome struct {
	Created  any
	Error    string
	Rows     discussionWriteRows
	Metadata []metadataRow
	Sequence [][]any
	Events   []domain.DomainEvent
	Webhooks []json.RawMessage
	Realtime []json.RawMessage
}

var recurringOccurrenceCollaboration = regexp.MustCompile(`collab_\d+_[0-9a-f]{16}`)

func (f automationCycleScheduleFixture) recurringOccurrenceExec(t *testing.T, query string, args ...any) {
	t.Helper()
	f.reader.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, f.data.Workspace.URLKey).Close()
	if f.reader.config.Driver == "postgres" || f.reader.config.Driver == "postgresql" {
		for index := 1; strings.Contains(query, "?"); index++ {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", index), 1)
		}
	}
	if _, err := f.reader.reader.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f automationCycleScheduleFixture) recurringOccurrenceRows(t *testing.T, query string, arg any) [][]any {
	t.Helper()
	rows := f.reader.query(t, query, arg)
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for index, value := range values {
			if raw, ok := value.([]byte); ok {
				values[index] = string(raw)
			}
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

// recurringOccurrenceSeed replaces the workspace with the fixture's baseline plus a
// recurring source, its sub-issue templates and the case's edit, sets the
// issue sequence, and refreshes the bounded view.
func recurringOccurrenceSeed(t *testing.T, f automationCycleScheduleFixture, test recurringOccurrenceCase, now time.Time, reload bool) {
	t.Helper()
	for _, source := range []*store.SQLiteStore{f.repository, f.probe} {
		source.SetWebhookSink(nil)
		source.SetRealtimeSink(nil)
	}
	if err := f.repository.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	restore := store.ForceFullMutationsForTesting(true)
	err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw206_fixture", "", nil, func(data *domain.Bootstrap) error {
		base := recurringOccurrenceClone(t, f.base)
		*data = base
		data.Issues, data.Activities, data.Comments = []domain.Issue{}, map[string][]domain.ActivityEvent{}, map[string][]domain.Comment{}
		data.Notifications, data.NotificationDeliveries = []domain.Notification{}, []domain.NotificationDelivery{}
		recurringOccurrenceFamily(f, data, now)
		if test.edit != nil {
			test.edit(f, data, now)
		}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	sourceID := f.recurringOccurrenceId("source")
	for start := 0; start < test.bulk; start += 1000 {
		var batch []domain.Issue
		for index := start; index < min(start+1000, test.bulk); index++ {
			at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			batch = append(batch, domain.Issue{
				ID: fmt.Sprintf("%sbulk-%05d", f.prefix, index), Number: 5000 + index, Identifier: fmt.Sprintf("REC-%d", 5000+index),
				Title: "Archived bulk child", Team: domain.Team{ID: f.team, Key: "REC", Name: "Recurring team"},
				State:   domain.WorkflowState{ID: f.recurringOccurrenceId("todo"), Name: "Todo", Type: "unstarted", TeamID: f.team, Position: 1, Default: true},
				Creator: f.base.Viewer, ParentID: &sourceID, ArchivedAt: &at, Version: 1, CreatedAt: at, UpdatedAt: at,
			})
		}
		if err := f.repository.ImportIssues(t.Context(), f.data.Workspace.URLKey, batch); err != nil {
			t.Fatal(err)
		}
	}
	sequence := test.sequence
	if sequence == 0 {
		sequence = 1999
	}
	f.recurringOccurrenceExec(t, `UPDATE issue_number_sequences SET last_number=? WHERE workspace_key=?`, sequence, f.data.Workspace.URLKey)
	if test.noSequence {
		f.recurringOccurrenceExec(t, `DELETE FROM issue_number_sequences WHERE workspace_key=?`, f.data.Workspace.URLKey)
	}
	if reload {
		if err := f.repository.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
}

func recurringOccurrenceClone(t *testing.T, data domain.Bootstrap) domain.Bootstrap {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var result domain.Bootstrap
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func (f automationCycleScheduleFixture) recurringOccurrenceId(name string) string {
	return f.prefix + name
}

// recurringOccurrenceFamily seeds a weekly source due on 2026-09-07 in a triage
// team with labels, an assignee, a rich description and document content,
// and three sub-issue templates in explicit order (one archived, one in a
// private team, one in an archived team), an existing recent edit history
// and an unrelated high-numbered issue outside the scope.
func recurringOccurrenceFamily(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
	team := domain.Team{ID: f.team, Key: "REC", Name: "Recurring team"}
	private := domain.Team{ID: f.recurringOccurrenceId("private"), Key: "RPRV", Name: "Private recurring team", Private: true}
	archivedTeam := domain.Team{ID: f.recurringOccurrenceId("archived-team"), Key: "RARC", Name: "Archived team", ArchivedAt: &now}
	data.Teams = append(data.Teams, team, private, archivedTeam)
	if data.TeamSettings == nil {
		data.TeamSettings = map[string]domain.TeamSettings{}
	}
	data.TeamSettings[team.ID] = domain.TeamSettings{TeamID: team.ID, Timezone: "America/Los_Angeles", TriageEnabled: true, Access: "public"}
	data.TeamSettings[private.ID] = domain.TeamSettings{TeamID: private.ID, Timezone: "UTC", Access: "private"}
	backlog := domain.WorkflowState{ID: f.recurringOccurrenceId("backlog"), Name: "Backlog", Type: "backlog", TeamID: team.ID, Position: 0}
	todo := domain.WorkflowState{ID: f.recurringOccurrenceId("todo"), Name: "Todo", Type: "unstarted", TeamID: team.ID, Position: 1, Default: true}
	privateTodo := domain.WorkflowState{ID: f.recurringOccurrenceId("private-todo"), Name: "Todo", Type: "unstarted", TeamID: private.ID, Position: 0}
	data.States = append(data.States, backlog, todo, privateTodo)
	settings := data.TeamSettings[team.ID]
	settings.DefaultStateID = backlog.ID
	data.TeamSettings[team.ID] = settings
	label := domain.IssueLabel{ID: f.recurringOccurrenceId("label"), Name: "Chore", Scope: team.ID}
	archivedLabel := domain.IssueLabel{ID: f.recurringOccurrenceId("archived-label"), Name: "Old", ArchivedAt: &now}
	data.Labels = append(data.Labels, label, archivedLabel)
	viewer := data.Viewer
	assignee := f.actor
	due := "2026-09-07"
	dueAt := recurrenceCreationInstant(day(due), teamLocation("America/Los_Angeles"))
	sourceID := f.recurringOccurrenceId("source")
	estimate := 3.0
	source := domain.Issue{
		ID: sourceID, Version: 4, Number: 1000, Identifier: "REC-1000", Title: "Weekly report", Description: "Exact recurring text",
		DescriptionState: "legacy-state", Team: team, Creator: viewer, State: todo, Priority: 2, PriorityLabel: "High", Estimate: &estimate,
		Labels: []domain.IssueLabel{label, archivedLabel}, Assignee: &assignee, SubscriberIDs: []string{viewer.ID, assignee.ID},
		Recurrence: "weekly", DueDate: &due, NextOccurrenceAt: &dueAt, Icon: "repeat",
		SubIssueIDs: []string{f.recurringOccurrenceId("child-b"), f.recurringOccurrenceId("child-a")},
		DocumentContent: &domain.DocumentContent{ID: f.recurringOccurrenceId("content"), Version: 2, Content: "Copied body", ContentState: "must-clear",
			ContentData: map[string]any{"type": "doc", "content": []any{map[string]any{"value": "kept"}}}},
		CreatedAt: now.Add(-30 * 24 * time.Hour), UpdatedAt: now.Add(-time.Hour), Reactions: map[string][]string{}, Relations: []domain.IssueRelation{},
	}
	child := func(id string, number int, title string, team domain.Team, state domain.WorkflowState) domain.Issue {
		issue := source
		issue.ID, issue.Number, issue.Identifier, issue.Title, issue.Team, issue.State = id, number, fmt.Sprintf("%s-%d", team.Key, number), title, team, state
		issue.ParentID, issue.Recurrence, issue.NextOccurrenceAt, issue.DueDate, issue.SubIssueIDs, issue.Icon = &sourceID, "", nil, nil, nil, ""
		issue.Assignee, issue.Labels, issue.DocumentContent, issue.Estimate = nil, []domain.IssueLabel{label}, nil, nil
		return issue
	}
	childA := child(f.recurringOccurrenceId("child-a"), 1001, "Native first child", team, todo)
	childA.Assignee = &viewer
	childB := child(f.recurringOccurrenceId("child-b"), 2500, "Explicit first child", private, privateTodo)
	childB.Description = "Private child body"
	childC := child(f.recurringOccurrenceId("child-c"), 1003, "Archived team child", archivedTeam, todo)
	archivedChild := child(f.recurringOccurrenceId("child-archived"), 1004, "Archived child", team, todo)
	archivedChild.ArchivedAt = &now
	unrelated := child(f.recurringOccurrenceId("unrelated"), 3000, "Unrelated high number", team, todo)
	unrelated.ParentID = nil
	data.Issues = append(data.Issues, childA, source, childB, childC, archivedChild, unrelated)
	data.Activities[sourceID] = []domain.ActivityEvent{{ID: f.recurringOccurrenceId("history"), Type: "issue.updated", Actor: viewer,
		CreatedAt: now.Add(-20 * time.Minute), Metadata: domain.ActivityMetadata{"priority": "2", "priorityBefore": "3"}}}
	data.Comments[sourceID] = []domain.Comment{{ID: f.recurringOccurrenceId("comment"), User: viewer, Body: "Kept comment", CreatedAt: now.Add(-40 * time.Minute)}}
}

func recurringOccurrenceNormalize(t *testing.T, value any, started time.Time) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := recurringOccurrenceCollaboration.ReplaceAllString(string(raw), "<collaboration>")
	text = scaleTriageStamp.ReplaceAllString(text, "<stamp>")
	text = scaleTriageClock.ReplaceAllStringFunc(text, func(clock string) string {
		if at, err := time.Parse(time.RFC3339Nano, clock); err == nil && !at.Before(started) {
			return "<now>"
		}
		return clock
	})
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	// SQL content rows are a set ordered by generated IDs on ties.
	if rows, ok := decoded["Rows"].(map[string]any); ok {
		if content, ok := rows["Content"].([]any); ok {
			sort.SliceStable(content, func(i, j int) bool {
				a, _ := json.Marshal(content[i])
				b, _ := json.Marshal(content[j])
				return bytes.Compare(a, b) < 0
			})
		}
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(normalized)
}

func recurringOccurrenceRun(t *testing.T, f automationCycleScheduleFixture, source *store.SQLiteStore, test recurringOccurrenceCase, now time.Time, call func(*server) (any, error)) (recurringOccurrenceOutcome, time.Time) {
	t.Helper()
	priorIDs := map[string]bool{}
	for _, event := range automationCycleScheduleEvents(t, f) {
		priorIDs[event.ID] = true
	}
	service := &server{store: source, realtime: newRealtimeHub()}
	var got recurringOccurrenceOutcome
	var mu sync.Mutex
	source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
			t.Error("recurring webhook lost its output owner")
		}
		got.Webhooks = append(got.Webhooks, discussionRoutesEnvelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	// Notification signals are published by the store that owns the SQL
	// handle at commit.
	source.SetRealtimeSink(service.publishRealtime)
	f.repository.SetRealtimeSink(service.publishRealtime)
	started := time.Now().UTC()
	created, err := call(service)
	source.SetWebhookSink(nil)
	source.SetRealtimeSink(nil)
	f.repository.SetRealtimeSink(nil)
	service.realtime.mu.Lock()
	for _, event := range service.realtime.history[f.data.Workspace.URLKey] {
		got.Realtime = append(got.Realtime, discussionRoutesEnvelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID, "clientId": event.ClientID,
			"payload": event.Payload, "journaled": event.Seq > 0,
		}))
	}
	service.realtime.mu.Unlock()
	got.Created, got.Error = created, scaleChatPointError(err)
	got.Rows = discussionWriteSql(t, f.notificationsFixture)
	got.Metadata = automationCycleScheduleMetadata(t, f)
	got.Sequence = f.recurringOccurrenceRows(t, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, f.data.Workspace.URLKey)
	for _, event := range automationCycleScheduleEvents(t, f) {
		if !priorIDs[event.ID] {
			got.Events = append(got.Events, event)
		}
	}
	if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
		t.Fatal("recurring occurrence retained work or decoded complete metadata", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
	}
	return got, started
}

func recurringOccurrenceCall(f automationCycleScheduleFixture, test recurringOccurrenceCase, now time.Time) func(*server) (any, error) {
	return func(service *server) (any, error) {
		if test.tick {
			ctx := context.Background()
			if service.store.BoundedMetadataEnabled() {
				var closeOutputs func()
				ctx, closeOutputs = beginIndependentMetadataOutputReads(ctx)
				defer closeOutputs()
			}
			return service.generateWorkspaceRecurringIssues(ctx, f.data.Workspace.URLKey, now)
		}
		created, err := service.createRecurringOccurrence(context.Background(), f.data.Workspace.URLKey, f.recurringOccurrenceId("source"), now)
		return created, err
	}
}

func recurringOccurrenceDifferential(t *testing.T, f automationCycleScheduleFixture, test recurringOccurrenceCase, now time.Time) recurringOccurrenceOutcome {
	t.Helper()
	var expected string
	var legacy recurringOccurrenceOutcome
	for _, mode := range []struct {
		name            string
		bounded, reload bool
	}{{"legacy", false, false}, {"warm", true, false}, {"legacy-reloaded", false, true}, {"reloaded", true, true}} {
		recurringOccurrenceSeed(t, f, test, now, mode.reload)
		source := f.repository
		if mode.bounded {
			source = f.probe
			if mode.reload {
				var err error
				source, err = f.repository.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		got, started := recurringOccurrenceRun(t, f, source, test, now, recurringOccurrenceCall(f, test, now))
		normalized := recurringOccurrenceNormalize(t, got, started)
		if mode.name == "legacy" {
			expected, legacy = normalized, got
			continue
		}
		if normalized != expected {
			t.Fatalf("%s: %s differs: %s", test.name, mode.name, policyFirstDifference(expected, normalized))
		}
	}
	return legacy
}

func TestRecurringOccurrenceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := automationCycleScheduleSetup(t)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	// A recent edit the source's history update merges into: real time, one
	// value for every mode of the case.
	recent := time.Now().UTC().Add(-2 * time.Minute)
	source := func(data *domain.Bootstrap) *domain.Issue {
		for index := range data.Issues {
			if data.Issues[index].ID == f.recurringOccurrenceId("source") {
				return &data.Issues[index]
			}
		}
		panic("recurring source missing")
	}
	cases := []recurringOccurrenceCase{
		{name: "weekly-catch-up-with-sub-issues", want: 4},
		{name: "daily", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			source(data).Recurrence = "daily"
		}},
		{name: "monthly-day-31", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			due := "2026-08-31"
			at := recurrenceCreationInstant(day(due), teamLocation("America/Los_Angeles"))
			issue := source(data)
			issue.Recurrence, issue.DueDate, issue.NextOccurrenceAt = "FREQ=MONTHLY;BYMONTHDAY=31", &due, &at
		}},
		{name: "rrule-weekly-interval", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			source(data).Recurrence = "FREQ=WEEKLY;INTERVAL=2"
		}},
		{name: "series-occurrence-kept", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			issue := source(data)
			issue.RecurrenceSeriesID, issue.RecurrenceOccurrence = f.recurringOccurrenceId("series"), "2026-09-07"
		}},
		{name: "no-sub-issues", want: 1, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool { return issue.ParentID != nil })
		}},
		{name: "private-source-team", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			for index := range data.Teams {
				if data.Teams[index].ID == f.team {
					data.Teams[index].Private = true
				}
			}
			settings := data.TeamSettings[f.team]
			settings.Access = "private"
			data.TeamSettings[f.team] = settings
		}},
		{name: "sequence-ahead-of-issues", sequence: 4999, want: 4},
		{name: "missing-sequence-row", noSequence: true, want: 4},
		{name: "default-team-settings", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			delete(data.TeamSettings, f.team)
		}},
		{name: "assignee-removed", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			gone := domain.User{ID: f.recurringOccurrenceId("gone-user"), Name: "Gone"}
			source(data).Assignee = &gone
		}},
		{name: "project-cycle-sla", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			project := domain.Project{ID: f.project, Name: "Recurring project", Icon: "box", Color: "#123456", TeamIDs: []string{f.team}, CreatedAt: now, UpdatedAt: now}
			data.Projects = append(data.Projects, project)
			source(data).Project = &domain.ProjectSummary{ID: project.ID, Name: project.Name}
			data.CycleSettings = map[string]domain.CycleSettings{f.team: {Enabled: true, AutoAddDueDate: true}}
			data.Cycles = append(data.Cycles, domain.Cycle{ID: f.recurringOccurrenceId("cycle"), TeamID: f.team, Status: "current", Number: 7,
				StartsAt: time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)})
			if data.Settings == nil {
				data.Settings = map[string]any{}
			}
			data.Settings["sla"] = map[string]any{"enabled": true, "workWeek": "monFri"}
			data.SLARules = append(data.SLARules,
				domain.SLARule{ID: f.recurringOccurrenceId("other-team-sla"), Name: "Other", TeamIDs: []string{f.other}, Enabled: true, TargetMinutes: 30},
				domain.SLARule{ID: f.recurringOccurrenceId("sla"), Name: "Recurring", TeamIDs: []string{f.team}, Enabled: true, TargetMinutes: 60, BusinessHours: true},
				domain.SLARule{ID: f.recurringOccurrenceId("later-sla"), Name: "Later", Enabled: true, TargetMinutes: 90})
			data.SLAEvents = append(data.SLAEvents, domain.SLAEvent{ID: f.recurringOccurrenceId("old-sla-event"), IssueID: f.recurringOccurrenceId("unrelated"), Type: "started", CreatedAt: now})
			data.IssueSLAs = append(data.IssueSLAs,
				domain.IssueSLA{ID: f.recurringOccurrenceId("unrelated-sla"), IssueID: f.recurringOccurrenceId("unrelated"), RuleID: f.recurringOccurrenceId("sla"), Status: "active", StartedAt: now, DueAt: now},
				domain.IssueSLA{ID: f.recurringOccurrenceId("colliding-sla"), IssueID: "issue_2501", RuleID: f.recurringOccurrenceId("sla"), Status: "removed", StartedAt: now, DueAt: now},
				domain.IssueSLA{ID: f.recurringOccurrenceId("colliding-later"), IssueID: "issue_2501", RuleID: f.recurringOccurrenceId("later-sla"), Status: "active", StartedAt: now, DueAt: now.Add(time.Hour)})
		}},
		{name: "sla-collision-breached", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			if data.Settings == nil {
				data.Settings = map[string]any{}
			}
			data.Settings["sla"] = map[string]any{"enabled": true}
			data.SLARules = append(data.SLARules, domain.SLARule{ID: f.recurringOccurrenceId("sla"), Name: "Recurring", Enabled: true, TargetMinutes: 60})
			data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: f.recurringOccurrenceId("stale-sla"), IssueID: "issue_2501", RuleID: f.recurringOccurrenceId("sla"),
				Status: "active", StartedAt: now.Add(-48 * time.Hour), DueAt: now.Add(-24 * time.Hour)})
		}},
		{name: "archived-label-group", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			group := domain.LabelGroup{ID: f.recurringOccurrenceId("group"), Name: "Old group", ArchivedAt: &now}
			data.LabelGroups = append(data.LabelGroups, group)
			grouped := domain.IssueLabel{ID: f.recurringOccurrenceId("grouped"), Name: "Grouped", GroupID: group.ID}
			duplicate := domain.IssueLabel{ID: f.recurringOccurrenceId("label"), Name: "Chore duplicate"}
			data.Labels = append(data.Labels, grouped, duplicate)
			issue := source(data)
			issue.Labels = append(issue.Labels, grouped)
		}},
		{name: "merged-source-history", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			data.Activities[f.recurringOccurrenceId("source")] = []domain.ActivityEvent{{ID: f.recurringOccurrenceId("recent-edit"), Type: "issue.updated", Actor: data.Viewer,
				CreatedAt: recent, Metadata: domain.ActivityMetadata{"title": "Weekly report", "titleBefore": "Report"}}}
			data.Comments[f.recurringOccurrenceId("source")] = nil
		}},
		{name: "large-description", want: 4, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			source(data).Description = strings.Repeat("legal recurring body ", 50000)
		}},
		{name: "existing-instance", want: -1, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			instance := *source(data)
			instance.ID, instance.Number, instance.Identifier = f.recurringOccurrenceId("existing"), 1500, "REC-1500"
			instance.Recurrence, instance.NextOccurrenceAt, instance.SubIssueIDs, instance.DocumentContent = "", nil, nil, nil
			instance.RecurrenceSeriesID, instance.RecurrenceOccurrence = f.recurringOccurrenceId("source"), "2026-09-28"
			data.Issues = append(data.Issues, instance)
		}},
		{name: "invalid-rule", want: -1, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			source(data).Recurrence = "FREQ=SECONDLY"
		}},
		{name: "retired-team", want: -1, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			for index := range data.Teams {
				if data.Teams[index].ID == f.team {
					data.Teams[index].RetiredAt = &now
				}
			}
		}},
		{name: "missing-team", want: -1, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			data.Teams = slices.DeleteFunc(data.Teams, func(team domain.Team) bool { return team.ID == f.team })
		}},
		{name: "missing-due-date", want: -1, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			source(data).DueDate = nil
		}},
		{name: "due-moved-later", want: -1, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			due := "2026-10-05"
			source(data).DueDate = &due
		}},
		{name: "not-due", want: 0, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			later := now.Add(time.Hour)
			source(data).NextOccurrenceAt = &later
		}},
		{name: "archived-source", want: 0, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			source(data).ArchivedAt = &now
		}},
		{name: "deleted-source", want: 0, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			data.Issues = slices.DeleteFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == f.recurringOccurrenceId("source") })
		}},
		{name: "no-workflow-state-rolls-back", want: 0, err: errInvalid, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			data.States = slices.DeleteFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == f.team || state.TeamID == "" })
			settings := data.TeamSettings[f.team]
			settings.DefaultStateID = ""
			data.TeamSettings[f.team] = settings
		}},
		{name: "tick-several-due-series", tick: true, want: 5, edit: func(f automationCycleScheduleFixture, data *domain.Bootstrap, now time.Time) {
			second := *source(data)
			second.ID, second.Number, second.Identifier, second.Title = f.recurringOccurrenceId("second-source"), 1600, "REC-1600", "Daily standup"
			second.Recurrence, second.SubIssueIDs, second.DocumentContent = "daily", nil, nil
			data.Issues = append(data.Issues, second)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			legacy := recurringOccurrenceDifferential(t, f, test, now)
			if test.err != nil && !strings.Contains(legacy.Error, test.err.Error()) {
				t.Fatalf("legacy error %q, want %v", legacy.Error, test.err)
			}
			events := 0
			if test.want != 0 {
				events = 1
				if test.tick {
					events = 2
				}
			}
			if len(legacy.Events) != events || len(legacy.Webhooks) != events {
				t.Fatalf("legacy events %d webhooks %d, want %d", len(legacy.Events), len(legacy.Webhooks), events)
			}
			if test.want > 0 && !test.tick {
				created, _ := legacy.Created.(*domain.Issue)
				if created == nil || len(created.SubIssueIDs) != test.want-1 || len(legacy.Realtime) < 1+test.want {
					t.Fatalf("legacy did not create its family: %+v realtime=%d", created, len(legacy.Realtime))
				}
			}
		})
	}
	t.Run("concurrent-ticks-create-once", func(t *testing.T) {
		test := recurringOccurrenceCase{name: "concurrent"}
		recurringOccurrenceSeed(t, f, test, now, false)
		want, wantStarted := recurringOccurrenceRun(t, f, f.repository, test, now, recurringOccurrenceCall(f, test, now))
		want.Created, want.Webhooks, want.Realtime = nil, nil, nil
		expected := recurringOccurrenceNormalize(t, want, wantStarted)
		for attempt := range 3 {
			recurringOccurrenceSeed(t, f, test, now, attempt == 2)
			got, started := recurringOccurrenceRun(t, f, f.probe, test, now, func(service *server) (any, error) {
				var wait sync.WaitGroup
				results := make([]*domain.Issue, 3)
				errs := make([]error, 3)
				for index := range results {
					wait.Add(1)
					go func() {
						defer wait.Done()
						results[index], errs[index] = service.createRecurringOccurrence(context.Background(), f.data.Workspace.URLKey, f.recurringOccurrenceId("source"), now)
					}()
				}
				wait.Wait()
				created := 0
				for index, issue := range results {
					if errs[index] != nil {
						t.Errorf("concurrent occurrence failed: %v", errs[index])
					}
					if issue != nil {
						created++
					}
				}
				if created != 1 {
					t.Errorf("concurrent ticks created %d occurrences", created)
				}
				return nil, nil
			})
			got.Created, got.Webhooks, got.Realtime = nil, nil, nil
			if normalized := recurringOccurrenceNormalize(t, got, started); normalized != expected {
				t.Fatalf("attempt %d: concurrent ticks differ from one literal tick: %s", attempt, policyFirstDifference(expected, normalized))
			}
		}
	})
	t.Run("stale-image-conflict-retries", func(t *testing.T) {
		// Another server edits the colliding issue SLA row after the warm view
		// installed its image. The occurrence edits the same row: its rebase
		// conflicts, it refreshes and retries, and the result is the literal
		// sequential order (peer edit, then occurrence).
		test := recurringOccurrenceCase{name: "conflict", edit: cases[11].edit}
		peer := func(t *testing.T) {
			if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw206_peer", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.IssueSLAs {
					if data.IssueSLAs[index].ID == f.recurringOccurrenceId("colliding-later") {
						data.IssueSLAs[index].RemainingMinutes = 42
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		occurrence := func(service *server) error {
			_, err := service.createRecurringOccurrence(context.Background(), f.data.Workspace.URLKey, f.recurringOccurrenceId("source"), now)
			return err
		}
		recurringOccurrenceSeed(t, f, test, now, false)
		want, wantStarted := recurringOccurrenceRun(t, f, f.repository, test, now, func(service *server) (any, error) {
			peer(t)
			return nil, occurrence(service)
		})
		want.Webhooks, want.Realtime, want.Events = nil, nil, nil
		expected := recurringOccurrenceNormalize(t, want, wantStarted)
		for _, reload := range []bool{false, true} {
			recurringOccurrenceSeed(t, f, test, now, reload)
			got, started := recurringOccurrenceRun(t, f, f.probe, test, now, func(service *server) (any, error) {
				peer(t)
				return nil, occurrence(service)
			})
			got.Webhooks, got.Realtime, got.Events = nil, nil, nil
			if normalized := recurringOccurrenceNormalize(t, got, started); normalized != expected {
				t.Fatalf("reload=%t: stale image state differs from the literal order: %s", reload, policyFirstDifference(expected, normalized))
			}
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		test := recurringOccurrenceCase{name: "cancelled"}
		for _, bounded := range []bool{false, true} {
			recurringOccurrenceSeed(t, f, test, now, false)
			source := f.repository
			if bounded {
				source = f.probe
			}
			before := discussionWriteSql(t, f.notificationsFixture)
			got, _ := recurringOccurrenceRun(t, f, source, test, now, func(service *server) (any, error) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return service.createRecurringOccurrence(ctx, f.data.Workspace.URLKey, f.recurringOccurrenceId("source"), now)
			})
			if !strings.Contains(got.Error, context.Canceled.Error()) || len(got.Events) != 0 || len(got.Realtime) != 0 {
				t.Fatalf("bounded=%t cancelled occurrence: %q events=%d", bounded, got.Error, len(got.Events))
			}
			if recurringOccurrenceNormalize(t, map[string]any{"Rows": before}, time.Now()) != recurringOccurrenceNormalize(t, map[string]any{"Rows": got.Rows}, time.Now()) {
				t.Fatalf("bounded=%t cancelled occurrence changed rows", bounded)
			}
		}
	})
}

// Over the literal 20,000-record scope the literal scoped writer falls back
// to the full writer, which numbers from every issue. The bounded writer's
// full image path hydrates every issue and content record, which exceeds the
// shared metadata work budget for such a family (a known root gate). This
// pins that the refusal is atomic: no row, sequence, event or publication
// changes and all admitted work is released, while the literal writer creates
// the instance numbered from every issue.
func TestRecurringOccurrenceFullFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := automationCycleScheduleSetup(t)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	test := recurringOccurrenceCase{name: "over-20000-family", sequence: 21999, bulk: 20001}
	recurringOccurrenceSeed(t, f, test, now, false)
	legacy, _ := recurringOccurrenceRun(t, f, f.repository, test, now, recurringOccurrenceCall(f, test, now))
	created, _ := legacy.Created.(*domain.Issue)
	if created == nil || len(created.SubIssueIDs) != 3 || created.Number != 25001 {
		t.Fatalf("literal full fallback did not number from every issue: %+v %s", created, legacy.Error)
	}
	recurringOccurrenceSeed(t, f, test, now, false)
	before := discussionWriteSql(t, f.notificationsFixture)
	bounded, _ := recurringOccurrenceRun(t, f, f.probe, test, now, recurringOccurrenceCall(f, test, now))
	if !strings.Contains(bounded.Error, "metadata process work budget exceeded") || len(bounded.Events) != 0 || len(bounded.Realtime) != 0 ||
		recurringOccurrenceNormalize(t, map[string]any{"Rows": before}, time.Now()) != recurringOccurrenceNormalize(t, map[string]any{"Rows": bounded.Rows}, time.Now()) {
		t.Fatalf("bounded over-limit family was not refused atomically: %s events=%d", bounded.Error, len(bounded.Events))
	}
}
