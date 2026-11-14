package store

import (
	"context"
	"encoding"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const metadataInstalledVisitWork = 64 << 10
const metadataOwnedReductionMinimumWork = 4 << 10
const metadataOwnedReductionHandleWork = 512

// InstalledMetadataShape distinguishes a missing path from a present nil
// collection. Type is the exact addressed type; Kind is its unwrapped kind.
type InstalledMetadataShape struct {
	Found bool
	Nil   bool
	Type  reflect.Type
	Kind  reflect.Kind
}

// InstalledMetadataEntry is borrowed until its Visit callback returns.
// Ordinal is the original array position, or -1 for struct/map entries.
type InstalledMetadataEntry struct {
	Key, Value InstalledMetadataRef
	Ordinal    int
}

// InstalledMetadataRef is opaque and cannot outlive its entry callback.
type InstalledMetadataRef struct {
	borrow *metadataInstalledBorrow
	node   metadataLogicalNode
	native reflect.Value
}

type metadataInstalledBorrow struct {
	mu     sync.RWMutex
	active atomic.Bool
	owner  *InstalledMetadataRead
	parent *metadataInstalledBorrow
}

func (b *metadataInstalledBorrow) live(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b == nil || !b.active.Load() {
		return errMetadataLogicalClosed
	}
	if b.parent != nil {
		if err := b.parent.live(ctx); err != nil {
			return err
		}
	}
	b.owner.mu.Lock()
	defer b.owner.mu.Unlock()
	if b.owner.closed {
		return errMetadataLogicalClosed
	}
	return nil
}

func newMetadataInstalledBorrow(owner *InstalledMetadataRead, parent *metadataInstalledBorrow) *metadataInstalledBorrow {
	borrow := &metadataInstalledBorrow{owner: owner, parent: parent}
	borrow.active.Store(true)
	return borrow
}

func (b *metadataInstalledBorrow) close() {
	b.mu.Lock()
	b.active.Store(false)
	b.mu.Unlock()
}

// WithValue independently admits exact typed decoding and the declared
// processing allowance. Retaining a value requires a separate admitted result
// lifetime, such as Reduce, that remains owned through its final use.
func (r InstalledMetadataRef) WithValue(ctx context.Context, processingWorkBytes int, visit func(any) error) error {
	if r.borrow == nil || visit == nil {
		return ErrMetadataRecordQuery
	}
	r.borrow.mu.RLock()
	defer r.borrow.mu.RUnlock()
	if err := r.borrow.live(ctx); err != nil {
		return err
	}
	cost := r.node.cost
	if r.native.IsValid() {
		var err error
		cost, err = metadataAggregateNativeCost(ctx, r.native, 0)
		if err != nil {
			return err
		}
	}
	if processingWorkBytes < 0 || cost > metadataWorkBytes ||
		uint64(processingWorkBytes) > metadataWorkBytes-cost {
		return ErrMetadataWorkBudget
	}
	bound, err := r.borrow.owner.Context(ctx)
	if err != nil {
		return err
	}
	work, release, err := r.borrow.owner.store.tryAdmitAdditionalMetadataWork(bound, int(cost)+processingWorkBytes)
	if err != nil {
		return err
	}
	defer release()
	value := r.native
	if !value.IsValid() {
		value, err = r.node.Value(work, int(cost))
	}
	if err == nil {
		err = r.borrow.live(ctx)
	}
	if err != nil {
		return err
	}
	if err := visit(value.Interface()); err != nil {
		return err
	}
	return r.borrow.live(ctx)
}

// WithValueForEncoding independently admits typed decoding and JSON encoder
// work before visit. extraWorkBytes covers the caller's other processing.
func (r InstalledMetadataRef) WithValueForEncoding(ctx context.Context, extraWorkBytes int, visit func(any) error) error {
	if visit == nil {
		return ErrMetadataRecordQuery
	}
	if extraWorkBytes < 0 || extraWorkBytes > metadataWorkBytes-metadataInstalledVisitWork {
		return ErrMetadataWorkBudget
	}
	return r.WithValue(ctx, extraWorkBytes+metadataInstalledVisitWork, func(value any) error {
		return r.withEncodingWork(ctx, value, visit)
	})
}

// WithFieldsForEncoding decodes only declared struct fields, then reserves
// their complete JSON encoding work before visit.
func (r InstalledMetadataRef) WithFieldsForEncoding(ctx context.Context, fields []string, extraWorkBytes int, visit func(any) error) error {
	if visit == nil {
		return ErrMetadataRecordQuery
	}
	if extraWorkBytes < 0 || extraWorkBytes > metadataWorkBytes-metadataInstalledVisitWork {
		return ErrMetadataWorkBudget
	}
	return r.WithFields(ctx, fields, extraWorkBytes+metadataInstalledVisitWork, func(value any) error {
		return r.withEncodingWork(ctx, value, visit)
	})
}

func (r InstalledMetadataRef) withEncodingWork(ctx context.Context, value any, visit func(any) error) error {
	native := reflect.ValueOf(value)
	if native.IsValid() {
		if err := metadataInstalledResultType(ctx, native, 0); err != nil {
			return err
		}
	}
	workBytes, err := metadataAggregatePayloadEncodingWork(ctx, native, 0)
	if err != nil {
		return err
	}
	bound, err := r.borrow.owner.Context(ctx)
	if err != nil {
		return err
	}
	_, release, err := r.borrow.owner.store.tryAdmitAdditionalMetadataWork(bound, workBytes)
	if err != nil {
		return err
	}
	defer release()
	return visit(value)
}

// StreamBytes borrows a string/byte reader only for the callback. Callers
// separately own the allowance for any retained output or processing buffers.
func (r InstalledMetadataRef) StreamBytes(ctx context.Context, visit func(io.Reader) error) error {
	if r.borrow == nil || visit == nil {
		return ErrMetadataRecordQuery
	}
	r.borrow.mu.RLock()
	defer r.borrow.mu.RUnlock()
	if err := r.borrow.live(ctx); err != nil {
		return err
	}
	if r.native.IsValid() {
		return ErrMetadataRecordQuery
	}
	bound, err := r.borrow.owner.Context(ctx)
	if err != nil {
		return err
	}
	work, release, err := r.borrow.owner.store.tryAdmitAdditionalMetadataWork(bound, metadataInstalledVisitWork)
	if err != nil {
		return err
	}
	defer release()
	node, present, err := metadataInstalledUnwrap(work, r.node)
	if err != nil {
		return err
	}
	if !present {
		return ErrMetadataRecordQuery
	}
	return node.StreamBytes(work, func(reader io.Reader) error {
		stream := &metadataInstalledReader{reader: reader, borrow: r.borrow, ctx: ctx}
		stream.active.Store(true)
		defer stream.active.Store(false)
		if err := visit(stream); err != nil {
			return err
		}
		return r.borrow.live(ctx)
	})
}

type metadataInstalledReader struct {
	reader io.Reader
	borrow *metadataInstalledBorrow
	ctx    context.Context
	active atomic.Bool
}

func (r *metadataInstalledReader) Read(p []byte) (int, error) {
	r.borrow.mu.RLock()
	defer r.borrow.mu.RUnlock()
	if !r.active.Load() {
		return 0, errMetadataLogicalClosed
	}
	if err := r.borrow.live(r.ctx); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Visit traverses exact JSON struct names and native string map keys. It
// visits immediate collection entries without materializing the collection.
// It is trusted storage, with no viewer filtering or authorization.
func (r *InstalledMetadataRead) Visit(ctx context.Context, path []string, visit func(context.Context, InstalledMetadataEntry) error) (InstalledMetadataShape, error) {
	var shape InstalledMetadataShape
	if visit == nil {
		return shape, ErrMetadataRecordQuery
	}
	err := r.withInstalledPath(ctx, path, func(bound, work context.Context, node metadataLogicalNode, found bool) error {
		if !found {
			return nil
		}
		var err error
		shape, err = r.visitInstalledNode(ctx, bound, work, node, nil, visit)
		return err
	})
	return shape, err
}

// Child borrows one exact installed value, including a terminal scalar. A
// missing path calls visit with Found=false and an unusable ref.
func (r *InstalledMetadataRead) Child(ctx context.Context, path []string, visit func(context.Context, InstalledMetadataShape, InstalledMetadataRef) error) error {
	if visit == nil {
		return ErrMetadataRecordQuery
	}
	return r.withInstalledPath(ctx, path, func(bound, work context.Context, node metadataLogicalNode, found bool) error {
		if !found {
			return visit(bound, InstalledMetadataShape{}, InstalledMetadataRef{})
		}
		shape, _, err := metadataInstalledShape(work, node)
		if err != nil {
			return err
		}
		borrow := newMetadataInstalledBorrow(r, nil)
		defer borrow.close()
		if err := visit(bound, shape, InstalledMetadataRef{borrow: borrow, node: node}); err != nil {
			return err
		}
		return borrow.live(ctx)
	})
}

func (r *InstalledMetadataRead) withInstalledPath(ctx context.Context, path []string, visit func(context.Context, context.Context, metadataLogicalNode, bool) error) error {
	if len(path) == 0 || len(path) > metadataLogicalDepth {
		return ErrMetadataRecordQuery
	}
	bound, err := r.Context(ctx)
	if err != nil {
		return err
	}
	source, err := r.retainSource(bound, r.workspace)
	if err != nil {
		return err
	}
	defer source.Release()
	work, release, err := r.store.tryAdmitAdditionalMetadataWork(bound, metadataInstalledVisitWork)
	if err != nil {
		return err
	}
	defer release()
	var node metadataLogicalNode
	found := false
	err = source.VisitFields(work, func(name string, current metadataLogicalNode) error {
		if name == path[0] {
			node, found = current, true
		}
		return nil
	})
	for _, part := range path[1:] {
		if err != nil || !found {
			break
		}
		node, found, err = metadataInstalledChild(work, node, part)
	}
	if err == nil {
		_, err = r.Context(ctx)
	}
	if err == nil {
		err = visit(bound, work, node, found)
	}
	if err == nil {
		_, err = r.Context(ctx)
	}
	return err
}

func metadataInstalledShape(ctx context.Context, node metadataLogicalNode) (InstalledMetadataShape, metadataLogicalNode, error) {
	shape := InstalledMetadataShape{Found: true, Type: node.Type()}
	node, present, err := metadataInstalledUnwrap(ctx, node)
	if err != nil {
		return shape, node, err
	}
	shape.Kind = node.Type().Kind()
	if !present {
		shape.Nil = true
		return shape, node, nil
	}
	if shape.Kind == reflect.Slice || shape.Kind == reflect.Map {
		shape.Nil, err = node.isNil(ctx)
	}
	return shape, node, err
}

func (r *InstalledMetadataRead) visitInstalledNode(ctx, bound, work context.Context, node metadataLogicalNode, parent *metadataInstalledBorrow, visit func(context.Context, InstalledMetadataEntry) error) (InstalledMetadataShape, error) {
	shape, node, err := metadataInstalledShape(work, node)
	if err != nil || shape.Nil {
		return shape, err
	}
	emit := func(key InstalledMetadataRef, ordinal int, value metadataLogicalNode) error {
		if _, err := r.Context(ctx); err != nil {
			return err
		}
		borrow := newMetadataInstalledBorrow(r, parent)
		key.borrow = borrow
		entry := InstalledMetadataEntry{Key: key, Value: InstalledMetadataRef{borrow: borrow, node: value}, Ordinal: ordinal}
		defer borrow.close()
		if err := visit(bound, entry); err != nil {
			return err
		}
		return borrow.live(ctx)
	}
	if shape.Kind == reflect.Map {
		err = node.MapEntries(work, func(key, value metadataLogicalNode) error {
			return emit(InstalledMetadataRef{node: key}, -1, value)
		})
	} else if shape.Kind == reflect.Slice || shape.Kind == reflect.Array || shape.Kind == reflect.Struct {
		if shape.Kind == reflect.Slice && node.Type().Elem().Kind() == reflect.Uint8 || node.Type() == reflect.TypeFor[time.Time]() {
			return shape, ErrMetadataRecordQuery
		}
		err = node.Children(work, func(key reflect.Value, value metadataLogicalNode) error {
			ordinal := -1
			if shape.Kind == reflect.Struct {
				field, _ := node.Type().FieldByName(key.String())
				name, ok := metadataInstalledFieldName(field)
				if !ok {
					return nil
				}
				key = reflect.ValueOf(name)
			} else {
				ordinal = int(key.Int())
			}
			return emit(InstalledMetadataRef{native: key}, ordinal, value)
		})
	} else {
		err = ErrMetadataRecordQuery
	}
	if err == nil {
		_, err = r.Context(ctx)
	}
	return shape, err
}

// Child borrows one nested value without decoding its parent. A missing path
// calls visit with Found=false and an unusable ref. Paths use exact JSON
// struct field names and native string map keys; an empty path selects self.
func (r InstalledMetadataRef) Child(ctx context.Context, path []string, visit func(context.Context, InstalledMetadataShape, InstalledMetadataRef) error) error {
	if r.borrow == nil || visit == nil || len(path) > metadataLogicalDepth || r.native.IsValid() {
		return ErrMetadataRecordQuery
	}
	r.borrow.mu.RLock()
	defer r.borrow.mu.RUnlock()
	if err := r.borrow.live(ctx); err != nil {
		return err
	}
	bound, err := r.borrow.owner.Context(ctx)
	if err != nil {
		return err
	}
	work, release, err := r.borrow.owner.store.tryAdmitAdditionalMetadataWork(bound, metadataInstalledVisitWork)
	if err != nil {
		return err
	}
	defer release()
	node, found := r.node, true
	for _, part := range path {
		node, found, err = metadataInstalledChild(work, node, part)
		if err != nil || !found {
			break
		}
	}
	if err != nil {
		return err
	}
	if !found {
		if err := visit(bound, InstalledMetadataShape{}, InstalledMetadataRef{}); err != nil {
			return err
		}
		return r.borrow.live(ctx)
	}
	shape, _, err := metadataInstalledShape(work, node)
	if err != nil {
		return err
	}
	childBorrow := newMetadataInstalledBorrow(r.borrow.owner, r.borrow)
	defer childBorrow.close()
	if err := visit(bound, shape, InstalledMetadataRef{borrow: childBorrow, node: node}); err != nil {
		return err
	}
	return childBorrow.live(ctx)
}

// StringEqual compares exact native string bytes without decoding an entire
// large nonmatching value. Non-string and nil values compare false.
func (r InstalledMetadataRef) StringEqual(ctx context.Context, want string) (bool, error) {
	if r.borrow == nil {
		return false, ErrMetadataRecordQuery
	}
	if r.native.IsValid() {
		r.borrow.mu.RLock()
		defer r.borrow.mu.RUnlock()
		if err := r.borrow.live(ctx); err != nil {
			return false, err
		}
		matched := r.native.Kind() == reflect.String && r.native.String() == want
		return matched, r.borrow.live(ctx)
	}
	matched := false
	err := r.Child(ctx, nil, func(bound context.Context, shape InstalledMetadataShape, child InstalledMetadataRef) error {
		if shape.Nil || shape.Kind != reflect.String {
			return nil
		}
		node, _, err := metadataInstalledUnwrap(bound, child.node)
		if err != nil {
			return err
		}
		matched, err = node.StringEqual(bound, want)
		return err
	})
	return matched, err
}

// Visit borrows the immediate entries of a nested collection. The enclosing
// entry and every nested callback must remain live.
func (r InstalledMetadataRef) Visit(ctx context.Context, path []string, visit func(context.Context, InstalledMetadataEntry) error) (InstalledMetadataShape, error) {
	var shape InstalledMetadataShape
	if visit == nil {
		return shape, ErrMetadataRecordQuery
	}
	err := r.Child(ctx, path, func(bound context.Context, selected InstalledMetadataShape, child InstalledMetadataRef) error {
		shape = selected
		if !shape.Found || shape.Nil {
			return nil
		}
		var err error
		shape, err = r.borrow.owner.visitInstalledNode(ctx, bound, bound, child.node, child.borrow, visit)
		return err
	})
	return shape, err
}

// WithFields materializes only the declared immediate JSON fields of the
// underlying struct. It returns that exact struct type with excluded fields
// zeroed, without decoding them. Retained output needs a separate owner.
func (r InstalledMetadataRef) WithFields(ctx context.Context, fields []string, processingWorkBytes int, visit func(any) error) error {
	if visit == nil || len(fields) > metadataLogicalDepth || processingWorkBytes < 0 {
		return ErrMetadataRecordQuery
	}
	return r.Child(ctx, nil, func(bound context.Context, shape InstalledMetadataShape, child InstalledMetadataRef) error {
		node, present, err := metadataInstalledUnwrap(bound, child.node)
		if err != nil {
			return err
		}
		if !present || shape.Kind != reflect.Struct || node.Type() == reflect.TypeFor[time.Time]() {
			return ErrMetadataRecordQuery
		}
		found := make([]bool, len(fields))
		cost := uint64(node.Type().Size())*2 + 128
		err = node.Children(bound, func(key reflect.Value, value metadataLogicalNode) error {
			field, _ := node.Type().FieldByName(key.String())
			name, visible := metadataInstalledFieldName(field)
			if !visible || !slices.Contains(fields, name) {
				return nil
			}
			for i, selected := range fields {
				found[i] = found[i] || name == selected
			}
			if value.cost > metadataWorkBytes || cost > metadataWorkBytes-value.cost {
				return ErrMetadataWorkBudget
			}
			cost += value.cost
			return nil
		})
		if err != nil {
			return err
		}
		if slices.Contains(found, false) {
			return ErrMetadataRecordQuery
		}
		if cost > metadataWorkBytes || uint64(processingWorkBytes) > metadataWorkBytes-cost {
			return ErrMetadataWorkBudget
		}
		work, release, err := r.borrow.owner.store.tryAdmitAdditionalMetadataWork(bound, int(cost)+processingWorkBytes)
		if err != nil {
			return err
		}
		defer release()
		value := reflect.New(node.Type()).Elem()
		err = node.Children(work, func(key reflect.Value, selected metadataLogicalNode) error {
			field, _ := node.Type().FieldByName(key.String())
			name, visible := metadataInstalledFieldName(field)
			if !visible || !slices.Contains(fields, name) {
				return nil
			}
			part, err := selected.Value(work, int(selected.cost))
			if err != nil {
				return err
			}
			value.FieldByIndex(field.Index).Set(part)
			return nil
		})
		if err != nil {
			return err
		}
		if err := child.borrow.live(ctx); err != nil {
			return err
		}
		return visit(value.Interface())
	})
}

func metadataInstalledUnwrap(ctx context.Context, node metadataLogicalNode) (metadataLogicalNode, bool, error) {
	for depth := 0; depth < metadataLogicalDepth; depth++ {
		if node.Type().Kind() != reflect.Interface && node.Type().Kind() != reflect.Pointer {
			return node, true, nil
		}
		var child metadataLogicalNode
		found := false
		err := node.Children(ctx, func(_ reflect.Value, value metadataLogicalNode) error {
			child, found = value, true
			return nil
		})
		if err != nil || !found {
			return node, false, err
		}
		node = child
	}
	return node, false, ErrMetadataWorkBudget
}

func metadataInstalledFieldName(field reflect.StructField) (string, bool) {
	if field.PkgPath != "" {
		return "", false
	}
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name == "-" {
		return "", false
	}
	if name == "" {
		name = field.Name
	}
	return name, true
}

func metadataInstalledChild(ctx context.Context, node metadataLogicalNode, part string) (metadataLogicalNode, bool, error) {
	node, present, err := metadataInstalledUnwrap(ctx, node)
	if err != nil || !present {
		return metadataLogicalNode{}, false, err
	}
	var child metadataLogicalNode
	found := false
	switch node.Type().Kind() {
	case reflect.Map:
		// Other key types are still visitable as opaque typed entry refs.
		if node.Type().Key().Kind() != reflect.String {
			return child, false, ErrMetadataRecordQuery
		}
		child, found, err = node.MapChild(ctx, part)
	case reflect.Struct:
		err = node.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
			field, _ := node.Type().FieldByName(key.String())
			name, visible := metadataInstalledFieldName(field)
			if visible && name == part {
				child, found = value, true
			}
			return nil
		})
	default:
		return child, false, ErrMetadataRecordQuery
	}
	return child, found, err
}

