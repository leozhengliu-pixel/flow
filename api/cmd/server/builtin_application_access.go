package main

import (
	"context"
	"net/http"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type builtinInvokerContextKey struct{}

func (s *server) builtinApplicationContext(r *http.Request, app domain.ApplicationInstallation, task domain.AgentTask) (*http.Request, domain.Bootstrap, error) {
	user, err := s.store.UserByID(r.Context(), task.CreatorID)
	if err != nil || !user.Active || user.App {
		return r, domain.Bootstrap{}, store.ErrAuthForbidden
	}
	metadata, ok := s.store.WorkspaceMetadata(task.WorkspaceKey)
	if !ok {
		return r, metadata, store.ErrAuthForbidden
	}
	role, status, err := s.store.WorkspaceRole(r.Context(), metadata.Workspace.ID, user.ID)
	if err != nil || status != "active" || agentWorkspacePolicy(metadata.WorkspaceSettings, role) != nil {
		return r, metadata, store.ErrAuthForbidden
	}
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	data, err := s.applicationTaskContext(r.WithContext(ctx), task, false)
	if err != nil {
		return r, data, err
	}
	data.Viewer, data.ViewerRole = app.User(), "app"
	ctx = context.WithValue(r.Context(), builtinInvokerContextKey{}, user)
	return r.WithContext(ctx), data, nil
}
