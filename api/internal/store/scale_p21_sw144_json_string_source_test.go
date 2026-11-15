package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type scaleP21SW144Input struct {
	input  io.ReaderAt
	mu     sync.Mutex
	reads  int
	max    int
	err    error
	before func()
}

func (r *scaleP21SW144Input) ReadAt(p []byte, offset int64) (int, error) {
	r.mu.Lock()
	r.reads++
	r.max = max(r.max, len(p))
	err, before := r.err, r.before
	r.mu.Unlock()
	if before != nil {
		before()
	}
	if err != nil {
		return 0, err
	}
	return r.input.ReadAt(p, offset)
}

func scaleP21SW144Owner(t *testing.T) (*SQLiteStore, *ResourceRead) {
	t.Helper()
	view := scaleP21HotLogoStore(t, nil)
	owner := &ResourceRead{store: view}
	t.Cleanup(owner.Close)
	return view, owner
}

func scaleP21SW144Expected(t *testing.T, raw []byte, mode metadataJSONStringMode) []byte {
	t.Helper()
	switch mode {
	case metadataJSONStringQuoted:
		encoded, err := json.Marshal(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	case metadataJSONStringRawToken:
		return bytes.Clone(raw)
	case metadataJSONStringBase64:
		encoded, err := json.Marshal(base64.StdEncoding.EncodeToString(raw))
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	case metadataJSONStringCompactRaw:
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			t.Fatal(err)
		}
		var escaped bytes.Buffer
		json.HTMLEscape(&escaped, compact.Bytes())
		return escaped.Bytes()
	}
	t.Fatal("unknown test mode")
	return nil
}

func scaleP21SW144CompareAt(t *testing.T, source *metadataJSONStringSource, expected io.ReaderAt, offset int64, size int) {
	t.Helper()
	before, after := make([]byte, size), make([]byte, size)
	wantN, wantErr := expected.ReadAt(before, offset)
	gotN, gotErr := source.ReadAt(after, offset)
	if wantN != gotN || !bytes.Equal(before[:wantN], after[:gotN]) ||
		!errors.Is(gotErr, wantErr) || !errors.Is(wantErr, gotErr) {
		t.Fatalf("ReadAt differs offset=%d size=%d counts=%d/%d errors=%v/%v", offset, size, gotN, wantN, gotErr, wantErr)
	}
}

