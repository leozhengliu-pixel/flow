package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"flow/api/internal/domain"
)

// Issue updates and issue discussions share collections of the stored
// workspace copy with their callbacks instead of copying them (projects and
// trash for issue updates, see issueUpdateMetadata; trash, hidden, for
// discussions). A callback that changed one in place would change the copy
// every request is served from without writing the database. After issue
// writes that read projects (project, milestone, team move, state) and
// discussion writes, the writing server's copy of those collections must
// equal the database's (decoded by the other server).
func TestIssueWritesLeaveTheSharedCollectionsAlone(t *testing.T) {
	peers := newPeerServers(t)
	base := peers.first.URL
	admin := peerAdmin(t, base)
	const workspace = "test-workspace"
	teamsURL := base + "/api/workspaces/" + workspace + "/teams"
	home := authRequest[domain.Team](t, admin, http.MethodPost, teamsURL, map[string]any{"name": "Shared Home", "key": "SHH"}, "", http.StatusCreated)
	away := authRequest[domain.Team](t, admin, http.MethodPost, teamsURL, map[string]any{"name": "Shared Away", "key": "SHA"}, "", http.StatusCreated)
	project := authRequest[domain.Project](t, admin, http.MethodPost, base+"/api/projects", map[string]any{"name": "Shared project", "teamIds": []string{home.ID}}, workspace, http.StatusCreated)
	milestone := authRequest[domain.ProjectMilestone](t, admin, http.MethodPost, base+"/api/projects/"+project.ID+"/milestones", map[string]any{"name": "Shared milestone"}, workspace, http.StatusCreated)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, base+"/api/issues", map[string]any{"title": "Shared issue", "teamId": home.ID}, workspace, http.StatusCreated)
	// Trash entries to keep.
	for _, title := range []string{"Trashed one", "Trashed two"} {
		trashed := authRequest[domain.Issue](t, admin, http.MethodPost, base+"/api/issues", map[string]any{"title": title, "teamId": home.ID}, workspace, http.StatusCreated)
		authRequest[any](t, admin, http.MethodDelete, base+"/api/issues/"+trashed.ID, nil, workspace, http.StatusNoContent)
	}
	states := authRequest[domain.Bootstrap](t, admin, http.MethodGet, base+"/api/bootstrap", nil, workspace, http.StatusOK).States
	done := ""
	for _, state := range states {
		if state.Type == "completed" && (state.TeamID == home.ID || state.TeamID == "" && done == "") {
			done = state.ID
		}
	}

	issueURL := base + "/api/issues/" + issue.ID
	for _, update := range []map[string]any{
		{"projectId": project.ID},
		{"projectMilestoneId": milestone.ID},
		{"title": "Shared issue renamed", "priority": 2},
		{"stateId": done},
		{"teamId": away.ID},
		{"projectId": project.ID},
	} {
		authRequest[domain.Issue](t, admin, http.MethodPatch, issueURL, update, workspace, http.StatusOK)
	}
	comment := authRequest[domain.Comment](t, admin, http.MethodPost, issueURL+"/comments", map[string]any{"body": "Shared comment"}, workspace, http.StatusCreated)
	authRequest[domain.Comment](t, admin, http.MethodPost, issueURL+"/comments", map[string]any{"body": "Shared reply", "parentId": comment.ID}, workspace, http.StatusCreated)
	authRequest[domain.Comment](t, admin, http.MethodPatch, issueURL+"/comments/"+comment.ID, map[string]any{"body": "Shared comment edited"}, workspace, http.StatusOK)
	authRequest[any](t, admin, http.MethodPost, issueURL+"/comments/"+comment.ID+"/reactions", map[string]any{"emoji": "👍"}, workspace, http.StatusOK)

	written, ok := peers.stores[0].WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	if err := peers.stores[1].ReloadWorkspace(context.Background(), workspace); err != nil {
		t.Fatal(err)
	}
	stored, _ := peers.stores[1].WorkspaceMetadata(workspace)
	if len(stored.Trash) < 2 {
		t.Fatalf("stored trash has %d entries", len(stored.Trash))
	}
	for name, pair := range map[string][2]any{"projects": {written.Projects, stored.Projects}, "trash": {written.Trash, stored.Trash}} {
		memory, _ := json.Marshal(pair[0])
		database, _ := json.Marshal(pair[1])
		if string(memory) != string(database) {
			t.Errorf("the writing server's %s differ from the database's:\nmemory   %s\ndatabase %s", name, memory, database)
		}
	}
}
