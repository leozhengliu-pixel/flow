package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func loadErrorsEventCount(t *testing.T, repo *store.SQLiteStore, eventType string) int {
	t.Helper()
	events, err := repo.Events(t.Context(), "")
	if err != nil {
		t.Fatal("could not read events")
	}
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func holdLoadErrorsWork(t *testing.T, repo *store.SQLiteStore, free int) func() {
	t.Helper()
	room := repo.MetadataWorkLimitForTesting() - repo.MetadataWorkUsedForTesting()
	release, err := repo.HoldMetadataWorkForTesting(room - free)
	if err != nil {
		t.Fatal("could not hold metadata work")
	}
	t.Cleanup(release)
	return release
}

func TestScaleP21ActivationViewCreateAuthorizationBudget(t *testing.T) {
	peers, f, _, project := scaleP21ActivationHTTPPeers(t, "current")
	peer := peers[0]
	s := peer.service
	repo, workspace := s.store, f.data.Workspace.URLKey
	events := func() int { return loadErrorsEventCount(t, repo, "view.created") }
	post := func(t *testing.T, resourceStage http.Handler, teamID, projectID string) (int, string) {
		t.Helper()
		input := map[string]string{"name": t.Name(), "resource": "issues", "scope": "workspace"}
		if teamID != "" {
			input["teamId"] = teamID
		}
		if projectID != "" {
			input["projectId"] = projectID
		}
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, peer.URL+"/api/views", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Workspace-Key", workspace)
		if resourceStage != nil {
			w := httptest.NewRecorder()
			resourceStage.ServeHTTP(w, req)
			return w.Code, w.Header().Get("Retry-After")
		}
		res, err := f.admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode, res.Header.Get("Retry-After")
	}

	// Start after session authentication for the workspace-scoped case. Its
	// two reads need the same allowance, so a hold placed before the request
	// would stop at authentication policy before reaching resource authorization.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/views", s.createSavedView)
	resourceStage := resourceAuthorizationStage(s, mux, workspace, f.data.Viewer)

	restore := store.SetMetadataWorkRetryWaitForTesting(50 * time.Millisecond)
	defer restore()

	for _, tc := range []struct {
		name, projectID string
		resourceStage   http.Handler
		free            int
		stale           bool
	}{
		{name: "authentication-policy", free: 1 << 20},
		{name: "workspace-authorization", resourceStage: resourceStage, free: 1 << 20},
		{name: "stale-workspace-policy", resourceStage: resourceStage, free: 3 << 20, stale: true},
		{name: "project-scoped-view", projectID: project, resourceStage: resourceStage, free: 2 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := events()
			if tc.stale {
				if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
			}
			release := holdLoadErrorsWork(t, repo, tc.free)
			status, retry := post(t, tc.resourceStage, "", tc.projectID)
			release()
			if got := events(); status != http.StatusServiceUnavailable || retry == "" || got != before {
				t.Fatalf("held work returned %d, Retry-After=%t and %d new events", status, retry != "", got-before)
			}
			if status, _ := post(t, tc.resourceStage, "", tc.projectID); status != http.StatusCreated || events() != before+1 {
				t.Fatalf("released work returned %d, want 201 and one event", status)
			}
		})
	}
	before := events()
	if status, retry := post(t, nil, "unavailable-team", ""); status != http.StatusForbidden || retry != "" || events() != before {
		t.Fatalf("view create with an unavailable team returned %d, want 403 and no event", status)
	}
}

