package main

import (
	"database/sql"
	"net/http"
	"path/filepath"
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
		})
	}
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
