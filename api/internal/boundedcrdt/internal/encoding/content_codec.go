package encoding

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"encoding/json"
	"fmt"

	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/lib0"
)

// writeJSON appends `varstring(JSON.stringify(v))` to buf, matching
// yjs UpdateEncoderV1.writeJSON (testdata/gen/node_modules/yjs/src/
// utils/UpdateEncoder.js:115). Used by KindFormat and KindEmbed
// content variants — both encode their payload as a JSON-string,
// NOT as a lib0 Any TLV. The two encoding paths look superficially
// similar (both varuint-prefix some bytes) but diverge on the
// payload: lib0 Any starts with a 1-byte type tag (116..127),
// JSON starts with the textual representation.
//
// nil values encode as the JSON literal "null".
func writeJSON(mc *meter.Meter, buf []byte, v block.Any) []byte {
	mc.Enter()
	defer mc.Leave()

	raw, err := json.Marshal(v)
	if err != nil {
		// Fall back to the JSON null literal — yjs has the same
		// failure mode (JSON.stringify of a circular structure
		// throws; we degrade rather than panic).
		raw = meter.Bytes(mc, "null")
	}
	return lib0.WriteVarString(mc, buf, meter.ToString(mc, raw))
}

// readJSON consumes a varstring + JSON-parses it. Returns the
// parsed value plus the unconsumed tail. Mirrors yjs
// UpdateDecoderV1.readJSON (UpdateDecoder.js:113).
func readJSON(mc *meter.Meter, buf []byte, strs stringMode) (block.Any, []byte, error) {
	mc.Enter()
	defer mc.Leave()

	s, n, err := strs.readString(mc, buf)
	if err != nil {
		return nil, buf, err
	}
	var v any
	if err := meter.Unmarshal(mc, meter.Bytes(mc, s), &v); err != nil {
		return nil, buf, meter.Errorf(mc, "readJSON: %w", err)
	}
	return v, buf[n:], nil
}

// EncodeContent appends the wire-format payload of c to buf. Caller
// has already emitted the info byte; this writes only the content
// field that follows.
//
// Mirrors yrs/src/block.rs:1844-1872 ItemContent::encode.
//
// Supported in this commit: KindAny, KindString, KindBinary,
// KindDeleted, KindType, KindFormat, KindEmbed.
// Skipped: KindDoc, KindMove, KindJSON.
// All deferred kinds panic on encode rather than emit silently-wrong
// bytes.
func EncodeContent(mc *meter.Meter, buf []byte, c block.Content) []byte {
	mc.Enter()
	defer mc.Leave()

	switch c.Kind {
	case block.KindAny:
		buf = lib0.WriteVarUint(mc, buf, uint64(len(c.Anys)))
		for _, v := range c.Anys {
			mc.Step()

			buf = EncodeAny(mc, buf, v)
		}
		return buf
	case block.KindString:
		return lib0.WriteVarString(mc, buf, c.Str)
	case block.KindBinary:
		return lib0.WriteVarUint8Array(mc, buf, c.Bytes)
	case block.KindDeleted:
		return lib0.WriteVarUint(mc, buf, c.DeletedLen)
	case block.KindEmbed:
		// Single JSON-encoded payload — yjs ContentEmbed.write
		// calls writeJSON which is `varstring(JSON.stringify(v))`
		// in the V1 encoder (testdata/gen/node_modules/yjs/src/
		// structs/ContentEmbed.js + UpdateEncoder.js:115). NOT
		// lib0 Any TLV — the encoders diverge here.
		var v block.Any
		if len(c.Anys) > 0 {
			v = c.Anys[0]
		}
		return writeJSON(mc, buf, v)
	case block.KindFormat:
		// varstring(key) + varstring(JSON.stringify(value)) —
		// yjs ContentFormat.write + writeJSON, per V1 encoder
		// (UpdateEncoder.js:115). NOT lib0 Any.
		buf = lib0.WriteVarString(mc, buf, c.FormatKey)
		var v block.Any
		if len(c.Anys) > 0 {
			v = c.Anys[0]
		}
		return writeJSON(mc, buf, v)
	case block.KindType:
		// ContentType payload: varuint(typeRef) + optional
		// varstring(name) for XmlElement (refID 3) and XmlHook
		// (refID 5). Per docs/yrs-port-notes/nested-types.md §2,
		// citing yjs/src/structs/ContentType.js:1507-1510 and
		// yrs equivalent.
		if c.Branch == nil {
			panic("encoding.EncodeContent: KindType with nil Branch")
		}
		buf = lib0.WriteVarUint(mc, buf, uint64(c.Branch.TypeRef))
		switch c.Branch.TypeRef {
		case block.TypeRefXmlElement, block.TypeRefXmlHook:
			buf = lib0.WriteVarString(mc, buf, c.Branch.Name)
		}
		return buf
	case block.KindDoc:
		// ContentDoc payload: varstring(guid) + Any(opts). Mirrors
		// yjs ContentDoc.write. opts is always an object (possibly
		// empty), never nil, so the Any tag is the object tag.
		buf = lib0.WriteVarString(mc, buf, c.DocGuid)
		return EncodeDocOpts(mc, buf, c.DocOpts)
	default:
		panic(fmt.Sprintf("encoding.EncodeContent: unsupported kind %d (supported: Any, String, Binary, Deleted, Type, Doc)", c.Kind))
	}
}

