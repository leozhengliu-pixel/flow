package store

import (
	"context"
	"strings"
)

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
			statements = append(statements, `CREATE TRIGGER metadata_records_search_`+strings.ToLower(event)+` AFTER `+event+` ON workspace_metadata_records FOR EACH ROW `+insert+` ON DUPLICATE KEY UPDATE content=VALUES(content)`)
		}
		statements = append(statements, `CREATE TRIGGER metadata_records_search_delete AFTER DELETE ON workspace_metadata_records FOR EACH ROW `+remove)
	case "postgres":
		statements = append(statements, `CREATE INDEX IF NOT EXISTS metadata_search_trigram ON metadata_search_documents USING gin(content gin_trgm_ops)`,
			`CREATE OR REPLACE FUNCTION flow_metadata_search_sync() RETURNS trigger AS $$ BEGIN IF TG_OP='DELETE' THEN `+remove+`; RETURN OLD; ELSE `+insert+` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=EXCLUDED.content WHERE metadata_search_documents.content<>EXCLUDED.content; RETURN NEW; END IF; END; $$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE TRIGGER metadata_records_search_sync AFTER INSERT OR UPDATE OR DELETE ON workspace_metadata_records FOR EACH ROW EXECUTE FUNCTION flow_metadata_search_sync()`)
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && (isDuplicateIndex(err) || strings.Contains(strings.ToLower(err.Error()), "trigger already exists"))) {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) migrateMetadataSearchIndex(ctx context.Context) error {
	for workspace := range s.workspaces {
		for _, kind := range searchMetadataKinds {
			var complete int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_search_migrations WHERE workspace_key=? AND field=?`, workspace, kind.field).Scan(&complete); err != nil {
				return err
			}
			if complete > 0 {
				continue
			}
			last := ""
			for {
				rows, err := s.db.QueryContext(ctx, `SELECT record_key FROM workspace_metadata_records m WHERE workspace_key=? AND field=? AND record_key>? AND NOT EXISTS(SELECT 1 FROM metadata_search_documents s WHERE s.workspace_key=m.workspace_key AND s.field=m.field AND s.record_key=m.record_key) ORDER BY record_key LIMIT 128`, workspace, kind.field, last)
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
				_, err = s.db.ExecContext(ctx, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) SELECT workspace_key,field,record_key,`+s.metadataSearchContent("data")+` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+where+` ON CONFLICT(workspace_key,field,record_key) DO NOTHING`, args...)
				if err != nil {
					return err
				}
				last = ids[len(ids)-1]
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO metadata_search_migrations(workspace_key,field) VALUES(?,?) ON CONFLICT DO NOTHING`, workspace, kind.field); err != nil {
				return err
			}
		}
	}
	return nil
}
