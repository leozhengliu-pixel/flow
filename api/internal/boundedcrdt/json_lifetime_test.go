package boundedcrdt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScaleP21BoundedCRDTJSONLinearOwnership(t *testing.T) {
	for _, size := range []int{4 << 20, 16 << 20} {
		t.Run(fmt.Sprintf("HTML-%dMiB", size>>20), func(t *testing.T) {
			want := strings.Repeat("<", size)
			raw, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			a := &scaleP21CRDTAdmission{limit: 256 << 20}
			releaseInput, err := a.acquire(uint64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			got, release, err := DecodeJSON(t.Context(), a.acquire, raw)
			if err != nil || got != want {
				t.Fatalf("legal HTML scalar refused/changed: input=%d output=%d peak=%d err=%v", len(raw), size, a.peak, err)
			}
			heldGraph := a.used - uint64(len(raw))
			if heldGraph > uint64(size)+(128<<10) || a.peak > uint64(len(raw))*2+uint64(size)+(1<<20) {
				t.Fatalf("scratch/traversal retained as graph: graph=%d peak=%d input=%d", heldGraph, a.peak, len(raw))
			}
			if a.peak <= a.used+uint64(len(raw))/2 {
				t.Fatal("escaped scalar scratch was not separately admitted/released")
			}
			t.Logf("decoded_bytes=%d input_bytes=%d graph_held=%d admitted_peak=%d", size, len(raw), heldGraph, a.peak)
			release()
			if a.used != uint64(len(raw)) {
				t.Fatal("graph release retained its scalar work")
			}
			releaseInput()
			if a.used != 0 {
				t.Fatal("input/output lifetime leaked")
			}
		})
	}
	t.Run("dense-scalars-reuse-fixed-decoder-scratch", func(t *testing.T) {
		raw := []byte("[" + strings.Repeat("1,", 9999) + "1]")
		a := &scaleP21CRDTAdmission{limit: 4 << 20}
		got, release, err := DecodeJSON(t.Context(), a.acquire, raw)
		if err != nil {
			t.Fatal("dense legal scalars retained per-call decoder scratch", err, a.peak)
		}
		if len(got.([]any)) != 10000 {
			t.Fatal("dense scalar values changed")
		}
		release()
		if a.used != 0 {
			t.Fatal("dense scalar owner leaked")
		}
	})
	t.Run("output-does-not-alias-input", func(t *testing.T) {
		raw := []byte(`{"plain":"copied string","escaped":"\u003c\u003e","unicode":"\ud800\udc00"}`)
		var want any
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
		a := &scaleP21CRDTAdmission{}
		got, release, err := DecodeJSON(t.Context(), a.acquire, raw)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		for i := range raw {
			raw[i] = 'x'
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("normalized strings borrowed their temporary input")
		}
	})
}

func TestScaleP21BoundedCRDTJSONLinearProgressAndCancel(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(strings.Repeat(" ", 8<<20) + "null"),
		[]byte(`"` + strings.Repeat("a", 8<<20) + `"`),
		[]byte(`"` + strings.Repeat(`\u003c`, 1<<20) + `"`),
		[]byte(strings.Repeat(" ", 8<<20) + "true false"),
	} {
		base, cancel := context.WithCancel(t.Context())
		ctx := &scaleP21JSONProgressContext{Context: base, cancel: cancel}
		a := &scaleP21CRDTAdmission{limit: 64 << 20}
		var want any
		wantErr := json.Unmarshal(raw, &want)
		got, release, err := DecodeJSON(ctx, a.acquire, raw)
		if (err == nil) != (wantErr == nil) || err == nil && !reflect.DeepEqual(got, want) {
			t.Fatal("input-bounded traversal changed scalar/whitespace semantics", err, wantErr)
		}
		if calls := ctx.calls.Load(); calls > int64(len(raw)/512+1000) {
			t.Fatal("linear input scan retained per-byte iteration bookkeeping", calls, len(raw))
		}
		if release != nil {
			release()
		}
		cancel()
		if a.used != 0 {
			t.Fatal("linear scan owner leaked")
		}
		base, cancel = context.WithCancel(t.Context())
		ctx = &scaleP21JSONProgressContext{Context: base, cancel: cancel, cancelAt: 10}
		a = &scaleP21CRDTAdmission{limit: 64 << 20}
		_, release, err = DecodeJSON(ctx, a.acquire, raw)
		cancel()
		if release != nil {
			release()
		}
		if !errors.Is(err, context.Canceled) || a.used != 0 {
			t.Fatal("input-linear cancellation lost precedence or leaked", err, a.used)
		}
	}
}

func TestScaleP21BoundedCRDTJSONTemporaryFailureRelease(t *testing.T) {
	raw := []byte(`{"a":"` + strings.Repeat("a", 2<<20) + `","b":"` + strings.Repeat("b", 2<<20) + `"}`)
	for _, failure := range []int{2, 3, 4, 6, 7} {
		a := &scaleP21CRDTAdmission{limit: 32 << 20, failAt: failure}
		_, release, err := DecodeJSON(t.Context(), a.acquire, raw)
		if release != nil {
			release()
		}
		if !errors.Is(err, scaleP21CRDTBudget) || a.used != 0 {
			t.Fatal("partial graph/temporary admission refusal leaked", failure, a.calls, err, a.used)
		}
	}
	for _, suffix := range []string{`1e400,"tail":true}`, `1e400,"tail":`, `1e400,"tail":[1,]}`} {
		input := []byte(`{"before":"` + strings.Repeat("x", 2<<20) + `","huge":` + suffix)
		a := &scaleP21CRDTAdmission{limit: 32 << 20}
		got, release, err := DecodeJSON(t.Context(), a.acquire, input)
		if release != nil {
			release()
		}
		if !errors.Is(err, ErrInvalidUpdate) || got != nil || a.used != 0 {
			t.Fatal("invalid partial decode escaped or leaked", err, a.used)
		}
	}
	// Long numeric conversion errors own their input/error strings only for
	// the stdlib scalar call; they must not become an artificial quota error.
	input := []byte("1" + strings.Repeat("0", 2<<20))
	a := &scaleP21CRDTAdmission{limit: 32 << 20}
	_, release, err := DecodeJSON(t.Context(), a.acquire, input)
	if release != nil {
		release()
	}
	if !errors.Is(err, ErrInvalidUpdate) || a.used != 0 {
		t.Fatal("long numeric error scratch was retained or unadmitted", err, a.used)
	}
}

type scaleP21JSONProgressContext struct {
	context.Context
	cancel   context.CancelFunc
	cancelAt int64
	calls    atomic.Int64
}

func (c *scaleP21JSONProgressContext) Err() error {
	if c.calls.Add(1) == c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}
