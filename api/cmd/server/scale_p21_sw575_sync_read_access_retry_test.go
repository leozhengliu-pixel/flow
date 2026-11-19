package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// SW575: a sync records window read whose summary snapshot finds the
// workspace metadata changed under it (a write committed between the read's
// SQL revision and its copy check) reads again, a bounded number of times,
// and answers the post-change data; only metadata that keeps changing past
// the bound still answers the 503 it always did.
func TestScaleP21SW575SyncReadRetriesChangedAccessMetadata(t *testing.T) {
	scaleP19Options(t)
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			prefix := idgen.New("sw575-")
			open := prefix + "open"
			public := f.data.Teams[0].ID
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw575_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = []domain.Project{
					{ID: open, Name: "Open", TeamIDs: []string{public}, Status: data.ProjectStatuses[0]},
					{ID: prefix + "other", Name: "Other", TeamIDs: []string{public}, Status: data.ProjectStatuses[0]},
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			// The forced change mirrors the production writer (a key's
			// last-used time): it advances the workspace metadata revision
			// without advancing the authorization epoch, so the read must
			// answer it instead of revoking the viewer's access. A project
			// update header is not policy-bearing and reaches the window as
			// the project's latest update time.
			base := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
			minute := 0
			change := func(label string) time.Time {
				minute++
				at := base.Add(time.Duration(minute) * time.Minute)
				if err := repo.MutateWorkspace(context.Background(), workspace, "scale_p21.sw575_update", "", nil, func(data *domain.Bootstrap) error {
					if data.ProjectUpdates == nil {
						data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
					}
					data.ProjectUpdates[open] = []domain.ProjectUpdate{{ID: prefix + label, ProjectID: open, CreatedAt: at, Health: "onTrack", User: data.Viewer}}
					return nil
				}); err != nil {
					t.Error(err)
				}
				return at
			}
			read := func(ctx context.Context, client *http.Client, token string) *httptest.ResponseRecorder {
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, scaleP19Path("projects", scaleP19ProjectQuery(), "", 100), nil)
				r.Header.Set("X-Workspace-Key", workspace)
				r.Header.Set("X-Request-ID", "scale-p21-sw575")
				if token != "" {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				if client != nil {
					origin, _ := url.Parse(f.host.URL)
					for _, cookie := range client.Jar.Cookies(origin) {
						r.AddCookie(cookie)
					}
				}
				w := httptest.NewRecorder()
				f.host.Config.Handler.ServeHTTP(w, r)
				return w
			}
			// The flaky CI reads used a team-selected key, whose first use
			// writes its last-used time in the background. Settle that write
			// (later uses within the minute write nothing) so the forced
			// changes below are the only ones.
			selected := scaleP03Key(t, f, []string{"read"}, []string{public})
			if w := read(t.Context(), nil, selected); w.Code != http.StatusOK {
				t.Fatalf("warm read %d %.300s", w.Code, w.Body.String())
			}
			scaleP21SW575SettleKeyUse(t, repo, workspace, selected)

			bound := 0
			for _, principal := range []struct {
				name   string
				client *http.Client
				token  string
			}{{"admin", f.admin, ""}, {"key-selected", nil, selected}} {
				t.Run(principal.name, func(t *testing.T) {
					// Metadata changing on every attempt still answers the
					// unchanged 503 once the bound is reached.
					calls := 0
					always := store.WithAccessChangedReadHookForTesting(t.Context(), func(attempt int) {
						calls++
						change(fmt.Sprintf("%s-always-%d", principal.name, attempt))
					})
					w := read(always, principal.client, principal.token)
					var body map[string]any
					if w.Code != http.StatusServiceUnavailable || json.Unmarshal(w.Body.Bytes(), &body) != nil ||
						!reflect.DeepEqual(body, map[string]any{"code": "UNAVAILABLE", "type": "UNAVAILABLE", "requestId": "scale-p21-sw575", "error": "Sync records are not ready; use the legacy read endpoint"}) {
						t.Fatalf("changing metadata past the bound: %d %.300s", w.Code, w.Body.String())
					}
					// The old read took three snapshots back to back.
					if calls <= 3 || bound != 0 && calls != bound {
						t.Fatalf("snapshot attempts = %d (bound %d)", calls, bound)
					}
					bound = calls

					// Changes on all but the last attempt (and on more attempts
					// than the old read took) answer the post-change data, byte
					// for byte the response of a read that raced nothing.
					for _, changes := range []int{1, 3, bound - 1} {
						calls = 0
						var last time.Time
						ctx := store.WithAccessChangedReadHookForTesting(t.Context(), func(attempt int) {
							calls++
							if attempt <= changes {
								last = change(fmt.Sprintf("%s-%d-%d", principal.name, changes, attempt))
							}
						})
						started := time.Now()
						w := read(ctx, principal.client, principal.token)
						if w.Code != http.StatusOK {
							t.Fatalf("%d changes: %d %.300s", changes, w.Code, w.Body.String())
						}
						if calls != changes+1 {
							t.Fatalf("%d changes took %d snapshot attempts", changes, calls)
						}
						var page struct {
							Items         []domain.Project     `json:"items"`
							LatestUpdates map[string]time.Time `json:"latestUpdates"`
						}
						if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
							t.Fatal(err)
						}
						if !slices.ContainsFunc(page.Items, func(project domain.Project) bool { return project.ID == open }) || !page.LatestUpdates[open].Equal(last) {
							t.Fatalf("%d changes: read did not answer the post-change data (want %s): %.300s", changes, last, w.Body.String())
						}
						if quiet := read(t.Context(), principal.client, principal.token); quiet.Code != http.StatusOK || !bytes.Equal(quiet.Body.Bytes(), w.Body.Bytes()) {
							t.Fatalf("%d changes: retried response differs from a quiet read\n%s\n%s", changes, w.Body.String(), quiet.Body.String())
						}
						t.Logf("%d changes answered in %s", changes, time.Since(started))
					}
				})
			}
		})
	}
}

func scaleP21SW575SettleKeyUse(t *testing.T, repo *store.SQLiteStore, workspace, secret string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	hash := secretHash(secret)
	for {
		data, ok := repo.BootstrapForContext(ctx, workspace)
		if ok && slices.ContainsFunc(data.APIKeys, func(key domain.APIKey) bool { return key.SecretHash == hash && key.LastUsedAt != nil }) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("key use did not settle", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}
