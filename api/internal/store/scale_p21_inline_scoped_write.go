package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) prepareScopedInlineSelection(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, scope MetadataRecordScope) (MetadataRecordScope, []string, int, func(), error) {
	fields, err := s.inlineMetadataFields(ctx, tx, workspace, metadataRecordFields(scope)...)
	if err != nil {
		return scope, nil, 0, nil, err
	}
	// RawMessage's Go slice is an opaque JSON field, not an entity array.
	// Its stored object can be split into map rows by the literal writer.
	// Complete-field persistence already owns that representation change.
	fields = slices.DeleteFunc(fields, func(name string) bool {
		index, _, _ := metadataLogicalField(name)
		return reflect.TypeFor[domain.Bootstrap]().Field(index).Type == metadataAggregateRawPayloadType
	})
	load := scope
	load.Fields = slices.Clone(scope.Fields)
	limit := uint64(2 << 20)
	for _, name := range fields {
		index, _, _ := metadataLogicalField(name)
		cost := source.fields[index].cost
		if cost > metadataWorkBytes/8 || limit > metadataWorkBytes/8-cost {
			return scope, nil, 0, nil, ErrMetadataWorkBudget
		}
		limit += cost
		if !slices.Contains(load.Fields, name) {
			load.Fields = append(load.Fields, name)
		}
	}
	release := func() {}
	if extra := int(limit-(2<<20)) * 4; extra != 0 {
		_, release, err = s.tryAdmitAdditionalMetadataWork(ctx, extra)
		if err != nil {
			return scope, nil, 0, nil, err
		}
	}
	return load, fields, int(limit), release, nil
}

// Point scopes permit edits to every occurrence of a selected ID, but they do
// not grant writes to later duplicate occurrences of another ID.
func validateScopedInlineMetadataChanges(ctx context.Context, before, next domain.Bootstrap, scope MetadataRecordScope, fields []string, appendOrders map[metadataRecordKey]int) error {
	left, right := reflect.ValueOf(before), reflect.ValueOf(next)
	for _, name := range fields {
		if slices.Contains(scope.Fields, name) {
			continue
		}
		field, ok := metadataFieldByName(name)
		if !ok {
			return ErrMetadataRecordQuery
		}
		old, after := left.Field(field.index), right.Field(field.index)
		if old.Kind() != reflect.Slice {
			continue
		}
		untouched := func(value reflect.Value) ([]reflect.Value, map[string]int, error) {
			var result []reflect.Value
			counts := map[string]int{}
			for index := 0; index < value.Len(); index++ {
				if err := ctx.Err(); err != nil {
					return nil, nil, err
				}
				record := value.Index(index)
				id, ok := metadataRecordValueKey(record)
				if !ok {
					return nil, nil, ErrMetadataRecordQuery
				}
				if metadataRecordSelected(scope, name, id, true) {
					counts[id]++
				} else {
					result = append(result, record)
				}
			}
			return result, counts, nil
		}
		a, oldCounts, err := untouched(old)
		if err != nil {
			return err
		}
		b, nextCounts, err := untouched(after)
		if err != nil {
			return err
		}
		if len(a) != len(b) {
			return fmt.Errorf("%w: undeclared inline membership %s", ErrMetadataRecordQuery, name)
		}
		for index := range a {
			if !metadataValueEqual(a[index], b[index]) {
				return fmt.Errorf("%w: undeclared inline record %s", ErrMetadataRecordQuery, name)
			}
		}
		for id, count := range nextCounts {
			if count > oldCounts[id] {
				if _, allowed := appendOrders[metadataRecordKey{field: name, key: id}]; !allowed {
					return fmt.Errorf("%w: point creation requires a complete field or declared append", ErrMetadataRecordQuery)
				}
			}
		}
	}
	return nil
}

