package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"flow/api/internal/idgen"
)

const recordJournalSchemaVersion = 29

// Triggers capture keys, never canonical bodies. The transaction token is
// connection-local on external databases. SQLite has one writer, so its
// token row is set with the first write and cleared before commit.
func (s *SQLiteStore) ensureRecordJournal(ctx context.Context) error {
	applied, err := s.migrationApplied(ctx, recordJournalSchemaVersion)
	if err != nil {
		return err
	}
	key, blob := "VARCHAR(191)", "BLOB"
	if s.dialect == "mysql" {
		key, blob = "VARBINARY(191)", "LONGBLOB"
	} else if s.dialect == "postgres" {
		key, blob = `VARCHAR(191) COLLATE "C"`, "BYTEA"
	}
	recordKey := strings.Replace(key, "191", "2304", 1)
	statements := []string{
		`CREATE TABLE IF NOT EXISTS record_journal_mode(id INTEGER PRIMARY KEY,enabled INTEGER NOT NULL,capturing INTEGER NOT NULL,generation VARCHAR(64) NOT NULL)`,
		`INSERT INTO record_journal_mode(id,enabled,capturing,generation) SELECT 1,0,CASE WHEN EXISTS(SELECT 1 FROM schema_migrations WHERE version=26) THEN 1 ELSE 0 END,'' WHERE 1=1 ON CONFLICT(id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS record_journal_coverage(workspace_key ` + key + ` PRIMARY KEY,phase INTEGER NOT NULL,last_field ` + key + ` NOT NULL,last_key ` + recordKey + ` NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS record_journal_context(id INTEGER PRIMARY KEY,tx_id VARCHAR(64) NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS record_journal_active(tx_id VARCHAR(64) PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS record_versions(workspace_key ` + key + ` NOT NULL,field ` + key + ` NOT NULL,record_key ` + recordKey + ` NOT NULL,version BIGINT NOT NULL,deleted INTEGER NOT NULL,PRIMARY KEY(workspace_key,field,record_key))`,
		`CREATE TABLE IF NOT EXISTS record_journal_dirty(tx_id VARCHAR(64) NOT NULL,workspace_key ` + key + ` NOT NULL,field ` + key + ` NOT NULL,record_key ` + recordKey + ` NOT NULL,source VARCHAR(40) NOT NULL,PRIMARY KEY(tx_id,workspace_key,field,record_key))`,
		`CREATE TABLE IF NOT EXISTS workspace_authz_epochs(workspace_id VARCHAR(191) PRIMARY KEY,epoch BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS workspace_record_journal_heads(workspace_id VARCHAR(191) PRIMARY KEY,head BIGINT NOT NULL,pruned BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS record_journal(id VARCHAR(64) PRIMARY KEY,workspace_id VARCHAR(191) NOT NULL,seq BIGINT,authz_epoch VARCHAR(64) NOT NULL,payload ` + blob + ` NOT NULL,created_at VARCHAR(40) NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS record_journal_events(event_id VARCHAR(191) PRIMARY KEY,journal_id VARCHAR(64) NOT NULL,workspace_id VARCHAR(191) NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS record_journal_event_pending(tx_id VARCHAR(64) NOT NULL,event_id VARCHAR(191) PRIMARY KEY,workspace_id VARCHAR(191) NOT NULL)`,
		`INSERT INTO record_journal_context(id,tx_id) VALUES(1,'') ON CONFLICT(id) DO NOTHING`,
	}
	if s.dialect == "sqlite" {
		// These sidecars are addressed only by their declared keys. Avoid a
		// second rowid B-tree and its WAL frames for every journal insertion.
		for i, statement := range statements {
			if strings.HasPrefix(statement, "CREATE TABLE") && !strings.Contains(statement, "id INTEGER PRIMARY KEY") {
				statements[i] += " WITHOUT ROWID"
			}
		}
	}
	for _, index := range []string{
		`record_journal_workspace_idx ON record_journal(workspace_id,seq)`,
		`record_journal_events_tx_idx ON record_journal_events(journal_id)`,
		`record_journal_pending_tx_idx ON record_journal_event_pending(tx_id)`,
		`record_journal_pending_workspace_idx ON record_journal_event_pending(workspace_id)`,
		`record_journal_dirty_workspace_idx ON record_journal_dirty(workspace_key,tx_id)`,
	} {
		prefix := "CREATE INDEX IF NOT EXISTS "
		if s.dialect == "mysql" {
			prefix = "CREATE INDEX "
		}
		statements = append(statements, prefix+index)
	}
	if err := s.applyMigrationStatements(ctx, statements); err != nil {
		return fmt.Errorf("record journal schema: %w", err)
	}
	if !applied && s.dialect != "sqlite" {
		if err := s.widenRecordJournalKeys(ctx); err != nil {
			return err
		}
	}
	var enabled, capturing int
	var generation string
	if err := s.db.QueryRowContext(ctx, `SELECT enabled,capturing,generation FROM record_journal_mode WHERE id=1`).Scan(&enabled, &capturing, &generation); err != nil {
		return err
	}
	// Off removes canonical-table triggers. Disable authority before DDL so
	// enabled peers fail closed until the next bounded repair completes.
	if recordJournalMode() == "off" {
		if _, err := s.db.ExecContext(ctx, `UPDATE record_journal_mode SET enabled=0 WHERE id=1`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE record_journal_context SET tx_id='' WHERE id=1`); err != nil {
			return err
		}
		if err := s.dropRecordJournalTriggers(ctx, capturing != 0); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE record_journal_mode SET capturing=0 WHERE id=1`); err != nil {
			return err
		}
		return s.recordJournalMigration(ctx)
	}
	if !applied {
		if _, err := s.db.ExecContext(ctx, `UPDATE record_journal_mode SET enabled=0 WHERE id=1`); err != nil {
			return err
		}
		enabled = 0
		if err := s.dropRecordJournalTriggers(ctx, capturing != 0); err != nil {
			return err
		}
	}
	// Persist intent before DDL, including a crash partway through MySQL's
	// nontransactional trigger installation. A restricted account cannot
	// mistake invisible information_schema rows for confirmed absence.
	if _, err := s.db.ExecContext(ctx, `UPDATE record_journal_mode SET capturing=1 WHERE id=1`); err != nil {
		return err
	}
	if s.dialect == "sqlite" {
		if err := s.ensureSQLiteRecordJournalInline(ctx); err != nil {
			return err
		}
	}
	for index, spec := range recordJournalTriggerSpecs(s.dialect) {
		if err := s.installRecordJournalTrigger(ctx, spec); err != nil {
			// Restricted MySQL accounts may lack CREATE TRIGGER. Existing
			// business writes still work in shadow; journal consumers fail
			// closed and ResolveAccess keeps its live-policy compatibility path.
			if s.dialect == "mysql" && isMySQLSearchPermissionError(err) && recordJournalMode() != "on" {
				if capturing == 0 && index == 0 {
					if _, resetErr := s.db.ExecContext(ctx, `UPDATE record_journal_mode SET capturing=0 WHERE id=1`); resetErr != nil {
						return resetErr
					}
				}
				slog.Warn("record journal unavailable: trigger permission required", "error", err)
				return nil
			}
			return fmt.Errorf("record journal trigger %s: %w", spec.name, err)
		}
	}
	if s.dialect == "sqlite" {
		if err := s.ensureSQLiteRecordJournalFlush(ctx); err != nil {
			return err
		}
	}
	if enabled == 0 {
		generation = idgen.New("rjg")
		// Startup does not scan bodies. Existing workspaces stay unavailable
		// until their versions and summaries are repaired in bounded batches.
		if _, err := s.db.ExecContext(ctx, `INSERT INTO record_journal_coverage(workspace_key,phase,last_field,last_key) SELECT workspace_key,0,'','' FROM workspace_states WHERE 1=1 ON CONFLICT(workspace_key) DO UPDATE SET phase=0,last_field='',last_key=''`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE metadata_catalog_collections SET last_record_key='',started=0,phase=0,omissions=0,digest='',revision=''`); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE record_journal_mode SET enabled=1,generation=? WHERE id=1`, generation); err != nil {
			return err
		}
	}
	if !applied {
		// One marker per legacy workspace prevents claiming historical
		// coverage. New/old replica writes are captured by the triggers above.
		if _, err := s.db.ExecContext(ctx, `INSERT INTO record_journal_event_pending(tx_id,event_id,workspace_id) SELECT '',MIN(id),workspace_id FROM domain_events WHERE workspace_id IS NOT NULL AND workspace_id<>'' GROUP BY workspace_id ON CONFLICT(event_id) DO NOTHING`); err != nil {
			return err
		}
	}
	if err := s.recordJournalMigration(ctx); err != nil {
		return err
	}
	s.db.journalReady.Store(true)
	return nil
}

