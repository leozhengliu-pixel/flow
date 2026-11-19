package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW570: the loop run read routes answer the same status and the same bytes
// with bounded metadata on, warm and after a SQL reload, as flag off, with
// authentication disabled (development mode) and enabled: the run list (runs
// present or absent, the viewer's own feedback), one run, its event log, and a
// loop that does not exist.
func TestScaleP21SW570LoopRunReads(t *testing.T) {
	for _, mode := range []string{"auth-disabled", "auth-enabled"} {
		t.Run(mode, func(t *testing.T) {
			authEnabled := mode == "auth-enabled"
			t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { repository.Close() })
			ctx := context.Background()
			workspace := repository.Bootstrap().Workspace.URLKey
			service := func(repo *store.SQLiteStore) http.Handler {
				return newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: !authEnabled})
			}
			legacy := service(repository)
			var cookies []*http.Cookie
			do := func(handler http.Handler, method, path string, input any) (int, string) {
				t.Helper()
				var body *strings.Reader
				if input != nil {
					raw, _ := json.Marshal(input)
					body = strings.NewReader(string(raw))
				} else {
					body = strings.NewReader("")
				}
				request := httptest.NewRequest(method, path, body)
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Workspace-Key", workspace)
				for _, cookie := range cookies {
					request.AddCookie(cookie)
				}
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, request)
				return recorder.Code, sw570RequestID.ReplaceAllString(recorder.Body.String(), `"requestId":"-"`)
			}
			viewerID := ""
			if authEnabled {
				status, raw := do(legacy, http.MethodPost, "/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"})
				if status != http.StatusOK {
					t.Fatalf("SW570 login %d: %s", status, raw)
				}
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"test-password"}`))
				request.Header.Set("Content-Type", "application/json")
				legacy.ServeHTTP(recorder, request)
				cookies = recorder.Result().Cookies()
				var session domain.AuthSession
				_ = json.Unmarshal(recorder.Body.Bytes(), &session)
				viewerID = session.User.ID
			} else {
				viewerID = repository.Bootstrap().Viewer.ID
			}
			if status, raw := do(legacy, http.MethodPatch, "/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{"loops": true}}); status != http.StatusOK {
				t.Fatalf("SW570 enabling loops %d: %s", status, raw)
			}
			create := func(name string) domain.Loop {
				status, raw := do(legacy, http.MethodPost, "/api/loops", map[string]any{"name": name, "instructions": "Do it.", "status": "published"})
				if status != http.StatusCreated {
					t.Fatalf("SW570 create loop %d: %s", status, raw)
				}
				var loop domain.Loop
				if err := json.Unmarshal([]byte(raw), &loop); err != nil {
					t.Fatal(err)
				}
				return loop
			}
			busy, empty := create("SW570 busy"), create("SW570 empty")
			base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
			for index, status := range []string{"completed", "failed", "needs_review", "cancelled"} {
				finished := base.Add(time.Duration(index)*time.Hour + time.Minute)
				run := domain.LoopRun{ID: "sw570-run-" + string(rune('a'+index)), LoopID: busy.ID, Status: status, Trigger: []string{"manual", "schedule", "event", "manual"}[index], StartedAt: base.Add(time.Duration(index) * time.Hour), FinishedAt: &finished, Output: "out " + status}
				if index == 0 {
					run.Feedback = []domain.LoopRunFeedback{{UserID: viewerID, Rating: "up", Comment: "good", At: base}, {UserID: "someone-else", Rating: "down", At: base}}
				}
				if index == 2 {
					run.EntityType, run.EntityIdentifier = "issue", "SW-1"
				}
				if err := repository.CreateLoopRun(ctx, workspace, run, store.LoopRunLease{}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := repository.UpdateLoopRun(ctx, workspace, "sw570-run-a", []store.LoopRunEvent{{Kind: "note", Data: json.RawMessage(`{"text":"one"}`), CreatedAt: base}, {Kind: "note", Data: json.RawMessage(`{"text":"two"}`), CreatedAt: base}}, func(*domain.LoopRun) error { return nil }); err != nil {
				t.Fatal(err)
			}
			probe, err := repository.BoundedMetadataViewForTesting(ctx)
			if err != nil {
				t.Fatal(err)
			}
			warm := service(probe)
			if err := repository.ReloadWorkspace(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			reloadedProbe, err := repository.BoundedMetadataViewForTesting(ctx)
			if err != nil {
				t.Fatal(err)
			}
			reloaded := service(reloadedProbe)
			paths := []string{
				"/api/loops/" + busy.ID + "/runs",
				"/api/loops/" + empty.ID + "/runs",
				"/api/loops/sw570-missing/runs",
				"/api/loops/" + busy.ID + "/runs/sw570-run-a",
				"/api/loops/" + busy.ID + "/runs/sw570-run-b",
				"/api/loops/" + empty.ID + "/runs/sw570-run-a",
				"/api/loops/" + busy.ID + "/runs/sw570-missing",
				"/api/loops/sw570-missing/runs/sw570-run-a",
				"/api/loops/" + busy.ID + "/runs/sw570-run-a/events",
				"/api/loops/" + busy.ID + "/runs/sw570-run-a/events?after=1",
				"/api/loops/" + busy.ID + "/runs/sw570-run-b/events",
				"/api/loops/" + empty.ID + "/runs/sw570-run-a/events",
				"/api/loops/" + busy.ID + "/runs/sw570-missing/events",
			}
			for _, path := range paths {
				wantStatus, wantBody := do(legacy, http.MethodGet, path, nil)
				for name, handler := range map[string]http.Handler{"bounded warm": warm, "bounded reloaded": reloaded} {
					status, body := do(handler, http.MethodGet, path, nil)
					if status != wantStatus || body != wantBody {
						t.Errorf("SW570 %s GET %s: %s answered %d %s, legacy %d %s", mode, path, name, status, body, wantStatus, wantBody)
					}
				}
			}
			if status, body := do(legacy, http.MethodGet, paths[0], nil); status != http.StatusOK || !strings.Contains(body, `"viewerRating":"up"`) || !strings.Contains(body, "out failed") {
				t.Fatalf("SW570 %s legacy run list is not what the differential needs: %d %s", mode, status, body)
			}
			if status, _ := do(legacy, http.MethodGet, paths[2], nil); status != http.StatusNotFound {
				t.Fatalf("SW570 %s missing loop status %d", mode, status)
			}
		})
	}
}

var sw570RequestID = regexp.MustCompile(`"requestId":"[0-9a-f]*"`)