// The caller passes nil recordBase to persistWorkspaceTx. This method owns
// only its metadata block; account metadata, import inputs and events still
// use that original transaction function.
func (s *SQLiteStore) persistScopedCompleteFields(ctx context.Context, tx *sqlTx, workspace string, before, target *domain.Bootstrap, scope MetadataRecordScope, inline []string) error {
	old := domain.Bootstrap{Workspace: before.Workspace}
	next := domain.Bootstrap{Workspace: target.Workspace}
	left, right := reflect.ValueOf(&old).Elem(), reflect.ValueOf(&next).Elem()
	original, changed := reflect.ValueOf(before).Elem(), reflect.ValueOf(target).Elem()
	for _, field := range persistedMetadataFields() {
		if (field.name == "viewer" || slices.Contains(scope.Fields, field.name)) && !slices.Contains(inline, field.name) {
			left.Field(field.index).Set(original.Field(field.index))
			right.Field(field.index).Set(changed.Field(field.index))
		}
	}
	fields := changedMetadataFields(old, next)
	if len(fields) == 0 {
		return nil
	}
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return err
	}
	readCtx := withLegacyMetadataDeltaReadBudget(ctx, owner)
	replaced, _, err := s.rebaseMetadata(readCtx, tx, workspace, &old, &next, fields, nil)
	if err != nil {
		return err
	}
	for _, field := range replaced {
		setRebasedField(field, changed.Field(field.index), right.Field(field.index))
	}
	fields = changedMetadataFields(old, next)
	if len(fields) == 0 {
		return nil
	}
	length := owner.byteLength("data")
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT "+length+" FROM workspace_states WHERE workspace_key=?", workspace).Scan(&size); err != nil {
		return err
	}
	root, err := s.scanMetadataInlineRoot(ctx, tx, workspace, size)
	if err != nil {
		return err
	}
	defer root.Close()
	shapes, err := root.shapes()
	if err != nil {
		return err
	}
	refs := newIssueReferences(domain.Bootstrap{Users: target.Users})
	names := []string{}
	for _, field := range fields {
		a, b := left.Field(field.index), right.Field(field.index)
		done := false
		if shapes[field.name] == "updates" {
			done, err = writeUpdatesFieldDelta(readCtx, tx, workspace, refs, field, a, b)
		} else if shapes[field.name] == "array" {
			done, err = writeArrayFieldDelta(readCtx, tx, workspace, refs, field, a, b)
		}
		if err != nil {
			return err
		}
		if !done {
			names = append(names, field.name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return s.writeStreamedMetadataSlowFieldsRoot(ctx, tx, workspace, old, next, names, refs, root, owner)
}

func (s *SQLiteStore) ensureScopedMetadataShape(ctx context.Context, tx *sqlTx, workspace, field, shape, lock string, streamed bool) error {
	if !streamed {
		return ensureOAuthMetadataShape(ctx, tx, workspace, field, shape, lock)
	}
	owner := &ResourceRead{store: s}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return err
	}
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT "+owner.byteLength("data")+" FROM workspace_states WHERE workspace_key=?"+lock, workspace).Scan(&size); err != nil {
		return err
	}
	root, err := s.scanMetadataInlineRoot(ctx, tx, workspace, size)
	if err != nil {
		return err
	}
	defer root.Close()
	shapes, err := root.shapes()
	if err != nil {
		return err
	}
	if shapes[field] == shape {
		return nil
	}
	if part, exists := root.fields[field]; exists && shapes[field] == "" {
		if part.size > 4 {
			return ErrMetadataFieldInline
		}
		raw := make([]byte, int(part.size))
		if _, err := root.source.ReadAt(raw, part.offset); err != nil {
			return err
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) != 0 && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte("[]")) && !bytes.Equal(raw, []byte("{}")) {
			return ErrMetadataFieldInline
		}
	}
	shapes[field] = shape
	manifest, err := json.Marshal(shapes)
	if err != nil {
		return err
	}
	raw := json.RawMessage("{}")
	if shape == "array" {
		raw = json.RawMessage("[]")
	}
	return s.writeStreamedMetadataRootPatch(ctx, tx, workspace, "", root,
		map[string]json.RawMessage{field: raw, metadataCollectionsKey: manifest}, owner, false)
}
