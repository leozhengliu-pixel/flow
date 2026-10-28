package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Chat replies on the agent worker (FLOW_AGENT_RUNTIME=worker, docs/agent-runtime-design.md §6, §7.1, §7.5). The
// reply is the same streaming chat message the in-process agent saves (agent_live_reply.go): status "streaming"
// while it runs, its parts the text so far, the tool calls with their results and what waits for the owner, and
// the finished (failed, stopped or interrupted) reply in its place at the end. Only who runs the model loop changes:
//
//   - queue: a chat message renders the prompt exactly as the in-process reply does (buildAgentChatPrompt, in the
//     owner's request), saves the empty streaming reply, and queues a chat run acting as the owner (principal mode
//     user) whose payload names the reply. The rendered prompt is kept with the run (agent_run_keys
//     workerChatContextKey) and served by GET …/payload. The request that asked follows the reply (SSE events while
//     the worker posts here, the finished session at the end), so the stream and non-stream routes answer as before.
//   - activities: the worker posts text deltas, tool calls and results, ask_user's question, approvals, the
//     response and errors to POST …/runs/{id}/activities; Flow turns them into the reply's parts in this process
//     (workerChatReply) and saves them (part boundaries at once, text at most once per agentLiveSaveInterval), so
//     the agent.message_* realtime events, a reloaded page and use-agent-reply-follow see the reply as they see an
//     in-process one. A connector's question (MCP elicitation) is attached by the API process that holds it.
//   - waiting: ask_user ends the reply with its question, as in-process; the next message is a new run. A connector
//     set to always ask holds its calls: the reply keeps them pending with approval ids, the worker suspends the
//     run (RunState in agent_session_states, status awaiting), and once every held call is answered through POST
//     …/approvals/{id} Flow queues a resume run that continues the same reply.
//   - stop: POST …/stop cancels the reply's run (a queued or awaiting run at once, a held one through the cancel
//     long-poll); a connector question waiting in the run's call is cancelled too. The reply is saved as stopped.
//   - end: the response finishes the reply; the run's ledger (agent_run_outcomes.go) and tool budget apply through
//     the run guard (chat mode) and /mcp. A run whose worker died is requeued with the reply reset (nothing was
//     written) or interrupted (the reconciler, or the live-reply sweeper once the run ended without saving it).

// workerChatMessagePrefix starts the id of a reply that runs on the worker: agent_message_ plus its first run's id.
const workerChatMessagePrefix = "agent_message_run_"

// Run keys of a chat run (agent_run_keys).
const (
	workerChatContextKey = "chat:context"
	workerChatBudgetKey  = "chat:budget"
)

// workerChatDeclined is the error of a held call the person rejected.
const workerChatDeclined = "Action declined by user"

// workerChatRunInfo is what a chat run's payload says about its reply (agent_runs.payload "chat").
type workerChatRunInfo struct {
	MessageID string `json:"messageId"`
	// LinkBase is the app URL of the request that asked (links in the reply are rebased onto it).
	LinkBase string `json:"linkBase,omitempty"`
	MaxTurns int    `json:"maxTurns"`
	MaxCalls int    `json:"maxCalls"`
	// Request is the owner's message the reply answers (the outcome check reads it).
	Request string `json:"request,omitempty"`
	Chinese bool   `json:"chinese,omitempty"`
	// Started is when the reply started (unix ms).
	Started int64 `json:"started"`
	// Resume is set on a run that continues a suspended reply: the person's decision per held call id.
	Resume *workerChatResume `json:"resume,omitempty"`
}

type workerChatResume struct {
	Decisions map[string]string `json:"decisions"`
}

func workerChatInfo(run store.AgentRun) (workerChatRunInfo, bool) {
	if run.Kind != store.AgentRunKindChat || len(run.Payload) == 0 {
		return workerChatRunInfo{}, false
	}
	var payload struct {
		Chat *workerChatRunInfo `json:"chat"`
	}
	if json.Unmarshal(run.Payload, &payload) != nil || payload.Chat == nil || !strings.HasPrefix(payload.Chat.MessageID, workerChatMessagePrefix) {
		return workerChatRunInfo{}, false
	}
	return *payload.Chat, true
}

func workerChatMessage(id string) bool { return strings.HasPrefix(id, workerChatMessagePrefix) }

// agentChatOnWorker reports whether a chat reply runs on the agent worker. Loop builder conversations (their own
// question tool and drafts), conversations with sent files (read in-process) and approvals saved by earlier
// versions stay in-process.
func (s *server) agentChatOnWorker(session domain.AgentSession) bool {
	if !s.agentRuntimeWorker() || !s.agentRuntimeEnabled() || len(session.LoopIDs) > 0 {
		return false
	}
	if _, _, _, _, legacy := pendingAgentApproval(&session); legacy {
		return false
	}
	return !slices.ContainsFunc(session.Messages, func(message domain.AgentMessage) bool { return len(message.Attachments) > 0 })
}

func newWorkerChatRunID() string {
	random := make([]byte, 6)
	_, _ = rand.Read(random)
	return fmt.Sprintf("run_%016x%s", time.Now().UnixNano(), hex.EncodeToString(random))
}

// workerRunPromptMessages turns the provider conversation (without its system message) into the run payload's.
func workerRunPromptMessages(messages []agentProviderMessage) []agentRunPromptMessage {
	result := make([]agentRunPromptMessage, 0, len(messages))
	for _, message := range messages {
		if message.Role == "system" {
			continue
		}
		item := agentRunPromptMessage{Role: message.Role, Content: message.Content}
		for _, call := range message.ToolCalls {
			arguments := string(call.Arguments)
			if arguments == "" {
				arguments = "{}"
			}
			item.ToolCalls = append(item.ToolCalls, agentRunPromptToolCall{ID: call.ID, Name: agentToolBaseName(call.Name), Arguments: arguments})
		}
		if message.ToolResult != nil {
			item.Role, item.ToolCallID, item.IsError = "tool", message.ToolResult.CallID, message.ToolResult.IsError
			item.Content = message.ToolResult.Content
		}
		result = append(result, item)
	}
	return result
}

