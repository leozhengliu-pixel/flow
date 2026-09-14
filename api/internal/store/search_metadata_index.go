package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func isMySQLSearchPermissionError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "command denied") || strings.Contains(msg, "access denied") || strings.Contains(msg, "specific access") || strings.Contains(msg, "super privilege")
}

var metadataSearchFieldSet = func() map[string]bool {
	result := map[string]bool{}
	for _, kind := range searchMetadataKinds {
		result[kind.field] = true
	}
	return result
}()

// syncMetadataSearchDocument is the application-level fallback for MySQL
// installations that cannot create triggers (for example without SUPER or
// SET_USER_ID). It runs in the same transaction as the metadata record write,
// so inserts, edits and deletes remain atomically searchable without a full
// workspace reindex.
func syncMetadataSearchDocument(ctx context.Context, tx *sqlTx, workspace, field, key string, raw []byte) error {
	if !metadataSearchFieldSet[field] {
		return nil
	}
	if raw == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, key)
		return err
	}
	content := metadataSearchText(raw)
	if tx.dialect == "mysql" {
		_, err := tx.ExecContext(ctx, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`, workspace, field, key, content)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) VALUES(?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content`, workspace, field, key, content)
	return err
}

func metadataSearchText(raw []byte) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	parts := make([]string, 0, 13)
	for _, field := range []string{"name", "title", "displayName", "email", "summary", "description", "content", "domains", "version", "status", "tier", "scope", "resource"} {
		item, ok := value[field]
		if !ok || item == nil {
			parts = append(parts, "")
			continue
		}
		encoded, _ := json.Marshal(item)
		var textValue string
		if json.Unmarshal(encoded, &textValue) == nil {
			parts = append(parts, textValue)
		} else {
			// Match json_extract()/CONCAT_WS output used by trigger backfills,
			// including compact JSON arrays and scalar numbers/booleans.
			parts = append(parts, string(encoded))
		}
	}
	return strings.Join(parts, " ")
}

func (s *SQLiteStore) metadataSearchContent(column string) string {
	parts := []string{}
	for _, field := range []string{"name", "title", "displayName", "email", "summary", "description", "content", "domains", "version", "status", "tier", "scope", "resource"} {
		parts = append(parts, "COALESCE("+s.jsonText(column, field)+",'')")
	}
	if s.dialect == "mysql" {
		return "CONCAT_WS(' '," + strings.Join(parts, ",") + ")"
	}
	return strings.Join(parts, " || ' ' || ")
}

