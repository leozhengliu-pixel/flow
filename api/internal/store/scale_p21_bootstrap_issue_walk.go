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

	"flow/api/internal/domain"
)

const bootstrapIssuePayloadBatchRows = 32
const bootstrapIssuePayloadBatchBytes = 256 << 10

// The bootstrap owner supplies admitted raw reference inputs and keeps this
// matching SQL snapshot open through every synchronous row callback.
func (s *SQLiteStore) walkBoundedBootstrapIssueRecords(ctx context.Context, q IssueRecordQuery, visit func(domain.Issue) error) error {
	return s.walkBoundedBootstrapIssueRows(ctx, q, false, visit)
}

func (s *SQLiteStore) walkBoundedBootstrapIssueOutlines(ctx context.Context, workspace string, visit func(domain.Issue) error) error {
	return s.walkBoundedBootstrapIssueRows(ctx, IssueRecordQuery{Workspace: workspace}, true, visit)
}

func (s *SQLiteStore) walkBoundedBootstrapIssueRows(ctx context.Context, q IssueRecordQuery, outline bool, visit func(domain.Issue) error) error {
	if s == nil || !s.BoundedMetadataEnabled() || visit == nil {
		return ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	cursor := &ResourceRead{store: s}
	defer cursor.Close()
	queryCost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(q), 0)
	if err != nil {
		return err
	}
	if queryCost > (metadataWorkBytes-(64<<10))/8 {
		return ErrMetadataWorkBudget
	}
	if err := cursor.reserve(ctx, queryCost*8+64<<10); err != nil {
		return err
	}
	var where, prefix string
	var args, prefixArgs []any
	if !outline {
		where, args, err = issueRecordWhere(q)
		if err != nil {
			return err
		}
		prefix, prefixArgs = issueAccessCTE(q)
	}
	snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead)
	if !ok || snapshot.database != s.db || snapshot.workspace != q.Workspace || snapshot.reader == nil ||
		snapshot.reader.database != s.db || snapshot.reader.dialect != s.dialect {
		return ErrBoundedMetadataCoverage
	}
	workspace, body := q.Workspace, "i.data"
	var refs issueReferences
	if outline {
		workspace = snapshot.metadata.Workspace.URLKey
		where, args = "i.workspace_key=?", []any{workspace}
		body = "COALESCE(i.list_data,i.data)"
	} else {
		if err := reserveBootstrapIssueReferences(ctx, cursor, snapshot.metadata); err != nil {
			return err
		}
		refs = newIssueReferences(snapshot.metadata)
	}
	tx := snapshot.reader
	length := cursor.byteLength
	var maxID int64
	if err := tx.QueryRowContext(ctx, prefix+"SELECT COALESCE(MAX("+length("i.id")+"),0) FROM issue_records i WHERE "+where,
		append(prefixArgs, args...)...).Scan(&maxID); err != nil {
		return err
	}
	if maxID < 0 || maxID > (metadataWorkBytes-4096)/16 {
		return ErrMetadataWorkBudget
	}
	perHeader := uint64(maxID)*8 + 512
	pageRows := min(legacyIssueScanPageRows, max(1, int((256<<10)/perHeader)))
	if err := cursor.reserve(ctx, uint64(pageRows)*perHeader+uint64(maxID)*8+4096); err != nil {
		return err
	}
	lastID, lastOrder, lastBlob, started := "", int64(0), false, false
	for {
		pageWhere := where
		values := make([]any, 0, len(prefixArgs)+len(args)+3)
		values = append(values, prefixArgs...)
		values = append(values, maxID)
		values = append(values, args...)
		if started {
			var id any = lastID
			if s.dialect == "sqlite" && lastBlob {
				id = []byte(lastID)
			}
			pageWhere += " AND (i.collection_order,i.id)>(?,?)"
			values = append(values, lastOrder, id)
		}
		storage := "0"
		if s.dialect == "sqlite" {
			storage = "CASE WHEN typeof(i.id)='blob' THEN 1 ELSE 0 END"
		}
		query := prefix + "SELECT CASE WHEN " + length("i.id") + "<=? THEN i.id ELSE NULL END,i.collection_order," +
			length("i.id") + "," + length(body) + "," + storage +
			" FROM issue_records i WHERE " + pageWhere + " ORDER BY i.collection_order,i.id LIMIT " + strconv.Itoa(pageRows)
		headers, err := bootstrapIssueWalkHeaders(ctx, tx, query, values, maxID, pageRows)
		if err != nil {
			return err
		}
		if len(headers) == 0 {
			return ctx.Err()
		}
		for start := 0; start < len(headers); {
			end, size := start+1, headers[start].size
			for end < len(headers) && end-start < bootstrapIssuePayloadBatchRows && size <= bootstrapIssuePayloadBatchBytes &&
				headers[end].size <= bootstrapIssuePayloadBatchBytes-size {
				size += headers[end].size
				end++
			}
			if err := s.visitBootstrapIssueWalkBatch(ctx, tx, workspace, headers[start:end], size, refs, outline, visit); err != nil {
				return err
			}
			start = end
		}
		last := headers[len(headers)-1]
		lastID, lastOrder, lastBlob, started = strings.Clone(last.id), last.order, last.blob, true
		if len(headers) < pageRows {
			return ctx.Err()
		}
	}
}

