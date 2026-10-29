package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestChatIntakeRequestsStore(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-intake-requests.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	exerciseChatIntakeRequestStore(t, repo)
}

// exerciseChatIntakeRequestStore covers the request records on any dialect;
// each run uses its own workspace and connection.
func exerciseChatIntakeRequestStore(t *testing.T, repo *SQLiteStore) {
	t.Helper()
	ctx := t.Context()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	workspace, connection := "chat-requests-"+suffix, "chat_requests_"+suffix
	record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: connection, AppID: "cli_requests_" + suffix, TenantKey: "tenant_requests", Connected: true}
	if err := repo.PutChatIntegration(ctx, record); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.DeleteChatIntegration(t.Context(), workspace, connection) })

	request := domain.ChatIntakeRequest{ConnectionID: connection, ChatID: "oc_requests", MessageID: "om_request_1", BindingID: "binding_1", IssueID: "issue_chat_1", RequesterID: "user_1", RequesterOpenID: "ou_1", Source: domain.ChatRequestSourceReaction, Status: domain.ChatRequestPending}
	reserved, ok, err := repo.ReserveChatIntakeRequest(ctx, workspace, request)
	if err != nil || !ok || reserved.ID != ChatIntakeRequestID(connection, "om_request_1") || reserved.CreatedAt.IsZero() {
		t.Fatalf("reserve: %+v %v %v", reserved, ok, err)
	}
	// The same message never takes a second request, from anyone.
	other := request
	other.RequesterID, other.RequesterOpenID, other.Source, other.IssueID = "user_2", "ou_2", domain.ChatRequestSourceMention, "issue_chat_2"
	existing, ok, err := repo.ReserveChatIntakeRequest(ctx, workspace, other)
	if err != nil || ok || existing.RequesterID != "user_1" || existing.IssueID != "issue_chat_1" {
		t.Fatalf("second reservation: %+v %v %v", existing, ok, err)
	}
	if _, _, err := repo.ReserveChatIntakeRequest(ctx, workspace, domain.ChatIntakeRequest{ConnectionID: connection, ChatID: "oc_requests", MessageID: "om_bad", BindingID: "b", IssueID: "i", RequesterID: "u", RequesterOpenID: "o", Source: "email", Status: domain.ChatRequestPending}); err == nil {
		t.Fatal("request with an unknown source stored")
	}
	if _, _, err := repo.ReserveChatIntakeRequest(ctx, workspace, domain.ChatIntakeRequest{ConnectionID: "chat_requests_missing_" + suffix, ChatID: "oc", MessageID: "om_x", BindingID: "b", IssueID: "i", RequesterID: "u", RequesterOpenID: "o", Source: domain.ChatRequestSourceMention, Status: domain.ChatRequestPending}); err == nil {
		t.Fatal("request stored without its connection")
	}

	reserved.Status, reserved.AskID, reserved.Outcome = domain.ChatRequestCreated, "ask_chat_1", "completed"
	if err := repo.UpdateChatIntakeRequest(ctx, workspace, reserved); err != nil {
		t.Fatal(err)
	}
	found, err := repo.ChatIntakeRequestForMessage(ctx, workspace, connection, "om_request_1")
	if err != nil || found.Status != domain.ChatRequestCreated || found.AskID != "ask_chat_1" || found.Outcome != "completed" {
		t.Fatalf("by message: %+v %v", found, err)
	}
	if _, err := repo.ChatIntakeRequestForMessage(ctx, "another-"+suffix, connection, "om_request_1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("request leaked to another workspace: %v", err)
	}
	listed, err := repo.ChatIntakeRequestsForIssue(ctx, workspace, connection, "issue_chat_1")
	if err != nil || len(listed) != 1 || listed[0].MessageID != "om_request_1" {
		t.Fatalf("by issue: %+v %v", listed, err)
	}
	if listed, _ := repo.ChatIntakeRequestsForIssue(ctx, workspace, connection, "issue_chat_2"); len(listed) != 0 {
		t.Fatalf("issue filter: %+v", listed)
	}
	moved := reserved
	moved.MessageID = "om_request_other"
	if err := repo.UpdateChatIntakeRequest(ctx, workspace, moved); err == nil {
		t.Fatal("request moved to another message")
	}

	// A refused request releases the message for another member.
	if err := repo.DeleteChatIntakeRequest(ctx, workspace, connection, reserved.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repo.ReserveChatIntakeRequest(ctx, workspace, other); err != nil || !ok {
		t.Fatalf("released message: %v %v", ok, err)
	}

	// Disconnection removes the records.
	if err := repo.DeleteChatIntegration(ctx, workspace, connection); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_intake_requests WHERE workspace_key=?`, workspace).Scan(&count); err != nil || count != 0 {
		t.Fatalf("requests after disconnect: %d %v", count, err)
	}
}
