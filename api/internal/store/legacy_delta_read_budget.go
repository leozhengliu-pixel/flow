package store

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strconv"
)

type legacyMetadataDeltaReadBudgetKey struct{}

type legacyMetadataDeltaReadBudget struct {
	owner *ResourceRead
}

func legacyMetadataDeltaRowsError(rows *sql.Rows) error {
	readErr, closeErr := rows.Err(), rows.Close()
	if closeErr == nil {
		return readErr
	}
	if readErr == nil {
		return closeErr
	}
	return errors.Join(readErr, closeErr)
}

// The enclosing writer admits its native inputs and locks/admit the root.
// This guard owns canonical rows and headers at their actual SQL read, since
// read-committed transactions may observe new rows after an earlier preflight.
func withLegacyMetadataDeltaReadBudget(ctx context.Context, owner *ResourceRead) context.Context {
	return context.WithValue(ctx, legacyMetadataDeltaReadBudgetKey{}, &legacyMetadataDeltaReadBudget{owner: owner})
}

func legacyMetadataDeltaBudget(ctx context.Context) *legacyMetadataDeltaReadBudget {
	budget, _ := ctx.Value(legacyMetadataDeltaReadBudgetKey{}).(*legacyMetadataDeltaReadBudget)
	return budget
}

func (b *legacyMetadataDeltaReadBudget) check(tx *sqlTx) error {
	if b == nil || b.owner == nil || b.owner.store == nil || !b.owner.store.BoundedMetadataEnabled() ||
		tx == nil || tx.database != b.owner.store.db {
		return ErrBoundedMetadataCoverage
	}
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	if b.owner.closed {
		return errMetadataLogicalClosed
	}
	return nil
}

func (b *legacyMetadataDeltaReadBudget) reserve(ctx context.Context, size uint64) error {
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	if b.owner.closed {
		return errMetadataLogicalClosed
	}
	return b.owner.reserve(ctx, size)
}

func (b *legacyMetadataDeltaReadBudget) headerWire(ctx context.Context, tx *sqlTx) (*ResourceRead, error) {
	if err := b.check(tx); err != nil {
		return nil, err
	}
	wire := &ResourceRead{store: b.owner.store}
	if err := wire.reserve(ctx, MetadataRecordPageMaxBytes*4+64<<10); err != nil {
		wire.Close()
		return nil, err
	}
	return wire, nil
}

func (b *legacyMetadataDeltaReadBudget) headerColumn(column string, limit int) string {
	return "CASE WHEN " + b.owner.byteLength(column) + "<=" + strconv.Itoa(limit) + " THEN " + column + " ELSE NULL END"
}

func (b *legacyMetadataDeltaReadBudget) header(ctx context.Context, field, key sql.NullString) (metadataRecordKey, error) {
	if !field.Valid || !key.Valid {
		return metadataRecordKey{}, ErrMetadataWorkBudget
	}
	if err := b.reserve(ctx, uint64(len(field.String)+len(key.String))*8+1024); err != nil {
		return metadataRecordKey{}, err
	}
	return metadataRecordKey{field.String, key.String}, nil
}

// Bodies are admitted after a locking size read and guarded again in SQL.
// A same-transaction observer or a weaker backend cannot grow the cell into
// the driver between those reads. Unknown unchanged rows are kept as raw bytes.
func (b *legacyMetadataDeltaReadBudget) record(ctx context.Context, tx *sqlTx, workspace string, key metadataRecordKey, decode bool) ([]byte, error) {
	if err := b.check(tx); err != nil {
		return nil, err
	}
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	length := b.owner.byteLength("data")
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT "+length+" FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?"+lock,
		workspace, key.field, key.key).Scan(&size); err != nil {
		return nil, err
	}
	limit := int64(metadataWorkBytes / 4)
	if decode {
		limit = metadataWorkBytes / 16
	}
	if size < 0 || size > limit {
		return nil, ErrMetadataWorkBudget
	}
	if err := b.reserve(ctx, uint64(size)*3+4096); err != nil {
		return nil, err
	}
	var raw []byte
	var actual sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN data ELSE NULL END,"+length+
		" FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?"+lock,
		size, workspace, key.field, key.key).Scan(&raw, &actual); err != nil {
		return nil, err
	}
	if !actual.Valid || actual.Int64 < 0 {
		return nil, io.ErrUnexpectedEOF
	}
	if actual.Int64 > size {
		if actual.Int64 > limit {
			return nil, ErrMetadataWorkBudget
		}
		return nil, ErrConcurrentWrite
	}
	if int64(len(raw)) != actual.Int64 {
		return nil, io.ErrUnexpectedEOF
	}
	if decode {
		work, err := metadataAggregatePayloadJSONWork(ctx, raw)
		if err != nil {
			return nil, err
		}
		if work > (metadataWorkBytes-4096)/8 {
			return nil, ErrMetadataWorkBudget
		}
		if err := b.reserve(ctx, work*8+4096); err != nil {
			return nil, err
		}
	}
	return raw, ctx.Err()
}

