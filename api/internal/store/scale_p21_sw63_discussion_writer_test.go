package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW63Business struct {
	IssueVersion int64
	Issue        json.RawMessage
	Content      []any
	Threads      scaleP21IssuePhysicalField
}

func scaleP21SW63BusinessRows(t *testing.T, repo *SQLiteStore, workspace, issue string) scaleP21SW63Business {
	t.Helper()
	var result scaleP21SW63Business
	var raw []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT version,data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issue).Scan(&result.IssueVersion, &raw); err != nil {
		t.Fatal(err)
	}
	result.Issue = json.RawMessage(raw)
	rows, err := repo.db.QueryContext(t.Context(), `SELECT kind,resource_id,id,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,id`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind, resource, id string
		var raw []byte
		if err := rows.Scan(&kind, &resource, &id, &raw); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		result.Content = append(result.Content, []any{kind, resource, id, json.RawMessage(raw)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	result.Threads = scaleP21IssuePhysical(t, repo, workspace, "threadSubscriptions")
	return result
}

func scaleP21SW63EventIDs(t *testing.T, repo *SQLiteStore, workspace string) map[string]bool {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT id FROM domain_events WHERE workspace_id=(SELECT workspace_id FROM workspace_states WHERE workspace_key=?)`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		result[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW63NewEvents(t *testing.T, repo *SQLiteStore, workspace string, before map[string]bool) []any {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT id,event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=(SELECT workspace_id FROM workspace_states WHERE workspace_key=?) ORDER BY created_at,id`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []any
	for rows.Next() {
		var id, event, aggregate string
		var payload []byte
		var previous sql.NullString
		if err := rows.Scan(&id, &event, &aggregate, &payload, &previous); err != nil {
			t.Fatal(err)
		}
		if before[id] {
			continue
		}
		var prior json.RawMessage
		if previous.Valid && previous.String != "" {
			prior = json.RawMessage(previous.String)
		}
		// Event IDs and commit clocks are incidental; all business IDs and
		// clocks below are fixed fixture values and compared exactly.
		result = append(result, []any{event, aggregate, json.RawMessage(payload), prior})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW63JSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW63Seed(t *testing.T, f scaleP09Fixture, issue domain.Issue, at time.Time) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	f.SetWebhookSink(nil)
	f.SetRealtimeSink(nil)
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw63_fixture", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Issues {
			if data.Issues[index].ID == issue.ID {
				data.Issues[index] = issue
			}
		}
		root := issue.ID + "-root"
		data.Comments[issue.ID] = []domain.Comment{
			{ID: root, Version: 3, Body: "Before exact root", BodyData: map[string]any{"native": int64(7)}, User: data.Viewer, CreatedAt: at, Reactions: map[string][]string{}},
			{ID: issue.ID + "-reply", Version: 2, Body: "Untouched reply", ParentID: &root, User: data.Viewer, CreatedAt: at.Add(time.Second), Reactions: nil},
		}
		data.Activities[issue.ID] = []domain.ActivityEvent{}
		data.ThreadSubscriptions = []domain.ThreadSubscription{
			{ID: issue.ID + "-watch", UserID: data.Viewer.ID, IssueID: issue.ID, CommentID: root, State: "subscribed", CreatedAt: at, UpdatedAt: at},
			{ID: issue.ID + "-unrelated-watch", UserID: data.Viewer.ID, IssueID: issue.ID + "-unrelated", CommentID: "other", State: "muted", CreatedAt: at, UpdatedAt: at},
		}
		notification := issue.ID + "-notification"
		data.Notifications = []domain.Notification{{ID: notification, RecipientID: data.Viewer.ID, IssueID: issue.ID, CommentID: root,
			Actor: data.Viewer, Category: "comments", Type: "comment", SourceType: "comment", SourceID: root,
			GroupKey: "exact-group", OccurrenceCount: 2, LatestActorIDs: []string{data.Viewer.ID}, CreatedAt: at, UpdatedAt: at}}
		data.NotificationDeliveries = []domain.NotificationDelivery{{ID: "delivery_" + notification + "_chat", NotificationID: notification,
			RecipientID: data.Viewer.ID, Channel: "chat", Status: "pending", CreatedAt: at, UpdatedAt: at}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW63DiscussionWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, reload := range []bool{false, true} {
		for _, mode := range []string{"changed", "deleted", "noop", "retry"} {
			t.Run(fmt.Sprintf("reload=%t/%s", reload, mode), func(t *testing.T) {
				f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Selected project"}}, nil)
				workspace, at := f.data.Workspace.URLKey, time.Now().UTC().Add(-time.Minute)
				actor := f.data.Viewer
				actor.Via = &domain.ActorBot{ID: "sw63-via", Type: domain.ActorBotTypeOAuthApplication, Name: "Exact writer attribution"}
				type outcome struct {
					Rows                       scaleP21SW63Business
					Captures                   []json.RawMessage
					Events, Webhooks, Realtime []any
					Calls                      int
				}
				var outputs []any
				for _, bounded := range []bool{false, true} {
					scaleP21SW63Seed(t, f, issue, at)
					repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
					before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
					prior := scaleP21SW63EventIDs(t, repo, workspace)
					var closes []func()
					closeOutput := func() {
						for _, close := range closes {
							close()
							close()
						}
					}
					t.Cleanup(closeOutput)
					ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
						closes = append(closes, close)
						return true
					})
					ctx = ContextWithActor(WithIssueDiscussionMutation(ctx, issue.ID, issue.ID+"-root"), actor)
					var captured []*domain.Bootstrap
					type callbackResult struct {
						Comment *domain.Comment
						Viewer  domain.User
						Via     *domain.ActorBot
						Role    string
					}
					var pending []callbackResult
					got := outcome{}
					repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						if bounded && repo.MetadataWorkUsedForTesting() == 0 {
							t.Error("writer webhook escaped output ownership")
						}
						raw, err := json.Marshal([]any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
						if err != nil {
							t.Fatal(err)
						}
						got.Webhooks = append(got.Webhooks, json.RawMessage(raw))
					})
					primaryRealtime := 0
					captureRealtime := func(_ string, event domain.RealtimeEvent) {
						if bounded && repo.MetadataWorkUsedForTesting() == 0 {
							t.Error("writer realtime output escaped ownership")
						}
						if event.Type == "comment.updated" || event.Type == "comment.deleted" {
							primaryRealtime++
						}
						raw, err := json.Marshal([]any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
						if err != nil {
							t.Fatal(err)
						}
						got.Realtime = append(got.Realtime, json.RawMessage(raw))
					}
					repo.SetRealtimeSink(captureRealtime)
					if repo != f.SQLiteStore {
						// The shared transaction's notification signals use the
						// original SQL store; the primary event uses the writer.
						f.SetRealtimeSink(captureRealtime)
					}
					event := "comment.updated"
					if mode == "deleted" {
						event = "comment.deleted"
					}
					payload := map[string]any{"commentId": issue.ID + "-root", "body": "After exact root"}
					callback := func(data *domain.Bootstrap) (string, error) {
						got.Calls++
						if data.Viewer.ID != actor.ID || !reflect.DeepEqual(data.Viewer.Via, actor.Via) {
							t.Fatal("writer replaced live request attribution")
						}
						captured = append(captured, data)
						captureResult := func() {
							result := callbackResult{Viewer: data.Viewer, Via: data.Viewer.Via, Role: data.ViewerRole}
							if mode != "deleted" {
								comment := data.Comments[issue.ID][0]
								result.Comment = &comment
							}
							pending = append(pending, result)
						}
						if mode == "noop" {
							captureResult()
							return issue.ID, ErrNoMutation
						}
						if mode == "deleted" {
							data.Comments[issue.ID] = slices.DeleteFunc(data.Comments[issue.ID], func(comment domain.Comment) bool {
								return comment.ID == issue.ID+"-root" || comment.ParentID != nil && *comment.ParentID == issue.ID+"-root"
							})
							data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(subscription domain.ThreadSubscription) bool {
								return subscription.IssueID == issue.ID && subscription.CommentID == issue.ID+"-root"
							})
						} else {
							comment := &data.Comments[issue.ID][0]
							comment.Body, comment.Version, comment.EditedAt = "After exact root", comment.Version+1, new(at.Add(3*time.Second))
							data.Notifications[0].OccurrenceCount++
							data.Notifications[0].UpdatedAt = at.Add(3 * time.Second)
							data.NotificationDeliveries[0].Status, data.NotificationDeliveries[0].UpdatedAt = "pending-disabled", at.Add(3*time.Second)
						}
						data.Activities[issue.ID] = append(data.Activities[issue.ID], domain.ActivityEvent{
							ID: issue.ID + "-activity", Type: event, Actor: data.Viewer, CreatedAt: at.Add(3 * time.Second),
							Metadata: domain.ActivityMetadata{"commentId": issue.ID + "-root"},
						})
						captureResult()
						if mode == "retry" && got.Calls == 1 {
							return issue.ID, ErrConcurrentWrite
						}
						return issue.ID, nil
					}
					var err error
					if bounded {
						err = repo.mutateBoundedIssueDiscussion(ctx, workspace, event, payload, callback)
					} else {
						restore := ForceFullMutationsForTesting(true)
						err = repo.mutateIssueScope(ctx, workspace, event, payload, callback)
						restore()
					}
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					if repo != f.SQLiteStore {
						f.SetRealtimeSink(nil)
					}
					if err != nil {
						t.Fatal("actual discussion writer", bounded, mode, err)
					}
					wantCalls, wantEvents := 1, 1
					if mode == "retry" {
						wantCalls = 2
					}
					if mode == "noop" {
						wantEvents = 0
					}
					wantSignals := 0
					if mode == "changed" || mode == "retry" {
						wantSignals = 1
					}
					if got.Calls != wantCalls || len(got.Webhooks) != wantEvents || primaryRealtime != wantEvents || len(got.Realtime) != wantEvents+wantSignals {
						t.Fatal("writer callback/publication count changed", got.Calls, len(got.Webhooks), len(got.Realtime))
					}
					if bounded && (len(closes) != wantCalls || repo.MetadataWorkUsedForTesting() == 0) {
						t.Fatal("attempt outputs were released before their containing response", len(closes), wantCalls)
					}
					for _, data := range captured {
						if bounded && reflect.DeepEqual(*data, domain.Bootstrap{}) {
							t.Fatal("writer cleared a captured attempt before output close")
						}
					}
					for _, result := range pending {
						raw, err := json.Marshal(result)
						if err != nil {
							t.Fatal(err)
						}
						got.Captures = append(got.Captures, raw)
					}
					got.Rows = scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
					got.Events = scaleP21SW63NewEvents(t, repo, workspace, prior)
					if len(got.Events) != wantEvents || mode == "noop" && !reflect.DeepEqual(before, got.Rows) {
						t.Fatal("writer no-op or durable event semantics changed")
					}
					outputs = append(outputs, scaleP21SW63JSON(t, got))
					closeOutput()
					if bounded {
						for _, data := range captured {
							if !reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("closed writer attempt retained borrowed callback data")
							}
						}
						if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("actual discussion writer leaked work or decoded full metadata")
						}
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("literal/bounded discussion writer business/output differential", outputs)
				}
			})
		}
	}
}

