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
	identity := domain.ChatIdentity{UserID: data.Viewer.ID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
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
	identity := domain.ChatIdentity{UserID: data.Viewer.ID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
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

func TestChatIdentityExpiresWithWorkspaceSessionDuration(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-session-duration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir()}
	data := repository.Bootstrap()
	if err := repository.MutateWorkspace(context.Background(), data.Workspace.URLKey, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.SessionDurationDays = 7
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read := func(identity domain.ChatIdentity) error {
		return s.chatAuthorizedMutation(withChatIdentityAuthentication(context.Background(), identity), data.Workspace.URLKey, identity.UserID, http.MethodGet, "/api/issue-records/bootstrap", nil, nil)
	}
	identity := domain.ChatIdentity{UserID: data.Viewer.ID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC().Add(-6 * 24 * time.Hour)}
	if err := read(identity); err != nil {
		t.Fatalf("binding within the session duration was rejected: %v", err)
	}
	for name, consented := range map[string]time.Time{"expired": time.Now().UTC().Add(-8 * 24 * time.Hour), "missing consent time": {}} {
		identity.AuthenticatedAt = consented
		err := read(identity)
		var failure *chatMutationError
		if !errors.As(err, &failure) || failure.Status != http.StatusUnauthorized || failure.Message != chatReconnectMessage {
			t.Fatalf("%s binding was not asked to reconnect: %v", name, err)
		}
	}
	if err := repository.MutateWorkspace(context.Background(), data.Workspace.URLKey, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.RequireTwoFactor = true
		data.WorkspaceSettings.DisableAdminBypass = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	staleMFA := time.Now().UTC().Add(-8 * 24 * time.Hour)
	identity = domain.ChatIdentity{UserID: data.Viewer.ID, AuthProvider: "password", AuthenticatedAt: time.Now().UTC(), MFAVerifiedAt: &staleMFA}
	if err := read(identity); err == nil {
		t.Fatal("second-factor evidence older than the session duration was accepted")
	}
	freshMFA := time.Now().UTC().Add(-time.Hour)
	identity.MFAVerifiedAt = &freshMFA
	if err := read(identity); err != nil {
		t.Fatalf("recent second-factor evidence was rejected: %v", err)
	}
}

func TestChatNotificationsResumeAfterTheirCursorFallsOutOfRetention(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "chat-cursor-gap.db")
	repository, err := store.OpenSQLiteTestFixture(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir()}
	workspace := repository.Bootstrap().Workspace.URLKey
	ctx := context.Background()
	if err := repository.InitializeChatDomainCursor(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if err := repository.MutateWorkspace(ctx, workspace, "integration.updated", "chat", nil, func(*domain.Bootstrap) error { return nil }); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(ctx, `UPDATE workspace_event_seq SET head=head+5,pruned=head+5 WHERE workspace_id=(SELECT workspace_id FROM workspace_states WHERE workspace_key=?)`, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ChatDomainEventPage(ctx, workspace, 10); !errors.Is(err, store.ErrChatDomainCursorExpired) {
		t.Fatalf("expected an expired cursor, got %v", err)
	}
	if err := s.queueCommittedChatEvents(ctx, workspace); err != nil {
		t.Fatalf("expired cursor stopped notifications: %v", err)
	}
	if _, err := repository.ChatDomainEventPage(ctx, workspace, 10); err != nil {
		t.Fatalf("cursor was not moved past the lost window: %v", err)
	}
}
