package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestLegacyWritesAndRealtimeDoNotReadUnrelatedIssues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bounded.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", 200)
	issue := authRequest[domain.Issue](t, client, "POST", host.URL+"/api/issues", map[string]any{"title": "Bounded legacy issue"}, "test-workspace", 201)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A full collection scan fails on these unrelated payloads. Indexed reads
	// of the target and scalar permission checks remain valid.
	if _, err := db.Exec(`UPDATE issue_records SET data=? WHERE id<>?`, []byte("invalid-unrelated-json"), issue.ID); err != nil {
		t.Fatal(err)
	}
	authRequest[domain.Issue](t, client, "POST", host.URL+"/api/issues", map[string]any{"title": "Created without reading unrelated issues"}, "test-workspace", 201)
	updated := authRequest[domain.Issue](t, client, "PATCH", host.URL+"/api/issues/"+issue.ID, map[string]any{"title": "Updated without copying workspace"}, "test-workspace", 200)
	if updated.Title != "Updated without copying workspace" {
		t.Fatal("write was not persisted")
	}
	comment := authRequest[domain.Comment](t, client, "POST", host.URL+"/api/issues/"+issue.ID+"/comments", map[string]string{"body": "Bounded comment"}, "test-workspace", 201)
	if comment.ID == "" {
		t.Fatal("comment missing")
	}
	authRequest[[]domain.Presence](t, client, "POST", host.URL+"/api/realtime/presence", map[string]string{"clientId": "legacy-client", "issueId": issue.ID}, "test-workspace", 200)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", host.URL+"/api/realtime/events?workspace=test-workspace", nil)
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("legacy SSE status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"type":"connected"`) {
			break
		}
	}
	s.realtime.publish("test-workspace", domain.RealtimeEvent{ID: "bounded-event", Type: "issue.updated", AggregateID: issue.ID, CreatedAt: time.Now().UTC()})
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(line, `"type":"issue.updated"`) {
			if !strings.Contains(line, updated.Title) {
				t.Fatal("SSE did not include current authorized entity")
			}
			break
		}
	}
	response.Body.Close()
	authRequest[any](t, client, "DELETE", host.URL+"/api/issues/"+issue.ID, nil, "test-workspace", 204)
}

// Explicit opt-in keeps the normal test suite and developer disks small.
// Run with GOMEMLIMIT=650MiB FLOW_TEST_ISSUE_MEMORY=1 and -count=1.
func TestLegacyIssueMemoryAt75675Rows(t *testing.T) {
	if os.Getenv("FLOW_TEST_ISSUE_MEMORY") != "1" {
		t.Skip("opt-in memory regression")
	}
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	base := data.Issues[0]
	base.Description = strings.Repeat("x", 2048)
	base.Labels, base.SubscriberIDs, base.SubIssueIDs, base.Relations = nil, nil, nil, nil
	base.Project, base.CycleID, base.ParentID = nil, nil, nil
	s := &server{store: repo, authDisabled: true, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(s))
	defer host.Close()
	client := &http.Client{Timeout: 60 * time.Second}
	previous := 0
	for _, total := range []int{1000, 75675} {
		for start := previous; start < total; start += 500 {
			items := make([]domain.Issue, min(500, total-start))
			for i := range items {
				items[i] = base
				items[i].ID, items[i].Identifier = fmt.Sprintf("memory-%09d", start+i), fmt.Sprintf("MEM-%d", start+i)
				items[i].Number, items[i].SortOrder = start+i+1000, float64(start+i)
			}
			if err := repo.ImportIssues(context.Background(), data.Workspace.URLKey, items); err != nil {
				t.Fatal(err)
			}
		}
		previous = total
		debug.FreeOSMemory()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		ctx, cancel := context.WithCancel(context.Background())
		var streams []*http.Response
		for i := 0; i < 4; i++ {
			r, _ := http.NewRequestWithContext(ctx, "GET", host.URL+"/api/realtime/events?workspace=test-workspace", nil)
			response, err := client.Do(r)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			if response.StatusCode != 200 {
				response.Body.Close()
				cancel()
				t.Fatalf("SSE status=%d", response.StatusCode)
			}
			streams = append(streams, response)
		}
		debug.FreeOSMemory()
		var held runtime.MemStats
		runtime.ReadMemStats(&held)
		start := time.Now()
		for i := 0; i < 20; i++ {
			for _, job := range []struct{ method, path, body string }{
				{"PATCH", "/api/issues/memory-000000000", `{"title":"Bounded memory update"}`},
				{"POST", "/api/realtime/presence", `{"clientId":"memory-client","issueId":"memory-000000000"}`},
			} {
				r, _ := http.NewRequest(job.method, host.URL+job.path+"?workspace=test-workspace", strings.NewReader(job.body))
				r.Header.Set("Content-Type", "application/json")
				response, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("%s status=%d", job.path, response.StatusCode)
				}
			}
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		t.Logf("rows=%d SSE_connections=4 heap_before=%d heap_with_SSE=%d allocated_40_requests=%d elapsed=%s", total, before.HeapAlloc, held.HeapAlloc, after.TotalAlloc-held.TotalAlloc, time.Since(start))
		for _, response := range streams {
			response.Body.Close()
		}
		cancel()
		if held.HeapAlloc > before.HeapAlloc+32<<20 {
			t.Fatal("SSE retained a workspace-sized collection")
		}
		if total == 75675 {
			start := time.Now()
			response, err := client.Get(host.URL + "/api/bootstrap?workspace=test-workspace")
			if err != nil {
				t.Fatal(err)
			}
			bytes, err := io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("bootstrap status=%d error=%v", response.StatusCode, err)
			}
			t.Logf("legacy_bootstrap_bytes=%d elapsed=%s", bytes, time.Since(start))
			if peak, err := os.ReadFile("/sys/fs/cgroup/memory.peak"); err == nil {
				t.Logf("container_memory_peak_bytes=%s", strings.TrimSpace(string(peak)))
			}
		}
	}
}
