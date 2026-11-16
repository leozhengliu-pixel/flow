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

type scaleP21TeamMoveOptions struct {
	project      string
	triageTarget bool
	triageIssue  bool
	auto         bool
	mergeAt      time.Time
}

type scaleP21TeamMoveState struct {
	scaleP21TriageState
	Records, Documents, Sequence, Aliases, IssueLabels [][]any
}

type scaleP21TeamMoveOutcome struct {
	Code               int
	Body               json.RawMessage
	State              scaleP21TeamMoveState
	Webhooks, Realtime []json.RawMessage
	// Installed is the source's installed documents and suggestions after
	// the call; Reloaded is the same read from a view loaded from SQL.
	Installed, Reloaded json.RawMessage
}

type scaleP21TeamMoveFixture struct {
	*scaleP21TriageWriterFixture
	sequence int64
}

func scaleP21TeamMoveSetup(t *testing.T) scaleP21TeamMoveFixture {
	t.Helper()
	triage := scaleP21TriageWriterSetup(t)
	f := scaleP21TeamMoveFixture{scaleP21TriageWriterFixture: &triage}
	rows := f.rows(t, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, f.data.Workspace.URLKey)
	if len(rows) != 1 {
		t.Fatal("issue sequence missing")
	}
	f.sequence = scaleP21TeamMoveInt(t, rows[0][0])
	return f
}

