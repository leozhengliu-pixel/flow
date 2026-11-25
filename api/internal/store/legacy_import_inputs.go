package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"time"
)

type legacyContentWriteBudgetKey struct{}

// prepareLegacyImportImage preserves hydrateImportInputs' unconditional scans
// and import-before-migration error order, before ordinary content hydration.
func (s *SQLiteStore) prepareLegacyImportImage(ctx context.Context, workspace string, source *metadataLogicalSource) (_ *metadataLogicalSource, err error) {
	if s == nil || !s.BoundedMetadataEnabled() || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	defer source.Release()
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	current := source
	defer func() {
		if err != nil && current != source {
			current.Release()
		}
	}()
	for _, field := range [][3]string{
		{"importJobs", "import_input", "Rows"},
		{"migrationJobs", "migration_input", "Bundle"},
	} {
		next, err := s.prepareLegacyImportKind(ctx, workspace, current, field[0], field[1], field[2])
		if err != nil {
			return nil, err
		}
		if current != source {
			current.Release()
		}
		current = next
	}
	return current, nil
}

func (s *SQLiteStore) prepareLegacyImportKind(ctx context.Context, workspace string, source *metadataLogicalSource, field, kind, payload string) (_ *metadataLogicalSource, err error) {
	work := &ResourceRead{store: s}
	defer work.Close()
	if uint64(len(workspace)) > (metadataWorkBytes-(1<<20))/16 {
		return nil, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(len(workspace))*16+1<<20); err != nil {
		return nil, err
	}
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	if _, err := stage.exec(ctx, 4096, `CREATE TABLE content(resource BLOB NOT NULL,ordinal INTEGER NOT NULL,frame_offset INTEGER NOT NULL,frame_size INTEGER NOT NULL,frame_cost INTEGER NOT NULL,PRIMARY KEY(resource,ordinal)) WITHOUT ROWID`); err != nil {
		return nil, err
	}
	frames, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer frames.Close()
	var tx *sqlTx
	borrowed := false
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
		if snapshot.reader == nil {
			return nil, ErrMetadataRecordQuery
		}
		tx, borrowed = snapshot.reader, true
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
	}
	keyLength, bodyLength := work.byteLength("resource_id"), work.byteLength("data")
	var maxKey, maxBody int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+keyLength+"),0),COALESCE(MAX("+bodyLength+"),0) FROM workspace_content_records WHERE workspace_key=? AND kind=?", workspace, kind).Scan(&maxKey, &maxBody); err != nil {
		return nil, err
	}
	if maxKey < 0 || maxBody < 0 || maxKey > metadataWorkBytes/8 || maxBody > metadataWorkBytes/16 {
		return nil, ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(maxKey)*8+uint64(maxBody)*3+128<<10); err != nil {
		return nil, err
	}
	scanner := legacyContentImageNode{store: s, tx: tx, workspace: workspace, kind: kind}
	if err := scanner.stageRows(ctx, stage, frames, reflect.TypeFor[importInput](), keyLength, bodyLength, maxKey, maxBody); err != nil {
		return nil, err
	}
	// The literal read's statement is closed before applying values to jobs.
	// Each normal kind freezes independently; explicit caller snapshots stay
	// borrowed exactly as the database wrapper routes its original reads.
	if !borrowed {
		if err := tx.Rollback(); err != nil {
			return nil, err
		}
	}
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == field {
			return legacyImportJobsImage{store: s, stage: stage, frames: frames, base: original, payload: payload}, true, nil
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
}

type legacyImportJobsImage struct {
	store   *SQLiteStore
	stage   *sw26MigrationStage
	frames  *metadataLogicalBuilder
	base    metadataLogicalNode
	payload string
}

