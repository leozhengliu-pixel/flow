package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
)

// A collection's first records (an entity list that was nil or empty and
// has no canonical rows) take the full-field kernel: the literal writer
// encodes the whole list, splits it into rows with orders 0..n-1, writes
// each row and marks the field "array" in the root. When one of those rows
// is large (a loop's first version snapshotting 11 MiB of instructions)
// the full-field kernel's whole-list copies exceed the work budget. The
// large first-records kernel keeps the literal result:
//
//   - rebase: rebaseMetadata merges only records present before and after,
//     so with no records before it changes nothing; the full-field rebase
//     is skipped instead of materializing the list;
//   - root: the literal splitter stores an entity list's root member as []
//     whatever its records, so the slow writer receives the field as an
//     empty list and writes the root member and shape exactly as for the
//     records (its row sync finds no rows of the field and writes none);
//   - rows: each record is then written at its splitter order by the row
//     writers (writeLargeRow for a large row, the original row write for a
//     small one), with no previous row.
//
// The list must split into rows (every ID non-empty, at most 191 bytes and
// unique, the ID member named id) and the field must have no canonical
// rows; anything else keeps the full-field kernel.

// largeRowFirstRecords reports whether f is such a first-records write and
// returns its rows (IDs and nodes in list order).
func (p *legacyMetadataWritePlan) largeRowFirstRecords(ctx context.Context, f *legacyMetadataWriteField) ([]legacyMetadataWriteRow, bool, error) {
	kind, ok := largeRowKind(f)
	if !ok || f.next.typ == nil || p.progress != nil && f.field.name == "projects" {
		return nil, false, nil
	}
	id, _ := kind.FieldByName("ID")
	if tag := id.Tag.Get("json"); id.PkgPath != "" || len(id.Index) != 1 || tag != "id" && tag != "id,omitempty" {
		return nil, false, nil
	}
	empty := true
	if err := f.before.Children(ctx, func(reflect.Value, metadataLogicalNode) error {
		empty = false
		return ErrMetadataIterationDone
	}); err != nil && !errors.Is(err, ErrMetadataIterationDone) {
		return nil, false, err
	}
	if !empty {
		return nil, false, nil
	}
	if absent, err := f.next.isNil(ctx); err != nil || absent {
		return nil, false, err
	}
	large := false
	if err := f.next.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		if record.cost > largeRowNodeCost {
			large = true
			return ErrMetadataIterationDone
		}
		return nil
	}); err != nil && !errors.Is(err, ErrMetadataIterationDone) {
		return nil, false, err
	}
	if !large {
		return nil, false, nil
	}
	var rows []legacyMetadataWriteRow
	seen := map[string]bool{}
	valid := true
	err := f.next.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		node, err := issueHotField(ctx, record, "ID")
		if err != nil {
			return err
		}
		if node.typ == nil || node.cost > 4096 {
			valid = false
			return ErrMetadataIterationDone
		}
		if err := p.work.reserve(ctx, node.cost*2+256); err != nil {
			return err
		}
		value, err := node.Value(ctx, int(node.cost))
		if err != nil {
			return err
		}
		key := value.String()
		if key == "" || len(key) > 191 || seen[key] {
			valid = false
			return ErrMetadataIterationDone
		}
		seen[key] = true
		rows = append(rows, legacyMetadataWriteRow{id: key, next: record, oldOrdinal: -1, ordinal: len(rows)})
		return nil
	})
	if err != nil && !errors.Is(err, ErrMetadataIterationDone) {
		return nil, false, err
	}
	if !valid || len(rows) == 0 {
		return nil, false, nil
	}
	var stored int
	if err := p.tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?", p.workspace, f.field.name).Scan(&stored); err != nil {
		return nil, false, err
	}
	if stored != 0 {
		return nil, false, nil
	}
	return rows, true, nil
}

// rebaseLargeRowFirstRecords is rebaseFull's kernel for first records:
// rebaseMetadata leaves a collection without earlier records unchanged.
func (p *legacyMetadataWritePlan) rebaseLargeRowFirstRecords(ctx context.Context, f *legacyMetadataWriteField) (bool, error) {
	_, handled, err := p.largeRowFirstRecords(ctx, f)
	return handled, err
}

// largeRowFirstSkeleton is the field the slow writer receives: the original
// before value (nil or empty) and the records' ID-only stand-ins, from
// which the splitter derives the same root member and shape (an empty list
// would be omitted by an omitempty field).
func (p *legacyMetadataWritePlan) largeRowFirstSkeleton(ctx context.Context, f *legacyMetadataWriteField, rows []legacyMetadataWriteRow) (reflect.Value, reflect.Value, error) {
	before := reflect.Zero(f.before.typ)
	absent, err := f.before.isNil(ctx)
	if err != nil {
		return reflect.Value{}, reflect.Value{}, err
	}
	if !absent {
		before = reflect.MakeSlice(f.before.typ, 0, 0)
	}
	if err := p.work.reserve(ctx, uint64(len(rows))*(uint64(f.next.typ.Elem().Size())*2+512)+4096); err != nil {
		return reflect.Value{}, reflect.Value{}, err
	}
	after := reflect.MakeSlice(f.next.typ, len(rows), len(rows))
	for index, row := range rows {
		after.Index(index).FieldByName("ID").SetString(row.id)
	}
	return before, after, nil
}

// writeLargeRowFirstRecords writes the first records' rows at their
// splitter orders after the slow writer wrote the root.
func (p *legacyMetadataWritePlan) writeLargeRowFirstRecords(ctx context.Context, f *legacyMetadataWriteField, rows []legacyMetadataWriteRow) error {
	for index := range rows {
		row := &rows[index]
		if handled, err := p.writeLargeRow(ctx, f, row, row.ordinal); handled || err != nil {
			if err != nil {
				return err
			}
			continue
		}
		// writeArray's row write for a row without a previous row.
		if err := func() error {
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
			return writeMetadataRecordRow(ctx, p.tx, p.workspace, metadataRecordKey{f.field.name, row.id}, metadataRecord{data: raw, order: row.ordinal}, nil)
		}(); err != nil {
			return err
		}
	}
	return nil
}

// largeRowFirstWrite is one first-records field the persist step writes
// after the slow writer.
type largeRowFirstWrite struct {
	field *legacyMetadataWriteField
	rows  []legacyMetadataWriteRow
}

// largeRowRootOnlyFieldsKey names the first-records fields the slow writer
// receives as ID-only stand-in lists: it writes their root member and shape
// (which the splitter derives from the IDs alone) and leaves their rows to
// writeLargeRowFirstRecords.
type largeRowRootOnlyFieldsKey struct{}

// largeRowSyncFields drops the root-only fields from a slow writer's row
// sync: their wanted stand-in records and their field names. Other fields
// keep the original sync arguments.
func largeRowSyncFields(ctx context.Context, wanted map[metadataRecordKey]metadataRecord, names []string) (map[metadataRecordKey]metadataRecord, []string) {
	rootOnly, _ := ctx.Value(largeRowRootOnlyFieldsKey{}).(map[string]bool)
	if len(rootOnly) == 0 {
		return wanted, names
	}
	kept := make(map[metadataRecordKey]metadataRecord, len(wanted))
	for key, value := range wanted {
		if !rootOnly[key.field] {
			kept[key] = value
		}
	}
	fields := make([]string, 0, len(names))
	for _, name := range names {
		if !rootOnly[name] {
			fields = append(fields, name)
		}
	}
	return kept, fields
}
