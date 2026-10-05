package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type triageFilterFixture struct {
	repo      *store.SQLiteStore
	handler   http.Handler
	bootstrap domain.Bootstrap
	issue     domain.Issue
	other     domain.Issue
	project   domain.Project
	label     domain.IssueLabel
	user      domain.User
	team      domain.Team
}

func newTriageFilterFixture(t *testing.T) triageFilterFixture {
	t.Helper()
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	bootstrap := repo.Bootstrap()
	team := bootstrap.Teams[0]
	backlog := slices.IndexFunc(bootstrap.States, func(state domain.WorkflowState) bool {
		return state.Type == "backlog"
	})
	if backlog < 0 {
		t.Fatal("no backlog state")
	}
	if err := repo.MutateWorkspace(t.Context(), bootstrap.Workspace.URLKey, "test.triage_setup", team.ID, nil, func(data *domain.Bootstrap) error {
		settings := teamSettings(data, team.ID)
		settings.TriageEnabled = true
		data.TeamSettings[team.ID] = settings
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
		data.WorkspaceSettings.FeatureSettings.TriageIntelligence = domain.TriageIntelligenceSettings{
			AssigneeAction: "suggest", ProjectAction: "suggest", LabelAction: "suggest",
			TeamAction: "suggest", DuplicateAction: "suggest", RelatedAction: "suggest",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	other := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Existing checkout bug", "teamId": team.ID}, http.StatusCreated)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Triage me", "teamId": team.ID, "stateId": bootstrap.States[backlog].ID}, http.StatusCreated)
	user := bootstrap.Users[slices.IndexFunc(bootstrap.Users, func(user domain.User) bool { return user.Active && !user.App && user.ID != bootstrap.Viewer.ID })]
	f := triageFilterFixture{repo: repo, handler: handler, bootstrap: bootstrap, issue: issue, other: other, project: bootstrap.Projects[0], label: bootstrap.Labels[0], user: user, team: bootstrap.Teams[len(bootstrap.Teams)-1]}
	now := time.Now().UTC()
	suggestion := func(id, kind string) domain.IssueSuggestion {
		item := domain.IssueSuggestion{ID: id, IssueID: issue.ID, Type: kind, State: "active", StateChangedAt: now, CreatedAt: now, UpdatedAt: now}
		switch kind {
		case "assignee":
			item.SuggestedUserID = f.user.ID
		case "project":
			item.SuggestedProjectID = f.project.ID
		case "label":
			item.SuggestedLabelID = f.label.ID
		case "team":
			item.SuggestedTeamID = f.team.ID
		default:
			item.SuggestedIssueID = other.ID
		}
		return item
	}
	if err := repo.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), issue.ID), bootstrap.Workspace.URLKey, "test.suggestions", issue.ID, nil, func(data *domain.Bootstrap) error {
		target, err := issueByID(data, issue.ID)
		if err != nil {
			return err
		}
		storeTriageSuggestions(data, target, []domain.IssueSuggestion{
			suggestion("s-assignee", "assignee"), suggestion("s-project", "project"), suggestion("s-label", "label"),
			suggestion("s-team", "team"), suggestion("s-duplicate", "similarIssue"), suggestion("s-related", "relatedIssue"),
		}, now, triageSourceHeuristic, "")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f triageFilterFixture) record(t *testing.T) domain.Issue {
	t.Helper()
	return requestJSON[domain.Issue](t, f.handler, http.MethodGet, "/api/issue-records/"+f.issue.ID, nil, http.StatusOK)
}

