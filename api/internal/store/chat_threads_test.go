package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestChatThreadsInitiativeBindingsAndProjectSetsStore(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "chat-threads.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	exerciseChatThreadStore(t, repo)
}

// exerciseChatThreadStore covers the thread, initiative binding and project
// set storage on any dialect; each run uses its own workspace and connection.
func exerciseChatThreadStore(t *testing.T, repo *SQLiteStore) {
	t.Helper()
	ctx := t.Context()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	workspace, connection := "chat-threads-"+suffix, "chat_threads_"+suffix
	record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "feishu", ConnectionID: connection, AppID: "cli_threads_" + suffix, TenantKey: "tenant_threads", Connected: true}
	if err := repo.PutChatIntegration(ctx, record); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.DeleteChatIntegration(t.Context(), workspace, connection) })

	// Threads: one per resource and group; a new root replaces the thread and
	// its items.
	thread, err := repo.PutChatThread(ctx, workspace, domain.ChatThread{ConnectionID: connection, ChatID: "oc_a", ResourceType: "issue", ResourceID: "issue_1", RootMessageID: "om_root_1", BindingID: "binding_1"})
	if err != nil {
		t.Fatal(err)
	}
	if thread.SyncFrom.IsZero() || thread.ID != ChatThreadID(connection, "om_root_1") {
		t.Fatalf("thread defaults: %+v", thread)
	}
	again, err := repo.PutChatThread(ctx, workspace, domain.ChatThread{ConnectionID: connection, ChatID: "oc_a", ResourceType: "issue", ResourceID: "issue_1", RootMessageID: "om_root_1", PlatformThreadID: "omt_1"})
	if err != nil || again.ID != thread.ID {
		t.Fatalf("same root rewrite: %+v %v", again, err)
	}
	if found, err := repo.ChatThread(ctx, workspace, connection, "oc_a", "issue", "issue_1"); err != nil || found.PlatformThreadID != "omt_1" {
		t.Fatalf("thread by resource: %+v %v", found, err)
	}
	if _, err := repo.ChatThread(ctx, workspace, connection, "oc_b", "issue", "issue_1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("thread leaked to another group: %v", err)
	}
	other, err := repo.PutChatThread(ctx, workspace, domain.ChatThread{ConnectionID: connection, ChatID: "oc_b", ResourceType: "issue", ResourceID: "issue_1", RootMessageID: "om_root_b"})
	if err != nil || other.ID == thread.ID {
		t.Fatalf("second group's thread: %+v %v", other, err)
	}
	item := domain.ChatThreadItem{ThreadID: thread.ID, ItemType: "comment", ItemID: "comment_1", MessageID: "om_comment_1", Origin: domain.ChatThreadOriginFlow, Version: 1}
	for attempt, want := range []bool{true, false} {
		if inserted, err := repo.InsertChatThreadItem(ctx, workspace, connection, item); err != nil || inserted != want {
			t.Fatalf("item insert %d: %v %v", attempt, inserted, err)
		}
	}
	if _, err := repo.InsertChatThreadItem(ctx, workspace, connection, domain.ChatThreadItem{ThreadID: thread.ID, ItemType: "comment", ItemID: "comment_bad", MessageID: "om_bad", Origin: "elsewhere"}); err == nil {
		t.Fatal("item with an unknown origin stored")
	}
	stored, err := repo.ChatThreadItemForMessage(ctx, workspace, connection, "om_comment_1")
	if err != nil || stored.ID != ChatThreadItemID(thread.ID, "comment", "comment_1") {
		t.Fatalf("item by message: %+v %v", stored, err)
	}
	stored.MessageID, stored.Version, stored.Deleted = "om_comment_1b", 2, true
	if err := repo.UpdateChatThreadItem(ctx, workspace, connection, stored); err != nil {
		t.Fatal(err)
	}
	items, err := repo.ChatThreadItems(ctx, workspace, connection, thread.ID)
	if err != nil || len(items) != 1 || items[0].MessageID != "om_comment_1b" || !items[0].Deleted || items[0].Version != 2 {
		t.Fatalf("updated item: %+v %v", items, err)
	}
	if _, err := repo.ChatThreadItemForMessage(ctx, workspace, connection, "om_comment_1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old message still maps: %v", err)
	}
	replaced, err := repo.PutChatThread(ctx, workspace, domain.ChatThread{ConnectionID: connection, ChatID: "oc_a", ResourceType: "issue", ResourceID: "issue_1", RootMessageID: "om_root_2"})
	if err != nil || replaced.ID == thread.ID {
		t.Fatalf("replacement: %+v %v", replaced, err)
	}
	if items, _ := repo.ChatThreadItems(ctx, workspace, connection, thread.ID); len(items) != 0 {
		t.Fatalf("replaced thread kept items: %+v", items)
	}
	if found, err := repo.ChatThreadForRoot(ctx, workspace, connection, "om_root_2"); err != nil || found.ResourceID != "issue_1" || found.ChatID != "oc_a" {
		t.Fatalf("thread by root: %+v %v", found, err)
	}
	if _, err := repo.ChatThreadForRoot(ctx, workspace, connection, "om_root_1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replaced root still routes: %v", err)
	}
	if err := repo.DeleteChatThread(ctx, workspace, connection, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ChatThreadForRoot(ctx, workspace, connection, "om_root_b"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted thread still routes: %v", err)
	}
	if _, err := repo.PutChatThread(ctx, workspace, domain.ChatThread{ConnectionID: "chat_threads_missing_" + suffix, ChatID: "oc_a", ResourceType: "issue", ResourceID: "issue_2", RootMessageID: "om_root_x"}); err == nil {
		t.Fatal("thread stored without its connection")
	}

	// Initiative bindings: one per group and initiative.
	initiative := domain.ChatInitiativeBinding{ID: "chat_initiative_binding_" + suffix, ConnectionID: connection, ChatID: "oc_a", ChatName: "Launch room", InitiativeID: "initiative_1", Enabled: true, CreatedAt: time.Now().UTC()}
	if err := repo.PutChatInitiativeBinding(ctx, workspace, initiative); err != nil {
		t.Fatal(err)
	}
	duplicate := initiative
	duplicate.ID += "_duplicate"
	if err := repo.PutChatInitiativeBinding(ctx, workspace, duplicate); err == nil {
		t.Fatal("group bound to the same initiative twice")
	}
	initiative.Enabled = false
	if err := repo.PutChatInitiativeBinding(ctx, workspace, initiative); err != nil {
		t.Fatal(err)
	}
	if listed, err := repo.ChatInitiativeBindings(ctx, workspace, connection, "initiative_1"); err != nil || len(listed) != 1 || listed[0].Enabled {
		t.Fatalf("initiative bindings: %+v %v", listed, err)
	}
	if listed, _ := repo.ChatInitiativeBindings(ctx, workspace, connection, "initiative_2"); len(listed) != 0 {
		t.Fatalf("initiative filter: %+v", listed)
	}
	if err := repo.PutChatInitiativeBinding(ctx, "another-"+suffix, initiative); err == nil {
		t.Fatal("binding moved to another workspace")
	}

	// Project sets: a stored single-project binding gets a one-project set,
	// keeping its project and every other field; the migration is repeatable.
	raw := []byte(`{"id":"binding_legacy_` + suffix + `","connectionId":"` + connection + `","chatId":"oc_legacy","teamId":"team_1","projectId":"project_1","events":["issue.created"],"enabled":true,"futureField":{"kept":true}}`)
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO chat_bindings(id,workspace_key,connection_id,chat_id,payload) VALUES(?,?,?,?,?)`, "binding_legacy_"+suffix, workspace, connection, "oc_legacy", raw); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutChatBinding(ctx, workspace, domain.ChatBinding{ID: "binding_all_" + suffix, ConnectionID: connection, ChatID: "oc_all", TeamID: "team_1", Events: []string{}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repo.migrateChatBindingProjectSets(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var payload []byte
	if err := repo.db.QueryRowContext(ctx, `SELECT payload FROM chat_bindings WHERE id=?`, "binding_legacy_"+suffix).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(payload, &fields)
	if ids, _ := fields["projectIds"].([]any); len(ids) != 1 || ids[0] != "project_1" || fields["projectId"] != "project_1" || fields["futureField"] == nil {
		t.Fatalf("migrated payload: %s", payload)
	}
	bindings, err := repo.ChatBindings(ctx, workspace, connection)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bindings[slices.IndexFunc(bindings, func(item domain.ChatBinding) bool { return item.ChatID == "oc_legacy" })]
	all := bindings[slices.IndexFunc(bindings, func(item domain.ChatBinding) bool { return item.ChatID == "oc_all" })]
	if !slices.Equal(legacy.ProjectIDs, []string{"project_1"}) || legacy.ProjectID != "project_1" || len(all.ProjectIDs) != 0 || all.ProjectID != "" {
		t.Fatalf("normalized bindings: %+v %+v", legacy, all)
	}
	set := domain.ChatBinding{ID: "binding_set_" + suffix, ConnectionID: connection, ChatID: "oc_set", TeamID: "team_1", ProjectIDs: []string{"project_3", "project_2", "project_3"}, Events: []string{}}
	if err := repo.PutChatBinding(ctx, workspace, set); err != nil {
		t.Fatal(err)
	}
	bindings, _ = repo.ChatBindings(ctx, workspace, connection)
	saved := bindings[slices.IndexFunc(bindings, func(item domain.ChatBinding) bool { return item.ChatID == "oc_set" })]
	if !slices.Equal(saved.ProjectIDs, []string{"project_2", "project_3"}) || saved.ProjectID != "" {
		t.Fatalf("project set: %+v", saved)
	}

	// Retention and disconnection remove everything.
	if err := repo.purgeChatThreads(ctx, time.Now().Add(chatThreadRetention+time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ChatThreadForRoot(ctx, workspace, connection, "om_root_2"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired thread kept: %v", err)
	}
	if _, err := repo.PutChatThread(ctx, workspace, domain.ChatThread{ConnectionID: connection, ChatID: "oc_c", ResourceType: "issue", ResourceID: "issue_3", RootMessageID: "om_root_c"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteChatIntegration(ctx, workspace, connection); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"chat_threads", "chat_thread_items", "chat_initiative_bindings", "chat_bindings"} {
		var count int
		if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE workspace_key=?`, workspace).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s after disconnect: %d %v", table, count, err)
		}
	}
}
