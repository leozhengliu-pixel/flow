package store

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"flow/api/internal/domain"
)

type resourceReadCompatibilityKey struct{}

// The installed resource bodies are not immutable: some catalog writers
// update them under mu. Retain one compact wire identity per resource before
// acquiring SQL, rather than retaining those mutable graphs across a transaction.
type resourceReadCompatibilitySnapshot struct {
	database  *sqlDatabase
	workspace string
	data      domain.Bootstrap
	digests   map[resourceWireIdentity]struct{}
}

type resourceWireIdentity struct {
	key    RecordKey
	digest [sha256.Size]byte
}

const resourceReadWireCacheMaxBytes = 8 << 20

// Retain only the last workspace's compact witness. Old request contexts may
// keep earlier immutable witnesses until they release their SQL snapshots.
type resourceReadWireCache struct {
	mu                     sync.Mutex
	workspace, workspaceID string
	epoch                  uint64
	digests                map[resourceWireIdentity]struct{}
}

// The caller holds mu before acquiring SQL. Writers never take the cache
// mutex; both known and unknown publications advance copyRevisionEpoch.
func (s *SQLiteStore) resourceReadCompatibilityDigestsLocked(ctx context.Context, workspace string, data domain.Bootstrap) (map[resourceWireIdentity]struct{}, error) {
	cache := &s.resourceWireCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cache.workspace == workspace && cache.workspaceID == data.Workspace.ID && cache.epoch == s.copyRevisionEpoch && cache.digests != nil {
		return cache.digests, nil
	}
	digests, err := resourceReadCompatibilityDigests(ctx, data)
	if err != nil {
		return nil, err
	}
	// Conservative accounting includes key storage, bucket slack, IDs and
	// map headers. An oversized witness remains exact and request-owned.
	bytes := int64(256) + 256*int64(len(data.Projects)+len(data.Initiatives))
	for identity := range digests {
		bytes += 2 * int64(len(identity.key.Kind)+len(identity.key.ID))
		if bytes > resourceReadWireCacheMaxBytes {
			cache.workspace, cache.workspaceID, cache.epoch = "", "", 0
			cache.digests = nil
			return digests, nil
		}
	}
	cache.workspace, cache.workspaceID, cache.epoch = workspace, data.Workspace.ID, s.copyRevisionEpoch
	cache.digests = digests
	return digests, nil
}

func resourceReadCompatibilityDigests(ctx context.Context, data domain.Bootstrap) (map[resourceWireIdentity]struct{}, error) {
	result := make(map[resourceWireIdentity]struct{}, len(data.Projects)+len(data.Initiatives))
	for _, item := range data.Projects {
		digest, err := resourceWireDigest(ctx, item)
		if err != nil {
			return nil, err
		}
		result[resourceWireIdentity{RecordKey{Kind: "projects", ID: item.ID}, digest}] = struct{}{}
	}
	for _, item := range data.Initiatives {
		digest, err := resourceWireDigest(ctx, item)
		if err != nil {
			return nil, err
		}
		result[resourceWireIdentity{RecordKey{Kind: "initiatives", ID: item.ID}, digest}] = struct{}{}
	}
	return result, nil
}

func (snapshot resourceReadCompatibilitySnapshot) matches(ctx context.Context, data domain.Bootstrap) (bool, error) {
	if snapshot.digests == nil {
		return resourceReadMatchesInstalled(snapshot.data, data), nil
	}
	match := func(key RecordKey, item any) (bool, error) {
		got, err := resourceWireDigest(ctx, item)
		_, found := snapshot.digests[resourceWireIdentity{key, got}]
		return err == nil && found, err
	}
	for _, item := range data.Projects {
		if ok, err := match(RecordKey{Kind: "projects", ID: item.ID}, item); ok || err != nil {
			return ok, err
		}
	}
	for _, item := range data.Initiatives {
		if ok, err := match(RecordKey{Kind: "initiatives", ID: item.ID}, item); ok || err != nil {
			return ok, err
		}
	}
	return false, nil
}