func (s *SQLiteStore) recordJournalMigration(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?) ON CONFLICT(version) DO NOTHING`, 26, "transaction record journal and authorization epochs", time.Now().UTC().Format(time.RFC3339Nano))
	if err == nil {
		_, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?) ON CONFLICT(version) DO NOTHING`, 27, "record journal activation and bounded commit maintenance", time.Now().UTC().Format(time.RFC3339Nano))
	}
	if err == nil {
		_, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?) ON CONFLICT(version) DO NOTHING`, 28, "bounded SQLite journal transaction input", time.Now().UTC().Format(time.RFC3339Nano))
	}
	if err == nil {
		_, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?) ON CONFLICT(version) DO NOTHING`, recordJournalSchemaVersion, "record journal identity comparisons across MySQL collations", time.Now().UTC().Format(time.RFC3339Nano))
	}
	return err
}

func (s *SQLiteStore) dropRecordJournalTriggers(ctx context.Context, installed bool) error {
	for _, spec := range recordJournalTriggerSpecs(s.dialect) {
		if s.dialect == "mysql" {
			var count int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name=?`, spec.name).Scan(&count); err != nil {
				return err
			}
			if count == 0 && !installed {
				continue
			}
		}
		query := `DROP TRIGGER IF EXISTS ` + spec.name
		if s.dialect == "postgres" {
			query += ` ON ` + spec.table
		}
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

type recordJournalTrigger struct {
	name, table, event, condition, body string
}

// Keep the registered tables explicit: the coverage test enumerates every
// writer of these tables, including dynamic erasure/purge/rename statements.
var recordJournalTables = map[string]bool{
	"workspace_metadata_records": true,
	"workspace_states":           true,
	"issue_records":              true,
	"issue_permission_records":   true,
	"workspace_content_records":  true,
	"workspace_memberships":      true,
	"team_memberships":           true,
	"auth_users":                 true,
	"domain_events":              true,
}

func recordJournalTriggerSpecs(dialect string) []recordJournalTrigger {
	token := `(SELECT tx_id FROM record_journal_context WHERE id=1)`
	if dialect == "mysql" {
		token = `COALESCE(CAST(@flow_record_tx AS BINARY),'')`
	} else if dialect == "postgres" {
		token = `COALESCE(current_setting('flow.record_tx',true),'')`
	}
	upsert := func(table, columns, values, conflict, update string) string {
		q := `INSERT INTO ` + table + `(` + columns + `) VALUES(` + values + `) ON CONFLICT(` + conflict + `) DO UPDATE SET ` + update
		return rewriteSQL(q, dialect) + ";"
	}
	epoch := func(workspaceID string) string {
		return rewriteSQL(`INSERT INTO workspace_authz_epochs(workspace_id,epoch) SELECT `+workspaceID+`,1 WHERE `+workspaceID+`<>'' ON CONFLICT(workspace_id) DO UPDATE SET epoch=workspace_authz_epochs.epoch+1`, dialect) + ";"
	}
	dirty := func(workspace, field, id, source string) string {
		if dialect == "sqlite" {
			return recordJournalInlineDirtySQL(token, workspace, field, id, source)
		}
		return upsert("record_journal_dirty", "tx_id,workspace_key,field,record_key,source", token+","+workspace+","+field+","+id+",'"+source+"'", "tx_id,workspace_key,field,record_key", "source=excluded.source")
	}
	version := func(workspace, field, id, deleted string) string {
		return upsert("record_versions", "workspace_key,field,record_key,version,deleted", workspace+","+field+","+id+",1,"+deleted, "workspace_key,field,record_key", "version=record_versions.version+1,deleted=excluded.deleted")
	}
	workspaceID := func(workspace string) string {
		// Canonical records can precede the root on workspace creation.
		// The root trigger supplies that epoch once the ID exists.
		return `COALESCE((SELECT workspace_id FROM workspace_states WHERE workspace_key=` + workspace + `),'')`
	}
	join := func(a, b string) string {
		return recordJournalContentKeySQL(dialect, a, b)
	}
	var specs []recordJournalTrigger
	for _, table := range []string{"workspace_metadata_records", "workspace_states", "issue_records", "issue_permission_records", "workspace_content_records", "workspace_memberships", "team_memberships", "auth_users", "domain_events"} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			row, deleted := "NEW", "0"
			if event == "DELETE" {
				row, deleted = "OLD", "1"
			}
			spec := recordJournalTrigger{name: "flow_rj_" + table + "_" + strings.ToLower(event), table: table, event: event}
			workspace, field, id := row+".workspace_key", "", ""
			switch table {
			case "domain_events":
				if event != "INSERT" {
					continue
				}
				spec.condition = `NEW.workspace_id IS NOT NULL AND NEW.workspace_id<>''`
				spec.body = upsert("record_journal_event_pending", "tx_id,event_id,workspace_id", token+",NEW.id,NEW.workspace_id", "event_id", "tx_id=excluded.tx_id")
				if dialect == "sqlite" {
					spec.body = recordJournalInlineEventSQL(token)
				}
			case "auth_users":
				if event == "INSERT" {
					continue
				}
				if event == "UPDATE" {
					spec.condition = `NEW.active<>OLD.active OR COALESCE(NEW.deleted_at,'')<>COALESCE(OLD.deleted_at,'')`
				}
				// Foreign-key cascades do not fire child triggers in MySQL.
				// A BEFORE trigger captures the memberships before deletion.
				q := `INSERT INTO workspace_authz_epochs(workspace_id,epoch) SELECT workspace_id,1 FROM workspace_memberships WHERE user_id=` + row + `.id AND 1=1 ON CONFLICT(workspace_id) DO UPDATE SET epoch=workspace_authz_epochs.epoch+1`
				spec.body = rewriteSQL(q, dialect) + ";"
				q = `INSERT INTO record_journal_dirty(tx_id,workspace_key,field,record_key,source) SELECT ` + token + `,w.workspace_key,'@authz',` + row + `.id,'auth_users' FROM workspace_states w JOIN workspace_memberships m ON m.workspace_id=w.workspace_id WHERE m.user_id=` + row + `.id AND 1=1 ON CONFLICT(tx_id,workspace_key,field,record_key) DO UPDATE SET source=excluded.source`
				spec.body += rewriteSQL(q, dialect) + ";"
			case "workspace_memberships", "team_memberships":
				workspace = `COALESCE((SELECT workspace_key FROM workspace_states WHERE workspace_id=` + row + `.workspace_id),'')`
				if event == "UPDATE" {
					spec.condition = `NEW.role<>OLD.role`
					if table == "workspace_memberships" {
						spec.condition += ` OR NEW.status<>OLD.status`
					} else {
						spec.condition += ` OR NEW.origin<>OLD.origin`
					}
				}
				spec.body = epoch(row+".workspace_id") + dirty(workspace, "'@authz'", row+".user_id", table)
			case "issue_permission_records":
				spec.body = epoch(workspaceID(workspace)) + dirty(workspace, "'@authz'", row+".issue_id", table)
			default:
				switch table {
				case "workspace_metadata_records":
					field, id = row+".field", row+".record_key"
				case "workspace_states":
					field, id = "'@core'", "'root'"
				case "issue_records":
					field, id = "'issues'", row+".id"
				case "workspace_content_records":
					field = `CASE ` + row + `.kind WHEN 'comment' THEN 'comments' WHEN 'activity' THEN 'activities' WHEN 'notification' THEN 'notifications' WHEN 'delivery' THEN 'notificationDeliveries' ELSE ` + row + `.kind END`
					id = row + ".id"
					// A content ID is scoped by its parent in canonical SQL.
					id = join(row+".resource_id", id)
				}
				if event == "UPDATE" {
					spec.condition = `NEW.data<>OLD.data`
					if table == "workspace_metadata_records" {
						spec.condition += ` OR NEW.collection_order<>OLD.collection_order OR NEW.workspace_key<>OLD.workspace_key`
					} else if table == "issue_records" {
						spec.condition += ` OR NEW.collection_order<>OLD.collection_order OR NEW.workspace_key<>OLD.workspace_key`
					} else if table == "workspace_content_records" {
						spec.condition += ` OR NEW.status<>OLD.status OR NEW.next_attempt_at<>OLD.next_attempt_at OR NEW.owner_id<>OLD.owner_id OR NEW.record_version<>OLD.record_version OR NEW.workspace_key<>OLD.workspace_key`
					}
				}
				spec.body = version(workspace, field, id, deleted) + dirty(workspace, field, id, table)
				if table == "workspace_metadata_records" || table == "workspace_states" || table == "issue_records" || table == "workspace_content_records" {
					// Old/direct writers do not queue cache revisions. Without
					// this, a new epoch could be paired with stale installed policy.
					untracked := token + `=''`
					if dialect == "mysql" {
						// Session variables survive a canceled/rolled-back pooled
						// transaction. Only a transactional marker proves that
						// its token belongs to an active application writer.
						untracked += ` OR NOT EXISTS (SELECT 1 FROM record_journal_active WHERE tx_id=` + token + `)`
					}
					kind := "metadata"
					if table == "issue_records" || table == "workspace_content_records" {
						kind = "issues"
					}
					bump := rewriteSQL(`INSERT INTO workspace_cache_revisions(workspace_key,kind,shard,revision) SELECT `+workspace+`,'`+kind+`',0,1 WHERE `+untracked+` ON CONFLICT(workspace_key,kind,shard) DO UPDATE SET revision=workspace_cache_revisions.revision+1`, dialect) + ";"
					spec.body += bump
				}
				if table == "workspace_states" {
					spec.body += epoch(row + ".workspace_id")
					if event == "DELETE" {
						// The ID owns the epoch across a rename. Do not recreate
						// a version under the retired URL key after it was moved.
						spec.body = epoch(row+".workspace_id") + dirty(workspace, "'@authz'", "'root'", table)
					}
				} else if table == "workspace_metadata_records" {
					// Changes to policy-bearing records conservatively revoke
					// cached contexts. Other records do not advance the epoch.
					condition := row + `.field IN ('teams','teamSettings','documents','projects','initiatives','apiKeys','oauthApplications','oauthAuthorizations','users','workspaceSettings','settings')`
					if event == "UPDATE" {
						without := func(row, field string) string { return `json_remove(` + row + `.data,'$.` + field + `')` }
						if dialect == "postgres" {
							without = func(row, field string) string {
								return `(convert_from(` + row + `.data,'UTF8')::jsonb - '` + field + `')`
							}
						} else if dialect == "mysql" {
							without = func(row, field string) string {
								return `CAST(JSON_REMOVE(CONVERT(` + row + `.data USING utf8mb4),'$.` + field + `') AS BINARY)`
							}
						}
						condition += ` AND (` + row + `.field NOT IN ('apiKeys','oauthAuthorizations') OR ` + without("NEW", "lastUsedAt") + `<>` + without("OLD", "lastUsedAt") + `)`
						condition += ` AND (` + row + `.field<>'teamSettings' OR ` + without("NEW", "timezone") + `<>` + without("OLD", "timezone") + `)`
					}
					if dialect == "sqlite" {
						spec.body += `INSERT INTO workspace_authz_epochs(workspace_id,epoch) SELECT ` + workspaceID(workspace) + `,1 WHERE ` + condition + ` ON CONFLICT(workspace_id) DO UPDATE SET epoch=workspace_authz_epochs.epoch+1;`
					} else {
						spec.body += `IF ` + condition + ` THEN ` + epoch(workspaceID(workspace)) + ` END IF;`
					}
				}
			}
			if event == "DELETE" && table != "auth_users" && table != "workspace_states" {
				condition := `EXISTS (SELECT 1 FROM workspace_states WHERE workspace_key=` + workspace + `)`
				if table == "workspace_metadata_records" || table == "issue_records" || table == "workspace_content_records" {
					// A rename moves versions before canonical rows and writes
					// its new root last. Deletions in that interval still need
					// tombstones. A purge has already removed these versions.
					condition = `(` + condition + ` OR EXISTS (SELECT 1 FROM record_versions WHERE workspace_key=` + workspace + ` AND field=` + field + ` AND record_key=` + id + `))`
				}
				if spec.condition != "" {
					condition += " AND (" + spec.condition + ")"
				}
				spec.condition = condition
			}
			specs = append(specs, spec)
		}
	}
	return specs
}

func (s *SQLiteStore) installRecordJournalTrigger(ctx context.Context, spec recordJournalTrigger) error {
	timing := "AFTER"
	if spec.table == "auth_users" && spec.event == "DELETE" {
		timing = "BEFORE"
	}
	var query string
	switch s.dialect {
	case "sqlite":
		when := ""
		if spec.condition != "" {
			when = " WHEN " + spec.condition
		}
		query = `CREATE TRIGGER IF NOT EXISTS ` + spec.name + ` ` + timing + ` ` + spec.event + ` ON ` + spec.table + when + ` BEGIN ` + spec.body + ` END`
	case "mysql":
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name=?`, spec.name).Scan(&count); err != nil || count > 0 {
			return err
		}
		body := spec.body
		if spec.condition != "" {
			body = "IF " + spec.condition + " THEN " + body + " END IF;"
		}
		query = `CREATE TRIGGER ` + spec.name + ` ` + timing + ` ` + spec.event + ` ON ` + spec.table + ` FOR EACH ROW BEGIN ` + body + ` END`
	case "postgres":
		body := spec.body
		if spec.condition != "" {
			body = "IF " + spec.condition + " THEN " + body + " END IF;"
		}
		returned := "NULL"
		if timing == "BEFORE" {
			returned = "OLD"
		}
		if _, err := s.db.ExecContext(ctx, `CREATE OR REPLACE FUNCTION `+spec.name+`() RETURNS TRIGGER AS $$ BEGIN `+body+` RETURN `+returned+`; END; $$ LANGUAGE plpgsql`); err != nil {
			return err
		}
		query = `CREATE OR REPLACE TRIGGER ` + spec.name + ` ` + timing + ` ` + spec.event + ` ON ` + spec.table + ` FOR EACH ROW EXECUTE FUNCTION ` + spec.name + `()`
	}
	_, err := s.db.ExecContext(ctx, query)
	return err
}
