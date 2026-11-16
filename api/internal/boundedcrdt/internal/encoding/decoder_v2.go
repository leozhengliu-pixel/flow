package encoding

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"errors"

	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/lib0"
)

// DecoderV2 is the column-oriented update decoder matching yjs
// UpdateDecoderV2 (testdata/gen/node_modules/yjs/src/utils/
// UpdateDecoder.js:96-180). The constructor reads all 9 column
// decoders upfront from the wire (each as a length-prefixed
// varbuffer); per-field Read* methods then pull one value from
// the appropriate column per block iteration.
//
// Top-level update headers (block-count, start-clock, client-count)
// come from the raw rest stream via ReadVarUint — only per-block
// field reads pull from columns.
//
// Pattern: construct with NewDecoderV2, call Read* in the SAME
// order the encoder called Write*. Wire format has no length
// self-check; any iteration-order mismatch silently corrupts.
type DecoderV2 struct {
	rest    []byte
	restPos int

	keyClock   *lib0.IntDiffOptRleDecoder
	client     *lib0.UintOptRleDecoder
	leftClock  *lib0.IntDiffOptRleDecoder
	rightClock *lib0.IntDiffOptRleDecoder
	info       *lib0.RleDecoder
	str        *lib0.StringDecoder
	parentInfo *lib0.RleDecoder
	typeRef    *lib0.UintOptRleDecoder
	length     *lib0.UintOptRleDecoder

	// keys is the receiver's key-table — populated as new keys
	// are encountered (asymmetric with the encoder side; see
	// port-note gotcha 5). ReadKey indexes by keyClock value.
	keys []string
}

// ErrV2BadFeatureFlag is returned when the leading byte isn't
// the expected 0x00. Indicates wire-format drift (a future V3?)
// or that the bytes aren't actually V2 (could be V1 or junk).
var ErrV2BadFeatureFlag = errors.New("encoding: V2 update has non-zero feature flag")

// NewDecoderV2 wraps buf as a V2 decoder. Reads the feature flag
// + 9 column lengths upfront so subsequent Read* calls are O(1)
// per value.
func NewDecoderV2(mc *meter.Meter, buf []byte) (*DecoderV2, error) {
	mc.Enter()
	defer mc.Leave()

	if len(buf) < 1 {
		return nil, meter.Errorf(mc, "V2 update: empty buffer")
	}
	if buf[0] != 0x00 {
		return nil, meter.Errorf(mc, "%w: got %#x", ErrV2BadFeatureFlag, buf[0])
	}
	buf = buf[1:]

	keyClockBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode keyClock column: %w", err)
	}
	buf = buf[n:]

	clientBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode client column: %w", err)
	}
	buf = buf[n:]

	leftClockBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode leftClock column: %w", err)
	}
	buf = buf[n:]

	rightClockBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode rightClock column: %w", err)
	}
	buf = buf[n:]

	infoBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode info column: %w", err)
	}
	buf = buf[n:]

	stringBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode string column: %w", err)
	}
	buf = buf[n:]

	parentInfoBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode parentInfo column: %w", err)
	}
	buf = buf[n:]

	typeRefBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode typeRef column: %w", err)
	}
	buf = buf[n:]

	lenBytes, n, err := lib0.ReadVarUint8Array(mc, buf)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode len column: %w", err)
	}
	buf = buf[n:]

	strDec, err := lib0.NewStringDecoder(mc, stringBytes)
	if err != nil {
		return nil, meter.Errorf(mc, "V2 decode string-column inner: %w", err)
	}

	return meter.Pointer(mc, DecoderV2{
		rest:       buf,
		keyClock:   lib0.NewIntDiffOptRleDecoderFor(mc, keyClockBytes),
		client:     lib0.NewUintOptRleDecoder(mc, clientBytes),
		leftClock:  lib0.NewIntDiffOptRleDecoderFor(mc, leftClockBytes),
		rightClock: lib0.NewIntDiffOptRleDecoderFor(mc, rightClockBytes),
		info:       lib0.NewRleDecoder(mc, infoBytes, lib0.ReadUint8Func),
		str:        strDec,
		parentInfo: lib0.NewRleDecoder(mc, parentInfoBytes, lib0.ReadUint8Func),
		typeRef:    lib0.NewUintOptRleDecoder(mc, typeRefBytes),
		length:     lib0.NewUintOptRleDecoder(mc, lenBytes),
	}), nil
}

// ReadLeftID pulls one (client, clock) pair from the shared
// client column + left-clock column.
func (d *DecoderV2) ReadLeftID(mc *meter.Meter) (block.ID, error) {
	mc.Enter()
	defer mc.Leave()

	c, err := d.client.Read(mc)
	if err != nil {
		return block.ID{}, meter.Errorf(mc, "ReadLeftID client: %w", err)
	}
	clock, err := d.leftClock.Read(mc)
	if err != nil {
		return block.ID{}, meter.Errorf(mc, "ReadLeftID clock: %w", err)
	}
	return block.ID{Client: c, Clock: uint64(clock)}, nil
}

// ReadRightID pulls from the shared client + right-clock columns.
func (d *DecoderV2) ReadRightID(mc *meter.Meter) (block.ID, error) {
	mc.Enter()
	defer mc.Leave()

	c, err := d.client.Read(mc)
	if err != nil {
		return block.ID{}, meter.Errorf(mc, "ReadRightID client: %w", err)
	}
	clock, err := d.rightClock.Read(mc)
	if err != nil {
		return block.ID{}, meter.Errorf(mc, "ReadRightID clock: %w", err)
	}
	return block.ID{Client: c, Clock: uint64(clock)}, nil
}

