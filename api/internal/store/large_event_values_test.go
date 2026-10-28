package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// The tests below run on SQLite and, with FLOW_TEST_DATABASE_DRIVER and
// FLOW_TEST_DATABASE_URL, on a throwaway MySQL or Postgres database. MySQL
// TEXT holds 64 KiB; event values for large documents are far bigger.

// largeEventMarkdown returns a Markdown body of at least size bytes with
// multi-byte text, quotes, backslashes and markup, so it reads back equal
// only when nothing was cut or re-encoded.
func largeEventMarkdown(label string, size int) string {
	var builder strings.Builder
	for line := 0; builder.Len() < size; line++ {
		fmt.Fprintf(&builder, "## %s section %d\n\nPlan 计划 ✅ \"quoted\" \\path\\ <tag> & text for line %d.\n\n", label, line, line)
	}
	return builder.String()
}

// largeEventDocument adds an enabled webhook (so mutations record previous
// values) and a document with the given body to the fixture workspace. The
// returned cleanup removes both through whichever store is open then.
func largeEventDocument(t *testing.T, repository *SQLiteStore, content string) (workspace, documentID string, cleanup func(*SQLiteStore)) {
	t.Helper()
	ctx := context.Background()
	workspace = repository.Bootstrap().Workspace.URLKey
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	webhookID, documentID := "large-event-webhook-"+suffix, "large-event-document-"+suffix
	repository.SetWebhookSink(func(string, domain.DomainEvent) {})
	if err := repository.MutateWorkspace(ctx, workspace, "webhook.created", webhookID, nil, func(data *domain.Bootstrap) error {
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: webhookID, Name: "Large values", URL: "https://hooks.example.test/large", Enabled: true, ResourceTypes: []string{"Document"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repository.MutateWorkspace(ctx, workspace, "document.created", documentID, nil, func(data *domain.Bootstrap) error {
		data.Documents = append([]domain.Document{{ID: documentID, SlugID: documentID, Title: "Large", Content: content, Creator: data.Viewer, ProjectIDs: []string{}, TeamIDs: []string{}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: now, UpdatedAt: now}}, data.Documents...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cleanup = func(repository *SQLiteStore) {
		_ = repository.MutateWorkspace(ctx, workspace, "test.cleanup", documentID, nil, func(data *domain.Bootstrap) error {
			data.Documents = slices.DeleteFunc(data.Documents, func(item domain.Document) bool { return item.ID == documentID })
			data.Webhooks = slices.DeleteFunc(data.Webhooks, func(item domain.Webhook) bool { return item.ID == webhookID })
			return nil
		})
		_, _ = repository.db.ExecContext(ctx, `DELETE FROM domain_events WHERE aggregate_id=?`, documentID)
	}
	return workspace, documentID, cleanup
}

func largeEventEditDocument(repository *SQLiteStore, workspace, documentID, content string) error {
	return repository.MutateWorkspace(context.Background(), workspace, "document.updated", documentID, map[string]any{"content": content}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == documentID })
		if index < 0 {
			return fmt.Errorf("document %s not found", documentID)
		}
		data.Documents[index].Content = content
		data.Documents[index].Version++
		data.Documents[index].UpdatedAt = time.Now().UTC()
		return nil
	})
}

func storedLargeEventDocument(t *testing.T, repository *SQLiteStore, workspace, documentID string) domain.Document {
	t.Helper()
	data, ok := repository.BootstrapFor(workspace)
	if !ok {
		t.Fatalf("workspace %s not loaded", workspace)
	}
	index := slices.IndexFunc(data.Documents, func(item domain.Document) bool { return item.ID == documentID })
	if index < 0 {
		t.Fatalf("document %s not found", documentID)
	}
	return data.Documents[index]
}

// A document edit over 64 KiB records the pre-edit body in previous_values;
// on MySQL that column was TEXT and the whole write failed with error 1406.
func TestLargeEventValuesDocumentEdit(t *testing.T) {
	for name, open := range loopAppendStoreOpeners(t) {
		t.Run(name, func(t *testing.T) { testLargeEventValuesDocumentEdit(t, open) })
	}
}

func testLargeEventValuesDocumentEdit(t *testing.T, open func() *SQLiteStore) {
	ctx := context.Background()
	repository := open()
	original, updated := largeEventMarkdown("original", 173<<10), largeEventMarkdown("updated", 173<<10)
	workspace, documentID, cleanup := largeEventDocument(t, repository, original)
	defer func() { cleanup(repository); repository.Close() }()
	if err := largeEventEditDocument(repository, workspace, documentID, updated); err != nil {
		t.Fatalf("large document edit: %v", err)
	}
	if document := storedLargeEventDocument(t, repository, workspace, documentID); document.Content != updated || document.Version != 1 {
		t.Fatalf("edited document: version %d, content equal %v", document.Version, document.Content == updated)
	}
	var payloadBytes, previousBytes int
	var previous []byte
	if err := repository.db.QueryRowContext(ctx, `SELECT previous_values,LENGTH(payload),LENGTH(previous_values) FROM domain_events WHERE aggregate_id=? AND event_type='document.updated'`, documentID).Scan(&previous, &payloadBytes, &previousBytes); err != nil {
		t.Fatal(err)
	}
	t.Logf("domain_events bytes for a %d byte document edit: payload %d, previous_values %d", len(updated), payloadBytes, previousBytes)
	loaded, err := repository.LoadOffloadedJSON(ctx, workspace, previous)
	if err != nil {
		t.Fatal(err)
	}
	var before struct {
		Content string `json:"content"`
		Version int64  `json:"version"`
	}
	if err := json.Unmarshal(loaded, &before); err != nil || before.Content != original || before.Version != 0 {
		t.Fatalf("previous values: content equal %v, version %d, %v", before.Content == original, before.Version, err)
	}
	// The stored state survives a reopen (it is read from the database).
	repository.Close()
	repository = open()
	if document := storedLargeEventDocument(t, repository, workspace, documentID); document.Content != updated || document.Version != 1 {
		t.Fatalf("reopened document: version %d, content equal %v", document.Version, document.Content == updated)
	}
	events, err := repository.Events(ctx, documentID)
	if err != nil || len(events) == 0 {
		t.Fatalf("events: %d %v", len(events), err)
	}
	if last := events[len(events)-1]; last.Type != "document.updated" || !bytes.Equal(last.PreviousValues, previous) {
		t.Fatalf("read back previous values: type %s, equal %v", last.Type, bytes.Equal(last.PreviousValues, previous))
	}
}

// Migration 16 widens the large value columns of a MySQL database created
// with TEXT columns, keeping their rows, and changes nothing on a re-run.
// Postgres and SQLite TEXT are unbounded and keep their type.
func TestLargeEventValuesColumnMigration(t *testing.T) {
	for name, open := range loopAppendStoreOpeners(t) {
		t.Run(name, func(t *testing.T) { testLargeEventValuesColumnMigration(t, name, open) })
	}
}

func testLargeEventValuesColumnMigration(t *testing.T, dialect string, open func() *SQLiteStore) {
	ctx := context.Background()
	if dialect == "mysql" {
		// The shared test database already holds values TEXT cannot take
		// back: build the pre-migration schema in a scratch database.
		open = scratchMySQLDatabase(t, open)
	}
	repository := open()
	defer func() { repository.Close() }()
	columnType := func(table, column string) (string, string) {
		t.Helper()
		var kind, nullable string
		var err error
		switch dialect {
		case "mysql":
			err = repository.db.QueryRowContext(ctx, `SELECT DATA_TYPE,IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, table, column).Scan(&kind, &nullable)
		case "postgres":
			err = repository.db.QueryRowContext(ctx, `SELECT data_type,is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? AND column_name=?`, table, column).Scan(&kind, &nullable)
		default:
			rows, queryErr := repository.db.QueryContext(ctx, `SELECT type,"notnull" FROM pragma_table_info('`+table+`') WHERE name=?`, column)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			defer rows.Close()
			var notNull int
			if !rows.Next() {
				t.Fatalf("%s.%s not found", table, column)
			}
			err = rows.Scan(&kind, &notNull)
			nullable = map[int]string{0: "YES", 1: "NO"}[notNull]
		}
		if err != nil {
			t.Fatalf("%s.%s: %v", table, column, err)
		}
		return strings.ToLower(kind), nullable
	}
	want := map[string][2]string{"domain_events.previous_values": {"longtext", "YES"}, "auth_identities.claims_json": {"longtext", "NO"}, "code_host_accounts.scopes": {"mediumtext", "NO"}}
	if dialect != "mysql" {
		for name, expected := range want {
			table, column, _ := strings.Cut(name, ".")
			if kind, nullable := columnType(table, column); kind != "text" || nullable != expected[1] {
				t.Fatalf("%s is %s (nullable %s), want unbounded text", name, kind, nullable)
			}
		}
		if err := repository.widenLargeValueColumns(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	// A database from before migration 16: TEXT columns holding rows.
	for _, statement := range []string{
		"ALTER TABLE domain_events MODIFY COLUMN previous_values TEXT NULL",
		"ALTER TABLE auth_identities MODIFY COLUMN claims_json TEXT NOT NULL",
		"ALTER TABLE code_host_accounts MODIFY COLUMN scopes TEXT NOT NULL",
		"DELETE FROM schema_migrations WHERE version=16",
	} {
		if _, err := repository.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	eventID := fmt.Sprintf("large-event-legacy-%d", time.Now().UnixNano())
	legacy, _ := json.Marshal(map[string]string{"content": largeEventMarkdown("legacy", 40<<10)}) // fits TEXT once escaped
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, eventID, "document.updated", eventID, []byte(`{}`), string(legacy), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = repository.db.ExecContext(ctx, `DELETE FROM domain_events WHERE id=?`, eventID) }()
	var rowsBefore int
	if err := repository.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events`).Scan(&rowsBefore); err != nil {
		t.Fatal(err)
	}
	if kind, _ := columnType("domain_events", "previous_values"); kind != "text" {
		t.Fatalf("legacy column is %s", kind)
	}
	repository.Close()
	repository = open()
	for name, expected := range want {
		table, column, _ := strings.Cut(name, ".")
		if kind, nullable := columnType(table, column); kind != expected[0] || nullable != expected[1] {
			t.Fatalf("%s is %s (nullable %s) after the migration, want %v", name, kind, nullable, expected)
		}
	}
	var rowsAfter int
	var stored []byte
	if err := repository.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events`).Scan(&rowsAfter); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.QueryRowContext(ctx, `SELECT previous_values FROM domain_events WHERE id=?`, eventID).Scan(&stored); err != nil || !bytes.Equal(stored, legacy) || rowsAfter != rowsBefore {
		t.Fatalf("rows after the migration: %d of %d, legacy row equal %v, %v", rowsAfter, rowsBefore, bytes.Equal(stored, legacy), err)
	}
	// A re-run (another server, or a lost migration record) changes nothing.
	for range 2 {
		if err := repository.widenLargeValueColumns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if kind, _ := columnType("domain_events", "previous_values"); kind != "longtext" {
		t.Fatalf("re-run left %s", kind)
	}
}

// scratchMySQLDatabase creates an empty database next to the configured
// MySQL test database, drops it when the test ends, and returns an opener
// for it.
func scratchMySQLDatabase(t *testing.T, open func() *SQLiteStore) func() *SQLiteStore {
	t.Helper()
	ctx := context.Background()
	parsed, err := url.Parse(os.Getenv("FLOW_TEST_DATABASE_URL"))
	if err != nil || parsed.Scheme != "mysql" {
		t.Skip("the scratch database needs a mysql:// FLOW_TEST_DATABASE_URL")
	}
	name := fmt.Sprintf("flow_scratch_%d", time.Now().UnixNano())
	admin := open()
	if _, err := admin.db.ExecContext(ctx, "CREATE DATABASE `"+name+"` CHARACTER SET utf8mb4"); err != nil {
		admin.Close()
		t.Skipf("cannot create a scratch database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.db.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+name+"`"); err != nil {
			t.Errorf("drop scratch database %s: %v", name, err)
		}
		admin.Close()
	})
	parsed.Path = "/" + name
	return func() *SQLiteStore {
		repository, err := OpenDatabase(DatabaseConfig{Driver: "mysql", URL: parsed.String(), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repository
	}
}

// A failure after the event row is inserted rolls the whole write back: the
// document, its version, the event and the change cursor stay as they were.
func TestLargeEventValuesFailedWriteRollsBack(t *testing.T) {
	for name, open := range loopAppendStoreOpeners(t) {
		t.Run(name, func(t *testing.T) { testLargeEventValuesFailedWriteRollsBack(t, name, open) })
	}
}

func testLargeEventValuesFailedWriteRollsBack(t *testing.T, dialect string, open func() *SQLiteStore) {
	ctx := context.Background()
	repository := open()
	original := largeEventMarkdown("original", 100<<10)
	workspace, documentID, cleanup := largeEventDocument(t, repository, original)
	defer func() { cleanup(repository); repository.Close() }()
	var create, drop []string
	switch dialect {
	case "sqlite":
		create = []string{`CREATE TRIGGER large_event_fail AFTER INSERT ON domain_events WHEN NEW.aggregate_id='` + documentID + `' BEGIN SELECT RAISE(ABORT,'forced failure after the event insert'); END`}
		drop = []string{`DROP TRIGGER IF EXISTS large_event_fail`}
	case "mysql":
		create = []string{`CREATE TRIGGER large_event_fail AFTER INSERT ON domain_events FOR EACH ROW BEGIN IF NEW.aggregate_id='` + documentID + `' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='forced failure after the event insert'; END IF; END`}
		drop = []string{`DROP TRIGGER IF EXISTS large_event_fail`}
	case "postgres":
		create = []string{
			`CREATE OR REPLACE FUNCTION large_event_fail() RETURNS trigger AS $body$ BEGIN IF NEW.aggregate_id='` + documentID + `' THEN RAISE EXCEPTION 'forced failure after the event insert'; END IF; RETURN NEW; END $body$ LANGUAGE plpgsql`,
			`CREATE TRIGGER large_event_fail AFTER INSERT ON domain_events FOR EACH ROW EXECUTE FUNCTION large_event_fail()`,
		}
		drop = []string{`DROP TRIGGER IF EXISTS large_event_fail ON domain_events`, `DROP FUNCTION IF EXISTS large_event_fail()`}
	default:
		t.Skipf("no trigger for %s", dialect)
	}
	removeTrigger := func() {
		for _, statement := range drop {
			if _, err := repository.db.ExecContext(ctx, statement); err != nil {
				t.Errorf("%s: %v", statement, err)
			}
		}
	}
	for _, statement := range create {
		if _, err := repository.db.ExecContext(ctx, statement); err != nil {
			removeTrigger()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	defer removeTrigger()
	workspaceID := repository.Bootstrap().Workspace.ID
	head := func() int64 {
		var value sql.NullInt64
		_ = repository.db.QueryRowContext(ctx, `SELECT head FROM workspace_event_seq WHERE workspace_id=?`, workspaceID).Scan(&value)
		return value.Int64
	}
	headBefore := head()
	err := largeEventEditDocument(repository, workspace, documentID, largeEventMarkdown("updated", 100<<10))
	if err == nil || !strings.Contains(err.Error(), "forced failure") {
		t.Fatalf("edit with a failing event insert: %v", err)
	}
	removeTrigger()
	drop = nil
	if document := storedLargeEventDocument(t, repository, workspace, documentID); document.Content != original || document.Version != 0 {
		t.Fatalf("in-memory document after a failed write: version %d, content equal %v", document.Version, document.Content == original)
	}
	var events int
	if err := repository.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE aggregate_id=? AND event_type='document.updated'`, documentID).Scan(&events); err != nil || events != 0 {
		t.Fatalf("events after a failed write: %d %v", events, err)
	}
	if after := head(); after != headBefore {
		t.Fatalf("change cursor moved from %d to %d", headBefore, after)
	}
	repository.Close()
	repository = open()
	if document := storedLargeEventDocument(t, repository, workspace, documentID); document.Content != original || document.Version != 0 {
		t.Fatalf("stored document after a failed write: version %d, content equal %v", document.Version, document.Content == original)
	}
}
