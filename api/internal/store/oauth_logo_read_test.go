package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21HotLogoStore(t *testing.T, values map[string]domain.Bootstrap) *SQLiteStore {
	t.Helper()
	repo := &SQLiteStore{workspaces: map[string]domain.Bootstrap{}, metadataResidency: newMetadataResidency()}
	for key, data := range values {
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
		if err != nil {
			t.Fatal(err)
		}
		repo.workspaces[key] = domain.Bootstrap{Workspace: data.Workspace}
		repo.installMetadataLogicalSource(key, source)
	}
	t.Cleanup(repo.releaseMetadataLogicalSources)
	return repo
}

func TestScaleP21HotOAuthLogoNativeProjection(t *testing.T) {
	logo := "/uploads/oauth_app_icon_native.png"
	stamp := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("unrelated-zone", 7*3600+123))
	apps := make([]domain.OAuthApplication, 0, 66)
	for i := 0; i < 64; i++ {
		apps = append(apps, domain.OAuthApplication{ID: "duplicate", ClientID: "client", LogoURL: logo, PublicEnabled: i%2 == 0, CreatedAt: stamp})
	}
	apps = append(apps,
		domain.OAuthApplication{ID: "matched-large", ClientID: "cl\x00ient", LogoURL: logo, Description: strings.Repeat("matched", 500000), CreatedAt: stamp},
		domain.OAuthApplication{ID: "unmatched-large", ClientID: strings.Repeat("skip", 800000), LogoURL: "other", Description: strings.Repeat("skip", 800000), CreatedAt: stamp},
	)
	values := map[string]domain.Bootstrap{"physical\x00key": {OAuthApplications: apps}}
	literal := &SQLiteStore{workspaces: values}
	repo := scaleP21HotLogoStore(t, values)
	want := literal.OAuthApplicationsWithLogo(logo)
	var got []OAuthApplicationLogoRef
	var expired OAuthApplicationLogoMatch
	found, err := repo.VisitOAuthApplicationLogos(t.Context(), logo, nil, func(match OAuthApplicationLogoMatch) (bool, error) {
		expired = match
		err := match.WithClientID(func(clientID string) error {
			got = append(got, OAuthApplicationLogoRef{WorkspaceKey: match.WorkspaceKey, Application: domain.OAuthApplication{ClientID: clientID, PublicEnabled: match.PublicEnabled}})
			return nil
		})
		return false, err
	})
	if err != nil || !found || len(got) != len(want) {
		t.Fatal("native projection lost duplicates or matched rows", found, len(got), len(want), err)
	}
	for i := range want {
		if got[i].WorkspaceKey != want[i].WorkspaceKey || got[i].Application.ClientID != want[i].Application.ClientID || got[i].Application.PublicEnabled != want[i].Application.PublicEnabled {
			t.Fatalf("match %d changed native scalar values", i)
		}
	}
	if err := expired.WithClientID(func(string) error { t.Fatal("expired callback ran"); return nil }); err == nil {
		t.Fatal("match escaped borrowed lifetime")
	}
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("logo traversal leaked work")
	}
}

