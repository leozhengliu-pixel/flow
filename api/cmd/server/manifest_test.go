package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW24ManifestExportDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, targetID := f.data.Workspace.URLKey, idgen.New("oauth_app_sw24_")
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		target := domain.OAuthApplication{
			ID: targetID, Name: "Manifest export", ClientID: idgen.New("flow_client_sw24_"),
			ClientSecret: "sw24-private-client-secret", CreatorID: "other-creator",
			Description: "Visible application description", DeveloperName: "Fixture developer", DeveloperURL: "https://developer.example.test",
			LogoURL: "https://developer.example.test/icon.png", RedirectURIs: []string{"https://client.example.test/callback"},
			Scopes:         []string{"read", "write", "app:assignable", "app:mentionable"},
			WebhookEnabled: true, WebhookURL: "https://hooks.example.test", WebhookResourceTypes: []string{"Issue", "Project"},
			SupportsClientCredentials: true, PublicEnabled: true, GitHubUsername: "fixture[bot]", CreatedAt: now, UpdatedAt: now,
		}
		reset := func(large bool) {
			t.Helper()
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "oauthApplications"), workspace, "scale_p21_sw24.seed", "", nil, func(data *domain.Bootstrap) error {
				data.OAuthApplications = []domain.OAuthApplication{target}
				if large {
					data.OAuthApplications[0].Description = strings.Repeat("large selected application ", 18000)
				}
				for range 70 {
					data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: idgen.New("sw24-unrelated-app-"), Name: "Retained"})
				}
				data.OAuthApplications[1].Description = strings.Repeat("unrelated application ", 20000)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		reset(false)
		before := scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, id := range []string{targetID, targetID + "-missing", "Manifest export", strings.Repeat("x", 192), targetID + "\x00"} {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r.Method = http.MethodGet
					r.URL.Path = "/api/oauth-applications/" + id + "/manifest"
					r.SetPathValue("id", id)
					scaleP21M3Call(&server{store: source}, w, r, (*server).exportOAuthApplicationManifest)
					want := http.StatusNotFound
					if id == targetID {
						want = http.StatusOK
					}
					if w.Code != want {
						t.Fatalf("manifest export status=%d want=%d body=%s", w.Code, want, w.Body.String())
					}
					if strings.Contains(w.Body.String(), target.ClientSecret) || strings.Contains(w.Body.String(), target.ClientID) {
						t.Fatal("manifest export disclosed application credentials")
					}
					if id == targetID {
						expected := exportOAuthAppManifest(r, target)
						if !reflect.DeepEqual(scaleP21SW10JSON(t, expected), scaleP21M3JSON(t, w.Body.Bytes())) {
							t.Fatal("manifest lost optional fields, references, or nil/empty rules")
						}
					}
					results = append(results, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes())})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("manifest differential: %s", scaleP21M2Diff(results[0], results[1]))
				}
			}
		})
		withoutAdmin := &domain.APIKey{ID: "sw24-no-admin", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, withoutAdmin, nil), httptest.NewRecorder()
			r.SetPathValue("id", targetID)
			(&server{store: source}).exportOAuthApplicationManifest(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatal("key without admin scope exposed an application")
			}
		}
		member := f.principals[2].user
		queued := f.request(member, nil, nil)
		queued.SetPathValue("id", targetID)
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				w := httptest.NewRecorder()
				scaleP21M3Call(&server{store: source}, w, queued, (*server).exportOAuthApplicationManifest)
				if w.Code != http.StatusForbidden {
					t.Fatalf("%s export retained revoked authority", phase)
				}
			}
		}
		if before != scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications") || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("manifest export changed canonical application state or decoded full metadata")
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW24 manifest foreign", idgen.New("sw24-manifest-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		f.probe, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
			r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
			r.SetPathValue("id", targetID)
			scaleP21M3Call(&server{store: source}, w, r, (*server).exportOAuthApplicationManifest)
			if w.Code != http.StatusNotFound {
				t.Fatal("manifest escaped its workspace")
			}
		}
		reset(true)
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, withoutAdmin, nil), httptest.NewRecorder()
			r.SetPathValue("id", targetID)
			(&server{store: source}).exportOAuthApplicationManifest(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatal("hidden oversized application disclosed size")
			}
		}
		r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", targetID)
		(&server{store: f.probe}).exportOAuthApplicationManifest(w, r)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) {
			t.Fatal("selected oversized application lacked explicit budget error")
		}
		reset(false)
		r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", targetID)
		(&server{store: f.probe}).exportOAuthApplicationManifest(w, r)
		if w.Code != http.StatusOK {
			t.Fatal("manifest after budget failure did not recover")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", targetID)
		(&server{store: f.probe}).exportOAuthApplicationManifest(w, r.WithContext(ctx))
		if w.Body.Len() > 0 && w.Code < http.StatusBadRequest || strings.Contains(w.Body.String(), `"schemaVersion"`) || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("cancelled export released application metadata")
		}
	}
}