func reserveBootstrapIssueReferences(ctx context.Context, owner *ResourceRead, data domain.Bootstrap) error {
	cost := uint64(4096)
	for _, family := range []struct {
		count int
		size  uintptr
	}{
		{len(data.Teams), reflect.TypeFor[domain.Team]().Size()},
		{len(data.States), reflect.TypeFor[domain.WorkflowState]().Size()},
		{len(data.Users), reflect.TypeFor[domain.User]().Size()},
		{len(data.Labels), reflect.TypeFor[domain.IssueLabel]().Size()},
		{len(data.Projects), reflect.TypeFor[domain.ProjectSummary]().Size()},
	} {
		// Root ownership already covers referenced strings and descendants.
		// Allow map buckets, growth and copied value structs before allocation.
		perEntry := uint64(family.size)*4 + 256
		if uint64(family.count) > (metadataWorkBytes-cost)/perEntry {
			return ErrMetadataWorkBudget
		}
		cost += uint64(family.count) * perEntry
	}
	return owner.reserve(ctx, cost)
}

func bootstrapIssueWalkHeaders(ctx context.Context, tx *sqlTx, query string, args []any, maxID int64, limit int) ([]legacyIssueScanHeader, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	headers := make([]legacyIssueScanHeader, 0, limit)
	for rows.Next() {
		var header legacyIssueScanHeader
		var id sql.NullString
		var keySize, bodySize sql.NullInt64
		if err := rows.Scan(&id, &header.order, &keySize, &bodySize, &header.blob); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !id.Valid || !keySize.Valid || !bodySize.Valid || keySize.Int64 < 0 || keySize.Int64 > maxID ||
			int64(len(id.String)) != keySize.Int64 || bodySize.Int64 < 0 || len(headers) >= limit {
			_ = rows.Close()
			return nil, io.ErrUnexpectedEOF
		}
		header.id, header.size = id.String, bodySize.Int64
		headers = append(headers, header)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return headers, nil
}

func (s *SQLiteStore) visitBootstrapIssueWalkBatch(ctx context.Context, tx *sqlTx, workspace string, headers []legacyIssueScanHeader, size int64, refs issueReferences, outline bool, visit func(domain.Issue) error) error {
	if len(headers) == 0 || len(headers) > bootstrapIssuePayloadBatchRows || size < 0 ||
		len(headers) > 1 && size > bootstrapIssuePayloadBatchBytes {
		return ErrMetadataWorkBudget
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	wire := &ResourceRead{store: s}
	defer wire.Close()
	fixed := uint64(64<<10) + uint64(len(headers))*512
	if uint64(size) > (metadataWorkBytes-fixed)/3 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, fixed+uint64(size)*3); err != nil {
		return err
	}
	body := "data"
	if outline {
		body = "COALESCE(list_data,data)"
	}
	length := wire.byteLength(body)
	queries, args := make([]string, 0, len(headers)), make([]any, 0, len(headers)*3)
	for index, header := range headers {
		var id any = header.id
		if s.dialect == "sqlite" && header.blob {
			id = []byte(header.id)
		}
		queries = append(queries, "SELECT "+strconv.Itoa(index)+" AS ordinal,CASE WHEN "+length+"<=? THEN "+body+
			" ELSE NULL END AS data,"+length+" AS row_bytes FROM issue_records WHERE workspace_key=? AND id=?")
		args = append(args, header.size, workspace, id)
	}
	rows, err := tx.QueryContext(ctx, strings.Join(queries, " UNION ALL ")+" ORDER BY ordinal", args...)
	if err != nil {
		return err
	}
	bodies := make([][]byte, 0, len(headers))
	defer func() {
		for _, raw := range bodies {
			clear(raw)
		}
	}()
	for rows.Next() {
		var ordinal int
		var raw []byte
		var actual sql.NullInt64
		if err := rows.Scan(&ordinal, &raw, &actual); err != nil {
			_ = rows.Close()
			return err
		}
		if ordinal != len(bodies) || ordinal >= len(headers) || !actual.Valid ||
			actual.Int64 != headers[ordinal].size || int64(len(raw)) != headers[ordinal].size {
			clear(raw)
			_ = rows.Close()
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
		err := s.visitBootstrapIssueWalkRow(ctx, raw, refs, outline, visit)
		clear(raw)
		bodies[index] = nil
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *SQLiteStore) visitBootstrapIssueWalkRow(ctx context.Context, raw []byte, refs issueReferences, outline bool, visit func(domain.Issue) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owner := &ResourceRead{store: s}
	defer owner.Close()
	work, err := resourceWindowTypedJSONWork(ctx, raw, reflect.TypeFor[domain.Issue]())
	if err != nil {
		return err
	}
	if err := owner.reserve(ctx, work); err != nil {
		return err
	}
	var issue domain.Issue
	if err := json.Unmarshal(raw, &issue); err != nil {
		return err
	}
	normalizeIssueRecord(&issue)
	// resolve clones the labels and materializes a few reference pointers;
	// their deep values remain owned by the bootstrap's raw input lifetime.
	overlay := uint64(4096) + uint64(len(issue.Labels))*uint64(reflect.TypeFor[domain.IssueLabel]().Size())*2 +
		uint64(reflect.TypeFor[domain.User]().Size())*4 + uint64(reflect.TypeFor[domain.ProjectSummary]().Size())*2
	if outline {
		overlay += uint64(len(issue.Reactions)) * 256
	}
	if err := owner.reserve(ctx, overlay); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if outline {
		issue = issueListProjection(issue)
	} else {
		issue = refs.resolve(issue)
	}
	if err := visit(issue); err != nil {
		return err
	}
	return ctx.Err()
}
