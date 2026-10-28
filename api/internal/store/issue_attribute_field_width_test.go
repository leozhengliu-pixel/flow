package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// scratchPostgresDatabase is scratchMySQLDatabase for a postgres://
// FLOW_TEST_DATABASE_URL.
func scratchPostgresDatabase(t *testing.T, open func() *SQLiteStore) func() *SQLiteStore {
	t.Helper()
	ctx := context.Background()
	parsed, err := url.Parse(os.Getenv("FLOW_TEST_DATABASE_URL"))
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Skip("the scratch database needs a postgres:// FLOW_TEST_DATABASE_URL")
	}
	name := fmt.Sprintf("flow_scratch_%d", time.Now().UnixNano())
	admin := open()
	if _, err := admin.db.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		admin.Close()
		t.Skipf("cannot create a scratch database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.db.ExecContext(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Errorf("drop scratch database %s: %v", name, err)
		}
		admin.Close()
	})
	parsed.Path = "/" + name
	return func() *SQLiteStore {
		repository, err := OpenDatabase(DatabaseConfig{Driver: "postgres", URL: parsed.String(), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repository
	}
}

// Migration 17 widens issue_attribute_records.field from VARCHAR(32) to
// VARCHAR(191) on MySQL and Postgres, keeping its rows and indexes, and
// changes nothing on a re-run; a fresh install is created at 191. Triage
// suggestion fields longer than 32 characters are then stored, and one
// longer than the column is stored as a prefix plus digest.
func TestIssueAttributeFieldWidthMigration(t *testing.T) {
	for name, open := range loopAppendStoreOpeners(t) {
		t.Run(name, func(t *testing.T) { testIssueAttributeFieldWidthMigration(t, name, open) })
	}
}

func testIssueAttributeFieldWidthMigration(t *testing.T, dialect string, open func() *SQLiteStore) {
	ctx := context.Background()
	switch dialect {
	case "mysql":
		open = scratchMySQLDatabase(t, open)
	case "postgres":
		open = scratchPostgresDatabase(t, open)
	}
	repository := open()
	defer func() { repository.Close() }()
	width := func() (int64, string) {
		t.Helper()
		var length sql.NullInt64
		var nullable string
		var err error
		switch dialect {
		case "mysql":
			err = repository.db.QueryRowContext(ctx, `SELECT CHARACTER_MAXIMUM_LENGTH,IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='issue_attribute_records' AND COLUMN_NAME='field'`).Scan(&length, &nullable)
		case "postgres":
			err = repository.db.QueryRowContext(ctx, `SELECT character_maximum_length,is_nullable FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='issue_attribute_records' AND column_name='field'`).Scan(&length, &nullable)
		default:
			return 0, ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return length.Int64, nullable
	}
	workspace := repository.Bootstrap().Workspace.URLKey
	issueID := repository.Bootstrap().Issues[0].ID
	setSuggestions := func(labels ...string) error {
		return repository.MutateWorkspace(WithIssueRecordMutations(ctx, issueID), workspace, "test.issue_attribute_width", issueID, nil, func(data *domain.Bootstrap) error {
			index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == issueID })
			data.Issues[index].SuggestedLabelIDs = labels
			data.Issues[index].Version++
			data.Issues[index].UpdatedAt = time.Now().UTC()
			return nil
		})
	}
	fields := func() []string {
		t.Helper()
		rows, err := repository.db.QueryContext(ctx, `SELECT field FROM issue_attribute_records WHERE workspace_key=? AND issue_id=? ORDER BY field`, workspace, issueID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		list := []string{}
		for rows.Next() {
			var field string
			if err := rows.Scan(&field); err != nil {
				t.Fatal(err)
			}
			list = append(list, field)
		}
		return list
	}

	if dialect != "sqlite" {
		if length, nullable := width(); length != 191 || nullable != "NO" {
			t.Fatalf("fresh install: field is VARCHAR(%d) nullable %s, want VARCHAR(191) NOT NULL", length, nullable)
		}
		// A database from before migration 17, holding a short attribute row.
		if err := setSuggestions("label_a"); err != nil {
			t.Fatal(err)
		}
		legacy := map[string]string{
			"mysql":    "ALTER TABLE issue_attribute_records MODIFY COLUMN field VARCHAR(32) NOT NULL",
			"postgres": "ALTER TABLE issue_attribute_records ALTER COLUMN field TYPE VARCHAR(32)",
		}[dialect]
		for _, statement := range []string{legacy, "DELETE FROM schema_migrations WHERE version=17"} {
			if _, err := repository.db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
		if length, _ := width(); length != 32 {
			t.Fatalf("legacy field is VARCHAR(%d)", length)
		}
		// The failure this migration fixes.
		if err := setSuggestions("label_1761600000000000000"); err == nil {
			t.Fatal("a 40-character field fit VARCHAR(32)")
		}
		var before int
		if err := repository.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_attribute_records`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		repository.Close()
		repository = open()
		if length, nullable := width(); length != 191 || nullable != "NO" {
			t.Fatalf("after the migration: field is VARCHAR(%d) nullable %s", length, nullable)
		}
		var after int
		if err := repository.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_attribute_records`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before || !slices.Contains(fields(), "suggestedLabel:label_a") {
			t.Fatalf("rows after the migration: %d of %d, fields %v", after, before, fields())
		}
		var applied int
		if err := repository.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=17`).Scan(&applied); err != nil || applied != 1 {
			t.Fatalf("migration 17 recorded %d times: %v", applied, err)
		}
	}
	// A re-run (another server, or a lost migration record) changes nothing.
	for range 2 {
		if err := repository.widenKeyColumns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if length, _ := width(); dialect != "sqlite" && length != 191 {
		t.Fatalf("re-run left VARCHAR(%d)", length)
	}

	long := "label_import_" + strings.Repeat("9", 300)
	if err := setSuggestions("label_1761600000000000000", long); err != nil {
		t.Fatalf("store long suggestion fields: %v", err)
	}
	stored := fields()
	if !slices.Contains(stored, "suggestedLabel:label_1761600000000000000") || !slices.Contains(stored, indexKey("suggestedLabel:"+long)) {
		t.Fatalf("stored fields %v", stored)
	}
	for _, field := range stored {
		if len(field) > indexKeyMaxBytes {
			t.Fatalf("field of %d bytes stored", len(field))
		}
	}
	for _, label := range []string{"label_1761600000000000000", long} {
		page, err := repository.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Limit: 50, Filter: IssueFilter{Field: "suggestedLabel:" + label, Operator: "isNotEmpty"}})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(page.Items, func(issue domain.Issue) bool { return issue.ID == issueID }) {
			t.Fatalf("filter suggestedLabel:%s… did not match", label[:20])
		}
	}
}