// DecodeContent reads a Content payload from buf given the content
// ref-number (the low nibble of the info byte). Returns the parsed
// Content plus the unconsumed tail.
//
// Mirrors yrs/src/block.rs ItemContent::decode dispatch.
func DecodeContent(mc *meter.Meter, buf []byte, refNum uint8) (block.Content, []byte, error) {
	mc.Enter()
	defer mc.Leave()

	return decodeContent(mc, buf, refNum, strictStrings)
}

func decodeContent(mc *meter.Meter, buf []byte, refNum uint8, strs stringMode) (block.Content, []byte, error) {
	mc.Enter()
	defer mc.Leave()

	switch block.ContentKind(refNum) {
	case block.KindAny:
		count, n, err := lib0.ReadVarUint(mc, buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		buf = buf[n:]
		if err := checkDecodeCount(mc, count, len(buf)); err != nil {
			return block.Content{}, buf, err
		}
		anys := meter.MakeSlice[[]block.Any, block.Any](mc, uint64(count), uint64(count))
		for i := uint64(0); i < count; i++ {
			mc.Step()

			v, tail, err := decodeAny(mc, buf, strs)
			if err != nil {
				return block.Content{}, buf, err
			}
			anys[i] = v
			buf = tail
		}
		return block.Content{Kind: block.KindAny, Anys: anys}, buf, nil
	case block.KindString:
		s, n, err := strs.readString(mc, buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		return block.Content{Kind: block.KindString, Str: s}, buf[n:], nil
	case block.KindBinary:
		b, n, err := lib0.ReadVarUint8Array(mc, buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		// Copy the slice — DecodeContent's return must not alias the
		// input buffer, otherwise mutation of the buffer corrupts
		// stored content.
		out := meter.MakeSlice[[]byte, byte](mc, uint64(len(b)), uint64(len(b)))
		copy(out, b)
		return block.Content{Kind: block.KindBinary, Bytes: out}, buf[n:], nil
	case block.KindDeleted:
		v, n, err := lib0.ReadVarUint(mc, buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		return block.Content{Kind: block.KindDeleted, DeletedLen: v}, buf[n:], nil
	case block.KindEmbed:
		// JSON-string payload, NOT lib0 Any. Mirrors
		// yjs UpdateDecoder.readJSON.
		v, tail, err := readJSON(mc, buf, strs)
		if err != nil {
			return block.Content{}, buf, err
		}
		return block.Content{Kind: block.KindEmbed, Anys: meter.Literal[[]block.Any](mc, 144, func() []block.Any {
			return []block.Any{v}
		})}, tail, nil
	case block.KindFormat:
		key, n, err := strs.readString(mc, buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		buf = buf[n:]
		v, tail, err := readJSON(mc, buf, strs)
		if err != nil {
			return block.Content{}, buf, err
		}
		return block.Content{Kind: block.KindFormat, FormatKey: key, Anys: meter.Literal[[]block.Any](mc, 144, func() []block.Any {
			return []block.Any{v}
		})}, tail, nil
	case block.KindType:
		// Mirror of EncodeContent.KindType. Build an empty Branch
		// with the wire-supplied TypeRef. The Branch.Item back-
		// reference is set by the Repair / Integrate path once the
		// containing Item is fully constructed (gotcha 2 in
		// nested-types.md). Map field is lazily allocated when the
		// types layer first writes a map-key.
		typeRefU, n, err := lib0.ReadVarUint(mc, buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		buf = buf[n:]
		typeRef := block.TypeRef(typeRefU)
		br := meter.Pointer(mc, block.Branch{TypeRef: typeRef})
		switch typeRef {
		case block.TypeRefXmlElement, block.TypeRefXmlHook:
			name, n, err := strs.readString(mc, buf)
			if err != nil {
				return block.Content{}, buf, err
			}
			br.Name = name
			buf = buf[n:]
		}
		return block.Content{Kind: block.KindType, Branch: br}, buf, nil
	case block.KindDoc:
		// Mirror of EncodeContent.KindDoc: varstring(guid) + Any(opts).
		guid, n, err := strs.readString(mc, buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		buf = buf[n:]
		optsAny, tail, err := decodeAny(mc, buf, strs)
		if err != nil {
			return block.Content{}, buf, err
		}
		opts, _ := optsAny.(map[string]any)
		if opts == nil {
			opts = meter.Literal[map[string]any](mc, 128, func() map[string]any {
				return map[string]any{}
			})
		}
		return block.Content{Kind: block.KindDoc, DocGuid: guid, DocOpts: opts}, tail, nil
	default:
		return block.Content{}, buf, meter.Errorf(mc, "encoding.DecodeContent: unsupported content kind %d (tech-debt.md)", refNum)
	}
}
