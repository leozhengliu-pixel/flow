package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

type legacyMetadataStreamedOrderKey struct{}

// withLegacyMetadataStreamedOrder opts the named record-backed array fields of
// one persistLegacyMetadataImage call into the streamed reorder below.
func withLegacyMetadataStreamedOrder(ctx context.Context, fields ...string) context.Context {
	return context.WithValue(ctx, legacyMetadataStreamedOrderKey{}, fields)
}

func legacyMetadataStreamedOrder(ctx context.Context, field string) bool {
	fields, _ := ctx.Value(legacyMetadataStreamedOrderKey{}).([]string)
	return slices.Contains(fields, field)
}

// arrayReorderReady admits an array field the order-preserving fast path
// refuses (surviving records changed their relative order, or new records sit
// between them) when its record IDs are unique, valid row keys on both sides.
// Duplicates keep the materializing writer, which owns their inline shape.
func (p *legacyMetadataWritePlan) arrayReorderReady(f *legacyMetadataWriteField) bool {
	if f.full || f.field.name == "projects" && !p.streamProjectBackfill(f) || f.field.name == "agentSessions" {
		return false
	}
	for _, order := range [][]int{f.oldOrder, f.order} {
		seen := make(map[string]bool, len(order))
		for _, index := range order {
			id := f.rows[index].id
			if id == "" || len(id) > 191 || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	return true
}

// legacyArrayReorderChunk is the number of records whose stored orders one
// statement reads.
const legacyArrayReorderChunk = 256

// Suggestion records have no offloaded bodies or per-record application hooks.
// Other collections still use the individual writer, including its hooks.
type legacyArrayWrites struct {
	plan  *legacyMetadataWritePlan
	field string
	work  *ResourceRead
	args  []any
	moves []legacyArrayMove
}

type legacyArrayMove struct {
	id    string
	order int
}

func (w *legacyArrayWrites) close() {
	if w.work != nil {
		w.work.Close()
		w.work = nil
	}
}

func (w *legacyArrayWrites) canBatch(id string) bool {
	// PostgreSQL removes NULs from bound keys. Keep those exceptional writes
	// separate, since two distinct keys can then target the same row.
	return w.field == "issueSuggestions" && (w.plan.tx.dialect != "postgres" || strings.IndexByte(id, 0) < 0)
}

func (w *legacyArrayWrites) upsert(ctx context.Context, id string, value metadataRecord, previous []byte) error {
	p := w.plan
	if w.canBatch(id) && len(value.data) <= metadataRecordChunkBytes {
		if w.work == nil {
			w.work = &ResourceRead{store: p.store}
		}
		// Keep the encoded rows and driver arguments charged until execution.
		if err := w.work.reserve(ctx, uint64(len(value.data))*3+512); err == nil {
			w.args = append(w.args, p.workspace, w.field, id, value.order, StripJSONNUL(value.data))
			if len(w.args) < MetadataRecordBatchMaxKeys*5 {
				return nil
			}
			return w.flushUpserts(ctx)
		}
	}
	if err := w.flush(ctx); err != nil {
		return err
	}
	return writeOwnedMetadataRecordRow(ctx, p.store, p.tx, p.workspace, metadataRecordKey{w.field, id}, value, previous)
}

func (w *legacyArrayWrites) flush(ctx context.Context) error {
	if err := w.flushUpserts(ctx); err != nil {
		return err
	}
	return w.flushMoves(ctx)
}

func (w *legacyArrayWrites) flushUpserts(ctx context.Context) error {
	defer w.close()
	if len(w.args) == 0 {
		return nil
	}
	// The first row's tuple is literal, so the statement keeps the shape the
	// metadata revision scan checks: every row binds the plan's workspace,
	// the first bound value (metadataWriteWorkspace).
	_, err := w.plan.tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?)`+
		strings.Repeat(",(?,?,?,?,?)", len(w.args)/5-1)+` ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`, w.args...)
	clear(w.args)
	w.args = w.args[:0]
	w.plan.tx.metadataWritten = true
	return err
}

func (w *legacyArrayWrites) move(ctx context.Context, id string, order int) error {
	if w.canBatch(id) {
		w.moves = append(w.moves, legacyArrayMove{id, order})
		if len(w.moves) < MetadataRecordBatchMaxKeys {
			return nil
		}
		return w.flushMoves(ctx)
	}
	if err := w.flush(ctx); err != nil {
		return err
	}
	p := w.plan
	_, err := p.tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field=? AND record_key=?`, order, p.workspace, w.field, id)
	p.tx.metadataWritten = true
	return err
}

