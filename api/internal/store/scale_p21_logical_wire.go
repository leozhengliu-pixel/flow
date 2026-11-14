package store

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"reflect"
	"time"
)

const metadataLogicalWireTime byte = 2
const metadataLogicalWireTimeMaxBytes = 256

func metadataLogicalWireCustomMarshaler(typ reflect.Type) bool {
	if typ == reflect.TypeFor[time.Time]() || typ == reflect.TypeFor[*time.Time]() {
		return false
	}
	for _, candidate := range []reflect.Type{typ, reflect.PointerTo(typ)} {
		if candidate.Implements(reflect.TypeFor[json.Marshaler]()) || candidate.Implements(reflect.TypeFor[encoding.TextMarshaler]()) {
			return true
		}
	}
	return false
}

// WireValue owns a typed JSON-equivalent value under the caller's complete
// decoded-byte allowance. Detached time zones preserve wire output, not native
// equality; this result must never become a mutation preimage.
func (n metadataLogicalNode) WireValue(ctx context.Context, maxBytes int) (reflect.Value, error) {
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, err
	}
	if maxBytes < 0 || n.cost > uint64(maxBytes) {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := n.source.Retain(); err != nil {
		return reflect.Value{}, err
	}
	defer n.source.Release()
	return n.materialize(ctx, 0, true)
}

// Unknown Location graphs stay unavailable to native readers. Their bounded
// wall-clock/offset receipt is independently checked against captured JSON.
func (b *metadataLogicalBuilder) writeWireTime(node metadataLogicalNode, value time.Time) (metadataLogicalNode, error) {
	raw, marshalErr := value.MarshalJSON()
	failed := byte(0)
	if marshalErr != nil {
		failed, raw = 1, []byte(marshalErr.Error())
	}
	if len(raw) > metadataLogicalWireTimeMaxBytes {
		return node, ErrBoundedMetadataCoverage
	}
	node.cost += 512 + uint64(len(raw))*2
	year, month, day := value.Date()
	hour, minute, second := value.Clock()
	_, offset := value.Zone()
	parts := [8]int{year, int(month), day, hour, minute, second, value.Nanosecond(), offset}
	if err := b.write([]byte{metadataLogicalWireTime}); err != nil {
		return node, err
	}
	for _, part := range parts {
		if err := b.writeUint(uint64(part)); err != nil {
			return node, err
		}
	}
	if err := b.write([]byte{failed}); err != nil {
		return node, err
	}
	if err := b.writeUint(uint64(len(raw))); err != nil {
		return node, err
	}
	return node, b.write(raw)
}

func (n metadataLogicalNode) wireTimeValue(cursor *metadataLogicalCursor) (reflect.Value, error) {
	var parts [8]int
	for index := range parts {
		part, err := cursor.uint()
		if err != nil {
			return reflect.Value{}, err
		}
		parts[index] = int(part)
	}
	failed, err := cursor.byte()
	if err != nil {
		return reflect.Value{}, err
	}
	size, err := cursor.uint()
	if err != nil {
		return reflect.Value{}, err
	}
	if failed > 1 || size > metadataLogicalWireTimeMaxBytes {
		return reflect.Value{}, ErrMetadataRecordQuery
	}
	var raw [metadataLogicalWireTimeMaxBytes]byte
	if err := cursor.read(raw[:size]); err != nil {
		return reflect.Value{}, err
	}
	value := time.Date(parts[0], time.Month(parts[1]), parts[2], parts[3], parts[4], parts[5], parts[6], time.FixedZone("", parts[7]))
	actual, marshalErr := value.MarshalJSON()
	if failed == 1 {
		if marshalErr == nil || marshalErr.Error() != string(raw[:size]) {
			return reflect.Value{}, ErrBoundedMetadataCoverage
		}
	} else if marshalErr != nil || !bytes.Equal(actual, raw[:size]) {
		return reflect.Value{}, ErrBoundedMetadataCoverage
	}
	// Invalid times remain typed invalid values: the eventual encoder keeps
	// original traversal, omitempty and pointer MarshalJSON error semantics.
	return reflect.ValueOf(value), nil
}
