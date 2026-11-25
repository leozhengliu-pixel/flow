package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"flow/api/internal/domain"
)

const bootstrapBodyMarkerPrefix = "\x00flow-p21-bootstrap-body:"
const bootstrapBodyRawMarkerPrefix = "\\u0000flow-p21-bootstrap-raw:"

var bootstrapBodyPaths = [][]string{
	{"AgentSessions", "*", "Messages", "*", "Content"},
	{"AgentSessions", "*", "Messages", "*", "Parts", "*", "Text"},
	{"AgentSessions", "*", "Messages", "*", "Parts", "*", "ToolCall", "Arguments"},
	{"AgentSessions", "*", "Messages", "*", "Parts", "*", "ToolCall", "Result"},
	{"AgentSessions", "*", "Messages", "*", "Parts", "*", "ToolCall", "Error"},
	{"Projects", "*", "Description"},
	{"Projects", "*", "Comments", "*", "Body"},
	{"Projects", "*", "Comments", "*", "BodyData"},
	{"Projects", "*", "DescriptionRevisions", "*", "Description"},
	{"Documents", "*", "Content"},
	{"Documents", "*", "ContentState"},
	{"Documents", "*", "ContentData"},
	{"Documents", "*", "Revisions", "*", "Content"},
	{"Documents", "*", "Revisions", "*", "ContentState"},
	{"Documents", "*", "Revisions", "*", "ContentData"},
	{"Initiatives", "*", "Description"},
	{"Initiatives", "*", "Comments", "*", "Body"},
	{"Initiatives", "*", "Comments", "*", "BodyData"},
	{"Initiatives", "*", "DescriptionHistory", "*", "Description"},
	{"Loops", "*", "Instructions"},
	{"Loops", "*", "Description"},
	{"Loops", "*", "InstructionsData"},
	{"Trash", "*", "Payload"},
	{"ProjectUpdates", "{}", "*", "Body"},
	{"ProjectUpdates", "{}", "*", "BodyData"},
	{"ProjectUpdates", "{}", "*", "Comments", "*", "Body"},
	{"ProjectUpdates", "{}", "*", "Comments", "*", "BodyData"},
	{"InitiativeUpdates", "{}", "*", "Body"},
	{"InitiativeUpdates", "{}", "*", "BodyData"},
	{"InitiativeUpdates", "{}", "*", "Comments", "*", "Body"},
	{"InitiativeUpdates", "{}", "*", "Comments", "*", "BodyData"},
}

type bootstrapBodyPathPart struct {
	field string
	index int
	row   bool
	entry bool
}

type bootstrapBodyPath struct {
	parts [8]bootstrapBodyPathPart
	count int
	dead  bool
}

func (p bootstrapBodyPath) field(name string) bootstrapBodyPath {
	if p.count == len(p.parts) {
		p.dead = true
		return p
	}
	p.parts[p.count] = bootstrapBodyPathPart{field: name}
	p.count++
	return p
}

func (p bootstrapBodyPath) row(index int) bootstrapBodyPath {
	if p.count == len(p.parts) {
		p.dead = true
		return p
	}
	p.parts[p.count] = bootstrapBodyPathPart{row: true, index: index}
	p.count++
	return p
}

func (p bootstrapBodyPath) entry(index int) bootstrapBodyPath {
	if p.count == len(p.parts) {
		p.dead = true
		return p
	}
	p.parts[p.count] = bootstrapBodyPathPart{entry: true, index: index}
	p.count++
	return p
}

func (p bootstrapBodyPath) matches(pattern []string, complete bool) bool {
	if p.dead || p.count > len(pattern) || complete && p.count != len(pattern) {
		return false
	}
	for index := range p.count {
		part := p.parts[index]
		if pattern[index] == "*" {
			if !part.row {
				return false
			}
		} else if pattern[index] == "{}" {
			if !part.entry {
				return false
			}
		} else if part.row || part.entry || part.field != pattern[index] {
			return false
		}
	}
	return true
}

func (p bootstrapBodyPath) opaque() bool {
	for _, pattern := range bootstrapBodyPaths {
		if p.matches(pattern, true) {
			return true
		}
	}
	return false
}

func (p bootstrapBodyPath) raw() bool {
	return p.matches([]string{"Trash", "*", "Payload"}, true) ||
		p.matches([]string{"AgentSessions", "*", "Messages", "*", "Parts", "*", "ToolCall", "Arguments"}, true) ||
		p.matches([]string{"AgentSessions", "*", "Messages", "*", "Parts", "*", "ToolCall", "Result"}, true)
}

func (p bootstrapBodyPath) containsOpaque() bool {
	for _, pattern := range bootstrapBodyPaths {
		if p.matches(pattern, false) {
			return true
		}
	}
	return false
}

