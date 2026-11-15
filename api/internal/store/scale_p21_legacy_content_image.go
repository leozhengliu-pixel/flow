package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
)

// prepareLegacyContentImage supplies only the four missing content fields.
// The caller's transaction is borrowed throughout this synchronous transform.
func (s *SQLiteStore) prepareLegacyContentImage(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource) (_ *metadataLogicalSource, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil || tx == nil || tx.database != s.db || tx.dialect != s.dialect {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	defer source.Release()
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	work := &ResourceRead{store: s}
	defer work.Close()
	if uint64(len(workspace)) > (metadataWorkBytes-(1<<20))/8 {
		return nil, ErrMetadataWorkBudget
	}
	// Fixed work covers the stage's bounded cache and at most four prepared
	// field builders, plus the active spool/transform codec buffers.
	if err := work.reserve(ctx, uint64(len(workspace))*8+1<<20); err != nil {
		return nil, err
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return nil, err
	}
	if stamp.workspaceID == "" || source.Stamp().workspaceID != stamp.workspaceID {
		return nil, ErrMetadataRecordQuery
	}
	shape, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer shape.Close()
	fields := [...]struct {
		name, kind string
		prepared   metadataLogicalNode
	}{
		{name: "activities", kind: "activity"},
		{name: "comments", kind: "comment"},
		{name: "notifications", kind: "notification"},
		{name: "notificationDeliveries", kind: "delivery"},
	}
	// Prepare in literal hydration order, independently of Bootstrap's field
	// declaration order, so the first SQL/JSON failure remains the same.
	for i := range fields {
		index, _, ok := metadataLogicalField(fields[i].name)
		if !ok {
			return nil, ErrMetadataRecordQuery
		}
		original := source.fields[index]
		missing, err := original.isNil(ctx)
		if err != nil {
			return nil, err
		}
		if !missing {
			continue
		}
		// The literal hydrator starts with an empty nonnil map or slice.
		var empty reflect.Value
		if original.typ.Kind() == reflect.Map {
			empty = reflect.MakeMap(original.typ)
		} else {
			empty = reflect.MakeSlice(original.typ, 0, 0)
		}
		base, err := shape.writeValue(empty, 0, false)
		if err != nil {
			return nil, err
		}
		node := legacyContentImageNode{store: s, tx: tx, workspace: workspace, kind: fields[i].kind, shape: base}
		if original.typ.Kind() == reflect.Map {
			node.groupShape, err = shape.writeValue(reflect.MakeSlice(original.typ.Elem(), 0, 0), 0, false)
			if err != nil {
				return nil, err
			}
		}
		part, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return nil, err
		}
		defer part.Close()
		fields[i].prepared, err = part.writeAggregateNode(node, 0)
		if err != nil {
			return nil, err
		}
	}
	result, err := source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		for _, field := range fields {
			if field.name == name && field.prepared.typ != nil {
				return metadataAggregateStoredNode{value: field.prepared}, true, nil
			}
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		result.Release()
		return nil, err
	}
	return result, nil
}

type legacyContentImageNode struct {
	store      *SQLiteStore
	tx         *sqlTx
	workspace  string
	kind       string
	shape      metadataLogicalNode
	groupShape metadataLogicalNode
}

func (n legacyContentImageNode) nodeType() reflect.Type           { return n.shape.typ }
func (n legacyContentImageNode) logicalBase() metadataLogicalNode { return n.shape }
func (n legacyContentImageNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyContentImageNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	if n.shape.typ.Kind() != reflect.Slice {
		return ErrMetadataRecordQuery
	}
	return n.withCursor(ctx, func(cursor *legacyContentImageCursor) error {
		ordinal := 0
		for cursor.present {
			if err := visit(reflect.ValueOf(ordinal), metadataAggregateStoredNode{value: cursor.frame}); err != nil {
				return err
			}
			ordinal++
			if err := cursor.next(); err != nil {
				return err
			}
		}
		return nil
	})
}
func (n legacyContentImageNode) logicalMapEntries(ctx context.Context, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	if n.shape.typ.Kind() != reflect.Map {
		return ErrMetadataRecordQuery
	}
	return n.withCursor(ctx, func(cursor *legacyContentImageCursor) error {
		for cursor.present {
			resource := cursor.resource
			group := legacyContentImageGroup{cursor: cursor, resource: resource, shape: n.groupShape}
			if err := visit(metadataAggregateNativeNode{value: reflect.ValueOf(resource)}, group); err != nil {
				return err
			}
			// A group must be consumed before the cursor advances to another key.
			if cursor.present && cursor.resource == resource {
				return ErrMetadataRecordQuery
			}
		}
		return nil
	})
}

