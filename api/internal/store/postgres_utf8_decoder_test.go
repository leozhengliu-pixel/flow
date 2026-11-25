package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func openRawUTF8TestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || databaseURL == "" {
		repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "raw-utf8.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		return repo
	}
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

// The repairing decoder agrees with Go's per-byte U+FFFD substitution for
// every sequence class, and flow_utf8 with convert_from for valid text in
// either definition.
func TestPostgresUTF8DecoderMatchesGo(t *testing.T) {
	repo := openRawUTF8TestStore(t)
	if repo.dialect != "postgres" {
		t.Skip("PostgreSQL decoder")
	}
	cases := [][]byte{
		[]byte(`{"a":"plain ascii"}`), []byte("视图 \U0001F600 é"), {},
		{'a', 0xff, 'b'}, {0xe2, 0x82}, {0xe2, 0x82, 0xac, 0xe2}, {0xc0, 0x80}, {0xed, 0xa0, 0x80},
		{0xf4, 0x90, 0x80, 0x80}, {0xf5, 0x80}, {0x80, 0x80, 'x'}, {0xf0, 0x9f, 0x98, 0x80, 0xf0, 0x9f, 0x98},
		append([]byte("好"), 0xfe, 0xe5, 0xa5, 0xbd),
		append(bytes.Repeat([]byte("字x"), 50000), 0xff),
	}
	for index, raw := range cases {
		functions := []string{"flow_utf8_repair"}
		if utf8.Valid(raw) {
			functions = append(functions, "flow_utf8")
		}
		for _, function := range functions {
			var got string
			if err := repo.db.QueryRowContext(context.Background(), `SELECT `+function+`(?)`, raw).Scan(&got); err != nil {
				t.Fatal(index, function, err)
			}
			if want := string([]rune(string(raw))); got != want {
				t.Fatalf("case %d: %s=%q want %q", index, function, got, want)
			}
		}
	}
}

