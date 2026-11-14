package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

var scaleP21SW53UnsafeJSONCalls atomic.Int32

type scaleP21SW53UnsafeJSON struct{ Value string }

func (v scaleP21SW53UnsafeJSON) MarshalJSON() ([]byte, error) {
	scaleP21SW53UnsafeJSONCalls.Add(1)
	return []byte(`{"unexpected":true}`), nil
}

func TestScaleP21SW53RefIndexedNativeMapChild(t *testing.T) {
	data := domain.Bootstrap{UserSettings: map[string]domain.UserSettings{
		"native\x00key": {UserID: "inner-mismatch", Language: "zh-CN", CodingPromptTemplate: strings.Repeat("p", 3<<20)},
	}}
	for i := 0; i < 256; i++ {
		data.UserSettings[fmt.Sprint(i)] = domain.UserSettings{UserID: "other", Language: "en"}
	}
	read, store := scaleP21SW34DataRead(t, data)
	packed, err := metadataLogicalUnitsImport(t.Context(), read.source)
	if err != nil {
		t.Fatal(err)
	}
	read.source.Release()
	read.source = packed
	store.metadataResidency.work.limit = 512 << 10
	counter := &sw29ProducerSQLDriver{}
	driver := fmt.Sprintf("scale-p21-ref-map-%d", sw29ProducerDriverSequence.Add(1))
	sql.Register(driver, counter)
	arena := packed.units.arena
	counted, err := sql.Open(driver, arena.index.stage.path)
	if err != nil {
		t.Fatal(err)
	}
	counted.SetMaxOpenConns(1)
	original := arena.index.stage.db
	arena.index.stage.db = counted
	defer func() {
		arena.index.stage.db = original
		if err := counted.Close(); err != nil {
			t.Error(err)
		}
	}()
	canonical := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { canonical++ })
	err = read.Child(ctx, []string{"userSettings", "native\x00key", "language"}, func(ctx context.Context, shape InstalledMetadataShape, ref InstalledMetadataRef) error {
		if !shape.Found || shape.Kind != reflect.String {
			t.Fatal("indexed scalar shape changed", shape)
		}
		match, err := ref.StringEqual(ctx, "zh-CN")
		if !match {
			t.Fatal("indexed native-key child changed language")
		}
		return err
	})
	calls := counter.calls.Load()
	t.Logf("records=257 local-sql=%d canonical-sql=%d", calls, canonical)
	if err != nil || canonical != 0 || calls == 0 || calls > 512 {
		t.Fatal("public Child still scanned unrelated native map units", calls, canonical, err)
	}
	if err := read.Child(ctx, []string{"userSettings", "inner-mismatch"}, func(_ context.Context, shape InstalledMetadataShape, _ InstalledMetadataRef) error {
		if shape.Found {
			t.Fatal("inner UserID replaced exact native key")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW53RefEncodingLegalLargeRecord(t *testing.T) {
	type row struct {
		ID   string `json:"id"`
		Body string `json:"body"`
	}
	value := row{ID: "large", Body: strings.Repeat("\x00<&\u2028", 300<<10)}
	read, store := scaleP21SW34MemoryRead(t, map[string]any{"rows": []row{value}})
	spool, err := NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	before := store.metadataResidency.work.used
	_, err = read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.WithValueForEncoding(ctx, 0, func(actual any) error {
			if !reflect.DeepEqual(actual, value) || store.metadataResidency.work.used < before+len(value.Body)*20 {
				t.Fatal("JSON encoding began without full independent expansion allowance")
			}
			return json.NewEncoder(spool).Encode(actual)
		})
	})
	if err != nil || store.metadataResidency.work.used != before {
		t.Fatal("legal large encoded record rejected/leaked", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(spool)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(value)
	if err != nil || string(actual) != string(want)+"\n" {
		t.Fatal("record encoding changed control/HTML/UTF8 semantics", err)
	}
}

func TestScaleP21SW53RefFieldsEncodingOmitsGiantRows(t *testing.T) {
	value := domain.ImportJob{
		ID: "job", UserID: "owner", Filename: "warm\x00filename", Status: "mapping",
		Headers: []string{}, Errors: []string{}, CreatedAt: time.Date(2026, 11, 13, 1, 2, 3, 4, time.UTC),
		Rows: []map[string]string{{"giant": strings.Repeat("x", 3<<20)}},
	}
	read, store := scaleP21SW34MemoryRead(t, map[string]any{"rows": []domain.ImportJob{value}})
	store.metadataResidency.work.limit = 1 << 20
	spool, err := NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	_, err = read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		if err := entry.Value.WithValueForEncoding(ctx, 0, func(any) error { t.Fatal("giant full row fit control budget"); return nil }); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("fixture did not exclude whole-row decode", err)
		}
		return entry.Value.WithFieldsForEncoding(ctx, []string{"id", "userId", "filename", "status", "headers", "errors", "createdAt"}, 0, func(actual any) error {
			return json.NewEncoder(spool).Encode(actual)
		})
	})
	if err != nil {
		t.Fatal("selected public row required giant unused Rows", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	actual, err := io.ReadAll(spool)
	if err != nil {
		t.Fatal(err)
	}
	value.Rows = nil
	want, err := json.Marshal(value)
	if err != nil || string(actual) != string(want)+"\n" {
		t.Fatal("typed partial row differs from old row with Rows cleared", err)
	}
}

func TestScaleP21SW53RefEncodingAdmissionAndOpaqueMethods(t *testing.T) {
	read, store := scaleP21SW34MemoryRead(t, map[string]any{
		"rows":   []string{strings.Repeat("x", 64<<10)},
		"opaque": []scaleP21SW53UnsafeJSON{{Value: "native"}},
	})
	before := store.metadataResidency.work.used
	_, release, err := store.tryAdmitAdditionalMetadataWork(t.Context(), metadataWorkBytes-before-(512<<10))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	_, err = read.Visit(t.Context(), []string{"settings", "rows"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.WithValueForEncoding(ctx, 0, func(any) error { called = true; return nil })
	})
	release()
	if !errors.Is(err, ErrMetadataWorkBudget) || called || store.metadataResidency.work.used != before {
		t.Fatal("encoder borrowed decoded/cursor credit or ran before admission", called, err)
	}
	scaleP21SW53UnsafeJSONCalls.Store(0)
	_, err = read.Visit(t.Context(), []string{"settings", "opaque"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
		return entry.Value.WithValueForEncoding(ctx, 0, func(any) error { called = true; return nil })
	})
	if !errors.Is(err, ErrBoundedMetadataCoverage) || called || scaleP21SW53UnsafeJSONCalls.Load() != 0 || store.metadataResidency.work.used != before {
		t.Fatal("opaque JSON method bypassed preflight allowance", called, err)
	}
}
