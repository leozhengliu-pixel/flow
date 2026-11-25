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

type scaleP21SW149Case struct {
	name, event, operation, mode string
	scoped                       bool
}

type scaleP21SW149Outcome struct {
	Business                   scaleP21SW63Business
	Metadata                   scaleP21SW76Metadata
	Indexes                    map[string][][]any
	Installed                  domain.Bootstrap
	InstalledVia               *domain.ActorBot
	Events, Webhooks, Realtime []any
	Callback                   []scaleP21SW116Capture
	Saved                      domain.Attachment
	AccountKey, LastWorkspace  string
	AccountViewer              json.RawMessage
	ErrorType, ErrorText       string
	Calls                      int
}

func scaleP21SW149Setup(t *testing.T) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	if fullMutationsForced.Load() {
		t.Fatal("attachment differential requires ordinary literal dispatch")
	}
}

func scaleP21SW149Issue(issue domain.Issue, at time.Time, mode string) domain.Issue {
	issue.Description, issue.DescriptionState = "Before markdown\n", "old-rich-text"
	issue.DocumentContent = &domain.DocumentContent{
		ID: "sw149-before-generation", Version: 7, Content: "Before markdown\n",
		ContentState: "old-content-state", ContentData: map[string]any{"text": "before"}, UpdatedAt: at,
	}
	issue.Attachments = []domain.Attachment{
		{ID: "sw149-existing", IssueID: issue.ID, Title: "First existing", URL: "https://example.test/existing",
			ContentType: "text/uri-list", CreatedAt: at, Metadata: map[string]any{"native": int64(9007199254740993)}},
		{ID: "sw149-untouched", IssueID: issue.ID, Title: "Untouched", URL: "https://example.test/untouched",
			ContentType: "text/uri-list", CreatedAt: at, Metadata: map[string]any{}},
	}
	switch mode {
	case "nil-attachments":
		issue.Attachments = nil
	case "empty-attachments":
		issue.Attachments = []domain.Attachment{}
	case "empty-attachment-id":
		issue.Attachments[0].ID = ""
	case "duplicate-attachment-id":
		issue.Attachments[1].ID = issue.Attachments[0].ID
	case "duplicate-url":
		issue.Attachments[1].URL = issue.Attachments[0].URL
	case "native-attachment-id":
		issue.Attachments[0].ID = " Native/attachment:Id "
	}
	return issue
}

func scaleP21SW149Seed(t *testing.T, f scaleP09Fixture, issue domain.Issue, at time.Time, mode string) {
	t.Helper()
	seedMode := "nil-map"
	switch mode {
	case "nested-aggregate", "stale-progress", "native-duplicates":
		seedMode = mode
	}
	if mode == "physical-activity" {
		// Both halves start from the same owned workspace; a prior literal
		// attempt may have rewritten decoded IDs into different physical keys.
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='activity'`, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
	}
	scaleP21SW116Seed(t, f, issue, at, seedMode)
	if mode != "physical-activity" {
		return
	}
	for index, decodedID := range []string{"", "duplicate", "duplicate"} {
		value := domain.ActivityEvent{ID: decodedID, Type: "attachment.created", Actor: f.data.Viewer,
			CreatedAt: at, Metadata: domain.ActivityMetadata{"title": fmt.Sprint("physical-", index)}}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data) VALUES(?,'activity',?,?,?,?)`,
			f.data.Workspace.URLKey, issue.ID, fmt.Sprint("sw149-physical-", index), at.Format(time.RFC3339Nano), raw); err != nil {
			t.Fatal(err)
		}
	}
	// A nil nonresident family makes both literal dispatchers obtain their
	// existing physical SQL rows rather than an authoritative warm empty map.
	f.mu.Lock()
	data := f.workspaces[f.data.Workspace.URLKey]
	data.Activities = nil
	f.workspaces[f.data.Workspace.URLKey] = data
	f.mu.Unlock()
}

