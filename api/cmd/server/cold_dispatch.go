package main

import (
	"context"
	"net/http"

	"flow/api/internal/store"
)

type scaleColdRoute struct {
	method, path, consumer, writer string
}

// These bindings admit the installed authorization bridge and selected handler
// bridges. Writer names alone certify neither publication nor activation.
var scaleColdRoutes = []scaleColdRoute{
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
	{http.MethodPost, "/api/drafts", "server/advanced_features.go:createDraft", "draft.created"},
	{http.MethodPatch, "/api/drafts/{id}", "server/advanced_features.go:updateDraft", "draft.updated"},
	{http.MethodGet, "/api/oauth/authorized-applications", "server/oauth_authorized_applications.go:listAuthorizedApplications", ""},
	{http.MethodPatch, "/api/oauth-applications/{id}", "server/settings_features.go:updateOAuthApplication", "oauth_application.updated"},
	{http.MethodDelete, "/api/oauth/authorized-applications/mcp", "server/oauth_authorized_applications.go:revokeMCPAuthorizations", ""},
	{http.MethodPost, "/api/oauth-applications", "server/settings_features.go:createOAuthApplication", "oauth_application.created"},
	{http.MethodDelete, "/api/oauth-applications/{id}", "server/settings_features.go:deleteOAuthApplication", "oauth_application.deleted"},
	{http.MethodPost, "/api/trash/{id}/restore", "server/advanced_features.go:restoreTrashEntry", "trash.restored"},
	{http.MethodDelete, "/api/trash/{id}", "server/advanced_features.go:purgeTrashEntry", "trash.purged"},
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
