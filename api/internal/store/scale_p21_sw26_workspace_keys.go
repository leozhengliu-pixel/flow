package store

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

func sw26ShortJSONString(ctx context.Context, source io.ReaderAt, part metadataCollectionRange) (string, error) {
	var text strings.Builder
	long := false
	_, err := VisitMetadataJSONStringChunks(ctx, io.NewSectionReader(source, part.offset, part.size), func(chunk []byte) error {
		if !long {
			if len(chunk) > 191-text.Len() {
				long = true
			} else {
				text.Write(chunk)
			}
		}
		return nil
	})
	if long {
		return strings.Repeat("x", 192), err
	}
	return text.String(), err
}

func sw26EventURLKey(ctx context.Context, source *sw26EventReaderAt) (string, error) {
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(io.NewSectionReader(source, 0, source.size), metadataRecordChunkBytes)}
	if err := scanner.spaces(); err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	kind, err := scanner.peek()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil
		}
		return "", err
	}
	key := ""
	if kind != '{' {
		_, err = scanner.value(0)
	} else {
		err = scanner.object(func(name, value metadataCollectionRange) error {
			field, err := sw26ShortJSONString(ctx, source, name)
			if err != nil || !strings.EqualFold(field, "urlKey") {
				return err
			}
			var head [1]byte
			if _, err := source.ReadAt(head[:], value.offset); err != nil {
				return err
			}
			// encoding/json leaves a string unchanged on null or a type
			// error; its caller deliberately ignores such type errors.
			if head[0] == '"' {
				key, err = sw26ShortJSONString(ctx, source, value)
			}
			return err
		}, 0)
	}
	if err == nil {
		err = scanner.end()
	}
	if errors.Is(err, errMetadataCollectionJSON) || errors.Is(err, ErrMetadataJSONString) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return key, nil
}

func (s *SQLiteStore) seedWorkspaceKeyRenamesBounded(ctx context.Context) (recorded int, err error) {
	ctx, release, err := s.admitMetadataWork(ctx, 8<<20)
	if err != nil {
		return 0, err
	}
	defer release()
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	for _, query := range []string{
		`CREATE TABLE live(id TEXT PRIMARY KEY COLLATE BINARY,key TEXT NOT NULL) WITHOUT ROWID`,
		`CREATE TABLE pairs(id TEXT COLLATE BINARY,key TEXT COLLATE BINARY,at TEXT NOT NULL,PRIMARY KEY(id,key)) WITHOUT ROWID`,
	} {
		if _, err := stage.exec(ctx, 0, query); err != nil {
			return 0, err
		}
	}
	// Preserve the legacy map iteration's final value for imported duplicate
	// workspace IDs, without retaining any complete Bootstrap collection.
	s.mu.RLock()
	for key, data := range s.workspaces {
		bytes := int64(len(key) + len(data.Workspace.ID))
		if bytes > MetadataRecordPageMaxBytes {
			s.mu.RUnlock()
			return 0, ErrMetadataWorkBudget
		}
		if _, err := stage.exec(ctx, bytes, `INSERT INTO live(id,key) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET key=excluded.key`, data.Workspace.ID, key); err != nil {
			s.mu.RUnlock()
			return 0, err
		}
	}
	s.mu.RUnlock()
	last := ""
	for {
		count, next, err := s.sw26StageRenameEventPage(ctx, stage, last)
		if err != nil {
			return 0, err
		}
		last = next
		if count < 500 {
			break
		}
	}
	return s.sw26ApplyRenamePairs(ctx, stage)
}