type legacyContentImageGroup struct {
	cursor   *legacyContentImageCursor
	resource string
	shape    metadataLogicalNode
}

func (n legacyContentImageGroup) nodeType() reflect.Type           { return n.shape.typ }
func (n legacyContentImageGroup) logicalBase() metadataLogicalNode { return n.shape }
func (n legacyContentImageGroup) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyContentImageGroup) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	ordinal := 0
	for n.cursor.present && n.cursor.resource == n.resource {
		if err := visit(reflect.ValueOf(ordinal), metadataAggregateStoredNode{value: n.cursor.frame}); err != nil {
			return err
		}
		ordinal++
		if err := n.cursor.next(); err != nil {
			return err
		}
	}
	return nil
}

// Every resource is the exact Go string produced by Scan. BLOB ordering in the
// private SQLite stage is bytewise Go string ordering, including NUL/invalid
// UTF-8; source ordinal preserves each group's literal SQL encounter order.
func (n legacyContentImageNode) withCursor(ctx context.Context, visit func(*legacyContentImageCursor) error) (err error) {
	wire := &ResourceRead{store: n.store}
	defer wire.Close()
	keyLength, bodyLength := wire.byteLength("resource_id"), wire.byteLength("data")
	var maxKey, maxBody int64
	if err := n.tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+keyLength+"),0),COALESCE(MAX("+bodyLength+"),0) FROM workspace_content_records WHERE workspace_key=? AND kind=?", n.workspace, n.kind).Scan(&maxKey, &maxBody); err != nil {
		return err
	}
	if maxKey < 0 || maxBody < 0 || maxKey > metadataWorkBytes/8 || maxBody > metadataWorkBytes/16 {
		return ErrMetadataWorkBudget
	}
	cost := uint64(maxKey)*8 + uint64(maxBody)*3 + 128<<10
	if err := wire.reserve(ctx, cost); err != nil {
		return err
	}
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := stage.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	if _, err := stage.exec(ctx, 4096, `CREATE TABLE content(resource BLOB NOT NULL,ordinal INTEGER NOT NULL,frame_offset INTEGER NOT NULL,frame_size INTEGER NOT NULL,frame_cost INTEGER NOT NULL,PRIMARY KEY(resource,ordinal)) WITHOUT ROWID`); err != nil {
		return err
	}
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return err
	}
	defer frames.Close()
	typ := n.shape.typ.Elem()
	if n.shape.typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	if err := n.stageRows(ctx, stage, frames, typ, keyLength, bodyLength, maxKey, maxBody); err != nil {
		return err
	}
	rows, err := stage.db.QueryContext(ctx, `SELECT LENGTH(resource),CASE WHEN LENGTH(resource)<=? THEN resource ELSE NULL END,frame_offset,frame_size,frame_cost FROM content ORDER BY resource,ordinal`, maxKey)
	if err != nil {
		return err
	}
	cursor := &legacyContentImageCursor{rows: rows, maxKey: maxKey, frames: frames, typ: typ}
	defer func() {
		cursor.resource, cursor.frame = "", metadataLogicalNode{}
		if closeErr := errors.Join(rows.Err(), rows.Close()); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	if err := cursor.next(); err != nil {
		return err
	}
	if err := visit(cursor); err != nil {
		return err
	}
	return ctx.Err()
}

