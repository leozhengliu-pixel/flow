package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW29LogicalUnitsPhysicalBindingIsGenerationOwned(t *testing.T) {
	ctx := t.Context()
	logicalID := strings.Repeat("logical\x00body-", 1000)
	logical := RecordKey{Kind: "customEmojis", ID: logicalID}
	source, err := captureMetadataLogicalSource(ctx, domain.Bootstrap{
		Workspace:    domain.Workspace{ID: "physical-origin"},
		CustomEmojis: []domain.CustomEmoji{{ID: logicalID, Name: "before"}},
	}, copyStamp{"physical-origin", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	packed, err := metadataLogicalUnitsImport(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	physical := RecordKey{Kind: "customEmojis", ID: "physical\x00row"}
	binding := metadataLogicalPhysicalBinding{
		Generation: packed.generation, Logical: logical, Canonical: physical, Storage: 1,
	}
	bound, err := packed.WithPhysicalBindings(ctx, []metadataLogicalPhysicalBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	defer bound.Release()
	if _, _, err := packed.PhysicalBinding(ctx, logical); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old pinned root acquired a later producer binding", err)
	}
	got, storage, err := bound.PhysicalBinding(ctx, logical)
	if err != nil || got != physical || storage != 1 {
		t.Fatal("body ID substituted for physical BLOB key", got, storage, err)
	}
	if retry, err := bound.WithPhysicalBindings(ctx, []metadataLogicalPhysicalBinding{binding}); retry != nil || !errors.Is(err, ErrMetadataRecordQuery) {
		if retry != nil {
			retry.Release()
		}
		t.Fatal("earlier generation receipt rebound to a different root", err)
	}
	conflict := binding
	conflict.Generation, conflict.Storage = bound.generation, 0
	if retry, err := bound.WithPhysicalBindings(ctx, []metadataLogicalPhysicalBinding{conflict}); retry != nil || !errors.Is(err, ErrMetadataRecordQuery) {
		if retry != nil {
			retry.Release()
		}
		t.Fatal("TEXT and BLOB physical classes collapsed in one immutable root", err)
	}
	scope := MetadataRecordScope{Write: []RecordKey{logical}}
	before, err := bound.Select(ctx, scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	next := cloneBootstrap(before)
	next.CustomEmojis[0].Name = "after"
	updated, err := bound.Fork(ctx, before, next, scope, bound.Stamp())
	if err != nil {
		t.Fatal(err)
	}
	defer updated.Release()
	got, storage, err = updated.PhysicalBinding(ctx, logical)
	if err != nil || got != physical || storage != 1 {
		t.Fatal("point replacement lost its explicit producer locator", got, storage, err)
	}
	next.CustomEmojis = nil
	deleted, err := updated.Fork(ctx, next, next, scope, updated.Stamp())
	if err != nil {
		t.Fatal(err)
	}
	defer deleted.Release()
	if _, _, err := deleted.PhysicalBinding(ctx, logical); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted logical unit retained its physical binding", err)
	}
	if got, storage, err := bound.PhysicalBinding(ctx, logical); err != nil || got != physical || storage != 1 {
		t.Fatal("later deletion changed pinned earlier physical binding", err)
	}
}
