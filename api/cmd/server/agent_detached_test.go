package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A reply keeps running when the page that asked goes away (navigating,
// reloading, closing the tab) and is saved when it finishes; the stop
// endpoint ends it early and keeps what it produced.
func TestAgentReplySurvivesDisconnectAndStops(t *testing.T) {
	release := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello \"}\n\n"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n" +
				"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		case <-r.Context().Done():
		}
	}))
	defer provider.Close()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, agent: appconfig.AgentConfig{Enabled: true, Protocol: "openai-responses", BaseURL: provider.URL, Model: "flow-test", MaxOutputTokens: 256}, agentClient: provider.Client()}))
	defer api.Close()

	// Starts a new chat, waits for session.started, then drops the connection.
	startAndLeave := func(message string) string {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPost, api.URL+"/api/agent/sessions/stream", strings.NewReader(`{"message":"`+message+`","location":"page"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Workspace-Key", "test-workspace")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 0, 1<<20), 16<<20)
		event, id := "", ""
		for id == "" && scanner.Scan() {
			line := scanner.Text()
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				event = name
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok && event == "session.started" {
				var payload struct {
					Session domain.AgentSession `json:"session"`
				}
				if err := json.Unmarshal([]byte(data), &payload); err != nil {
					t.Fatal(err)
				}
				id = payload.Session.ID
			}
		}
		response.Body.Close() // the page goes away mid-reply
		if id == "" {
			t.Fatal("stream never started")
		}
		return id
	}
	lastReply := func(id string) (domain.AgentMessage, bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			session := apiCall[domain.AgentSession](t, api.URL, http.MethodGet, "/api/agent/sessions/"+id, http.StatusOK)
			if last := session.Messages[len(session.Messages)-1]; last.Role == "assistant" {
				return last, true
			}
			time.Sleep(20 * time.Millisecond)
		}
		return domain.AgentMessage{}, false
	}

	id := startAndLeave("hi")
	time.Sleep(50 * time.Millisecond)
	close(release)
	reply, ok := lastReply(id)
	if !ok || reply.Content != "Hello world" {
		t.Fatalf("reply after the page left = %+v (saved %v)", reply, ok)
	}

	release = make(chan struct{}) // the next reply blocks until stopped
	id = startAndLeave("again")
	apiCall[any](t, api.URL, http.MethodPost, "/api/agent/sessions/"+id+"/stop", http.StatusNoContent)
	stopped, ok := lastReply(id)
	if !ok || !strings.Contains(partsText(stopped.Parts), "Hello") || !strings.Contains(partsText(stopped.Parts), "Generation stopped") {
		t.Fatalf("stopped reply = %+v (saved %v)", stopped, ok)
	}
}

func partsText(parts []domain.AgentMessagePart) string {
	var text strings.Builder
	for _, part := range parts {
		text.WriteString(part.Text + " ")
	}
	return text.String()
}

func apiCall[T any](t *testing.T, base, method, path string, want int) T {
	t.Helper()
	request, _ := http.NewRequest(method, base+path, nil)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, response.StatusCode, want, raw)
	}
	var value T
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}
