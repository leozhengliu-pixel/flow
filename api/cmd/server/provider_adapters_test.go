package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestProviderWebhooksVerifySignaturesAndDeduplicateImports(t *testing.T) {
	for _, provider := range []string{"sentry", "intercom"} {
		t.Run(provider, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/" || r.URL.Path == "/me" {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "authenticated", "auth": map[string]any{"scopes": []string{"event:read"}}})
					return
				}
				if provider == "sentry" {
					_ = json.NewEncoder(w).Encode(map[string]any{"title": "Webhook report", "permalink": "https://sentry.io/issues/42/"})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"title": "Webhook conversation", "source": map[string]any{"body": "Please help", "author": map[string]any{"email": "requester@customer.example"}}})
				}
			}))
			defer upstream.Close()
			t.Setenv(providerEnv(provider)+"_API_URL", upstream.URL)
			t.Setenv(providerEnv(provider)+"_ACCESS_TOKEN", "fixture-token")
			t.Setenv(providerEnv(provider)+"_WEBHOOK_SECRET", "fixture-signing")
			repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			handler := newHandler(&server{store: repo, authDisabled: true})
			before := repo.Bootstrap()
			requestJSON[domain.IntegrationConnection](t, handler, "POST", "/api/integrations/"+provider+"/configure", map[string]string{"teamId": before.Teams[0].ID}, 200)
			body := `{"action":"created","data":{"issue":{"id":"42"}}}`
			header := "Sentry-Hook-Signature"
			if provider == "intercom" {
				body = `{"topic":"conversation.user.created","data":{"item":{"id":"42"}}}`
				header = "X-Hub-Signature"
			}
			mac := hmac.New(sha256.New, []byte("fixture-signing"))
			if provider == "intercom" {
				mac = hmac.New(sha1.New, []byte("fixture-signing"))
			}
			_, _ = mac.Write([]byte(body))
			signature := hex.EncodeToString(mac.Sum(nil))
			if provider == "intercom" {
				signature = "sha1=" + signature
			}
			deliver := func(signature string) int {
				request := httptest.NewRequest("POST", "/api/integrations/"+provider+"/webhook?workspace=test-workspace", strings.NewReader(body))
				request.Header.Set(header, signature)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != 202 && signature != "invalid" {
					t.Log(response.Body.String())
				}
				return response.Code
			}
			if status := deliver("invalid"); status != 401 {
				t.Fatalf("unsigned webhook accepted: %d", status)
			}
			if status := deliver(signature); status != 202 {
				t.Fatalf("signed webhook failed: %d", status)
			}
			if status := deliver(signature); status != 202 {
				t.Fatalf("retry failed: %d", status)
			}
			after := repo.Bootstrap()
			if len(after.Issues) != len(before.Issues)+1 {
				t.Fatal("webhook retry duplicated issue")
			}
			if provider == "intercom" && len(after.CustomerRequests) != len(before.CustomerRequests)+1 {
				t.Fatal("Intercom did not link the customer request")
			}
		})
	}
}

