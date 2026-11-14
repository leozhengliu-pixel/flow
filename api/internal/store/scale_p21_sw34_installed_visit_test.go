package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW34Row struct {
	ID     string    `json:"id"`
	Body   string    `json:"body"`
	At     time.Time `json:"at"`
	Nil    []string  `json:"nil"`
	Empty  []string  `json:"empty"`
	Number any       `json:"number"`
}

func scaleP21SW34MemoryRead(t *testing.T, settings map[string]any) (*InstalledMetadataRead, *SQLiteStore) {
	t.Helper()
	return scaleP21SW34DataRead(t, domain.Bootstrap{Settings: settings})
}

func scaleP21SW34DataRead(t *testing.T, data domain.Bootstrap) (*InstalledMetadataRead, *SQLiteStore) {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	store := &SQLiteStore{metadataResidency: newMetadataResidency()}
	_, release, err := store.tryAdmitAdditionalMetadataWork(t.Context(), metadataInstalledVisitWork)
	if err != nil {
		source.Release()
		t.Fatal(err)
	}
	read := &InstalledMetadataRead{
		store: store, workspace: "sw34-memory", source: source, release: release,
		selections: map[*OwnedMetadataSelection]struct{}{}, reductions: map[*OwnedMetadataReduction]struct{}{},
	}
	t.Cleanup(func() {
		read.Close()
		if store.metadataResidency.work.used != 0 {
			t.Error("installed visit/reduction leaked work")
		}
	})
	return read, store
}