// matches reports whether the triage issue matches filter on the paged (SQL)
// path and on the in-memory query path; both must agree.
func (f triageFilterFixture) matches(t *testing.T, filter map[string]any) bool {
	t.Helper()
	raw, _ := json.Marshal(filter)
	paged := requestJSON[store.IssueRecordPage](t, f.handler, http.MethodGet, "/api/issue-records?limit=100&filter="+url.QueryEscape(string(raw)), nil, http.StatusOK)
	memory := requestJSON[issueQueryResponse](t, f.handler, http.MethodGet, "/api/issues?limit=100&filter="+url.QueryEscape(string(raw)), nil, http.StatusOK)
	contains := func(items []domain.Issue) bool {
		return slices.ContainsFunc(items, func(item domain.Issue) bool { return item.ID == f.issue.ID })
	}
	if contains(paged.Items) != contains(memory.Items) {
		t.Fatalf("paged and in-memory filters disagree for %s: paged=%v memory=%v", raw, contains(paged.Items), contains(memory.Items))
	}
	return contains(paged.Items)
}

func TestTriageSuggestionTargetsAreStoredIndexedAndFilterable(t *testing.T) {
	f := newTriageFilterFixture(t)
	issue := f.record(t)
	if !slices.Equal(issue.SuggestedAssigneeIDs, []string{f.user.ID}) || !slices.Equal(issue.SuggestedProjectIDs, []string{f.project.ID}) || !slices.Equal(issue.SuggestedLabelIDs, []string{f.label.ID}) || !slices.Equal(issue.SuggestedTeamIDs, []string{f.team.ID}) || !slices.Equal(issue.SuggestedDuplicateIDs, []string{f.other.ID}) || !slices.Equal(issue.SuggestedRelatedIDs, []string{f.other.ID}) {
		t.Fatalf("suggestion targets were not stored on the issue: %+v", issue)
	}
	present := func(field string) map[string]any { return map[string]any{"field": field, "operator": "isNotEmpty"} }
	for _, field := range []string{"suggestedAssignee:" + f.user.ID, "suggestedProject:" + f.project.ID, "suggestedLabel:" + f.label.ID, "suggestedTeam:" + f.team.ID, "suggestedDuplicate:" + f.other.ID, "suggestedRelated:" + f.other.ID, "suggested:assignee", "suggested:project", "suggested:label", "suggested:team", "suggested:duplicate", "suggested:related"} {
		if !f.matches(t, present(field)) {
			t.Fatalf("%s did not match the triage issue", field)
		}
	}
	if f.matches(t, present("suggestedAssignee:someone-else")) || f.matches(t, map[string]any{"field": "suggested:assignee", "operator": "isEmpty"}) {
		t.Fatal("suggested assignee filter matched the wrong issues")
	}
	// Named fields of the in-memory vocabulary agree too.
	if !f.matches(t, map[string]any{"and": []any{present("suggested:duplicate"), map[string]any{"field": "id", "values": []string{f.issue.ID}}}}) {
		t.Fatal("combined filter failed")
	}

	// Dismissing drops the target from the issue and its index.
	requestJSON[domain.IssueSuggestion](t, f.handler, http.MethodPost, "/api/issues/"+f.issue.ID+"/suggestions/s-assignee/dismiss", nil, http.StatusOK)
	if issue = f.record(t); len(issue.SuggestedAssigneeIDs) != 0 || issue.Version <= f.issue.Version {
		t.Fatalf("dismissed assignee suggestion still stored: %+v", issue.SuggestedAssigneeIDs)
	}
	if f.matches(t, present("suggested:assignee")) || f.matches(t, present("suggestedAssignee:"+f.user.ID)) {
		t.Fatal("dismissed assignee suggestion still filterable")
	}
	// Accepting applies the suggestion and drops it.
	requestJSON[domain.IssueSuggestion](t, f.handler, http.MethodPost, "/api/issues/"+f.issue.ID+"/suggestions/s-label/accept", nil, http.StatusOK)
	if issue = f.record(t); len(issue.SuggestedLabelIDs) != 0 || !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return label.ID == f.label.ID }) {
		t.Fatalf("accepted label suggestion: labels=%+v suggested=%+v", issue.Labels, issue.SuggestedLabelIDs)
	}
	if f.matches(t, present("suggested:label")) {
		t.Fatal("accepted label suggestion still filterable")
	}

	// Deleting a suggested target prunes it from the issue.
	requestJSON[map[string]any](t, f.handler, http.MethodDelete, "/api/projects/"+f.project.ID, nil, http.StatusNoContent)
	if issue = f.record(t); len(issue.SuggestedProjectIDs) != 0 || f.matches(t, present("suggested:project")) {
		t.Fatalf("deleted project still suggested: %+v", issue.SuggestedProjectIDs)
	}
	requestJSON[map[string]any](t, f.handler, http.MethodDelete, "/api/issues/"+f.other.ID, nil, http.StatusNoContent)
	if issue = f.record(t); len(issue.SuggestedDuplicateIDs) != 0 || len(issue.SuggestedRelatedIDs) != 0 || f.matches(t, present("suggested:duplicate")) {
		t.Fatalf("deleted issue still suggested: dup=%+v related=%+v", issue.SuggestedDuplicateIDs, issue.SuggestedRelatedIDs)
	}
	if !slices.Equal(issue.SuggestedTeamIDs, []string{f.team.ID}) {
		t.Fatalf("unrelated suggestions were pruned: %+v", issue.SuggestedTeamIDs)
	}
}