func (w *legacyArrayWrites) flushMoves(ctx context.Context) error {
	if len(w.moves) == 0 {
		return nil
	}
	ids := make([]string, 0, len(w.moves))
	args := make([]any, 0, 3*len(w.moves)+2)
	for _, move := range w.moves {
		ids = append(ids, move.id)
		args = append(args, move.id, move.order)
	}
	clause, keys := bindList("record_key", ids)
	args = append(args, w.plan.workspace, w.field)
	args = append(args, keys...)
	_, err := w.plan.tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET collection_order=CASE record_key`+
		strings.Repeat(" WHEN ? THEN ?", len(w.moves))+
		` ELSE collection_order END WHERE workspace_key=? AND field=? AND `+clause, args...)
	clear(w.moves)
	w.moves = w.moves[:0]
	w.plan.tx.metadataWritten = true
	return err
}

func (p *legacyMetadataWritePlan) deleteArrayDropped(ctx context.Context, f *legacyMetadataWriteField, seen map[string]bool) error {
	var ids []string
	flush := func() error {
		if len(ids) == 0 {
			return nil
		}
		clause, args := bindList("record_key", ids)
		_, err := p.tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause,
			append([]any{p.workspace, f.field.name}, args...)...)
		p.tx.metadataWritten = true
		ids = ids[:0]
		return err
	}
	for _, index := range f.oldOrder {
		row := f.rows[index]
		if row.deleted || seen[row.id] {
			continue
		}
		if f.field.name != "issueSuggestions" {
			if err := deleteMetadataRecordRow(ctx, p.tx, p.workspace, metadataRecordKey{f.field.name, row.id}); err != nil {
				return err
			}
			continue
		}
		ids = append(ids, row.id)
		if len(ids) == MetadataRecordBatchMaxKeys {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	return flush()
}

// writeArrayReorder persists a reordered array field as the materializing
// writer's applyMetadataRecords does with known records, one chunk of records
// at a time instead of both complete collections:
//
//   - the wanted records are the callback's list (records another write
//     deleted during the rebase left out), each at its list index;
//   - issueCollectionOrder over the stored orders keeps stored positions when
//     the stored records between the first and the last kept one are present
//     and ascending, and otherwise numbers every wanted record by its index;
//   - a record equal to the known one (or, for one this writer did not know,
//     to the stored row) only gets its new order, a known record another
//     write deleted is not written again, any other record is written whole;
//   - known records the list dropped are deleted; stored records this writer
//     did not know keep their rows and orders.
func (p *legacyMetadataWritePlan) writeArrayReorder(ctx context.Context, f *legacyMetadataWriteField) error {
	writes := legacyArrayWrites{plan: p, field: f.field.name}
	defer writes.close()
	wanted := make([]int, 0, len(f.order))
	for _, index := range f.order {
		if !f.rows[index].deleted {
			wanted = append(wanted, index)
		}
	}
	chunks := func(visit func(start int, orders map[string]int) error) error {
		for start := 0; start < len(wanted); start += legacyArrayReorderChunk {
			part := wanted[start:min(start+legacyArrayReorderChunk, len(wanted))]
			keys := make([]string, 0, len(part))
			for _, index := range part {
				keys = append(keys, f.rows[index].id)
			}
			work := &ResourceRead{store: p.store}
			guard := legacyMetadataDeltaBudget(withLegacyMetadataDeltaReadBudget(ctx, work))
			orders, err := guard.arrayOrders(ctx, p.tx, p.workspace, f.field.name, keys)
			if err == nil {
				err = visit(start, orders)
			}
			work.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	// issueCollectionOrder's first/last stored record and stability.
	first, last, firstOrder, lastOrder := -1, -1, 0, 0
	stable, gap := true, false
	previousID := ""
	if err := chunks(func(start int, orders map[string]int) error {
		for offset := range min(legacyArrayReorderChunk, len(wanted)-start) {
			i, id := start+offset, f.rows[wanted[start+offset]].id
			order, present := orders[id]
			switch {
			case !present:
				gap = gap || first >= 0
			case first < 0:
				first, last, firstOrder, lastOrder = i, i, order, order
			default:
				if gap || order < lastOrder || order == lastOrder && id < previousID {
					stable = false
				}
				last, lastOrder = i, order
			}
			if present {
				previousID = id
			}
		}
		return nil
	}); err != nil {
		return err
	}
	position := func(i int, stored int) int {
		switch {
		case first < 0 || !stable:
			return i
		case i < first:
			return firstOrder - first + i
		case i > last:
			return lastOrder + i - last
		}
		return stored
	}
	if err := chunks(func(start int, orders map[string]int) error {
		for offset := range min(legacyArrayReorderChunk, len(wanted)-start) {
			i, row := start+offset, &f.rows[wanted[start+offset]]
			stored, present := orders[row.id]
			if err := p.writeArrayReorderRow(ctx, f, &writes, row, stored, present, position(i, stored)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := writes.flush(ctx); err != nil {
		return err
	}
	seen := make(map[string]bool, len(wanted))
	for _, index := range wanted {
		seen[f.rows[index].id] = true
	}
	return p.deleteArrayDropped(ctx, f, seen)
}

func (p *legacyMetadataWritePlan) writeArrayReorderRow(ctx context.Context, f *legacyMetadataWriteField, writes *legacyArrayWrites, row *legacyMetadataWriteRow, stored int, present bool, order int) error {
	known := row.before.typ != nil
	key := metadataRecordKey{f.field.name, row.id}
	moveOnly := func() error {
		if stored == order {
			return nil
		}
		return writes.move(ctx, row.id, order)
	}
	if known && !present {
		// Another write deleted it; this write does not bring it back.
		return nil
	}
	if known && present && !row.changed {
		return moveOnly()
	}
	work := &ResourceRead{store: p.store}
	defer work.Close()
	if err := legacyActorWriteNodeWork(ctx, work, row.next, 64); err != nil {
		return err
	}
	next, err := row.next.Value(ctx, metadataWorkBytes())
	if err != nil {
		return err
	}
	raw, err := encodeOwnedMetadataRecord(ctx, work, next)
	if err != nil {
		return err
	}
	var previous []byte
	if known {
		if err := legacyActorWriteNodeWork(ctx, work, row.before, 64); err != nil {
			return err
		}
		old, err := row.before.Value(ctx, metadataWorkBytes())
		if err != nil {
			return err
		}
		if previous, err = encodeOwnedMetadataRecord(ctx, work, old); err != nil {
			return err
		}
	} else if present {
		guard := legacyMetadataDeltaBudget(withLegacyMetadataDeltaReadBudget(ctx, work))
		if previous, err = guard.record(ctx, p.tx, p.workspace, key, false); err != nil {
			return err
		}
	}
	refs, err := p.references(ctx, p.actual, work, next.Interface(), nil, previous, raw)
	if err != nil {
		return err
	}
	if present && storedRecordEqual(p.tx, refs, key.field, previous, raw) {
		return moveOnly()
	}
	return writes.upsert(ctx, row.id, metadataRecord{data: raw, order: order}, previous)
}

// arrayTransitionReady admits a collection stored inline in the root (its
// stored list repeats or lacks an ID, so splitWorkspaceMetadata kept it
// whole) whose target splits into one row per record: the stored root still
// lists it inline, it has no canonical rows, and the target is a non-nil list
// of structs with an id that the literal splitter accepts (inlineTargetShape).
func (p *legacyMetadataWritePlan) arrayTransitionReady(ctx context.Context, f *legacyMetadataWriteField, shapes map[string]string) (bool, error) {
	if !f.full || f.next.typ == nil || shapes[f.field.name] != "" || updateMetadataField(f.field.name) ||
		f.field.name == "projects" || f.field.name == "agentSessions" {
		return false, nil
	}
	element := f.next.typ
	if element.Kind() != reflect.Slice || element.Elem().Kind() != reflect.Struct {
		return false, nil
	}
	id, found := element.Elem().FieldByName("ID")
	if !found || id.Type.Kind() != reflect.String || id.PkgPath != "" || len(id.Index) != 1 {
		return false, nil
	}
	if tag := id.Tag.Get("json"); tag != "id" && tag != "id,omitempty" {
		return false, nil
	}
	if absent, err := f.next.isNil(ctx); err != nil || absent {
		return false, err
	}
	backed, err := metadataFieldRecordBacked(ctx, p.tx, p.workspace, f.field.name, f.next.typ)
	if err != nil || backed {
		return false, err
	}
	var rows int
	if err := p.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?", p.workspace, f.field.name).Scan(&rows); err != nil {
		return false, err
	}
	if rows != 0 {
		return false, nil
	}
	inline, err := p.inlineTargetShape(ctx, f.next)
	return !inline, err
}

// writeArrayTransition persists an arrayTransitionReady field as the
// materializing writer does (splitWorkspaceMetadata, then syncMetadataRecords
// over no stored or known rows, then the root patch): every target record is
// written as a new row ordered by its list index, the root keeps the field as
// [] and its collection manifest names it "array".
func (p *legacyMetadataWritePlan) writeArrayTransition(ctx context.Context, f *legacyMetadataWriteField) error {
	writes := legacyArrayWrites{plan: p, field: f.field.name}
	defer writes.close()
	err := f.next.Children(ctx, func(key reflect.Value, item metadataLogicalNode) error {
		work := &ResourceRead{store: p.store}
		defer work.Close()
		if err := legacyActorWriteNodeWork(ctx, work, item, 64); err != nil {
			return err
		}
		value, err := item.Value(ctx, metadataWorkBytes())
		if err != nil {
			return err
		}
		raw, err := encodeOwnedMetadataRecord(ctx, work, value)
		if err != nil {
			return err
		}
		// The splitter keys the row by the encoded record's id.
		var entity struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &entity); err != nil {
			return err
		}
		if entity.ID == "" || len(entity.ID) > 191 {
			return ErrBoundedMetadataCoverage
		}
		return writes.upsert(ctx, entity.ID, metadataRecord{data: raw, order: int(key.Int())}, nil)
	})
	if err != nil {
		return err
	}
	if err := writes.flush(ctx); err != nil {
		return err
	}
	owner := &ResourceRead{store: p.store}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return err
	}
	lock := ""
	if p.tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var size int64
	if err := p.tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+" FROM workspace_states WHERE workspace_key=?"+lock, p.workspace).Scan(&size); err != nil {
		return err
	}
	root, err := p.store.scanMetadataInlineRoot(ctx, p.tx, p.workspace, size)
	if err != nil {
		return err
	}
	defer root.Close()
	shapes, err := root.shapes()
	if err != nil {
		return err
	}
	shapes[f.field.name] = "array"
	manifest, err := json.Marshal(shapes)
	if err != nil {
		return err
	}
	patch := map[string]json.RawMessage{f.field.name: json.RawMessage(`[]`), metadataCollectionsKey: manifest}
	return p.store.writeStreamedMetadataRootPatch(ctx, p.tx, p.workspace, p.workspaceID, root, patch, owner, true)
}
