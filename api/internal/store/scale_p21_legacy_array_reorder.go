package store

import (
	"context"
	"encoding/json"
	"slices"
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
	if f.full || f.field.name == "projects" || f.field.name == "agentSessions" {
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
			if err := p.writeArrayReorderRow(ctx, f, row, stored, present, position(i, stored)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	seen := make(map[string]bool, len(wanted))
	for _, index := range wanted {
		seen[f.rows[index].id] = true
	}
	for _, index := range f.oldOrder {
		row := f.rows[index]
		if !row.deleted && !seen[row.id] {
			if err := deleteMetadataRecordRow(ctx, p.tx, p.workspace, metadataRecordKey{f.field.name, row.id}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *legacyMetadataWritePlan) writeArrayReorderRow(ctx context.Context, f *legacyMetadataWriteField, row *legacyMetadataWriteRow, stored int, present bool, order int) error {
	known := row.before.typ != nil
	key := metadataRecordKey{f.field.name, row.id}
	moveOnly := func() error {
		if stored == order {
			return nil
		}
		if _, err := p.tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field=? AND record_key=?`, order, p.workspace, key.field, key.key); err != nil {
			return err
		}
		p.tx.metadataWritten = true
		return nil
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
	next, err := row.next.Value(ctx, metadataWorkBytes)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(next.Interface())
	if err != nil {
		return err
	}
	var previous []byte
	if known {
		if err := legacyActorWriteNodeWork(ctx, work, row.before, 64); err != nil {
			return err
		}
		old, err := row.before.Value(ctx, metadataWorkBytes)
		if err != nil {
			return err
		}
		if previous, err = json.Marshal(old.Interface()); err != nil {
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
	return writeMetadataRecordRow(ctx, p.tx, p.workspace, key, metadataRecord{data: raw, order: order}, previous)
}
