package store

import (
	"context"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

// The decoded-image cache of the COLD path (bounded metadata on).
//
// A COLD facade, a facade refresh and a legacy view each decode a complete
// workspace image from its installed logical source; on a large workspace
// that decode is most of a COLD request. The cache keeps, per workspace, the
// last decoded image of an installed generation and reuses it:
//
//   - the same generation (source) is not decoded again;
//   - another generation decodes only what changed: a field whose frame has
//     the same origin as the cached one (metadataLogicalFrameOrigin: same
//     bytes, same decode) reuses its decoded value, and the records of a large
//     collection reuse theirs the same way, so a HOT write that rewrote one
//     project decodes that project only.
//
// Cached values are never handed out. Every facade and view receives its own
// exact deep copy (coldImageCopy), so the legacy code on a facade owns its
// graph exactly as it owned a fresh decode: in-place edits, appends into
// spare capacity and sorts of a facade's slices never reach the cache or
// another request. A decode is always of the generation the caller pinned;
// the cache is keyed by that generation and by frame origins, never by time,
// so it cannot serve a stale generation.
//
// The cache is bounded by decoded bytes (FLOW_COLD_DECODE_CACHE_BYTES,
// by default 35% of the memory ceiling; 0 disables it): the least recently used images are
// dropped beyond it, an image larger than the budget is not kept, and an
// image unused for FLOW_COLD_DECODE_CACHE_IDLE (default 5m) is dropped.

const defaultColdDecodeCacheIdle = 5 * time.Minute

// coldDecodeElementMinBytes: a slice field smaller than this decodes whole
// (its records are not worth a memo).
const coldDecodeElementMinBytes = 64 << 10

func coldDecodeCacheBudget() int64 {
	if strings.TrimSpace(os.Getenv("FLOW_COLD_DECODE_CACHE_BYTES")) == "0" {
		return 0
	}
	return metadataBudgetBytes("FLOW_COLD_DECODE_CACHE_BYTES", metadataMemoryShare(35, 100))
}

func coldDecodeCacheIdle() time.Duration {
	return envPositiveDuration("FLOW_COLD_DECODE_CACHE_IDLE", defaultColdDecodeCacheIdle)
}

// coldFrameKey identifies the bytes of a frame by its origin
// (metadataLogicalFrameOrigin) and how it decodes. Holding the storage
// pointer keeps its address from being reused while the key exists.
type coldFrameKey struct {
	storage      *metadataLogicalStorage
	offset, size int64
	typ          reflect.Type
	flags        uint8
	// patches is the origin storage's patch counter when the frame was
	// decoded: a later patch of that storage's flushed bytes retires it.
	patches int64
}

func coldFrameKeyOf(node metadataLogicalNode) (coldFrameKey, bool) {
	origin := metadataLogicalFrameOrigin(node)
	if origin.source == nil {
		return coldFrameKey{}, false
	}
	patches := origin.source.patches.Load()
	if patches%2 != 0 {
		// A patch is in flight.
		return coldFrameKey{}, false
	}
	var flags uint8
	for bit, set := range []bool{origin.unavailable, origin.blocked, origin.generic, origin.genericRange} {
		if set {
			flags |= 1 << bit
		}
	}
	return coldFrameKey{storage: origin.source, offset: origin.offset, size: origin.size, typ: origin.typ, flags: flags, patches: patches}, true
}

type coldDecodedField struct {
	key   coldFrameKey
	keyed bool
	value reflect.Value
	// bytes is the heap the decoded value holds (decodedValueBytes).
	bytes int64
	// elements memoizes the records of a large slice field by frame key.
	elements map[coldFrameKey]coldDecodedElement
	ordinals *recordOrdinalIndex
	// encodingID is shared with every image that reuses this frame.
	encodingID IssueBootstrapGeneration
}

// coldDecodedElement includes its boxed record and its referenced heap.
type coldDecodedElement struct {
	value reflect.Value
	bytes int64
}

type coldDecodedImage struct {
	source *metadataLogicalSource
	fields []coldDecodedField
	data   domain.Bootstrap
	bytes  int64
	used   time.Time
	// view: the decoded size of the paged bootstrap's view of a shared
	// image (scale_p21_resident_generation.go), measured once.
	view residentViewSize
	// display: whether the writers' display refresh changes nothing in
	// the image (scale_p21_resident_document_write.go), checked once.
	display residentDisplayChecks
}

type coldDecodeCache struct {
	mu     sync.Mutex
	images map[string]*coldDecodedImage
	bytes  int64
	idle   *time.Timer
}

// COLD decode cache counters (load measurement and tests).
var (
	coldDecodeImageHits     atomic.Int64
	coldDecodeImageBuilds   atomic.Int64
	coldDecodeFieldReuses   atomic.Int64
	coldDecodeFieldDecodes  atomic.Int64
	coldDecodeElementReuses atomic.Int64
	coldDecodeElementDecode atomic.Int64
	coldDecodeEvictions     atomic.Int64
	coldDecodeCopyNanos     atomic.Int64
	coldDecodeCachedBytes   atomic.Int64
)

// ColdDecodeCacheStatsForTesting reports the decode cache counters.
func ColdDecodeCacheStatsForTesting() map[string]int64 {
	return map[string]int64{
		"imageHits": coldDecodeImageHits.Load(), "imageBuilds": coldDecodeImageBuilds.Load(),
		"fieldReuses": coldDecodeFieldReuses.Load(), "fieldDecodes": coldDecodeFieldDecodes.Load(),
		"elementReuses": coldDecodeElementReuses.Load(), "elementDecodes": coldDecodeElementDecode.Load(), "elementSeeded": coldDecodeElementSeeded.Load(),
		"evictions": coldDecodeEvictions.Load(), "cachedBytes": coldDecodeCachedBytes.Load(),
	}
}

// decodeColdImage returns an owned decode of workspace's generation source
// (only fields, plus the workspace record, when fields is set): exactly what
// decoding source field by field gives, after domain.RebuildTeamDirectory.
// The caller has source retained.
func (r *metadataResidency) decodeColdImage(ctx context.Context, workspace string, source *metadataLogicalSource, fields []string) (domain.Bootstrap, error) {
	if err := ctx.Err(); err != nil {
		return domain.Bootstrap{}, err
	}
	budget := coldDecodeCacheBudget()
	if budget == 0 {
		image, err := buildColdDecodedImage(ctx, source, nil, fields, false)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		return image.data, nil
	}
	cache := &r.coldImages
	cache.mu.Lock()
	previous := cache.images[workspace]
	if previous != nil {
		previous.used = time.Now()
	}
	cache.mu.Unlock()
	if current, err := coldImageCurrent(ctx, previous, source, fields); err != nil {
		return domain.Bootstrap{}, err
	} else if current {
		coldDecodeImageHits.Add(1)
		started := time.Now()
		data := coldImageCopy(ctx, previous.data, fields)
		coldDecodeCopyNanos.Add(int64(time.Since(started)))
		return data, ctx.Err()
	}
	var image *coldDecodedImage
	if fields == nil {
		// The whole image: built once per generation however many
		// facades and views ask for it together (residentImage).
		// One larger than the budget is not kept, but it may share values
		// with the cached image of an earlier generation: it is copied too.
		built, _, err := r.residentImageBuild(ctx, workspace, source)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		image = built
	} else {
		built, err := buildColdDecodedImage(ctx, source, previous, fields, true)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		image = built
	}
	started := time.Now()
	data := coldImageCopy(ctx, image.data, fields)
	coldDecodeCopyNanos.Add(int64(time.Since(started)))
	return data, ctx.Err()
}

// coldImageCurrent reports whether image decodes source: every (requested)
// field of source has the frame of the image's field. It compares frames, not
// the source pointer, so a source edited in place is never served from an
// earlier decode.
func coldImageCurrent(ctx context.Context, image *coldDecodedImage, source *metadataLogicalSource, fields []string) (bool, error) {
	if image == nil {
		return false, nil
	}
	current, index := true, 0
	err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		defer func() { index++ }()
		if !current || fields != nil && name != "workspace" && !slices.Contains(fields, name) {
			return nil
		}
		key, keyed := coldFrameKeyOf(node)
		if index >= len(image.fields) || !keyed || !image.fields[index].keyed || image.fields[index].key != key || !image.fields[index].value.IsValid() {
			current = false
		}
		return nil
	})
	return current && err == nil, err
}