// runAgentSessionOnWorker queues the session's next reply for the worker and follows it until it settles: writer
// gets the stream events as the worker's activities arrive here, and the finished session is returned.
func (s *server) runAgentSessionOnWorker(r *http.Request, data domain.Bootstrap, session *domain.AgentSession, writer *agentEventWriter) (domain.AgentSession, error) {
	prompt, err := s.buildAgentChatPrompt(r, &data, *session)
	if err != nil {
		return domain.AgentSession{}, err
	}
	latest := agentLatestUserMessage(*session)
	runID := newWorkerChatRunID()
	started := time.Now().UTC()
	info := workerChatRunInfo{MessageID: "agent_message_" + runID, LinkBase: agentLinkBaseURL(r), MaxTurns: s.agentMaxToolTurns(), MaxCalls: s.agentMaxToolCalls(), Request: latest, Chinese: containsHan(latest), Started: started.UnixMilli()}
	promptContext := agentRunPromptContext{System: prompt.system, Messages: workerRunPromptMessages(prompt.messages), LatestPrompt: latest, WebAccess: prompt.web}
	snapshot := s.withAgentBodies(r, *session)
	// The reply exists before the run is queued, so a reloaded page or another tab sees it working at once.
	err = s.store.MutateWorkspace(store.WithMetadataFields(r.Context(), "agentSessions"), workspaceKey(r), "agent.message_updated", session.ID, nil, func(next *domain.Bootstrap) error {
		current, err := ownedAgentSession(next, session.ID)
		if err != nil {
			return err
		}
		stamp := started
		current.Messages = append(current.Messages, domain.AgentMessage{ID: info.MessageID, Role: "assistant", Parts: []domain.AgentMessagePart{}, Status: agentMessageStreaming, CreatedAt: started, UpdatedAt: &stamp})
		return nil
	})
	if err != nil {
		return domain.AgentSession{}, err
	}
	events, unsubscribe := workerChatSubscribe(info.MessageID)
	defer unsubscribe()
	principal, _ := json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: data.Viewer.ID})
	if _, err := s.enqueueWorkerChatRun(r.Context(), store.AgentRun{ID: runID, WorkspaceKey: workspaceKey(r), SessionID: session.ID, Principal: principal}, info, &promptContext, ""); err != nil {
		run := store.AgentRun{ID: runID, WorkspaceKey: workspaceKey(r), SessionID: session.ID, Kind: store.AgentRunKindChat, Principal: principal}
		if reply, replyErr := s.workerChatReplyFor(context.WithoutCancel(r.Context()), run, info); replyErr == nil {
			reply.finishFailure(context.WithoutCancel(r.Context()), "The reply could not be queued for the agent worker.", false)
		}
		return domain.AgentSession{}, err
	}
	titleDone := s.startAgentSessionTitle(r, *session, prompt.mentions)
	if writer != nil {
		writer.lenient = true
		if err := writer.send(agentStreamEvent{Type: "session.started", Session: &snapshot, MessageID: info.MessageID}); err != nil {
			return domain.AgentSession{}, err
		}
	}
	// Another API process may take the worker's posts: the saved reply is re-read every second as well.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	settled := false
	for !settled {
		select {
		case event := <-events:
			if event.Type == workerChatSettledEvent {
				settled = true
				break
			}
			if writer != nil {
				_ = writer.send(event)
			}
		case <-ticker.C:
			if _, streaming := s.workerChatStoredReply(r.Context(), workspaceKey(r), session.ID, data.Viewer.ID, info.MessageID); !streaming {
				settled = true
			}
		case <-r.Context().Done():
			return domain.AgentSession{}, r.Context().Err()
		}
	}
	if titleDone != nil {
		select {
		case <-titleDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	fresh := s.workspaceData(r)
	completed, err := ownedAgentSession(&fresh, session.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	// A failed or stopped reply ends the request the way an in-process one does.
	for _, message := range completed.Messages {
		if message.ID != info.MessageID || len(message.Parts) == 0 {
			continue
		}
		if last := message.Parts[len(message.Parts)-1]; last.Type == "error" && last.ID == message.ID+"_error" {
			if last.Text == "Generation stopped" {
				return domain.AgentSession{}, context.Canceled
			}
			return domain.AgentSession{}, errors.New(last.Text)
		}
	}
	return *completed, nil
}

// workerChatStoredReply reads the saved reply and whether it is still streaming.
func (s *server) workerChatStoredReply(ctx context.Context, workspace, sessionID, owner, messageID string) (domain.AgentMessage, bool) {
	data, ok := s.store.WorkspaceMetadataFields(workspace, "agentSessions")
	if !ok {
		return domain.AgentMessage{}, false
	}
	for _, session := range data.AgentSessions {
		if session.ID != sessionID || session.UserID != owner {
			continue
		}
		for _, message := range session.Messages {
			if message.ID == messageID {
				return message, message.Status == agentMessageStreaming
			}
		}
	}
	return domain.AgentMessage{}, false
}

// enqueueWorkerChatRun queues a chat run for the reply info names. run carries the id (generated when empty),
// workspace, session and principal; promptContext (a new reply) is kept with the run; copyFrom names the run whose
// ledger and tool budget the new one continues (a resume).
func (s *server) enqueueWorkerChatRun(ctx context.Context, run store.AgentRun, info workerChatRunInfo, promptContext *agentRunPromptContext, copyFrom string) (store.AgentRun, error) {
	if run.ID == "" {
		run.ID = newWorkerChatRunID()
	}
	ctx = context.WithoutCancel(ctx)
	run.Kind = store.AgentRunKindChat
	if promptContext != nil {
		raw, err := json.Marshal(promptContext)
		if err != nil {
			return store.AgentRun{}, err
		}
		if err := s.store.SaveAgentRunKey(ctx, run, workerChatContextKey, raw); err != nil {
			return store.AgentRun{}, err
		}
	}
	if copyFrom != "" {
		for _, key := range []string{agentRunLedgerKey, workerChatBudgetKey} {
			if raw, found, err := s.store.AgentRunKey(ctx, copyFrom, key); err == nil && found {
				_ = s.store.ReplaceAgentRunKey(ctx, run, key, raw)
			}
		}
	}
	payload, err := json.Marshal(map[string]any{"chat": info})
	if err != nil {
		return store.AgentRun{}, err
	}
	idempotency := ""
	if copyFrom != "" {
		idempotency = "chat-resume:" + copyFrom
	}
	queued, _, err := s.store.EnqueueAgentRun(ctx, store.AgentRunInput{ID: run.ID, WorkspaceKey: run.WorkspaceKey, SessionID: run.SessionID, Kind: store.AgentRunKindChat, IdempotencyKey: idempotency, Principal: run.Principal, Payload: payload})
	return queued, err
}

// ---------------------------------------------------------------- stream events

// workerChatSettledEvent tells a follower the reply was saved in its final form (never sent to a browser).
const workerChatSettledEvent = "worker.settled"

type workerChatSubscribers struct {
	mu    sync.Mutex
	chans map[chan agentStreamEvent]struct{}
}

// workerChatFollowers holds the requests following each worker reply in this process (message id →
// *workerChatSubscribers).
var workerChatFollowers sync.Map

func workerChatSubscribe(messageID string) (<-chan agentStreamEvent, func()) {
	value, _ := workerChatFollowers.LoadOrStore(messageID, &workerChatSubscribers{chans: map[chan agentStreamEvent]struct{}{}})
	subscribers := value.(*workerChatSubscribers)
	events := make(chan agentStreamEvent, 256)
	subscribers.mu.Lock()
	subscribers.chans[events] = struct{}{}
	subscribers.mu.Unlock()
	return events, func() {
		subscribers.mu.Lock()
		delete(subscribers.chans, events)
		empty := len(subscribers.chans) == 0
		subscribers.mu.Unlock()
		if empty {
			workerChatFollowers.CompareAndDelete(messageID, subscribers)
		}
	}
}

// workerChatPublish hands events to the reply's followers; a follower that cannot keep up loses deltas (the
// finished session it gets at the end has everything).
func workerChatPublish(messageID string, events ...agentStreamEvent) {
	value, ok := workerChatFollowers.Load(messageID)
	if !ok {
		return
	}
	subscribers := value.(*workerChatSubscribers)
	subscribers.mu.Lock()
	defer subscribers.mu.Unlock()
	for _, event := range events {
		for follower := range subscribers.chans {
			select {
			case follower <- event:
			default:
				if event.Type == workerChatSettledEvent {
					// The end must arrive: make room for it.
					select {
					case <-follower:
					default:
					}
					select {
					case follower <- event:
					default:
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------- the reply

// workerChatReply is a worker chat reply as this process writes it while a run posts to it: the parts in memory
// (as the in-process run keeps them) and saved as a full snapshot.
type workerChatReply struct {
	s     *server
	run   store.AgentRun
	info  workerChatRunInfo
	owner domain.User

	mu        sync.Mutex
	message   domain.AgentMessage
	finished  bool
	dirty     bool
	saveTimer *time.Timer
	narration int
}

// workerChatReplies holds the replies runs post to in this process (message id → *workerChatReply).
var workerChatReplies sync.Map

// workerChatReplyFor is the reply run posts to, read from the conversation when this process does not hold it yet
// (another process, or an earlier run of the reply, wrote it). errAgentLiveSkip: the reply is no longer streaming.
func (s *server) workerChatReplyFor(ctx context.Context, run store.AgentRun, info workerChatRunInfo) (*workerChatReply, error) {
	if value, ok := workerChatReplies.Load(info.MessageID); ok {
		reply := value.(*workerChatReply)
		if reply.run.ID == run.ID {
			return reply, nil
		}
		reply.release()
	}
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		return nil, err
	}
	owner, err := s.store.UserByID(ctx, principal.UserID)
	if err != nil {
		return nil, err
	}
	data, ok := s.store.WorkspaceMetadataFields(run.WorkspaceKey, "agentSessions")
	if !ok {
		return nil, errNotFound
	}
	var stored *domain.AgentSession
	for index := range data.AgentSessions {
		if data.AgentSessions[index].ID == run.SessionID && data.AgentSessions[index].UserID == owner.ID {
			stored = &data.AgentSessions[index]
		}
	}
	if stored == nil {
		return nil, errAgentLiveSkip
	}
	index := slices.IndexFunc(stored.Messages, func(message domain.AgentMessage) bool { return message.ID == info.MessageID })
	if index < 0 || stored.Messages[index].Status != agentMessageStreaming {
		return nil, errAgentLiveSkip
	}
	// Offloaded text and results are loaded: the reply is saved whole.
	one := *stored
	one.Messages = []domain.AgentMessage{stored.Messages[index]}
	if loaded, err := s.store.LoadAgentSessionBodies(ctx, run.WorkspaceKey, one); err == nil && len(loaded.Messages) == 1 {
		one = loaded
	}
	message, err := cloneAgentMessage(one.Messages[0])
	if err != nil {
		return nil, err
	}
	reply := &workerChatReply{s: s, run: run, info: info, owner: owner, message: message}
	for _, part := range message.Parts {
		if part.Type == "reasoning" {
			reply.narration++
		}
	}
	actual, _ := workerChatReplies.LoadOrStore(info.MessageID, reply)
	return actual.(*workerChatReply), nil
}

func cloneAgentMessage(message domain.AgentMessage) (domain.AgentMessage, error) {
	raw, err := json.Marshal(message)
	if err != nil {
		return domain.AgentMessage{}, err
	}
	var copied domain.AgentMessage
	err = json.Unmarshal(raw, &copied)
	if copied.Parts == nil {
		copied.Parts = []domain.AgentMessagePart{}
	}
	copied.Offloaded = nil
	return copied, err
}

func (c *workerChatReply) ctx(parent context.Context) context.Context {
	return store.WithMetadataFields(store.ContextWithActor(context.WithoutCancel(parent), c.owner), "agentSessions")
}

// release stops tracking the reply in this process (its run ended); unsaved text is saved first.
func (c *workerChatReply) release() {
	c.mu.Lock()
	if c.saveTimer != nil {
		c.saveTimer.Stop()
		c.saveTimer = nil
	}
	if c.dirty && !c.finished {
		_ = c.saveLocked(context.Background())
	}
	c.mu.Unlock()
	workerChatReplies.CompareAndDelete(c.info.MessageID, c)
}

// saveLocked saves the reply as it is now (still streaming).
func (c *workerChatReply) saveLocked(parent context.Context) error {
	if c.finished {
		return errAgentLiveSkip
	}
	c.dirty = false
	message, err := cloneAgentMessage(c.message)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	message.Status, message.UpdatedAt, message.Content = agentMessageStreaming, &now, agentLiveText(message.Parts)
	err = c.replaceStored(parent, "agent.message_updated", message, nil)
	if errors.Is(err, errAgentLiveSkip) {
		// The conversation no longer holds the reply as streaming (edited, deleted, or finished elsewhere).
		c.finished = true
	} else if err != nil {
		log.Printf("save worker chat reply %s: %v", c.info.MessageID, err)
	}
	return err
}

// replaceStored puts message in place of the streaming reply; activity (the finished reply) is logged with it.
func (c *workerChatReply) replaceStored(parent context.Context, event string, message domain.AgentMessage, activity *domain.AgentActivity) error {
	return c.s.store.MutateWorkspace(c.ctx(parent), c.run.WorkspaceKey, event, c.run.SessionID, nil, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, c.run.SessionID)
		if err != nil {
			return errAgentLiveSkip
		}
		index := slices.IndexFunc(session.Messages, func(item domain.AgentMessage) bool { return item.ID == message.ID })
		if index < 0 || session.Messages[index].Status != agentMessageStreaming {
			return errAgentLiveSkip
		}
		session.Messages[index] = message
		if activity != nil {
			data.AgentActivities = append(data.AgentActivities, *activity)
			session.UpdatedAt = message.CreatedAt
			if message.UpdatedAt == nil {
				session.UpdatedAt = time.Now().UTC()
			}
		}
		return nil
	})
}

// changed records a change: a part boundary saves at once, streamed text within agentLiveSaveInterval.
func (c *workerChatReply) changedLocked(ctx context.Context, force bool) {
	if force {
		if c.saveTimer != nil {
			c.saveTimer.Stop()
			c.saveTimer = nil
		}
		_ = c.saveLocked(ctx)
		return
	}
	c.dirty = true
	if c.saveTimer == nil {
		c.saveTimer = time.AfterFunc(agentLiveSaveInterval, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.saveTimer = nil
			if c.dirty {
				_ = c.saveLocked(context.Background())
			}
		})
	}
}

func (c *workerChatReply) partIndex(id string) int {
	return slices.IndexFunc(c.message.Parts, func(part domain.AgentMessagePart) bool { return part.ID == id })
}

func (c *workerChatReply) callIndex(callID string) int {
	return slices.IndexFunc(c.message.Parts, func(part domain.AgentMessagePart) bool {
		return part.ToolCall != nil && part.ToolCall.ID == callID && (part.Type == "toolCall" || part.Type == "question")
	})
}

func (c *workerChatReply) textID() string { return c.info.MessageID + "_text" }

func (c *workerChatReply) textLocked() string {
	if index := c.partIndex(c.textID()); index >= 0 {
		return c.message.Parts[index].Text
	}
	return ""
}

// foldNarrationLocked turns the text streamed before a tool call into a step of the work, as the in-process reply
// keeps mid-task narration inside the work group: only the last turn's text is the answer.
func (c *workerChatReply) foldNarrationLocked() []agentStreamEvent {
	index := c.partIndex(c.textID())
	if index < 0 {
		return nil
	}
	narration := strings.TrimSpace(c.message.Parts[index].Text)
	if narration == "" {
		c.message.Parts = slices.Delete(c.message.Parts, index, index+1)
	} else {
		c.narration++
		c.message.Parts[index] = domain.AgentMessagePart{ID: fmt.Sprintf("%s_narration_%d", c.info.MessageID, c.narration), Type: "reasoning", Text: narration, Status: "completed"}
	}
	return []agentStreamEvent{{Type: "text.replaced", MessageID: c.info.MessageID, Delta: ""}}
}

// annotate names a connector call's connector and tool, as the in-process reply shows them.
func (c *workerChatReply) annotate(ctx context.Context, call *domain.AgentToolCall) {
	if !strings.HasPrefix(call.Name, "external_") {
		return
	}
	if tool, ok := c.s.agentRunConnectorTool(ctx, c.run, call.Name); ok {
		call.Connector, call.RemoteTool = tool.Policy.label(), tool.RemoteName
	}
}

// workerChatResultJSON is a tool result as a reply part stores it: the tool's JSON, or the text as a JSON string.
func workerChatResultJSON(result string) json.RawMessage {
	if trimmed := strings.TrimSpace(result); trimmed != "" && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	raw, _ := json.Marshal(result)
	return raw
}

// finishLocked saves message as the reply's final form and tells the followers.
func (c *workerChatReply) finishLocked(ctx context.Context, message domain.AgentMessage) {
	if c.finished {
		return
	}
	if c.saveTimer != nil {
		c.saveTimer.Stop()
		c.saveTimer = nil
	}
	message.Status, message.UpdatedAt = "", nil
	activity := domain.AgentActivity{ID: idgen.New("agent_activity_"), SessionID: c.run.SessionID, Type: "response", Status: "completed", Body: message.Content, Metadata: map[string]any{"durationMs": message.DurationMS, "partCount": len(message.Parts), "runId": c.run.ID}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	err := c.replaceStored(ctx, "agent.message_completed", message, &activity)
	if err != nil && !errors.Is(err, errAgentLiveSkip) {
		log.Printf("save finished worker chat reply %s: %v", c.info.MessageID, err)
		return
	}
	c.finished, c.dirty = true, false
	c.message = message
	workerChatPublish(c.info.MessageID, agentStreamEvent{Type: workerChatSettledEvent, MessageID: c.info.MessageID})
}

func (c *workerChatReply) durationMS() int64 {
	if c.info.Started > 0 {
		return time.Since(time.UnixMilli(c.info.Started)).Milliseconds()
	}
	return 0
}

// completeLocked finishes the reply with its answer, as persistAgentCompletion saves an in-process one.
func (c *workerChatReply) completeLocked(ctx context.Context, answer string, extra ...domain.AgentMessagePart) {
	message, err := cloneAgentMessage(c.message)
	if err != nil {
		return
	}
	workspace := c.run.WorkspaceKey
	answer = rebaseAgentWorkspaceLinks(strings.TrimSpace(answer), workspace, c.info.LinkBase)
	index := slices.IndexFunc(message.Parts, func(part domain.AgentMessagePart) bool { return part.ID == c.textID() })
	switch {
	case index >= 0 && answer == "":
		message.Parts = slices.Delete(message.Parts, index, index+1)
	case index >= 0:
		message.Parts[index].Text, message.Parts[index].Status = answer, "completed"
	case answer != "":
		message.Parts = append(message.Parts, domain.AgentMessagePart{ID: c.textID(), Type: "text", Text: answer, Status: "completed"})
	}
	for index := range message.Parts {
		part := &message.Parts[index]
		if part.Status == "running" {
			part.Status = "completed"
		}
		if part.ToolCall != nil && part.ToolCall.Status == "running" {
			call := *part.ToolCall
			call.Status = "completed"
			part.ToolCall = &call
		}
	}
	message.Parts = append(message.Parts, extra...)
	message.Content, message.DurationMS = answer, c.durationMS()
	workerChatPublish(c.info.MessageID, agentStreamEvent{Type: "text.replaced", MessageID: c.info.MessageID, Delta: answer})
	c.finishLocked(ctx, message)
}

// finishFailure saves the reply as failed (or stopped), as persistAgentFailure saves an in-process one.
func (c *workerChatReply) finishFailure(ctx context.Context, reason string, stopped bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failLocked(ctx, reason, stopped)
}

func (c *workerChatReply) failLocked(ctx context.Context, reason string, stopped bool) {
	message, err := cloneAgentMessage(c.message)
	if err != nil {
		return
	}
	settleInterruptedAgentParts(message.Parts, stopped)
	for index := range message.Parts {
		part := &message.Parts[index]
		if part.Status == "running" || part.Status == "pending" {
			part.Status = "error"
		}
		if part.ToolCall != nil && (part.ToolCall.Status == "running" || part.ToolCall.Status == "pending") {
			call := *part.ToolCall
			call.Status, call.ApprovalID = "error", ""
			part.ToolCall = &call
		}
	}
	if stopped {
		reason = "Generation stopped"
	}
	if strings.TrimSpace(reason) == "" {
		reason = "Flow Agent is unavailable"
	}
	message.Parts = append(message.Parts, domain.AgentMessagePart{ID: message.ID + "_error", Type: "error", Text: agentClip(reason, 2000), Status: "error"})
	message.Content, message.DurationMS = strings.TrimSpace(c.textLocked()), c.durationMS()
	c.finishLocked(ctx, message)
}

// interrupt saves the reply as interrupted (its run's worker went away), as the live-reply sweeper does.
func (c *workerChatReply) interrupt(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	message, err := cloneAgentMessage(c.message)
	if err != nil {
		return
	}
	interruptAgentLiveMessage(&message)
	message.DurationMS = c.durationMS()
	c.finishLocked(ctx, message)
}

// reset empties the reply for a new attempt of its run (the previous one wrote nothing).
func (c *workerChatReply) reset(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.message.Parts, c.narration = []domain.AgentMessagePart{}, 0
	c.changedLocked(ctx, true)
	workerChatPublish(c.info.MessageID, agentStreamEvent{Type: "text.replaced", MessageID: c.info.MessageID, Delta: ""})
}

// ---------------------------------------------------------------- activities

// workerChatActivity is an activity a chat run posts (the worker's FlowActivity, design doc §5).
type workerChatActivity struct {
	ActivityID string `json:"activityId"`
	// Type: thought, text.delta, question.delta, action, action.result, elicitation, response, error.
	Type string `json:"type"`
	Body string `json:"body"`
	// CallID is the tool call an action, action.result or question.delta is about.
	CallID  string `json:"callId"`
	Content *struct {
		Action    string          `json:"action"`
		Parameter json.RawMessage `json:"parameter"`
		Result    string          `json:"result"`
	} `json:"content"`
	IsError bool `json:"isError"`
	// Change is the change-set entry /mcp recorded for the call (agentRunChangeRef).
	Change      *agentRunChangeRef     `json:"change"`
	Elicitation *workerChatElicitation `json:"elicitation"`
}

// workerChatElicitation is what a suspended run waits for: ask_user's question (select) or held calls (approval).
type workerChatElicitation struct {
	Kind          string                       `json:"kind"`
	CallID        string                       `json:"callId"`
	Arguments     string                       `json:"arguments"`
	Body          string                       `json:"body"`
	Options       []domain.AgentQuestionOption `json:"options"`
	AllowFreeText bool                         `json:"allowFreeText"`
	Calls         []struct {
		CallID    string `json:"callId"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"calls"`
}

var workerChatActivityTypes = []string{"thought", "text.delta", "question.delta", "action", "action.result", "elicitation", "response", "error"}

// postWorkerChatActivity applies one activity of a chat run to its reply.
func (s *server) postWorkerChatActivity(w http.ResponseWriter, r *http.Request, run store.AgentRun, info workerChatRunInfo) {
	var input workerChatActivity
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.ActivityID) > 128 || len(input.CallID) > 256 {
		writeError(w, http.StatusBadRequest, "activityId or callId is too long")
		return
	}
	if !slices.Contains(workerChatActivityTypes, input.Type) {
		writeError(w, http.StatusBadRequest, "type must be thought, text.delta, question.delta, action, action.result, elicitation, response or error")
		return
	}
	if (input.Type == "action" || input.Type == "action.result") && (input.CallID == "" || input.Content == nil) {
		writeError(w, http.StatusBadRequest, "action activities need callId and content")
		return
	}
	if input.Type == "elicitation" && (input.Elicitation == nil || input.Elicitation.Kind != "select" && input.Elicitation.Kind != "approval") {
		writeError(w, http.StatusBadRequest, "an elicitation is a select or an approval")
		return
	}
	key := ""
	if input.ActivityID != "" {
		key = "activity:" + input.ActivityID
		if cached, found, err := s.store.AgentRunKey(r.Context(), run.ID, key); err == nil && found {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(cached)
			return
		}
	}
	reply, err := s.workerChatReplyFor(r.Context(), run, info)
	if errors.Is(err, errAgentLiveSkip) {
		// The reply finished (stopped, edited away): nothing more is saved.
		writeJSON(w, http.StatusOK, map[string]any{"messageId": info.MessageID, "settled": true})
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, "the run's reply cannot be written")
		return
	}
	settled := reply.apply(r.Context(), input)
	if settled {
		reply.release()
	}
	body, _ := json.Marshal(map[string]any{"messageId": info.MessageID, "settled": settled})
	if key != "" {
		_ = s.store.SaveAgentRunKey(r.Context(), run, key, body)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

// apply changes the reply by one activity and reports whether the reply is finished.
func (c *workerChatReply) apply(ctx context.Context, input workerChatActivity) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return true
	}
	messageID := c.info.MessageID
	switch input.Type {
	case "thought":
		// The ack and reasoning: the person sees "Thinking…" and the running step instead (not saved, as in-process).
	case "text.delta":
		if input.Body == "" {
			break
		}
		index := c.partIndex(c.textID())
		created := index < 0
		if created {
			index = len(c.message.Parts)
			c.message.Parts = append(c.message.Parts, domain.AgentMessagePart{ID: c.textID(), Type: "text", Status: "running"})
		}
		c.message.Parts[index].Text += input.Body
		part := c.message.Parts[index]
		part.Text = ""
		workerChatPublish(messageID, agentStreamEvent{Type: "text.delta", MessageID: messageID, Delta: input.Body, Part: &part})
		c.changedLocked(ctx, created)
	case "question.delta":
		// ask_user's question while the model writes it: a running question part the finished question replaces.
		id := fmt.Sprintf("%s_question_%s", messageID, input.CallID)
		part := domain.AgentMessagePart{ID: id, Type: "question", Status: "running", Text: input.Body, Question: &domain.AgentQuestion{Options: []domain.AgentQuestionOption{}}}
		index := c.partIndex(id)
		if index < 0 {
			c.message.Parts = append(c.message.Parts, part)
		} else {
			c.message.Parts[index] = part
		}
		workerChatPublish(messageID, agentStreamEvent{Type: "tool.delta", MessageID: messageID, Part: &part})
		c.changedLocked(ctx, index < 0)
	case "action":
		name := agentToolBaseName(input.Content.Action)
		if name == agentLoadToolsTool || name == askUserTool {
			// Bookkeeping, or the question (question.delta and the elicitation show it).
			break
		}
		events := c.foldNarrationLocked()
		call := domain.AgentToolCall{ID: input.CallID, Name: name, Arguments: workerChatArguments(input.Content.Parameter), Status: "running"}
		c.annotate(ctx, &call)
		index := c.callIndex(input.CallID)
		if index < 0 {
			index = len(c.message.Parts)
			c.message.Parts = append(c.message.Parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, index), Type: "toolCall", Status: "running", ToolCall: &call})
		} else {
			c.message.Parts[index].Status, c.message.Parts[index].ToolCall = "running", &call
		}
		part := c.message.Parts[index]
		workerChatPublish(messageID, append(events, agentStreamEvent{Type: "tool.started", MessageID: messageID, Part: &part})...)
		c.changedLocked(ctx, true)
	case "action.result":
		name := agentToolBaseName(input.Content.Action)
		if name == agentLoadToolsTool || name == askUserTool {
			break
		}
		index := c.callIndex(input.CallID)
		if index < 0 {
			// A held call the person approved runs on resume without a new call activity.
			events := c.foldNarrationLocked()
			call := domain.AgentToolCall{ID: input.CallID, Name: name, Status: "running"}
			c.annotate(ctx, &call)
			index = len(c.message.Parts)
			c.message.Parts = append(c.message.Parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, index), Type: "toolCall", Status: "running", ToolCall: &call})
			workerChatPublish(messageID, events...)
		}
		current := c.message.Parts[index].ToolCall
		if current != nil && current.Error == workerChatDeclined {
			// The rejected call's result is the rejection the model was told.
			break
		}
		call := domain.AgentToolCall{ID: input.CallID, Name: name}
		if current != nil {
			call = *current
		}
		call.ApprovalID = ""
		if input.IsError {
			text := strings.TrimSpace(input.Content.Result)
			call.Status, call.Error = "error", agentClip(text, 2000)
			call.Result, _ = json.Marshal(map[string]string{"error": call.Error})
		} else {
			call.Status, call.Error, call.Result = "completed", "", workerChatResultJSON(input.Content.Result)
		}
		if change := c.s.workerChatChange(ctx, c.run, input.Change); change != nil {
			call.Change = change
		}
		c.message.Parts[index].Status, c.message.Parts[index].ToolCall = call.Status, &call
		part := c.message.Parts[index]
		workerChatPublish(messageID, agentStreamEvent{Type: "tool.completed", MessageID: messageID, Part: &part})
		c.changedLocked(ctx, true)
	case "elicitation":
		if input.Elicitation.Kind == "select" {
			c.askLocked(ctx, *input.Elicitation)
			break
		}
		c.holdLocked(ctx, *input.Elicitation)
	case "response":
		if c.s.workerChatCancelRequested(ctx, c.run.ID) {
			c.failLocked(ctx, "", true)
			break
		}
		answer := c.s.workerChatCheckedAnswer(ctx, c.run, c.info, input.Body)
		extra := []domain.AgentMessagePart{}
		if budget := c.s.workerChatBudget(ctx, c.run.ID); budget.stop != "" {
			if strings.TrimSpace(answer) == "" {
				answer = agentToolBudgetFallback(budget.stop, c.info.MaxTurns, c.info.MaxCalls, c.info.Chinese, c.changesLocked(), true)
			}
			extra = append(extra, domain.AgentMessagePart{ID: messageID + "_budget", Type: "event", Text: agentToolBudgetNotice(budget.stop, c.info.MaxTurns, c.info.MaxCalls, c.info.Chinese), Status: "completed"})
		}
		if strings.TrimSpace(answer) == "" {
			c.failLocked(ctx, "Flow Agent provider returned an empty response", false)
			break
		}
		c.completeLocked(ctx, answer, extra...)
	case "error":
		c.failLocked(ctx, input.Body, c.s.workerChatCancelRequested(ctx, c.run.ID))
	}
	return c.finished
}

