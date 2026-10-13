package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"flow/api/internal/store"
)

// uninstallApplicationInstallation removes an installed application from the
// workspace (admins only): its tokens are revoked, its active sessions
// canceled, its app user leaves the workspace and its managed webhooks are
// removed.
func (s *server) uninstallApplicationInstallation(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	if !s.authDisabled && !workspaceAdminRole(data.ViewerRole) {
		writeError(w, http.StatusForbidden, "Workspace admin access required")
		return
	}
	_, err := s.store.UninstallApplication(r.Context(), data.Workspace.URLKey, r.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "Application not found")
		return
	case errors.Is(err, store.ErrBuiltinApplication):
		writeError(w, http.StatusConflict, "The built-in Flow agent can't be uninstalled; turn off Flow Agent instead")
		return
	case err != nil:
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	s.reconcileApplicationWebhooks(context.WithoutCancel(r.Context()), data.Workspace.URLKey)
	w.WriteHeader(http.StatusNoContent)
}
