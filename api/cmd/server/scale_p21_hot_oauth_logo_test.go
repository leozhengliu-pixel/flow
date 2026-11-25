package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotOAuthLogoAccessDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21M2Setup(t, repo)
	key := f.data.Workspace.URLKey
	name := "oauth_app_icon_" + idgen.Stamp() + ".png"
	url := "/uploads/" + name
	stamp := time.Date(2026, 11, 14, 12, 0, 0, 1, time.FixedZone("native-long-zone", 7*3600+123))
	app := domain.OAuthApplication{
		ID: idgen.New("hot-logo-"), Name: "warm\x00name", ClientID: idgen.New("hot-logo-client-"),
		LogoURL: url, CreatedAt: stamp, Description: strings.Repeat("unmatched body ", 240000),
	}
	for _, public := range []bool{false, true} {
		app.PublicEnabled = public
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "oauthApplications"), key, "scale_p21.hot_logo_seed", "", nil, func(data *domain.Bootstrap) error {
			data.OAuthApplications = []domain.OAuthApplication{app, app, {ID: "unmatched", LogoURL: url + "\x00", Description: app.Description}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, key); err != nil {
			t.Fatal(err)
		}
		for _, owner := range []*store.FileUpload{
			nil,
			{Purpose: store.FileUploadPurposeOAuthAppIcon, WorkspaceKey: key, UserID: f.data.Viewer.ID},
			{Purpose: store.FileUploadPurposeOAuthAppIcon, WorkspaceKey: "foreign", UserID: "foreign"},
			{Purpose: "asset", WorkspaceKey: key, UserID: f.data.Viewer.ID},
			{Purpose: store.FileUploadPurposeOAuthAppIcon, WorkspaceKey: key, UserID: f.data.Viewer.ID, ContentType: "image/custom"},
		} {
			f.matrix(t, func(t *testing.T, user domain.User, credential *domain.APIKey) {
				request := f.literalRequest(user, credential, nil)
				old := &server{store: repo}
				icon, wantFound := old.resolveOAuthAppIcon(name, owner)
				wantAllowed := wantFound && old.oauthAppIconReadable(request.Context(), icon, user.ID)
				got, gotFound, gotAllowed, err := (&server{store: f.probe}).boundedOAuthAppIconAccess(request.Context(), name, owner, user.ID, true)
				if err != nil || gotFound != wantFound || gotAllowed != wantAllowed || got.contentType != icon.contentType {
					t.Fatalf("public=%t owner=%v found=%t/%t readable=%t/%t type=%q/%q err=%v", public, owner, gotFound, wantFound, gotAllowed, wantAllowed, got.contentType, icon.contentType, err)
				}
				// Key restrictions and the request's selected workspace do not
				// grant or remove the original image membership permission.
				request.Header.Set("X-Workspace-Key", "other-workspace")
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				wantCanceled := wantFound && old.oauthAppIconReadable(ctx, icon, user.ID)
				_, found, allowed, err := (&server{store: f.probe}).boundedOAuthAppIconAccess(ctx, name, owner, user.ID, true)
				if err != nil || found != wantFound || allowed != wantCanceled {
					t.Fatalf("contextless source/canceled membership boundary changed: found=%t allowed=%t want=%t err=%v", found, allowed, wantCanceled, err)
				}
			})
		}
		for _, check := range []bool{false, true} {
			old := &server{store: repo}
			icon, found := old.resolveOAuthAppIcon(name, nil)
			want := found && (!check || old.oauthAppIconReadable(t.Context(), icon, ""))
			_, gotFound, got, err := (&server{store: f.probe}).boundedOAuthAppIconAccess(t.Context(), name, nil, "", check)
			if err != nil || gotFound != found || got != want {
				t.Fatal("anonymous or already-signed boundary changed", found, gotFound, got, want, err)
			}
		}
	}
}

func TestScaleP21HotOAuthLogoOwnedImageBoundary(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21M2Setup(t, repo)
	name := "oauth_app_icon_" + idgen.Stamp() + ".svg"
	app := domain.OAuthApplication{ID: idgen.New("hot-logo-"), ClientID: idgen.New("hot-logo-client-"), LogoURL: "/uploads/" + name, PublicEnabled: true}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "oauthApplications"), f.data.Workspace.URLKey, "scale_p21.hot_logo_seed", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = []domain.OAuthApplication{app}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{name, strings.TrimSuffix(name, ".svg") + ".exe", "ordinary.png"} {
		for _, check := range []bool{false, true} {
			want, found, allowed, err := (&server{store: repo}).ownedImageAccess(t.Context(), candidate, f.data.Viewer.ID, check)
			got, gotFound, gotAllowed, gotErr := (&server{store: f.probe}).ownedImageAccess(t.Context(), candidate, f.data.Viewer.ID, check)
			if err != nil || gotErr != nil || gotFound != found || gotAllowed != allowed || got.contentType != want.contentType || got.name != want.name {
				t.Fatal("finite image boundary differs", candidate, check, found, gotFound, allowed, gotAllowed, err, gotErr)
			}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, found, allowed, err := (&server{store: f.probe}).ownedImageAccess(ctx, name, f.data.Viewer.ID, true)
	if err != nil || !found || !allowed {
		t.Fatal("public native source incorrectly inherited SQL cancellation", found, allowed, err)
	}
	foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Foreign app", idgen.New("hot-logo-foreign-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
	privateName := "oauth_app_icon_" + idgen.Stamp() + ".png"
	private := domain.OAuthApplication{ID: idgen.New("hot-logo-app-"), Name: "Installed icon", ClientID: idgen.New("hot-logo-client-"), LogoURL: "/uploads/" + privateName, Scopes: []string{"read"}, RedirectURIs: []string{"https://example.test/callback"}, CreatedAt: time.Now().UTC()}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "oauthApplications"), foreign.Workspace.URLKey, "scale_p21.hot_logo_seed", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = []domain.OAuthApplication{private}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	for _, installed := range []bool{false, true} {
		if installed {
			app, err := repo.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), f.data.Workspace.URLKey, domain.ApplicationInstallation{
				ClientID: private.ClientID, Name: "Icon install", InstalledBy: f.data.Viewer.ID, Active: true, Scopes: []string{"read"}, TeamIDs: []string{f.public},
			}, "hot-logo-secret")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.appUsers = append(f.resources.appUsers, struct{ workspace, id string }{f.data.Workspace.URLKey, app.UserID})
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			request := f.literalRequest(user, key, nil)
			icon, found := (&server{store: repo}).resolveOAuthAppIcon(privateName, nil)
			want := found && (&server{store: repo}).oauthAppIconReadable(request.Context(), icon, user.ID)
			_, gotFound, got, err := (&server{store: f.probe}).boundedOAuthAppIconAccess(request.Context(), privateName, nil, user.ID, true)
			if err != nil || gotFound != found || got != want {
				t.Fatalf("installation=%t access=%t/%t found=%t/%t err=%v", installed, got, want, gotFound, found, err)
			}
			if installed && !got {
				t.Fatal("active installed-workspace member lost private icon")
			}
		})
	}
}