// buildColdDecodedImage decodes source, reusing previous's field and record
// values whose frames have the same origin. memo keeps the per-record memo
// of large slice fields for the next generation.
func buildColdDecodedImage(ctx context.Context, source *metadataLogicalSource, previous *coldDecodedImage, fields []string, memo bool) (*coldDecodedImage, error) {
	ctx = coldMetadataGenericDecodeContext(ctx, map[string]*metadataLogicalSource{"": source})
	image := &coldDecodedImage{source: source, used: time.Now()}
	value := reflect.ValueOf(&image.data).Elem()
	index := 0
	err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		defer func() { index++ }()
		field := coldDecodedField{}
		image.fields = append(image.fields, field)
		if fields != nil && name != "workspace" && !slices.Contains(fields, name) {
			return nil
		}
		key, keyed := coldFrameKeyOf(node)
		var before *coldDecodedField
		if previous != nil && index < len(previous.fields) {
			before = &previous.fields[index]
		}
		if keyed && before != nil && before.keyed && before.key == key && before.value.IsValid() {
			coldDecodeFieldReuses.Add(1)
			field = *before
		} else {
			seeds, _ := ctx.Value(coldDecodeSeedsKey{}).(coldDecodeSeeds)
			decoded, elements, bytes, err := decodeColdField(ctx, node, before, memo, seeds[name])
			if err != nil {
				return err
			}
			coldDecodeFieldDecodes.Add(1)
			field = coldDecodedField{key: key, keyed: keyed, value: decoded, bytes: bytes, elements: elements}
			if memo && keyed && slices.Contains(bootstrapEncodingFields[:], name) {
				field.encodingID = IssueBootstrapGeneration(bootstrapEncodingSequence.Add(1))
			}
			if memo && decoded.Kind() == reflect.Slice && decoded.Len() > 0 {
				if _, ok := recordOrdinalField(decoded.Type().Elem()); ok {
					field.ordinals = &recordOrdinalIndex{}
				}
			}
		}
		image.bytes += field.bytes
		image.fields[index] = field
		value.Field(index).Set(field.value)
		return nil
	})
	if err != nil {
		return nil, err
	}
	domain.RebuildTeamDirectory(&image.data)
	if memo {
		for _, directory := range []any{image.data.TeamByID, image.data.TeamByKey, image.data.TeamChildren, image.data.LabelIndex} {
			image.bytes += decodedValueBytes(reflect.ValueOf(directory))
		}
		image.bytes += int64(reflect.TypeFor[coldDecodedImage]().Size()) +
			int64(bootstrapNativeAllocation(uint64(cap(image.fields))*uint64(reflect.TypeFor[coldDecodedField]().Size())))
	}
	coldDecodeImageBuilds.Add(1)
	return image, ctx.Err()
}

