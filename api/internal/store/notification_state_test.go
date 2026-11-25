package store

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP14Store(t *testing.T) (*SQLiteStore, domain.Bootstrap) {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (driver == "") != (databaseURL == "") {
		t.Fatal("P14 requires both database driver and URL")
	}
	config := DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 2}
	switch driver {
	case "":
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "p14.db")
	case "sqlite":
		config.Path = databaseURL
	case "mysql", "postgres", "postgresql":
	default:
		t.Fatalf("unsupported database driver %q", driver)
	}
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	user, token, err := repo.Register(t.Context(), "P14 recipient", idgen.New("p14-")+"@example.test", "p14-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.EraseAccount(context.Background(), user.ID); err != nil {
			t.Error(err)
		}
	})
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	data, err := repo.CreateWorkspace(ContextWithActor(t.Context(), user), "P14 isolated", idgen.New("p14-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := repo.DeleteWorkspace(context.Background(), data.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	})
	return repo, data
}

func scaleP14Notifications(t *testing.T, repo *SQLiteStore, data domain.Bootstrap) []domain.Notification {
	t.Helper()
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	items := []domain.Notification{
		{ID: idgen.New("p14-unread-"), RecipientID: data.Viewer.ID, Actor: data.Viewer, CreatedAt: now, UpdatedAt: now},
		{ID: idgen.New("p14-read-"), RecipientID: data.Viewer.ID, Actor: data.Viewer, ReadAt: &past, CreatedAt: now, UpdatedAt: now},
		{ID: idgen.New("p14-archived-"), RecipientID: data.Viewer.ID, ArchivedAt: &past, CreatedAt: now, UpdatedAt: now},
		{ID: idgen.New("p14-deleted-"), RecipientID: data.Viewer.ID, DeletedAt: &past, CreatedAt: now, UpdatedAt: now},
		{ID: idgen.New("p14-snoozed-"), RecipientID: data.Viewer.ID, SnoozedUntil: &future, CreatedAt: now, UpdatedAt: now},
		{ID: idgen.New("p14-awake-"), RecipientID: data.Viewer.ID, SnoozedUntil: &past, CreatedAt: now, UpdatedAt: now},
		{ID: idgen.New("p14-other-"), RecipientID: "other-" + data.Viewer.ID, CreatedAt: now, UpdatedAt: now},
	}
	tx, err := repo.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, item := range items {
		if err := writeContentRecord(t.Context(), tx, data.Workspace.URLKey, "notification", "", item); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return items
}

func TestScaleP14BaselineUnreadQuery(t *testing.T) {
	repo, data := scaleP14Store(t)
	scaleP14Notifications(t, repo, data)
	var queries []string
	ctx := WithSQLStatementObserver(t.Context(), func(query string) { queries = append(queries, query) })
	started := time.Now()
	page, err := repo.QueryNotifications(ctx, NotificationQuery{Workspace: data.Workspace.URLKey, UserID: data.Viewer.ID, Limit: 1})
	if err != nil || page.UnreadCount != 2 {
		t.Fatalf("legacy unread count = %d, error = %v", page.UnreadCount, err)
	}
	selects := 0
	for _, query := range queries {
		if strings.HasPrefix(query, "SELECT") {
			selects++
		}
	}
	t.Logf("baseline unread recovery: selects=%d elapsed=%s count=%d", selects, time.Since(started), page.UnreadCount)
	if selects < 2 {
		t.Fatalf("expected count plus page reads, got %d", selects)
	}
}

func TestScaleP14StateMatchesLegacyLifecycle(t *testing.T) {
	repo, data := scaleP14Store(t)
	items := scaleP14Notifications(t, repo, data)
	workspace, recipient := data.Workspace.URLKey, data.Viewer.ID
	compare := func(t *testing.T) {
		t.Helper()
		page, err := repo.QueryNotifications(t.Context(), NotificationQuery{Workspace: workspace, UserID: recipient, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		var queries []string
		started := time.Now()
		state, err := repo.NotificationState(WithSQLStatementObserver(t.Context(), func(q string) { queries = append(queries, q) }), workspace, recipient)
		if err != nil || state.UnreadCount != page.UnreadCount {
			t.Fatalf("overlay=%+v legacy=%d error=%v", state, page.UnreadCount, err)
		}
		if len(queries) != 1 || strings.Contains(queries[0], "SELECT data") {
			t.Fatalf("state read must be one count/revision statement: %v", queries)
		}
		t.Logf("overlay unread recovery: selects=%d elapsed=%s count=%d", len(queries), time.Since(started), state.UnreadCount)
	}
	compare(t)
	for _, action := range []string{"markRead", "markUnread", "archive", "unarchive", "snooze", "unsnooze", "delete", "markAllRead", "archiveAll", "snoozeAll", "deleteRead", "deleteReadCompleted", "deleteAll"} {
		t.Run(action, func(t *testing.T) {
			future := time.Now().UTC().Add(time.Hour)
			ids := []string{items[0].ID}
			if strings.Contains(action, "All") || strings.HasPrefix(action, "deleteRead") {
				ids = nil
			}
			_, err := repo.BatchNotificationRecords(t.Context(), workspace, recipient, domain.NotificationBatchInput{Action: action, IDs: ids}, &future)
			if err != nil {
				t.Fatal(err)
			}
			compare(t)
		})
	}
	for _, state := range []string{"read", "unread", "archived", "unarchived", "snoozed", "unsnoozed", "favorite", "deleted"} {
		t.Run("single-"+state, func(t *testing.T) {
			_, err := repo.UpdateNotificationRecord(t.Context(), workspace, recipient, items[5].ID, "notification."+state, nil, func(item *domain.Notification) {
				now, future := time.Now().UTC(), time.Now().UTC().Add(time.Hour)
				switch state {
				case "read":
					item.ReadAt = &now
				case "unread":
					item.ReadAt = nil
				case "archived":
					item.ArchivedAt = &now
				case "unarchived":
					item.ArchivedAt = nil
				case "snoozed":
					item.SnoozedUntil = &future
				case "unsnoozed":
					item.SnoozedUntil = nil
				case "favorite":
					item.Favorite = true
				case "deleted":
					item.DeletedAt = &now
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			compare(t)
		})
	}
}

func TestScaleP14CommitRollbackAndRecipientCoalescing(t *testing.T) {
	repo, data := scaleP14Store(t)
	items := scaleP14Notifications(t, repo, data)
	workspace, recipient := data.Workspace.URLKey, data.Viewer.ID
	changes := []NotificationStateChange{
		{WorkspaceKey: workspace, RecipientID: recipient, ChangedIDs: []string{items[0].ID}},
		{WorkspaceKey: workspace, RecipientID: recipient, ChangedIDs: []string{items[0].ID, items[1].ID}},
		{WorkspaceKey: workspace, RecipientID: items[6].RecipientID, ChangedIDs: []string{items[6].ID}},
	}
	tx, err := repo.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := items[0]
	now := time.Now().UTC()
	changed.ReadAt = &now
	if err := writeContentRecord(t.Context(), tx, workspace, "notification", "", changed); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.PrepareNotificationStates(t.Context(), changes); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	state, err := repo.NotificationState(t.Context(), workspace, recipient)
	if err != nil || state.Revision != "0" || state.UnreadCount != 2 {
		t.Fatalf("rollback leaked revision: %+v %v", state, err)
	}
	commit := scaleP14Prepare(t, repo, changes)
	var recipients []string
	var reads int
	ctx := WithSQLStatementObserver(t.Context(), func(string) { reads++ })
	if err := commit.Publish(ctx, func(w, r string, state NotificationState) {
		recipients = append(recipients, r)
		if w != workspace || state.Revision != "1" || state.Invalidated {
			t.Errorf("bad envelope: %s %s %+v", w, r, state)
		}
		if r == recipient && (len(state.ChangedIDs) != 2 || state.UnreadCount != 2) {
			t.Errorf("uncoalesced state: %+v", state)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 2 || reads != 2 {
		t.Fatalf("recipients=%v reads=%d", recipients, reads)
	}
}

func scaleP14Prepare(t *testing.T, repo *SQLiteStore, changes []NotificationStateChange) NotificationStateCommit {
	t.Helper()
	tx, err := repo.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	commit, err := tx.PrepareNotificationStates(t.Context(), changes)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return commit
}

func TestScaleP14LatePublishAndBoundedChanges(t *testing.T) {
	repo, data := scaleP14Store(t)
	items := scaleP14Notifications(t, repo, data)
	change := NotificationStateChange{WorkspaceKey: data.Workspace.URLKey, RecipientID: data.Viewer.ID, ChangedIDs: []string{items[0].ID}}
	first := scaleP14Prepare(t, repo, []NotificationStateChange{change})
	second := scaleP14Prepare(t, repo, []NotificationStateChange{change})
	if err := first.Publish(t.Context(), func(_, _ string, state NotificationState) {
		if state.Revision != "2" || !state.Invalidated || len(state.ChangedIDs) != 0 {
			t.Errorf("late publisher silently skipped changes: %+v", state)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.Publish(t.Context(), func(_, _ string, state NotificationState) {
		if state.Invalidated || len(state.ChangedIDs) != 1 {
			t.Errorf("latest change lost: %+v", state)
		}
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 1000 {
		change.ChangedIDs = append(change.ChangedIDs, "notification-"+strconv.Itoa(i))
	}
	batch := scaleP14Prepare(t, repo, []NotificationStateChange{change})
	if err := batch.Publish(t.Context(), func(_, _ string, state NotificationState) {
		if !state.Invalidated || len(state.ChangedIDs) != 0 {
			t.Errorf("oversized batch retained ids: %+v", state)
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP14RecordChangesRetainRemovedOwners(t *testing.T) {
	before := domain.Notification{ID: "old", RecipientID: "old-owner"}
	after := domain.Notification{ID: "new", RecipientID: "new-owner"}
	if len(NotificationStateChanges("workspace", &before, &before)) != 0 || len(NotificationStateChanges("workspace", nil, nil)) != 0 {
		t.Fatal("no-op produced a change")
	}
	got := NotificationStateChanges("workspace", &before, &after)
	if len(got) != 2 || got[0].RecipientID != "old-owner" || got[1].RecipientID != "new-owner" {
		t.Fatalf("owner move lost recipient: %v", got)
	}
	if got := NotificationStateChanges("workspace", &before, nil); len(got) != 1 || got[0].ChangedIDs[0] != before.ID {
		t.Fatalf("removal lost identity: %v", got)
	}
}

func TestScaleP14NotificationCredentialBinding(t *testing.T) {
	repo, data := scaleP14Store(t)
	key := &domain.APIKey{ID: "p14-key", Actor: "user", SecretHash: "not-exported", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{data.Teams[0].ID}}
	access, err := repo.ResolveAccess(t.Context(), data.Workspace.URLKey, data.Viewer.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	workspace, credential, err := repo.NotificationStateCredential(access)
	if err != nil || workspace != data.Workspace.URLKey || credential.SecretHash != "" {
		t.Fatalf("invalid notification credential binding: %v", err)
	}
	credential.TeamIDs[0], credential.Scopes[0] = "other-team", "admin"
	_, again, err := repo.NotificationStateCredential(access)
	if err != nil || again.TeamIDs[0] != data.Teams[0].ID || again.Scopes[0] != "read" {
		t.Fatal("notification credential exposed mutable policy")
	}
	access.PrincipalID = "another-recipient"
	if _, _, err := repo.NotificationStateCredential(access); err == nil {
		t.Fatal("notification credential accepted a forged principal")
	}
}

// Enumerate every canonical SQL notification writer, including generic writers
// and lifecycle operations. Business producers below all reach these writers.
func TestScaleP14WriterCoverage(t *testing.T) {
	writers := map[string]string{
		"writeContentRecord": "content_records.go", "writeContentRecordRows": "content_records.go", "syncContentRecordsFrom": "content_records.go", "persistWorkspaceTx": "sqlite.go", "UpdateNotificationRecord": "notification_record_mutation.go",
		"BatchNotificationRecords": "notification_batch.go", "ErasePersonFromWorkspace": "personal_data.go", "scrubPersonCopies": "personal_data.go",
		"deleteTeamOwnedIssueRecords": "team_deletion.go", "PurgeWorkspaceRows": "workspace_purge.go", "moveWorkspaceKeyRows": "workspace_rename.go", "rewriteWorkspaceKeyJSON": "workspace_rename.go",
	}
	got := NotificationStateWriters()
	for name, file := range writers {
		if !slices.Contains(got, name) {
			t.Errorf("capability omits %s", name)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, decl := range parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("writer inventory is stale: %s:%s", file, name)
		}
	}
	if len(got) != len(writers) {
		t.Fatalf("writer inventory differs: %v", got)
	}
	coverage := NotificationStateCoverage{Writers: got, CommitHook: true, RecipientProjection: true, AllReplicas: true, SnoozeExpiry: true}
	if NotificationStateReady(NotificationStateCoverage{}) || !NotificationStateReady(coverage) {
		t.Fatal("invalid capability default")
	}
	for i := range got {
		partial := coverage
		partial.Writers = append(slices.Clone(got[:i]), got[i+1:]...)
		if NotificationStateReady(partial) {
			t.Errorf("capability advertised without %s", got[i])
		}
	}
	for _, field := range []string{"CommitHook", "RecipientProjection", "AllReplicas", "SnoozeExpiry"} {
		partial := coverage
		reflect.ValueOf(&partial).Elem().FieldByName(field).SetBool(false)
		if NotificationStateReady(partial) {
			t.Errorf("capability advertised without %s", field)
		}
	}
}
