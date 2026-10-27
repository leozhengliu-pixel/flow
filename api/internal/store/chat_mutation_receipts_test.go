package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestChatMutationReceiptSurvivesLaterEditsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if repo != nil {
			repo.Close()
		}
	}()
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	issue := data.Issues[0]
	originalVersion := issue.Version
	ctx := WithChatMutationKey(ContextWithActor(t.Context(), data.Viewer), "chat-update-recovered")
	committed, err := repo.UpdateIssueRecord(ctx, workspace, issue.ID, &originalVersion, IssueMutationScope{}, func(_ *domain.Bootstrap, current *domain.Issue) error { current.Title = "Applied by chat"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if id, found, err := repo.ChatMutationReceipt(t.Context(), workspace, data.Viewer.ID, ChatMutationKey(ctx)); err != nil || !found || id != issue.ID {
		t.Fatalf("receipt not committed atomically: %q %v %v", id, found, err)
	}
	webCtx := ContextWithActor(t.Context(), data.Viewer)
	webVersion := committed.Version
	later, err := repo.UpdateIssueRecord(webCtx, workspace, issue.ID, &webVersion, IssueMutationScope{}, func(_ *domain.Bootstrap, current *domain.Issue) error { current.Title = "Later web edit"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo = nil
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := repo.UpdateIssueRecord(ctx, workspace, issue.ID, &originalVersion, IssueMutationScope{}, func(*domain.Bootstrap, *domain.Issue) error {
		t.Fatal("replayed update ran after durable receipt")
		return nil
	})
	if err != nil || replayed.Title != later.Title || replayed.Version != later.Version {
		t.Fatalf("retry failed or overwrote a later web edit: %#v %v", replayed, err)
	}
	events, err := repo.Events(t.Context(), issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	updates := 0
	for _, event := range events {
		if event.Type == "issue.updated" {
			updates++
		}
	}
	if updates != 2 {
		t.Fatalf("replay emitted duplicate update event: %d", updates)
	}
}

func TestChatMutationReceiptsScopeKeysAndRollback(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	issue := data.Issues[0]
	ctx := WithChatMutationKey(ContextWithActor(t.Context(), data.Viewer), "actor-scoped-key")
	if _, err = repo.UpdateIssueRecord(ctx, workspace, issue.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, current *domain.Issue) error { current.Title = "Scoped update"; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ workspace, user, key string }{{"other-workspace", data.Viewer.ID, ChatMutationKey(ctx)}, {workspace, "another-user", ChatMutationKey(ctx)}, {workspace, data.Viewer.ID, "another-key"}} {
		if _, found, err := repo.ChatMutationReceipt(t.Context(), scope.workspace, scope.user, scope.key); err != nil || found {
			t.Fatalf("receipt leaked across scope: %#v %v %v", scope, found, err)
		}
	}
	other := data.Issues[1]
	if _, err = repo.UpdateIssueRecord(ctx, workspace, other.ID, nil, IssueMutationScope{}, func(*domain.Bootstrap, *domain.Issue) error {
		t.Fatal("same command key targeted another issue")
		return nil
	}); !errors.Is(err, ErrIssueQuery) {
		t.Fatalf("cross-issue key reuse accepted: %v", err)
	}
	noActor := WithChatMutationKey(t.Context(), "no-actor")
	if _, err = repo.UpdateIssueRecord(noActor, workspace, issue.ID, nil, IssueMutationScope{}, func(*domain.Bootstrap, *domain.Issue) error { t.Fatal("unidentified chat actor executed"); return nil }); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("missing actor accepted: %v", err)
	}
	app := data.Viewer
	app.App = true
	appCtx := WithChatMutationKey(ContextWithActor(t.Context(), app), "app-actor")
	if _, err = repo.UpdateIssueRecord(appCtx, workspace, issue.ID, nil, IssueMutationScope{}, func(*domain.Bootstrap, *domain.Issue) error {
		t.Fatal("application identity executed personal chat command")
		return nil
	}); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("app actor accepted: %v", err)
	}
	before, err := repo.IssueRecord(t.Context(), workspace, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	rollbackCtx := WithChatMutationKey(ContextWithActor(t.Context(), data.Viewer), "rolled-back-command")
	// Payload serialization fails after the issue row has been written, so the
	// transaction must roll back both the entity and its completion receipt.
	_, err = repo.UpdateIssueRecord(rollbackCtx, workspace, issue.ID, nil, IssueMutationScope{Payload: func() {}}, func(_ *domain.Bootstrap, current *domain.Issue) error { current.Title = "Must roll back"; return nil })
	if err == nil {
		t.Fatal("invalid event payload committed")
	}
	if _, found, err := repo.ChatMutationReceipt(t.Context(), workspace, data.Viewer.ID, ChatMutationKey(rollbackCtx)); err != nil || found {
		t.Fatalf("rolled-back command retained a receipt: %v %v", found, err)
	}
	after, err := repo.IssueRecord(t.Context(), workspace, issue.ID)
	if err != nil || after.Version != before.Version || after.Title != before.Title {
		t.Fatalf("failed transaction persisted issue: %#v %v", after, err)
	}
}

func TestChatMutationNoopReceiptAndRetention(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	workspace := data.Workspace.URLKey
	issue := data.Issues[0]
	ctx := WithChatMutationKey(ContextWithActor(context.Background(), data.Viewer), "chat-noop")
	result, err := repo.UpdateIssueRecord(ctx, workspace, issue.ID, nil, IssueMutationScope{}, func(*domain.Bootstrap, *domain.Issue) error { return ErrNoMutation })
	if err != nil || result.Version != issue.Version {
		t.Fatalf("no-op changed issue: %#v %v", result, err)
	}
	if _, found, err := repo.ChatMutationReceipt(t.Context(), workspace, data.Viewer.ID, ChatMutationKey(ctx)); err != nil || !found {
		t.Fatalf("no-op command receipt missing: %v %v", found, err)
	}
	if err = repo.PurgeChatHistory(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.ChatMutationReceipt(t.Context(), workspace, data.Viewer.ID, ChatMutationKey(ctx)); err != nil || !found {
		t.Fatal("recent receipt was purged")
	}
	if _, err = repo.db.ExecContext(t.Context(), `UPDATE chat_mutation_receipts SET created_at=? WHERE workspace_key=? AND user_id=? AND mutation_key=?`, time.Now().Add(-31*24*time.Hour).UnixMilli(), workspace, data.Viewer.ID, ChatMutationKey(ctx)); err != nil {
		t.Fatal(err)
	}
	if err = repo.PurgeChatHistory(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.ChatMutationReceipt(t.Context(), workspace, data.Viewer.ID, ChatMutationKey(ctx)); err != nil || found {
		t.Fatal("expired receipt survived cleanup")
	}
}
