package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"flow/api/internal/domain"
)

type attributeHeader struct {
	id   string
	size int64
}

type attributeBatch struct {
	store    *SQLiteStore
	ids      []string
	desired  [][]string
	releases []func()
}

func (b *attributeBatch) close() {
	for _, release := range b.releases {
		release()
	}
	b.releases = nil
}

func (b *attributeBatch) add(ctx context.Context, issue domain.Issue) error {
	attributes := issueAttributes(issue)
	rows := make([][]string, 0, len(attributes))
	for field, value := range attributes {
		rows = append(rows, []string{issue.ID, indexKey(field), indexKey(value)})
	}
	return b.retain(ctx, issue.ID, rows)
}

func (b *attributeBatch) retain(ctx context.Context, id string, rows [][]string) error {
	// syncIssueIndexRows JSON-encodes composite keys. Its strings can expand
	// sixfold, with encoder growth and a returned copy: 4+3*6 bytes per byte.
	bytes := int64(len(id))*22 + int64(len(rows)+1)*1024
	for _, row := range rows {
		for _, value := range row {
			bytes += int64(len(value)) * 22
		}
	}
	if bytes > int64(metadataWorkBytes()) {
		return ErrMetadataWorkBudget
	}
	_, release, err := b.store.tryAdmitAdditionalMetadataWork(ctx, int(bytes))
	if err != nil {
		return err
	}
	b.releases = append(b.releases, release)
	b.ids = append(b.ids, id)
	b.desired = append(b.desired, rows...)
	return nil
}

func (s *SQLiteStore) attributeColumnLength(column string) string {
	if s.dialect == "sqlite" {
		return "LENGTH(CAST(" + column + " AS BLOB))"
	}
	return "OCTET_LENGTH(" + column + ")"
}

func (s *SQLiteStore) attributeHeaders(ctx context.Context, tx *sqlTx, workspace, last string) ([]attributeHeader, func(), error) {
	where, args := "workspace_key=? AND id>?", []any{workspace, last}
	page := " FROM issue_records WHERE " + where + " ORDER BY id LIMIT 250"
	var count, keyBytes int64
	query := "SELECT COUNT(*),COALESCE(SUM(key_bytes),0) FROM (SELECT " + s.attributeColumnLength("id") + " key_bytes" + page + ") sw17_headers"
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count, &keyBytes); err != nil {
		return nil, nil, err
	}
	if keyBytes < 0 || keyBytes > int64(metadataWorkBytes())/3 || count < 0 || count > 250 {
		return nil, nil, ErrMetadataWorkBudget
	}
	cost := keyBytes*3 + count*128 + 64<<10
	if cost > int64(metadataWorkBytes()) {
		return nil, nil, ErrMetadataWorkBudget
	}
	headerCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.QueryContext(headerCtx, "SELECT id,"+s.attributeColumnLength("data")+page, args...)
	if err != nil {
		release()
		return nil, nil, err
	}
	headers := make([]attributeHeader, 0, int(count))
	for rows.Next() {
		var header attributeHeader
		if err := rows.Scan(&header.id, &header.size); err != nil {
			rows.Close()
			release()
			return nil, nil, err
		}
		if header.size < 0 {
			rows.Close()
			release()
			return nil, nil, ErrMetadataRecordQuery
		}
		headers = append(headers, header)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(headers) != int(count) {
		release()
		if err == nil {
			err = ErrConcurrentWrite
		}
		return nil, nil, err
	}
	return headers, release, nil
}

func (s *SQLiteStore) readAttributeIssue(ctx context.Context, tx *sqlTx, workspace string, header attributeHeader, visit func(context.Context, domain.Issue) error) error {
	if header.size > (int64(metadataWorkBytes())-(64<<10))/3 {
		return ErrMetadataWorkBudget
	}
	rawCost := int(header.size)*3 + 64<<10
	rawCtx, releaseRaw, err := s.tryAdmitAdditionalMetadataWork(ctx, rawCost)
	if err != nil {
		return err
	}
	defer releaseRaw()
	var raw []byte
	query := "SELECT CASE WHEN " + s.attributeColumnLength("data") + "<=? THEN data ELSE NULL END FROM issue_records WHERE workspace_key=? AND " + s.metadataRecordKeyEquals("id", "?")
	if err := tx.QueryRowContext(rawCtx, query, header.size, workspace, header.id).Scan(&raw); err != nil {
		return err
	}
	if int64(len(raw)) != header.size {
		return ErrMetadataWorkBudget
	}
	return s.decodeAttributeIssue(rawCtx, raw, rawCost, visit)
}

