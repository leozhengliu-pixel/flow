package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21TriageFixture struct {
	scaleP21M2Fixture
	reloaded              *store.SQLiteStore
	private, child        string
	triage, visible, gone domain.Issue
	hidden                domain.Issue
	projects              []string
}

// The fixture seeds every target class the literal list filter tests: visible,
// private, sub-team, mixed, unscoped, archived and missing projects;
// visible, private, inherited and missing teams; visible, granted-private and
// missing issues; inactive states, other issues' rows and duplicate IDs.
func scaleP21TriageSetup(t *testing.T, repo *store.SQLiteStore) scaleP21TriageFixture {
	t.Helper()
	return scaleP21TriageSeed(t, scaleP21M2Setup(t, repo))
}

func scaleP21TriageSeed(t *testing.T, m2 scaleP21M2Fixture) scaleP21TriageFixture {
	t.Helper()
	f := scaleP21TriageFixture{scaleP21M2Fixture: m2}
	repo := m2.repository
	workspace := f.data.Workspace.URLKey
	triage := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "Triage list bounded differential", "teamId": f.public}, workspace, http.StatusCreated)
	visible := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "Visible related target", "teamId": f.public}, workspace, http.StatusCreated)
	full, ok := repo.BootstrapFor(workspace)
	if !ok {
		t.Fatal("triage fixture workspace missing")
	}
	for _, team := range full.Teams {
		switch {
		case strings.HasPrefix(team.ID, "m2-private-"):
			f.private = team.ID
		case strings.HasPrefix(team.ID, "m2-child-"):
			f.child = team.ID
		}
	}
	for _, issue := range full.Issues {
		switch issue.ID {
		case triage.ID:
			f.triage = issue
		case visible.ID:
			f.visible = issue
		}
		if issue.Title == "M2 private grant" {
			f.hidden = issue
		}
	}
	if f.private == "" || f.child == "" || f.triage.ID == "" || f.visible.ID == "" || f.hidden.ID == "" {
		t.Fatal("triage fixture targets missing")
	}
	f.gone = domain.Issue{ID: "triage-missing-issue"}
	archived := time.Date(2026, 3, 4, 5, 6, 7, 8, time.UTC)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	generated := stamp.Add(time.Minute)
	suggestion := func(id, issue, kind, state string, target ...string) domain.IssueSuggestion {
		item := domain.IssueSuggestion{
			ID: id, IssueID: issue, Type: kind, State: state, StateChangedAt: stamp, CreatedAt: stamp, UpdatedAt: stamp.Add(time.Second),
			Metadata: map[string]any{"rank": 1, "score": 0.625, "reasons": []string{"Exact\x00reason", "第二"}, "nested": map[string]any{"empty": []any{}, "null": nil}},
		}
		switch kind {
		case "project":
			item.SuggestedProjectID = target[0]
		case "team":
			item.SuggestedTeamID = target[0]
		case "assignee":
			item.SuggestedUserID = target[0]
		case "label":
			item.SuggestedLabelID = target[0]
		default:
			item.SuggestedIssueID = target[0]
		}
		return item
	}
	f.projects = []string{"triage-p-public", "triage-p-private", "triage-p-child", "triage-p-mixed", "triage-p-unscoped", "triage-p-archived", "triage-p-ghost", "m2-unrelated"}
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.triage_seed", "", nil, func(data *domain.Bootstrap) error {
		if data.WorkspaceSettings.FeatureFlags == nil {
			data.WorkspaceSettings.FeatureFlags = map[string]bool{}
		}
		data.WorkspaceSettings.FeatureFlags["triage-intelligence"] = true
		for _, team := range []string{f.public, f.private} {
			settings := data.TeamSettings[team]
			settings.TeamID, settings.TriageEnabled = team, true
			if team == f.private {
				settings.Access = "private"
			}
			data.TeamSettings[team] = settings
		}
		data.Projects = append(data.Projects,
			domain.Project{ID: "triage-p-public", Name: "Public", TeamIDs: []string{f.public}},
			domain.Project{ID: "triage-p-private", Name: "Private", TeamIDs: []string{f.private}},
			domain.Project{ID: "triage-p-child", Name: "Child", TeamIDs: []string{f.child}},
			domain.Project{ID: "triage-p-mixed", Name: "Mixed", TeamIDs: []string{f.private, f.public}},
			domain.Project{ID: "triage-p-unscoped", Name: "Unscoped", TeamIDs: []string{}},
			domain.Project{ID: "triage-p-archived", Name: "Archived", TeamIDs: []string{f.public}, ArchivedAt: &archived},
		)
		var items []domain.IssueSuggestion
		for index, project := range f.projects {
			items = append(items, suggestion("triage-s-project-"+project, f.triage.ID, "project", "active", project))
			if index%3 == 0 {
				items = append(items, suggestion("triage-s-dismissed-"+project, f.triage.ID, "project", "dismissed", project))
			}
		}
		for _, team := range []string{f.public, f.private, f.child, "triage-team-ghost"} {
			items = append(items, suggestion("triage-s-team-"+team, f.triage.ID, "team", "active", team))
		}
		for _, target := range []string{f.visible.ID, f.hidden.ID, f.gone.ID, f.triage.ID} {
			items = append(items, suggestion("triage-s-similar-"+target, f.triage.ID, "similarIssue", "active", target))
			items = append(items, suggestion("triage-s-related-"+target, f.triage.ID, "relatedIssue", "accepted", target))
		}
		items = append(items,
			suggestion("triage-s-assignee", f.triage.ID, "assignee", "active", data.Viewer.ID),
			suggestion("triage-s-label", f.triage.ID, "label", "active", "triage-label-ghost"),
			suggestion("triage-s-assignee", f.triage.ID, "assignee", "active", "triage-user-ghost"),
			suggestion("triage-s-other", f.visible.ID, "project", "active", "triage-p-public"),
			suggestion("triage-s-hidden", f.hidden.ID, "team", "active", f.private),
			suggestion("triage-s-hidden-project", f.hidden.ID, "project", "active", "triage-p-mixed"),
		)
		items[3].Metadata = nil
		data.IssueSuggestions = append(data.IssueSuggestions, items...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = repo.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), f.triage.ID), workspace, "scale_p21.triage_seed_issue", f.triage.ID, nil, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, f.triage.ID)
		if err != nil {
			return err
		}
		issue.SuggestionsGeneratedAt = &generated
		issue.SuggestionsSource = triageSourceAI
		issue.SuggestionsThinking = "Exact\x00thinking"
		f.triage = *issue
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal("refresh warm view:", err)
	}
	// A second view loads its installed generation from canonical SQL.
	f.reloaded, err = repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal("reloaded view:", err)
	}
	return f
}