func (n legacyImportJobsImage) nodeType() reflect.Type           { return n.base.typ }
func (n legacyImportJobsImage) logicalBase() metadataLogicalNode { return n.base }
func (n legacyImportJobsImage) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyImportJobsImage) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, job metadataLogicalNode) error {
		body, err := issueHotField(ctx, job, n.payload)
		if err != nil {
			return err
		}
		absent, err := body.isNil(ctx)
		if err != nil {
			return err
		}
		if !absent {
			return visit(key, metadataAggregateStoredNode{value: job})
		}
		if n.payload == "Rows" {
			status, err := issueHotField(ctx, job, "Status")
			if err != nil {
				return err
			}
			completed, err := status.StringEqual(ctx, "completed")
			if err != nil {
				return err
			}
			if completed {
				return visit(key, metadataAggregateStoredNode{value: job})
			}
		}
		work := &ResourceRead{store: n.store}
		defer work.Close()
		idNode, err := issueHotField(ctx, job, "ID")
		if err != nil {
			return err
		}
		id, err := work.node(ctx, idNode, false)
		if err != nil {
			return err
		}
		var offset, size, cost int64
		err = n.stage.db.QueryRowContext(ctx, `SELECT frame_offset,frame_size,frame_cost FROM content WHERE resource=? ORDER BY ordinal LIMIT 1`, []byte(id.String())).Scan(&offset, &size, &cost)
		if errors.Is(err, sql.ErrNoRows) {
			return visit(key, metadataAggregateStoredNode{value: job})
		}
		if err != nil {
			return err
		}
		if offset < 0 || size < 16 || cost < 0 || cost > metadataWorkBytes || size > n.frames.offset || offset > n.frames.offset-size {
			return ErrMetadataRecordQuery
		}
		input := metadataLogicalNode{source: n.frames.storage, offset: offset, size: size, cost: uint64(cost), typ: reflect.TypeFor[importInput]()}
		replacement, err := issueHotField(ctx, input, n.payload)
		if err != nil {
			return err
		}
		return visit(key, legacyImportJobImage{base: job, payload: n.payload, replacement: replacement})
	})
}

type legacyImportJobImage struct {
	base        metadataLogicalNode
	payload     string
	replacement metadataLogicalNode
}

func (n legacyImportJobImage) nodeType() reflect.Type           { return n.base.typ }
func (n legacyImportJobImage) logicalBase() metadataLogicalNode { return n.base }
func (n legacyImportJobImage) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacyImportJobImage) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() == n.payload {
			child = n.replacement
		}
		return visit(key, metadataAggregateStoredNode{value: child})
	})
}

// persistLegacyImportInputs retains the literal input write/prune order. Its
// source and transaction are borrowed; membership and removals live on disk.
func (s *SQLiteStore) persistLegacyImportInputs(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource) error {
	if s == nil || !s.BoundedMetadataEnabled() || tx == nil || tx.database != s.db || tx.dialect != s.dialect || source == nil {
		return ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return err
	}
	defer source.Release()
	ctx = metadataLogicalSourceScratchContext(metadataLogicalUnpinnedContext(ctx), source)
	work := &ResourceRead{store: s}
	defer work.Close()
	if uint64(len(workspace)) > (metadataWorkBytes-(1<<20))/16 {
		return ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(len(workspace))*16+1<<20); err != nil {
		return err
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return err
	}
	if stamp.workspaceID == "" || stamp.workspaceID != source.Stamp().workspaceID {
		return ErrMetadataRecordQuery
	}
	index, _, _ := metadataLogicalField("workspace")
	id, err := issueHotField(ctx, source.fields[index], "ID")
	if err != nil {
		return err
	}
	matched, err := id.StringEqual(ctx, stamp.workspaceID)
	if err != nil {
		return err
	}
	if !matched {
		return ErrMetadataRecordQuery
	}
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return err
	}
	defer stage.Close()
	for _, query := range []string{
		`CREATE TABLE wanted(kind TEXT NOT NULL,id BLOB NOT NULL,PRIMARY KEY(kind,id)) WITHOUT ROWID`,
		`CREATE TABLE removed(ordinal INTEGER PRIMARY KEY,kind BLOB NOT NULL,id BLOB NOT NULL)`,
	} {
		if _, err := stage.exec(ctx, 4096, query); err != nil {
			return err
		}
	}
	for _, field := range [][3]string{
		{"importJobs", "import_input", "Rows"},
		{"migrationJobs", "migration_input", "Bundle"},
	} {
		index, _, _ := metadataLogicalField(field[0])
		if err := source.fields[index].Children(ctx, func(_ reflect.Value, job metadataLogicalNode) error {
			return s.persistLegacyImportInput(ctx, tx, workspace, stage, field[1], field[2], job)
		}); err != nil {
			return err
		}
	}
	return s.pruneLegacyImportInputs(ctx, tx, workspace, stage)
}

