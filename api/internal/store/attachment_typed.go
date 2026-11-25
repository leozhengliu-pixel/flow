package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"flow/api/internal/domain"
)

type scaleP21SW16AttachmentHeader struct {
	id   string
	size int64
}

type scaleP21SW16AttachmentBatch struct {
	store    *SQLiteStore
	ids      []string
	rows     [][]string
	releases []func()
}

func (b *scaleP21SW16AttachmentBatch) close() {
	for _, release := range b.releases {
		release()
	}
	b.releases = nil
}

func (s *SQLiteStore) scaleP21SW16AttachmentBytes(column string) string {
	if s.dialect == "sqlite" {
		return "LENGTH(CAST(" + column + " AS BLOB))"
	}
	return "OCTET_LENGTH(" + column + ")"
}

// Bounded headers and one complete typed Issue are hydrated at a time.
// Sparse IDs/hashes stay admitted until the original index-difference writer
// and decoded-ID checkpoint commit together.
func (s *SQLiteStore) scaleP21SW16MigrateTypedAttachmentIndex(ctx context.Context) error {
	ctx, release, err := s.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		return err
	}
	defer release()
	s.mu.RLock()
	keyBytes := int64(0)
	for key := range s.workspaces {
		keyBytes += int64(len(key))*3 + 128
	}
	s.mu.RUnlock()
	if keyBytes > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, releaseKeys, err := s.tryAdmitAdditionalMetadataWork(ctx, int(keyBytes)+64<<10)
	if err != nil {
		return err
	}
	defer releaseKeys()
	s.mu.RLock()
	current := int64(0)
	keys := make([]string, 0, len(s.workspaces))
	for key := range s.workspaces {
		current += int64(len(key))*3 + 128
		if current > keyBytes {
			s.mu.RUnlock()
			return ErrMetadataWorkBudget
		}
		keys = append(keys, strings.Clone(key))
	}
	s.mu.RUnlock()
	for _, workspace := range keys {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_attachment_migrations(workspace_key,last_id,complete) VALUES(?,'',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
		for {
			done, err := s.scaleP21SW16TypedAttachmentStep(ctx, workspace)
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

func (s *SQLiteStore) scaleP21SW16TypedAttachmentStep(ctx context.Context, workspace string) (bool, error) {
	last, complete, releaseCheckpoint, err := s.scaleP21SW16AttachmentCheckpoint(ctx, workspace)
	if err != nil {
		return false, err
	}
	defer releaseCheckpoint()
	if complete != 0 {
		return true, nil
	}
	options := metadataReadOptions(s.dialect)
	options.ReadOnly = false
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	headers, releaseHeaders, err := s.scaleP21SW16TypedAttachmentHeaders(ctx, tx, workspace, last)
	if err != nil {
		return false, err
	}
	defer releaseHeaders()
	batch := scaleP21SW16AttachmentBatch{store: s}
	defer batch.close()
	for _, header := range headers {
		if err := s.scaleP21SW16ReadAttachmentIssue(ctx, tx, workspace, header, batch.add); err != nil {
			return false, err
		}
	}
	if err := batch.write(ctx, tx, workspace); err != nil {
		return false, err
	}
	if len(batch.ids) > 0 {
		// Legacy advances with the last decoded Issue.ID, even when it
		// differs from the selected physical row key.
		last = batch.ids[len(batch.ids)-1]
	}
	if len(headers) < 64 {
		complete = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE issue_attachment_migrations SET last_id=?,complete=? WHERE workspace_key=?`, last, complete, workspace); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return complete != 0, nil
}

func (s *SQLiteStore) scaleP21SW16AttachmentCheckpoint(ctx context.Context, workspace string) (string, int, func(), error) {
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return "", 0, nil, err
	}
	defer tx.Rollback()
	var size int64
	var complete int
	if err := tx.QueryRowContext(ctx, "SELECT "+s.scaleP21SW16AttachmentBytes("last_id")+",complete FROM issue_attachment_migrations WHERE workspace_key=?", workspace).Scan(&size, &complete); err != nil {
		return "", 0, nil, err
	}
	if complete != 0 {
		return "", complete, func() {}, nil
	}
	if size < 0 || size > (metadataWorkBytes-(64<<10))/3 {
		return "", 0, nil, ErrMetadataWorkBudget
	}
	readCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(size)*3+64<<10)
	if err != nil {
		return "", 0, nil, err
	}
	var last sql.NullString
	err = tx.QueryRowContext(readCtx, "SELECT CASE WHEN "+s.scaleP21SW16AttachmentBytes("last_id")+"<=? THEN last_id ELSE NULL END FROM issue_attachment_migrations WHERE workspace_key=?", size, workspace).Scan(&last)
	if err != nil || !last.Valid {
		release()
		if err == nil {
			err = ErrMetadataWorkBudget
		}
		return "", 0, nil, err
	}
	return last.String, complete, release, nil
}

func (s *SQLiteStore) scaleP21SW16TypedAttachmentHeaders(ctx context.Context, tx *sqlTx, workspace, last string) ([]scaleP21SW16AttachmentHeader, func(), error) {
	page := " FROM issue_records WHERE workspace_key=? AND id>? ORDER BY id LIMIT 64"
	var count, bytes int64
	query := "SELECT COUNT(*),COALESCE(SUM(id_bytes),0) FROM (SELECT " + s.scaleP21SW16AttachmentBytes("id") + " id_bytes" + page + ") sw16_headers"
	if err := tx.QueryRowContext(ctx, query, workspace, last).Scan(&count, &bytes); err != nil {
		return nil, nil, err
	}
	if count < 0 || count > 64 || bytes < 0 || bytes > (metadataWorkBytes-(64<<10)-count*128)/3 {
		return nil, nil, ErrMetadataWorkBudget
	}
	headerCtx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(bytes)*3+int(count)*128+64<<10)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.QueryContext(headerCtx, "SELECT id,"+s.scaleP21SW16AttachmentBytes("data")+page, workspace, last)
	if err != nil {
		release()
		return nil, nil, err
	}
	headers := make([]scaleP21SW16AttachmentHeader, 0, int(count))
	for rows.Next() {
		var header scaleP21SW16AttachmentHeader
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
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil || len(headers) != int(count) {
		release()
		if err == nil {
			err = ErrConcurrentWrite
		}
		return nil, nil, err
	}
	return headers, release, nil
}

func (s *SQLiteStore) scaleP21SW16ReadAttachmentIssue(ctx context.Context, tx *sqlTx, workspace string, header scaleP21SW16AttachmentHeader, visit func(context.Context, domain.Issue) error) error {
	if header.size < 0 || header.size > (metadataWorkBytes-(64<<10))/3 {
		return ErrMetadataWorkBudget
	}
	rawCtx, releaseRaw, err := s.tryAdmitAdditionalMetadataWork(ctx, int(header.size)*3+64<<10)
	if err != nil {
		return err
	}
	defer releaseRaw()
	var raw []byte
	query := "SELECT CASE WHEN " + s.scaleP21SW16AttachmentBytes("data") + "<=? THEN data ELSE NULL END FROM issue_records WHERE workspace_key=? AND " + s.metadataRecordKeyEquals("id", "?")
	if err := tx.QueryRowContext(rawCtx, query, header.size, workspace, header.id).Scan(&raw); err != nil {
		return err
	}
	if int64(len(raw)) != header.size {
		return ErrMetadataWorkBudget
	}
	budget := metadataHydrationBudget{bytes: len(raw), work: metadataWorkBytes}
	if err := budget.consume(raw); err != nil {
		return fmt.Errorf("%w: complete attachment source issue", ErrMetadataWorkBudget)
	}
	typedCost := metadataWorkBytes - budget.work + 128<<10
	typedCtx, releaseTyped, err := s.tryAdmitAdditionalMetadataWork(rawCtx, typedCost)
	if err != nil {
		return err
	}
	defer releaseTyped()
	var issue domain.Issue
	if err := json.Unmarshal(raw, &issue); err != nil {
		return err
	}
	return visit(typedCtx, issue)
}

func (b *scaleP21SW16AttachmentBatch) add(ctx context.Context, issue domain.Issue) error {
	// syncIssueIndexRows serializes composite keys and owns maps for both
	// desired and prior rows; charge those temporary representations too.
	cost := int64(len(issue.ID))*22 + 1024
	cost += int64(len(issue.Attachments)) * (1024 + 22*(int64(len(issue.ID))+64))
	if cost > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := b.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		return err
	}
	b.releases = append(b.releases, release)
	id := strings.Clone(issue.ID)
	b.ids = append(b.ids, id)
	for _, attachment := range issue.Attachments {
		b.rows = append(b.rows, []string{id, attachmentKey(attachment.URL)})
	}
	return nil
}

func (b *scaleP21SW16AttachmentBatch) write(ctx context.Context, tx *sqlTx, workspace string) error {
	if len(b.ids) == 0 {
		return nil
	}
	where, ids := bindList("issue_id", b.ids)
	length := b.store.scaleP21SW16AttachmentBytes("issue_id") + "+" + b.store.scaleP21SW16AttachmentBytes("url_hash")
	var count, bytes int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+length+"),0) FROM issue_attachment_records WHERE workspace_key=? AND "+where, append([]any{workspace}, ids...)...).Scan(&count, &bytes); err != nil {
		return err
	}
	if count < 0 || bytes < 0 || count > metadataWorkBytes/1024 || bytes > (metadataWorkBytes-count*1024-(64<<10))/22 {
		return ErrMetadataWorkBudget
	}
	writeCtx, release, err := b.store.tryAdmitAdditionalMetadataWork(ctx, int(bytes)*22+int(count)*1024+64<<10)
	if err != nil {
		return err
	}
	defer release()
	return syncIssueIndexRows(writeCtx, tx, workspace, "issue_attachment_records", []string{"issue_id", "url_hash"}, 2, b.ids, b.rows)
}
