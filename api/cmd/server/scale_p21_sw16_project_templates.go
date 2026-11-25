package main

import (
	"context"
	"net/http"
)

func (s *server) scaleP21SW16ListProjectTemplates(w http.ResponseWriter, r *http.Request) {
	// The original BootstrapFor is context-insensitive. Snapshot that same
	// installed source without cancelling it on an already-ended request.
	read, found, err := s.store.BeginProjectTemplatesRead(context.WithoutCancel(r.Context()), workspaceKey(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	defer read.Close()
	writeJSON(w, http.StatusOK, read.Templates())
}
