package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotChatSourcesQueueDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		initiativeID := idgen.New("chat-source-")
		at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		for _, test := range []struct {
			name string
			want int
		}{
			{"workspace-owner", 1}, {"public-lead", 1}, {"admin-authorizer", 1},
			{"hipaa", 0}, {"private-lead", 0}, {"retired-lead", 0}, {"private-parent", 0},
			{"missing-initiative", 0}, {"disabled", 0}, {"revoked", 0},
			{"before-binding", 0}, {"former-owner", 0}, {"guest-authorizer", 0},
			{"missing-authorizer", 0}, {"wrong-event", 0},
		} {
			t.Run(test.name, func(t *testing.T) {
				teamID := idgen.New("chat-team-")
				owner := f.principals[2].user
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.chat_source_seed", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.HIPAACompliance = test.name == "hipaa"
					team := domain.Team{ID: teamID, Private: test.name == "private-lead"}
					if test.name == "retired-lead" {
						team.RetiredAt = &at
					}
					data.Teams = append(data.Teams, team)
					settings := domain.TeamSettings{}
					if test.name == "private-parent" {
						settings.ParentTeamID = teamID + "-parent"
						data.Teams = append(data.Teams, domain.Team{ID: settings.ParentTeamID, Private: true})
					}
					data.TeamSettings[teamID] = settings
					initiative := domain.Initiative{ID: initiativeID, Name: "Queued initiative", Owner: &owner}
					if test.name == "public-lead" || test.name == "private-lead" || test.name == "retired-lead" || test.name == "private-parent" {
						initiative.LeadTeamID = teamID
					}
					data.Initiatives = []domain.Initiative{initiative}
					if test.name == "missing-initiative" {
						data.Initiatives = nil
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: idgen.New("chat-source-connection-"), AppID: "chat-source-app", TenantKey: "chat-source-tenant", Connected: true}
				binding := domain.ChatInitiativeBinding{ID: idgen.New("chat-source-binding-"), ConnectionID: record.ConnectionID, ChatID: "chat-source-group", InitiativeID: initiativeID, Enabled: test.name != "disabled", CreatedAt: at.Add(-time.Minute), Authorization: &domain.ChatInitiativeBindingAuthorization{UserID: owner.ID}}
				switch test.name {
				case "admin-authorizer":
					binding.Authorization.UserID = f.data.Viewer.ID
				case "former-owner":
					binding.Authorization.UserID = f.principals[4].user.ID
				case "guest-authorizer":
					binding.Authorization.UserID = f.principals[3].user.ID
				case "missing-authorizer":
					binding.Authorization = nil
				case "revoked":
					binding.RevokedAt = &at
				case "before-binding":
					binding.CreatedAt = at.Add(time.Minute)
				}
				payload, _ := json.Marshal(childEventPayload{Child: &webhookChild{ID: "exact-update"}})
				event := domain.DomainEvent{ID: idgen.New("chat-source-event-"), Type: "initiative.update_created", AggregateID: initiativeID, Payload: payload, CreatedAt: at}
				if test.name == "wrong-event" {
					event.Type = "initiative.updated"
				}
				var outputs [][]domain.ChatJob
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if err := repo.PutChatIntegration(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					if err := repo.PutChatInitiativeBinding(t.Context(), workspace, binding); err != nil {
						t.Fatal(err)
					}
					if err := (&server{store: source}).queueChatInitiativeBindingEvent(t.Context(), workspace, record, event); err != nil {
						t.Fatal(err)
					}
					jobs, err := repo.ClaimChatJobs(t.Context(), workspace, "notification", 100, time.Now().Add(time.Second))
					if err != nil || len(jobs) != test.want {
						t.Fatalf("queued jobs=%d want=%d err=%v", len(jobs), test.want, err)
					}
					for index := range jobs {
						jobs[index].RunAt, jobs[index].LeaseUntil = time.Time{}, time.Time{}
					}
					outputs = append(outputs, jobs)
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("queued output leaked metadata work")
					}
					if err := repo.DeleteChatIntegration(t.Context(), workspace, record.ConnectionID); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("exact queued job changed", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded chat queue decoded full metadata")
		}
	}
}

func TestScaleP21HotChatSourcesRenderingDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		projectID, initiativeID := idgen.New("chat-project-"), idgen.New("chat-initiative-")
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Chat personal source", "teamId": f.public}, workspace, http.StatusCreated)
		at := time.Date(2026, 11, 13, 12, 13, 14, 0, time.UTC)
		actor := f.principals[2].user
		actor.DisplayName = "Stale actor"
		var sent []map[string]any
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(request.URL.Path, "tenant_access_token"):
				return chatTestResponse(200, `{"code":0,"tenant_access_token":"chat-source-token","expire":7200}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/chats/"):
				return chatTestResponse(200, `{"code":0,"data":{"external":false,"tenant_key":"chat-source-tenant","chat_mode":"group"}}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/messages"):
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				sent = append(sent, map[string]any{"method": request.Method, "path": request.URL.Path, "query": request.URL.RawQuery, "body": body})
				return chatTestResponse(200, `{"code":0,"data":{"message_id":"exact-message","thread_id":"exact-platform-thread"}}`), nil
			}
			t.Fatalf("unexpected chat request %s", request.URL.Path)
			return nil, nil
		})
		for _, test := range []struct {
			kind, variant string
			messages      int
			code          string
		}{
			{"project", "created", 1, ""}, {"project", "legacy-event", 1, ""},
			{"project", "edited", 1, ""}, {"project", "deleted", 1, ""},
			{"project", "private", 0, ""}, {"project", "missing-update", 0, ""},
			{"initiative", "created", 1, ""}, {"initiative", "legacy-event", 1, ""},
			{"initiative", "health-fallback", 1, ""}, {"initiative", "private", 0, ""},
			{"personal", "issue", 0, ""}, {"personal", "review-precedence", 0, ""},
			{"personal", "title", 0, ""}, {"personal", "missing-review", 0, chatPersonalNoAccess},
			{"personal", "hipaa", 0, chatPersonalSharingBlocked},
			{"personal", "unknown-user", 0, chatPersonalNoAccess},
		} {
			t.Run(test.kind+"/"+test.variant, func(t *testing.T) {
				notificationID := idgen.New("chat-notification-")
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.chat_render_seed", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.HIPAACompliance = test.variant == "hipaa"
					for index := range data.Users {
						if data.Users[index].ID == actor.ID {
							data.Users[index].DisplayName = "Current directory name"
						}
					}
					data.UserSettings[actor.ID] = domain.UserSettings{Language: "zh-CN", Timezone: "Asia/Shanghai"}
					data.TeamSettings[f.public] = domain.TeamSettings{TeamID: f.public, Timezone: "America/Los_Angeles"}
					team := f.public
					if test.variant == "private" {
						team = idgen.New("chat-private-")
						data.Teams = append(data.Teams, domain.Team{ID: team, Private: true})
					}
					data.Projects = []domain.Project{{ID: projectID, Name: "Exact project", SlugID: "exact-project-slug", TeamIDs: []string{team}}}
					data.Initiatives = []domain.Initiative{{ID: initiativeID, Name: "Exact initiative", SlugID: "exact-initiative-slug", LeadTeamID: team, Owner: &actor, Health: "atRisk"}}
					data.ProjectUpdates[projectID] = []domain.ProjectUpdate{
						{ID: "newer-update", ProjectID: projectID, Body: "Newer update", Health: "offTrack", User: actor, CreatedAt: at.Add(time.Hour)},
						{ID: "exact-update", ProjectID: projectID, Body: "Exact body", BodyData: map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "mention", "attrs": map[string]any{"id": actor.ID, "label": "Stale mention", "mentionType": "user"}}}}}}, Health: "onTrack", User: actor, CreatedAt: at, EditedAt: &at},
					}
					data.InitiativeUpdates[initiativeID] = []domain.InitiativeUpdate{
						{ID: "newer-update", InitiativeID: initiativeID, Body: "Newer initiative", User: actor, CreatedAt: at.Add(time.Hour)},
						{ID: "exact-update", InitiativeID: initiativeID, Body: "Exact initiative body", Health: "offTrack", User: actor, CreatedAt: at},
					}
					notification := domain.Notification{ID: notificationID, RecipientID: actor.ID, Type: "assignment", IssueID: issue.ID, Actor: actor, CreatedAt: at}
					switch test.variant {
					case "review-precedence":
						notification.ReviewID = "exact-review-slug"
					case "missing-review":
						notification.ReviewID = "missing-review"
					case "title":
						notification.IssueID, notification.Title, notification.Text = "", "Exact standalone title", "Exact standalone line"
					}
					data.Notifications = append(data.Notifications, notification)
					data.Reviews = []domain.CodeReview{{ID: "exact-review", SlugID: "exact-review-slug", Title: "Exact review title"}}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: idgen.New("chat-render-connection-"), AppID: idgen.New("chat-render-app-"), TenantKey: "chat-source-tenant", Connected: true}
				t.Cleanup(func() {
					if err := repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID); err != nil {
						t.Error(err)
					}
					if err := repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record)); err != nil {
						t.Error(err)
					}
				})
				binding := domain.ChatBinding{ID: idgen.New("chat-render-binding-"), ConnectionID: record.ConnectionID, ChatID: "chat-render-group", TeamID: f.public, Enabled: true}
				initiativeBinding := domain.ChatInitiativeBinding{ID: binding.ID, ConnectionID: record.ConnectionID, ChatID: binding.ChatID, InitiativeID: initiativeID, Enabled: true, Authorization: &domain.ChatInitiativeBindingAuthorization{UserID: actor.ID}}
				payload := chatNotificationJob{Event: "project.update_created", ProjectID: projectID, InitiativeID: initiativeID, InitiativeBindingID: binding.ID, UpdateID: "exact-update", EventAt: at.Add(time.Minute)}
				switch test.variant {
				case "legacy-event":
					payload.UpdateID = ""
				case "edited":
					payload.Event = "project.update_updated"
				case "deleted":
					payload.Event = "project.update_deleted"
				case "missing-update", "health-fallback":
					payload.UpdateID = "missing-update"
				}
				type outcome struct {
					Messages []map[string]any
					Item     notificationEmailItem
					Code     string
					Thread   domain.ChatThread
				}
				var outputs []outcome
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					if err := repo.PutChatIntegration(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					if err := repo.PutChatInitiativeBinding(t.Context(), workspace, initiativeBinding); err != nil {
						t.Fatal(err)
					}
					credentials, _ := json.Marshal(chatCredentials{AppSecret: "chat-test-secret"})
					if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), credentials, time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
					if test.variant == "edited" || test.variant == "deleted" {
						if _, err := repo.PutChatThread(t.Context(), workspace, domain.ChatThread{ID: "exact-stored-thread", ConnectionID: record.ConnectionID, ChatID: binding.ChatID, ResourceType: chatThreadResourceProjectUpdate, ResourceID: payload.UpdateID, RootMessageID: "exact-message", BindingID: binding.ID}); err != nil {
							t.Fatal(err)
						}
					}
					sent = nil
					result := outcome{}
					service := &server{store: source}
					var releases []func()
					ctx := store.WithMetadataOutputOwner(t.Context(), func(close func()) bool {
						releases = append(releases, close)
						return true
					})
					var err error
					switch test.kind {
					case "project":
						err = service.chatSendProjectUpdate(ctx, record, binding, domain.ChatJob{DedupKey: "exact-dedup"}, payload)
						updateID := payload.UpdateID
						if updateID == "" {
							updateID = "exact-update"
						}
						thread, lookupErr := repo.ChatThread(ctx, workspace, record.ConnectionID, binding.ChatID, chatThreadResourceProjectUpdate, updateID)
						if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
							t.Fatal(lookupErr)
						}
						thread.ID, thread.CreatedAt, thread.UpdatedAt, thread.SyncFrom = "", time.Time{}, time.Time{}, time.Time{}
						result.Thread = thread
					case "initiative":
						err = service.chatSendInitiativeBindingUpdate(ctx, record, domain.ChatJob{DedupKey: "exact-dedup"}, payload)
					case "personal":
						userID := actor.ID
						if test.variant == "unknown-user" {
							userID = "missing-chat-user"
						}
						result.Item, result.Code, err = service.chatPersonalNotificationItem(ctx, record, chatPersonalJob{UserID: userID, NotificationID: notificationID})
						if err == nil && source == f.probe && source.MetadataWorkUsedForTesting() == 0 {
							t.Fatal("personal by-value result released before encoding")
						}
						if _, encodeErr := json.Marshal(result.Item); encodeErr != nil {
							t.Fatal(encodeErr)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					result.Messages = sent
					for _, release := range releases {
						release()
					}
					if len(result.Messages) != test.messages || result.Code != test.code {
						t.Fatalf("output messages=%d code=%s want=%d/%s", len(result.Messages), result.Code, test.messages, test.code)
					}
					outputs = append(outputs, result)
					if source.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("rendered output leaked metadata work")
					}
					if err := repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID); err != nil {
						t.Fatal(err)
					}
					if err := repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record)); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("exact rendered output changed", scaleP21M2Diff(outputs[0], outputs[1]))
				}
				if test.variant == "created" && test.kind == "project" && !strings.Contains(fmt.Sprint(outputs[1].Messages), "Current directory name") {
					t.Fatal("fixture did not exercise installed directory rendering")
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded chat rendering decoded full metadata")
		}
	}
}