func TestScaleP21HotOAuthLogoAtomicRegistryAndErrors(t *testing.T) {
	logo := "same\x00logo"
	values := map[string]domain.Bootstrap{
		"physical-a": {Workspace: domain.Workspace{URLKey: "logical-old"}, OAuthApplications: []domain.OAuthApplication{{ClientID: "a-old", LogoURL: logo}}},
		"physical-b": {OAuthApplications: []domain.OAuthApplication{{ClientID: "b-old", LogoURL: logo}}},
	}
	repo := scaleP21HotLogoStore(t, values)
	var seen []string
	_, err := repo.VisitOAuthApplicationLogos(t.Context(), logo, nil, func(match OAuthApplicationLogoMatch) (bool, error) {
		if len(seen) == 0 {
			for key := range values {
				next := domain.Bootstrap{OAuthApplications: []domain.OAuthApplication{{ClientID: "replacement", LogoURL: logo}}}
				source, err := captureMetadataLogicalSource(t.Context(), next, copyStamp{})
				if err != nil {
					return false, err
				}
				repo.installMetadataLogicalSource(key, source)
			}
		}
		return false, match.WithClientID(func(clientID string) error { seen = append(seen, clientID); return nil })
	})
	slices.Sort(seen)
	if err != nil || !slices.Equal(seen, []string{"a-old", "b-old"}) {
		t.Fatal("registry callback mixed installed generations", seen, err)
	}
	missing := "logical-old"
	found, err := repo.VisitOAuthApplicationLogos(t.Context(), logo, &missing, func(OAuthApplicationLogoMatch) (bool, error) {
		t.Fatal("logical key substituted for physical key")
		return false, nil
	})
	if err != nil || found {
		t.Fatal("physical workspace restriction changed", found, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repo.VisitOAuthApplicationLogos(ctx, logo, nil, func(OAuthApplicationLogoMatch) (bool, error) { return false, nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled traversal did not fail", err)
	}
	failure := errors.New("visitor failed")
	if _, err := repo.VisitOAuthApplicationLogos(t.Context(), logo, nil, func(OAuthApplicationLogoMatch) (bool, error) { return false, failure }); !errors.Is(err, failure) {
		t.Fatal("visitor failure changed", err)
	}
	oldLimit := repo.metadataResidency.work.limit
	repo.metadataResidency.work.limit = 1
	if _, err := repo.VisitOAuthApplicationLogos(t.Context(), logo, nil, func(OAuthApplicationLogoMatch) (bool, error) { return false, nil }); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("saturated traversal did not reject before callback", err)
	}
	repo.metadataResidency.work.limit = oldLimit
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("failed traversal leaked work")
	}
}

func TestScaleP21HotOAuthLogoInstallationsDetached(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	f.db.SetMaxOpenConns(1)
	clientID := "hot-logo-client-" + f.data.Viewer.ID
	second, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "Logo second", "hot-logo-"+f.data.Viewer.ID, "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.DeleteWorkspace(context.Background(), second.Workspace.URLKey) })
	workspaces := []string{f.data.Workspace.URLKey, second.Workspace.URLKey}
	ids := []string{"hot-logo-a-" + f.data.Viewer.ID, "hot-logo-b-" + f.data.Viewer.ID}
	for i, id := range ids {
		_, err := f.db.ExecContext(t.Context(), `INSERT INTO application_installations(id,workspace_key,client_id,user_id,data,webhook_secret) VALUES(?,?,?,?,?,?)`,
			id, workspaces[i], clientID, id+"-user", `{}`, "")
		if err != nil {
			t.Fatal("installation fixture", i, err)
		}
		t.Cleanup(func() {
			_, _ = f.db.ExecContext(context.Background(), `DELETE FROM application_installations WHERE id=?`, id)
		})
	}
	legacySQL, boundedSQL := 0, 0
	legacyContext := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "application_installations") {
			legacySQL++
		}
	})
	want, err := f.ApplicationInstallationsByClient(legacyContext, clientID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	boundedContext := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "application_installations") {
			boundedSQL++
		}
	})
	_, err = f.VisitOAuthLogoInstallations(boundedContext, clientID, func(workspace string) bool {
		if _, err := f.WorkspaceIDForKey(t.Context(), workspace); err != nil {
			t.Fatal("detached callback could not query on pool-one", err)
		}
		got = append(got, workspace)
		return false
	})
	expected := make([]string, len(want))
	for i := range want {
		expected[i] = want[i].WorkspaceKey
	}
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatal("installation order changed", got, expected, err)
	}
	if legacySQL != 1 || boundedSQL != 2 {
		t.Fatal("installation extraction SQL bound changed", legacySQL, boundedSQL)
	}
	t.Logf("installation extraction SQL: literal=%d bounded=%d (one scalar maximum-byte preflight)", legacySQL, boundedSQL)
}

func TestScaleP21HotOAuthLogoWarmAndReloaded(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	logo := "/uploads/" + f.data.Viewer.ID + "\x00.png"
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "oauthApplications"), workspace, "scale_p21.hot_logo_seed", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = []domain.OAuthApplication{{ID: "hot-logo-" + f.data.Viewer.ID, ClientID: "client\x00native", LogoURL: logo, PublicEnabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, reload := range []bool{false, true} {
		if reload {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, selected := range []string{logo, strings.ReplaceAll(logo, "\x00", "")} {
			want := f.OAuthApplicationsWithLogo(selected)
			var got []string
			found, err := view.VisitOAuthApplicationLogos(t.Context(), selected, nil, func(match OAuthApplicationLogoMatch) (bool, error) {
				return false, match.WithClientID(func(clientID string) error { got = append(got, clientID); return nil })
			})
			if err != nil || found != (len(want) > 0) || len(got) != len(want) {
				t.Fatal("warm/reloaded membership differs", reload, found, got, want, err)
			}
			for i := range want {
				if got[i] != want[i].Application.ClientID {
					t.Fatal("canonical normalization replaced installed native client identity", reload, got[i], want[i].Application.ClientID)
				}
			}
		}
		view.releaseMetadataLogicalSources()
	}
}
