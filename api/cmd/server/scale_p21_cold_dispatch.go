package main

import (
	"context"
	"net/http"

	"flow/api/internal/store"
)

type scaleP21ColdRoute struct {
	method, path, consumer, writer string
}

// This manifest registers only production bridges already installed below.
// It is deliberately not an activation waiver for other cold consumers.
var scaleP21ColdRoutes = []scaleP21ColdRoute{
	{http.MethodPost, "/api/project-statuses", "server/settings_features.go:createProjectStatus", "project_status.created"},
	{http.MethodPatch, "/api/project-statuses/{id}", "server/settings_features.go:updateProjectStatus", "project_status.updated"},
	{http.MethodDelete, "/api/project-statuses/{id}", "server/settings_features.go:deleteProjectStatus", "project_status.deleted"},
	{http.MethodPost, "/api/project-statuses/reorder", "server/settings_features.go:reorderProjectStatuses", "project_status.reordered"},
	{http.MethodPost, "/api/issue-templates", "server/settings_features.go:createWorkspaceIssueTemplate", "issue_template.created"},
	{http.MethodPatch, "/api/issue-templates/{id}", "server/settings_features.go:updateWorkspaceIssueTemplate", "issue_template.updated"},
	{http.MethodDelete, "/api/issue-templates/{id}", "server/settings_features.go:deleteWorkspaceIssueTemplate", "issue_template.deleted"},
	{http.MethodPost, "/api/document-templates", "server/settings_features.go:createDocumentTemplate", "document_template.created"},
	{http.MethodPatch, "/api/document-templates/{id}", "server/settings_features.go:updateDocumentTemplate", "document_template.updated"},
	{http.MethodDelete, "/api/document-templates/{id}", "server/settings_features.go:deleteDocumentTemplate", "document_template.deleted"},
	{http.MethodGet, "/api/loop-config", "server/loop_settings.go:getLoopConfig", ""},
	{http.MethodGet, "/api/workspace/loop-settings", "server/loop_settings.go:getLoopConfig", ""},
	{http.MethodPatch, "/api/workspace/loop-settings", "server/loop_settings.go:updateLoopSettings", "workspace_preferences.updated"},
	{http.MethodPost, "/api/drafts", "server/advanced_features.go:createDraft", "draft.created"},
	{http.MethodPatch, "/api/drafts/{id}", "server/advanced_features.go:updateDraft", "draft.updated"},
}

func (s *server) withColdMetadataRequest(w http.ResponseWriter, r *http.Request, consumer, writer string, run func(*store.SQLiteStore, *http.Request)) {
	if !s.store.BoundedMetadataEnabled() {
		run(s.store, r)
		return
	}
	invoke := func(ctx context.Context, repo *store.SQLiteStore) error {
		run(repo, r.WithContext(ctx))
		return nil
	}
	var err error
	if writer == "" {
		err = s.store.WithColdMetadataRead(r.Context(), consumer, invoke)
	} else {
		err = s.store.WithColdMetadataMutation(r.Context(), consumer, workspaceKey(r), writer, nil, invoke)
	}
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
	}
}
