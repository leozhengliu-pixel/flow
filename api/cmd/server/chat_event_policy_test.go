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

func TestScaleP21ChatEventAdaptersDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Chat event issue", "teamId": f.public}, workspace, http.StatusCreated)
		actor := f.principals[2].user
		projectID, initiativeID := idgen.New("chat-event-project-"), idgen.New("chat-event-initiative-")
		const templateID, askID = "chat-event-template", "chat-event-ask"
		at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		var messages []map[string]any
		groupCalls := 0
		membership := true
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(request.URL.Path, "tenant_access_token"):
				return chatTestResponse(200, `{"code":0,"tenant_access_token":"event-policy-token","expire":7200}`), nil
			case strings.HasSuffix(request.URL.Path, "/members/is_in_chat"):
				groupCalls++
				if membership {
					return chatTestResponse(200, `{"code":0,"data":{"is_in_chat":true}}`), nil
				}
				return chatTestResponse(200, `{"code":0,"data":{"is_in_chat":false}}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/chats/"):
				groupCalls++
				return chatTestResponse(200, `{"code":0,"data":{"name":"Policy Group","external":false,"tenant_key":"event-policy-tenant","chat_mode":"group"}}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/messages"):
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				messages = append(messages, map[string]any{"method": request.Method, "path": request.URL.Path, "query": request.URL.RawQuery, "body": body})
				return chatTestResponse(200, `{"code":0,"data":{"message_id":"event-policy-message","thread_id":"event-policy-thread"}}`), nil
			}
			t.Fatalf("unexpected event-policy request %s", request.URL.Path)
			return nil, nil
		})
		for _, test := range []struct {
			kind, variant string
			jobs, sends   int
			failure       bool
		}{
			{"queue", "issue", 1, 0, false},
			{"queue", "project", 1, 0, false},
			{"queue", "initiative", 1, 0, false},
			{"queue", "ask", 1, 0, false},
			{"queue", "ask-empty-first", 0, 0, false},
			{"queue", "ask-missing", 0, 0, false},
			{"queue", "no-bindings", 0, 0, false},
			{"queue", "disabled", 0, 0, false},
			{"queue", "future-binding", 0, 0, false},
			{"queue", "template-form", 0, 0, false},
			{"queue", "template-fields", 0, 0, false},
			{"queue", "template-duplicate-deny", 0, 0, false},
			{"queue", "template-missing", 0, 0, false},
			{"queue", "template-foreign-team", 0, 0, false},
			{"queue", "template-foreign-visibility", 0, 0, false},
			{"queue", "template-project-outside", 0, 0, false},
			{"queue", "hipaa", 0, 0, false},
			{"queue", "retired", 0, 0, false},
			{"queue", "request-comment", 1, 0, false},
			{"queue", "refresh-disabled", 0, 0, false},
			{"binding", "plain", 0, 0, false},
			{"binding", "template-duplicate-deny", 0, 0, true},
			{"binding", "private-allowed", 0, 0, false},
			{"binding", "authorizer-denied", 0, 0, true},
			{"binding", "membership-absent", 0, 0, true},
			{"binding", "retired", 0, 0, true},
			{"send", "named", 0, 1, false},
			{"send", "legacy-event", 0, 1, false},
			{"send", "health-fallback", 0, 1, false},
			{"send", "contributing-private", 0, 0, false},
			{"process", "result", 0, 1, false},
			{"process", "auth-expired", 0, 0, true},
			{"process", "unlinked", 0, 0, true},
			{"process", "stale-scope", 0, 0, false},
			{"process", "initiative", 0, 1, false},
		} {
			t.Run(test.kind+"/"+test.variant, func(t *testing.T) {
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.chat_event_adapter", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.HIPAACompliance = test.variant == "hipaa"
					for index := range data.Teams {
						if data.Teams[index].ID == f.public {
							data.Teams[index].Private = test.variant == "private-allowed" || test.variant == "authorizer-denied"
							data.Teams[index].RetiredAt = nil
							if test.variant == "retired" {
								data.Teams[index].RetiredAt = &at
							}
						}
					}
					data.TeamSettings[f.public] = domain.TeamSettings{TeamID: f.public, Timezone: "Asia/Shanghai"}
					data.UserSettings[actor.ID] = domain.UserSettings{Language: "zh-CN", Timezone: "Asia/Shanghai"}
					data.Projects = []domain.Project{{ID: projectID, Name: "Event project", SlugID: "event-project-slug", TeamIDs: []string{f.public}}}
					initiative := domain.Initiative{ID: initiativeID, Name: "Event initiative", SlugID: "event-initiative-slug", LeadTeamID: f.public, Health: "atRisk"}
					if test.variant == "contributing-private" {
						private := idgen.New("chat-event-private-")
						data.Teams = append(data.Teams, domain.Team{ID: private, Private: true})
						initiative.ContributingTeamIDs = []string{private}
					}
					data.Initiatives = []domain.Initiative{initiative}
					data.InitiativeUpdates[initiativeID] = []domain.InitiativeUpdate{
						{ID: "event-later", InitiativeID: initiativeID, Body: "Later event update", Health: "offTrack", User: actor, CreatedAt: at.Add(time.Minute)},
						{ID: "event-update", InitiativeID: initiativeID, Body: "Original event update", Health: "onTrack", User: actor, CreatedAt: at},
					}
					template := domain.IssueTemplate{ID: templateID, TeamID: f.public, TemplateType: "issue", Body: strings.Repeat("ignored template body ", 20000)}
					switch test.variant {
					case "template-form":
						template.TemplateType = "form"
					case "template-fields":
						template.FormFields = []domain.TemplateFormField{{Label: "A question"}}
					case "template-foreign-team":
						template.TeamID = "foreign-team"
					case "template-foreign-visibility":
						template.VisibilityTeamID = "foreign-team"
					case "template-project-outside":
						template.ProjectID = "foreign-project"
					}
					data.IssueTemplates = []domain.IssueTemplate{template}
					if test.variant == "template-duplicate-deny" {
						data.IssueTemplates = append(data.IssueTemplates, domain.IssueTemplate{ID: templateID, TemplateType: "form"})
					}
					if test.variant == "template-missing" {
						data.IssueTemplates = nil
					}
					data.Asks = []domain.Ask{{ID: askID, IssueID: issue.ID, Body: strings.Repeat("ignored ask body ", 20000)}}
					if test.variant == "ask-empty-first" {
						data.Asks = []domain.Ask{{ID: askID}, {ID: askID, IssueID: issue.ID}}
					}
					if test.variant == "ask-missing" {
						data.Asks = nil
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: idgen.New("chat-event-connection-"), AppID: idgen.New("chat-event-app-"), TenantKey: "event-policy-tenant", Connected: true}
				binding := domain.ChatBinding{ID: idgen.New("chat-event-binding-"), ConnectionID: record.ConnectionID, ChatID: "event-policy-group", TeamID: f.public, TemplateID: templateID, Enabled: true, AllowUpdate: true, CreatedAt: at.Add(-time.Minute), Events: []string{"issue.created", "project.update_created", "initiative.update_created"}}
				if test.variant == "disabled" {
					binding.Enabled = false
				}
				if test.variant == "future-binding" {
					binding.CreatedAt = at.Add(time.Minute)
				}
				if test.variant == "refresh-disabled" {
					binding.AllowUpdate = false
				}
				if test.variant == "private-allowed" || test.variant == "authorizer-denied" {
					binding.Authorization = &domain.ChatBindingAuthorization{UserID: f.data.Viewer.ID, PrivateTeam: true}
					if test.variant == "authorizer-denied" {
						binding.Authorization.UserID = actor.ID
					}
				}
				event := domain.DomainEvent{ID: idgen.New("chat-event-"), Type: "issue.created", AggregateID: issue.ID, CreatedAt: at}
				switch test.variant {
				case "project":
					event.Type, event.AggregateID = "project.update_created", projectID
				case "initiative":
					event.Type, event.AggregateID = "initiative.update_created", initiativeID
				case "ask", "ask-empty-first", "ask-missing":
					event.Type, event.AggregateID = "ask.decided", askID
				case "request-comment":
					event.Type = "comment.created"
				case "refresh-disabled":
					event.Type = "issue.updated"
				}
				event.Payload, _ = json.Marshal(childEventPayload{Child: &webhookChild{ID: "event-update"}})
				type outcome struct {
					Jobs       []domain.ChatJob
					Messages   []map[string]any
					GroupCalls int
					Error      string
				}
				var outputs []outcome
				t.Cleanup(func() {
					_ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID)
					_ = repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record))
				})
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if err := repo.PutChatIntegration(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					if test.variant != "no-bindings" {
						if err := repo.PutChatBinding(t.Context(), workspace, binding); err != nil {
							t.Fatal(err)
						}
					}
					credentials, _ := json.Marshal(chatCredentials{AppSecret: "event-policy-test-secret"})
					if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), credentials, time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
					if test.variant == "ask" || test.variant == "request-comment" {
						if _, _, err := repo.ReserveChatIntakeRequest(t.Context(), workspace, domain.ChatIntakeRequest{ConnectionID: record.ConnectionID, ChatID: binding.ChatID, BindingID: binding.ID, MessageID: "event-request-message", IssueID: issue.ID, AskID: askID, RequesterID: actor.ID, RequesterOpenID: "event-policy-open", Source: domain.ChatRequestSourceMention, Status: domain.ChatRequestCreated}); err != nil {
							t.Fatal(err)
						}
					}
					identity := domain.ChatIdentity{UserID: actor.ID, OpenID: "event-policy-open", TenantKey: record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
					if test.variant == "auth-expired" {
						identity.AuthenticatedAt = time.Now().UTC().Add(-40 * 24 * time.Hour)
					}
					if test.variant != "unlinked" {
						if err := repo.PutChatIdentity(t.Context(), workspace, record.ConnectionID, identity); err != nil {
							t.Fatal(err)
						}
					}
					messages, groupCalls, membership = nil, 0, test.variant != "membership-absent"
					service := &server{store: source}
					var err error
					switch test.kind {
					case "queue":
						err = service.queueChatDomainEvent(t.Context(), workspace, event)
						if err == nil {
							err = service.queueChatDomainEvent(t.Context(), workspace, event)
						}
					case "binding":
						err = service.chatBindingPermitted(t.Context(), record, binding, test.variant == "membership-absent")
					case "send":
						payload := chatNotificationJob{InitiativeID: initiativeID, UpdateID: "event-update", EventAt: at}
						if test.variant == "legacy-event" {
							payload.UpdateID = ""
						}
						if test.variant == "health-fallback" {
							payload.UpdateID = "missing"
						}
						err = service.chatSendInitiativeUpdate(t.Context(), record, binding, domain.ChatJob{DedupKey: "event-policy-dedup"}, payload)
					case "process":
						payload := chatNotificationJob{BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), IssueID: issue.ID, Kind: "result", PersonalOpenID: identity.OpenID, Operation: "update"}
						if test.variant == "stale-scope" {
							payload.BindingScope = "stale"
						}
						if test.variant == "initiative" {
							payload.Kind, payload.InitiativeID, payload.UpdateID = "initiative.update_created", initiativeID, "event-update"
						}
						raw, _ := json.Marshal(payload)
						err = service.processChatNotification(t.Context(), record, domain.ChatJob{DedupKey: "event-policy-dedup", Payload: raw})
					}
					result := outcome{Messages: messages, GroupCalls: groupCalls}
					if err != nil {
						result.Error = err.Error()
					}
					if (err != nil) != test.failure || len(messages) != test.sends {
						t.Fatalf("unexpected outcome error=%v sends=%d want_failure=%t sends=%d", err, len(messages), test.failure, test.sends)
					}
					result.Jobs, err = repo.ClaimChatJobs(t.Context(), workspace, "notification", 100, time.Now().Add(time.Second))
					if err != nil || len(result.Jobs) != test.jobs {
						t.Fatalf("jobs=%d want=%d err=%v", len(result.Jobs), test.jobs, err)
					}
					for index := range result.Jobs {
						result.Jobs[index].RunAt, result.Jobs[index].LeaseUntil = time.Time{}, time.Time{}
					}
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("event adapter leaked work")
					}
					outputs = append(outputs, result)
					if err := repo.DeleteChatIntegration(t.Context(), workspace, record.ConnectionID); err != nil {
						t.Fatal(err)
					}
					if err := repo.DeleteConnectorSecret(t.Context(), chatCredentialID(record)); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("event adapter output changed", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("event adapter decoded complete metadata")
		}
	}
}
