package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW24LegacyIdentityIDs(s *SQLiteStore) []string {
	s.mu.RLock()
	ids := []string{}
	for _, data := range s.workspaces {
		for _, app := range data.OAuthApplications {
			ids = append(ids, app.ClientID)
		}
	}
	s.mu.RUnlock()
	slices.Sort(ids)
	return slices.Compact(ids)
}

func TestScaleP21SW24ApplicationIdentityIDsSource(t *testing.T) {
	large := strings.Repeat("ignored source ", 600000)
	for _, apps := range [][]domain.OAuthApplication{nil, {}, {
		{ClientID: "z"}, {ClientID: ""}, {ClientID: "duplicate"},
		{ClientID: "a\x00b"}, {ClientID: "ab"}, {ClientID: "Case"}, {ClientID: "case"},
		{ClientID: " trail "}, {ClientID: "\u754c"}, {ClientID: string([]byte{0xff})},
		{ClientID: strings.Repeat("long", 80)}, {ClientID: "duplicate"},
	}} {
		source := &SQLiteStore{
			workspaces: map[string]domain.Bootstrap{
				"physical\x00key": {OAuthApplications: apps, Settings: map[string]any{"ignored": large}},
				"other":           {OAuthApplications: []domain.OAuthApplication{{ClientID: "duplicate", Description: large}}},
			},
			metadataResidency: newMetadataResidency(),
		}
		source.metadataResidency.stamps["physical\x00key"] = copyStamp{workspaceID: "stale", revision: -1}
		want := scaleP21SW24LegacyIdentityIDs(source)
		read, err := source.beginApplicationIdentityIDsRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var actual []string
		originalCtx := context.WithValue(t.Context(), struct{ identity bool }{}, "caller")
		first, second := errors.New("first dispatch"), errors.New("later dispatch")
		got := syncApplicationIdentityIDs(originalCtx, read.ids, func(ctx context.Context, id string) error {
			if ctx != originalCtx || source.metadataResidency.work.used == 0 {
				t.Fatal("batch changed downstream context or released identity admission")
			}
			actual = append(actual, id)
			if len(actual) == 1 {
				return first
			}
			if len(actual) == 3 {
				return second
			}
			return nil
		})
		if !reflect.DeepEqual(actual, want) || !errors.Is(got, first) || len(want) >= 3 && !errors.Is(got, second) {
			t.Fatalf("installed order/compact/error join changed: actual=%q want=%q error=%v", actual, want, got)
		}
		if len(want) >= 3 && got.Error() != first.Error()+"\n"+second.Error() {
			t.Fatal("batch error order changed", got)
		}
		read.close()
		read.close()
		if read.ids != nil || source.metadataResidency.work.used != 0 || source.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("enumeration leaked work or decoded complete metadata")
		}
	}
	empty := &SQLiteStore{metadataResidency: newMetadataResidency()}
	if err := empty.SyncAllApplicationIdentities(t.Context()); err != nil || empty.metadataResidency.work.used != 0 {
		t.Fatal("empty batch changed nil result", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for _, bounded := range []bool{false, true} {
		blankName := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"source": {
			OAuthApplications: []domain.OAuthApplication{{ClientID: "enumerated-after-cancel"}},
		}}}
		if bounded {
			blankName.metadataResidency = newMetadataResidency()
		}
		if err := blankName.SyncAllApplicationIdentities(cancelled); err != nil {
			t.Fatal("enumeration introduced cancellation before the unchanged no-name decision", err)
		}
	}
}

func TestScaleP21SW24ApplicationIdentityIDsOwnership(t *testing.T) {
	large := strings.Repeat("x", 8<<20)
	id := large[100:110]
	source := &SQLiteStore{
		workspaces:        map[string]domain.Bootstrap{"workspace": {OAuthApplications: []domain.OAuthApplication{{ClientID: id}}}},
		metadataResidency: newMetadataResidency(),
	}
	parent, releaseParent, err := source.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	read, err := source.beginApplicationIdentityIDsRead(parent)
	if err != nil {
		t.Fatal(err)
	}
	if unsafe.StringData(read.ids[0]) == unsafe.StringData(id) {
		t.Fatal("short selected ID retained oversized source backing")
	}
	releaseParent()
	if source.metadataResidency.work.used == 0 {
		t.Fatal("parent release dropped the independent enumeration allowance")
	}
	source.mu.Lock()
	source.workspaces["workspace"] = domain.Bootstrap{}
	source.mu.Unlock()
	if len(read.ids) != 1 || read.ids[0] != id {
		t.Fatal("installed replacement mutated detached IDs")
	}
	read.close()
	if source.metadataResidency.work.used != 0 {
		t.Fatal("retired generation retained admission")
	}
	if read, err := source.beginApplicationIdentityIDsRead(parent); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("expired parent admitted an independent list", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if read, err := source.beginApplicationIdentityIDsRead(cancelled); read != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("direct helper cancellation changed", err)
	}
}

