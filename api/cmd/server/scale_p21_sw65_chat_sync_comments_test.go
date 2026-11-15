package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW65ChatSyncCommentsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := newChatWorkflowFixture(t)
	repo, workspace := f.s.store, f.record.WorkspaceKey
	data, found := repo.BootstrapFor(workspace)
	if !found || len(data.Issues) == 0 {
		t.Fatal("comment sync fixture has no issue")
	}
	issue := data.Issues[0]
	issue.State = domain.WorkflowState{ID: "sw65-state", Name: "Inherited state"}
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	type platformCall struct {
		Method, Path string
		Body         map[string]any
	}
	type outcome struct {
		Calls   []platformCall
		Thread  domain.ChatThread
		Items   []domain.ChatThreadItem
		Actions []domain.ChatAction
		Error   string
	}
	var selected *store.SQLiteStore
	var calls []platformCall
	var variant string
	transport := http.DefaultTransport
	http.DefaultTransport = chatWorkflowTransport(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/im/v1/messages") && (request.Method == http.MethodPost || request.Method == http.MethodPatch) {
			if selected.BoundedMetadataEnabled() && selected.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("comment sync released display/workflow before actual platform send")
			}
			raw, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatal(err)
			}
			request.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			calls = append(calls, platformCall{request.Method, request.URL.Path, body})
			if variant == "post-failure" || variant == "patch-failure" && request.Method == http.MethodPatch {
				return chatTestResponse(http.StatusServiceUnavailable, `{"code":999,"msg":"Exact comment sync failure"}`), nil
			}
			if variant == "recalled-root" && strings.HasSuffix(request.URL.Path, "/sw65-root/reply") {
				return chatTestResponse(http.StatusBadRequest, `{"code":230110,"msg":"Root was recalled"}`), nil
			}
		}
		return transport.RoundTrip(request)
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	at := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
	for _, test := range []struct {
		name                  string
		posts, patches, items int
		wantError, fallback   bool
	}{
		{name: "new-and-reply", posts: 2, items: 2},
		{name: "mixed-edit-delete", posts: 3, patches: 2, items: 7},
		{name: "patch-failure", posts: 1, patches: 1, items: 1},
		{name: "post-failure", posts: 1, wantError: true},
		{name: "missing-thread", posts: 2, items: 1, fallback: true},
		{name: "recalled-root", posts: 3, items: 1, fallback: true},
		{name: "skip-without-thread"},
		{name: "sync-limit", posts: chatThreadSyncLimit, items: chatThreadSyncLimit},
		{name: "chinese", posts: 1, items: 1},
		{name: "noecho-empty-old", items: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := f.binding
			binding.Language = "en"
			if test.name == "chinese" {
				binding.Language = "zh-CN"
			}
			comment := func(id, body string, offset int) domain.Comment {
				return domain.Comment{ID: id, Version: 2, Body: body, CreatedAt: at.Add(time.Duration(offset) * time.Minute), User: domain.User{ID: f.identity.UserID, Name: "Stale author"}}
			}
			comments := []domain.Comment{comment("sw65-new", "Exact new <comment>\x00\u51c6", 1)}
			seedItems := []domain.ChatThreadItem{}
			seedItem := func(id, message, origin string, version int64) {
				seedItems = append(seedItems, domain.ChatThreadItem{ItemType: chatThreadItemComment, ItemID: id, MessageID: message, Origin: origin, Version: version, CreatedAt: at})
			}
			switch test.name {
			case "new-and-reply":
				reply := comment("sw65-reply", strings.Repeat("Long reply ", 250), 2)
				parent := "sw65-new"
				reply.ParentID, reply.BotActor = &parent, &domain.ActorBot{Name: "Named bot"}
				comments = append(comments, reply)
			case "mixed-edit-delete":
				comments = append(comments, comment("sw65-flow-edit", "Edited Flow body", 2), comment("sw65-chat-edit", "Edited member body", 3),
					comment("sw65-known", "Do not echo", 4), comment("sw65-empty", " \n\t ", 5), comment("sw65-old", "Too old", -10))
				seedItem("sw65-flow-edit", "sw65-edit", domain.ChatThreadOriginFlow, 1)
				seedItem("sw65-chat-edit", "sw65-member", domain.ChatThreadOriginChat, 1)
				seedItem("sw65-known", "sw65-known-message", domain.ChatThreadOriginChat, 2)
				seedItem("sw65-deleted-flow", "sw65-deleted-message", domain.ChatThreadOriginFlow, 1)
				seedItem("sw65-deleted-chat", "sw65-deleted-member", domain.ChatThreadOriginChat, 1)
				seedItems = append(seedItems, domain.ChatThreadItem{ItemType: chatThreadItemLinkCard, ItemID: "sw65-link", MessageID: "sw65-link-message", Origin: domain.ChatThreadOriginFlow, CreatedAt: at})
			case "patch-failure":
				comments = []domain.Comment{comment("sw65-flow-edit", "Edited Flow body", 2)}
				seedItem("sw65-flow-edit", "sw65-edit", domain.ChatThreadOriginFlow, 1)
			case "sync-limit":
				comments = nil
				for index := range 25 {
					comments = append(comments, comment(fmt.Sprintf("sw65-limit-%02d", index), fmt.Sprintf("Limit comment %02d", index), index+1))
				}
			case "noecho-empty-old":
				comments = []domain.Comment{comment("sw65-known", "Do not echo", 1), comment("sw65-empty", " \n ", 2), comment("sw65-old", "Too old", -10)}
				seedItem("sw65-known", "sw65-known-message", domain.ChatThreadOriginChat, 2)
			}
			var outputs []outcome
			for _, source := range []*store.SQLiteStore{repo, probe} {
				if existing, err := repo.ChatThread(t.Context(), workspace, f.record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID); err == nil {
					if err := repo.DeleteChatThread(t.Context(), workspace, f.record.ConnectionID, existing.ID); err != nil {
						t.Fatal(err)
					}
				} else if !errors.Is(err, sql.ErrNoRows) {
					t.Fatal(err)
				}
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw65_seed", "", nil, func(next *domain.Bootstrap) error {
					next.Comments[issue.ID] = comments
					first := data.Viewer
					first.DisplayName = "Directory first <name>\x00\u51c6"
					later := first
					later.DisplayName = "Wrong later directory name"
					next.Users = []domain.User{first, later}
					next.TeamSettings[issue.Team.ID] = domain.TeamSettings{TeamID: issue.Team.ID, ParentTeamID: "sw65-parent", InheritWorkflowStatuses: true}
					next.TeamSettings["sw65-parent"] = domain.TeamSettings{TeamID: "sw65-parent", DefaultStateID: "sw65-state"}
					next.States = []domain.WorkflowState{{ID: "sw65-state", TeamID: "sw65-parent", Name: "Inherited exact workflow", Default: true}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if test.name != "missing-thread" && test.name != "skip-without-thread" {
					thread, err := repo.PutChatThread(t.Context(), workspace, domain.ChatThread{
						ConnectionID: f.record.ConnectionID, ChatID: binding.ChatID, ResourceType: domain.ChatThreadResourceIssue, ResourceID: issue.ID,
						RootMessageID: "sw65-root", BindingID: binding.ID, CreatedAt: at, SyncFrom: at.Add(-time.Minute)})
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range seedItems {
						item.ThreadID = thread.ID
						if inserted, err := repo.InsertChatThreadItem(t.Context(), workspace, f.record.ConnectionID, item); err != nil || !inserted {
							t.Fatal(inserted, err)
						}
					}
				}
				if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				selected, variant, calls = source, test.name, nil
				before, started := source.WorkspaceMetadataDecodes(), time.Now().UTC()
				ctx := store.WithSQLStatementObserver(t.Context(), func(query string) {
					if source == probe && (strings.HasPrefix(query, "INSERT INTO chat_thread_items") || strings.HasPrefix(query, "UPDATE chat_thread_items")) && source.MetadataWorkUsedForTesting() == 0 {
						t.Fatal("comment sync released display before durable item persistence")
					}
				})
				payload := chatNotificationJob{Event: "comment.created", EventAt: at}
				if test.name == "skip-without-thread" {
					payload.Event = "comment.updated"
				}
				err := (&server{store: source, allowedOrigin: "https://flow.example.test"}).chatSyncIssueComments(ctx, f.record, binding, issue, domain.ChatJob{DedupKey: "sw65-" + test.name}, payload)
				got := outcome{Calls: slices.Clone(calls)}
				if err != nil {
					got.Error = err.Error()
				}
				if (err != nil) != test.wantError {
					t.Fatal("unexpected comment sync error", err)
				}
				posts, patches := 0, 0
				actionIDs := map[string]bool{}
				var collectActions func(any)
				collectActions = func(value any) {
					switch value := value.(type) {
					case map[string]any:
						if id, ok := value["actionId"].(string); ok {
							actionIDs[id] = true
						}
						for _, child := range value {
							collectActions(child)
						}
					case []any:
						for _, child := range value {
							collectActions(child)
						}
					}
				}
				for _, call := range got.Calls {
					if call.Method == http.MethodPost {
						posts++
					} else {
						patches++
					}
					var card any
					_ = json.Unmarshal([]byte(call.Body["content"].(string)), &card)
					collectActions(card)
				}
				if posts != test.posts || patches != test.patches {
					t.Fatal("comment sync platform operations changed", posts, patches)
				}
				raw, _ := json.Marshal(got.Calls)
				if strings.Contains(string(raw), "Wrong later directory name") || strings.Contains(string(raw), "Do not echo") || strings.Contains(string(raw), "Too old") {
					t.Fatal("comment sync changed directory order or skip/noecho rules")
				}
				if test.fallback && (!strings.Contains(string(raw), "Inherited exact workflow") || len(actionIDs) != 4) {
					t.Fatal("issue-card fallback lost actual inherited selector/actions", len(actionIDs))
				}
				if test.name == "new-and-reply" && (!strings.Contains(string(raw), "Directory first") || !strings.Contains(string(raw), "Named bot")) {
					t.Fatal("comment/reply author display changed")
				}
				thread, threadErr := source.ChatThread(t.Context(), workspace, f.record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID)
				if threadErr != nil && !errors.Is(threadErr, sql.ErrNoRows) {
					t.Fatal(threadErr)
				}
				if threadErr == nil {
					got.Items, err = source.ChatThreadItems(t.Context(), workspace, f.record.ConnectionID, thread.ID)
					if err != nil {
						t.Fatal(err)
					}
					if test.name == "recalled-root" {
						if thread.SyncFrom.Before(started) || thread.SyncFrom.After(time.Now().UTC()) {
							t.Fatal("recalled root restart cutoff changed")
						}
						thread.SyncFrom = time.Time{}
					}
					thread.CreatedAt, thread.UpdatedAt = time.Time{}, time.Time{}
					got.Thread = thread
				}
				if len(got.Items) != test.items {
					t.Fatal("durable mirrored item count changed", len(got.Items), test.items)
				}
				for index := range got.Items {
					got.Items[index].UpdatedAt = time.Time{}
				}
				ids := make([]string, 0, len(actionIDs))
				for id := range actionIDs {
					ids = append(ids, id)
				}
				slices.Sort(ids)
				for _, id := range ids {
					action, err := source.GetChatAction(t.Context(), workspace, f.record.ConnectionID, id)
					if err != nil || action.ExpiresAt.Before(started.Add(14*24*time.Hour)) {
						t.Fatal("fallback action was not durably live", err)
					}
					action.ExpiresAt = time.Time{}
					got.Actions = append(got.Actions, action)
				}
				if source.MetadataWorkUsedForTesting() != 0 || source == probe && source.WorkspaceMetadataDecodes() != before {
					t.Fatal("comment sync leaked work or decoded full metadata")
				}
				outputs = append(outputs, got)
			}
			if !reflect.DeepEqual(outputs[0], outputs[1]) {
				t.Fatal("actual comment sync legacy/bounded differential", scaleP21M2Diff(outputs[0], outputs[1]))
			}
		})
	}
}
