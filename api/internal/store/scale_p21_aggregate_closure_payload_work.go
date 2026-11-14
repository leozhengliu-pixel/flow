package store

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var metadataAggregateRawPayloadType = reflect.TypeOf(json.RawMessage(nil))

func metadataAggregatePayloadEncodingWork(ctx context.Context, value reflect.Value, depth int) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if depth >= metadataLogicalDepth {
		return 0, ErrMetadataWorkBudget
	}
	if !value.IsValid() {
		return 128 << 10, nil
	}
	cost := uint64(128)
	add := func(part uint64) error {
		if part > metadataWorkBytes || cost > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	child := func(value reflect.Value) error {
		part, err := metadataAggregatePayloadEncodingWork(ctx, value, depth+1)
		if err != nil {
			return err
		}
		return add(uint64(part))
	}
	switch value.Kind() {
	case reflect.String:
		// JSON permits six encoded bytes per input byte. Account for the
		// encoder's growing buffer, its returned copy, and scalar overhead.
		if err := add(uint64(value.Len()) * 24); err != nil {
			return 0, err
		}
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			if err := child(value.Elem()); err != nil {
				return 0, err
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			copies := uint64(8)
			if value.Kind() == reflect.Array || value.Type() == metadataAggregateRawPayloadType {
				copies = 24
			}
			if err := add(uint64(value.Len()) * copies); err != nil {
				return 0, err
			}
		} else {
			for i := 0; i < value.Len(); i++ {
				if err := child(value.Index(i)); err != nil {
					return 0, err
				}
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if err := child(iter.Key()); err != nil {
				return 0, err
			}
			if err := child(iter.Value()); err != nil {
				return 0, err
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				continue
			}
			if err := add(uint64(len(field.Name)+len(field.Tag.Get("json"))) * 24); err != nil {
				return 0, err
			}
			if err := child(value.Field(i)); err != nil {
				return 0, err
			}
		}
	default:
		if err := add(256); err != nil {
			return 0, err
		}
	}
	return int(cost), nil
}

// The raw payload is retained through publication. enrichRealtimePayload also
// decodes its maps/interfaces and encodes the enriched envelope. Container
// charges prevent many tiny JSON objects from defeating a byte-only bound.
func metadataAggregatePayloadJSONWork(ctx context.Context, raw []byte) (uint64, error) {
	if len(raw) > metadataWorkBytes/16 {
		return 0, ErrMetadataWorkBudget
	}
	cost := uint64(len(raw)) * 16
	quoted, escaped := false, false
	for i, c := range raw {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
			cost += 128
		case '{', '[':
			cost += 1024
		case ':', ',':
			cost += 128
		}
		if cost > metadataWorkBytes {
			return 0, ErrMetadataWorkBudget
		}
	}
	return cost, ctx.Err()
}

func (s *SQLiteStore) reserveMetadataAggregatePayload(ctx context.Context, payload any) (int, func(), error) {
	work, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(payload), 0)
	if err != nil {
		return 0, nil, err
	}
	work = max(work, 128<<10)
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, work)
	return work, release, err
}

const metadataAggregateProxyString = 4096

type metadataAggregateJSONWork struct {
	encoded, shell, generic uint64
}

var metadataAggregateJSONMarshaler = reflect.TypeFor[json.Marshaler]()
var metadataAggregateTextMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
var metadataAggregateTimeType = reflect.TypeFor[time.Time]()
var metadataAggregateNumberType = reflect.TypeFor[json.Number]()
var errMetadataAggregateProfile = errors.New("aggregate JSON profile needs its legacy path")

func metadataAggregateCustomJSON(t reflect.Type) bool {
	return t != metadataAggregateTimeType && t != reflect.PointerTo(metadataAggregateTimeType) && (t.Implements(metadataAggregateJSONMarshaler) ||
		t.Implements(metadataAggregateTextMarshaler) || reflect.PointerTo(t).Implements(metadataAggregateJSONMarshaler) ||
		reflect.PointerTo(t).Implements(metadataAggregateTextMarshaler))
}

