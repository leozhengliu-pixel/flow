package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// Dedicated complete inline-field writes own their full dynamically admitted
// inputs. Keep the generic page/record contract for every other declaration.
func validateDiscussionInlineSelection(data domain.Bootstrap, scope MetadataRecordScope, inline []string) error {
	validation := data
	selected := scope
	selected.Fields = slices.Clone(scope.Fields)
	complete := []string{}
	for _, name := range inline {
		if !slices.Contains(scope.Fields, name) {
			continue
		}
		field, ok := metadataFieldByName(name)
		if !ok {
			return ErrMetadataRecordQuery
		}
		reflect.ValueOf(&validation).Elem().Field(field.index).SetZero()
		complete = append(complete, name)
	}
	selected.Fields = slices.DeleteFunc(selected.Fields, func(name string) bool { return slices.Contains(complete, name) })
	selected.Read = slices.DeleteFunc(slices.Clone(scope.Read), func(key RecordKey) bool { return slices.Contains(complete, key.Kind) })
	selected.Write = slices.DeleteFunc(slices.Clone(scope.Write), func(key RecordKey) bool { return slices.Contains(complete, key.Kind) })
	return validateMetadataLogicalSelection(validation, selected)
}

// Inline settings have no canonical cursor row. Read their complete JSON
// field under its own admission, retaining only the selected cursor afterward.
// The same SQL snapshot and root lock are used by normal writes and retries.
func (w *pulseSummaryWork) inlineCursor(tx *sqlTx, workspace, lock string) ([]byte, bool, error) {
	var size int64
	length := w.store.scaleP21SW22VisibilityLength("data")
	if err := tx.QueryRowContext(w.ctx, "SELECT "+length+" FROM workspace_states WHERE workspace_key=?"+lock, workspace).Scan(&size); err != nil {
		return nil, false, err
	}
	if size < 0 || size > int64(w.store.maxStateBytes) {
		return nil, false, ErrMetadataWorkBudget
	}
	if err := w.reserve(2 << 20); err != nil {
		return nil, false, err
	}
	root, err := w.store.scanMetadataInlineRoot(w.ctx, tx, workspace, size)
	if err != nil {
		return nil, false, err
	}
	shapes, err := root.shapes()
	if err != nil {
		return nil, false, err
	}
	if shapes["settings"] != "" {
		return nil, false, ErrConcurrentWrite
	}
	part, exists := root.fields["settings"]
	if !exists {
		return nil, false, nil
	}
	if part.size < 0 || uint64(part.size) > (metadataWorkBytes-(64<<10))/32 {
		return nil, false, ErrMetadataWorkBudget
	}
	if err := w.reserve(uint64(part.size)*32 + 64<<10); err != nil {
		return nil, false, err
	}
	raw := make([]byte, int(part.size))
	if _, err := root.source.ReadAt(raw, part.offset); err != nil {
		return nil, false, err
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, false, err
	}
	value, found := settings["pulseDeliveryCursors"]
	return value, found, w.ctx.Err()
}

func (s *SQLiteStore) discussionStreamedRootWork(ctx context.Context, tx *sqlTx, workspace string, work *ResourceRead, size int64, changes []metadataRecordChange) error {
	if s.dialect != "sqlite" {
		// Several chunk reads must observe one root after the selected
		// metadata row locks, matching the legacy streamed writer order.
		if err := tx.QueryRowContext(ctx, "SELECT "+work.byteLength("data")+" FROM workspace_states WHERE workspace_key=? FOR UPDATE", workspace).Scan(&size); err != nil {
			return err
		}
	}
	if size < 0 || size > int64(s.maxStateBytes) {
		return ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, 2<<20); err != nil {
		return err
	}
	root, err := s.scanMetadataInlineRoot(ctx, tx, workspace, size)
	if err != nil {
		return err
	}
	shapes, err := root.shapes()
	if err != nil {
		return err
	}
	for _, change := range changes {
		field, _, ok := metadataRecordType(change.field)
		if !ok {
			return ErrMetadataRecordQuery
		}
		shape := "array"
		if reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type.Kind() == reflect.Map {
			shape = "map"
		}
		inline := false
		if part, exists := root.fields[change.field]; exists && part.size > 2 {
			first := [1]byte{}
			if _, err := root.source.ReadAt(first[:], part.offset); err != nil {
				return err
			}
			inline = first[0] == '[' || first[0] == '{'
		}
		if actual := shapes[change.field]; actual != "" && actual != shape || inline {
			return fmt.Errorf("%w: discussion point write requires split %s", ErrBoundedMetadataCoverage, change.field)
		}
	}
	return ctx.Err()
}