func TestScaleP21SW144JSONStringSourceExactBytesAndSeeks(t *testing.T) {
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	raws := [][]byte{
		nil, {}, []byte(""), []byte("plain /uploads/a?x=<>&\x00\n\r\t\b\f\"\\"),
		[]byte("\u4e2d\U0001f600\u2028\u2029\ufffd"), allBytes,
		{0xf0, 0x9f, 0x98}, {0xed, 0xa0, 0x80}, {0xc0, 0xaf}, {0xe2, 0x80},
		[]byte(strings.Repeat("A", metadataJSONStringCheckpointBytes-2) + "\U0001f600/uploads/a"),
		[]byte(strings.Repeat("A", metadataJSONStringBufferBytes+3) + "\U0001f600\u2028\u2029<&\x00/uploads/a"),
		[]byte(strings.Repeat("x", metadataJSONStringCheckpointBytes-4) + "/uploads/a"),
	}
	for _, mode := range []metadataJSONStringMode{metadataJSONStringQuoted, metadataJSONStringRawToken, metadataJSONStringBase64} {
		for _, raw := range raws {
			view, owner := scaleP21SW144Owner(t)
			input := &scaleP21SW144Input{input: bytes.NewReader(raw), before: func() {
				if view.MetadataWorkUsedForTesting() == 0 {
					t.Error("read happened before scratch/index admission")
				}
			}}
			source, err := newMetadataJSONStringSource(t.Context(), owner, input, int64(len(raw)), mode)
			if err != nil {
				t.Fatal(mode, len(raw), err)
			}
			want := scaleP21SW144Expected(t, raw, mode)
			if source.Size() != int64(len(want)) || source.ContainsUploads() != bytes.Contains(want, []byte("/uploads/")) {
				t.Fatal("encoded size/marker differs", mode, len(raw), source.Size(), len(want))
			}
			got, err := io.ReadAll(io.NewSectionReader(source, 0, source.Size()))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("complete encoded bytes differ", mode, len(raw), err)
			}
			expected := bytes.NewReader(want)
			if len(want) < 2048 {
				for offset := int64(0); offset <= int64(len(want))+1; offset++ {
					for _, size := range []int{0, 1, 2, 3, 5, 17, len(want) + 2} {
						scaleP21SW144CompareAt(t, source, expected, offset, size)
					}
				}
			}
			random := rand.New(rand.NewSource(144))
			for range 100 {
				scaleP21SW144CompareAt(t, source, expected, int64(random.Intn(len(want)+2)), random.Intn(1025))
			}
			if n, err := source.ReadAt(make([]byte, 1), -1); n != 0 || !errors.Is(err, os.ErrInvalid) {
				t.Fatal("negative offset accepted", n, err)
			}
			if len(source.checkpoints) > len(raw)/metadataJSONStringCheckpointBytes+2 || input.max > metadataJSONStringBufferBytes+4 {
				t.Fatal("unbounded staging/index scratch", mode, input.max, len(source.checkpoints))
			}
			source.Close()
			source.Close()
			owner.Close()
			if source.Size() != 0 || source.ContainsUploads() || view.MetadataWorkUsedForTesting() != 0 ||
				source.input != nil || source.buffer != nil || source.encoded != nil || source.checkpoints != nil || source.ctx != nil ||
				source.scope != nil || source.previous != nil || source.next != nil {
				t.Fatal("closed source retained input/index/work")
			}
		}
	}
	// This marker exists only in the encoded base64 view, across its block edge.
	raw, err := base64.StdEncoding.DecodeString(strings.Repeat("A", metadataJSONStringBufferBytes-4) + "/uploads/AAA")
	if err != nil {
		t.Fatal(err)
	}
	_, owner := scaleP21SW144Owner(t)
	source, err := newMetadataJSONStringSource(t.Context(), owner, bytes.NewReader(raw), int64(len(raw)), metadataJSONStringBase64)
	if err != nil || !source.ContainsUploads() || bytes.Contains(raw, []byte("/uploads/")) {
		t.Fatal("base64 marker was inferred from input rather than encoded blocks", err)
	}
}

func TestScaleP21SW144JSONStringCompactRawExact(t *testing.T) {
	chunked, err := json.Marshal(strings.Repeat("\\ \" /uploads/a ", 2000))
	if err != nil {
		t.Fatal(err)
	}
	inputs := [][]byte{
		[]byte(` { "a" : -0, "a":1.000e+09, "escaped":"\/uploads\/a", "url" : "/uploads/a?<>&" } `),
		[]byte(" \n [ \"\u4e2d\U0001f600\u2028\u2029\", \"\\\\\\\"\", \"\\ud800\", \"\\udc00\", null, true, false ]\t "),
		[]byte("\"invalid UTF8 \xff remains lexical\""),
		chunked,
		[]byte(strings.Repeat(" \n\t\r", 10000) + `{}` + strings.Repeat(" \n\t\r", 10000)),
	}
	for _, raw := range inputs {
		view, owner := scaleP21SW144Owner(t)
		source, err := newMetadataJSONStringSource(t.Context(), owner, bytes.NewReader(raw), int64(len(raw)), metadataJSONStringCompactRaw)
		if err != nil {
			t.Fatal(err)
		}
		want := scaleP21SW144Expected(t, raw, metadataJSONStringCompactRaw)
		got, err := io.ReadAll(io.NewSectionReader(source, 0, source.Size()))
		if err != nil || !bytes.Equal(got, want) || source.ContainsUploads() != bytes.Contains(want, []byte("/uploads/")) {
			t.Fatal("compaction changed whitespace/number/escape lexical bytes", err)
		}
		random := rand.New(rand.NewSource(144))
		for range 300 {
			scaleP21SW144CompareAt(t, source, bytes.NewReader(want), int64(random.Intn(len(want)+2)), random.Intn(1025))
		}
		source.Close()
		owner.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("compact index leaked")
		}
	}
	for _, raw := range []string{"", " \n ", "1 2", `{"a":1,}`, `"unterminated`, `"bad\q"`, `{"a":01}`, `"\uZZZZ"`} {
		var oracle bytes.Buffer
		if json.Compact(&oracle, []byte(raw)) == nil {
			t.Fatal("invalid test fixture")
		}
		view, owner := scaleP21SW144Owner(t)
		source, err := newMetadataJSONStringSource(t.Context(), owner, strings.NewReader(raw), int64(len(raw)), metadataJSONStringCompactRaw)
		if err == nil || source != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("invalid whole RawMessage was published or retained work", source, err)
		}
	}
}