func metadataAggregateJSONProfile(ctx context.Context, node metadataAggregateNode, depth int) (metadataAggregateJSONWork, bool, error) {
	var result metadataAggregateJSONWork
	if err := ctx.Err(); err != nil {
		return result, false, err
	}
	if node == nil {
		return result, true, nil
	}
	if depth >= metadataLogicalDepth {
		return result, false, ErrMetadataWorkBudget
	}
	t := node.nodeType()
	if t == metadataAggregateRawPayloadType {
		length, err := metadataAggregateByteLength(ctx, node)
		return metadataAggregateJSONWork{4, 4, 128}, length == 0, err
	}
	if t == metadataAggregateNumberType || metadataAggregateCustomJSON(t) {
		return result, false, nil
	}
	if t == metadataAggregateTimeType {
		return metadataAggregateJSONWork{64, 64, 512}, true, nil
	}
	add := func(part metadataAggregateJSONWork) error {
		result.encoded += part.encoded
		result.shell += part.shell
		result.generic += part.generic
		if result.encoded > metadataWorkBytes || result.shell > metadataWorkBytes || result.generic > metadataWorkBytes {
			return ErrMetadataWorkBudget
		}
		return nil
	}
	child := func(value metadataAggregateNode) (bool, error) {
		part, plain, err := metadataAggregateJSONProfile(ctx, value, depth+1)
		if err != nil || !plain {
			return plain, err
		}
		return true, add(part)
	}
	switch t.Kind() {
	case reflect.String:
		encoded, decoded, err := metadataAggregateStringProfile(ctx, node)
		shell := encoded
		if decoded > metadataAggregateProxyString {
			shell = max(encoded, metadataAggregateProxyString+40)
			if decoded > 3*metadataAggregateProxyString {
				shell = metadataAggregateProxyString + 40
			}
		}
		return metadataAggregateJSONWork{encoded, shell, decoded*2 + 384}, true, err
	case reflect.Interface, reflect.Pointer:
		result = metadataAggregateJSONWork{4, 4, 128}
		count, plain := 0, true
		err := node.nodeChildren(ctx, func(_ reflect.Value, value metadataAggregateNode) error {
			count++
			result = metadataAggregateJSONWork{}
			var err error
			plain, err = child(value)
			return err
		})
		return result, plain && count <= 1, err
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return metadataAggregateJSONWork{64, 64, 384}, true, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String && (t.Key().Kind() < reflect.Int || t.Key().Kind() > reflect.Uintptr) ||
			metadataAggregateCustomJSON(t.Key()) {
			return result, false, nil
		}
		result = metadataAggregateJSONWork{4, 4, 512}
		plain := true
		err := metadataAggregateJSONEntries(ctx, node, func(key, value metadataAggregateNode) error {
			keyWork := metadataAggregateJSONWork{64, 64, 384}
			if key.nodeType().Kind() == reflect.String {
				encoded, decoded, err := metadataAggregateStringProfile(ctx, key)
				if err != nil {
					return err
				}
				// Keys are left to encoding/json for exact order and
				// replacement-character collision semantics.
				if decoded > metadataAggregateProxyString {
					plain = false
					return errMetadataAggregateStop
				}
				keyWork = metadataAggregateJSONWork{encoded, encoded, decoded*2 + 384}
			}
			if err := add(keyWork); err != nil {
				return err
			}
			if err := add(metadataAggregateJSONWork{2, 2, 128}); err != nil {
				return err
			}
			var err error
			plain, err = child(value)
			if err == nil && !plain {
				err = errMetadataAggregateStop
			}
			return err
		})
		if errors.Is(err, errMetadataAggregateStop) {
			err = nil
		}
		if errors.Is(err, errMetadataAggregateProfile) {
			plain, err = false, nil
		}
		return result, plain, err
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			if metadataAggregateCustomJSON(t.Elem()) {
				return result, false, nil
			}
			length, err := metadataAggregateByteLength(ctx, node)
			size := max((length+2)/3*4+2, 4)
			return metadataAggregateJSONWork{size, size, size*2 + 512}, true, err
		}
		fallthrough
	case reflect.Array:
		result = metadataAggregateJSONWork{4, 4, 512}
		plain := true
		err := node.nodeChildren(ctx, func(_ reflect.Value, value metadataAggregateNode) error {
			if err := add(metadataAggregateJSONWork{1, 1, 128}); err != nil {
				return err
			}
			var err error
			plain, err = child(value)
			if err == nil && !plain {
				err = errMetadataAggregateStop
			}
			return err
		})
		if errors.Is(err, errMetadataAggregateStop) {
			err = nil
		}
		return result, plain, err
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.Anonymous && field.PkgPath != "" {
				// The standard encoder can promote exported fields from
				// an unexported embedding; the source descriptor omits it.
				return result, false, nil
			}
		}
		result = metadataAggregateJSONWork{2, 2, 512}
		plain := true
		err := node.nodeChildren(ctx, func(key reflect.Value, value metadataAggregateNode) error {
			field, ok := t.FieldByName(key.String())
			if !ok || field.PkgPath != "" || field.Tag.Get("json") == "-" {
				return nil
			}
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			for option := range strings.SplitSeq(options, ",") {
				if option != "" && option != "omitempty" {
					plain = false
					return errMetadataAggregateStop
				}
			}
			// Count both forms so embedding/tag validation can only reduce
			// the shell that the standard encoder actually produces.
			encoded := uint64(6*(len(name)+len(field.Name)) + 8)
			if err := add(metadataAggregateJSONWork{encoded, encoded, uint64(2*(len(name)+len(field.Name))) + 384}); err != nil {
				return err
			}
			var err error
			plain, err = child(value)
			if err == nil && !plain {
				err = errMetadataAggregateStop
			}
			return err
		})
		if errors.Is(err, errMetadataAggregateStop) {
			err = nil
		}
		return result, plain, err
	default:
		return result, false, nil
	}
}

