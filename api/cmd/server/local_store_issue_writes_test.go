package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"flow/api/internal/domain"
)

func TestLocalStoreIssueWriteReplay(t *testing.T) {
	enableLocalStoreShadowTest(t)
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := resourceAccessScaleFixture(t, repo)
			workspace := f.data.Workspace.URLKey
			origin, _ := url.Parse(f.host.URL)
			created := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]string{
				"teamId": f.data.Teams[0].ID, "title": "Browser receipt fixture",
			}, workspace, http.StatusCreated)
			issuePath := "/api/issue-records/" + created.ID
			viewer := f.data.Viewer.ID
			access, err := repo.ResolveAccess(t.Context(), workspace, viewer, nil)
			if err != nil {
				t.Fatal(err)
			}
			mutationID := "667e3c46-1d81-4fc8-a556-776cdb6a906b"
			send := func(t *testing.T, handler http.Handler, viewer, epoch, auth, title string, expected int64, want int) domain.Issue {
				t.Helper()
				r := httptest.NewRequest(http.MethodPatch, f.host.URL+issuePath, bytes.NewReader(mustJSON(t, domain.IssueUpdateInput{Title: &title, ExpectedVersion: &expected})))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-Workspace-Key", workspace)
				r.Header.Set(localStoreHeader, viewer)
				r.Header.Set("X-Flow-Local-Mutation", mutationID)
				r.Header.Set("X-Flow-Local-Authz-Epoch", epoch)
				if auth != "" {
					r.Header.Set("Authorization", "Bearer "+auth)
				} else {
					for _, cookie := range f.admin.Jar.Cookies(origin) {
						r.AddCookie(cookie)
					}
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != want {
					var failure struct{ Error string }
					if w.Code >= 400 {
						_ = json.Unmarshal(w.Body.Bytes(), &failure)
					}
					t.Fatalf("local issue PATCH status %d, want %d (%s)", w.Code, want, failure.Error)
				}
				var item domain.Issue
				if want == http.StatusOK {
					if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
						t.Fatal(err)
					}
					if got := w.Header().Get("X-Flow-Local-Rebase-Version"); got != strconv.FormatInt(expected+1, 10) {
						t.Fatalf("receipt version = %q, want %d", got, expected+1)
					}
				}
				return item
			}
			legacy := f.host.Config.Handler
			send(t, legacy, viewer, "expired", "", "Expired authority", created.Version, http.StatusForbidden)
			send(t, legacy, "wrong-viewer", access.AuthzEpoch, "", "Other account", created.Version, http.StatusForbidden)
			token := scaleKey(t, f, []string{"read", "write"}, []string{f.data.Teams[0].ID})
			send(t, legacy, viewer, access.AuthzEpoch, token, "Key cannot replay", created.Version, http.StatusForbidden)
			// Issuance can change the access epoch.
			access, err = repo.ResolveAccess(t.Context(), workspace, viewer, nil)
			if err != nil {
				t.Fatal(err)
			}
			updated := send(t, legacy, viewer, access.AuthzEpoch, "", "One committed edit", created.Version, http.StatusOK)
			later := authRequest[domain.Issue](t, f.admin, http.MethodPatch, f.host.URL+issuePath, map[string]any{
				"title": "Later ordinary edit", "expectedVersion": updated.Version,
			}, workspace, http.StatusOK)
			bounded, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []struct {
				name    string
				handler http.Handler
			}{{"legacy", legacy}, {"bounded", newHandler(&server{store: bounded, uploadPath: t.TempDir()})}} {
				t.Run(mode.name, func(t *testing.T) {
					replayed := send(t, mode.handler, viewer, access.AuthzEpoch, "", updated.Title, created.Version, http.StatusOK)
					if replayed.Version != later.Version || replayed.Title != later.Title || updated.Version != created.Version+1 {
						t.Fatal("replay changed the committed issue")
					}
				})
			}
		})
	}
}
