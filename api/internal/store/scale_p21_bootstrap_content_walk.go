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

const bootstrapContentHeaderRows = 64
const bootstrapContentHeaderWork = 256 << 10
const bootstrapContentBodyRows = 32
const bootstrapContentBodyBytes = 256 << 10

type bootstrapContentReader interface {
	metadataReader
	metadataRowReader
}

type bootstrapContentHeader struct {
	resource, created, id             string
	resourceBlob, createdBlob, idBlob bool
	size                              int64
}

func bootstrapContentKey(store *SQLiteStore, value string, blob bool) any {
	if store.dialect == "sqlite" && blob {
		return []byte(value)
	}
	return value
}

// The visitor borrows resource/raw through its synchronous call. Retained
// decoded values, maps and identifiers require a separate caller-owned lease.
func (s *SQLiteStore) walkBoundedBootstrapContentRecords(ctx context.Context, workspace, kind string, visit func(string, json.RawMessage) error) (err error) {
	if s == nil || !s.BoundedMetadataEnabled() {
		return ErrBoundedMetadataCoverage
	}
	if visit == nil {
		return ErrMetadataRecordQuery
	}
	visitorFailed := false
	defer func() {
		if err != nil && !visitorFailed && ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	fixed := &ResourceRead{store: s}
	defer fixed.Close()
	bytes := uint64(len(workspace)) + uint64(len(kind))
	if bytes > (metadataWorkBytes-(64<<10))/8 {
		return ErrMetadataWorkBudget
	}
	if err := fixed.reserve(ctx, bytes*8+(64<<10)); err != nil {
		return err
	}
	var reader bootstrapContentReader
	if held, borrowed := ctx.Value(workspaceReadKey{}).(workspaceRead); borrowed && held.workspace == workspace {
		if held.reader == nil {
			return ErrMetadataRecordQuery
		}
		reader = held.reader
	} else {
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return err
		}
		defer tx.Rollback()
		reader = tx
	}
	var last *bootstrapContentHeader
	var cursor *ResourceRead
	defer func() {
		if cursor != nil {
			cursor.Close()
		}
	}()
	for {
		headers, page, err := s.bootstrapContentHeaders(ctx, reader, workspace, kind, last)
		if err != nil {
			return err
		}
		if len(headers) == 0 {
			page.Close()
			return ctx.Err()
		}
		for start := 0; start < len(headers); {
			end, size := start+1, headers[start].size
			for end < len(headers) && end-start < bootstrapContentBodyRows &&
				size <= bootstrapContentBodyBytes && headers[end].size <= bootstrapContentBodyBytes-size {
				size += headers[end].size
				end++
			}
			err := s.bootstrapContentBodies(ctx, reader, workspace, kind, headers[start:end], size, func(resource string, raw json.RawMessage) error {
				err := visit(resource, raw)
				visitorFailed = err != nil
				return err
			})
			if err != nil {
				page.Close()
				return err
			}
			start = end
		}
		header := headers[len(headers)-1]
		next := &ResourceRead{store: s}
		size := uint64(len(header.resource)) + uint64(len(header.created)) + uint64(len(header.id))
		if err := next.reserve(ctx, size*8+4096); err != nil {
			next.Close()
			page.Close()
			return err
		}
		header.resource, header.created, header.id = strings.Clone(header.resource), strings.Clone(header.created), strings.Clone(header.id)
		last = &header
		if cursor != nil {
			cursor.Close()
		}
		cursor = next
		page.Close()
	}
}