type scaleP21SW144Repeated struct {
	prefix, pattern, suffix []byte
	repeats                 int64
}

func (r scaleP21SW144Repeated) Size() int64 {
	return int64(len(r.prefix)) + int64(len(r.pattern))*r.repeats + int64(len(r.suffix))
}

func (r scaleP21SW144Repeated) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, os.ErrInvalid
	}
	if offset >= r.Size() {
		return 0, io.EOF
	}
	n := 0
	middle := int64(len(r.prefix)) + int64(len(r.pattern))*r.repeats
	for n < len(p) && offset < r.Size() {
		switch {
		case offset < int64(len(r.prefix)):
			p[n] = r.prefix[offset]
		case offset < middle:
			p[n] = r.pattern[(offset-int64(len(r.prefix)))%int64(len(r.pattern))]
		default:
			p[n] = r.suffix[offset-middle]
		}
		n++
		offset++
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func TestScaleP21SW144JSONStringGiantWithoutStaging(t *testing.T) {
	pattern := []byte("abc/uploads/a&<")
	repeats := int64((8 << 20) / len(pattern))
	quoted := scaleP21SW144Expected(t, pattern, metadataJSONStringQuoted)
	for _, mode := range []metadataJSONStringMode{metadataJSONStringQuoted, metadataJSONStringRawToken, metadataJSONStringBase64, metadataJSONStringCompactRaw} {
		raw := scaleP21SW144Repeated{pattern: pattern, repeats: repeats}
		expected := raw
		switch mode {
		case metadataJSONStringQuoted:
			expected.prefix, expected.pattern, expected.suffix = []byte(`"`), quoted[1:len(quoted)-1], []byte(`"`)
		case metadataJSONStringBase64:
			expected.prefix, expected.pattern, expected.suffix = []byte(`"`), []byte(base64.StdEncoding.EncodeToString(pattern)), []byte(`"`)
		case metadataJSONStringCompactRaw:
			raw.prefix, raw.suffix = []byte(" { \"body\" : \""), []byte("\" } \n")
			expected.prefix, expected.pattern, expected.suffix = []byte(`{"body":"`), quoted[1:len(quoted)-1], []byte(`"}`)
		}
		view, owner := scaleP21SW144Owner(t)
		input := &scaleP21SW144Input{input: raw}
		source, err := newMetadataJSONStringSource(t.Context(), owner, input, raw.Size(), mode)
		if err != nil {
			t.Fatal(mode, err)
		}
		if source.Size() != expected.Size() || len(source.checkpoints) > int(raw.Size()/metadataJSONStringCheckpointBytes)+2 ||
			input.max > metadataJSONStringBufferBytes+4 {
			t.Fatal("giant source staged body-sized work", mode, input.max, len(source.checkpoints))
		}
		random := rand.New(rand.NewSource(144))
		for range 100 {
			scaleP21SW144CompareAt(t, source, expected, random.Int63n(expected.Size()+2), random.Intn(2049))
		}
		t.Log("mode / raw bytes / encoded bytes / admitted work / checkpoints / max read", mode, raw.Size(), source.Size(), view.MetadataWorkUsedForTesting(), len(source.checkpoints), input.max)
		owner.Close()
		if view.MetadataWorkUsedForTesting() != 0 || source.Size() != 0 {
			t.Fatal("parent close retained a giant source")
		}
	}
}

func TestScaleP21SW144JSONStringFailuresAndLifetime(t *testing.T) {
	inputError := errors.New("SW144 input failure")
	for _, mode := range []metadataJSONStringMode{metadataJSONStringQuoted, metadataJSONStringRawToken, metadataJSONStringBase64, metadataJSONStringCompactRaw} {
		view, owner := scaleP21SW144Owner(t)
		raw := []byte(`"ok"`)
		limit := view.metadataResidency.work.limit
		view.metadataResidency.work.limit = 32 << 10
		input := &scaleP21SW144Input{input: bytes.NewReader(raw)}
		if source, err := newMetadataJSONStringSource(t.Context(), owner, input, int64(len(raw)), mode); source != nil || !errors.Is(err, ErrMetadataWorkBudget) || input.reads != 0 {
			t.Fatal("quota failed after input/index allocation", mode, source, err, input.reads)
		}
		view.metadataResidency.work.limit = limit
		input.err = inputError
		if source, err := newMetadataJSONStringSource(t.Context(), owner, input, int64(len(raw)), mode); source != nil || !errors.Is(err, inputError) {
			t.Fatal("input error was swallowed", mode, source, err)
		}
		input.err = nil
		if source, err := newMetadataJSONStringSource(t.Context(), owner, input, 100, mode); source != nil || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal("declared input size exceeded available bytes", mode, source, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		input.before = cancel
		if source, err := newMetadataJSONStringSource(ctx, owner, input, int64(len(raw)), mode); source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("factory cancellation published a source", mode, source, err)
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("factory error retained an allowance")
		}
		input.before = nil
		ctx, cancel = context.WithCancel(t.Context())
		source, err := newMetadataJSONStringSource(ctx, owner, input, int64(len(raw)), mode)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		if n, err := source.ReadAt(make([]byte, 1), 0); n != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled view remained readable", n, err)
		}
		source.Close()
		large := []byte(`"` + strings.Repeat("x", 4*metadataJSONStringBufferBytes) + `"`)
		failing := &scaleP21SW144Input{input: bytes.NewReader(large)}
		source, err = newMetadataJSONStringSource(t.Context(), owner, failing, int64(len(large)), mode)
		if err != nil {
			t.Fatal(err)
		}
		failing.err = inputError
		if _, err := source.ReadAt(make([]byte, 1), 1); !errors.Is(err, inputError) {
			t.Fatal("seek/replay input error was swallowed", mode, err)
		}
		source.Close()
		owner.Close()
		if _, err := newMetadataJSONStringSource(t.Context(), owner, input, int64(len(raw)), mode); !errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("closed output owner acquired new work", mode, err)
		}
	}
	for _, mode := range []metadataJSONStringMode{metadataJSONStringQuoted, metadataJSONStringBase64, metadataJSONStringCompactRaw} {
		view, owner := scaleP21SW144Owner(t)
		input := &scaleP21SW144Input{input: strings.NewReader("")}
		if source, err := newMetadataJSONStringSource(t.Context(), owner, input, math.MaxInt64, mode); source != nil || !errors.Is(err, ErrMetadataWorkBudget) ||
			input.reads != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("giant index/encoded offset escaped pre-admission cap", mode, source, err)
		}
	}
	view, owner := scaleP21SW144Owner(t)
	input := &scaleP21SW144Input{input: strings.NewReader("data"), before: owner.Close}
	if source, err := newMetadataJSONStringSource(t.Context(), owner, input, 4, metadataJSONStringQuoted); source != nil ||
		!errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("parent closed during indexing retained unpublished work", source, err)
	}
}

