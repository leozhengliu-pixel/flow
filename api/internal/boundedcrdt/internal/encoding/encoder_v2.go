package encoding

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/lib0"
)

// EncoderV2 is the column-oriented update encoder matching yjs
// UpdateEncoderV2 (testdata/gen/node_modules/yjs/src/utils/
// UpdateEncoder.js:115-220) and yrs EncoderV2. Block-write methods
// route field writes to 9 per-column buffers + 1 raw rest stream;
// Bytes() concatenates them with length prefixes behind a 0x00
// feature flag byte.
//
// Wire layout — see docs/yrs-port-notes/update-v2.md §1. Column
// order is wire-load-bearing; DO NOT reorder.
//
// Top-level update headers (per-client block-count, start-clock,
// outer client-count) go to the rest stream via WriteVarUint —
// only per-block field writes get column-routed. See port note §4
// gotcha "Top-level block-run structure stays the same as V1".
//
// Pattern: construct with NewEncoderV2, call the per-field Write*
// methods + WriteVarUint for headers, then call Bytes() exactly
// once at the end to flush all columns and assemble the wire bytes.
// EncoderV2 is single-use; do not call Bytes() twice.
type EncoderV2 struct {
	rest []byte

	keyClock   *lib0.IntDiffOptRleEncoder
	client     *lib0.UintOptRleEncoder
	leftClock  *lib0.IntDiffOptRleEncoder
	rightClock *lib0.IntDiffOptRleEncoder
	info       *lib0.RleEncoder
	str        *lib0.StringEncoder
	parentInfo *lib0.RleEncoder
	typeRef    *lib0.UintOptRleEncoder
	length     *lib0.UintOptRleEncoder

	// keyClockCounter mirrors yjs's keyClock field — incremented
	// on every WriteKey because per port-note gotcha 5 the
	// encoder-side keyMap is never populated (yjs "bug" we
	// replicate bit-for-bit for wire compat).
	keyClockCounter uint32
}

// NewEncoderV2 returns a fresh EncoderV2 with all column buffers
// initialized.
func NewEncoderV2(mc *meter.Meter) *EncoderV2 {
	mc.Enter()
	defer mc.Leave()

	return meter.Pointer(mc, EncoderV2{
		keyClock:   lib0.NewIntDiffOptRleEncoder(mc),
		client:     lib0.NewUintOptRleEncoder(mc),
		leftClock:  lib0.NewIntDiffOptRleEncoder(mc),
		rightClock: lib0.NewIntDiffOptRleEncoder(mc),
		info:       lib0.NewRleEncoder(mc, lib0.WriteUint8Func),
		str:        lib0.NewStringEncoder(mc),
		parentInfo: lib0.NewRleEncoder(mc, lib0.WriteUint8Func),
		typeRef:    lib0.NewUintOptRleEncoder(mc),
		length:     lib0.NewUintOptRleEncoder(mc),
	})
}

// WriteLeftID routes the (client, clock) pair to the shared
// client column + the left-clock column. Mirrors
// UpdateEncoderV2.writeLeftID (UpdateEncoder.js:156-159).
func (e *EncoderV2) WriteLeftID(mc *meter.Meter, id block.ID) {
	mc.Enter()
	defer mc.Leave()

	e.client.Write(mc, id.Client)
	e.leftClock.Write(mc, int64(id.Clock))
}

// WriteRightID routes to the shared client column + the
// right-clock column.
func (e *EncoderV2) WriteRightID(mc *meter.Meter, id block.ID) {
	mc.Enter()
	defer mc.Leave()

	e.client.Write(mc, id.Client)
	e.rightClock.Write(mc, int64(id.Clock))
}

// WriteClient routes a standalone client ID (e.g. per-client
// block-run header) to the shared client column.
func (e *EncoderV2) WriteClient(mc *meter.Meter, client uint64) {
	mc.Enter()
	defer mc.Leave()

	e.client.Write(mc, client)
}

// WriteInfo routes an info byte to the per-byte RLE column.
func (e *EncoderV2) WriteInfo(mc *meter.Meter, info uint8) {
	mc.Enter()
	defer mc.Leave()

	e.info.Write(mc, uint64(info))
}

// WriteString routes a string to the string column. Used for
// parent root-type names and ContentString payloads.
func (e *EncoderV2) WriteString(mc *meter.Meter, s string) {
	mc.Enter()
	defer mc.Leave()

	e.str.Write(mc, s)
}

// WriteParentInfo routes the "parent is name vs item-ID" boolean
// tag to the parent-info RLE column.
func (e *EncoderV2) WriteParentInfo(mc *meter.Meter, isRootName bool) {
	mc.Enter()
	defer mc.Leave()

	var v uint64
	if isRootName {
		v = 1
	}
	e.parentInfo.Write(mc, v)
}