func (p bootstrapBodyPath) text() string {
	var text strings.Builder
	for index := range p.count {
		part := p.parts[index]
		if part.row {
			text.WriteByte('[')
			text.WriteString(strconv.Itoa(part.index))
			text.WriteByte(']')
		} else if part.entry {
			text.WriteByte('{')
			text.WriteString(strconv.Itoa(part.index))
			text.WriteByte('}')
		} else {
			if index != 0 {
				text.WriteByte('.')
			}
			text.WriteString(part.field)
		}
	}
	return text.String()
}

type bootstrapBodyIdentity struct {
	pointer *byte
	length  int
}

func bootstrapBodyStringIdentity(value string) bootstrapBodyIdentity {
	return bootstrapBodyIdentity{pointer: unsafe.StringData(value), length: len(value)}
}

type bootstrapBodyOrigin struct {
	token string
	path  string
	node  metadataLogicalNode
	start uintptr
	end   uintptr
}

// bootstrapBodyProjection owns the input retain, typed header graph, marker
// provenance, bound output source and all work. The input caller keeps its own
// source reference. bind's returned source/replacements are borrowed until Close.
type bootstrapBodyProjection struct {
	Data         domain.Bootstrap
	input        *metadataLogicalSource
	output       *metadataLogicalSource
	replacements map[int64]metadataLogicalNode
	origins      map[bootstrapBodyIdentity]*bootstrapBodyOrigin
	rawOrigins   map[string]*bootstrapBodyOrigin
	ranges       []*bootstrapBodyOrigin
	nativeMarker map[bootstrapBodyIdentity]string
	prefix       string
	rawPrefix    string
	owner        *ResourceRead
	// leases hold work, the projection's admission; committed is the part
	// of it the projection's headers retain: the structural and body
	// occurrence work admitted up front plus the native bytes of every
	// decoded header subtree (decode). bind exchanges it for the output's.
	leases               []func()
	work                 uint64
	committed            uint64
	bound                bool
	stripUpdateSnapshots bool
	closed               bool
	once                 sync.Once
}

type bootstrapBodyProjectionOptions struct {
	OmittedRootFields    []string
	StripUpdateSnapshots bool
}

func (p *bootstrapBodyProjection) omits(path bootstrapBodyPath) bool {
	return p.stripUpdateSnapshots &&
		(path.matches([]string{"ProjectUpdates", "{}", "*", "Snapshot"}, true) ||
			path.matches([]string{"InitiativeUpdates", "{}", "*", "Snapshot"}, true))
}

func (p *bootstrapBodyProjection) Close() {
	if p != nil {
		p.once.Do(func() {
			p.closed = true
			p.Data = domain.Bootstrap{}
			if p.output != nil {
				p.output.Release()
			}
			p.releaseLeases()
			p.owner.Close()
			p.input, p.output = nil, nil
			p.replacements, p.origins, p.nativeMarker, p.ranges = nil, nil, nil, nil
			p.rawOrigins, p.rawPrefix = nil, ""
			p.owner, p.prefix, p.work, p.committed = nil, "", 0, 0
		})
	}
}

func (p *bootstrapBodyProjection) reserve(ctx context.Context, cost uint64) error {
	if cost > metadataWorkBytes-p.work {
		return ErrMetadataWorkBudget
	}
	_, release, err := p.owner.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		return err
	}
	p.leases = append(p.leases, release)
	p.work += cost
	return nil
}

func (p *bootstrapBodyProjection) releaseLeases() {
	for _, release := range p.leases {
		release()
	}
	p.leases, p.work, p.committed = nil, 0, 0
}

// bootstrapBodyAdmissionStep is the least a decode grows the admission by, so
// a projection of many small subtrees admits work a few times, not per row.
const bootstrapBodyAdmissionStep = 1 << 20