func TestNativeProviderAdaptersPerformRealRequestsAndImports(t *testing.T) {
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	for _, provider := range []string{"notion", "figma", "sentry", "intercom", "google-calendar", "cursor"} {
		t.Run(provider, func(t *testing.T) {
			repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if provider == "cursor" {
					user, _, ok := r.BasicAuth()
					if !ok || user != "fixture-token" {
						t.Error("Cursor Basic auth missing")
					}
				} else if provider == "figma" {
					if r.Header.Get("X-Figma-Token") != "fixture-token" {
						t.Error("Figma token missing")
					}
				} else if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("Bearer token missing")
				}
				switch {
				case strings.Contains(r.URL.Path, "/blocks/"):
					_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"type": "paragraph", "paragraph": map[string]any{"rich_text": []any{map[string]any{"plain_text": "Page content"}}}}}})
				case strings.Contains(r.URL.Path, "/pages/"):
					_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://www.notion.so/page", "properties": map[string]any{"title": map[string]any{"type": "title", "title": []any{map[string]any{"plain_text": "Notion title"}}}}})
				case strings.Contains(r.URL.Path, "/files/"):
					_ = json.NewEncoder(w).Encode(map[string]any{"name": "Design", "lastModified": "2026-09-10"})
				case strings.Contains(r.URL.Path, "/issues/"):
					_ = json.NewEncoder(w).Encode(map[string]any{"title": "Sentry error", "permalink": "https://sentry.io/issues/42/", "count": "2"})
				case strings.Contains(r.URL.Path, "/conversations/"):
					_ = json.NewEncoder(w).Encode(map[string]any{"title": "Help requested", "source": map[string]any{"body": "Customer request"}})
				case strings.Contains(r.URL.Path, "/calendars/primary/events"):
					if r.URL.Query().Get("eventTypes") != "outOfOffice" {
						t.Error("unbounded personal calendar request")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"eventType": "outOfOffice", "start": map[string]any{"dateTime": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, "end": map[string]any{"dateTime": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}}})
				case r.URL.Path == "/agents" && r.Method == "POST":
					var input map[string]any
					_ = json.NewDecoder(r.Body).Decode(&input)
					if input["prompt"] == nil || input["repos"] == nil {
						t.Error("coding request lacks source or issue prompt")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"agent": map[string]any{"id": "remote-agent"}, "run": map[string]any{"id": "run-1", "status": "CREATING"}})
				case r.URL.Path == "/agents/remote-agent/runs/run-1":
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "run-1", "status": "FINISHED", "git": map[string]any{"branches": []any{map[string]any{"prUrl": "https://github.com/owner/repo/pull/1"}}}})
				case r.URL.Path == "/agents/remote-agent/runs/run-1/cancel":
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "remote-agent"})
				default:
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "me", "auth": map[string]any{"scopes": []string{"event:read"}}, "items": []any{}, "kind": "calendar#calendarList"})
				}
			}))
			defer upstream.Close()
			t.Setenv(providerEnv(provider)+"_API_URL", upstream.URL)
			handler := newHandler(&server{store: repo, authDisabled: true, uploadPath: t.TempDir()})
			connection := requestJSON[domain.IntegrationConnection](t, handler, "POST", "/api/integrations/"+provider+"/configure", map[string]any{"token": "fixture-token"}, 200)
			if connection.Status != "connected" {
				t.Fatal("verified connection not connected")
			}
			before := repo.Bootstrap()
			issue := before.Issues[0]
			action := providerActionInput{Action: "preview", ResourceID: "42", IssueID: issue.ID, TeamID: issue.Team.ID, Repository: "https://github.com/owner/repo"}
			switch provider {
			case "google-calendar":
				action.Action = "syncAvailability"
			case "cursor":
				action.Action = "launch"
			case "intercom", "sentry":
				action.Action = "import"
			case "notion", "figma":
				action.Action = "attach"
			}
			path := "/api/integrations/" + provider + "/actions"
			result := requestJSON[providerResult](t, handler, "POST", path, action, 200)
			if provider == "intercom" || provider == "sentry" {
				replay := requestJSON[providerResult](t, handler, "POST", path, action, 200)
				after := repo.Bootstrap()
				if replay.IssueID != result.IssueID || len(after.Issues) != len(before.Issues)+1 {
					t.Fatal("import is not idempotent")
				}
				if provider == "intercom" {
					found := false
					for _, ask := range after.Asks {
						found = found || ask.IssueID == result.IssueID && ask.Source == "intercom"
					}
					if !found {
						t.Fatal("Intercom request did not create Ask")
					}
				}
			}
			if provider == "google-calendar" && result.AvailableUntil == nil {
				t.Fatal("OOO not synchronized")
			}
			if provider == "cursor" {
				action.Action = "status"
				action.ResourceID = result.ID
				status := requestJSON[providerResult](t, handler, "POST", path, action, 200)
				if status.Status != "FINISHED" || status.URL == "" {
					t.Fatal("coding completion not synchronized")
				}
				action.Action = "stop"
				requestJSON[providerResult](t, handler, "POST", path, action, 200)
			}
			if calls.Load() < 2 {
				t.Fatal("provider operation never reached upstream")
			}
			public := requestJSON[domain.Bootstrap](t, handler, "GET", "/api/bootstrap", nil, 200)
			raw, _ := json.Marshal(public)
			if strings.Contains(string(raw), "fixture-token") {
				t.Fatal("credential exposed through bootstrap")
			}
		})
	}
}

func TestProviderConnectionDoesNotReportSuccessOnUpstreamFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) }))
	defer upstream.Close()
	t.Setenv("FLOW_INTEGRATION_SENTRY_API_URL", upstream.URL)
	t.Setenv("FLOW_INTEGRATION_SENTRY_ACCESS_TOKEN", "bad-token")
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, authDisabled: true})
	requestJSON[map[string]any](t, handler, "POST", "/api/integrations/sentry/configure", map[string]any{}, 502)
	for _, connection := range repo.Bootstrap().IntegrationConnections {
		if connection.Provider == "sentry" {
			t.Fatal("failed credentials persisted as connected")
		}
	}
}

