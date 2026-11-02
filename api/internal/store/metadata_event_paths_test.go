package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type metadataEventCase struct {
	event, aggregate string
	apply            func(*domain.Bootstrap)
}

// Keep one case per audited callback, including its nested record changes.
func metadataEventCases() []metadataEventCase {
	at := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	return []metadataEventCase{
		{"release_pipeline.access_key_used", "pipeline_metadata", func(data *domain.Bootstrap) { data.ReleasePipelines[0].AccessKeyLastUsedAt = &at }},
		{"agent.message_feedback", "session_metadata", func(data *domain.Bootstrap) {
			data.AgentSessions[0].Messages[0].Feedback = &domain.AgentFeedback{Rating: "positive", Comment: "Useful", UpdatedAt: at}
		}},
		{"agent.session_feedback", "session_metadata", func(data *domain.Bootstrap) {
			data.AgentSessions[0].Feedback = append(data.AgentSessions[0].Feedback, domain.AgentFeedback{Comment: "Useful conversation", UpdatedAt: at})
		}},
		{"agent.message_changes", "session_metadata", func(data *domain.Bootstrap) {
			data.AgentSessions[0].Messages[0].Parts[0].ToolCall.Change = &domain.AgentChange{State: "undone", UpdatedAt: &at}
		}},
		{"agent.approval_resolved", "session_metadata", func(data *domain.Bootstrap) {
			data.AgentSessions[0].Messages[0].Parts[1].Question.Approval.Decision = "approved"
		}},
		{"release_pipeline.access_key_revoked", "pipeline_metadata", func(data *domain.Bootstrap) {
			data.ReleasePipelines[0].AccessKeyRevokedAt = &at
			data.ReleasePipelines[0].UpdatedAt = at
		}},
		{"document.attachment_created", "document_metadata", func(data *domain.Bootstrap) {
			data.Documents[0].Attachments = append(data.Documents[0].Attachments, domain.Attachment{ID: "created", URL: "/uploads/metadata", Creator: data.Viewer, CreatedAt: at})
		}},
		{"document.attachment_saved", "document_metadata", func(data *domain.Bootstrap) {
			data.Documents[0].Attachments[0].Title = "Saved link"
			data.Documents[0].Attachments[0].UpdatedAt = &at
		}},
		{"document.attachment_deleted", "document_metadata", func(data *domain.Bootstrap) { data.Documents[0].Attachments = data.Documents[0].Attachments[:1] }},
		{"email_intake.updated", "intake_metadata", func(data *domain.Bootstrap) {
			data.EmailIntakeAddresses[0].SenderName = "Updated intake"
			data.EmailIntakeAddresses[0].TemplateID = data.IssueTemplates[0].ID
			data.EmailIntakeAddresses[0].UpdatedAt = at
		}},
		{"oauth_application.secret_rotated", "application_metadata", func(data *domain.Bootstrap) {
			data.OAuthApplications[0].ClientSecret = "rotated-fixture"
			data.OAuthApplications[0].CredentialsAuthorizedBy = data.Viewer.ID
			data.OAuthApplications[0].UpdatedAt = at
		}},
		{"review.written_back", "review_metadata", func(data *domain.Bootstrap) {
			data.Reviews[0].Events[0].Remote = &domain.ReviewEventRemote{ID: "remote", Kind: "note"}
			data.Reviews[0].Events[0].Sync = nil
			data.Reviews[0].Events[0].UpdatedAt = &at
		}},
	}
}

func seedMetadataEvents(data *domain.Bootstrap) {
	data.Webhooks = []domain.Webhook{{ID: "webhook_metadata", URL: "https://example.test/events", Enabled: true}}
	data.Documents = []domain.Document{{ID: "document_metadata", Creator: data.Viewer, InheritedTeamIDs: []string{data.Teams[0].ID}, Attachments: []domain.Attachment{{ID: "saved", URL: "https://example.test/link"}, {ID: "deleted"}}}}
	data.IssueTemplates = []domain.IssueTemplate{{ID: "template_metadata", TeamID: data.Teams[0].ID}}
	data.EmailIntakeAddresses = []domain.EmailIntakeAddress{{ID: "intake_metadata", TeamID: data.Teams[0].ID, Enabled: true}}
	data.OAuthApplications = []domain.OAuthApplication{{ID: "application_metadata", ClientSecret: "seed-fixture"}}
	data.Reviews = []domain.CodeReview{{ID: "review_metadata", Events: []domain.ReviewEvent{{ID: "review_event_metadata", Sync: &domain.ReviewEventSync{State: "pending", Op: "create"}}}}}
	data.ReleasePipelines = []domain.ReleasePipeline{{ID: "pipeline_metadata", Name: "Metadata", AccessKeyHash: "fixture", TeamIDs: []string{data.Teams[0].ID}}}
	data.AgentSessions = []domain.AgentSession{{ID: "session_metadata", UserID: data.Viewer.ID, Messages: []domain.AgentMessage{{ID: "message_metadata", Role: "assistant", Parts: []domain.AgentMessagePart{
		{ID: "tool", Type: "toolCall", ToolCall: &domain.AgentToolCall{Change: &domain.AgentChange{Undoable: true}}},
		{ID: "approval", Type: "question", Question: &domain.AgentQuestion{Approval: &domain.AgentApproval{}}},
	}}}}}
}