func TestScaleP21SW24ApplicationIdentityIDsBounds(t *testing.T) {
	source := &SQLiteStore{
		workspaces:        map[string]domain.Bootstrap{"workspace": {OAuthApplications: []domain.OAuthApplication{{ClientID: strings.Repeat("x", scaleP21SW24IdentityIDsMaxWork/2)}}}},
		metadataResidency: newMetadataResidency(),
	}
	if read, err := source.beginApplicationIdentityIDsRead(t.Context()); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || source.metadataResidency.work.used != 0 {
		t.Fatal("oversized ID copied before admission", err)
	}
	source.workspaces["workspace"] = domain.Bootstrap{OAuthApplications: []domain.OAuthApplication{{ClientID: "small"}}}
	full, releaseFull, err := source.admitMetadataWork(t.Context(), metadataWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if read, err := source.beginApplicationIdentityIDsRead(full); read != nil || !errors.Is(err, ErrMetadataWorkBudget) || source.metadataResidency.work.waiting != 0 {
		t.Fatal("saturated list waited or borrowed parent work", err)
	}
	releaseFull()
	recovered, err := source.beginApplicationIdentityIDsRead(t.Context())
	if err != nil {
		t.Fatal("enumeration budget did not recover", err)
	}
	recovered.close()
}

func TestScaleP21SW24ApplicationIdentityIDsAllocations(t *testing.T) {
	var previous float64
	for _, size := range []int{64 << 10, 8 << 20} {
		source := &SQLiteStore{
			workspaces: map[string]domain.Bootstrap{"source": {OAuthApplications: []domain.OAuthApplication{
				{ClientID: "b", Description: strings.Repeat("x", size)}, {ClientID: "a"}, {ClientID: "b"},
			}}},
			metadataResidency: newMetadataResidency(),
		}
		before := testing.AllocsPerRun(10, func() {
			if ids := scaleP21SW24LegacyIdentityIDs(source); !slices.Equal(ids, []string{"a", "b"}) {
				panic("legacy IDs")
			}
		})
		after := testing.AllocsPerRun(10, func() {
			read, err := source.beginApplicationIdentityIDsRead(t.Context())
			if err != nil {
				panic(err)
			}
			if err := syncApplicationIdentityIDs(t.Context(), read.ids, func(context.Context, string) error { return nil }); err != nil {
				panic(err)
			}
			read.close()
		})
		if previous != 0 && previous != after {
			t.Fatalf("ignored descriptions scaled enumeration allocations: %g/%g", previous, after)
		}
		previous = after
		t.Logf("ignored description bytes=%d allocations/op legacy enumeration=%g admitted detached batch=%g", size, before, after)
	}
}

func TestScaleP21SW24ApplicationIdentityIDsProducer(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, client, appID := f.data.Workspace.URLKey, idgen.New("sw24-client-"), idgen.New("sw24-application-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "oauthApplications"), workspace, "scale_p21_sw24.identity_seed", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{
			ID: appID, ClientID: client, Name: "Current identity", LogoURL: "https://example.test/current.png", Scopes: []string{"read"},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	installation := domain.ApplicationInstallation{
		ClientID: client, Name: "Stale identity", AvatarURL: "https://example.test/stale.png",
		Scopes: []string{"read"}, TeamIDs: []string{f.data.Teams[0].ID}, InstalledBy: f.data.Viewer.ID, Active: true,
	}
	stale, err := f.InstallApplication(t.Context(), workspace, installation, "sw24-test-webhook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := f.ErasePersonFromWorkspace(ctx, workspace, stale.UserID); err != nil {
			t.Error(err)
		}
		if err := f.EraseAccount(ctx, stale.UserID); err != nil {
			t.Error(err)
		}
	})
	for _, bounded := range []bool{false, true} {
		if _, err := f.InstallApplication(t.Context(), workspace, installation, ""); err != nil {
			t.Fatal(err)
		}
		// Decompose only enumeration against the SAME actual installed
		// source. The separate resident loader does not retain applications.
		previous := f.metadataResidency
		if bounded {
			f.metadataResidency = newMetadataResidency()
		}
		err := f.SyncAllApplicationIdentities(t.Context())
		if bounded && f.metadataResidency.work.used != 0 {
			t.Fatal("real sync batch leaked enumeration allowance")
		}
		f.metadataResidency = previous
		if err != nil {
			t.Fatal("real identity batch", err)
		}
		user, err := f.UserByID(t.Context(), stale.UserID)
		if err != nil || user.Name != "Current identity" || user.DisplayName != "Current identity" || user.AvatarURL != "https://example.test/current.png" {
			t.Fatal("downstream identity synchronization changed", user, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.SyncAllApplicationIdentities(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal("off batch cancellation changed", err)
	}
	previous := f.metadataResidency
	f.metadataResidency = newMetadataResidency()
	err = f.SyncAllApplicationIdentities(cancelled)
	used := f.metadataResidency.work.used
	f.metadataResidency = previous
	if !errors.Is(err, context.Canceled) || used != 0 {
		t.Fatal("enumeration changed downstream cancellation or leaked work", err)
	}
}
