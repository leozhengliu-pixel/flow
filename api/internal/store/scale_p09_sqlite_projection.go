package store

import "context"

// Dispatch by projection state before opening any catalog indexes. Most small
// edits leave the allowlisted summary unchanged and need only its fingerprint.
func (s *SQLiteStore) ensureSQLiteRecordJournalProjection(ctx context.Context) error {
	queries := []string{
		`CREATE VIEW IF NOT EXISTS record_journal_projection_v3 AS SELECT CAST(NULL AS TEXT) AS workspace_key,CAST(NULL AS TEXT) AS field,CAST(NULL AS TEXT) AS record_key,CAST(NULL AS BLOB) AS data WHERE 0`,
		`CREATE TRIGGER IF NOT EXISTS flow_rj_projection_same_v3 INSTEAD OF INSERT ON record_journal_projection_v3
			WHEN NOT json_extract(NEW.data,'$.Deleted') AND NOT json_extract(NEW.data,'$.Rewrite') BEGIN
			UPDATE metadata_catalog SET version=json_extract(NEW.data,'$.Version'),body_bytes=json_extract(NEW.data,'$.Bytes')
				WHERE workspace_key=NEW.workspace_key AND field=NEW.field AND record_key=NEW.record_key;
		END`,
		`CREATE TRIGGER IF NOT EXISTS flow_rj_projection_delete_v3 INSTEAD OF INSERT ON record_journal_projection_v3
			WHEN json_extract(NEW.data,'$.Deleted') BEGIN
			DELETE FROM metadata_catalog_relations WHERE workspace_key=NEW.workspace_key AND field=NEW.field AND record_key=NEW.record_key;
			DELETE FROM metadata_catalog WHERE workspace_key=NEW.workspace_key AND field=NEW.field AND record_key=NEW.record_key;
		END`,
		`CREATE TRIGGER IF NOT EXISTS flow_rj_projection_write_v3 INSTEAD OF INSERT ON record_journal_projection_v3
			WHEN NOT json_extract(NEW.data,'$.Deleted') AND json_extract(NEW.data,'$.Rewrite') BEGIN
			DELETE FROM metadata_catalog_relations WHERE workspace_key=NEW.workspace_key AND field=NEW.field AND record_key=NEW.record_key;
			INSERT INTO metadata_catalog(workspace_key,field,record_key,collection_order,version,body_bytes,summary,omitted,created_at,updated_at,archived)
				VALUES(NEW.workspace_key,NEW.field,NEW.record_key,json_extract(NEW.data,'$.Order'),json_extract(NEW.data,'$.Version'),json_extract(NEW.data,'$.Bytes'),
					json_extract(NEW.data,'$.Summary'),json_extract(NEW.data,'$.Omitted'),json_extract(NEW.data,'$.Created'),json_extract(NEW.data,'$.Updated'),json_extract(NEW.data,'$.Archived'))
				ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,version=excluded.version,body_bytes=excluded.body_bytes,summary=excluded.summary,omitted=excluded.omitted,created_at=excluded.created_at,updated_at=excluded.updated_at,archived=excluded.archived;
			INSERT INTO metadata_catalog_relations(workspace_key,field,record_key,relation,value_hash,value)
				SELECT NEW.workspace_key,NEW.field,NEW.record_key,json_extract(r.value,'$.Name'),json_extract(r.value,'$.Hash'),json_extract(r.value,'$.Value')
				FROM json_each(NEW.data,'$.Relations') r WHERE 1=1 ON CONFLICT(workspace_key,field,record_key,relation,value_hash) DO NOTHING;
		END`,
	}
	for _, query := range queries {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}
