package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

type scaleP21SW15JSONStringReader struct {
	input io.Reader
	max   int
}

func (r *scaleP21SW15JSONStringReader) Read(output []byte) (int, error) {
	return r.input.Read(output[:min(len(output), r.max)])
}

func scaleP21SW15JSONStringDifferential(t *testing.T, raw []byte, readBytes int) {
	t.Helper()
	var expected string
	legacyErr := json.Unmarshal(raw, &expected)
	var actual bytes.Buffer
	input := &scaleP21SW15JSONStringReader{input: bytes.NewReader(raw), max: readBytes}
	null, err := VisitMetadataJSONStringChunks(t.Context(), input, func(chunk []byte) error {
		if len(chunk) == 0 || len(chunk) > MetadataJSONStringChunkBytes || !utf8.Valid(chunk) {
			t.Fatalf("invalid decoded chunk length=%d utf8=%v", len(chunk), utf8.Valid(chunk))
		}
		_, err := actual.Write(chunk)
		return err
	})
	if (err == nil) != (legacyErr == nil) {
		t.Fatalf("error parity raw=%q length=%d legacy=%v bounded=%v", raw[:min(len(raw), 128)], len(raw), legacyErr, err)
	}
	if err == nil {
		if actual.String() != expected {
			t.Fatalf("decoded bytes differ raw=%q length=%d actual=%q expected=%q", raw[:min(len(raw), 128)], len(raw), actual.Bytes(), expected)
		}
		if null != bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			t.Fatalf("null classification=%v raw=%q", null, raw[:min(len(raw), 128)])
		}
	}
}

func TestScaleP21SW0JSONStringDifferential(t *testing.T) {
	fixtures := [][]byte{
		[]byte(`""`),
		[]byte(`"plain text"`),
		[]byte(`"\"\\\/\b\f\n\r\t"`),
		[]byte(`"\u0000\u001f\u007f\u0080\u07ff\u0800\ud7ff\ue000\uffff"`),
		[]byte(`"\uD800\uDC00\uDBFF\uDFFF"`),
		[]byte(`"\ud800"`),
		[]byte(`"\udfff"`),
		[]byte(`"\ud800\ud800\udc00"`),
		[]byte(`"\udc00\ud800\udc00"`),
		[]byte(`"\ud800\\udc00"`),
		[]byte(`"\ud800\u0041"`),
		[]byte(`"\ud800\udfff"`),
		[]byte(`null`),
		[]byte(" \r\n\t null \t\r\n "),
		[]byte(" \r\n\t \"spaced\" \t\r\n "),
		[]byte(`"null"`),
		[]byte(`true`),
		[]byte(`false`),
		[]byte(`0`),
		[]byte(`[]`),
		[]byte(`{}`),
		[]byte(``),
		[]byte(`"`),
		[]byte(`"unterminated`),
		[]byte(`"ending\`),
		[]byte(`"\v"`),
		[]byte(`"\'"`),
		[]byte(`"\u"`),
		[]byte(`"\u001"`),
		[]byte(`"\u00xz"`),
		[]byte(`"\ud800\u12"`),
		[]byte(`"\ud800\uGGGG"`),
		[]byte(`"first" "second"`),
		[]byte(`null[]`),
		[]byte(`"valid"invalid`),
	}
	for _, malformed := range [][]byte{
		{0x80}, {0xc0, 0xaf}, {0xe0, 0x80, 0xaf}, {0xed, 0xa0, 0x80},
		{0xf4, 0x90, 0x80, 0x80}, {0xe2}, {0xe2, 0x82}, {0xff, 0xfe},
		{0xe2, 'x', 0x82}, {0xef, 0xbf, 0xbd},
	} {
		fixtures = append(fixtures, append(append([]byte{'"'}, malformed...), '"'))
	}
	for control := byte(0); control < 0x20; control++ {
		fixtures = append(fixtures, []byte{'"', 'a', control, 'b', '"'})
	}
	for _, high := range []rune{0xd800, 0xd801, 0xda00, 0xdbff} {
		for _, low := range []rune{0xdc00, 0xdc01, 0xdf00, 0xdfff} {
			fixtures = append(fixtures, []byte(fmt.Sprintf(`"\u%04x\u%04x"`, high, low)))
		}
	}
	for index, raw := range fixtures {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			for _, readBytes := range []int{1, 2, 3, 4, 5, 6, 7, 31, 4096} {
				scaleP21SW15JSONStringDifferential(t, raw, readBytes)
			}
		})
	}
}

func TestScaleP21SW0JSONStringChunkBoundaries(t *testing.T) {
	for _, prefix := range []int{4091, 4092, 4093, 4094, 4095, 4096, 4097} {
		raw := []byte(`"` + strings.Repeat("a", prefix) + `\uD834\uDD1E` +
			strings.Repeat("b", prefix) + string(rune(0x1f600)) +
			strings.Repeat("c", prefix) + `\ud800` +
			strings.Repeat("d", prefix) + `"`)
		for _, readBytes := range []int{1, 2, 3, 5, 4093, 4096, 4097} {
			scaleP21SW15JSONStringDifferential(t, raw, readBytes)
		}
	}
	// Malformed source after emitted chunks must still be rejected even when
	// a consumer stops retaining bytes after its own prefix limit.
	raw := []byte(`"` + strings.Repeat("a", MetadataJSONStringChunkBytes*3) + `\x"`)
	var prefix []byte
	_, err := VisitMetadataJSONStringChunks(t.Context(), bytes.NewReader(raw), func(chunk []byte) error {
		if len(prefix) < 2000 {
			prefix = append(prefix, chunk[:min(len(chunk), 2000-len(prefix))]...)
		}
		return nil
	})
	if err == nil || len(prefix) != 2000 {
		t.Fatalf("suffix validation error=%v retained=%d", err, len(prefix))
	}
}

