package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW42NotificationPointDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{61}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, actor := f.data.Workspace.URLKey, f.principals[2].user
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
			map[string]any{"title": "Exact notification point issue", "teamId": f.public, "description": strings.Repeat("Complete selected issue body. ", 10000)}, workspace, http.StatusCreated)
		at := time.Date(2026, 11, 14, 6, 0, 0, 0, time.UTC)
		var active *store.SQLiteStore
		var messages []map[string]any
		var failSend, expectHeld bool
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(request.URL.Path, "tenant_access_token"):
				return chatTestResponse(http.StatusOK, `{"code":0,"tenant_access_token":"sw42-notification-token","expire":7200}`), nil
			case strings.HasSuffix(request.URL.Path, "/members/is_in_chat"):
				return chatTestResponse(http.StatusOK, `{"code":0,"data":{"is_in_chat":true}}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/chats/"):
				return chatTestResponse(http.StatusOK, `{"code":0,"data":{"external":false,"tenant_key":"sw42-notification-tenant","chat_mode":"group"}}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/messages"):
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if active == f.probe && expectHeld && active.MetadataWorkUsedForTesting() == 0 {
					t.Error("point result or derived error outlived its output owner")
				}
				messages = append(messages, map[string]any{"method": request.Method, "path": request.URL.Path, "query": request.URL.RawQuery, "body": body})
				if failSend {
					return chatTestResponse(http.StatusServiceUnavailable, `{"code":999,"msg":"Retry notification output"}`), nil
				}
				return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":"sw42-notification-message","chat_id":"sw42-personal-chat"}}`), nil
			}
			t.Fatalf("unexpected notification platform request %s", request.URL.Path)
			return nil, nil
		})
		cases := []struct {
			kind, variant string
			sends         int
			failure, held bool
		}{
			{"result", "success", 1, false, true},
			{"result", "expired", 0, true, false},
			{"result", "unlinked", 0, true, false},
			{"result", "stale-scope", 0, false, false},
			{"result", "send-failure", 1, true, true},
			{"result", "closed-owner", 1, false, true},
			{"card", "missing-issue", 0, false, true},
			{"card", "expired", 0, false, true},
			{"card", "review-precedence", 0, false, false},
			{"card", "missing-notification", 0, false, false},
			{"personal-worker", "expired", 1, false, true},
			{"personal-worker", "standalone", 1, false, true},
			{"personal-worker", "send-failure", 1, true, true},
			{"quick", "invalid-priority", 1, false, true},
			{"quick", "identifier", 1, false, true},
			{"quick", "expired", 1, false, true},
			{"quick", "missing-issue", 1, false, true},
			{"quick", "wrong-user", 1, false, false},
		}
		for _, test := range cases {
			t.Run(test.kind+"/"+test.variant, func(t *testing.T) {
				record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, ConnectionID: idgen.New("sw42-notification-connection-"),
					Provider: "lark", AppID: "sw42-notification-app", TenantKey: "sw42-notification-tenant", Connected: true}
				binding := domain.ChatBinding{ID: "sw42-notification-binding", ConnectionID: record.ConnectionID, ChatID: "sw42-notification-group",
					TeamID: f.public, Enabled: true, CreatedAt: at.Add(-time.Hour)}
				identity := domain.ChatIdentity{UserID: actor.ID, OpenID: "sw42-notification-open", TenantKey: record.TenantKey,
					AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
				if test.variant == "expired" || test.kind == "personal-worker" && test.variant == "send-failure" {
					identity.AuthenticatedAt = time.Now().UTC().Add(-40 * 24 * time.Hour)
				}
				notificationID, deliveryID := idgen.New("sw42-notification-"), idgen.New("sw42-delivery-")
				t.Cleanup(func() {
					_ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID)
					_ = repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record))
				})
				type outcome struct {
					Messages []map[string]any
					Card     any
					Actions  any
					Delivery any
					Error    string
				}
				var outputs []outcome
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw42_notification_fixture", "", nil, func(data *domain.Bootstrap) error {
						data.UserSettings[actor.ID] = domain.UserSettings{Language: "system", Timezone: "UTC"}
						notification := domain.Notification{ID: notificationID, RecipientID: actor.ID, IssueID: issue.ID, Type: "assignment",
							Title: "Exact notification title", Text: "Exact notification text", Actor: f.data.Viewer, CreatedAt: at, UpdatedAt: at}
						if test.variant == "missing-issue" {
							notification.IssueID = "sw42-missing-issue"
						}
						if test.variant == "review-precedence" {
							notification.ReviewID = "sw42-review"
						}
						if test.variant == "standalone" {
							notification.IssueID = ""
						}
						data.Notifications = []domain.Notification{notification}
						if test.variant == "missing-notification" {
							data.Notifications = nil
						}
						data.NotificationDeliveries = []domain.NotificationDelivery{{ID: deliveryID, NotificationID: notificationID,
							RecipientID: actor.ID, Channel: "chat", Status: "delivering", CreatedAt: at, UpdatedAt: at}}
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
					if err := repo.PutChatBinding(t.Context(), workspace, binding); err != nil {
						t.Fatal(err)
					}
					if test.variant != "unlinked" {
						if err := repo.PutChatIdentity(t.Context(), workspace, record.ConnectionID, identity); err != nil {
							t.Fatal(err)
						}
					}
					secret, _ := json.Marshal(chatCredentials{AppSecret: "sw42-notification-secret"})
					if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), secret, time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
					metadata, found := repo.WorkspaceMetadata(workspace)
					if !found {
						t.Fatal("caller metadata fixture missing")
					}
					metadata.Viewer, metadata.ViewerRole = actor, "member"
					service := &server{store: source, allowedOrigin: "https://flow.example.test"}
					active, messages, failSend, expectHeld = source, nil, test.variant == "send-failure", test.held
					result := outcome{}
					var err error
					ctx := withChatIdentityAuthentication(t.Context(), identity)
					switch test.kind {
					case "result":
						payload := chatNotificationJob{BindingID: binding.ID, BindingScope: chatBindingFingerprint(binding), IssueID: issue.ID,
							Kind: "result", PersonalOpenID: identity.OpenID, Operation: "update"}
						if test.variant == "stale-scope" {
							payload.BindingScope = "stale"
						}
						raw, _ := json.Marshal(payload)
						call := t.Context()
						if test.variant == "closed-owner" {
							var close func()
							call, close = beginMetadataOutputReads(call)
							close()
						}
						err = service.processChatNotification(call, record, domain.ChatJob{DedupKey: "sw42-notification-dedup", Payload: raw})
						if source == f.probe && test.variant == "closed-owner" && !errors.Is(err, store.ErrBoundedMetadataCoverage) {
							t.Fatal("point coverage failure was hidden as access refusal", err)
						}
					case "card":
						owned, close := beginMetadataOutputReads(ctx)
						card, actions, failure := service.chatPersonalNotificationCard(owned, record, identity,
							chatPersonalJob{UserID: actor.ID, NotificationID: notificationID},
							notificationEmailItem{Title: "Exact card title", Line: "Exact card line", URL: "https://flow.example.test/card"},
							domain.ChatJob{DedupKey: "sw42-notification-dedup"})
						err = failure
						if source == f.probe && test.held && source.MetadataWorkUsedForTesting() == 0 {
							t.Fatal("returned card/error lost point ownership before encoding")
						}
						result.Card, result.Actions = scaleP21M2JSON(t, card, false), scaleP21M2JSON(t, actions, false)
						close()
					case "personal-worker":
						raw, _ := json.Marshal(chatPersonalJob{DeliveryID: deliveryID, NotificationID: notificationID, UserID: actor.ID, OpenID: identity.OpenID})
						err = service.processChatPersonalNotification(t.Context(), record, domain.ChatJob{DedupKey: "sw42-notification-dedup", Payload: raw})
						deliveries, lookup := repo.NotificationDeliveries(t.Context(), workspace, []string{"delivering", "delivered", "failed"}, time.Now().Add(time.Hour))
						if lookup != nil {
							t.Fatal(lookup)
						}
						for _, delivery := range deliveries.NotificationDeliveries {
							if delivery.ID == deliveryID {
								delivery.DeliveredAt = nil
								result.Delivery = scaleP21M2JSON(t, delivery, false)
							}
						}
					case "quick":
						action := chatExtendedAction{chatCardAction: chatCardAction{Kind: chatQuickPriority, IssueID: issue.ID}}
						if test.variant == "identifier" {
							action.IssueID = issue.Identifier
						}
						if test.variant == "missing-issue" {
							action.IssueID = "sw42-missing-issue"
						}
						raw, _ := json.Marshal(action)
						userID := actor.ID
						if test.variant == "wrong-user" {
							userID = "other-user"
						}
						err = service.chatRunQuickAction(ctx, record, identity, metadata, domain.ChatJob{DedupKey: "sw42-notification-dedup"},
							chatInboundCommand{Values: map[string]json.RawMessage{"option": json.RawMessage(`"9"`)}},
							domain.ChatAction{UserID: userID, Payload: raw})
					}
					if err != nil {
						result.Error = err.Error()
					}
					result.Messages = messages
					wantSends, wantFailure := test.sends, test.failure
					if source == f.probe && test.variant == "closed-owner" {
						wantSends, wantFailure = 0, true
					}
					if len(messages) != wantSends || (err != nil) != wantFailure {
						t.Fatalf("notification point output changed sends=%d want=%d error=%v", len(messages), wantSends, err)
					}
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("notification point operation leaked output admission")
					}
					outputs = append(outputs, result)
					if err := repo.DeleteChatIntegration(t.Context(), workspace, record.ConnectionID); err != nil {
						t.Fatal(err)
					}
				}
				if test.variant != "closed-owner" && !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("notification point differential", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("notification point adapters decoded complete workspace metadata")
		}
	}
}
