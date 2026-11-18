package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// scaleP21GenericJSONCostRescan is the former range-per-node measurement,
// kept verbatim as the oracle for the single-pass measurement.
func scaleP21GenericJSONCostRescan(ctx context.Context, typ reflect.Type, part legacySessionJSONPart, work *metadataAggregateTraversal) (uint64, error) {
	if !metadataGenericJSONType(typ) {
		return 0, ErrBoundedMetadataCoverage
	}
	if err := work.enter(); err != nil {
		return 0, err
	}
	defer work.leave()
	cost := uint64(typ.Size())*2 + 128
	add := func(size uint64) error {
		if size > metadataWorkBytes || cost > metadataWorkBytes-size {
			return ErrMetadataWorkBudget
		}
		cost += size
		return nil
	}
	prefix, err := metadataGenericJSONPrefix(ctx, part)
	if err != nil || prefix == 'n' {
		return cost, err
	}
	if typ.Kind() == reflect.Interface {
		child, err := scaleP21GenericJSONCostRescan(ctx, metadataGenericJSONDynamic(prefix), part, work)
		if err != nil {
			return 0, err
		}
		if err := add(child); err != nil {
			return 0, err
		}
		return cost, nil
	}
	if metadataGenericJSONDynamic(prefix) != typ {
		return 0, ErrMetadataRecordQuery
	}
	scanner := metadataGenericJSONScanner(ctx, part)
	switch typ.Kind() {
	case reflect.String:
		if err := add(64); err != nil {
			return 0, err
		}
		_, err = VisitMetadataJSONStringChunks(ctx, part.reader(), func(value []byte) error {
			return add(uint64(len(value)) * 2)
		})
	case reflect.Map:
		err = scanner.object(func(key, value metadataCollectionRange) error {
			if err := add(uint64(typ.Elem().Size())*2 + 64); err != nil {
				return err
			}
			for _, child := range []struct {
				typ  reflect.Type
				part metadataCollectionRange
			}{{typ.Key(), key}, {typ.Elem(), value}} {
				size, err := scaleP21GenericJSONCostRescan(ctx, child.typ, legacySessionJSONPart{part.input, child.part.offset, child.part.size}, work)
				if err != nil {
					return err
				}
				if err := add(size); err != nil {
					return err
				}
			}
			return nil
		}, 0)
	case reflect.Slice:
		err = scanner.array(func(value metadataCollectionRange) error {
			if err := add(uint64(typ.Elem().Size())*2 + 64); err != nil {
				return err
			}
			size, err := scaleP21GenericJSONCostRescan(ctx, typ.Elem(), legacySessionJSONPart{part.input, value.offset, value.size}, work)
			if err != nil {
				return err
			}
			return add(size)
		}, 0)
	}
	return cost, err
}

func scaleP21GenericJSONCostRandom(r *rand.Rand, depth int) any {
	choice := r.Intn(9)
	if depth <= 0 {
		choice = r.Intn(5)
	}
	switch choice {
	case 0:
		return nil
	case 1:
		return r.Intn(2) == 0
	case 2:
		return []float64{0, -1.5, 1e-7, 9007199254740993, 3}[r.Intn(5)]
	case 3, 4:
		return []string{"", "a", "\x00<>& ", "é漢字", strings.Repeat("x", r.Intn(5000)), "\"\\/\b\f\n\r\t"}[r.Intn(6)]
	case 5, 6:
		items := make([]any, r.Intn(5))
		for index := range items {
			items[index] = scaleP21GenericJSONCostRandom(r, depth-1)
		}
		return items
	default:
		items := map[string]any{}
		for index := range r.Intn(5) {
			items[fmt.Sprintf("k%dé\"%d", index, r.Intn(3))] = scaleP21GenericJSONCostRandom(r, depth-1)
		}
		return items
	}
}