func TestAuditedMetadataEventsAvoidIssueHydration(t *testing.T) {
	for _, test := range metadataEventCases() {
		t.Run(test.event, func(t *testing.T) {
			repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "metadata.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			data := repo.Bootstrap()
			seedMetadataEvents(&data)
			writeFullMetadata(t, repo, data.Workspace.URLKey, data)
			repo.installWorkspace(data.Workspace.URLKey, collectionMetadata(data), false)
			// A full-path fallback fails before the callback, not just a trace assertion.
			if _, err := repo.db.ExecContext(t.Context(), `UPDATE issue_records SET data='not-json' WHERE workspace_key=?`, data.Workspace.URLKey); err != nil {
				t.Fatal(err)
			}
			ctx, trace := startMutationTrace(t.Context())
			err = repo.dispatchMutation(ctx, data.Workspace.URLKey, test.event, nil, func(next *domain.Bootstrap) (string, error) {
				if len(next.Issues)+len(next.Comments)+len(next.Activities)+len(next.Notifications)+len(next.NotificationDeliveries) != 0 {
					t.Fatal("metadata callback loaded issue/content collections")
				}
				test.apply(next)
				return test.aggregate, nil
			})
			if err != nil || trace.path != "metadata" {
				t.Fatalf("path=%s error=%v", trace.path, err)
			}
		})
	}
}

func TestAuditedMetadataEventsMatchFullPath(t *testing.T) {
	seed, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatal(err)
	}
	base := seed.Bootstrap()
	seedMetadataEvents(&base)
	seed.Close()
	for _, test := range metadataEventCases() {
		t.Run(test.event, func(t *testing.T) {
			type outcome struct {
				metadata  metadataDump
				hook      domain.DomainEvent
				realtime  domain.RealtimeEvent
				persisted string
			}
			results := []outcome{}
			for _, full := range []bool{false, true} {
				repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "comparison.db"))
				if err != nil {
					t.Fatal(err)
				}
				data := cloneBootstrap(base)
				writeFullMetadata(t, repo, data.Workspace.URLKey, data)
				repo.installWorkspace(data.Workspace.URLKey, collectionMetadata(data), false)
				got := outcome{}
				repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { got.hook = event })
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { got.realtime = event })
				restore := ForceFullMutationsForTesting(full)
				err = repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, test.event, test.aggregate, map[string]string{"fixture": "metadata"}, func(next *domain.Bootstrap) error { test.apply(next); return nil })
				restore()
				if err != nil {
					t.Fatal(err)
				}
				got.metadata = dumpWorkspaceMetadata(t, repo, data.Workspace.URLKey)
				var eventType, aggregate string
				var payload, previous []byte
				if err := repo.db.QueryRowContext(t.Context(), `SELECT event_type,aggregate_id,payload,previous_values FROM domain_events WHERE event_type=? ORDER BY created_at DESC LIMIT 1`, test.event).Scan(&eventType, &aggregate, &payload, &previous); err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal([]any{eventType, aggregate, json.RawMessage(payload), json.RawMessage(previous)})
				got.persisted = string(encoded)
				got.hook.ID, got.hook.CreatedAt = "", time.Time{}
				got.realtime.ID, got.realtime.CreatedAt = "", time.Time{}
				results = append(results, got)
				// ErrNoMutation is the callback contract for unchanged feedback.
				writes := auditWrites(t, repo)
				if _, err := repo.db.ExecContext(t.Context(), `CREATE TRIGGER audit_metadata_events AFTER INSERT ON domain_events BEGIN INSERT INTO write_audit VALUES('domain_events'); END`); err != nil {
					t.Fatal(err)
				}
				restore = ForceFullMutationsForTesting(full)
				err = repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, test.event, test.aggregate, nil, func(*domain.Bootstrap) error { return ErrNoMutation })
				restore()
				if err != nil || len(writes()) != 0 {
					t.Fatalf("no-op wrote rows: %v", err)
				}
				// Callback errors retain identity and never publish or write.
				sentinel := errors.New("callback failure")
				err = repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, test.event, test.aggregate, nil, func(*domain.Bootstrap) error { return sentinel })
				if !errors.Is(err, sentinel) || len(writes()) != 0 {
					t.Fatalf("callback error=%v", err)
				}
				repo.Close()
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatalf("metadata/full outcomes differ: metadata=%t webhook=%t realtime=%t persisted=%t", reflect.DeepEqual(results[0].metadata, results[1].metadata), reflect.DeepEqual(results[0].hook, results[1].hook), reflect.DeepEqual(results[0].realtime, results[1].realtime), results[0].persisted == results[1].persisted)
			}
		})
	}
}