func scaleP21SW34Entries(t *testing.T, read *InstalledMetadataRead, ctx context.Context, path ...string) []any {
	t.Helper()
	result, err := read.Reduce(ctx, 2<<20, func(ctx context.Context) (any, error) {
		var got []any
		_, err := read.Visit(ctx, path, func(ctx context.Context, entry InstalledMetadataEntry) error {
			return entry.Value.WithValue(ctx, 64<<10, func(value any) error {
				got = append(got, value)
				return nil
			})
		})
		return got, err
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(result.Close)
	return result.Data.([]any)
}

func TestScaleP21SW34VisitWarmReloadedAndPinnedDifferential(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	rows := make([]scaleP21SW34Row, 128)
	for i := range rows {
		rows[i] = scaleP21SW34Row{
			ID: fmt.Sprintf("row-%d", i/2), Body: fmt.Sprintf("body\x00%d", i),
			At:  time.Date(2026, 11, 13, 12, 34, i%60, i, metadataLogicalProcessLocal),
			Nil: nil, Empty: []string{}, Number: int64(9007199254740993),
		}
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21_sw34.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw34Rows"] = rows
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	warm, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("literal full accessor lost workspace")
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	read, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		t.Errorf("installed traversal unexpectedly queried canonical SQL: %s", query)
	})
	reduction, err := read.Reduce(ctx, 2<<20, func(ctx context.Context) (any, error) {
		got := make([]scaleP21SW34Row, 0, len(rows))
		shape, err := read.Visit(ctx, []string{"settings", "sw34Rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
			if entry.Ordinal != len(got) {
				t.Fatal("array ordinal/order changed", entry.Ordinal, len(got))
			}
			return entry.Value.WithValue(ctx, 64<<10, func(value any) error {
				got = append(got, value.(scaleP21SW34Row))
				return nil
			})
		})
		if !shape.Found || shape.Nil || shape.Kind != reflect.Slice {
			t.Fatal("stored array shape changed", shape)
		}
		return got, err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reduction.Close()
	if !reflect.DeepEqual(reduction.Data, warm.Settings["sw34Rows"]) {
		t.Fatal("warm exact types, time offsets, duplicate IDs or nil/empty changed")
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21_sw34.peer", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw27Typed"] = "new generation"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	view.mu.Lock()
	view.lastWorkspaceKey = "missing-default"
	view.mu.Unlock()
	pinned := scaleP21SW34Entries(t, read, ctx, "settings", "sw34Rows")
	if len(pinned) != len(rows) || !reflect.DeepEqual(pinned[0], rows[0]) {
		t.Fatal("old read followed installed rewrite/default")
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	cold, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("literal reloaded accessor lost workspace")
	}
	fresh, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer fresh.Close()
	if got := scaleP21SW34Entries(t, fresh, ctx, "settings", "sw34Rows"); !reflect.DeepEqual(got, cold.Settings["sw34Rows"]) {
		t.Fatal("reloaded/mixed peer traversal differs from literal installed state", got, cold.Settings["sw34Rows"])
	}
}

func TestScaleP21SW34VisitShapesExactPathsAndOpaqueKeys(t *testing.T) {
	type tagged struct {
		Exact []string `json:"eXact"`
		Skip  []string `json:"-"`
	}
	read, _ := scaleP21SW34MemoryRead(t, map[string]any{
		"nil": nil, "typedNil": []string(nil), "empty": []string{},
		"nilMap": map[string]string(nil), "emptyMap": map[string]string{},
		"tagged":                       &tagged{Exact: []string{"one", "one"}, Skip: []string{"hidden"}},
		"nativeMap":                    map[int]string{7: "seven", 9: "nine"},
		strings.Repeat("key", 128<<10): []string{"huge unrelated native key"},
	})
	for _, test := range []struct {
		path  []string
		found bool
		nil   bool
	}{
		{[]string{"settings", "missing"}, false, false},
		{[]string{"settings", "nil"}, true, true},
		{[]string{"settings", "typedNil"}, true, true},
		{[]string{"settings", "empty"}, true, false},
		{[]string{"settings", "nilMap"}, true, true},
		{[]string{"settings", "emptyMap"}, true, false},
		{[]string{"settings", "tagged", "Exact"}, false, false},
		{[]string{"settings", "tagged", "exact"}, false, false},
		{[]string{"settings", "tagged", "eXact"}, true, false},
		{[]string{"settings", "tagged", "Skip"}, false, false},
	} {
		count := 0
		shape, err := read.Visit(t.Context(), test.path, func(context.Context, InstalledMetadataEntry) error {
			count++
			return nil
		})
		if err != nil || shape.Found != test.found || shape.Nil != test.nil {
			t.Fatal("path shape", test.path, shape, err)
		}
		if count != 0 && !reflect.DeepEqual(test.path, []string{"settings", "tagged", "eXact"}) {
			t.Fatal("missing/nil/empty path emitted entries", test.path, count)
		}
	}
	got := map[int]string{}
	_, err := read.Visit(t.Context(), []string{"settings", "nativeMap"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		if entry.Ordinal != -1 {
			t.Fatal("native map invented an array ordinal")
		}
		var key int
		if err := entry.Key.WithValue(ctx, 0, func(value any) error { key = value.(int); return nil }); err != nil {
			return err
		}
		return entry.Value.WithValue(ctx, 0, func(value any) error { got[key] = value.(string); return nil })
	})
	if err != nil || !reflect.DeepEqual(got, map[int]string{7: "seven", 9: "nine"}) {
		t.Fatal("opaque native keys changed", got, err)
	}
}

func TestScaleP21SW34VisitLegalLargeRecordAndStream(t *testing.T) {
	body := strings.Repeat("body\x00", 700<<10)
	read, store := scaleP21SW34MemoryRead(t, map[string]any{"rows": []scaleP21SW34Row{{ID: "large", Body: body}}, "bytes": []string{body}})
	before := store.metadataResidency.work.used
	_, err := read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.WithValue(ctx, 2<<20, func(value any) error {
			if value.(scaleP21SW34Row).Body != body || store.metadataResidency.work.used <= before+len(body)*2 {
				t.Fatal("large exact record was truncated or materialized without independent work")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal("legal record larger than generic Select limit was rejected", err)
	}
	_, err = read.Visit(t.Context(), []string{"settings", "bytes"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.StreamBytes(ctx, func(reader io.Reader) error {
			var buffer [4096]byte
			n, err := io.CopyBuffer(io.Discard, reader, buffer[:])
			if n != int64(len(body)) {
				t.Fatal("stream truncated legal body", n)
			}
			return err
		})
	})
	if err != nil || store.metadataResidency.work.used != before {
		t.Fatal("large streaming read leaked work", err)
	}
}

func TestScaleP21SW34VisitBorrowAndStreamExpiry(t *testing.T) {
	read, store := scaleP21SW34MemoryRead(t, map[string]any{"rows": []string{"first", "second"}})
	var escaped InstalledMetadataRef
	var reader io.Reader
	_, err := read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		if entry.Ordinal == 0 {
			escaped = entry.Value
			err := entry.Value.StreamBytes(ctx, func(r io.Reader) error { reader = r; return nil })
			if err != nil {
				return err
			}
			if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, errMetadataLogicalClosed) {
				t.Fatal("stream escaped its own callback", err)
			}
			return nil
		}
		if err := escaped.WithValue(ctx, 0, func(any) error { t.Fatal("expired first entry decoded"); return nil }); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("entry stayed live through later Visit callbacks", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := escaped.StreamBytes(t.Context(), func(io.Reader) error { return nil }); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("entry stream survived Visit return", err)
	}
	_, err = read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.StreamBytes(ctx, func(reader io.Reader) error {
			read.Close()
			if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, errMetadataLogicalClosed) {
				t.Fatal("reader ignored owning read Close", err)
			}
			return nil
		})
	})
	if !errors.Is(err, errMetadataLogicalClosed) || store.metadataResidency.work.used != 0 {
		t.Fatal("closed read released or retained active callback work incorrectly", err)
	}
}

func TestScaleP21SW34VisitCancellationUnsupportedAndAdmission(t *testing.T) {
	read, store := scaleP21SW34MemoryRead(t, map[string]any{"rows": []string{"first", "second"}, "opaque": []any{func() {}}})
	before := store.metadataResidency.work.used
	ctx, cancel := context.WithCancel(t.Context())
	count := 0
	_, err := read.Visit(ctx, []string{"settings", "rows"}, func(context.Context, InstalledMetadataEntry) error { count++; cancel(); return nil })
	if !errors.Is(err, context.Canceled) || count != 1 || store.metadataResidency.work.used != before {
		t.Fatal("canceled traversal continued/leaked", count, err)
	}
	_, err = read.Visit(t.Context(), []string{"settings", "opaque"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.WithValue(ctx, 0, func(any) error { t.Fatal("opaque value silently normalized"); return nil })
	})
	if !errors.Is(err, ErrBoundedMetadataCoverage) || store.metadataResidency.work.used != before {
		t.Fatal("opaque typed value was not explicit coverage failure", err)
	}
	_, release, err := store.tryAdmitAdditionalMetadataWork(t.Context(), metadataWorkBytes-before-metadataInstalledVisitWork)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.WithValue(ctx, 0, func(any) error { called = true; return nil })
	})
	release()
	if !errors.Is(err, ErrMetadataWorkBudget) || called || store.metadataResidency.work.used != before {
		t.Fatal("entry borrowed its cursor/descriptor work", called, err)
	}
}

