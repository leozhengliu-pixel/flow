package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func stateNamed(t *testing.T, states []domain.WorkflowState, name string) domain.WorkflowState {
	t.Helper()
	index := slices.IndexFunc(states, func(state domain.WorkflowState) bool { return state.Name == name })
	if index < 0 {
		t.Fatalf("no state %q in %+v", name, states)
	}
	return states[index]
}

func labelIDs(labels []domain.IssueLabel) []string {
	ids := make([]string, 0, len(labels))
	for _, label := range labels {
		ids = append(ids, label.ID)
	}
	slices.Sort(ids)
	return ids
}

// PATCH /api/issues/{id} with teamId moves the issue like the app's team
// picker: a new identifier from the target team (the old one keeps
// resolving), the equivalent status, team-scoped labels mapped or dropped, the
// cycle left, history and an issue.updated event naming the new team.
func TestIssuePatchMovesIssueToAnotherTeam(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	var mu sync.Mutex
	events := []domain.RealtimeEvent{}
	repository.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	})

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	source := bootstrap.Teams[0]
	target := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Move Target", "key": "MOV"}, http.StatusCreated)
	// The target team gets its own workflow: copies of the workspace states plus "Doing".
	requestJSON[domain.WorkflowState](t, handler, http.MethodPost, "/api/teams/"+target.ID+"/states", map[string]any{"name": "Doing", "type": "started"}, http.StatusCreated)
	sourceStates := requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/"+source.ID+"/states", nil, http.StatusOK)
	targetStates := requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/"+target.ID+"/states", nil, http.StatusOK)
	inProgress, targetInProgress := stateNamed(t, sourceStates, "In Progress"), stateNamed(t, targetStates, "In Progress")
	if inProgress.ID == targetInProgress.ID {
		t.Fatalf("target team shares the source state %s", inProgress.ID)
	}

	shared := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Shared"}, http.StatusCreated)
	sourceFrontend := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/teams/"+source.ID+"/labels", map[string]any{"name": "Frontend"}, http.StatusCreated)
	sourceOnly := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/teams/"+source.ID+"/labels", map[string]any{"name": "Source only"}, http.StatusCreated)
	targetFrontend := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/teams/"+target.ID+"/labels", map[string]any{"name": "frontend"}, http.StatusCreated)

	requestJSON[domain.CycleSettings](t, handler, http.MethodPatch, "/api/teams/"+source.ID+"/cycle-settings", map[string]any{"enabled": true, "autoCreate": true, "upcomingCount": 1}, http.StatusOK)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	cycleIndex := slices.IndexFunc(bootstrap.Cycles, func(cycle domain.Cycle) bool { return cycle.TeamID == source.ID })
	if cycleIndex < 0 {
		t.Fatal("no cycle for the source team")
	}
	cycle := bootstrap.Cycles[cycleIndex]

	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Move me", "teamId": source.ID, "stateId": inProgress.ID,
		"labelIds": []string{shared.ID, sourceFrontend.ID, sourceOnly.ID}, "cycleId": cycle.ID,
	}, http.StatusCreated)
	child := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Child stays", "teamId": source.ID, "parentId": issue.ID}, http.StatusCreated)
	if issue.CycleID == nil || len(issue.Labels) != 3 || issue.State.ID != inProgress.ID {
		t.Fatalf("setup issue = %+v", issue)
	}
	oldIdentifier := issue.Identifier
	mu.Lock()
	events = events[:0]
	mu.Unlock()

	moved := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"teamId": target.ID, "expectedVersion": issue.Version}, http.StatusOK)
	if moved.ID != issue.ID || moved.Team.ID != target.ID {
		t.Fatalf("issue not moved: %+v", moved.Team)
	}
	if moved.Number <= child.Number || moved.Identifier != "MOV-"+strconv.Itoa(moved.Number) {
		t.Fatalf("identifier = %s (number %d), want MOV- with a fresh number above %d", moved.Identifier, moved.Number, child.Number)
	}
	if !slices.Equal(moved.PreviousIdentifiers, []string{oldIdentifier}) {
		t.Fatalf("previous identifiers = %v, want [%s]", moved.PreviousIdentifiers, oldIdentifier)
	}
	if moved.State.ID != targetInProgress.ID {
		t.Fatalf("state = %s (%s), want the target team's In Progress %s", moved.State.ID, moved.State.Name, targetInProgress.ID)
	}
	if want := []string{shared.ID, targetFrontend.ID}; !slices.Equal(labelIDs(moved.Labels), sortedCopy(want)) {
		t.Fatalf("labels = %v, want %v (shared kept, Frontend mapped, source-only dropped)", labelIDs(moved.Labels), want)
	}
	if moved.CycleID != nil {
		t.Fatalf("issue kept the source team's cycle %s", *moved.CycleID)
	}

	stored := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID, nil, http.StatusOK)
	if stored.Team.ID != target.ID || stored.Identifier != moved.Identifier {
		t.Fatalf("stored issue = %s in %s", stored.Identifier, stored.Team.ID)
	}
	byOld := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+oldIdentifier, nil, http.StatusOK)
	if byOld.ID != issue.ID || byOld.Identifier != moved.Identifier {
		t.Fatalf("old identifier resolved to %s %s", byOld.ID, byOld.Identifier)
	}
	byNew := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+strings.ToLower(moved.Identifier), nil, http.StatusOK)
	if byNew.ID != issue.ID {
		t.Fatalf("new identifier resolved to %s", byNew.ID)
	}
	// Writes by the old identifier reach the issue too.
	renamed := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+oldIdentifier, map[string]any{"title": "Moved and renamed"}, http.StatusOK)
	if renamed.ID != issue.ID || renamed.Title != "Moved and renamed" {
		t.Fatalf("PATCH by old identifier = %s %q", renamed.ID, renamed.Title)
	}

	childNow := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+child.ID, nil, http.StatusOK)
	if childNow.Team.ID != source.ID || childNow.Identifier != child.Identifier || childNow.ParentID == nil || *childNow.ParentID != issue.ID {
		t.Fatalf("sub-issue changed: %s %s parent=%v", childNow.Identifier, childNow.Team.ID, childNow.ParentID)
	}

	history := requestJSON[store.IssueHistory](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID+"/history", nil, http.StatusOK)
	entry := slices.IndexFunc(history.Activities, func(activity domain.ActivityEvent) bool {
		return activity.Type == "issue.updated" && activity.Metadata["team"] == target.ID
	})
	if entry < 0 {
		t.Fatalf("no team move in the history: %+v", history.Activities)
	}
	metadata := history.Activities[entry].Metadata
	if metadata["teamBefore"] != source.ID || metadata["identifierBefore"] != oldIdentifier || metadata["identifier"] != moved.Identifier || metadata["stateBeforeId"] != inProgress.ID || metadata["cycleBefore"] != cycle.ID || metadata["teamName"] != "Move Target" {
		t.Fatalf("history metadata = %v", metadata)
	}

	mu.Lock()
	var moveEvent *domain.RealtimeEvent
	for index := range events {
		if events[index].Type == "issue.updated" && events[index].AggregateID == issue.ID {
			moveEvent = &events[index]
			break
		}
	}
	mu.Unlock()
	if moveEvent == nil {
		t.Fatal("the move published no issue.updated event")
	}
	var payload struct {
		Entity domain.Issue `json:"entity"`
	}
	if err := json.Unmarshal(moveEvent.Payload, &payload); err != nil || payload.Entity.Team.ID != target.ID || payload.Entity.Identifier != moved.Identifier {
		t.Fatalf("issue.updated entity = %s in %s (%v)", payload.Entity.Identifier, payload.Entity.Team.ID, err)
	}
	// The collaboration access re-check sees the team the issue moved to.
	scope, ok := collaborationAccessScope(*moveEvent)
	if !ok || scope.issues[issue.ID] != target.ID {
		t.Fatalf("collaboration scope = %+v", scope)
	}

	// The sequence moved past the new number: the next issue does not collide.
	next := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "After the move", "teamId": target.ID}, http.StatusCreated)
	if next.Number <= moved.Number {
		t.Fatalf("next issue number %d reuses the moved issue's %d", next.Number, moved.Number)
	}

	// Batch updates move issues the same way, back to the source team.
	back := requestJSON[[]domain.Issue](t, handler, http.MethodPost, "/api/issues/batch", map[string]any{"issueIds": []string{issue.ID}, "update": map[string]any{"teamId": source.ID}}, http.StatusOK)
	if len(back) != 1 || back[0].Team.ID != source.ID || back[0].Number <= next.Number || !strings.HasPrefix(back[0].Identifier, source.Key+"-") {
		t.Fatalf("batch move = %+v", back)
	}
	if !slices.Equal(back[0].PreviousIdentifiers, []string{oldIdentifier, moved.Identifier}) || back[0].State.ID != inProgress.ID {
		t.Fatalf("batch move kept %v in %s", back[0].PreviousIdentifiers, back[0].State.ID)
	}
	for _, identifier := range []string{oldIdentifier, moved.Identifier, back[0].Identifier} {
		if found := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+identifier, nil, http.StatusOK); found.ID != issue.ID {
			t.Fatalf("%s resolved to %s", identifier, found.ID)
		}
	}

	// Moving to the team it is in is a no-op; unknown teams are rejected.
	same := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"teamId": source.ID}, http.StatusOK)
	if same.Identifier != back[0].Identifier || same.Version != back[0].Version {
		t.Fatalf("same-team move changed the issue: %s v%d", same.Identifier, same.Version)
	}
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"teamId": "team_missing"}, http.StatusBadRequest)
}