// decode materializes one header subtree. While it decodes, the admission
// covers what earlier subtrees retain plus this subtree's frame cost (the
// bound Value admits); afterwards only the native bytes it retains stay
// committed. The admission only grows, so the bytes allocated never exceed
// it. Charging the frame cost of every subtree for the whole read instead
// overstates the headers many times over (a frame charges each scalar field
// as its own node): a 5,000-issue workspace's project progress histories
// alone came to more than the whole process work budget, so every bootstrap
// of it was refused although its headers take tens of MiB.
func (p *bootstrapBodyProjection) decode(ctx context.Context, node metadataLogicalNode) (reflect.Value, error) {
	if node.cost > metadataWorkBytes-p.committed {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := p.admitCommitted(ctx, p.committed+node.cost); err != nil {
		return reflect.Value{}, err
	}
	value, err := node.Value(ctx, int(node.cost))
	if err != nil {
		return value, err
	}
	retained, err := bootstrapNativeBytes(ctx, value, 0)
	if err != nil {
		return reflect.Value{}, err
	}
	if retained > metadataWorkBytes-p.committed {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	p.committed += retained
	return value, p.admitCommitted(ctx, p.committed)
}

// admitCommitted grows the admission to at least need.
func (p *bootstrapBodyProjection) admitCommitted(ctx context.Context, need uint64) error {
	if need <= p.work {
		return nil
	}
	if need > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	return p.reserve(ctx, min(max(need-p.work, bootstrapBodyAdmissionStep), metadataWorkBytes-p.work))
}

// bootstrapNativeBytes bounds the heap a decoded value retains beyond its own
// inline size: string and byte data, slice backing arrays, pointer and boxed
// interface targets, and map tables. Every allocation is rounded up for the
// allocator's size classes and a map is charged whole groups of eight slots,
// so the bound holds for the small maps and short strings headers consist
// of. Shared targets are charged at every reference. A time.Time charges
// nothing: decoded and projected times use the process's shared locations.
func bootstrapNativeBytes(ctx context.Context, value reflect.Value, depth int) (uint64, error) {
	if depth >= metadataLogicalDepth {
		return 0, ErrMetadataWorkBudget
	}
	if !value.IsValid() || bootstrapNativeFlat(value.Type()) {
		return 0, nil
	}
	total := uint64(0)
	add := func(part uint64) error {
		if part > metadataWorkBytes || total > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		total += part
		return nil
	}
	child := func(item reflect.Value) error {
		part, err := bootstrapNativeBytes(ctx, item, depth+1)
		if err == nil {
			err = add(part)
		}
		return err
	}
	switch value.Kind() {
	case reflect.String:
		return bootstrapNativeAllocation(uint64(value.Len())), nil
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return 0, nil
		}
		target := value.Elem()
		if value.Kind() == reflect.Pointer || !bootstrapNativePointerShaped(target.Type()) {
			if err := add(bootstrapNativeAllocation(uint64(target.Type().Size()))); err != nil {
				return 0, err
			}
		}
		return total, child(target)
	case reflect.Slice:
		if value.IsNil() {
			return 0, nil
		}
		element := value.Type().Elem()
		if uint64(value.Cap()) > metadataWorkBytes/max(uint64(element.Size()), 1) {
			return 0, ErrMetadataWorkBudget
		}
		if err := add(bootstrapNativeAllocation(uint64(value.Cap()) * uint64(element.Size()))); err != nil {
			return 0, err
		}
		if bootstrapNativeFlat(element) {
			return total, nil
		}
		for index := range value.Len() {
			if index%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
			}
			if err := child(value.Index(index)); err != nil {
				return 0, err
			}
		}
		return total, nil
	case reflect.Array:
		for index := range value.Len() {
			if err := child(value.Index(index)); err != nil {
				return 0, err
			}
		}
		return total, nil
	case reflect.Map:
		if value.IsNil() {
			return 0, nil
		}
		// Swiss tables: one group of eight slots up to eight entries, then a
		// power-of-two slot count at a 7/8 load factor; a control byte per
		// slot, table and directory headers.
		slots := uint64(8)
		for count := uint64(value.Len()); slots*7 < count*8; {
			slots *= 2
		}
		slot := uint64(value.Type().Key().Size()+value.Type().Elem().Size()) + 1
		if slots > metadataWorkBytes/slot {
			return 0, ErrMetadataWorkBudget
		}
		if err := add(bootstrapNativeAllocation(slots*slot) + 128 + (slots/1024+1)*64); err != nil {
			return 0, err
		}
		if bootstrapNativeFlat(value.Type().Key()) && bootstrapNativeFlat(value.Type().Elem()) {
			return total, nil
		}
		iter := value.MapRange()
		for iter.Next() {
			if err := child(iter.Key()); err != nil {
				return 0, err
			}
			if err := child(iter.Value()); err != nil {
				return 0, err
			}
		}
		return total, ctx.Err()
	case reflect.Struct:
		for index := range value.NumField() {
			if err := child(value.Field(index)); err != nil {
				return 0, err
			}
		}
		return total, nil
	}
	return 0, nil
}

// bootstrapNativeAllocation rounds an allocation up for size classes (at
// most an eighth wasted above the 16-byte classes).
func bootstrapNativeAllocation(size uint64) uint64 {
	if size == 0 {
		return 0
	}
	return size + size/8 + 16
}

// bootstrapNativePointerShaped reports whether an interface holds a value of
// the type without boxing it.
func bootstrapNativePointerShaped(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	}
	return false
}

var bootstrapNativeFlatTypes sync.Map