func TestScaleP21SW0JSONStringRandomDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(2131501))
	for index := range 2000 {
		payload := make([]byte, random.Intn(257))
		if _, err := random.Read(payload); err != nil {
			t.Fatal(err)
		}
		var raw []byte
		switch index % 4 {
		case 0:
			raw = payload
		case 1:
			raw = append(append([]byte{'"'}, payload...), '"')
		case 2:
			var err error
			raw, err = json.Marshal(string(payload))
			if err != nil {
				t.Fatal(err)
			}
		case 3:
			raw = []byte(fmt.Sprintf(`"\u%04x\u%04x"`, random.Intn(65536), random.Intn(65536)))
		}
		scaleP21SW15JSONStringDifferential(t, raw, random.Intn(31)+1)
	}
}

type scaleP21SW15JSONStringReadFailure struct {
	input io.Reader
	err   error
}

func (r scaleP21SW15JSONStringReadFailure) Read(output []byte) (int, error) {
	n, err := r.input.Read(output)
	if errors.Is(err, io.EOF) {
		err = r.err
	}
	return n, err
}

func TestScaleP21SW0JSONStringCancellationAndFailures(t *testing.T) {
	raw := []byte(`"` + strings.Repeat("x", MetadataJSONStringChunkBytes*3) + `"`)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := VisitMetadataJSONStringChunks(cancelled, bytes.NewReader(raw), func([]byte) error {
		t.Fatal("already-cancelled decoder invoked visitor")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("initial cancellation=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	if _, err := VisitMetadataJSONStringChunks(ctx, bytes.NewReader(raw), func([]byte) error {
		calls++
		cancel()
		return nil
	}); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("mid-stream cancellation=%v callbacks=%d", err, calls)
	}
	callbackErr := errors.New("owned callback failed")
	calls = 0
	if _, err := VisitMetadataJSONStringChunks(t.Context(), bytes.NewReader(raw), func([]byte) error {
		calls++
		return callbackErr
	}); !errors.Is(err, callbackErr) || calls != 1 {
		t.Fatalf("callback failure=%v callbacks=%d", err, calls)
	}
	readerErr := errors.New("owned reader failed")
	if _, err := VisitMetadataJSONStringChunks(t.Context(), scaleP21SW15JSONStringReadFailure{
		input: strings.NewReader(`"complete"`), err: readerErr,
	}, func([]byte) error { return nil }); !errors.Is(err, readerErr) {
		t.Fatalf("reader failure=%v", err)
	}
	if _, err := VisitMetadataJSONStringChunks(t.Context(), bytes.NewReader(raw), nil); !errors.Is(err, ErrMetadataJSONString) {
		t.Fatalf("nil visitor=%v", err)
	}
}

func TestScaleP21SW0JSONStringAllocationBound(t *testing.T) {
	small := []byte(`"short"`)
	literal := []byte(`"` + strings.Repeat("a", 2<<20) + `"`)
	escaped := []byte(`"` + strings.Repeat(`\uD834\uDD1E`, 1<<17) + `"`)
	measure := func(raw []byte) uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := VisitMetadataJSONStringChunks(t.Context(), bytes.NewReader(raw), func([]byte) error { return nil })
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		return after.TotalAlloc - before.TotalAlloc
	}
	baseline := measure(small)
	for _, raw := range [][]byte{literal, escaped} {
		allocated := measure(raw)
		if allocated > baseline+(32<<10) || allocated > 64<<10 {
			t.Fatalf("string-sized allocation raw=%d baseline=%d allocated=%d", len(raw), baseline, allocated)
		}
		t.Logf("raw=%d decoded-stream allocated=%d baseline=%d", len(raw), allocated, baseline)
	}
}

func FuzzScaleP21SW0JSONStringChunks(f *testing.F) {
	for _, raw := range [][]byte{
		[]byte(`""`), []byte(`null`), []byte(`"\uD834\uDD1E"`), []byte(`"\ud800"`),
		[]byte{'"', 0xe2, 0x82, '"'}, []byte(`"bad\x"`), []byte(`"first" "second"`),
	} {
		f.Add(raw, byte(1))
	}
	f.Fuzz(func(t *testing.T, raw []byte, readBytes byte) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		scaleP21SW15JSONStringDifferential(t, raw, int(readBytes)%31+1)
	})
}