func (c *workerChatReply) changesLocked() int {
	changes := 0
	for _, part := range c.message.Parts {
		if part.ToolCall != nil && part.ToolCall.Change != nil {
			changes++
		}
	}
	return changes
}

// askLocked ends the reply with ask_user's question, as the in-process reply ends on one: the person answers with
// their next message.
func (c *workerChatReply) askLocked(ctx context.Context, elicitation workerChatElicitation) {
	messageID := c.info.MessageID
	arguments := workerChatArguments(json.RawMessage(elicitation.Arguments))
	call := domain.AgentToolCall{ID: elicitation.CallID, Name: askUserTool, Arguments: arguments, Status: "pending"}
	question, options, err := parseAskUser(call)
	if err != nil {
		question = strings.TrimSpace(elicitation.Body)
		options = domain.AgentQuestion{Options: elicitation.Options, AllowFreeText: elicitation.AllowFreeText}
		if options.Options == nil {
			options.Options = []domain.AgentQuestionOption{}
		}
	}
	id := fmt.Sprintf("%s_question_%s", messageID, elicitation.CallID)
	part := domain.AgentMessagePart{ID: id, Type: "question", Text: question, Status: "pending", ToolCall: &call, Question: &options}
	if index := c.partIndex(id); index >= 0 {
		c.message.Parts[index] = part
	} else {
		c.message.Parts = append(c.message.Parts, part)
	}
	workerChatPublish(messageID, agentStreamEvent{Type: "tool.completed", MessageID: messageID, Part: &part})
	message, err := cloneAgentMessage(c.message)
	if err != nil {
		return
	}
	for index := range message.Parts {
		if message.Parts[index].Status == "running" {
			message.Parts[index].Status = "completed"
		}
	}
	text := rebaseAgentWorkspaceLinks(strings.TrimSpace(c.textLocked()), c.run.WorkspaceKey, c.info.LinkBase)
	if index := slices.IndexFunc(message.Parts, func(part domain.AgentMessagePart) bool { return part.ID == c.textID() }); index >= 0 {
		message.Parts[index].Text = text
	}
	// The reply's text (and what the model sees of it later) ends with the question; the part draws its options.
	message.Content, message.DurationMS = strings.TrimSpace(text+"\n\n"+question), c.durationMS()
	c.finishLocked(ctx, message)
}