func TestSentryAnonymousHTTP200IsNotAConnectedAccount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"auth":null,"user":null}`))
	}))
	defer upstream.Close()
	t.Setenv("FLOW_INTEGRATION_SENTRY_API_URL", upstream.URL)
	t.Setenv("FLOW_INTEGRATION_SENTRY_ACCESS_TOKEN", "ignored-token")
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, authDisabled: true})
	requestJSON[map[string]any](t, handler, "POST", "/api/integrations/sentry/configure", map[string]any{}, 502)
	for _, connection := range repo.Bootstrap().IntegrationConnections {
		if connection.Provider == "sentry" {
			t.Fatal("anonymous response was marked connected")
		}
	}
}

func TestZapierActionsCreateOneIssueAndWebhook(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, authDisabled: true})
	requestJSON[domain.IntegrationConnection](t, handler, "POST", "/api/integrations/zapier/configure", map[string]any{}, 200)
	before := repo.Bootstrap()
	input := providerActionInput{Action: "createIssue", ResourceID: "zap-123", Title: "Zap issue", TeamID: before.Teams[0].ID}
	first := requestJSON[providerResult](t, handler, "POST", "/api/integrations/zapier/actions", input, 200)
	second := requestJSON[providerResult](t, handler, "POST", "/api/integrations/zapier/actions", input, 200)
	if first.IssueID != second.IssueID || len(repo.Bootstrap().Issues) != len(before.Issues)+1 {
		t.Fatal("Zap retry duplicated issue")
	}
	input = providerActionInput{Action: "subscribe", URL: "https://hooks.zapier.com/hooks/catch/test"}
	subscribed := requestJSON[providerResult](t, handler, "POST", "/api/integrations/zapier/actions", input, 200)
	if subscribed.ID == "" {
		t.Fatal("webhook not created")
	}
	requestJSON[providerResult](t, handler, "POST", "/api/integrations/zapier/actions", providerActionInput{Action: "unsubscribe", ResourceID: subscribed.ID}, 200)
}

func TestCodexAdapterUsesAppServerThreadAndTurn(t *testing.T) {
	var started atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			var msg struct {
				ID     *int           `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if wsjson.Read(r.Context(), conn, &msg) != nil {
				return
			}
			if msg.ID == nil {
				continue
			}
			var result any = map[string]any{}
			switch msg.Method {
			case "account/read":
				result = map[string]any{"requiresOpenaiAuth": true, "account": map[string]any{"type": "apiKey"}}
			case "thread/start":
				if msg.Params["sandbox"] != "workspace-write" || msg.Params["cwd"] != "/workspace/fixture" {
					t.Error("coding environment scope not constrained")
				}
				result = map[string]any{"thread": map[string]any{"id": "thread-1"}}
			case "turn/start":
				started.Store(true)
				result = map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}}
			case "thread/read":
				result = map[string]any{"thread": map[string]any{"turns": []any{map[string]any{"id": "turn-1", "status": "completed"}}}}
			}
			if err := wsjson.Write(context.Background(), conn, map[string]any{"id": *msg.ID, "result": result}); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	t.Setenv("FLOW_CODEX_APP_SERVER_URL", strings.Replace(upstream.URL, "http:", "ws:", 1))
	t.Setenv("FLOW_CODEX_WORKSPACES", `{"test-workspace":"/workspace/fixture"}`)
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, authDisabled: true})
	requestJSON[domain.IntegrationConnection](t, handler, "POST", "/api/integrations/codex/configure", map[string]any{}, 200)
	issue := repo.Bootstrap().Issues[0]
	job := requestJSON[providerResult](t, handler, "POST", "/api/integrations/codex/actions", providerActionInput{Action: "launch", IssueID: issue.ID}, 200)
	if !started.Load() || job.ID == "" {
		t.Fatal("Codex turn was not started")
	}
	status := requestJSON[providerResult](t, handler, "POST", "/api/integrations/codex/actions", providerActionInput{Action: "status", ResourceID: job.ID}, 200)
	if status.Status != "completed" {
		t.Fatalf("unexpected status %s", fmt.Sprint(status))
	}
}
