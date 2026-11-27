package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
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
	content := capSearchText(metadataSearchText(raw), tx.database.searchMaxChars)
	return syncMetadataSearchContent(ctx, tx, workspace, field, key, content)
}

func syncMetadataSearchContent(ctx context.Context, tx *sqlTx, workspace, field, key, content string) error {
	_, err := tx.ExecContext(ctx, metadataSQLStageLegacySearchSQL(tx.dialect), workspace, field, key, content)
	return err
}

func metadataSearchText(raw []byte) string {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	parts := make([]string, 0, len(metadataSearchTextFields))
	for _, field := range metadataSearchTextFields {
		item, ok := value[field]
		if !ok || item == nil {
			parts = append(parts, "")
			continue
		}
		parts = append(parts, metadataSearchPart(item))
	}
	return strings.Join(parts, " ")
}

// metadataSearchTextFields are the record fields whose text a metadata
// search document holds, in order, separated by single spaces.
var metadataSearchTextFields = []string{"name", "title", "displayName", "email", "summary", "description", "content", "domains", "version", "status", "tier", "scope", "resource"}

// metadataSearchPart is the text of one non-null field value.
func metadataSearchPart(item any) string {
	encoded, _ := json.Marshal(item)
	var textValue string
	if json.Unmarshal(encoded, &textValue) == nil {
		return textValue
	}
	// Match json_extract()/CONCAT_WS output used by trigger backfills,
	// including compact JSON arrays and scalar numbers/booleans.
	return string(encoded)
}

