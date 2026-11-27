package store

import (
	"context"
	"encoding/json"
	"fmt"

	"flow/api/internal/domain"
)

const recordWorkMax = 64 << 20
const recordOverhead = 128 << 10
const recordExpansion = 24

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
	predicate, selectedArgs := s.applicationCandidate(applicationID)
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
			if err := s.visitRequest(ctx, tx, workspace, applicationID, item.id, item.size, visit); err != nil {
				return err
			}
		}
		last, started, ordinal = page[len(page)-1].id, true, page[len(page)-1].order
	}
}

func (s *SQLiteStore) visitRequest(ctx context.Context, tx *sqlTx, workspace, applicationID, id string, size int64, visit func(context.Context, domain.OAuthSyncGroupRequest) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if size < 0 || size > (recordWorkMax-recordOverhead)/recordExpansion {
		return fmt.Errorf("%w: sync request exceeds one-record compatibility allowance", ErrMetadataWorkBudget)
	}
	// Raw SQL bytes, typed strings, JSON escaping and the encoder's temporary
	// buffers are charged together. No per-record lease survives this callback.
	reservation := int(size)*recordExpansion + recordOverhead
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

func (s *SQLiteStore) applicationCandidate(applicationID string) (string, []any) {
	switch s.dialect {
	case "postgres":
		// json, rather than jsonb, preserves duplicate object members. A
		// candidate matches any member; encoding/json decides the final value.
		// json_each refuses a scalar or array document, which has no members
		// (SQLite's json_each yields none named applicationId): such a
		// document is a candidate exactly when no application is selected.
		return `CASE WHEN json_typeof(flow_utf8(data)::json)='object' THEN EXISTS (SELECT 1 FROM json_each(flow_utf8(data)::json) AS sw21(key,value) WHERE LOWER(sw21.key)='applicationid' AND (json_typeof(sw21.value)='string' AND sw21.value#>>'{}'=? OR json_typeof(sw21.value)='null' AND ?='')) OR (?='' AND NOT EXISTS (SELECT 1 FROM json_each(flow_utf8(data)::json) AS sw21(key,value) WHERE LOWER(sw21.key)='applicationid')) ELSE ?='' END`, []any{applicationID, applicationID, applicationID, applicationID}
	case "mysql":
		// Like SQLite and PostgreSQL, select the top-level members whose key
		// folds to applicationid (JSON_KEYS decodes escaped keys) and whose
		// value is the selected string or null, or no such member when no
		// application is selected. MySQL's binary JSON collapses duplicate keys,
		// keeping the last member, while encoding/json keeps the last non-null
		// one, so the candidates where they can differ are decoded as well
		// instead of trusting whichever value that parser chose: two unescaped
		// applicationId keys (any case), or an escaped key that may have
		// collapsed with one (a backslash and a null member). A regular
		// expression over the raw text exceeded MySQL's regexp_time_limit on
		// legal large rows, and a search of every member's value selected
		// unrelated rows with any empty field. Unrelated large rows stay in SQL.
		text := "CONVERT(data USING utf8mb4)"
		folded := "LOWER(" + text + ")"
		key := `"applicationid"`
		keys := `JSON_TABLE(JSON_KEYS(` + text + `),'$[*]' COLUMNS(k LONGTEXT PATH '$')) AS sw21 WHERE LOWER(sw21.k)='applicationid'`
		member := `JSON_EXTRACT(` + text + `,CONCAT('$.',JSON_QUOTE(sw21.k)))`
		return `EXISTS (SELECT 1 FROM ` + keys + ` AND (JSON_TYPE(` + member + `)='STRING' AND CAST(JSON_UNQUOTE(` + member + `) AS BINARY)=CAST(? AS BINARY) OR JSON_TYPE(` + member + `)='NULL' AND (?='' OR LOCATE('\\',` + text + `)>0))) OR LENGTH(` + folded + `)-LENGTH(REPLACE(` + folded + `,?,''))>=? OR (?='' AND NOT EXISTS (SELECT 1 FROM ` + keys + `))`, []any{applicationID, applicationID, key, len(key) * 2, applicationID}
	default:
		return `EXISTS (SELECT 1 FROM json_each(data) AS sw21 WHERE LOWER(sw21.key)='applicationid' AND (sw21.type='text' AND sw21.value=? OR sw21.type='null' AND ?='')) OR (?='' AND NOT EXISTS (SELECT 1 FROM json_each(data) AS sw21 WHERE LOWER(sw21.key)='applicationid'))`, []any{applicationID, applicationID, applicationID}
	}
}
