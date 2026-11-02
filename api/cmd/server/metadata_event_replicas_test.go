package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

var metadataReplicaEvents = []string{"release_pipeline.access_key_used", "agent.message_feedback", "agent.session_feedback", "agent.message_changes", "agent.approval_resolved", "release_pipeline.access_key_revoked"}

func metadataReplicaID(kind string, replica int) string {
	return fmt.Sprintf("metadata_%s_%d", kind, replica)
}

func metadataReplicaEdit(data *domain.Bootstrap, event string, replica, round int) error {
	stamp := time.Date(2026, 11, 1, 12, replica, round, 0, time.UTC)
	note := fmt.Sprintf("replica %d round %d", replica, round)
	index := slices.IndexFunc(data.AgentSessions, func(item domain.AgentSession) bool { return item.ID == metadataReplicaID("session", replica) })
	if index < 0 {
		return errNotFound
	}
	session := &data.AgentSessions[index]
	pipeline := releasePipelineByID(data, metadataReplicaID("pipeline", replica))
	if pipeline == nil {
		return errNotFound
	}
	switch event {
	case "release_pipeline.access_key_used":
		pipeline.AccessKeyLastUsedAt = &stamp
	case "release_pipeline.access_key_revoked":
		pipeline.AccessKeyRevokedAt = &stamp
	case "agent.message_feedback":
		session.Messages[0].Feedback = &domain.AgentFeedback{Rating: "positive", Comment: note, UpdatedAt: stamp}
	case "agent.session_feedback":
		session.Feedback = append(session.Feedback, domain.AgentFeedback{Comment: note, UpdatedAt: stamp})
	case "agent.message_changes":
		session.Messages[0].Parts[0].ToolCall.Change = &domain.AgentChange{State: "undone", Note: note, UpdatedAt: &stamp}
	case "agent.approval_resolved":
		session.Messages[0].Parts[1].Question.Approval.Decision = note
	default:
		return errInvalid
	}
	return nil
}

