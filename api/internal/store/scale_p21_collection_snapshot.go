package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"flow/api/internal/domain"
)

type metadataCollectionSnapshot struct {
	store     *SQLiteStore
	tx        *sqlTx
	workspace string
	root      *metadataSnapshotReaderAt
}

// MetadataCollectionsInSnapshot borrows a caller-owned repeatable-read
// transaction and a live work lease of at least 2 MiB. With no kinds, it
// exposes only root-field occurrences. It never queues for
// admission, creates spools, or closes the transaction or lease. The caller
// keeps both alive until Close and uses this transaction for callback SQL.
// Callback contexts route store SQL to tx and retain the parent's lease.
// Fresh authority checks use the original context after the caller closes tx.
// Readers support io.ReadSeeker and io.ReaderAt, and must stay in callbacks.
func (s *SQLiteStore) MetadataCollectionsInSnapshot(ctx context.Context, tx *sqlTx, workspace string, kinds ...string) (_ *MetadataCollectionRead, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tx == nil || tx.database != s.db || tx.dialect != s.dialect || workspace == "" || len(kinds) > 50 {
		return nil, ErrMetadataRecordQuery
	}
	lease, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
	if !ok || s.metadataResidency == nil || lease.budget != s.metadataResidency.work {
		return nil, ErrMetadataWorkBudget
	}
	lease.budget.mu.Lock()
	admitted := lease.live && lease.bytes >= 2<<20
	lease.budget.mu.Unlock()
	if !admitted {
		return nil, ErrMetadataWorkBudget
	}
	snapshot := &metadataCollectionSnapshot{store: s, tx: tx, workspace: workspace}
	callbackCtx := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
	r := &MetadataCollectionRead{ctx: callbackCtx, collections: map[string]*metadataDetachedCollection{}, snapshot: snapshot}
	for _, kind := range kinds {
		field, ok := metadataFieldByName(kind)
		if !ok || !validMetadataKey(RecordKey{Kind: kind}) {
			return nil, ErrMetadataRecordQuery
		}
		t := reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type
		object := t.Kind() == reflect.Map && t.Key().Kind() == reflect.String ||
			kind == "workspaceSettings" && t.Kind() == reflect.Struct
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 || t.Kind() != reflect.Slice && !object {
			return nil, ErrMetadataRecordQuery
		}
		r.collections[kind] = &metadataDetachedCollection{array: t.Kind() == reflect.Slice, inlineNil: true, shape: MetadataArrayShape{Nil: true}}
	}
	if s.dialect == "postgres" && strings.ContainsRune(workspace, '\x00') {
		return r, nil
	}
	rootLength := "LENGTH(data)"
	if s.dialect == "sqlite" {
		rootLength = "LENGTH(CAST(data AS BLOB))"
	}
	err = tx.QueryRowContext(ctx, `SELECT `+rootLength+` FROM workspace_states WHERE `+s.metadataRecordKeyEquals("workspace_key", "?"), workspace).Scan(&r.rootSize)
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	r.stamp, err = readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	snapshot.root = &metadataSnapshotReaderAt{ctx: callbackCtx, snapshot: snapshot, size: r.rootSize}
	for _, field := range r.collections {
		field.shape.WorkspaceFound = true
	}
	if err := r.scanRoot(nil); err != nil {
		return nil, err
	}
	for kind, field := range r.collections {
		if !field.manifestSeen {
			continue
		}
		if err := snapshot.scanHeaders(ctx, kind, func(key RecordKey, size int64) error {
			if len(key.ID) > 191 || size < 0 {
				return ErrMetadataRecordQuery
			}
			if field.manifest != "array" && field.manifest != "map" || field.array != (field.manifest == "array") {
				return fmt.Errorf("%w: collection %s has shape %q", ErrMetadataRecordQuery, kind, field.manifest)
			}
			if err := validateMetadataCollectionJSON(ctx, r.rowSource(kind, key.ID, metadataCollectionRange{size: size})); err != nil {
				return err
			}
			field.count++
			return nil
		}); err != nil {
			return nil, err
		}
		if field.count > 0 {
			field.shape.FieldPresent, field.shape.Nil = true, false
		}
	}
	return r, nil
}

func (r *MetadataCollectionRead) rootSource() io.ReaderAt {
	if r.snapshot != nil {
		return r.snapshot.root
	}
	return r.root
}