// holdLocked shows the calls a connector set to always ask holds, each waiting for the person's decision.
func (c *workerChatReply) holdLocked(ctx context.Context, elicitation workerChatElicitation) {
	messageID := c.info.MessageID
	events := c.foldNarrationLocked()
	for _, held := range elicitation.Calls {
		approvalID, err := randomSecret("agent_approval_")
		if err != nil {
			continue
		}
		call := domain.AgentToolCall{ID: held.CallID, Name: agentToolBaseName(held.Name), Arguments: workerChatArguments(json.RawMessage(held.Arguments)), Status: "pending", ApprovalID: approvalID}
		c.annotate(ctx, &call)
		index := c.callIndex(held.CallID)
		if index < 0 {
			index = len(c.message.Parts)
			c.message.Parts = append(c.message.Parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, index), Type: "toolCall"})
		}
		c.message.Parts[index].Status, c.message.Parts[index].ToolCall = "pending", &call
		part := c.message.Parts[index]
		events = append(events, agentStreamEvent{Type: "tool.approval_required", MessageID: messageID, ApprovalID: approvalID, Part: &part})
	}
	workerChatPublish(messageID, events...)
	c.changedLocked(ctx, true)
}

// workerChatArguments is a call's arguments as a part keeps them (a JSON object).
func workerChatArguments(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	// The worker may send the arguments as a JSON string of JSON.
	var text string
	if json.Unmarshal([]byte(trimmed), &text) == nil {
		trimmed = strings.TrimSpace(text)
	}
	if !json.Valid([]byte(trimmed)) {
		encoded, _ := json.Marshal(map[string]string{"input": trimmed})
		return encoded
	}
	return json.RawMessage(trimmed)
}