// Gate-only routes exercise concurrent dispatch with deterministic callbacks;
// ordinary handler tests above cover the production validation and side effects.
func TestMetadataEventReplicaProcess(t *testing.T) {
	if os.Getenv("FLOW_METADATA_CHILD") != "1" {
		t.Skip("metadata gate subprocess")
	}
	repo, err := store.OpenDatabase(contractReplicaConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	coordinator, err := coordination.Open(ctx, coordination.Config{Mode: "standalone", URL: os.Getenv("FLOW_TEST_REDIS_URL"), Prefix: "metadata-replicas", LockWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()
	repo.SetWorkspaceCoordinator(coordinator)
	srv := &server{store: repo, authDisabled: true, uploadPath: os.Getenv("FLOW_METADATA_DIR"), coordinator: coordinator, coordinationContext: ctx}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /gate/{event}", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Replica, Round int }
		if !decodeJSON(w, r, &input) {
			return
		}
		event := r.PathValue("event")
		if !slices.Contains(metadataReplicaEvents, event) {
			writeError(w, http.StatusBadRequest, "unknown gate event")
			return
		}
		id := metadataReplicaID("session", input.Replica)
		if strings.HasPrefix(event, "release_pipeline.") {
			id = metadataReplicaID("pipeline", input.Replica)
		}
		err := repo.MutateWorkspace(r.Context(), contractReplicaWorkspace, event, id, input, func(data *domain.Bootstrap) error {
			return metadataReplicaEdit(data, event, input.Replica, input.Round)
		})
		respondMutation(w, err, http.StatusOK, map[string]bool{"saved": err == nil})
	})
	mux.Handle("/", newHandler(srv))
	api := httptest.NewServer(mux)
	defer api.Close()
	if err := os.WriteFile(filepath.Join(os.Getenv("FLOW_METADATA_DIR"), "url"), []byte(api.URL), 0600); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}

func TestMetadataEventTwoReplicaGate(t *testing.T) {
	if os.Getenv("FLOW_METADATA_REPLICAS") != "1" {
		t.Skip("opt-in two-process metadata gate")
	}
	config := contractReplicaConfig()
	if config.Driver == "" || config.URL == "" || os.Getenv("FLOW_TEST_REDIS_URL") == "" {
		t.Fatal("requires throwaway database and Redis")
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.HasWorkspace(contractReplicaWorkspace) {
		if _, err := repo.CreateWorkspace(t.Context(), "Metadata scale", contractReplicaWorkspace, "us"); err != nil {
			t.Fatal(err)
		}
	}
	err = repo.MutateWorkspace(t.Context(), contractReplicaWorkspace, "loop.created", "metadata_seed", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions, data.ReleasePipelines, data.Drafts = nil, nil, nil
		for i := range 2 {
			data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: metadataReplicaID("session", i), UserID: data.Viewer.ID, Messages: []domain.AgentMessage{{ID: "reply", Role: "assistant", Parts: []domain.AgentMessagePart{
				{ID: "tool", ToolCall: &domain.AgentToolCall{Change: &domain.AgentChange{Undoable: true}}},
				{ID: "approval", Question: &domain.AgentQuestion{Approval: &domain.AgentApproval{}}},
			}}}})
			data.ReleasePipelines = append(data.ReleasePipelines, domain.ReleasePipeline{ID: metadataReplicaID("pipeline", i), Name: "Metadata"})
			data.Drafts = append(data.Drafts, domain.Draft{ID: metadataReplicaID("draft", i), Type: "issue", UserID: data.Viewer.ID})
		}
		return nil
	})
	repo.Close()
	if err != nil {
		t.Fatal(err)
	}
	driver := config.Driver
	if driver == "postgres" {
		driver = "pgx"
	}
	db, err := sql.Open(driver, config.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	digest := func() string {
		hash := sha256.New()
		for _, table := range []string{"issue_records", "workspace_content_records"} {
			rows, err := db.Query("SELECT id,data FROM " + table + " WHERE workspace_key='contract-scale' ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id string
				var raw []byte
				if err := rows.Scan(&id, &raw); err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(hash, "%d:%s%d:", len(id), id, len(raw))
				hash.Write(raw)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
		return fmt.Sprintf("%x", hash.Sum(nil))
	}
	before := digest()
	var processes []*exec.Cmd
	var urls []string
	stop := func() {
		for _, cmd := range processes {
			_ = cmd.Process.Signal(os.Interrupt)
		}
		for _, cmd := range processes {
			if err := cmd.Wait(); err != nil {
				t.Errorf("replica process: %v (see %s)", err, cmd.Dir)
			}
		}
		processes = nil
	}
	t.Cleanup(stop)
	for i := range 2 {
		dir := t.TempDir()
		output, err := os.Create(filepath.Join(dir, "replica.log"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { output.Close() })
		cmd := exec.Command(os.Args[0], "-test.run=^TestMetadataEventReplicaProcess$", "-test.timeout=3m")
		cmd.Env = append(os.Environ(), "FLOW_METADATA_CHILD=1", "FLOW_METADATA_DIR="+dir)
		cmd.Stdout, cmd.Stderr, cmd.Dir = output, output, dir
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		processes = append(processes, cmd)
		deadline := time.Now().Add(90 * time.Second)
		for {
			url, err := os.ReadFile(filepath.Join(dir, "url"))
			if err == nil {
				urls = append(urls, string(url))
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("replica %d did not start; see %s", i, dir)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	request := func(base, method, path string, input any) error {
		raw, _ := json.Marshal(input)
		req, _ := http.NewRequest(method, base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Workspace-Key", contractReplicaWorkspace)
		response, err := client.Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK || strings.Contains(string(body), "workspace is busy") {
			return fmt.Errorf("%s status=%d: %.500s", path, response.StatusCode, body)
		}
		return nil
	}
	started := time.Now()
	const rounds = 12
	for round := range rounds {
		var group sync.WaitGroup
		failures := make(chan error, 2*(len(metadataReplicaEvents)+1))
		for i := range 2 {
			for _, event := range metadataReplicaEvents {
				group.Add(1)
				go func() {
					defer group.Done()
					failures <- request(urls[i], http.MethodPost, "/gate/"+event, map[string]int{"replica": i, "round": round})
				}()
			}
			group.Add(1)
			go func() {
				defer group.Done()
				failures <- request(urls[i], http.MethodPatch, "/api/drafts/"+metadataReplicaID("draft", i), map[string]string{"title": fmt.Sprintf("Replica %d round %d", i, round), "body": fmt.Sprintf("Saved body %d", round)})
			}()
		}
		group.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	stop()
	if digest() != before {
		t.Fatal("issue/comment/activity bytes changed")
	}
	repo, err = store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data, ok := repo.WorkspaceMetadata(contractReplicaWorkspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	for i := range 2 {
		// Every field and every appended feedback entry is checked, not just
		// the last request's response from a possibly stale replica cache.
		session := data.AgentSessions[slices.IndexFunc(data.AgentSessions, func(s domain.AgentSession) bool { return s.ID == metadataReplicaID("session", i) })]
		note := fmt.Sprintf("replica %d round %d", i, rounds-1)
		if session.Messages[0].Feedback == nil || session.Messages[0].Feedback.Comment != note || session.Messages[0].Parts[0].ToolCall.Change.Note != note || session.Messages[0].Parts[1].Question.Approval.Decision != note || len(session.Feedback) != rounds {
			t.Fatalf("replica %d lost session updates", i)
		}
		for round := range rounds {
			if !slices.ContainsFunc(session.Feedback, func(f domain.AgentFeedback) bool { return f.Comment == fmt.Sprintf("replica %d round %d", i, round) }) {
				t.Fatalf("lost feedback round %d", round)
			}
		}
		pipeline := releasePipelineByID(&data, metadataReplicaID("pipeline", i))
		stamp := time.Date(2026, 11, 1, 12, i, rounds-1, 0, time.UTC)
		if pipeline == nil || pipeline.AccessKeyLastUsedAt == nil || !pipeline.AccessKeyLastUsedAt.Equal(stamp) || pipeline.AccessKeyRevokedAt == nil || !pipeline.AccessKeyRevokedAt.Equal(stamp) {
			t.Fatalf("replica %d lost key updates", i)
		}
		if !slices.ContainsFunc(data.Drafts, func(d domain.Draft) bool {
			return d.ID == metadataReplicaID("draft", i) && d.Title == fmt.Sprintf("Replica %d round %d", i, rounds-1) && d.Body == fmt.Sprintf("Saved body %d", rounds-1)
		}) {
			t.Fatalf("replica %d lost draft", i)
		}
	}
	t.Logf("METADATA_REPLICAS driver=%s processes=2 events=%d requests=%d busy=0 lost_updates=0 content_sha256=%s elapsed=%s", config.Driver, len(metadataReplicaEvents), rounds*2*(len(metadataReplicaEvents)+1), before, time.Since(started).Round(time.Millisecond))
}
