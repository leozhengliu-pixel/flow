package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Exercise the production callbacks, authorization and HTTP errors on both
// dispatch paths. Store tests separately compare persisted rows and envelopes.
func TestMetadataEventHandlerFullPathParity(t *testing.T) {
	for _, full := range []bool{false, true} {
		name := "metadata"
		if full {
			name = "full"
		}
		t.Run(name, func(t *testing.T) {
			restore := store.ForceFullMutationsForTesting(full)
			defer restore()
			t.Run("feedback", TestAgentMessageAndSessionFeedback)
			t.Run("approval", TestAgentChatSavedApprovalStillResolves)
			t.Run("changes", TestAgentChangeSetRecordsWritesAndUndoesThem)
			t.Run("release_keys", TestReleaseAccessKeyRotationGraceAndRevocation)
			t.Run("document_upload", TestDocumentAttachmentUploadIsServedToDocumentViewersOnly)
			t.Run("document_links", TestMCPDocumentAttachmentUploadFlow)
			t.Run("document_permissions", TestMCPDocumentAttachmentPermissions)
			t.Run("email_intake", TestSystemEmailIntakeAddressWizardFlow)
			t.Run("email_templates", TestEmailIntakeExcludedDomainAndTemplate)
			t.Run("application_secret", TestApplicationSecretRotationAndDeveloperTokens)
			t.Run("review_writeback", TestReviewWriteBackPostsAsTheMember)
			t.Run("review_retry", TestReviewWriteBackRefusedMemberFailsThenRetrySucceeds)
			t.Run("unchanged", testMetadataUnchangedHandlers)
			t.Run("migration_bookkeeping", testMetadataMigrationHandlers)
			t.Run("sync_decisions", testMetadataSyncDecisions)
			t.Run("token_bookkeeping", TestGitLabTokenLifecycleStoresEncryptedTokenRotatesAndDisconnects)
		})
	}
}