func TestTriageSuggestionTargetsFollowRefreshAndLeavingTriage(t *testing.T) {
	f := newTriageFilterFixture(t)
	// Run again regenerates: the issue mirrors exactly the active suggestions.
	refreshed := requestJSON[[]domain.IssueSuggestion](t, f.handler, http.MethodPost, "/api/issues/"+f.issue.ID+"/suggestions/refresh", nil, http.StatusOK)
	issue := f.record(t)
	want := map[string][]string{}
	for _, item := range refreshed {
		if item.State != "active" {
			continue
		}
		key := map[string]string{"assignee": "assignee", "project": "project", "label": "label", "team": "team", "similarIssue": "duplicate", "relatedIssue": "related"}[item.Type]
		want[key] = append(want[key], firstNonEmpty(item.SuggestedUserID, item.SuggestedProjectID, item.SuggestedLabelID, item.SuggestedTeamID, item.SuggestedIssueID))
	}
	for key, got := range map[string][]string{"assignee": issue.SuggestedAssigneeIDs, "project": issue.SuggestedProjectIDs, "label": issue.SuggestedLabelIDs, "team": issue.SuggestedTeamIDs, "duplicate": issue.SuggestedDuplicateIDs, "related": issue.SuggestedRelatedIDs} {
		expected := slices.Compact(slices.Sorted(slices.Values(want[key])))
		if !slices.Equal(got, expected) {
			t.Fatalf("%s after refresh: got %v want %v", key, got, expected)
		}
	}
	// Leaving triage clears every suggestion target.
	started := f.bootstrap.States[slices.IndexFunc(f.bootstrap.States, func(state domain.WorkflowState) bool {
		return state.Type == "started"
	})]
	requestJSON[domain.Issue](t, f.handler, http.MethodPatch, "/api/issue-records/"+f.issue.ID, map[string]any{"stateId": started.ID}, http.StatusOK)
	issue = f.record(t)
	if len(issue.SuggestedAssigneeIDs)+len(issue.SuggestedProjectIDs)+len(issue.SuggestedLabelIDs)+len(issue.SuggestedTeamIDs)+len(issue.SuggestedDuplicateIDs)+len(issue.SuggestedRelatedIDs) != 0 {
		t.Fatalf("triaged issue kept suggestion targets: %+v", issue)
	}
}