func TestScaleP21SW34ReducePreAdmissionOutputAndClose(t *testing.T) {
	read, store := scaleP21SW34MemoryRead(t, map[string]any{"rows": []string{"one", "two"}})
	before := store.metadataResidency.work.used
	called := false
	_, release, err := store.tryAdmitAdditionalMetadataWork(t.Context(), metadataWorkBytes-before)
	if err != nil {
		t.Fatal(err)
	}
	result, err := read.Reduce(t.Context(), 1<<20, func(context.Context) (any, error) { called = true; return "impossible", nil })
	release()
	if result != nil || !errors.Is(err, ErrMetadataWorkBudget) || called {
		t.Fatal("reduction ran before independent result admission", called, err)
	}
	result, err = read.Reduce(t.Context(), 1, func(context.Context) (any, error) { called = true; return nil, nil })
	if result != nil || !errors.Is(err, ErrMetadataWorkBudget) || called {
		t.Fatal("tiny result allowance could retain uncharged handles", called, err)
	}
	result, err = read.Reduce(t.Context(), 4<<10, func(context.Context) (any, error) { return nil, nil })
	if err != nil || result == nil || store.metadataResidency.work.used != before+4<<10 {
		t.Fatal("nil result did not retain its handle allowance", err)
	}
	result.Close()
	for _, value := range []any{strings.Repeat("x", 64<<10), make([]byte, 0, 1<<20), struct{ hidden []byte }{make([]byte, 64<<10)}, func() {}} {
		called := false
		result, err := read.Reduce(t.Context(), 4<<10, func(context.Context) (any, error) { called = true; return value, nil })
		if result != nil || err == nil || !called || store.metadataResidency.work.used != before {
			t.Fatal("oversized/unsupported retained output exposed or leaked", err)
		}
	}
	result, err = read.Reduce(t.Context(), 1<<20, func(ctx context.Context) (any, error) {
		if store.metadataResidency.work.used != before+1<<20 {
			t.Fatal("result allowance not held before callback")
		}
		got := []string{}
		_, err := read.Visit(ctx, []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
			return entry.Value.WithValue(ctx, 0, func(value any) error { got = append(got, value.(string)); return nil })
		})
		return got, err
	})
	if err != nil || !reflect.DeepEqual(result.Data, []string{"one", "two"}) || store.metadataResidency.work.used != before+1<<20 {
		t.Fatal("valid reduction lost output/lifetime", err)
	}
	result.Close()
	result.Close()
	if result.Data != nil || len(read.reductions) != 0 || store.metadataResidency.work.used != before {
		t.Fatal("individual result Close retained value/work")
	}
	result, err = read.Reduce(t.Context(), 64<<20, func(context.Context) (any, error) { return make([]byte, 4<<20), nil })
	if err != nil || len(result.Data.([]byte)) != 4<<20 {
		t.Fatal("legal byte result was charged as millions of separately allocated objects", err)
	}
	result.Close()
	result, err = read.Reduce(t.Context(), 1<<20, func(context.Context) (any, error) { return "owned until read.Close", nil })
	if err != nil {
		t.Fatal(err)
	}
	read.Close()
	if result.Data != nil || store.metadataResidency.work.used != 0 {
		t.Fatal("read.Close did not close retained reductions")
	}
}

