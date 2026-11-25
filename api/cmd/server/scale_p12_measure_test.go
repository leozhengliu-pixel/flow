package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP12Request(workspace string, user domain.User) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/realtime/events?workspace="+workspace, nil)
	return r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, user))
}

// The same cold event and viewers are used before and after implementation.
// SQL is counted separately so observation does not affect the timed region.
func TestScaleP12Measurements(t *testing.T) {
	if os.Getenv("FLOW_SCALE_P12_MEASURE") != "1" {
		t.Skip("set FLOW_SCALE_P12_MEASURE=1 to measure fan-out")
	}
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		_, user := scaleP03Join(t, f, "P12 measurement", fmt.Sprintf("p12-measure-%d@example.test", time.Now().UnixNano()), "member", f.data.Teams[0].ID)
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "P12 measured issue", "teamId": f.data.Teams[0].ID}, f.data.Workspace.URLKey, http.StatusCreated)
		for _, viewers := range []int{1, 100, 2000} {
			t.Run(fmt.Sprint(viewers), func(t *testing.T) {
				for pass := range 3 {
					event := domain.RealtimeEvent{ID: fmt.Sprintf("p12-measure-%d-%d", viewers, pass), Type: "issue.updated", AggregateID: issue.ID, Payload: []byte(`{"title":"P12 measured issue"}`)}
					requests := make([]*http.Request, viewers)
					for i := range requests {
						requests[i] = scaleP12Request(f.data.Workspace.URLKey, user)
					}
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					start := time.Now()
					for _, r := range requests {
						_, visible, err := f.service.pagedRealtimeEvent(r, event)
						if err != nil || !visible {
							t.Fatalf("projection visible=%v err=%v", visible, err)
						}
					}
					elapsed := time.Since(start)
					runtime.ReadMemStats(&after)
					event.ID += "-sql"
					total, source := 0, 0
					for _, r := range requests {
						ctx := store.WithSQLStatementObserver(r.Context(), func(query string) {
							total++
							if strings.Contains(query, "issue_records") || strings.Contains(query, "workspace_metadata_records") || strings.Contains(query, "agent_tasks") {
								source++
							}
						})
						if _, _, err := f.service.pagedRealtimeEvent(r.WithContext(ctx), event); err != nil {
							t.Fatal(err)
						}
					}
					t.Logf("pass=%d viewers=%d sql/event=%d source_sql/event=%d ns/viewer=%d bytes/viewer=%d allocs/viewer=%d", pass, viewers, total, source, elapsed.Nanoseconds()/int64(viewers), (after.TotalAlloc-before.TotalAlloc)/uint64(viewers), (after.Mallocs-before.Mallocs)/uint64(viewers))
				}
			})
		}
	}
}