func resourceWireDigest(ctx context.Context, value any) ([sha256.Size]byte, error) {
	hash := sha256.New()
	out := bufio.NewWriterSize(hash, 4096)
	if err := resourceWireJSON(ctx, out, reflect.ValueOf(value), 0); err != nil {
		return [sha256.Size]byte{}, err
	}
	if err := out.Flush(); err != nil {
		return [sha256.Size]byte{}, err
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

var resourceJSONMarshaler = reflect.TypeFor[json.Marshaler]()
var resourceTextMarshaler = reflect.TypeFor[encoding.TextMarshaler]()

// Ordinary domain records, descriptions, histories and rich-text maps use
// fixed encoding scratch. Custom marshalers and unusual dynamic struct shapes
// retain encoding/json's behavior and its whole-custom-value buffer.
func resourceWireJSON(ctx context.Context, out *bufio.Writer, value reflect.Value, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 1000 {
		raw, err := json.Marshal(value.Interface())
		if err != nil {
			return err
		}
		_, err = out.Write(raw)
		return err
	}
	if !value.IsValid() {
		_, err := out.WriteString("null")
		return err
	}
	if value.Type() == reflect.TypeFor[json.RawMessage]() {
		return resourceWireRawJSON(ctx, out, value.Bytes())
	}
	if value.Kind() == reflect.Pointer && !value.IsNil() && value.Type().Elem() == reflect.TypeFor[json.RawMessage]() {
		return resourceWireRawJSON(ctx, out, value.Elem().Bytes())
	}
	if value.CanAddr() && (reflect.PointerTo(value.Type()).Implements(resourceJSONMarshaler) || reflect.PointerTo(value.Type()).Implements(resourceTextMarshaler)) {
		value = value.Addr()
	}
	if value.Type().Implements(resourceJSONMarshaler) || value.Type().Implements(resourceTextMarshaler) {
		raw, err := json.Marshal(value.Interface())
		if err != nil {
			return err
		}
		_, err = out.Write(raw)
		return err
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			_, err := out.WriteString("null")
			return err
		}
		return resourceWireJSON(ctx, out, value.Elem(), depth+1)
	case reflect.String:
		if value.Type() == reflect.TypeFor[json.Number]() {
			raw, err := json.Marshal(value.Interface())
			if err != nil {
				return err
			}
			_, err = out.Write(raw)
			return err
		}
		return resourceWireString(ctx, out, value.String())
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice {
			if value.IsNil() {
				_, err := out.WriteString("null")
				return err
			}
			if value.Type().Elem().Kind() == reflect.Uint8 &&
				!reflect.PointerTo(value.Type().Elem()).Implements(resourceJSONMarshaler) &&
				!reflect.PointerTo(value.Type().Elem()).Implements(resourceTextMarshaler) {
				if err := out.WriteByte('"'); err != nil {
					return err
				}
				encoder := base64.NewEncoder(base64.StdEncoding, out)
				raw := value.Bytes()
				for len(raw) > 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
					end := min(4096, len(raw))
					if _, err := encoder.Write(raw[:end]); err != nil {
						return err
					}
					raw = raw[end:]
				}
				if err := encoder.Close(); err != nil {
					return err
				}
				return out.WriteByte('"')
			}
		}
		out.WriteByte('[')
		for index := 0; index < value.Len(); index++ {
			if index > 0 {
				out.WriteByte(',')
			}
			if err := resourceWireJSON(ctx, out, value.Index(index), depth+1); err != nil {
				return err
			}
		}
		return out.WriteByte(']')
	case reflect.Map:
		if !value.IsNil() && value.Type().Key() == reflect.TypeFor[string]() {
			out.WriteByte('{')
			// Keep key ordering identical to encoding/json with fixed scratch
			// space even for a large mutable rich-text object.
			var after string
			first, haveAfter := true, false
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				keys := make([]string, 0, min(128, value.Len()))
				iter := value.MapRange()
				scanned := 0
				for iter.Next() {
					scanned++
					if scanned%128 == 0 {
						if err := ctx.Err(); err != nil {
							return err
						}
					}
					key := iter.Key().String()
					if haveAfter && key <= after {
						continue
					}
					if len(keys) < cap(keys) {
						keys = append(keys, key)
						slices.Sort(keys)
					} else if key < keys[len(keys)-1] {
						keys[len(keys)-1] = key
						slices.Sort(keys)
					}
				}
				if len(keys) == 0 {
					break
				}
				for _, key := range keys {
					if !first {
						out.WriteByte(',')
					}
					first = false
					if err := resourceWireString(ctx, out, key); err != nil {
						return err
					}
					out.WriteByte(':')
					if err := resourceWireJSON(ctx, out, value.MapIndex(reflect.ValueOf(key)), depth+1); err != nil {
						return err
					}
				}
				after, haveAfter = keys[len(keys)-1], true
			}
			return out.WriteByte('}')
		}
	case reflect.Struct:
		if value.Type().PkgPath() != "flow/api/internal/domain" {
			raw, err := json.Marshal(value.Interface())
			if err != nil {
				return err
			}
			_, err = out.Write(raw)
			return err
		}
		names := make(map[string]bool, value.NumField())
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			tag := strings.Split(field.Tag.Get("json"), ",")
			name := tag[0]
			if name == "" {
				name = field.Name
			}
			if !field.IsExported() || name == "-" {
				continue
			}
			if field.Anonymous || slices.Contains(tag, "string") || slices.Contains(tag, "omitzero") || names[name] {
				raw, err := json.Marshal(value.Interface())
				if err != nil {
					return err
				}
				_, err = out.Write(raw)
				return err
			}
			names[name] = true
		}
		out.WriteByte('{')
		first := true
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if !field.IsExported() {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			item := value.Field(index)
			if slices.Contains(tag[1:], "omitempty") && resourceWireEmpty(item) {
				continue
			}
			if !first {
				out.WriteByte(',')
			}
			first = false
			name := tag[0]
			if name == "" {
				name = field.Name
			}
			if err := resourceWireString(ctx, out, name); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := resourceWireJSON(ctx, out, item, depth+1); err != nil {
				return err
			}
		}
		return out.WriteByte('}')
	}
	raw, err := json.Marshal(value.Interface())
	if err != nil {
		return err
	}
	_, err = out.Write(raw)
	return err
}

