package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Team moves through the bounded suggestion writer (issueTeamMove) compared
// with the legacy scoped writer: accepted team suggestions, auto-applied
// team suggestions of batch generation and of runs, permission refusals,
// concurrent moves into one team and the image reloaded from SQL.

type scaleTeamMoveOptions struct {
	project      string
	triageTarget bool
	triageIssue  bool
	auto         bool
	mergeAt      time.Time
}

type scaleTeamMoveState struct {
	scaleTriageState
	Records, Documents, Sequence, Aliases, IssueLabels [][]any
}

type scaleTeamMoveOutcome struct {
	Code               int
	Body               json.RawMessage
	State              scaleTeamMoveState
	Webhooks, Realtime []json.RawMessage
	// Installed is the source's installed documents and suggestions after
	// the call; Reloaded is the same read from a view loaded from SQL.
	Installed, Reloaded json.RawMessage
}

type scaleTeamMoveFixture struct {
	*scaleTriageWriterFixture
	sequence int64
}

func scaleTeamMoveSetup(t *testing.T) scaleTeamMoveFixture {
	t.Helper()
	triage := scaleTriageWriterSetup(t)
	f := scaleTeamMoveFixture{scaleTriageWriterFixture: &triage}
	f.sequence = f.currentSequence(t)
	return f
}

func (f scaleTeamMoveFixture) currentSequence(t *testing.T) int64 {
	t.Helper()
	rows := f.rows(t, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, f.data.Workspace.URLKey)
	if len(rows) != 1 {
		t.Fatal("issue sequence missing")
	}
	return scaleTeamMoveInt(t, rows[0][0])
}

func scaleTeamMoveInt(t *testing.T, value any) int64 {
	t.Helper()
	switch number := value.(type) {
	case int64:
		return number
	case int32:
		return int64(number)
	case int:
		return int64(number)
	case string:
		var parsed int64
		if _, err := fmt.Sscan(number, &parsed); err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	t.Fatalf("unexpected sequence value %T", value)
	return 0
}

func (f scaleTeamMoveFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	f.reader.query(t, `SELECT 1 FROM issue_number_sequences WHERE workspace_key=?`, f.data.Workspace.URLKey).Close()
	if f.reader.config.Driver == "postgres" || f.reader.config.Driver == "postgresql" {
		for index := range args {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", index+1), 1)
		}
	}
	if _, err := f.reader.reader.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f scaleTeamMoveFixture) resetSequenceAndAliases(t *testing.T) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	f.exec(t, `UPDATE issue_number_sequences SET last_number=? WHERE workspace_key=?`, f.sequence, workspace)
	f.exec(t, `DELETE FROM issue_identifier_aliases WHERE workspace_key=?`, workspace)
}

