package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW29LogicalProducerWarmCold(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	assertPacked := func() *metadataLogicalSource {
		t.Helper()
		source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		if source.units == nil || source.generation == ([16]byte{}) {
			source.Release()
			t.Fatal("installed producer did not publish an independent indexed root")
		}
		return source
	}
	warm := assertPacked()
	defer warm.Release()
	scope := MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Typed"}}}
	wanted, found := f.WorkspaceMetadata(workspace)
	observed, ok, err := view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	if err != nil || !found || !ok || !reflect.DeepEqual(observed.Settings["sw27Typed"], wanted.Settings["sw27Typed"]) {
		t.Fatal("warm indexed producer changed the literal installed value", err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw27Typed"] = "producer-cold-peer"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	cold := assertPacked()
	defer cold.Release()
	if warm.generation == cold.generation || warm.units.arena == cold.units.arena {
		t.Fatal("cold publication reused an earlier root or arena")
	}
	observed, ok, err = view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	if err != nil || !ok || observed.Settings["sw27Typed"] != "producer-cold-peer" {
		t.Fatal("cold indexed producer did not publish its actual SQL snapshot", err)
	}
	old, err := warm.Select(t.Context(), scope, 2<<20)
	if err != nil || !reflect.DeepEqual(old.Settings["sw27Typed"], wanted.Settings["sw27Typed"]) {
		t.Fatal("cold publication changed a retained warm root", err)
	}
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("installed producers hydrated full metadata or leaked work")
	}
}

func TestScaleP21SW29LogicalProducerAdmissionFallback(t *testing.T) {
	ctx := t.Context()
	source, err := captureMetadataLogicalSource(ctx, domain.Bootstrap{
		Workspace: domain.Workspace{ID: "unit-fallback"},
		Settings:  map[string]any{"native": int64(42)},
	}, copyStamp{"unit-fallback", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	injected := metadataLogicalDiskBytes - disk
	if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, injected, metadataLogicalDiskBytes) {
		t.Fatal("could not reserve disk quota")
	}
	fallbacks := metadataLogicalUnitsFallbacks[metadataLogicalUnitsFallbackDisk].Load()
	actual, err := metadataLogicalUnitsForInstalled(ctx, source)
	metadataLogicalDiskUsed.Add(-injected)
	if err != nil || actual != source || source.units != nil {
		t.Fatal("index admission changed the already-supported typed-source path", err)
	}
	if metadataLogicalUnitsFallbacks[metadataLogicalUnitsFallbackDisk].Load() != fallbacks+1 {
		t.Fatal("optional disk fallback did not record its fixed reason class")
	}
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("failed conversion leaked its separate disk/descriptor admission")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if result, err := metadataLogicalUnitsForInstalled(cancelled, source); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled conversion published a fallback", err)
	}
	if _, err := source.Select(ctx, MetadataRecordScope{Fields: []string{"settings"}}, 2<<20); err != nil {
		t.Fatal("failed conversion consumed its caller's typed-source reference", err)
	}
}

type sw29ProducerArenaPath struct {
	metadataLogicalFile
	path string
}

func (f sw29ProducerArenaPath) Name() string { return f.path }

func TestScaleP21SW29LogicalProducerIOFallback(t *testing.T) {
	ctx := t.Context()
	source, err := captureMetadataLogicalSource(ctx, domain.Bootstrap{
		Workspace: domain.Workspace{ID: "unit-io-fallback"},
		Settings:  map[string]any{"native": int64(42)},
	}, copyStamp{"unit-io-fallback", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	storage := source.stores[0]
	original := storage.file
	storage.file = sw29ProducerArenaPath{original, filepath.Join(t.TempDir(), "missing", "source")}
	defer func() { storage.file = original }()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	fallbacks := metadataLogicalUnitsFallbacks[metadataLogicalUnitsFallbackOther].Load()
	actual, err := metadataLogicalUnitsForInstalled(ctx, source)
	if err != nil || actual != source || source.units != nil {
		t.Fatal("optional arena I/O failure replaced the validated typed source", err)
	}
	if metadataLogicalUnitsFallbacks[metadataLogicalUnitsFallbackOther].Load() != fallbacks+1 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("I/O fallback lost its bounded reason or leaked admission")
	}
	got, err := source.Select(ctx, MetadataRecordScope{Fields: []string{"settings"}}, 2<<20)
	if err != nil || got.Settings["native"] != int64(42) {
		t.Fatal("I/O fallback changed the literal native value", err)
	}
}

func TestScaleP21SW29LogicalProducerLargeRefreshFallback(t *testing.T) {
	ctx := t.Context()
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "unit-large-refresh"}}
	data.Users = []domain.User{{ID: "actor", Name: "new-directory-name"}}
	for i := 0; i < 64; i++ {
		data.Projects = append(data.Projects, domain.Project{
			ID: fmt.Sprint(i), Name: strings.Repeat("legal-large-project", 4096),
			Creator: &domain.User{ID: "actor", Name: "old-reference"},
		})
		data.Issues = append(data.Issues, domain.Issue{ID: fmt.Sprint(i)})
	}
	typed, err := captureMetadataLogicalSource(ctx, data, copyStamp{"unit-large-refresh", -1})
	if err != nil {
		t.Fatal(err)
	}
	defer typed.Release()
	packed, err := metadataLogicalUnitsImport(ctx, typed)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	refresh := func(source *metadataLogicalSource) (*metadataLogicalSource, error) {
		return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if name == "projects" {
				return metadataAggregateRefreshCollection{source, name, metadataAggregateStoredNode{node}}, true, nil
			}
			return metadataAggregateStoredNode{node}, false, nil
		})
	}
	expected, err := refresh(typed)
	if err != nil {
		t.Fatal(err)
	}
	defer expected.Release()
	actual, err := refresh(packed)
	if err != nil {
		t.Fatal("streaming compatibility transform rejected a previously legal large collection", err)
	}
	defer actual.Release()
	scope := MetadataRecordScope{Fields: []string{"projects"}}
	want, err := expected.Select(ctx, scope, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, err := actual.Select(ctx, scope, 32<<20)
	if err != nil || !reflect.DeepEqual(got, want) || got.Projects[63].Creator.Name != "new-directory-name" {
		t.Fatal("streaming unit fallback changed reference-refresh semantics", err)
	}
	candidate, err := metadataLogicalInstalledCandidate(ctx, actual)
	if err != nil {
		t.Fatal("transient removal acquired an unrelated 50-record field limit", err)
	}
	defer candidate.Release()
	stripped, err := candidate.Select(ctx, MetadataRecordScope{Fields: []string{"issues"}}, 2<<20)
	if err != nil || stripped.Issues != nil {
		t.Fatal("streaming transient replacement changed nil collection shape", err)
	}
}

func TestScaleP21SW29LogicalProducerExternalTypedFallback(t *testing.T) {
	f, warm, stamp := scaleP21SW27WarmLogicalFixture(t)
	ctx, workspace := t.Context(), f.data.Workspace.URLKey
	typed, err := captureMetadataLogicalSource(ctx, warm, stamp)
	if err != nil {
		t.Fatal(err)
	}
	defer typed.Release()
	packed, err := metadataLogicalUnitsImport(ctx, typed)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	expected, err := f.hydrateMetadataLogicalSource(ctx, tx, typed, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer expected.Release()
	actual, err := f.hydrateMetadataLogicalSource(ctx, tx, packed, workspace)
	if err != nil {
		t.Fatal("packed source did not retain the exact external typed fallback", err)
	}
	defer actual.Release()
	var fields []string
	if err := typed.VisitFields(ctx, func(name string, _ metadataLogicalNode) error {
		if name != "-" {
			fields = append(fields, name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scope := MetadataRecordScope{Fields: fields}
	want, err := expected.Select(ctx, scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, err := actual.Select(ctx, scope, 64<<20)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("external typed fallback changed an untouched native field", err)
	}
}
