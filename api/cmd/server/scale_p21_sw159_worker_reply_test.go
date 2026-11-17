package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW159Message(id string, at time.Time) domain.AgentMessage {
	return domain.AgentMessage{
		ID: id, Role: "assistant", Content: "native saved reply\x00", DurationMS: 9007199254740993, AnswerTo: "question",
		Parts: []domain.AgentMessagePart{
			{ID: "text", Type: "text", Title: "Text", Text: "saved text", Status: "running", Offloaded: domain.OffloadedContent{"text": {Key: "not-loaded-part", Size: 42, Summary: "part summary"}}},
			{ID: "tool", Type: "toolCall", Title: "Tool", Status: "running", ToolCall: &domain.AgentToolCall{
				ID: "call", Name: "external_tool", Arguments: json.RawMessage(`{ "id": "issue", "duplicate": 1, "duplicate": 2 }`),
				Result: json.RawMessage(`{"ok":true}`), Status: "completed", Error: "saved error", Title: "Call title",
				Connector: "Connector", RemoteTool: "remote", ApprovalID: "approval",
				Offloaded: domain.OffloadedContent{"result": {Key: "not-loaded-result", Size: 45}},
				Change: &domain.AgentChange{Action: "updated", EntityType: "issue", EntityID: "issue", ParentID: "parent", Label: "ISS-1", URL: "/issue",
					Fields: []string{"title", "title"}, Before: json.RawMessage(`{"title":"before"}`), After: json.RawMessage(`{"title":"after"}`),
					Undoable: true, State: "undone", Note: "note", Skipped: []string{}, UpdatedAt: &at},
			}},
			{ID: "question", Type: "question", Question: &domain.AgentQuestion{Options: []domain.AgentQuestionOption{{Label: "Keep", Value: "keep"}}, AllowFreeText: true, Selected: "keep", Answer: "answer",
				Approval: &domain.AgentApproval{Kind: "connector", Body: "preview", Health: "onTrack", Decision: "approved",
					Targets: []domain.AgentApprovalTarget{{Kind: "issue", ID: "issue", Label: "ISS-1", Title: "Issue", Href: "/issue", SubIssues: 2, Affected: map[string]int{"comments": 3}, Permanent: true}},
					Parent:  &domain.AgentApprovalTarget{Kind: "project", ID: "project"}, Calls: []domain.AgentToolCall{{ID: "held", Name: "save_issue", Status: "completed"}}}}},
			{ID: "elicitation", Type: "elicitation", Elicitation: &domain.AgentElicitation{ID: "elicitation", SessionID: "session", ConnectorName: "Connector", ConnectorURL: "https://example.test/mcp",
				Mode: "form", Message: "Question", Schema: json.RawMessage(`{"type":"object"}`), URL: "https://example.test/form", Action: "accept"}},
		},
		Mentions:    []domain.AgentMention{{Type: "issue", ID: "issue", Label: "ISS-1"}, {Type: "user", ID: "owner"}},
		Attachments: []domain.AgentMessageAttachment{{Name: "file", ContentType: "text/plain", Size: 9007199254740993, URL: "content://not-loaded"}},
		Feedback:    &domain.AgentFeedback{Rating: "positive", Comment: "feedback", UpdatedAt: at},
		CreatedAt:   at, Status: agentMessageStreaming, UpdatedAt: &at,
		Outcome:      &domain.AgentReplyOutcome{Status: "interrupted", Reason: "worker_lost"},
		WorkerCursor: &domain.AgentWorkerCursor{Run: "source-run", Attempt: 2, Seq: 9007199254740993},
		Offloaded:    domain.OffloadedContent{"content": {Key: "not-loaded-message", Size: 99, Summary: "message summary"}},
	}
}

func scaleP21SW159MessageFields(before, after domain.AgentMessage) []string {
	var fields []string
	left, right := reflect.ValueOf(before), reflect.ValueOf(after)
	for index := 0; index < left.NumField(); index++ {
		if !reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface()) {
			fields = append(fields, left.Type().Field(index).Name)
		}
	}
	return fields
}

