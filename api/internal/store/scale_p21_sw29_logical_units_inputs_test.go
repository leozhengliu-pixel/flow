package store

import (
	"errors"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW29LogicalUnitsTimestampInputsAndRetainedReceipt(t *testing.T) {
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "inputs-origin"},
		Settings:  map[string]any{"timestamp": time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)},
	}
	original, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{"inputs-origin", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer original.Release()
	packed, err := metadataLogicalUnitsImport(t.Context(), original)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	work, release, err := repo.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	work, parser := repo.WithMetadataTimestampInputs(work)
	work, finish, err := repo.startMetadataTimestampInputs(work)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	unbind, err := repo.bindMetadataTimestampInputs(work)
	if err != nil {
		t.Fatal(err)
	}
	defer unbind()
	parsed, err := parser.ParseRFC3339("2026-11-13T12:13:14+08:01")
	if err != nil {
		t.Fatal(err)
	}
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "timestamp"}}}
	var retained *metadataLogicalSource
	for _, source := range []*metadataLogicalSource{original, packed} {
		before, err := source.Select(work, scope, 2<<20)
		if err != nil {
			t.Fatal(err)
		}
		next := cloneBootstrap(before)
		next.Settings["timestamp"] = parsed
		candidate, err := source.Fork(work, before, next, scope, source.Stamp())
		if err != nil {
			t.Fatal("trusted producer input did not pass native encoder", err)
		}
		actual, err := candidate.Select(t.Context(), scope, 2<<20)
		if source == packed {
			retained = candidate
			defer retained.Release()
		} else {
			candidate.Release()
		}
		if err != nil || actual.Settings["timestamp"].(time.Time) != parsed {
			t.Fatal("producer input lost native location receipt identity", err)
		}
	}
	unbind()
	finish()
	afterInputClose, err := retained.Select(t.Context(), scope, 2<<20)
	if err != nil || afterInputClose.Settings["timestamp"].(time.Time) != parsed {
		t.Fatal("closing producer ledger invalidated independently owned unit receipt", err)
	}
	before, err := packed.Select(t.Context(), scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	next := cloneBootstrap(before)
	next.Settings["timestamp"] = parsed
	if source, err := packed.Fork(work, before, next, scope, packed.Stamp()); source != nil || !errors.Is(err, ErrMetadataRecordQuery) {
		if source != nil {
			source.Release()
		}
		t.Fatal("expired producer input scope silently authorized a new receipt", err)
	}
	// An unproven native pointer must fail before a candidate can publish.
	next.Settings["timestamp"] = time.Date(2026, 11, 13, 1, 2, 3, 4, time.FixedZone("unproven", 7331))
	if source, err := packed.Fork(t.Context(), before, next, scope, packed.Stamp()); source != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		if source != nil {
			source.Release()
		}
		t.Fatal("unproven changed unit reached publication", err)
	}
}