// prepare seeds the move targets after each reset: four teams (a parent, a
// sub-team inheriting its workflow, a team without settings or matching
// labels, a triage team), their states, same-named labels in live,
// archived, project-only and archived-group variants, attached documents,
// the scoped issues' move-relevant fields and team suggestions. The issue
// sequence and aliases return to the fixture values.
func (f scaleTeamMoveFixture) prepare(opts scaleTeamMoveOptions) func(*testing.T) {
	return func(t *testing.T) {
		repo, workspace := f.repository, f.data.Workspace.URLKey
		f.resetSequenceAndAliases(t)
		at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		archived := at.Add(time.Hour)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.team_move_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams,
				domain.Team{ID: "tm-parent", Name: "Move parent", Key: "TMP"}, domain.Team{ID: "tm-sub", Name: "Move sub", Key: "TMS"},
				domain.Team{ID: "tm-bare", Name: "Move bare", Key: "TMB"}, domain.Team{ID: "tm-triage", Name: "Move triage", Key: "TMT"},
				domain.Team{ID: "tm-other", Name: "Move other", Key: "TMO"})
			data.TeamSettings["tm-parent"] = domain.TeamSettings{TeamID: "tm-parent", EstimateType: "notUsed", DefaultStateID: "tmp-todo"}
			data.TeamSettings["tm-sub"] = domain.TeamSettings{TeamID: "tm-sub", ParentTeamID: "tm-parent", InheritWorkflowStatuses: true}
			data.TeamSettings["tm-triage"] = domain.TeamSettings{TeamID: "tm-triage", TriageEnabled: opts.triageTarget, DefaultStateID: "tmt-triage"}
			data.States = append(data.States,
				domain.WorkflowState{ID: "tmp-done", TeamID: "tm-parent", Name: "Done", Type: "completed", Position: 4},
				domain.WorkflowState{ID: "tmp-backlog", TeamID: "tm-parent", Name: "Backlog", Type: "backlog", Position: 1},
				domain.WorkflowState{ID: "tmp-todo", TeamID: "tm-parent", Name: "Todo", Type: "unstarted", Position: 2},
				domain.WorkflowState{ID: "tmp-started", TeamID: "tm-parent", Name: "Started", Type: "started", Position: 3},
				domain.WorkflowState{ID: "tmt-triage", TeamID: "tm-triage", Name: "Triage", Type: "backlog", Position: 0},
				domain.WorkflowState{ID: "tmt-todo", TeamID: "tm-triage", Name: "Todo", Type: "unstarted", Position: 1},
				domain.WorkflowState{ID: "tmb-b", TeamID: "tm-bare", Name: "B", Type: "backlog", Position: 2},
				domain.WorkflowState{ID: "tmb-a", TeamID: "tm-bare", Name: "A", Type: "backlog", Position: 2, Default: true},
				domain.WorkflowState{ID: "tmo-todo", TeamID: "tm-other", Name: "Todo", Type: "unstarted", Position: 1})
			data.LabelGroups = append(data.LabelGroups,
				domain.LabelGroup{ID: "tm-g-area", Name: "Area", ResourceType: "issue", CreatedAt: at},
				domain.LabelGroup{ID: "tm-g-archived", Name: "Old", ResourceType: "issue", CreatedAt: at, ArchivedAt: &archived})
			data.Labels = append(data.Labels,
				domain.IssueLabel{ID: "tm-l-bug", Name: "Crash", Scope: f.public, CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-shared", Name: "Shared", GroupID: "tm-g-area", CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-area", Name: "Area", Scope: f.public, CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-area-archived", Name: "area", Scope: "tm-parent", CreatedAt: at, ArchivedAt: &archived},
				domain.IssueLabel{ID: "tm-l-project-bug", Name: "Crash", Scope: "tm-parent", ResourceType: "project", CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-parent-bug", Name: " crash ", Scope: "tm-parent", CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-parent-area", Name: "AREA", Scope: "tm-parent", GroupID: "tm-g-area", CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-grouped-bug", Name: "crash", Scope: "tm-triage", GroupID: "tm-g-archived", CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-triage-bug", Name: "CRASH", Scope: "tm-triage", CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-tie-b", Name: "Cross team preview crash", Description: "Image preview crashes on load", CreatedAt: at},
				domain.IssueLabel{ID: "tm-l-tie-a", Name: "Cross team preview crash", Description: "Image preview crashes on load", CreatedAt: at})
			data.Projects = append(data.Projects,
				// inferTeam scores tm-parent and tm-sub equally: the tie goes to tm-parent.
				domain.Project{ID: "tm-p-cross", Name: "Cross", TeamIDs: []string{f.public, "tm-parent", "tm-sub"}},
				// Equally named projects and labels tie in inferProject and inferLabel.
				domain.Project{ID: "tm-p-tie-b", Name: "Cross team preview", TeamIDs: []string{f.public}},
				domain.Project{ID: "tm-p-tie-a", Name: "Cross team preview", TeamIDs: []string{f.public}},
				domain.Project{ID: "tm-p-docs", Name: "Docs", TeamIDs: []string{f.private, "tm-ghost-team", "tm-triage"}})
			data.Documents = append(data.Documents,
				domain.Document{ID: "tm-doc-a", SlugID: "tm-doc-a", Title: "Attached A", Content: "Alpha", IssueID: f.triage.ID, IssueTeamID: f.public,
					InheritedTeamIDs: []string{f.public, f.private}, ProjectIDs: []string{"tm-p-docs"}, TeamIDs: []string{}, SubscriberIDs: []string{}, Version: 3, CreatedAt: at, UpdatedAt: at},
				domain.Document{ID: "tm-doc-free", SlugID: "tm-doc-free", Title: "Free", Content: "Free", ProjectIDs: []string{"tm-p-docs"}, TeamIDs: []string{}, SubscriberIDs: []string{}, CreatedAt: at, UpdatedAt: at},
				domain.Document{ID: "tm-doc-b", SlugID: "tm-doc-b", Title: "Attached B", Content: "Beta", IssueID: f.triage.ID, IssueTeamID: f.public,
					InheritedTeamIDs: []string{f.public}, ProjectIDs: []string{}, TeamIDs: []string{}, SubscriberIDs: []string{}, CreatedAt: at, UpdatedAt: at},
				domain.Document{ID: "tm-doc-c", SlugID: "tm-doc-c", Title: "Attached C", Content: "Gamma", IssueID: f.visible.ID, IssueTeamID: f.public,
					InheritedTeamIDs: []string{f.public}, ProjectIDs: []string{"tm-p-cross"}, TeamIDs: []string{}, SubscriberIDs: []string{}, CreatedAt: at, UpdatedAt: at})
			for _, item := range []struct{ id, issue, team string }{
				{"tm-s-parent", f.triage.ID, "tm-parent"}, {"tm-s-sub", f.triage.ID, "tm-sub"}, {"tm-s-bare", f.triage.ID, "tm-bare"},
				{"tm-s-triage", f.triage.ID, "tm-triage"}, {"tm-s-visible-parent", f.visible.ID, "tm-parent"},
			} {
				data.IssueSuggestions = append(data.IssueSuggestions, domain.IssueSuggestion{ID: item.id, IssueID: item.issue, Type: "team", State: "active",
					SuggestedTeamID: item.team, StateChangedAt: at, CreatedAt: at, UpdatedAt: at, Metadata: map[string]any{"rank": 1}})
			}
			settings := &data.WorkspaceSettings.FeatureSettings.TriageIntelligence
			settings.AssigneeAction, settings.ProjectAction, settings.LabelAction = "suggest", "suggest", "suggest"
			settings.DuplicateAction, settings.RelatedAction, settings.TeamAction = "suggest", "suggest", "suggest"
			if opts.auto {
				settings.TeamAction = "auto"
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		labels := map[string]domain.IssueLabel{}
		if full, ok := repo.BootstrapFor(workspace); ok {
			for _, label := range full.Labels {
				labels[label.ID] = label
			}
		}
		for _, id := range []string{f.triage.ID, f.visible.ID} {
			if err := repo.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), id), workspace, "scale_p21.team_move_issue", id, nil, func(data *domain.Bootstrap) error {
				issue, err := issueByID(data, id)
				if err != nil {
					return err
				}
				// An old creation: the move's issue.updated entry is always recorded.
				issue.CreatedAt = at
				if id == f.visible.ID {
					parent := f.triage.ID
					issue.ParentID = &parent
					return nil
				}
				estimate, cycle, milestone := 3.0, "tm-cycle", "tm-milestone"
				issue.Estimate, issue.CycleID, issue.AddedToCycle = &estimate, &cycle, "manual"
				issue.Labels = []domain.IssueLabel{labels["tm-l-bug"], labels["tm-l-shared"], labels["tm-l-area"]}
				issue.Project, issue.ProjectMilestoneID = nil, nil
				if opts.project != "" {
					issue.Project = &domain.ProjectSummary{ID: opts.project}
					issue.ProjectMilestoneID = &milestone
				}
				if opts.triageIssue {
					issue.Title, issue.Description = "Cross team preview crash", "The cross team image preview crashes on load."
					issue.State = domain.WorkflowState{ID: "triage-backlog", Name: "Triage", Type: "backlog", TeamID: f.public}
					issue.TriagedAt, issue.SuggestionsGeneratedAt = nil, nil
					// Unlabelled, so inferLabel scores the tied labels.
					issue.Labels = nil
				}
				if !opts.mergeAt.IsZero() {
					data.Activities[id] = append(data.Activities[id], domain.ActivityEvent{ID: "tm-activity-prior", Type: "issue.updated", CreatedAt: opts.mergeAt,
						Actor: f.data.Viewer, Metadata: map[string]string{"priority": "2", "priorityBefore": "0"}})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func (f scaleTeamMoveFixture) state(t *testing.T) scaleTeamMoveState {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	return scaleTeamMoveState{
		scaleTriageState: f.scaleTriageWriterFixture.state(t),
		Records:          f.rows(t, `SELECT id,identifier,team_id,state_id,state_type,project_id,cycle_id,parent_id,version FROM issue_records WHERE workspace_key=? ORDER BY id`, workspace),
		Documents:        f.rows(t, `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' ORDER BY collection_order,record_key`, workspace),
		Sequence:         f.rows(t, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, workspace),
		Aliases:          f.rows(t, `SELECT issue_id,identifier FROM issue_identifier_aliases WHERE workspace_key=? ORDER BY issue_id,identifier`, workspace),
		IssueLabels:      f.rows(t, `SELECT issue_id,label_id FROM issue_label_records WHERE workspace_key=? ORDER BY issue_id,label_id`, workspace),
	}
}

// installed reads the installed documents and suggestions of repo: the
// legacy snapshot, or a bounded view's installed image.
func (f scaleTeamMoveFixture) installed(t *testing.T, repo *store.SQLiteStore) json.RawMessage {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	var documents []domain.Document
	var suggestions []domain.IssueSuggestion
	if !repo.BoundedMetadataEnabled() {
		full, ok := repo.BootstrapFor(workspace)
		if !ok {
			t.Fatal("workspace missing")
		}
		documents, suggestions = full.Documents, full.IssueSuggestions
	} else {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("installed read", err)
		}
		for _, field := range []struct {
			name   string
			target any
		}{{"documents", &documents}, {"issueSuggestions", &suggestions}} {
			var raw strings.Builder
			if err := read.StreamFieldJSON(t.Context(), field.name, &raw); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(raw.String()), field.target); err != nil {
				t.Fatal(err)
			}
		}
		read.Close()
	}
	raw, err := json.Marshal(map[string]any{"documents": documents, "suggestions": suggestions})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f scaleTeamMoveFixture) run(t *testing.T, source string, call func(*server) *httptest.ResponseRecorder) (scaleTeamMoveOutcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.reset(t, source)
	got := scaleTeamMoveOutcome{}
	var mu sync.Mutex
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Webhooks = append(got.Webhooks, discussionRoutesEnvelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	service.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Realtime = append(got.Realtime, discussionRoutesEnvelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"clientId": event.ClientID, "payload": event.Payload,
		}))
	})
	prior := map[any]bool{}
	for _, event := range f.state(t).Events {
		prior[event[0]] = true
	}
	if w := call(service); w != nil {
		got.Code, got.Body = w.Code, json.RawMessage(w.Body.String())
	}
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	got.State = f.state(t)
	var events [][]any
	for _, event := range got.State.Events {
		if !prior[event[0]] {
			events = append(events, event[1:])
		}
	}
	got.State.Events = events
	if service.store.BoundedMetadataEnabled() && (service.store.WorkspaceMetadataDecodes() != 0 || service.store.MetadataWorkUsedForTesting() != 0) {
		t.Fatal("bounded team move decoded full metadata or retained work")
	}
	got.Installed = f.installed(t, service.store)
	// A store opened on the same database loads the workspace from SQL.
	reloaded, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	got.Reloaded = f.installed(t, reloaded)
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
	// Persisted bodies lose NUL (PostgreSQL refuses it); the snapshot keeps
	// it until the next load.
	nul := strings.NewReplacer(`\u0000`, "")
	if installed, reloaded := nul.Replace(string(got.Installed)), nul.Replace(string(got.Reloaded)); installed != reloaded {
		if dir := os.Getenv("FLOW_TEAM_MOVE_DUMP"); dir != "" {
			_ = os.WriteFile(dir+"/installed.json", []byte(installed), 0o600)
			_ = os.WriteFile(dir+"/reloaded.json", []byte(reloaded), 0o600)
		}
		t.Fatalf("%s: installed image differs from the SQL reload: %s", source, policyFirstDifference(installed, reloaded))
	}
	return got, started
}