// ReadClient returns the next client ID from the shared client column.
func (d *DecoderV2) ReadClient(mc *meter.Meter) (uint64, error) {
	mc.Enter()
	defer mc.Leave()

	return d.client.Read(mc)
}

// ReadInfo returns the next info byte from the info RLE column.
func (d *DecoderV2) ReadInfo(mc *meter.Meter) (uint8, error) {
	mc.Enter()
	defer mc.Leave()

	v, err := d.info.Read(mc)
	return uint8(v), err
}

// ReadString returns the next string from the string column.
func (d *DecoderV2) ReadString(mc *meter.Meter) (string, error) {
	mc.Enter()
	defer mc.Leave()

	return d.str.Read(mc)
}

// ReadParentInfo returns true if the parent is referenced by
// root-name, false if by item ID. Maps the 0/1 byte from the
// parent-info RLE column.
func (d *DecoderV2) ReadParentInfo(mc *meter.Meter) (bool, error) {
	mc.Enter()
	defer mc.Leave()

	v, err := d.parentInfo.Read(mc)
	return v == 1, err
}

// ReadTypeRef returns the next TypeRef byte from the type-ref
// RLE column.
func (d *DecoderV2) ReadTypeRef(mc *meter.Meter) (uint8, error) {
	mc.Enter()
	defer mc.Leave()

	v, err := d.typeRef.Read(mc)
	return uint8(v), err
}

// ReadLen returns the next length value from the length column.
func (d *DecoderV2) ReadLen(mc *meter.Meter) (uint64, error) {
	mc.Enter()
	defer mc.Leave()

	return d.length.Read(mc)
}

// ReadVarUint returns the next varuint from the raw rest stream.
// Used for top-level structure headers.
func (d *DecoderV2) ReadVarUint(mc *meter.Meter) (uint64, error) {
	mc.Enter()
	defer mc.Leave()

	v, n, err := lib0.ReadVarUint(mc, d.rest[d.restPos:])
	if err != nil {
		return 0, err
	}
	d.restPos += n
	return v, nil
}

// ReadRestBytes returns the unconsumed rest stream — used by
// content decoders that need raw access.
func (d *DecoderV2) ReadRestBytes(mc *meter.Meter) []byte {
	mc.Enter()
	defer mc.Leave()

	return d.rest[d.restPos:]
}

// AdvanceRest moves the rest cursor forward by n bytes. Content
// decoders call this after consuming bytes via ReadRestBytes.
func (d *DecoderV2) AdvanceRest(mc *meter.Meter, n int) {
	mc.Enter()
	defer mc.Leave()

	d.restPos += n
}

// ReadAny consumes the next lib0 Any TLV from the rest stream.
func (d *DecoderV2) ReadAny(mc *meter.Meter) (block.Any, error) {
	mc.Enter()
	defer mc.Leave()

	v, tail, err := decodeAny(mc, d.rest[d.restPos:], strictStrings)
	if err != nil {
		return nil, err
	}
	d.restPos = len(d.rest) - len(tail)
	return v, nil
}

// ReadVarBuf consumes the next varbuffer from the rest stream.
func (d *DecoderV2) ReadVarBuf(mc *meter.Meter) ([]byte, error) {
	mc.Enter()
	defer mc.Leave()

	b, n, err := lib0.ReadVarUint8Array(mc, d.rest[d.restPos:])
	if err != nil {
		return nil, err
	}
	d.restPos += n
	// Copy — the underlying rest slice may be reused.
	out := meter.MakeSlice[[]byte, byte](mc, uint64(len(b)), uint64(len(b)))
	copy(out, b)
	return out, nil
}

// ReadKey resolves a map-key reference. Unlike the encoder side,
// the decoder DOES populate a key table — see port-note gotcha 5
// for the asymmetry. First read of a given keyClock value reads
// a string from the string column AND caches it; subsequent reads
// of the same keyClock return the cached string.
func (d *DecoderV2) ReadKey(mc *meter.Meter) (string, error) {
	mc.Enter()
	defer mc.Leave()

	clock, err := d.keyClock.Read(mc)
	if err != nil {
		return "", meter.Errorf(mc, "ReadKey keyClock: %w", err)
	}
	idx := int(clock)
	if idx >= 0 && idx < len(d.keys) {
		return d.keys[idx], nil
	}
	// Index out of range or larger than current keys — read a
	// new string and grow the table.
	s, err := d.str.Read(mc)
	if err != nil {
		return "", meter.Errorf(mc, "ReadKey string: %w", err)
	}
	d.keys = meter.Append(mc, d.keys, s)
	return s, nil
}

// ReadJSON reads a format value or an embed: a lib0 Any in the rest
// stream, as yjs UpdateDecoderV2.readJSON reads it, with numbers mapped
// to the types the V1 path yields so callers see the same Go types
// whichever wire format delivered the document. See json_any.go.
func (d *DecoderV2) ReadJSON(mc *meter.Meter) (block.Any, error) {
	mc.Enter()
	defer mc.Leave()

	v, err := d.ReadAny(mc)
	if err != nil {
		return nil, err
	}
	return jsonValueFromAny(mc, v), nil
}