func (f scaleP21TriageFixture) services(agent bool) []*server {
	services := []*server{{store: f.repository}, {store: f.probe}, {store: f.reloaded}}
	for _, service := range services {
		service.agent.Enabled = agent
	}
	return services
}

func TestScaleP21TriageListSuggestionsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21TriageSetup(t, repo)
		workspace := f.data.Workspace.URLKey
		cases, distinct, statuses := 0, map[string]bool{}, map[int]bool{}
		call := func(t *testing.T, services []*server, prepare func() *http.Request, id string) {
			t.Helper()
			var outcomes []string
			for index, service := range services {
				r := prepare()
				r.SetPathValue("id", id)
				w := httptest.NewRecorder()
				service.listIssueSuggestions(w, r)
				outcomes = append(outcomes, w.Result().Status+"\n"+w.Body.String())
				distinct[outcomes[0]], statuses[w.Code] = true, true
				if index > 0 && outcomes[index] != outcomes[0] {
					t.Fatalf("list %q service %d changed:\nlegacy:  %s\nbounded: %s", id, index, outcomes[0], outcomes[index])
				}
			}
			cases++
		}
		ids := []string{f.triage.ID, f.triage.Identifier, f.visible.ID, f.hidden.ID, f.gone.ID}
		for _, agent := range []bool{false, true} {
			services := f.services(agent)
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				for _, id := range ids {
					call(t, services, func() *http.Request { return f.request(user, key, nil) }, id)
				}
			})
		}
		// Absent request actors fall back to the installed Viewer; bindings
		// cover the default, missing and foreign workspace selections.
		for _, binding := range []string{workspace, "", "triage-foreign-workspace"} {
			for _, cancelled := range []bool{false, true} {
				for _, user := range []domain.User{{}, f.data.Viewer, f.principals[2].user} {
					prepare := func() *http.Request {
						r := f.request(user, nil, nil)
						r.Header.Set("X-Workspace-Key", binding)
						if cancelled {
							ctx, cancel := context.WithCancel(r.Context())
							cancel()
							r = r.WithContext(ctx)
						}
						return r
					}
					for _, id := range []string{f.triage.ID, f.gone.ID} {
						call(t, f.services(false), prepare, id)
					}
				}
			}
		}
		// The response must actually exercise the filter rather than an
		// empty or failing path.
		w := httptest.NewRecorder()
		r := f.request(f.data.Viewer, nil, nil)
		r.SetPathValue("id", f.triage.ID)
		(&server{store: f.probe}).listIssueSuggestions(w, r)
		var body struct {
			Suggestions []domain.IssueSuggestion `json:"suggestions"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Suggestions) < 5 {
			t.Fatalf("owner list did not exercise suggestions: %d %s", w.Code, w.Body.String())
		}
		for _, item := range body.Suggestions {
			if item.State != "active" || item.SuggestedProjectID == "triage-p-private" || item.SuggestedProjectID == "triage-p-ghost" || item.SuggestedTeamID == "triage-team-ghost" || item.SuggestedIssueID == f.gone.ID {
				t.Fatalf("owner list kept a filtered suggestion: %+v", item)
			}
		}
		for _, probe := range []*store.SQLiteStore{f.probe, f.reloaded} {
			if probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("bounded triage list decoded complete metadata")
			}
		}
		// Principals, credentials and bindings must reach distinct filtered
		// lists, missing issues, access refusals and projection failures.
		for _, code := range []int{http.StatusOK, http.StatusNotFound, http.StatusInternalServerError} {
			if !statuses[code] {
				t.Fatalf("differential never observed status %d: %v", code, statuses)
			}
		}
		if len(distinct) < 8 {
			t.Fatalf("differential observed only %d distinct responses", len(distinct))
		}
		t.Logf("triage list differential cases=%d (x3 services), distinct responses=%d, statuses=%v", cases, len(distinct), statuses)
	}
}

// The primitives are compared directly with the literal accessors they
// replace: all states, duplicates and order of one issue's suggestions, and
// the paged projection's team rows and project filter for every principal.
func TestScaleP21TriageSuggestionPrimitivesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21TriageSetup(t, repo)
		workspace := f.data.Workspace.URLKey
		legacy, ok := repo.WorkspaceMetadata(workspace)
		if !ok {
			t.Fatal("legacy metadata missing")
		}
		for _, probe := range []*store.SQLiteStore{f.probe, f.reloaded} {
			installed, found, err := probe.BeginInstalledMetadataRead(t.Context(), workspace)
			if err != nil || !found {
				t.Fatal("installed read", found, err)
			}
			for _, id := range []string{f.triage.ID, f.visible.ID, f.hidden.ID, f.gone.ID, ""} {
				want := slices.DeleteFunc(slices.Clone(legacy.IssueSuggestions), func(item domain.IssueSuggestion) bool { return item.IssueID != id })
				read, err := installed.ReadIssueSuggestions(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				if a, b := scaleP21TriageJSON(t, want), scaleP21TriageJSON(t, read.Suggestions); a != b {
					t.Fatalf("suggestions for %q changed:\nlegacy:  %s\nbounded: %s", id, a, b)
				}
				if id == f.triage.ID && len(read.Suggestions) < 20 {
					t.Fatal("fixture suggestions missing", len(read.Suggestions))
				}
				read.Close()
			}
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				actor := mcpActor{WorkspaceKey: workspace, User: user}
				if key != nil {
					actor.APIKey = *key
				}
				data, legacyErr := (&server{store: repo}).mcpWorkspaceData(t.Context(), actor)
				scope, err := installed.ViewerProjectionScope(t.Context(), user.ID)
				if (legacyErr != nil) != (err != nil) {
					t.Fatal("projection error changed", legacyErr, err)
				}
				if err != nil {
					return
				}
				var credential func(string) bool
				if apiKeyTeamRestrictionSelected(actor.APIKey) {
					credential = func(id string) bool { return slices.Contains(actor.APIKey.TeamIDs, id) }
				}
				teams, rows := map[string]bool{}, map[string]bool{}
				for _, team := range data.Teams {
					teams[team.ID] = true
				}
				for id := range scope.Teams {
					if credential == nil || credential(id) {
						rows[id] = true
					}
				}
				if !reflect.DeepEqual(teams, rows) {
					t.Fatalf("team rows changed: %v %v", teams, rows)
				}
				for _, id := range append(slices.Clone(f.projects), "") {
					want := slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
					got, err := installed.ProjectVisibleInProjection(t.Context(), scope, id, credential)
					if err != nil || got != want {
						t.Fatalf("project %q visibility %v, legacy %v: %v", id, got, want, err)
					}
				}
			})
			for _, user := range []string{"", "triage-not-a-member"} {
				_, legacyErr := (&server{store: repo}).mcpWorkspaceData(t.Context(), mcpActor{WorkspaceKey: workspace, User: domain.User{ID: user}})
				if _, err := installed.ViewerProjectionScope(t.Context(), user); legacyErr == nil || err == nil {
					t.Fatal("non-member projection succeeded", legacyErr, err)
				}
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := installed.ReadIssueSuggestions(cancelled, f.triage.ID); err == nil {
				t.Fatal("cancelled suggestion read succeeded")
			}
			if _, err := installed.ViewerProjectionScope(cancelled, f.data.Viewer.ID); err == nil {
				t.Fatal("cancelled projection succeeded")
			}
			installed.Close()
			if _, err := installed.ReadIssueSuggestions(t.Context(), f.triage.ID); err == nil {
				t.Fatal("closed installed read still selected suggestions")
			}
			if probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("triage primitives decoded complete metadata")
			}
		}
	}
}

func scaleP21TriageJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The bounded projector equals mcpWorkspaceData for every collection the
// triage scorers read, for every principal and credential, including an
// application key without initiative scope; the AI prompt and the preview
// response built from it are byte-identical.
func TestScaleP21TriageProjectedWorkspaceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21TriageSetup(t, repo)
		workspace := f.data.Workspace.URLKey
		at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.triage_projection_seed", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureSettings.TriageIntelligence.WorkspaceGuidance = "Route preview work to the marketplace project."
			data.LabelGroups = append(data.LabelGroups, domain.LabelGroup{ID: "triage-g-private", Name: "Private group", Scope: f.private})
			data.Labels = append(data.Labels,
				domain.IssueLabel{ID: "triage-l-preview", Name: "Image preview", Description: "Marketplace image preview", Scope: "workspace"},
				domain.IssueLabel{ID: "triage-l-private-grouped", Name: "Private preview", Scope: f.private, GroupID: "triage-g-private"},
				domain.IssueLabel{ID: "triage-l-public-team", Name: "Public preview", Scope: f.public, GroupID: "triage-g-private"},
				domain.IssueLabel{ID: "triage-l-archived", Name: "Old preview", ArchivedAt: &at},
			)
			for index := range data.Projects {
				if data.Projects[index].ID == "triage-p-public" {
					data.Projects[index].Description = "Marketplace image preview component work."
					data.Projects[index].LabelIDs = []string{"triage-l-preview", "triage-l-public-team"}
					data.Projects[index].Lead = &data.Viewer
					data.Projects[index].MemberIDs = []string{data.Viewer.ID}
					data.Projects[index].DependencyIDs = []string{"triage-p-private", "triage-p-mixed"}
				}
			}
			data.Initiatives = append(data.Initiatives,
				domain.Initiative{ID: "triage-i-public", Name: "Public initiative", ContributingTeamIDs: []string{f.public}, ProjectIDs: []string{"triage-p-public"}, LabelIDs: []string{"triage-l-preview"}},
				domain.Initiative{ID: "triage-i-private", Name: "Private initiative", LeadTeamID: f.private, LabelIDs: []string{"triage-l-private-grouped", "triage-l-preview"}},
			)
			settings := data.TeamSettings[f.public]
			settings.Description = "Marketplace and preview work."
			data.TeamSettings[f.public] = settings
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		views := []*store.SQLiteStore{f.probe}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		reloaded, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		views = append(views, reloaded)
		pick := func(data domain.Bootstrap) string {
			return scaleP21TriageJSON(t, map[string]any{
				"viewer": data.Viewer, "role": data.ViewerRole, "teams": data.Teams, "teamSettings": data.TeamSettings,
				"workspaceSettings": data.WorkspaceSettings, "users": data.Users, "members": data.Members, "teamMembers": data.TeamMembers,
				"projects": data.Projects, "labels": data.Labels, "labelGroups": data.LabelGroups, "initiatives": data.Initiatives,
			})
		}
		app := &domain.APIKey{ID: "m2-app", Actor: "app", Scopes: []string{"read", "write"}, TeamRestriction: "all"}
		pairs := 0
		check := func(t *testing.T, user domain.User, key *domain.APIKey) {
			actor := mcpActor{WorkspaceKey: workspace, User: user}
			if key != nil {
				actor.APIKey = *key
			}
			legacy, legacyErr := (&server{store: repo}).mcpWorkspaceData(t.Context(), actor)
			issue := f.triage
			pool := []domain.Issue{f.visible, f.hidden}
			for _, view := range views {
				installed, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
				if err != nil || !found {
					t.Fatal("installed read", found, err)
				}
				bounded, release, err := (&server{store: view}).mcpWorkspaceDataProjected(t.Context(), actor, installed, triageWorkspaceCollections...)
				if (legacyErr == nil) != (err == nil) || legacyErr != nil && legacyErr.Error() != err.Error() {
					t.Fatalf("projection error changed: %v / %v", legacyErr, err)
				}
				if err == nil {
					if a, b := pick(legacy), pick(bounded); a != b {
						t.Fatalf("projected workspace changed: %s", scaleP21M2Diff(a, b))
					}
					left, right := buildTriageAIContext(&legacy, issue, pool), buildTriageAIContext(&bounded, issue, pool)
					if a, b := triageAIPrompt(&left), triageAIPrompt(&right); a != b {
						t.Fatalf("triage AI prompt changed: %s", scaleP21M2Diff(a, b))
					}
				}
				release()
				installed.Close()
				if view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("projected workspace decoded complete metadata")
				}
			}
			pairs++
		}
		f.matrix(t, check)
		for _, principal := range f.principals {
			t.Run(principal.name+"/app", func(t *testing.T) { check(t, principal.user, app) })
		}
		for _, user := range []domain.User{{}, {ID: "triage-not-a-member"}} {
			t.Run("non-member", func(t *testing.T) { check(t, user, nil) })
		}
		// The preview route itself, through every source.
		for _, query := range []string{"text=Marketplace+image+preview", "text=Marketplace+image+preview&teamId=" + f.private, "text=", "text=" + strings.Repeat("x", 501)} {
			for _, principal := range f.principals {
				var outcomes []string
				for _, service := range []*server{{store: repo}, {store: f.probe}, {store: reloaded}} {
					r := f.request(principal.user, nil, nil)
					r.URL.RawQuery = query
					w := httptest.NewRecorder()
					service.previewIssueSuggestions(w, r)
					outcomes = append(outcomes, w.Result().Status+"\n"+w.Body.String())
				}
				if outcomes[1] != outcomes[0] || outcomes[2] != outcomes[0] {
					t.Fatalf("preview %q for %s changed: %v", query, principal.name, outcomes)
				}
			}
		}
		t.Logf("projected workspace pairs=%d", pairs)
	}
}