func (f scaleTeamMoveFixture) differential(t *testing.T, name string, call func(*server) *httptest.ResponseRecorder) []scaleTeamMoveOutcome {
	t.Helper()
	var normalized []string
	var outcomes []scaleTeamMoveOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.run(t, source, call)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleTriageNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Fatalf("%s: %s changed: %s", name, source, policyFirstDifference(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

// scaleTeamMoveSorted orders concurrent publications by their normalized
// envelope; each envelope is still compared byte for byte.
func scaleTeamMoveSorted(t *testing.T, items []json.RawMessage, started time.Time) []json.RawMessage {
	t.Helper()
	var normalized []string
	for _, item := range items {
		normalized = append(normalized, scaleTriageNormalize(t, item, started))
	}
	slices.Sort(normalized)
	result := []json.RawMessage{}
	for _, item := range normalized {
		result = append(result, json.RawMessage(item))
	}
	return result
}

func scaleTeamMoveIssue(t *testing.T, outcome scaleTeamMoveOutcome, id string) domain.Issue {
	t.Helper()
	for _, row := range outcome.State.Issues {
		if row[0] == id {
			var issue domain.Issue
			if err := json.Unmarshal([]byte(row[3].(string)), &issue); err != nil {
				t.Fatal(err)
			}
			return issue
		}
	}
	t.Fatalf("issue %s missing", id)
	return domain.Issue{}
}

func scaleTeamMoveLabelIDs(issue domain.Issue) []string {
	ids := []string{}
	for _, label := range issue.Labels {
		ids = append(ids, label.ID)
	}
	return ids
}

func TestScaleTeamMoveAcceptDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleTeamMoveSetup(t)
	owner, admin, member, guest := f.data.Viewer, f.principals[1].user, f.principals[2].user, f.principals[3].user
	selected := &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
	merge := time.Now().UTC()
	type expectation struct {
		team, state string
		labels      []string
		project     bool
	}
	cases := []struct {
		name       string
		opts       scaleTeamMoveOptions
		user       domain.User
		key        *domain.APIKey
		suggestion string
		stop       bool
		code       int
		want       *expectation
	}{
		{"parent-team-incompatible-project", scaleTeamMoveOptions{project: "triage-p-public"}, owner, nil, "tm-s-parent", false, http.StatusOK,
			&expectation{"tm-parent", "tmp-todo", []string{"tm-l-parent-bug", "tm-l-shared"}, false}},
		{"sub-team-inherited-workflow-compatible-project", scaleTeamMoveOptions{project: "tm-p-cross"}, owner, nil, "tm-s-sub", false, http.StatusOK,
			&expectation{"tm-sub", "tmp-backlog", []string{"tm-l-parent-bug", "tm-l-shared"}, true}},
		{"team-without-settings-or-matching-labels", scaleTeamMoveOptions{}, owner, nil, "tm-s-bare", false, http.StatusOK,
			&expectation{"tm-bare", "tmb-a", []string{"tm-l-shared"}, false}},
		{"triage-target-on", scaleTeamMoveOptions{triageTarget: true, project: "tm-p-cross"}, owner, nil, "tm-s-triage", false, http.StatusOK,
			&expectation{"tm-triage", "tmt-triage", []string{"tm-l-triage-bug", "tm-l-shared"}, false}},
		{"triage-target-off", scaleTeamMoveOptions{}, owner, nil, "tm-s-triage", false, http.StatusOK,
			&expectation{"tm-triage", "tmt-triage", []string{"tm-l-triage-bug", "tm-l-shared"}, false}},
		{"merged-update-activity", scaleTeamMoveOptions{mergeAt: merge, project: "triage-p-public"}, owner, nil, "tm-s-parent", false, http.StatusOK,
			&expectation{"tm-parent", "tmp-todo", []string{"tm-l-parent-bug", "tm-l-shared"}, false}},
		{"member-moves", scaleTeamMoveOptions{}, member, nil, "tm-s-parent", false, http.StatusOK,
			&expectation{"tm-parent", "tmp-todo", []string{"tm-l-parent-bug", "tm-l-shared"}, false}},
		{"same-team", scaleTeamMoveOptions{}, owner, nil, "triage-s-team-" + f.public, false, http.StatusOK, nil},
		{"private-team-admin-outsider", scaleTeamMoveOptions{}, owner, nil, "triage-s-team-" + f.private, false, http.StatusForbidden, nil},
		{"private-team-admin-member", scaleTeamMoveOptions{}, admin, nil, "triage-s-team-" + f.private, false, 0, nil},
		{"sub-team-of-private", scaleTeamMoveOptions{}, owner, nil, "triage-s-team-" + f.child, false, 0, nil},
		{"guest", scaleTeamMoveOptions{}, guest, nil, "triage-s-team-" + f.public, false, http.StatusForbidden, nil},
		{"ghost-team", scaleTeamMoveOptions{}, owner, nil, "triage-s-team-triage-team-ghost", false, http.StatusForbidden, nil},
		{"selected-key", scaleTeamMoveOptions{}, owner, selected, "tm-s-parent", false, http.StatusForbidden, nil},
		{"cancelled", scaleTeamMoveOptions{}, owner, nil, "tm-s-parent", true, 0, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.scaleTriageWriterFixture.prepare = f.prepare(test.opts)
			defer func() { f.scaleTriageWriterFixture.prepare = nil }()
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				return f.stateRequest(service, test.user, test.key, f.triage.ID, test.suggestion, true, test.stop)
			})
			legacy := outcomes[0]
			t.Logf("legacy status %d", legacy.Code)
			if test.code != 0 && legacy.Code != test.code {
				t.Fatalf("legacy status %d, want %d: %s", legacy.Code, test.code, legacy.Body)
			}
			if test.want == nil {
				return
			}
			issue := scaleTeamMoveIssue(t, legacy, f.triage.ID)
			wantNumber := f.sequence + 1
			if issue.Team.ID != test.want.team || issue.State.ID != test.want.state || int64(issue.Number) != wantNumber ||
				issue.Identifier != fmt.Sprintf("%s-%d", issue.Team.Key, wantNumber) || len(issue.PreviousIdentifiers) != 1 ||
				!reflect.DeepEqual(scaleTeamMoveLabelIDs(issue), test.want.labels) || (issue.Project != nil) != test.want.project || issue.CycleID != nil {
				t.Fatalf("legacy move: team %s state %s number %d identifier %s previous %v labels %v project %v",
					issue.Team.ID, issue.State.ID, issue.Number, issue.Identifier, issue.PreviousIdentifiers, scaleTeamMoveLabelIDs(issue), issue.Project)
			}
			if len(legacy.State.Aliases) != 1 || len(legacy.State.Sequence) != 1 || scaleTeamMoveInt(t, legacy.State.Sequence[0][0]) != wantNumber {
				t.Fatalf("legacy aliases %v sequence %v", legacy.State.Aliases, legacy.State.Sequence)
			}
			scoped := 0
			for _, row := range legacy.State.Documents {
				var document domain.Document
				if err := json.Unmarshal([]byte(row[2].(string)), &document); err != nil {
					t.Fatal(err)
				}
				if document.IssueID == f.triage.ID {
					scoped++
					if document.IssueTeamID != test.want.team || len(document.InheritedTeamIDs) == 0 || document.InheritedTeamIDs[0] != test.want.team {
						t.Fatalf("legacy attached document scope %s %v", document.IssueTeamID, document.InheritedTeamIDs)
					}
				}
			}
			if scoped != 2 {
				t.Fatalf("legacy attached documents %d", scoped)
			}
			if !test.opts.mergeAt.IsZero() {
				merged := false
				for _, row := range legacy.State.Content {
					merged = merged || row[0] == "activity" && strings.Contains(row[3].(string), "tm-activity-prior") && strings.Contains(row[3].(string), `"team"`)
				}
				if !merged {
					t.Fatal("legacy move did not merge into the prior update activity")
				}
			}
		})
	}
}

