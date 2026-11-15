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

type scaleP21SW155Outcome struct {
	scaleP21SW149Outcome
	Updated   domain.Issue
	Timezones []string
	ScopeIDs  [][]string
}

func scaleP21SW155EventPreviousTiming(outcome any) map[string]any {
	events, _ := outcome.(map[string]any)["Events"].([]any)
	selected := map[string]any{}
	if len(events) == 0 {
		return selected
	}
	previous, _ := events[0].([]any)[3].(map[string]any)
	for _, field := range []string{"dueDate", "version", "updatedAt"} {
		if value, present := previous[field]; present {
			selected[field] = value
		}
	}
	return selected
}

func TestScaleP21SW155IssueRecurrenceTimingDifferential(t *testing.T) {
	scaleP21SW149Setup(t)
	for _, reload := range []bool{false, true} {
		for _, mode := range []string{
			"set-missing-due", "replace-due", "clear-due", "clear-next", "clock-only", "iana-next", "whitespace-dual-id",
			"noop", "retry", "cancel-before", "cancel-callback", "cancel-persist",
			"callback-error", "no-actor", "foreign-actor", "nested-aggregate", "stale-progress",
		} {
			t.Run(fmt.Sprint("reload=", reload, "/", mode), func(t *testing.T) {
				f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Recurrence timing progress"}}, nil)
				workspace := f.data.Workspace.URLKey
				at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
				tick := time.Date(2026, 11, 15, 23, 45, 12, 567890123, time.Local)
				issue.Recurrence, issue.DueDate, issue.NextOccurrenceAt = "daily", new("2026-11-15"), new(at)
				nextOccurrence := tick.Add(24 * time.Hour)
				if mode == "iana-next" {
					location, err := time.LoadLocation("Asia/Kathmandu")
					if err != nil {
						t.Fatal(err)
					}
					issue.NextOccurrenceAt = new(time.Date(2026, 11, 15, 0, 0, 0, 123456789, location))
					nextOccurrence = time.Date(2026, 11, 17, 0, 1, 0, 987654321, location)
				}
				if mode == "set-missing-due" {
					issue.DueDate = nil
				}
				if mode == "whitespace-dual-id" {
					issue.ID, issue.Identifier = " "+issue.ID+" ", "P21-155"
					if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw155_whitespace_seed", "", nil, func(data *domain.Bootstrap) error {
						data.Issues = append(data.Issues, issue)
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw155_timezone_seed", "", nil, func(data *domain.Bootstrap) error {
					if data.TeamSettings == nil {
						data.TeamSettings = map[string]domain.TeamSettings{}
					}
					settings := data.TeamSettings[issue.Team.ID]
					settings.TeamID, settings.Timezone = issue.Team.ID, "Asia/Kathmandu"
					data.TeamSettings[issue.Team.ID] = settings
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				metadata, found := f.WorkspaceMetadata(workspace)
				if !found {
					t.Fatal("recurrence reset metadata missing")
				}
				if metadata.UserSettings == nil {
					metadata.UserSettings = map[string]domain.UserSettings{}
				}
				if metadata.NotificationPreferences == nil {
					metadata.NotificationPreferences = map[string]domain.NotificationPreferences{}
				}
				for _, user := range metadata.Users {
					settings, preference := defaultUserSettings(user.ID), defaultNotificationPreferences(user.ID)
					settings.UpdatedAt, preference.UpdatedAt = at, at
					metadata.UserSettings[user.ID], metadata.NotificationPreferences[user.ID] = settings, preference
				}
				actor := scaleP21SW69OwnedActor(f.data.Viewer)
				actor.Name = "Recurring request actor"
				actor.Via = &domain.ActorBot{ID: "sw155-request-app", Name: "Recurring attribution", Type: domain.ActorBotTypeOAuthApplication}
				if mode == "foreign-actor" {
					actor.ID = "sw155-not-a-workspace-member"
				}
				var outcomes []any
				var queries []int64
				for _, bounded := range []bool{false, true} {
					writeFullMetadata(t, f.SQLiteStore, workspace, cloneBootstrap(metadata))
					scaleP21SW116Seed(t, f, issue, at, mode)
					repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
					repo.mu.Lock()
					repo.lastWorkspaceKey = ""
					repo.mu.Unlock()
					before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
					beforeMetadata := scaleP21SW76MetadataImage(t, repo.db, workspace)
					beforeIndexes := scaleP21SW98Indexes(t, repo, workspace)
					prior := scaleP21SW63EventIDs(t, repo, workspace)
					ctx := t.Context()
					if mode != "no-actor" {
						ctx = ContextWithActor(ctx, actor)
					}
					scopeIDs := []string{issue.ID}
					if trimmed := strings.TrimSpace(issue.ID); trimmed != issue.ID {
						scopeIDs = append(scopeIDs, trimmed)
					}
					ctx = ContextWithIssueRecurrenceTimingMutation(WithMutationScope(ctx, MutationScope{IssueIDs: scopeIDs}))
					var owner *MetadataJSONRead
					outerWork := 0
					if bounded {
						var err error
						owner, err = repo.BeginMetadataJSONRead(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer owner.Close()
						ctx = owner.Context(ctx)
						outerWork = repo.MetadataWorkUsedForTesting()
					}
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()
					if mode == "cancel-before" {
						cancel()
					}
					got := scaleP21SW155Outcome{}
					var captured []*domain.Bootstrap
					checkOwner := func() {
						t.Helper()
						if bounded && repo.MetadataWorkUsedForTesting() <= outerWork {
							t.Error("recurrence publication lost callback output ownership")
						}
					}
					repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						checkOwner()
						got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
					})
					repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						checkOwner()
						got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
					})
					var statements atomic.Int64
					var observing atomic.Bool
					observing.Store(true)
					persistCanceled := false
					mutationCtx := WithSQLStatementObserver(ctx, func(query string) {
						if observing.Load() {
							statements.Add(1)
							if mode == "cancel-persist" && strings.Contains(strings.ToLower(query), "insert into domain_events") {
								persistCanceled = true
								cancel()
							}
						}
					})
					payload := map[string]string{"automation": "recurring"}
					err := repo.MutateWorkspace(mutationCtx, workspace, "issue.updated", issue.ID, payload, func(data *domain.Bootstrap) error {
						got.Calls++
						captured = append(captured, data)
						selected := make([]string, len(data.Issues))
						for index, value := range data.Issues {
							selected[index] = value.ID
						}
						got.ScopeIDs = append(got.ScopeIDs, selected)
						if mode == "noop" {
							return ErrNoMutation
						}
						index := slices.IndexFunc(data.Issues, func(value domain.Issue) bool { return value.ID == issue.ID })
						if index < 0 {
							t.Fatal("recurrence callback lost target")
						}
						target := &data.Issues[index]
						switch mode {
						case "clear-due":
							target.DueDate = nil
						case "clear-next":
							target.NextOccurrenceAt = nil
						case "clock-only":
						default:
							target.DueDate, target.NextOccurrenceAt = new("2026-11-17"), new(nextOccurrence)
						}
						target.Version++
						target.UpdatedAt = tick
						got.Updated = *target
						got.Callback = append(got.Callback, scaleP21SW116Capture{
							Viewer: data.Viewer, Via: data.Viewer.Via, Role: data.ViewerRole, Saved: *target, BeforeLabels: slices.Clone(target.Labels),
						})
						got.Timezones = append(got.Timezones, data.TeamSettings[target.Team.ID].Timezone)
						switch mode {
						case "retry":
							if got.Calls == 1 {
								return ErrConcurrentWrite
							}
						case "cancel-callback":
							cancel()
						case "callback-error":
							return ErrIssueQuery
						}
						return nil
					})
					observing.Store(false)
					queries = append(queries, statements.Load())
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					if err != nil {
						got.ErrorType, got.ErrorText = fmt.Sprintf("%T", err), err.Error()
					}
					failed := slices.Contains([]string{"cancel-before", "cancel-callback", "cancel-persist", "callback-error"}, mode)
					if failed && err == nil || !failed && err != nil {
						t.Fatal("recurrence success/error changed", bounded, mode, got.Calls, err)
					}
					if mode == "callback-error" && !errors.Is(err, ErrIssueQuery) ||
						(mode == "cancel-callback" || mode == "cancel-persist") && !errors.Is(err, context.Canceled) {
						t.Fatal("recurrence error contract changed", mode, err)
					}
					if mode == "cancel-persist" && !persistCanceled {
						t.Fatal("recurrence cancellation never reached event persistence")
					}
					got.Business = scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
					got.Metadata = scaleP21SW76MetadataImage(t, repo.db, workspace)
					got.Indexes = scaleP21SW98Indexes(t, repo, workspace)
					got.Installed = scaleP21SW98Installed(t, repo, workspace)
					got.InstalledVia = got.Installed.Viewer.Via
					got.Events = scaleP21SW63NewEvents(t, repo, workspace, prior)
					if err := repo.db.QueryRowContext(t.Context(), `SELECT last_workspace_key,viewer FROM account_state WHERE id=1`).Scan(&got.AccountKey, &got.AccountViewer); err != nil {
						t.Fatal(err)
					}
					repo.mu.RLock()
					got.LastWorkspace = repo.lastWorkspaceKey
					repo.mu.RUnlock()
					wantEvents, wantCalls := 1, 1
					if failed || mode == "noop" {
						wantEvents = 0
						if !reflect.DeepEqual(got.Business, before) || !reflect.DeepEqual(got.Metadata, beforeMetadata) || !reflect.DeepEqual(got.Indexes, beforeIndexes) {
							t.Fatal("failed/no-op recurrence changed persistent state")
						}
						got.Updated = domain.Issue{}
						for index := range got.Callback {
							got.Callback[index].Saved.Labels = slices.Clone(got.Callback[index].BeforeLabels)
						}
					}
					if mode == "cancel-before" {
						wantCalls = 0
					} else if mode == "retry" {
						wantCalls = 2
					}
					wantLastWorkspace := workspace
					if wantEvents == 0 {
						wantLastWorkspace = ""
					}
					if got.Calls != wantCalls || len(got.Events) != wantEvents || len(got.Webhooks) != wantEvents || len(got.Realtime) != wantEvents || got.LastWorkspace != wantLastWorkspace {
						t.Fatal("recurrence callback/publication/scoped effect count changed", got.Calls, len(got.Events), len(got.Webhooks), len(got.Realtime), got.LastWorkspace)
					}
					if wantEvents != 0 {
						if got.Updated.Version != issue.Version+1 || !got.Updated.UpdatedAt.Equal(tick) ||
							got.Updated.UpdatedAt.Format(time.RFC3339Nano) != tick.Format(time.RFC3339Nano) {
							t.Fatal("recurrence writer replaced callback version or captured tick clock")
						}
						if got.Timezones[len(got.Timezones)-1] != "Asia/Kathmandu" {
							t.Fatal("recurrence callback lost affected-team timezone", bounded, got.Timezones)
						}
						event := got.Events[0].([]any)
						raw, _ := json.Marshal(payload)
						if event[0] != "issue.updated" || event[1] != issue.ID || string(event[2].(json.RawMessage)) != string(raw) {
							t.Fatal("recurrence event/payload changed")
						}
					}
					if bounded && got.Calls != 0 {
						checkOwner()
						for _, data := range captured {
							if reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("recurrence callback cleared before caller last use")
							}
						}
						if err := owner.ReserveEncoding(t.Context(), []any{got.Updated, got.Callback}); err != nil {
							t.Fatal(err)
						}
					}
					outcomes = append(outcomes, scaleP21SW63JSON(t, got))
					if bounded {
						owner.Close()
						owner.Close()
						for _, data := range captured {
							if !reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("recurrence callback survived output release")
							}
						}
						if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("recurrence writer leaked work or hydrated full metadata")
						}
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					oldTiming, _ := json.Marshal(scaleP21SW155EventPreviousTiming(outcomes[0]))
					boundedTiming, _ := json.Marshal(scaleP21SW155EventPreviousTiming(outcomes[1]))
					t.Logf("event_previous_timing legacy=%s bounded=%s", oldTiming, boundedTiming)
					t.Fatal("literal/bounded recurrence differs", scaleP21SW98FirstDiff(outcomes[0], outcomes[1], "recurrence"))
				}
				t.Logf("reload=%t mode=%s legacy_SQL=%d bounded_SQL=%d", reload, mode, queries[0], queries[1])
			})
		}
	}
}