func (b *legacyMetadataDeltaReadBudget) lockRecords(ctx context.Context, tx *sqlTx, workspace, field string, keys []string) (map[string][]byte, error) {
	wire, err := b.headerWire(ctx, tx)
	if err != nil {
		return nil, err
	}
	defer wire.Close()
	if err := b.reserve(ctx, uint64(len(keys))*128+1024); err != nil {
		return nil, err
	}
	stored := make(map[string][]byte, len(keys))
	rememberOrder := tx.dialect == "sqlite" && tx.database.journalReady.Load() && len(keys) == 1
	if rememberOrder {
		tx.journalOrder.valid = false
	}
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	for start := 0; start < len(keys); start += MetadataRecordBatchMaxKeys {
		part := keys[start:min(start+MetadataRecordBatchMaxKeys, len(keys))]
		clause, args := bindList("record_key", part)
		values := append([]any{workspace, field}, args...)
		where := " FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND " + clause
		length := b.owner.byteLength("data")
		var maximum int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+length+"),0)"+where, values...).Scan(&maximum); err != nil {
			return nil, err
		}
		if maximum < 0 || maximum > metadataWorkBytes/16 {
			return nil, ErrMetadataWorkBudget
		}
		bodyWire := &ResourceRead{store: b.owner.store}
		if err := bodyWire.reserve(ctx, uint64(maximum)*3+64<<10); err != nil {
			bodyWire.Close()
			return nil, err
		}
		err := func() error {
			defer bodyWire.Close()
			payloadArgs := append([]any{maximum}, values...)
			rows, err := tx.QueryContext(ctx, "SELECT "+b.headerColumn("record_key", MetadataRecordPageMaxBytes)+
				",CASE WHEN "+length+"<=? THEN data ELSE NULL END,"+length+",collection_order"+where+" ORDER BY record_key"+lock,
				payloadArgs...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var key sql.NullString
				var raw []byte
				var actual sql.NullInt64
				var order int64
				if err := rows.Scan(&key, &raw, &actual, &order); err != nil {
					rows.Close()
					return err
				}
				header, err := b.header(ctx, sql.NullString{String: field, Valid: true}, key)
				if err != nil {
					rows.Close()
					return err
				}
				if !actual.Valid || actual.Int64 < 0 || actual.Int64 > maximum || int64(len(raw)) != actual.Int64 {
					rows.Close()
					if actual.Valid && actual.Int64 > metadataWorkBytes/16 {
						return ErrMetadataWorkBudget
					}
					return ErrConcurrentWrite
				}
				work, err := metadataAggregatePayloadJSONWork(ctx, raw)
				if err != nil {
					rows.Close()
					return err
				}
				if work > (metadataWorkBytes-uint64(len(raw))*3-4096)/8 {
					rows.Close()
					return ErrMetadataWorkBudget
				}
				if err := b.reserve(ctx, work*8+uint64(len(raw))*3+4096); err != nil {
					rows.Close()
					return err
				}
				stored[header.key] = raw
				if rememberOrder {
					tx.journalOrder = recordJournalOrder{workspace, field, header.key, order, true}
				}
			}
			return legacyMetadataDeltaRowsError(rows)
		}()
		if err != nil {
			return nil, err
		}
	}
	return stored, nil
}

func (b *legacyMetadataDeltaReadBudget) syncRecords(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, shapes map[string]string, wanted map[metadataRecordKey]metadataRecord, fields []string, known map[metadataRecordKey]metadataRecord) error {
	wire, err := b.headerWire(ctx, tx)
	if err != nil {
		return err
	}
	defer wire.Close()
	query := "SELECT " + b.headerColumn("field", 256) + "," + b.headerColumn("record_key", MetadataRecordPageMaxBytes) +
		",collection_order FROM workspace_metadata_records WHERE workspace_key=?"
	args := []any{workspace}
	if fields != nil {
		if len(fields) == 0 {
			return nil
		}
		clause, values := bindList("field", fields)
		query += " AND " + clause
		args = append(args, values...)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	previous := map[metadataRecordKey]metadataRecord{}
	missing := []metadataRecordKey{}
	for rows.Next() {
		var field, key sql.NullString
		var value metadataRecord
		if err := rows.Scan(&field, &key, &value.order); err != nil {
			rows.Close()
			return err
		}
		header, err := b.header(ctx, field, key)
		if err != nil {
			rows.Close()
			return err
		}
		if stored, found := known[header]; known != nil && found {
			value.data = stored.data
		} else {
			missing = append(missing, header)
		}
		previous[header] = value
	}
	if err := legacyMetadataDeltaRowsError(rows); err != nil {
		return err
	}
	for _, key := range missing {
		value := previous[key]
		value.data, err = b.record(ctx, tx, workspace, key, false)
		if err != nil {
			return err
		}
		previous[key] = value
	}
	return applyMetadataRecords(ctx, tx, workspace, refs, shapes, wanted, previous, known)
}

func (b *legacyMetadataDeltaReadBudget) arrayOrders(ctx context.Context, tx *sqlTx, workspace, field string, keys []string) (map[string]int, error) {
	wire, err := b.headerWire(ctx, tx)
	if err != nil {
		return nil, err
	}
	defer wire.Close()
	clause, values := bindList("record_key", keys)
	rows, err := tx.QueryContext(ctx, "SELECT "+b.headerColumn("record_key", MetadataRecordPageMaxBytes)+
		",collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND "+clause,
		append([]any{workspace, field}, values...)...)
	if err != nil {
		return nil, err
	}
	orders := map[string]int{}
	for rows.Next() {
		var key sql.NullString
		var order int
		if err := rows.Scan(&key, &order); err != nil {
			rows.Close()
			return nil, err
		}
		header, err := b.header(ctx, sql.NullString{String: field, Valid: true}, key)
		if err != nil {
			rows.Close()
			return nil, err
		}
		orders[header.key] = order
	}
	return orders, legacyMetadataDeltaRowsError(rows)
}
