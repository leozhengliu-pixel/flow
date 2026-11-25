// Package meter admits allocations and traversal work before the fork performs
// them. A meter belongs to one temporary document and is never shared.
package meter

import (
	"context"
	"errors"
	"math"
	"reflect"
	"unsafe"
)

type Admission func(uint64) (func(), error)

var ErrClosed = errors.New("bounded CRDT owner closed")
var ErrOverflow = errors.New("bounded CRDT allocation overflow")

type Abort struct{ Err error }

var overflowAbort = &Abort{Err: ErrOverflow}

type lease struct {
	release func()
	next    *lease
}

type Meter struct {
	ctx     context.Context
	admit   Admission
	leases  *lease
	credit  uint64
	stack   uint64
	stackHi uint64
	steps   uint64
	closed  bool
	failed  error
	abort   Abort
}

func NewMeter(ctx context.Context, admit Admission) (*Meter, error) {
	if err := RuntimeError(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if admit == nil {
		return nil, ErrClosed
	}
	release, err := admit(16 << 10)
	if err != nil {
		return nil, err
	}
	return &Meter{ctx: ctx, admit: admit, leases: &lease{release: release}, credit: 8 << 10}, nil
}

func (m *Meter) fail(err error) {
	m.failed = err
	m.abort.Err = err
	panic(&m.abort)
}

func (m *Meter) Check() {
	if m == nil {
		return
	}
	if m.failed != nil {
		m.fail(m.failed)
	}
	if m.closed {
		m.fail(ErrClosed)
	}
	if err := m.ctx.Err(); err != nil {
		m.fail(err)
	}
}

func Add(a, b uint64) uint64 {
	if b > math.MaxUint64-a {
		panic(overflowAbort)
	}
	return a + b
}

func Mul(a, b uint64) uint64 {
	if a != 0 && b > math.MaxUint64/a {
		panic(overflowAbort)
	}
	return a * b
}

func (m *Meter) Reserve(cost uint64) {
	if m == nil {
		return
	}
	m.Check()
	cost = Add(cost, 15) &^ 15
	if cost <= m.credit {
		m.credit -= cost
		return
	}
	// Each acquisition also pays for the lease and the caller's allowance
	// bookkeeping. Small credits amortize admission without masking a refusal.
	need := Add(cost-m.credit, 256)
	chunk := max(need, 16<<10)
	release, err := m.admit(chunk)
	if err != nil && chunk > need && m.ctx.Err() == nil {
		chunk = need
		release, err = m.admit(chunk)
	}
	if err != nil {
		m.fail(err)
	}
	m.leases = &lease{release: release, next: m.leases}
	m.credit = Add(m.credit, chunk-256) - cost
}

// Temporary owns scalar decoder scratch independently of the retained graph.
// Its caller releases it in the allocating stack frame.
func (m *Meter) Temporary(cost uint64) func() {
	if m == nil {
		return func() {}
	}
	m.Check()
	release, err := m.admit(Add(AllocationSize(cost), 256))
	if err != nil {
		m.fail(err)
	}
	return release
}

// Step charges an eight-byte work token. The tokens remain owned for the
// document lifetime, so repeated pending/conflict scans cannot run unbounded.
func (m *Meter) Step() {
	if m == nil {
		return
	}
	m.Check()
	if m.steps&127 == 0 {
		m.Reserve(128 * 8)
	}
	m.steps++
}

// Enter reserves two frames ahead, so callers admit their children's stack
// growth before those children execute a prologue. Sibling calls reuse the
// high-water allowance. Leave uses no returned closure or heap allocation.
func (m *Meter) Enter() {
	if m == nil {
		return
	}
	m.Step()
	next := Add(m.stack, 4096)
	ahead := Add(next, 2*4096)
	if ahead > m.stackHi {
		m.Reserve(ahead - m.stackHi)
		m.stackHi = ahead
	}
	m.stack = next
}

func (m *Meter) Leave() {
	if m != nil {
		m.stack -= 4096
	}
}

func (m *Meter) Close() {
	if m == nil || m.closed {
		return
	}
	m.closed = true
	for l := m.leases; l != nil; l = l.next {
		l.release()
	}
	m.leases = nil
	m.admit = nil
	m.ctx = nil
	m.failed = nil
	m.abort.Err = nil
}

func NewValue[T any](m *Meter) *T {
	var z T
	m.Reserve(AllocationSize(uint64(unsafe.Sizeof(z))))
	return new(T)
}

func New[T any](m *Meter) *T { return NewValue[T](m) }

func Pointer[T any](m *Meter, value T) *T {
	ptr := NewValue[T](m)
	*ptr = value
	return ptr
}

func Literal[T any](m *Meter, cost uint64, build func() T) T {
	m.Reserve(AllocationSize(cost))
	return build()
}

func MakeSlice[S ~[]E, E any](m *Meter, length, capacity uint64) S {
	var z E
	m.Reserve(AllocationSize(Mul(capacity, uint64(unsafe.Sizeof(z)))))
	if length > capacity || capacity > uint64(math.MaxInt) {
		panic(overflowAbort)
	}
	return make(S, int(length), int(capacity))
}

func Append[S ~[]E, E any](m *Meter, dst S, values ...E) S {
	m.Check()
	size := Add(uint64(len(dst)), uint64(len(values)))
	if size > uint64(cap(dst)) {
		capacity := max(size, Mul(uint64(cap(dst)), 2))
		next := MakeSlice[S, E](m, uint64(len(dst)), capacity)
		copy(next, dst)
		dst = next
	}
	return append(dst, values...)
}

func AppendString(m *Meter, dst []byte, value string) []byte {
	size := Add(uint64(len(dst)), uint64(len(value)))
	if size > uint64(cap(dst)) {
		next := MakeSlice[[]byte, byte](m, uint64(len(dst)), max(size, Mul(uint64(cap(dst)), 2)))
		copy(next, dst)
		dst = next
	}
	return append(dst, value...)
}

// MapEntryCost covers both live and superseded Swiss-map tables/directories
// over geometric growth, including one-entry groups and indirect entries.
// It is derived from key/value widths, rather than the wire-input size.
func MapEntryCost[K comparable, V any]() uint64 {
	var k K
	var v V
	return Add(256, Mul(16, Add(uint64(unsafe.Sizeof(k)), uint64(unsafe.Sizeof(v)))))
}

func MakeMap[M ~map[K]V, K comparable, V any](m *Meter, count uint64) M {
	m.Reserve(AllocationSize(Add(128, Mul(count, MapEntryCost[K, V]()))))
	if count > uint64(math.MaxInt) {
		panic(overflowAbort)
	}
	return make(M, int(count))
}

func Set[M ~map[K]V, K comparable, V any](m *Meter, dst M, key K, value V) {
	m.Check()
	if _, exists := dst[key]; !exists {
		m.Reserve(MapEntryCost[K, V]())
		mapInsertion(m, dst, key)
	}
	dst[key] = value
}

func Concat[S ~string](m *Meter, a, b S) S {
	m.Reserve(AllocationSize(Add(uint64(len(a)), uint64(len(b)))))
	return a + b
}

type stringInput interface {
	~string | ~[]byte | ~[]rune | ~int | ~int32 | ~uint8 | ~uint32 | ~uint64 | ~int64
}

func ToString[T stringInput](m *Meter, value T) string {
	m.Reserve(128)
	source := reflect.ValueOf(value)
	size := uint64(4)
	switch source.Kind() {
	case reflect.String:
		size = uint64(source.Len())
	case reflect.Slice:
		size = uint64(source.Len())
		if source.Type().Elem().Kind() == reflect.Int32 {
			size = Mul(size, 4)
		}
	}
	m.Reserve(AllocationSize(size))
	return source.Convert(reflect.TypeFor[string]()).String()
}

func Bytes[S ~string](m *Meter, value S) []byte {
	m.Reserve(AllocationSize(uint64(len(value))))
	return []byte(value)
}

func Unmarshal(m *Meter, raw []byte, value any) error {
	return unmarshalJSON(m, raw, value)
}