// OwnedMetadataReduction holds Data and its independently admitted result/
// encoding allowance through Close. Data must not escape that lifetime.
type OwnedMetadataReduction struct {
	Data    any
	mu      sync.Mutex
	owner   *InstalledMetadataRead
	release func()
	closed  bool
}

// Reduce reserves before calling fn. resultWorkBytes is at least 4 KiB to
// cover the retained result handle. The caller keeps intermediates within
// that allowance; final validation checks retained typed/encoding work.
func (r *InstalledMetadataRead) Reduce(ctx context.Context, resultWorkBytes int, fn func(context.Context) (any, error)) (*OwnedMetadataReduction, error) {
	if fn == nil || resultWorkBytes < metadataOwnedReductionMinimumWork || resultWorkBytes > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	bound, err := r.Context(ctx)
	if err != nil {
		return nil, err
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(bound, resultWorkBytes)
	if err != nil {
		return nil, err
	}
	data, err := fn(bound)
	if err == nil {
		err = metadataInstalledResultFits(ctx, data, resultWorkBytes)
	}
	if err == nil {
		_, err = r.Context(ctx)
	}
	if err != nil {
		release()
		return nil, err
	}
	result := &OwnedMetadataReduction{Data: data, owner: r, release: release}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		result.Close()
		return nil, errMetadataLogicalClosed
	}
	r.reductions[result] = struct{}{}
	r.mu.Unlock()
	return result, nil
}

