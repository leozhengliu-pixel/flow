package store

import (
	"context"
	"encoding"
	"encoding/json"
	"reflect"
	"sync"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

var metadataAggregateRawPayloadType = reflect.TypeOf(json.RawMessage(nil))

// MetadataJSONEncodingWork estimates one encoding/json Marshal or Encode.
func MetadataJSONEncodingWork(ctx context.Context, value any) (int, error) {
	return metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
}

func metadataAggregatePayloadEncodingWork(ctx context.Context, value reflect.Value, depth int) (int, error) {
	return metadataPayloadEncodingWork(ctx, value, depth, 0, false)
}

// The regular encoder grows both a bytes.Buffer and individual appendString
// results. The buffer at least doubles, so the sum of its capacities is at
// most four encoded widths before rounding. A growing append uses at least
// 1.25x the previous capacity and its last allocation is at most 1.25x the
// leaf width plus size-class slack. Reserve for that whole history, the
// returned copy, and a possible storage copy. Strings with no escapes need
// only the opening quote, a single append of the text, and the closing quote.
const (
	metadataJSONPlainCopies   = 9
	metadataJSONEscapedCopies = 16
)

func metadataPayloadEncodingWork(ctx context.Context, value reflect.Value, depth int, sizedFields uint64, sizedRevisions bool) (int, error) {
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
	if depth == 0 {
		cost += 1024 // cold encoder state and small allocation rounding
	}
	add := func(part uint64) error {
		if part > uint64(metadataWorkBytes()) || cost > uint64(metadataWorkBytes())-part {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	child := func(value reflect.Value, copies uint64) error {
		part, err := metadataAggregatePayloadEncodingWork(ctx, value, depth+1)
		if err != nil {
			return err
		}
		return add(uint64(part) * copies)
	}
	if value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if !value.IsNil() {
			if value.Kind() == reflect.Pointer && metadataJSONMarshaler(value.Type()) && !metadataJSONMarshaler(value.Type().Elem()) {
				return 0, ErrMetadataWorkBudget
			}
			if err := child(value.Elem(), 1); err != nil {
				return 0, err
			}
		}
		return int(cost), nil
	}
	if value.Type() == metadataAggregateRawPayloadType {
		work, err := metadataRawEncodingWork(ctx, value.Bytes(), false)
		if err != nil {
			return 0, err
		}
		allowed := add(work)
		return int(cost), allowed
	}
	if value.Type().Name() != "" && value.Type().PkgPath() != "" {
		switch value.Type() {
		case reflect.TypeFor[time.Time](), reflect.TypeFor[domain.OptionalInt]():
			return int(cost) + 320, nil
		case reflect.TypeFor[domain.ActivityMetadata]():
			work, err := metadataActivityEncodingWork(ctx, value)
			if err != nil {
				return 0, err
			}
			allowed := add(work)
			return int(cost), allowed
		case reflect.TypeFor[domain.AgentToolCall]():
			// The method calls Marshal on the underlying struct and may
			// also quote a non-JSON result before either outer compaction.
			call := value.Interface().(domain.AgentToolCall)
			type plain domain.AgentToolCall
			inner, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(plain(call)), depth+1)
			if err != nil {
				return 0, err
			}
			extra := uint64(inner)*2 + uint64(len(call.Result))*metadataJSONEscapedCopies*6
			allowed := add(extra)
			return int(cost), allowed
		default:
			if metadataJSONCustomMarshaler(value.Type()) {
				// An arbitrary marshaler can allocate independently of
				// its fields, so a structural estimate cannot admit it.
				return 0, ErrMetadataWorkBudget
			}
		}
	}
	switch value.Kind() {
	case reflect.String:
		work, err := metadataStandardStringEncodingWork(ctx, value.String())
		if err != nil {
			return 0, err
		}
		if err := add(work); err != nil {
			return 0, err
		}
	case reflect.Slice, reflect.Array:
		if elem := value.Type().Elem(); elem.Kind() == reflect.Uint8 {
			copies := uint64(16) // base64 and its append
			if value.Kind() == reflect.Array {
				copies = 64 // comma and up to three digits per element
			}
			if elem.PkgPath() != "" && metadataJSONCustomMarshaler(elem) {
				return 0, ErrMetadataWorkBudget
			}
			if err := add(uint64(value.Len()) * copies); err != nil {
				return 0, err
			}
		} else {
			for i := 0; i < value.Len(); i++ {
				if err := child(value.Index(i), 1); err != nil {
					return 0, err
				}
			}
		}
	case reflect.Map:
		key := value.Type().Key()
		if key.Kind() != reflect.String && key.Implements(reflect.TypeFor[encoding.TextMarshaler]()) {
			return 0, ErrMetadataWorkBudget
		}
		iter := value.MapRange()
		for iter.Next() {
			if err := child(iter.Key(), 1); err != nil {
				return 0, err
			}
			if err := child(iter.Value(), 1); err != nil {
				return 0, err
			}
		}
	case reflect.Struct:
		for _, field := range metadataJSONWorkFields(value.Type()) {
			item := value.Field(field.index)
			if field.omitEmpty && metadataJSONEmpty(item) {
				// encoding/json writes neither the key nor the value of an
				// empty omitempty field, and a decoder of that encoding builds
				// nothing for it. Charging such fields as written made a long
				// project progress history (mostly empty points) cost about
				// 400 times its encoded size. The inline bytes stay charged.
				if err := add(field.inline); err != nil {
					return 0, err
				}
				continue
			}
			if err := add(field.keyWork); err != nil {
				return 0, err
			}
			if sizedFields&(uint64(1)<<field.index) != 0 {
				body := reflect.Indirect(item)
				work := uint64(8192)
				if body.Kind() == reflect.String {
					part, err := metadataAggregateStringEncodingWork(ctx, body.String())
					if err != nil {
						return 0, err
					}
					work += part
				} else {
					_, selected := metadataJSONLargeBodies(body)
					part, err := metadataPayloadEncodingWork(ctx, body, depth+1, selected, false)
					if err != nil {
						return 0, err
					}
					work += uint64(part)
				}
				if err := add(work); err != nil {
					return 0, err
				}
				continue
			}
			if sizedRevisions && field.index == metadataJSONDocumentRevisions.index {
				if err := add(uint64(item.Len())*256 + 8192); err != nil {
					return 0, err
				}
				for i := 0; i < item.Len(); i++ {
					entry := item.Index(i)
					_, selected := metadataJSONLargeBodies(entry)
					work, err := metadataPayloadEncodingWork(ctx, entry, depth+2, selected, false)
					if err != nil {
						return 0, err
					}
					if err := add(uint64(work)); err != nil {
						return 0, err
					}
				}
				continue
			}
			copies := uint64(1)
			if field.quoted {
				copies = 3 // encode a string, then escape that encoding
			}
			if err := child(item, copies); err != nil {
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

type metadataJSONWorkField struct {
	index             int
	omitEmpty, quoted bool
	inline, keyWork   uint64
}

var metadataJSONWorkPlans sync.Map

func metadataJSONWorkFields(typ reflect.Type) []metadataJSONWorkField {
	if cached, ok := metadataJSONWorkPlans.Load(typ); ok {
		return cached.([]metadataJSONWorkField)
	}
	fields := make([]metadataJSONWorkField, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if field.PkgPath != "" && !(field.Anonymous && field.Type.Kind() == reflect.Struct) || tag == "-" {
			continue
		}
		omitEmpty, quoted := metadataJSONTag(tag)
		fields = append(fields, metadataJSONWorkField{i, omitEmpty, quoted, uint64(field.Type.Size()), uint64(len(field.Name)+len(tag)) * 24})
	}
	plan, _ := metadataJSONWorkPlans.LoadOrStore(typ, fields)
	return plan.([]metadataJSONWorkField)
}

func metadataJSONMarshaler(typ reflect.Type) bool {
	return typ.Implements(reflect.TypeFor[json.Marshaler]()) || typ.Implements(reflect.TypeFor[encoding.TextMarshaler]())
}

var metadataJSONCustomMarshalers sync.Map

func metadataJSONCustomMarshaler(typ reflect.Type) bool {
	if cached, ok := metadataJSONCustomMarshalers.Load(typ); ok {
		return cached.(bool)
	}
	custom := metadataJSONMarshaler(typ) || metadataJSONMarshaler(reflect.PointerTo(typ))
	metadataJSONCustomMarshalers.Store(typ, custom)
	return custom
}

// metadataAggregateOmittedField reports whether encoding/json omits a field
// with this tag and value: an omitempty field holding an empty value, as
// encoding/json's isEmptyValue defines it.
func metadataAggregateOmittedField(tag string, value reflect.Value) bool {
	omitEmpty, _ := metadataJSONTag(tag)
	return omitEmpty && metadataJSONEmpty(value)
}

func metadataJSONEmpty(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return value.IsZero()
	}
	return false
}

// The exact width is also used by the encoder that writes into pre-sized
// storage and by the logical streamer.
func metadataAggregateStringEncodingWork(ctx context.Context, value string) (uint64, error) {
	width, err := metadataJSONStringWidth(ctx, value, uint64(metadataWorkBytes())/4)
	return width * 4, err
}

func metadataStandardStringEncodingWork(ctx context.Context, value string) (uint64, error) {
	width, err := metadataJSONStringWidth(ctx, value, uint64(metadataWorkBytes())/metadataJSONPlainCopies)
	if err != nil {
		return 0, err
	}
	if width == uint64(len(value))+2 {
		return width * metadataJSONPlainCopies, nil
	}
	return width * metadataJSONEscapedCopies, nil
}

func metadataJSONStringWidth(ctx context.Context, value string, limit uint64) (uint64, error) {
	if limit < 2 {
		return 0, ErrMetadataWorkBudget
	}
	encoded := uint64(2)
	nextCheck := 0
	for index := 0; index < len(value); {
		if index >= nextCheck {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			nextCheck = index + 4096
		}
		// A run of ASCII that encodes as itself is counted at once.
		if run := index; value[run] < utf8.RuneSelf && logicalJSONASCIIQuotes[value[run]] == "" {
			end := min(len(value), nextCheck)
			for run < end && value[run] < utf8.RuneSelf && logicalJSONASCIIQuotes[value[run]] == "" {
				run++
			}
			if uint64(run-index) > limit-encoded {
				return 0, ErrMetadataWorkBudget
			}
			encoded += uint64(run - index)
			index = run
			continue
		}
		char, size := utf8.DecodeRuneInString(value[index:])
		width := uint64(size)
		switch {
		case char < utf8.RuneSelf:
			if escaped := logicalJSONASCIIQuotes[char]; escaped != "" {
				width = uint64(len(escaped))
			}
		case char == utf8.RuneError && size == 1, char == '\u2028', char == '\u2029':
			width = 6
		}
		if width > limit-encoded {
			return 0, ErrMetadataWorkBudget
		}
		encoded += width
		index += size
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return encoded, nil
}

func metadataRawEncodingWork(ctx context.Context, raw []byte, sized bool) (uint64, error) {
	width, expansion, scanner, _, err := metadataRawJSONSize(ctx, raw)
	if err != nil {
		return 0, err
	}
	if sized {
		// Validation has a nesting stack. Compaction needs only its final
		// slice, with a second width for allocator rounding and NUL stripping.
		return (width+expansion)*2 + scanner + 8192, nil
	}
	// Marshal grows the buffer to len(raw) before compacting the value.
	// If escaping expands it, appendCompact can grow again. Charge both.
	work := width*8 + scanner
	if expansion != 0 {
		work += (width + expansion) * 8
	}
	return work, nil
}

func metadataRawJSONSize(ctx context.Context, raw []byte) (width, expansion, scanner, structure uint64, err error) {
	width = uint64(len(raw))
	depth, high := uint64(0), uint64(0)
	quoted, escaped := false, false
	for i, c := range raw {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, 0, 0, 0, err
			}
		}
		if escaped {
			escaped = false
			continue
		}
		if quoted {
			switch c {
			case '\\':
				escaped = true
			case '"':
				quoted = false
			case '<', '>', '&':
				expansion += 5
			case 0xe2:
				if i+2 < len(raw) && raw[i+1] == 0x80 && raw[i+2]&^1 == 0xa8 {
					expansion += 3
				}
			}
		} else {
			switch c {
			case '"':
				quoted = true
			case '{', '[':
				depth++
				high = max(high, depth)
				structure += 1024
			case '}', ']':
				if depth > 0 {
					depth--
				}
			case ':', ',':
				structure += 512
			}
		}
	}
	scanner = min(high, 10000)*64 + 512
	if high > 1024 {
		// Raw validation re-scans failures to recover the syntax error.
		// encoding/json can drop the first scanner's deep stack.
		scanner *= 2
	}
	return width, expansion, scanner, structure, ctx.Err()
}

func metadataActivityEncodingWork(ctx context.Context, value reflect.Value) (uint64, error) {
	cost := uint64(1024)
	iter := value.MapRange()
	for iter.Next() {
		key, text := iter.Key().String(), iter.Value().String()
		k, err := metadataAggregateStringEncodingWork(ctx, key)
		if err != nil {
			return 0, err
		}
		v, err := metadataAggregateStringEncodingWork(ctx, text)
		if err != nil {
			return 0, err
		}
		// The inner map is built and encoded by the custom method. The
		// outer Marshal pre-grows for its result and compacts it again.
		cost += (k+v)*5 + 512
		if key == "addedLabelIds" || key == "removedLabelIds" {
			decode, err := metadataAggregatePayloadJSONWork(ctx, text)
			if err != nil {
				return 0, err
			}
			cost += decode
		}
		if cost > uint64(metadataWorkBytes()) {
			return 0, ErrMetadataWorkBudget
		}
	}
	return cost, nil
}

// The raw payload is retained through publication. enrichRealtimePayload also
// decodes its maps/interfaces and encodes the enriched envelope. Container
// charges prevent many tiny JSON objects from defeating a byte-only bound.
func metadataAggregatePayloadJSONWork[JSON []byte | json.RawMessage | string](ctx context.Context, raw JSON) (uint64, error) {
	if len(raw) > metadataWorkBytes()/16 {
		return 0, ErrMetadataWorkBudget
	}
	cost := uint64(len(raw)) * 16
	quoted, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
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
		if cost > uint64(metadataWorkBytes()) {
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

func (s *SQLiteStore) encodeMetadataMutationPayload(ctx context.Context, payload any) (json.RawMessage, func(), error) {
	return s.encodeMetadataJSONRetained(ctx, reflect.ValueOf(payload), marshalMetadataJSON)
}

func (s *SQLiteStore) encodeMetadataJSONRetained(ctx context.Context, value reflect.Value, encode func(any) ([]byte, error)) ([]byte, func(), error) {
	work, err := metadataRecordEncodingWork(ctx, value)
	if err != nil {
		return nil, nil, err
	}
	_, releaseEncoding, err := s.tryAdmitAdditionalMetadataWork(ctx, max(work, 128<<10))
	if err != nil {
		return nil, nil, err
	}
	defer releaseEncoding()
	payload := any(nil)
	if value.IsValid() {
		payload = value.Interface()
	}
	raw, err := encode(payload)
	if err != nil {
		return nil, nil, err
	}
	_, retain, err := s.tryAdmitAdditionalMetadataWork(ctx, ((cap(raw)+8191)&^8191)+64)
	if err != nil {
		return nil, nil, err
	}
	return raw, retain, nil
}