func (s *SQLiteStore) metadataSearchContent(column string) string {
	parts := []string{}
	for _, field := range metadataSearchTextFields {
		parts = append(parts, "COALESCE("+s.jsonText(column, field)+",'')")
	}
	if s.dialect == "mysql" {
		return s.searchCapExpression("CONCAT_WS(' '," + strings.Join(parts, ",") + ")")
	}
	return s.searchCapExpression(strings.Join(parts, " || ' ' || "))
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
	// Checkpoints make the legacy index repair resumable. A completed field is
	// reconciled again (repairing stale rows, not just missing rows) only when
	// the rules that decide a document's content changed since it completed.
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata_search_migration_checkpoints(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,last_record_key VARCHAR(191) NOT NULL DEFAULT '',completed INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(workspace_key,field))`); err != nil {
		return err
	}
	// The rules a completed reconciliation used (metadataSearchRules). A start
	// with the same rules skips the field instead of walking it again.
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata_search_index_rules(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,rules VARCHAR(64) NOT NULL,PRIMARY KEY(workspace_key,field))`); err != nil {
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
		// Full-text creation is optional on restricted MySQL users.
		if err := s.ensureMySQLNgramIndex(ctx, "metadata_search_documents", "metadata_search_fulltext", true, fullTextIndexRebuilds("metadata_search_documents", metadataSearchStopwordsMigrationVersion, metadataSearchRestoreMigrationVersion)...); err != nil {
			return err
		}
		// An account that certainly cannot create triggers issues no trigger
		// DDL; the application-level sync is authoritative either way.
		denied, err := s.mysqlTriggerAuthorityDenied(ctx)
		if err != nil {
			return err
		}
		if !denied {
			for _, event := range []string{"INSERT", "UPDATE"} {
				statements = append(statements, `CREATE TRIGGER metadata_records_search_`+strings.ToLower(event)+` AFTER `+event+` ON workspace_metadata_records FOR EACH ROW `+insert+` ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`)
			}
			statements = append(statements, `CREATE TRIGGER metadata_records_search_delete AFTER DELETE ON workspace_metadata_records FOR EACH ROW `+remove)
		}
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

// writeMetadataSearchPage copies the search documents of one page of records
// (keys of one workspace field) into metadata_search_documents, rewriting the
// documents that differ from their records. Both index repairs use it.
//
// On MySQL a multi-row INSERT ... ON DUPLICATE KEY UPDATE that changes rows
// drops them from the InnoDB FULLTEXT index (8.4: MATCH no longer finds them,
// LIKE does; see mysqlFullTextIndexTables), so the documents that
// exist are updated in place and only missing ones are inserted. The
// comparison is the upsert's: the column's collation decides what differs.
// The documents hold the text the application writes (metadataSearchText;
// see mysqlMetadataSearchPageContent), so an unchanged document is not
// rewritten on every start.
func (s *SQLiteStore) writeMetadataSearchPage(ctx context.Context, workspace, field string, keys []string) error {
	if s.dialect == "mysql" {
		var err error
		keys, err = s.repairShortMySQLSearchDocuments(ctx, workspace, field, keys)
		if err != nil || len(keys) == 0 {
			return err
		}
	}
	where, args := bindList("record_key", keys)
	args = append([]any{workspace, field}, args...)
	if s.dialect == "mysql" {
		content, contentArgs, release, err := s.mysqlMetadataSearchPageContent(ctx, args, where)
		if err != nil {
			return err
		}
		defer release()
		mine, _ := bindList("m.record_key", keys)
		// Walk the index document keys, then look up each canonical row
		// directly. Casting the outer text key retains exact byte identity
		// and keeps the canonical binary primary key usable.
		theirs, _ := bindList("idx.record_key", keys)
		updateArgs := append(append(append(append([]any{}, contentArgs...), args...), args...), contentArgs...)
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := writeSearchIndexRepair(ctx, tx, workspace, `UPDATE metadata_search_documents idx STRAIGHT_JOIN workspace_metadata_records m ON m.workspace_key=idx.workspace_key AND m.field=idx.field AND m.record_key=CAST(idx.record_key AS BINARY) SET idx.content=`+content+` WHERE m.workspace_key=? AND m.field=? AND `+mine+` AND idx.workspace_key=? AND idx.field=? AND `+theirs+` AND idx.content<>`+content, updateArgs...); err != nil {
			return err
		}
		// The upsert stays for a document written meanwhile; without a
		// concurrent writer it only inserts.
		if err := writeSearchIndexRepair(ctx, tx, workspace, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) SELECT workspace_key,field,record_key,`+content+` FROM workspace_metadata_records m WHERE workspace_key=? AND field=? AND `+where+` AND NOT EXISTS (SELECT 1 FROM metadata_search_documents idx WHERE idx.workspace_key=m.workspace_key AND idx.field=m.field AND idx.record_key=m.record_key) ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`, append(append([]any{}, contentArgs...), args...)...); err != nil {
			return err
		}
		return tx.Commit()
	}
	upsert := ` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content`
	if s.dialect == "postgres" {
		upsert = ` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=EXCLUDED.content WHERE metadata_search_documents.content<>EXCLUDED.content`
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := writeSearchIndexRepair(ctx, tx, workspace, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) SELECT workspace_key,field,record_key,`+s.metadataSearchContent("data")+` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+where+upsert, args...); err != nil {
		return err
	}
	return tx.Commit()
}

// Parse ordinary rows once in Go, using the same projection as live writes.
// Large records still use SQL so a large description cannot exhaust memory.
func (s *SQLiteStore) repairShortMySQLSearchDocuments(ctx context.Context, workspace, field string, keys []string) ([]string, error) {
	const maxBody = 32 << 10
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, maxBody*(len(keys)*6+32))
	if errors.Is(err, ErrMetadataWorkBudget) {
		return keys, nil
	}
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	where, args := bindList("record_key", keys)
	// Hold the canonical rows until the index changes commit, just like the
	// trigger. A concurrent edit must not be overwritten with an old body.
	rows, err := tx.QueryContext(ctx, `SELECT record_key,CASE WHEN LENGTH(data)<=? THEN data END
		FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+where+` ORDER BY record_key FOR UPDATE`,
		append([]any{maxBody, workspace, field}, args...)...)
	if err != nil {
		return nil, err
	}
	var large []string
	projected := make(map[string]string, len(keys))
	for rows.Next() {
		var key string
		var body []byte
		if err := rows.Scan(&key, &body); err != nil {
			rows.Close()
			return nil, err
		}
		if body == nil {
			large = append(large, key)
			continue
		}
		projected[key] = capSearchText(metadataSearchText(body), s.db.searchMaxChars)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(projected) == 0 {
		return large, tx.Commit()
	}

	rows, err = tx.QueryContext(ctx, `SELECT record_key,LOWER(SHA2(content,256)) FROM metadata_search_documents
		WHERE workspace_key=? AND field=? AND `+where, append([]any{workspace, field}, args...)...)
	if err != nil {
		return nil, err
	}
	different := make(map[string]bool, len(projected))
	for rows.Next() {
		var key, digest string
		if err := rows.Scan(&key, &digest); err != nil {
			rows.Close()
			return nil, err
		}
		if body, ok := projected[key]; ok && digest != metadataFingerprint([]byte(body)) {
			different[key] = true
		} else {
			delete(projected, key)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	for _, key := range keys {
		body, ok := projected[key]
		if !ok {
			continue
		}
		if different[key] {
			// Update existing documents in place to preserve InnoDB's
			// FULLTEXT entries. Keep the index column's comparison rules.
			err = writeSearchIndexRepair(ctx, tx, workspace, `UPDATE metadata_search_documents SET content=?
				WHERE workspace_key=? AND field=? AND record_key=? AND content<>?`, body, workspace, field, key, body)
		} else {
			err = writeSearchIndexRepair(ctx, tx, workspace, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content)
				VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`, workspace, field, key, body)
		}
		if err != nil {
			return nil, err
		}
	}
	return large, tx.Commit()
}

func writeSearchIndexRepair(ctx context.Context, tx *sqlTx, workspace, statement string, args ...any) error {
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil || rows > 0 {
		tx.queueCacheRevision(workspace, cacheKindSearchIndex)
	}
	return nil
}

// mysqlMetadataSearchPageContent is the MySQL text expression, over the
// records aliased m, of the document the application writes for each record
// of a page (pageArgs: workspace, field and the keys of where), with its
// arguments. MySQL renders a field value that is not a string (a project's
// status object, an array of domains, a number, a boolean) as its own JSON
// text, with spaces and its own key order, where the application writes Go's
// compact JSON; such values are read once per distinct value, formatted by
// metadataSearchPart and passed back, while strings stay in SQL. A string
// "null", which SQL reads as missing, is formatted the same way. A value
// that changed after it was read keeps MySQL's text, as do all values of a
// page whose reading the metadata work budget refuses; the document is then
// still complete, only written again on the next repair.
func (s *SQLiteStore) mysqlMetadataSearchPageContent(ctx context.Context, pageArgs []any, where string) (string, []any, func(), error) {
	texts := make([]string, len(metadataSearchTextFields))
	selections := make([]string, len(metadataSearchTextFields))
	var args []any
	for index, name := range metadataSearchTextFields {
		value := "JSON_EXTRACT(CONVERT(m.data USING utf8mb4),'$." + name + "')"
		texts[index] = "CAST(" + value + " AS CHAR CHARACTER SET utf8mb4) COLLATE utf8mb4_bin"
		selections[index] = "SELECT " + strconv.Itoa(index) + " part," + texts[index] + " value FROM workspace_metadata_records m WHERE workspace_key=? AND field=? AND " + where +
			" AND (JSON_TYPE(" + value + ") NOT IN ('STRING','NULL') OR JSON_TYPE(" + value + ")='STRING' AND JSON_UNQUOTE(" + value + ")=CAST('null' AS BINARY))"
		args = append(args, pageArgs...)
	}
	values := strings.Join(selections, " UNION ")
	plain := s.metadataSearchContent("m.data")
	var count, size int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(OCTET_LENGTH(value)),0) FROM (`+values+`) metadata_search_values`, args...).Scan(&count, &size); err != nil {
		return "", nil, nil, err
	}
	if count == 0 {
		return plain, nil, func() {}, nil
	}
	cost := int64(64<<10) + count*512 + size*4
	if size > int64(metadataWorkBytes()) || cost > int64(metadataWorkBytes()) {
		return plain, nil, func() {}, nil
	}
	readCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if errors.Is(err, ErrMetadataWorkBudget) {
		return plain, nil, func() {}, nil
	}
	if err != nil {
		return "", nil, nil, err
	}
	rows, err := s.db.QueryContext(readCtx, `SELECT part,value FROM (`+values+`) metadata_search_values`, args...)
	if err != nil {
		release()
		return "", nil, nil, err
	}
	defer rows.Close()
	cases := make([][]string, len(metadataSearchTextFields))
	caseArgs := make([][]any, len(metadataSearchTextFields))
	var read int64
	for rows.Next() {
		var part int
		var raw string
		if err := rows.Scan(&part, &raw); err != nil {
			release()
			return "", nil, nil, err
		}
		// A value written after the size was read waits for the next repair.
		if read += int64(len(raw)); read > size || part < 0 || part >= len(cases) {
			continue
		}
		var item any
		if json.Unmarshal([]byte(raw), &item) != nil {
			continue
		}
		text := ""
		if item != nil {
			text = metadataSearchPart(item)
		}
		cases[part] = append(cases[part], "WHEN CAST(? AS BINARY) THEN ?")
		caseArgs[part] = append(caseArgs[part], raw, text)
	}
	if err := rows.Err(); err != nil {
		release()
		return "", nil, nil, err
	}
	parts := make([]string, len(metadataSearchTextFields))
	var contentArgs []any
	for index, name := range metadataSearchTextFields {
		fallback := "COALESCE(" + s.jsonText("m.data", name) + ",'')"
		if len(cases[index]) == 0 {
			parts[index] = fallback
			continue
		}
		// A simple CASE extracts the value once per record; the searched form
		// (WHEN <value>=? ...) extracted it again for every WHEN, a page
		// parsing each record's JSON once per record of the page.
		parts[index] = "CASE " + texts[index] + " " + strings.Join(cases[index], " ") + " ELSE " + fallback + " END"
		contentArgs = append(contentArgs, caseArgs[index]...)
	}
	return s.searchCapExpression("CONCAT_WS(' '," + strings.Join(parts, ",") + ")"), contentArgs, release, nil
}

func (s *SQLiteStore) migrateMetadataSearchIndex(ctx context.Context) error {
	if s.BoundedMetadataEnabled() {
		return s.sw17MigrateMetadataSearchIndexBounded(ctx)
	}
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
			if current, err := s.metadataSearchFieldCurrent(ctx, workspace, kind.field, complete); err != nil {
				return err
			} else if current {
				continue
			}
			if complete != 0 {
				// Completed under other rules: reconcile every record again.
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
				if err := s.writeMetadataSearchPage(ctx, workspace, kind.field, ids); err != nil {
					return err
				}
				last = ids[len(ids)-1]
				if _, err = s.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET last_record_key=?,completed=0 WHERE workspace_key=? AND field=?`, last, workspace, kind.field); err != nil {
					return err
				}
			}
			// Remove rows left behind by records deleted before this repair ran.
			if err := s.pruneMetadataSearchDocuments(ctx, workspace, kind.field); err != nil {
				return err
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET completed=1 WHERE workspace_key=? AND field=?`, workspace, kind.field); err != nil {
				return err
			}
			if err := s.recordMetadataSearchRules(ctx, workspace, kind.field); err != nil {
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

func (s *SQLiteStore) pruneMetadataSearchDocuments(ctx context.Context, workspace, field string) error {
	recordKey := "metadata_search_documents.record_key"
	if s.dialect == "mysql" {
		recordKey = "CAST(" + recordKey + " AS BINARY)"
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field=?
		AND NOT EXISTS (SELECT 1 FROM workspace_metadata_records m WHERE m.workspace_key=? AND m.field=? AND m.record_key=`+
		recordKey+`)`, workspace, field, workspace, field)
	return err
}
