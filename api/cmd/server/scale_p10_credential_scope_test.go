package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestScaleP10SyncAPIKeyTeamScopeRoutes(t *testing.T) {
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := apiKeyScopeFixture(t, repo)
			workspace := f.data.Workspace.URLKey
			for _, kind := range []string{"projects", "initiatives"} {
				for {
					progress, err := repo.BackfillMetadataCatalog(t.Context(), workspace, kind)
					if err != nil {
						t.Fatal(err)
					}
					if progress.Complete {
						break
					}
				}
			}
			epochs := map[string]bool{}
			for _, spec := range []struct {
				name, actor, restriction string
				teams                    []string
				wantTotal                int64
			}{
				{"selected-set", "user", "selected", []string{"scope-public"}, 2},
				{"selected-empty", "user", "selected", []string{}, 0},
				{"unrestricted", "user", "all", nil, 3},
				{"app-selected-set", "app", "selected", []string{"scope-public"}, 2},
				{"app-selected-empty", "app", "selected", []string{}, 0},
			} {
				t.Run(spec.name, func(t *testing.T) {
					secret, key := apiKeyScopeCredential(t, f, spec.actor, spec.restriction, spec.teams, []string{"read", "initiative:read"})
					read := func(method, path, body string) *httptest.ResponseRecorder {
						t.Helper()
						var response *httptest.ResponseRecorder
						for attempt := 0; attempt < 3; attempt++ {
							request := httptest.NewRequest(method, path, strings.NewReader(body))
							request.Header.Set("Authorization", "Bearer "+secret)
							response = httptest.NewRecorder()
							f.host.Config.Handler.ServeHTTP(response, request)
							if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "Sync access changed") {
								break
							}
							time.Sleep(10 * time.Millisecond)
						}
						return response
					}
					manifestResponse := read(http.MethodGet, "/api/sync/manifest", "")
					var manifest syncManifestResponse
					if manifestResponse.Code != http.StatusOK || json.Unmarshal(manifestResponse.Body.Bytes(), &manifest) != nil {
						t.Fatalf("manifest status=%d", manifestResponse.Code)
					}
					if manifest.ViewerID != key.SubjectID() || manifest.WorkspaceID != f.data.Workspace.ID || manifest.AuthzEpoch == "" || epochs[manifest.AuthzEpoch] {
						t.Fatal("manifest did not bind the effective credential")
					}
					epochs[manifest.AuthzEpoch] = true
					if spec.restriction == "selected" && (slices.Contains(manifest.Core.Viewer.AppTeamIDs, "scope-private") || spec.wantTotal == 0 && len(manifest.Core.Viewer.AppTeamIDs) != 0) {
						t.Fatal("manifest exposed viewer team references outside the key")
					}
					if slices.Contains(manifest.Capabilities, "sync-v2") || manifest.Collections["projects"].Ready || strings.Contains(manifestResponse.Body.String(), "scope-hidden-") {
						t.Fatal("uncertified manifest exposed hidden records or enabled sync")
					}
					for _, kind := range []string{"projects", "initiatives"} {
						response := read(http.MethodGet, "/api/sync/records?kind="+kind, "")
						if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "scope-hidden-") || strings.Contains(response.Body.String(), "issueCount") {
							t.Fatal("unready record page exposed credential-scoped data", response.Code)
						}
					}
					for _, projection := range []string{"summary", "detail"} {
						body := `{"keys":[{"kind":"projects","id":"scope-project"},{"kind":"projects","id":"scope-hidden-project"},{"kind":"initiatives","id":"scope-initiative"}],"projection":"` + projection + `"}`
						response := read(http.MethodPost, "/api/sync/batch", body)
						if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "scope-hidden-") || strings.Contains(response.Body.String(), "issueCount") {
							t.Fatal("unready record batch exposed references or counts", response.Code)
						}
					}
					empty := read(http.MethodPost, "/api/sync/batch", `{"keys":[]}`)
					var batch struct {
						Items      []json.RawMessage `json:"items"`
						AuthzEpoch string            `json:"authzEpoch"`
					}
					if empty.Code != http.StatusOK || json.Unmarshal(empty.Body.Bytes(), &batch) != nil || batch.Items == nil || len(batch.Items) != 0 || batch.AuthzEpoch == "" {
						t.Fatal("empty read batch lost its credential or empty shape", empty.Code)
					}
					var previous []byte
					for _, mode := range []string{"off", "on", "shadow"} {
						t.Setenv("FLOW_INDEXED_READS", mode)
						response := read(http.MethodGet, "/api/projects?includeTotal=true&limit=100", "")
						var page projectCustomerPage
						if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Total != spec.wantTotal {
							t.Fatal("credential directory changed its scoped count", response.Code, page.Total)
						}
						if spec.restriction == "selected" && strings.Contains(response.Body.String(), "scope-hidden-") {
							t.Fatal("indexed directory retained a hidden reference")
						}
						if previous != nil && !bytes.Equal(previous, response.Body.Bytes()) {
							t.Fatal("indexed flag bypassed the credential projection")
						}
						previous = bytes.Clone(response.Body.Bytes())
					}
				})
				t.Setenv("FLOW_INDEXED_READS", "on")
			}
		})
	}
}

func TestScaleP10SyncManifestViewerCredentialScope(t *testing.T) {
	for _, fixture := range scaleP03ServerTestStores(t) {
		f := apiKeyScopeFixture(t, fixture)
		for _, teams := range [][]string{{"scope-public"}, {}} {
			_, key := apiKeyScopeCredential(t, f, "app", "selected", teams, []string{"read"})
			access, err := fixture.ResolveAccess(t.Context(), f.data.Workspace.URLKey, key.SubjectID(), &key)
			if err != nil {
				t.Fatal(err)
			}
			core, err := fixture.ReadRecordCore(t.Context(), access)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(core.Viewer.AppTeamIDs, "scope-private") || len(teams) == 0 && len(core.Viewer.AppTeamIDs) != 0 {
				t.Fatal("manifest core did not intersect viewer references with the key")
			}
		}
	}
}