// decodedValueBytes is the heap a decoded value holds beyond its own inline
// size, counted as bootstrapNativeBytes counts it (allocation size classes,
// whole map groups, every reference of a shared target) but without a cap:
// the image cache charges images by it.
func decodedValueBytes(value reflect.Value) int64 {
	return int64(min(decodedValueBytesAt(value, 0), math.MaxInt64/2))
}

func decodedValueBytesAt(value reflect.Value, depth int) uint64 {
	if !value.IsValid() || depth >= metadataLogicalDepth || bootstrapNativeFlat(value.Type()) {
		return 0
	}
	total := uint64(0)
	switch value.Kind() {
	case reflect.String:
		return bootstrapNativeAllocation(uint64(value.Len()))
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return 0
		}
		target := value.Elem()
		if value.Kind() == reflect.Pointer || !bootstrapNativePointerShaped(target.Type()) {
			total += bootstrapNativeAllocation(uint64(target.Type().Size()))
		}
		return total + decodedValueBytesAt(target, depth+1)
	case reflect.Slice:
		if value.IsNil() {
			return 0
		}
		element := value.Type().Elem()
		total += bootstrapNativeAllocation(uint64(value.Cap()) * uint64(element.Size()))
		if !bootstrapNativeFlat(element) {
			for index := range value.Len() {
				total += decodedValueBytesAt(value.Index(index), depth+1)
			}
		}
		return total
	case reflect.Array:
		for index := range value.Len() {
			total += decodedValueBytesAt(value.Index(index), depth+1)
		}
		return total
	case reflect.Map:
		if value.IsNil() {
			return 0
		}
		total += decodedMapBytes(value.Len(), value.Type().Key().Size()+value.Type().Elem().Size())
		if bootstrapNativeFlat(value.Type().Key()) && bootstrapNativeFlat(value.Type().Elem()) {
			return total
		}
		iter := value.MapRange()
		for iter.Next() {
			total += decodedValueBytesAt(iter.Key(), depth+1) + decodedValueBytesAt(iter.Value(), depth+1)
		}
		return total
	case reflect.Struct:
		for index := range value.NumField() {
			total += decodedValueBytesAt(value.Field(index), depth+1)
		}
		return total
	}
	return 0
}

