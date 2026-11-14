package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"reflect"

	"flow/api/internal/domain"
)

type metadataLogicalPhysicalBinding struct {
	Generation [16]byte
	Logical    RecordKey
	Canonical  RecordKey
	Storage    uint8
}

func metadataLogicalUnitsPhysicalKey(unit uint64) []byte {
	var key [8]byte
	binary.BigEndian.PutUint64(key[:], unit)
	return key[:]
}

func metadataLogicalUnitsPhysicalValue(unit uint64, key RecordKey, storage uint8) []byte {
	value := make([]byte, 11+len(key.Kind)+len(key.ID))
	value[0] = 3
	binary.BigEndian.PutUint64(value[1:9], unit)
	value[9], value[10] = storage, byte(len(key.Kind))
	copy(value[11:], key.Kind)
	copy(value[11+len(key.Kind):], key.ID)
	return value
}

func metadataLogicalUnitsDecodePhysical(value []byte) (RecordKey, uint8, error) {
	if len(value) < 11 || value[0] != 3 || value[9] > 1 || int(value[10]) > len(value)-11 {
		return RecordKey{}, 0, ErrMetadataRecordQuery
	}
	cut := 11 + int(value[10])
	if len(value)-cut > 191 {
		return RecordKey{}, 0, ErrMetadataRecordQuery
	}
	return RecordKey{Kind: string(value[11:cut]), ID: string(value[cut:])}, value[9], nil
}

// Only a producer that observed canonical row identity may supply bindings.
// Binding creates a distinct immutable root; an earlier pinned root does not
// gain a locator after publication. A body ID never invents a physical key.
func (s *metadataLogicalSource) WithPhysicalBindings(ctx context.Context, bindings []metadataLogicalPhysicalBinding) (*metadataLogicalSource, error) {
	if len(bindings) > MetadataRecordBatchMaxKeys {
		return nil, ErrMetadataPageBudget
	}
	if err := s.Retain(); err != nil {
		return nil, err
	}
	defer s.Release()
	if s.units == nil {
		return nil, ErrMetadataRecordQuery
	}
	base, a := s.units, s.units.arena
	root, err := a.change(ctx, base, s.Stamp(), func(tx *sql.Tx, trees *metadataLogicalUnitTrees, _ []metadataLogicalUnitsField) error {
		fields := map[string]metadataLogicalNode{}
		for i, node := range s.fields {
			if node.typ != nil {
				fields[jsonFieldName(reflect.TypeFor[domain.Bootstrap]().Field(i))] = node
			}
		}
		c := &sw29LogicalCorpus{index: a.index, fields: fields, points: trees[1], unitArena: a, unitContext: ctx}
		for _, binding := range bindings {
			if binding.Generation != s.generation || binding.Storage > 1 || len(binding.Canonical.Kind) > 191 || len(binding.Canonical.ID) > 191 ||
				binding.Canonical.Kind != binding.Logical.Kind {
				return ErrMetadataRecordQuery
			}
			var unit uint64
			if err := c.pointRecords(ctx, binding.Logical.Kind, binding.Logical.ID, func(_ uint64, node metadataLogicalNode) error {
				if unit != 0 {
					return ErrMetadataRecordQuery
				}
				unit = node.source.file.(*metadataLogicalUnitFile).unit
				return nil
			}); err != nil {
				return err
			}
			if unit == 0 {
				return sql.ErrNoRows
			}
			value := metadataLogicalUnitsPhysicalValue(unit, binding.Canonical, binding.Storage)
			key := metadataLogicalUnitsPhysicalKey(unit)
			existing, found, err := a.index.Get(ctx, trees[5], key)
			if err != nil {
				return err
			}
			if found && !bytes.Equal(existing, value) {
				return ErrMetadataRecordQuery
			}
			trees[5], err = a.index.Put(ctx, trees[5], key, value)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	source, err := root.source(ctx)
	if err != nil {
		root.Release()
	}
	return source, err
}

func (s *metadataLogicalSource) PhysicalBinding(ctx context.Context, logical RecordKey) (RecordKey, uint8, error) {
	if err := s.Retain(); err != nil {
		return RecordKey{}, 0, err
	}
	defer s.Release()
	if s.units == nil {
		return RecordKey{}, 0, ErrMetadataRecordQuery
	}
	c := s.unitCorpus(ctx)
	var unit uint64
	if err := c.pointRecords(ctx, logical.Kind, logical.ID, func(_ uint64, node metadataLogicalNode) error {
		if unit != 0 {
			return ErrMetadataRecordQuery
		}
		unit = node.source.file.(*metadataLogicalUnitFile).unit
		return nil
	}); err != nil {
		return RecordKey{}, 0, err
	}
	if unit == 0 {
		return RecordKey{}, 0, sql.ErrNoRows
	}
	value, found, err := c.index.Get(ctx, s.units.trees[5], metadataLogicalUnitsPhysicalKey(unit))
	if err != nil {
		return RecordKey{}, 0, err
	}
	if !found {
		return RecordKey{}, 0, sql.ErrNoRows
	}
	return metadataLogicalUnitsDecodePhysical(value)
}
