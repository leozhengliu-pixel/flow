package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestMoveWorkspaceLabelGroupToTeams(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	before := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	first := before.Issues[0]
	secondTeam := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Second team", "key": "SEC"}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Second team issue", "teamId": secondTeam.ID}, http.StatusCreated)
	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Stage", "resourceType": "issue", "multiSelect": true}, http.StatusCreated)
	alpha := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Alpha", "resourceType": "issue", "groupId": group.ID}, http.StatusCreated)
	beta := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{"name": "Beta", "resourceType": "issue", "groupId": group.ID}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+first.ID, map[string]any{"labelIds": []string{alpha.ID}}, http.StatusOK)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+second.ID, map[string]any{"labelIds": []string{alpha.ID, beta.ID}}, http.StatusOK)

	moved := requestJSON[[]domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups/"+group.ID+"/move-to-teams", map[string]any{}, http.StatusOK)
	teams := map[string]bool{first.Team.ID: true, secondTeam.ID: true}
	if len(moved) != 2 {
		t.Fatalf("moved groups %#v", moved)
	}
	for _, copy := range moved {
		if !teams[copy.Scope] || copy.ID == group.ID || copy.Name != "Stage" || !copy.MultiSelect {
			t.Fatalf("invalid team group %#v", copy)
		}
	}
	after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(after.LabelGroups, func(item domain.LabelGroup) bool { return item.ID == group.ID }) ||
		slices.ContainsFunc(after.Labels, func(label domain.IssueLabel) bool { return label.ID == alpha.ID || label.ID == beta.ID }) {
		t.Fatal("the workspace group or its labels survived the move")
	}
	for _, copy := range moved {
		names := []string{}
		for _, label := range after.Labels {
			if label.GroupID == copy.ID {
				if label.Scope != copy.Scope {
					t.Fatalf("label %#v is not in its group's team", label)
				}
				names = append(names, label.Name)
			}
		}
		if slices.Sort(names); !slices.Equal(names, []string{"Alpha", "Beta"}) {
			t.Fatalf("team group %s labels %v", copy.Scope, names)
		}
	}
	for issueID, want := range map[string][]string{first.ID: {"Alpha"}, second.ID: {"Alpha", "Beta"}} {
		issue := findIssue(t, after.Issues, issueID)
		names := []string{}
		for _, label := range issue.Labels {
			if label.Scope != issue.Team.ID {
				t.Fatalf("issue %s kept a label outside its team: %#v", issueID, label)
			}
			names = append(names, label.Name)
		}
		if slices.Sort(names); !slices.Equal(names, want) {
			t.Fatalf("issue %s labels %v want %v", issueID, names, want)
		}
	}
	// A team group cannot be moved again, and an unused group is refused.
	requestJSON[any](t, handler, http.MethodPost, "/api/label-groups/"+moved[0].ID+"/move-to-teams", map[string]any{}, http.StatusNotFound)
	unused := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{"name": "Unused", "resourceType": "issue"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, "/api/label-groups/"+unused.ID+"/move-to-teams", map[string]any{}, http.StatusBadRequest)
}
