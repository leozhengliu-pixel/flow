package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

const legacyIssueScanPageRows = 50

// legacyIssueRow owns one complete row from the literal legacy issue stream.
// Its physical SQL identity remains separate from the JSON-owned issue ID.
type legacyIssueRow struct {
	PhysicalID string
	Record     storedIssueRecord

	mu          sync.Mutex
	owner       *ResourceRead
	transferred bool
	idBlob      bool
}

type legacyIssueScanHeader struct {
	id    string
	order int64
	size  int64
	blob  bool
}

func (r *legacyIssueRow) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.Record.raw)
	r.PhysicalID, r.Record = "", storedIssueRecord{}
	r.owner.Close()
	r.owner = nil
}

// TransferOutput extends a matched row through the containing output lifetime.
// Unmatched rows are closed automatically when their callback returns.
func (r *legacyIssueRow) TransferOutput(ctx context.Context) error {
	if r == nil {
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	r.mu.Lock()
	if r.owner == nil {
		r.mu.Unlock()
		return errMetadataLogicalClosed
	}
	if r.transferred {
		r.mu.Unlock()
		return nil
	}
	r.transferred = true
	r.mu.Unlock()
	if !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

func (r *legacyIssueRow) finish() {
	r.mu.Lock()
	transferred := r.transferred
	r.mu.Unlock()
	if !transferred {
		r.Close()
	}
}

// scanLegacyIssueRows preserves readIssueRecordsWithBase's complete normalized
// row stream without retaining all IDs or issue values. Header pages contain
// at most 50 locators; payload batches are byte-bounded and closed before visit.
func (s *SQLiteStore) scanLegacyIssueRows(ctx context.Context, tx *sqlTx, workspace string, visit func(*legacyIssueRow) error) error {
	if s == nil || tx == nil || tx.database != s.db || tx.dialect != s.dialect {
		return ErrBoundedMetadataCoverage
	}
	if visit == nil {
		return ErrMetadataRecordQuery
	}
	if uint64(len(workspace)) > (metadataWorkBytes-(64<<10))/8 {
		return ErrMetadataWorkBudget
	}
	cursor := &ResourceRead{store: s}
	defer cursor.Close()
	if err := cursor.reserve(ctx, uint64(len(workspace))*8+64<<10); err != nil {
		return err
	}
	idLength, bodyLength := cursor.byteLength("id"), cursor.byteLength("data")
	var maxID, maxBody int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+idLength+"),0),COALESCE(MAX("+bodyLength+"),0) FROM issue_records WHERE workspace_key=?", workspace).Scan(&maxID, &maxBody); err != nil {
		return err
	}
	if maxID < 0 || maxBody < 0 || maxBody > metadataWorkBytes/16 || maxID > (metadataWorkBytes-4096)/8 {
		return ErrMetadataWorkBudget
	}
	// Large physical keys reduce page width instead of multiplying one key's
	// maximum by 50. The same allowance retains the native continuation key.
	perHeader := uint64(maxID)*8 + 512
	pageRows := min(legacyIssueScanPageRows, max(1, int((256<<10)/perHeader)))
	headerWork := uint64(pageRows)*perHeader + uint64(maxID)*8 + 4096
	if headerWork > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	if err := cursor.reserve(ctx, headerWork); err != nil {
		return err
	}
	lastID, lastOrder, lastBlob, started := "", int64(0), false, false
	for {
		headers, err := s.legacyIssueScanHeaders(ctx, tx, workspace, maxID, maxBody, lastID, lastOrder, lastBlob, started, pageRows)
		if err != nil {
			return err
		}
		if len(headers) == 0 {
			return ctx.Err()
		}
		for start := 0; start < len(headers); {
			end, size := start+1, headers[start].size
			for end < len(headers) && size <= issueMutationRowsBatchBytes && headers[end].size <= issueMutationRowsBatchBytes-size {
				size += headers[end].size
				end++
			}
			if err := s.visitLegacyIssueBatch(ctx, tx, workspace, headers[start:end], size, visit); err != nil {
				return err
			}
			start = end
		}
		last := headers[len(headers)-1]
		lastID, lastOrder, lastBlob, started = strings.Clone(last.id), last.order, last.blob, true
		if len(headers) < pageRows {
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func (s *SQLiteStore) legacyIssueScanHeaders(ctx context.Context, tx *sqlTx, workspace string, maxID, maxBody int64, lastID string, lastOrder int64, lastBlob, started bool, pageRows int) ([]legacyIssueScanHeader, error) {
	where, args := "workspace_key=?", []any{maxID, workspace}
	if started {
		id := any(lastID)
		if s.dialect == "sqlite" && lastBlob {
			id = []byte(lastID)
		}
		where += " AND (collection_order,id)>(?,?)"
		args = append(args, lastOrder, id)
	}
	storage := "0"
	if s.dialect == "sqlite" {
		storage = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END"
	}
	length := (&ResourceRead{store: s}).byteLength
	query := "SELECT CASE WHEN " + length("id") + "<=? THEN id ELSE NULL END,collection_order," + length("id") + "," + length("data") + "," + storage +
		" FROM issue_records WHERE " + where + " ORDER BY collection_order,id LIMIT " + strconv.Itoa(pageRows)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	headers := make([]legacyIssueScanHeader, 0, pageRows)
	for rows.Next() {
		var physical sql.NullString
		var idSize, bodySize sql.NullInt64
		var header legacyIssueScanHeader
		if err := rows.Scan(&physical, &header.order, &idSize, &bodySize, &header.blob); err != nil {
			rows.Close()
			return nil, err
		}
		if !physical.Valid || !idSize.Valid || !bodySize.Valid || idSize.Int64 < 0 || bodySize.Int64 < 0 ||
			idSize.Int64 > maxID || bodySize.Int64 > maxBody || int64(len(physical.String)) != idSize.Int64 || len(headers) >= pageRows {
			rows.Close()
			return nil, io.ErrUnexpectedEOF
		}
		header.id, header.size = physical.String, bodySize.Int64
		headers = append(headers, header)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return headers, nil
}

func (s *SQLiteStore) visitLegacyIssueBatch(ctx context.Context, tx *sqlTx, workspace string, headers []legacyIssueScanHeader, size int64, visit func(*legacyIssueRow) error) error {
	if len(headers) == 0 || len(headers) > legacyIssueScanPageRows || size < 0 || uint64(size) > (metadataWorkBytes-(64<<10)-uint64(len(headers))*512)/3 {
		return ErrMetadataWorkBudget
	}
	wire := &ResourceRead{store: s}
	defer wire.Close()
	if err := wire.reserve(ctx, uint64(size)*3+uint64(len(headers))*512+64<<10); err != nil {
		return err
	}
	queries, args := make([]string, 0, len(headers)), make([]any, 0, len(headers)*3)
	for index, header := range headers {
		id := any(header.id)
		if s.dialect == "sqlite" && header.blob {
			id = []byte(header.id)
		}
		queries = append(queries, "SELECT "+strconv.Itoa(index)+" AS ordinal,CASE WHEN "+wire.byteLength("data")+"<=? THEN data ELSE NULL END AS data,"+wire.byteLength("data")+" AS row_bytes FROM issue_records WHERE workspace_key=? AND id=?")
		args = append(args, header.size, workspace, id)
	}
	rows, err := tx.QueryContext(ctx, strings.Join(queries, " UNION ALL ")+" ORDER BY ordinal", args...)
	if err != nil {
		return err
	}
	bodies := make([][]byte, 0, len(headers))
	for rows.Next() {
		var ordinal int
		var raw []byte
		var size sql.NullInt64
		if err := rows.Scan(&ordinal, &raw, &size); err != nil {
			rows.Close()
			return err
		}
		if ordinal != len(bodies) || ordinal >= len(headers) || !size.Valid || size.Int64 != headers[ordinal].size || int64(len(raw)) != headers[ordinal].size {
			rows.Close()
			return io.ErrUnexpectedEOF
		}
		bodies = append(bodies, raw)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(bodies) != len(headers) {
		return io.ErrUnexpectedEOF
	}
	for index, raw := range bodies {
		row, err := s.decodeLegacyIssueRow(ctx, headers[index], raw)
		if err != nil {
			return err
		}
		err = func() error {
			defer row.finish()
			return visit(row)
		}()
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) decodeLegacyIssueRow(ctx context.Context, header legacyIssueScanHeader, raw []byte) (_ *legacyIssueRow, err error) {
	work, err := metadataAggregatePayloadJSONWork(ctx, raw)
	if err != nil {
		return nil, err
	}
	fixed := uint64(reflect.TypeFor[storedIssueRecord]().Size())*2 + 4096
	cost := uint64(len(raw))*3 + uint64(len(header.id))*8 + fixed
	if cost > metadataWorkBytes || work > metadataWorkBytes-cost {
		return nil, ErrMetadataWorkBudget
	}
	row := &legacyIssueRow{owner: &ResourceRead{store: s}, idBlob: header.blob}
	defer func() {
		if err != nil {
			row.Close()
		}
	}()
	if err = row.owner.reserve(ctx, cost+work); err != nil {
		return nil, err
	}
	row.PhysicalID, row.Record = header.id, storedIssueRecord{raw: raw, order: header.order}
	if err = json.Unmarshal(raw, &row.Record.issue); err != nil {
		return nil, err
	}
	normalizeIssueRecord(&row.Record.issue)
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return row, nil
}
