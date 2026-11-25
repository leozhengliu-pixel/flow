package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21SW56DMInboxDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := newChatWorkflowFixture(t)
	repo := f.s.store
	workspace := f.record.WorkspaceKey
	data, found := repo.BootstrapFor(workspace)
	if !found || len(data.Issues) == 0 {
		t.Fatal("inbox fixture has no issue")
	}
	issue := data.Issues[0]
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	var selected *store.SQLiteStore
	failPlatform := false
	transport := http.DefaultTransport
	http.DefaultTransport = chatWorkflowTransport(func(request *http.Request) (*http.Response, error) {
		message := request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/im/v1/messages")
		if message && selected.BoundedMetadataEnabled() && selected.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("inbox display closed before actual platform send")
		}
		response, err := transport.RoundTrip(request)
		if message && failPlatform && err == nil {
			response.Body.Close()
			return chatTestResponse(http.StatusServiceUnavailable, `{"code":999,"msg":"inbox unavailable"}`), nil
		}
		return response, err
	})
	t.Cleanup(func() { http.DefaultTransport = transport })
	stamp := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
	for _, test := range []struct {
		name       string
		want, omit []string
		wantError  bool
	}{
		{name: "empty", want: []string{"Your inbox is empty.", "Open Inbox"}},
		{name: "mixed", want: []string{"Inbox (5 unread)", issue.Identifier, "First review", "Open Flow to read it."}, omit: []string{"Wrong later review", "Archived title"}},
		{name: "review-order", want: []string{"First review", "Second review"}, omit: []string{"Wrong later review", "Wrong duplicate review"}},
		{name: "unavailable", want: []string{"Inbox (3 unread)", "Your inbox is empty."}},
		{name: "limit-before-filter", want: []string{"Inbox (12 unread)", "Visible tenth"}, omit: []string{"Too old"}},
		{name: "chinese", want: []string{"\u6536\u4ef6\u7bb1", "\u6761\u672a\u8bfb", issue.Identifier}},
		{name: "platform-failure", wantError: true},
		{name: "missing-workspace", wantError: true},
		{name: "inactive-viewer", want: []string{"Your Flow access to this workspace has changed."}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw56_seed", "", nil, func(next *domain.Bootstrap) error {
				next.Reviews = []domain.CodeReview{
					{ID: "sw56-first", SlugID: "sw56-collision", Title: "First review"},
					{ID: "sw56-collision", SlugID: "sw56-later", Title: "Wrong later review"},
					{ID: "sw56-first", SlugID: "sw56-duplicate", Title: "Wrong duplicate review"},
					{ID: "sw56-second", SlugID: "sw56-second-slug", Title: "Second review"},
				}
				next.Notifications = []domain.Notification{}
				add := func(item domain.Notification) {
					item.ID = fmt.Sprintf("sw56-notification-%02d", len(next.Notifications))
					item.RecipientID, item.Actor = f.identity.UserID, domain.User{ID: "sw56-actor", DisplayName: "Stored actor"}
					item.CreatedAt = stamp.Add(time.Duration(len(next.Notifications)) * time.Minute)
					next.Notifications = append(next.Notifications, item)
				}
				switch test.name {
				case "empty":
				case "review-order":
					for _, ref := range []string{"sw56-first", "sw56-second-slug", "sw56-missing", "sw56-collision"} {
						add(domain.Notification{ReviewID: ref, IssueID: "sw56-missing-issue"})
					}
				case "unavailable":
					add(domain.Notification{IssueID: "sw56-missing-issue"})
					add(domain.Notification{ReviewID: "sw56-missing-review"})
					add(domain.Notification{})
				case "limit-before-filter":
					add(domain.Notification{Title: "Too old first"})
					add(domain.Notification{Title: "Too old second"})
					add(domain.Notification{Title: "Visible tenth"})
					for range 9 {
						add(domain.Notification{IssueID: "sw56-missing-issue"})
					}
				default:
					add(domain.Notification{Type: "assignment", IssueID: issue.ID})
					add(domain.Notification{ReviewID: "sw56-collision", IssueID: "sw56-missing-issue"})
					add(domain.Notification{Title: "Standalone title"})
					add(domain.Notification{IssueID: "sw56-missing-issue"})
					add(domain.Notification{ReviewID: "sw56-missing-review"})
					add(domain.Notification{Title: "Archived title", ArchivedAt: &stamp})
					add(domain.Notification{Type: "comment", IssueID: issue.ID, ReadAt: &stamp})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				Messages []map[string]any
				Error    string
			}
			var outputs []outcome
			for _, source := range []*store.SQLiteStore{repo, probe} {
				selected, failPlatform, f.sent = source, test.name == "platform-failure", nil
				dm := chatDMRequest{
					record: f.record, identity: f.identity,
					target:  chatDMTarget{Workspace: workspace},
					command: chatInboundCommand{OpenID: f.identity.OpenID, MessageID: "sw56-" + test.name},
					job:     domain.ChatJob{DedupKey: "sw56-" + test.name}, zh: test.name == "chinese",
				}
				if test.name == "missing-workspace" {
					dm.target.Workspace += "-missing"
				}
				if test.name == "inactive-viewer" {
					dm.identity.UserID = "sw56-missing-user"
				}
				before := source.WorkspaceMetadataDecodes()
				err := (&server{store: source, allowedOrigin: "https://flow.example.test"}).chatDMInbox(context.Background(), dm)
				got := outcome{Messages: f.sent}
				if err != nil {
					got.Error = err.Error()
				}
				if (err != nil) != test.wantError {
					t.Fatal("unexpected inbox error", err)
				}
				if test.name != "missing-workspace" && len(got.Messages) != 1 {
					t.Fatal("inbox send count changed", len(got.Messages))
				}
				raw, err := json.Marshal(got.Messages)
				if err != nil {
					t.Fatal(err)
				}
				for _, text := range test.want {
					if !strings.Contains(string(raw), text) {
						t.Fatal("missing inbox output", text, string(raw))
					}
				}
				for _, text := range test.omit {
					if strings.Contains(string(raw), text) {
						t.Fatal("unexpected inbox output", text, string(raw))
					}
				}
				if source.MetadataWorkUsedForTesting() != 0 || source == probe && source.WorkspaceMetadataDecodes() != before {
					t.Fatal("inbox leaked work or decoded full metadata")
				}
				outputs = append(outputs, got)
			}
			if !reflect.DeepEqual(outputs[0], outputs[1]) {
				t.Fatal("actual legacy/bounded inbox output differs", outputs)
			}
		})
	}
}
