package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP03SmallReadFixture(t *testing.T, repo *store.SQLiteStore, scale int) scaleP03PlatformFixture {
	t.Helper()
	f := scaleP03Fixture(t, repo)
	// The five-second delivery sweep mutates workspace settings; keep request
	// cost measurements independent of unrelated scheduled maintenance.
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p03.seeded", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "p03-target", Name: "Small target", TeamIDs: []string{data.Teams[0].ID}, Description: "Small body"})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "p03-initiative", Name: "Small initiative", ProjectIDs: []string{"p03-target"}, Creator: data.Viewer})
		body := strings.Repeat("x", scale*8<<10)
		for i := range 64 {
			id := fmt.Sprintf("p03-unrelated-%d", i)
			data.Projects = append(data.Projects, domain.Project{ID: id, Name: id, TeamIDs: []string{data.Teams[0].ID}, Description: body})
			raw, _ := json.Marshal(map[string]string{"id": id, "description": body})
			data.Trash = append(data.Trash, domain.TrashEntry{ID: id, ResourceType: "project", ResourceID: id, Payload: raw, DeletedBy: data.Viewer, DeletedAt: time.Now().UTC()})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func scaleP03SmallReadRequest(f scaleP03PlatformFixture, path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	origin, _ := url.Parse(f.host.URL)
	for _, cookie := range f.admin.Jar.Cookies(origin) {
		r.AddCookie(cookie)
	}
	return r
}

func scaleP03SQLStatements(t *testing.T, f scaleP03PlatformFixture, path string) (queries []string, resolves int) {
	t.Helper()
	r := scaleP03SmallReadRequest(f, path)
	var mu sync.Mutex
	r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(query string) {
		mu.Lock()
		defer mu.Unlock()
		queries = append(queries, query)
		if strings.HasPrefix(query, "SELECT team_id,role FROM team_memberships WHERE workspace_id=? AND user_id=? ORDER BY team_id") || strings.Contains(query, "LEFT JOIN team_memberships t ON t.workspace_id=m.workspace_id AND t.user_id=m.user_id") {
			resolves++
		}
	}))
	w := httptest.NewRecorder()
	f.host.Config.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
	}
	return queries, resolves
}

// The timed region has no observer; statement counts use one separate request
// through the same complete middleware chain and fixture.
func scaleP03MeasureRequestCosts(t *testing.T) {
	for _, scale := range []int{1, 10} {
		for driver, repo := range scaleP03ServerTestStores(t) {
			t.Run(fmt.Sprintf("%s/body%d", driver, scale), func(t *testing.T) {
				f := scaleP03SmallReadFixture(t, repo, scale)
				for _, mode := range []string{"off", "on"} {
					t.Setenv("FLOW_RECORD_ACCESS", mode)
					for _, path := range []string{"/api/projects/p03-target", "/api/initiatives/p03-initiative"} {
						t.Run(mode+path, func(t *testing.T) {
							queries, resolves := scaleP03SQLStatements(t, f, path)
							var failure string
							result := testing.Benchmark(func(b *testing.B) {
								for range b.N {
									w := httptest.NewRecorder()
									f.host.Config.Handler.ServeHTTP(w, scaleP03SmallReadRequest(f, path))
									if w.Code != http.StatusOK {
										failure = fmt.Sprintf("GET %s: %d %s", path, w.Code, w.Body)
										b.Fatal(failure)
									}
								}
							})
							t.Logf("driver=%s mode=%s body_scale=%d route=%s sql/request=%d resolves/request=%d %s %s", driver, mode, scale, path, len(queries), resolves, result.String(), result.MemString())
							if result.N == 0 {
								t.Fatalf("measurement did not run: %s", failure)
							}
							if mode == "on" && result.AllocedBytesPerOp() > 2<<20 {
								t.Fatalf("small detail allocated %d bytes, budget is 2 MiB", result.AllocedBytesPerOp())
							}
						})
					}
				}
			})
		}
	}
}

func TestScaleP03RequestSQLBudget(t *testing.T) {
	for _, scale := range []int{1, 10} {
		for driver, repo := range scaleP03ServerTestStores(t) {
			t.Run(fmt.Sprintf("%s/body%d", driver, scale), func(t *testing.T) {
				f := scaleP03SmallReadFixture(t, repo, scale)
				t.Setenv("FLOW_RECORD_ACCESS", "off")
				member, _ := scaleP03Join(t, f, "P03 cost member", fmt.Sprintf("p03-cost-%d@example.com", time.Now().UnixNano()), "member", f.data.Teams[0].ID)
				for _, state := range []string{"canonical", "installed"} {
					if state == "installed" {
						if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
							t.Fatal(err)
						}
					}
					for _, client := range []struct {
						name string
						http *http.Client
					}{{"owner", f.admin}, {"member", member}} {
						viewer := f
						viewer.admin = client.http
						for _, path := range []string{"/api/projects/p03-target", "/api/initiatives/p03-initiative"} {
							t.Setenv("FLOW_RECORD_ACCESS", "off")
							legacy, _ := scaleP03SQLStatements(t, viewer, path)
							t.Setenv("FLOW_RECORD_ACCESS", "on")
							enabled, resolves := scaleP03SQLStatements(t, viewer, path)
							t.Logf("principal=%s state=%s route=%s legacy_sql=%d on_sql=%d resolves=%d", client.name, state, path, len(legacy), len(enabled), resolves)
							if resolves != 1 || len(enabled) > len(legacy)+1 {
								t.Fatalf("state=%s route=%s: on SQL=%d, legacy=%d, resolves=%d; statements=%q", state, path, len(enabled), len(legacy), resolves, enabled)
							}
						}
					}
				}
			})
		}
	}
}