func TestScaleP21SW63DiscussionWriterRollbackAndOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, mode := range []string{"missing-owner", "canceled-before", "canceled-callback", "callback-error"} {
		t.Run(mode, func(t *testing.T) {
			f, issue := scaleP21IssuePersistenceFixture(t, nil, nil)
			workspace, at := f.data.Workspace.URLKey, time.Now().UTC().Add(-time.Minute)
			scaleP21SW63Seed(t, f, issue, at)
			repo := scaleP21IssuePersistenceView(t, f, true, false)
			before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
			prior := scaleP21SW63EventIDs(t, repo, workspace)
			source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			var closes []func()
			closeOutput := func() {
				for _, close := range closes {
					close()
					close()
				}
			}
			t.Cleanup(closeOutput)
			ctx := t.Context()
			if mode != "missing-owner" {
				ctx = WithMetadataOutputOwner(ctx, func(close func()) bool {
					closes = append(closes, close)
					return true
				})
			}
			ctx, cancel := context.WithCancel(ContextWithActor(WithIssueDiscussionMutation(ctx, issue.ID, issue.ID+"-root"), f.data.Viewer))
			defer cancel()
			if mode == "canceled-before" {
				cancel()
			}
			calls, publications := 0, 0
			var captured *domain.Bootstrap
			repo.SetWebhookSink(func(string, domain.DomainEvent) { publications++ })
			repo.SetRealtimeSink(func(string, domain.RealtimeEvent) { publications++ })
			err = repo.mutateBoundedIssueDiscussion(ctx, workspace, "comment.updated", map[string]string{"body": "Rollback"}, func(data *domain.Bootstrap) (string, error) {
				calls++
				captured = data
				data.Comments[issue.ID][0].Body = "Must not commit"
				data.ThreadSubscriptions = nil
				if mode == "canceled-callback" {
					cancel()
					return issue.ID, nil
				}
				return issue.ID, ErrIssueQuery
			})
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			want := ErrIssueQuery
			if mode == "missing-owner" {
				want = ErrBoundedMetadataCoverage
			} else if mode == "canceled-before" || mode == "canceled-callback" {
				want = context.Canceled
			}
			wantCalls := 1
			if mode == "missing-owner" || mode == "canceled-before" {
				wantCalls = 0
			}
			if !errors.Is(err, want) || calls != wantCalls || publications != 0 ||
				!reflect.DeepEqual(before, scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)) ||
				len(scaleP21SW63NewEvents(t, repo, workspace, prior)) != 0 {
				t.Fatal("failed writer changed callback/SQL/publication semantics", err, calls, publications)
			}
			current, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			same := current == source
			current.Release()
			if !same {
				t.Fatal("failed writer installed an uncommitted source")
			}
			if wantCalls != 0 && (len(closes) != 1 || repo.MetadataWorkUsedForTesting() == 0 || captured == nil) {
				t.Fatal("failed callback output was released before its containing response")
			}
			if wantCalls != 0 && (len(captured.Comments[issue.ID]) == 0 || captured.Comments[issue.ID][0].Body != "Must not commit") {
				t.Fatal("failed callback's still-owned result was cleared before output close")
			}
			closeOutput()
			if captured != nil && !reflect.DeepEqual(*captured, domain.Bootstrap{}) ||
				repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("failed writer output close leaked or retained callback data")
			}
		})
	}
}
