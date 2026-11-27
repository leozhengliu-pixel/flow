package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Supply a principal as authentication would, then exercise authorization and
// alias dispatch before ServeMux populates the path values.
func commentActionReadScopeHandler(s *server, gate *metadataReadGate, original bool) http.Handler {
	mux := http.NewServeMux()
	list := s.listCommentActions
	if original {
		list = s.originalList
	}
	mux.HandleFunc("GET /api/issue-records/{id}/comment-actions", s.issueRecordAlias(list))
	return boundedLegacyIssueWrites(gate.holdProjections(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, release := beginRequestResourceReads(r)
		defer release()
		s.resolveIssuePathReference(r)
		workspace, userID := workspaceKey(r), authUser(r).ID
		var originalData domain.Bootstrap
		var handoff *issueRecordAuthorization
		if original {
			// Restore the two pre-fix workspace reads while exercising the
			// same issue query, resource policy and alias handoff.
			_, query, err := s.issueRecordsQuery(r)
			if err == nil {
				originalData, err = s.store.PagedWorkspaceMetadata(r.Context(), query.Workspace, userID)
			}
			if err == nil {
				filterBootstrapForAPIKey(&originalData, r)
				originalData, err = s.issueRecordAuthorizationRows(r, originalData, query, false)
			}
			if err != nil {
				writeError(w, http.StatusForbidden, "This resource is outside your teams")
				return
			}
			handoff = &issueRecordAuthorization{path: r.URL.Path, workspace: workspace, userID: userID, data: originalData}
			*r = *r.WithContext(context.WithValue(r.Context(), issueRecordAuthorizationKey{}, handoff))
		}
		if !s.authorizeResourceRequest(w, r, workspace, userID) {
			return
		}
		if handoff != nil {
			handoff.data = originalData
			handoff.taken.Store(false)
		}
		mux.ServeHTTP(w, r)
	})))
}