// bootstrapNativeFlat reports whether values of t own no heap memory.
func bootstrapNativeFlat(t reflect.Type) bool {
	if t == reflect.TypeFor[time.Time]() {
		return true
	}
	if flat, found := bootstrapNativeFlatTypes.Load(t); found {
		return flat.(bool)
	}
	flat := true
	switch t.Kind() {
	case reflect.String, reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		flat = false
	case reflect.Array:
		flat = t.Len() == 0 || bootstrapNativeFlat(t.Elem())
	case reflect.Struct:
		for index := range t.NumField() {
			if !bootstrapNativeFlat(t.Field(index).Type) {
				flat = false
				break
			}
		}
	}
	bootstrapNativeFlatTypes.Store(t, flat)
	return flat
}

func bootstrapBodyAddWork(total *uint64, part uint64) error {
	if part > metadataWorkBytes || *total > metadataWorkBytes-part {
		return ErrMetadataWorkBudget
	}
	*total += part
	return nil
}

func (p *bootstrapBodyProjection) projectedWork(ctx context.Context, node metadataLogicalNode, path bootstrapBodyPath, total *uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node.typ == nil {
		return ErrBoundedMetadataCoverage
	}
	if p.omits(path) {
		return bootstrapBodyAddWork(total, uint64(node.typ.Size())*8+512)
	}
	if path.opaque() {
		if path.raw() {
			if node.typ != reflect.TypeFor[json.RawMessage]() {
				return ErrBoundedMetadataCoverage
			}
			return bootstrapBodyAddWork(total, 2048)
		}
		if node.typ.Kind() != reflect.String && node.typ != reflect.TypeFor[map[string]any]() {
			return ErrBoundedMetadataCoverage
		}
		return bootstrapBodyAddWork(total, 2048)
	}
	if !path.containsOpaque() {
		// A header subtree is admitted where it is decoded (decode): its
		// frame cost while decoding, then the native bytes it retains. Its
		// inline bytes are its parent's.
		if node.cost > metadataWorkBytes {
			return ErrMetadataWorkBudget
		}
		return nil
	}
	// A node on the way to a body is built in place: a value allocated for
	// it, copied into its parent's inline bytes (a slice's backing array is
	// the sum of its rows'), and a small fixed allowance.
	if err := bootstrapBodyAddWork(total, 2*bootstrapNativeAllocation(uint64(node.typ.Size()))+128); err != nil {
		return err
	}
	if node.typ.Kind() == reflect.Map {
		if node.typ.Key().Kind() != reflect.String {
			return ErrBoundedMetadataCoverage
		}
		ordinal := 0
		return node.MapEntries(ctx, func(key, child metadataLogicalNode) error {
			if key.cost > (metadataWorkBytes-512)/8 {
				return ErrMetadataWorkBudget
			}
			if err := bootstrapBodyAddWork(total, key.cost*8+512); err != nil {
				return err
			}
			next := path.entry(ordinal)
			ordinal++
			return p.projectedWork(ctx, child, next, total)
		})
	}
	return node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		next := path
		switch node.typ.Kind() {
		case reflect.Struct:
			next = path.field(key.String())
		case reflect.Slice, reflect.Array:
			next = path.row(int(key.Int()))
		}
		return p.projectedWork(ctx, child, next, total)
	})
}

func (p *bootstrapBodyProjection) token(node metadataLogicalNode, path bootstrapBodyPath) string {
	value := strings.Clone(p.prefix + strconv.Itoa(len(p.origins)))
	identity := bootstrapBodyStringIdentity(value)
	start := uintptr(unsafe.Pointer(identity.pointer))
	origin := &bootstrapBodyOrigin{token: value, path: path.text(), node: node, start: start, end: start + uintptr(len(value))}
	p.origins[identity] = origin
	p.ranges = append(p.ranges, origin)
	return value
}

func (p *bootstrapBodyProjection) rawToken(node metadataLogicalNode, path bootstrapBodyPath) json.RawMessage {
	token := `"` + p.rawPrefix + strconv.Itoa(len(p.rawOrigins)) + `"`
	p.rawOrigins[token] = &bootstrapBodyOrigin{token: token, path: path.text(), node: node}
	return json.RawMessage(token)
}

func bootstrapBodyCollectionSize(ctx context.Context, node metadataLogicalNode) (int, bool, error) {
	if node.generic {
		return node.genericCollectionSize(ctx)
	}
	cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
	present, err := cursor.byte()
	if err != nil || present == 0 {
		return 0, false, err
	}
	count, err := cursor.uint()
	if err != nil || count > node.cost || count > uint64(^uint(0)>>1) {
		return 0, false, errors.Join(err, ErrMetadataRecordQuery)
	}
	return int(count), true, nil
}

