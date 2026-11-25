package meter

import (
	"errors"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"unsafe"
)

var ErrRuntimeLayout = errors.New("bounded CRDT requires the Go 1.26 Swiss map layout")

// These read-only prefixes mirror internal/abi.Type, internal/abi.MapType and
// internal/runtime/maps.{Map,table} in the repository's Go 1.26 toolchain.
// No pointer is retained and no runtime-owned memory is modified.
type runtimeType struct {
	size, pointers uintptr
	hash           uint32
	flags          [4]byte
	equal          func(unsafe.Pointer, unsafe.Pointer) bool
	gc             unsafe.Pointer
	name, pointer  int32
}
type runtimeMapType struct {
	runtimeType
	key, elem, group *runtimeType
	hasher           func(unsafe.Pointer, uintptr) uintptr
	groupSize        uintptr
	slotSize         uintptr
	elemOffset       uintptr
	flags            uint32
}
type runtimeMap struct {
	used                  uint64
	seed                  uintptr
	directory             unsafe.Pointer
	directoryLen          int
	depth, shift, writing uint8
	tombstones            bool
	clearSequence         uint64
}
type runtimeTable struct {
	used, capacity, growth uint16
	depth                  uint8
	index                  int
	groups                 unsafe.Pointer
	groupsMask             uint64
}

var layoutSupported = detectRuntimeLayout()

func runtimeSupported() bool { return layoutSupported }

func RuntimeError() error {
	if !runtimeSupported() {
		return ErrRuntimeLayout
	}
	return nil
}

func detectRuntimeLayout() bool {
	if !strings.HasPrefix(runtime.Version(), "go1.26.") || unsafe.Sizeof(uintptr(0)) != 8 {
		return false
	}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, setting := range build.Settings {
		if setting.Key == "GOEXPERIMENT" && setting.Value != "" && setting.Value != "none" {
			return false
		}
	}
	probe := map[uint64]uint64{1: 2}
	header := (*runtimeMap)(*(*unsafe.Pointer)(unsafe.Pointer(&probe)))
	rt := reflect.TypeFor[map[uint64]uint64]()
	descriptor := (*runtimeMapType)((*[2]unsafe.Pointer)(unsafe.Pointer(&rt))[1])
	return header.used == 1 && header.directoryLen == 0 && header.directory != nil &&
		descriptor.size == 8 && descriptor.groupSize == 8+8*16 &&
		descriptor.slotSize == 16 && descriptor.elemOffset == 8 &&
		descriptor.hasher != nil
}

// mapInsertion admits all table/directory allocations the next insertion can
// cause, including repeated splits when every existing key shares hash bits.
// Per-entry width charging alone cannot cover that directory amplification.
func mapInsertion[M ~map[K]V, K comparable, V any](m *Meter, dst M, key K) {
	if m == nil {
		return
	}
	if !runtimeSupported() {
		m.fail(ErrRuntimeLayout)
	}
	switch reflect.TypeFor[K]().Kind() {
	case reflect.String, reflect.Uint64, reflect.Pointer:
	default:
		// Engine keys are strings, client clocks, or object identities.
		// In particular, a NaN key's runtime hash is non-repeatable.
		m.fail(ErrRuntimeLayout)
	}
	header := (*runtimeMap)(*(*unsafe.Pointer)(unsafe.Pointer(&dst)))
	if header == nil {
		return // preserve the native nil-map panic at the actual assignment
	}
	rt := reflect.TypeFor[M]()
	descriptor := (*runtimeMapType)((*[2]unsafe.Pointer)(unsafe.Pointer(&rt))[1])
	groupBytes := uint64(descriptor.groupSize)
	tableCost := func(capacity uint64) uint64 {
		return Add(AllocationSize(uint64(unsafe.Sizeof(runtimeTable{}))),
			AllocationSize(Mul(capacity/8, groupBytes)))
	}
	if header.directoryLen == 0 {
		if header.directory == nil {
			m.Reserve(AllocationSize(groupBytes))
		} else if header.used == 8 {
			m.Reserve(Add(tableCost(16), AllocationSize(8)))
		}
		return
	}
	// A dynamic runtime hasher makes an address-taken generic key escape.
	// Allocate one admitted key cell and reuse it for the whole prediction,
	// rather than heap-allocating the range variable on every hash.
	keyCell := NewValue[K](m)
	*keyCell = key
	hash := descriptor.hasher(unsafe.Pointer(keyCell), header.seed)
	index := hash >> header.shift
	table := *(**runtimeTable)(unsafe.Add(header.directory, index*unsafe.Sizeof(uintptr(0))))
	if table.growth != 0 {
		return
	}
	if table.capacity < 1024 {
		m.Reserve(tableCost(uint64(table.capacity) * 2))
		return
	}
	depth, global := table.depth, header.depth
	directoryLen := uint64(header.directoryLen)
	for {
		m.Step()
		depth++
		// Two new 1024-slot tables, including allocator rounding and
		// table descriptors, are allocated before the old one is dropped.
		m.Reserve(Mul(2, tableCost(1024)))
		if depth > global {
			directoryLen = Mul(directoryLen, 2)
			m.Reserve(AllocationSize(Mul(directoryLen, 8)))
			global = depth
		}
		if depth >= 64 {
			// With no differing hash bit, runtime would keep splitting.
			// The next directory allocation has already been admitted;
			// an exhausted bounded allowance refuses before it happens.
			m.fail(ErrOverflow)
		}
		shift := 64 - depth
		count := uint64(0)
		for candidate := range dst {
			m.Step()
			*keyCell = candidate
			h := descriptor.hasher(unsafe.Pointer(keyCell), header.seed)
			if h>>shift == hash>>shift {
				count++
			}
		}
		if count < 896 {
			return
		}
	}
}