func TestScaleP21SW159WorkerReplyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace := f.data.Workspace.URLKey
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range []struct{ name, operation, internal string }{
			{"full-native", "read", "off"}, {"full-native-internal", "read", "on"},
			{"finished", "read", "off"}, {"deleted", "read", "off"}, {"deleted-internal", "read", "on"},
			{"duplicate-sessions", "read", "off"}, {"duplicate-messages", "read", "off"},
			{"first-finished-duplicate", "read", "off"}, {"nil-parts", "read", "off"}, {"empty-parts", "read", "off"},
			{"empty-session", "read", "off"}, {"empty-owner", "read", "off"}, {"empty-message", "read", "off"},
			{"nul-session", "read", "off"}, {"nul-owner", "read", "off"}, {"nul-message", "read", "off"}, {"nul-status", "read", "off"},
			{"missing-message", "read", "off"}, {"missing-session", "read", "off"}, {"missing-workspace", "read", "off"},
			{"foreign-owner", "read", "off"}, {"slug-shadow", "read", "off"}, {"reloaded", "read", "off"},
			{"retained-after-replace", "read", "off"}, {"cancel-after-output", "read", "off"},
			{"canceled", "read", "off"}, {"canceled-internal", "read", "on"}, {"of-canceled", "of", "off"}, {"of-canceled-internal", "of", "on"},
			{"of-invalid-principal", "of", "off"}, {"pinned-old-source", "read", "off"},
			{"streaming-only", "streaming", "off"}, {"streaming-only-finished", "streaming", "off"},
			{"reply-for", "reply-for", "off"}, {"reply-for-finished", "reply-for", "off"},
			{"update-draft", "update", "off"}, {"update-writer", "update", "off"}, {"update-skip", "update", "off"}, {"update-canceled", "update", "off"},
			{"resume-approved", "resume", "off"}, {"resume-pending", "resume", "off"},
			{"unowned", "read", "off"}, {"closed-owner", "read", "off"}, {"budget-refused", "read", "off"},
		} {
			t.Run(spec.name, func(t *testing.T) {
				type outcome struct {
					Message      domain.AgentMessage
					Streaming    bool
					Finished     bool
					DraftChecked bool
					Error        string
					Saved        []domain.AgentSession
					Resume       *workerChatResume
				}
				var outputs []outcome
				var legacyDraft domain.AgentMessage
				prefix := idgen.New("sw159-")
				sessionID, owner, messageID := prefix+"session", f.data.Viewer.ID, workerChatMessagePrefix+prefix
				at := time.Now().UTC()
				for _, source := range []*store.SQLiteStore{repo, probe} {
					id, userID, mid, key := sessionID, owner, messageID, workspace
					switch spec.name {
					case "empty-session":
						id = ""
					case "empty-owner":
						userID = ""
					case "empty-message":
						mid = ""
					case "nul-session":
						id += "\x00"
					case "nul-owner":
						userID += "\x00"
					case "nul-message":
						mid += "\x00"
					}
					message := scaleP21SW159Message(mid, at)
					if strings.Contains(spec.name, "finished") || spec.name == "update-skip" {
						message.Status = ""
					}
					if spec.name == "nul-status" {
						message.Status += "\x00"
					}
					if spec.name == "nil-parts" {
						message.Parts = nil
					}
					if spec.name == "empty-parts" {
						message.Parts = []domain.AgentMessagePart{}
					}
					if spec.operation == "update" {
						message.WorkerCursor = nil
					}
					if spec.name == "update-writer" {
						message.Parts = []domain.AgentMessagePart{{ID: "text", Type: "text", Text: "writer boundary", Status: "running"}}
						message.Offloaded = nil
					}
					if spec.operation == "resume" {
						message.Parts = []domain.AgentMessagePart{
							{ID: "approve", Type: "toolCall", ToolCall: &domain.AgentToolCall{ID: "approved-call", ApprovalID: "approval", Status: "completed"}},
							{ID: "reject", Type: "toolCall", ToolCall: &domain.AgentToolCall{ID: "rejected-call", ApprovalID: "approval-2", Status: "completed", Error: workerChatDeclined}},
						}
						if spec.name == "resume-pending" {
							message.Parts[0].ToolCall.Status = "pending"
						}
					}
					session := domain.AgentSession{ID: id, UserID: userID, SlugID: prefix + "slug", Title: strings.Repeat("ignored session ", 1000), Messages: []domain.AgentMessage{message}, CreatedAt: at, UpdatedAt: at}
					sessions := []domain.AgentSession{session}
					switch spec.name {
					case "deleted", "deleted-internal":
						sessions[0].DeletedAt = &at
					case "duplicate-sessions":
						sessions = []domain.AgentSession{{ID: id, UserID: userID}, {ID: id, UserID: userID, Messages: []domain.AgentMessage{}}, session, session}
					case "duplicate-messages", "first-finished-duplicate":
						later := message
						later.Content, later.Status = "wrong later occurrence", agentMessageStreaming
						sessions[0].Messages = append(sessions[0].Messages, later)
					case "slug-shadow":
						sessions = append([]domain.AgentSession{{ID: prefix + "other", UserID: userID, SlugID: id, Messages: []domain.AgentMessage{message}}}, sessions...)
						sessions[0].Messages[0].Content = "wrong slug match"
					}
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "scale_p21.sw159_seed", "", nil, func(data *domain.Bootstrap) error {
						data.AgentSessions = sessions
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if spec.name == "reloaded" {
						if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
							t.Fatal(err)
						}
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					t.Setenv("FLOW_INTERNAL_CONVERSATION_RECORDS", spec.internal)
					readCtx := t.Context()
					var pin *store.InstalledMetadataRead
					if spec.name == "pinned-old-source" {
						var found bool
						pin, found, err = probe.BeginInstalledMetadataRead(t.Context(), workspace)
						if err != nil || !found {
							t.Fatal("old source pin unavailable", found, err)
						}
						t.Cleanup(pin.Close)
						readCtx, err = pin.Context(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "scale_p21.sw159_fresh", "", nil, func(data *domain.Bootstrap) error {
							data.AgentSessions[0].Messages[0].Content = "fresh contextless reply"
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
					}
					if strings.Contains(spec.name, "canceled") {
						var cancel context.CancelFunc
						readCtx, cancel = context.WithCancel(readCtx)
						cancel()
					}
					var cancelOutput context.CancelFunc
					if spec.name == "cancel-after-output" {
						readCtx, cancelOutput = context.WithCancel(readCtx)
					}
					switch spec.name {
					case "missing-message":
						mid += "-missing"
					case "missing-session":
						id += "-missing"
					case "missing-workspace":
						key += "-missing"
					case "foreign-owner":
						userID += "-other"
					}
					original, _ := repo.WorkspaceMetadataFields(workspace, "agentSessions")
					if spec.name != "reloaded" && spec.name != "pinned-old-source" && !reflect.DeepEqual(original.AgentSessions, sessions) {
						t.Fatal("typed producer changed the native selector/message witness")
					}
					service := &server{store: source}
					result := outcome{}
					beforeDecodes := probe.WorkspaceMetadataDecodes()
					baseline := probe.MetadataWorkUsedForTesting()
					var closes []func()
					ownedCtx := store.WithMetadataOutputOwner(readCtx, func(close func()) bool {
						closes = append(closes, close)
						return true
					})
					if spec.name == "unowned" {
						ownedCtx = readCtx
					}
					var closedOutput *store.MetadataJSONRead
					if spec.name == "closed-owner" && source == probe {
						closedOutput, err = probe.BeginMetadataJSONRead(t.Context())
						if err != nil {
							t.Fatal(err)
						}
						closedOutput.Close()
						ownedCtx = closedOutput.Context(readCtx)
					} else if spec.name == "closed-owner" {
						ownedCtx = store.WithMetadataOutputOwner(readCtx, func(func()) bool { return false })
					}
					var holds []*store.MetadataJSONRead
					t.Cleanup(func() {
						for _, close := range closes {
							close()
						}
						for _, hold := range holds {
							hold.Close()
						}
					})
					if spec.name == "budget-refused" && source == probe {
						for len(holds) < 4096 {
							hold, err := probe.BeginMetadataJSONRead(t.Context())
							if errors.Is(err, store.ErrMetadataWorkBudget) {
								break
							}
							if err != nil {
								t.Fatal(err)
							}
							holds = append(holds, hold)
						}
						if len(holds) == 4096 {
							t.Fatal("work budget fixture did not reach admission refusal")
						}
						baseline = probe.MetadataWorkUsedForTesting()
					}
					principal, _ := json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: userID})
					run := store.AgentRun{ID: prefix + "run", WorkspaceKey: key, SessionID: id, Principal: principal, Kind: store.AgentRunKindChat, Attempts: 1}
					info := workerChatRunInfo{MessageID: mid, MaxCalls: 10, MaxTurns: 5}
					switch spec.operation {
					case "read":
						result.Message, result.Streaming = service.workerChatStoredReply(ownedCtx, key, id, userID, mid)
					case "of":
						if spec.name == "of-invalid-principal" {
							run.Principal = json.RawMessage(`{"mode":"invalid"}`)
						}
						result.Message, result.Streaming = service.workerChatStoredReplyOf(ownedCtx, run, info)
					case "streaming":
						result.Streaming = service.workerChatStoredReplyStreaming(readCtx, key, id, userID, mid)
					case "reply-for":
						reply, err := service.workerChatReplyFor(readCtx, run, info)
						result.Streaming = reply != nil
						if err != nil {
							result.Error = err.Error()
						}
						if spec.name == "reply-for-finished" && (!errors.Is(err, errAgentLiveSkip) || reply != nil) ||
							spec.name == "reply-for" && (err != nil || reply == nil) {
							t.Fatal("worker reply guard changed its return/error", reply, err)
						}
					case "update":
						reply := &workerChatReply{s: service, run: run, info: info, owner: f.data.Viewer}
						result.Finished, err = reply.update(readCtx, false, func(draft *workerChatDraft) {
							if source == probe && probe.MetadataWorkUsedForTesting() <= baseline {
								t.Error("saved reply lost its owner before draft use")
							}
							if source == repo {
								var snapshotErr error
								legacyDraft, snapshotErr = cloneAgentMessage(draft.message)
								if snapshotErr != nil {
									t.Fatal("snapshot legacy draft", snapshotErr)
								}
							} else if !reflect.DeepEqual(draft.message, legacyDraft) {
								t.Error("complete draft fields differ while stored reply is owned", scaleP21SW159MessageFields(legacyDraft, draft.message))
							}
							result.DraftChecked = true
							if spec.name == "update-draft" {
								return
							}
							draft.message.Content, draft.message.Status = "saved effect", ""
							draft.message.UpdatedAt = &at
							draft.changed, draft.finished = true, true
						})
						if err != nil {
							result.Error = err.Error()
						}
						switch spec.name {
						case "update-draft":
							if result.Finished || err != nil || !result.DraftChecked {
								t.Fatal("read-only draft use changed its result", result.Finished, err)
							}
						case "update-writer":
							if !result.Finished || err != nil {
								t.Fatal("verified writer did not save the reply", result.Finished, err)
							}
						case "update-skip":
							if !result.Finished || !errors.Is(err, errAgentLiveSkip) {
								t.Fatal("finished reply did not preserve skip result", result.Finished, err)
							}
						case "update-canceled":
							if result.Finished || !errors.Is(err, context.Canceled) {
								t.Fatal("canceled update changed its error/result", result.Finished, err)
							}
						}
					case "resume":
						queued, inserted, err := repo.EnqueueAgentRun(t.Context(), store.AgentRunInput{
							ID: idgen.New("sw159-await-"), WorkspaceKey: workspace, SessionID: id, Kind: store.AgentRunKindChat,
							Principal: principal, Payload: json.RawMessage(`{}`), NotBefore: time.Unix(1, 0),
						})
						if err != nil || !inserted {
							t.Fatal("awaiting run seed failed", inserted, err)
						}
						claimOwner := idgen.New("sw159-worker-")
						claimed, err := repo.ClaimAgentRuns(t.Context(), store.AgentRunClaim{Owner: claimOwner, Free: map[int]int{0: 1}, Max: 1, Lease: time.Minute, Now: time.Now()})
						if err != nil || len(claimed) != 1 || claimed[0].ID != queued.ID {
							t.Fatal("awaiting fixture claimed an unrelated run", claimed, err)
						}
						run, err = repo.FinishAgentRun(t.Context(), queued.ID, claimOwner, store.AgentRunResult{Status: store.AgentRunAwaiting}, time.Now())
						if err != nil {
							t.Fatal(err)
						}
						heldThroughEnqueue := false
						ctx := store.WithSQLStatementObserver(readCtx, func(query string) {
							if strings.Contains(query, "agent_run_states") && source == probe {
								heldThroughEnqueue = true
								if probe.MetadataWorkUsedForTesting() <= baseline {
									t.Error("reply owner ended before resume decisions/enqueue")
								}
							}
						})
						service.resumeWorkerChat(ctx, run, info)
						runs, err := repo.SessionAgentRuns(t.Context(), workspace, id, 50)
						if err != nil {
							t.Fatal(err)
						}
						for _, resumed := range runs {
							if resumed.IdempotencyKey != "chat-resume:"+run.ID {
								continue
							}
							parsed, ok := workerChatInfo(resumed)
							if !ok || parsed.MessageID != mid || parsed.Resume == nil {
								continue
							}
							result.Resume = parsed.Resume
							if _, err := repo.CancelAgentRun(t.Context(), resumed.ID); err != nil {
								t.Fatal(err)
							}
						}
						if spec.name == "resume-approved" && (result.Resume == nil || source == probe && !heldThroughEnqueue) {
							t.Fatal("approved reply did not resume through owned final use")
						}
						if spec.name == "resume-approved" && !reflect.DeepEqual(result.Resume, &workerChatResume{Decisions: map[string]string{"approved-call": "approved", "rejected-call": "rejected"}}) ||
							spec.name == "resume-pending" && result.Resume != nil {
							t.Fatal("resume changed approval decisions or resumed a pending call", result.Resume)
						}
						if _, err := repo.CancelAgentRun(t.Context(), run.ID); err != nil {
							t.Fatal(err)
						}
					}
					if cancelOutput != nil {
						cancelOutput()
					}
					if spec.name == "retained-after-replace" {
						if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "agentSessions"), workspace, "scale_p21.sw159_replaced", "", nil, func(data *domain.Bootstrap) error {
							data.AgentSessions[0].Messages[0].Content = "replacement"
							data.AgentSessions[0].Messages[0].Parts[0].Text = "replacement part"
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(result.Message, original.AgentSessions[0].Messages[0]) {
							t.Fatal("returned native reply changed after installed replacement")
						}
					}
					refusal := source == probe && (spec.name == "unowned" || spec.name == "closed-owner" || spec.name == "budget-refused")
					if refusal {
						if !reflect.DeepEqual(result.Message, domain.AgentMessage{}) || result.Streaming || len(closes) != 0 || probe.MetadataWorkUsedForTesting() != baseline {
							t.Fatal("unowned/refused reply escaped or retained partial work", spec.name)
						}
						if closedOutput != nil && !errors.Is(closedOutput.Err(), store.ErrBoundedMetadataCoverage) {
							t.Fatal("closed output owner did not retain the refusal error", closedOutput.Err())
						}
					} else if source == probe && spec.internal == "off" && !reflect.DeepEqual(result.Message, domain.AgentMessage{}) {
						if probe.MetadataWorkUsedForTesting() <= baseline || len(closes) != 1 {
							t.Fatal("complete reply escaped its source/output owner")
						}
						// Encoding and every nested value still belong to the held
						// point, including native times and offloaded references.
						if _, err := json.Marshal(result.Message); err != nil || probe.MetadataWorkUsedForTesting() <= baseline {
							t.Fatal("returned reply outlived ownership during encoding", err)
						}
					}
					if source == probe {
						if !refusal && (!reflect.DeepEqual(result.Message, outputs[0].Message) || result.Streaming != outputs[0].Streaming) {
							t.Fatal("complete native reply/status differ before output close", spec.name, scaleP21SW159MessageFields(outputs[0].Message, result.Message), outputs[0].Streaming, result.Streaming)
						}
						// Compare native values while owned; the remaining effect
						// comparison retains the independent legacy oracle value.
						result.Message, result.Streaming = outputs[0].Message, outputs[0].Streaming
					}
					for _, close := range closes {
						close()
						close()
					}
					for _, hold := range holds {
						hold.Close()
					}
					if spec.name == "update-writer" {
						// The bounded view shares SQL, not the oracle's native copy.
						if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
							t.Fatal(err)
						}
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					saved, _ := repo.WorkspaceMetadataFields(workspace, "agentSessions")
					result.Saved = saved.AgentSessions
					if spec.operation != "update" && spec.name != "retained-after-replace" && !reflect.DeepEqual(result.Saved, original.AgentSessions) {
						t.Fatal("reply read/resume modified stored session data")
					}
					if spec.name == "update-writer" {
						if len(result.Saved) != 1 || len(result.Saved[0].Messages) != 1 ||
							result.Saved[0].Messages[0].Content != "saved effect" || result.Saved[0].Messages[0].Status != "" {
							t.Fatal("verified writer changed persisted effects")
						}
					}
					if spec.name == "update-draft" && !reflect.DeepEqual(result.Saved, original.AgentSessions) ||
						spec.name == "update-skip" && !reflect.DeepEqual(result.Saved, original.AgentSessions) ||
						spec.name == "update-canceled" && !reflect.DeepEqual(result.Saved, original.AgentSessions) {
						t.Fatal("skipped/canceled update changed session data")
					}
					if probe.WorkspaceMetadataDecodes() != beforeDecodes || probe.MetadataWorkUsedForTesting() != baseline && spec.name != "budget-refused" {
						t.Fatal("worker reply hydrated metadata or leaked work")
					}
					if spec.name == "budget-refused" && probe.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("refused reply leaked after releasing budget holders")
					}
					if pin != nil {
						pin.Close()
						if probe.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("contextless reply retained the caller's old source")
						}
					}
					outputs = append(outputs, result)
				}
				if spec.name == "update-writer" {
					if !outputs[0].DraftChecked || !outputs[1].DraftChecked {
						t.Fatal("writer fixture did not reach owned full draft use")
					}
				}
				if !reflect.DeepEqual(outputs[0], outputs[1]) {
					t.Fatalf("worker reply result/error/effects differ: legacy=%+v bounded=%+v", outputs[0], outputs[1])
				}
			})
		}
		if probe.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("worker reply table retained operation owners")
		}
	}
}
