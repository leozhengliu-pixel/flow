package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type CollaborationRecoveryCursor struct {
	AfterTime, AfterID, ThroughTime, ThroughID string
	Revision                                   int64
	StateHash                                  [32]byte
	Started                                    bool
}

type CollaborationRecoveryPage struct {
	Cursor                  CollaborationRecoveryCursor
	ContentState            string
	Updates                 []DocumentCollaborationUpdate
	More, Restart, Conflict bool
}

// Each page has its own MVCC snapshot and releases SQL before any send. A
// changed compacted base restarts the same generation; Yjs merges it with
// previously sent chunks. A replacement generation is never mixed in.
func (s *SQLiteStore) ReadCollaborationRecoveryPage(ctx context.Context, workspace, document, issue, record string, cursor CollaborationRecoveryCursor, maxBytes int) (CollaborationRecoveryPage, error) {
	page := CollaborationRecoveryPage{Cursor: cursor, Updates: []DocumentCollaborationUpdate{}}
	if maxBytes <= 0 {
		maxBytes = 4 << 20
	}
	options := &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}
	if s.dialect == "sqlite" {
		options = nil
	}
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(revision),0) FROM workspace_cache_revisions WHERE workspace_key=?`, workspace).Scan(&revision); err != nil {
		return page, err
	}
	if !cursor.Started || revision != cursor.Revision {
		statePath, generationPath := "contentState", "collaborationId"
		table, where, args := "workspace_metadata_records", "workspace_key=? AND field='documents' AND record_key=?", []any{workspace, record}
		fallback := record
		if issue != "" {
			statePath, generationPath = "documentContent.contentState", "documentContent.id"
			table, where, args = "issue_records", "workspace_key=? AND id=?", []any{workspace, issue}
			fallback = "document_content_" + issue
		}
		extract := func(path string) string {
			if s.dialect == "postgres" {
				p := ""
				for _, c := range path {
					if c == '.' {
						p += ","
					} else {
						p += string(c)
					}
				}
				return "(convert_from(data, 'UTF8')::jsonb #>> '{" + p + "}')"
			}
			return s.jsonText("data", path)
		}
		var stateValue sql.NullString
		var generation string
		stateExpression := `COALESCE(` + extract(statePath) + `,'')`
		stateLength := `OCTET_LENGTH(` + stateExpression + `)`
		if s.dialect == "sqlite" {
			stateLength = `LENGTH(CAST(` + stateExpression + ` AS BLOB))`
		}
		query := `SELECT CASE WHEN ` + stateLength + `<=8388608 THEN ` + stateExpression + ` ELSE NULL END,COALESCE(NULLIF(` + extract(generationPath) + `,''),?) FROM ` + table + ` WHERE ` + where
		args = append([]any{fallback}, args...)
		err := tx.QueryRowContext(ctx, query, args...).Scan(&stateValue, &generation)
		if errors.Is(err, sql.ErrNoRows) || err == nil && generation != document {
			page.Conflict = true
			return page, nil
		}
		if err != nil {
			return page, err
		}
		if !stateValue.Valid {
			return page, fmt.Errorf("collaboration base exceeds frame budget")
		}
		state := stateValue.String
		hash := sha256.Sum256([]byte(state))
		if !cursor.Started || hash != cursor.StateHash {
			page.Restart = cursor.Started
			cursor = CollaborationRecoveryCursor{StateHash: hash}
			page.ContentState = state
		}
		cursor.Revision = revision
	}
	if !cursor.Started {
		err := tx.QueryRowContext(ctx, `SELECT created_at,update_id FROM document_collaboration_updates WHERE workspace_key=? AND document_id=? ORDER BY created_at DESC,update_id DESC LIMIT 1`, workspace, document).Scan(&cursor.ThroughTime, &cursor.ThroughID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return page, err
		}
		cursor.Started = true
	}
	length := "OCTET_LENGTH(update_data)"
	if s.dialect == "sqlite" {
		length = "LENGTH(update_data)"
	}
	query := `SELECT update_id,client_id,CASE WHEN payload_bytes<=2097152 AND (running_bytes<=? OR ordinal=1) THEN update_data ELSE NULL END,created_at FROM (SELECT bounded.*,SUM(payload_bytes+512) OVER (ORDER BY created_at,update_id) running_bytes,ROW_NUMBER() OVER (ORDER BY created_at,update_id) ordinal FROM (SELECT update_id,client_id,update_data,created_at,` + length + ` payload_bytes FROM document_collaboration_updates WHERE workspace_key=? AND document_id=? AND (created_at>? OR (created_at=? AND update_id>?)) AND (created_at<? OR (created_at=? AND update_id<=?)) ORDER BY created_at,update_id LIMIT 64) bounded) budgeted ORDER BY created_at,update_id`
	rows, err := tx.QueryContext(ctx, query, maxBytes, workspace, document, cursor.AfterTime, cursor.AfterTime, cursor.AfterID, cursor.ThroughTime, cursor.ThroughTime, cursor.ThroughID)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var update DocumentCollaborationUpdate
		var created string
		if err := rows.Scan(&update.ID, &update.ClientID, &update.Data, &created); err != nil {
			return page, err
		}
		if update.Data == nil {
			if len(page.Updates) == 0 {
				return page, fmt.Errorf("collaboration update exceeds frame budget")
			}
			page.More = true
			break
		}
		update.DocumentID = document
		update.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		page.Updates = append(page.Updates, update)
		cursor.AfterTime, cursor.AfterID = created, update.ID
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Updates) == 0 && !page.More {
		cursor.AfterTime, cursor.AfterID = cursor.ThroughTime, cursor.ThroughID
	}
	page.More = page.More || cursor.AfterTime < cursor.ThroughTime || cursor.AfterTime == cursor.ThroughTime && cursor.AfterID < cursor.ThroughID
	page.Cursor = cursor
	return page, nil
}
