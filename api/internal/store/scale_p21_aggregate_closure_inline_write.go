package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"flow/api/internal/domain"
)

const metadataLogicalInlineWorkLimit = 2 << 20

// Reference refresh serializes one member at a time. The unrelated root and
// the complete decoded Members collection never enter Go working memory.
func (s *SQLiteStore) persistMetadataLogicalInlineField(ctx context.Context, tx *sqlTx, workspace, name string, before, after metadataLogicalNode) error {
	field, ok := metadataFieldByName(name)
	if !ok || before.typ != after.typ || before.typ != reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type {
		return ErrMetadataRecordQuery
	}
	root, closeRoot, err := s.metadataLogicalRootSnapshot(ctx, tx, workspace)
	if err != nil {
		return err
	}
	defer closeRoot()
	if root.shapes[name] != "" {
		return fmt.Errorf("%w: %s is a split field, not an inline field", ErrBoundedMetadataCoverage, name)
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=?", workspace, name).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("%w: inline %s has unexpected split rows", ErrBoundedMetadataCoverage, name)
	}
	value, release, err := s.spoolMetadataLogicalInline(ctx, workspace, after)
	if err != nil {
		return err
	}
	defer release()
	return s.patchMetadataLogicalRoot(ctx, tx, workspace, root.stamp.workspaceID, map[string]metadataLogicalRootReplacement{name: value}, true)
}

func (s *SQLiteStore) spoolMetadataLogicalInline(ctx context.Context, workspace string, node metadataLogicalNode) (_ metadataLogicalRootReplacement, closeSpool func(), err error) {
	spool, err := newMetadataLogicalRecordSpool(s.metadataLogicalScratchContext(ctx, workspace))
	if err != nil {
		return metadataLogicalRootReplacement{}, nil, err
	}
	defer func() {
		if err != nil {
			_ = spool.Close()
		}
	}()
	write := func(raw []byte) error {
		if int64(len(raw)) > int64(s.maxStateBytes)-spool.bytes {
			return fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
		}
		_, err := spool.Write(raw)
		return err
	}
	encode := func(value metadataLogicalNode) error {
		if value.cost > uint64(metadataWorkBytes/8) {
			return ErrMetadataWorkBudget
		}
		required := int(value.cost)*8 + 128<<10
		release, err := s.reserveMetadataLogicalRootWork(ctx, required)
		if err != nil {
			return err
		}
		defer release()
		decoded, err := value.Value(ctx, required)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(decoded.Interface())
		if err != nil {
			return err
		}
		return write(raw)
	}
	if node.typ.Kind() == reflect.Slice && node.typ.Elem().Kind() != reflect.Uint8 {
		isNil, err := node.isNil(ctx)
		if err != nil {
			return metadataLogicalRootReplacement{}, nil, err
		}
		if isNil {
			err = write([]byte("null"))
		} else if err = write([]byte("[")); err == nil {
			first := true
			err = node.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error {
				if !first {
					if err := write([]byte(",")); err != nil {
						return err
					}
				}
				first = false
				return encode(child)
			})
			if err == nil {
				err = write([]byte("]"))
			}
		}
	} else {
		err = encode(node)
	}
	if err != nil {
		return metadataLogicalRootReplacement{}, nil, err
	}
	return metadataLogicalRootReplacement{spool: spool, size: spool.bytes}, func() { _ = spool.Close() }, nil
}