func (n legacyContentImageNode) stageRows(ctx context.Context, stage *sw26MigrationStage, frames *metadataLogicalBuilder, typ reflect.Type, keyLength, bodyLength string, maxKey, maxBody int64) (err error) {
	query := "SELECT " + keyLength + "," + bodyLength + ",CASE WHEN " + keyLength + "<=? THEN resource_id ELSE NULL END,CASE WHEN " + bodyLength + "<=? THEN data ELSE NULL END FROM workspace_content_records WHERE workspace_key=? AND kind=? ORDER BY created_at,id"
	rows, err := n.tx.QueryContext(ctx, query, maxKey, maxBody, n.workspace, n.kind)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := errors.Join(rows.Err(), rows.Close()); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	ordinal := int64(0)
	for rows.Next() {
		var keySize, bodySize sql.NullInt64
		var resource sql.NullString
		var raw []byte
		if err := rows.Scan(&keySize, &bodySize, &resource, &raw); err != nil {
			return err
		}
		if !keySize.Valid || !bodySize.Valid || !resource.Valid || keySize.Int64 < 0 || bodySize.Int64 < 0 ||
			keySize.Int64 > maxKey || bodySize.Int64 > maxBody || int64(len(resource.String)) != keySize.Int64 || int64(len(raw)) != bodySize.Int64 {
			return io.ErrUnexpectedEOF
		}
		frame, err := legacyContentImageFrame(ctx, n.store, frames, typ, raw)
		if err != nil {
			return err
		}
		if _, err := stage.exec(ctx, keySize.Int64+256, `INSERT INTO content(resource,ordinal,frame_offset,frame_size,frame_cost) VALUES(?,?,?,?,?)`, []byte(resource.String), ordinal, frame.offset, frame.size, int64(frame.cost)); err != nil {
			return err
		}
		resource.String, raw = "", nil
		if ordinal == math.MaxInt64 {
			return ErrMetadataWorkBudget
		}
		ordinal++
	}
	return ctx.Err()
}

type legacyContentImageCursor struct {
	rows     *sql.Rows
	maxKey   int64
	frames   *metadataLogicalBuilder
	typ      reflect.Type
	resource string
	frame    metadataLogicalNode
	present  bool
}

func (c *legacyContentImageCursor) next() error {
	c.resource, c.frame, c.present = "", metadataLogicalNode{}, false
	if !c.rows.Next() {
		return c.rows.Err()
	}
	var keySize sql.NullInt64
	var resource sql.NullString
	var offset, size, cost int64
	if err := c.rows.Scan(&keySize, &resource, &offset, &size, &cost); err != nil {
		return err
	}
	if !keySize.Valid || !resource.Valid || keySize.Int64 < 0 || keySize.Int64 > c.maxKey || int64(len(resource.String)) != keySize.Int64 ||
		offset < 0 || size < 16 || cost < 0 || cost > metadataWorkBytes || size > c.frames.offset || offset > c.frames.offset-size {
		return io.ErrUnexpectedEOF
	}
	c.frame = metadataLogicalNode{source: c.frames.storage, offset: offset, size: size, cost: uint64(cost), typ: c.typ}
	c.resource, c.present = resource.String, true
	return nil
}

func legacyContentImageFrame(ctx context.Context, store *SQLiteStore, frames *metadataLogicalBuilder, typ reflect.Type, raw []byte) (_ metadataLogicalNode, err error) {
	work := &ResourceRead{store: store}
	defer work.Close()
	jsonWork, err := metadataAggregatePayloadJSONWork(ctx, raw)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	fixed := uint64(typ.Size())*4 + 4096
	if fixed > metadataWorkBytes || jsonWork > metadataWorkBytes-fixed {
		return metadataLogicalNode{}, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, jsonWork+fixed); err != nil {
		return metadataLogicalNode{}, err
	}
	value := reflect.New(typ)
	defer func() { value.Elem().SetZero() }()
	if err := json.Unmarshal(raw, value.Interface()); err != nil {
		return metadataLogicalNode{}, err
	}
	nativeWork, err := metadataAggregateNativeCost(ctx, value.Elem(), 0)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if nativeWork > (metadataWorkBytes-(64<<10))/2 {
		return metadataLogicalNode{}, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, nativeWork*2+64<<10); err != nil {
		return metadataLogicalNode{}, err
	}
	// Only this fresh standard JSON decode supplies location receipts.
	if err := frames.auditJSONLocations(value.Elem()); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeValue(value.Elem(), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if node.unavailable || node.blocked {
		return metadataLogicalNode{}, ErrBoundedMetadataCoverage
	}
	return node, nil
}
