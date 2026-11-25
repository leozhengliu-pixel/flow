package main

import (
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"net/http"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A comment posted after the personal setting is enabled through the API
// starts the same comment-action generation flag off and flag on, with the
// bounded source warm or reloaded from SQL.
func TestScaleP21CommentActionSettingStartDifferential(t *testing.T) {
	for _, mode := range []string{"off", "on-warm", "on-reloaded"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			path := filepath.Join(t.TempDir(), "flow.db")
			if mode != "off" {
				// Seed the fixture flag off, then start the bounded store on it.
				seed, err := store.OpenSQLiteTestFixture(path)
				if err != nil {
					t.Fatal(err)
				}
				if report, err := seed.PrepareBoundedMetadata(t.Context()); err != nil || !report.Ready {
					t.Fatalf("prepare: %v %+v", err, report)
				}
				if err := seed.Close(); err != nil {
					t.Fatal(err)
				}
				t.Setenv("FLOW_BOUNDED_METADATA", "on")
			}
			calls := &atomic.Int32{}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(chatToolCallSSE("proposal", "propose_comment_action", map[string]any{"action": "priority", "arguments": map[string]any{"priority": 1}})))
			}))
			t.Cleanup(provider.Close)
			repo, err := store.OpenSQLiteTestFixture(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { repo.Close() })
			if (mode != "off") != repo.BoundedMetadataEnabled() {
				t.Fatalf("bounded metadata = %v in mode %s", repo.BoundedMetadataEnabled(), mode)
			}
			s := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true, BaseURL: provider.URL, Model: "scripted", MaxOutputTokens: 256}, agentClient: provider.Client()}
			h := newHandler(s)
			data := repo.Bootstrap()
			issue := requestJSON[domain.Issue](t, h, "POST", "/api/issues", domain.IssueCreateInput{Title: "Comment actions", TeamID: data.Teams[0].ID}, 201)
			issue = ageIssueForActivityTest(t, repo, data.Workspace.URLKey, issue)
			enabled := true
			requestJSON[domain.UserSettings](t, h, "PATCH", "/api/account/settings", map[string]bool{"agentEnabled": enabled, "suggestedActionsOnComments": enabled}, 200)
			if mode == "on-reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), data.Workspace.URLKey); err != nil {
					t.Fatal(err)
				}
			}
			requestJSON[domain.Comment](t, h, "POST", "/api/issues/"+issue.ID+"/comments", domain.CommentCreateInput{Body: "Please make this urgent."}, 201)
			deadline := time.Now().Add(5 * time.Second)
			for calls.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if calls.Load() != 1 {
				t.Fatalf("%s: provider calls = %d, want 1", mode, calls.Load())
			}
			for {
				raw, _ := repo.WorkspaceMetadata(data.Workspace.URLKey)
				if len(raw.CommentActions) == 1 && raw.CommentActions[0].Action == "priority" {
					break
				}
				if time.Now().After(deadline.Add(5 * time.Second)) {
					t.Fatalf("%s: suggestions = %v", mode, raw.CommentActions)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