func TestScaleP21SW144JSONStringConcurrentReadsAndClose(t *testing.T) {
	raw, err := json.Marshal(strings.Repeat("\u4e2d /uploads/a?x=<>& \x00 \"\\", 4000))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []metadataJSONStringMode{metadataJSONStringQuoted, metadataJSONStringRawToken, metadataJSONStringBase64, metadataJSONStringCompactRaw} {
		view, owner := scaleP21SW144Owner(t)
		source, err := newMetadataJSONStringSource(t.Context(), owner, bytes.NewReader(raw), int64(len(raw)), mode)
		if err != nil {
			t.Fatal(err)
		}
		expected := bytes.NewReader(scaleP21SW144Expected(t, raw, mode))
		var jobs sync.WaitGroup
		for worker := range 4 {
			jobs.Go(func() {
				random := rand.New(rand.NewSource(int64(144 + worker)))
				for range 40 {
					scaleP21SW144CompareAt(t, source, expected, random.Int63n(source.Size()+1), random.Intn(2049))
				}
			})
		}
		jobs.Wait()
		jobs.Go(owner.Close)
		jobs.Go(source.Close)
		jobs.Go(source.Close)
		jobs.Wait()
		if n, err := source.ReadAt(make([]byte, 1), 0); n != 0 || !errors.Is(err, errMetadataLogicalClosed) ||
			view.MetadataWorkUsedForTesting() != 0 || source.input != nil || source.checkpoints != nil || source.buffer != nil {
			t.Fatal("concurrent close retained usable source/work", n, err)
		}
	}
}

