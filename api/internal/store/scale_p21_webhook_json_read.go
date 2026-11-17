package store

import (
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// WebhookAggregateRead pins the literal WorkspaceMetadata projection. It reads
// a JSON view, rather than the typed event aggregate lookup: JSON objects use
// fresh Go map traversal, own IDs precede children, and raw JSON is searchable.
// No returned value escapes the operation's admitted work lifetime.
type WebhookAggregateRead struct {
	mu        sync.Mutex
	installed *InstalledMetadataRead
	store     *SQLiteStore
	root      webhookJSONValue
	plans     map[reflect.Type][]webhookJSONField
	releases  []func()
	checked   bool
	valid     bool
	closed    bool
}

type webhookJSONValue struct {
	node   metadataLogicalNode
	fields []metadataLogicalNode
	value  any
	quoted bool
}

type webhookJSONField struct {
	name                string
	index               []int
	typ                 reflect.Type
	tagged, empty, zero bool
	quoted              bool
}

// BeginWebhookAggregateRead is trusted routing input, not an authorization
// boundary. The caller retains its original accessor when bounded mode is off.
func (s *SQLiteStore) BeginWebhookAggregateRead(ctx context.Context, workspace string) (*WebhookAggregateRead, bool, error) {
	// WorkspaceMetadata has no caller snapshot argument. Capture the current
	// installed generation at this lazy lookup, independently of earlier reads.
	ctx = context.WithValue(ctx, metadataLogicalReadKey{}, nil)
	installed, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	return &WebhookAggregateRead{
		installed: installed, store: s,
		root: webhookJSONValue{fields: installed.source.fields},
	}, true, nil
}

func (r *WebhookAggregateRead) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.root = webhookJSONValue{}
		r.plans = nil
		for _, release := range r.releases {
			release()
		}
		r.releases = nil
		r.installed.Close()
		r.installed = nil
	}
}

// TeamsAllowed preserves the first matching JSON object's teamId/team/teamIds
// precedence. An unrelated JSON encoding/decoding error makes the lookup false,
// just as the legacy whole-graph round trip did. Budget and context failures
// remain explicit errors. Each call creates a fresh object traversal.
func (r *WebhookAggregateRead) TeamsAllowed(ctx context.Context, id string, teams []string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false, errMetadataLogicalClosed
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if id == "" {
		return false, nil
	}
	if r.checked && !r.valid {
		return false, nil
	}
	var allowed bool
	err := r.walk(ctx, r.root, false, id, func(object map[string]webhookJSONValue) error {
		var err error
		allowed, err = r.objectTeamsAllowed(ctx, object, teams)
		if err != nil {
			return err
		}
		return errMetadataAggregateStop
	})
	if errors.Is(err, errMetadataAggregateStop) {
		err = nil
	}
	if errors.Is(err, errWebhookJSONInvalid) {
		r.checked, r.valid = true, false
		return false, nil
	}
	if err != nil || !allowed {
		return false, err
	}
	// A negative lookup needs no serialization: even a whole-graph JSON
	// failure would deny it. Validate the complete graph before allowing.
	if !r.checked {
		err := r.walk(ctx, r.root, true, "", nil)
		if err != nil && !errors.Is(err, errWebhookJSONInvalid) {
			return false, err
		}
		r.checked, r.valid = true, err == nil
	}
	return r.valid, nil
}

var errWebhookJSONInvalid = errors.New("legacy webhook JSON round trip failed")

func (r *WebhookAggregateRead) admit(ctx context.Context, cost uint64) (func(), error) {
	if cost > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	return release, err
}

func webhookJSONNormalize(value string) string {
	if utf8.ValidString(value) {
		return value
	}
	return strings.Map(func(char rune) rune { return char }, value)
}

func (r *WebhookAggregateRead) decoded(ctx context.Context, value webhookJSONValue, visit func(any) error) error {
	if value.node.typ == nil {
		return visit(value.value)
	}
	if value.node.cost > (metadataWorkBytes-(64<<10))/24 {
		return ErrMetadataWorkBudget
	}
	release, err := r.admit(ctx, 64<<10+value.node.cost*24)
	if err != nil {
		return err
	}
	defer release()
	native, err := value.node.WireValue(ctx, int(value.node.cost))
	if err != nil {
		return err
	}
	raw, err := json.Marshal(native.Interface())
	if err != nil {
		return errWebhookJSONInvalid
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return errWebhookJSONInvalid
	}
	if value.quoted && generic != nil {
		generic = string(raw)
	}
	return visit(generic)
}

