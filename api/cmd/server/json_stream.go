package main

import (
	"bufio"
	"context"
	"encoding"
	"encoding/json"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"flow/api/internal/store"
)

// writeStreamedJSON writes value (a struct) exactly as writeJSON would, but
// encodes each slice field one element at a time through a small buffer.
//
// json.Encoder.Encode first builds the whole document in memory: for a large
// workspace's metadata that is a buffer of tens of MiB per request (grown by
// doubling, so about twice that is allocated), held until a slow client has
// read all of it. Here the largest buffer is one element. The output is
// byte-identical to json.Marshal(value) followed by a newline (see
// json_stream_test.go). If encoding fails or the client goes away after the
// status line was sent, the response is aborted rather than left as valid
// but truncated JSON.
func writeStreamedJSON(ctx context.Context, w http.ResponseWriter, status int, value any) {
	if status == http.StatusOK || status == http.StatusCreated {
		ctx = hydratedSigningContext(ctx, w)
	}
	v := reflect.ValueOf(value)
	plan := streamPlanFor(v.Type())
	if plan == nil {
		writeJSON(w, status, value)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if store.RecordHydrationEnabled() {
		prepareHydratedBootstrapSpool(w, value)
	}
	buffer := bufio.NewWriterSize(w, 64<<10)
	if err := encodeStreamedStruct(ctx, buffer, v, plan); err != nil {
		log.Printf("encode streamed response: %v", err)
		panic(http.ErrAbortHandler)
	}
	buffer.WriteByte('\n')
	if err := buffer.Flush(); err != nil {
		log.Printf("write streamed response: %v", err)
	}
}

type streamField struct {
	index     int
	key       []byte // `"name":`
	omitEmpty bool
	// stream encodes a slice one element at a time.
	stream bool
}

var streamPlans sync.Map // reflect.Type -> []streamField (nil when unsupported)

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// streamPlanFor returns the encoding plan of a struct type, or nil when the
// type uses something the streamed writer does not reproduce (a custom
// marshaler, embedded fields, tag options other than omitempty).
func streamPlanFor(t reflect.Type) []streamField {
	if cached, ok := streamPlans.Load(t); ok {
		return cached.([]streamField)
	}
	plan := buildStreamPlan(t)
	streamPlans.Store(t, plan)
	return plan
}

func buildStreamPlan(t reflect.Type) []streamField {
	if t.Kind() != reflect.Struct || t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType) || t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType) {
		return nil
	}
	plan := []streamField{}
	seen := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.Anonymous {
			return nil
		}
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		omitEmpty := false
		for option := range strings.SplitSeq(options, ",") {
			switch option {
			case "":
			case "omitempty":
				omitEmpty = true
			default:
				return nil
			}
		}
		if name == "" {
			name = field.Name
		}
		if seen[name] {
			return nil
		}
		seen[name] = true
		key, err := json.Marshal(name)
		if err != nil {
			return nil
		}
		ft := field.Type
		stream := ft.Kind() == reflect.Slice && ft.Elem().Kind() != reflect.Uint8 && !ft.Implements(jsonMarshalerType) && !ft.Implements(textMarshalerType)
		plan = append(plan, streamField{index: i, key: append(key, ':'), omitEmpty: omitEmpty, stream: stream})
	}
	return plan
}

// isEmptyJSONValue is encoding/json's omitempty test.
func isEmptyJSONValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

func encodeStreamedStruct(ctx context.Context, buffer *bufio.Writer, v reflect.Value, plan []streamField) error {
	if store.RecordHydrationEnabled() {
		return encodeHydratedStruct(ctx, buffer, v, plan, 0)
	}
	buffer.WriteByte('{')
	first := true
	for _, field := range plan {
		value := v.Field(field.index)
		if field.omitEmpty && isEmptyJSONValue(value) {
			continue
		}
		if !first {
			buffer.WriteByte(',')
		}
		first = false
		buffer.Write(field.key)
		if !field.stream || value.IsNil() {
			// The struct was passed by value: its fields are not addressable,
			// as in json.Marshal(value).
			raw, err := json.Marshal(value.Interface())
			if err != nil {
				return err
			}
			buffer.Write(raw)
			continue
		}
		buffer.WriteByte('[')
		for index := 0; index < value.Len(); index++ {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if index%256 == 0 && ctx.Err() != nil {
				return ctx.Err()
			}
			// Slice elements are addressable in encoding/json, so pointer
			// receiver marshalers apply to them: encode through the address.
			raw, err := json.Marshal(value.Index(index).Addr().Interface())
			if err != nil {
				return err
			}
			if _, err := buffer.Write(raw); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	}
	buffer.WriteByte('}')
	return nil
}