// Rows whose body holds raw invalid UTF-8 inside an opaque string read like
// SQLite for ASCII keys and valid text, and are still rewritten byte for byte.
func TestRawInvalidUTF8RecordsReadAndRenameOnEveryDialect(t *testing.T) {
	repo := openRawUTF8TestStore(t)
	ctx := context.Background()
	workspace := repo.Bootstrap().Workspace.URLKey
	raw := append([]byte(`{"id":"raw-view","name":"视图","workspace":"`+workspace+`","nested":{"leaf":"ok"},"teamIds":["t1","t2"],"filters":"bad `), 0xff, 0xfe)
	raw = append(raw, []byte(` tail","position":3}`)...)
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?)`, workspace, "views", "raw-view", raw); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = repo.db.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND record_key=?`, workspace, "raw-view")
	})
	read := func(field string) string {
		t.Helper()
		var value *string
		if err := repo.db.QueryRowContext(ctx, `SELECT `+repo.jsonText("data", field)+` FROM workspace_metadata_records WHERE workspace_key=? AND field='views' AND record_key='raw-view'`, workspace).Scan(&value); err != nil {
			t.Fatal(field, err)
		}
		if value == nil {
			return "<null>"
		}
		return *value
	}
	for field, want := range map[string]string{"id": "raw-view", "name": "视图", "nested.leaf": "ok", "position": "3", "missing": "<null>"} {
		if got := read(field); got != want {
			t.Fatalf("%s=%q want %q", field, got, want)
		}
	}
	if got := read("teamIds"); !strings.Contains(got, `"t1"`) || !strings.Contains(got, `"t2"`) {
		t.Fatal("array member", got)
	}
	if got := read("filters"); repo.dialect == "postgres" {
		if want := string([]rune("bad \xff\xfe tail")); got != want {
			t.Fatalf("filters=%q want %q", got, want)
		}
	} else if got != "bad \xff\xfe tail" {
		t.Fatalf("filters=%q", got)
	}

	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	renamed := "renamed-" + workspace
	if err := repo.rewriteWorkspaceKeyJSON(ctx, tx, "workspace_metadata_records", renameColumn{name: "data", binary: true}, workspace, workspace, renamed); err != nil {
		_ = tx.Rollback()
		t.Fatal("rename of a raw invalid UTF-8 record", err)
	}
	var stored []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='views' AND record_key='raw-view'`, workspace).Scan(&stored); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	_ = tx.Rollback()
	want := bytes.Replace(raw, []byte(`"workspace":"`+workspace+`"`), []byte(`"workspace":"`+renamed+`"`), 1)
	if !bytes.Equal(stored, want) {
		t.Fatalf("rename changed bytes: %q want %q", stored, want)
	}
}

// flow_utf8 starts as the inlined convert_from and becomes the repair decoder
// before the first raw invalid UTF-8 JSON body is stored, or at its first
// install when an existing column already holds one. Blobs that are not JSON
// (ciphertexts, compressed bodies) do not switch it.
func TestPostgresUTF8DecoderSwitch(t *testing.T) {
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver != "postgres" || databaseURL == "" {
		t.Skip("PostgreSQL decoder")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("utf8_switch_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) })
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	db, err := sql.Open("pgx", databaseURL+separator+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	mode := func() string {
		t.Helper()
		var source string
		if err := db.QueryRowContext(ctx, postgresUTF8Mode).Scan(&source); err != nil {
			t.Fatal(err)
		}
		switch source {
		case postgresUTF8FastSource:
			return "fast"
		case postgresUTF8RepairSource:
			return "repair"
		}
		return "other: " + source
	}
	reinstall := func(want string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `DROP FUNCTION IF EXISTS flow_utf8(bytea)`); err != nil {
			t.Fatal(err)
		}
		repair, err := installPostgresUTF8Decoder(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if got := mode(); got != want || repair != (want == "repair") {
			t.Fatalf("install chose %s (repair=%v), want %s", got, repair, want)
		}
	}
	reinstall("fast")
	if _, err := db.ExecContext(ctx, `CREATE TABLE bodies(id INTEGER PRIMARY KEY, data BYTEA)`); err != nil {
		t.Fatal(err)
	}
	var plan string
	rows, err := db.QueryContext(ctx, `EXPLAIN (VERBOSE, COSTS OFF) SELECT flow_utf8(data)::jsonb->>'id' FROM bodies`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	rows.Close()
	if !strings.Contains(plan, "convert_from(data, 'UTF8'::name)") || strings.Contains(plan, "flow_utf8(") {
		t.Fatalf("the fast decoder is not inlined:\n%s", plan)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO bodies VALUES(1,$1),(2,$2),(3,NULL)`, []byte(`{"id":"视图"}`), []byte{0x1f, 0x8b, 0xff, 0x00, 0xfe, '{'}); err != nil {
		t.Fatal(err)
	}
	reinstall("fast")
	raw := []byte(`{"id":"raw","v":"` + "\xff\xfe" + `"}`)
	if _, err := db.ExecContext(ctx, `INSERT INTO bodies VALUES(4,$1)`, raw); err != nil {
		t.Fatal(err)
	}
	reinstall("repair")
	if _, err := db.ExecContext(ctx, `DELETE FROM bodies WHERE id=4`); err != nil {
		t.Fatal(err)
	}
	reinstall("fast")

	// Writes through the statement wrappers switch before storing the body.
	database := &sqlDatabase{DB: db, dialect: "postgres"}
	database.utf8Repair.Store(false)
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO bodies VALUES(?,?)`, 5, json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if got := mode(); got != "fast" || database.utf8Repair.Load() {
		t.Fatal("a rolled back write kept its switch", got)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO bodies VALUES(?,?)`, 6, []byte(`not json `+"\xff")); err != nil {
		t.Fatal(err)
	}
	if got := mode(); got != "fast" {
		t.Fatal("a raw blob that is not JSON switched the decoder", got)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM bodies WHERE id=6`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO bodies VALUES(?,?)`, 7, raw); err != nil {
		t.Fatal(err)
	}
	if got := mode(); got != "repair" || !database.utf8Repair.Load() {
		t.Fatal("a raw JSON body did not switch the decoder", got)
	}
	var id, value string
	if err := db.QueryRowContext(ctx, `SELECT flow_utf8(data)::jsonb->>'id', flow_utf8(data)::jsonb->>'v' FROM bodies WHERE id=7`).Scan(&id, &value); err != nil {
		t.Fatal(err)
	}
	if id != "raw" || value != "��" {
		t.Fatalf("repaired read %q %q", id, value)
	}
	// Another switch is a no-op; reinstalling keeps the repair decoder.
	if _, err := db.ExecContext(ctx, `SELECT flow_utf8_enable_repair()`); err != nil {
		t.Fatal(err)
	}
	if repair, err := installPostgresUTF8Decoder(ctx, db); err != nil || !repair || mode() != "repair" {
		t.Fatal("reinstall left repair", repair, err)
	}
}