func webhookJSONCodec(typ reflect.Type) bool {
	return typ.Implements(reflect.TypeFor[json.Marshaler]()) ||
		typ.Implements(reflect.TypeFor[encoding.TextMarshaler]()) ||
		typ == reflect.TypeFor[json.Number]()
}

func (r *WebhookAggregateRead) unwrap(ctx context.Context, value webhookJSONValue) (webhookJSONValue, error) {
	for value.node.typ != nil && (value.node.typ.Kind() == reflect.Pointer || value.node.typ.Kind() == reflect.Interface) && !webhookJSONCodec(value.node.typ) {
		var child metadataLogicalNode
		if err := value.node.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			child = node
			return nil
		}); err != nil {
			return value, err
		}
		value.node = child
	}
	return value, nil
}

func (r *WebhookAggregateRead) walk(ctx context.Context, value webhookJSONValue, validate bool, id string, found func(map[string]webhookJSONValue) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	value, err = r.unwrap(ctx, value)
	if err != nil {
		return err
	}
	if value.node.typ != nil {
		typ := value.node.typ
		if value.quoted || webhookJSONCodec(typ) {
			if !validate && (value.quoted || typ == reflect.TypeFor[time.Time]() || typ == reflect.TypeFor[*time.Time]() ||
				typ == reflect.TypeFor[domain.OptionalInt]() || typ == reflect.TypeFor[*domain.OptionalInt]() || typ == reflect.TypeFor[json.Number]()) {
				return nil
			}
			return r.decoded(ctx, value, func(decoded any) error {
				return r.walk(ctx, webhookJSONValue{value: decoded}, validate, id, found)
			})
		}
		switch typ.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return nil
		case reflect.Float32, reflect.Float64, reflect.Func, reflect.Chan, reflect.Complex64, reflect.Complex128, reflect.UnsafePointer:
			if !validate {
				return nil
			}
			return r.decoded(ctx, value, func(any) error { return nil })
		case reflect.Array, reflect.Slice:
			if typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Uint8 {
				return nil
			}
			return value.node.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error {
				return r.walk(ctx, webhookJSONValue{node: child}, validate, id, found)
			})
		case reflect.Map, reflect.Struct:
		default:
			return ErrBoundedMetadataCoverage
		}
	} else if value.fields == nil {
		switch decoded := value.value.(type) {
		case map[string]any:
			release, err := r.admit(ctx, 4096+uint64(len(decoded))*256)
			if err != nil {
				return err
			}
			defer release()
			object := make(map[string]webhookJSONValue, len(decoded))
			for key, child := range decoded {
				object[key] = webhookJSONValue{value: child}
			}
			return r.walkObject(ctx, object, validate, id, found)
		case []any:
			for _, child := range decoded {
				if err := r.walk(ctx, webhookJSONValue{value: child}, validate, id, found); err != nil {
					return err
				}
			}
			return nil
		default:
			return nil
		}
	}
	return r.object(ctx, value, func(object map[string]webhookJSONValue) error {
		return r.walkObject(ctx, object, validate, id, found)
	})
}

func (r *WebhookAggregateRead) walkObject(ctx context.Context, object map[string]webhookJSONValue, validate bool, id string, found func(map[string]webhookJSONValue) error) error {
	if !validate {
		match := false
		if candidate, exists := object["id"]; exists {
			if err := r.decoded(ctx, candidate, func(value any) error {
				text, ok := value.(string)
				match = ok && text == id
				return nil
			}); err != nil {
				return err
			}
		}
		if match {
			return found(object)
		}
	}
	for _, child := range object {
		if err := r.walk(ctx, child, validate, id, found); err != nil {
			return err
		}
	}
	return nil
}

