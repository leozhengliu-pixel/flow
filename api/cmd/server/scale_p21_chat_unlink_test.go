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

func TestScaleP21ChatUnlinkDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		var cards []map[string]any
		platformFailure := false
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			if strings.Contains(request.URL.Path, "tenant_access_token") {
				return chatTestResponse(200, `{"code":0,"tenant_access_token":"unlink-test-token","expire":7200}`), nil
			}
			if request.Method == http.MethodPatch && strings.Contains(request.URL.Path, "/im/v1/messages/") {
				var body struct {
					Content string `json:"content"`
				}
				var card map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(body.Content), &card); err != nil {
					t.Fatal(err)
				}
				cards = append(cards, card)
				if platformFailure {
					return chatTestResponse(400, `{"code":230001,"msg":"test card refusal"}`), nil
				}
				return chatTestResponse(200, `{"code":0}`), nil
			}
			t.Fatalf("unexpected unlink request %s %s", request.Method, request.URL.Path)
			return nil, nil
		})
		for _, name := range []string{"duplicates-and-deleted", "empty-session", "empty-owner", "missing-session", "missing-workspace", "foreign-owner", "in-process", "foreign-in-process", "chinese", "read-canceled", "platform-failure"} {
			t.Run(name, func(t *testing.T) {
				type outcome struct {
					Canceled   []bool
					StateGone  bool
					LocalStops int
					Final      *chatDMReplyView
					Cards      []map[string]any
					Failed     bool
				}
				var outputs []outcome
				for _, source := range []*store.SQLiteStore{repo, probe} {
					sessionID, userID := idgen.New("unlink-session-"), f.data.Viewer.ID
					if name == "empty-owner" {
						userID = ""
					}
					messageIDs := []string{
						workerChatMessagePrefix + "unlink_a", workerChatMessagePrefix + "unlink_b",
						workerChatMessagePrefix + "unlink_other_owner", workerChatMessagePrefix + "unlink_other_session",
						workerChatMessagePrefix + "unlink_finished", "legacy_unlink_message",
					}
					at := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
					sessions := []domain.AgentSession{
						{ID: sessionID, UserID: userID, Messages: nil},
						{ID: sessionID, UserID: userID, Messages: []domain.AgentMessage{{ID: messageIDs[0], Status: agentMessageStreaming, Content: strings.Repeat("ignored body ", 20000)}}},
						{ID: sessionID, UserID: userID, DeletedAt: &at, Messages: []domain.AgentMessage{{ID: messageIDs[1], Status: agentMessageStreaming}}},
						{ID: sessionID, UserID: "other-owner", Messages: []domain.AgentMessage{{ID: messageIDs[2], Status: agentMessageStreaming}}},
						{ID: sessionID + "-other", UserID: userID, Messages: []domain.AgentMessage{{ID: messageIDs[3], Status: agentMessageStreaming}}},
						{ID: sessionID, UserID: userID, Messages: []domain.AgentMessage{{ID: messageIDs[4]}, {ID: messageIDs[5], Status: agentMessageStreaming}}},
					}
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "scale_p21.unlink", "", nil, func(data *domain.Bootstrap) error {
						data.AgentSessions = sessions
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					runIDs := make([]string, len(messageIDs))
					for index, messageID := range messageIDs {
						runSession := sessionID
						if index == 3 {
							runSession += "-other"
						}
						payload, _ := json.Marshal(map[string]any{"chat": workerChatRunInfo{MessageID: messageID}})
						// These native queued runs have no usable principal. Cancellation
						// is real; the separately pending full-message finisher refuses
						// before reading or mutating session metadata.
						run, inserted, err := repo.EnqueueAgentRun(t.Context(), store.AgentRunInput{WorkspaceKey: workspace, SessionID: runSession, Kind: store.AgentRunKindChat, Payload: payload})
						if err != nil || !inserted {
							t.Fatal(inserted, err)
						}
						runIDs[index] = run.ID
					}
					for _, id := range []string{sessionID, sessionID + "-other"} {
						if err := repo.SaveAgentSessionState(t.Context(), store.AgentSessionState{WorkspaceKey: workspace, SessionID: id, Data: []byte("preserved state")}); err != nil {
							t.Fatal(err)
						}
					}
					record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: idgen.New("unlink-connection-"), AppID: idgen.New("unlink-app-"), TenantKey: "unlink-tenant", Connected: true}
					if err := repo.PutChatIntegration(t.Context(), record); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						_ = repo.DeleteChatIntegration(context.Background(), workspace, record.ConnectionID)
						_ = repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record))
					})
					raw, _ := json.Marshal(chatCredentials{AppSecret: "unlink-test-secret"})
					if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), raw, time.Now().Add(time.Hour)); err != nil {
						t.Fatal(err)
					}
					payload := chatDMReplyJob{UserID: userID, Workspace: workspace, SessionID: sessionID, CardID: "unlink-card"}
					raw, _ = json.Marshal(payload)
					job := domain.ChatJob{ID: idgen.New("unlink-job-"), WorkspaceKey: workspace, ConnectionID: record.ConnectionID, Kind: chatDMReplyJobKind, DedupKey: idgen.New("unlink-dedup-"), Payload: raw}
					if inserted, err := repo.EnqueueChatJob(t.Context(), job); err != nil || !inserted {
						t.Fatal(inserted, err)
					}
					jobs, err := repo.ClaimChatJobs(t.Context(), workspace, chatDMReplyJobKind, 1, time.Now())
					if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID {
						t.Fatal("claim unlink reply", len(jobs), err)
					}
					service := &server{store: source}
					localStops := 0
					if name == "in-process" || name == "foreign-in-process" {
						runWorkspace := workspace
						if name == "foreign-in-process" {
							runWorkspace += "-foreign"
						}
						service.agentRuns.Store(sessionID, agentRun{workspace: runWorkspace, cancel: func() { localStops++ }})
					}
					replySessionID, replyOwner := sessionID, userID
					if name == "empty-session" {
						replySessionID = ""
					}
					if name == "missing-session" {
						replySessionID += "-missing"
					}
					if name == "foreign-owner" {
						replyOwner = "missing-owner"
					}
					ctx, cancel := context.WithCancel(t.Context())
					lease := &chatDMReplyLease{s: service, ctx: ctx, cancel: cancel, job: jobs[0], payload: payload}
					targetWorkspace := workspace
					if name == "missing-workspace" {
						targetWorkspace += "-missing"
					}
					dm := chatDMRequest{record: record, identity: domain.ChatIdentity{UserID: replyOwner}, target: chatDMTarget{Workspace: targetWorkspace}, zh: name == "chinese"}
					reply := &chatDMReply{s: service, lease: lease, dm: dm, sessionID: replySessionID}
					readCtx := ctx
					if name == "read-canceled" {
						var readCancel context.CancelFunc
						readCtx, readCancel = context.WithCancel(ctx)
						readCancel()
					}
					cards, platformFailure = nil, name == "platform-failure"
					before := probe.WorkspaceMetadataDecodes()
					reply.stopForUnlink(readCtx)
					result := outcome{LocalStops: localStops, Final: lease.payload.Final, Cards: cards, Failed: reply.deliveryErr != nil}
					for _, id := range runIDs {
						run, err := repo.AgentRun(t.Context(), id)
						if err != nil {
							t.Fatal(err)
						}
						result.Canceled = append(result.Canceled, run.Status == store.AgentRunCanceled)
					}
					wantStop := name != "empty-session" && name != "missing-session" && name != "missing-workspace" && name != "foreign-owner" && name != "in-process" && name != "read-canceled"
					if !reflect.DeepEqual(result.Canceled, []bool{wantStop, wantStop, false, false, false, false}) {
						t.Fatal("unlink canceled the wrong session occurrences", result.Canceled)
					}
					wantLocal := 0
					if name == "in-process" {
						wantLocal = 1
					}
					if result.LocalStops != wantLocal {
						t.Fatal("unlink changed the local cancellation boundary", result.LocalStops)
					}
					_, err = repo.AgentSessionStateFor(t.Context(), workspace, sessionID)
					result.StateGone = errors.Is(err, store.ErrAgentRunNotFound)
					if result.StateGone != wantStop || err != nil && !result.StateGone {
						t.Fatal("unlink changed the wrong session state", err)
					}
					otherState, err := repo.AgentSessionStateFor(t.Context(), workspace, sessionID+"-other")
					if err != nil || string(otherState.Data) != "preserved state" {
						t.Fatal("unlink changed unrelated worker state", err)
					}
					if result.Final == nil || result.Final.Status != "stopped" || len(result.Cards) != 1 || result.Failed != (name == "platform-failure" || name == "read-canceled" && source == probe) {
						t.Fatal("unlink final card/error changed", result)
					}
					if name == "read-canceled" && source == probe && !errors.Is(reply.deliveryErr, context.Canceled) {
						t.Fatal("bounded read cancellation became success", reply.deliveryErr)
					}
					if probe.MetadataWorkUsedForTesting() != 0 || probe.WorkspaceMetadataDecodes() != before {
						t.Fatal("unlink header read hydrated full metadata or leaked")
					}
					saved, err := repo.ChatReplyJobPayload(t.Context(), workspace, record.ConnectionID, job.ID)
					var checkpoint chatDMReplyJob
					if err != nil || json.Unmarshal(saved, &checkpoint) != nil || !reflect.DeepEqual(checkpoint.Final, result.Final) {
						t.Fatal("unlink final checkpoint changed", err)
					}
					after, _ := repo.WorkspaceMetadataFields(workspace, "agentSessions")
					if !reflect.DeepEqual(after.AgentSessions, sessions) {
						t.Fatal("unlink header used as a mutation preimage")
					}
					if err := repo.FinishChatJob(t.Context(), lease.job, "completed", time.Time{}, ""); err != nil {
						t.Fatal(err)
					}
					cancel()
					if err := repo.DeleteChatIntegration(t.Context(), workspace, record.ConnectionID); err != nil {
						t.Fatal(err)
					}
					if err := repo.DeleteConnectorSecret(t.Context(), chatCredentialID(record)); err != nil {
						t.Fatal(err)
					}
					if name == "read-canceled" {
						result.Failed = false
					}
					outputs = append(outputs, result)
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatal("literal/bounded unlink effects differ", outputs[0], outputs[1])
				}
			})
		}
	}
}
