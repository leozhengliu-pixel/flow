package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unsafe"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW0InstalledIdentity(t *testing.T, source *SQLiteStore, ref string) (InstalledWorkspaceIdentity, bool) {
	t.Helper()
	read, found, err := source.BeginInstalledWorkspaceIdentityRead(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if read == nil {
		return InstalledWorkspaceIdentity{}, found
	}
	defer read.Close()
	return InstalledWorkspaceIdentity{
		RegistryKey: strings.Clone(read.Identity.RegistryKey),
		URLKey:      strings.Clone(read.Identity.URLKey),
		ID:          strings.Clone(read.Identity.ID),
	}, found
}

func TestScaleP21SW0InstalledWorkspaceExactRegistry(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		source := &SQLiteStore{
			lastWorkspaceKey: "physical\x00default",
			workspaces: map[string]domain.Bootstrap{
				"physical\x00default": {Workspace: domain.Workspace{ID: "default-id", URLKey: "physicaldefault"}},
				"Case":                {Workspace: domain.Workspace{ID: "case-id", URLKey: "different-body-key"}},
				"trail ":              {Workspace: domain.Workspace{ID: "trailing-id", URLKey: "trail"}},
				" ":                   {Workspace: domain.Workspace{ID: "space-id", URLKey: "space"}},
			},
		}
		if bounded {
			source.metadataResidency = newMetadataResidency()
			source.metadataResidency.stamps["Case"] = copyStamp{workspaceID: "stale", revision: -1}
		}
		for _, ref := range []string{"", "physical\x00default", "physicaldefault", "Case", "case", "CASE", "different-body-key", "trail ", "trail", " ", "missing"} {
			identity, found := scaleP21SW0InstalledIdentity(t, source, ref)
			selected := ref
			if selected == "" {
				selected = source.lastWorkspaceKey
			}
			legacy, wantFound := source.WorkspaceMetadata(ref)
			if found != wantFound {
				t.Fatalf("bounded=%v ref=%q found=%v want=%v", bounded, ref, found, wantFound)
			}
			if !found {
				if identity != (InstalledWorkspaceIdentity{}) {
					t.Fatal("missing key returned a partial identity")
				}
				continue
			}
			if identity.RegistryKey != selected || identity.URLKey != legacy.Workspace.URLKey || identity.ID != legacy.Workspace.ID {
				t.Fatalf("bounded=%v ref=%q source=%+v legacy=%+v", bounded, ref, identity, legacy.Workspace)
			}
		}
		read, found, err := source.BeginInstalledWorkspaceIdentityRead(t.Context(), "")
		if err != nil || !found || read == nil {
			t.Fatal(err)
		}
		source.mu.Lock()
		delete(source.workspaces, "physical\x00default")
		source.lastWorkspaceKey = "Case"
		source.mu.Unlock()
		if read.Identity != (InstalledWorkspaceIdentity{"physical\x00default", "physicaldefault", "default-id"}) {
			t.Fatal("later publication mutated the detached identity")
		}
		if err := read.Close(); err != nil || read.Identity != (InstalledWorkspaceIdentity{}) {
			t.Fatal("close did not clear owned identity", err)
		}
		if err := read.Close(); err != nil {
			t.Fatal("repeated close", err)
		}
		if err := (*InstalledWorkspaceIdentityRead)(nil).Close(); err != nil {
			t.Fatal("nil close", err)
		}
		if err := (&InstalledWorkspaceIdentityRead{}).Close(); err != nil {
			t.Fatal("empty close", err)
		}
		if _, found := scaleP21SW0InstalledIdentity(t, source, "physical\x00default"); found {
			t.Fatal("removed registry key remained installed")
		}
		if next, found := scaleP21SW0InstalledIdentity(t, source, ""); !found || next.RegistryKey != "Case" {
			t.Fatal("default did not follow installed publication")
		}
		source.lastWorkspaceKey = "deleted"
		if _, found := scaleP21SW0InstalledIdentity(t, source, ""); found {
			t.Fatal("missing default invented a first-workspace fallback")
		}
	}
	for _, source := range []*SQLiteStore{
		{},
		{workspaces: map[string]domain.Bootstrap{}},
		{workspaces: map[string]domain.Bootstrap{"": {Workspace: domain.Workspace{ID: "empty-key-id"}}}},
	} {
		identity, found := scaleP21SW0InstalledIdentity(t, source, "")
		_, emptyExists := source.workspaces[""]
		if identity.RegistryKey != "" || found != emptyExists || found && identity.ID != "empty-key-id" {
			t.Fatal("empty installed key differs from legacy map semantics")
		}
	}
}

