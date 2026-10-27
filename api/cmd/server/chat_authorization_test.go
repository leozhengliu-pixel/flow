package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestChatAuthorizedOperationsPreserveActorVersionAndCommentIdempotency(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "chat-authorized.db")
	repository, err := store.OpenSQLiteTestFixture(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir()}
	data := repository.Bootstrap()
	identity := domain.ChatIdentity{UserID: data.Viewer.ID, AuthProvider: "password"}
	ctx := withChatIdentityAuthentication(context.Background(), identity)
	var created domain.Issue
	input := domain.IssueCreateInput{Title: "Created through a verified chat identity", TeamID: data.Teams[0].ID}
	if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodPost, "/api/issue-records", input, &created); err != nil {
		t.Fatal(err)
	}
	var updated domain.Issue
	if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodPatch, "/api/issue-records/"+created.ID, map[string]any{"title": "First update", "expectedVersion": created.Version}, &updated); err != nil {
		t.Fatal(err)
	}
	if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodPatch, "/api/issue-records/"+created.ID, map[string]any{"title": "Stale overwrite", "expectedVersion": created.Version}, nil); err == nil {
		t.Fatal("stale card overwrote an updated issue")
	} else {
		var failure *chatMutationError
		if !errors.As(err, &failure) || failure.Status != http.StatusConflict {
			t.Fatalf("expected version conflict, got %v", err)
		}
	}
	ctx = store.WithCommentCreationKey(ctx, "comment_chat_replayed_command")
	var first, retry domain.Comment
	for _, result := range []*domain.Comment{&first, &retry} {
		if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodPost, "/api/issue-records/"+created.ID+"/comments", domain.CommentCreateInput{Body: "A single comment across worker restarts"}, result); err != nil {
			t.Fatal(err)
		}
	}
	if first.ID != retry.ID || first.User.ID != identity.UserID {
		t.Fatalf("comment duplicate or actor changed: %+v %+v", first, retry)
	}
	full, ok := repository.BootstrapFor(data.Workspace.URLKey)
	if !ok || len(full.Comments[created.ID]) != 1 {
		t.Fatalf("comment retry was not atomic: %#v", full.Comments[created.ID])
	}
	if err := s.chatAuthorizedMutation(context.Background(), data.Workspace.URLKey, identity.UserID, http.MethodGet, "/api/issue-records/"+created.ID, nil, nil); err == nil {
		t.Fatal("unverified caller gained bound-user access")
	}
	if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodPost, "/api/api-keys", nil, nil); err == nil {
		t.Fatal("chat bridge dispatched an arbitrary credential route")
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, `UPDATE auth_users SET active=0 WHERE id=?`, identity.UserID); err != nil {
		t.Fatal(err)
	}
	if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodGet, "/api/issue-records/"+created.ID, nil, nil); err == nil {
		t.Fatal("deactivated Flow account retained chat access")
	}
}

func TestChatIdentityRechecksCurrentWorkspaceAuthenticationPolicy(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir()}
	data := repository.Bootstrap()
	identity := domain.ChatIdentity{UserID: data.Viewer.ID, AuthProvider: "password"}
	if err := repository.MutateWorkspace(context.Background(), data.Workspace.URLKey, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.RequireTwoFactor = true
		data.WorkspaceSettings.DisableAdminBypass = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := withChatIdentityAuthentication(context.Background(), identity)
	if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, nil); err == nil {
		t.Fatal("binding without MFA bypassed the new workspace policy")
	}
	now := time.Now().UTC()
	identity.MFAVerifiedAt = &now
	ctx = withChatIdentityAuthentication(context.Background(), identity)
	if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, nil); err != nil {
		t.Fatalf("valid consent evidence was rejected: %v", err)
	}
	if err := repository.MutateWorkspace(context.Background(), data.Workspace.URLKey, "identity_provider.updated", "provider_enforced", nil, func(data *domain.Bootstrap) error {
		data.IdentityProviders = append(data.IdentityProviders, domain.IdentityProvider{ID: "provider_enforced", Enabled: true, Enforced: true, Issuer: "https://login.example.test"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.chatAuthorizedMutation(ctx, data.Workspace.URLKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, nil); err == nil {
		t.Fatal("old password binding bypassed newly enforced enterprise authentication")
	}
}
