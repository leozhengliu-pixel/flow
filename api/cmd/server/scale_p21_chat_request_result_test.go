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

func TestScaleP21ChatRequestResultDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{43}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, user := f.data.Workspace.URLKey, f.principals[2].user
		issue := domain.Issue{ID: idgen.New("request-result-issue-"), Identifier: "RESULT-1", Title: "Exact request title",
			Team: domain.Team{ID: f.public, Name: "Request team"}, State: domain.WorkflowState{Name: "Triage"}, Version: 7}
		cases := []struct {
			name, provider, language, wantLanguage string
			missing, noPersonal, unlinked          bool
			cardFail, personalFail, replace        bool
			messages                               int
		}{
			{name: "selected-chinese", provider: "lark", language: "zh-CN", wantLanguage: "zh", messages: 2},
			{name: "selected-other", provider: "feishu", language: "fr", wantLanguage: "en", messages: 2},
			{name: "system-lark", provider: "lark", language: "system", wantLanguage: "en", messages: 2},
			{name: "auto-feishu", provider: "feishu", language: "auto", wantLanguage: "zh", messages: 2},
			{name: "missing-settings", provider: "lark", missing: true, wantLanguage: "en", messages: 2},
			{name: "no-personal-recipient", provider: "lark", noPersonal: true, messages: 1},
			{name: "unlinked-recipient", provider: "lark", unlinked: true, messages: 1},
			{name: "card-failure", provider: "lark", cardFail: true, messages: 1},
			{name: "personal-failure", provider: "lark", language: "zh", personalFail: true, messages: 2},
			{name: "settings-replaced-after-card", provider: "lark", language: "en", replace: true, wantLanguage: "zh", messages: 2},
		}
		var active *store.SQLiteStore
		var scenario int
		var sent []map[string]any
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			if strings.Contains(request.URL.Path, "tenant_access_token") {
				return chatTestResponse(http.StatusOK, `{"code":0,"tenant_access_token":"request-result-token","expire":7200}`), nil
			}
			if !strings.Contains(request.URL.Path, "/im/v1/messages") {
				t.Fatalf("unexpected platform request %s", request.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			sent = append(sent, map[string]any{"path": request.URL.Path, "query": request.URL.RawQuery, "body": body})
			test := cases[scenario]
			personal := request.URL.Query().Get("receive_id_type") == "open_id"
			if !personal && len(sent) == 1 && active == f.probe && active.MetadataWorkUsedForTesting() == 0 {
				t.Error("issue card output escaped its owner before the first platform send")
			}
			if personal && active == f.probe && active.MetadataWorkUsedForTesting() == 0 {
				t.Error("language output escaped its admitted display read before sending")
			}
			if !personal && test.replace {
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.request_result_replace", "", nil, func(data *domain.Bootstrap) error {
					data.UserSettings[user.ID] = domain.UserSettings{Language: "zh"}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
			}
			if !personal && test.cardFail || personal && test.personalFail {
				return chatTestResponse(http.StatusServiceUnavailable, `{"code":999,"msg":"Retry exact request"}`), nil
			}
			return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":"request-result-message","thread_id":"request-result-thread"}}`), nil
		})
		for index, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				scenario = index
				record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, ConnectionID: idgen.New("request-result-connection-"),
					Provider: test.provider, AppID: "request-result-app", TenantKey: "request-result-tenant", Connected: true}
				t.Cleanup(func() {
					_ = repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record))
					_ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID)
				})
				binding := domain.ChatBinding{ID: "request-result-binding", ConnectionID: record.ConnectionID, TeamID: f.public, ChatID: "request-result-group", Enabled: true}
				payload := chatNotificationJob{PersonalOpenID: "request-result-open-id"}
				if test.noPersonal {
					payload.PersonalOpenID = ""
				}
				type outcome struct {
					Messages []map[string]any
					Error    string
				}
				var outputs []outcome
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.request_result_seed", "", nil, func(data *domain.Bootstrap) error {
						if test.missing {
							delete(data.UserSettings, user.ID)
						} else {
							data.UserSettings[user.ID] = domain.UserSettings{Language: test.language}
						}
						data.UserSettings["request-result-unrelated"] = domain.UserSettings{AgentInstructions: strings.Repeat("Unrelated settings ", 30000)}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.PutChatIntegration(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					if !test.unlinked {
						if err := repo.PutChatIdentity(t.Context(), workspace, record.ConnectionID, domain.ChatIdentity{
							UserID: user.ID, OpenID: "request-result-open-id", TenantKey: record.TenantKey,
						}); err != nil {
							t.Fatal(err)
						}
					}
					secret, _ := json.Marshal(chatCredentials{AppSecret: "request-result-secret"})
					if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), secret, time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
					active, sent = source, nil
					service := &server{store: source, allowedOrigin: "https://flow.example.test"}
					err := service.chatSendRequestResult(t.Context(), record, binding, issue, domain.ChatJob{DedupKey: "request-result-dedup"}, payload)
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("request result retained output ownership after returning")
					}
					result := outcome{Messages: sent}
					if err != nil {
						result.Error = err.Error()
					}
					if len(sent) != test.messages || (err != nil) != (test.cardFail || test.personalFail) {
						t.Fatal("request-result output or failure changed", len(sent), err)
					}
					if test.wantLanguage != "" {
						var content map[string]string
						if err := json.Unmarshal([]byte(sent[1]["body"].(map[string]any)["content"].(string)), &content); err != nil {
							t.Fatal(err)
						}
						want := chatT(test.wantLanguage, "request_saved_link", issue.Identifier, chatRequestIssueURL(service, record, issue))
						if content["text"] != want {
							t.Fatal("request confirmation language or link changed", content["text"])
						}
					}
					outputs = append(outputs, result)
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("request confirmation leaked display ownership")
					}
					if err := repo.DeleteChatIntegration(t.Context(), workspace, record.ConnectionID); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("request-result differential", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("request confirmation decoded complete workspace metadata")
		}
	}
}
