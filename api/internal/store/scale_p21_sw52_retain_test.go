package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW52RetainIndependentLifetime(t *testing.T) {
	original, s := scaleP21SW34MemoryRead(t, map[string]any{"rows": []string{"old", "old"}})
	initial := s.metadataResidency.work.used
	retained, err := original.Retain(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if retained.source != original.source || retained.workspace != original.workspace || s.metadataResidency.work.used <= initial {
		t.Fatal("retained owner did not independently own exact source and descriptor")
	}
	original.Close()
	if got := scaleP21SW34Entries(t, retained, t.Context(), "settings", "rows"); !reflect.DeepEqual(got, []any{"old", "old"}) {
		t.Fatal("parent Close expired retained source", got)
	}
	retained.Close()
	retained.Close()
	if s.metadataResidency.work.used != 0 {
		t.Fatal("independent retain leaked work", s.metadataResidency.work.used)
	}
	if _, err := original.Retain(t.Context()); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed owner retained", err)
	}
}

func TestScaleP21SW52RetainIncomingBindingAndCancellation(t *testing.T) {
	original, s := scaleP21SW34MemoryRead(t, map[string]any{"rows": []string{"pinned"}})
	bound, err := original.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	retained, found, err := s.retainInstalledMetadataRead(bound, original.workspace)
	if err != nil || !found || retained.source != original.source {
		t.Fatal("incoming generation was replaced", found, err)
	}
	retained.Close()
	if _, _, err := s.retainInstalledMetadataRead(bound, "foreign"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("foreign workspace accepted", err)
	}
	foreign := &SQLiteStore{db: &sqlDatabase{}}
	if _, _, err := foreign.retainInstalledMetadataRead(bound, original.workspace); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("foreign database accepted", err)
	}
	ctx, cancel := context.WithCancel(bound)
	cancel()
	before := s.metadataResidency.work.used
	if _, err := original.Retain(ctx); !errors.Is(err, context.Canceled) || s.metadataResidency.work.used != before {
		t.Fatal("canceled retain acquired work", err)
	}
	original.Close()
	if _, _, err := s.retainInstalledMetadataRead(bound, ""); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed incoming owner silently fell back", err)
	}
}

func TestScaleP21SW52RetainPinnedAcrossWriteAndReload(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21_sw52.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw52"] = []string{"old"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	original, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer original.Close()
	bound, err := original.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21_sw52.next", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw52"] = []string{"new"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	retained, found, err := view.retainInstalledMetadataRead(bound, "")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer retained.Close()
	original.Close()
	if got := scaleP21SW34Entries(t, retained, t.Context(), "settings", "sw52"); !reflect.DeepEqual(got, []any{"old"}) {
		t.Fatal("retain followed later write/reload", got)
	}
}

func TestScaleP21SW52RetainSaturationDoesNotBorrow(t *testing.T) {
	original, s := scaleP21SW34MemoryRead(t, map[string]any{"rows": []string{"old"}})
	before := s.metadataResidency.work.used
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(t.Context(), s.metadataResidency.work.limit-before)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := original.Retain(ctx); !errors.Is(err, ErrMetadataWorkBudget) {
		release()
		t.Fatal("retain borrowed caller work instead of reserving independently", err)
	}
	release()
	if s.metadataResidency.work.used != before {
		t.Fatal("rejected retain leaked a descriptor")
	}
}