func metadataInstalledResultFits(ctx context.Context, data any, allowance int) error {
	value := reflect.ValueOf(data)
	if !value.IsValid() {
		return ctx.Err()
	}
	if err := metadataInstalledResultType(ctx, value, 0); err != nil {
		return err
	}
	retained, err := metadataInstalledRetainedCost(ctx, value, 0)
	if err != nil {
		return err
	}
	encoding, err := metadataAggregatePayloadEncodingWork(ctx, value, 0)
	if err != nil {
		return err
	}
	available := uint64(allowance - metadataOwnedReductionHandleWork)
	if retained > available || uint64(encoding) > available-retained {
		return ErrMetadataWorkBudget
	}
	return nil
}

func metadataInstalledRetainedCost(ctx context.Context, value reflect.Value, depth int) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if depth >= metadataLogicalDepth {
		return 0, ErrMetadataWorkBudget
	}
	cost := uint64(value.Type().Size())*2 + 128
	add := func(child reflect.Value) error {
		part, err := metadataInstalledRetainedCost(ctx, child, depth+1)
		if err != nil {
			return err
		}
		if part > metadataWorkBytes || cost > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	switch value.Kind() {
	case reflect.String:
		cost += uint64(value.Len())*2 + 64
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			if err := add(value.Elem()); err != nil {
				return 0, err
			}
		}
	case reflect.Slice, reflect.Array:
		retained := value
		if value.Kind() == reflect.Slice {
			retained = value.Slice(0, value.Cap())
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			cost += uint64(retained.Len())*2 + 64
			break
		}
		cost += uint64(retained.Len()) * (uint64(value.Type().Elem().Size())*2 + 64)
		for i := 0; i < retained.Len(); i++ {
			if err := add(retained.Index(i)); err != nil {
				return 0, err
			}
		}
	case reflect.Map:
		cost += uint64(value.Len()) * (uint64(value.Type().Elem().Size())*2 + 64)
		iter := value.MapRange()
		for iter.Next() {
			if err := add(iter.Key()); err != nil {
				return 0, err
			}
			if err := add(iter.Value()); err != nil {
				return 0, err
			}
		}
	case reflect.Struct:
		if value.Type() == reflect.TypeFor[time.Time]() {
			break
		}
		for i := 0; i < value.NumField(); i++ {
			if err := add(value.Field(i)); err != nil {
				return 0, err
			}
		}
	}
	if cost > metadataWorkBytes {
		return 0, ErrMetadataWorkBudget
	}
	return cost, nil
}

