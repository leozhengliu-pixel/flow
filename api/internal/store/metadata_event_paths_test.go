package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
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
		{"migration.previewed", "migration_preview_metadata", func(data *domain.Bootstrap) {
			job := domain.MigrationJob{ID: "migration_preview_metadata", UserID: data.Viewer.ID, Status: "mapping", Bundle: json.RawMessage(`{"version":1,"source":"flow"}`), CreatedAt: at, UpdatedAt: at}
			data.MigrationJobs = append([]domain.MigrationJob{job}, data.MigrationJobs...)
			data.AuditLog = append([]domain.AuditLogEntry{{ID: "audit_metadata_preview", Actor: data.Viewer, Action: "previewed", ResourceType: "migration", ResourceID: job.ID, CreatedAt: at}}, data.AuditLog...)
		}},
		{"migration.mappings_updated", "migration_metadata", func(data *domain.Bootstrap) {
			index := slices.IndexFunc(data.MigrationJobs, func(job domain.MigrationJob) bool { return job.ID == "migration_metadata" })
			data.MigrationJobs[index].Mappings[0].Action = "invite"
			data.MigrationJobs[index].UpdatedAt = at
		}},
		{"migration.users_invited", "migration_metadata", func(data *domain.Bootstrap) {
			index := slices.IndexFunc(data.MigrationJobs, func(job domain.MigrationJob) bool { return job.ID == "migration_metadata" })
			data.MigrationJobs[index].Mappings[0].Status, data.MigrationJobs[index].Mappings[0].TargetID = "invited", "invitation_metadata"
			data.MigrationJobs[index].UpdatedAt = at
		}},
		{"integration.token_updated", "integration_metadata", func(data *domain.Bootstrap) {
			data.IntegrationConnections[0].Config["tokenName"] = "Updated"
			data.IntegrationConnections[0].Scopes = []string{"read"}
			data.IntegrationConnections[0].UpdatedAt = at
		}},
		{"integration.token_rotated", "integration_metadata", func(data *domain.Bootstrap) {
			data.IntegrationConnections[0].Config["lastRotatedAt"] = at.Format(time.RFC3339)
			delete(data.IntegrationConnections[0].Config, "rotationFailureReason")
			data.IntegrationConnections[0].UpdatedAt = at
		}},
		{"integration.token_rotation_failed", "integration_metadata", func(data *domain.Bootstrap) {
			data.IntegrationConnections[0].Config["rotationFailureReason"] = "Scripted failure"
			data.IntegrationConnections[0].UpdatedAt = at
		}},
		{"integration.token_rotation_updated", "integration_metadata", func(data *domain.Bootstrap) {
			data.IntegrationConnections[0].Config["rotationEnabled"] = "false"
			delete(data.IntegrationConnections[0].Config, "rotationFailureReason")
			data.IntegrationConnections[0].UpdatedAt = at
		}},
		{"oauth_sync_group.approved", "sync_metadata", func(data *domain.Bootstrap) {
			data.OAuthSyncGroupRequests[0].Status, data.OAuthSyncGroupRequests[0].ReviewedBy, data.OAuthSyncGroupRequests[0].ReviewedAt = "approved", data.Viewer.ID, &at
		}},
		{"oauth_sync_group.denied", "sync_metadata", func(data *domain.Bootstrap) {
			data.OAuthSyncGroupRequests[0].Status, data.OAuthSyncGroupRequests[0].ReviewedBy, data.OAuthSyncGroupRequests[0].ReviewedAt = "denied", data.Viewer.ID, &at
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
	data.MigrationJobs = []domain.MigrationJob{{ID: "migration_metadata", Status: "mapping", Mappings: []domain.MigrationEntityMapping{{ID: "mapping_metadata", EntityType: "user", SourceID: "source", Action: "review"}}, Bundle: json.RawMessage(`{"source":"flow"}`)}}
	data.IntegrationConnections = []domain.IntegrationConnection{{ID: "integration_metadata", Config: map[string]string{"rotationFailureReason": "Previous failure"}}}
	data.OAuthSyncGroupRequests = []domain.OAuthSyncGroupRequest{{ID: "sync_metadata", Status: "pending"}}
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
				inputs    map[string]string
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
				got.inputs = map[string]string{}
				rows, err := repo.db.QueryContext(t.Context(), `SELECT id,data FROM workspace_content_records WHERE workspace_key=? AND kind='migration_input'`, data.Workspace.URLKey)
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var id, raw string
					if err := rows.Scan(&id, &raw); err != nil {
						t.Fatal(err)
					}
					got.inputs[id] = raw
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
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
				restore = ForceFullMutationsForTesting(full)
				err = repo.MutateWorkspace(t.Context(), data.Workspace.URLKey, test.event, test.aggregate, nil, func(*domain.Bootstrap) error { return sentinel })
				restore()
				if !errors.Is(err, sentinel) || len(writes()) != 0 {
					t.Fatalf("callback error=%v", err)
				}
				repo.Close()
			}
			if !reflect.DeepEqual(results[0], results[1]) {
				t.Fatalf("metadata/full outcomes differ: metadata=%t webhook=%t realtime=%t persisted=%t inputs=%t", reflect.DeepEqual(results[0].metadata, results[1].metadata), reflect.DeepEqual(results[0].hook, results[1].hook), reflect.DeepEqual(results[0].realtime, results[1].realtime), results[0].persisted == results[1].persisted, reflect.DeepEqual(results[0].inputs, results[1].inputs))
			}
		})
	}
}

func TestIssueCountCallbacksKeepLoadedIssues(t *testing.T) {
	for _, event := range []string{"workspace_invitation.accepted", "workspace_invite_link.joined"} {
		t.Run(event, func(t *testing.T) {
			repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "count.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			data := repo.Bootstrap()
			ctx, trace := startMutationTrace(t.Context())
			err = repo.dispatchMutation(ctx, data.Workspace.URLKey, event, nil, func(next *domain.Bootstrap) (string, error) {
				if len(next.Issues) != len(data.Issues) || len(next.Issues) == 0 {
					t.Fatal("membership response would silently report an empty issue count")
				}
				return "", ErrNoMutation
			})
			if err != nil || trace.path != "full" {
				t.Fatalf("path=%s error=%v", trace.path, err)
			}
		})
	}
}