func resourceWireEmpty(value reflect.Value) bool {
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

func resourceWireString(ctx context.Context, out *bufio.Writer, value string) error {
	out.WriteByte('"')
	for len(value) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(4096, len(value))
		for end < len(value) && end > 0 && !utf8.RuneStart(value[end]) {
			end--
		}
		if end == 0 {
			end = min(4096, len(value))
		}
		raw, err := json.Marshal(value[:end])
		if err != nil {
			return err
		}
		if _, err := out.Write(raw[1 : len(raw)-1]); err != nil {
			return err
		}
		value = value[end:]
	}
	return out.WriteByte('"')
}

func resourceWireRawJSON(ctx context.Context, out *bufio.Writer, raw []byte) error {
	if raw == nil {
		_, err := out.WriteString("null")
		return err
	}
	if !json.Valid(raw) {
		return fmt.Errorf("invalid resource raw JSON")
	}
	quoted, escaped := false, false
	start := 0
	for index := 0; index < len(raw); index++ {
		if index-start >= 4096 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := out.Write(raw[start:index]); err != nil {
				return err
			}
			start = index
		}
		c := raw[index]
		if !quoted && (c == ' ' || c == '\n' || c == '\r' || c == '\t') {
			out.Write(raw[start:index])
			start = index + 1
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
			replacement, size := "", 1
			switch c {
			case '<':
				replacement = `\u003c`
			case '>':
				replacement = `\u003e`
			case '&':
				replacement = `\u0026`
			case 0xe2:
				if index+2 < len(raw) && raw[index+1] == 0x80 && (raw[index+2] == 0xa8 || raw[index+2] == 0xa9) {
					replacement, size = `\u2028`, 3
					if raw[index+2] == 0xa9 {
						replacement = `\u2029`
					}
				}
			}
			if replacement != "" {
				out.Write(raw[start:index])
				out.WriteString(replacement)
				index += size - 1
				start = index + 1
			}
		}
	}
	_, err := out.Write(raw[start:])
	return err
}
