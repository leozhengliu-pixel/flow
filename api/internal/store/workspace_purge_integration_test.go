//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

type externalColumn struct {
	name, kind string
	required   bool
}

// externalSchema lists the tables of the external database (PostgreSQL or
// MySQL) with their columns.
func externalSchema(t *testing.T, repo *SQLiteStore) map[string][]externalColumn {
	t.Helper()
	query := `SELECT table_name,column_name,data_type,is_nullable,COALESCE(column_default,'') FROM information_schema.columns WHERE table_schema=current_schema() ORDER BY table_name,ordinal_position`
	if repo.dialect == "mysql" {
		query = `SELECT table_name,column_name,data_type,is_nullable,COALESCE(column_default,'') FROM information_schema.columns WHERE table_schema=DATABASE() ORDER BY table_name,ordinal_position`
	}
	rows, err := repo.db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	schema := map[string][]externalColumn{}
	for rows.Next() {
		var table, name, kind, nullable, fallback string
		if err := rows.Scan(&table, &name, &kind, &nullable, &fallback); err != nil {
			t.Fatal(err)
		}
		table, name = strings.ToLower(table), strings.ToLower(name)
		schema[table] = append(schema[table], externalColumn{name: name, kind: strings.ToLower(kind), required: nullable == "NO" && fallback == ""})
	}
	return schema
}

func insertExternalRow(t *testing.T, repo *SQLiteStore, table string, columns []externalColumn, values map[string]any, serial int) error {
	t.Helper()
	names, marks, args := []string{}, []string{}, []any{}
	for _, column := range columns {
		value, ok := values[column.name]
		if !ok {
			if !column.required {
				continue
			}
			switch {
			case strings.Contains(column.kind, "int") || column.kind == "numeric" || column.kind == "decimal" || column.kind == "double precision" || column.kind == "double" || column.kind == "real" || column.kind == "float":
				value = serial
			case column.kind == "boolean":
				value = false
			case strings.Contains(column.kind, "blob") || column.kind == "bytea":
				value = []byte(`{}`)
			case column.kind == "json" || column.kind == "jsonb":
				value = `{}`
			default:
				value = fmt.Sprintf("p%d", serial)
			}
		}
		names = append(names, column.name)
		marks = append(marks, "?")
		args = append(args, value)
	}
	_, err := repo.db.ExecContext(context.Background(), `INSERT INTO `+table+`(`+strings.Join(names, ",")+`) VALUES(`+strings.Join(marks, ",")+`) ON CONFLICT DO NOTHING`, args...)
	return err
}

// TestExternalDatabaseWorkspacePurge runs the purge coverage and row checks
// on PostgreSQL or MySQL: every table with a workspace column is covered, and
// deleting a workspace removes its row from each of them and nothing else.
func TestExternalDatabaseWorkspacePurge(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	ctx := context.Background()
	repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	schema := externalSchema(t, repo)
	direct := map[string]bool{}
	for _, table := range workspacePurgeTables {
		direct[table.table] = true
		if _, ok := schema[table.table]; !ok {
			t.Errorf("workspacePurgeTables lists %s, which the %s schema does not have", table.table, driver)
		}
	}
	for table, columns := range schema {
		if direct[table] || workspacePurgeKept[table] != "" {
			continue
		}
		for _, column := range columns {
			if column.name == "workspace_key" || column.name == "workspace_id" {
				t.Errorf("%s table %s has a workspace column but is not in workspacePurgeTables", driver, table)
			}
		}
		if workspacePurgeIndirect[table] == "" && workspacePurgeExempt[table] == "" {
			t.Errorf("%s table %s is not classified for workspace purges", driver, table)
		}
	}

	const key, otherKey = "purge-integration", "purge-integration-other"
	_ = repo.DeleteWorkspace(ctx, key)
	created, err := repo.CreateWorkspace(ctx, "Purge integration", key, "us")
	if err != nil {
		t.Fatal(err)
	}
	target := WorkspacePurgeTarget{Key: key, ID: created.Workspace.ID}
	other := WorkspacePurgeTarget{Key: otherKey, ID: "purge-integration-other-id"}
	const userID = "usr_purge_integration"
	if err := insertExternalRow(t, repo, "auth_users", schema["auth_users"], map[string]any{"id": userID, "email": "purge-integration@example.test"}, 1); err != nil {
		t.Fatal(err)
	}
	for index, table := range workspacePurgeTables {
		for ownerIndex, owner := range []WorkspacePurgeTarget{target, other} {
			value := owner.Key
			if table.byID {
				value = owner.ID
			}
			serial := 900000 + index*2 + ownerIndex
			values := map[string]any{table.column: value}
			err := insertExternalRow(t, repo, table.table, schema[table.table], values, serial)
			if err != nil && strings.Contains(strings.ToLower(err.Error()), "foreign key") {
				values["user_id"], values["inviter_id"] = userID, userID
				err = insertExternalRow(t, repo, table.table, schema[table.table], values, serial)
			}
			if err != nil {
				t.Fatalf("insert into %s: %v", table.table, err)
			}
		}
	}
	count := func(table, column, value string) int {
		var n int
		if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+column+`=?`, value).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	for _, table := range workspacePurgeTables {
		value := target.Key
		if table.byID {
			value = target.ID
		}
		if count(table.table, table.column, value) == 0 {
			t.Fatalf("setup left %s without a row of the workspace", table.table)
		}
	}
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens(token_hash,data,expires_at,created_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, "rt_purge_integration", []byte(`{"workspaceKey":"`+key+`"}`), "2999-01-01", "2026-01-01"); err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteWorkspace(ctx, key); err != nil {
		t.Fatal(err)
	}
	for _, table := range workspacePurgeTables {
		value, otherValue := target.Key, other.Key
		if table.byID {
			value, otherValue = target.ID, other.ID
		}
		if n := count(table.table, table.column, value); n != 0 {
			t.Errorf("%s keeps %d rows of the deleted workspace", table.table, n)
		}
		if count(table.table, table.column, otherValue) == 0 {
			t.Errorf("%s lost the other workspace's row", table.table)
		}
	}
	if n := count("oauth_refresh_tokens", "token_hash", "rt_purge_integration"); n != 0 {
		t.Errorf("OAuth refresh token of the deleted workspace kept")
	}
	// Leave the shared database clean.
	for _, table := range workspacePurgeTables {
		value := other.Key
		if table.byID {
			value = other.ID
		}
		if _, err := repo.deleteWorkspaceRows(ctx, table.table, table.column, value); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = repo.db.ExecContext(ctx, `DELETE FROM auth_users WHERE id=?`, userID)
}