func TestScaleP21SW155IssueRecurrenceTimingQuotaAndProfile(t *testing.T) {
	scaleP21SW149Setup(t)
	f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Recurrence timing profile"}}, nil)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
	scaleP21SW116Seed(t, f, issue, at, "nil-map")
	repo := scaleP21IssuePersistenceView(t, f, true, false)
	for _, mode := range []string{
		"quota", "unowned", "missing-marker", "unscoped", "wrong-event", "title", "recurrence",
		"reactions", "attachments", "description", "description-state", "document-content",
		"activity", "comments", "notifications", "deliveries", "actor", "actor-via", "viewer-role",
		"workspace", "policy", "issue-scope",
	} {
		t.Run(mode, func(t *testing.T) {
			before, metadata, indexes := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID),
				scaleP21SW76MetadataImage(t, repo.db, workspace), scaleP21SW98Indexes(t, repo, workspace)
			actor := scaleP21SW69OwnedActor(f.data.Viewer)
			actor.Via = &domain.ActorBot{ID: "sw155-profile-app", Name: "Original attribution", Type: domain.ActorBotTypeOAuthApplication}
			ctx := ContextWithActor(t.Context(), actor)
			if mode != "unscoped" {
				ctx = WithMutationScope(ctx, MutationScope{IssueIDs: []string{issue.ID}})
			}
			if mode != "missing-marker" {
				ctx = ContextWithIssueRecurrenceTimingMutation(ctx)
			}
			var owner *MetadataJSONRead
			if mode != "unowned" {
				var err error
				owner, err = repo.BeginMetadataJSONRead(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer owner.Close()
				ctx = owner.Context(ctx)
			}
			limit := repo.metadataResidency.work.limit
			if mode == "quota" {
				repo.metadataResidency.work.limit = repo.MetadataWorkUsedForTesting()
			}
			event := "issue.updated"
			if mode == "wrong-event" {
				event = "issue.created"
			}
			calls := 0
			err := repo.MutateWorkspace(ctx, workspace, event, issue.ID, map[string]string{"automation": "recurring"}, func(data *domain.Bootstrap) error {
				calls++
				index := slices.IndexFunc(data.Issues, func(value domain.Issue) bool { return value.ID == issue.ID })
				if index < 0 {
					t.Fatal("profile callback lost target")
				}
				target := &data.Issues[index]
				switch mode {
				case "title":
					target.Title = "Outside timing profile"
				case "recurrence":
					target.Recurrence = "weekly"
				case "reactions":
					target.Reactions = map[string][]string{"like": {actor.ID}}
				case "attachments":
					target.Attachments = append(target.Attachments, domain.Attachment{URL: "https://example.test/outside"})
				case "description":
					target.Description = "Outside timing profile"
				case "description-state":
					target.DescriptionState = "Outside timing profile"
				case "document-content":
					target.DocumentContent = &domain.DocumentContent{ID: "sw155-outside"}
				case "activity":
					data.Activities[issue.ID] = append(data.Activities[issue.ID], domain.ActivityEvent{ID: "sw155-outside", Type: "issue.updated", Actor: actor, CreatedAt: at})
				case "comments":
					data.Comments[issue.ID] = append(data.Comments[issue.ID], domain.Comment{ID: "sw155-outside", Body: "Outside timing profile"})
				case "notifications":
					data.Notifications = append(data.Notifications, domain.Notification{ID: "sw155-outside", RecipientID: actor.ID})
				case "deliveries":
					data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "sw155-outside"})
				case "actor":
					data.Viewer.Name = "Changed attribution"
				case "actor-via":
					data.Viewer.Via.Name = "Changed attribution"
				case "viewer-role":
					data.ViewerRole = "guest"
				case "workspace":
					data.Workspace.Name = "Outside timing profile"
				case "policy":
					data.WorkspaceSettings.PreventGuestAgents = !data.WorkspaceSettings.PreventGuestAgents
				case "issue-scope":
					data.Issues = append(data.Issues, domain.Issue{ID: "sw155-outside"})
				}
				return nil
			})
			repo.metadataResidency.work.limit = limit
			expected := ErrBoundedMetadataCoverage
			if mode == "quota" {
				expected = ErrMetadataWorkBudget
			}
			if !errors.Is(err, expected) {
				t.Fatal("recurrence quota/profile boundary changed", calls, err)
			}
			if slices.Contains([]string{"quota", "unowned", "missing-marker", "unscoped", "wrong-event"}, mode) && calls != 0 {
				t.Fatal("undeclared recurrence callback ran", mode, calls)
			}
			if !reflect.DeepEqual(before, scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)) ||
				!reflect.DeepEqual(metadata, scaleP21SW76MetadataImage(t, repo.db, workspace)) ||
				!reflect.DeepEqual(indexes, scaleP21SW98Indexes(t, repo, workspace)) {
				t.Fatal("rejected recurrence callback persisted effects")
			}
			if owner != nil {
				owner.Close()
			}
			if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("rejected recurrence callback retained work or decoded whole metadata")
			}
		})
	}
}
