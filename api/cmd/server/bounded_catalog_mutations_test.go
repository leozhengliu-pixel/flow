package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21ActivationConcurrentCatalogMutations(t *testing.T) {
	peers, fixture, issue, project := scaleP21ActivationHTTPPeers(t, "full")
	workspace := fixture.data.Workspace.URLKey
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	status, doc, err := activationRequest[domain.Document](ctx, fixture.admin, http.MethodPost, peers[0].URL+"/api/documents",
		workspace, map[string]string{"title": "Concurrent catalog document"})
	if err != nil || status != http.StatusCreated || doc.ID == "" {
		t.Fatalf("document fixture status %d", status)
	}

	cases := []struct {
		method, route, body, event string
		want, replica              int
	}{
		{http.MethodDelete, "/api/documents/" + doc.ID, "", "document.deleted", http.StatusNoContent, 0},
		{http.MethodDelete, "/api/issue-records/" + issue.ID, "", "issue.deleted", http.StatusNoContent, 1},
		{http.MethodPatch, "/api/projects/" + project, `{"name":"Catalog project"}`, "project.updated", http.StatusOK, 0},
		{http.MethodPost, "/api/views", `{"name":"Catalog view","resource":"issues","scope":"workspace"}`, "view.created", http.StatusCreated, 1},
	}
	before := make([]int, len(cases))
	for i, tc := range cases {
		before[i] = loadErrorsEventCount(t, peers[0].service.store, tc.event)
	}
	start := make(chan struct{})
	var clients sync.WaitGroup
	request := func(index int, allowRetry bool) bool {
		tc := cases[index]
		req, err := http.NewRequestWithContext(ctx, tc.method, peers[tc.replica].URL+tc.route, strings.NewReader(tc.body))
		if err != nil {
			t.Error("could not construct catalog mutation")
			return false
		}
		req.Header.Set("X-Workspace-Key", workspace)
		req.Header.Set("Content-Type", "application/json")
		res, err := fixture.admin.Do(req)
		if err != nil {
			t.Errorf("%s failed", tc.event)
			return false
		}
		defer res.Body.Close()
		if allowRetry && res.StatusCode == http.StatusServiceUnavailable && res.Header.Get("Retry-After") != "" {
			_, _ = io.Copy(io.Discard, res.Body)
			return true
		}
		if res.StatusCode != tc.want {
			var failure struct{ Error string }
			_ = json.NewDecoder(res.Body).Decode(&failure)
			stage := "unclassified"
			switch failure.Error {
			case "You don't have access to this workspace":
				stage = "issue policy"
			case "This resource is outside your teams":
				stage = "resource policy"
			}
			t.Errorf("%s status %d, want %d (%s)", tc.event, res.StatusCode, tc.want, stage)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		return false
	}
	retry := make([]bool, len(cases))
	for i := range cases {
		clients.Go(func() {
			<-start
			retry[i] = request(i, true)
		})
	}
	close(start)
	clients.Wait()
	for i, refused := range retry {
		if refused {
			request(i, false)
		}
	}
	for _, peer := range peers {
		if err := peer.service.store.ReloadWorkspace(ctx, workspace); err != nil {
			t.Fatal("could not reload the catalog after concurrent writes")
		}
	}
	for i, tc := range cases {
		if events := loadErrorsEventCount(t, peers[0].service.store, tc.event); events != before[i]+1 {
			t.Errorf("%s produced %d events, want one", tc.event, events-before[i])
		}
	}
}

func TestScaleP21ActivationIssueDeleteStaleAuthorization(t *testing.T) {
	peers, fixture, issue, project := scaleP21ActivationHTTPPeers(t, "full")
	workspace := fixture.data.Workspace.URLKey
	before := loadErrorsEventCount(t, peers[0].service.store, "issue.deleted")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	checked, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var readPolicy bool
	var epochChecks atomic.Int32
	readCtx := store.WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, " FROM workspace_states CROSS JOIN record_journal_mode") {
			readPolicy = true
		}
		if readPolicy && strings.HasPrefix(query, "SELECT COALESCE((SELECT epoch FROM workspace_authz_epochs") {
			epochChecks.Add(1)
			once.Do(func() {
				close(checked)
				select {
				case <-resume:
				case <-ctx.Done():
				}
			})
		}
	})
	deleteURL := peers[1].URL + "/api/issue-records/" + issue.ID
	req, err := http.NewRequestWithContext(readCtx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		t.Fatal("could not construct issue deletion")
	}
	req.Header.Set("X-Workspace-Key", workspace)
	for _, cookie := range fixture.admin.Jar.Cookies(req.URL) {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		peers[1].Config.Handler.ServeHTTP(w, req)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-checked:
	case <-done:
		t.Fatalf("issue deletion returned %d before checking its policy epoch", w.Code)
	case <-ctx.Done():
		t.Fatal("issue deletion did not reach the live epoch check")
	}
	// Updating the project advances the policy epoch while deletion is
	// between its policy snapshot and live check.
	status, _, err := activationRequest[domain.Project](ctx, fixture.admin, http.MethodPatch, peers[0].URL+"/api/projects/"+project,
		workspace, map[string]string{"name": "Updated before epoch check"})
	if err != nil || status != http.StatusOK {
		t.Fatalf("project update status %d", status)
	}
	close(resume)
	<-done
	if scaleP03DatabaseConfig(t).Driver == "sqlite" {
		// A SQLite cookie session rebuilds its authorization when the policy
		// epoch moves, so the deletion commits only after a fresh live check.
		if w.Code != http.StatusNoContent || loadErrorsEventCount(t, peers[0].service.store, "issue.deleted") != before+1 {
			t.Fatalf("re-authorized issue deletion returned %d, want 204 with one event", w.Code)
		}
		if epochChecks.Load() < 2 {
			t.Fatal("issue deletion committed without re-checking the policy epoch")
		}
		return
	}
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("stale issue authorization returned %d, want 503 with Retry-After", w.Code)
	}
	if loadErrorsEventCount(t, peers[0].service.store, "issue.deleted") != before {
		t.Fatal("issue deletion committed after its authorization became stale")
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		t.Fatal("could not construct retry")
	}
	req.Header.Set("X-Workspace-Key", workspace)
	res, err := fixture.admin.Do(req)
	if err != nil {
		t.Fatal("issue deletion retry failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent || loadErrorsEventCount(t, peers[0].service.store, "issue.deleted") != before+1 {
		t.Fatalf("retried issue deletion returned %d, want 204 with one event", res.StatusCode)
	}
}