func TestSyncIssueSuggestionTargetsSkipsMissingTargets(t *testing.T) {
	data := &domain.Bootstrap{
		Users:  []domain.User{{ID: "active", Active: true}, {ID: "suspended"}},
		Labels: []domain.IssueLabel{{ID: "label"}},
		Teams:  []domain.Team{{ID: "team"}},
		IssueSuggestions: []domain.IssueSuggestion{
			{IssueID: "i", Type: "assignee", State: "active", SuggestedUserID: "active"},
			{IssueID: "i", Type: "assignee", State: "active", SuggestedUserID: "suspended"},
			{IssueID: "i", Type: "label", State: "dismissed", SuggestedLabelID: "label"},
			{IssueID: "i", Type: "label", State: "active", SuggestedLabelID: "deleted"},
			{IssueID: "i", Type: "team", State: "active", SuggestedTeamID: "team"},
			{IssueID: "other", Type: "team", State: "active", SuggestedTeamID: "team"},
		},
	}
	issue := &domain.Issue{ID: "i"}
	if !syncIssueSuggestionTargets(data, issue) || !slices.Equal(issue.SuggestedAssigneeIDs, []string{"active"}) || len(issue.SuggestedLabelIDs) != 0 || !slices.Equal(issue.SuggestedTeamIDs, []string{"team"}) {
		t.Fatalf("targets: %+v", issue)
	}
	if syncIssueSuggestionTargets(data, issue) {
		t.Fatal("unchanged targets reported a change")
	}
}

func TestAssigneePickerAppSelectionDelegatesAndFiltersBySession(t *testing.T) {
	_, handler, data, app := applicationFixture(t)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Agent work", "teamId": data.Teams[0].ID}, http.StatusCreated)
	// The assignee picker sends the agent as assigneeId (like Linear); the
	// server turns it into a delegation and keeps a human assignee.
	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"assigneeId": app.UserID}, http.StatusOK)
	if updated.Delegate == nil || updated.Delegate.ID != app.UserID || updated.AgentSessionID == "" || updated.Assignee == nil || updated.Assignee.App {
		t.Fatalf("assigning an agent did not delegate: %+v", updated)
	}
	query := func(filter map[string]any) []domain.Issue {
		raw, _ := json.Marshal(filter)
		return requestJSON[store.IssueRecordPage](t, handler, http.MethodGet, "/api/issue-records?limit=100&filter="+url.QueryEscape(string(raw)), nil, http.StatusOK).Items
	}
	items := query(map[string]any{"field": "agentSessionState", "values": []string{"pending", "active", "awaitingInput"}})
	if len(items) != 1 || items[0].ID != issue.ID || items[0].AgentSessionState != "pending" {
		t.Fatalf("active session filter: %+v", items)
	}
	if items = query(map[string]any{"field": "agentSessionState", "values": []string{"error"}}); len(items) != 0 {
		t.Fatalf("error session filter: %+v", items)
	}
	memory := requestJSON[issueQueryResponse](t, handler, http.MethodGet, "/api/issues?limit=100&filter="+url.QueryEscape(`{"field":"agentSessionState","values":["pending"]}`), nil, http.StatusOK)
	if len(memory.Items) != 1 || memory.Items[0].ID != issue.ID {
		t.Fatalf("in-memory session filter: %+v", memory.Items)
	}
}

func TestBuiltinFlowAgentIsInstalledAtStartupWhenEnabled(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "builtin.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := &server{store: repo, authDisabled: true, uploadPath: t.TempDir()}
	s.agent.Enabled = true
	for range 2 {
		s.ensureBuiltinApplications(context.Background())
	}
	data := repo.Bootstrap()
	agents := slices.DeleteFunc(slices.Clone(data.Users), func(user domain.User) bool { return !user.BuiltinAgent })
	if len(agents) != 1 || agents[0].DisplayName != "Flow" || !agents[0].CanDelegateTo(data.Teams[0].ID) {
		t.Fatalf("built-in agent members: %+v", agents)
	}
	// The built-in agent is offered and delegatable through the assignee path.
	handler := newHandler(s)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Ask Flow", "teamId": data.Teams[0].ID, "assigneeId": agents[0].ID}, http.StatusCreated)
	if issue.Delegate == nil || issue.Delegate.ID != agents[0].ID {
		t.Fatalf("create with the Flow agent as assignee: %+v", issue)
	}
	s.agent.Enabled = false
	s.ensureBuiltinApplications(context.Background())
	if user, err := repo.UserByID(t.Context(), agents[0].ID); err != nil || user.Active {
		t.Fatalf("disabled agent still active: %+v %v", user, err)
	}
}
