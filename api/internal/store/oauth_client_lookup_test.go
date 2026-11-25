package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// SW411: OAuthApplicationByClientID on the legacy store, a warm bounded probe
// and a bounded view loaded from SQL. Every lookup (present, duplicate in a
// workspace, empty, case/space variants, NUL, unknown, built-in, nil and empty
// lists, a record larger than a page, unicode) must return the same value,
// nil-ness of its lists included, with the secret blank and its lists
// unshared; bounded, without decoding a workspace or retaining work. Across
// workspaces the original's first match is map iteration order; the bounded
// lookup takes the first workspace key.

func scaleP21SW411Lookup(t *testing.T, repo *SQLiteStore, client string) (domain.OAuthApplication, bool) {
	t.Helper()
	decodes := repo.WorkspaceMetadataDecodes()
	app, found := repo.OAuthApplicationByClientID(client)
	if repo.BoundedMetadataEnabled() && (repo.WorkspaceMetadataDecodes() != decodes || repo.metadataResidency.work.used != 0) {
		t.Fatalf("bounded lookup of %q decoded metadata or retained work %d", client, repo.metadataResidency.work.used)
	}
	if app.ClientSecret != "" {
		t.Fatalf("lookup of %q exposed the client secret", client)
	}
	return app, found
}

func TestScaleP21SW411OAuthApplicationByClientIDDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	ctx := ContextWithActor(t.Context(), f.data.Viewer)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 6, 7, 8, 9, 10, 123456789, time.FixedZone("x", 3600))
	large := strings.Repeat("large 界 description ", 30000)
	apps := []domain.OAuthApplication{
		{ID: "sw411-full", ClientID: "sw411-full", ClientSecret: "sw411-secret", Name: "Full 界", Description: "described", LogoURL: "https://example.test/logo.png",
			RedirectURIs: []string{"https://example.test/a", "https://example.test/b"}, Scopes: []string{"read", "write"}, SupportsClientCredentials: true,
			CredentialsAuthorizedBy: "usr-a", PublicEnabled: true, CreatorID: "usr-a", CreatedAt: at, UpdatedAt: at.Add(time.Hour),
			DeveloperName: "Dev", DeveloperURL: "https://example.test/dev", GitHubUsername: "bot[bot]",
			WebhookEnabled: true, WebhookURL: "https://hooks.example.test/x", WebhookResourceTypes: []string{"Issue", "Comment"}, WebhookSecretPrefix: "whsec"},
		{ID: "sw411-nil", ClientID: "sw411-nil", Name: "Nil lists"},
		{ID: "sw411-empty-lists", ClientID: "sw411-empty-lists", Name: "Empty lists", RedirectURIs: []string{}, Scopes: []string{}, WebhookResourceTypes: []string{}},
		{ID: "sw411-dup-1", ClientID: "sw411-dup", Name: "Dup first", ClientSecret: "dup-secret"},
		{ID: "sw411-dup-2", ClientID: "sw411-dup", Name: "Dup second"},
		{ID: "sw411-blank", Name: "Blank client"},
		{ID: "sw411-large", ClientID: "sw411-large", Name: "Large", Description: large, Scopes: []string{"read"}},
		{ID: "sw411-many-scopes", ClientID: "sw411-many-scopes", Name: "Many", Scopes: slices.Repeat([]string{"a"}, 20000)},
		{ID: "sw411-space", ClientID: " sw411-space ", Name: "Spaced"},
		{ID: "sw411-unicode", ClientID: "sw411-界", Name: "Unicode"},
	}
	if err := f.MutateWorkspace(ctx, workspace, "scale_p21_sw411.apps", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = append(data.OAuthApplications, apps...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	prefix := idgen.New("sw411-")
	var others []string
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		for _, key := range others {
			if err := f.DeleteWorkspace(cleanup, key); err != nil {
				t.Error(err)
			}
		}
	})
	for _, suffix := range []string{"-a", "-z"} {
		data, err := f.CreateWorkspace(ctx, "SW411 "+suffix, prefix+suffix, "us")
		if err != nil {
			t.Fatal(err)
		}
		others = append(others, data.Workspace.URLKey)
		if err := f.MutateWorkspace(ctx, data.Workspace.URLKey, "scale_p21_sw411.shared", "", nil, func(data *domain.Bootstrap) error {
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: "sw411-shared" + suffix, ClientID: "sw411-shared", Name: "Shared " + suffix, ClientSecret: "shared-secret", Scopes: []string{suffix}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	for _, key := range append([]string{workspace}, others...) {
		if err := probe.ReloadWorkspace(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := f.BoundedMetadataViewForTesting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	clients := []string{"sw411-full", "sw411-nil", "sw411-empty-lists", "sw411-dup", "", "sw411-large", "sw411-many-scopes", " sw411-space ", "sw411-space",
		"sw411-界", "SW411-FULL", "sw411-full ", "sw411-full\x00", "sw411-missing", BuiltinApplicationClientID, "sw411-shared-a"}
	compare := func(stage string) {
		t.Helper()
		for _, client := range clients {
			want, wantFound := scaleP21SW411Lookup(t, f.SQLiteStore, client)
			wantJSON, _ := json.Marshal(want)
			for name, view := range map[string]*SQLiteStore{"warm": probe, "reloaded": reloaded} {
				got, found := scaleP21SW411Lookup(t, view, client)
				gotJSON, _ := json.Marshal(got)
				// Equal as a load reads them: the legacy store keeps a writer's
				// time location and an empty omitempty list until reload; JSON
				// and the stored (non-omitempty) lists' nil-ness are exact.
				nils := func(app domain.OAuthApplication) [2]bool {
					return [2]bool{app.RedirectURIs == nil, app.Scopes == nil}
				}
				if found != wantFound || nils(got) != nils(want) || string(gotJSON) != string(wantJSON) {
					t.Fatalf("%s: %s lookup of %q changed: found %v/%v\nlegacy  %.400s\nbounded %.400s", stage, name, client, wantFound, found, wantJSON, gotJSON)
				}
				if found && len(got.RedirectURIs) > 0 {
					got.RedirectURIs[0] = "mutated"
					again, _ := scaleP21SW411Lookup(t, view, client)
					if again.RedirectURIs[0] == "mutated" {
						t.Fatal("lookup shares its redirect list")
					}
				}
			}
		}
	}
	compare("seeded")
	// The bounded lookup reads canonical SQL: a later write is seen without a
	// peer reload, as the legacy store sees its own writes.
	if err := f.MutateWorkspace(ctx, workspace, "scale_p21_sw411.rename", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.OAuthApplications {
			switch data.OAuthApplications[index].ID {
			case "sw411-full":
				data.OAuthApplications[index].Name = "Renamed"
				data.OAuthApplications[index].ClientSecret = "rotated"
			case "sw411-dup-1":
				data.OAuthApplications = slices.Delete(data.OAuthApplications, index, index+1)
				return nil
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	compare("after write")
	// The same client ID in two other workspaces: legacy returns either, in
	// map order; bounded returns the one of the first workspace key, and
	// skips a workspace it has not installed.
	seen := map[string]bool{}
	for range 64 {
		app, found := f.OAuthApplicationByClientID("sw411-shared")
		if !found {
			t.Fatal("legacy shared lookup missing")
		}
		seen[app.Name] = true
	}
	t.Logf("legacy shared client ID resolved to %d distinct applications over 64 lookups: %v", len(seen), seen)
	for name, view := range map[string]*SQLiteStore{"warm": probe, "reloaded": reloaded} {
		app, found := scaleP21SW411Lookup(t, view, "sw411-shared")
		if !found || app.Name != "Shared -a" {
			t.Fatalf("%s: shared lookup %v %q", name, found, app.Name)
		}
		for _, missing := range []string{others[0], others[1]} {
			view.mu.Lock()
			held := view.workspaces[missing]
			delete(view.workspaces, missing)
			view.mu.Unlock()
			legacyView := map[string]domain.Bootstrap{}
			f.mu.RLock()
			for key, data := range f.workspaces {
				if key != missing {
					legacyView[key] = data
				}
			}
			f.mu.RUnlock()
			want, wantFound := (&SQLiteStore{workspaces: legacyView}).OAuthApplicationByClientID("sw411-shared")
			got, found := scaleP21SW411Lookup(t, view, "sw411-shared")
			view.mu.Lock()
			view.workspaces[missing] = held
			view.mu.Unlock()
			if wantJSON, gotJSON := fmt.Sprint(json.Marshal(want)), fmt.Sprint(json.Marshal(got)); found != wantFound || wantJSON != gotJSON {
				t.Fatalf("%s: lookup did not skip uninstalled workspace %s: %q/%q", name, missing, got.Name, want.Name)
			}
		}
	}
	// Admission refused: not found, nothing retained (the bounded contract).
	limit := probe.metadataResidency.work.limit
	probe.metadataResidency.work.limit = 1
	if _, found := probe.OAuthApplicationByClientID("sw411-nil"); found || probe.metadataResidency.work.used != 0 {
		t.Fatal("refused admission returned an application or retained work")
	}
	probe.metadataResidency.work.limit = limit
	if err := f.MutateWorkspace(ctx, workspace, "scale_p21_sw411.remove", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = slices.DeleteFunc(data.OAuthApplications, func(app domain.OAuthApplication) bool { return strings.HasPrefix(app.ID, "sw411-") })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	compare("removed")
}