func sortedCopy(values []string) []string {
	values = slices.Clone(values)
	slices.Sort(values)
	return values
}

// Unsupported fields are rejected instead of being ignored with a success.
func TestIssuePatchRejectsUnsupportedFields(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Strict"}, http.StatusCreated)
	for _, path := range []string{"/api/issues/" + issue.ID, "/api/issue-records/" + issue.ID} {
		response := requestJSON[map[string]any](t, handler, http.MethodPatch, path, map[string]any{"title": "Changed", "team": "MOV"}, http.StatusBadRequest)
		if !strings.Contains(response["error"].(string), `"team"`) {
			t.Fatalf("PATCH %s error = %v", path, response["error"])
		}
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/issues/batch", map[string]any{"issueIds": []string{issue.ID}, "update": map[string]any{"teamKey": "MOV"}}, http.StatusBadRequest)
	if current := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+issue.ID, nil, http.StatusOK); current.Title != "Strict" || current.Version != issue.Version {
		t.Fatalf("a rejected update changed the issue: %q v%d", current.Title, current.Version)
	}
}

// Moving needs the target team's content: members cannot move issues into a
// private team they are not in, and guests cannot move issues at all.
func TestIssueTeamMoveRequiresAccessToTheTargetTeam(t *testing.T) {
	server := newSingleAuthServer(t)
	admin := peerAdmin(t, server.URL)
	home := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Move Home", "key": "MHOM"}, "", http.StatusCreated)
	open := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Move Open", "key": "MOPN"}, "", http.StatusCreated)
	private := authRequest[domain.Team](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Move Private", "key": "MPRV", "private": true}, "", http.StatusCreated)
	join := func(name, email, role string, teams ...string) *http.Client {
		client, user := verifiedAuthClient(t, server.URL, name, email)
		invitations := authRequest[[]domain.Invitation](t, admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{user.Email}, "role": role, "teamIds": teams}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, server.URL+"/api/invitations/accept", map[string]string{"token": invitations[0].Token}, "", http.StatusOK)
		return client
	}
	member := join("Move Member", "move-member@example.com", "member", home.ID)
	guest := join("Move Guest", "move-guest@example.com", "guest", home.ID, open.ID)
	issue := authRequest[domain.Issue](t, member, http.MethodPost, server.URL+"/api/issues", map[string]any{"title": "Guarded move", "teamId": home.ID}, "test-workspace", http.StatusCreated)
	patch := func(client *http.Client, teamID string, status int) domain.Issue {
		return authRequest[domain.Issue](t, client, http.MethodPatch, server.URL+"/api/issues/"+issue.ID, map[string]any{"teamId": teamID}, "test-workspace", status)
	}

	patch(member, private.ID, http.StatusForbidden)
	authRequest[any](t, member, http.MethodPost, server.URL+"/api/issues/batch", map[string]any{"issueIds": []string{issue.ID}, "update": map[string]any{"teamId": private.ID}}, "test-workspace", http.StatusForbidden)
	patch(guest, open.ID, http.StatusForbidden)
	if current := authRequest[domain.Issue](t, admin, http.MethodGet, server.URL+"/api/issue-records/"+issue.ID, nil, "test-workspace", http.StatusOK); current.Team.ID != home.ID || current.Identifier != issue.Identifier {
		t.Fatalf("a refused move changed the issue: %s in %s", current.Identifier, current.Team.ID)
	}

	// A public team's content is open to members; the admin is in the private team.
	moved := patch(member, open.ID, http.StatusOK)
	if moved.Team.ID != open.ID || !strings.HasPrefix(moved.Identifier, "MOPN-") {
		t.Fatalf("member move = %s in %s", moved.Identifier, moved.Team.ID)
	}
	moved = patch(admin, private.ID, http.StatusOK)
	if moved.Team.ID != private.ID || !strings.HasPrefix(moved.Identifier, "MPRV-") {
		t.Fatalf("admin move = %s in %s", moved.Identifier, moved.Team.ID)
	}
	// Signed-in reads and writes find it by its first identifier.
	if found := authRequest[domain.Issue](t, admin, http.MethodGet, server.URL+"/api/issue-records/"+issue.Identifier, nil, "test-workspace", http.StatusOK); found.ID != issue.ID || found.Identifier != moved.Identifier {
		t.Fatalf("old identifier resolved to %s %s", found.ID, found.Identifier)
	}
	if renamed := authRequest[domain.Issue](t, admin, http.MethodPatch, server.URL+"/api/issues/"+issue.Identifier, map[string]any{"title": "Renamed by old identifier"}, "test-workspace", http.StatusOK); renamed.ID != issue.ID || renamed.Title != "Renamed by old identifier" {
		t.Fatalf("PATCH by old identifier = %s %q", renamed.ID, renamed.Title)
	}
	// The member lost the issue with its team, by any identifier.
	authRequest[any](t, member, http.MethodGet, server.URL+"/api/issue-records/"+issue.Identifier, nil, "test-workspace", http.StatusNotFound)
	authRequest[any](t, member, http.MethodGet, server.URL+"/api/issue-records/"+moved.Identifier, nil, "test-workspace", http.StatusNotFound)
}