func (s *SQLiteStore) sw26ApplyRenamePairs(ctx context.Context, stage *sw26MigrationStage) (int, error) {
	recorded, lastID, lastKey, started := 0, "", "", false
	for {
		query, args := `SELECT p.id,p.key,p.at FROM pairs p`, []any{}
		if started {
			query += ` WHERE p.id>? OR (p.id=? AND p.key>?)`
			args = append(args, lastID, lastID, lastKey)
		}
		rows, err := stage.db.QueryContext(ctx, query+` ORDER BY p.id,p.key LIMIT 64`, args...)
		if err != nil {
			return recorded, err
		}
		type pair struct{ id, key, at string }
		page := make([]pair, 0, 64)
		for rows.Next() {
			var item pair
			if err := rows.Scan(&item.id, &item.key, &item.at); err != nil {
				rows.Close()
				return recorded, err
			}
			page = append(page, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return recorded, err
		}
		for _, item := range page {
			var current string
			err := stage.db.QueryRowContext(ctx, `SELECT key FROM live WHERE id=?`, item.id).Scan(&current)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return recorded, err
			}
			if current == item.key {
				continue
			}
			at := item.at
			if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
				at = time.Now().UTC().Format(time.RFC3339Nano)
			}
			result, err := s.db.ExecContext(ctx, `INSERT INTO workspace_key_renames(workspace_id,old_key,new_key,renamed_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, item.id, item.key, current, at)
			if err != nil {
				return recorded, err
			}
			recorded += int(rowsAffected(result))
		}
		if len(page) < 64 {
			return recorded, nil
		}
		item := page[len(page)-1]
		lastID, lastKey, started = item.id, item.key, true
	}
}

func (s *SQLiteStore) sw26StageRenameEventPage(ctx context.Context, stage *sw26MigrationStage, last string) (int, string, error) {
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return 0, last, err
	}
	defer tx.Rollback()
	bytes := func(column string) string {
		if s.dialect == "sqlite" {
			return "LENGTH(CAST(" + column + " AS BLOB))"
		}
		if s.dialect == "postgres" && column != "payload" {
			return "OCTET_LENGTH(" + column + ")"
		}
		return "LENGTH(" + column + ")"
	}
	short := func(column string, max int) string {
		return fmt.Sprintf("CASE WHEN %s<=%d THEN %s ELSE NULL END", bytes(column), max, column)
	}
	query := `SELECT ` + short("id", 768) + `,event_type,` + short("aggregate_id", 191) + `,` + short("workspace_id", 191) + `,COALESCE(` + bytes("workspace_id") + `,0),` + short("created_at", 512) + `,` + bytes("payload") + ` FROM domain_events WHERE event_type IN ('workspace.created','workspace.updated','workspace.deletion_scheduled','workspace.deletion_canceled') AND id>? ORDER BY id LIMIT 500`
	rows, err := tx.QueryContext(ctx, query, last)
	if err != nil {
		return 0, last, err
	}
	type header struct {
		id, kind, aggregate, workspace, created sql.NullString
		workspaceBytes, size                    int64
	}
	page := make([]header, 0, 500)
	for rows.Next() {
		var item header
		if err := rows.Scan(&item.id, &item.kind, &item.aggregate, &item.workspace, &item.workspaceBytes, &item.created, &item.size); err != nil {
			rows.Close()
			return 0, last, err
		}
		if !item.id.Valid || !item.created.Valid || item.size < 0 {
			rows.Close()
			return 0, last, ErrMetadataPageBudget
		}
		page = append(page, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, last, err
	}
	for _, item := range page {
		id, key := item.workspace.String, ""
		if item.workspaceBytes > 191 {
			continue
		}
		switch item.kind.String {
		case "workspace.created", "workspace.updated":
			if id == "" {
				id = item.aggregate.String
			}
			key, err = sw26EventURLKey(ctx, &sw26EventReaderAt{ctx: ctx, tx: tx, id: item.id.String, size: item.size})
			if err != nil {
				return 0, last, err
			}
		default:
			key = item.aggregate.String
		}
		if id == "" || key == "" || len(id) > 191 || len(key) > 191 {
			continue
		}
		if _, err := stage.exec(ctx, int64(len(id)+len(key)+len(item.created.String)), `INSERT INTO pairs(id,key,at) VALUES(?,?,?) ON CONFLICT(id,key) DO UPDATE SET at=CASE WHEN excluded.at>pairs.at THEN excluded.at ELSE pairs.at END`, id, key, item.created.String); err != nil {
			return 0, last, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, last, err
	}
	if len(page) != 0 {
		last = page[len(page)-1].id.String
	}
	return len(page), last, nil
}