func (s *server) originalList(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	data.Viewer = requestActor(s, r)
	if _, settings, ok := s.accountCollections(r.Context(), data.Workspace.URLKey, data.Viewer.ID, false); ok && settings.UserID != "" {
		data.UserSettings = map[string]domain.UserSettings{data.Viewer.ID: settings}
	}
	issue, err := issueByID(&data, r.PathValue("id"))
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	result := []domain.CommentAction{}
	if s.commentActionsAllowed(data, *issue) {
		raw, _ := s.store.WorkspaceMetadata(data.Workspace.URLKey)
		for _, action := range raw.CommentActions {
			if action.IssueID != issue.ID || action.Status == "dismissed" {
				continue
			}
			comment, err := s.store.ResourceComment(r.Context(), data.Workspace.URLKey, issue.ID, action.CommentID)
			if err != nil || action.Status == "pending" && comment.Version != action.CommentVersion {
				continue
			}
			if action.Status == "accepted" || validateCommentAction(&raw, *issue, action) == nil {
				result = append(result, action)
			}
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func commentActionReadScopeRequest(workspace, path string, user domain.User, key *domain.APIKey) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Workspace-Key", workspace)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	if key != nil {
		ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
	}
	return r.WithContext(store.ContextWithActor(ctx, user))
}

func TestLegacyCommentActionReadAllocations(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo, err := store.OpenSQLite(t.TempDir() + "/sw240.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := resourceAccessScaleFixture(t, repo)
	workspace, owner := f.data.Workspace.URLKey, f.data.Viewer
	team := f.data.Teams[0].ID
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW240 allocation issue", "teamId": team}, workspace, http.StatusCreated)
	service := &server{store: repo, agent: appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true}}
	path := "/api/issue-records/" + issue.ID + "/comment-actions"
	gate := newMetadataReadGate(1, 0, time.Second, 0)
	handlers := []http.Handler{commentActionReadScopeHandler(service, gate, true), commentActionReadScopeHandler(service, gate, false)}
	var allocated [2][2]int64
	var escapedAllocs [2]float64
	for i, count := range []int{0, 100} {
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw240_projects", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = make([]domain.Project, 0, count)
			for index := range count {
				data.Projects = append(data.Projects, domain.Project{
					ID: fmt.Sprintf("sw240-unrelated-project-%03d", index), Name: "Unrelated project", TeamIDs: []string{team},
				})
			}
			data.WorkspaceSettings.FeatureFlags = map[string]bool{"ai": true}
			data.UserSettings = map[string]domain.UserSettings{owner.ID: {UserID: owner.ID, AgentEnabled: true}}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for j, handler := range handlers {
			result := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, commentActionReadScopeRequest(workspace, path, owner, nil))
					if response.Code != http.StatusOK || response.Body.String() != "[]\n" {
						b.Fatal(response.Code, response.Body.String())
					}
				}
			})
			allocated[i][j] = result.AllocedBytesPerOp()
			t.Logf("unrelated_projects=%d original=%t bytes/read=%d allocs/read=%d", count, j == 0, allocated[i][j], result.AllocsPerOp())
		}
		// ServeMux treats the escaped reference as one path segment.
		escapedPath := "/api/issue-records/" + issue.ID + "%2Fmissing/comment-actions"
		escapedAllocs[i] = testing.AllocsPerRun(20, func() {
			response := httptest.NewRecorder()
			handlers[1].ServeHTTP(response, commentActionReadScopeRequest(workspace, escapedPath, owner, nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("escaped issue reference: HTTP %d (%s)", response.Code, response.Body.String())
			}
		})
	}
	if growth := allocated[1][0] - allocated[0][0]; growth < 64<<10 {
		t.Fatalf("original route did not reproduce allocation growth: %d bytes/read", growth)
	}
	if delta := allocated[1][1] - allocated[0][1]; delta < -(8<<10) || delta > 8<<10 {
		t.Fatalf("scoped route allocation changed by %d bytes/read with 100 unrelated projects", delta)
	}
	if delta := escapedAllocs[1] - escapedAllocs[0]; delta < -128 || delta > 128 {
		t.Fatalf("escaped reference allocation changed by %.0f allocations/read with 100 unrelated projects", delta)
	}
	if admissions := gate.served.Load(); admissions != 0 {
		t.Fatalf("empty comment-action reads used %d projection slots", admissions)
	}

	// Keep a slot free and exhaust only the shared byte budget. The
	// development HEAD alias and explicit broader selections must use it.
	development := &server{store: repo, authDisabled: true, agent: service.agent}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/issues/{id}/comment-actions", development.listCommentActions)
	fallbackGate := newMetadataReadGate(2, 0, time.Second, 0)
	fallbackGate.projectionBudget = newLegacyProjectionBudget(func(*http.Request) int64 { return 1 }, 1)
	if !fallbackGate.projectionBudget.used.TryAcquire(1) {
		t.Fatal("could not set up shared projection budget")
	}
	defer fallbackGate.projectionBudget.used.Release(1)
	authenticated := commentActionReadScopeHandler(service, fallbackGate, false)
	for _, test := range []struct {
		name, method, path, body string
		handler                  http.Handler
	}{
		{name: "development HEAD", method: http.MethodHead, path: "/api/issues/" + issue.ID + "/comment-actions", handler: fallbackGate.holdProjections(mux)},
		{name: "release filter", method: http.MethodGet, path: path + "?releaseId=missing-release", handler: authenticated},
		{name: "extra issue IDs", method: http.MethodGet, path: path, body: `{"issueIds":["another-issue"]}`, handler: authenticated},
	} {
		request := commentActionReadScopeRequest(workspace, test.path, owner, nil)
		request.Method = test.method
		if test.body != "" {
			request = httptest.NewRequestWithContext(request.Context(), test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("X-Workspace-Key", workspace)
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		test.handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" {
			t.Fatalf("%s bypassed projection budget: HTTP %d (%s)", test.name, response.Code, response.Body.String())
		}
	}
}

func TestCommentActionReadDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := notificationsSetup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	outsider, admin := f.principals[0].user, f.principals[1].user
	member, guest, teammate := f.principals[2].user, f.principals[3].user, f.principals[4].user
	raw, ok := repo.WorkspaceMetadata(workspace)
	privateIndex := slices.IndexFunc(raw.Teams, func(team domain.Team) bool { return team.Private })
	if !ok || privateIndex < 0 {
		t.Fatal("private team missing from fixture")
	}
	private := raw.Teams[privateIndex]
	prefix := idgen.New("sw240-")
	id, parentID, commentID := prefix+"issue", prefix+"parent", prefix+"comment"
	stateID, globalID, labelID, userID := prefix+"inherited-state", prefix+"global-state", prefix+"inherited-label", prefix+"assignee"
	stamp := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	action := func(name, status, kind string, version int64, args domain.CommentActionArguments) domain.CommentAction {
		return domain.CommentAction{ID: prefix + name, IssueID: id, CommentID: commentID, CommentVersion: version,
			TargetUserID: admin.ID, Status: status, Action: kind, Arguments: args, CreatedAt: stamp}
	}
	labels := []string{labelID}
	want := []domain.CommentAction{
		action("state", "pending", "status", 3, domain.CommentActionArguments{StateID: &stateID}),
		action("label", "pending", "labels", 3, domain.CommentActionArguments{LabelIDs: &labels}),
		action("assign", "pending", "assignee", 3, domain.CommentActionArguments{AssigneeID: &userID}),
		action("accepted", "accepted", "invalid", 1, domain.CommentActionArguments{}),
	}
	actions := append(slices.Clone(want),
		action("stale", "pending", "status", 2, domain.CommentActionArguments{StateID: &stateID}),
		action("suppressed-global", "pending", "status", 3, domain.CommentActionArguments{StateID: &globalID}),
		action("dismissed", "dismissed", "labels", 3, domain.CommentActionArguments{LabelIDs: &labels}),
		action("invalid", "pending", "unknown", 3, domain.CommentActionArguments{}),
	)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw240_actions", "", nil, func(data *domain.Bootstrap) error {
		data.Issues = append(data.Issues,
			domain.Issue{ID: parentID, Identifier: "SW240-41", Team: private, Creator: admin, CreatedAt: stamp, UpdatedAt: stamp,
				Permissions: []domain.IssuePermission{{ID: prefix + "grant", IssueID: parentID, SubjectType: "user", SubjectID: member.ID, Role: "editor"}}},
			domain.Issue{ID: id, Identifier: "SW240-42", PreviousIdentifiers: []string{"OLD240-42"}, Team: private, Creator: admin, ParentID: &parentID, CreatedAt: stamp, UpdatedAt: stamp,
				Permissions: []domain.IssuePermission{{ID: prefix + "guest", IssueID: id, SubjectType: "user", SubjectID: guest.ID, Role: "viewer"}}},
		)
		data.Comments[id] = []domain.Comment{{ID: commentID, Version: 3, User: admin, Body: "Please update this issue.", CreatedAt: stamp}}
		data.CommentActions = append(data.CommentActions, actions...)
		data.States = append(data.States, domain.WorkflowState{ID: stateID, TeamID: f.public}, domain.WorkflowState{ID: globalID})
		data.Labels = append(data.Labels, domain.IssueLabel{ID: labelID, Scope: f.public})
		data.Users = append(data.Users, domain.User{ID: userID, Active: true})
		settings := data.TeamSettings[private.ID]
		settings.ParentTeamID, settings.InheritWorkflowStatuses = f.public, true
		data.TeamSettings[private.ID] = settings
		data.WorkspaceSettings.FeatureFlags = map[string]bool{"ai": true}
		data.WorkspaceSettings.PreventGuestAgents = false
		data.UserSettings = map[string]domain.UserSettings{}
		for _, principal := range f.principals {
			data.UserSettings[principal.user.ID] = domain.UserSettings{UserID: principal.user.ID, AgentEnabled: true}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal(err)
	}
	selected := &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{private.ID}}
	excluded := &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}
	empty := &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected"}
	legacy := &server{store: repo, agent: appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true}}
	bounded := &server{store: f.probe, agent: legacy.agent}
	gate := newMetadataReadGate(1, 0, time.Second, 0)
	handlers := []http.Handler{
		commentActionReadScopeHandler(legacy, gate, true),
		commentActionReadScopeHandler(legacy, gate, false),
		commentActionReadScopeHandler(bounded, gate, false),
	}
	for _, test := range []struct {
		name, ref, query string
		user             domain.User
		key              *domain.APIKey
		allowed          bool
	}{
		{name: "admin-private-member", ref: id, user: admin, allowed: true},
		{name: "member-private-member", ref: id, user: teammate, allowed: true},
		{name: "member-inherited-grant", ref: id, user: member, allowed: true},
		{name: "guest-viewer", ref: id, user: guest},
		{name: "admin-private-outsider", ref: id, user: outsider},
		{name: "selected-team", ref: id, user: admin, key: selected, allowed: true},
		{name: "excluded-team", ref: id, user: member, key: excluded},
		{name: "empty-team-selection", ref: id, user: admin, key: empty},
		{name: "identifier", ref: "sw240-42", user: admin, allowed: true},
		{name: "historical-identifier", ref: "old240-42", user: member, allowed: true},
		{name: "historical-identifier-outsider", ref: "old240-42", user: outsider},
		{name: "missing", ref: prefix + "missing", user: admin},
		{name: "malformed-filter", ref: id, query: "?filter=%7B", user: admin},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, base := range []string{"/api/issue-records/", "/api/issues/"} {
				path := base + test.ref + "/comment-actions" + test.query
				var original *httptest.ResponseRecorder
				for mode, handler := range handlers {
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, commentActionReadScopeRequest(workspace, path, test.user, test.key))
					code := http.StatusForbidden
					if test.allowed {
						code = http.StatusOK
						var got []domain.CommentAction
						if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !reflect.DeepEqual(got, want) {
							t.Fatalf("%s mode %d: unexpected visible actions %s (%v)", path, mode, w.Body.String(), err)
						}
					}
					if w.Code != code {
						t.Fatalf("%s mode %d: status %d, want %d (%s)", path, mode, w.Code, code, w.Body.String())
					}
					if original == nil {
						original = w
					} else if w.Body.String() != original.Body.String() || !reflect.DeepEqual(w.Header(), original.Header()) {
						t.Fatalf("%s mode %d: response differs from original: %s vs %s", path, mode, w.Body.String(), original.Body.String())
					}
				}
			}
		})
	}
}
