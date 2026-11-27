package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type desktopAckWriteInput struct {
	NotificationIDs []string `json:"notificationIds"`
}

func desktopAckWriteMetadata(t *testing.T, f scaleFixture, at time.Time) domain.Bootstrap {
	t.Helper()
	data, found := f.WorkspaceMetadata(f.data.Workspace.URLKey)
	if !found {
		t.Fatal("desktop metadata missing")
	}
	if data.UserSettings == nil {
		data.UserSettings = map[string]domain.UserSettings{}
	}
	if data.NotificationPreferences == nil {
		data.NotificationPreferences = map[string]domain.NotificationPreferences{}
	}
	for _, user := range data.Users {
		if _, found := data.UserSettings[user.ID]; !found {
			value := defaultUserSettings(user.ID)
			value.UpdatedAt = at
			data.UserSettings[user.ID] = value
		}
		if _, found := data.NotificationPreferences[user.ID]; !found {
			value := defaultNotificationPreferences(user.ID)
			value.UpdatedAt = at
			data.NotificationPreferences[user.ID] = value
		}
	}
	return data
}

// reloadEqual: the bounded installed image equals a fresh SQL
// reload of the same view.
func reloadEqual(t *testing.T, repo *SQLiteStore, workspace string, installed domain.Bootstrap) {
	t.Helper()
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind='metadata'`, workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if reloaded := legacyDiscussionWriteInstalled(t, repo, workspace); !reflect.DeepEqual(installed, reloaded) {
		t.Fatal("peer-raced installed image differs from the SQL reload", firstDiff(discussionWriterJSON(t, installed), discussionWriterJSON(t, reloaded), "installed"))
	}
}

func desktopAckWriteSeed(t *testing.T, f scaleFixture, issue domain.Issue, at time.Time, mode, viewer string) {
	t.Helper()
	discussionWriterSeed(t, f, issue, at)
	deliveries := []domain.NotificationDelivery{
		{ID: "sw134-first", NotificationID: "selected", RecipientID: viewer, Channel: "desktop", Status: "pending", Error: "Preserve first error", Attempts: 3, CreatedAt: at, UpdatedAt: at},
		{ID: "sw134-second", NotificationID: "selected", RecipientID: viewer, Channel: "desktop", Status: "failed", Error: "Preserve second error", Attempts: 7, CreatedAt: at, UpdatedAt: at},
		{ID: "sw134-other-recipient", NotificationID: "selected", RecipientID: "other", Channel: "desktop", Status: "failed", CreatedAt: at, UpdatedAt: at},
		{ID: "sw134-chat", NotificationID: "selected", RecipientID: viewer, Channel: "chat", Status: "pending", CreatedAt: at, UpdatedAt: at},
		{ID: "sw134-unselected", NotificationID: "unselected", RecipientID: viewer, Channel: "desktop", Status: "pending-disabled", CreatedAt: at, UpdatedAt: at},
		{ID: "sw134-empty", NotificationID: "", RecipientID: viewer, Channel: "desktop", Status: "delivered", DeliveredAt: &at, AcknowledgedAt: &at, CreatedAt: at, UpdatedAt: at},
	}
	if mode == "duplicate-native" {
		deliveries[1].ID = deliveries[0].ID
	}
	if mode == "large-unrelated" {
		deliveries[2].Error = strings.Repeat("Unrelated legal delivery detail. ", 10000)
	}
	if err := f.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw134_seed", "", nil, func(data *domain.Bootstrap) error {
		data.NotificationDeliveries = deliveries
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Keep the warm native occurrence topology; reloaded cases deliberately
	// use the canonical delivery image, which may have one duplicate ID.
	f.mu.Lock()
	data := f.workspaces[f.data.Workspace.URLKey]
	data.NotificationDeliveries = deliveries
	f.workspaces[f.data.Workspace.URLKey] = data
	f.mu.Unlock()
}

func TestDesktopAcknowledgementDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, reload := range []bool{false, true} {
		for _, mode := range []string{"selected", "empty-input", "nil-input", "empty-id", "duplicate-input", "duplicate-native", "large-unrelated", "empty-viewer", "canonical-merge", "retry", "noop", "cancel-before", "cancel-callback", "cancel-persist", "callback-error", "audit", "audit-cancel-persist"} {
			t.Run(fmt.Sprintf("reload=%t/%s", reload, mode), func(t *testing.T) {
				f, issue := scaleIssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Desktop count closure"}}, nil)
				workspace := f.data.Workspace.URLKey
				at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
				metadata := desktopAckWriteMetadata(t, f, at)
				actor := ownedActor(f.data.Viewer)
				actor.Name = "SW134 live request actor"
				actor.Via = &domain.ActorBot{ID: "sw134-app", Name: "Exact desktop attribution", Type: domain.ActorBotTypeOAuthApplication}
				viewer := actor.ID
				if mode == "empty-viewer" {
					viewer = ""
				}
				type outcome struct {
					Business                   discussionWriterBusiness
					Metadata                   legacyActorWriteMetadata
					Indexes                    map[string][][]any
					Installed                  domain.Bootstrap
					InstalledVia               *domain.ActorBot
					Events, Webhooks, Realtime []any
					Saved                      []any
					Audit                      []domain.AuditLogEntry
					Calls                      int
					AccountKey, LastWorkspace  string
					AccountViewer              json.RawMessage
					ErrorType, ErrorText       string
				}
				var outputs []any
				var counts []int64
				for _, bounded := range []bool{false, true} {
					writeFullMetadata(t, f.SQLiteStore, workspace, cloneBootstrap(metadata))
					desktopAckWriteSeed(t, f, issue, at, mode, viewer)
					repo := scaleIssuePersistenceView(t, f, bounded, reload)
					if _, err := repo.db.ExecContext(t.Context(), `UPDATE account_state SET last_workspace_key=? WHERE id=1`, "sw134-before"); err != nil {
						t.Fatal(err)
					}
					repo.mu.Lock()
					repo.lastWorkspaceKey = "sw134-before"
					repo.mu.Unlock()
					before := businessRows(t, repo, workspace, issue.ID)
					beforeMetadata := metadataImage(t, repo.db, workspace)
					beforeIndexes := legacyDiscussionWriteIndexes(t, repo, workspace)
					prior := eventIDs(t, repo, workspace)
					ctx := ContextWithActor(t.Context(), actor)
					if bounded {
						ctx = ContextWithDesktopNotificationAcknowledgement(ctx)
					}
					var owner *MetadataJSONRead
					if bounded {
						var err error
						owner, err = repo.BeginMetadataJSONRead(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer owner.Close()
						ctx = owner.Context(ctx)
					}
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()
					if mode == "cancel-before" {
						cancel()
					}
					input := desktopAckWriteInput{NotificationIDs: []string{"selected"}}
					switch mode {
					case "nil-input":
						input.NotificationIDs = nil
					case "empty-input":
						input.NotificationIDs = []string{}
					case "empty-id":
						input.NotificationIDs = []string{""}
					case "duplicate-input":
						input.NotificationIDs = []string{"selected", "selected", "missing"}
					}
					got := outcome{}
					auditID := fmt.Sprintf("sw134-audit-%s-%t", f.data.Viewer.ID, bounded)
					var captured []*domain.Bootstrap
					repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						if bounded && repo.MetadataWorkUsedForTesting() == 0 {
							t.Error("desktop webhook escaped owned output")
						}
						got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
					})
					repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						if bounded && repo.MetadataWorkUsedForTesting() == 0 {
							t.Error("desktop realtime escaped owned output")
						}
						got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
					})
					var statements atomic.Int64
					var observing atomic.Bool
					observing.Store(true)
					persistCancelled := false
					mutationCtx := WithSQLStatementObserver(ctx, func(query string) {
						if observing.Load() {
							statements.Add(1)
							if strings.HasSuffix(mode, "cancel-persist") && strings.Contains(strings.ToLower(query), "insert into domain_events") {
								persistCancelled = true
								cancel()
							}
						}
					})
					err := repo.MutateWorkspace(mutationCtx, workspace, "notification.desktop_acknowledged", viewer, input, func(data *domain.Bootstrap) error {
						got.Calls++
						captured = append(captured, data)
						for index := range data.NotificationDeliveries {
							delivery := &data.NotificationDeliveries[index]
							if delivery.RecipientID == viewer && delivery.Channel == "desktop" && slices.Contains(input.NotificationIDs, delivery.NotificationID) {
								now := at.Add(time.Hour)
								delivery.Status, delivery.AcknowledgedAt, delivery.DeliveredAt, delivery.UpdatedAt = "delivered", &now, &now, now
								got.Saved = append(got.Saved, []any{delivery.ID, delivery.Status, delivery.AcknowledgedAt, delivery.DeliveredAt, delivery.UpdatedAt})
							}
						}
						if strings.HasPrefix(mode, "audit") {
							// The public MutateWorkspace audit wrapper removes
							// these staged rows before sparse validation and
							// flushes them in the mutation's own transaction.
							data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{
								ID: auditID, Actor: data.Viewer, Action: "sw134.desktop_acknowledged",
								ResourceType: "notification", ResourceID: viewer, CreatedAt: at.Add(time.Hour),
								Metadata: map[string]any{"request": "Exact staged desktop audit"},
							})
						}
						if mode == "canonical-merge" {
							peer := legacyActorWriteBegin(t, f)
							legacyMetadataWritePeer(t, peer, workspace, "actor-merged", metadata)
							if err := peer.Commit(); err != nil {
								t.Fatal(err)
							}
						}
						if mode == "retry" && got.Calls == 1 {
							return ErrConcurrentWrite
						}
						if mode == "cancel-callback" {
							cancel()
						}
						if mode == "callback-error" {
							return ErrIssueQuery
						}
						if mode == "noop" {
							return ErrNoMutation
						}
						return nil
					})
					observing.Store(false)
					counts = append(counts, statements.Load())
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					if err != nil {
						got.ErrorType, got.ErrorText = fmt.Sprintf("%T", err), err.Error()
					}
					failed := mode == "cancel-before" || mode == "cancel-callback" || strings.HasSuffix(mode, "cancel-persist") || mode == "callback-error"
					if failed && err == nil || !failed && err != nil {
						t.Fatal("desktop literal/bounded success changed", bounded, got.Calls, err)
					}
					if strings.HasSuffix(mode, "cancel-persist") && !persistCancelled {
						t.Fatal("desktop cancellation missed final event write")
					}
					if mode == "callback-error" && !errors.Is(err, ErrIssueQuery) || strings.Contains(mode, "cancel-") && !errors.Is(err, context.Canceled) {
						t.Fatal("desktop error identity changed", err)
					}
					got.Business = businessRows(t, repo, workspace, issue.ID)
					got.Metadata = metadataImage(t, repo.db, workspace)
					got.Indexes = legacyDiscussionWriteIndexes(t, repo, workspace)
					got.Installed = legacyDiscussionWriteInstalled(t, repo, workspace)
					got.InstalledVia = got.Installed.Viewer.Via
					if mode == "canonical-merge" {
						// A peer committed between the writer's installed
						// image and its transaction. The literal writer keeps
						// its copy (stored counters re-derived, the request
						// actor's attribution on the viewer) at an unknown
						// revision; the bounded writer reloads its image from
						// SQL after the commit, as a peer event's reload
						// would, so it must equal that reload instead.
						if bounded {
							reloadEqual(t, repo, workspace, got.Installed)
						}
						got.Installed, got.InstalledVia = domain.Bootstrap{}, nil
					}
					got.Events = newEvents(t, repo, workspace, prior)
					if strings.HasPrefix(mode, "audit") {
						entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, AuditQuery{Limit: 1000})
						if err != nil {
							t.Fatal(err)
						}
						for _, entry := range entries {
							if entry.ID == auditID {
								// IDs and chain position differ because both
								// arms append to one persistent audit chain.
								entry.ID, entry.Seq, entry.Hash = "", 0, ""
								got.Audit = append(got.Audit, entry)
							}
						}
						want := 1
						if failed {
							want = 0
						}
						if len(got.Audit) != want {
							t.Fatal("desktop audit commit/rollback changed", bounded, len(got.Audit), want)
						}
						chain, err := repo.VerifyAuditChain(t.Context(), f.data.Workspace.ID)
						if err != nil || !chain.Valid {
							t.Fatal("desktop ack broke audit chain", err, chain)
						}
					}
					if err := repo.db.QueryRowContext(t.Context(), `SELECT last_workspace_key,viewer FROM account_state WHERE id=1`).Scan(&got.AccountKey, &got.AccountViewer); err != nil {
						t.Fatal(err)
					}
					repo.mu.RLock()
					got.LastWorkspace = repo.lastWorkspaceKey
					repo.mu.RUnlock()
					wantCalls, wantEvents := 1, 1
					if mode == "retry" {
						wantCalls = 2
					}
					if mode == "cancel-before" {
						wantCalls = 0
					}
					if failed || mode == "noop" {
						wantEvents = 0
						if !reflect.DeepEqual(got.Business, before) || !reflect.DeepEqual(got.Metadata, beforeMetadata) || !reflect.DeepEqual(got.Indexes, beforeIndexes) {
							t.Fatal("failed/no-op desktop ack changed canonical state", bounded)
						}
						if got.LastWorkspace != "sw134-before" || got.AccountKey != "sw134-before" {
							t.Fatal("failed/no-op desktop ack changed last workspace/account")
						}
					} else if got.LastWorkspace != workspace || got.AccountKey != workspace {
						t.Fatal("desktop ack lost full-writer last workspace/account effects")
					}
					if got.Calls != wantCalls || len(got.Events) != wantEvents || len(got.Webhooks) != wantEvents || len(got.Realtime) != wantEvents {
						t.Fatal("desktop callback/publication count changed", bounded, got.Calls, len(got.Events), len(got.Webhooks), len(got.Realtime))
					}
					outputs = append(outputs, discussionWriterJSON(t, got))
					if bounded {
						for _, data := range captured {
							if repo.MetadataWorkUsedForTesting() == 0 || reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("desktop callback released before caller last use")
							}
						}
						owner.Close()
						owner.Close()
						for _, data := range captured {
							if !reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("desktop callback survived response release")
							}
						}
						if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("desktop writer leaked work or hydrated complete metadata")
						}
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("desktop acknowledgement differs", firstDiff(outputs[0], outputs[1], "desktop"))
				}
				t.Logf("reload=%t mode=%s writer_SQL_old=%d writer_SQL_bounded=%d", reload, mode, counts[0], counts[1])
			})
		}
	}
}

func TestDesktopAcknowledgementRequiresOptIn(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleIssuePersistenceFixture(t, nil, nil)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
	desktopAckWriteSeed(t, f, issue, at, "selected", f.data.Viewer.ID)
	repo := scaleIssuePersistenceView(t, f, true, false)
	owner, err := repo.BeginMetadataJSONRead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	before := businessRows(t, repo, workspace, issue.ID)
	calls := 0
	err = repo.MutateWorkspace(owner.Context(t.Context()), workspace, "notification.desktop_acknowledged", f.data.Viewer.ID, desktopAckWriteInput{}, func(*domain.Bootstrap) error {
		calls++
		return nil
	})
	if !errors.Is(err, ErrBoundedMetadataCoverage) || calls != 0 || !reflect.DeepEqual(before, businessRows(t, repo, workspace, issue.ID)) {
		t.Fatal("unmarked callback was routed to sparse desktop inputs", calls, err)
	}
}
