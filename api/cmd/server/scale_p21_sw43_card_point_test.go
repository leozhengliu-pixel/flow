package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW43ChatUpdateFormPointDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{73}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw43-card-")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "states", "teamSettings"), workspace, "scale_p21.sw43_card_workflow", "", nil, func(data *domain.Bootstrap) error {
			data.States = []domain.WorkflowState{
				{ID: prefix + "todo", TeamID: f.public, Name: "Exact todo", Type: "unstarted", Default: true},
				{ID: prefix + "done", TeamID: f.public, Name: "Exact done", Type: "completed"},
			}
			settings := data.TeamSettings[f.public]
			settings.DefaultStateID = prefix + "todo"
			data.TeamSettings[f.public] = settings
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
			map[string]any{"title": "SW43 exact form title", "teamId": f.public, "stateId": prefix + "todo"}, workspace, http.StatusCreated)
		record := domain.ChatIntegrationRecord{
			WorkspaceKey: workspace, ConnectionID: prefix + "connection", Provider: "lark",
			AppID: prefix + "app", TenantKey: prefix + "tenant", Connected: true,
		}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record))
			_ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID)
		})
		secret, _ := json.Marshal(chatCredentials{AppSecret: "sw43-card-secret"})
		if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), secret, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		identity := domain.ChatIdentity{
			UserID: f.data.Viewer.ID, OpenID: prefix + "open", TenantKey: record.TenantKey,
			AuthProvider: "password", AuthenticatedAt: time.Now().UTC(),
		}
		binding := domain.ChatBinding{ID: prefix + "binding", ConnectionID: record.ConnectionID, ChatID: prefix + "group", TeamID: f.public, AllowUpdate: true, Enabled: true}
		var active *store.SQLiteStore
		var sent []map[string]any
		failSend := false
		mockChatTransport(t, func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "tenant_access_token") {
				return chatTestResponse(http.StatusOK, `{"code":0,"tenant_access_token":"sw43-card-token","expire":7200}`), nil
			}
			if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/im/v1/messages") {
				t.Fatalf("unexpected update-form request %s %s", r.Method, r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			sent = append(sent, body)
			if active.BoundedMetadataEnabled() && active.MetadataWorkUsedForTesting() == 0 {
				t.Error("point result or derived error lost its owner before send")
			}
			if failSend {
				return chatTestResponse(http.StatusServiceUnavailable, `{"code":999,"msg":"Exact form send failure"}`), nil
			}
			return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":"sw43-personal-message","chat_id":"sw43-personal-chat"}}`), nil
		})
		type outcome struct {
			Sent   []map[string]any
			Action chatCardAction
			Error  string
		}
		for _, test := range []struct {
			name, kind string
			missing    bool
			fail       bool
		}{
			{name: "state", kind: "open_state"},
			{name: "assignee", kind: "open_assignee"},
			{name: "comment", kind: "open_comment"},
			{name: "missing-issue-error", kind: "open_comment", missing: true},
			{name: "send-error", kind: "open_comment", fail: true},
		} {
			t.Run(test.name, func(t *testing.T) {
				var outputs []outcome
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					metadata, err := repo.PagedWorkspaceMetadata(t.Context(), workspace, identity.UserID)
					if err != nil {
						t.Fatal(err)
					}
					original := chatCardAction{Kind: test.kind, IssueID: issue.ID, MessageID: "sw43-original-message", SourceMessageID: "sw43-source-message"}
					if test.missing {
						original.IssueID = prefix + "missing"
					}
					job := domain.ChatJob{ID: prefix + test.name, DedupKey: prefix + test.name, WorkspaceKey: workspace, ConnectionID: record.ConnectionID}
					actionID := "chat_action_" + chatKey(record.ConnectionID, job.DedupKey, strings.TrimPrefix(test.kind, "open_"))
					if _, err := repo.ConsumeChatAction(t.Context(), workspace, record.ConnectionID, actionID); err != nil {
						t.Fatal(err)
					}
					command := chatInboundCommand{OpenID: identity.OpenID, MessageID: "sw43-command-message"}
					active, sent, failSend = source, nil, test.fail
					service := &server{store: source, allowedOrigin: "https://flow.example.test"}
					ctx := withChatIdentityAuthentication(t.Context(), identity)
					err = service.chatSendUpdateForm(ctx, record, binding, identity, metadata, job, command, original)
					result := outcome{Sent: sent}
					if err != nil {
						result.Error = err.Error()
					}
					if len(sent) != 1 || (err != nil) != test.fail {
						t.Fatal("update form output/error changed", test.name, len(sent), err)
					}
					if !test.missing {
						stored, err := repo.GetChatAction(t.Context(), workspace, record.ConnectionID, actionID)
						if err != nil || json.Unmarshal(stored.Payload, &result.Action) != nil {
							t.Fatal("form action not persisted", err)
						}
						if result.Action.ExpectedVersion != issue.Version || result.Action.IssueID != issue.ID {
							t.Fatal("form action lost issue version or identity")
						}
					}
					outputs = append(outputs, result)
					if source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("point form leaked output work or decoded complete metadata")
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("update-form output differential", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
	}
}
