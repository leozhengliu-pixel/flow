package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

func validateMetadataLogicalRootIdentity(ctx context.Context, source *metadataLogicalSource) error {
	matched := false
	err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name != "workspace" {
			return nil
		}
		id, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{node}, "ID")
		if err != nil {
			return err
		}
		matched, err = metadataAggregateStringsEqual(ctx, id,
			metadataAggregateNativeNode{reflect.ValueOf(source.Stamp().workspaceID)})
		return err
	})
	if err != nil {
		return err
	}
	if !matched {
		return ErrMetadataRecordQuery
	}
	return nil
}

// validateMetadataLogicalRootRead preserves resident root certification without
// materializing unrelated inline records. Policy extraction belongs to the
// normalized logical source and its separate policy/work admission.
func (s *SQLiteStore) validateMetadataLogicalRootRead(ctx context.Context, tx *sqlTx, workspace string, read *MetadataCollectionRead) error {
	if read == nil || read.closed {
		return ErrMetadataRecordQuery
	}
	if read.rootSize > int64(s.maxStateBytes) {
		return fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	type fieldHeader struct {
		first byte
		size  int64
	}
	fields := map[string]fieldHeader{}
	var manifest metadataCollectionRange
	scanner := read.scanner(metadataCollectionRange{size: read.rootSize})
	if err := scanner.spaces(); err != nil {
		return err
	}
	err := scanner.object(func(key, value metadataCollectionRange) error {
		name, err := read.shortString(key)
		if err != nil {
			return err
		}
		if name == metadataCollectionsKey {
			manifest = value
			return nil
		}
		if _, known := metadataResidencyFields()[name]; !known {
			return fmt.Errorf("%w: unknown field %q", ErrBoundedMetadataCoverage, name)
		}
		input := read.scanner(value)
		if err := input.spaces(); err != nil {
			return err
		}
		first, err := input.peek()
		fields[name] = fieldHeader{first, value.size}
		return err
	}, 0)
	if err != nil {
		return err
	}
	if err := scanner.end(); err != nil {
		return err
	}
	if manifest.size == 0 {
		return fmt.Errorf("%w: split metadata backfill is required", ErrBoundedMetadataCoverage)
	}
	shapes := map[string]string{}
	input := read.scanner(manifest)
	if err := input.spaces(); err != nil {
		return err
	}
	if first, err := input.peek(); err != nil {
		return err
	} else if first != 'n' {
		if err := input.object(func(key, value metadataCollectionRange) error {
			name, err := read.shortString(key)
			if err != nil {
				return err
			}
			mode, known := metadataResidencyFields()[name]
			if !known {
				return fmt.Errorf("%w: unknown collection %q", ErrBoundedMetadataCoverage, name)
			}
			if mode == "core" {
				return fmt.Errorf("%w: core field %q must remain inline", ErrBoundedMetadataCoverage, name)
			}
			if value.size > 4096 {
				return ErrMetadataRecordQuery
			}
			raw := make([]byte, int(value.size))
			if _, err := read.rootSource().ReadAt(raw, value.offset); err != nil {
				return err
			}
			var shape string
			if err := json.Unmarshal(raw, &shape); err != nil {
				return err
			}
			shapes[name] = shape
			return nil
		}, 0); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(shapes))
	for name, shape := range shapes {
		if shape != "array" && shape != "map" && shape != "updates" {
			return ErrMetadataRecordQuery
		}
		names = append(names, name)
	}
	for name, value := range fields {
		mode := metadataResidencyFields()[name]
		if shapes[name] == "" && value.size > 2 && (value.first == '[' || value.first == '{') && mode != "core" && mode != "records" {
			return fmt.Errorf("%w: unsplit field %q", ErrBoundedMetadataCoverage, name)
		}
	}
	slices.Sort(names)
	clause, values := bindList("field", names)
	var unknown string
	err = tx.QueryRowContext(ctx, `SELECT field FROM workspace_metadata_records WHERE workspace_key=? AND NOT (`+clause+`) ORDER BY field,record_key LIMIT 1`,
		append([]any{workspace}, values...)...).Scan(&unknown)
	if err == nil {
		return fmt.Errorf("%w: canonical field %q is outside the collection manifest", ErrBoundedMetadataCoverage, unknown)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if s.metadataResidency.requireCoverage {
		return s.validateResidentBackfill(ctx, tx, workspace)
	}
	return nil
}
