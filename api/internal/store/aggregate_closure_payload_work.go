package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
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
		if part > uint64(metadataWorkBytes()) || cost > uint64(metadataWorkBytes())-part {
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
		work, err := metadataAggregateStringEncodingWork(ctx, value.String())
		if err != nil {
			return 0, err
		}
		if err := add(work); err != nil {
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
			tag := field.Tag.Get("json")
			if field.PkgPath != "" || tag == "-" {
				continue
			}
			if metadataAggregateOmittedField(tag, value.Field(i)) {
				// encoding/json writes neither the key nor the value of an
				// empty omitempty field, and a decoder of that encoding builds
				// nothing for it. Charging such fields as written made a long
				// project progress history (mostly empty points) cost about
				// 400 times its encoded size. The inline bytes stay charged.
				if err := add(uint64(field.Type.Size())); err != nil {
					return 0, err
				}
				continue
			}
			if err := add(uint64(len(field.Name)+len(tag)) * 24); err != nil {
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

// metadataAggregateOmittedField reports whether encoding/json omits a field
// with this tag and value: an omitempty field holding an empty value, as
// encoding/json's isEmptyValue defines it.
func metadataAggregateOmittedField(tag string, value reflect.Value) bool {
	_, options, _ := strings.Cut(tag, ",")
	omitEmpty := false
	for options != "" && !omitEmpty {
		var option string
		option, options, _ = strings.Cut(options, ",")
		omitEmpty = option == "omitempty"
	}
	if !omitEmpty {
		return false
	}
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

// Count the bytes encoding/json actually emits, before allocating any output.
// Four encoded widths cover appendString's temporary growth, the encoder's
// old and replacement buffers and Marshal's returned copy. This retains the
// previous buffer bound without charging every ordinary byte as a \u escape.
func metadataAggregateStringEncodingWork(ctx context.Context, value string) (uint64, error) {
	encoded := uint64(2)
	limit := uint64(metadataWorkBytes()) / 4
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
	return encoded * 4, nil
}

// The raw payload is retained through publication. enrichRealtimePayload also
// decodes its maps/interfaces and encodes the enriched envelope. Container
// charges prevent many tiny JSON objects from defeating a byte-only bound.
func metadataAggregatePayloadJSONWork(ctx context.Context, raw []byte) (uint64, error) {
	if len(raw) > metadataWorkBytes()/16 {
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