func TestScaleP21SW144JSONStringScopeRetainsOnlyActiveViews(t *testing.T) {
	view, owner := scaleP21SW144Owner(t)
	scopeWork := 0
	for range 1000 {
		source, err := newMetadataJSONStringSource(t.Context(), owner, strings.NewReader("/uploads/a"), 10, metadataJSONStringQuoted)
		if err != nil {
			t.Fatal(err)
		}
		if owner.jsonStrings == nil || len(owner.release) != 1 {
			t.Fatal("JSON leaves appended individual owner closers")
		}
		source.Close()
		owner.jsonStrings.mu.Lock()
		head := owner.jsonStrings.head
		owner.jsonStrings.mu.Unlock()
		if scopeWork == 0 {
			scopeWork = view.MetadataWorkUsedForTesting()
		}
		if head != nil || scopeWork == 0 || view.MetadataWorkUsedForTesting() != scopeWork ||
			source.scope != nil || source.previous != nil || source.next != nil {
			t.Fatal("closed leaves accumulated registry entries or allowance", scopeWork, view.MetadataWorkUsedForTesting())
		}
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("shared registry descriptor allowance leaked")
	}
}

func TestScaleP21SW144JSONStringScopeConcurrentDrainAndRelease(t *testing.T) {
	view, owner := scaleP21SW144Owner(t)
	sources := make([]*metadataJSONStringSource, 24)
	for i := range sources {
		var err error
		sources[i], err = newMetadataJSONStringSource(t.Context(), owner, strings.NewReader("/uploads/a"), 10, metadataJSONStringQuoted)
		if err != nil {
			t.Fatal(err)
		}
	}
	scope := owner.jsonStrings
	for _, i := range []int{8, 0, 23, 15, 4} {
		sources[i].Close()
	}
	scope.mu.Lock()
	count := 0
	for source := scope.head; source != nil; source = source.next {
		count++
	}
	scope.mu.Unlock()
	if count != len(sources)-5 {
		t.Fatal("middle/head/tail removal corrupted active registry", count)
	}
	var jobs sync.WaitGroup
	jobs.Go(owner.Close)
	jobs.Go(scope.Close)
	for _, source := range sources {
		jobs.Go(source.Close)
	}
	jobs.Wait()
	if view.MetadataWorkUsedForTesting() != 0 || scope.head != nil {
		t.Fatal("concurrent registry drain retained source work")
	}
	for _, source := range sources {
		if source.Size() != 0 || source.scope != nil || source.previous != nil || source.next != nil {
			t.Fatal("registry drain left a live or linked source")
		}
	}

	view, owner = scaleP21SW144Owner(t)
	source, err := newMetadataJSONStringSource(t.Context(), owner, strings.NewReader("value"), 5, metadataJSONStringQuoted)
	if err != nil {
		t.Fatal(err)
	}
	scope = owner.jsonStrings
	releasing, allowRelease, sourceReleased, scopeReleased := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	sourceRelease := source.release
	source.release = func() {
		close(releasing)
		<-allowRelease
		sourceRelease()
		close(sourceReleased)
	}
	scopeRelease := scope.release
	scope.release = func() {
		select {
		case <-sourceReleased:
		default:
			t.Error("registry descriptor released before its closing leaf")
		}
		scopeRelease()
		close(scopeReleased)
	}
	sourceDone, ownerDone := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(allowRelease) })
	go func() { source.Close(); close(sourceDone) }()
	<-releasing
	go func() { owner.Close(); close(ownerDone) }()
	select {
	case <-scopeReleased:
		t.Error("owner completed while a leaf release was still active")
	case <-time.After(100 * time.Millisecond):
	}
	unblock.Do(func() { close(allowRelease) })
	<-sourceDone
	<-ownerDone
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("completed source/registry release leaked work")
	}
}