func decodedMapBytes(count int, slotBytes uintptr) uint64 {
	slots := uint64(8)
	for slots*7 < uint64(count)*8 {
		slots *= 2
	}
	return bootstrapNativeAllocation(slots*(uint64(slotBytes)+1)) + 128 + (slots/1024+1)*64
}

// coldDecodeSeeds are decoded values a writer hands to the image of the
// generation it wrote (withColdDecodeSeeds): per field name, per ordinal,
// exactly the value the writer encoded as that record's frame.
type coldDecodeSeeds map[string]map[int]reflect.Value

type coldDecodeSeedsKey struct{}

// withColdDecodeSeeds makes the image builds under ctx take seeds instead of
// decoding the records they name. The caller guarantees that each seeded
// ordinal of the source it builds holds a frame encoding that value.
func withColdDecodeSeeds(ctx context.Context, seeds coldDecodeSeeds) context.Context {
	return context.WithValue(ctx, coldDecodeSeedsKey{}, seeds)
}

var coldDecodeElementSeeded atomic.Int64

// decodeColdField decodes one field. A large slice of records decodes record
// by record, reusing the previous generation's value of every record whose
// frame has the same origin, and taking the seeded value of a record the
// writer of this generation encoded; any other field decodes whole, as
// before.
func decodeColdField(ctx context.Context, node metadataLogicalNode, before *coldDecodedField, memo bool, seed map[int]reflect.Value) (reflect.Value, map[coldFrameKey]coldDecodedElement, int64, error) {
	whole := func() (reflect.Value, map[coldFrameKey]coldDecodedElement, int64, error) {
		// Value's cap is solely an integer representability check here.
		// The cold slot, rather than HOT admission, owns the decoded graph.
		decoded, err := node.Value(ctx, int(^uint(0)>>1))
		if err != nil || !memo {
			return decoded, nil, 0, err
		}
		return decoded, nil, decodedValueBytes(decoded) + coldBoxedValueBytes(decoded) + coldOrdinalBytes(decoded), nil
	}
	if !memo || node.generic || node.blocked || node.unavailable || node.size < coldDecodeElementMinBytes ||
		node.typ.Kind() != reflect.Slice || node.typ.Elem().Kind() == reflect.Uint8 {
		return whole()
	}
	missing, err := node.isNil(ctx)
	if err != nil {
		return reflect.Value{}, nil, 0, err
	}
	if missing {
		return whole()
	}
	var previous map[coldFrameKey]coldDecodedElement
	if before != nil {
		previous = before.elements
	}
	elements := map[coldFrameKey]coldDecodedElement{}
	values := []reflect.Value{}
	var bytes int64
	appendValue := func(element coldDecodedElement) {
		bytes += element.bytes
		values = append(values, element.value)
	}
	err = node.Children(ctx, func(ordinal reflect.Value, child metadataLogicalNode) error {
		key, keyed := coldFrameKeyOf(child)
		if keyed && !child.generic {
			if reused, found := previous[key]; found {
				coldDecodeElementReuses.Add(1)
				elements[key] = reused
				appendValue(reused)
				return nil
			}
			if seeded, found := seed[int(ordinal.Int())]; found && seeded.Type() == child.typ {
				coldDecodeElementSeeded.Add(1)
				element := coldDecodedElement{seeded, decodedValueBytes(seeded) + coldBoxedValueBytes(seeded)}
				elements[key] = element
				appendValue(element)
				return nil
			}
		}
		// The record decodes exactly as inside its collection (no
		// admission check of its own, as materialize does).
		decoded, err := child.value(ctx, 1)
		if err != nil {
			return err
		}
		coldDecodeElementDecode.Add(1)
		element := coldDecodedElement{decoded, decodedValueBytes(decoded)}
		if keyed && !child.generic {
			element.bytes += coldBoxedValueBytes(decoded)
			elements[key] = element
		}
		appendValue(element)
		return nil
	})
	if err != nil {
		return reflect.Value{}, nil, 0, err
	}
	result := reflect.MakeSlice(node.typ, len(values), len(values))
	for i, element := range values {
		result.Index(i).Set(element)
	}
	bytes += int64(bootstrapNativeAllocation(uint64(result.Cap()) * uint64(node.typ.Elem().Size())))
	bytes += int64(decodedMapBytes(len(elements), reflect.TypeFor[coldFrameKey]().Size()+reflect.TypeFor[coldDecodedElement]().Size()))
	return result, elements, bytes + coldBoxedValueBytes(result) + coldOrdinalBytes(result), nil
}