func (s *SQLiteStore) ensureMetadataSearchIndex(ctx context.Context) error {
	textType := "TEXT"
	if s.dialect == "mysql" {
		textType = "LONGTEXT"
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata_search_documents(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,record_key VARCHAR(191) NOT NULL,content `+textType+` NOT NULL,PRIMARY KEY(workspace_key,field,record_key))`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata_search_migrations(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,PRIMARY KEY(workspace_key,field))`); err != nil {
		return err
	}
	// Checkpoints make the legacy index repair resumable.  The completed bit is
	// deliberately reset on the next startup so an already-migrated database is
	// reconciled again (repairing stale rows, not just missing rows).
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata_search_migration_checkpoints(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,last_record_key VARCHAR(191) NOT NULL DEFAULT '',completed INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(workspace_key,field))`); err != nil {
		return err
	}
	fields := `('projects','documents','initiatives','users','customers','releases','savedViews')`
	insert := `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) SELECT NEW.workspace_key,NEW.field,NEW.record_key,` + s.metadataSearchContent("NEW.data") + ` WHERE NEW.field IN ` + fields
	remove := `DELETE FROM metadata_search_documents WHERE workspace_key=OLD.workspace_key AND field=OLD.field AND record_key=OLD.record_key`
	statements := []string{}
	switch s.dialect {
	case "sqlite":
		statements = append(statements,
			`CREATE VIRTUAL TABLE IF NOT EXISTS metadata_search_fts USING fts5(content,content='metadata_search_documents',content_rowid='rowid',tokenize='trigram')`,
			`CREATE TRIGGER IF NOT EXISTS metadata_search_insert AFTER INSERT ON metadata_search_documents BEGIN INSERT INTO metadata_search_fts(rowid,content) VALUES(new.rowid,new.content); END`,
			`CREATE TRIGGER IF NOT EXISTS metadata_search_delete AFTER DELETE ON metadata_search_documents BEGIN INSERT INTO metadata_search_fts(metadata_search_fts,rowid,content) VALUES('delete',old.rowid,old.content); END`,
			`CREATE TRIGGER IF NOT EXISTS metadata_search_update AFTER UPDATE ON metadata_search_documents BEGIN INSERT INTO metadata_search_fts(metadata_search_fts,rowid,content) VALUES('delete',old.rowid,old.content); INSERT INTO metadata_search_fts(rowid,content) VALUES(new.rowid,new.content); END`,
			`CREATE TRIGGER IF NOT EXISTS metadata_records_search_insert AFTER INSERT ON workspace_metadata_records BEGIN `+insert+` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content; END`,
			`CREATE TRIGGER IF NOT EXISTS metadata_records_search_update AFTER UPDATE ON workspace_metadata_records WHEN NEW.data<>OLD.data BEGIN `+insert+` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content; END`,
			`CREATE TRIGGER IF NOT EXISTS metadata_records_search_delete AFTER DELETE ON workspace_metadata_records BEGIN `+remove+`; END`)
	case "mysql":
		statements = append(statements, `CREATE FULLTEXT INDEX metadata_search_fulltext ON metadata_search_documents(content) WITH PARSER ngram`)
		for _, event := range []string{"INSERT", "UPDATE"} {
			statements = append(statements, `CREATE TRIGGER metadata_records_search_`+strings.ToLower(event)+` AFTER `+event+` ON workspace_metadata_records FOR EACH ROW `+insert+` ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`)
		}
		statements = append(statements, `CREATE TRIGGER metadata_records_search_delete AFTER DELETE ON workspace_metadata_records FOR EACH ROW `+remove)
	case "postgres":
		statements = append(statements, `CREATE INDEX IF NOT EXISTS metadata_search_trigram ON metadata_search_documents USING gin(content gin_trgm_ops)`,
			`CREATE OR REPLACE FUNCTION flow_metadata_search_sync() RETURNS trigger AS $$ BEGIN IF TG_OP='DELETE' THEN `+remove+`; RETURN OLD; ELSE `+insert+` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=EXCLUDED.content WHERE metadata_search_documents.content<>EXCLUDED.content; RETURN NEW; END IF; END; $$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE TRIGGER metadata_records_search_sync AFTER INSERT OR UPDATE OR DELETE ON workspace_metadata_records FOR EACH ROW EXECUTE FUNCTION flow_metadata_search_sync()`)
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			if s.dialect == "mysql" && (isDuplicateIndex(err) || strings.Contains(strings.ToLower(err.Error()), "trigger already exists") || isMySQLSearchPermissionError(err)) {
				// Trigger/full-text creation is optional on restricted MySQL users;
				// application-level transactional sync remains authoritative.
				continue
			}
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) migrateMetadataSearchIndex(ctx context.Context) error {
	for workspace := range s.workspaces {
		for _, kind := range searchMetadataKinds {
			var last string
			var complete int
			err := s.db.QueryRowContext(ctx, `SELECT last_record_key,completed FROM metadata_search_migration_checkpoints WHERE workspace_key=? AND field=?`, workspace, kind.field).Scan(&last, &complete)
			if err != nil {
				if err != sql.ErrNoRows {
					return err
				}
				if _, err = s.db.ExecContext(ctx, `INSERT INTO metadata_search_migration_checkpoints(workspace_key,field,last_record_key,completed) VALUES(?,?,?,0)`, workspace, kind.field, ""); err != nil {
					return err
				}
				last = ""
			}
			if complete != 0 {
				last = ""
				if _, err := s.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET last_record_key='',completed=0 WHERE workspace_key=? AND field=?`, workspace, kind.field); err != nil {
					return err
				}
			}
			for {
				rows, err := s.db.QueryContext(ctx, `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key>? ORDER BY record_key LIMIT 128`, workspace, kind.field, last)
				if err != nil {
					return err
				}
				ids := []string{}
				for rows.Next() {
					var id string
					if err := rows.Scan(&id); err != nil {
						rows.Close()
						return err
					}
					ids = append(ids, id)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if len(ids) == 0 {
					break
				}
				where, args := bindList("record_key", ids)
				args = append([]any{workspace, kind.field}, args...)
				upsert := ` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content`
				if s.dialect == "mysql" {
					upsert = ` ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`
				}
				if s.dialect == "postgres" {
					upsert = ` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=EXCLUDED.content WHERE metadata_search_documents.content<>EXCLUDED.content`
				}
				_, err = s.db.ExecContext(ctx, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) SELECT workspace_key,field,record_key,`+s.metadataSearchContent("data")+` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+where+upsert, args...)
				if err != nil {
					return err
				}
				last = ids[len(ids)-1]
				if _, err = s.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET last_record_key=?,completed=0 WHERE workspace_key=? AND field=?`, last, workspace, kind.field); err != nil {
					return err
				}
			}
			// Remove rows left behind by records deleted before this repair ran.
			if _, err := s.db.ExecContext(ctx, `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field=? AND NOT EXISTS (SELECT 1 FROM workspace_metadata_records m WHERE m.workspace_key=metadata_search_documents.workspace_key AND m.field=metadata_search_documents.field AND m.record_key=metadata_search_documents.record_key)`, workspace, kind.field); err != nil {
				return err
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET completed=1 WHERE workspace_key=? AND field=?`, workspace, kind.field); err != nil {
				return err
			}
			migrationInsert := `INSERT INTO metadata_search_migrations(workspace_key,field) VALUES(?,?) ON CONFLICT DO NOTHING`
			if s.dialect == "mysql" {
				migrationInsert = `INSERT IGNORE INTO metadata_search_migrations(workspace_key,field) VALUES(?,?)`
			}
			if _, err := s.db.ExecContext(ctx, migrationInsert, workspace, kind.field); err != nil {
				return err
			}
		}
	}
	return nil
}