func (s *SQLiteStore) bootstrapContentHeaders(ctx context.Context, reader bootstrapContentReader, workspace, kind string, last *bootstrapContentHeader) (_ []bootstrapContentHeader, _ *ResourceRead, err error) {
	owner := &ResourceRead{store: s}
	defer func() {
		if err != nil {
			owner.Close()
		}
	}()
	where := "workspace_key=? AND kind=?"
	args := []any{workspace, kind}
	if last != nil {
		where += " AND (resource_id,created_at,id)>(?,?,?)"
		args = append(args, bootstrapContentKey(s, last.resource, last.resourceBlob),
			bootstrapContentKey(s, last.created, last.createdBlob), bootstrapContentKey(s, last.id, last.idBlob))
	}
	length := owner.byteLength
	selection := " FROM workspace_content_records WHERE " + where + " ORDER BY resource_id,created_at,id LIMIT " + strconv.Itoa(bootstrapContentHeaderRows)
	var count, resourceSize, createdSize, idSize int64
	query := "SELECT COUNT(*),COALESCE(MAX(" + length("resource_id") + "),0),COALESCE(MAX(" + length("created_at") + "),0),COALESCE(MAX(" + length("id") + "),0) FROM (SELECT resource_id,created_at,id" + selection + ") bootstrap_content_bounds"
	if err := reader.QueryRowContext(ctx, query, args...).Scan(&count, &resourceSize, &createdSize, &idSize); err != nil {
		return nil, nil, err
	}
	if count == 0 {
		return nil, owner, nil
	}
	if count < 0 || count > bootstrapContentHeaderRows || resourceSize < 0 || createdSize < 0 || idSize < 0 ||
		resourceSize > bootstrapContentHeaderWork || createdSize > bootstrapContentHeaderWork || idSize > bootstrapContentHeaderWork {
		return nil, nil, ErrMetadataWorkBudget
	}
	perRow := uint64(resourceSize+createdSize+idSize)*8 + 512
	if perRow > bootstrapContentHeaderWork-4096 {
		return nil, nil, ErrMetadataWorkBudget
	}
	rowsLimit := min(int(count), int((bootstrapContentHeaderWork-4096)/perRow))
	if err := owner.reserve(ctx, uint64(rowsLimit)*perRow+4096); err != nil {
		return nil, nil, err
	}
	resourceStorage, createdStorage, idStorage := "0", "0", "0"
	if s.dialect == "sqlite" {
		resourceStorage = "CASE WHEN typeof(resource_id)='blob' THEN 1 ELSE 0 END"
		createdStorage = "CASE WHEN typeof(created_at)='blob' THEN 1 ELSE 0 END"
		idStorage = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END"
	}
	query = "SELECT CASE WHEN " + length("resource_id") + "<=? THEN resource_id ELSE NULL END," +
		"CASE WHEN " + length("created_at") + "<=? THEN created_at ELSE NULL END," +
		"CASE WHEN " + length("id") + "<=? THEN id ELSE NULL END," +
		length("data") + "," + resourceStorage + "," + createdStorage + "," + idStorage +
		" FROM workspace_content_records WHERE " + where + " ORDER BY resource_id,created_at,id LIMIT " + strconv.Itoa(rowsLimit)
	values := append([]any{resourceSize, createdSize, idSize}, args...)
	rows, err := reader.QueryContext(ctx, query, values...)
	if err != nil {
		return nil, nil, err
	}
	headers := make([]bootstrapContentHeader, 0, rowsLimit)
	for rows.Next() {
		var resource, created, id sql.NullString
		var size sql.NullInt64
		var header bootstrapContentHeader
		if err := rows.Scan(&resource, &created, &id, &size, &header.resourceBlob, &header.createdBlob, &header.idBlob); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if !resource.Valid || !created.Valid || !id.Valid || !size.Valid || size.Int64 < 0 ||
			int64(len(resource.String)) > resourceSize || int64(len(created.String)) > createdSize || int64(len(id.String)) > idSize ||
			len(headers) >= rowsLimit {
			rows.Close()
			return nil, nil, io.ErrUnexpectedEOF
		}
		header.resource, header.created, header.id, header.size = resource.String, created.String, id.String, size.Int64
		headers = append(headers, header)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	if len(headers) != rowsLimit {
		return nil, nil, io.ErrUnexpectedEOF
	}
	return headers, owner, nil
}

func (s *SQLiteStore) bootstrapContentBodies(ctx context.Context, reader bootstrapContentReader, workspace, kind string, headers []bootstrapContentHeader, size int64, visit func(string, json.RawMessage) error) error {
	wire := &ResourceRead{store: s}
	var bodies [][]byte
	defer func() {
		for _, raw := range bodies {
			clear(raw)
		}
		wire.Close()
	}()
	if len(headers) == 0 || len(headers) > bootstrapContentBodyRows || size < 0 || size > metadataWorkBytes/16 ||
		len(headers) > 1 && size > bootstrapContentBodyBytes {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(size)*3+uint64(len(headers))*512+(64<<10)); err != nil {
		return err
	}
	where := "workspace_key=? AND kind=? AND resource_id=? AND id=?"
	queries, args := make([]string, 0, len(headers)), make([]any, 0, len(headers)*5)
	for index, header := range headers {
		queries = append(queries, "SELECT "+strconv.Itoa(index)+" AS ordinal,CASE WHEN "+wire.byteLength("data")+"<=? THEN data ELSE NULL END AS data,"+
			wire.byteLength("data")+" AS row_bytes FROM workspace_content_records WHERE "+where)
		args = append(args, header.size, workspace, kind, bootstrapContentKey(s, header.resource, header.resourceBlob), bootstrapContentKey(s, header.id, header.idBlob))
	}
	rows, err := reader.QueryContext(ctx, strings.Join(queries, " UNION ALL ")+" ORDER BY ordinal", args...)
	if err != nil {
		return err
	}
	bodies = make([][]byte, 0, len(headers))
	for rows.Next() {
		var ordinal int
		var raw []byte
		var actual sql.NullInt64
		if err := rows.Scan(&ordinal, &raw, &actual); err != nil {
			rows.Close()
			return err
		}
		if ordinal != len(bodies) || ordinal >= len(headers) || !actual.Valid ||
			actual.Int64 != headers[ordinal].size || int64(len(raw)) != headers[ordinal].size {
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
		err := s.bootstrapContentVisit(ctx, kind, headers[index].resource, raw, visit)
		clear(raw)
		bodies[index] = nil
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *SQLiteStore) bootstrapContentVisit(ctx context.Context, kind, resource string, raw []byte, visit func(string, json.RawMessage) error) error {
	row := &ResourceRead{store: s}
	defer row.Close()
	typ := reflect.TypeFor[any]()
	switch kind {
	case "comment":
		typ = reflect.TypeFor[domain.Comment]()
	case "activity":
		typ = reflect.TypeFor[domain.ActivityEvent]()
	case "notification":
		typ = reflect.TypeFor[domain.Notification]()
	case "delivery":
		typ = reflect.TypeFor[domain.NotificationDelivery]()
	}
	work, err := resourceWindowTypedJSONWork(ctx, raw, typ)
	if err != nil {
		var syntax *json.SyntaxError
		if !errors.As(err, &syntax) {
			return err
		}
		// The original walker never validates bytes: an ignored malformed row
		// must still reach the visitor. Its own decoder decides the error.
		work, err = metadataAggregatePayloadJSONWork(ctx, raw)
		if err != nil {
			return err
		}
	}
	if work > (metadataWorkBytes-(64<<10))/3 {
		return ErrMetadataWorkBudget
	}
	if err := row.reserve(ctx, work*3+(64<<10)); err != nil {
		return err
	}
	if err := visit(resource, json.RawMessage(raw)); err != nil {
		return err
	}
	return ctx.Err()
}
