package store

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW118NativeViewer(suffix string) domain.User {
	at := time.Now().Add(13 * time.Minute)
	return domain.User{
		App: true, BuiltinAgent: true, OAuthClientID: "client-" + suffix,
		AppScopes:         []string{"write", "", "write", "\xffnative"},
		AppTeamIDs:        []string{"team-" + suffix, "", "team-" + suffix},
		AppAllPublicTeams: true, OutOfOfficeUntil: &at,
		ID: "global-" + suffix, UserID: "user-" + suffix,
		Name: "Native \x00" + suffix, DisplayName: "Display " + suffix,
		Username: "handle-" + suffix, JobTitle: "Engineer", Email: suffix + "@example.test",
		AvatarURL: "data:image/png;base64," + strings.Repeat("a", 257),
		Active:    true, EmailVerified: true, Deleted: true,
		Via: &domain.ActorBot{
			ID: "bot-" + suffix, Type: domain.ActorBotTypeAgent,
			Name: "Bot", AvatarURL: "bot-avatar", UserDisplayName: "External",
			UserAvatarURL: "external-avatar", OnBehalfOfID: "principal",
			OnBehalfOfName: "Principal",
		},
	}
}

func scaleP21SW118SetViewer(repo *SQLiteStore, viewer domain.User) {
	repo.mu.Lock()
	repo.viewer = viewer
	repo.mu.Unlock()
}

func scaleP21SW118AssertViewer(t *testing.T, got, want domain.User) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default viewer differs from literal Account().Viewer: got=%#v want=%#v", got, want)
	}
	if got.Via != want.Via || got.OutOfOfficeUntil != want.OutOfOfficeUntil {
		t.Fatal("default viewer changed native pointer ownership")
	}
	for _, pair := range [][2][]string{{got.AppScopes, want.AppScopes}, {got.AppTeamIDs, want.AppTeamIDs}} {
		if len(pair[0]) > 0 && &pair[0][0] != &pair[1][0] {
			t.Fatal("default viewer cloned a shallow Account viewer slice")
		}
		if cap(pair[0]) != cap(pair[1]) {
			t.Fatal("default viewer changed slice capacity")
		}
	}
}

func TestScaleP21SW118DefaultViewerLiteralOracle(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	native := scaleP21SW118NativeViewer("oracle")
	fixed := native
	at := time.Date(2026, 11, 15, 1, 2, 3, 456789123, time.FixedZone("sw118-native", 5*3600+45*60))
	fixed.OutOfOfficeUntil = &at
	empty := native
	empty.AppScopes, empty.AppTeamIDs = []string{}, []string{}
	empty.OutOfOfficeUntil, empty.Via = nil, nil
	for _, source := range []struct {
		name string
		repo *SQLiteStore
	}{{"legacy", f.SQLiteStore}, {"bounded", view}} {
		for _, value := range []struct {
			name   string
			viewer domain.User
		}{{"zero", domain.User{}}, {"native-monotonic", native}, {"native-location", fixed}, {"empty-slices", empty}} {
			t.Run(source.name+"/"+value.name, func(t *testing.T) {
				scaleP21SW118SetViewer(source.repo, value.viewer)
				for _, selected := range []string{workspace, "", "sw118-absent"} {
					source.repo.mu.Lock()
					source.repo.lastWorkspaceKey = selected
					data := source.repo.workspaces[workspace]
					data.Viewer = domain.User{ID: "workspace-only", Active: true}
					source.repo.workspaces[workspace] = data
					source.repo.mu.Unlock()
					want := source.repo.Account().Viewer
					beforeDecodes := source.repo.WorkspaceMetadataDecodes()
					scaleP21SW118AssertViewer(t, source.repo.DefaultViewer(), want)
					if source.repo.MetadataWorkUsedForTesting() != 0 || source.repo.WorkspaceMetadataDecodes() != beforeDecodes {
						t.Fatal("scalar default viewer retained work or hydrated metadata")
					}
				}
			})
		}
	}
}

func TestScaleP21SW118DefaultViewerWarmReloadAndRevocation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	native := scaleP21SW118NativeViewer("warm")
	scaleP21SW118SetViewer(f.SQLiteStore, native)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	// Test view construction intentionally clones its viewer; each getter's
	// oracle is its own literal Account result, including native ownership.
	scaleP21SW118AssertViewer(t, view.DefaultViewer(), view.Account().Viewer)
	view.mu.Lock()
	data := view.workspaces[workspace]
	data.Viewer = domain.User{ID: "sw118-stale-workspace"}
	view.workspaces[workspace] = data
	view.lastWorkspaceKey = ""
	view.mu.Unlock()
	for _, reload := range []func() error{
		func() error { return view.ReloadWorkspace(t.Context(), workspace) },
		func() error {
			return view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace)
		},
	} {
		if err := reload(); err != nil {
			t.Fatal(err)
		}
		scaleP21SW118AssertViewer(t, view.DefaultViewer(), view.Account().Viewer)
		if view.DefaultViewer().ID != native.ID {
			t.Fatal("workspace reload substituted the workspace viewer for the global viewer")
		}
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		for _, state := range []string{"inactive", "deleted", "replacement", "cleared"} {
			next := scaleP21SW118NativeViewer(state)
			switch state {
			case "inactive":
				next.Active = false
			case "deleted":
				next.Active, next.Deleted, next.Name, next.Email = false, true, "", ""
			case "cleared":
				next = domain.User{}
			}
			scaleP21SW118SetViewer(source, next)
			scaleP21SW118AssertViewer(t, source.DefaultViewer(), source.Account().Viewer)
		}
	}
}