// Concurrent acceptances moving two issues into one team serialize: each
// bounded result equals one legacy order, with distinct numbers.
func TestScaleTeamMoveConcurrentSameTeam(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleTeamMoveSetup(t)
	f.scaleTriageWriterFixture.prepare = f.prepare(scaleTeamMoveOptions{project: "tm-p-cross"})
	owner := f.data.Viewer
	first := func(service *server) *httptest.ResponseRecorder {
		return f.stateRequest(service, owner, nil, f.triage.ID, "tm-s-parent", true, false)
	}
	second := func(service *server) *httptest.ResponseRecorder {
		return f.stateRequest(service, owner, nil, f.visible.ID, "tm-s-visible-parent", true, false)
	}
	expected := map[string]bool{}
	for _, order := range [][2]func(*server) *httptest.ResponseRecorder{{first, second}, {second, first}} {
		got, started := f.run(t, "legacy", func(service *server) *httptest.ResponseRecorder {
			for _, call := range order {
				if w := call(service); w.Code != http.StatusOK {
					t.Fatalf("legacy move: %d %s", w.Code, w.Body.String())
				}
			}
			return nil
		})
		numbers := map[int]bool{}
		for _, id := range []string{f.triage.ID, f.visible.ID} {
			numbers[scaleTeamMoveIssue(t, got, id).Number] = true
		}
		if !numbers[int(f.sequence)+1] || !numbers[int(f.sequence)+2] {
			t.Fatalf("legacy numbers %v", numbers)
		}
		got.Webhooks, got.Realtime = scaleTeamMoveSorted(t, got.Webhooks, started), scaleTeamMoveSorted(t, got.Realtime, started)
		expected[scaleTriageNormalize(t, got, started)] = true
	}
	for _, source := range []string{"warm", "reloaded"} {
		for attempt := range 3 {
			got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
				var wait sync.WaitGroup
				codes := make([]int, 2)
				for index, call := range []func(*server) *httptest.ResponseRecorder{first, second} {
					wait.Add(1)
					go func() {
						defer wait.Done()
						codes[index] = call(service).Code
					}()
				}
				wait.Wait()
				if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
					t.Errorf("concurrent moves failed: %v", codes)
				}
				return nil
			})
			if len(got.Webhooks) != 2 || len(got.Realtime) != 2 {
				t.Fatalf("%s: publications %d/%d", source, len(got.Webhooks), len(got.Realtime))
			}
			got.Webhooks, got.Realtime = scaleTeamMoveSorted(t, got.Webhooks, started), scaleTeamMoveSorted(t, got.Realtime, started)
			if state := scaleTriageNormalize(t, got, started); !expected[state] {
				t.Fatalf("%s attempt %d: concurrent moves match no sequential legacy order", source, attempt)
			}
		}
	}
}

