// Package anyval checks and fixes the plain values ygo stores in items: the
// value kinds the Any encoder writes, and values written as JSON.
package anyval

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"encoding/json"
	"unicode/utf8"

	"flow/api/internal/boundedcrdt/internal/utf16"
)

// Valid returns v with every string and object key that is not valid
// UTF-8 fixed by utf16.ValidString, for the value kinds EncodeAny writes
// (nil, bool, numbers, string, []byte, []any, map[string]any). A value that
// needs no change is returned as it is, with no allocation; otherwise only
// the containers on the way to a change are copied, and the caller's value
// is never modified. Two keys of one object that become equal once fixed
// keep the value of the key that sorts last. Below MaxTreeDepth the walk
// stops and leaves the rest as it is: such values, and cyclic ones, are
// outside what the API promises.
func Valid(mc *meter.Meter, v any) any {
	mc.Enter()
	defer mc.Leave()

	if !needsFix(mc, v, 0) {
		return v
	}
	return fixAny(mc, v, 0)
}

// ValidJSON returns v made valid for content that is written as JSON
// (embed and format values). The decision covers the whole value, as the
// encoders decide at encode (encoding.jsonValueForAny): a plain tree all
// the way down gets Valid and keeps the encoding it has in V1 and V2.
// Anything else is marshaled once, here, its bytes fixed by
// utf16.ValidString, and stored as that JSON, a json.RawMessage, which both
// encoders already take as the JSON it holds. So every encoding of the item
// writes the same bytes, whatever a marshaler would return on a later call;
// the JSON text is kept as it is, numbers and escaped surrogates included;
// and two such values with the same JSON compare equal. A value
// encoding/json cannot marshal (a cycle) becomes nil, which both formats
// already write for it.
func ValidJSON(mc *meter.Meter, v any) any {
	mc.Enter()
	defer mc.Leave()

	if IsTree(mc, v, 0) {
		return Valid(mc, v)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return meter.Bytes(mc, utf16.ValidString(mc, meter.ToString(mc, raw)))
}

// NeedsJSONFix reports whether ValidJSON would change v: a value that is not
// a plain tree is always marshaled.
func NeedsJSONFix(mc *meter.Meter, v any) bool {
	mc.Enter()
	defer mc.Leave()

	return !IsTree(mc, v, 0) || needsFix(mc, v, 0)
}

// NeedsFix reports whether Valid would change v.
func NeedsFix(mc *meter.Meter, v any) bool {
	mc.Enter()
	defer mc.Leave()

	return needsFix(mc, v, 0)
}

func needsFix(mc *meter.Meter, v any, depth int) bool {
	mc.Enter()
	defer mc.Leave()

	if depth > MaxTreeDepth {
		return false
	}
	switch x := v.(type) {
	case string:
		return !utf8.ValidString(x)
	case []any:
		for _, el := range x {
			mc.Step()

			if needsFix(mc, el, depth+1) {
				return true
			}
		}
	case map[string]any:
		for k, el := range x {
			mc.Step()

			if !utf8.ValidString(k) || needsFix(mc, el, depth+1) {
				return true
			}
		}
	}
	return false
}

// fixAny rebuilds v, which needsFix reported; children that need no change
// are kept as they are, so a valid nested container keeps its identity.
func fixAny(mc *meter.Meter, v any, depth int) any {
	mc.Enter()
	defer mc.Leave()

	if depth > MaxTreeDepth || !needsFix(mc, v, depth) {
		return v
	}
	switch x := v.(type) {
	case string:
		return utf16.ValidString(mc, x)
	case []any:
		out := meter.MakeSlice[[]any, any](mc, uint64(len(x)), uint64(len(x)))
		for i, el := range x {
			mc.Step()

			out[i] = fixAny(mc, el, depth+1)
		}
		return out
	case map[string]any:
		keys := meter.MakeSlice[[]string, string](mc, uint64(0), uint64(len(x)))
		for k := range x {
			mc.Step()

			keys = meter.Append(mc, keys, k)
		}
		meter.Strings(mc, keys)
		out := meter.MakeMap[map[string]any, string, any](mc, uint64(len(x)))
		for _, k := range keys {
			mc.Step()
			meter.Set(mc, out, utf16.ValidString(mc, k), fixAny(mc, x[k], depth+1))
		}
		return out
	}
	return v
}

// MaxTreeDepth bounds the walk over a caller's value. Nothing decoded from
// the wire is anywhere near this deep; a value that is, or that refers back to
// itself, is handed to encoding/json instead, which detects the cycle and
// degrades it to null exactly as the V1 path does. Without the bound a
// self-referencing map recursed until the stack ran out - a fatal crash, not a
// recoverable panic - where it used to encode as null.
const MaxTreeDepth = 1000

// IsTree reports whether v is a plain tree the Any encoder can take as it
// is, once its numbers are classified: nil, bool, string, []byte, int,
// int32, int64, float32, float64, and []any / map[string]any of those, no
// deeper than MaxTreeDepth.
func IsTree(mc *meter.Meter, v any, depth int) bool {
	mc.Enter()
	defer mc.Leave()

	if depth > MaxTreeDepth {
		return false
	}
	switch x := v.(type) {
	case nil, bool, string, []byte, int, int32, int64, float32, float64:
		return true
	case []any:
		for _, el := range x {
			mc.Step()

			if !IsTree(mc, el, depth+1) {
				return false
			}
		}
		return true
	case map[string]any:
		for _, el := range x {
			mc.Step()

			if !IsTree(mc, el, depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