func metadataInstalledResultType(ctx context.Context, value reflect.Value, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth >= metadataLogicalDepth {
		return ErrMetadataWorkBudget
	}
	t := value.Type()
	if t == reflect.TypeFor[time.Time]() || t == reflect.TypeFor[*time.Time]() ||
		t == metadataAggregateRawPayloadType || t == reflect.PointerTo(metadataAggregateRawPayloadType) {
		return nil
	}
	// Arbitrary marshal methods may allocate an unbounded encoded result.
	if t.Implements(reflect.TypeFor[json.Marshaler]()) || t.Implements(reflect.TypeFor[encoding.TextMarshaler]()) ||
		reflect.PointerTo(t).Implements(reflect.TypeFor[json.Marshaler]()) || reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextMarshaler]()) {
		return ErrBoundedMetadataCoverage
	}
	child := func(v reflect.Value) error { return metadataInstalledResultType(ctx, v, depth+1) }
	switch value.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		return nil
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return child(value.Elem())
		}
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() != reflect.Uint8 {
			for i := 0; i < value.Len(); i++ {
				if err := child(value.Index(i)); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if err := child(iter.Key()); err != nil {
				return err
			}
			if err := child(iter.Value()); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if err := child(value.Field(i)); err != nil {
				return err
			}
		}
	default:
		return ErrBoundedMetadataCoverage
	}
	return nil
}

func (r *OwnedMetadataReduction) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	owner, release := r.owner, r.release
	r.Data, r.owner, r.release = nil, nil, nil
	r.mu.Unlock()
	if owner != nil {
		owner.mu.Lock()
		delete(owner.reductions, r)
		owner.mu.Unlock()
	}
	if release != nil {
		release()
	}
}
