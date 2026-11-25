package store

import "context"

func (s *SQLiteStore) ensureSQLiteRecordJournalInline(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(record_journal_context)`)
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var position, required, primary int
		var name, kind string
		var fallback any
		if err := rows.Scan(&position, &name, &kind, &required, &fallback, &primary); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, column := range []string{"keys", "events"} {
		if !columns[column] {
			if _, err := s.db.ExecContext(ctx, `ALTER TABLE record_journal_context ADD COLUMN `+column+` BLOB NOT NULL DEFAULT '[]'`); err != nil {
				return err
			}
		}
	}
	for _, query := range []string{
		`CREATE TRIGGER IF NOT EXISTS flow_rj_context AFTER UPDATE OF tx_id ON record_journal_context BEGIN
			INSERT INTO record_journal_dirty(tx_id,workspace_key,field,record_key,source)
				SELECT OLD.tx_id,json_extract(k.value,'$.workspace'),json_extract(k.value,'$.field'),json_extract(k.value,'$.id'),json_extract(k.value,'$.source')
				FROM json_each(OLD.keys) k WHERE 1=1
				ON CONFLICT(tx_id,workspace_key,field,record_key) DO UPDATE SET source=excluded.source;
			INSERT INTO record_journal_event_pending(tx_id,event_id,workspace_id)
				SELECT OLD.tx_id,json_extract(e.value,'$.id'),json_extract(e.value,'$.workspaceId') FROM json_each(OLD.events) e WHERE 1=1
				ON CONFLICT(event_id) DO UPDATE SET tx_id=excluded.tx_id;
			UPDATE record_journal_context SET keys='[]',events='[]' WHERE id=1 AND (json_array_length(keys)>0 OR json_array_length(events)>0);
		END`,
	} {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func recordJournalInlineCondition(token string) string {
	return `(` + token + ` LIKE 'rjv2%' AND (SELECT json_array_length(keys)+json_array_length(events) FROM record_journal_context WHERE id=1)<16)`
}

func recordJournalInlineDirtySQL(token, workspace, field, id, source string) string {
	condition := recordJournalInlineCondition(token)
	return `INSERT INTO record_journal_dirty(tx_id,workspace_key,field,record_key,source) SELECT ` + token + `,` + workspace + `,` + field + `,` + id + `,'` + source + `' WHERE NOT ` + condition + ` ON CONFLICT(tx_id,workspace_key,field,record_key) DO UPDATE SET source=excluded.source;
		UPDATE record_journal_context SET keys=jsonb_insert(keys,'$[#]',jsonb_object('workspace',` + workspace + `,'field',` + field + `,'id',` + id + `,'source','` + source + `')) WHERE id=1 AND ` + condition + `;`
}

func recordJournalInlineEventSQL(token string) string {
	condition := recordJournalInlineCondition(token)
	return `INSERT INTO record_journal_event_pending(tx_id,event_id,workspace_id) SELECT ` + token + `,NEW.id,NEW.workspace_id WHERE NOT ` + condition + ` ON CONFLICT(event_id) DO UPDATE SET tx_id=excluded.tx_id;
		UPDATE record_journal_context SET events=jsonb_insert(events,'$[#]',jsonb_object('id',NEW.id,'workspaceId',NEW.workspace_id)) WHERE id=1 AND ` + condition + `;`
}

// This runs before the maintenance savepoint. A deferred-budget commit must
// retain the same durable coverage markers as an old writer. The context
// trigger also rescues an interrupted protocol participant before token reuse.
func (tx *sqlTx) materializeInlineJournal() error {
	if tx.dialect != "sqlite" {
		return nil
	}
	_, err := tx.Tx.ExecContext(tx.context(), `UPDATE record_journal_context SET tx_id=tx_id WHERE id=1`)
	return err
}

const recordJournalInlineCount = `(SELECT json_array_length(keys)+json_array_length(events) FROM record_journal_context WHERE id=1)`
