package main

import (
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
)

func TestTeamHierarchyFiveLevelsAndSubtreeMove(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, authDisabled: true, uploadPath: t.TempDir()})
	parent := ""
	ids := []string{}
	for i := 0; i < 5; i++ {
		team := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": fmt.Sprintf("Level %d", i+1), "key": fmt.Sprintf("L%d", i+1), "parentTeamId": parent}, http.StatusCreated)
		ids = append(ids, team.ID)
		parent = team.ID
	}
	requestJSON[any](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Too deep", "key": "L6", "parentTeamId": parent}, 400)
	requestJSON[any](t, handler, "PATCH", "/api/teams/"+ids[0]+"/settings", map[string]any{"parentTeamId": ids[4]}, 400)
	other := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Other", "key": "OT"}, 201)
	requestJSON[any](t, handler, "PATCH", "/api/teams/"+ids[0]+"/settings", map[string]any{"parentTeamId": other.ID}, 400)
	requestJSON[any](t, handler, "PATCH", "/api/teams/"+ids[1]+"/settings", map[string]any{"parentTeamId": other.ID}, 200)
	requestJSON[any](t, handler, "PATCH", "/api/teams/"+ids[1]+"/settings", map[string]any{"parentTeamId": ""}, 200)
	data := requestJSON[domain.Bootstrap](t, handler, "GET", "/api/bootstrap", nil, 200)
	if data.TeamSettings[ids[1]].ParentTeamID != "" || data.TeamSettings[ids[2]].ParentTeamID != ids[1] {
		t.Fatal("detaching a subtree corrupted descendants")
	}
}

func TestTeamHierarchyIssueQueryIncludesDescendantsOnlyWhenRequested(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, authDisabled: true, uploadPath: t.TempDir()}
	handler := newHandler(s)
	root := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Root", "key": "RT"}, 201)
	child := requestJSON[domain.Team](t, handler, "POST", "/api/workspaces/test-workspace/teams", map[string]any{"name": "Child", "key": "CH", "parentTeamId": root.ID}, 201)
	for _, id := range []string{root.ID, child.ID} {
		requestJSON[domain.Issue](t, handler, "POST", "/api/issue-records", map[string]any{"title": "Scoped issue", "teamId": id}, 201)
	}
	exact := requestJSON[store.IssueRecordPage](t, handler, "GET", "/api/issue-records?teamId="+root.ID+"&includeTotal=true", nil, 200)
	tree := requestJSON[store.IssueRecordPage](t, handler, "GET", "/api/issue-records?teamId="+root.ID+"&includeTotal=true&includeSubTeams=true&limit=1", nil, 200)
	if len(exact.Items) != 1 || tree.Total != 2 || len(tree.Items) != 1 || tree.NextCursor == "" {
		t.Fatalf("wrong scoped pagination: exact=%+v tree=%+v", exact, tree)
	}
	next := requestJSON[store.IssueRecordPage](t, handler, "GET", "/api/issue-records?teamId="+root.ID+"&includeSubTeams=true&limit=1&cursor="+tree.NextCursor, nil, 200)
	if len(next.Items) != 1 || next.Items[0].ID == tree.Items[0].ID {
		t.Fatal("invalid next page")
	}
}