func metadataAggregateJSONEntries(ctx context.Context, node metadataAggregateNode, visit func(metadataAggregateNode, metadataAggregateNode) error) error {
	switch value := node.(type) {
	case metadataAggregateStoredNode:
		return value.value.MapEntries(ctx, func(key, child metadataLogicalNode) error {
			return visit(metadataAggregateStoredNode{key}, metadataAggregateStoredNode{child})
		})
	case metadataAggregateNativeNode:
		iter := value.value.MapRange()
		for iter.Next() {
			if err := visit(metadataAggregateNativeNode{iter.Key()}, metadataAggregateNativeNode{iter.Value()}); err != nil {
				return err
			}
		}
		return nil
	case metadataLogicalTransformMap:
		return value.logicalMapEntries(ctx, visit)
	case metadataAggregateOverlayNode:
		stored, ok := value.base.(metadataAggregateStoredNode)
		if !ok {
			return errMetadataAggregateProfile
		}
		seen := map[string]bool{}
		err := stored.value.MapEntries(ctx, func(key, child metadataLogicalNode) error {
			for _, point := range append(append([]RecordKey(nil), value.scope.Read...), value.scope.Write...) {
				if point.Kind != value.field {
					continue
				}
				match, err := key.StringEqual(ctx, point.ID)
				if err != nil {
					return err
				}
				if match {
					seen[point.ID] = true
					if replacement, found := value.selectedRecord(point.ID); found {
						return visit(metadataAggregateStoredNode{key}, metadataAggregateNativeNode{replacement})
					}
					return nil
				}
			}
			return visit(metadataAggregateStoredNode{key}, metadataAggregateStoredNode{child})
		})
		if err != nil {
			return err
		}
		iter := value.selected.MapRange()
		for iter.Next() {
			id := iter.Key().String()
			if !seen[id] && metadataRecordSelected(value.scope, value.field, id, false) {
				if err := visit(metadataAggregateNativeNode{iter.Key()}, metadataAggregateNativeNode{iter.Value()}); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return errMetadataAggregateProfile
	}
}

func metadataAggregateByteLength(ctx context.Context, node metadataAggregateNode) (uint64, error) {
	if native, ok := node.(metadataAggregateNativeNode); ok {
		return uint64(native.value.Len()), ctx.Err()
	}
	if stored, ok := node.(metadataAggregateStoredNode); ok {
		var size int64
		err := stored.value.StreamBytes(ctx, func(input io.Reader) error {
			var err error
			size, err = io.CopyBuffer(io.Discard, input, make([]byte, 32<<10))
			return err
		})
		return uint64(size), err
	}
	return 0, ErrMetadataRecordQuery
}

func metadataAggregateStringProfile(ctx context.Context, node metadataAggregateNode) (uint64, uint64, error) {
	if native, ok := node.(metadataAggregateNativeNode); ok {
		return metadataAggregateStringSizesContext(ctx, native.value.String())
	}
	if stored, ok := node.(metadataAggregateStoredNode); ok {
		var encoded, decoded uint64 = 2, 0
		err := stored.value.StreamBytes(ctx, func(input io.Reader) error {
			buffer := make([]byte, (32<<10)+utf8.UTFMax)
			carry := 0
			for {
				n, err := input.Read(buffer[carry : len(buffer)-utf8.UTFMax])
				end := carry + n
				used := 0
				for used < end {
					if err != io.EOF && !utf8.FullRune(buffer[used:end]) {
						break
					}
					r, size := utf8.DecodeRune(buffer[used:end])
					e, d := metadataAggregateRuneSize(r, size, buffer[used])
					encoded, decoded = encoded+e, decoded+d
					used += size
				}
				carry = copy(buffer, buffer[used:end])
				if err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}
				if err := ctx.Err(); err != nil {
					return err
				}
			}
		})
		return encoded, decoded, err
	}
	return 0, 0, ErrMetadataRecordQuery
}

func metadataAggregateStringSizes(value string) (uint64, uint64) {
	encoded, decoded, _ := metadataAggregateStringSizesContext(context.Background(), value)
	return encoded, decoded
}

func metadataAggregateStringSizesContext(ctx context.Context, value string) (uint64, uint64, error) {
	encoded, decoded := uint64(2), uint64(0)
	scanned := 0
	for len(value) > 0 {
		if scanned >= 32<<10 {
			if err := ctx.Err(); err != nil {
				return 0, 0, err
			}
			scanned = 0
		}
		r, size := utf8.DecodeRuneInString(value)
		e, d := metadataAggregateRuneSize(r, size, value[0])
		encoded, decoded = encoded+e, decoded+d
		scanned += size
		value = value[size:]
	}
	return encoded, decoded, ctx.Err()
}

func metadataAggregateRuneSize(r rune, size int, first byte) (uint64, uint64) {
	if r == utf8.RuneError && size == 1 {
		return 6, 3
	}
	if r == '\u2028' || r == '\u2029' || first == '<' || first == '>' || first == '&' {
		return 6, uint64(size)
	}
	if r == '"' || r == '\\' || r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t' {
		return 2, uint64(size)
	}
	if r < 0x20 {
		return 6, uint64(size)
	}
	return uint64(size), uint64(size)
}

