package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Stream nested collections and strings too: one document or history entry
// can be much larger than a page. Custom marshalers retain encoding/json's
// behavior; ordinary domain records never need an entity-sized JSON buffer.
func encodeHydratedJSON(ctx context.Context, buffer *bufio.Writer, value reflect.Value, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !value.IsValid() {
		_, err := buffer.WriteString("null")
		return err
	}
	t := value.Type()
	if t == reflect.TypeFor[domain.AgentToolCall]() {
		return encodeHydratedAgentToolCall(ctx, buffer, value.Interface().(domain.AgentToolCall), depth)
	}
	if value.Kind() == reflect.Pointer && !value.IsNil() && t.Elem() == reflect.TypeFor[domain.AgentToolCall]() {
		return encodeHydratedAgentToolCall(ctx, buffer, value.Elem().Interface().(domain.AgentToolCall), depth)
	}
	if t == reflect.TypeFor[json.RawMessage]() {
		return encodeHydratedRawJSON(ctx, buffer, value.Bytes())
	}
	if value.Kind() == reflect.Pointer && !value.IsNil() && t.Elem() == reflect.TypeFor[json.RawMessage]() {
		return encodeHydratedRawJSON(ctx, buffer, value.Elem().Bytes())
	}
	custom := t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType)
	if value.CanAddr() && (reflect.PointerTo(t).Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)) {
		value = value.Addr()
		custom = true
	}
	if custom || depth > 1000 {
		raw, err := json.Marshal(value.Interface())
		if err != nil {
			return err
		}
		_, err = buffer.Write(raw)
		return err
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			_, err := buffer.WriteString("null")
			return err
		}
		return encodeHydratedJSON(ctx, buffer, value.Elem(), depth+1)
	case reflect.String:
		return encodeHydratedString(ctx, buffer, value.String())
	case reflect.Struct:
		if plan := streamPlanFor(t); plan != nil {
			return encodeHydratedStruct(ctx, buffer, value, plan, depth+1)
		}
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice {
			if value.IsNil() {
				_, err := buffer.WriteString("null")
				return err
			}
			if value.Type().Elem().Kind() == reflect.Uint8 {
				buffer.WriteByte('"')
				encoder := base64.NewEncoder(base64.StdEncoding, buffer)
				body := value.Bytes()
				for start := 0; start < len(body); start += 16 << 10 {
					if err := ctx.Err(); err != nil {
						return err
					}
					if _, err := encoder.Write(body[start:min(start+16<<10, len(body))]); err != nil {
						return err
					}
				}
				if err := encoder.Close(); err != nil {
					return err
				}
				return buffer.WriteByte('"')
			}
		}
		buffer.WriteByte('[')
		for i := 0; i < value.Len(); i++ {
			if i > 0 {
				buffer.WriteByte(',')
			}
			if err := encodeHydratedJSON(ctx, buffer, value.Index(i), depth+1); err != nil {
				return err
			}
		}
		return buffer.WriteByte(']')
	case reflect.Map:
		if value.IsNil() || t.Key().Kind() != reflect.String || t.Key().Implements(textMarshalerType) {
			break
		}
		keys := value.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int {
			if a.String() < b.String() {
				return -1
			}
			if a.String() > b.String() {
				return 1
			}
			return 0
		})
		buffer.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				buffer.WriteByte(',')
			}
			if err := encodeHydratedString(ctx, buffer, key.String()); err != nil {
				return err
			}
			buffer.WriteByte(':')
			if err := encodeHydratedJSON(ctx, buffer, value.MapIndex(key), depth+1); err != nil {
				return err
			}
		}
		return buffer.WriteByte('}')
	}
	raw, err := json.Marshal(value.Interface())
	if err != nil {
		return err
	}
	_, err = buffer.Write(raw)
	return err
}

// Reproduce AgentToolCall.MarshalJSON's partial-provider-argument handling
// while streaming its possibly very large tool result.
func encodeHydratedAgentToolCall(ctx context.Context, buffer *bufio.Writer, call domain.AgentToolCall, depth int) error {
	type plain domain.AgentToolCall
	if !json.Valid(call.Arguments) {
		call.Arguments = json.RawMessage(`{}`)
	}
	invalidResult := len(call.Result) > 0 && !json.Valid(call.Result)
	value := reflect.ValueOf(plain(call))
	buffer.WriteByte('{')
	first := true
	for _, field := range streamPlanFor(value.Type()) {
		item := value.Field(field.index)
		if field.omitEmpty && isEmptyJSONValue(item) {
			continue
		}
		if !first {
			buffer.WriteByte(',')
		}
		first = false
		buffer.Write(field.key)
		if invalidResult && string(field.key) == `"result":` {
			buffer.WriteByte('"')
			for start := 0; start < len(call.Result); {
				end := min(start+16<<10, len(call.Result))
				for end < len(call.Result) && !utf8.RuneStart(call.Result[end]) {
					end--
					if end == start {
						end = min(start+16<<10, len(call.Result))
						break
					}
				}
				if err := encodeHydratedStringContent(ctx, buffer, string(call.Result[start:end])); err != nil {
					return err
				}
				start = end
			}
			buffer.WriteByte('"')
		} else if err := encodeHydratedJSON(ctx, buffer, item, depth+1); err != nil {
			return err
		}
	}
	return buffer.WriteByte('}')
}

