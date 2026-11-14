package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"flow/api/internal/domain"
)

const scaleP21SW21RecordWorkMax = 64 << 20
const scaleP21SW21RecordOverhead = 128 << 10
const scaleP21SW21RecordExpansion = 24

// VisitOAuthSyncGroupRequests is a trusted snapshot-consistent compatibility
// scan. Only candidate identities and lengths enter the header pages. One
// typed record is admitted, fetched and visited at a time; callers must finish
// encoding it before returning and must not retain the record or its context.
// This is not an authorization boundary.
func (s *SQLiteStore) VisitOAuthSyncGroupRequests(ctx context.Context, workspace, applicationID string, visit func(context.Context, domain.OAuthSyncGroupRequest) error) error {
	if workspace == "" || visit == nil {
		return ErrMetadataRecordQuery
	}
	// Header pages and the response writer's small buffers remain resident
	// across callbacks. Keep their lease separate from each adaptive record.
	_, releaseHeaders, err := s.admitMetadataWork(ctx, 256<<10)
	if err != nil {
		return err
	}
	defer releaseHeaders()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	predicate, selectedArgs := s.scaleP21SW21ApplicationCandidate(applicationID)
	last, started, ordinal := "", false, int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		where := "workspace_key=? AND field='oauthSyncGroupRequests' AND (" + predicate + ")"
		args := append([]any{workspace}, selectedArgs...)
		if started {
			where += " AND (collection_order>? OR (collection_order=? AND record_key>?))"
			args = append(args, ordinal, ordinal, last)
		}
		rows, err := tx.QueryContext(ctx, "SELECT record_key,collection_order,LENGTH(data) FROM workspace_metadata_records WHERE "+where+" ORDER BY collection_order,record_key LIMIT 50", args...)
		if err != nil {
			return err
		}
		type header struct {
			id          string
			order, size int64
		}
		page := make([]header, 0, 50)
		for rows.Next() {
			var item header
			if err := rows.Scan(&item.id, &item.order, &item.size); err != nil {
				rows.Close()
				return err
			}
			page = append(page, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, item := range page {
			if err := s.scaleP21SW21VisitRequest(ctx, tx, workspace, applicationID, item.id, item.size, visit); err != nil {
				return err
			}
		}
		last, started, ordinal = page[len(page)-1].id, true, page[len(page)-1].order
	}
}

func (s *SQLiteStore) scaleP21SW21VisitRequest(ctx context.Context, tx *sqlTx, workspace, applicationID, id string, size int64, visit func(context.Context, domain.OAuthSyncGroupRequest) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if size < 0 || size > (scaleP21SW21RecordWorkMax-scaleP21SW21RecordOverhead)/scaleP21SW21RecordExpansion {
		return fmt.Errorf("%w: sync request exceeds one-record compatibility allowance", ErrMetadataWorkBudget)
	}
	// Raw SQL bytes, typed strings, JSON escaping and the encoder's temporary
	// buffers are charged together. No per-record lease survives this callback.
	reservation := int(size)*scaleP21SW21RecordExpansion + scaleP21SW21RecordOverhead
	recordCtx, release, err := s.admitMetadataWork(ctx, reservation)
	if err != nil {
		return err
	}
	defer release()
	var raw []byte
	if err := tx.QueryRowContext(recordCtx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field='oauthSyncGroupRequests' AND `+s.metadataRecordKeyEquals("record_key", "?"), size, workspace, id).Scan(&raw); err != nil {
		return err
	}
	if raw == nil || int64(len(raw)) != size {
		return ErrMetadataWorkBudget
	}
	var item domain.OAuthSyncGroupRequest
	if err := json.Unmarshal(raw, &item); err != nil {
		return err
	}
	if item.ApplicationID != applicationID {
		// Candidate matching is deliberately broader than Go's final typed
		// match for duplicate/case-varied JSON keys and mixed database dialects.
		return nil
	}
	// Publication must not try to acquire a sole database connection while
	// this callback pins it. Existing epoch readers fail closed on this marker.
	recordCtx = context.WithValue(recordCtx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
	return visit(recordCtx, item)
}

func (s *SQLiteStore) scaleP21SW21ApplicationCandidate(applicationID string) (string, []any) {
	switch s.dialect {
	case "postgres":
		// json, rather than jsonb, preserves duplicate object members. A
		// candidate matches any member; encoding/json decides the final value.
		return `EXISTS (SELECT 1 FROM json_each(convert_from(data,'UTF8')::json) AS sw21(key,value) WHERE LOWER(sw21.key)='applicationid' AND (json_typeof(sw21.value)='string' AND sw21.value#>>'{}'=? OR json_typeof(sw21.value)='null' AND ?='')) OR (?='' AND NOT EXISTS (SELECT 1 FROM json_each(convert_from(data,'UTF8')::json) AS sw21(key,value) WHERE LOWER(sw21.key)='applicationid'))`, []any{applicationID, applicationID, applicationID}
	case "mysql":
		// MySQL's binary JSON functions collapse duplicate keys. Conservatively
		// decode duplicate/escaped-key candidates instead of trusting whichever
		// value that parser chose. Normal unrelated large names stay in SQL.
		text := "CONVERT(data USING utf8mb4)"
		folded := "LOWER(" + text + ")"
		key := `"applicationid"`
		search := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(applicationID)
		escapedKey := `(^|[,{])[[:space:]]*"([^"\\]|\\.)*\\([^"\\]|\\.)*"[[:space:]]*:`
		return `JSON_SEARCH(` + text + `,'one',?,'!','$.*') IS NOT NULL OR REGEXP_LIKE(` + text + `,?,'c') OR LENGTH(` + folded + `)-LENGTH(REPLACE(` + folded + `,?,''))>=? OR (?='' AND ` + s.jsonText("data", "applicationId") + ` IS NULL)`, []any{search, escapedKey, key, len(key) * 2, applicationID}
	default:
		return `EXISTS (SELECT 1 FROM json_each(data) AS sw21 WHERE LOWER(sw21.key)='applicationid' AND (sw21.type='text' AND sw21.value=? OR sw21.type='null' AND ?='')) OR (?='' AND NOT EXISTS (SELECT 1 FROM json_each(data) AS sw21 WHERE LOWER(sw21.key)='applicationid'))`, []any{applicationID, applicationID, applicationID}
	}
}
