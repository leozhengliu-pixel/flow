package main

import (
	"net/http"

	"flow/api/internal/domain"
)

// listAuditLog returns the workspace audit log (admins only, like the
// bootstrap copy) so the audit log page can refresh without a full reload.
func (s *server) listAuditLog(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	entries := data.AuditLog
	if entries == nil {
		entries = []domain.AuditLogEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