func (s *SQLiteStore) persistLegacyImportInput(ctx context.Context, tx *sqlTx, workspace string, stage *sw26MigrationStage, kind, payload string, job metadataLogicalNode) error {
	work := &ResourceRead{store: s}
	defer work.Close()
	if err := work.reserve(ctx, 64<<10); err != nil {
		return err
	}
	if kind == "import_input" {
		status, err := issueHotField(ctx, job, "Status")
		if err != nil {
			return err
		}
		completed, err := status.StringEqual(ctx, "completed")
		if err != nil || completed {
			return err
		}
	}
	idNode, err := issueHotField(ctx, job, "ID")
	if err != nil {
		return err
	}
	id, err := work.node(ctx, idNode, false)
	if err != nil {
		return err
	}
	value := importInput{ID: id.String()}
	if _, err := stage.exec(ctx, int64(len(value.ID)+len(kind)+256),
		`INSERT INTO wanted(kind,id) VALUES(?,?) ON CONFLICT(kind,id) DO NOTHING`, kind, []byte(value.ID)); err != nil {
		return err
	}
	bodyNode, err := issueHotField(ctx, job, payload)
	if err != nil {
		return err
	}
	absent, err := bodyNode.isNil(ctx)
	if err != nil || absent {
		return err
	}
	body, err := work.node(ctx, bodyNode, false)
	if err != nil {
		return err
	}
	createdNode, err := issueHotField(ctx, job, "CreatedAt")
	if err != nil {
		return err
	}
	created, err := work.node(ctx, createdNode, false)
	if err != nil {
		return err
	}
	value.CreatedAt = created.Interface().(time.Time)
	if payload == "Rows" {
		value.Rows = body.Interface().([]map[string]string)
	} else {
		value.Bundle = body.Bytes()
	}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return err
	}
	if uint64(cost) > (metadataWorkBytes-(64<<10))/4 {
		return ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(cost)*4+64<<10); err != nil {
		return err
	}
	// writeContentRecord's canonical before-row read uses this private guard;
	// its unguarded branch remains the literal original for other callers.
	writeCtx := context.WithValue(ctx, legacyContentWriteBudgetKey{}, s)
	return writeContentRecord(writeCtx, tx, workspace, kind, value.ID, value)
}

// readLegacyContentBefore is the opt-in previous-row read used by the
// original content writer. A missing preflight is its ordinary SQL miss.
func readLegacyContentBefore(ctx context.Context, tx *sqlTx, workspace, kind, resource, id string, store *SQLiteStore) (_ []byte, _ int, release func(), err error) {
	release = func() {}
	if store == nil || !store.BoundedMetadataEnabled() ||
		tx == nil || tx.database != store.db {
		return nil, 0, release, ErrBoundedMetadataCoverage
	}
	owner := &ResourceRead{store: store}
	defer func() {
		if err != nil {
			owner.Close()
		}
	}()
	length := owner.byteLength("data")
	where := ` FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=? AND id=?`
	args := []any{workspace, kind, resource, id}
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT "+length+where, args...).Scan(&size); err != nil {
		return nil, 0, release, err
	}
	// Driver bytes and the returned copy are admitted before transfer. The
	// proven structural counter then admits parsing before the writer uses it.
	if size < 0 || size > metadataWorkBytes/8 {
		return nil, 0, release, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(size)*4+64<<10); err != nil {
		return nil, 0, release, err
	}
	var raw []byte
	var actual sql.NullInt64
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN data ELSE NULL END,"+length+
		",record_version"+where, append([]any{size}, args...)...).Scan(&raw, &actual, &version); err != nil {
		return nil, 0, release, err
	}
	if !actual.Valid || actual.Int64 < 0 {
		return nil, 0, release, ErrMetadataRecordQuery
	}
	if actual.Int64 > size {
		if actual.Int64 > metadataWorkBytes/8 {
			return nil, 0, release, ErrMetadataWorkBudget
		}
		return nil, 0, release, ErrConcurrentWrite
	}
	if int64(len(raw)) != actual.Int64 {
		return nil, 0, release, ErrMetadataRecordQuery
	}
	decode, err := metadataAggregatePayloadJSONWork(ctx, raw)
	if err != nil {
		return nil, 0, release, err
	}
	if err := owner.reserve(ctx, decode); err != nil {
		return nil, 0, release, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, release, err
	}
	return raw, version, owner.Close, nil
}