func encodeHydratedRawJSON(ctx context.Context, buffer *bufio.Writer, raw []byte) error {
	if raw == nil {
		_, err := buffer.WriteString("null")
		return err
	}
	if !json.Valid(raw) {
		return fmt.Errorf("invalid raw JSON")
	}
	quoted, escaped := false, false
	start := 0
	for i := 0; i < len(raw); i++ {
		if i-start >= 16<<10 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := buffer.Write(raw[start:i]); err != nil {
				return err
			}
			start = i
		}
		c := raw[i]
		if !quoted && (c == ' ' || c == '\n' || c == '\r' || c == '\t') {
			buffer.Write(raw[start:i])
			start = i + 1
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quoted {
			escaped = true
			continue
		}
		if c == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			var replacement string
			size := 1
			switch c {
			case '<':
				replacement = `\u003c`
			case '>':
				replacement = `\u003e`
			case '&':
				replacement = `\u0026`
			case 0xe2:
				if i+2 < len(raw) && raw[i+1] == 0x80 && (raw[i+2] == 0xa8 || raw[i+2] == 0xa9) {
					replacement = `\u2028`
					if raw[i+2] == 0xa9 {
						replacement = `\u2029`
					}
					size = 3
				}
			}
			if replacement != "" {
				buffer.Write(raw[start:i])
				buffer.WriteString(replacement)
				i += size - 1
				start = i + 1
			}
		}
	}
	_, err := buffer.Write(raw[start:])
	return err
}

func encodeHydratedStruct(ctx context.Context, buffer *bufio.Writer, value reflect.Value, plan []streamField, depth int) error {
	buffer.WriteByte('{')
	first := true
	for _, field := range plan {
		item := value.Field(field.index)
		if field.omitEmpty && isEmptyJSONValue(item) {
			continue
		}
		if !first {
			buffer.WriteByte(',')
		}
		first = false
		buffer.Write(field.key)
		if err := encodeHydratedJSON(ctx, buffer, item, depth+1); err != nil {
			return err
		}
	}
	return buffer.WriteByte('}')
}

func encodeHydratedString(ctx context.Context, buffer *bufio.Writer, value string) error {
	buffer.WriteByte('"')
	if err := encodeHydratedStringContent(ctx, buffer, value); err != nil {
		return err
	}
	return buffer.WriteByte('"')
}

func encodeHydratedStringContent(ctx context.Context, buffer *bufio.Writer, value string) error {
	start := 0
	for i := 0; i < len(value); {
		if i-start >= 16<<10 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := buffer.WriteString(value[start:i]); err != nil {
				return err
			}
			start = i
		}
		c := value[i]
		var escaped string
		size := 1
		switch {
		case c < utf8.RuneSelf:
			escaped = hydratedASCIIQuotes[c]
		case c >= utf8.RuneSelf:
			r, n := utf8.DecodeRuneInString(value[i:])
			size = n
			if r == utf8.RuneError && n == 1 {
				escaped = `\ufffd`
			} else if r == '\u2028' {
				escaped = `\u2028`
			} else if r == '\u2029' {
				escaped = `\u2029`
			}
		}
		if escaped != "" {
			if _, err := buffer.WriteString(value[start:i]); err != nil {
				return err
			}
			if _, err := buffer.WriteString(escaped); err != nil {
				return err
			}
			start = i + size
		}
		i += size
	}
	_, err := buffer.WriteString(value[start:])
	return err
}

var hydratedASCIIQuotes = func() [128]string {
	const hex = "0123456789abcdef"
	var escaped [128]string
	for c := range 32 {
		escaped[c] = string([]byte{'\\', 'u', '0', '0', hex[c>>4], hex[c&0xf]})
	}
	escaped['\\'], escaped['"'] = `\\`, `\"`
	escaped['\n'], escaped['\r'], escaped['\t'], escaped['\b'], escaped['\f'] = `\n`, `\r`, `\t`, `\b`, `\f`
	escaped['<'], escaped['>'], escaped['&'] = `\u003c`, `\u003e`, `\u0026`
	return escaped
}()