func (p *bootstrapBodyProjection) project(ctx context.Context, node metadataLogicalNode, path bootstrapBodyPath) (reflect.Value, error) {
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, err
	}
	if p.omits(path) {
		return reflect.Zero(node.typ), nil
	}
	if path.opaque() {
		if node.typ.Kind() == reflect.String {
			empty, err := node.StringEqual(ctx, "")
			if err != nil {
				return reflect.Value{}, err
			}
			if empty {
				return reflect.Zero(node.typ), nil
			}
			return reflect.ValueOf(p.token(node, path)), nil
		}
		size, present, err := bootstrapBodyCollectionSize(ctx, node)
		if err != nil {
			return reflect.Value{}, err
		}
		if !present {
			return reflect.Zero(node.typ), nil
		}
		if size == 0 {
			if path.raw() {
				return reflect.MakeSlice(node.typ, 0, 0), nil
			}
			return reflect.MakeMap(node.typ), nil
		}
		if path.raw() {
			return reflect.ValueOf(p.rawToken(node, path)), nil
		}
		token := p.token(node, path)
		return reflect.ValueOf(map[string]any{token: token}), nil
	}
	if !path.containsOpaque() {
		return p.decode(ctx, node)
	}
	result := reflect.New(node.typ).Elem()
	switch node.typ.Kind() {
	case reflect.Pointer, reflect.Interface:
		err := node.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error {
			value, err := p.project(ctx, child, path)
			if err != nil {
				return err
			}
			if node.typ.Kind() == reflect.Pointer {
				if value.Type() != node.typ.Elem() {
					return ErrMetadataRecordQuery
				}
				result.Set(reflect.New(node.typ.Elem()))
				result.Elem().Set(value)
			} else {
				if !value.Type().AssignableTo(node.typ) {
					return ErrMetadataRecordQuery
				}
				result.Set(value)
			}
			return nil
		})
		return result, err
	case reflect.Struct:
		// A struct frame holds its fields in declaration order.
		fields, ordinal := metadataStructFields(node.typ), 0
		err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			index := ordinal
			ordinal++
			if index >= len(fields) || fields[index].name != key.String() {
				return ErrMetadataRecordQuery
			}
			value, err := p.project(ctx, child, path.field(fields[index].name))
			if err != nil {
				return err
			}
			target := result.Field(index)
			if !target.CanSet() || target.Type() != value.Type() {
				return ErrMetadataRecordQuery
			}
			target.Set(value)
			return nil
		})
		return result, err
	case reflect.Slice:
		count, present, err := bootstrapBodyCollectionSize(ctx, node)
		if err != nil || !present {
			return result, err
		}
		result.Set(reflect.MakeSlice(node.typ, count, count))
		err = node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			index := int(key.Int())
			value, err := p.project(ctx, child, path.row(index))
			if err == nil {
				if index < 0 || index >= result.Len() || result.Index(index).Type() != value.Type() {
					return ErrMetadataRecordQuery
				}
				result.Index(index).Set(value)
			}
			return err
		})
		return result, err
	case reflect.Map:
		if node.typ.Key().Kind() != reflect.String {
			return reflect.Value{}, ErrBoundedMetadataCoverage
		}
		count, present, err := bootstrapBodyCollectionSize(ctx, node)
		if err != nil || !present {
			return result, err
		}
		result.Set(reflect.MakeMapWithSize(node.typ, count))
		ordinal := 0
		err = node.MapEntries(ctx, func(key, child metadataLogicalNode) error {
			native, err := key.Value(ctx, int(key.cost))
			if err != nil {
				return err
			}
			value, err := p.project(ctx, child, path.entry(ordinal))
			ordinal++
			if err != nil {
				return err
			}
			result.SetMapIndex(native, value)
			return nil
		})
		return result, err
	default:
		return reflect.Value{}, ErrBoundedMetadataCoverage
	}
}

func (s *SQLiteStore) beginBootstrapBodyProjection(ctx context.Context, source *metadataLogicalSource, omittedRootFields ...string) (_ *bootstrapBodyProjection, err error) {
	return s.beginBootstrapBodyProjectionWithOptions(ctx, source, bootstrapBodyProjectionOptions{OmittedRootFields: omittedRootFields})
}

