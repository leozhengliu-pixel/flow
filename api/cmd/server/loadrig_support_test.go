//go:build loadtest

package main

// Shared pieces of the bounded-metadata load rig (scripts/loadrig/loadrig.sh):
// the fixture written by TestLoadRigSeed and read by TestLoadRigDrive, a
// small HTTP helper, and two local stand-ins the rig can run as separate
// processes: an OpenAI-compatible fake model (no paid model calls) and an
// in-memory Redis for the two-replica topology when no Redis image is
// available.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// loadRigFixture is the seeded workspace the driver needs. It holds session
// tokens and API key secrets, so it is written 0600 and never printed.
type loadRigFixture struct {
	Workspace   string              `json:"workspace"`
	WorkspaceID string              `json:"workspaceId"`
	Scale       float64             `json:"scale"`
	Admin       loadRigIdentity     `json:"admin"`
	Identities  []loadRigIdentity   `json:"identities"`
	Teams       []loadRigTeam       `json:"teams"`
	Issues      []loadRigIssueRef   `json:"issues"`
	IssueCount  int                 `json:"issueCount"`
	Projects    []loadRigRef        `json:"projects"`
	Documents   []loadRigRef        `json:"documents"`
	LoopIDs     []string            `json:"loopIds"`
	ViewIDs     []string            `json:"viewIds"`
	LabelIDs    []string            `json:"labelIds"`
	APIKeys     []loadRigAPIKey     `json:"apiKeys"`
	Words       []string            `json:"searchWords"`
	Seed        map[string]any      `json:"seed"`
	Extra       map[string][]string `json:"extra,omitempty"`
}

type loadRigIdentity struct {
	ID      string   `json:"id"`
	Token   string   `json:"token"`
	Role    string   `json:"role"`
	TeamIDs []string `json:"teamIds"`
}

type loadRigTeam struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Private bool   `json:"private"`
}

type loadRigIssueRef struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier"`
	TeamID     string `json:"teamId"`
}

// loadRigRef is a project or document and the team that owns it.
type loadRigRef struct {
	ID     string `json:"id"`
	TeamID string `json:"teamId"`
}

type loadRigAPIKey struct {
	ID      string   `json:"id"`
	Secret  string   `json:"secret"`
	TeamIDs []string `json:"teamIds"`
	Scopes  []string `json:"scopes"`
}

func loadRigEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func loadRigEnvFloat(t *testing.T, name string, fallback float64) float64 {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		t.Fatalf("%s must be a positive number", name)
	}
	return value
}

func loadRigEnvInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		t.Fatalf("%s must be a non-negative integer", name)
	}
	return value
}

func loadRigEnvDuration(t *testing.T, name string, fallback time.Duration) time.Duration {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 0 {
		t.Fatalf("%s must be a duration", name)
	}
	return value
}

func loadRigWriteJSON(path string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".loadrig-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(raw, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// loadRigCall sends one JSON request and decodes a 2xx answer into out.
// Errors never include credentials.
func loadRigCall(ctx context.Context, client *http.Client, method, url, workspace string, input, out any) (int, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if workspace != "" {
		request.Header.Set("X-Workspace-Key", workspace)
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		message := string(raw)
		if len(message) > 300 {
			message = message[:300]
		}
		return response.StatusCode, fmt.Errorf("%s %s: status %d: %s", method, loadRigPathOnly(url), response.StatusCode, message)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return response.StatusCode, fmt.Errorf("%s %s: decode: %w", method, loadRigPathOnly(url), err)
		}
	}
	return response.StatusCode, nil
}

func loadRigPathOnly(raw string) string {
	if index := strings.Index(raw, "/api/"); index >= 0 {
		raw = raw[index:]
	}
	if index := strings.IndexByte(raw, '?'); index >= 0 {
		raw = raw[:index]
	}
	return raw
}

func loadRigSignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// TestLoadRigFakeModel serves an OpenAI-compatible chat completions model
// on FLOW_LOADRIG_FAKE_MODEL_ADDR until interrupted, so loop runs and agent
// replies cost nothing. A turn with a finish_run tool first reads the team
// list, then finishes the run; other turns answer briefly. Each answer waits
// FLOW_LOADRIG_FAKE_MODEL_DELAY (default 300ms) to model provider latency.
func TestLoadRigFakeModel(t *testing.T) {
	addr := os.Getenv("FLOW_LOADRIG_FAKE_MODEL_ADDR")
	if addr == "" {
		t.Skip("set FLOW_LOADRIG_FAKE_MODEL_ADDR")
	}
	delay := loadRigEnvDuration(t, "FLOW_LOADRIG_FAKE_MODEL_DELAY", 300*time.Millisecond)
	var servedCount, toolTurns atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []map[string]any `json:"messages"`
			Tools    []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&request)
		served := servedCount.Add(1)
		finish, read, toolResults := false, false, 0
		for _, tool := range request.Tools {
			finish = finish || tool.Function.Name == "finish_run"
			read = read || tool.Function.Name == "list_teams"
		}
		for _, message := range request.Messages {
			if message["role"] == "tool" {
				toolResults++
			}
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		call := func(name, arguments string) {
			toolTurns.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("loadrig-%d", served), "object": "chat.completion", "model": "loadrig-fake",
				"choices": []map[string]any{{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "content": "",
					"tool_calls": []map[string]any{{"id": fmt.Sprintf("call_%d", served), "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}}}}},
				"usage": map[string]int{"prompt_tokens": 1000, "completion_tokens": 20, "total_tokens": 1020}})
		}
		switch {
		case finish && read && toolResults == 0:
			call("list_teams", `{}`)
		case finish && toolResults == 1:
			call("finish_run", `{"status":"done","summary":"Load rig run complete.","done":[],"notDone":[]}`)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": fmt.Sprintf("loadrig-%d", served), "object": "chat.completion", "model": "loadrig-fake",
				"choices": []map[string]any{{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "Load rig answer."}}},
				"usage":   map[string]int{"prompt_tokens": 1000, "completion_tokens": 5, "total_tokens": 1005}})
		}
	})
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := loadRigSignalContext()
	defer cancel()
	go func() { _ = server.Serve(listener) }()
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	_ = server.Shutdown(shutdown)
	t.Logf("fake model served %d completions (%d tool turns)", servedCount.Load(), toolTurns.Load())
}

// TestLoadRigRedis serves an in-memory Redis (miniredis) on
// FLOW_LOADRIG_REDIS_ADDR until interrupted: the coordination backend for the
// two-replica topology when the Redis container image is not available.
func TestLoadRigRedis(t *testing.T) {
	addr := os.Getenv("FLOW_LOADRIG_REDIS_ADDR")
	if addr == "" {
		t.Skip("set FLOW_LOADRIG_REDIS_ADDR")
	}
	redis := miniredis.NewMiniRedis()
	if err := redis.StartAddr(addr); err != nil {
		t.Fatal(err)
	}
	defer redis.Close()
	ctx, cancel := loadRigSignalContext()
	defer cancel()
	<-ctx.Done()
}