func TestScaleP21SW118DefaultViewerShallowOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	viewer := scaleP21SW118NativeViewer("ownership")
	viewer.AppScopes = append(make([]string, 0, 16), viewer.AppScopes...)
	viewer.AppTeamIDs = append(make([]string, 0, 12), viewer.AppTeamIDs...)
	scaleP21SW118SetViewer(f.SQLiteStore, viewer)
	want, got := f.Account().Viewer, f.DefaultViewer()
	scaleP21SW118AssertViewer(t, got, want)
	got.Name = "caller scalar"
	if f.DefaultViewer().Name != viewer.Name {
		t.Fatal("caller scalar assignment changed the installed viewer")
	}
	// Account and the scalar getter both borrow these native references.
	got.AppScopes[0], got.AppTeamIDs[0] = "caller-scope", "caller-team"
	got.Via.Name = "Caller bot"
	*got.OutOfOfficeUntil = got.OutOfOfficeUntil.Add(time.Hour)
	scaleP21SW118AssertViewer(t, f.DefaultViewer(), f.Account().Viewer)
	current := f.DefaultViewer()
	if current.AppScopes[0] != "caller-scope" || current.AppTeamIDs[0] != "caller-team" || current.Via.Name != "Caller bot" {
		t.Fatal("getter changed Account's shallow native ownership")
	}
}

func TestScaleP21SW118DefaultViewerNoSQLOrWork(t *testing.T) {
	viewer := scaleP21SW118NativeViewer("no-sql")
	viewer.AppScopes = make([]string, 8192)
	viewer.AppTeamIDs = make([]string, 8192)
	for _, bounded := range []bool{false, true} {
		repo := &SQLiteStore{viewer: viewer}
		if bounded {
			repo.metadataResidency = newMetadataResidency()
			repo.metadataResidency.work.limit = 1
			_, release, err := repo.admitMetadataWork(t.Context(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if repo.MetadataWorkUsedForTesting() != 1 {
				t.Fatal("fixture did not saturate its work allowance")
			}
			scaleP21SW118AssertViewer(t, repo.DefaultViewer(), viewer)
			if repo.MetadataWorkUsedForTesting() != 1 {
				t.Fatal("getter changed the caller's saturated allowance")
			}
			release()
		}
		var got domain.User
		allocations := testing.AllocsPerRun(100, func() { got = repo.DefaultViewer() })
		scaleP21SW118AssertViewer(t, got, viewer)
		if allocations != 0 || repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
			t.Fatalf("scalar getter allocated, retained work, or hydrated metadata: bounded=%t allocs=%v", bounded, allocations)
		}
	}
}

func TestScaleP21SW118DefaultViewerConcurrentReplacement(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	first, second := scaleP21SW118NativeViewer("concurrent-first"), scaleP21SW118NativeViewer("concurrent-second")
	first.Active, second.Active = true, false
	scaleP21SW118SetViewer(f.SQLiteStore, first)
	first = f.Account().Viewer
	scaleP21SW118SetViewer(f.SQLiteStore, second)
	second = f.Account().Viewer
	repo := &SQLiteStore{viewer: first, metadataResidency: newMetadataResidency()}
	start := make(chan struct{})
	bad := make(chan domain.User, 1)
	var workers sync.WaitGroup
	workers.Add(5)
	go func() {
		defer workers.Done()
		<-start
		for at := 0; at < 1000; at++ {
			next := first
			if at%2 == 0 {
				next = second
			}
			scaleP21SW118SetViewer(repo, next)
		}
	}()
	for at := 0; at < 4; at++ {
		go func() {
			defer workers.Done()
			<-start
			for read := 0; read < 1000; read++ {
				got := repo.DefaultViewer()
				if !reflect.DeepEqual(got, first) && !reflect.DeepEqual(got, second) {
					select {
					case bad <- got:
					default:
					}
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	select {
	case got := <-bad:
		t.Fatalf("concurrent replacement exposed a torn viewer: %#v", got)
	default:
	}
	if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("concurrent scalar reads retained work or decoded metadata")
	}
}
