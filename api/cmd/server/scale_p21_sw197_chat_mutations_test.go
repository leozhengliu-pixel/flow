package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW197Exec(t *testing.T, database *scaleP04Database, query string, args ...any) {
	t.Helper()
	database.query(t, `SELECT 1 FROM workspace_states WHERE workspace_key=?`, database.key).Close()
	if database.config.Driver == "postgres" || database.config.Driver == "postgresql" {
		for index := range args {
			query = strings.Replace(query, "?", fmt.Sprintf("$%d", index+1), 1)
		}
	}
	if _, err := database.reader.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW197Values(values map[string]string) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	for key, value := range values {
		result[key], _ = json.Marshal(value)
	}
	return result
}

func scaleP21SW197StatusClock(value any, started, finished time.Time) {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.EqualFold(key, "statusChangedAt") {
				if text, ok := child.(string); ok {
					if clock, err := time.Parse(time.RFC3339Nano, text); err == nil && !clock.Before(started) && !clock.After(finished) {
						value[key] = "<generated-clock>"
						continue
					}
				}
			}
			scaleP21SW197StatusClock(child, started, finished)
		}
	case []any:
		for _, child := range value {
			scaleP21SW197StatusClock(child, started, finished)
		}
	}
}

func TestScaleP21SW197ChatMutationAdaptersDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f, database, issue := scaleP21SW44ChatFixture(t)
	repo, workspace := database.repo, database.key
	prefix := idgen.New("sw197-")
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("owned chat adapter fixture missing")
	}
	nextNumber := baseline.NextIssueNumber
	for _, item := range baseline.Issues {
		nextNumber = max(nextNumber, item.Number+1)
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	sqlFixture := scaleP21SW10Fixture{reader: database}
	sqlFixture.data.Workspace.URLKey = workspace
	platform := f.threadPlatform(t)
	targetState := issue.State.ID
	for _, state := range statesForTeam(&baseline, issue.Team.ID) {
		if state.ID != issue.State.ID {
			targetState = state.ID
			break
		}
	}
	if targetState == issue.State.ID {
		t.Fatal("owned fixture needs a second workflow state for the actual state action")
	}
	for _, operation := range []string{"dm-create", "message-create", "group-create", "group-state", "group-assignee", "group-comment", "file-request", "process-request"} {
		for _, refusal := range []string{"", "invalid-input", "expired-consent"} {
			t.Run(operation+"/"+refusal, func(t *testing.T) {
				var expected any
				for _, source := range []*store.SQLiteStore{repo, probe} {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					probe.SetWebhookSink(nil)
					probe.SetRealtimeSink(nil)
					restore := store.ForceFullMutationsForTesting(true)
					err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw197_seed", "", nil, func(data *domain.Bootstrap) error {
						var original domain.Bootstrap
						if err := json.Unmarshal(saved, &original); err != nil {
							return err
						}
						*data = original
						data.NextIssueNumber = nextNumber
						data.Asks = []domain.Ask{}
						return nil
					})
					restore()
					if err != nil {
						t.Fatal(err)
					}
					for _, table := range []string{"chat_actions", "chat_jobs", "chat_intake_requests", "chat_thread_items", "chat_threads"} {
						scaleP21SW197Exec(t, database, "DELETE FROM "+table+" WHERE workspace_key=? AND connection_id=?", workspace, f.record.ConnectionID)
					}
					scaleP21SW197Exec(t, database, `DELETE FROM chat_mutation_receipts WHERE workspace_key=?`, workspace)
					scaleP21SW197Exec(t, database, `UPDATE issue_number_sequences SET last_number=? WHERE workspace_key=?`, nextNumber-1, workspace)
					f.binding.Requests = &domain.ChatBindingRequests{Enabled: true, Emoji: "OK"}
					if err := repo.PutChatBinding(t.Context(), workspace, f.binding); err != nil {
						t.Fatal(err)
					}
					identity := f.identity
					if refusal == "expired-consent" {
						identity.AuthProvider = ""
					}
					if err := repo.PutChatIdentity(t.Context(), workspace, f.record.ConnectionID, identity); err != nil {
						t.Fatal(err)
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					f.s.store = source
					f.sent, f.patched, f.responseIDs = nil, nil, map[string]string{}
					platform.calls = nil
					before := scaleP21SW59SQL(t, sqlFixture)
					job := domain.ChatJob{ID: prefix + "job", DedupKey: prefix + operation, WorkspaceKey: workspace, ConnectionID: f.record.ConnectionID}
					actionID := prefix + "action"
					messageID := "om_" + prefix + operation
					createdID := "issue_chat_" + chatKey(f.record.ConnectionID, actionID)
					if operation == "file-request" || operation == "process-request" {
						createdID = chatRequestIssueID(f, messageID)
					}
					aggregateIDs := []string{issue.ID, createdID, "ask_chat_" + chatKey(f.record.ConnectionID, "ask", createdID)[:32]}
					priorEvents := map[string]bool{}
					for _, id := range aggregateIDs {
						events, err := repo.Events(t.Context(), id)
						if err != nil {
							t.Fatal(err)
						}
						for _, event := range events {
							priorEvents[event.ID] = true
						}
					}
					got := scaleP21SW59Outcome{}
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, event))
					})
					captureRealtime := func(_ string, event domain.RealtimeEvent) {
						got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
					}
					source.SetRealtimeSink(captureRealtime)
					if source != repo {
						repo.SetRealtimeSink(captureRealtime)
					}
					started := time.Now().UTC().Truncate(time.Millisecond)
					var operationErr error
					func() {
						ctx, closeOutput := beginMetadataOutputReads(t.Context())
						defer closeOutput()
						ctx = withChatIdentityAuthentication(ctx, identity)
						var metadata domain.Bootstrap
						if operation != "process-request" {
							// This is the already-proven workflow input boundary used
							// by the callers of the unchanged leaf adapters.
							operationErr = f.s.chatAuthorizedWorkflowHeaders(ctx, workspace, identity.UserID, &metadata)
							if operationErr != nil {
								return
							}
						}
						title := "Exact chat adapter title"
						if refusal == "invalid-input" {
							title = ""
						}
						values := scaleP21SW197Values(map[string]string{
							"title": title, "description": "Exact chat body <>&", "teamId": f.binding.TeamID,
							"priority": "2", "assigneeId": "__unassigned", "stateId": targetState, "comment": title,
						})
						command := chatInboundCommand{OpenID: identity.OpenID, ChatID: f.binding.ChatID, MessageID: messageID, ActionID: actionID, Values: values}
						action := chatCardAction{
							Kind: "create", IssueID: issue.ID,
							StateIDs: []string{targetState}, AssigneeIDs: []string{identity.UserID},
							ExpectedVersion: issue.Version,
						}
						switch operation {
						case "dm-create":
							stored := domain.ChatAction{ID: actionID, WorkspaceKey: workspace, ConnectionID: f.record.ConnectionID, UserID: identity.UserID, ExpiresAt: time.Now().Add(time.Hour)}
							if err := source.PutChatAction(ctx, stored); err != nil {
								t.Fatal(err)
							}
							dm := chatDMRequest{record: f.record, identity: identity, target: chatDMTarget{Workspace: workspace, Identity: identity}, command: command, job: job}
							operationErr = f.s.chatDMCreateIssue(ctx, dm, chatDMAction{TeamIDs: []string{f.binding.TeamID}, AssigneeIDs: action.AssigneeIDs})
						case "message-create":
							action.SourceMessageID = messageID
							extended := chatExtendedAction{chatCardAction: action, TeamIDs: []string{f.binding.TeamID}, SourceChatID: f.binding.ChatID}
							raw, err := json.Marshal(extended)
							if err != nil {
								t.Fatal(err)
							}
							operationErr = f.s.chatCreateFromMessage(ctx, f.record, identity, metadata, job, command, domain.ChatAction{UserID: identity.UserID, Payload: raw})
						case "group-create", "group-state", "group-assignee", "group-comment":
							if operation != "group-create" {
								action.Kind = strings.TrimPrefix(operation, "group-")
							}
							if refusal == "invalid-input" {
								command.Values["stateId"] = json.RawMessage(`"not-a-workflow-state"`)
								command.Values["assigneeId"] = json.RawMessage(`"not-an-offered-user"`)
							}
							operationErr = f.s.chatExecuteAction(ctx, f.record, f.binding, identity, metadata, job, command, action)
						case "file-request":
							draft := chatRequestDraft{MessageID: messageID, ChatID: f.binding.ChatID, Text: title, Source: domain.ChatRequestSourceShortcut}
							if refusal == "invalid-input" {
								draft.MessageID = "invalid-platform-id"
							}
							outcome, err := f.s.chatFileRequest(ctx, f.record, f.binding, identity, metadata, job, draft)
							operationErr = err
							if refusal == "" && (err != nil || outcome.Issue.ID != createdID || outcome.Code != "" || outcome.Existing != nil || outcome.Busy) {
								t.Fatal("request did not create the stable issue", err, outcome)
							}
							got.Header = http.Header{"Request-Code": []string{outcome.Code}}
						case "process-request":
							command.Values = scaleP21SW197Values(map[string]string{"source": domain.ChatRequestSourceShortcut, "explicit": "true", "text": title})
							if refusal == "invalid-input" {
								command.MessageID = "invalid-platform-id"
							}
							operationErr = f.s.processChatRequest(ctx, f.record, job, command)
						}
					}()
					finished := time.Now().UTC()
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					repo.SetRealtimeSink(nil)
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("chat adapter leaked work or decoded the full workspace", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
					}
					got.Rows = scaleP21SW59SQL(t, sqlFixture)
					for _, id := range aggregateIDs {
						events, err := repo.Events(t.Context(), id)
						if err != nil {
							t.Fatal(err)
						}
						for _, event := range events {
							if !priorEvents[event.ID] {
								got.Events = append(got.Events, event)
							}
						}
					}
					requests, err := repo.ChatIntakeRequestsForIssue(t.Context(), workspace, f.record.ConnectionID, createdID)
					if err != nil {
						t.Fatal(err)
					}
					jobs := []any{}
					rows := database.query(t, `SELECT kind,dedup_key,payload,status,attempts,last_error FROM chat_jobs WHERE workspace_key=? ORDER BY kind,dedup_key`, workspace)
					for rows.Next() {
						var kind, dedup, status, lastError string
						var payload []byte
						var attempts int
						if err := rows.Scan(&kind, &dedup, &payload, &status, &attempts, &lastError); err != nil {
							t.Fatal(err)
						}
						jobs = append(jobs, []any{kind, dedup, json.RawMessage(payload), status, attempts, lastError})
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					calls := []any{}
					for _, call := range platform.calls {
						body := map[string]any{}
						for key, value := range call.body {
							if key == "content" {
								if text, ok := value.(string); ok {
									var card any
									if json.Unmarshal([]byte(text), &card) == nil {
										value = card
									}
								}
							}
							body[key] = value
						}
						calls = append(calls, []any{call.method, call.path, body})
					}
					got.Response, err = json.Marshal(map[string]any{
						"error": scaleP21ChatPointError(operationErr), "requests": requests, "jobs": jobs, "platform": calls,
					})
					if err != nil {
						t.Fatal(err)
					}
					normalized := scaleP21SW59Normalized(t, got, before, started, finished)
					scaleP21SW197StatusClock(normalized, started, finished)
					if source == repo {
						expected = normalized
					} else if !reflect.DeepEqual(expected, normalized) {
						oldJSON, _ := json.Marshal(expected)
						newJSON, _ := json.Marshal(normalized)
						t.Fatalf("chat adapter differs:\nlegacy=%s\nbounded=%s", oldJSON, newJSON)
					}
					if refusal == "" {
						if operationErr != nil || len(got.Events) == 0 {
							t.Fatal("successful adapter did not commit", operationErr, len(got.Events))
						}
						if operation == "file-request" || operation == "process-request" {
							if len(requests) != 1 || requests[0].Status != domain.ChatRequestCreated || requests[0].AskID == "" ||
								!slices.ContainsFunc(got.Events, func(event domain.DomainEvent) bool { return event.Type == "ask.created" }) {
								t.Fatal("request adapter lost durable Ask completion", requests)
							}
						}
					}
				}
			})
		}
	}
}