// Auto-applied team suggestions: batch generation and runs move the issue
// through the same closure, the candidates' teams declared.
func TestScaleTeamMoveAutoApplyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleTeamMoveSetup(t)
	workspace := f.data.Workspace.URLKey
	generate := func(ids ...string) func(*server) *httptest.ResponseRecorder {
		return func(service *server) *httptest.ResponseRecorder {
			r := f.literalRequest(f.data.Viewer, nil, nil)
			r, closeOutput := beginRequestResourceReads(r)
			defer closeOutput()
			results, err := service.generateTriageIntelligenceForIssues(r.Context(), workspace, ids)
			w := httptest.NewRecorder()
			code := http.StatusOK
			if errors.Is(err, store.ErrNoMutation) {
				code = http.StatusNoContent
			} else if err != nil {
				t.Fatalf("generate bounded=%t: %v", service.store.BoundedMetadataEnabled(), err)
			}
			writeJSON(w, code, results)
			return w
		}
	}
	refresh := func(service *server) *httptest.ResponseRecorder {
		r := f.literalRequest(f.data.Viewer, nil, nil)
		r.SetPathValue("id", f.triage.ID)
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		w := httptest.NewRecorder()
		service.refreshIssueSuggestions(w, r)
		return w
	}
	for _, test := range []struct {
		name  string
		opts  scaleTeamMoveOptions
		call  func(*server) *httptest.ResponseRecorder
		moved bool
	}{
		{"generate-auto-move", scaleTeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"}, generate(f.triage.ID), true},
		{"generate-batch-auto-move", scaleTeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"}, generate(f.visible.ID, f.triage.ID, f.gone.ID), true},
		{"generate-auto-without-team", scaleTeamMoveOptions{auto: true, triageIssue: true}, generate(f.triage.ID), false},
		{"generate-suggest-only", scaleTeamMoveOptions{triageIssue: true, project: "tm-p-cross"}, generate(f.triage.ID), false},
		{"run-auto-move", scaleTeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"}, refresh, true},
		{"run-auto-move-merged-activity", scaleTeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross", mergeAt: time.Now().UTC()}, refresh, true},
		{"run-auto-without-team", scaleTeamMoveOptions{auto: true, triageIssue: true}, refresh, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.scaleTriageWriterFixture.prepare = f.prepare(test.opts)
			defer func() { f.scaleTriageWriterFixture.prepare = nil }()
			outcomes := f.differential(t, test.name, test.call)
			legacy := outcomes[0]
			if legacy.Code != http.StatusOK {
				t.Fatalf("legacy status %d: %s", legacy.Code, legacy.Body)
			}
			if issue := scaleTeamMoveIssue(t, legacy, f.triage.ID); (issue.Team.ID == "tm-parent") != test.moved {
				t.Fatalf("legacy team %s, moved want %t", issue.Team.ID, test.moved)
			}
			// Ties go to the target scored first.
			installed := string(legacy.Installed)
			if strings.Contains(installed, `"suggestedTeamId":"tm-sub"`) || strings.Contains(installed, `"tm-p-tie-a"`) || strings.Contains(installed, `"tm-l-tie-a"`) {
				t.Fatal("a tie went to the later target")
			}
			if !strings.Contains(installed, `"tm-l-tie-b"`) {
				t.Fatal("the tied labels were not suggested")
			}
		})
	}
}

// The kernel refuses a callback that leaves its closure before any write.
func TestScaleTeamMoveKernelRefusals(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleTeamMoveSetup(t)
	f.scaleTriageWriterFixture.prepare = f.prepare(scaleTeamMoveOptions{})
	for _, test := range []struct {
		name   string
		mutate func(*domain.Bootstrap, *domain.Issue)
	}{
		{"undeclared-team", func(data *domain.Bootstrap, issue *domain.Issue) {
			for _, team := range data.Teams {
				if team.ID == "tm-other" {
					moveIssueToTeam(data, issue, team, nil, map[string]string{})
				}
			}
		}},
		{"renumber-without-move", func(data *domain.Bootstrap, issue *domain.Issue) {
			issue.Number = data.NextIssueNumber
		}},
		{"document-content", func(data *domain.Bootstrap, issue *domain.Issue) {
			for _, team := range data.Teams {
				if team.ID == "tm-parent" {
					moveIssueToTeam(data, issue, team, &data.States[0], map[string]string{})
				}
			}
			for index := range data.Documents {
				data.Documents[index].Title = "changed"
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, source := range []string{"warm", "reloaded"} {
				service := f.reset(t, source)
				before := f.state(t)
				owned, closeOwned := beginIndependentMetadataOutputReads(t.Context())
				ctx := store.ContextWithActor(owned, f.data.Viewer)
				ctx = store.ContextWithIssueSuggestionWrite(store.WithIssueRenumbering(store.WithIssueRecordMutations(ctx, f.triage.ID)), store.IssueSuggestionWrite{})
				ran := false
				err := service.store.MutateWorkspace(ctx, f.data.Workspace.URLKey, "issue.suggestion_accepted", "tm", nil, func(data *domain.Bootstrap) error {
					issue, err := issueByID(data, f.triage.ID)
					if err != nil {
						return err
					}
					ran = true
					test.mutate(data, issue)
					return nil
				})
				closeOwned()
				if !ran || !errors.Is(err, store.ErrIssueTeamMoveRefused) || !errors.Is(err, store.ErrBoundedMetadataCoverage) {
					t.Fatalf("%s: not a typed refusal: ran=%t %v", source, ran, err)
				}
				if after := f.state(t); !reflect.DeepEqual(before, after) {
					t.Fatalf("%s: refused move wrote: %s", source, policyFirstDifference(fmt.Sprint(before), fmt.Sprint(after)))
				}
				if service.store.MetadataWorkUsedForTesting() != 0 {
					t.Fatalf("%s: refused move retained work", source)
				}
			}
		})
	}
}