func webhookJSONFieldPlan(typ reflect.Type) []webhookJSONField {
	type level struct {
		typ   reflect.Type
		index []int
	}
	next := []level{{typ: typ}}
	visited := map[reflect.Type]bool{}
	var fields []webhookJSONField
	for len(next) > 0 {
		current := next
		next = nil
		counts := map[reflect.Type]int{}
		for _, parent := range current {
			counts[parent.typ]++
		}
		for _, parent := range current {
			if visited[parent.typ] {
				continue
			}
			visited[parent.typ] = true
			for index := 0; index < parent.typ.NumField(); index++ {
				field := parent.typ.Field(index)
				kind := field.Type
				if kind.Kind() == reflect.Pointer {
					kind = kind.Elem()
				}
				if !field.IsExported() && (!field.Anonymous || kind.Kind() != reflect.Struct) || field.Tag.Get("json") == "-" {
					continue
				}
				parts := strings.Split(field.Tag.Get("json"), ",")
				name := parts[0]
				for _, char := range name {
					if !unicode.IsLetter(char) && !unicode.IsDigit(char) && !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", char) {
						name = ""
						break
					}
				}
				path := append(slices.Clone(parent.index), index)
				if name == "" && field.Anonymous && kind.Kind() == reflect.Struct {
					next = append(next, level{typ: kind, index: path})
					if counts[parent.typ] > 1 {
						next = append(next, level{typ: kind, index: path})
					}
					continue
				}
				tagged := name != ""
				if name == "" {
					name = field.Name
				}
				quoted := slices.Contains(parts[1:], "string")
				switch kind.Kind() {
				case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
					reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
					reflect.Float32, reflect.Float64, reflect.String:
				default:
					quoted = false
				}
				planned := webhookJSONField{name, path, field.Type, tagged, slices.Contains(parts[1:], "omitempty"), slices.Contains(parts[1:], "omitzero"), quoted}
				fields = append(fields, planned)
				if counts[parent.typ] > 1 {
					fields = append(fields, planned)
				}
			}
		}
	}
	slices.SortFunc(fields, func(a, b webhookJSONField) int {
		if order := strings.Compare(a.name, b.name); order != 0 {
			return order
		}
		if len(a.index) != len(b.index) {
			return len(a.index) - len(b.index)
		}
		if a.tagged != b.tagged {
			if a.tagged {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	result := fields[:0]
	for index := 0; index < len(fields); {
		end := index + 1
		for end < len(fields) && fields[end].name == fields[index].name {
			end++
		}
		if end == index+1 || len(fields[index].index) != len(fields[index+1].index) || fields[index].tagged != fields[index+1].tagged {
			result = append(result, fields[index])
		}
		index = end
	}
	slices.SortFunc(result, func(a, b webhookJSONField) int { return slices.Compare(a.index, b.index) })
	return result
}

func (r *WebhookAggregateRead) plan(ctx context.Context, typ reflect.Type) ([]webhookJSONField, error) {
	if plan, found := r.plans[typ]; found {
		return plan, nil
	}
	seen := map[reflect.Type]bool{}
	var fields func(reflect.Type) int
	fields = func(typ reflect.Type) int {
		if seen[typ] {
			return 0
		}
		seen[typ] = true
		count := typ.NumField()
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			child := field.Type
			if child.Kind() == reflect.Pointer {
				child = child.Elem()
			}
			if field.Anonymous && child.Kind() == reflect.Struct {
				count += fields(child)
			}
		}
		return count
	}
	release, err := r.admit(ctx, 4096+uint64(fields(typ))*2048)
	if err != nil {
		return nil, err
	}
	plan := webhookJSONFieldPlan(typ)
	if r.plans == nil {
		r.plans = map[reflect.Type][]webhookJSONField{}
	}
	r.plans[typ] = plan
	r.releases = append(r.releases, release)
	return plan, nil
}

func (r *WebhookAggregateRead) field(ctx context.Context, root webhookJSONValue, path []int) (metadataLogicalNode, error) {
	node := root.node
	for depth, index := range path {
		if depth == 0 && root.fields != nil {
			node = root.fields[index]
			continue
		}
		unwrapped, err := r.unwrap(ctx, webhookJSONValue{node: node})
		if err != nil || unwrapped.node.typ == nil {
			return metadataLogicalNode{}, err
		}
		node = unwrapped.node
		var child metadataLogicalNode
		err = node.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
			if key.String() == node.typ.Field(index).Name {
				child = value
				return errMetadataAggregateStop
			}
			return nil
		})
		if err != nil && !errors.Is(err, errMetadataAggregateStop) {
			return metadataLogicalNode{}, err
		}
		node = child
	}
	return node, nil
}

