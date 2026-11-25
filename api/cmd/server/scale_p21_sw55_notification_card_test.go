package main

import (
	"bytes"
	"context"
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

func TestScaleP21SW55PersonalNotificationCardDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, actor := f.data.Workspace.URLKey, f.principals[2].user
		prefix := idgen.New("sw55-card-")
		record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, ConnectionID: prefix + "connection", Provider: "lark", AppID: prefix + "app"}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID) })
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "states", "teamSettings"), workspace, "scale_p21.sw55_workflow", "", nil, func(data *domain.Bootstrap) error {
			data.States = append(data.States,
				domain.WorkflowState{ID: prefix + "todo", TeamID: f.public, Name: "SW55 exact todo", Type: "unstarted", Default: true},
				domain.WorkflowState{ID: prefix + "done", TeamID: f.public, Name: "SW55 exact done", Type: "completed"})
			settings := data.TeamSettings[f.public]
			settings.DefaultStateID = prefix + "todo"
			data.TeamSettings[f.public] = settings
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
			map[string]any{"title": "SW55 visible notification issue", "teamId": f.public, "stateId": prefix + "todo", "assigneeId": actor.ID}, workspace, http.StatusCreated)
		metadata, found := repo.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("notification fixture metadata missing")
		}
		privateTeam := ""
		for _, project := range metadata.Projects {
			if project.ID == "m2-unrelated" {
				privateTeam = project.TeamIDs[0]
			}
		}
		if privateTeam == "" {
			t.Fatal("notification fixture private team missing")
		}
		privateAdmin := authClient(t)
		authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login",
			map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
		hidden := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records",
			map[string]any{"title": "SW55 hidden notification issue", "teamId": privateTeam}, workspace, http.StatusCreated)
		at := time.Date(2026, 11, 14, 6, 0, 0, 0, time.UTC)
		for _, language := range []struct{ name, saved, provider, want string }{
			{"english-over-feishu", "en-US", "feishu", "en"},
			{"chinese-over-lark", "zh-CN", "lark", "zh"},
			{"system-feishu", "system", "feishu", "zh"},
		} {
			for _, variant := range []string{"missing-issue", "review-precedence", "unauthorized-issue", "allowed-issue"} {
				t.Run(language.name+"/"+variant, func(t *testing.T) {
					notificationID := prefix + "notification"
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "userSettings", "notifications"), workspace, "scale_p21.sw55_notification", "", nil, func(data *domain.Bootstrap) error {
						data.UserSettings[actor.ID] = domain.UserSettings{UserID: actor.ID, Language: language.saved}
						notification := domain.Notification{ID: notificationID, RecipientID: actor.ID, IssueID: issue.ID,
							Type: "assignment", Title: "SW55 notification", Text: "SW55 exact notification line", Actor: f.data.Viewer, CreatedAt: at, UpdatedAt: at}
						switch variant {
						case "missing-issue":
							notification.IssueID = prefix + "missing"
						case "review-precedence":
							notification.ReviewID = prefix + "review"
						case "unauthorized-issue":
							notification.IssueID = hidden.ID
						}
						data.Notifications = []domain.Notification{notification}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					type outcome struct {
						Card    json.RawMessage
						Actions []domain.ChatAction
					}
					var outputs []outcome
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						func() {
							ctx, closeOutput := beginMetadataOutputReads(t.Context())
							defer closeOutput()
							localRecord := record
							localRecord.Provider = language.provider
							identity := domain.ChatIdentity{UserID: actor.ID, OpenID: prefix + "open", AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
							started := time.Now().UTC()
							card, actions, err := (&server{store: source}).chatPersonalNotificationCard(ctx, localRecord, identity,
								chatPersonalJob{UserID: actor.ID, NotificationID: notificationID},
								notificationEmailItem{Title: "SW55 card title", Line: "SW55 exact card line", URL: "https://flow.example.test/sw55"},
								domain.ChatJob{DedupKey: prefix + language.name + variant})
							if err != nil {
								t.Fatal(err)
							}
							if source == f.probe && source.MetadataWorkUsedForTesting() == 0 {
								t.Fatal("returned notification card lost output ownership before encoding")
							}
							raw, err := json.Marshal(card)
							if err != nil || !strings.Contains(string(raw), chatT(language.want, "open_in_flow")) {
								t.Fatal("notification card lost recipient language", err)
							}
							wantActions := 0
							if variant == "allowed-issue" {
								wantActions = 3
								if !strings.Contains(string(raw), issue.Title) || !strings.Contains(string(raw), prefix+"todo") {
									t.Fatal("allowed notification card lost its issue or workflow selector")
								}
							}
							if len(actions) != wantActions || strings.Contains(string(raw), hidden.Title) {
								t.Fatal("notification card exposed hidden issue data or changed controls", len(actions))
							}
							for index := range actions {
								action := &actions[index]
								stored, err := source.GetChatAction(ctx, workspace, record.ConnectionID, action.ID)
								a, _ := json.Marshal(action)
								b, _ := json.Marshal(stored)
								if err != nil || !bytes.Equal(a, b) || action.ExpiresAt.Before(started.Add(14*24*time.Hour)) || action.ExpiresAt.After(time.Now().UTC().Add(14*24*time.Hour)) {
									t.Fatal("notification card durable action changed", err)
								}
								if consumed, err := source.ConsumeChatAction(ctx, workspace, record.ConnectionID, action.ID); err != nil || !consumed {
									t.Fatal("notification card action was not live", consumed, err)
								}
								action.ExpiresAt = time.Time{}
							}
							outputs = append(outputs, outcome{Card: raw, Actions: actions})
						}()
						if source.MetadataWorkUsedForTesting() != 0 || source == f.probe && source.WorkspaceMetadataDecodes() != 0 {
							t.Fatal("notification card leaked work or decoded full workspace metadata")
						}
					}
					if !reflect.DeepEqual(outputs[0], outputs[1]) {
						t.Fatal("actual personal notification card differential", scaleP21M2Diff(outputs[0], outputs[1]))
					}
				})
			}
		}
	}
}
