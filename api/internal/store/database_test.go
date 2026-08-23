package store

import (
	"strings"
	"testing"
)

func TestDatabaseDialectRewriting(t *testing.T) {
	postgres := rewriteSQL("SELECT * FROM auth_users WHERE email=? AND active=?", "postgres")
	if postgres != "SELECT * FROM auth_users WHERE email=$1 AND active=$2" {
		t.Fatalf("postgres rewrite = %q", postgres)
	}
	mysql := rewriteSQL("INSERT INTO auth_account_state(user_id,last_workspace_key,updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,updated_at=excluded.updated_at", "mysql")
	if !strings.Contains(mysql, "ON DUPLICATE KEY UPDATE") || !strings.Contains(mysql, "VALUES(last_workspace_key)") {
		t.Fatalf("mysql upsert rewrite = %q", mysql)
	}
	ignore := rewriteSQL("INSERT INTO auth_users(id) VALUES(?) ON CONFLICT DO NOTHING", "mysql")
	if ignore != "INSERT IGNORE INTO auth_users(id) VALUES(?)" {
		t.Fatalf("mysql ignore rewrite = %q", ignore)
	}
}

func TestMySQLURLConversion(t *testing.T) {
	dsn, err := mysqlDSN("mysql://flow:secret@db:3306/flow?tls=true")
	if err != nil || dsn != "flow:secret@tcp(db:3306)/flow?parseTime=true&tls=true" {
		t.Fatalf("mysql DSN=%q err=%v", dsn, err)
	}
}
