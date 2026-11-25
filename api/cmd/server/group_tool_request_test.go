package main

import (
	"encoding/json"
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

// executeChatGroupRequest reads the typed workflow header contract instead
// of the generic typed bootstrap. Flag off both are the same literal call;
// bounded, the header contract owns the read. Compare every durable effect.
func TestScaleP21SW267GroupToolRequestDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f, database, issue := scaleP21SW44ChatFixture(t)
	repo, workspace := database.repo, database.key
	prefix := idgen.New("sw267-")
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
	for _, operation := range []string{"group-tool-request"} {
		for _, refusal := range []string{"", "invalid-input", "expired-consent", "existing", "requests-disabled"} {
			t.Run(operation+"/"+refusal, func(t *testing.T) {
				var expected any
				for _, source := range []*store.SQLiteStore{repo, probe} {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					probe.SetWebhookSink(nil)
					probe.SetRealtimeSink(nil)
					restore := store.ForceFullMutationsForTesting(true)
					err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw267_seed", "", nil, func(data *domain.Bootstrap) error {
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
					f.binding.Requests = &domain.ChatBindingRequests{Enabled: refusal != "requests-disabled", Emoji: "OK"}
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
					messageID := "om_" + prefix + operation
					createdID := chatRequestIssueID(f, messageID)
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
						title := "Exact group tool title"
						if refusal == "invalid-input" {
							title = "  "
						}
						arguments, err := json.Marshal(chatGroupRequestInput{Title: title, Description: "Exact group request body <>&"})
						if err != nil {
							t.Fatal(err)
						}
						ctx = withChatGroupRequests(ctx, chatGroupRequestScope{
							Provider: f.record.Provider, ConnectionID: f.record.ConnectionID,
							ChatID: f.binding.ChatID, OpenID: identity.OpenID, MessageID: messageID, Root: messageID,
						})
						results := []string{}
						attempts := 1
						if refusal == "existing" {
							attempts = 2
						}
						for range attempts {
							result, err := f.s.executeChatGroupRequest(ctx, workspace, baseline.Viewer, domain.AgentToolCall{Name: chatGroupRequestTool, Arguments: arguments})
							operationErr = err
							results = append(results, string(result), scaleP21ChatPointError(err))
						}
						got.Header = http.Header{"Group-Tool-Result": results}
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
					if refusal == "existing" && (len(requests) != 1 || operationErr == nil || !strings.Contains(operationErr.Error(), "Nothing was filed")) {
						t.Fatal("repeated group request was not refused as existing", operationErr, requests)
					}
					if refusal != "" && refusal != "existing" && (operationErr == nil || len(requests) != 0) {
						t.Fatal("refused group request filed something", operationErr, requests)
					}
					if refusal == "" || refusal == "existing" {
						if refusal == "" && operationErr != nil || len(got.Events) == 0 {
							t.Fatal("successful adapter did not commit", operationErr, len(got.Events))
						}
						{
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
