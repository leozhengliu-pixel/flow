package meter

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"unsafe"
)

func TestScaleP21BoundedCRDTMapLayoutAndSkew(t *testing.T) {
	if !runtimeSupported() {
		t.Fatal(ErrRuntimeLayout, runtime.Version())
	}
	var held, peak uint64
	m, err := NewMeter(t.Context(), func(size uint64) (func(), error) {
		held += size
		peak = max(peak, held)
		return func() { held -= size }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		m.Close()
		if held != 0 {
			t.Fatal("meter leaked")
		}
	}()
	values := MakeMap[map[string]any, string, any](m, 0)
	header := (*runtimeMap)(*(*unsafe.Pointer)(unsafe.Pointer(&values)))
	rt := reflect.TypeFor[map[string]any]()
	descriptor := (*runtimeMapType)((*[2]unsafe.Pointer)(unsafe.Pointer(&rt))[1])
	if descriptor.size != 8 || descriptor.groupSize < 8+8*32 || header.used != 0 || header.directoryLen != 0 {
		t.Fatal("runtime map layout changed")
	}
	// Force several full-table splits into the same hash-prefix partition.
	// Unlike random-map fixtures, this proves directory amplification is
	// included before Set instead of relying on well-distributed inputs.
	for candidate, inserted := 0, 0; inserted < 1100; candidate++ {
		key := strconv.Itoa(candidate)
		if descriptor.hasher(unsafe.Pointer(&key), header.seed)>>60 != 0 {
			continue
		}
		Set(m, values, key, any(inserted))
		inserted++
	}
	if len(values) != 1100 || header.used != 1100 || header.directoryLen < 16 {
		t.Fatal("skew fixture did not split the runtime directory", len(values), header.used, header.directoryLen)
	}
	t.Logf("entries=%d directory=%d admitted_peak=%d", len(values), header.directoryLen, peak)
}

func TestScaleP21BoundedCRDTMapGrowthFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	m, err := NewMeter(ctx, func(uint64) (func(), error) { return func() {}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	values := MakeMap[map[uint64]string, uint64, string](m, 0)
	for i := uint64(0); i < 2048; i++ {
		Set(m, values, i, fmt.Sprint(i))
	}
	before := len(values)
	cancel()
	defer func() {
		abort, ok := recover().(*Abort)
		if !ok || abort.Err != context.Canceled || len(values) != before {
			t.Fatal("cancellation did not stop insertion before mutation")
		}
	}()
	Set(m, values, uint64(99999), "after cancellation")
}