func metadataAggregateSameFrame(ctx context.Context, before, after metadataAggregateNode) (bool, error) {
	if before == nil || after == nil {
		return before == nil && after == nil, ctx.Err()
	}
	left, leftOK := before.(metadataAggregateStoredNode)
	right, rightOK := after.(metadataAggregateStoredNode)
	if !leftOK || !rightOK || left.value.source == nil || right.value.source == nil ||
		left.value.typ != right.value.typ || left.value.size != right.value.size ||
		left.value.cost != right.value.cost || left.value.unavailable || right.value.unavailable {
		return false, ctx.Err()
	}
	if left.value.source == right.value.source && left.value.offset == right.value.offset {
		return true, ctx.Err()
	}
	if !metadataLogicalRegistryContains(left.value.source, right.value.source) &&
		!metadataLogicalRegistryContains(right.value.source, left.value.source) {
		return false, nil
	}
	var a, b [32 << 10]byte
	for offset := int64(0); offset < left.value.size; {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		size := min(int64(len(a)), left.value.size-offset)
		if _, err := metadataLogicalReadAt(ctx, left.value.source.file, a[:size], left.value.offset+offset); err != nil {
			return false, err
		}
		if _, err := metadataLogicalReadAt(ctx, right.value.source.file, b[:size], right.value.offset+offset); err != nil {
			return false, err
		}
		if !bytes.Equal(a[:size], b[:size]) {
			return false, nil
		}
		offset += size
	}
	return true, nil
}