func (s *SQLiteStore) decodeAttributeIssue(ctx context.Context, raw []byte, rawCost int, visit func(context.Context, domain.Issue) error) error {
	// The existing record hydration token model accounts for typed maps/slices as well
	// as strings. Only one full Issue is decoded, never a 250-body slice.
	budget := metadataHydrationBudget{bytes: len(raw), work: metadataWorkBytes()}
	if err := budget.consume(raw); err != nil {
		return fmt.Errorf("%w: issue attribute decode", ErrMetadataWorkBudget)
	}
	typedCost := metadataWorkBytes() - budget.work
	typedCtx := ctx
	if extra := typedCost - rawCost; extra > 0 {
		var releaseTyped func()
		var err error
		typedCtx, releaseTyped, err = s.tryAdmitAdditionalMetadataWork(ctx, extra)
		if err != nil {
			return err
		}
		defer releaseTyped()
	}
	var issue domain.Issue
	if err := json.Unmarshal(raw, &issue); err != nil {
		return err
	}
	return visit(typedCtx, issue)
}

func (b *attributeBatch) write(ctx context.Context, tx *sqlTx, workspace string) error {
	if len(b.ids) == 0 {
		return nil
	}
	where, ids := bindList("issue_id", b.ids)
	length := b.store.attributeColumnLength("issue_id") + "+" + b.store.attributeColumnLength("field") + "+" + b.store.attributeColumnLength("value")
	var count, bytes int64
	query := "SELECT COUNT(*),COALESCE(SUM(" + length + "),0) FROM issue_attribute_records WHERE workspace_key=? AND " + where
	if err := tx.QueryRowContext(ctx, query, append([]any{workspace}, ids...)...).Scan(&count, &bytes); err != nil {
		return err
	}
	if bytes < 0 || bytes > int64(metadataWorkBytes())/22 || count < 0 || count > int64(metadataWorkBytes())/1024 {
		return ErrMetadataWorkBudget
	}
	cost := bytes*22 + count*1024 + 64<<10
	if cost > int64(metadataWorkBytes()) {
		return ErrMetadataWorkBudget
	}
	writeCtx, release, err := b.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		return err
	}
	defer release()
	return syncIssueIndexRows(writeCtx, tx, workspace, "issue_attribute_records", []string{"issue_id", "field", "value"}, 2, b.ids, b.desired)
}

func (s *SQLiteStore) attributeCheckpoint(ctx context.Context, workspace string) (string, int, func(), error) {
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return "", 0, nil, err
	}
	defer tx.Rollback()
	var size int64
	var complete int
	query := "SELECT " + s.attributeColumnLength("last_id") + ",complete FROM issue_attribute_migrations WHERE workspace_key=?"
	if err := tx.QueryRowContext(ctx, query, workspace).Scan(&size, &complete); err != nil {
		return "", 0, nil, err
	}
	if complete >= issueAttributeVersion {
		return "", complete, func() {}, nil
	}
	if size < 0 || size > (int64(metadataWorkBytes())-(64<<10))/3 {
		return "", 0, nil, ErrMetadataWorkBudget
	}
	readCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(size)*3+64<<10)
	if err != nil {
		return "", 0, nil, err
	}
	var last sql.NullString
	query = "SELECT CASE WHEN " + s.attributeColumnLength("last_id") + "<=? THEN last_id ELSE NULL END FROM issue_attribute_migrations WHERE workspace_key=?"
	if err := tx.QueryRowContext(readCtx, query, size, workspace).Scan(&last); err != nil || !last.Valid {
		release()
		if err == nil {
			err = ErrMetadataWorkBudget
		}
		return "", 0, nil, err
	}
	return last.String, complete, release, nil
}