// workerChatChange is the change /mcp recorded for a call of the reply's runs, for the result card and Undo.
func (s *server) workerChatChange(ctx context.Context, run store.AgentRun, ref *agentRunChangeRef) *domain.AgentChange {
	if ref == nil || ref.RunID == "" {
		return nil
	}
	if ref.RunID != run.ID {
		other, err := s.store.AgentRun(ctx, ref.RunID)
		if err != nil || other.WorkspaceKey != run.WorkspaceKey || other.SessionID != run.SessionID {
			return nil
		}
	}
	changes, err := s.store.AgentRunChanges(ctx, ref.RunID)
	if err != nil {
		return nil
	}
	for _, item := range changes {
		if item.Seq == ref.Seq {
			change := item.Change
			return &change
		}
	}
	return nil
}

func (s *server) workerChatCancelRequested(ctx context.Context, runID string) bool {
	run, err := s.store.AgentRun(ctx, runID)
	return err == nil && (run.CancelRequested || run.Status == store.AgentRunCanceled)
}

// workerChatCheckedAnswer is the response a chat run posts, checked against the run's ledger when the run did not
// have its last word through the guard (which checked it then), as agentRunCheckedResponse checks a thread run's.
func (s *server) workerChatCheckedAnswer(ctx context.Context, run store.AgentRun, info workerChatRunInfo, body string) string {
	if raw, found, err := s.store.AgentRunKey(ctx, run.ID, workerRunGuardKey); err == nil && found {
		var state workerRunGuardState
		if json.Unmarshal(raw, &state) == nil && state.Finished {
			return body
		}
	}
	if body == agentNothingExecutedText(true) || body == agentNothingExecutedText(false) {
		return body
	}
	ledger := s.agentRunLedger(ctx, run)
	if info.Resume == nil && s.workerChatBudget(ctx, run.ID).stop == "" && s.agentUngroundedText(info.Request, body, ledger) {
		return agentNothingExecutedText(info.Chinese)
	}
	return ledger.report(body, info.Chinese)
}

