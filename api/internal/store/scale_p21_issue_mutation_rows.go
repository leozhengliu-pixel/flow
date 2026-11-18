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

const (
	issueMutationRowsBatchSize  = 32
	issueMutationRowsBatchBytes = 256 << 10
)

// issueMutationRows owns complete mutation preimages and their raw comparison
// payloads. Its transaction belongs to the caller and outlives this owner.
type issueMutationRows struct {
	mu         sync.Mutex
	owner      *ResourceRead
	tx         *sqlTx
	workspace  string
	results    []map[string]storedIssueRecord
	discussion *discussionRowsOwned
	// rawOnly keeps only the stored payloads (record.raw, record.order):
	// callers decode the issues they need under their own allowance.
	rawOnly bool
}

type issueMutationRowHeader struct {
	id    string
	order int64
	size  int64
	blob  bool
}

func (s *SQLiteStore) newIssueMutationRows(tx *sqlTx, workspace string) *issueMutationRows {
	return &issueMutationRows{owner: &ResourceRead{store: s}, tx: tx, workspace: workspace}
}

func (r *issueMutationRows) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, result := range r.results {
		clear(result)
	}
	r.results = nil
	r.discussion.close()
	r.discussion = nil
	r.tx, r.workspace = nil, ""
	r.owner.Close()
	r.owner = nil
}