func scaleP21SW149Mutate(data *domain.Bootstrap, issueID string, test scaleP21SW149Case, at time.Time) (domain.Attachment, error) {
	index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == issueID })
	if index < 0 {
		return domain.Attachment{}, ErrIssueQuery
	}
	target := &data.Issues[index]
	now := at.Add(time.Hour)
	if test.operation == "update" || test.operation == "upsert" {
		id := "sw149-existing"
		switch test.mode {
		case "empty-attachment-id":
			id = ""
		case "native-attachment-id":
			id = " Native/attachment:Id "
		}
		index := slices.IndexFunc(target.Attachments, func(item domain.Attachment) bool {
			if test.operation == "upsert" {
				return item.URL == "https://example.test/existing"
			}
			return item.ID == id
		})
		if index < 0 {
			return domain.Attachment{}, ErrIssueQuery
		}
		target.Attachments[index].Title = "Changed attachment"
		target.Attachments[index].Subtitle = "Exact metadata update"
		target.Attachments[index].IconURL = "https://example.test/icon.png"
		target.Attachments[index].Metadata = map[string]any{"text": "updated", "native": int64(9007199254740993), "empty": []string{}, "nil": []string(nil)}
		target.Attachments[index].UpdatedAt = &now
		return target.Attachments[index], nil
	}
	contentType := "text/uri-list"
	if test.operation == "file" || test.operation == "embed" {
		contentType = "image/png"
	}
	attachment := domain.Attachment{
		ID: "sw149-created", IssueID: target.ID, Title: "Evidence", URL: "https://example.test/evidence.png",
		ContentType: contentType, Size: 12345, CreatedAt: now, Creator: data.Viewer,
		Metadata: map[string]any{"integer": int64(9007199254740993)},
	}
	if strings.HasPrefix(test.mode, "upsert-event-") || test.name == "scoped-link" {
		attachment.UpdatedAt = &now
	}
	if data.Viewer.Via != nil {
		actor := *data.Viewer.Via
		attachment.BotActor = &actor
	}
	target.Attachments = append(target.Attachments, attachment)
	activityType := "issue.link_created"
	if test.operation == "file" || test.operation == "embed" {
		activityType = "attachment.created"
	}
	RecordIssueActivity(data, target.ID, domain.ActivityEvent{
		ID: "sw149-activity", Type: activityType, Actor: data.Viewer, BotActor: attachment.BotActor, CreatedAt: now,
		Metadata: domain.ActivityMetadata{"attachmentId": attachment.ID, "title": attachment.Title, "url": attachment.URL,
			"linkTitle": attachment.Title, "linkUrl": attachment.URL, "linkSource": "url"},
	})
	if test.operation == "embed" {
		// applyIssueAttachmentEmbed's markdown-only applyUpdate result. Its
		// generated collaboration ID and wall clock are fixed for the oracle.
		target.Description = strings.TrimRight(target.Description, "\n") + "\n\n![Evidence](" + attachment.URL + ")\n"
		target.DescriptionState = ""
		version := int64(1)
		if target.DocumentContent != nil {
			version = target.DocumentContent.Version + 1
		}
		target.DocumentContent = &domain.DocumentContent{
			ID: "sw149-next-generation", Version: version, Content: target.Description,
			ContentState: "", ContentData: nil, UpdatedAt: now,
		}
	}
	return attachment, nil
}