// ---------------------------------------------------------------- run end, stop, approvals

// settleWorkerChatRun saves a chat run's reply when the worker finishes the run without having finished the reply.
func (s *server) settleWorkerChatRun(ctx context.Context, run store.AgentRun, info workerChatRunInfo, status, failure string) {
	reply, err := s.workerChatReplyFor(ctx, run, info)
	if err != nil {
		return
	}
	defer reply.release()
	reply.mu.Lock()
	defer reply.mu.Unlock()
	if reply.finished {
		return
	}
	switch status {
	case store.AgentRunAwaiting:
		// Held calls wait for the person (the reply stays streaming); anything else is an unfinished reply.
		if slices.ContainsFunc(reply.message.Parts, func(part domain.AgentMessagePart) bool {
			return part.ToolCall != nil && part.ToolCall.ApprovalID != "" && part.ToolCall.Status == "pending"
		}) {
			if reply.dirty {
				_ = reply.saveLocked(ctx)
			}
			return
		}
		reply.failLocked(ctx, "The reply stopped waiting for an answer it never asked for.", false)
	case store.AgentRunDone:
		if text := reply.textLocked(); strings.TrimSpace(text) != "" {
			reply.completeLocked(ctx, s.workerChatCheckedAnswer(ctx, run, info, text))
			return
		}
		reply.failLocked(ctx, "Flow Agent provider returned an empty response", false)
	case store.AgentRunCanceled:
		reply.failLocked(ctx, "", true)
	case store.AgentRunInterrupted:
		message, err := cloneAgentMessage(reply.message)
		if err == nil {
			interruptAgentLiveMessage(&message)
			reply.finishLocked(ctx, message)
		}
	default:
		reply.failLocked(ctx, firstNonEmpty(strings.TrimSpace(failure), "The agent run failed."), false)
	}
}

// resumeWorkerChat queues the run that continues an awaiting reply once the person answered every call it holds.
func (s *server) resumeWorkerChat(ctx context.Context, run store.AgentRun, info workerChatRunInfo) {
	current, err := s.store.AgentRun(ctx, run.ID)
	if err != nil || current.Status != store.AgentRunAwaiting {
		return
	}
	message, streaming := s.workerChatStoredReplyOf(ctx, current, info)
	if !streaming {
		return
	}
	decisions := map[string]string{}
	for _, part := range message.Parts {
		call := part.ToolCall
		if call == nil || call.ApprovalID == "" {
			continue
		}
		switch {
		case call.Status == "pending":
			return
		case call.Error == workerChatDeclined:
			decisions[call.ID] = "rejected"
		default:
			decisions[call.ID] = "approved"
		}
	}
	if len(decisions) == 0 {
		return
	}
	next := info
	next.Resume = &workerChatResume{Decisions: decisions}
	if _, err := s.enqueueWorkerChatRun(ctx, store.AgentRun{WorkspaceKey: current.WorkspaceKey, SessionID: current.SessionID, Principal: current.Principal}, next, nil, current.ID); err != nil {
		log.Printf("resume worker chat reply %s: %v", info.MessageID, err)
	}
}

func (s *server) workerChatStoredReplyOf(_ context.Context, run store.AgentRun, info workerChatRunInfo) (domain.AgentMessage, bool) {
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		return domain.AgentMessage{}, false
	}
	return s.workerChatStoredReply(context.Background(), run.WorkspaceKey, run.SessionID, principal.UserID, info.MessageID)
}

// workerChatRunOf is the latest run of a worker reply.
func (s *server) workerChatRunOf(ctx context.Context, workspace, sessionID, messageID string) (store.AgentRun, workerChatRunInfo, bool) {
	runs, err := s.store.SessionAgentRuns(ctx, workspace, sessionID, 50)
	if err != nil {
		return store.AgentRun{}, workerChatRunInfo{}, false
	}
	for _, run := range runs {
		if info, ok := workerChatInfo(run); ok && info.MessageID == messageID {
			return run, info, true
		}
	}
	return store.AgentRun{}, workerChatRunInfo{}, false
}

// stopWorkerChat stops the worker replies of a chat that are still being written; false when there are none.
func (s *server) stopWorkerChat(ctx context.Context, session domain.AgentSession, workspace string) bool {
	stopped := false
	for _, message := range session.Messages {
		if message.Status != agentMessageStreaming || !workerChatMessage(message.ID) {
			continue
		}
		run, info, ok := s.workerChatRunOf(ctx, workspace, session.ID, message.ID)
		if !ok {
			continue
		}
		stopped = true
		canceled, err := s.store.CancelAgentRun(ctx, run.ID)
		if err != nil {
			continue
		}
		cancelWorkerRunCalls(run.ID)
		if store.AgentRunHeld(canceled.Status) {
			// The worker stops within moments (cancel long-poll) and finishes the run canceled.
			continue
		}
		_ = s.store.DeleteAgentSessionState(ctx, workspace, session.ID)
		if reply, err := s.workerChatReplyFor(ctx, canceled, info); err == nil {
			reply.finishFailure(ctx, "", true)
			reply.release()
		}
	}
	return stopped
}

