package main

import (
	"context"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// auditLogOf is a workspace's audit log as the table holds it, newest first.
func auditLogOf(t testing.TB, repo *store.SQLiteStore, workspaceKey string) []domain.AuditLogEntry {
	t.Helper()
	workspaceID, err := repo.WorkspaceIDForKey(context.Background(), workspaceKey)
	if err != nil {
		t.Fatalf("workspace %q: %v", workspaceKey, err)
	}
	entries, err := repo.ListAuditEntries(context.Background(), workspaceID, store.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// auditLogEntries is auditLogOf for helpers without a *testing.T (none when
// the workspace or table cannot be read).
func auditLogEntries(repo *store.SQLiteStore, workspaceKey string) []domain.AuditLogEntry {
	workspaceID, err := repo.WorkspaceIDForKey(context.Background(), workspaceKey)
	if err != nil {
		return nil
	}
	entries, _ := repo.ListAuditEntries(context.Background(), workspaceID, store.AuditQuery{})
	return entries
}
