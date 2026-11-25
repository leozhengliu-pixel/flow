package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW175Request struct {
	Method, Path string
	Body         any
}

func TestScaleP21SW175ChatInboundWorkflowHeaderDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, database, issue := scaleP21SW44ChatFixture(t)
	repo, workspace := database.repo, database.key
	prefix := idgen.New("sw175-")
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "states", "teamSettings", "projects", "issueTemplates", "userSettings"), workspace, "team.settings_updated", f.binding.TeamID, nil, func(data *domain.Bootstrap) error {
		data.States = []domain.WorkflowState{
			{ID: prefix + "todo", TeamID: f.binding.TeamID, Name: "Exact workflow todo", Type: "unstarted", Position: 2, Default: true},
			{ID: prefix + "done", TeamID: f.binding.TeamID, Name: "Exact workflow done", Type: "completed", Position: 3},
		}
		settings := data.TeamSettings[f.binding.TeamID]
		settings.TeamID, settings.DefaultStateID, settings.DefaultPriority = f.binding.TeamID, prefix+"todo", 2
		data.TeamSettings[f.binding.TeamID] = settings
		data.Projects = []domain.Project{
			{ID: prefix + "project-one", Name: "First offered project", TeamIDs: []string{f.binding.TeamID}},
			{ID: prefix + "project-two", Name: "Second offered project", TeamIDs: []string{f.binding.TeamID}},
		}
		data.IssueTemplates = []domain.IssueTemplate{{
			ID: prefix + "template", TeamID: f.binding.TeamID, Name: "Exact template", Title: "Exact template title",
			StateID: prefix + "todo", Priority: 3, AssigneeID: f.identity.UserID, ProjectID: prefix + "project-one",
		}}
		preference := data.UserSettings[f.identity.UserID]
		preference.UserID, preference.Language = f.identity.UserID, "zh-CN"
		data.UserSettings[f.identity.UserID] = preference
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport
	defer func() { http.DefaultTransport = transport }()
	var requests []scaleP21SW175Request
	requireHeaders := false
	http.DefaultTransport = chatWorkflowTransport(func(request *http.Request) (*http.Response, error) {
		platform := request.URL.Hostname() == "open.feishu.cn" || request.URL.Hostname() == "open.larksuite.com"
		if platform && !strings.Contains(request.URL.Path, "tenant_access_token") {
			var payload any
			if request.Body != nil {
				raw, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				request.Body = io.NopCloser(bytes.NewReader(raw))
				if len(raw) != 0 {
					if err := json.Unmarshal(raw, &payload); err != nil {
						t.Fatal(err)
					}
				}
			}
			requests = append(requests, scaleP21SW175Request{request.Method, request.URL.RequestURI(), payload})
			if requireHeaders && f.s.store.BoundedMetadataEnabled() && f.s.store.MetadataWorkUsedForTesting() == 0 {
				t.Error("workflow headers were released before actual platform request")
			}
		}
		return transport.RoundTrip(request)
	})
	type outcome struct {
		Requests []scaleP21SW175Request
		Action   *chatCardAction
		Error    string
	}
	for _, test := range []struct {
		name, kind, generated string
		posts                 int
	}{
		{"create-form", "create_form", "create", 1},
		{"open-state", "open_state", "state", 1},
		{"open-assignee", "open_assignee", "assignee", 1},
		{"open-comment", "open_comment", "comment", 1},
		{"expired-action", "open_state", "", 1},
		{"expired-auth", "create_form", "", 1},
		{"create-disabled", "create_form", "", 1},
		{"updates-disabled", "open_state", "", 0},
		{"scope-changed", "open_state", "", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var outcomes []outcome
			for _, source := range []*store.SQLiteStore{repo, view} {
				identity, binding := f.identity, f.binding
				if test.name == "expired-auth" {
					identity.AuthenticatedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
				}
				if test.kind == "create_form" {
					binding.ProjectIDs = []string{prefix + "project-one", prefix + "project-two"}
					binding.TemplateID = prefix + "template"
				}
				if test.name == "create-disabled" {
					binding.AllowCreate = false
				}
				if test.name == "updates-disabled" {
					binding.AllowUpdate = false
				}
				if err := repo.PutChatIdentity(t.Context(), workspace, f.record.ConnectionID, identity); err != nil {
					t.Fatal(err)
				}
				if err := repo.PutChatBinding(t.Context(), workspace, binding); err != nil {
					t.Fatal(err)
				}
				job := domain.ChatJob{ID: prefix + test.name, WorkspaceKey: workspace, ConnectionID: f.record.ConnectionID, Kind: "inbound", DedupKey: prefix + test.name}
				command := chatInboundCommand{Kind: "card_action", OpenID: identity.OpenID, ChatID: binding.ChatID, MessageID: prefix + "source-message", ActionID: prefix + test.name}
				if test.kind == "create_form" {
					command.Kind = test.kind
				} else {
					action := chatCardAction{
						Kind: test.kind, BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding),
						ChatID: binding.ChatID, MessageID: command.MessageID, IssueID: issue.ID,
					}
					if test.name == "scope-changed" {
						action.BindingScope += "-changed"
					}
					raw, _ := json.Marshal(action)
					expires := time.Now().Add(time.Hour)
					if test.name == "expired-action" {
						expires = time.Now().Add(time.Second)
					}
					if err := repo.PutChatAction(t.Context(), domain.ChatAction{ID: command.ActionID, WorkspaceKey: workspace, ConnectionID: f.record.ConnectionID, Payload: raw, ExpiresAt: expires}); err != nil {
						t.Fatal(err)
					}
					if test.name == "expired-action" {
						// The writer only accepts live actions; expire the stored fixture naturally.
						select {
						case <-time.After(time.Until(expires)):
						case <-t.Context().Done():
							t.Fatal(t.Context().Err())
						}
					}
				}
				outputID := "chat_action_" + chatKey(f.record.ConnectionID, job.DedupKey, test.generated)
				if _, err := repo.ConsumeChatAction(t.Context(), workspace, f.record.ConnectionID, outputID); err != nil {
					t.Fatal(err)
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				f.s.store, requests, requireHeaders = source, nil, test.name != "expired-auth"
				ctx, closeOutputs := beginIndependentMetadataOutputReads(t.Context())
				err := f.s.processChatInbound(ctx, f.record, job, command)
				got := outcome{Requests: requests}
				if err != nil {
					got.Error = err.Error()
				}
				posts := 0
				for _, request := range requests {
					if request.Method == http.MethodPost && strings.Contains(request.Path, "/im/v1/messages") {
						posts++
					}
				}
				if posts != test.posts || (err != nil) != (test.name == "updates-disabled") {
					t.Errorf("inbound branch changed: posts=%d want=%d err=%v", posts, test.posts, err)
				}
				if test.generated != "" {
					stored, err := repo.GetChatAction(t.Context(), workspace, f.record.ConnectionID, outputID)
					var action chatCardAction
					if err != nil || json.Unmarshal(stored.Payload, &action) != nil {
						t.Error("inbound form did not persist its action", err)
					}
					got.Action = &action
				}
				if source.BoundedMetadataEnabled() && requireHeaders && source.MetadataWorkUsedForTesting() == 0 {
					t.Error("inbound returned before its containing output owner released headers")
				}
				closeOutputs()
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Error("inbound workflow leaked work or decoded full metadata")
				}
				outcomes = append(outcomes, got)
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("actual inbound form differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
		})
	}
}
