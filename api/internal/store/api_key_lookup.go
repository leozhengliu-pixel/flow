package store

import (
	"context"
	"strings"
)

func (s *SQLiteStore) ensureAPIKeyLookup(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS api_key_lookup(workspace_key VARCHAR(191) NOT NULL,key_id VARCHAR(191) NOT NULL,secret_hash VARCHAR(64) NOT NULL,PRIMARY KEY(workspace_key,key_id))`); err != nil {
		return err
	}
	index := "CREATE INDEX IF NOT EXISTS api_key_lookup_hash ON api_key_lookup(secret_hash)"
	if s.dialect == "mysql" {
		index = "CREATE INDEX api_key_lookup_hash ON api_key_lookup(secret_hash)"
	}
	if _, err := s.db.ExecContext(ctx, index); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS api_key_lookup_migrations(workspace_key VARCHAR(191) PRIMARY KEY)`); err != nil {
		return err
	}
	insert := `INSERT INTO api_key_lookup(workspace_key,key_id,secret_hash) SELECT NEW.workspace_key,NEW.record_key,COALESCE(` + s.jsonText("NEW.data", "secretHash") + `,'') WHERE NEW.field='apiKeys'`
	remove := `DELETE FROM api_key_lookup WHERE workspace_key=OLD.workspace_key AND key_id=OLD.record_key AND OLD.field='apiKeys'`
	statements := []string{}
	switch s.dialect {
	case "sqlite":
		for _, event := range []string{"INSERT", "UPDATE"} {
			statements = append(statements, `CREATE TRIGGER IF NOT EXISTS api_key_lookup_`+strings.ToLower(event)+` AFTER `+event+` ON workspace_metadata_records WHEN NEW.field='apiKeys' BEGIN `+insert+` ON CONFLICT(workspace_key,key_id) DO UPDATE SET secret_hash=excluded.secret_hash WHERE api_key_lookup.secret_hash<>excluded.secret_hash; END`)
		}
		statements = append(statements, `CREATE TRIGGER IF NOT EXISTS api_key_lookup_delete AFTER DELETE ON workspace_metadata_records WHEN OLD.field='apiKeys' BEGIN `+remove+`; END`)
	case "mysql":
		for _, event := range []string{"INSERT", "UPDATE"} {
			statements = append(statements, `CREATE TRIGGER api_key_lookup_`+strings.ToLower(event)+` AFTER `+event+` ON workspace_metadata_records FOR EACH ROW `+insert+` ON DUPLICATE KEY UPDATE secret_hash=VALUES(secret_hash)`)
		}
		statements = append(statements, `CREATE TRIGGER api_key_lookup_delete AFTER DELETE ON workspace_metadata_records FOR EACH ROW `+remove)
	case "postgres":
		statements = append(statements, `CREATE OR REPLACE FUNCTION flow_api_key_lookup_sync() RETURNS trigger AS $$ BEGIN IF TG_OP='DELETE' THEN `+remove+`; RETURN OLD; ELSE `+insert+` ON CONFLICT(workspace_key,key_id) DO UPDATE SET secret_hash=EXCLUDED.secret_hash WHERE api_key_lookup.secret_hash<>EXCLUDED.secret_hash; RETURN NEW; END IF; END; $$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE TRIGGER api_key_lookup_sync AFTER INSERT OR UPDATE OR DELETE ON workspace_metadata_records FOR EACH ROW EXECUTE FUNCTION flow_api_key_lookup_sync()`)
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && (isDuplicateIndex(err) || strings.Contains(strings.ToLower(err.Error()), "trigger already exists"))) {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) migrateAPIKeyLookup(ctx context.Context) error {
	for _, workspace := range s.WorkspaceKeys() {
		var complete int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_key_lookup_migrations WHERE workspace_key=?`, workspace).Scan(&complete); err != nil {
			return err
		}
		if complete > 0 {
			continue
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO api_key_lookup(workspace_key,key_id,secret_hash) SELECT workspace_key,record_key,COALESCE(`+s.jsonText("data", "secretHash")+`,'') FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' ON CONFLICT(workspace_key,key_id) DO NOTHING`, workspace); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO api_key_lookup_migrations(workspace_key) VALUES(?) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
	}
	return nil
}
