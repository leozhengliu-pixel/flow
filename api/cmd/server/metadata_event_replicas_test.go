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

var metadataReplicaEvents = []string{
	"release_pipeline.access_key_used", "agent.message_feedback", "agent.session_feedback", "agent.message_changes", "agent.approval_resolved", "release_pipeline.access_key_revoked",
	"document.attachment_created", "document.attachment_saved", "document.attachment_deleted", "email_intake.updated", "oauth_application.secret_rotated", "review.written_back",
	"migration.previewed", "migration.mappings_updated", "migration.users_invited", "integration.token_updated", "integration.token_rotated", "integration.token_rotation_failed", "integration.token_rotation_updated", "oauth_sync_group.approved", "oauth_sync_group.denied",
}

func metadataReplicaID(kind string, replica int) string {
	return fmt.Sprintf("metadata_%s_%d", kind, replica)
}

func metadataReplicaAggregate(event string, replica, round int) string {
	switch {
	case event == "migration.previewed":
		return fmt.Sprintf("metadata_preview_%d_%d", replica, round)
	case strings.HasPrefix(event, "migration."):
		return metadataReplicaID("job", replica)
	case strings.HasPrefix(event, "integration."):
		return metadataReplicaID("integration", replica)
	case strings.HasPrefix(event, "oauth_sync_group."):
		return metadataReplicaID("sync_"+strings.TrimPrefix(event, "oauth_sync_group."), replica)
	case strings.HasPrefix(event, "document."):
		return metadataReplicaID("document", replica)
	case strings.HasPrefix(event, "email_intake."):
		return metadataReplicaID("intake", replica)
	case strings.HasPrefix(event, "oauth_application."):
		return metadataReplicaID("application", replica)
	case strings.HasPrefix(event, "review."):
		return metadataReplicaID("review", replica)
	case strings.HasPrefix(event, "release_pipeline."):
		return metadataReplicaID("pipeline", replica)
	default:
		return metadataReplicaID("session", replica)
	}
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
	case "document.attachment_created", "document.attachment_saved", "document.attachment_deleted":
		document, err := documentByID(data, metadataReplicaID("document", replica))
		if err != nil {
			return err
		}
		switch event {
		case "document.attachment_created":
			document.Attachments = append(document.Attachments, domain.Attachment{ID: fmt.Sprintf("created_%d", round), Title: note, CreatedAt: stamp})
		case "document.attachment_saved":
			index := slices.IndexFunc(document.Attachments, func(a domain.Attachment) bool { return a.ID == "saved" })
			if index < 0 {
				return errNotFound
			}
			document.Attachments[index].Title, document.Attachments[index].UpdatedAt = note, &stamp
		case "document.attachment_deleted":
			document.Attachments = slices.DeleteFunc(document.Attachments, func(a domain.Attachment) bool { return a.ID == fmt.Sprintf("delete_%d", round) })
		}
	case "email_intake.updated":
		index := slices.IndexFunc(data.EmailIntakeAddresses, func(a domain.EmailIntakeAddress) bool { return a.ID == metadataReplicaID("intake", replica) })
		if index < 0 {
			return errNotFound
		}
		data.EmailIntakeAddresses[index].SenderName = note
	case "oauth_application.secret_rotated":
		index := slices.IndexFunc(data.OAuthApplications, func(a domain.OAuthApplication) bool { return a.ID == metadataReplicaID("application", replica) })
		if index < 0 {
			return errNotFound
		}
		data.OAuthApplications[index].ClientSecret = note
	case "review.written_back":
		index := slices.IndexFunc(data.Reviews, func(a domain.CodeReview) bool { return a.ID == metadataReplicaID("review", replica) })
		if index < 0 {
			return errNotFound
		}
		data.Reviews[index].Events[0].Sync = &domain.ReviewEventSync{State: "failed", Error: note, AttemptedAt: stamp}
	case "migration.previewed":
		id := metadataReplicaAggregate(event, replica, round)
		bundle, _ := json.Marshal(map[string]string{"note": note})
		data.MigrationJobs = append([]domain.MigrationJob{{ID: id, Status: "mapping", Bundle: bundle, UserID: data.Viewer.ID, CreatedAt: stamp}}, data.MigrationJobs...)
		data.AuditLog = append([]domain.AuditLogEntry{{ID: "audit_" + id, ResourceID: id, ResourceType: "migration", Action: "previewed", Actor: data.Viewer, CreatedAt: stamp}}, data.AuditLog...)
	case "migration.mappings_updated", "migration.users_invited":
		job, err := migrationByID(data, metadataReplicaID("job", replica))
		if err != nil {
			return err
		}
		if event == "migration.mappings_updated" {
			job.Mappings[0].TargetName = note
		} else {
			job.Mappings[0].TargetID, job.Mappings[0].Status = note, "invited"
		}
		job.UpdatedAt = stamp
	case "integration.token_updated", "integration.token_rotated", "integration.token_rotation_failed", "integration.token_rotation_updated":
		index := slices.IndexFunc(data.IntegrationConnections, func(a domain.IntegrationConnection) bool { return a.ID == metadataReplicaID("integration", replica) })
		if index < 0 {
			return errNotFound
		}
		key := map[string]string{"integration.token_updated": "tokenName", "integration.token_rotated": "lastRotatedAt", "integration.token_rotation_failed": "rotationFailureReason", "integration.token_rotation_updated": "nextRotationAt"}[event]
		if data.IntegrationConnections[index].Config == nil {
			data.IntegrationConnections[index].Config = map[string]string{}
		}
		data.IntegrationConnections[index].Config[key] = note
	case "oauth_sync_group.approved", "oauth_sync_group.denied":
		index := slices.IndexFunc(data.OAuthSyncGroupRequests, func(a domain.OAuthSyncGroupRequest) bool {
			return a.ID == metadataReplicaAggregate(event, replica, round)
		})
		if index < 0 {
			return errNotFound
		}
		data.OAuthSyncGroupRequests[index].Status = strings.TrimPrefix(event, "oauth_sync_group.")
		data.OAuthSyncGroupRequests[index].ReviewedBy, data.OAuthSyncGroupRequests[index].ReviewedAt = note, &stamp
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
		id := metadataReplicaAggregate(event, input.Replica, input.Round)
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
		if err := repo.MutateWorkspace(t.Context(), contractReplicaWorkspace, "test.metadata_content", "seed", nil, func(data *domain.Bootstrap) error {
			data.Issues = append(data.Issues, domain.Issue{ID: "metadata_issue", Identifier: "META-1", Number: 1, Title: "Unchanged", Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, Version: 1})
			if data.Comments == nil {
				data.Comments = map[string][]domain.Comment{}
			}
			if data.Activities == nil {
				data.Activities = map[string][]domain.ActivityEvent{}
			}
			data.Comments["metadata_issue"] = []domain.Comment{{ID: "metadata_comment", Body: "Unchanged", User: data.Viewer}}
			data.Activities["metadata_issue"] = []domain.ActivityEvent{{ID: "metadata_activity", Type: "issue.created", Actor: data.Viewer}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	err = repo.MutateWorkspace(t.Context(), contractReplicaWorkspace, "loop.created", "metadata_seed", nil, func(data *domain.Bootstrap) error {
		data.AgentSessions, data.ReleasePipelines, data.Drafts = nil, nil, nil
		data.Webhooks = nil
		data.Documents, data.EmailIntakeAddresses, data.OAuthApplications, data.Reviews = nil, nil, nil, nil
		data.MigrationJobs, data.IntegrationConnections, data.OAuthSyncGroupRequests, data.AuditLog = nil, nil, nil, nil
		for i := range 2 {
			data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: metadataReplicaID("session", i), UserID: data.Viewer.ID, Messages: []domain.AgentMessage{{ID: "reply", Role: "assistant", Parts: []domain.AgentMessagePart{
				{ID: "tool", ToolCall: &domain.AgentToolCall{Change: &domain.AgentChange{Undoable: true}}},
				{ID: "approval", Question: &domain.AgentQuestion{Approval: &domain.AgentApproval{}}},
			}}}})
			data.ReleasePipelines = append(data.ReleasePipelines, domain.ReleasePipeline{ID: metadataReplicaID("pipeline", i), Name: "Metadata"})
			data.Drafts = append(data.Drafts, domain.Draft{ID: metadataReplicaID("draft", i), Type: "issue", UserID: data.Viewer.ID})
			attachments := []domain.Attachment{{ID: "saved"}}
			for round := range 12 {
				attachments = append(attachments, domain.Attachment{ID: fmt.Sprintf("delete_%d", round)})
			}
			data.Documents = append(data.Documents, domain.Document{ID: metadataReplicaID("document", i), Creator: data.Viewer, Attachments: attachments})
			data.EmailIntakeAddresses = append(data.EmailIntakeAddresses, domain.EmailIntakeAddress{ID: metadataReplicaID("intake", i), TeamID: data.Teams[0].ID})
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{ID: metadataReplicaID("application", i)})
			data.Reviews = append(data.Reviews, domain.CodeReview{ID: metadataReplicaID("review", i), Events: []domain.ReviewEvent{{ID: "event"}}})
			data.MigrationJobs = append(data.MigrationJobs, domain.MigrationJob{ID: metadataReplicaID("job", i), Status: "mapping", Mappings: []domain.MigrationEntityMapping{{ID: "mapping", EntityType: "user", SourceID: "source"}}})
			data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{ID: metadataReplicaID("integration", i), Config: map[string]string{}})
			for _, status := range []string{"approved", "denied"} {
				data.OAuthSyncGroupRequests = append(data.OAuthSyncGroupRequests, domain.OAuthSyncGroupRequest{ID: metadataReplicaID("sync_"+status, i), Status: "pending"})
			}
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
			filter := ""
			if table == "workspace_content_records" {
				filter = " AND kind IN ('comment','activity','notification','delivery')"
			}
			rows, err := db.Query("SELECT id,data FROM " + table + " WHERE workspace_key='contract-scale'" + filter + " ORDER BY id")
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
		dir, err := os.MkdirTemp(os.Getenv("TMPDIR"), "metadata-replica-")
		if err != nil {
			t.Fatal(err)
		}
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
	audit := auditLogEntries(repo, contractReplicaWorkspace)
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
		document, err := documentByID(&data, metadataReplicaID("document", i))
		if err != nil || len(document.Attachments) != rounds+1 {
			t.Fatalf("replica %d lost attachments", i)
		}
		if !slices.ContainsFunc(document.Attachments, func(a domain.Attachment) bool { return a.ID == "saved" && a.Title == note }) {
			t.Fatalf("replica %d lost saved link", i)
		}
		for round := range rounds {
			if !slices.ContainsFunc(document.Attachments, func(a domain.Attachment) bool {
				return a.ID == fmt.Sprintf("created_%d", round) && a.Title == fmt.Sprintf("replica %d round %d", i, round)
			}) {
				t.Fatalf("lost upload round %d", round)
			}
		}
		if !slices.ContainsFunc(data.EmailIntakeAddresses, func(a domain.EmailIntakeAddress) bool {
			return a.ID == metadataReplicaID("intake", i) && a.SenderName == note
		}) {
			t.Fatalf("replica %d lost intake edit", i)
		}
		if !slices.ContainsFunc(data.OAuthApplications, func(a domain.OAuthApplication) bool {
			return a.ID == metadataReplicaID("application", i) && a.ClientSecret == note
		}) {
			t.Fatalf("replica %d lost secret rotation", i)
		}
		if !slices.ContainsFunc(data.Reviews, func(a domain.CodeReview) bool {
			return a.ID == metadataReplicaID("review", i) && a.Events[0].Sync != nil && a.Events[0].Sync.Error == note
		}) {
			t.Fatalf("replica %d lost writeback", i)
		}
		job, err := migrationByID(&data, metadataReplicaID("job", i))
		if err != nil || job.Mappings[0].TargetName != note || job.Mappings[0].TargetID != note || job.Mappings[0].Status != "invited" {
			t.Fatalf("replica %d lost mapping/invitation bookkeeping", i)
		}
		for round := range rounds {
			id := metadataReplicaAggregate("migration.previewed", i, round)
			if _, err := migrationByID(&data, id); err != nil {
				t.Fatalf("lost preview %s", id)
			}
			if !slices.ContainsFunc(audit, func(a domain.AuditLogEntry) bool { return a.ResourceID == id && a.Action == "previewed" }) {
				t.Fatalf("lost preview audit %s", id)
			}
			marker := "?"
			if config.Driver == "postgres" {
				marker = "$1"
			}
			var raw []byte
			if err := db.QueryRow("SELECT data FROM workspace_content_records WHERE workspace_key='contract-scale' AND kind='migration_input' AND id="+marker, id).Scan(&raw); err != nil {
				t.Fatal("lost source bundle")
			}
			var source struct{ Bundle json.RawMessage }
			if json.Unmarshal(raw, &source) != nil {
				t.Fatal("invalid source bundle")
			}
			var bundle map[string]string
			if json.Unmarshal(source.Bundle, &bundle) != nil || bundle["note"] != fmt.Sprintf("replica %d round %d", i, round) {
				t.Fatal("source bundle changed")
			}
		}
		connection := data.IntegrationConnections[slices.IndexFunc(data.IntegrationConnections, func(a domain.IntegrationConnection) bool { return a.ID == metadataReplicaID("integration", i) })]
		for _, key := range []string{"tokenName", "lastRotatedAt", "rotationFailureReason", "nextRotationAt"} {
			if connection.Config[key] != note {
				t.Fatalf("replica %d lost %s", i, key)
			}
		}
		for _, status := range []string{"approved", "denied"} {
			if !slices.ContainsFunc(data.OAuthSyncGroupRequests, func(a domain.OAuthSyncGroupRequest) bool {
				return a.ID == metadataReplicaID("sync_"+status, i) && a.Status == status && a.ReviewedBy == note
			}) {
				t.Fatalf("replica %d lost %s sync decision", i, status)
			}
		}
	}
	t.Logf("METADATA_REPLICAS driver=%s processes=2 events=%d requests=%d busy=0 lost_updates=0 content_sha256=%s elapsed=%s", config.Driver, len(metadataReplicaEvents), rounds*2*(len(metadataReplicaEvents)+1), before, time.Since(started).Round(time.Millisecond))
}