func (r *MetadataCollectionRead) rowSource(kind, key string, part metadataCollectionRange) *io.SectionReader {
	if r.snapshot != nil {
		return io.NewSectionReader(&metadataSnapshotReaderAt{ctx: r.ctx, snapshot: r.snapshot, key: RecordKey{Kind: kind, ID: key}, size: part.size}, 0, part.size)
	}
	return io.NewSectionReader(r.data, part.offset, part.size)
}

// ReplayMetadataReader opens an independent cursor over a callback reader.
// It has the same callback lifetime as its input; it retains no source bytes.
func ReplayMetadataReader(reader io.Reader, encodedBytes int64) (*io.SectionReader, error) {
	source, ok := reader.(io.ReaderAt)
	if !ok || encodedBytes < 0 {
		return nil, ErrMetadataRecordQuery
	}
	return io.NewSectionReader(source, 0, encodedBytes), nil
}

type metadataSnapshotReaderAt struct {
	ctx      context.Context
	snapshot *metadataCollectionSnapshot
	key      RecordKey
	size     int64
}

func (r *metadataSnapshotReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	snapshot := r.snapshot
	data := "data"
	if snapshot.tx.dialect == "sqlite" {
		data = "CAST(data AS BLOB)"
	}
	where := snapshot.store.metadataRecordKeyEquals("workspace_key", "?")
	table := "workspace_states"
	if r.key.Kind != "" {
		table = "workspace_metadata_records"
		where += " AND " + snapshot.store.metadataRecordKeyEquals("field", "?") +
			" AND " + snapshot.store.metadataRecordKeyEquals("record_key", "?")
	}
	n := 0
	for n < len(p) && offset+int64(n) < r.size {
		size := min(int64(metadataRecordChunkBytes), int64(len(p)-n), r.size-offset-int64(n))
		args := []any{offset + int64(n) + 1, size, snapshot.workspace}
		if r.key.Kind != "" {
			args = append(args, r.key.Kind, r.key.ID)
		}
		var chunk []byte
		if err := snapshot.tx.QueryRowContext(r.ctx, "SELECT SUBSTR("+data+",?,?) FROM "+table+" WHERE "+where, args...).Scan(&chunk); err != nil {
			return n, err
		}
		if int64(len(chunk)) != size {
			return n, io.ErrUnexpectedEOF
		}
		n += copy(p[n:], chunk)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (r *metadataCollectionSnapshot) scanHeaders(ctx context.Context, kind string, visit func(RecordKey, int64) error) error {
	keyOrder, keyBound, bodyLength := "record_key", "?", "LENGTH(data)"
	switch r.tx.dialect {
	case "sqlite":
		keyOrder, keyBound = "CAST(record_key AS BLOB)", "CAST(? AS BLOB)"
		bodyLength = "LENGTH(CAST(data AS BLOB))"
	case "mysql":
		keyOrder, keyBound = "CAST(record_key AS BINARY)", "CAST(? AS BINARY)"
	case "postgres":
		keyOrder, keyBound = `record_key COLLATE "C"`, `? COLLATE "C"`
	}
	last, started, ordinal := "", false, int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		where := r.store.metadataRecordKeyEquals("workspace_key", "?") + " AND " + r.store.metadataRecordKeyEquals("field", "?")
		args := []any{r.workspace, kind}
		if started {
			where += " AND (collection_order>? OR (collection_order=? AND " + keyOrder + ">" + keyBound + "))"
			args = append(args, ordinal, ordinal, last)
		}
		keyLength := "LENGTH(record_key)"
		if r.tx.dialect == "sqlite" {
			keyLength = "LENGTH(CAST(record_key AS BLOB))"
		} else if r.tx.dialect == "postgres" {
			keyLength = "OCTET_LENGTH(record_key)"
		}
		// Check lengths before scanning text keys into memory. Key payloads
		// remain bounded even for manually imported oversized identifiers.
		var invalid int
		err := r.tx.QueryRowContext(ctx, "SELECT 1 FROM workspace_metadata_records WHERE "+where+" AND "+keyLength+">191 LIMIT 1", args...).Scan(&invalid)
		if err == nil {
			return ErrMetadataRecordQuery
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		rows, err := r.tx.QueryContext(ctx, "SELECT record_key,collection_order,"+bodyLength+" FROM workspace_metadata_records WHERE "+where+" ORDER BY collection_order,"+keyOrder+" LIMIT 50", args...)
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
			if err := visit(RecordKey{Kind: kind, ID: item.id}, item.size); err != nil {
				return err
			}
		}
		last, started, ordinal = page[len(page)-1].id, true, page[len(page)-1].order
	}
}