func (s *SQLiteStore) beginBootstrapBodyProjectionWithOptions(ctx context.Context, source *metadataLogicalSource, options bootstrapBodyProjectionOptions) (_ *bootstrapBodyProjection, err error) {
	if !s.BoundedMetadataEnabled() || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	p := &bootstrapBodyProjection{
		input: source, owner: &ResourceRead{store: s, source: source},
		origins: map[bootstrapBodyIdentity]*bootstrapBodyOrigin{}, nativeMarker: map[bootstrapBodyIdentity]string{},
		rawOrigins:           map[string]*bootstrapBodyOrigin{},
		stripUpdateSnapshots: options.StripUpdateSnapshots,
	}
	defer func() {
		if err != nil {
			if canceled := ctx.Err(); canceled != nil {
				err = canceled
			}
			p.Close()
		}
	}()
	ctx = metadataLogicalSourceScratchContext(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), source)
	if err = p.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	p.prefix = bootstrapBodyMarkerPrefix + hex.EncodeToString(nonce[:16]) + ":"
	p.rawPrefix = bootstrapBodyRawMarkerPrefix + hex.EncodeToString(nonce[16:]) + ":"
	target := reflect.ValueOf(&p.Data).Elem()
	if len(source.fields) != target.NumField() {
		return nil, ErrBoundedMetadataCoverage
	}
	omitted := make([]bool, target.NumField())
	for _, name := range options.OmittedRootFields {
		matched := false
		for index := range target.NumField() {
			field := target.Type().Field(index)
			if name == field.Name || name == strings.Split(field.Tag.Get("json"), ",")[0] {
				omitted[index], matched = true, true
				break
			}
		}
		if !matched {
			return nil, ErrMetadataRecordQuery
		}
	}
	work := uint64(0)
	for index := range target.NumField() {
		field := target.Type().Field(index)
		if omitted[index] || field.Tag.Get("json") == "-" && field.Name != "NextIssueNumber" {
			continue
		}
		if source.fields[index].typ != field.Type {
			return nil, ErrBoundedMetadataCoverage
		}
		if err = p.projectedWork(ctx, source.fields[index], bootstrapBodyPath{}.field(field.Name), &work); err != nil {
			return nil, err
		}
	}
	if err = p.reserve(ctx, work); err != nil {
		return nil, err
	}
	p.committed = p.work
	for index := range target.NumField() {
		field := target.Type().Field(index)
		if omitted[index] || field.Tag.Get("json") == "-" && field.Name != "NextIssueNumber" {
			continue
		}
		value, loadErr := p.project(ctx, source.fields[index], bootstrapBodyPath{}.field(field.Name))
		if loadErr != nil {
			return nil, loadErr
		}
		target.Field(index).Set(value)
	}
	slices.SortFunc(p.ranges, func(a, b *bootstrapBodyOrigin) int {
		if a.start < b.start {
			return -1
		}
		if a.start > b.start {
			return 1
		}
		return 0
	})
	if err = p.findNativeMarkers(ctx, reflect.ValueOf(p.Data), 0); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *bootstrapBodyProjection) originalString(value string) (metadataLogicalNode, bool) {
	if p == nil || p.closed || value == "" {
		return metadataLogicalNode{}, false
	}
	origin := p.origins[bootstrapBodyStringIdentity(value)]
	if origin == nil || origin.token != value || origin.node.typ.Kind() != reflect.String {
		return metadataLogicalNode{}, false
	}
	return origin.node, true
}

func bootstrapBodyMarkerText(value string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if prefix != "" && strings.Contains(value, prefix) {
			return true
		}
	}
	return false
}

func bootstrapBodyMarkerBytes(ctx context.Context, value reflect.Value, prefixes ...string) (bool, error) {
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		for start := 0; start+len(prefix) <= value.Len(); start++ {
			if start%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return false, err
				}
			}
			matches := true
			for index := range len(prefix) {
				if value.Index(start+index).Uint() != uint64(prefix[index]) {
					matches = false
					break
				}
			}
			if matches {
				return true, nil
			}
		}
	}
	return false, nil
}

func (p *bootstrapBodyProjection) tokenAlias(value string) bool {
	if value == "" {
		return false
	}
	ptr := uintptr(unsafe.Pointer(unsafe.StringData(value)))
	index := sortBootstrapBodyRange(p.ranges, ptr)
	return index >= 0 && ptr < p.ranges[index].end
}

func sortBootstrapBodyRange(ranges []*bootstrapBodyOrigin, pointer uintptr) int {
	start, end := 0, len(ranges)
	for start < end {
		mid := start + (end-start)/2
		if ranges[mid].start <= pointer {
			start = mid + 1
		} else {
			end = mid
		}
	}
	return start - 1
}

func (p *bootstrapBodyProjection) findNativeMarkers(ctx context.Context, value reflect.Value, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth >= metadataLogicalDepth {
		return ErrMetadataWorkBudget
	}
	if value.Kind() == reflect.String {
		text := value.String()
		if bootstrapBodyMarkerText(text, p.prefix, p.rawPrefix) && !p.tokenAlias(text) {
			p.nativeMarker[bootstrapBodyStringIdentity(text)] = text
		}
		return nil
	}
	return bootstrapBodyVisitValue(value, func(child reflect.Value) error {
		return p.findNativeMarkers(ctx, child, depth+1)
	})
}

