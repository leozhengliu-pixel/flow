package store

import (
	"context"
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
// The cache is bounded (FLOW_COLD_DECODE_CACHE_BYTES of installed spool
// bytes, default 256 MiB; 0 disables it): the least recently used images are
// dropped beyond it, an image larger than the budget is not kept, and an
// image unused for FLOW_COLD_DECODE_CACHE_IDLE (default 5m) is dropped.

const defaultColdDecodeCacheBytes = 256 << 20
const defaultColdDecodeCacheIdle = 5 * time.Minute

// coldDecodeElementMinBytes: a slice field smaller than this decodes whole
// (its records are not worth a memo).
const coldDecodeElementMinBytes = 64 << 10

func coldDecodeCacheBudget() int64 {
	if strings.TrimSpace(os.Getenv("FLOW_COLD_DECODE_CACHE_BYTES")) == "0" {
		return 0
	}
	return metadataBudgetBytes("FLOW_COLD_DECODE_CACHE_BYTES", defaultColdDecodeCacheBytes)
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
	// elements memoizes the records of a large slice field by frame key.
	elements map[coldFrameKey]reflect.Value
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
		"elementReuses": coldDecodeElementReuses.Load(), "elementDecodes": coldDecodeElementDecode.Load(),
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
		data := coldImageCopy(previous.data, fields)
		coldDecodeCopyNanos.Add(int64(time.Since(started)))
		return data, ctx.Err()
	}
	if budget <= 0 {
		// No cache: a private decode, as before.
		image, err := buildColdDecodedImage(ctx, source, nil, fields, false)
		if err != nil {
			return domain.Bootstrap{}, err
		}
		return image.data, nil
	}
	image, err := buildColdDecodedImage(ctx, source, previous, fields, true)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if fields == nil {
		r.storeColdImage(workspace, image, budget)
	}
	started := time.Now()
	data := coldImageCopy(image.data, fields)
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
		image.bytes += node.size
		key, keyed := coldFrameKeyOf(node)
		var before *coldDecodedField
		if previous != nil && index < len(previous.fields) {
			before = &previous.fields[index]
		}
		if keyed && before != nil && before.keyed && before.key == key && before.value.IsValid() {
			coldDecodeFieldReuses.Add(1)
			field = coldDecodedField{key: key, keyed: true, value: before.value, elements: before.elements}
		} else {
			decoded, elements, err := decodeColdField(ctx, node, before, memo)
			if err != nil {
				return err
			}
			coldDecodeFieldDecodes.Add(1)
			field = coldDecodedField{key: key, keyed: keyed, value: decoded, elements: elements}
		}
		image.fields[index] = field
		value.Field(index).Set(field.value)
		return nil
	})
	if err != nil {
		return nil, err
	}
	domain.RebuildTeamDirectory(&image.data)
	coldDecodeImageBuilds.Add(1)
	return image, ctx.Err()
}

// decodeColdField decodes one field. A large slice of records decodes record
// by record, reusing the previous generation's value of every record whose
// frame has the same origin; any other field decodes whole, as before.
func decodeColdField(ctx context.Context, node metadataLogicalNode, before *coldDecodedField, memo bool) (reflect.Value, map[coldFrameKey]reflect.Value, error) {
	whole := func() (reflect.Value, map[coldFrameKey]reflect.Value, error) {
		// Value's cap is solely an integer representability check here.
		// The cold slot, rather than HOT admission, owns the decoded graph.
		decoded, err := node.Value(ctx, int(^uint(0)>>1))
		return decoded, nil, err
	}
	if !memo || node.generic || node.blocked || node.unavailable || node.size < coldDecodeElementMinBytes ||
		node.typ.Kind() != reflect.Slice || node.typ.Elem().Kind() == reflect.Uint8 {
		return whole()
	}
	missing, err := node.isNil(ctx)
	if err != nil {
		return reflect.Value{}, nil, err
	}
	if missing {
		return whole()
	}
	var previous map[coldFrameKey]reflect.Value
	if before != nil {
		previous = before.elements
	}
	elements := map[coldFrameKey]reflect.Value{}
	values := []reflect.Value{}
	err = node.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error {
		key, keyed := coldFrameKeyOf(child)
		if keyed && !child.generic {
			if reused, found := previous[key]; found {
				coldDecodeElementReuses.Add(1)
				elements[key] = reused
				values = append(values, reused)
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
		if keyed && !child.generic {
			elements[key] = decoded
		}
		values = append(values, decoded)
		return nil
	})
	if err != nil {
		return reflect.Value{}, nil, err
	}
	result := reflect.MakeSlice(node.typ, len(values), len(values))
	for i, element := range values {
		result.Index(i).Set(element)
	}
	return result, elements, nil
}

// storeColdImage keeps image as workspace's cached image when it is the
// installed generation, then trims the cache to budget.
func (r *metadataResidency) storeColdImage(workspace string, image *coldDecodedImage, budget int64) {
	r.mu.Lock()
	installed := r.logical[workspace] == image.source
	r.mu.Unlock()
	if !installed || image.bytes > budget {
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
func coldImageCopy(data domain.Bootstrap, fields []string) domain.Bootstrap {
	value := reflect.ValueOf(data)
	result := reflect.New(value.Type()).Elem()
	t := value.Type()
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
		result.Field(i).Set(exactCopyValue(value.Field(i)))
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