func TestScaleP21SW149IssueAttachmentWriterDifferential(t *testing.T) {
	scaleP21SW149Setup(t)
	cases := []scaleP21SW149Case{
		{"file-nil", "attachment.created", "file", "nil-attachments", true},
		{"file-empty", "attachment.created", "file", "empty-attachments", true},
		{"embed", "attachment.created", "embed", "", true},
		{"scoped-link", "attachment.created", "link", "", true},
		{"update", "attachment.updated", "update", "", true},
		{"update-empty-id", "attachment.updated", "update", "empty-attachment-id", true},
		{"update-native-id", "attachment.updated", "update", "native-attachment-id", true},
		{"update-duplicate-id", "attachment.updated", "update", "duplicate-attachment-id", true},
		{"upsert-first-url", "attachment.updated", "upsert", "duplicate-url", true},
		{"stale-updated-create", "attachment.updated", "link", "upsert-event-updated-create", true},
		{"stale-created-update", "attachment.created", "upsert", "upsert-event-created-update", true},
		{"legacy-link", "issue.link_created", "link", "", false},
	}
	for _, scoped := range []bool{false, true} {
		for _, mode := range []string{"nested-aggregate", "stale-progress", "native-duplicates", "physical-activity", "retry", "noop", "cancel-before", "cancel-callback", "cancel-persist", "callback-error", "no-actor", "foreign-actor"} {
			event, operation := "issue.link_created", "link"
			if scoped {
				event, operation = "attachment.created", "file"
			}
			cases = append(cases, scaleP21SW149Case{fmt.Sprint(scoped, "/", mode), event, operation, mode, scoped})
		}
	}
	for _, reload := range []bool{false, true} {
		for _, test := range cases {
			if reload && test.mode == "native-duplicates" {
				continue
			}
			t.Run(fmt.Sprint("reload=", reload, "/", test.name), func(t *testing.T) {
				f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Attachment progress"}}, nil)
				workspace := f.data.Workspace.URLKey
				at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
				issue = scaleP21SW149Issue(issue, at, test.mode)
				metadata, found := f.WorkspaceMetadata(workspace)
				if !found {
					t.Fatal("attachment reset metadata missing")
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
				actor.Name = "Attachment request actor"
				actor.Via = &domain.ActorBot{ID: "sw149-request-app", Name: "Attachment attribution", Type: domain.ActorBotTypeOAuthApplication}
				if test.mode == "foreign-actor" {
					actor.ID = "sw149-not-a-workspace-member"
				}
				var outcomes []any
				var beforeContent, afterContent [][]string
				var queries []int64
				for _, bounded := range []bool{false, true} {
					writeFullMetadata(t, f.SQLiteStore, workspace, cloneBootstrap(metadata))
					scaleP21SW149Seed(t, f, issue, at, test.mode)
					repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
					repo.mu.Lock()
					repo.lastWorkspaceKey = ""
					repo.mu.Unlock()
					before := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
					beforeContent = append(beforeContent, scaleP21SW149ContentKeys(before.Content))
					beforeMetadata := scaleP21SW76MetadataImage(t, repo.db, workspace)
					beforeIndexes := scaleP21SW98Indexes(t, repo, workspace)
					prior := scaleP21SW63EventIDs(t, repo, workspace)
					ctx := t.Context()
					if test.mode != "no-actor" {
						ctx = ContextWithActor(ctx, actor)
					}
					if test.scoped {
						ctx = WithIssueRecordMutations(ctx, issue.ID)
					}
					// Both flag states carry the same marker; off mode must
					// dispatch to the literal original before inspecting it.
					if strings.HasPrefix(test.mode, "upsert-event-") {
						ctx = ContextWithIssueAttachmentUpsertMutation(ctx)
					} else {
						ctx = ContextWithIssueAttachmentMutation(ctx)
					}
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
					if test.mode == "cancel-before" {
						cancel()
					}
					got := scaleP21SW149Outcome{}
					var captured []*domain.Bootstrap
					checkOwner := func() {
						if bounded && repo.MetadataWorkUsedForTesting() <= outerWork {
							t.Error("attachment publication lost output ownership")
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
							if test.mode == "cancel-persist" && strings.Contains(strings.ToLower(query), "insert into domain_events") {
								persistCanceled = true
								cancel()
							}
						}
					})
					payload := map[string]any{"title": "Evidence", "url": "https://example.test/evidence.png"}
					if test.operation == "upsert" {
						payload["url"] = "https://example.test/existing"
					}
					err := repo.MutateWorkspace(mutationCtx, workspace, test.event, issue.ID, payload, func(data *domain.Bootstrap) error {
						got.Calls++
						captured = append(captured, data)
						if test.mode == "noop" {
							return ErrNoMutation
						}
						var err error
						got.Saved, err = scaleP21SW149Mutate(data, issue.ID, test, at)
						if err != nil {
							return err
						}
						index := slices.IndexFunc(data.Issues, func(value domain.Issue) bool { return value.ID == issue.ID })
						got.Callback = append(got.Callback, scaleP21SW116Capture{
							Viewer: data.Viewer, Via: data.Viewer.Via, Role: data.ViewerRole,
							Saved: data.Issues[index], BeforeLabels: slices.Clone(data.Issues[index].Labels),
						})
						switch test.mode {
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
					if test.mode == "callback-error" && !errors.Is(err, ErrIssueQuery) ||
						(test.mode == "cancel-callback" || test.mode == "cancel-persist") && !errors.Is(err, context.Canceled) {
						t.Fatal("attachment callback/cancellation error changed", err)
					}
					if err != nil && !slices.Contains([]string{"cancel-before", "cancel-callback", "cancel-persist", "callback-error", "foreign-actor", "physical-activity"}, test.mode) {
						t.Fatal("ordinary attachment failed", bounded, got.Calls, err)
					}
					if test.mode == "cancel-persist" && !persistCanceled {
						t.Fatal("cancellation did not reach event persistence")
					}
					got.Business = scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)
					afterContent = append(afterContent, scaleP21SW149ContentKeys(got.Business.Content))
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
					failed := err != nil
					wantEvents := 1
					if failed || test.mode == "noop" {
						wantEvents = 0
						if !reflect.DeepEqual(got.Business, before) || !reflect.DeepEqual(got.Metadata, beforeMetadata) || !reflect.DeepEqual(got.Indexes, beforeIndexes) {
							t.Fatal("failed/no-op attachment changed persistent state")
						}
						for index := range got.Callback {
							got.Callback[index].Saved.Labels = slices.Clone(got.Callback[index].BeforeLabels)
						}
					}
					if len(got.Events) != wantEvents || len(got.Webhooks) != wantEvents || len(got.Realtime) != wantEvents {
						t.Fatal("attachment event/webhook/realtime count changed", got.Calls, len(got.Events), len(got.Webhooks), len(got.Realtime))
					}
					if test.scoped || failed || test.mode == "noop" {
						if got.LastWorkspace != "" {
							t.Fatal("attachment added full-writer last-workspace effects")
						}
					} else if got.LastWorkspace != workspace {
						t.Fatal("legacy attachment lost last-workspace effect")
					}
					if bounded && got.Calls != 0 {
						checkOwner()
						for _, data := range captured {
							if reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("attachment callback cleared before caller last use")
							}
						}
						if err := owner.ReserveEncoding(t.Context(), []any{got.Saved, got.Callback}); err != nil {
							t.Fatal(err)
						}
					}
					outcomes = append(outcomes, scaleP21SW63JSON(t, got))
					if bounded {
						owner.Close()
						owner.Close()
						for _, data := range captured {
							if !reflect.DeepEqual(*data, domain.Bootstrap{}) {
								t.Fatal("attachment callback survived output release")
							}
						}
						if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("attachment writer leaked work or hydrated full metadata")
						}
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Logf("content keys before legacy=%q bounded=%q; after legacy=%q bounded=%q", beforeContent[0], beforeContent[1], afterContent[0], afterContent[1])
					t.Fatal("literal/bounded attachment differs", scaleP21SW98FirstDiff(outcomes[0], outcomes[1], "attachment"))
				}
				t.Logf("scoped=%t reload=%t operation=%s mode=%s legacy_SQL=%d bounded_SQL=%d", test.scoped, reload, test.operation, test.mode, queries[0], queries[1])
			})
		}
	}
}

func scaleP21SW149ContentKeys(rows []any) []string {
	result := make([]string, 0, len(rows))
	for _, value := range rows {
		row := value.([]any)
		result = append(result, fmt.Sprint(row[0], "/", row[1], "/", row[2]))
	}
	return result
}

func TestScaleP21SW149IssueAttachmentQuotaAndProfile(t *testing.T) {
	scaleP21SW149Setup(t)
	for _, scoped := range []bool{false, true} {
		for _, mode := range []string{"quota", "unowned", "title", "reactions", "notifications", "comments", "version",
			"updated-description", "updated-document-content", "updated-activity", "unscoped-description", "unscoped-document-content",
			"upsert-description", "upsert-document-content"} {
			if (strings.HasPrefix(mode, "updated-") || strings.HasPrefix(mode, "upsert-")) && !scoped || strings.HasPrefix(mode, "unscoped-") && scoped {
				continue
			}
			t.Run(fmt.Sprint(scoped, "/", mode), func(t *testing.T) {
				f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Attachment profile"}}, nil)
				workspace := f.data.Workspace.URLKey
				at := time.Date(2026, 11, 15, 2, 3, 4, 567, time.UTC)
				scaleP21SW149Seed(t, f, scaleP21SW149Issue(issue, at, ""), at, "")
				repo := scaleP21IssuePersistenceView(t, f, true, false)
				before, metadata, indexes := scaleP21SW63BusinessRows(t, repo, workspace, issue.ID),
					scaleP21SW76MetadataImage(t, repo.db, workspace), scaleP21SW98Indexes(t, repo, workspace)
				ctx := ContextWithIssueAttachmentMutation(ContextWithActor(t.Context(), f.data.Viewer))
				if strings.HasPrefix(mode, "upsert-") {
					ctx = ContextWithIssueAttachmentUpsertMutation(ContextWithActor(t.Context(), f.data.Viewer))
				}
				event := "issue.link_created"
				if scoped {
					ctx, event = WithIssueRecordMutations(ctx, issue.ID), "attachment.created"
				}
				if strings.HasPrefix(mode, "updated-") {
					event = "attachment.updated"
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
				calls := 0
				err := repo.MutateWorkspace(ctx, workspace, event, issue.ID, nil, func(data *domain.Bootstrap) error {
					calls++
					index := slices.IndexFunc(data.Issues, func(value domain.Issue) bool { return value.ID == issue.ID })
					switch mode {
					case "title":
						data.Issues[index].Title = "Outside attachment profile"
					case "reactions":
						data.Issues[index].Reactions = map[string][]string{"like": {data.Viewer.ID}}
					case "notifications":
						data.Notifications = append(data.Notifications, domain.Notification{ID: "sw149-outside", RecipientID: data.Viewer.ID})
					case "comments":
						data.Comments[issue.ID] = append(data.Comments[issue.ID], domain.Comment{ID: "sw149-outside", Body: "Outside attachment profile"})
					case "version":
						data.Issues[index].Version++
					case "updated-description", "unscoped-description", "upsert-description":
						data.Issues[index].Description = "Outside this attachment profile"
					case "updated-document-content", "unscoped-document-content", "upsert-document-content":
						data.Issues[index].DocumentContent = &domain.DocumentContent{ID: "sw149-outside"}
					case "updated-activity":
						RecordIssueActivity(data, issue.ID, domain.ActivityEvent{ID: "sw149-outside", Type: "attachment.created", Actor: data.Viewer, CreatedAt: at})
					}
					return nil
				})
				repo.metadataResidency.work.limit = limit
				expected := ErrBoundedMetadataCoverage
				if mode == "quota" {
					expected = ErrMetadataWorkBudget
				}
				if !errors.Is(err, expected) || (mode == "quota" || mode == "unowned") && calls != 0 {
					t.Fatal("attachment quota/profile boundary changed", calls, err)
				}
				if !reflect.DeepEqual(before, scaleP21SW63BusinessRows(t, repo, workspace, issue.ID)) ||
					!reflect.DeepEqual(metadata, scaleP21SW76MetadataImage(t, repo.db, workspace)) ||
					!reflect.DeepEqual(indexes, scaleP21SW98Indexes(t, repo, workspace)) {
					t.Fatal("rejected attachment callback persisted effects")
				}
				if owner != nil {
					owner.Close()
				}
				if repo.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("rejected attachment callback retained work")
				}
			})
		}
	}
}
