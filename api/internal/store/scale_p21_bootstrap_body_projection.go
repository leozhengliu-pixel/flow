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
}

type bootstrapBodyPathPart struct {
	field string
	index int
	row   bool
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
		} else if part.row || part.field != pattern[index] {
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
	return p.matches([]string{"Trash", "*", "Payload"}, true)
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
	work         uint64
	bound        bool
	closed       bool
	once         sync.Once
}

func (p *bootstrapBodyProjection) Close() {
	if p != nil {
		p.once.Do(func() {
			p.closed = true
			p.Data = domain.Bootstrap{}
			if p.output != nil {
				p.output.Release()
			}
			p.owner.Close()
			p.input, p.output = nil, nil
			p.replacements, p.origins, p.nativeMarker, p.ranges = nil, nil, nil, nil
			p.rawOrigins, p.rawPrefix = nil, ""
			p.owner, p.prefix, p.work = nil, "", 0
		})
	}
}

func (p *bootstrapBodyProjection) reserve(ctx context.Context, cost uint64) error {
	if cost > metadataWorkBytes-p.work {
		return ErrMetadataWorkBudget
	}
	if err := p.owner.reserve(ctx, cost); err != nil {
		return err
	}
	p.work += cost
	return nil
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
		if node.cost > (metadataWorkBytes-512)/8 {
			return ErrMetadataWorkBudget
		}
		return bootstrapBodyAddWork(total, node.cost*8+512)
	}
	if err := bootstrapBodyAddWork(total, uint64(node.typ.Size())*8+512); err != nil {
		return err
	}
	return node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		next := path
		if node.typ.Kind() == reflect.Struct {
			next = path.field(key.String())
		} else {
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
		return node.Value(ctx, int(node.cost))
	}
	result := reflect.New(node.typ).Elem()
	switch node.typ.Kind() {
	case reflect.Struct:
		err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			value, err := p.project(ctx, child, path.field(key.String()))
			if err != nil {
				return err
			}
			target := result.FieldByName(key.String())
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
	default:
		return reflect.Value{}, ErrBoundedMetadataCoverage
	}
}

func (s *SQLiteStore) beginBootstrapBodyProjection(ctx context.Context, source *metadataLogicalSource, omittedRootFields ...string) (_ *bootstrapBodyProjection, err error) {
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
		rawOrigins: map[string]*bootstrapBodyOrigin{},
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
	for _, name := range omittedRootFields {
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
		for index := range value.NumField() {
			field := value.Type().Field(index)
			if field.PkgPath == "" && field.Tag.Get("json") != "-" {
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
			next.dead = true
			return p.bindNode(ctx, item, child, next, depth+1)
		})
	}
	return node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		var item reflect.Value
		next := path
		switch value.Kind() {
		case reflect.Struct:
			field, ok := value.Type().FieldByName(key.String())
			if !ok {
				return ErrMetadataRecordQuery
			}
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				return nil
			}
			item, next = value.FieldByIndex(field.Index), path.field(field.Name)
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
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(projected), 0)
	if err != nil {
		return nil, nil, err
	}
	if cost > (metadataWorkBytes-(512<<10))/8 {
		return nil, nil, ErrMetadataWorkBudget
	}
	if err = p.reserve(ctx, cost*8+(512<<10)); err != nil {
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