func testMetadataMigrationHandlers(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "migrations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	data := repo.Bootstrap()
	bundle := domain.MigrationBundle{Version: 1, Source: "flow", Workspace: domain.Workspace{ID: "source"}, Users: []domain.User{{ID: "source_user", Name: "New member", DisplayName: "New member", Email: "metadata-invite@example.test"}}}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "metadata-bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(part).Encode(bundle); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/migrations/preview", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("preview: %d %.300s", response.Code, response.Body.String())
	}
	var job domain.MigrationJob
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil || job.ID == "" || job.Bundle != nil {
		t.Fatal("invalid public preview")
	}
	base := "/api/migrations/" + job.ID
	input := map[string]any{"mappings": []map[string]string{{"entityType": "user", "sourceId": "source_user", "action": "invite"}}, "targetTeamId": data.Teams[0].ID}
	job = requestJSON[domain.MigrationJob](t, handler, http.MethodPatch, base+"/mappings", input, http.StatusOK)
	if len(job.Mappings) != 1 || job.Mappings[0].Action != "invite" {
		t.Fatal("mapping edit was lost")
	}
	publications := 0
	repo.SetRealtimeSink(func(string, domain.RealtimeEvent) { publications++ })
	unchanged := requestJSON[domain.MigrationJob](t, handler, http.MethodPatch, base+"/mappings", input, http.StatusOK)
	if publications != 0 || !unchanged.UpdatedAt.Equal(job.UpdatedAt) {
		t.Fatal("identical mapping edit wrote an event or timestamp")
	}
	job = requestJSON[domain.MigrationJob](t, handler, http.MethodPost, base+"/invite-users", nil, http.StatusOK)
	if job.Mappings[0].Status != "invited" || job.Mappings[0].TargetID == "" {
		t.Fatal("invitation result was not recorded")
	}
	fresh := repo.Bootstrap()
	invitations, err := repo.ListInvitations(t.Context(), data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(invitations, func(invitation domain.Invitation) bool {
		return invitation.ID == job.Mappings[0].TargetID && invitation.Email == "metadata-invite@example.test"
	}) {
		t.Fatal("separate invitation was lost")
	}
	if !slices.ContainsFunc(auditLogOf(t, repo, fresh.Workspace.URLKey), func(entry domain.AuditLogEntry) bool {
		return entry.ResourceID == job.ID && entry.Action == "previewed"
	}) {
		t.Fatal("preview audit was lost")
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/migrations/missing/mappings", input, http.StatusNotFound)
}

func testMetadataSyncDecisions(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	err = repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "oauth_application.created", "metadata_app", nil, func(next *domain.Bootstrap) error {
		next.OAuthApplications = append(next.OAuthApplications, domain.OAuthApplication{ID: "metadata_app", Name: "Metadata"})
		next.OAuthSyncGroupRequests = []domain.OAuthSyncGroupRequest{{ID: "metadata_sync", ApplicationID: "metadata_app", Status: "pending"}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	path := "/api/oauth-applications/metadata_app/sync-groups/metadata_sync"
	for _, status := range []string{"approved", "denied"} {
		request := requestJSON[domain.OAuthSyncGroupRequest](t, handler, http.MethodPost, path, map[string]string{"status": status, "teamId": data.Teams[0].ID}, http.StatusOK)
		if request.Status != status || request.ReviewedAt == nil || request.TeamID != data.Teams[0].ID {
			t.Fatalf("lost %s decision", status)
		}
	}
	requestJSON[any](t, handler, http.MethodPost, path, map[string]string{"status": "unknown"}, http.StatusUnprocessableEntity)
	requestJSON[any](t, handler, http.MethodPost, "/api/oauth-applications/metadata_app/sync-groups/missing", map[string]string{"status": "approved"}, http.StatusNotFound)
}

func testMetadataUnchangedHandlers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unchanged.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	err = repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, "agent.session_created", "metadata_noop", nil, func(next *domain.Bootstrap) error {
		next.AgentSessions = []domain.AgentSession{{ID: "metadata_noop", UserID: next.Viewer.ID, Messages: []domain.AgentMessage{{ID: "reply", Role: "assistant", Feedback: &domain.AgentFeedback{Rating: "positive", Comment: "Useful", UpdatedAt: at}}}}}
		next.EmailIntakeAddresses = []domain.EmailIntakeAddress{{ID: "metadata_intake", TeamID: data.Teams[0].ID, SenderName: "Helpdesk", UpdatedAt: at}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := newHandler(&server{store: repo, uploadPath: t.TempDir(), authDisabled: true})
	publications := 0
	repo.SetWebhookSink(func(string, domain.DomainEvent) { publications++ })
	repo.SetRealtimeSink(func(string, domain.RealtimeEvent) { publications++ })
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE noop_writes(table_name TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"domain_events", "workspace_metadata_records", "workspace_states", "workspace_content_records", "issue_records"} {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, err := db.Exec("CREATE TRIGGER noop_" + table + "_" + operation + " AFTER " + operation + " ON " + table + " BEGIN INSERT INTO noop_writes VALUES('" + table + "'); END"); err != nil {
				t.Fatal(err)
			}
		}
	}
	session := requestJSON[domain.AgentSession](t, handler, http.MethodPut, "/api/agent/sessions/metadata_noop/messages/reply/feedback", map[string]string{"rating": "positive", "comment": "Useful"}, http.StatusOK)
	if session.Messages[0].Feedback == nil || !session.Messages[0].Feedback.UpdatedAt.Equal(at) {
		t.Fatal("identical feedback changed its timestamp")
	}
	address := requestJSON[emailIntakeAddressView](t, handler, http.MethodPatch, "/api/teams/"+data.Teams[0].ID+"/email-intake-addresses/metadata_intake", map[string]string{"senderName": "Helpdesk"}, http.StatusOK)
	if !address.UpdatedAt.Equal(at) {
		t.Fatal("identical intake edit changed its timestamp")
	}
	var writes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM noop_writes`).Scan(&writes); err != nil {
		t.Fatal(err)
	}
	if writes != 0 || publications != 0 {
		t.Fatalf("unchanged handlers wrote %d rows and published %d envelopes", writes, publications)
	}
}