// The single-pass measurement must return the former value and error for
// every generic type over normalized ranges, whitespace-separated ranges,
// invalid ranges' error presence and deep chains.
func TestScaleP21GenericJSONCostSinglePass(t *testing.T) {
	repo := scaleP21SW248Repo(t)
	types := []reflect.Type{
		reflect.TypeFor[any](), reflect.TypeFor[map[string]any](), reflect.TypeFor[[]any](),
		reflect.TypeFor[string](), reflect.TypeFor[bool](), reflect.TypeFor[float64](), reflect.TypeFor[int](),
	}
	measure := func(ctx context.Context, typ reflect.Type, raw string, rescan bool) (uint64, error) {
		var cost uint64
		err := metadataGenericJSONWork(ctx, repo, func(work *metadataAggregateTraversal) error {
			part := legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))}
			var err error
			if rescan {
				cost, err = scaleP21GenericJSONCostRescan(ctx, typ, part, work)
			} else {
				cost, err = metadataGenericJSONCost(ctx, typ, part, work)
			}
			return err
		})
		return cost, err
	}
	same := func(t *testing.T, ctx context.Context, raw string, valid bool) {
		t.Helper()
		for _, typ := range types {
			want, wantErr := measure(ctx, typ, raw, true)
			got, gotErr := measure(ctx, typ, raw, false)
			if valid && (want != got || !errors.Is(gotErr, wantErr) && fmt.Sprint(gotErr) != fmt.Sprint(wantErr)) {
				t.Fatalf("generic JSON cost differs type=%s raw=%.200q cost=%d/%d err=%v/%v", typ, raw, want, got, wantErr, gotErr)
			}
			if !valid && (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("single-pass cost changed acceptance type=%s raw=%.200q err=%v/%v", typ, raw, wantErr, gotErr)
			}
		}
	}
	r := rand.New(rand.NewSource(248))
	for index := range 400 {
		raw, err := json.Marshal(scaleP21GenericJSONCostRandom(r, 1+index%6))
		if err != nil {
			t.Fatal(err)
		}
		same(t, t.Context(), string(raw), true)
		indented, err := json.MarshalIndent(json.RawMessage(raw), " ", "\t")
		if err != nil {
			t.Fatal(err)
		}
		same(t, t.Context(), string(indented), true)
	}
	for _, raw := range []string{
		`null`, `{}`, `[]`, `""`, `true`, `false`, `-0`, `1e-7`, `{"a":null,"b":[null,{}]}`, `[{"x":"\ud800"}]`,
		`{"same":{"old":1},"same":{"new":2}}`, ` {"a" : [ 1 , "x" ] } `,
		strings.Repeat(`{"next":[`, 256) + `{"id":"deep","n":-0,"empty":{},"null":null}` + strings.Repeat(`]}`, 256),
		`[` + strings.Repeat(`[`, 600) + `"leaf"` + strings.Repeat(`]`, 600) + `]`,
	} {
		same(t, t.Context(), raw, true)
	}
	for _, raw := range []string{``, `{`, `[1,`, `{"a"}`, `{"a":1,}`, `[1 2]`, `"unterminated`} {
		same(t, t.Context(), raw, false)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, typ := range types {
		for _, measure := range []func(context.Context, reflect.Type, legacySessionJSONPart, *metadataAggregateTraversal) (uint64, error){
			scaleP21GenericJSONCostRescan, metadataGenericJSONCost,
		} {
			work, err := beginMetadataAggregateTraversal(t.Context(), func(uint64) (func(), error) { return func() {}, nil })
			if err != nil {
				t.Fatal(err)
			}
			raw := `{"a":[1]}`
			work.ctx = cancelled
			_, err = measure(cancelled, typ, legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))}, work)
			work.Close()
			if !errors.Is(err, context.Canceled) && typ != reflect.TypeFor[int]() {
				t.Fatal("cancelled cost measurement changed its error", typ, err)
			}
		}
	}

	// One pass per measured range: a deep chain reads its bytes a bounded
	// number of times instead of once per enclosing container.
	deep := strings.Repeat(`{"next":[`, 1000) + `{"id":"deep","text":"` + strings.Repeat("x", 4096) + `"}` + strings.Repeat(`]}`, 1000)
	input := &scaleP21GenericJSONCountingReader{ReaderAt: strings.NewReader(deep)}
	err := metadataGenericJSONWork(t.Context(), repo, func(work *metadataAggregateTraversal) error {
		_, err := metadataGenericJSONCost(t.Context(), reflect.TypeFor[map[string]any](), legacySessionJSONPart{input, 0, int64(len(deep))}, work)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.read > 3*int64(len(deep))+64<<10 {
		t.Fatal("deep generic JSON cost rescans nested ranges", input.read, len(deep))
	}
}

type scaleP21GenericJSONCountingReader struct {
	io.ReaderAt
	read int64
}

func (r *scaleP21GenericJSONCountingReader) ReadAt(buffer []byte, offset int64) (int, error) {
	count, err := r.ReaderAt.ReadAt(buffer, offset)
	r.read += int64(count)
	return count, err
}