// answerWorkerChatApproval records the person's decision on a call a worker reply holds; handled is false when no
// worker reply of the session holds approvalID.
func (s *server) answerWorkerChatApproval(r *http.Request, session domain.AgentSession, approvalID, decision string) (handled bool, err error) {
	ctx := r.Context()
	session = s.withAgentBodies(r, session)
	for _, message := range session.Messages {
		if message.Status != agentMessageStreaming || !workerChatMessage(message.ID) {
			continue
		}
		if !slices.ContainsFunc(message.Parts, func(part domain.AgentMessagePart) bool {
			return part.ToolCall != nil && part.ToolCall.ApprovalID == approvalID
		}) {
			continue
		}
		run, info, ok := s.workerChatRunOf(ctx, workspaceKey(r), session.ID, message.ID)
		if !ok {
			return false, nil
		}
		reply, err := s.workerChatReplyFor(ctx, run, info)
		if err != nil {
			return true, errNotFound
		}
		answered := reply.decide(ctx, approvalID, decision)
		if !store.AgentRunHeld(run.Status) {
			// The awaiting run is done with this process; its resume rebuilds the reply.
			reply.release()
		}
		if !answered {
			return true, errNotFound
		}
		s.resumeWorkerChat(ctx, run, info)
		return true, nil
	}
	return false, nil
}

// decide applies a decision to the held call; false when it no longer waits.
func (c *workerChatReply) decide(ctx context.Context, approvalID, decision string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finished {
		return false
	}
	index := slices.IndexFunc(c.message.Parts, func(part domain.AgentMessagePart) bool {
		return part.ToolCall != nil && part.ToolCall.ApprovalID == approvalID && part.ToolCall.Status == "pending"
	})
	if index < 0 {
		return false
	}
	call := *c.message.Parts[index].ToolCall
	if decision == "approved" {
		call.Status = "running"
		c.message.Parts[index].Status = "running"
	} else {
		call.Status, call.Error = "error", workerChatDeclined
		call.Result = json.RawMessage(`{"error":"Action declined by user"}`)
		c.message.Parts[index].Status = "error"
	}
	c.message.Parts[index].ToolCall = &call
	part := c.message.Parts[index]
	workerChatPublish(c.info.MessageID, agentStreamEvent{Type: "tool.approval_resolved", MessageID: c.info.MessageID, ApprovalID: approvalID, Decision: decision, Part: &part})
	c.changedLocked(ctx, true)
	return true
}

// workerChatElicitationPart shows (or resolves) a connector's question on the reply of the chat run it was asked in.
func (s *server) workerChatElicitationPart(ctx context.Context, run store.AgentRun, part domain.AgentMessagePart, kind string) {
	info, ok := workerChatInfo(run)
	if !ok {
		return
	}
	reply, err := s.workerChatReplyFor(ctx, run, info)
	if err != nil {
		return
	}
	reply.mu.Lock()
	defer reply.mu.Unlock()
	if reply.finished {
		return
	}
	if index := reply.partIndex(part.ID); index >= 0 {
		reply.message.Parts[index] = part
	} else {
		reply.message.Parts = append(reply.message.Parts, part)
	}
	workerChatPublish(info.MessageID, agentStreamEvent{Type: kind, MessageID: info.MessageID, Part: &part})
	reply.changedLocked(ctx, true)
}

// workerRunCallStops cancels the connector questions waiting in a run's calls in this process (run id →
// *workerRunCalls), so a Stop ends them at once.
var workerRunCallStops sync.Map

type workerRunCalls struct {
	mu      sync.Mutex
	cancels map[*context.CancelFunc]struct{}
}

func cancelWorkerRunCalls(runID string) {
	value, ok := workerRunCallStops.Load(runID)
	if !ok {
		return
	}
	calls := value.(*workerRunCalls)
	calls.mu.Lock()
	defer calls.mu.Unlock()
	for cancel := range calls.cancels {
		(*cancel)()
	}
}

// agentRunStopContext is ctx, also cancelled when the run is stopped: here (cancelWorkerRunCalls), or anywhere
// (its cancel is requested or it is no longer held, re-checked every agentRuntimeCancelRecheck).
func (s *server) agentRunStopContext(parent context.Context, runID string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	value, _ := workerRunCallStops.LoadOrStore(runID, &workerRunCalls{cancels: map[*context.CancelFunc]struct{}{}})
	calls := value.(*workerRunCalls)
	calls.mu.Lock()
	calls.cancels[&cancel] = struct{}{}
	calls.mu.Unlock()
	go func() {
		ticker := time.NewTicker(agentRuntimeCancelRecheck)
		defer ticker.Stop()
		for {
			wake := s.store.AgentRunCancelWake()
			if run, err := s.store.AgentRun(ctx, runID); err == nil && (run.CancelRequested || !store.AgentRunHeld(run.Status)) {
				cancel()
			}
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-ticker.C:
			}
		}
	}()
	return ctx, func() {
		cancel()
		calls.mu.Lock()
		delete(calls.cancels, &cancel)
		empty := len(calls.cancels) == 0
		calls.mu.Unlock()
		if empty {
			workerRunCallStops.CompareAndDelete(runID, calls)
		}
	}
}

// ---------------------------------------------------------------- recovery

// recoverWorkerChatRun handles a chat run the reconciler released: requeued (nothing written) resets its reply for
// the new attempt; interrupted saves the reply as interrupted.
func (s *server) recoverWorkerChatRun(ctx context.Context, run store.AgentRun, requeued bool) bool {
	info, ok := workerChatInfo(run)
	if !ok {
		return false
	}
	reply, err := s.workerChatReplyFor(ctx, run, info)
	if err != nil {
		return true
	}
	if requeued {
		// The new attempt starts over: its ledger and budget too (what was recorded was not written).
		for _, key := range []string{agentRunLedgerKey, workerChatBudgetKey, workerRunGuardKey} {
			if _, found, err := s.store.AgentRunKey(ctx, run.ID, key); err == nil && found {
				_ = s.store.ReplaceAgentRunKey(ctx, run, key, []byte(`null`))
			}
		}
		reply.reset(ctx)
		reply.release()
		return true
	}
	reply.interrupt(ctx)
	reply.release()
	return true
}

// workerChatReplyAbandoned reports whether a stored worker reply has no run left to finish it: its latest run
// ended (more than a minute ago, so the run's own completion saves it first) or is missing.
func (s *server) workerChatReplyAbandoned(ctx context.Context, workspace, sessionID string, message domain.AgentMessage, now time.Time) (store.AgentRun, workerChatRunInfo, bool) {
	run, info, ok := s.workerChatRunOf(ctx, workspace, sessionID, message.ID)
	if !ok {
		return run, info, message.UpdatedAt == nil || now.Sub(*message.UpdatedAt) > agentLiveStaleAfter
	}
	switch run.Status {
	case store.AgentRunQueued, store.AgentRunLeased, store.AgentRunRunning, store.AgentRunAwaiting:
		return run, info, false
	}
	return run, info, now.Sub(time.UnixMilli(run.FinishedAt)) > time.Minute
}