// Returned maps remain owned until Close, including across subsequent loads.
// Failed attempts retain their partial results until Close; SQL and decode
// scratch are separate from the complete retained raw/native row owners.
func (r *issueMutationRows) load(ctx context.Context, ids []string, lock bool) (map[string]storedIssueRecord, error) {
	if r == nil {
		return nil, errMetadataLogicalClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.owner == nil {
		return nil, errMetadataLogicalClosed
	}
	if r.owner.store == nil || r.tx == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 1000 {
		return nil, ErrIssueQuery
	}
	recordBytes := uint64(reflect.TypeFor[storedIssueRecord]().Size())*4 + 1024
	cost := uint64(64<<10) + uint64(len(ids))*recordBytes
	if cost > metadataWorkBytes || uint64(len(r.workspace)) > (metadataWorkBytes-cost)/8 {
		return nil, ErrMetadataWorkBudget
	}
	cost += uint64(len(r.workspace)) * 8
	for _, id := range ids {
		if cost > metadataWorkBytes || uint64(len(id)) > (metadataWorkBytes-cost)/8 {
			return nil, ErrMetadataWorkBudget
		}
		cost += uint64(len(id)) * 8
	}
	if err := r.owner.reserve(ctx, cost); err != nil {
		return nil, err
	}
	result := make(map[string]storedIssueRecord, len(ids))
	r.results = append(r.results, result)
	pending := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			owned := strings.Clone(id)
			seen[owned] = true
			pending = append(pending, owned)
		}
	}
	for start := 0; start < len(pending); start += issueMutationRowsBatchSize {
		headers, err := r.headers(ctx, pending[start:min(start+issueMutationRowsBatchSize, len(pending))], lock)
		if err != nil {
			return nil, err
		}
		for next := 0; next < len(headers); {
			end, size := next+1, headers[next].size
			for end < len(headers) && end-next < issueMutationRowsBatchSize && size <= issueMutationRowsBatchBytes && headers[end].size <= issueMutationRowsBatchBytes-size {
				size += headers[end].size
				end++
			}
			if err := r.payloads(ctx, headers[next:end], size, result); err != nil {
				return nil, err
			}
			next = end
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *issueMutationRows) headers(ctx context.Context, ids []string, lock bool) ([]issueMutationRowHeader, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	clause, args := bindList("id", ids)
	where := " FROM issue_records WHERE workspace_key=? AND " + clause
	args = append([]any{r.workspace}, args...)
	length := r.owner.byteLength("id")
	var count, maximum sql.NullInt64
	if err := r.tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(MAX("+length+"),0)"+where, args...).Scan(&count, &maximum); err != nil {
		return nil, err
	}
	if !count.Valid || !maximum.Valid || count.Int64 < 0 || count.Int64 > int64(len(ids)) || maximum.Int64 < 0 {
		return nil, ErrMetadataRecordQuery
	}
	keySlots := uint64(len(ids)) * 8
	cost := uint64(64<<10) + uint64(len(ids))*512
	if uint64(maximum.Int64) > (metadataWorkBytes-cost)/keySlots {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, cost+uint64(maximum.Int64)*keySlots); err != nil {
		return nil, err
	}
	suffix, storage := "", "0"
	if r.owner.store.dialect == "sqlite" {
		storage = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END"
	} else if lock {
		suffix = " FOR UPDATE"
	}
	rows, err := r.tx.QueryContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN id ELSE NULL END,collection_order,"+r.owner.byteLength("data")+","+storage+where+suffix, append([]any{maximum.Int64}, args...)...)
	if err != nil {
		return nil, err
	}
	headers := make([]issueMutationRowHeader, 0, int(count.Int64))
	for rows.Next() {
		var id sql.NullString
		var size sql.NullInt64
		var header issueMutationRowHeader
		if err := rows.Scan(&id, &header.order, &size, &header.blob); err != nil {
			rows.Close()
			return nil, err
		}
		if !id.Valid || !size.Valid || int64(len(id.String)) > maximum.Int64 || int64(len(headers)) >= count.Int64 || size.Int64 < 0 {
			rows.Close()
			return nil, io.ErrUnexpectedEOF
		}
		if size.Int64 > metadataWorkBytes/16 {
			rows.Close()
			return nil, ErrMetadataWorkBudget
		}
		header.id, header.size = id.String, size.Int64
		headers = append(headers, header)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if int64(len(headers)) != count.Int64 {
		return nil, io.ErrUnexpectedEOF
	}
	return headers, nil
}

func (r *issueMutationRows) payloads(ctx context.Context, headers []issueMutationRowHeader, size int64, result map[string]storedIssueRecord) error {
	if len(headers) == 0 || len(headers) > issueMutationRowsBatchSize || size < 0 || uint64(size) > (metadataWorkBytes-(64<<10)-uint64(len(headers))*512)/3 {
		return ErrMetadataWorkBudget
	}
	// SQL guards every payload by its own captured size. This allowance covers
	// raw bytes, driver buffers and Scan copies before the query begins.
	sqlScratch := &ResourceRead{store: r.owner.store}
	sqlTransferred := false
	defer func() {
		if !sqlTransferred {
			sqlScratch.Close()
		}
	}()
	if err := sqlScratch.reserve(ctx, uint64(size)*3+uint64(len(headers))*512+64<<10); err != nil {
		return err
	}
	queries := make([]string, 0, len(headers))
	args := make([]any, 0, len(headers)*3)
	for index, header := range headers {
		id := any(header.id)
		if r.owner.store.dialect == "mysql" || r.owner.store.dialect == "sqlite" && header.blob {
			id = []byte(header.id)
		}
		queries = append(queries, "SELECT "+strconv.Itoa(index)+" AS ordinal,CASE WHEN "+r.owner.byteLength("data")+"<=? THEN data ELSE NULL END AS data,"+r.owner.byteLength("data")+" AS row_bytes FROM issue_records WHERE workspace_key=? AND id=?")
		args = append(args, header.size, r.workspace, id)
	}
	rows, err := r.tx.QueryContext(ctx, strings.Join(queries, " UNION ALL ")+" ORDER BY ordinal", args...)
	if err != nil {
		return err
	}
	bodies := make([][]byte, 0, len(headers))
	for rows.Next() {
		var ordinal int
		var body []byte
		var rowBytes sql.NullInt64
		if err := rows.Scan(&ordinal, &body, &rowBytes); err != nil {
			rows.Close()
			return err
		}
		if ordinal != len(bodies) || ordinal >= len(headers) || !rowBytes.Valid || rowBytes.Int64 != headers[ordinal].size || int64(len(body)) != headers[ordinal].size {
			rows.Close()
			return io.ErrUnexpectedEOF
		}
		bodies = append(bodies, body)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(bodies) != len(headers) {
		return io.ErrUnexpectedEOF
	}
	rawCost := uint64(len(bodies))*uint64(reflect.TypeFor[[]byte]().Size())*2 + 4096
	for _, body := range bodies {
		rawCost += uint64(cap(body))
	}
	rows = nil
	if err := r.owner.reserve(ctx, rawCost); err == nil {
		sqlScratch.Close()
	} else if errors.Is(err, ErrMetadataWorkBudget) {
		// Compaction must not add a refusal under pressure. The original
		// admitted SQL envelope already owns these exact retained bytes.
		r.owner.release = append(r.owner.release, sqlScratch.Close)
		sqlTransferred = true
	} else {
		return err
	}
	for index, body := range bodies {
		if r.rawOnly {
			result[headers[index].id] = storedIssueRecord{raw: body, order: headers[index].order}
			continue
		}
		if err := func() error {
			work, err := metadataAggregatePayloadJSONWork(ctx, body)
			if err != nil {
				return err
			}
			fixed := uint64(reflect.TypeFor[storedIssueRecord]().Size())*2 + 4096
			if work > metadataWorkBytes-fixed {
				return ErrMetadataWorkBudget
			}
			decodeScratch := &ResourceRead{store: r.owner.store}
			decodeTransferred := false
			defer func() {
				if !decodeTransferred {
					decodeScratch.Close()
				}
			}()
			if err := decodeScratch.reserve(ctx, work+fixed); err != nil {
				return err
			}
			record := storedIssueRecord{raw: body, order: headers[index].order}
			if err := json.Unmarshal(body, &record.issue); err != nil {
				return err
			}
			normalizeIssueRecord(&record.issue)
			native, nativeErr := metadataAggregateNativeCost(ctx, reflect.ValueOf(record.issue), 0)
			if nativeErr == nil && native <= metadataWorkBytes-fixed {
				nativeErr = r.owner.reserve(ctx, native+fixed)
			} else if nativeErr == nil {
				nativeErr = ErrMetadataWorkBudget
			}
			if nativeErr != nil {
				if !errors.Is(nativeErr, ErrMetadataWorkBudget) {
					return nativeErr
				}
				// The native meter's depth bound is narrower than the
				// unchanged decoder. Keep its original complete envelope
				// if compact admission cannot represent or fit this row.
				r.owner.release = append(r.owner.release, decodeScratch.Close)
				decodeTransferred = true
			}
			result[headers[index].id] = record
			return nil
		}(); err != nil {
			return err
		}
	}
	return ctx.Err()
}