func scaleP21TeamMoveInt(t *testing.T, value any) int64 {
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

func (f scaleP21TeamMoveFixture) exec(t *testing.T, query string, args ...any) {
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

// prepare seeds the move targets after each reset: four teams (a parent, a
// sub-team inheriting its workflow, a team without settings or matching
// labels, a triage team), their states, same-named labels in live,
// archived, project-only and archived-group variants, attached documents,
// the scoped issues' move-relevant fields and team suggestions. The issue
// sequence and aliases return to the fixture values.
func (f scaleP21TeamMoveFixture) prepare(opts scaleP21TeamMoveOptions) func(*testing.T) {
	return func(t *testing.T) {
		repo, workspace := f.repository, f.data.Workspace.URLKey
		f.exec(t, `UPDATE issue_number_sequences SET last_number=? WHERE workspace_key=?`, f.sequence, workspace)
		f.exec(t, `DELETE FROM issue_identifier_aliases WHERE workspace_key=?`, workspace)
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
				domain.IssueLabel{ID: "tm-l-triage-bug", Name: "CRASH", Scope: "tm-triage", CreatedAt: at})
			data.Projects = append(data.Projects,
				domain.Project{ID: "tm-p-cross", Name: "Cross", TeamIDs: []string{f.public, "tm-parent"}},
				domain.Project{ID: "tm-p-sub", Name: "Sub", TeamIDs: []string{f.public, "tm-sub"}},
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

func (f scaleP21TeamMoveFixture) state(t *testing.T) scaleP21TeamMoveState {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	return scaleP21TeamMoveState{
		scaleP21TriageState: f.scaleP21TriageWriterFixture.state(t),
		Records:             f.rows(t, `SELECT id,identifier,team_id,state_id,state_type,project_id,cycle_id,parent_id,version FROM issue_records WHERE workspace_key=? ORDER BY id`, workspace),
		Documents:           f.rows(t, `SELECT record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' ORDER BY collection_order,record_key`, workspace),
		Sequence:            f.rows(t, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, workspace),
		Aliases:             f.rows(t, `SELECT issue_id,identifier FROM issue_identifier_aliases WHERE workspace_key=? ORDER BY issue_id,identifier`, workspace),
		IssueLabels:         f.rows(t, `SELECT issue_id,label_id FROM issue_label_records WHERE workspace_key=? ORDER BY issue_id,label_id`, workspace),
	}
}

// installed reads the installed documents and suggestions of repo: the
// legacy snapshot, or a bounded view's installed image.
func (f scaleP21TeamMoveFixture) installed(t *testing.T, repo *store.SQLiteStore) json.RawMessage {
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

func (f scaleP21TeamMoveFixture) run(t *testing.T, source string, call func(*server) *httptest.ResponseRecorder) (scaleP21TeamMoveOutcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.reset(t, source)
	got := scaleP21TeamMoveOutcome{}
	var mu sync.Mutex
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	service.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
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
		t.Fatalf("%s: installed image differs from the SQL reload: %s", source, scaleP21M2Diff(installed, reloaded))
	}
	return got, started
}

func (f scaleP21TeamMoveFixture) differential(t *testing.T, name string, call func(*server) *httptest.ResponseRecorder) []scaleP21TeamMoveOutcome {
	t.Helper()
	var normalized []string
	var outcomes []scaleP21TeamMoveOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.run(t, source, call)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21TriageNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Fatalf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

// scaleP21TeamMoveSorted orders concurrent publications by their normalized
// envelope; each envelope is still compared byte for byte.
func scaleP21TeamMoveSorted(t *testing.T, items []json.RawMessage, started time.Time) []json.RawMessage {
	t.Helper()
	var normalized []string
	for _, item := range items {
		normalized = append(normalized, scaleP21TriageNormalize(t, item, started))
	}
	slices.Sort(normalized)
	result := []json.RawMessage{}
	for _, item := range normalized {
		result = append(result, json.RawMessage(item))
	}
	return result
}

func scaleP21TeamMoveIssue(t *testing.T, outcome scaleP21TeamMoveOutcome, id string) domain.Issue {
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

func scaleP21TeamMoveLabelIDs(issue domain.Issue) []string {
	ids := []string{}
	for _, label := range issue.Labels {
		ids = append(ids, label.ID)
	}
	return ids
}

func TestScaleP21TeamMoveAcceptDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamMoveSetup(t)
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
		opts       scaleP21TeamMoveOptions
		user       domain.User
		key        *domain.APIKey
		suggestion string
		stop       bool
		code       int
		want       *expectation
	}{
		{"parent-team-incompatible-project", scaleP21TeamMoveOptions{project: "triage-p-public"}, owner, nil, "tm-s-parent", false, http.StatusOK,
			&expectation{"tm-parent", "tmp-todo", []string{"tm-l-parent-bug", "tm-l-shared"}, false}},
		{"sub-team-inherited-workflow-compatible-project", scaleP21TeamMoveOptions{project: "tm-p-sub"}, owner, nil, "tm-s-sub", false, http.StatusOK,
			&expectation{"tm-sub", "tmp-backlog", []string{"tm-l-parent-bug", "tm-l-shared"}, true}},
		{"team-without-settings-or-matching-labels", scaleP21TeamMoveOptions{}, owner, nil, "tm-s-bare", false, http.StatusOK,
			&expectation{"tm-bare", "tmb-a", []string{"tm-l-shared"}, false}},
		{"triage-target-on", scaleP21TeamMoveOptions{triageTarget: true, project: "tm-p-cross"}, owner, nil, "tm-s-triage", false, http.StatusOK,
			&expectation{"tm-triage", "tmt-triage", []string{"tm-l-triage-bug", "tm-l-shared"}, false}},
		{"triage-target-off", scaleP21TeamMoveOptions{}, owner, nil, "tm-s-triage", false, http.StatusOK,
			&expectation{"tm-triage", "tmt-triage", []string{"tm-l-triage-bug", "tm-l-shared"}, false}},
		{"merged-update-activity", scaleP21TeamMoveOptions{mergeAt: merge, project: "triage-p-public"}, owner, nil, "tm-s-parent", false, http.StatusOK,
			&expectation{"tm-parent", "tmp-todo", []string{"tm-l-parent-bug", "tm-l-shared"}, false}},
		{"member-moves", scaleP21TeamMoveOptions{}, member, nil, "tm-s-parent", false, http.StatusOK,
			&expectation{"tm-parent", "tmp-todo", []string{"tm-l-parent-bug", "tm-l-shared"}, false}},
		{"same-team", scaleP21TeamMoveOptions{}, owner, nil, "triage-s-team-" + f.public, false, http.StatusOK, nil},
		{"private-team-admin-outsider", scaleP21TeamMoveOptions{}, owner, nil, "triage-s-team-" + f.private, false, http.StatusForbidden, nil},
		{"private-team-admin-member", scaleP21TeamMoveOptions{}, admin, nil, "triage-s-team-" + f.private, false, 0, nil},
		{"sub-team-of-private", scaleP21TeamMoveOptions{}, owner, nil, "triage-s-team-" + f.child, false, 0, nil},
		{"guest", scaleP21TeamMoveOptions{}, guest, nil, "triage-s-team-" + f.public, false, http.StatusForbidden, nil},
		{"ghost-team", scaleP21TeamMoveOptions{}, owner, nil, "triage-s-team-triage-team-ghost", false, http.StatusForbidden, nil},
		{"selected-key", scaleP21TeamMoveOptions{}, owner, selected, "tm-s-parent", false, http.StatusForbidden, nil},
		{"cancelled", scaleP21TeamMoveOptions{}, owner, nil, "tm-s-parent", true, 0, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(test.opts)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
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
			issue := scaleP21TeamMoveIssue(t, legacy, f.triage.ID)
			wantNumber := f.sequence + 1
			if issue.Team.ID != test.want.team || issue.State.ID != test.want.state || int64(issue.Number) != wantNumber ||
				issue.Identifier != fmt.Sprintf("%s-%d", issue.Team.Key, wantNumber) || len(issue.PreviousIdentifiers) != 1 ||
				!reflect.DeepEqual(scaleP21TeamMoveLabelIDs(issue), test.want.labels) || (issue.Project != nil) != test.want.project || issue.CycleID != nil {
				t.Fatalf("legacy move: team %s state %s number %d identifier %s previous %v labels %v project %v",
					issue.Team.ID, issue.State.ID, issue.Number, issue.Identifier, issue.PreviousIdentifiers, scaleP21TeamMoveLabelIDs(issue), issue.Project)
			}
			if len(legacy.State.Aliases) != 1 || len(legacy.State.Sequence) != 1 || scaleP21TeamMoveInt(t, legacy.State.Sequence[0][0]) != wantNumber {
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
func TestScaleP21TeamMoveConcurrentSameTeam(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamMoveSetup(t)
	f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21TeamMoveOptions{project: "tm-p-cross"})
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
			numbers[scaleP21TeamMoveIssue(t, got, id).Number] = true
		}
		if !numbers[int(f.sequence)+1] || !numbers[int(f.sequence)+2] {
			t.Fatalf("legacy numbers %v", numbers)
		}
		got.Webhooks, got.Realtime = scaleP21TeamMoveSorted(t, got.Webhooks, started), scaleP21TeamMoveSorted(t, got.Realtime, started)
		expected[scaleP21TriageNormalize(t, got, started)] = true
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
			got.Webhooks, got.Realtime = scaleP21TeamMoveSorted(t, got.Webhooks, started), scaleP21TeamMoveSorted(t, got.Realtime, started)
			if state := scaleP21TriageNormalize(t, got, started); !expected[state] {
				t.Fatalf("%s attempt %d: concurrent moves match no sequential legacy order", source, attempt)
			}
		}
	}
}

// Auto-applied team suggestions: batch generation and runs move the issue
// through the same closure, the candidates' teams declared.
func TestScaleP21TeamMoveAutoApplyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamMoveSetup(t)
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
		opts  scaleP21TeamMoveOptions
		call  func(*server) *httptest.ResponseRecorder
		moved bool
	}{
		{"generate-auto-move", scaleP21TeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"}, generate(f.triage.ID), true},
		{"generate-batch-auto-move", scaleP21TeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"}, generate(f.visible.ID, f.triage.ID, f.gone.ID), true},
		{"generate-auto-without-team", scaleP21TeamMoveOptions{auto: true, triageIssue: true}, generate(f.triage.ID), false},
		{"generate-suggest-only", scaleP21TeamMoveOptions{triageIssue: true, project: "tm-p-cross"}, generate(f.triage.ID), false},
		{"run-auto-move", scaleP21TeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross"}, refresh, true},
		{"run-auto-move-merged-activity", scaleP21TeamMoveOptions{auto: true, triageIssue: true, project: "tm-p-cross", mergeAt: time.Now().UTC()}, refresh, true},
		{"run-auto-without-team", scaleP21TeamMoveOptions{auto: true, triageIssue: true}, refresh, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(test.opts)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			outcomes := f.differential(t, test.name, test.call)
			legacy := outcomes[0]
			if legacy.Code != http.StatusOK {
				t.Fatalf("legacy status %d: %s", legacy.Code, legacy.Body)
			}
			if issue := scaleP21TeamMoveIssue(t, legacy, f.triage.ID); (issue.Team.ID == "tm-parent") != test.moved {
				t.Fatalf("legacy team %s, moved want %t", issue.Team.ID, test.moved)
			}
		})
	}
}

// The kernel refuses a callback that leaves its closure before any write.
func TestScaleP21TeamMoveKernelRefusals(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamMoveSetup(t)
	f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21TeamMoveOptions{})
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
					t.Fatalf("%s: refused move wrote: %s", source, scaleP21M2Diff(fmt.Sprint(before), fmt.Sprint(after)))
				}
				if service.store.MetadataWorkUsedForTesting() != 0 {
					t.Fatalf("%s: refused move retained work", source)
				}
			}
		})
	}
}