// sweepWorkerChatReplies saves worker replies whose runs ended without saving them (stopped ones as stopped, the
// others as interrupted); it returns how many it changed.
func (s *server) sweepWorkerChatReplies(ctx context.Context, now time.Time) int {
	swept := 0
	for _, key := range s.store.WorkspaceKeys() {
		if ctx.Err() != nil {
			return swept
		}
		data, ok := s.store.WorkspaceMetadataFields(key, "agentSessions")
		if !ok {
			continue
		}
		for _, session := range data.AgentSessions {
			for _, message := range session.Messages {
				if message.Status != agentMessageStreaming || !workerChatMessage(message.ID) {
					continue
				}
				run, info, abandoned := s.workerChatReplyAbandoned(ctx, key, session.ID, message, now)
				if !abandoned {
					continue
				}
				if run.ID == "" {
					// No run names the reply: interrupt it as the conversation holds it.
					run = store.AgentRun{ID: strings.TrimPrefix(message.ID, "agent_message_"), WorkspaceKey: key, SessionID: session.ID, Kind: store.AgentRunKindChat}
					run.Principal, _ = json.Marshal(agentRunPrincipal{Mode: agentRunModeUser, UserID: session.UserID})
					info = workerChatRunInfo{MessageID: message.ID}
				}
				reply, err := s.workerChatReplyFor(ctx, run, info)
				if err != nil {
					continue
				}
				if run.Status == store.AgentRunCanceled {
					reply.finishFailure(ctx, "", true)
				} else {
					reply.interrupt(ctx)
				}
				reply.release()
				swept++
			}
		}
	}
	return swept
}

// ---------------------------------------------------------------- tool budget

// workerChatBudgetState is a chat run's agentToolBudget as stored between its /mcp calls and guard turns.
type workerChatBudgetState struct {
	Calls       int               `json:"calls"`
	Failed      map[string]string `json:"failed,omitempty"`
	Repeats     int               `json:"repeats"`
	FailedTurns int               `json:"failedTurns"`
	TurnCalls   int               `json:"turnCalls"`
	TurnFailed  int               `json:"turnFailed"`
	Stop        string            `json:"stop,omitempty"`
	Nudged      bool              `json:"nudged,omitempty"`
}

var workerChatBudgetLocks sync.Map

func (s *server) workerChatBudgetLock(runID string) func() {
	lock, _ := workerChatBudgetLocks.LoadOrStore(runID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// workerChatBudget reads a chat run's tool budget.
func (s *server) workerChatBudget(ctx context.Context, runID string) *agentToolBudget {
	budget, _ := s.workerChatBudgetState(ctx, runID)
	return budget
}

func (s *server) workerChatBudgetState(ctx context.Context, runID string) (*agentToolBudget, workerChatBudgetState) {
	maxCalls := s.agentMaxToolCalls()
	var state workerChatBudgetState
	if raw, found, err := s.store.AgentRunKey(ctx, runID, workerChatBudgetKey); err == nil && found {
		_ = json.Unmarshal(raw, &state)
	}
	if run, err := s.store.AgentRun(ctx, runID); err == nil {
		if info, ok := workerChatInfo(run); ok && info.MaxCalls > 0 {
			maxCalls = info.MaxCalls
		}
	}
	budget := newAgentToolBudget(maxCalls)
	budget.calls, budget.repeats, budget.failedTurns, budget.turnCalls, budget.turnFailed, budget.stop = state.Calls, state.Repeats, state.FailedTurns, state.TurnCalls, state.TurnFailed, state.Stop
	for key, value := range state.Failed {
		budget.failed[key] = value
	}
	return budget, state
}

func (s *server) saveWorkerChatBudget(ctx context.Context, run store.AgentRun, budget *agentToolBudget, nudged bool) {
	state := workerChatBudgetState{Calls: budget.calls, Failed: budget.failed, Repeats: budget.repeats, FailedTurns: budget.failedTurns, TurnCalls: budget.turnCalls, TurnFailed: budget.turnFailed, Stop: budget.stop, Nudged: nudged}
	raw, err := json.Marshal(state)
	if err == nil {
		err = s.store.ReplaceAgentRunKey(context.WithoutCancel(ctx), run, workerChatBudgetKey, raw)
	}
	if err != nil {
		log.Printf("agent run %s: tool budget not saved: %v", run.ID, err)
	}
}

// workerChatToolBefore applies a chat run's tool budget to a /mcp call, as the in-process reply does before each
// call: run is false when the call is not run, content its result (an error).
func (s *server) workerChatToolBefore(ctx context.Context, run store.AgentRun, name string, args map[string]any) (content string, ok bool) {
	if _, chat := workerChatInfo(run); !chat {
		return "", true
	}
	unlock := s.workerChatBudgetLock(run.ID)
	defer unlock()
	budget, state := s.workerChatBudgetState(ctx, run.ID)
	arguments, _ := json.Marshal(args)
	content, ok = budget.before(domain.AgentToolCall{Name: name, Arguments: arguments})
	s.saveWorkerChatBudget(ctx, run, budget, state.Nudged)
	return content, ok
}

// workerChatToolAfter records a chat run's /mcp call outcome in its tool budget.
func (s *server) workerChatToolAfter(ctx context.Context, run store.AgentRun, name string, args map[string]any, content string, failed bool) {
	if _, chat := workerChatInfo(run); !chat {
		return
	}
	unlock := s.workerChatBudgetLock(run.ID)
	defer unlock()
	budget, state := s.workerChatBudgetState(ctx, run.ID)
	arguments, _ := json.Marshal(args)
	budget.after(domain.AgentToolCall{Name: name, Arguments: arguments}, content, failed)
	s.saveWorkerChatBudget(ctx, run, budget, state.Nudged)
}

// workerChatGuardOp is the run guard of a chat run (agent_runtime_guard.go): no coding context or connector
// budget, but the reply's tool budget at each model turn and the outcome check at its last word, as the in-process
// reply applies them.
func (s *server) workerChatGuardOp(ctx context.Context, run store.AgentRun, info workerChatRunInfo, state *workerRunGuardState, input workerRunGuardRequest) (workerRunGuardResponse, error) {
	response := workerRunGuardResponse{Enabled: true, DecisionRound: -1}
	switch input.Op {
	case "start", "before", "after", "coding":
	case "turn":
		unlock := s.workerChatBudgetLock(run.ID)
		budget, budgetState := s.workerChatBudgetState(ctx, run.ID)
		if input.Round > 0 {
			budget.endTurn()
		}
		maxTurns := info.MaxTurns
		if maxTurns <= 0 {
			maxTurns = s.agentMaxToolTurns()
		}
		if budget.stop == "" && input.Round >= maxTurns {
			budget.stop = agentBudgetTurns
		}
		nudged := budgetState.Nudged
		if budget.stop != "" {
			// The reply's last word: no more tools, what was done and what is left.
			response.Withhold = true
			if !nudged {
				response.Nudge, nudged = "\n\n"+agentToolBudgetNudge(budget.stop, maxTurns, info.MaxCalls), true
			}
		}
		s.saveWorkerChatBudget(ctx, run, budget, nudged)
		unlock()
	case "finish":
		ledger := s.agentRunLedger(ctx, run)
		stopped := s.workerChatBudget(ctx, run.ID).stop
		switch input.Phase {
		case "reply":
			if info.Resume == nil && stopped == "" && s.agentUngroundedText(info.Request, input.Text, ledger) {
				if !state.OutcomeNudged {
					response.Nudge, state.OutcomeNudged = agentUngroundedOutcomeNudge, true
					return response, nil
				}
				response.Reply = agentNothingExecutedText(info.Chinese)
			} else {
				response.Reply = ledger.report(input.Text, info.Chinese)
			}
		case "turnLimit":
			unlock := s.workerChatBudgetLock(run.ID)
			budget, budgetState := s.workerChatBudgetState(ctx, run.ID)
			if budget.stop == "" {
				budget.stop = agentBudgetTurns
			}
			s.saveWorkerChatBudget(ctx, run, budget, budgetState.Nudged)
			unlock()
			response.Reply = agentToolBudgetFallback(budget.stop, info.MaxTurns, info.MaxCalls, info.Chinese, -1, true)
		default:
			response.Reply = input.Text
		}
		state.Finished = true
	default:
		return response, store.ErrAgentRunInput
	}
	return response, nil
}