func coldBoxedValueBytes(value reflect.Value) int64 {
	return int64(bootstrapNativeAllocation(uint64(value.Type().Size())))
}

// Reserve for the lazy ID index, including the backing arrays of duplicate
// ID lists and map header values. The ID strings are already charged.
func coldOrdinalBytes(value reflect.Value) int64 {
	if value.Kind() != reflect.Slice || value.Len() == 0 {
		return 0
	}
	if _, ok := recordOrdinalField(value.Type().Elem()); !ok {
		return 0
	}
	return int64(decodedMapBytes(value.Len(), reflect.TypeFor[string]().Size()+reflect.TypeFor[[]int]().Size())) + int64(value.Len())*32 + 128
}

// storeColdImage keeps image as workspace's cached image when it is the
// installed generation, then trims the cache to budget.
func (r *metadataResidency) storeColdImage(workspace string, image *coldDecodedImage, budget int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logical[workspace] != image.source || image.bytes > budget {
		return
	}
	cache := &r.coldImages
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.images == nil {
		cache.images = map[string]*coldDecodedImage{}
	}
	if old := cache.images[workspace]; old != nil {
		cache.bytes -= old.bytes
	}
	cache.images[workspace] = image
	cache.bytes += image.bytes
	for cache.bytes > budget {
		oldest := ""
		for key, candidate := range cache.images {
			if key != workspace && (oldest == "" || candidate.used.Before(cache.images[oldest].used)) {
				oldest = key
			}
		}
		if oldest == "" {
			break
		}
		cache.bytes -= cache.images[oldest].bytes
		delete(cache.images, oldest)
		coldDecodeEvictions.Add(1)
	}
	coldDecodeCachedBytes.Store(cache.bytes)
	idle := coldDecodeCacheIdle()
	if cache.idle == nil {
		cache.idle = time.AfterFunc(idle, func() { r.expireColdImages(coldDecodeCacheIdle()) })
	} else {
		cache.idle.Reset(idle)
	}
}

