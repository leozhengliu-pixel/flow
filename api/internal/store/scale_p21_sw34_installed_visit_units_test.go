package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW34PackedRootNestedHeaderAndScalar(t *testing.T) {
	body := strings.Repeat("x", 3<<20)
	source, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{
		Workspace: domain.Workspace{URLKey: "packed-root", Name: body},
		Trash: []domain.TrashEntry{
			{ID: "duplicate", Title: "first", Payload: json.RawMessage(`"` + body + `"`), Links: &domain.TrashLinks{DocumentIDs: []string{body}}},
			{ID: "duplicate", Title: "second"},
		},
	}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	packed, err := metadataLogicalUnitsImport(t.Context(), source)
	source.Release()
	if err != nil {
		t.Fatal(err)
	}
	store := &SQLiteStore{metadataResidency: newMetadataResidency()}
	store.metadataResidency.work.limit = 1 << 20
	_, release, err := store.tryAdmitAdditionalMetadataWork(t.Context(), metadataInstalledVisitWork)
	if err != nil {
		packed.Release()
		t.Fatal(err)
	}
	read := &InstalledMetadataRead{
		store: store, source: packed, workspace: "packed-root", release: release,
		selections: map[*OwnedMetadataSelection]struct{}{}, reductions: map[*OwnedMetadataReduction]struct{}{},
	}
	defer read.Close()
	if err := read.Child(t.Context(), []string{"workspace", "urlKey"}, func(ctx context.Context, shape InstalledMetadataShape, ref InstalledMetadataRef) error {
		if !shape.Found {
			t.Fatal("packed scalar was missing")
		}
		match, err := ref.StringEqual(ctx, "packed-root")
		if !match {
			t.Fatal("packed scalar compare changed")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	done := errors.New("first match")
	result, err := read.Reduce(t.Context(), 256<<10, func(ctx context.Context) (any, error) {
		var header domain.TrashEntry
		_, err := read.Visit(ctx, []string{"trash"}, func(ctx context.Context, entry InstalledMetadataEntry) error {
			match := false
			if err := entry.Value.Child(ctx, []string{"id"}, func(ctx context.Context, _ InstalledMetadataShape, ref InstalledMetadataRef) error {
				var err error
				match, err = ref.StringEqual(ctx, "duplicate")
				return err
			}); err != nil || !match {
				return err
			}
			if err := entry.Value.WithFields(ctx, []string{"id", "title"}, 64<<10, func(value any) error {
				header = value.(domain.TrashEntry)
				return nil
			}); err != nil {
				return err
			}
			return done
		})
		if errors.Is(err, done) {
			err = nil
		}
		return header, err
	})
	if err != nil || !reflect.DeepEqual(result.Data, domain.TrashEntry{ID: "duplicate", Title: "first"}) {
		t.Fatal("packed exact first header requires giant unrelated payload materialization", err)
	}
	result.Close()
	read.Close()
	if store.metadataResidency.work.used != 0 {
		t.Fatal("packed bridge leaked work")
	}
}