func TestScaleP21SW0InstalledWorkspaceOwnedAdmission(t *testing.T) {
	source := &SQLiteStore{
		lastWorkspaceKey: "key", metadataResidency: newMetadataResidency(),
		workspaces: map[string]domain.Bootstrap{"key": {Workspace: domain.Workspace{ID: "id", URLKey: "logical"}}},
	}
	budget := source.metadataResidency.work
	parent, releaseParent, err := source.admitMetadataWork(t.Context(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, found, err := source.BeginInstalledWorkspaceIdentityRead(parent, "")
	if err != nil || !found {
		t.Fatal("nested read", err)
	}
	if budget.used <= 1<<20 {
		t.Fatal("owned identity borrowed its parent's lifetime")
	}
	releaseParent()
	if budget.used == 0 || read.Identity.ID != "id" {
		t.Fatal("parent release dropped the live identity allowance")
	}
	if err := read.Close(); err != nil || budget.used != 0 {
		t.Fatal("identity allowance leaked", err)
	}
	if _, _, err := source.BeginInstalledWorkspaceIdentityRead(parent, ""); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("expired parent lease admitted a detached read", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := source.BeginInstalledWorkspaceIdentityRead(cancelled, ""); !errors.Is(err, context.Canceled) || read != nil || budget.used != 0 {
		t.Fatal("cancelled read retained work", err)
	}
	_, releaseFull, err := source.admitMetadataWork(t.Context(), metadataWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := source.BeginInstalledWorkspaceIdentityRead(t.Context(), ""); !errors.Is(err, ErrMetadataWorkBudget) || !found {
		t.Fatal("exhausted budget did not fail explicitly", err)
	}
	releaseFull()
	source.workspaces["key"] = domain.Bootstrap{Workspace: domain.Workspace{ID: strings.Repeat("x", installedWorkspaceIdentityMaxBytes+1)}}
	if _, found, err := source.BeginInstalledWorkspaceIdentityRead(t.Context(), ""); !found || !errors.Is(err, ErrMetadataWorkBudget) || budget.used != 0 {
		t.Fatal("oversized identity was copied or leaked work", err)
	}
	source.workspaces["key"] = domain.Bootstrap{Workspace: domain.Workspace{ID: "recovered"}}
	recovered, found, err := source.BeginInstalledWorkspaceIdentityRead(t.Context(), "")
	if err != nil || !found || recovered.Identity.ID != "recovered" {
		t.Fatal("budget recovery", err)
	}
	defer recovered.Close()
	large := strings.Repeat("x", 8<<20)
	key, logical, id := large[:3], large[3:6], large[6:9]
	detachedSource := &SQLiteStore{
		lastWorkspaceKey: key, metadataResidency: newMetadataResidency(),
		workspaces: map[string]domain.Bootstrap{key: {Workspace: domain.Workspace{ID: id, URLKey: logical}}},
	}
	detached, found, err := detachedSource.BeginInstalledWorkspaceIdentityRead(t.Context(), "")
	if err != nil || !found {
		t.Fatal(err)
	}
	defer detached.Close()
	if unsafe.StringData(detached.Identity.RegistryKey) == unsafe.StringData(key) ||
		unsafe.StringData(detached.Identity.URLKey) == unsafe.StringData(logical) ||
		unsafe.StringData(detached.Identity.ID) == unsafe.StringData(id) {
		t.Fatal("retained short identities pinned oversized source string backing")
	}
}

func TestScaleP21SW0InstalledWorkspaceConstantWork(t *testing.T) {
	var previous float64
	for _, size := range []int{64 << 10, 8 << 20} {
		large := strings.Repeat("x", size)
		source := &SQLiteStore{
			lastWorkspaceKey: "key", metadataResidency: newMetadataResidency(),
			workspaces: map[string]domain.Bootstrap{"key": {
				Workspace: domain.Workspace{ID: "id", URLKey: "canonical", Name: large, LogoURL: large},
				Projects:  []domain.Project{{ID: "large", Description: large}},
				Settings:  map[string]any{"ignored": large},
			}},
		}
		native := testing.AllocsPerRun(20, func() {
			read, found, err := source.BeginInstalledWorkspaceIdentityRead(t.Context(), "")
			if err != nil || !found || read.Identity.ID != "id" {
				panic("native registry fixture")
			}
			read.Close()
		})
		if previous != 0 && previous != native {
			t.Fatalf("ignored fields scaled allocations: %g/%g", previous, native)
		}
		previous = native
		if source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0 {
			t.Fatal("identity read decoded or retained unrelated metadata")
		}
		t.Logf("ignored bytes=%d registry allocations/op=%g", size, native)
	}
	long := strings.Repeat("\u754c", 192)
	source := &SQLiteStore{lastWorkspaceKey: long, workspaces: map[string]domain.Bootstrap{
		long: {Workspace: domain.Workspace{ID: "long-id", URLKey: long}},
	}}
	if identity, found := scaleP21SW0InstalledIdentity(t, source, ""); !found || identity.RegistryKey != long || identity.URLKey != long {
		t.Fatal("installed key gained an unsupported byte-size validation")
	}
}

func TestScaleP21SW0InstalledWorkspaceProducer(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if f.dialect == "postgres" {
		t.Skip("PostgreSQL text columns cannot retain a physical NUL key")
	}
	physical := idgen.New("sw0-installed-") + "\x00tail"
	ctx := ContextWithActor(t.Context(), f.data.Viewer)
	created, err := f.CreateWorkspace(ctx, "Installed physical source", physical, "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var err error
		if f.HasWorkspace(physical) {
			err = f.DeleteWorkspace(ctx, physical)
		} else {
			err = f.PurgeWorkspaceRows(ctx, WorkspacePurgeTarget{Key: physical, ID: created.Workspace.ID}, map[string]int64{})
		}
		if err != nil {
			t.Error(err)
		}
	})
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	legacy, ok := f.WorkspaceMetadata("")
	canonical, found, err := scaleP21SW30CanonicalMetadataWitness(t.Context(), probe, "", MetadataRecordScope{})
	if !ok || !found || err != nil || legacy.Workspace.URLKey != physical || canonical.Workspace.URLKey != strings.ReplaceAll(physical, "\x00", "") {
		t.Fatal("producer did not exercise physical/body distinction", err)
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		old, ok := source.WorkspaceMetadata("")
		for _, ref := range []string{physical, ""} {
			identity, found := scaleP21SW0InstalledIdentity(t, source, ref)
			if !found || !ok || identity.RegistryKey != physical || identity.ID != created.Workspace.ID || identity.URLKey != old.Workspace.URLKey {
				t.Fatalf("installed identity=%+v legacy logical=%q", identity, old.Workspace.URLKey)
			}
		}
	}
	if err := f.ReloadWorkspace(t.Context(), physical); err != nil {
		t.Fatal(err)
	}
	reloaded, ok := f.WorkspaceMetadata("")
	identity, found := scaleP21SW0InstalledIdentity(t, f.SQLiteStore, "")
	if !ok || !found || identity.RegistryKey != physical || identity.URLKey != reloaded.Workspace.URLKey || identity.URLKey != canonical.Workspace.URLKey {
		t.Fatal("reload changed source selection semantics")
	}
	if err := f.RemoveWorkspaceFromCatalog(t.Context(), physical, nil); err != nil {
		t.Fatal(err)
	}
	if _, found := scaleP21SW0InstalledIdentity(t, f.SQLiteStore, physical); found {
		t.Fatal("catalog removal left a stale installed identity")
	}
}
