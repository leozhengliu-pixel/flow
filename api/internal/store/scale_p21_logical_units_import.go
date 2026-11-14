package store

import (
	"context"
	"database/sql"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

func (a *metadataLogicalUnitsArena) change(ctx context.Context, base *metadataLogicalUnitsRoot, stamp copyStamp, change func(*sql.Tx, *metadataLogicalUnitTrees, []metadataLogicalUnitsField) error) (_ *metadataLogicalUnitsRoot, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.beginLocked(ctx)
	if err != nil {
		if a.roots == 0 {
			_ = a.closeLocked()
		}
		return nil, err
	}
	a.imports = map[*metadataLogicalStorage]uint64{}
	var candidate *metadataLogicalUnitsRoot
	committed := false
	defer a.endLocked()
	defer func() {
		_ = tx.Rollback()
		if cleanupErr := a.sweepRegistriesLocked(); cleanupErr != nil {
			a.cleanupErr = cleanupErr
		}
		if !committed && candidate != nil {
			metadataLogicalDescriptorsUsed.Add(-metadataLogicalUnitRootDescriptor)
			candidate.refs = 0
		}
		if err != nil && a.roots == 0 {
			_ = a.closeLocked()
		}
		a.imports = nil
	}()
	var trees metadataLogicalUnitTrees
	fields := make([]metadataLogicalUnitsField, reflect.TypeFor[domain.Bootstrap]().NumField())
	if base != nil {
		trees = base.trees
		fields = slices.Clone(base.fields)
	}
	if err := change(tx, &trees, fields); err != nil {
		return nil, err
	}
	root, err := a.rootLocked(ctx, tx, trees, stamp)
	if err != nil {
		return nil, err
	}
	root.fields = fields
	candidate = root
	if err := a.collectLocked(ctx, tx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	a.roots++
	committed = true
	return root, nil
}

// Cold conversion imports each record once. Ordinary indexed Forks never call
// this function and never keep the original contiguous field storage alive.
func metadataLogicalUnitsImport(ctx context.Context, source *metadataLogicalSource) (*metadataLogicalSource, error) {
	if err := source.Retain(); err != nil {
		return nil, err
	}
	defer source.Release()
	if source.units != nil {
		return nil, ErrMetadataRecordQuery
	}
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	a, err := newMetadataLogicalUnitsArena(ctx)
	if err != nil {
		return nil, err
	}
	root, err := a.change(ctx, nil, source.Stamp(), func(tx *sql.Tx, trees *metadataLogicalUnitTrees, fields []metadataLogicalUnitsField) error {
		return source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
			index, _, ok := metadataLogicalField(name)
			if !ok || name == "-" {
				return nil
			}
			descriptor := metadataLogicalUnitsField{
				typ: node.typ, cost: node.cost, unavailable: node.unavailable, blocked: node.blocked,
			}
			add := func(ordinal uint64, key *metadataLogicalNode, value metadataLogicalNode) error {
				leaf := metadataLogicalUnitsLeaf{cost: value.cost, unavailable: value.unavailable, blocked: value.blocked}
				framed := value
				if key != nil {
					if key.source != value.source || key.offset+key.size != value.offset {
						return ErrMetadataRecordQuery
					}
					leaf.keySize, leaf.keyCost = uint64(key.size), key.cost
					framed.offset, framed.size = key.offset, key.size+value.size
				}
				unit, err := a.importLocked(ctx, tx, framed)
				if err != nil {
					return err
				}
				leaf.unit = unit
				trees[0], err = a.index.Put(ctx, trees[0], metadataLogicalUnitsOrderKey(index, ordinal), leaf.encode())
				if err != nil {
					return err
				}
				descriptor.count++
				descriptor.single = leaf
				descriptor.nextOrder = max(descriptor.nextOrder, ordinal+1)
				if leaf.unavailable {
					descriptor.unavailableUnits++
				}
				return a.addLeaf(ctx, tx, trees, index, ordinal, descriptor, leaf)
			}
			switch {
			case node.typ.Kind() == reflect.Map && !node.blocked:
				var ordinal uint64
				if err := node.MapEntries(ctx, func(key, value metadataLogicalNode) error {
					err := add(ordinal, &key, value)
					ordinal++
					return err
				}); err != nil {
					return err
				}
				descriptor.nilValue, err = node.isNil(ctx)
				if err != nil {
					return err
				}
			case node.typ.Kind() == reflect.Slice && node.typ.Elem().Kind() != reflect.Uint8 && !node.blocked,
				node.typ.Kind() == reflect.Struct && name == "workspaceSettings" && !node.blocked:
				if err := node.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
					ordinal := uint64(0)
					if node.typ.Kind() == reflect.Struct {
						field, ok := node.typ.FieldByName(key.String())
						if !ok {
							return ErrMetadataRecordQuery
						}
						ordinal = uint64(field.Index[0])
					} else {
						ordinal = uint64(key.Int())
					}
					return add(ordinal, nil, value)
				}); err != nil {
					return err
				}
				descriptor.nilValue, err = node.isNil(ctx)
				if err != nil {
					return err
				}
			default:
				if err := add(0, nil, node); err != nil {
					return err
				}
			}
			fields[index] = descriptor
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	// The original storage objects are not generation owners. Only detached
	// immutable registries survive cold conversion.
	a.imports = map[*metadataLogicalStorage]uint64{}
	a.mu.Unlock()
	result, err := root.source(ctx)
	if err != nil {
		root.Release()
	}
	return result, err
}

func (r *metadataLogicalUnitsRoot) source(ctx context.Context) (*metadataLogicalSource, error) {
	source := &metadataLogicalSource{
		refs: 1, stamp: r.stamp, generation: r.generation, units: r,
		fields: make([]metadataLogicalNode, len(r.fields)),
	}
	for index, field := range r.fields {
		if field.typ == nil {
			continue
		}
		virtual := !field.blocked && (field.typ.Kind() == reflect.Map ||
			field.typ.Kind() == reflect.Slice && field.typ.Elem().Kind() != reflect.Uint8 ||
			field.typ.Kind() == reflect.Struct && reflect.TypeFor[domain.Bootstrap]().Field(index).Name == "WorkspaceSettings")
		if virtual {
			source.fields[index] = metadataLogicalNode{
				source: &metadataLogicalStorage{unitRoot: r, file: &metadataLogicalUnitFile{path: r.arena.index.stage.path}},
				typ:    field.typ, cost: field.cost, unavailable: field.unavailable, blocked: field.blocked,
				collection: &metadataLogicalUnitsCollection{root: r, field: index, metadataLogicalUnitsField: field},
			}
			continue
		}
		node, err := r.node(ctx, field.single, field.typ, false)
		if err != nil {
			return nil, err
		}
		source.fields[index] = node
	}
	return source, nil
}

func (r *metadataLogicalUnitsRoot) fieldNode(ctx context.Context, index int) (metadataLogicalNode, error) {
	if index < 0 || index >= len(r.fields) {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	field := r.fields[index]
	if field.typ == nil {
		return metadataLogicalNode{}, ErrMetadataRecordQuery
	}
	virtual := !field.blocked && (field.typ.Kind() == reflect.Map ||
		field.typ.Kind() == reflect.Slice && field.typ.Elem().Kind() != reflect.Uint8 ||
		field.typ.Kind() == reflect.Struct && reflect.TypeFor[domain.Bootstrap]().Field(index).Name == "WorkspaceSettings")
	if virtual {
		return metadataLogicalNode{
			source: &metadataLogicalStorage{unitRoot: r, file: &metadataLogicalUnitFile{path: r.arena.index.stage.path}},
			typ:    field.typ, cost: field.cost, unavailable: field.unavailable, blocked: field.blocked,
			collection: &metadataLogicalUnitsCollection{root: r, field: index, metadataLogicalUnitsField: field},
		}, nil
	}
	return r.node(ctx, field.single, field.typ, false)
}
