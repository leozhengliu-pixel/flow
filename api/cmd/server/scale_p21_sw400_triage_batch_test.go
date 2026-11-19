package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A 1000-issue POST /api/issue-records/batch with Triage Intelligence on: the
// batch writer commits, then generateTriageIntelligenceForIssues scores every
// triage issue of the batch over its spooled candidate summaries in one
// suggestion write. Legacy, warm and SQL-reloaded runs agree byte for byte on
// the response, issue/content/suggestion/document rows, sequence, aliases,
// events, webhooks, realtime and the installed image; the legacy run must
// generate suggestions for every triage issue (a bounded generation failure
// would be logged and dropped, so it surfaces as a difference).

const scaleP21SW400Team = "sw400-team"

// scaleP21SW400Bulk adds two triage teams' worth of issues so the batch names
// size issues, keeping triage on. Titles share tokens only inside groups of
// twenty, so every issue has a bounded candidate set of its own; groups mix
// both teams, assignees (a member and a suspended former member), labels,
// projects, duplicates, non-triage states and stored suggestions. Archived,
// completed and unbatched open issues of each group are candidates only.
func scaleP21SW400Bulk(t *testing.T, f scaleP21IssueUpdateFixture, size int) []string {
	t.Helper()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ids := []string{f.triage.ID, f.visible.ID}
	member := f.principals[2].user
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw400_bulk", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
		public := domain.Team{ID: f.public, Key: f.public}
		for _, existing := range data.Teams {
			if existing.ID == f.public {
				public = existing
			}
		}
		second := domain.Team{ID: scaleP21SW400Team, Name: "Triage bulk", Key: "SWT"}
		data.Teams = append(data.Teams, second)
		data.TeamSettings[second.ID] = domain.TeamSettings{TeamID: second.ID, TriageEnabled: true, DefaultStateID: "sw400-backlog"}
		data.States = append(data.States,
			domain.WorkflowState{ID: "sw400-backlog", TeamID: second.ID, Name: "Backlog", Type: "backlog", Position: 0},
			domain.WorkflowState{ID: "sw400-todo", TeamID: second.ID, Name: "Todo", Type: "unstarted", Position: 1})
		var label domain.IssueLabel
		for _, item := range data.Labels {
			if item.ID == "triage-l-public" {
				label = item
			}
		}
		backlog := func(team domain.Team) domain.WorkflowState {
			if team.ID == second.ID {
				return domain.WorkflowState{ID: "sw400-backlog", TeamID: second.ID, Name: "Backlog", Type: "backlog"}
			}
			return domain.WorkflowState{ID: "state_backlog", Name: "Backlog", Type: "backlog"}
		}
		issue := func(id string, index int, title string, team domain.Team) domain.Issue {
			return domain.Issue{ID: id, Identifier: fmt.Sprintf("%s-%d", team.Key, 5000+index), Number: 5000 + index, Version: 1,
				Title: title, Team: team, State: backlog(team), Creator: f.data.Viewer, SubscriberIDs: []string{member.ID}, CreatedAt: at,
				UpdatedAt: at.Add(time.Duration(index%17) * time.Minute)}
		}
		title := func(index int) string {
			group := index / 20
			return fmt.Sprintf("Kestrel%02d Falcon%02d note%04d", group, group, index)
		}
		bulk := size - len(ids)
		for index := range bulk {
			id := fmt.Sprintf("sw400-bulk-%04d", index)
			ids = append(ids, id)
			team := public
			if (index/20)%2 == 1 {
				team = second
			}
			if index%5 == 0 {
				// The group's other team: inferTeam proposes the majority team.
				if team.ID == public.ID {
					team = second
				} else {
					team = public
				}
			}
			text := title(index)
			if index%37 == 1 && index > 0 {
				// A duplicate of the previous issue's title.
				text = title(index - 1)
			}
			item := issue(id, index, text, team)
			if index%11 == 0 {
				item.Description = fmt.Sprintf("Falcon%02d crash while saving note%04d", index/20, index)
			}
			switch {
			case index%7 == 0:
				former := f.former
				item.Assignee = &former
			case index%3 == 0:
				assignee := member
				item.Assignee = &assignee
			}
			if index%4 == 0 && label.ID != "" {
				item.Labels = []domain.IssueLabel{label}
			}
			if index%6 == 0 {
				item.Project = &domain.ProjectSummary{ID: "triage-p-public", Name: "Public"}
			}
			if index%9 == 0 {
				item.SubscriberIDs = append(item.SubscriberIDs, f.former.ID, member.ID)
			}
			switch {
			case index%41 == 0:
				item.State = domain.WorkflowState{ID: "state_todo", Name: "Todo", Type: "unstarted"}
			case index%43 == 0:
				triaged := at
				item.TriagedAt = &triaged
			}
			if index%50 == 0 {
				data.IssueSuggestions = append(data.IssueSuggestions,
					domain.IssueSuggestion{ID: "sw400-s-dismissed-" + id, IssueID: id, Type: "label", State: "dismissed", SuggestedLabelID: label.ID,
						StateChangedAt: at, CreatedAt: at, UpdatedAt: at},
					domain.IssueSuggestion{ID: "sw400-s-active-" + id, IssueID: id, Type: "project", State: "active", SuggestedProjectID: "triage-p-public",
						StateChangedAt: at, CreatedAt: at, UpdatedAt: at, Metadata: map[string]any{"rank": 1}})
			}
			data.Issues = append(data.Issues, item)
		}
		// Candidate-only issues: an unbatched open issue, an archived and a
		// completed one per group.
		for group := range (bulk + 19) / 20 {
			index := group * 20
			team := public
			if group%2 == 1 {
				team = second
			}
			open := issue(fmt.Sprintf("sw400-open-%02d", group), 3000+group, title(index), team)
			archived := issue(fmt.Sprintf("sw400-archived-%02d", group), 3100+group, title(index), team)
			archivedAt := at
			archived.ArchivedAt = &archivedAt
			done := issue(fmt.Sprintf("sw400-done-%02d", group), 3200+group, title(index), team)
			done.State = domain.WorkflowState{ID: "state_done", Name: "Done", Type: "completed"}
			data.Issues = append(data.Issues, open, archived, done)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, ok := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	return ids
}

func scaleP21SW400Actions(f scaleP21IssueUpdateFixture, edit func(*domain.TriageIntelligenceSettings)) func(*testing.T) {
	return func(t *testing.T) {
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw400_actions", "", nil, func(data *domain.Bootstrap) error {
			edit(&data.WorkspaceSettings.FeatureSettings.TriageIntelligence)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// The four cases run in two top-level tests so the CI shards spread them:
// on MySQL all four took 20 minutes in one test.
func TestScaleP21SW400ThousandIssueBatchTriageDifferential(t *testing.T) {
	scaleP21SW400Run(t, "priority-suggest", "priority-auto")
}

func TestScaleP21SW400ThousandIssueBatchTriageMoveDifferential(t *testing.T) {
	scaleP21SW400Run(t, "team-auto-move", "batch-move-into-triage-team")
}

func scaleP21SW400Run(t *testing.T, names ...string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	ids := scaleP21SW400Bulk(t, f, 1000)
	owner := f.data.Viewer
	ran := 0
	defer func() {
		if ran != len(names) {
			t.Fatal("SW400 ran", ran, "of the named cases", names)
		}
	}()
	for _, test := range []struct {
		name   string
		setup  func(*testing.T)
		update map[string]any
		// moved is the least number of batch issues the triage step moves.
		moved int
	}{
		{name: "priority-suggest", update: map[string]any{"priority": 2}},
		{name: "priority-auto", setup: scaleP21SW400Actions(f, func(settings *domain.TriageIntelligenceSettings) {
			settings.AssigneeAction, settings.ProjectAction, settings.LabelAction = "auto", "auto", "auto"
			settings.DuplicateAction, settings.RelatedAction = "auto", "auto"
		}), update: map[string]any{"priority": 3}},
		{name: "team-auto-move", setup: scaleP21SW400Actions(f, func(settings *domain.TriageIntelligenceSettings) {
			settings.TeamAction, settings.LabelAction = "auto", "auto"
		}), update: map[string]any{"priority": 1}, moved: 100},
		{name: "batch-move-into-triage-team", update: map[string]any{"teamId": scaleP21SW400Team}},
	} {
		if !slices.Contains(names, test.name) {
			continue
		}
		ran++
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{setup: test.setup, issue: f.triage.ID})
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			var logged []string
			started := time.Now().UTC()
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				var buffer bytes.Buffer
				writer, flags := log.Writer(), log.Flags()
				log.SetOutput(&buffer)
				defer func() {
					log.SetOutput(writer)
					log.SetFlags(flags)
				}()
				w := f.batch(service, owner, nil, ids, test.update, false)
				logged = append(logged, buffer.String())
				if strings.Contains(buffer.String(), "triage") {
					t.Logf("bounded=%t logged: %s", service.store.BoundedMetadataEnabled(), buffer.String())
				}
				return w
			})
			for index, text := range logged {
				if strings.Contains(text, "triage") {
					t.Fatalf("run %d logged a triage failure: %s", index, text)
				}
			}
			legacy := outcomes[0]
			if legacy.Code != http.StatusOK {
				t.Fatalf("legacy status %d: %s", legacy.Code, legacy.Body)
			}
			var issues []domain.Issue
			if err := json.Unmarshal(legacy.Body, &issues); err != nil {
				t.Fatal(err)
			}
			if len(issues) != len(ids) {
				t.Fatalf("legacy returned %d issues", len(issues))
			}
			generated, eligible, accepted := 0, 0, 0
			for _, issue := range issues {
				if issue.TriagedAt == nil && issue.State.Type == "backlog" {
					eligible++
				}
				if issue.SuggestionsGeneratedAt != nil && !issue.SuggestionsGeneratedAt.Before(started) {
					generated++
				}
			}
			for _, row := range legacy.State.Suggestions {
				if strings.Contains(row[2].(string), `"state":"accepted"`) && strings.Contains(row[0].(string), "issue_suggestion_") {
					accepted++
				}
			}
			t.Logf("legacy: %d issues, %d triage-eligible after the batch, %d generated, %d accepted, %d events, %d suggestions rows",
				len(issues), eligible, generated, accepted, len(legacy.State.Events), len(legacy.State.Suggestions))
			if generated < 900 {
				t.Fatalf("legacy generated suggestions for only %d issues", generated)
			}
			if len(legacy.State.Events) != 2 {
				t.Fatalf("legacy published %d events, want the batch and the suggestion write", len(legacy.State.Events))
			}
			if strings.Contains(test.name, "auto") && accepted == 0 {
				t.Fatal("legacy auto actions accepted nothing")
			}
			if test.moved > 0 {
				count := 0
				for _, issue := range issues {
					if len(issue.PreviousIdentifiers) > 0 {
						count++
					}
				}
				if count < test.moved {
					t.Fatalf("legacy triage moved %d issues, want at least %d", count, test.moved)
				}
			}
		})
	}
}

