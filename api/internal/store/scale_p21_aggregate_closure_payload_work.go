package store

import (
	"context"
	"encoding/json"
	"reflect"
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