func (r *WebhookAggregateRead) omitted(ctx context.Context, node metadataLogicalNode, field webhookJSONField) (bool, error) {
	if !field.empty && !field.zero {
		return false, nil
	}
	if node.generic {
		empty, err := node.genericEmpty(ctx, false)
		if err != nil || field.empty && empty {
			return empty, err
		}
		if field.zero {
			return node.genericEmpty(ctx, true)
		}
		return false, nil
	}
	if field.empty {
		cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
		switch node.typ.Kind() {
		case reflect.Array:
			if node.typ.Len() == 0 {
				return true, nil
			}
		case reflect.String:
			length, err := cursor.uint()
			if err != nil || length == 0 {
				return length == 0, err
			}
		case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
			present, err := cursor.byte()
			if err != nil || present == 0 {
				return present == 0, err
			}
			if node.typ.Kind() == reflect.Map || node.typ.Kind() == reflect.Slice {
				length, err := cursor.uint()
				if err != nil || length == 0 {
					return length == 0, err
				}
			}
		case reflect.Struct:
		default:
			var empty bool
			err := r.native(ctx, node, func(value reflect.Value) { empty = value.IsZero() })
			if err != nil || empty {
				return empty, err
			}
		}
	}
	if field.zero {
		zero := false
		err := r.native(ctx, node, func(value reflect.Value) {
			if (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) && value.IsNil() {
				zero = true
			} else if method := value.MethodByName("IsZero"); method.IsValid() && method.Type().NumIn() == 0 && method.Type().NumOut() == 1 && method.Type().Out(0).Kind() == reflect.Bool {
				zero = method.Call(nil)[0].Bool()
			} else {
				zero = value.IsZero()
			}
		})
		return zero, err
	}
	return false, nil
}

func (r *WebhookAggregateRead) native(ctx context.Context, node metadataLogicalNode, visit func(reflect.Value)) error {
	if node.cost > (metadataWorkBytes-4096)/2 {
		return ErrMetadataWorkBudget
	}
	release, err := r.admit(ctx, 4096+node.cost*2)
	if err != nil {
		return err
	}
	defer release()
	value, err := node.WireValue(ctx, int(node.cost))
	if err == nil {
		visit(value)
	}
	return err
}

func (r *WebhookAggregateRead) object(ctx context.Context, value webhookJSONValue, visit func(map[string]webhookJSONValue) error) error {
	typ := value.node.typ
	literalRoot := typ == nil && value.fields != nil
	if literalRoot {
		typ = reflect.TypeFor[domain.Bootstrap]()
	}
	// Object headers are admitted separately from all child bodies. A large
	// legal object may consume more work; unrelated scalar bodies never do.
	release, err := r.admit(ctx, 64<<10)
	if err != nil {
		return err
	}
	defer release()
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	object := map[string]webhookJSONValue{}
	put := func(name string, child webhookJSONValue) error {
		release, err := r.admit(ctx, 512+uint64(len(name))*8)
		if err != nil {
			return err
		}
		releases = append(releases, release)
		object[webhookJSONNormalize(name)] = child
		return nil
	}
	if typ.Kind() == reflect.Struct {
		plan, err := r.plan(ctx, typ)
		if err != nil {
			return err
		}
		if !literalRoot {
			value.fields = make([]metadataLogicalNode, typ.NumField())
			index := 0
			err := value.node.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
				value.fields[index] = node
				index++
				return nil
			})
			if err != nil {
				return err
			}
		}
		for _, field := range plan {
			// The literal accessor replaces these five heavy collections
			// with empty containers before cloning the metadata.
			if literalRoot && slices.Contains([]string{"issues", "activities", "comments", "notifications", "notificationDeliveries"}, field.name) {
				continue
			}
			node, err := r.field(ctx, value, field.index)
			if err != nil {
				return err
			}
			if node.typ == nil {
				continue
			}
			omit, err := r.omitted(ctx, node, field)
			if err != nil {
				return err
			}
			if !omit {
				if err := put(field.name, webhookJSONValue{node: node, quoted: field.quoted}); err != nil {
					return err
				}
			}
		}
		return visit(object)
	}
	keyType := typ.Key()
	textKey := keyType.Implements(reflect.TypeFor[encoding.TextMarshaler]())
	if keyType.Kind() != reflect.String && (keyType.Kind() < reflect.Int || keyType.Kind() > reflect.Uintptr) && !textKey {
		return errWebhookJSONInvalid
	}
	type entry struct {
		key   string
		value metadataLogicalNode
	}
	var entries []entry
	err = value.node.MapEntries(ctx, func(key, child metadataLogicalNode) error {
		var name string
		var keyErr error
		err := r.native(ctx, key, func(native reflect.Value) {
			switch {
			case native.Kind() == reflect.String:
				name = native.String()
			case textKey:
				if native.Kind() == reflect.Pointer && native.IsNil() {
					break
				}
				var raw []byte
				raw, keyErr = native.Interface().(encoding.TextMarshaler).MarshalText()
				name = string(raw)
			case native.Kind() >= reflect.Int && native.Kind() <= reflect.Int64:
				name = strconv.FormatInt(native.Int(), 10)
			case native.Kind() >= reflect.Uint && native.Kind() <= reflect.Uintptr:
				name = strconv.FormatUint(native.Uint(), 10)
			}
		})
		if err != nil {
			return err
		}
		if keyErr != nil {
			return errWebhookJSONInvalid
		}
		release, err := r.admit(ctx, 512+uint64(len(name))*8)
		if err != nil {
			return err
		}
		releases = append(releases, release)
		entries = append(entries, entry{strings.Clone(name), child})
		return nil
	})
	if err != nil {
		return err
	}
	slices.SortStableFunc(entries, func(a, b entry) int { return strings.Compare(a.key, b.key) })
	for _, entry := range entries {
		if err := put(entry.key, webhookJSONValue{node: entry.value}); err != nil {
			return err
		}
	}
	return visit(object)
}