func TestScaleP21SW34NestedScalarAndHeaderSkipGiantFields(t *testing.T) {
	body := strings.Repeat("x", 3<<20)
	rows := []domain.TrashEntry{
		{ID: "duplicate", ResourceType: "document", ResourceID: "doc", Title: "first",
			Payload: json.RawMessage(`"` + body + `"`), TeamIDs: []string{},
			DeletedAt: time.Date(2026, 11, 13, 1, 2, 3, 4, time.UTC),
			Links:     &domain.TrashLinks{DocumentIDs: []string{body}}},
		{ID: "duplicate", Title: "second"},
	}
	read, store := scaleP21SW34MemoryRead(t, map[string]any{"nested": map[string]any{"trash": rows}})
	store.metadataResidency.work.limit = 1 << 20
	var escapedChild InstalledMetadataRef
	done := errors.New("first matching header")
	result, err := read.Reduce(t.Context(), 256<<10, func(ctx context.Context) (any, error) {
		var header domain.TrashEntry
		_, err := read.Visit(ctx, []string{"settings"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
			var key string
			if err := entry.Key.WithValue(ctx, 0, func(value any) error { key = value.(string); return nil }); err != nil {
				return err
			}
			if key != "nested" {
				return nil
			}
			_, err := entry.Value.Visit(ctx, []string{"trash"}, func(ctx context.Context, item InstalledMetadataEntry) error {
				var id string
				if err := item.Value.Child(ctx, []string{"id"}, func(ctx context.Context, shape InstalledMetadataShape, child InstalledMetadataRef) error {
					if !shape.Found || shape.Kind != reflect.String {
						t.Fatal("nested scalar shape changed", shape)
					}
					escapedChild = child
					return child.WithValue(ctx, 0, func(value any) error { id = value.(string); return nil })
				}); err != nil {
					return err
				}
				if err := escapedChild.WithValue(ctx, 0, func(any) error { t.Fatal("child outlived its own callback"); return nil }); !errors.Is(err, errMetadataLogicalClosed) {
					t.Fatal("scalar child remained live inside parent entry", err)
				}
				if id != "duplicate" {
					return nil
				}
				if err := item.Value.WithValue(ctx, 0, func(any) error { t.Fatal("giant whole entry unexpectedly fit"); return nil }); !errors.Is(err, ErrMetadataWorkBudget) {
					t.Fatal("fixture does not prove whole entry exceeds available work", err)
				}
				if err := item.Value.WithFields(ctx, []string{"id", "resourceType", "resourceId", "title", "teamIds", "deletedBy", "deletedAt", "expiresAt"}, 64<<10, func(value any) error {
					header = value.(domain.TrashEntry)
					return nil
				}); err != nil {
					return err
				}
				return done
			})
			return err
		})
		if errors.Is(err, done) {
			err = nil
		}
		return header, err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	want := rows[0]
	want.Payload, want.Links = nil, nil
	want = cloneSnapshotValue(reflect.ValueOf(want)).Interface().(domain.TrashEntry)
	if !reflect.DeepEqual(result.Data, want) {
		t.Fatal("first-match typed header differs from literal clone after payload/link removal")
	}
}

func TestScaleP21SW34NestedMissingAndExpiredParent(t *testing.T) {
	read, _ := scaleP21SW34MemoryRead(t, map[string]any{"rows": []map[string]any{{"presentNil": nil, "nested": []string{"inside"}}}})
	var escaped InstalledMetadataRef
	_, err := read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		for _, test := range []struct {
			key   string
			found bool
			nil   bool
		}{{"missing", false, false}, {"presentNil", true, true}} {
			err := entry.Value.Child(ctx, []string{test.key}, func(_ context.Context, shape InstalledMetadataShape, _ InstalledMetadataRef) error {
				if shape.Found != test.found || shape.Nil != test.nil {
					t.Fatal("nested missing/present-nil collapsed", test, shape)
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		_, err := entry.Value.Visit(ctx, []string{"nested"}, func(ctx context.Context, nested InstalledMetadataEntry) error {
			escaped = nested.Value
			return nested.Value.WithValue(ctx, 0, func(value any) error {
				if value != "inside" {
					t.Fatal("nested array scalar changed", value)
				}
				return nil
			})
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := escaped.Child(t.Context(), nil, func(context.Context, InstalledMetadataShape, InstalledMetadataRef) error {
		t.Fatal("nested child escaped parent lifetime")
		return nil
	}); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("nested expired parent ignored", err)
	}
	_, err = read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.WithFields(ctx, []string{"missing"}, 0, func(any) error { t.Fatal("non-struct field projection accepted"); return nil })
	})
	if !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("field projection accepted wrong native shape", err)
	}
}

func TestScaleP21SW34RootScalarNativeKeyAndStringCompare(t *testing.T) {
	giant := strings.Repeat("giant", 700<<10)
	read, store := scaleP21SW34DataRead(t, domain.Bootstrap{
		Workspace: domain.Workspace{URLKey: "exact\x00key", Name: giant},
		UserSettings: map[string]domain.UserSettings{
			"native\x00key": {UserID: "different-field-id", Language: "zh-CN", CodingPromptTemplate: giant},
		},
		Settings: map[string]any{"giant": giant, "nil": nil, "number": 7},
	})
	store.metadataResidency.work.limit = 512 << 10
	for _, test := range []struct {
		path []string
		want string
	}{
		{[]string{"workspace", "urlKey"}, "exact\x00key"},
		{[]string{"userSettings", "native\x00key", "language"}, "zh-CN"},
	} {
		err := read.Child(t.Context(), test.path, func(ctx context.Context, shape InstalledMetadataShape, ref InstalledMetadataRef) error {
			if !shape.Found || shape.Nil || shape.Kind != reflect.String {
				t.Fatal("root scalar shape changed", test.path, shape)
			}
			return ref.WithValue(ctx, 0, func(value any) error {
				if value != test.want {
					t.Fatal("root scalar exact value/native key changed", test.path, value)
				}
				return nil
			})
		})
		if err != nil {
			t.Fatal("scalar selection decoded giant unrelated parent field", test.path, err)
		}
	}
	err := read.Child(t.Context(), []string{"userSettings", "different-field-id", "language"}, func(_ context.Context, shape InstalledMetadataShape, _ InstalledMetadataRef) error {
		if shape.Found {
			t.Fatal("native map key replaced by inner UserID field lookup")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"giant", "nil", "number"} {
		err := read.Child(t.Context(), []string{"settings", key}, func(ctx context.Context, _ InstalledMetadataShape, ref InstalledMetadataRef) error {
			match, err := ref.StringEqual(ctx, "small")
			if match {
				t.Fatal("nonmatching giant/non-string/nil scalar compared equal")
			}
			return err
		})
		if err != nil {
			t.Fatal("string predicate materialized giant scalar", key, err)
		}
	}
}

func TestScaleP21SW34ConcurrentCloseKeepsActiveWorkUntilReturn(t *testing.T) {
	read, store := scaleP21SW34MemoryRead(t, map[string]any{"rows": []string{"value"}})
	entered, proceed, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
			return entry.Value.StreamBytes(ctx, func(reader io.Reader) error {
				close(entered)
				<-proceed
				_, err := reader.Read(make([]byte, 1))
				return err
			})
		})
		finished <- err
	}()
	<-entered
	read.Close()
	if store.metadataResidency.work.used == 0 {
		t.Fatal("read.Close released active cursor/stream work before callback returned")
	}
	close(proceed)
	if err := <-finished; !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("in-flight reader ignored concurrent owner Close", err)
	}
	if store.metadataResidency.work.used != 0 {
		t.Fatal("concurrent close/return leaked active work")
	}
}
