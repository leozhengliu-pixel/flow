package store

import (
	"context"
	"database/sql"
)

func (s *SQLiteStore) sw17SearchByteLength(column string) string {
	if s.dialect == "sqlite" {
		return "LENGTH(CAST(" + column + " AS BLOB))"
	}
	return "OCTET_LENGTH(" + column + ")"
}

func (s *SQLiteStore) sw17SearchCheckpoint(ctx context.Context, workspace, field string) (string, int, func(), error) {
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return "", 0, nil, err
	}
	defer tx.Rollback()
	var size int64
	var complete int
	if err := tx.QueryRowContext(ctx, `SELECT `+s.sw17SearchByteLength("last_record_key")+`,completed FROM metadata_search_migration_checkpoints WHERE workspace_key=? AND field=?`, workspace, field).Scan(&size, &complete); err != nil {
		return "", 0, nil, err
	}
	if size < 0 || size > (metadataWorkBytes-(64<<10))/3 {
		return "", 0, nil, ErrMetadataWorkBudget
	}
	readCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(size)*3+64<<10)
	if err != nil {
		return "", 0, nil, err
	}
	var last string
	if err := tx.QueryRowContext(readCtx, `SELECT last_record_key FROM metadata_search_migration_checkpoints WHERE workspace_key=? AND field=?`, workspace, field).Scan(&last); err != nil {
		release()
		return "", 0, nil, err
	}
	return last, complete, release, nil
}

func (s *SQLiteStore) sw17SearchKeyPage(ctx context.Context, workspace, field, last string) ([]string, func(), error) {
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	page := ` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key>? ORDER BY record_key LIMIT 128`
	args := []any{workspace, field, last}
	var count, bytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(key_bytes),0) FROM (SELECT `+s.sw17SearchByteLength("record_key")+` key_bytes`+page+`) sw17_search_keys`, args...).Scan(&count, &bytes); err != nil {
		return nil, nil, err
	}
	if count < 0 || count > 128 || bytes < 0 || bytes > metadataWorkBytes/4 {
		return nil, nil, ErrMetadataWorkBudget
	}
	cost := bytes*4 + count*128 + 64<<10
	if cost > metadataWorkBytes {
		return nil, nil, ErrMetadataWorkBudget
	}
	readCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.QueryContext(readCtx, `SELECT record_key`+page, args...)
	if err != nil {
		release()
		return nil, nil, err
	}
	keys := make([]string, 0, int(count))
	var actualBytes int64
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			release()
			return nil, nil, err
		}
		actualBytes += int64(len(key))
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(keys) != int(count) || actualBytes != bytes {
		release()
		if err == nil {
			err = ErrConcurrentWrite
		}
		return nil, nil, err
	}
	return keys, release, nil
}

func (s *SQLiteStore) sw17MigrateMetadataSearchIndexBounded(ctx context.Context) error {
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	for workspace := range s.workspaces {
		for _, kind := range searchMetadataKinds {
			if err := s.sw17SearchFieldBounded(ctx, workspace, kind.field); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteStore) sw17SearchFieldBounded(ctx context.Context, workspace, field string) error {
	last, complete, releaseCheckpoint, err := s.sw17SearchCheckpoint(ctx, workspace, field)
	if err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO metadata_search_migration_checkpoints(workspace_key,field,last_record_key,completed) VALUES(?,?,?,0)`, workspace, field, ""); err != nil {
			return err
		}
		last, releaseCheckpoint = "", func() {}
	}
	defer func() { releaseCheckpoint() }()
	if complete != 0 {
		if _, err := s.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET last_record_key='',completed=0 WHERE workspace_key=? AND field=?`, workspace, field); err != nil {
			return err
		}
		releaseCheckpoint()
		last, releaseCheckpoint = "", func() {}
	}
	for {
		keys, releasePage, err := s.sw17SearchKeyPage(ctx, workspace, field, last)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			releasePage()
			break
		}
		err = func() error {
			defer releasePage()
			next := keys[len(keys)-1]
			// The new cursor survives the page through the next header query;
			// charge it separately before publishing any index changes.
			_, releaseNext, err := s.tryAdmitAdditionalMetadataWork(ctx, len(next)*3+64<<10)
			if err != nil {
				return err
			}
			installed := false
			defer func() {
				if !installed {
					releaseNext()
				}
			}()
			where, args := bindList("record_key", keys)
			args = append([]any{workspace, field}, args...)
			upsert := ` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=excluded.content WHERE metadata_search_documents.content<>excluded.content`
			if s.dialect == "mysql" {
				upsert = ` ON DUPLICATE KEY UPDATE content=IF(content<>VALUES(content),VALUES(content),content)`
			}
			if s.dialect == "postgres" {
				upsert = ` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET content=EXCLUDED.content WHERE metadata_search_documents.content<>EXCLUDED.content`
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO metadata_search_documents(workspace_key,field,record_key,content) SELECT workspace_key,field,record_key,`+s.metadataSearchContent("data")+` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+where+upsert, args...); err != nil {
				return err
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET last_record_key=?,completed=0 WHERE workspace_key=? AND field=?`, next, workspace, field); err != nil {
				return err
			}
			releaseCheckpoint()
			last, releaseCheckpoint = next, releaseNext
			installed = true
			return nil
		}()
		if err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM metadata_search_documents WHERE workspace_key=? AND field=? AND NOT EXISTS (SELECT 1 FROM workspace_metadata_records m WHERE m.workspace_key=metadata_search_documents.workspace_key AND m.field=metadata_search_documents.field AND m.record_key=metadata_search_documents.record_key)`, workspace, field); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE metadata_search_migration_checkpoints SET completed=1 WHERE workspace_key=? AND field=?`, workspace, field); err != nil {
		return err
	}
	insert := `INSERT INTO metadata_search_migrations(workspace_key,field) VALUES(?,?) ON CONFLICT DO NOTHING`
	if s.dialect == "mysql" {
		insert = `INSERT IGNORE INTO metadata_search_migrations(workspace_key,field) VALUES(?,?)`
	}
	_, err = s.db.ExecContext(ctx, insert, workspace, field)
	return err
}
