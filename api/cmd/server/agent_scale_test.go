package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// TestAgentStreamAtScale times how quickly the agent stream reports
// session.started (and the session endpoints respond) in a workspace with
// 100,000 issues. The model is a local stub, so the timings are Flow's own
// overhead. Opt-in: FLOW_SCALE_TEST=1 go test ./cmd/server -run TestAgentStreamAtScale -v
func TestAgentStreamAtScale(t *testing.T) {
	if os.Getenv("FLOW_SCALE_TEST") != "1" {
		t.Skip("set FLOW_SCALE_TEST=1 to run the 100k-issue agent timing test")
	}
	count := 100000
	if value, err := strconv.Atoi(os.Getenv("FLOW_SCALE_ISSUES")); err == nil && value > 0 {
		count = value
	}
	for _, auth := range []bool{false, true} {
		name := "authDisabled"
		if auth {
			name = "authEnabled"
		}
		t.Run(name, func(t *testing.T) { runAgentStreamScale(t, count, auth) })
	}
}

func runAgentStreamScale(t *testing.T, count int, auth bool) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hi.\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
	}))
	defer provider.Close()
	path := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	seedScaleIssues(t, path, count)
	repository, err = store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if auth {
		t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	}
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: !auth, agentAutoTitle: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256}, agentClient: provider.Client()}))
	defer api.Close()
	client := authClient(t)
	if auth {
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	}

	do := func(method, url, body string) *http.Response {
		t.Helper()
		request, _ := http.NewRequest(method, api.URL+url, strings.NewReader(body))
		request.Header.Set("X-Workspace-Key", "test-workspace")
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	stream := func(label, url, body string) domain.AgentSession {
		t.Helper()
		begin := time.Now()
		response := do(http.MethodPost, url, body)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(response.Body)
			t.Fatalf("%s: status %d: %.300s", label, response.StatusCode, raw)
		}
		headers := time.Since(begin)
		var started, completed time.Duration
		var session domain.AgentSession
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
		event := ""
		for scanner.Scan() {
			line := scanner.Text()
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				event = name
				if event == "session.started" && started == 0 {
					started = time.Since(begin)
				}
				if event == "session.completed" {
					completed = time.Since(begin)
				}
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "session.completed" {
				var payload struct {
					Session domain.AgentSession `json:"session"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err != nil {
					t.Fatal(err)
				}
				session = payload.Session
			}
		}
		if started == 0 || completed == 0 {
			t.Fatalf("%s: stream missing session.started/session.completed", label)
		}
		t.Logf("%-34s headers %9s  session.started %9s  completed %9s", label, headers.Round(time.Microsecond), started.Round(time.Microsecond), completed.Round(time.Microsecond))
		return session
	}
	timed := func(label, url string) {
		t.Helper()
		begin := time.Now()
		response := do(http.MethodGet, url, "")
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", label, response.StatusCode)
		}
		t.Logf("%-34s %9s %11d bytes", label, time.Since(begin).Round(time.Microsecond), len(raw))
	}

	begin := time.Now()
	session := stream("POST /api/agent/sessions/stream", "/api/agent/sessions/stream", `{"message":"please help","location":"page"}`)
	// The generated title is saved in the background after the reply.
	for {
		response := do(http.MethodGet, "/api/agent/sessions/"+session.ID, "")
		var current domain.AgentSession
		_ = json.NewDecoder(response.Body).Decode(&current)
		response.Body.Close()
		if current.Title == "Hi" {
			t.Logf("%-34s %9s after send", "generated title saved", time.Since(begin).Round(time.Microsecond))
			break
		}
		if time.Since(begin) > 30*time.Second {
			t.Fatalf("title was not generated: %q", current.Title)
		}
		time.Sleep(5 * time.Millisecond)
	}
	stream("POST sessions/{id}/messages/stream", "/api/agent/sessions/"+session.ID+"/messages/stream", `{"message":"again"}`)
	timed("GET /api/agent/sessions", "/api/agent/sessions")
	timed("GET /api/agent/sessions/{id}", "/api/agent/sessions/"+session.ID)
	timed("GET /api/bootstrap", "/api/bootstrap")
}
