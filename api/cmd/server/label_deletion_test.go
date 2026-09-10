package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestLabelDeletionPreservesScopeAndMissingResourceResponses(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "labels.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	teamID := data.Teams[0].ID
	if err := repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "label.created", "scoped-label", nil, func(next *domain.Bootstrap) error {
		next.Labels = append(next.Labels, domain.IssueLabel{ID: "scoped-label", Name: "Team label", Scope: teamID}, domain.IssueLabel{ID: "workspace-label", Name: "Workspace label", Scope: "Workspace", ResourceType: "project"})
		next.LabelGroups = append(next.LabelGroups, domain.LabelGroup{ID: "private-group", Name: "Private group", Scope: teamID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[any](t, handler, http.MethodDelete, "/api/labels/scoped-label", nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, "/api/label-groups/private-group", nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, "/api/teams/"+teamID+"/labels/workspace-label", nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, "/api/labels/workspace-label", nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, "/api/labels/workspace-label", nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, "/api/teams/"+teamID+"/labels/scoped-label", nil, http.StatusNoContent)
	metadata, _ := repo.WorkspaceMetadata(data.Workspace.URLKey)
	if slices.ContainsFunc(metadata.Labels, func(label domain.IssueLabel) bool { return label.ID == "scoped-label" || label.ID == "workspace-label" }) {
		t.Fatal("deleted label is cached")
	}
}
