package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP10ManifestAndUnreadyRoutes(t *testing.T) {
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			read := func(path string) *httptest.ResponseRecorder {
				return scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace)
			}
			t.Setenv("FLOW_SYNC_MANIFEST", "")
			t.Setenv("FLOW_INDEXED_READS", "")
			if got := read("/api/sync/manifest"); got.Code != http.StatusNotFound {
				t.Fatal("disabled manifest is not an old-server-compatible 404", got.Code)
			}
			if !f.service.notificationStateReadRequest(httptest.NewRequest(http.MethodGet, "/api/notifications/state", nil)) {
				state, missing := read("/api/notifications/state"), read("/api/notifications/p10-missing")
				if state.Code != missing.Code || !bytes.Equal(state.Body.Bytes(), missing.Body.Bytes()) {
					t.Fatal("absent notification-state package changed the legacy notification route")
				}
			}
			t.Setenv("FLOW_SYNC_MANIFEST", "on")
			w := read("/api/sync/manifest")
			if w.Code != http.StatusOK {
				t.Fatal("manifest unavailable", w.Code, w.Body)
			}
			var result syncManifestResponse
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Schema != 2 || result.WorkspaceID != f.data.Workspace.ID || result.ViewerID != f.data.Viewer.ID || result.AuthzEpoch == "" || result.Head == "" {
				t.Fatal("manifest identity contract missing")
			}
			if result.Capabilities == nil || slices.Contains(result.Capabilities, "sync-v2") || result.Collections["projects"].Ready {
				t.Fatal("unready dependencies advertised sync-v2")
			}
			if !slices.Contains(result.Core.Omitted, "favorites") || !slices.Contains(result.Core.Omitted, "teams") {
				t.Fatal("unloaded core fields were not marked omitted")
			}
			if w.Body.Len() > 256<<10 || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("manifest byte/cache budget violated")
			}
			if got := read("/api/sync/records?kind=projects"); got.Code != http.StatusNotFound {
				t.Fatal("manifest flag enabled indexed reads")
			}
			t.Setenv("FLOW_INDEXED_READS", "on")
			if got := read("/api/sync/records?kind=projects"); got.Code != http.StatusServiceUnavailable || strings.Contains(got.Body.String(), "p10-hidden") {
				t.Fatal("unready index was served", got.Code)
			}
			for _, query := range []string{"limit=101", "limit=bad", "maxBytes=1", "maxBytes=262145"} {
				if got := read("/api/sync/records?kind=projects&" + query); got.Code != http.StatusBadRequest {
					t.Fatal("invalid page budget accepted", query, got.Code)
				}
			}
			w = read("/api/sync/manifest")
			if w.Code != http.StatusOK {
				t.Fatal("manifest flag and readiness not independent")
			}
			t.Logf("manifest bytes=%d", w.Body.Len())
			for _, kind := range []string{"projects", "initiatives"} {
				for range 10 {
					progress, err := repo.BackfillMetadataCatalog(t.Context(), workspace, kind)
					if err != nil {
						t.Fatal(err)
					}
					if progress.Complete {
						break
					}
				}
			}
			if got := read("/api/sync/records?kind=projects"); got.Code != http.StatusServiceUnavailable {
				t.Fatal("a complete shadow pass was treated as writer coverage")
			}
			request := scaleP03SmallReadRequest(f, "/api/sync/manifest")
			statements := []string{}
			request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) { statements = append(statements, query) }))
			w = httptest.NewRecorder()
			f.host.Config.Handler.ServeHTTP(w, request)
			if w.Code != http.StatusOK {
				t.Fatal("observed manifest failed", w.Code)
			}
			for _, statement := range statements {
				if strings.Contains(statement, "SELECT data FROM workspace_metadata_records") && !strings.Contains(statement, "field='teams'") || strings.Contains(statement, "FROM issue_records") {
					t.Fatal("manifest read a canonical body", statement)
				}
			}
			t.Logf("manifest SQL/request=%d bytes=%d", len(statements), w.Body.Len())
		})
	}
}

func TestScaleP10BatchReadScopeAndBounds(t *testing.T) {
	t.Setenv("FLOW_INDEXED_READS", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			secret := scaleP03Key(t, f, []string{"read"}, nil)
			for _, test := range []struct {
				body string
				code int
			}{
				{`{"keys":[],"projection":"detail"}`, http.StatusOK},
				{`{"keys":[{"kind":"projects","id":"hidden"}]}`, http.StatusServiceUnavailable},
				{`{"keys":[],"unexpected":true}`, http.StatusBadRequest},
				{`{"keys":[]} {}`, http.StatusBadRequest},
				{`{"keys":[],"projection":"invalid"}`, http.StatusBadRequest},
				{`{"keys":[` + strings.TrimSuffix(strings.Repeat(`{"kind":"projects","id":"x"},`, 51), ",") + `]}`, http.StatusBadRequest},
				{strings.Repeat(" ", 33<<10) + `{}`, http.StatusBadRequest},
			} {
				var w *httptest.ResponseRecorder
				for attempt := 0; attempt < 3; attempt++ {
					r := httptest.NewRequest(http.MethodPost, "/api/sync/batch", strings.NewReader(test.body))
					r.Header.Set("Authorization", "Bearer "+secret)
					w = httptest.NewRecorder()
					f.host.Config.Handler.ServeHTTP(w, r)
					if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Sync access changed") {
						break
					}
					// P03 binds epochs to the complete metadata revision. The
					// first credential-use telemetry write can invalidate this
					// read too; clients retry the entire batch with fresh access.
					time.Sleep(10 * time.Millisecond)
				}
				if w.Code != test.code {
					t.Fatalf("read-scoped POST batch: status=%d want=%d body=%s", w.Code, test.code, w.Body)
				}
			}
		})
	}
}