// expireColdImages drops the images unused for idle and rearms itself while
// images remain.
func (r *metadataResidency) expireColdImages(idle time.Duration) {
	cache := &r.coldImages
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := time.Now()
	next := idle
	for key, image := range cache.images {
		if unused := now.Sub(image.used); unused >= idle {
			cache.bytes -= image.bytes
			delete(cache.images, key)
			coldDecodeEvictions.Add(1)
		} else if idle-unused < next {
			next = idle - unused
		}
	}
	coldDecodeCachedBytes.Store(cache.bytes)
	if len(cache.images) != 0 && cache.idle != nil {
		cache.idle.Reset(next)
	}
}

// forgetColdImage drops workspace's cached image ("" drops every one).
func (r *metadataResidency) forgetColdImage(workspace string) {
	cache := &r.coldImages
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for key, image := range cache.images {
		if workspace == "" || key == workspace {
			cache.bytes -= image.bytes
			delete(cache.images, key)
		}
	}
	coldDecodeCachedBytes.Store(cache.bytes)
}

// coldImageCopy is an exact deep copy of a cached image (only fields, plus
// the workspace record, when fields is set): every container is the copy's
// own, strings are shared (immutable), and unlike cloneBootstrap no field is
// dropped, so the copy is reflect.DeepEqual to the decode it copies.
const coldCopyReuseMinBytes = 1 << 20

type coldCopyReuseKey struct{}

// coldCopyReuse keeps large collection backing arrays borrowed for one COLD
// facade or authorization scope. The arrays return to the bounded shared pool
// only after the copied graph is dropped.
type coldCopyReuse struct {
	mu      sync.Mutex
	buffers []reflect.Value
	closed  bool
}

func beginColdCopyReuse(ctx context.Context) (context.Context, *coldCopyReuse, func()) {
	lease := &coldCopyReuse{}
	return context.WithValue(ctx, coldCopyReuseKey{}, lease), lease, lease.Close
}

func coldCopyReuseFromContext(ctx context.Context) *coldCopyReuse {
	lease, _ := ctx.Value(coldCopyReuseKey{}).(*coldCopyReuse)
	return lease
}

func (r *coldCopyReuse) take(value reflect.Value) (reflect.Value, bool) {
	if r == nil || value.Kind() != reflect.Slice || value.IsNil() ||
		int64(value.Len())*int64(value.Type().Elem().Size()) < coldCopyReuseMinBytes {
		return reflect.Value{}, false
	}
	copied := sharedHydrationSnapshotBuffers.take(value.Type(), value.Len())
	reflect.Copy(copied, value)
	if !exactTypeFlat(value.Type().Elem()) {
		for index := 0; index < copied.Len(); index++ {
			exactCopyInPlace(copied.Index(index))
		}
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		sharedHydrationSnapshotBuffers.put(copied)
		return reflect.Value{}, false
	}
	r.buffers = append(r.buffers, copied)
	r.mu.Unlock()
	return copied, true
}

func (r *coldCopyReuse) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	buffers := r.buffers
	r.buffers = nil
	r.mu.Unlock()
	for _, buffer := range buffers {
		sharedHydrationSnapshotBuffers.put(buffer)
	}
}

