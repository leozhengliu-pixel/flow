package store

import (
	"context"
	"errors"
	"reflect"
)

func (r *ResourceRead) residentField(kind string) reflect.Value {
	index, _, _ := metadataLogicalField(kind)
	node := r.source.fields[index]
	if !node.generic && !node.unavailable && !node.blocked {
		if field := r.image.matchingField(kind, node); field != nil {
			return field.value
		}
	}
	return reflect.Value{}
}

func (r *ResourceRead) residentRecord(ctx context.Context, kind string, ordinal int, node metadataLogicalNode, wire bool) (reflect.Value, error) {
	if field := r.residentField(kind); ordinal >= 0 && field.IsValid() && ordinal < field.Len() && !node.generic && !node.unavailable && !node.blocked {
		return r.ownedResidentValue(ctx, node, field.Index(ordinal))
	}
	return r.node(ctx, node, wire)
}

func (r *ResourceRead) ownedResidentValue(ctx context.Context, node metadataLogicalNode, value reflect.Value) (reflect.Value, error) {
	if err := r.reserveNode(ctx, node); err != nil {
		return reflect.Value{}, err
	}
	return exactCopyValue(value), nil
}

// A decoded array and the retained spool's header index share ordinals.
// Only the selected records need their frames visited on the resident path.
func (r *ResourceRead) visitResidentOrdinals(ctx context.Context, kind string, length int, ordinals []int, visit func(int, metadataLogicalNode) error) error {
	if len(ordinals) == 0 {
		return ctx.Err()
	}
	index, _, _ := metadataLogicalField(kind)
	node := r.source.fields[index]
	if !node.generic {
		const sliceHeaderBytes = 16 + 1 + 8 // Frame header, presence, and length.
		if headers, ok := node.source.childIndex(node.offset, node.size, node.offset+sliceHeaderBytes, length); ok {
			for _, ordinal := range ordinals {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := visit(ordinal, headers[ordinal].node(node.source, node.typ.Elem())); err != nil {
					if errors.Is(err, ErrMetadataIterationDone) {
						return nil
					}
					return err
				}
			}
			return nil
		}
	}
	return r.visitNative(ctx, kind, func(ordinal int, child metadataLogicalNode) error {
		if ordinal != ordinals[0] {
			return nil
		}
		ordinals = ordinals[1:]
		if err := visit(ordinal, child); err != nil {
			return err
		}
		if len(ordinals) == 0 {
			return ErrMetadataIterationDone
		}
		return nil
	})
}

func (r *ResourceRead) visitNativeID(ctx context.Context, kind, id string, visit func(int, metadataLogicalNode) error) error {
	index, _, _ := metadataLogicalField(kind)
	node := r.source.fields[index]
	if field := r.residentField(kind); field.IsValid() {
		if byID, ok := r.image.matchingRecordOrdinals(kind, node); ok {
			return r.visitResidentOrdinals(ctx, kind, field.Len(), byID[id], visit)
		}
	}
	return r.visitNative(ctx, kind, func(ordinal int, child metadataLogicalNode) error {
		matched, err := r.nativeID(ctx, child, id)
		if err != nil || !matched {
			return err
		}
		return visit(ordinal, child)
	})
}