func TestScaleP21ActivationMetadataMutationBudget(t *testing.T) {
	peers, f, _, project := scaleP21ActivationHTTPPeers(t, "current")
	s := peers[0].service
	repo, workspace := s.store, f.data.Workspace.URLKey
	request := func(method, path, id string, input any, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("X-Workspace-Key", workspace)
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", id)
		ctx, closeOutput := beginMetadataOutputReads(store.ContextWithActor(req.Context(), f.data.Viewer))
		defer closeOutput()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req.WithContext(ctx))
		return w
	}
	label := request(http.MethodPost, "/api/labels", "", map[string]string{"name": "Delete budget test"}, s.createWorkspaceLabel)
	var seededLabel domain.IssueLabel
	if label.Code != http.StatusCreated || json.Unmarshal(label.Body.Bytes(), &seededLabel) != nil || seededLabel.ID == "" {
		t.Fatalf("seed label returned %d", label.Code)
	}
	restore := store.SetMetadataWorkRetryWaitForTesting(50 * time.Millisecond)
	defer restore()
	for _, tc := range []struct {
		name, method, path, id, event string
		input                         any
		handler                       http.HandlerFunc
		want                          int
	}{
		{"label-create", http.MethodPost, "/api/labels", "", "label.created", map[string]string{"name": "New label"}, s.createWorkspaceLabel, http.StatusCreated},
		{"label-delete", http.MethodDelete, "/api/labels/" + seededLabel.ID, seededLabel.ID, "label.deleted", nil, s.deleteWorkspaceLabel, http.StatusNoContent},
		{"project-edit", http.MethodPatch, "/api/projects/" + project, project, "project.updated", map[string]string{"summary": "Edited project"}, s.updateProject, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := func() int { return loadErrorsEventCount(t, repo, tc.event) }
			before := events()
			run := func() *httptest.ResponseRecorder {
				return request(tc.method, tc.path, tc.id, tc.input, tc.handler)
			}
			release := holdLoadErrorsWork(t, repo, 32<<10)
			response := run()
			release()
			if got := events(); response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") == "" || got != before {
				t.Fatalf("held work returned %d, Retry-After=%t and %d new events", response.Code, response.Header().Get("Retry-After") != "", got-before)
			}
			if response := run(); response.Code != tc.want || events() != before+1 {
				t.Fatalf("released work returned %d, want %d and one event", response.Code, tc.want)
			}
		})
	}
}

func TestScaleP21ActivationMutationScratchBudget(t *testing.T) {
	peers, f, issue, project := scaleP21ActivationHTTPPeers(t, "current")
	peer := peers[0]
	repo, workspace := peer.service.store, f.data.Workspace.URLKey
	// The configured process budget exceeds the old 1 GiB spool guard.
	t.Setenv("FLOW_METADATA_SOURCE_DISK_BYTES", "1610612736")
	for _, tc := range []struct {
		name, method, path, event string
		input                     string
		want                      int
	}{
		{"api-key", http.MethodPost, "/api/api-keys", "api_key.created", `{"name":"Scratch budget key","scopes":["read"]}`, http.StatusCreated},
		{"label", http.MethodPost, "/api/labels", "label.created", `{"name":"Scratch budget label"}`, http.StatusCreated},
		{"project", http.MethodPatch, "/api/projects/" + project, "project.updated", `{"summary":"Scratch budget edit"}`, http.StatusOK},
		{"comment", http.MethodPost, "/api/issue-records/" + issue.ID + "/comments", "comment.created", `{"body":"Scratch budget comment"}`, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			post := func(free int64) (int, string) {
				t.Helper()
				release, err := store.HoldMetadataLogicalDiskForTesting(free)
				if err != nil {
					t.Fatal("could not hold metadata disk quota")
				}
				defer release()
				req, err := http.NewRequestWithContext(t.Context(), tc.method, peer.URL+tc.path, bytes.NewBufferString(tc.input))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("X-Workspace-Key", workspace)
				req.Header.Set("Content-Type", "application/json")
				res, err := f.admin.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				_, _ = io.Copy(io.Discard, res.Body)
				return res.StatusCode, res.Header.Get("Retry-After")
			}
			events := func() int { return loadErrorsEventCount(t, repo, tc.event) }
			before := events()
			if status, retry := post(0); status != http.StatusServiceUnavailable || retry == "" || events() != before {
				t.Fatalf("full disk quota returned %d, Retry-After=%t; want 503 and no event", status, retry != "")
			}
			if status, _ := post(64 << 20); status != tc.want || events() != before+1 {
				t.Fatalf("available disk quota returned %d; want %d and one event", status, tc.want)
			}
		})
	}
}