// WriteTypeRef routes a TypeRef tag (Array=0, Map=1, Text=2, ...)
// to the type-ref RLE column. Used inside ContentType payloads.
func (e *EncoderV2) WriteTypeRef(mc *meter.Meter, ref uint8) {
	mc.Enter()
	defer mc.Leave()

	e.typeRef.Write(mc, uint64(ref))
}

// WriteLen routes a run-length (GC range, ContentDeleted, content
// element count) to the length column.
func (e *EncoderV2) WriteLen(mc *meter.Meter, l uint64) {
	mc.Enter()
	defer mc.Leave()

	e.length.Write(mc, l)
}

// WriteVarUint routes a generic varuint to the raw rest stream.
// Used for top-level structure headers: outer client count,
// per-client block count, start clock.
func (e *EncoderV2) WriteVarUint(mc *meter.Meter, n uint64) {
	mc.Enter()
	defer mc.Leave()

	e.rest = lib0.WriteVarUint(mc, e.rest, n)
}

// WriteRestBytes appends raw bytes directly to the rest stream.
// Used by Content encoders (ContentAny, ContentBinary, etc.)
// that produce arbitrary byte payloads.
func (e *EncoderV2) WriteRestBytes(mc *meter.Meter, b []byte) {
	mc.Enter()
	defer mc.Leave()

	e.rest = meter.Append(mc, e.rest, b...)
}

// WriteAny routes a lib0 Any TLV value to the rest stream.
// Used by ContentAny / ContentEmbed when V2 hits them — note
// V2 keeps Any in rest (no column for it).
func (e *EncoderV2) WriteAny(mc *meter.Meter, v block.Any) {
	mc.Enter()
	defer mc.Leave()

	e.rest = EncodeAny(mc, e.rest, v)
}

// WriteVarBuf routes a length-prefixed byte array to the rest
// stream. Used by ContentBinary.
func (e *EncoderV2) WriteVarBuf(mc *meter.Meter, b []byte) {
	mc.Enter()
	defer mc.Leave()

	e.rest = lib0.WriteVarUint8Array(mc, e.rest, b)
}

// WriteKey emits a map-key reference. Mirrors yjs's "bug" per
// port-note gotcha 5: the encoder-side key table is NEVER
// populated, so every WriteKey writes a fresh string into the
// string column and increments the keyClock counter. The decoder
// side DOES build a key table — asymmetric but wire-compatible.
func (e *EncoderV2) WriteKey(mc *meter.Meter, key string) {
	mc.Enter()
	defer mc.Leave()

	e.keyClock.Write(mc, int64(e.keyClockCounter))
	e.str.Write(mc, key)
	e.keyClockCounter++
}

// WriteJSON writes a format value or an embed. In V2 that is a lib0 Any
// in the rest stream, NOT the JSON varstring V1 uses - yjs
// UpdateEncoderV2.writeJSON calls writeAny. This method used to reuse
// the V1 layout on the assumption that V2 "doesn't column-encode JSON
// payloads either", which is true and beside the point: the payload
// format differs, and the result was unreadable by yjs. See json_any.go.
func (e *EncoderV2) WriteJSON(mc *meter.Meter, v block.Any) {
	mc.Enter()
	defer mc.Leave()

	e.WriteAny(mc, jsonValueForAny(mc, v))
}

// Bytes flushes every column and assembles the final V2 wire
// bytes per port-note §1. Must be called exactly once; do not
// call any Write* method afterwards.
//
// Layout:
//
//	[0x00 feature flag]
//	[varuint len | keyClockBytes]
//	[varuint len | clientBytes]
//	[varuint len | leftClockBytes]
//	[varuint len | rightClockBytes]
//	[varuint len | infoBytes]
//	[varuint len | stringBytes]
//	[varuint len | parentInfoBytes]
//	[varuint len | typeRefBytes]
//	[varuint len | lenBytes]
//	[restBytes]                  (NO length prefix; reader takes the remainder)
func (e *EncoderV2) Bytes(mc *meter.Meter) []byte {
	mc.Enter()
	defer mc.Leave()

	out := meter.Literal[[]byte](mc, 129, func() []byte {
		return []byte{0x00}
	}) // feature flag
	out = lib0.WriteVarUint8Array(mc, out, e.keyClock.Bytes(mc))
	out = lib0.WriteVarUint8Array(mc, out, e.client.Bytes(mc))
	out = lib0.WriteVarUint8Array(mc, out, e.leftClock.Bytes(mc))
	out = lib0.WriteVarUint8Array(mc, out, e.rightClock.Bytes(mc))
	out = lib0.WriteVarUint8Array(mc, out, e.info.Bytes(mc))
	out = lib0.WriteVarUint8Array(mc, out, e.str.Bytes(mc))
	out = lib0.WriteVarUint8Array(mc, out, e.parentInfo.Bytes(mc))
	out = lib0.WriteVarUint8Array(mc, out, e.typeRef.Bytes(mc))
	out = lib0.WriteVarUint8Array(mc, out, e.length.Bytes(mc))
	out = meter.Append(mc, out, e.rest...)
	return out
}
