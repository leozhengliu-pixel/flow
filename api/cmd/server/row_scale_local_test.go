package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestLocalRowScale(t *testing.T) {
	dsn := os.Getenv("FLOW_ROW_SCALE_DSN")
	if dsn == "" {
		t.Skip("isolated benchmark database required")
	}
	ctx := context.Background()
	repo, err := store.OpenDatabase(store.DatabaseConfig{Driver: "mysql", URL: dsn, MaxOpenConns: 8, MaxIdleConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	const key = "row-scale"
	data, exists := repo.WorkspaceMetadata(key)
	if exists && os.Getenv("FLOW_ROW_SCALE_REUSE") != "1" {
		t.Fatal("use a fresh benchmark database")
	}
	if !exists {
		data, err = repo.CreateWorkspace(ctx, "Row scale", key, "us")
		if err != nil {
			t.Fatal(err)
		}
	}
	s := &server{store: repo, authDisabled: true}
	handler := newHandler(s)
	defer s.stopDeliveryScheduler(context.Background())
	host := httptest.NewUnstartedServer(handler)
	listener, err := net.Listen("tcp", "127.0.0.1:8086")
	if err != nil {
		t.Fatal(err)
	}
	host.Listener = listener
	host.Start()
	defer host.Close()
	emit := func(value any) { raw, _ := json.Marshal(value); fmt.Println("ROW_SCALE " + string(raw)) }
	client := &http.Client{Timeout: 15 * time.Second}
	date := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	previous := 0
	counts := []int{1000, 10000, 100000, 1000000}
	if exists {
		previous = 1000000
		counts = []int{1000000}
	}
	for _, count := range counts {
		start := time.Now()
		for first := previous; first < count; first += 1000 {
			issues := make([]domain.Issue, min(1000, count-first))
			for j := range issues {
				n := first + j + 1
				issues[j] = domain.Issue{ID: fmt.Sprintf("row-%09d", n), Identifier: fmt.Sprintf("ROW-%d", n), Number: n, Version: 1, Title: fmt.Sprintf("Row scale issue %d", n), Description: strings.Repeat("x", 256), Priority: n % 5, SortOrder: float64(n), Team: data.Teams[0], State: data.States[n%len(data.States)], Creator: data.Viewer, Assignee: &data.Viewer, CreatedAt: date, UpdatedAt: date, Labels: []domain.IssueLabel{}, SubscriberIDs: []string{}, SubIssueIDs: []string{}, Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}}
			}
			if err := repo.ImportIssues(ctx, key, issues); err != nil {
				t.Fatal(err)
			}
			if (first+1000)%100000 == 0 {
				emit(map[string]any{"phase": "import-progress", "rows": first + 1000})
			}
		}
		emit(map[string]any{"phase": "import", "count": count, "added": count - previous, "ms": time.Since(start).Milliseconds()})
		previous = count
		for _, job := range []struct{ name, method, path, body string }{{"metadata", "GET", "/api/issue-records/bootstrap?workspace=" + key, ""}, {"page", "GET", "/api/issue-records?workspace=" + key + "&limit=100", ""}, {"groups", "GET", "/api/issue-records/groups?workspace=" + key + "&groupBy=status", ""}, {"write", "PATCH", fmt.Sprintf("/api/issue-records/row-%09d?workspace=%s", count, key), `{"title":"Updated scale issue"}`}} {
			for _, concurrency := range []int{1, 4} {
				debug.FreeOSMemory()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)
				start := time.Now()
				var wg sync.WaitGroup
				var mu sync.Mutex
				var times []float64
				codes := map[int]int{}
				var bytes int64
				for worker := 0; worker < concurrency; worker++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for rep := 0; rep < 10; rep++ {
							begin := time.Now()
							r, _ := http.NewRequest(job.method, host.URL+job.path, strings.NewReader(job.body))
							r.Header.Set("Content-Type", "application/json")
							response, err := client.Do(r)
							code := 0
							var n int64
							if err == nil {
								code = response.StatusCode
								n, _ = io.Copy(io.Discard, response.Body)
								response.Body.Close()
							}
							ms := float64(time.Since(begin).Microseconds()) / 1000
							mu.Lock()
							times = append(times, ms)
							codes[code]++
							bytes += n
							mu.Unlock()
						}
					}()
				}
				wg.Wait()
				elapsed := time.Since(start)
				var after runtime.MemStats
				runtime.ReadMemStats(&after)
				sort.Float64s(times)
				emit(map[string]any{"phase": "http", "count": count, "operation": job.name, "concurrency": concurrency, "samples": len(times), "p50_ms": times[len(times)/2], "p95_ms": times[int(float64(len(times)-1)*.95)], "max_ms": times[len(times)-1], "rps": float64(len(times)) / elapsed.Seconds(), "statuses": codes, "allocated_per_request": (after.TotalAlloc - before.TotalAlloc) / uint64(len(times)), "response_bytes": bytes / int64(len(times)), "heap_bytes": after.HeapAlloc})
			}
		}
	}
	emit(map[string]any{"phase": "ready", "url": host.URL, "count": previous})
	if os.Getenv("FLOW_ROW_SCALE_SERVE") == "1" {
		time.Sleep(30 * time.Minute)
	}
}
