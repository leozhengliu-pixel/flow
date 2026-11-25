package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW53ChatWorkflowCardsDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw53-workflow-")
		teamID, parentID, middleID := f.data.Teams[0].ID, prefix+"parent", prefix+"middle"
		record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, ConnectionID: prefix + "connection", Provider: "lark", AppID: prefix + "app"}
		if err := repo.PutChatIntegration(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID) })
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		type optionResult struct {
			Options []map[string]any
			IDs     []string
			Default string
		}
		type outcome struct {
			Options json.RawMessage
			Cards   []json.RawMessage
			Actions [][]domain.ChatAction
		}
		for _, test := range []struct {
			name, wantDefault, selected, language string
			wantOptions                           int
		}{
			{name: "direct-configured-default", wantDefault: "zero", selected: "active", wantOptions: 3},
			{name: "inherited-two-hops", wantDefault: "zero", selected: "active", wantOptions: 3},
			{name: "global-fallback", wantDefault: "global", selected: "global", wantOptions: 1},
			{name: "missing-parent-global", wantDefault: "global", selected: "global", wantOptions: 1},
			{name: "missing-settings-synthesized", wantDefault: "done", selected: "active", wantOptions: 3},
			{name: "saved-zero-settings", wantDefault: "done", selected: "active", wantOptions: 3},
			{name: "saved-zero-inherited", wantDefault: "done", selected: "active", wantOptions: 3},
			{name: "equal-position-name-ties", wantDefault: "tie-first", selected: "tie-first", wantOptions: 2},
			{name: "duplicate-state-ids", wantDefault: "duplicate", selected: "duplicate", wantOptions: 3},
			{name: "empty-state-id", wantDefault: "after-empty", selected: "after-empty", wantOptions: 3},
			{name: "over100-synthesized-default-outside", wantDefault: "many-002", selected: "many-104", wantOptions: 100},
			{name: "over100-configured-default-outside", wantDefault: "many-002", selected: "many-104", wantOptions: 100},
			{name: "workflow-cycle", selected: "active"},
			{name: "nil-states", selected: "active"},
			{name: "empty-states", selected: "active"},
			{name: "estimate-inheritance", wantDefault: "zero", selected: "active", wantOptions: 3},
			{name: "chinese", wantDefault: "zero", selected: "active", language: "zh-CN", wantOptions: 3},
			{name: "provider-language", wantDefault: "zero", selected: "active", language: "system", wantOptions: 3},
		} {
			t.Run(test.name, func(t *testing.T) {
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "states", "teamSettings", "users", "userSettings"), workspace, "scale_p21.sw53_workflow", "", nil, func(data *domain.Bootstrap) error {
					data.States = []domain.WorkflowState{
						{ID: "done", TeamID: teamID, Name: "Done", Position: 2, Default: true},
						{ID: "global", Name: "Global", Default: true},
						{ID: "zero", TeamID: teamID, Name: "First", Position: 0},
						{ID: "active", TeamID: teamID, Name: "Active <state>\x00\u51c6", Position: 1, Default: true},
						{ID: "foreign", TeamID: prefix + "unrelated", Name: "Unrelated", Position: -1},
					}
					data.TeamSettings = map[string]domain.TeamSettings{
						teamID: {TeamID: teamID, DefaultStateID: "zero"},
					}
					switch test.name {
					case "inherited-two-hops", "saved-zero-inherited":
						for index := range data.States {
							if data.States[index].TeamID == teamID {
								data.States[index].TeamID = parentID
							}
						}
						data.TeamSettings[teamID] = domain.TeamSettings{TeamID: teamID, ParentTeamID: middleID, InheritWorkflowStatuses: true, DefaultStateID: "zero"}
						data.TeamSettings[middleID] = domain.TeamSettings{TeamID: middleID, ParentTeamID: parentID, InheritWorkflowStatuses: true}
						data.TeamSettings[parentID] = domain.TeamSettings{TeamID: parentID, DefaultStateID: "active"}
						if test.name == "saved-zero-inherited" {
							settings := data.TeamSettings[teamID]
							settings.TeamID = ""
							data.TeamSettings[teamID] = settings
						}
					case "global-fallback":
						data.States = data.States[1:2]
					case "missing-parent-global":
						data.TeamSettings[teamID] = domain.TeamSettings{TeamID: teamID, ParentTeamID: parentID, InheritWorkflowStatuses: true}
					case "missing-settings-synthesized":
						delete(data.TeamSettings, teamID)
					case "saved-zero-settings":
						data.TeamSettings[teamID] = domain.TeamSettings{DefaultStateID: "zero"}
					case "equal-position-name-ties":
						data.States = []domain.WorkflowState{
							{ID: "tie-second", TeamID: teamID, Name: "Same", Position: 7},
							{ID: "tie-first", TeamID: teamID, Name: "Same", Position: 7, Default: true},
						}
					case "duplicate-state-ids":
						data.States = []domain.WorkflowState{
							{ID: "duplicate", TeamID: teamID, Name: "First occurrence"},
							{ID: "duplicate", TeamID: teamID, Name: "Second occurrence", Position: 1, Default: true},
							{ID: "last", TeamID: teamID, Name: "Last", Position: 2},
						}
					case "empty-state-id":
						data.States = []domain.WorkflowState{
							{TeamID: teamID, Name: "Empty", Default: true},
							{ID: "after-empty", TeamID: teamID, Name: "After empty", Position: 1},
							{ID: "last", TeamID: teamID, Name: "Last", Position: 2},
						}
					case "over100-synthesized-default-outside", "over100-configured-default-outside":
						data.States = nil
						for index := 104; index >= 0; index-- {
							data.States = append(data.States, domain.WorkflowState{ID: fmt.Sprintf("many-%03d", index), TeamID: teamID, Name: fmt.Sprintf("State %03d", index), Position: float64(index), Default: index == 2 || index == 104})
						}
						if test.name == "over100-synthesized-default-outside" {
							delete(data.TeamSettings, teamID)
						} else {
							data.TeamSettings[teamID] = domain.TeamSettings{TeamID: teamID, DefaultStateID: "many-104"}
						}
					case "workflow-cycle":
						data.TeamSettings[teamID] = domain.TeamSettings{TeamID: teamID, ParentTeamID: parentID, InheritWorkflowStatuses: true}
						data.TeamSettings[parentID] = domain.TeamSettings{TeamID: parentID, ParentTeamID: teamID, InheritWorkflowStatuses: true}
					case "nil-states":
						data.States = nil
					case "empty-states":
						data.States = []domain.WorkflowState{}
					case "estimate-inheritance":
						settings := data.TeamSettings[teamID]
						settings.ParentTeamID, settings.InheritIssueEstimation = parentID, true
						data.TeamSettings[teamID] = settings
						data.TeamSettings[parentID] = domain.TeamSettings{TeamID: parentID, EstimateType: "fibonacci"}
					}
					data.Users = []domain.User{
						{ID: "sw53-assignee", Name: "Stored ID", DisplayName: "Current <person>\x00\u51c6"},
						{ID: "sw53-assignee", DisplayName: "Later duplicate"},
					}
					data.UserSettings[f.data.Viewer.ID] = domain.UserSettings{Language: test.language}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				localRecord := record
				if test.language == "system" {
					localRecord.Provider = "feishu"
				}
				binding := domain.ChatBinding{ID: prefix + "binding", TeamID: teamID, ChatID: "group", AllowUpdate: true, Language: test.language}
				identity := domain.ChatIdentity{UserID: f.data.Viewer.ID, OpenID: "sw53-open"}
				issue := domain.Issue{ID: prefix + "issue", Identifier: "WORKFLOW / <1>", Title: "Exact card <title>\x00\u51c6", Version: 17, Priority: 2,
					Team: domain.Team{ID: teamID, Name: "Real team"}, State: domain.WorkflowState{ID: test.selected, Name: "Displayed state"},
					Assignee: &domain.User{ID: "sw53-assignee", Name: "Stale name"}}
				var outputs []outcome
				for _, source := range []*store.SQLiteStore{repo, probe} {
					var got outcome
					func() {
						ctx, closeOutput := beginMetadataOutputReads(t.Context())
						defer closeOutput()
						var data domain.Bootstrap
						if source == probe {
							read, found, err := source.BeginChatDisplayRead(ctx, workspace, store.ChatDisplayRequest{})
							if err != nil || !found {
								t.Fatal(found, err)
							}
							defer read.Close()
							if err := read.LoadWorkflow(ctx, teamID); err != nil {
								t.Fatal(err)
							}
							data = read.Data
						} else {
							data, _ = source.WorkspaceMetadataFields(workspace, "states", "teamSettings")
						}
						options, ids, defaultID := chatStateOptions(data, teamID)
						if len(options) != test.wantOptions || defaultID != test.wantDefault {
							t.Fatal("workflow options/default changed", len(options), defaultID)
						}
						var err error
						got.Options, err = json.Marshal(optionResult{options, ids, defaultID})
						if err != nil {
							t.Fatal(err)
						}
					}()
					service := &server{store: source, allowedOrigin: "https://flow.example.test"}
					for _, kind := range []string{"group", "personal"} {
						func() {
							ctx, closeOutput := beginMetadataOutputReads(t.Context())
							defer closeOutput()
							heldAtWrite := true
							ctx = store.WithSQLStatementObserver(ctx, func(query string) {
								if source == probe && strings.HasPrefix(query, "INSERT INTO chat_actions") && source.MetadataWorkUsedForTesting() == 0 {
									heldAtWrite = false
								}
							})
							key, started := prefix+test.name+kind, time.Now().UTC()
							var card map[string]any
							var actions []domain.ChatAction
							var err error
							if kind == "group" {
								card, actions, err = service.chatIssueCard(ctx, localRecord, binding, issue, key, "shared-message", "source-message")
							} else {
								card, actions, err = service.chatPersonalIssueCard(ctx, localRecord, identity, issue, chatExtendedAction{
									chatCardAction: chatCardAction{MessageID: "personal-message", SourceMessageID: "source-message"},
									CardKey:        key, CardTitle: "Personal <title>", CardLine: "Notification line", CardURL: "https://flow.example.test/issue"})
							}
							if err != nil {
								t.Fatal(err)
							}
							count := 3
							if kind == "group" {
								count = 4
							}
							if len(actions) != count || !heldAtWrite || source == probe && source.MetadataWorkUsedForTesting() == 0 {
								t.Fatal("enabled card actions or output ownership changed", len(actions))
							}
							elements := card["elements"].([]map[string]any)
							controls := elements[len(elements)-1]["actions"].([]map[string]any)
							wantControls := 2
							if test.wantOptions > 0 {
								wantControls = 3
							}
							if len(controls) != wantControls {
								t.Fatal("actual enabled card omitted or added a workflow selector")
							}
							if test.wantOptions > 0 {
								status := controls[1]
								if len(status["options"].([]map[string]any)) != test.wantOptions {
									t.Fatal("actual card workflow options changed")
								}
								initial, selected := status["initial_option"]
								wantSelected := !strings.HasPrefix(test.selected, "many-")
								if selected != wantSelected || selected && initial != test.selected {
									t.Fatal("actual card changed offered-state selection fallback")
								}
							}
							raw, err := json.Marshal(card)
							if err != nil {
								t.Fatal(err)
							}
							got.Cards = append(got.Cards, raw)
							for index := range actions {
								action := &actions[index]
								stored, err := source.GetChatAction(ctx, workspace, record.ConnectionID, action.ID)
								a, _ := json.Marshal(action)
								b, _ := json.Marshal(stored)
								if err != nil || !bytes.Equal(a, b) || action.ExpiresAt.Before(started.Add(14*24*time.Hour)) || action.ExpiresAt.After(time.Now().UTC().Add(14*24*time.Hour)) {
									t.Fatal("live durable action differs", err)
								}
								if consumed, err := source.ConsumeChatAction(ctx, workspace, record.ConnectionID, action.ID); err != nil || !consumed {
									t.Fatal("card action was not live", consumed, err)
								}
								action.ExpiresAt = time.Time{}
							}
							got.Actions = append(got.Actions, actions)
						}()
						if source.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("card output owner leaked work")
						}
					}
					if source == probe && source.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("workflow card decoded complete workspace metadata")
					}
					outputs = append(outputs, got)
				}
				want, _ := json.Marshal(outputs[0])
				got, _ := json.Marshal(outputs[1])
				if !bytes.Equal(want, got) {
					t.Fatal("actual enabled group/personal workflow card differential", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
	}
}
