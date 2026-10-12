package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestDeveloperAgentExampleCompletesDelegatedIssue(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required to run the documented example")
	}
	s, host, admin, data := applicationHTTPFixture(t)
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Summary worker", "scopes": []string{"read", "app:mentionable", "app:assignable"}, "supportsClientCredentials": true}, data.Workspace.URLKey, 201)
	source, err := filepath.Abs("../../../docs/examples/application-agent.mjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	command := exec.CommandContext(ctx, node, source)
	command.Env = append(os.Environ(), "FLOW_BASE_URL="+host.URL, "FLOW_CLIENT_ID="+app.ClientID, "FLOW_CLIENT_SECRET="+app.ClientSecret)
	logPath := filepath.Join(t.TempDir(), "worker.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		cancel()
		log.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	t.Cleanup(func() { cancel(); <-done; log.Close() })
	deadline := time.Now().Add(15 * time.Second)
	var installation domain.ApplicationInstallation
	for time.Now().Before(deadline) {
		items, err := s.store.ListApplications(t.Context(), data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 1 {
			installation = items[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if installation.UserID == "" {
		raw, _ := os.ReadFile(logPath)
		t.Fatalf("example did not install: %s", raw)
	}
	issue := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issues", map[string]any{"title": "Summarize this issue", "teamId": data.Teams[0].ID, "delegateId": installation.UserID}, data.Workspace.URLKey, 201)
	if issue.AgentSessionID == "" {
		t.Fatal("delegation did not create a task")
	}
	for time.Now().Before(deadline) {
		task, err := s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == "complete" {
			activities, err := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
			if err != nil || len(activities) != 2 || activities[0].Type != "thought" || activities[1].Type != "response" {
				t.Fatalf("example activity contract: %+v %v", activities, err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	raw, _ := os.ReadFile(logPath)
	t.Fatalf("example did not finish the delegated issue: %s", raw)
}

func TestDeveloperAgentExampleStreamsClarifiesContinuesAndStops(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for the documented worker")
	}
	var calls atomic.Int32
	thirdStarted, thirdCanceled := make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(delta any, finish any) {
			raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		switch calls.Add(1) {
		case 1:
			send(map[string]any{"reasoning_content": "Need a language"}, nil)
			send(map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]string{"name": "request_input", "arguments": `{"question":"Language?","options":[{"value":"Chinese"},{"value":"English"}]}`}}}}, "tool_calls")
		case 2:
			if payload.Messages[len(payload.Messages)-1].Content != "Chinese" {
				t.Error("selection was not included in model history")
			}
			send(map[string]any{"content": "- First\n"}, nil)
			time.Sleep(600 * time.Millisecond)
			send(map[string]any{"content": "- Second\n"}, nil)
			time.Sleep(600 * time.Millisecond)
			send(map[string]any{"content": "- Third"}, "stop")
		default:
			close(thirdStarted)
			send(map[string]any{"reasoning_content": "Waiting"}, nil)
			<-r.Context().Done()
			close(thirdCanceled)
			return
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(provider.Close)
	s, host, admin, data := applicationHTTPFixture(t)
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Interactive worker", "scopes": []string{"read", "app:assignable", "app:mentionable"}, "supportsClientCredentials": true}, data.Workspace.URLKey, 201)
	source, _ := filepath.Abs("../../../docs/examples/application-agent.mjs")
	ctx, cancel := context.WithCancel(t.Context())
	command := exec.CommandContext(ctx, node, source)
	command.Env = append(os.Environ(), "FLOW_BASE_URL="+host.URL, "FLOW_CLIENT_ID="+app.ClientID, "FLOW_CLIENT_SECRET="+app.ClientSecret, "FLOW_LLM_BASE_URL="+provider.URL, "FLOW_LLM_API_KEY=test-only-provider-key", "FLOW_LLM_MODEL=test-model")
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = command.Wait(); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	deadline := time.Now().Add(15 * time.Second)
	wait := func(condition func() bool) {
		t.Helper()
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("worker did not reach expected state")
	}
	var installation domain.ApplicationInstallation
	wait(func() bool {
		apps, _ := s.store.ListApplications(t.Context(), data.Workspace.URLKey)
		if len(apps) == 1 {
			installation = apps[0]
			return true
		}
		return false
	})
	issue := authRequest[domain.Issue](t, admin, "POST", host.URL+"/api/issues", map[string]any{"title": "Ask for a language", "teamId": data.Teams[0].ID, "delegateId": installation.UserID}, data.Workspace.URLKey, 201)
	var task domain.AgentTask
	waitStatus := func(status string) {
		t.Helper()
		wait(func() bool {
			task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, issue.AgentSessionID)
			return task.Status == status
		})
	}
	waitStatus("awaitingInput")
	path := host.URL + "/api/agent-tasks/" + task.ID + "/activities"
	task = authRequest[domain.AgentTask](t, admin, "POST", path, map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Chinese"}, data.Workspace.URLKey, 200)
	waitStatus("complete")
	activities, _ := s.store.AgentActivities(t.Context(), data.Workspace.URLKey, task.ID, "")
	outputs := 0
	for _, activity := range activities {
		if activity.Type == "output" {
			outputs++
		}
	}
	if outputs < 2 {
		t.Fatal("worker did not emit incremental output")
	}
	task = authRequest[domain.AgentTask](t, admin, "POST", path, map[string]any{"expectedVersion": task.Version, "type": "prompt", "body": "Continue"}, data.Workspace.URLKey, 200)
	select {
	case <-thirdStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("follow-up did not start")
	}
	task, _ = s.store.AgentTask(t.Context(), data.Workspace.URLKey, task.ID)
	task = authRequest[domain.AgentTask](t, admin, "POST", path, map[string]any{"expectedVersion": task.Version, "type": "canceled"}, data.Workspace.URLKey, 200)
	select {
	case <-thirdCanceled:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not abort provider generation")
	}
	waitStatus("canceled")
}