func (s *SQLiteStore) pruneLegacyImportInputs(ctx context.Context, tx *sqlTx, workspace string, stage *sw26MigrationStage) error {
	wire := &ResourceRead{store: s}
	defer wire.Close()
	length := wire.byteLength("id")
	where := ` FROM workspace_content_records WHERE workspace_key=? AND kind IN ('import_input','migration_input')`
	var maximum int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+length+"),0)"+where, workspace).Scan(&maximum); err != nil {
		return err
	}
	if maximum < 0 || maximum > metadataWorkBytes/32 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(maximum)*3+64<<10); err != nil {
		return err
	}
	// The original query has no ORDER BY. Record its returned order before
	// deleting anything, so read errors cannot cause a partial prune.
	rows, err := tx.QueryContext(ctx, "SELECT "+wire.byteLength("kind")+","+length+
		",CASE WHEN "+wire.byteLength("kind")+"<=32 THEN kind ELSE NULL END,CASE WHEN "+length+"<=? THEN id ELSE NULL END"+where,
		maximum, workspace)
	if err != nil {
		return err
	}
	ordinal := int64(0)
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			if err := func() error {
				work := &ResourceRead{store: s}
				defer work.Close()
				var kind, id sql.NullString
				var kindSize, idSize sql.NullInt64
				if err := rows.Scan(&kindSize, &idSize, &kind, &id); err != nil {
					return err
				}
				if !kindSize.Valid || !idSize.Valid || kindSize.Int64 < 0 || kindSize.Int64 > 32 || idSize.Int64 < 0 ||
					!kind.Valid || !id.Valid || idSize.Int64 > maximum || int64(len(id.String)) != idSize.Int64 {
					if idSize.Valid && idSize.Int64 > maximum && idSize.Int64 <= metadataWorkBytes/32 {
						return ErrConcurrentWrite
					}
					return ErrMetadataWorkBudget
				}
				if err := work.reserve(ctx, uint64(len(kind.String)+len(id.String))*8+4096); err != nil {
					return err
				}
				var wanted bool
				if err := stage.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM wanted WHERE kind=? AND id=?)`,
					kind.String, []byte(id.String)).Scan(&wanted); err != nil {
					return err
				}
				if !wanted {
					if _, err := stage.exec(ctx, int64(len(kind.String)+len(id.String)+256),
						`INSERT INTO removed(ordinal,kind,id) VALUES(?,?,?)`, ordinal, []byte(kind.String), []byte(id.String)); err != nil {
						return err
					}
				}
				if ordinal == math.MaxInt64 {
					return ErrMetadataWorkBudget
				}
				ordinal++
				return nil
			}(); err != nil {
				return err
			}
		}
		return rows.Err()
	}()
	if err := errors.Join(err, rows.Close()); err != nil {
		return err
	}
	removed, err := stage.db.QueryContext(ctx, `SELECT CASE WHEN LENGTH(kind)<=32 THEN kind ELSE NULL END,CASE WHEN LENGTH(id)<=? THEN id ELSE NULL END FROM removed ORDER BY ordinal`, maximum)
	if err != nil {
		return err
	}
	defer removed.Close()
	for removed.Next() {
		if err := func() error {
			work := &ResourceRead{store: s}
			defer work.Close()
			var kind, id sql.NullString
			if err := removed.Scan(&kind, &id); err != nil {
				return err
			}
			if !kind.Valid || !id.Valid {
				return ErrMetadataWorkBudget
			}
			if err := work.reserve(ctx, uint64(len(kind.String)+len(id.String))*8+4096); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND id=?`, workspace, kind.String, id.String)
			return err
		}(); err != nil {
			return err
		}
	}
	return removed.Err()
}