func coldImageCopy(ctx context.Context, data domain.Bootstrap, fields []string) domain.Bootstrap {
	value := reflect.ValueOf(data)
	result := reflect.New(value.Type()).Elem()
	t := value.Type()
	reuse := coldCopyReuseFromContext(ctx)
	for i := 0; i < value.NumField(); i++ {
		if fields != nil {
			name := jsonFieldName(t.Field(i))
			if name != "workspace" && !slices.Contains(fields, name) {
				continue
			}
		}
		if t.Field(i).PkgPath != "" {
			continue
		}
		field := value.Field(i)
		if copied, ok := reuse.take(field); ok {
			result.Field(i).Set(copied)
			continue
		}
		result.Field(i).Set(exactCopyValue(field))
	}
	copied := result.Interface().(domain.Bootstrap)
	if fields != nil {
		// A field-limited view rebuilds its directory from what it holds,
		// as decodeColdWorkspace did.
		domain.RebuildTeamDirectory(&copied)
	}
	return copied
}

// exactClonePlans: per struct type, the exported fields whose value copy
// still shares a container (json:"-" fields included, unlike
// structClonePlan).
var exactClonePlans sync.Map

type exactClonePlan struct {
	flat bool
	deep []int
}

func exactPlanFor(t reflect.Type) *exactClonePlan {
	if cached, ok := exactClonePlans.Load(t); ok {
		return cached.(*exactClonePlan)
	}
	plan := &exactClonePlan{flat: true}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if !exactTypeFlat(field.Type) {
			plan.deep = append(plan.deep, i)
			plan.flat = false
		}
	}
	actual, _ := exactClonePlans.LoadOrStore(t, plan)
	return actual.(*exactClonePlan)
}

func exactTypeFlat(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		return false
	case reflect.Struct:
		return exactPlanFor(t).flat
	case reflect.Array:
		return exactTypeFlat(t.Elem())
	default:
		return true
	}
}

// exactCopyValue deep-copies containers (pointers, interfaces, slices and
// maps, keeping nil and length) and shares everything immutable.
func exactCopyValue(value reflect.Value) reflect.Value {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copied := reflect.New(value.Type().Elem())
		copied.Elem().Set(exactCopyValue(value.Elem()))
		return copied
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copied := reflect.New(value.Type()).Elem()
		copied.Set(exactCopyValue(value.Elem()))
		return copied
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copied := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		reflect.Copy(copied, value)
		if !exactTypeFlat(value.Type().Elem()) {
			for i := 0; i < copied.Len(); i++ {
				exactCopyInPlace(copied.Index(i))
			}
		}
		return copied
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copied := reflect.MakeMapWithSize(value.Type(), value.Len())
		flat := exactTypeFlat(value.Type().Elem())
		iter := value.MapRange()
		for iter.Next() {
			if flat {
				copied.SetMapIndex(iter.Key(), iter.Value())
			} else {
				copied.SetMapIndex(iter.Key(), exactCopyValue(iter.Value()))
			}
		}
		return copied
	case reflect.Struct:
		plan := exactPlanFor(value.Type())
		if plan.flat {
			return value
		}
		copied := reflect.New(value.Type()).Elem()
		copied.Set(value)
		for _, index := range plan.deep {
			exactCopyInPlace(copied.Field(index))
		}
		return copied
	case reflect.Array:
		if exactTypeFlat(value.Type().Elem()) {
			return value
		}
		copied := reflect.New(value.Type()).Elem()
		copied.Set(value)
		for i := 0; i < copied.Len(); i++ {
			exactCopyInPlace(copied.Index(i))
		}
		return copied
	default:
		return value
	}
}

func exactCopyInPlace(target reflect.Value) {
	switch target.Kind() {
	case reflect.Struct:
		if plan := exactPlanFor(target.Type()); !plan.flat {
			for _, index := range plan.deep {
				exactCopyInPlace(target.Field(index))
			}
		}
	case reflect.Array:
		if !exactTypeFlat(target.Type().Elem()) {
			for i := 0; i < target.Len(); i++ {
				exactCopyInPlace(target.Index(i))
			}
		}
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		target.Set(exactCopyValue(target))
	}
}
