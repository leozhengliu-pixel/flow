package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Include the last authorization check and transport write in the comparison.
func TestScaleP12EpochFanoutMeasurements(t *testing.T) {
	if os.Getenv("FLOW_SCALE_P12_MEASURE") != "1" {
		t.Skip("set FLOW_SCALE_P12_MEASURE=1 to measure delivery")
	}
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		_, user := scaleP03Join(t, f, "P12 epoch measurement", fmt.Sprintf("p12-epoch-%d@example.test", time.Now().UnixNano()), "member", f.data.Teams[0].ID)
		issue := authRequest[domain.Issue](t, f.admin, "POST", f.host.URL+"/api/issue-records", map[string]any{"title": "P12 delivery measurement", "teamId": f.data.Teams[0].ID}, f.data.Workspace.URLKey, 201)
		for _, viewers := range []int{1, 100, 2000} {
			for pass := range 3 {
				for _, mode := range []string{"off", "on"} {
					t.Setenv("FLOW_SYNC_V2", mode)
					event := domain.RealtimeEvent{ID: fmt.Sprintf("p12-delivery-%d-%d-%s", viewers, pass, mode), Type: "issue.updated", AggregateID: issue.ID, Payload: []byte(`{}`)}
					request := scaleP12Request(f.data.Workspace.URLKey, user)
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					latencies := make([]int64, viewers)
					start := time.Now()
					for i := range viewers {
						began := time.Now()
						if !f.service.writeProjectedRealtime(httptest.NewRecorder(), request, event) {
							t.Fatal("delivery failed")
						}
						latencies[i] = time.Since(began).Nanoseconds()
					}
					elapsed := time.Since(start)
					runtime.ReadMemStats(&after)
					event.ID += "-sql"
					sqlCount := 0
					ctx := store.WithSQLStatementObserver(request.Context(), func(string) { sqlCount++ })
					for range viewers {
						if !f.service.writeProjectedRealtime(httptest.NewRecorder(), request.WithContext(ctx), event) {
							t.Fatal("observed delivery failed")
						}
					}
					slices.Sort(latencies)
					t.Logf("pass=%d mode=%s viewers=%d sql/event=%d ns/viewer=%d p95_ns/viewer=%d bytes/viewer=%d allocs/viewer=%d", pass, mode, viewers, sqlCount, elapsed.Nanoseconds()/int64(viewers), latencies[(viewers-1)*95/100], (after.TotalAlloc-before.TotalAlloc)/uint64(viewers), (after.Mallocs-before.Mallocs)/uint64(viewers))
				}
			}
		}
	}
}

func TestScaleP12LiveFanoutMeasurements(t *testing.T) {
	if os.Getenv("FLOW_SCALE_P12_MEASURE") != "1" {
		t.Skip("set FLOW_SCALE_P12_MEASURE=1 to measure registered live fan-out")
	}
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		_, user := scaleP03Join(t, f, "P12 live measurement", fmt.Sprintf("p12-live-%d@example.test", time.Now().UnixNano()), "member", f.data.Teams[0].ID)
		issue := authRequest[domain.Issue](t, f.admin, "POST", f.host.URL+"/api/issue-records", map[string]any{"title": "P12 registered viewers", "teamId": f.data.Teams[0].ID}, f.data.Workspace.URLKey, 201)
		for _, viewers := range []int{1, 100, 2000} {
			t.Run(fmt.Sprint(viewers), func(t *testing.T) {
				for range viewers {
					_, stop := f.service.realtime.subscribe(f.data.Workspace.URLKey)
					t.Cleanup(stop)
				}
				run := func(event domain.RealtimeEvent, observed bool) (int64, int64, uint64, uint64, int64, int64, int64) {
					var workers sync.WaitGroup
					var statements, epochs, failures atomic.Int64
					start := make(chan struct{})
					latencies := make([]int64, viewers)
					runtime.GC()
					var before, after runtime.MemStats
					runtime.ReadMemStats(&before)
					began := time.Now()
					for i := range viewers {
						workers.Go(func() {
							request := scaleP12Request(f.data.Workspace.URLKey, user)
							if observed {
								ctx := store.WithSQLStatementObserver(request.Context(), func(query string) {
									statements.Add(1)
									if strings.Contains(query, "workspace_authz_epochs") {
										epochs.Add(1)
									}
								})
								request = request.WithContext(ctx)
							}
							<-start
							sent := time.Now()
							w := httptest.NewRecorder()
							if !f.service.writeProjectedRealtime(w, request, event) || w.Body.Len() == 0 {
								failures.Add(1)
							}
							latencies[i] = time.Since(sent).Nanoseconds()
						})
					}
					close(start)
					workers.Wait()
					elapsed := time.Since(began)
					runtime.ReadMemStats(&after)
					if failures.Load() != 0 && os.Getenv("FLOW_SCALE_P12_BASELINE") != "1" {
						t.Errorf("%d live deliveries failed", failures.Load())
					}
					slices.Sort(latencies)
					return elapsed.Nanoseconds() / int64(viewers), latencies[(viewers-1)*95/100], (after.TotalAlloc - before.TotalAlloc) / uint64(viewers), (after.Mallocs - before.Mallocs) / uint64(viewers), statements.Load(), epochs.Load(), failures.Load()
				}
				for pass := range 3 {
					for _, mode := range []string{"off", "on"} {
						t.Setenv("FLOW_SYNC_V2", mode)
						event := domain.RealtimeEvent{ID: fmt.Sprintf("p12-live-%d-%d-%s", viewers, pass, mode), Type: "issue.updated", AggregateID: issue.ID, Payload: []byte(`{}`)}
						ns, p95, bytes, allocations, _, _, failures := run(event, false)
						event.ID += "-sql"
						_, _, _, _, statements, epochs, observedFailures := run(event, true)
						if os.Getenv("FLOW_SCALE_P12_BASELINE") != "1" && mode == "on" && viewers > 1 && (statements > 32 || epochs > 1) {
							t.Errorf("live authority grew with viewers: SQL=%d epochs=%d", statements, epochs)
						}
						t.Logf("pass=%d mode=%s viewers=%d sql/event=%d epoch_sql/event=%d ns/viewer=%d p95_ns=%d bytes/viewer=%d allocs/viewer=%d failures=%d observed_failures=%d", pass, mode, viewers, statements, epochs, ns, p95, bytes, allocations, failures, observedFailures)
					}
				}
			})
		}
	}
}
