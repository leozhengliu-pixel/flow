package main

import (
	"bytes"
	"context"
	"database/sql"
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

func TestScaleP21ChatRequestStatusDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{47}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace := f.data.Workspace.URLKey
		issue := domain.Issue{ID: idgen.New("request-status-issue-"), Identifier: "STATUS-1", Title: "Exact status title",
			Team: domain.Team{ID: f.public, Name: "Request team"}, State: domain.WorkflowState{Type: "started", Name: "In progress"}, Version: 11}
		at := time.Date(2026, 11, 14, 6, 0, 0, 0, time.UTC)
		cases := []struct {
			name, state, previous, wantOutcome string
			variant                            string
			messages                           int
			noThread, fail, replay             bool
		}{
			{name: "active", state: "started", messages: 1},
			{name: "completed", state: "completed", wantOutcome: "completed", messages: 1},
			{name: "canceled", state: "canceled", wantOutcome: "canceled", messages: 1},
			{name: "reopened", state: "started", previous: "completed", messages: 1},
			{name: "rejected", state: "completed", variant: "rejected", wantOutcome: "rejected", messages: 1},
			{name: "rejected-empty-final", state: "started", variant: "empty-final", wantOutcome: "rejected", messages: 1},
			{name: "rejected-nil-approvals", state: "started", variant: "nil-approvals", wantOutcome: "rejected", messages: 1},
			{name: "nonrejected-ask", state: "completed", variant: "accepted", wantOutcome: "completed", messages: 1},
			{name: "first-duplicate-ask", state: "started", variant: "duplicate", messages: 1},
			{name: "empty-ask-id", state: "started", variant: "empty-id", wantOutcome: "rejected", messages: 1},
			{name: "missing-ask", state: "started", variant: "missing-ask", messages: 1},
			{name: "already-closed", state: "started", variant: "rejected", previous: "rejected", wantOutcome: "rejected"},
			{name: "rejected-replay", state: "started", variant: "rejected", wantOutcome: "rejected", messages: 1, replay: true},
			{name: "missing-request", state: "started", variant: "missing-request"},
			{name: "other-group", state: "started", variant: "other-group"},
			{name: "pending-request", state: "started", variant: "pending-request"},
			{name: "rebuild-thread", state: "completed", wantOutcome: "completed", noThread: true, messages: 2},
			{name: "send-failure", state: "completed", messages: 1, fail: true},
			{name: "long-selected-note", state: "started", variant: "long-note", wantOutcome: "rejected", messages: 1},
		}
		var active *store.SQLiteStore
		var scenario int
		var sent []map[string]any
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			if strings.Contains(request.URL.Path, "tenant_access_token") {
				return chatTestResponse(http.StatusOK, `{"code":0,"tenant_access_token":"request-status-token","expire":7200}`), nil
			}
			if !strings.Contains(request.URL.Path, "/im/v1/messages") {
				t.Fatalf("unexpected platform request %s", request.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			sent = append(sent, map[string]any{"path": request.URL.Path, "query": request.URL.RawQuery, "body": body})
			if active == f.probe && active.MetadataWorkUsedForTesting() == 0 {
				t.Error("status output escaped its admitted Ask read before sending")
			}
			if cases[scenario].fail {
				return chatTestResponse(http.StatusServiceUnavailable, `{"code":999,"msg":"Retry exact status"}`), nil
			}
			return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":"request-status-message","thread_id":"request-status-thread"}}`), nil
		})
		for index, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				scenario = index
				record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, ConnectionID: idgen.New("request-status-connection-"),
					Provider: "lark", AppID: "request-status-app", TenantKey: "request-status-tenant", Connected: true}
				binding := domain.ChatBinding{ID: "request-status-binding", ConnectionID: record.ConnectionID, TeamID: f.public, ChatID: "request-status-group", Enabled: true}
				t.Cleanup(func() {
					_ = repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record))
					_ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID)
				})
				type outcome struct {
					Messages []map[string]any
					Request  any
					Thread   any
					Error    string
				}
				var outputs []outcome
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					current := issue
					current.State.Type = test.state
					askID := "request-status-ask"
					if test.variant == "empty-id" {
						askID = ""
					}
					if source == repo {
						if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.request_status_seed", "", nil, func(data *domain.Bootstrap) error {
							unused := strings.Repeat("Unused ask body or approval ", 15000)
							ask := domain.Ask{ID: askID, Status: "rejected", Body: unused, Approvals: []domain.AskApproval{
								{Decision: "rejected", Note: unused}, {Decision: "rejected", Note: "\t\u2003Current final reason \x00 "},
								{Decision: "approved", Note: unused}, {Decision: "REJECTED", Note: unused},
							}}
							switch test.variant {
							case "", "missing-request", "other-group", "pending-request":
								ask.Status = "pending"
							case "accepted":
								ask.Status = "accepted"
							case "empty-final":
								ask.Approvals = append(ask.Approvals, domain.AskApproval{Decision: "rejected", Note: " \t\u2003 "})
							case "nil-approvals":
								ask.Approvals = nil
							case "long-note":
								ask.Approvals = append(ask.Approvals, domain.AskApproval{Decision: "rejected", Note: strings.Repeat("Complete selected reason ", 3000)})
							}
							data.Asks = []domain.Ask{{ID: "request-status-unrelated", Body: unused}, ask}
							if test.variant == "duplicate" {
								data.Asks[1].Status = "accepted"
								data.Asks = append(data.Asks, domain.Ask{ID: askID, Status: "rejected", Approvals: []domain.AskApproval{{Decision: "rejected", Note: "Wrong later Ask"}}})
							}
							if test.variant == "missing-ask" {
								data.Asks = data.Asks[:1]
							}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					before := scaleP21M3Fingerprint(t, repo, workspace, "asks")
					if err := repo.PutChatIntegration(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					if test.variant != "missing-request" {
						item := domain.ChatIntakeRequest{ConnectionID: record.ConnectionID, ChatID: binding.ChatID, BindingID: binding.ID,
							MessageID: "request-status-source-message", IssueID: issue.ID, AskID: askID,
							RequesterID: f.data.Viewer.ID, RequesterOpenID: "request-status-open-id",
							Source: domain.ChatRequestSourceMention, Status: domain.ChatRequestCreated, Outcome: test.previous}
						if test.variant == "other-group" {
							item.ChatID = "request-status-other-group"
						}
						if test.variant == "pending-request" {
							item.Status = domain.ChatRequestPending
						}
						if _, reserved, err := repo.ReserveChatIntakeRequest(t.Context(), workspace, item); err != nil || !reserved {
							t.Fatal("intake reservation failed", reserved, err)
						}
					}
					if !test.noThread {
						if _, err := repo.PutChatThread(t.Context(), workspace, domain.ChatThread{
							ConnectionID: record.ConnectionID, ChatID: binding.ChatID, ResourceType: domain.ChatThreadResourceIssue,
							ResourceID: issue.ID, RootMessageID: "request-status-root", BindingID: binding.ID, SyncFrom: at,
						}); err != nil {
							t.Fatal(err)
						}
					}
					secret, _ := json.Marshal(chatCredentials{AppSecret: "request-status-secret"})
					if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), secret, time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
					active, sent = source, nil
					service := &server{store: source, allowedOrigin: "https://flow.example.test"}
					job := domain.ChatJob{DedupKey: "request-status-dedup"}
					err := service.chatSendRequestStatus(t.Context(), record, binding, current, job)
					if err == nil && test.replay {
						err = service.chatSendRequestStatus(t.Context(), record, binding, current, job)
					}
					result := outcome{Messages: sent}
					if err != nil {
						result.Error = err.Error()
					}
					if len(sent) != test.messages || (err != nil) != test.fail {
						t.Fatal("status output or failure changed", len(sent), err)
					}
					request, lookup := repo.ChatIntakeRequestForMessage(t.Context(), workspace, record.ConnectionID, "request-status-source-message")
					if lookup == nil {
						result.Request = scaleP21M2JSON(t, request, false)
						if request.Outcome != test.wantOutcome {
							t.Fatal("stored request outcome changed", request.Outcome, test.wantOutcome)
						}
					} else if !errors.Is(lookup, sql.ErrNoRows) {
						t.Fatal(lookup)
					}
					thread, lookup := repo.ChatThread(t.Context(), workspace, record.ConnectionID, binding.ChatID, domain.ChatThreadResourceIssue, issue.ID)
					if lookup == nil {
						thread.SyncFrom = time.Time{}
						result.Thread = scaleP21M2JSON(t, thread, false)
					} else if !errors.Is(lookup, sql.ErrNoRows) {
						t.Fatal(lookup)
					}
					if source.MetadataWorkUsedForTesting() != 0 || before != scaleP21M3Fingerprint(t, repo, workspace, "asks") {
						t.Fatal("status output leaked ownership or changed Ask records")
					}
					outputs = append(outputs, result)
					if err := repo.DeleteChatIntegration(t.Context(), workspace, record.ConnectionID); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("request-status differential", scaleP21M2Diff(outputs[0], outputs[1]))
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("request status decoded complete workspace metadata")
		}
	}
}