func bootstrapBodyVisitValue(value reflect.Value, visit func(reflect.Value) error) error {
	if value.Type() == reflect.TypeFor[time.Time]() {
		return nil
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			return visit(value.Elem())
		}
	case reflect.Struct:
		for index, field := range metadataStructFields(value.Type()) {
			if field.exported && !field.jsonOmitted {
				if err := visit(value.Field(index)); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if err := visit(iter.Key()); err != nil {
				return err
			}
			if err := visit(iter.Value()); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() != reflect.Uint8 {
			for index := range value.Len() {
				if err := visit(value.Index(index)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (p *bootstrapBodyProjection) checkString(value string) error {
	if p.tokenAlias(value) {
		return ErrBoundedMetadataCoverage
	}
	if bootstrapBodyMarkerText(value, p.prefix, p.rawPrefix) && p.nativeMarker[bootstrapBodyStringIdentity(value)] != value {
		return ErrBoundedMetadataCoverage
	}
	return nil
}

func (p *bootstrapBodyProjection) checkOrdinary(ctx context.Context, value reflect.Value, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth >= metadataLogicalDepth {
		return ErrMetadataWorkBudget
	}
	if value.Kind() == reflect.String {
		return p.checkString(value.String())
	}
	if (value.Kind() == reflect.Slice || value.Kind() == reflect.Array) && value.Type().Elem().Kind() == reflect.Uint8 {
		marker, err := bootstrapBodyMarkerBytes(ctx, value, p.prefix, p.rawPrefix)
		if err != nil {
			return err
		}
		if marker {
			return ErrBoundedMetadataCoverage
		}
		return nil
	}
	return bootstrapBodyVisitValue(value, func(child reflect.Value) error {
		return p.checkOrdinary(ctx, child, depth+1)
	})
}

func (p *bootstrapBodyProjection) bindNode(ctx context.Context, value reflect.Value, node metadataLogicalNode, path bootstrapBodyPath, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth >= metadataLogicalDepth {
		return ErrMetadataWorkBudget
	}
	if value.Type() != node.typ {
		return ErrMetadataRecordQuery
	}
	// The captured frames are read field by field: one read of a node of at
	// most a window serves it and the nodes nested in it.
	node = node.windowed()
	if value.Type() == reflect.TypeFor[json.RawMessage]() && value.Len() <= 256 {
		if origin := p.rawOrigins[string(value.Bytes())]; origin != nil {
			if !path.raw() || origin.node.typ != node.typ {
				return ErrBoundedMetadataCoverage
			}
			p.replacements[node.offset] = origin.node
			return nil
		}
	}
	if value.Kind() == reflect.String {
		text := value.String()
		if origin := p.origins[bootstrapBodyStringIdentity(text)]; origin != nil && origin.token == text {
			if !path.opaque() || origin.node.typ != node.typ {
				return ErrBoundedMetadataCoverage
			}
			p.replacements[node.offset] = origin.node
			return nil
		}
		return p.checkString(text)
	}
	if value.Type() == reflect.TypeFor[map[string]any]() && value.Len() == 1 {
		iter := value.MapRange()
		iter.Next()
		key, item := iter.Key().String(), iter.Value()
		if item.Kind() == reflect.Interface && !item.IsNil() {
			item = item.Elem()
		}
		origin := p.origins[bootstrapBodyStringIdentity(key)]
		if origin != nil && item.Kind() == reflect.String && bootstrapBodyStringIdentity(item.String()) == bootstrapBodyStringIdentity(key) &&
			key == origin.token && item.String() == key {
			if !path.opaque() || origin.node.typ != node.typ {
				return ErrBoundedMetadataCoverage
			}
			p.replacements[node.offset] = origin.node
			return nil
		}
	}
	if value.Type() == reflect.TypeFor[time.Time]() {
		return nil
	}
	if (value.Kind() == reflect.Slice || value.Kind() == reflect.Array) && value.Type().Elem().Kind() == reflect.Uint8 {
		marker, err := bootstrapBodyMarkerBytes(ctx, value, p.prefix, p.rawPrefix)
		if err != nil {
			return err
		}
		if marker {
			return ErrBoundedMetadataCoverage
		}
		return nil
	}
	if value.Kind() == reflect.Map {
		// Check the native keys once, before capture's key decoding loses their
		// string backing identity. Framed key lookups then remain linear.
		iter := value.MapRange()
		for iter.Next() {
			if err := p.checkOrdinary(ctx, iter.Key(), depth+1); err != nil {
				return err
			}
		}
		ordinal := 0
		return node.MapEntries(ctx, func(key, child metadataLogicalNode) error {
			native, err := key.Value(ctx, int(key.cost))
			if err != nil {
				return err
			}
			item := value.MapIndex(native)
			if !item.IsValid() {
				return ErrBoundedMetadataCoverage
			}
			next := path
			if path.containsOpaque() {
				next = path.entry(ordinal)
			} else {
				next.dead = true
			}
			ordinal++
			return p.bindNode(ctx, item, child, next, depth+1)
		})
	}
	var fields []metadataStructField
	if value.Kind() == reflect.Struct {
		fields = metadataStructFields(value.Type())
	}
	ordinal := 0
	return node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		var item reflect.Value
		next := path
		switch value.Kind() {
		case reflect.Struct:
			// A struct frame holds its fields in declaration order.
			index := ordinal
			ordinal++
			if index >= len(fields) || fields[index].name != key.String() {
				return ErrMetadataRecordQuery
			}
			if !fields[index].exported || fields[index].jsonOmitted {
				return nil
			}
			item, next = value.Field(index), path.field(fields[index].name)
		case reflect.Slice, reflect.Array:
			index := int(key.Int())
			item, next = value.Index(index), path.row(index)
		case reflect.Interface, reflect.Pointer:
			item = value.Elem()
		default:
			return nil
		}
		return p.bindNode(ctx, item, child, next, depth+1)
	})
}

// Re-encoding headers transfers the source's audited timestamp locations.
func (p *bootstrapBodyProjection) capture(ctx context.Context, projected domain.Bootstrap) (*metadataLogicalSource, error) {
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer builder.Close()
	if err := builder.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	for _, storage := range p.input.stores {
		if err := builder.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	value := reflect.ValueOf(projected)
	for index := range value.NumField() {
		field, item := value.Type().Field(index), value.Field(index)
		if field.Tag.Get("json") == "-" && field.Name != "NextIssueNumber" {
			item = reflect.Zero(item.Type())
		}
		var node metadataLogicalNode
		if field.Name == "Viewer" {
			node, err = builder.writeAggregateNode(legacyActorUserNode{value: item}, 0)
		} else {
			node, err = builder.writeValue(item, 0, false)
		}
		if err != nil {
			return nil, err
		}
		builder.source.fields[index] = node
	}
	return builder.Finish(p.input.Stamp())
}

// bind captures admitted projected headers once. Returned offsets belong only
// to this output source; callers borrow both results through p.Close.
func (p *bootstrapBodyProjection) bind(ctx context.Context, projected domain.Bootstrap) (_ *metadataLogicalSource, _ map[int64]metadataLogicalNode, err error) {
	if p == nil || p.closed {
		return nil, nil, errMetadataLogicalClosed
	}
	if p.bound {
		return nil, nil, ErrMetadataRecordQuery
	}
	defer func() {
		if err != nil {
			if canceled := ctx.Err(); canceled != nil {
				err = canceled
			}
			p.Close()
		}
	}()
	ctx = metadataLogicalSourceScratchContext(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), p.input)
	// From here the projection's own headers are referenced only through
	// projected (the caller's projection of them), which is charged whole,
	// shared parts included; body occurrences keep their origins and gain a
	// replacement entry each. The projection's admission is exchanged for
	// this one, so a read holds the larger of the two, not their sum. The
	// capture writes to the output spool, whose buffers and child indexes
	// have budgets of their own.
	cost, err := bootstrapNativeBytes(ctx, reflect.ValueOf(projected), 0)
	if err != nil {
		return nil, nil, err
	}
	occurrences := uint64(len(p.origins) + len(p.rawOrigins))
	if cost > metadataWorkBytes || occurrences > (metadataWorkBytes-cost)/(2048+256) {
		return nil, nil, ErrMetadataWorkBudget
	}
	cost += occurrences*(2048+256) + uint64(reflect.TypeFor[domain.Bootstrap]().Size()) + 2<<20 + 512<<10
	if cost > metadataWorkBytes {
		return nil, nil, ErrMetadataWorkBudget
	}
	p.Data = domain.Bootstrap{}
	p.releaseLeases()
	if err = p.reserve(ctx, cost); err != nil {
		return nil, nil, err
	}
	p.output, err = p.capture(ctx, projected)
	if err != nil {
		return nil, nil, err
	}
	p.replacements = map[int64]metadataLogicalNode{}
	value := reflect.ValueOf(projected)
	for index := range value.NumField() {
		field := value.Type().Field(index)
		if field.Tag.Get("json") == "-" && field.Name != "NextIssueNumber" {
			continue
		}
		if err = p.bindNode(ctx, value.Field(index), p.output.fields[index], bootstrapBodyPath{}.field(field.Name), 0); err != nil {
			return nil, nil, err
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, nil, err
	}
	p.bound = true
	return p.output, p.replacements, nil
}