// Long strings are replaced only in a temporary typed shell. The standard
// encoder/decoder still owns tags, embedding, omitempty, numeric rounding,
// map-key ordering, invalid UTF-8 and duplicate normalized-key semantics.
type metadataAggregateJSONProxy struct {
	values       map[string]string
	counter      int
	ownedStrings bool
}

func (p *metadataAggregateJSONProxy) value(ctx context.Context, value reflect.Value, depth int) (reflect.Value, error) {
	if depth >= metadataLogicalDepth {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, err
	}
	t := value.Type()
	if t == metadataAggregateTimeType {
		return value, nil
	}
	switch value.Kind() {
	case reflect.String:
		if value.Len() <= metadataAggregateProxyString {
			return value, nil
		}
		// Remaining ordinary strings are <=4096 bytes; base64 output cannot
		// contain "." or ":". A marker cannot collide with either category.
		marker := strings.Repeat(".", metadataAggregateProxyString+1) + ":" + strconv.Itoa(p.counter)
		p.counter++
		if p.values == nil {
			p.values = map[string]string{}
		}
		p.values[marker] = value.String()
		out := reflect.New(t).Elem()
		out.SetString(marker)
		return out, nil
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(t), nil
		}
		child, err := p.value(ctx, value.Elem(), depth+1)
		if err != nil {
			return reflect.Value{}, err
		}
		if value.Kind() == reflect.Pointer {
			out := reflect.New(t.Elem())
			out.Elem().Set(child)
			return out, nil
		}
		out := reflect.New(t).Elem()
		out.Set(child)
		return out, nil
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(t), nil
		}
		out := reflect.MakeMapWithSize(t, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			child, err := p.value(ctx, iter.Value(), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.SetMapIndex(iter.Key(), child)
		}
		return out, nil
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && (value.IsNil() || t.Elem().Kind() == reflect.Uint8) {
			return value, nil
		}
		out := reflect.New(t).Elem()
		if value.Kind() == reflect.Slice {
			out = reflect.MakeSlice(t, value.Len(), value.Len())
		}
		for i := 0; i < value.Len(); i++ {
			child, err := p.value(ctx, value.Index(i), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Index(i).Set(child)
		}
		return out, nil
	case reflect.Struct:
		out := reflect.New(t).Elem()
		out.Set(value)
		for i := 0; i < value.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			if field.Tag.Get("json") == "-" {
				out.Field(i).SetZero()
				continue
			}
			child, err := p.value(ctx, value.Field(i), depth+1)
			if err != nil {
				return reflect.Value{}, err
			}
			out.Field(i).Set(child)
		}
		return out, nil
	default:
		return value, nil
	}
}

