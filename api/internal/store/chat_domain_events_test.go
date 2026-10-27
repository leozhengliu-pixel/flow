package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestChatDomainReplayRetainsUnacknowledgedEventsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat-replay.db")
	repository, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	workspace := "test-workspace"
	if err := repository.InitializeChatDomainCursor(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	repository.SetWebhookSink(func(string, domain.DomainEvent) {})
	if err := repository.MutateWorkspace(ctx, workspace, "integration.fixture", "chat", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: "chat", Provider: "feishu", Status: "connected"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	issue := repository.Bootstrap().Issues[0]
	originalTitle := issue.Title
	if _, err := repository.UpdateIssueRecord(ctx, workspace, issue.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, item *domain.Issue) error {
		item.Title = "Changed while the chat worker is stopped"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := repository.ChatDomainEventPage(ctx, workspace, 100)
	if err != nil || len(page) < 2 {
		t.Fatalf("events missing before acknowledgement: %#v %v", page, err)
	}
	last := page[len(page)-1]
	var previous map[string]any
	if last.Event.Type != "issue.updated" || json.Unmarshal(last.Event.PreviousValues, &previous) != nil || previous["title"] != originalTitle {
		t.Fatalf("a chat-only subscriber lost the change diff: %+v %s", last, last.Event.PreviousValues)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := repository.InitializeChatDomainCursor(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	replayed, err := repository.ChatDomainEventPage(ctx, workspace, 100)
	if err != nil || len(replayed) != len(page) || replayed[len(replayed)-1].Event.ID != last.Event.ID {
		t.Fatalf("restart skipped unacknowledged commit: %#v %v", replayed, err)
	}
	if err := repository.AdvanceChatDomainCursor(ctx, workspace, last.Seq); err != nil {
		t.Fatal(err)
	}
	if err := repository.AdvanceChatDomainCursor(ctx, workspace, page[0].Seq); err != nil {
		t.Fatal(err)
	}
	if remaining, err := repository.ChatDomainEventPage(ctx, workspace, 100); err != nil || len(remaining) != 0 {
		t.Fatalf("cursor regressed or acknowledged rows replayed: %#v %v", remaining, err)
	}
	if remaining, err := repository.ChatDomainEventPage(ctx, "different-workspace", 100); err != nil || len(remaining) != 0 {
		t.Fatalf("cross-workspace event leak: %#v %v", remaining, err)
	}
}

func TestChatDomainReplayReportsRetentionGap(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.InitializeChatDomainCursor(ctx, "test-workspace"); err != nil {
		t.Fatal(err)
	}
	if err := repository.MutateWorkspace(ctx, "test-workspace", "integration.updated", "chat", nil, func(*domain.Bootstrap) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(ctx, `UPDATE workspace_event_seq SET head=head+1,pruned=head+1 WHERE workspace_id=(SELECT workspace_id FROM workspace_states WHERE workspace_key=?)`, "test-workspace"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ChatDomainEventPage(ctx, "test-workspace", 20); !errors.Is(err, ErrChatDomainCursorExpired) {
		t.Fatalf("retention gap was hidden: %v", err)
	}
	from, to, err := repository.ResetChatDomainCursor(ctx, "test-workspace")
	if err != nil || to <= from {
		t.Fatalf("cursor was not moved to the head: %d -> %d %v", from, to, err)
	}
	if records, err := repository.ChatDomainEventPage(ctx, "test-workspace", 20); err != nil || len(records) != 0 {
		t.Fatalf("replay did not resume after the gap: %v %v", records, err)
	}
}

func TestChatWorkerLeaseCannotBeReleasedByAnExpiredWorker(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-worker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.InitializeChatDomainCursor(ctx, "test-workspace"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first, claimed, err := repository.ClaimChatWorker(ctx, "test-workspace", now, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("initial claim failed: %v", err)
	}
	if _, claimed, err := repository.ClaimChatWorker(ctx, "test-workspace", now, time.Minute); err != nil || claimed {
		t.Fatalf("concurrent worker acquired an active lease: %v", err)
	}
	second, claimed, err := repository.ClaimChatWorker(ctx, "test-workspace", now.Add(2*time.Minute), time.Minute)
	if err != nil || !claimed {
		t.Fatalf("expired claim did not recover: %v", err)
	}
	if err := repository.ReleaseChatWorker(ctx, "test-workspace", first); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := repository.ClaimChatWorker(ctx, "test-workspace", now.Add(2*time.Minute), time.Minute); err != nil || claimed {
		t.Fatalf("expired worker cleared successor lease: %v", err)
	}
	if err := repository.ReleaseChatWorker(ctx, "test-workspace", second); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := repository.ClaimChatWorker(ctx, "test-workspace", now.Add(2*time.Minute), time.Minute); err != nil || !claimed {
		t.Fatalf("released lease remained unavailable: %v", err)
	}
}

func TestChatSnapshotsSurviveInterruptedDisplayMetadataSync(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-partial-setup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.PutChatIntegration(ctx, domain.ChatIntegrationRecord{WorkspaceKey: "test-workspace", Provider: "feishu", ConnectionID: "chat-partial", AppID: "app-partial", TenantKey: "tenant-partial", Connected: true}); err != nil {
		t.Fatal(err)
	}
	repository.SetWebhookSink(func(string, domain.DomainEvent) {})
	issue := repository.Bootstrap().Issues[0]
	if _, err := repository.UpdateIssueRecord(ctx, "test-workspace", issue.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, item *domain.Issue) error {
		item.Title = "Retain diffs before legacy metadata catches up"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	events, err := repository.Events(ctx, issue.ID)
	if err != nil || len(events) == 0 || len(events[len(events)-1].PreviousValues) == 0 {
		t.Fatalf("authoritative connected application lost its event diff: %+v %v", events, err)
	}
}

func TestChatSnapshotFlagFollowsIntegrationWritesWithoutPerMutationQueries(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-snapshot-flag.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if repository.webhookNeeded(canceled, "test-workspace") {
		t.Fatal("a failed read forced event diffs")
	}
	if _, cached := repository.chatSnapshotFlags.Load("test-workspace"); cached {
		t.Fatal("a failed read was cached")
	}
	if repository.webhookNeeded(ctx, "test-workspace") {
		t.Fatal("workspace without chat applications needs diffs")
	}
	if value, cached := repository.chatSnapshotFlags.Load("test-workspace"); !cached || value.(bool) {
		t.Fatalf("flag was not cached after the first read: %v %v", value, cached)
	}
	record := domain.ChatIntegrationRecord{WorkspaceKey: "test-workspace", Provider: "lark", ConnectionID: "chat-flag", AppID: "app-flag"}
	if err := repository.PutChatIntegration(ctx, record); err != nil {
		t.Fatal(err)
	}
	if repository.webhookNeeded(ctx, "test-workspace") {
		t.Fatal("an unverified application needs diffs")
	}
	record.Connected, record.TenantKey = true, "tenant-flag"
	if err := repository.PutChatIntegration(ctx, record); err != nil {
		t.Fatal(err)
	}
	if !repository.webhookNeeded(canceled, "test-workspace") {
		t.Fatal("connected application write did not set the cached flag")
	}
	if err := repository.DeleteChatIntegration(ctx, "test-workspace", record.ConnectionID); err != nil {
		t.Fatal(err)
	}
	if repository.webhookNeeded(ctx, "test-workspace") {
		t.Fatal("disconnected application still needs diffs")
	}
}