func TestScaleP10LegacyConnectionsAndTokens(t *testing.T) {
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p10.fixture", "", nil, func(data *domain.Bootstrap) error {
				for i := range 4 {
					data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p10-%d", i), Name: fmt.Sprintf("P10 %d", i), TeamIDs: []string{data.Teams[0].ID}, PreviousSlugIDs: []string{fmt.Sprintf("old-p10-%d", i)}})
				}
				data.Favorites = append(data.Favorites, domain.Favorite{ID: "p10-favorite-project", UserID: f.data.Viewer.ID, ResourceType: "project", ResourceID: "p10-0"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			paths := []string{"teams", "users", "workflow-states", "issue-labels", "cycles", "projects", "project-milestones", "initiatives", "documents", "customers", "customer-needs", "releases", "webhooks", "comments"}
			for _, resource := range paths {
				for _, order := range []string{"createdAt", "updatedAt"} {
					path := "/api/" + resource + "?first=1&orderBy=" + order
					if resource == "comments" {
						path += "&projectId=p10-0"
					}
					t.Setenv("FLOW_INDEXED_READS", "off")
					want := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace)
					t.Setenv("FLOW_INDEXED_READS", "on")
					got := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace)
					if want.Code != got.Code || !bytes.Equal(want.Body.Bytes(), got.Body.Bytes()) {
						t.Fatal("legacy connection changed", resource, order, want.Code, got.Code)
					}
					var page connection[json.RawMessage]
					if want.Code == http.StatusOK && json.Unmarshal(want.Body.Bytes(), &page) == nil && page.PageInfo.EndCursor != nil {
						for _, suffix := range []string{"&after=", "&before="} {
							continuation := path + suffix + url.QueryEscape(*page.PageInfo.EndCursor)
							if suffix == "&before=" {
								continuation = strings.Replace(continuation, "first=1", "last=1", 1)
							}
							t.Setenv("FLOW_INDEXED_READS", "off")
							want := scaleP03Read(t, f, f.admin, "", http.MethodGet, continuation, workspace)
							t.Setenv("FLOW_INDEXED_READS", "on")
							got := scaleP03Read(t, f, f.admin, "", http.MethodGet, continuation, workspace)
							if want.Code != got.Code || !bytes.Equal(want.Body.Bytes(), got.Body.Bytes()) {
								t.Fatal("legacy continuation changed", resource, order)
							}
						}
						filterPath := path + "&filter=" + url.QueryEscape(`{"id":{"eq":"missing"}}`)
						t.Setenv("FLOW_INDEXED_READS", "off")
						want := scaleP03Read(t, f, f.admin, "", http.MethodGet, filterPath, workspace)
						t.Setenv("FLOW_INDEXED_READS", "on")
						got := scaleP03Read(t, f, f.admin, "", http.MethodGet, filterPath, workspace)
						if want.Code != got.Code || !bytes.Equal(want.Body.Bytes(), got.Body.Bytes()) {
							t.Fatal("legacy filter changed", resource)
						}
					}
				}
			}
			for _, path := range []string{"/api/projects?limit=1&includeTotal=true", "/api/projects/old-p10-0", "/api/bootstrap?projection=project-list"} {
				t.Setenv("FLOW_INDEXED_READS", "off")
				want := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace)
				t.Setenv("FLOW_INDEXED_READS", "on")
				got := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace)
				if want.Code != http.StatusOK || got.Code != want.Code || !bytes.Equal(want.Body.Bytes(), got.Body.Bytes()) {
					t.Fatal("legacy directory/favorite/slug response changed", path, want.Code, got.Code)
				}
			}
		})
	}
}

func TestScaleP10SyncResponseBudget(t *testing.T) {
	w := httptest.NewRecorder()
	writeSyncJSON(w, map[string]string{"title": strings.Repeat("<>&", 50000)}, 256<<10)
	if w.Code != http.StatusServiceUnavailable || w.Body.Len() > 256<<10 {
		t.Fatal("escaped JSON escaped the wire budget")
	}
}

func TestScaleP10ManifestAllocationWithBodyGrowth(t *testing.T) {
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	for _, scale := range []int{1, 10} {
		for driver, repo := range scaleP03ServerTestStores(t) {
			t.Run(fmt.Sprintf("%s/body%d", driver, scale), func(t *testing.T) {
				f := scaleP03SmallReadFixture(t, repo, scale)
				result := testing.Benchmark(func(b *testing.B) {
					for range b.N {
						w := httptest.NewRecorder()
						f.host.Config.Handler.ServeHTTP(w, scaleP03SmallReadRequest(f, "/api/sync/manifest"))
						if w.Code != http.StatusOK || w.Body.Len() > 256<<10 {
							b.Fatalf("manifest: status=%d bytes=%d", w.Code, w.Body.Len())
						}
					}
				})
				t.Logf("body_scale=%d %s %s", scale, result.String(), result.MemString())
				if result.N == 0 || result.AllocedBytesPerOp() > 2<<20 {
					t.Fatalf("manifest allocated %d bytes/op", result.AllocedBytesPerOp())
				}
			})
		}
	}
}