// The same 1000-issue batch with triage off over issues that already carry
// an update history (activities and notifications to their subscribers):
// the batch writer alone, legacy against bounded.
func TestScaleP21SW402ThousandIssueBatchWithHistoryDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	ids := scaleP21SW400Bulk(t, f, 1000)
	at := time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)
	member := f.principals[2].user
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw402_history", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = false
		for index, id := range ids {
			for step := range 2 {
				activity := domain.ActivityEvent{ID: fmt.Sprintf("sw402-a-%04d-%d", index, step), Type: "issue.updated", CreatedAt: at.Add(time.Duration(step) * time.Minute),
					Actor: f.data.Viewer, Metadata: map[string]string{"priority": fmt.Sprint(step + 1), "priorityBefore": fmt.Sprint(step)}}
				data.Activities[id] = append(data.Activities[id], activity)
				data.Notifications = append(data.Notifications, domain.Notification{ID: fmt.Sprintf("sw402-n-%04d-%d", index, step), RecipientID: member.ID, SourceType: "issue", SourceID: id, ActivityID: activity.ID, Actor: f.data.Viewer,
					IssueID: id, Type: "issue.updated", Category: "updates", GroupKey: "issue:" + id, OccurrenceCount: 1, CreatedAt: activity.CreatedAt})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, ok := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	f.runBatchCases(t, []scaleP21IssueBatchCase{
		{name: "1000-with-history", user: f.data.Viewer, ids: ids, update: map[string]any{"priority": 2}, code: http.StatusOK},
	})
}