func (s *SQLiteStore) migrateIssueAttributesBounded(ctx context.Context) error {
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	for workspace := range s.workspaces {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_attribute_migrations(workspace_key,last_id,complete) VALUES(?,'',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
		for {
			done, err := s.attributeStep(ctx, workspace)
			if err != nil {
				return err
			}
			if done {
				break
			}
		}
	}
	return nil
}

func (s *SQLiteStore) attributeStep(ctx context.Context, workspace string) (bool, error) {
	last, complete, releaseCheckpoint, err := s.attributeCheckpoint(ctx, workspace)
	if err != nil {
		return false, err
	}
	defer releaseCheckpoint()
	if complete >= issueAttributeVersion {
		return true, nil
	}
	if complete > 0 {
		if _, err := s.db.ExecContext(ctx, `UPDATE issue_attribute_migrations SET last_id='',complete=0 WHERE workspace_key=?`, workspace); err != nil {
			return false, err
		}
		last = ""
	}
	options := metadataReadOptions(s.dialect)
	options.ReadOnly = false
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	headers, releaseHeaders, err := s.attributeHeaders(ctx, tx, workspace, last)
	if err != nil {
		return false, err
	}
	defer releaseHeaders()
	batch := attributeBatch{store: s}
	defer batch.close()
	for _, header := range headers {
		if err := s.readAttributeIssue(ctx, tx, workspace, header, batch.add); err != nil {
			return false, err
		}
	}
	if err := batch.write(ctx, tx, workspace); err != nil {
		return false, err
	}
	if len(batch.ids) > 0 {
		last = batch.ids[len(batch.ids)-1]
	}
	if len(headers) < 250 {
		complete = issueAttributeVersion
	} else {
		complete = 0
	}
	if _, err := tx.ExecContext(ctx, `UPDATE issue_attribute_migrations SET last_id=?,complete=? WHERE workspace_key=?`, last, complete, workspace); err != nil {
		return false, err
	}
	return complete >= issueAttributeVersion, tx.Commit()
}

func (s *SQLiteStore) migrateRecurrenceInstancesBounded(ctx context.Context) error {
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS recurrence_instance_migrations(workspace_key VARCHAR(191) PRIMARY KEY)`); err != nil {
		return err
	}
	for workspace := range s.workspaces {
		if err := s.migrateRecurrenceWorkspace(ctx, workspace); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) migrateRecurrenceWorkspace(ctx context.Context, workspace string) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM recurrence_instance_migrations WHERE workspace_key=?`, workspace).Scan(&exists); err != nil || exists > 0 {
		return err
	}
	options := metadataReadOptions(s.dialect)
	options.ReadOnly = false
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	spool, err := NewRecordSpool()
	if err != nil {
		return err
	}
	defer spool.Close()
	if err := s.stageRecurrenceAttributes(ctx, tx, workspace, spool); err != nil {
		return err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	batch := attributeBatch{store: s}
	defer func() { batch.close() }()
	for {
		var header [16]byte
		if _, err := io.ReadFull(spool, header[:]); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		size, count := binary.LittleEndian.Uint64(header[:8]), binary.LittleEndian.Uint64(header[8:])
		if size > uint64(metadataWorkBytes())/4 || count > uint64(metadataWorkBytes())/1024 {
			return ErrMetadataWorkBudget
		}
		cost := size*4 + count*1024 + 64<<10
		if cost > uint64(metadataWorkBytes()) {
			return ErrMetadataWorkBudget
		}
		if err := func() error {
			recordCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(cost))
			if err != nil {
				return err
			}
			defer release()
			limited := &io.LimitedReader{R: spool, N: int64(size)}
			var record attributeProjection
			if err := json.NewDecoder(limited).Decode(&record); err != nil {
				return err
			}
			if limited.N != 0 || uint64(len(record.Rows)) != count {
				return ErrMetadataRecordQuery
			}
			return batch.retain(recordCtx, record.ID, record.Rows)
		}(); err != nil {
			return err
		}
		if len(batch.ids) == 250 {
			if err := batch.write(ctx, tx, workspace); err != nil {
				return err
			}
			batch.close()
			batch = attributeBatch{store: s}
		}
	}
	if err := batch.write(ctx, tx, workspace); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO recurrence_instance_migrations(workspace_key) VALUES(?) ON CONFLICT DO NOTHING`, workspace); err != nil {
		return err
	}
	return tx.Commit()
}

type attributeProjection struct {
	ID   string
	Rows [][]string
}

func (s *SQLiteStore) stageRecurrenceAttributes(ctx context.Context, tx *sqlTx, workspace string, spool *RecordSpool) error {
	column, pattern := "data", any(`%"recurrenceSeriesId":%`)
	switch s.dialect {
	case "sqlite":
		column = "CAST(data AS TEXT)"
	case "postgres":
		pattern = []byte(`%"recurrenceSeriesId":%`)
	}
	where := " FROM issue_records WHERE workspace_key=? AND " + column + " LIKE ?"
	var maximum int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+s.attributeColumnLength("data")+"),0)"+where, workspace, pattern).Scan(&maximum); err != nil {
		return err
	}
	if maximum < 0 || maximum > (int64(metadataWorkBytes())-(64<<10))/3 {
		return ErrMetadataWorkBudget
	}
	rawCost := int(maximum)*3 + 64<<10
	rawCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, rawCost)
	if err != nil {
		return err
	}
	defer release()
	// Preserve the original unordered cursor and its 250-selected-issue
	// batches. Sparse projections spool before writes so MySQL never needs
	// another statement while a source cursor occupies its connection.
	rows, err := tx.QueryContext(rawCtx, "SELECT data"+where, workspace, pattern)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if int64(len(raw)) > maximum {
			return ErrMetadataWorkBudget
		}
		if err := s.decodeAttributeIssue(rawCtx, raw, rawCost, func(recordCtx context.Context, issue domain.Issue) error {
			if issue.RecurrenceSeriesID == "" {
				return nil
			}
			projection := attributeBatch{store: s}
			defer projection.close()
			if err := projection.add(recordCtx, issue); err != nil {
				return err
			}
			data, err := json.Marshal(attributeProjection{ID: issue.ID, Rows: projection.desired})
			if err != nil {
				return err
			}
			var header [16]byte
			binary.LittleEndian.PutUint64(header[:8], uint64(len(data)))
			binary.LittleEndian.PutUint64(header[8:], uint64(len(projection.desired)))
			if n, err := spool.Write(header[:]); err != nil {
				return err
			} else if n != len(header) {
				return io.ErrShortWrite
			}
			if n, err := spool.Write(data); err != nil {
				return err
			} else if n != len(data) {
				return io.ErrShortWrite
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return rows.Err()
}