func (p *metadataAggregateJSONProxy) restore(ctx context.Context, value any, depth int) (any, error) {
	if depth >= metadataLogicalDepth {
		return nil, ErrMetadataWorkBudget
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch current := value.(type) {
	case string:
		if original, found := p.values[current]; found {
			if utf8.ValidString(original) {
				if p.ownedStrings {
					return original, nil
				}
				return strings.Clone(original), nil
			}
			_, size, err := metadataAggregateStringSizesContext(ctx, original)
			if err != nil {
				return nil, err
			}
			var out strings.Builder
			out.Grow(int(size))
			scanned := 0
			for len(original) > 0 {
				if scanned >= 32<<10 {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					scanned = 0
				}
				r, n := utf8.DecodeRuneInString(original)
				out.WriteRune(r)
				scanned += n
				original = original[n:]
			}
			return out.String(), nil
		}
	case map[string]any:
		for key, child := range current {
			out, err := p.restore(ctx, child, depth+1)
			if err != nil {
				return nil, err
			}
			current[key] = out
		}
	case []any:
		for i, child := range current {
			out, err := p.restore(ctx, child, depth+1)
			if err != nil {
				return nil, err
			}
			current[i] = out
		}
	}
	return value, nil
}

func metadataAggregateMarshalNormalized(ctx context.Context, value any) (json.RawMessage, error) {
	length, err := metadataAggregateNormalizedSize(ctx, value, 0)
	if err != nil {
		return nil, err
	}
	if length > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	result := make([]byte, int(length))
	writer := &metadataAggregateFixedJSON{ctx: ctx, data: result}
	if err := writer.value(value, 0); err != nil {
		return nil, err
	}
	if writer.offset != len(result) {
		return nil, ErrMetadataRecordQuery
	}
	return result, nil
}

func metadataAggregateNormalizedSize(ctx context.Context, value any, depth int) (uint64, error) {
	if depth >= 10003 {
		return 0, ErrMetadataWorkBudget
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var size uint64
	switch current := value.(type) {
	case nil:
		return 4, nil
	case string:
		var err error
		size, _, err = metadataAggregateStringSizesContext(ctx, current)
		if err != nil {
			return 0, err
		}
	case bool, float64:
		raw, err := json.Marshal(current)
		return uint64(len(raw)), err
	case []any:
		size = 2
		for i, item := range current {
			child, err := metadataAggregateNormalizedSize(ctx, item, depth+1)
			if err != nil {
				return 0, err
			}
			size += child
			if i > 0 {
				size++
			}
		}
	case map[string]any:
		size = 2
		for key, item := range current {
			keySize, _, err := metadataAggregateStringSizesContext(ctx, key)
			if err != nil {
				return 0, err
			}
			child, err := metadataAggregateNormalizedSize(ctx, item, depth+1)
			if err != nil {
				return 0, err
			}
			size += keySize + child + 2
		}
		if len(current) > 0 {
			size--
		}
	default:
		return 0, ErrMetadataRecordQuery
	}
	if size > metadataWorkBytes {
		return 0, ErrMetadataWorkBudget
	}
	return size, nil
}

type metadataAggregateFixedJSON struct {
	ctx    context.Context
	data   []byte
	offset int
}

func (w *metadataAggregateFixedJSON) write(value []byte) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if len(value) > len(w.data)-w.offset {
		return ErrMetadataWorkBudget
	}
	w.offset += copy(w.data[w.offset:], value)
	return nil
}

func (w *metadataAggregateFixedJSON) quoted(value string) error {
	if err := w.write([]byte{'"'}); err != nil {
		return err
	}
	encoder := json.NewEncoder(metadataAggregateQuotedChunkWriter{w})
	for len(value) > 0 {
		size := min(len(value), 16<<10)
		if size < len(value) && !utf8.RuneStart(value[size]) {
			start := size
			for start > 0 && size-start < utf8.UTFMax && !utf8.RuneStart(value[start]) {
				start--
			}
			_, width := utf8.DecodeRuneInString(value[start:])
			if width > 1 && start+width > size {
				size = start
			}
		}
		if err := encoder.Encode(value[:size]); err != nil {
			return err
		}
		value = value[size:]
	}
	return w.write([]byte{'"'})
}

type metadataAggregateQuotedChunkWriter struct{ target *metadataAggregateFixedJSON }

func (w metadataAggregateQuotedChunkWriter) Write(raw []byte) (int, error) {
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-2] != '"' || raw[len(raw)-1] != '\n' {
		return 0, ErrMetadataRecordQuery
	}
	if err := w.target.write(raw[1 : len(raw)-2]); err != nil {
		return 0, err
	}
	return len(raw), nil
}

func (w *metadataAggregateFixedJSON) value(value any, depth int) error {
	if depth >= 10003 {
		return ErrMetadataWorkBudget
	}
	switch current := value.(type) {
	case nil:
		return w.write([]byte("null"))
	case string:
		return w.quoted(current)
	case bool, float64:
		raw, err := json.Marshal(current)
		if err != nil {
			return err
		}
		return w.write(raw)
	case []any:
		if err := w.write([]byte{'['}); err != nil {
			return err
		}
		for i, item := range current {
			if i > 0 {
				if err := w.write([]byte{','}); err != nil {
					return err
				}
			}
			if err := w.value(item, depth+1); err != nil {
				return err
			}
		}
		return w.write([]byte{']'})
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if err := w.write([]byte{'{'}); err != nil {
			return err
		}
		for i, key := range keys {
			if i > 0 {
				if err := w.write([]byte{','}); err != nil {
					return err
				}
			}
			if err := w.quoted(key); err != nil {
				return err
			}
			if err := w.write([]byte{':'}); err != nil {
				return err
			}
			if err := w.value(current[key], depth+1); err != nil {
				return err
			}
		}
		return w.write([]byte{'}'})
	default:
		return ErrMetadataRecordQuery
	}
}
