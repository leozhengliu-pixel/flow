package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	MetadataRecordPageMaxBytes = 256 << 10
	MetadataRecordBatchMaxKeys = 50
	metadataRecordChunkBytes   = 16 << 10
	metadataSummaryMaxBytes    = 32 << 10
	metadataPageOverhead       = 8 << 10
)

var (
	ErrMetadataRecordQuery = errors.New("invalid metadata record query")
	ErrMetadataPageBudget  = errors.New("metadata page budget too small")
	ErrMetadataCursor      = errors.New("invalid or stale metadata catalog cursor")
	ErrMetadataCatalogGap  = errors.New("metadata catalog projection is incomplete")
)

type RecordKey struct{ Kind, ID string }

type PageRequest struct {
	Kind, Cursor, Filter, Order string
	Limit, MaxBytes             int
}

// MetadataRecord is owned by the caller. Omitted records keep a detail key;
// missing canonical rows are not presented as versioned tombstones.
// P05 versions are content fingerprints for shadow equality, not ordered
// journal versions. P09 must supply committed versions before sync serving.
type MetadataRecord struct {
	Key     RecordKey       `json:"key"`
	Version string          `json:"version"`
	Data    json.RawMessage `json:"data,omitempty"`
	Omitted bool            `json:"omitted,omitempty"`
	Missing bool            `json:"missing,omitempty"`
	Detail  *RecordKey      `json:"detail,omitempty"`
}

type RecordPage struct {
	Items      []MetadataRecord `json:"items"`
	Next       string           `json:"next"`
	HasMore    bool             `json:"hasMore"`
	Revision   string           `json:"revision"`
	AuthzEpoch string           `json:"authzEpoch"`
}

type RecordBatch struct {
	Items      []MetadataRecord `json:"items"`
	AuthzEpoch string           `json:"authzEpoch"`
}

type metadataCatalogCursor struct {
	Schema   int    `json:"s"`
	Binding  string `json:"b"`
	Revision string `json:"r"`
	Sort     string `json:"o"`
	Ordinal  int64  `json:"n"`
	ID       string `json:"i"`
}

func metadataPageLimits(q PageRequest) (int, int, error) {
	limit, budget := q.Limit, q.MaxBytes
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if budget <= 0 || budget > MetadataRecordPageMaxBytes {
		budget = MetadataRecordPageMaxBytes
	}
	if budget < metadataPageOverhead {
		return 0, 0, ErrMetadataPageBudget
	}
	return limit, budget, nil
}

func validMetadataKey(key RecordKey) bool {
	return key.Kind != "" && len(key.Kind) <= 191 && len(key.ID) <= 191 && !strings.ContainsRune(key.Kind+key.ID, '\x00')
}

func metadataFingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ReadCanonicalRecords is a trusted storage primitive, not an authorization
// boundary. P03/P10 must authorize, redact and check epochs around it. It never
// consults the shadow catalog, even when that catalog has finished a pass.
func (s *SQLiteStore) ReadCanonicalRecords(ctx context.Context, workspace string, keys []RecordKey, projection string) (RecordBatch, error) {
	result := RecordBatch{Items: []MetadataRecord{}}
	if workspace == "" || len(keys) > MetadataRecordBatchMaxKeys || projection != "detail" && projection != "summary" {
		return result, ErrMetadataRecordQuery
	}
	for _, key := range keys {
		if !validMetadataKey(key) {
			return result, ErrMetadataRecordQuery
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	return s.readCanonicalRecords(ctx, tx, workspace, keys, projection)
}

func (s *SQLiteStore) readCanonicalRecords(ctx context.Context, tx *sqlTx, workspace string, keys []RecordKey, projection string) (RecordBatch, error) {
	result := RecordBatch{Items: []MetadataRecord{}}
	remaining := MetadataRecordPageMaxBytes - metadataPageOverhead
	minimum, reserved := make([]int, len(keys)), 0
	for i, key := range keys {
		item := MetadataRecord{Key: key, Version: strings.Repeat("0", 64), Omitted: true, Detail: &key}
		raw, _ := json.Marshal(item)
		minimum[i] = len(raw) + 1
		reserved += minimum[i]
	}
	for i, key := range keys {
		reserved -= minimum[i]
		available := remaining - reserved
		var size int64
		var raw []byte
		// Test the length in SQL before transferring a body, including for a
		// caller whose previous keys have already consumed the batch budget.
		fetchLimit := max(0, available-1024)
		if projection == "summary" {
			fetchLimit = 0
		}
		err := tx.QueryRowContext(ctx, `SELECT LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, fetchLimit, workspace, key.Kind, key.ID).Scan(&size, &raw)
		item := MetadataRecord{Key: key}
		if errors.Is(err, sql.ErrNoRows) {
			item.Missing = true
		} else if err != nil {
			return result, err
		} else if raw != nil && projection == "detail" {
			item.Data, item.Version = json.RawMessage(raw), metadataFingerprint(raw)
		} else {
			row, err := s.projectCanonicalMetadata(ctx, tx, workspace, key, size)
			if err != nil {
				return result, err
			}
			item.Version, item.Data = row.version, row.summary
			item.Omitted, item.Detail = true, &key
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return result, err
		}
		if len(encoded)+1 > available {
			item.Data, item.Omitted, item.Detail = nil, true, &key
			encoded, _ = json.Marshal(item)
		}
		remaining -= len(encoded) + 1
		if remaining < 0 {
			return result, ErrMetadataPageBudget
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// ReadCanonicalRecordPage is the unfiltered hydration primitive for P15.
// Its opaque cursor is private to this trusted adapter; serving a viewer's
// page additionally requires P03/P10's policy, epoch and token binding.
func (s *SQLiteStore) ReadCanonicalRecordPage(ctx context.Context, workspace string, q PageRequest) (RecordPage, error) {
	page := RecordPage{Items: []MetadataRecord{}}
	limit, _, err := metadataPageLimits(q)
	if err != nil {
		return page, err
	}
	if workspace == "" || !validMetadataKey(RecordKey{Kind: q.Kind}) || q.Filter != "" || q.Order != "" && q.Order != "collection" {
		return page, ErrMetadataRecordQuery
	}
	limit = min(limit, MetadataRecordBatchMaxKeys)
	binding := catalogCursorBinding(workspace, q)
	cursor, err := decodeMetadataCatalogCursor(q.Cursor, binding, "canonical")
	if err != nil {
		return page, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	where, args := `workspace_key=? AND field=?`, []any{workspace, q.Kind}
	if cursor.Schema != 0 {
		where += ` AND (collection_order>? OR (collection_order=? AND record_key>?))`
		args = append(args, cursor.Ordinal, cursor.Ordinal, cursor.ID)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `SELECT record_key,collection_order FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	keys, ordinals := []RecordKey{}, []int64{}
	for rows.Next() {
		var id string
		var ordinal int64
		if err := rows.Scan(&id, &ordinal); err != nil {
			rows.Close()
			return page, err
		}
		keys, ordinals = append(keys, RecordKey{Kind: q.Kind, ID: id}), append(ordinals, ordinal)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	page.HasMore = len(keys) > limit
	if page.HasMore {
		keys = keys[:limit]
	}
	batch, err := s.readCanonicalRecords(ctx, tx, workspace, keys, "detail")
	if err != nil {
		return page, err
	}
	page.Items = batch.Items
	if q.MaxBytes > 0 && q.MaxBytes < MetadataRecordPageMaxBytes {
		// Small hydration budgets retain keys and versions rather than
		// splitting a record's JSON across pages.
		remaining := q.MaxBytes - metadataPageOverhead
		for i := range page.Items {
			item := &page.Items[i]
			raw, _ := json.Marshal(item)
			if len(raw)+1 > remaining {
				item.Data, item.Omitted, item.Detail = nil, true, &item.Key
				raw, _ = json.Marshal(item)
			}
			if len(raw)+1 > remaining {
				if i == 0 {
					return page, ErrMetadataPageBudget
				}
				page.Items, page.HasMore = page.Items[:i], true
				break
			}
			remaining -= len(raw) + 1
		}
	}
	if page.HasMore && len(page.Items) > 0 {
		last := len(page.Items) - 1
		raw, _ := json.Marshal(metadataCatalogCursor{Schema: 2, Binding: binding, Revision: "canonical", ID: keys[last].ID, Ordinal: ordinals[last]})
		page.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

// canonicalMetadataReader streams a single snapshot's BLOB in fixed chunks.
// A huge document, history or trash payload never becomes one Go allocation.
type canonicalMetadataReader struct {
	ctx             context.Context
	tx              *sqlTx
	workspace       string
	key             RecordKey
	size, offset    int64
	buffer          []byte
	captureLeft     int
	boundedCapture  bool
	captureExceeded bool
}

func (r *canonicalMetadataReader) Read(p []byte) (int, error) {
	if len(r.buffer) == 0 {
		if r.offset >= r.size {
			return 0, io.EOF
		}
		if r.boundedCapture && r.captureLeft <= 0 {
			r.captureExceeded = true
			return 0, ErrMetadataCatalogGap
		}
		n := min(int64(metadataRecordChunkBytes), r.size-r.offset)
		if r.boundedCapture {
			n = min(n, int64(r.captureLeft))
		}
		if err := r.tx.QueryRowContext(r.ctx, `SELECT SUBSTR(data,?,?) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, r.offset+1, n, r.workspace, r.key.Kind, r.key.ID).Scan(&r.buffer); err != nil {
			return 0, err
		}
		if len(r.buffer) == 0 {
			return 0, io.ErrUnexpectedEOF
		}
		r.offset += int64(len(r.buffer))
		if r.boundedCapture {
			r.captureLeft -= len(r.buffer)
		}
	}
	n := copy(p, r.buffer)
	r.buffer = r.buffer[n:]
	return n, nil
}

func catalogCursorBinding(workspace string, q PageRequest) string {
	return metadataFingerprint([]byte(fmt.Sprintf("2\x00%s\x00%s\x00%s\x00%s", workspace, q.Kind, q.Filter, q.Order)))
}

func decodeMetadataCatalogCursor(raw, binding, revision string) (metadataCatalogCursor, error) {
	var cursor metadataCatalogCursor
	if raw == "" {
		return cursor, nil
	}
	if len(raw) > 2048 {
		return cursor, ErrMetadataCursor
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Schema != 2 || cursor.Binding != binding || cursor.Revision != revision || len(cursor.ID) > 191 {
		return metadataCatalogCursor{}, ErrMetadataCursor
	}
	return cursor, nil
}