func (r *WebhookAggregateRead) objectTeamsAllowed(ctx context.Context, object map[string]webhookJSONValue, teams []string) (bool, error) {
	match := func(value webhookJSONValue) (bool, error) {
		var allowed bool
		err := r.decoded(ctx, value, func(decoded any) error {
			id, _ := decoded.(string)
			allowed = id != "" && slices.Contains(teams, id)
			return nil
		})
		return allowed, err
	}
	if value, ok := object["teamId"]; ok {
		var id string
		err := r.decoded(ctx, value, func(decoded any) error { id, _ = decoded.(string); return nil })
		if err != nil || id != "" {
			return slices.Contains(teams, id), err
		}
	}
	if value, ok := object["team"]; ok {
		unwrapped, err := r.unwrap(ctx, value)
		if err != nil {
			return false, err
		}
		var result bool
		var present bool
		team := func(object map[string]webhookJSONValue) error {
			id, ok := object["id"]
			if !ok {
				return nil
			}
			return r.decoded(ctx, id, func(value any) error {
				text, _ := value.(string)
				present, result = text != "", text != "" && slices.Contains(teams, text)
				return nil
			})
		}
		if unwrapped.node.typ != nil && (unwrapped.node.typ.Kind() == reflect.Struct || unwrapped.node.typ.Kind() == reflect.Map) && !webhookJSONCodec(unwrapped.node.typ) {
			err = r.object(ctx, unwrapped, team)
		} else {
			err = r.decoded(ctx, unwrapped, func(value any) error {
				if object, ok := value.(map[string]any); ok {
					text, _ := object["id"].(string)
					present, result = text != "", text != "" && slices.Contains(teams, text)
				}
				return nil
			})
		}
		if err != nil || present {
			return result, err
		}
	}
	if value, ok := object["teamIds"]; ok {
		unwrapped, err := r.unwrap(ctx, value)
		if err != nil {
			return false, err
		}
		if unwrapped.node.typ != nil && (unwrapped.node.typ.Kind() == reflect.Slice || unwrapped.node.typ.Kind() == reflect.Array) && unwrapped.node.typ.Elem().Kind() != reflect.Uint8 {
			var allowed bool
			err := unwrapped.node.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error {
				matches, err := match(webhookJSONValue{node: child})
				allowed = allowed || matches
				return err
			})
			return allowed, err
		}
		var allowed bool
		err = r.decoded(ctx, unwrapped, func(value any) error {
			if list, ok := value.([]any); ok {
				for _, item := range list {
					text, _ := item.(string)
					allowed = allowed || text != "" && slices.Contains(teams, text)
				}
			}
			return nil
		})
		return allowed, err
	}
	return false, nil
}