// A member whose issue description is open loses it when the issue moves to
// a private team they are not in.
func TestIssueMoveToPrivateTeamRevokesCollaboration(t *testing.T) {
	server := newSingleAuthServer(t)
	fixture := newIssueAccessFixture(t, server.URL, server.URL, issueAccessSetup{role: "member", inTeam: true})
	private := authRequest[domain.Team](t, fixture.admin, http.MethodPost, server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Closed Room", "key": "CLRM", "private": true}, "", http.StatusCreated)
	authRequest[domain.Issue](t, fixture.admin, http.MethodPatch, server.URL+"/api/issues/"+fixture.issue.ID, map[string]any{"teamId": private.ID}, "test-workspace", http.StatusOK)
	expectDocumentRevoked(t, fixture.socket, fixture.documentID)
}

// MCP save_issue with `team` on an existing issue moves it like the app:
// a new identifier, the target team's status of the same type, and the old
// identifier still finds it. Teams the caller cannot see are not found.
func TestMCPSaveIssueMovesIssueToAnotherTeam(t *testing.T) {
	f := newMCPContractFixture(t)
	if err := f.repository.UpdateMemberRole(t.Context(), f.data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	member := f.addMCPKey(t, "member-key", "usr_member", []string{"read", "write"}, nil)
	custom := []map[string]any{{"name": "Intake", "type": "backlog"}, {"name": "Ready", "type": "unstarted"}, {"name": "Doing", "type": "started"}, {"name": "Shipped", "type": "completed"}, {"name": "Dropped", "type": "canceled"}}
	f.objectWith(t, member, "save_team", map[string]any{"name": "Plain Move", "key": "PMV"})
	f.objectWith(t, member, "save_team", map[string]any{"name": "Secret Move", "key": "SMV", "private": true, "states": custom})
	f.objectWith(t, f.secret, "save_team", map[string]any{"name": "Hidden Move", "key": "HMV", "private": true, "states": custom})

	created := f.objectWith(t, member, "save_issue", map[string]any{"title": "travelling issue", "team": "PMV", "state": "In Progress"})
	id, oldIdentifier := created["id"].(string), created["identifier"].(string)
	before, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, id)
	if err != nil {
		t.Fatal(err)
	}
	moved := f.objectWith(t, member, "save_issue", map[string]any{"id": id, "team": "SMV"})
	identifier, _ := moved["identifier"].(string)
	if !strings.HasPrefix(identifier, "SMV-") || identifier == oldIdentifier {
		t.Fatalf("moved identifier = %q (was %s)", identifier, oldIdentifier)
	}
	stored, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, id)
	if err != nil {
		t.Fatal(err)
	}
	secret := f.statusesWith(t, member, "SMV")
	if stored.Team.Key != "SMV" || stored.Identifier != identifier || stored.State.Type != "started" || stored.State.ID == before.State.ID || secret[stored.State.Name] != stored.State.ID {
		t.Fatalf("stored issue = %s in %s, state %s %s (was %s), team states %v", stored.Identifier, stored.Team.Key, stored.State.ID, stored.State.Name, before.State.ID, secret)
	}
	// With a status named in the same call, the status resolves in the new team.
	back := f.objectWith(t, member, "save_issue", map[string]any{"id": id, "team": "PMV", "state": "Todo"})
	if after, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, id); err != nil || after.Team.Key != "PMV" || after.State.Name != "Todo" || !strings.HasPrefix(back["identifier"].(string), "PMV-") {
		t.Fatalf("move back = %v / %+v %v", back["identifier"], after.State, err)
	}
	moved = f.objectWith(t, member, "save_issue", map[string]any{"id": id, "team": "SMV", "state": "Doing"})
	if after, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, id); err != nil || after.State.ID != secret["Doing"] {
		t.Fatalf("move with state = %+v %v", after.State, err)
	}
	identifier = moved["identifier"].(string)
	if raw, _ := json.Marshal(f.objectWith(t, member, "get_issue", map[string]any{"id": oldIdentifier})); !strings.Contains(string(raw), identifier) {
		t.Fatalf("get_issue by the old identifier: %s", raw)
	}
	mcpDeniedWith(t, f, member, "save_issue", map[string]any{"id": id, "team": "HMV"}, "not found")
	if after, err := f.repository.IssueRecord(t.Context(), f.data.Workspace.URLKey, id); err != nil || after.Team.Key != "SMV" {
		t.Fatalf("a refused move changed the issue: %+v %v", after.Team, err)
	}
}
