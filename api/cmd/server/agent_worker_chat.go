package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	appconfig "flow/api/internal/config"
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
//     response and errors to POST …/runs/{id}/activities, numbered (seq); any API process stores each one and
//     applies the stored ones to the saved reply in their order (workerChatReply; part boundaries at once, text at
//     most once per agentLiveSaveInterval), so the agent.message_* realtime events, a reloaded page and
//     use-agent-reply-follow see the reply as they see an in-process one. A connector's question (MCP elicitation)
//     is attached by the API process that holds the connector call.
//   - waiting: ask_user ends the reply with its question, as in-process; the next message is a new run. A connector
//     set to always ask holds its calls: the reply keeps them pending with approval ids, the worker suspends the
//     run (RunState in agent_session_states, status awaiting), and once every held call is answered through POST
//     …/approvals/{id} Flow queues a resume run that continues the same reply.
//   - stop: POST …/stop cancels the reply's run (a queued or awaiting run at once, a held one through the cancel
//     long-poll); a connector question waiting in the run's call is cancelled too. The reply is saved as stopped.
//   - limits: held calls nobody answers within agentApprovalTimeout are rejected and the reply goes on, as the
//     in-process reply rejects them; a queued run no worker takes within FLOW_AGENT_WORKER_CLAIM_TIMEOUT is
//     cancelled and the reply ends with workerChatNoWorker (expireWorkerChatRun). The turn budget counts the turns of
//     every run of the reply (TurnsBefore).
//   - end: the response finishes the reply; the run's ledger (agent_run_outcomes.go) and tool budget apply through
//     the run guard (chat mode) and /mcp. A run whose worker died is requeued with the reply reset (nothing was
//     written) or interrupted (the reconciler, or the live-reply sweeper once the run ended without saving it).

// workerChatMessagePrefix starts the id of a reply that runs on the worker: agent_message_ plus its first run's id.
const workerChatMessagePrefix = "agent_message_run_"

// Keys of a chat run: its rendered prompt (agent_run_keys) and its tool budget (agent_run_states).
const (
	workerChatContextKey = "chat:context"
	workerChatBudgetKey  = "chat:budget"
)

// workerChatDeclined is the error of a held call the person rejected.
const workerChatDeclined = "Action declined by user"

// workerChatNoWorker ends a reply whose run no agent worker took within the claim timeout.
const workerChatNoWorker = "No agent worker is available; try again or contact your admin"

// workerChatExpiryCheck is how often a request following a reply checks its run against the claim timeout and the
// approval timeout (the live-reply sweeper checks every reply once a minute).
const workerChatExpiryCheck = 5 * time.Second

// agentWorkerClaimTimeout is how long a queued chat run may wait for a worker (FLOW_AGENT_WORKER_CLAIM_TIMEOUT).
func (s *server) agentWorkerClaimTimeout() time.Duration {
	if s.agent.WorkerClaimTimeout > 0 {
		return s.agent.WorkerClaimTimeout
	}
	return appconfig.DefaultAgentWorkerClaimTimeout
}

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
	// TurnsBefore is how many model turns earlier runs of the reply took (a resume continues their count, so the
	// turn budget is the reply's, as in-process, not each run's).
	TurnsBefore int `json:"turnsBefore,omitempty"`
	// Groups are the tool groups the in-process reply would start with (newAgentChatToolset: the request, the surface
	// the chat was opened on, the tools earlier replies used); the worker offers them besides the core tools.
	Groups []string `json:"groups,omitempty"`
	// LoopBuilder: the conversation builds loops (its text before a chip question stays in the reply, as
	// in-process); LoopQuestions: the run may ask chip questions (ask_question), as an in-process reply streamed to
	// the person may while the conversation holds a draft.
	LoopBuilder   bool `json:"loopBuilder,omitempty"`
	LoopQuestions bool `json:"loopQuestions,omitempty"`
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

// agentChatOnWorker reports whether a chat reply runs on the agent worker. Approvals saved by earlier versions stay
// in-process.
func (s *server) agentChatOnWorker(session domain.AgentSession) bool {
	if !s.agentRuntimeWorker() || !s.agentRuntimeEnabled() {
		return false
	}
	_, _, _, _, legacy := pendingAgentApproval(&session)
	return !legacy
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
		if message.Role == "user" {
			for _, image := range message.Images {
				item.Images = append(item.Images, agentRunPromptImage{Name: image.Name, MediaType: image.MediaType, Source: image.Source, MaxBytes: image.MaxBytes, Sniffed: image.Sniffed})
			}
		}
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
	// Files sent with the conversation's messages reach the model as the in-process reply gives them: a loop draft's
	// files with the first message, the newest images as image inputs, every other file as a note the file tools
	// (served through /mcp) read.
	s.addLoopAttachmentInputs(r.Context(), data, *session, prompt.messages)
	s.addAgentAttachmentInputs(r.Context(), prompt.messages)
	latest := agentLatestUserMessage(*session)
	runID := newWorkerChatRunID()
	started := time.Now().UTC()
	info := workerChatRunInfo{MessageID: "agent_message_" + runID, LinkBase: agentLinkBaseURL(r), MaxTurns: s.agentMaxToolTurns(), MaxCalls: s.agentMaxToolCalls(), Request: latest, Chinese: containsHan(latest), Started: started.UnixMilli(), Groups: newAgentChatToolset(*session).names(), LoopBuilder: len(session.LoopIDs) > 0, LoopQuestions: writer != nil && sessionHasDraftLoop(data, *session)}
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
	var expiryChecked time.Time
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
		case now := <-ticker.C:
			if _, streaming := s.workerChatStoredReply(r.Context(), workspaceKey(r), session.ID, data.Viewer.ID, info.MessageID); !streaming {
				settled = true
				break
			}
			// No worker took the run (or a resume) in time, or its held calls waited too long.
			if now.Sub(started) >= min(s.agentWorkerClaimTimeout(), agentApprovalTimeout) && now.Sub(expiryChecked) >= workerChatExpiryCheck {
				expiryChecked = now
				if latest, latestInfo, ok := s.workerChatRunOf(r.Context(), workspaceKey(r), session.ID, info.MessageID); ok {
					s.expireWorkerChatRun(r.Context(), latest, latestInfo, now)
				}
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
		s.copyAgentRunState(ctx, copyFrom, run, workerChatBudgetKey)
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

// workerChatReply is a worker chat reply as its run writes it. The conversation's saved copy is the reply's only
// copy, so any API process can take any post of any run:
//
//   - a post is stored first: a row of agent_run_activities under the run's attempt and the worker's sequence
//     number (seq), with what Flow resolved when it arrived (the connector of a call, the change it made, the
//     approval ids of held calls, how a response ends the reply). Storing is insert-if-absent, so a repeated post
//     (a retry, on this server or another) is a no-op;
//   - then the stored activities the saved reply does not hold yet are applied to it in seq order, each once and
//     only after the ones before it (a post that arrives early waits, up to workerChatGapWait, for the ones it
//     follows), and the result is saved in place of the saved reply — provided the saved reply is still the one
//     that was read (its UpdatedAt and WorkerCursor, the last activity it holds), else it is read again. So posts
//     handled by several servers at once, repeated or out of order give the same reply as one server in order.
//
// Every other change (an approval, a connector's question, a stop, the end of the run) is made on the saved reply
// the same way (update).
type workerChatReply struct {
	s     *server
	run   store.AgentRun
	info  workerChatRunInfo
	owner domain.User
}

// workerChatGapWait is how long applying waits for a missing activity (a post that has not arrived yet) before it
// goes on without it (the worker gave up on that post).
const workerChatGapWait = 10 * time.Second

// workerChatUpdateAttempts bounds the rounds of one update (another server saved the reply first).
const workerChatUpdateAttempts = 20

// errWorkerChatStale: the saved reply changed after it was read.
var errWorkerChatStale = errors.New("the worker chat reply kept changing while it was updated")

// workerChatReplyFor is the reply run writes. errAgentLiveSkip: the conversation no longer holds it streaming.
func (s *server) workerChatReplyFor(ctx context.Context, run store.AgentRun, info workerChatRunInfo) (*workerChatReply, error) {
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		return nil, err
	}
	owner, err := s.store.UserByID(ctx, principal.UserID)
	if err != nil {
		return nil, err
	}
	if _, streaming := s.workerChatStoredReply(ctx, run.WorkspaceKey, run.SessionID, owner.ID, info.MessageID); !streaming {
		return nil, errAgentLiveSkip
	}
	return &workerChatReply{s: s, run: run, info: info, owner: owner}, nil
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

func (c *workerChatReply) textID() string { return c.info.MessageID + "_text" }

func (c *workerChatReply) durationMS() int64 {
	if c.info.Started > 0 {
		return time.Since(time.UnixMilli(c.info.Started)).Milliseconds()
	}
	return 0
}

// cursorStart is where the run's activities start for a saved reply whose last applied activity is cursor; false
// when none of them apply any more (the reply moved on to a later run of it, or to a later attempt of this run).
func (c *workerChatReply) cursorStart(cursor *domain.AgentWorkerCursor) (int64, bool) {
	switch {
	case cursor == nil || cursor.Run == "":
		return 0, true
	case cursor.Run == c.run.ID && cursor.Attempt == c.run.Attempts:
		return cursor.Seq, true
	case cursor.Run == c.run.ID:
		return 0, cursor.Attempt < c.run.Attempts
	default:
		// Run ids grow with time (newWorkerChatRunID): a later run continues the reply (a resume).
		return 0, cursor.Run < c.run.ID
	}
}

// update applies the run's stored activities the saved reply does not hold yet (final: all of them, without
// waiting for missing ones — the run ended, so none can still arrive), then change (nil: none) unless the reply
// finished, and saves the result in place of the saved reply. change may run more than once (when another server
// saved the reply first), each time on a fresh draft. It reports whether the reply is finished; errAgentLiveSkip
// when the conversation no longer holds it streaming (finished, stopped or edited away).
func (c *workerChatReply) update(ctx context.Context, final bool, change func(*workerChatDraft)) (bool, error) {
	for range workerChatUpdateAttempts {
		stored, streaming := c.s.workerChatStoredReply(ctx, c.run.WorkspaceKey, c.run.SessionID, c.owner.ID, c.info.MessageID)
		if !streaming {
			return true, errAgentLiveSkip
		}
		draft, err := c.draft(ctx, stored)
		if err != nil {
			return false, err
		}
		waiting, err := draft.applyStored(ctx, final)
		if err != nil {
			return false, err
		}
		if change != nil && !draft.finished {
			change(draft)
		}
		if waiting {
			c.updateLater("gap", workerChatGapWait)
		}
		if !draft.changed {
			return draft.finished, nil
		}
		err = c.save(ctx, stored, draft)
		if errors.Is(err, errWorkerChatStale) {
			continue
		}
		if err != nil {
			return false, err
		}
		workerChatPublish(c.info.MessageID, draft.events...)
		return draft.finished, nil
	}
	return false, errWorkerChatStale
}

// draft is the saved reply with its offloaded text and results loaded, to change and save whole.
func (c *workerChatReply) draft(ctx context.Context, stored domain.AgentMessage) (*workerChatDraft, error) {
	one := domain.AgentSession{ID: c.run.SessionID, UserID: c.owner.ID, Messages: []domain.AgentMessage{stored}}
	if loaded, err := c.s.store.LoadAgentSessionBodies(ctx, c.run.WorkspaceKey, one); err == nil && len(loaded.Messages) == 1 {
		one = loaded
	}
	message, err := cloneAgentMessage(one.Messages[0])
	if err != nil {
		return nil, err
	}
	return &workerChatDraft{c: c, message: message}, nil
}

// save puts the draft in place of the saved reply if the saved reply is still stored (errWorkerChatStale
// otherwise); a finished reply is logged with its response activity.
func (c *workerChatReply) save(ctx context.Context, stored domain.AgentMessage, draft *workerChatDraft) error {
	message := draft.message
	event := "agent.message_updated"
	if draft.finished {
		event = "agent.message_completed"
	} else {
		now := time.Now().UTC()
		if stored.UpdatedAt != nil && !now.After(*stored.UpdatedAt) {
			now = stored.UpdatedAt.Add(time.Nanosecond)
		}
		message.Status, message.UpdatedAt, message.Content = agentMessageStreaming, &now, agentLiveText(message.Parts)
	}
	err := c.s.store.MutateWorkspace(c.ctx(ctx), c.run.WorkspaceKey, event, c.run.SessionID, nil, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, c.run.SessionID)
		if err != nil {
			return errAgentLiveSkip
		}
		index := slices.IndexFunc(session.Messages, func(item domain.AgentMessage) bool { return item.ID == message.ID })
		if index < 0 || session.Messages[index].Status != agentMessageStreaming {
			return errAgentLiveSkip
		}
		if !sameWorkerChatReply(session.Messages[index], stored) {
			return errWorkerChatStale
		}
		session.Messages[index] = message
		if draft.activity != nil {
			data.AgentActivities = append(data.AgentActivities, *draft.activity)
			session.UpdatedAt = message.CreatedAt
			if message.UpdatedAt == nil {
				session.UpdatedAt = time.Now().UTC()
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errAgentLiveSkip) && !errors.Is(err, errWorkerChatStale) {
		log.Printf("save worker chat reply %s: %v", c.info.MessageID, err)
	}
	return err
}

// sameWorkerChatReply reports whether the saved reply is still the one read: every save of a streaming reply moves
// its UpdatedAt on, and applying activities moves its cursor.
func sameWorkerChatReply(current, read domain.AgentMessage) bool {
	if current.UpdatedAt == nil || read.UpdatedAt == nil || !current.UpdatedAt.Equal(*read.UpdatedAt) || len(current.Parts) != len(read.Parts) {
		return false
	}
	if current.WorkerCursor == nil || read.WorkerCursor == nil {
		return current.WorkerCursor == nil && read.WorkerCursor == nil
	}
	return *current.WorkerCursor == *read.WorkerCursor
}

// workerChatLater holds the updates this process scheduled (message id and reason): streamed text is saved at most
// once per agentLiveSaveInterval, and an update waiting for a missing activity looks again after workerChatGapWait.
// Only a schedule: any later post or change, or the run's end, applies the same activities.
var workerChatLater sync.Map

func (c *workerChatReply) updateLater(reason string, delay time.Duration) {
	key := c.info.MessageID + "\x00" + reason
	if _, scheduled := workerChatLater.LoadOrStore(key, true); scheduled {
		return
	}
	time.AfterFunc(delay, func() {
		workerChatLater.Delete(key)
		if _, err := c.update(context.Background(), false, nil); err != nil && !errors.Is(err, errAgentLiveSkip) {
			log.Printf("update worker chat reply %s: %v", c.info.MessageID, err)
		}
	})
}

// workerChatDraft is the reply while one update changes it; events are sent to this process's followers once it
// is saved.
type workerChatDraft struct {
	c        *workerChatReply
	message  domain.AgentMessage
	changed  bool
	finished bool
	events   []agentStreamEvent
	activity *domain.AgentActivity
}

// applyStored applies the run's stored activities after the draft's cursor in seq order; waiting is set when it
// stopped at a missing activity that may still arrive.
func (d *workerChatDraft) applyStored(ctx context.Context, final bool) (waiting bool, err error) {
	after, ok := d.c.cursorStart(d.message.WorkerCursor)
	if !ok {
		return false, nil
	}
	run, attempt := d.c.run.ID, d.c.run.Attempts
	for {
		rows, err := d.c.s.store.AgentRunActivities(ctx, run, attempt, after)
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			if row.Seq != after+1 && !final && time.Since(time.UnixMilli(row.CreatedAt)) < workerChatGapWait {
				return true, nil
			}
			var activity workerChatStoredActivity
			if err := json.Unmarshal(row.Data, &activity); err != nil {
				log.Printf("worker chat run %s: activity %d unreadable: %v", run, row.Seq, err)
			}
			// An activity that shows nothing (a thought) moves the cursor with the next change saved; applying it
			// again changes nothing.
			d.message.WorkerCursor = &domain.AgentWorkerCursor{Run: run, Attempt: attempt, Seq: row.Seq}
			d.apply(activity)
			after = row.Seq
			if d.finished {
				return false, nil
			}
		}
		if len(rows) < store.AgentRunActivitiesMax {
			return false, nil
		}
	}
}

func (d *workerChatDraft) partIndex(id string) int {
	return slices.IndexFunc(d.message.Parts, func(part domain.AgentMessagePart) bool { return part.ID == id })
}

func (d *workerChatDraft) callIndex(callID string) int {
	return slices.IndexFunc(d.message.Parts, func(part domain.AgentMessagePart) bool {
		return part.ToolCall != nil && part.ToolCall.ID == callID && (part.Type == "toolCall" || part.Type == "question")
	})
}

func (d *workerChatDraft) text() string {
	texts := d.keptTexts(d.message)
	if index := d.partIndex(d.c.textID()); index >= 0 {
		texts = append(texts, d.message.Parts[index].Text)
	}
	return joinAgentTexts(texts)
}

// workerChatKeptPart marks the text a loop builder reply wrote before a chip question: it stays in the reply (as
// in-process, where it is not folded into the work log) and the reply's later text follows it.
const workerChatKeptPart = "_text_kept_"

func (d *workerChatDraft) keptTexts(message domain.AgentMessage) []string {
	var texts []string
	for _, part := range message.Parts {
		if strings.HasPrefix(part.ID, d.c.info.MessageID+workerChatKeptPart) {
			texts = append(texts, part.Text)
		}
	}
	return texts
}

// keepText keeps the text streamed so far in the reply (a loop builder's words before its question).
func (d *workerChatDraft) keepText() {
	index := d.partIndex(d.c.textID())
	if index < 0 {
		return
	}
	if strings.TrimSpace(d.message.Parts[index].Text) == "" {
		d.message.Parts = slices.Delete(d.message.Parts, index, index+1)
	} else {
		d.message.Parts[index].ID = fmt.Sprintf("%s%s%d", d.c.info.MessageID, workerChatKeptPart, len(d.keptTexts(d.message))+1)
		d.message.Parts[index].Status = "completed"
	}
	d.changed = true
}

// mergeKept makes the kept texts and last (the final turn's text) the reply's one text part, where the first kept
// text was (the in-process reply keeps one text part); it returns the reply's text.
func (d *workerChatDraft) mergeKept(message *domain.AgentMessage, last string) string {
	texts := d.keptTexts(*message)
	if len(texts) == 0 {
		return last
	}
	combined := joinAgentTexts(append(texts, last))
	parts := make([]domain.AgentMessagePart, 0, len(message.Parts))
	first := -1
	for _, part := range message.Parts {
		switch {
		case part.ID == d.c.textID():
			continue
		case strings.HasPrefix(part.ID, d.c.info.MessageID+workerChatKeptPart):
			if first >= 0 {
				continue
			}
			first = len(parts)
			part.ID = d.c.textID()
		}
		parts = append(parts, part)
	}
	parts[first].Text = combined
	message.Parts = parts
	return combined
}

// joinAgentTexts joins a reply's paragraphs (blank ones dropped), as the in-process loop builder separates its turns.
func joinAgentTexts(texts []string) string {
	kept := []string{}
	for _, text := range texts {
		if text = strings.TrimSpace(text); text != "" {
			kept = append(kept, text)
		}
	}
	return strings.Join(kept, "\n\n")
}

func (d *workerChatDraft) publish(events ...agentStreamEvent) { d.events = append(d.events, events...) }

// foldNarration turns the text streamed before a tool call into a step of the work, as the in-process reply keeps
// mid-task narration inside the work group: only the last turn's text is the answer.
func (d *workerChatDraft) foldNarration() {
	index := d.partIndex(d.c.textID())
	if index < 0 {
		return
	}
	narration := strings.TrimSpace(d.message.Parts[index].Text)
	if narration == "" {
		d.message.Parts = slices.Delete(d.message.Parts, index, index+1)
	} else {
		count := 0
		for _, part := range d.message.Parts {
			if part.Type == "reasoning" {
				count++
			}
		}
		d.message.Parts[index] = domain.AgentMessagePart{ID: fmt.Sprintf("%s_narration_%d", d.c.info.MessageID, count+1), Type: "reasoning", Text: narration, Status: "completed"}
	}
	d.publish(agentStreamEvent{Type: "text.replaced", MessageID: d.c.info.MessageID, Delta: ""})
}

// finish makes message the reply's final form.
func (d *workerChatDraft) finish(message domain.AgentMessage) {
	message.Status, message.UpdatedAt, message.WorkerCursor = "", nil, nil
	d.activity = &domain.AgentActivity{ID: idgen.New("agent_activity_"), SessionID: d.c.run.SessionID, Type: "response", Status: "completed", Body: message.Content, Metadata: map[string]any{"durationMs": message.DurationMS, "partCount": len(message.Parts), "runId": d.c.run.ID}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	d.message, d.finished, d.changed = message, true, true
	d.publish(agentStreamEvent{Type: workerChatSettledEvent, MessageID: d.c.info.MessageID})
}

// complete finishes the reply with its answer, as persistAgentCompletion saves an in-process one.
func (d *workerChatDraft) complete(answer string, durationMS int64, extra ...domain.AgentMessagePart) {
	message, err := cloneAgentMessage(d.message)
	if err != nil {
		return
	}
	answer = rebaseAgentWorkspaceLinks(d.mergeKept(&message, strings.TrimSpace(answer)), d.c.run.WorkspaceKey, d.c.info.LinkBase)
	index := slices.IndexFunc(message.Parts, func(part domain.AgentMessagePart) bool { return part.ID == d.c.textID() })
	switch {
	case index >= 0 && answer == "":
		message.Parts = slices.Delete(message.Parts, index, index+1)
	case index >= 0:
		message.Parts[index].Text, message.Parts[index].Status = answer, "completed"
	case answer != "":
		message.Parts = append(message.Parts, domain.AgentMessagePart{ID: d.c.textID(), Type: "text", Text: answer, Status: "completed"})
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
	message.Content, message.DurationMS = answer, durationMS
	d.publish(agentStreamEvent{Type: "text.replaced", MessageID: d.c.info.MessageID, Delta: answer})
	d.finish(message)
}

// fail finishes the reply as failed (or stopped), as persistAgentFailure saves an in-process one.
func (d *workerChatDraft) fail(reason string, stopped bool, durationMS int64) {
	message, err := cloneAgentMessage(d.message)
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
	message.Content, message.DurationMS = strings.TrimSpace(d.text()), durationMS
	d.finish(message)
}

// interrupt finishes the reply as interrupted (its run's worker went away), as the live-reply sweeper does.
func (d *workerChatDraft) interrupt(durationMS int64) {
	message, err := cloneAgentMessage(d.message)
	if err != nil {
		return
	}
	interruptAgentLiveMessage(&message)
	message.DurationMS = durationMS
	d.finish(message)
}

// holdsApprovals reports whether held calls wait for the person's decision.
func (d *workerChatDraft) holdsApprovals() bool {
	return slices.ContainsFunc(d.message.Parts, func(part domain.AgentMessagePart) bool {
		return part.ToolCall != nil && part.ToolCall.ApprovalID != "" && part.ToolCall.Status == "pending"
	})
}

// finishFailure saves the reply as failed (or stopped).
func (c *workerChatReply) finishFailure(ctx context.Context, reason string, stopped bool) {
	if _, err := c.update(ctx, false, func(d *workerChatDraft) { d.fail(reason, stopped, c.durationMS()) }); err != nil && !errors.Is(err, errAgentLiveSkip) {
		log.Printf("finish worker chat reply %s: %v", c.info.MessageID, err)
	}
}

// interrupt saves the reply as interrupted, with every activity its run stored.
func (c *workerChatReply) interrupt(ctx context.Context) {
	if _, err := c.update(ctx, true, func(d *workerChatDraft) { d.interrupt(c.durationMS()) }); err != nil && !errors.Is(err, errAgentLiveSkip) {
		log.Printf("interrupt worker chat reply %s: %v", c.info.MessageID, err)
	}
}

// reset empties the reply for a new attempt of its run (the previous one wrote nothing): the previous attempt's
// activities no longer apply.
func (c *workerChatReply) reset(ctx context.Context) {
	_, err := c.update(ctx, false, func(d *workerChatDraft) {
		d.message.Parts = []domain.AgentMessagePart{}
		d.message.WorkerCursor = &domain.AgentWorkerCursor{Run: c.run.ID, Attempt: c.run.Attempts, Seq: math.MaxInt64}
		d.changed = true
		d.publish(agentStreamEvent{Type: "text.replaced", MessageID: c.info.MessageID, Delta: ""})
	})
	if err != nil && !errors.Is(err, errAgentLiveSkip) {
		log.Printf("reset worker chat reply %s: %v", c.info.MessageID, err)
	}
}

// ---------------------------------------------------------------- activities

// workerChatActivity is an activity a chat run posts (the worker's FlowActivity, design doc §5).
type workerChatActivity struct {
	ActivityID string `json:"activityId"`
	// Seq numbers the run's posts (1, 2, …, per run attempt, in the order the worker makes them): a post repeated
	// with its seq is stored once, and posts apply in seq order. A post without one is numbered on arrival.
	Seq int64 `json:"seq,omitempty"`
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

// workerChatStoredActivity is an activity as stored for its run (agent_run_activities): the worker's activity with
// what Flow resolved when it arrived, so applying it gives the same reply on every server and every time.
type workerChatStoredActivity struct {
	workerChatActivity
	// Connector and RemoteTool name a connector call's connector and tool, as the in-process reply shows them.
	Connector  string `json:"connector,omitempty"`
	RemoteTool string `json:"remoteTool,omitempty"`
	// ChangeData is the change a call's result made (its card and Undo).
	ChangeData *domain.AgentChange `json:"changeData,omitempty"`
	// Title is the work-log label of a finished call, as the in-process reply names it ("Read file notes.pdf").
	Title string `json:"title,omitempty"`
	// Approvals are the held calls' approvals (by call id).
	Approvals map[string]workerChatApproval `json:"approvals,omitempty"`
	// End is how a response or an error ends the reply: complete (with Answer, the checked answer, and BudgetStop
	// when the tool budget was spent), fail or stop.
	End        string `json:"end,omitempty"`
	Answer     string `json:"answer,omitempty"`
	BudgetStop string `json:"budgetStop,omitempty"`
	DurationMS int64  `json:"durationMs,omitempty"`
}

// workerChatApproval is a held call's approval id, and its connector and tool.
type workerChatApproval struct {
	ID         string `json:"id"`
	Connector  string `json:"connector,omitempty"`
	RemoteTool string `json:"remoteTool,omitempty"`
}

var workerChatActivityTypes = []string{"thought", "text.delta", "question.delta", "action", "action.result", "elicitation", "response", "error"}

// workerChatActivitySeqMax bounds an activity's seq.
const workerChatActivitySeqMax = 1 << 40

// workerChatDeferred are the activities saved with the next change (streamed text at most once per
// agentLiveSaveInterval): their stream events go to this process's followers at once.
var workerChatDeferred = []string{"thought", "text.delta", "question.delta"}

// postWorkerChatActivity stores one activity of a chat run and brings its reply up to date.
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
	if input.Seq < 0 || input.Seq > workerChatActivitySeqMax {
		writeError(w, http.StatusBadRequest, "seq must be a positive number")
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
	ctx := r.Context()
	key := ""
	if input.ActivityID != "" && input.Seq == 0 {
		// A post without seq is repeated by its activity id.
		key = "activity:" + input.ActivityID
		if cached, found, err := s.store.AgentRunKey(ctx, run.ID, key); err == nil && found {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(cached)
			return
		}
	}
	reply, err := s.workerChatReplyFor(ctx, run, info)
	if errors.Is(err, errAgentLiveSkip) {
		// The reply finished (stopped, edited away): nothing more is saved.
		writeJSON(w, http.StatusOK, map[string]any{"messageId": info.MessageID, "settled": true})
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, "the run's reply cannot be written")
		return
	}
	stored, err := reply.resolve(ctx, input)
	var data []byte
	if err == nil {
		data, err = json.Marshal(stored)
	}
	fresh := true
	if err == nil {
		if input.Seq > 0 {
			fresh, err = s.store.SaveAgentRunActivity(ctx, run, run.Attempts, input.Seq, data)
		} else {
			_, err = s.store.AppendAgentRunActivity(ctx, run, run.Attempts, data)
		}
	}
	if err != nil {
		log.Printf("worker chat run %s: activity not stored: %v", run.ID, err)
		writeError(w, http.StatusInternalServerError, "the activity could not be stored")
		return
	}
	settled := false
	if slices.Contains(workerChatDeferred, input.Type) {
		if fresh {
			reply.publishDelta(input)
		}
		reply.updateLater("save", agentLiveSaveInterval)
	} else {
		settled, err = reply.update(ctx, false, nil)
		if errors.Is(err, errAgentLiveSkip) {
			settled, err = true, nil
		}
		if err != nil {
			log.Printf("worker chat run %s: reply not updated: %v", run.ID, err)
		}
	}
	body, _ := json.Marshal(map[string]any{"messageId": info.MessageID, "settled": settled})
	if key != "" {
		_ = s.store.SaveAgentRunKey(ctx, run, key, body)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

// publishDelta sends a streamed delta to this process's followers as it arrives (the reply saves it with its next
// change).
func (c *workerChatReply) publishDelta(input workerChatActivity) {
	messageID := c.info.MessageID
	switch input.Type {
	case "text.delta":
		if input.Body != "" {
			part := domain.AgentMessagePart{ID: c.textID(), Type: "text", Status: "running"}
			workerChatPublish(messageID, agentStreamEvent{Type: "text.delta", MessageID: messageID, Delta: input.Body, Part: &part})
		}
	case "question.delta":
		part := workerChatQuestionDelta(messageID, input)
		workerChatPublish(messageID, agentStreamEvent{Type: "tool.delta", MessageID: messageID, Part: &part})
	}
}

// workerChatQuestionDelta is ask_user's question while the model writes it: a running question part the finished
// question replaces.
func workerChatQuestionDelta(messageID string, input workerChatActivity) domain.AgentMessagePart {
	return domain.AgentMessagePart{ID: fmt.Sprintf("%s_question_%s", messageID, input.CallID), Type: "question", Status: "running", Text: input.Body, Question: &domain.AgentQuestion{Options: []domain.AgentQuestionOption{}}}
}

// resolve is the activity as stored: with what applying it needs from Flow at the time it arrived.
func (c *workerChatReply) resolve(ctx context.Context, input workerChatActivity) (workerChatStoredActivity, error) {
	stored := workerChatStoredActivity{workerChatActivity: input}
	switch input.Type {
	case "action", "action.result":
		stored.Connector, stored.RemoteTool = c.connectorOf(ctx, input.Content.Action)
		if input.Type == "action.result" {
			stored.ChangeData = c.s.workerChatChange(ctx, c.run, input.Change)
			if !input.IsError {
				stored.Title = c.toolTitle(ctx, input.Content.Action, input.Content.Parameter, input.Content.Result)
			}
		}
	case "elicitation":
		stored.DurationMS = c.durationMS()
		if input.Elicitation.Kind == "approval" {
			stored.Approvals = map[string]workerChatApproval{}
			for _, held := range input.Elicitation.Calls {
				approvalID, err := randomSecret("agent_approval_")
				if err != nil {
					return stored, err
				}
				approval := workerChatApproval{ID: approvalID}
				approval.Connector, approval.RemoteTool = c.connectorOf(ctx, held.Name)
				stored.Approvals[held.CallID] = approval
			}
		}
	case "response":
		stored.DurationMS = c.durationMS()
		if c.s.workerChatCancelRequested(ctx, c.run.ID) {
			stored.End = "stop"
			break
		}
		stored.End, stored.Answer = "complete", c.s.workerChatCheckedAnswer(ctx, c.run, c.info, input.Body)
		stored.BudgetStop = c.s.workerChatBudget(ctx, c.run).stop
	case "error":
		stored.DurationMS, stored.End = c.durationMS(), "fail"
		if c.s.workerChatCancelRequested(ctx, c.run.ID) {
			stored.End = "stop"
		}
	}
	return stored, nil
}

// connectorOf names a connector call's connector and tool, as the in-process reply shows them ("" for Flow's
// tools).
func (c *workerChatReply) connectorOf(ctx context.Context, name string) (connector, remoteTool string) {
	name = agentToolBaseName(name)
	if !strings.HasPrefix(name, "external_") {
		return "", ""
	}
	if tool, ok := c.s.agentRunConnectorTool(ctx, c.run, name); ok {
		return tool.Policy.label(), tool.RemoteName
	}
	return "", ""
}

// toolTitle is the label the in-process reply gives a finished call (a file tool's), "" for the others. parameter is
// the call's arguments, which the worker repeats with the result.
func (c *workerChatReply) toolTitle(ctx context.Context, name string, parameter json.RawMessage, result string) string {
	name = agentToolBaseName(name)
	call := domain.AgentToolCall{Name: name, Arguments: workerChatArguments(parameter), Result: workerChatResultJSON(result)}
	switch {
	case isAgentFileTool(name):
		return agentFileToolTitle(c.s.agentRunSessionToolContext(ctx, c.run), call)
	case name == "save_loop":
		// As the in-process reply labels a save_loop row.
		title := saveLoopToolTitle(call.Result)
		var saveArgs struct {
			Publish bool `json:"publish"`
		}
		if _ = json.Unmarshal(call.Arguments, &saveArgs); saveArgs.Publish {
			title = "Created automation"
		} else if title == "" {
			title = "Updated workflow definition draft"
		}
		return title
	}
	return ""
}

// apply changes the draft by one stored activity.
func (d *workerChatDraft) apply(input workerChatStoredActivity) {
	messageID := d.c.info.MessageID
	switch input.Type {
	case "thought":
		// The ack and reasoning: the person sees "Thinking…" and the running step instead (not saved, as in-process).
	case "text.delta":
		if input.Body == "" {
			break
		}
		index := d.partIndex(d.c.textID())
		if index < 0 {
			index = len(d.message.Parts)
			d.message.Parts = append(d.message.Parts, domain.AgentMessagePart{ID: d.c.textID(), Type: "text", Status: "running"})
		}
		d.message.Parts[index].Text += input.Body
		d.changed = true
	case "question.delta":
		part := workerChatQuestionDelta(messageID, input.workerChatActivity)
		if index := d.partIndex(part.ID); index < 0 {
			d.message.Parts = append(d.message.Parts, part)
		} else {
			d.message.Parts[index] = part
		}
		d.changed = true
	case "action":
		if input.Content == nil {
			break
		}
		name := agentToolBaseName(input.Content.Action)
		if name == loopQuestionTool {
			// The chip question shows as its elicitation; the loop builder's words before it stay in the reply.
			if d.c.info.LoopBuilder {
				d.keepText()
			}
			break
		}
		if name == agentLoadToolsTool || name == askUserTool {
			// Bookkeeping, or the question (question.delta and the elicitation show it).
			break
		}
		d.foldNarration()
		call := domain.AgentToolCall{ID: input.CallID, Name: name, Arguments: workerChatArguments(input.Content.Parameter), Status: "running", Connector: input.Connector, RemoteTool: input.RemoteTool}
		index := d.callIndex(input.CallID)
		if index < 0 {
			index = len(d.message.Parts)
			d.message.Parts = append(d.message.Parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, index), Type: "toolCall", Status: "running", ToolCall: &call})
		} else {
			d.message.Parts[index].Status, d.message.Parts[index].ToolCall = "running", &call
		}
		part := d.message.Parts[index]
		d.publish(agentStreamEvent{Type: "tool.started", MessageID: messageID, Part: &part})
		d.changed = true
	case "action.result":
		if input.Content == nil {
			break
		}
		name := agentToolBaseName(input.Content.Action)
		if name == agentLoadToolsTool || name == askUserTool || name == loopQuestionTool {
			break
		}
		index := d.callIndex(input.CallID)
		if index < 0 {
			// A held call the person approved runs on resume without a new call activity.
			d.foldNarration()
			call := domain.AgentToolCall{ID: input.CallID, Name: name, Status: "running", Connector: input.Connector, RemoteTool: input.RemoteTool}
			index = len(d.message.Parts)
			d.message.Parts = append(d.message.Parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, index), Type: "toolCall", Status: "running", ToolCall: &call})
		}
		current := d.message.Parts[index].ToolCall
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
		if input.ChangeData != nil {
			change := *input.ChangeData
			call.Change = &change
		}
		if input.Title != "" && !input.IsError {
			call.Title = input.Title
		}
		d.message.Parts[index].Status, d.message.Parts[index].ToolCall = call.Status, &call
		part := d.message.Parts[index]
		d.publish(agentStreamEvent{Type: "tool.completed", MessageID: messageID, Part: &part})
		d.changed = true
	case "elicitation":
		if input.Elicitation == nil {
			break
		}
		if input.Elicitation.Kind == "select" {
			d.ask(*input.Elicitation, input.DurationMS)
			break
		}
		d.hold(*input.Elicitation, input.Approvals)
	case "response":
		if input.End == "stop" {
			d.fail("", true, input.DurationMS)
			break
		}
		answer := input.Answer
		extra := []domain.AgentMessagePart{}
		if input.BudgetStop != "" {
			info := d.c.info
			if strings.TrimSpace(answer) == "" {
				answer = agentToolBudgetFallback(input.BudgetStop, info.MaxTurns, info.MaxCalls, info.Chinese, d.changes(), true)
			}
			extra = append(extra, domain.AgentMessagePart{ID: messageID + "_budget", Type: "event", Text: agentToolBudgetNotice(input.BudgetStop, info.MaxTurns, info.MaxCalls, info.Chinese), Status: "completed"})
		}
		if strings.TrimSpace(answer) == "" {
			d.fail("Flow Agent provider returned an empty response", false, input.DurationMS)
			break
		}
		d.complete(answer, input.DurationMS, extra...)
	case "error":
		d.fail(input.Body, input.End == "stop", input.DurationMS)
	}
}

func (d *workerChatDraft) changes() int {
	changes := 0
	for _, part := range d.message.Parts {
		if part.ToolCall != nil && part.ToolCall.Change != nil {
			changes++
		}
	}
	return changes
}

// ask ends the reply with ask_user's question, as the in-process reply ends on one: the person answers with their
// next message.
func (d *workerChatDraft) ask(elicitation workerChatElicitation, durationMS int64) {
	messageID := d.c.info.MessageID
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
	if index := d.partIndex(id); index >= 0 {
		d.message.Parts[index] = part
	} else {
		d.message.Parts = append(d.message.Parts, part)
	}
	d.publish(agentStreamEvent{Type: "tool.completed", MessageID: messageID, Part: &part})
	message, err := cloneAgentMessage(d.message)
	if err != nil {
		return
	}
	for index := range message.Parts {
		if message.Parts[index].Status == "running" {
			message.Parts[index].Status = "completed"
		}
	}
	current := ""
	if index := slices.IndexFunc(message.Parts, func(part domain.AgentMessagePart) bool { return part.ID == d.c.textID() }); index >= 0 {
		current = message.Parts[index].Text
	}
	text := rebaseAgentWorkspaceLinks(strings.TrimSpace(d.mergeKept(&message, current)), d.c.run.WorkspaceKey, d.c.info.LinkBase)
	if index := slices.IndexFunc(message.Parts, func(part domain.AgentMessagePart) bool { return part.ID == d.c.textID() }); index >= 0 {
		message.Parts[index].Text = text
	}
	// The reply's text (and what the model sees of it later) ends with the question; the part draws its options.
	message.Content, message.DurationMS = strings.TrimSpace(text+"\n\n"+question), durationMS
	d.finish(message)
}

// hold shows the calls a connector set to always ask holds, each waiting for the person's decision under the
// approval id stored with the activity.
func (d *workerChatDraft) hold(elicitation workerChatElicitation, approvals map[string]workerChatApproval) {
	messageID := d.c.info.MessageID
	d.foldNarration()
	for _, held := range elicitation.Calls {
		approval := approvals[held.CallID]
		if approval.ID == "" {
			continue
		}
		approvalID := approval.ID
		call := domain.AgentToolCall{ID: held.CallID, Name: agentToolBaseName(held.Name), Arguments: workerChatArguments(json.RawMessage(held.Arguments)), Status: "pending", ApprovalID: approvalID, Connector: approval.Connector, RemoteTool: approval.RemoteTool}
		index := d.callIndex(held.CallID)
		if index < 0 {
			index = len(d.message.Parts)
			d.message.Parts = append(d.message.Parts, domain.AgentMessagePart{ID: fmt.Sprintf("%s_tool_%d", messageID, index), Type: "toolCall"})
		}
		d.message.Parts[index].Status, d.message.Parts[index].ToolCall = "pending", &call
		part := d.message.Parts[index]
		d.publish(agentStreamEvent{Type: "tool.approval_required", MessageID: messageID, ApprovalID: approvalID, Part: &part})
	}
	d.changed = true
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

// workerChatResultJSON is a tool result as a reply part stores it: the tool's JSON, or the text as a JSON string.
func workerChatResultJSON(result string) json.RawMessage {
	if trimmed := strings.TrimSpace(result); trimmed != "" && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	raw, _ := json.Marshal(result)
	return raw
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
	if state, found := s.workerRunGuardStateOf(ctx, run.ID); found && state.Finished {
		return body
	}
	if body == agentNothingExecutedText(true) || body == agentNothingExecutedText(false) {
		return body
	}
	ledger := s.agentRunLedger(ctx, run)
	if info.Resume == nil && s.workerChatBudget(ctx, run).stop == "" && s.agentUngroundedText(info.Request, body, ledger) {
		return agentNothingExecutedText(info.Chinese)
	}
	return ledger.report(body, info.Chinese)
}

// ---------------------------------------------------------------- run end, stop, approvals

// settleWorkerChatRun saves a chat run's reply when the worker finishes the run without having finished the reply:
// with every activity the run stored, then as its status says.
func (s *server) settleWorkerChatRun(ctx context.Context, run store.AgentRun, info workerChatRunInfo, status, failure string) {
	reply, err := s.workerChatReplyFor(ctx, run, info)
	if err != nil {
		return
	}
	_, err = reply.update(ctx, true, func(d *workerChatDraft) {
		duration := reply.durationMS()
		switch status {
		case store.AgentRunAwaiting:
			// Held calls wait for the person (the reply stays streaming); anything else is an unfinished reply.
			if !d.holdsApprovals() {
				d.fail("The reply stopped waiting for an answer it never asked for.", false, duration)
			}
		case store.AgentRunDone:
			if text := d.text(); strings.TrimSpace(text) != "" {
				d.complete(s.workerChatCheckedAnswer(ctx, run, info, text), duration)
				return
			}
			d.fail("Flow Agent provider returned an empty response", false, duration)
		case store.AgentRunCanceled:
			d.fail("", true, duration)
		case store.AgentRunInterrupted:
			d.interrupt(duration)
		default:
			d.fail(firstNonEmpty(strings.TrimSpace(failure), "The agent run failed."), false, duration)
		}
	})
	if err != nil && !errors.Is(err, errAgentLiveSkip) {
		log.Printf("settle worker chat reply %s: %v", info.MessageID, err)
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
	if _, budget := s.workerChatBudgetState(ctx, current); budget.Turns > next.TurnsBefore {
		next.TurnsBefore = budget.Turns
	}
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
		if !reply.decide(ctx, approvalID, decision) {
			return true, errNotFound
		}
		s.resumeWorkerChat(ctx, run, info)
		return true, nil
	}
	return false, nil
}

// decide applies a decision to the held call; false when it no longer waits.
func (c *workerChatReply) decide(ctx context.Context, approvalID, decision string) bool {
	answered := false
	_, err := c.update(ctx, false, func(d *workerChatDraft) { answered = d.decide(approvalID, decision) })
	return err == nil && answered
}

func (d *workerChatDraft) decide(approvalID, decision string) bool {
	index := slices.IndexFunc(d.message.Parts, func(part domain.AgentMessagePart) bool {
		return part.ToolCall != nil && part.ToolCall.ApprovalID == approvalID && part.ToolCall.Status == "pending"
	})
	if index < 0 {
		return false
	}
	call := *d.message.Parts[index].ToolCall
	if decision == "approved" {
		call.Status = "running"
		d.message.Parts[index].Status = "running"
	} else {
		call.Status, call.Error = "error", workerChatDeclined
		call.Result = json.RawMessage(`{"error":"Action declined by user"}`)
		d.message.Parts[index].Status = "error"
	}
	d.message.Parts[index].ToolCall = &call
	part := d.message.Parts[index]
	d.publish(agentStreamEvent{Type: "tool.approval_resolved", MessageID: d.c.info.MessageID, ApprovalID: approvalID, Decision: decision, Part: &part})
	d.changed = true
	return true
}

// expireApprovals rejects the held calls that still wait for a decision, as the in-process reply rejects a call
// nobody answered; it returns how many.
func (c *workerChatReply) expireApprovals(ctx context.Context) int {
	rejected := 0
	_, err := c.update(ctx, false, func(d *workerChatDraft) {
		rejected = 0
		var waiting []string
		for _, part := range d.message.Parts {
			if part.ToolCall != nil && part.ToolCall.ApprovalID != "" && part.ToolCall.Status == "pending" {
				waiting = append(waiting, part.ToolCall.ApprovalID)
			}
		}
		for _, approvalID := range waiting {
			if d.decide(approvalID, "rejected") {
				rejected++
			}
		}
	})
	if err != nil {
		return 0
	}
	return rejected
}

// expireWorkerChatRun ends the wait of a worker reply's latest run when it is over its limit: a queued run no worker
// took within the claim timeout is cancelled and the reply ends with workerChatNoWorker; the held calls of a run
// awaiting decisions for agentApprovalTimeout are rejected and a resume run continues the reply. It reports whether
// it changed the reply.
func (s *server) expireWorkerChatRun(ctx context.Context, run store.AgentRun, info workerChatRunInfo, now time.Time) bool {
	switch run.Status {
	case store.AgentRunQueued:
		canceled, ok, err := s.store.CancelUnclaimedAgentRun(ctx, run.ID, workerChatNoWorker, now.Add(-s.agentWorkerClaimTimeout()))
		if err != nil || !ok {
			return false
		}
		if info.Resume != nil {
			// The suspended run nobody resumed.
			_ = s.store.DeleteAgentSessionState(ctx, run.WorkspaceKey, run.SessionID)
		}
		reply, err := s.workerChatReplyFor(ctx, canceled, info)
		if err != nil {
			return false
		}
		reply.finishFailure(ctx, workerChatNoWorker, false)
		return true
	case store.AgentRunAwaiting:
		// An awaiting run's FinishedAt is when it suspended on its held calls.
		if run.FinishedAt == 0 || now.Sub(time.UnixMilli(run.FinishedAt)) < agentApprovalTimeout {
			return false
		}
		reply, err := s.workerChatReplyFor(ctx, run, info)
		if err != nil {
			return false
		}
		if reply.expireApprovals(ctx) == 0 {
			return false
		}
		s.resumeWorkerChat(ctx, run, info)
		return true
	}
	return false
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
	if part.Elicitation != nil {
		// The reply keeps its own copy of the question: the caller's may still change.
		question := *part.Elicitation
		part.Elicitation = &question
	}
	_, err = reply.update(ctx, false, func(d *workerChatDraft) {
		if index := d.partIndex(part.ID); index >= 0 {
			d.message.Parts[index] = part
		} else {
			d.message.Parts = append(d.message.Parts, part)
		}
		shown := part
		d.publish(agentStreamEvent{Type: kind, MessageID: info.MessageID, Part: &shown})
		d.changed = true
	})
	if err != nil && !errors.Is(err, errAgentLiveSkip) {
		log.Printf("show connector question on worker chat reply %s: %v", info.MessageID, err)
	}
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
		s.resetAgentRunState(ctx, run, workerChatBudgetKey, workerRunGuardKey)
		reply.reset(ctx)
		return true
	}
	reply.interrupt(ctx)
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
// others as interrupted) and ends the waits over their limits (expireWorkerChatRun); it returns how many it changed.
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
					if run.ID != "" && s.expireWorkerChatRun(ctx, run, info, now) {
						swept++
					}
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
				canceled := run.Status == store.AgentRunCanceled
				if _, err := reply.update(ctx, true, func(d *workerChatDraft) {
					if canceled {
						d.fail("", true, reply.durationMS())
					} else {
						d.interrupt(reply.durationMS())
					}
				}); err == nil {
					swept++
				}
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
	// Turns is how many model turns the reply's runs took so far (copied to a resume with the budget).
	Turns int `json:"turns,omitempty"`
}

// workerChatBudgetMax is a worker run's call budget (a chat reply's or a built-in session run's): its payload's,
// else Flow's.
func (s *server) workerChatBudgetMax(run store.AgentRun) int {
	if maxCalls, ok := s.workerRunBudgetMax(run); ok && maxCalls > 0 {
		return maxCalls
	}
	return s.agentMaxToolCalls()
}

func workerChatBudgetDecode(raw []byte, maxCalls int) (*agentToolBudget, workerChatBudgetState) {
	var state workerChatBudgetState
	_ = json.Unmarshal(raw, &state)
	budget := newAgentToolBudget(maxCalls)
	budget.calls, budget.repeats, budget.failedTurns, budget.turnCalls, budget.turnFailed, budget.stop = state.Calls, state.Repeats, state.FailedTurns, state.TurnCalls, state.TurnFailed, state.Stop
	for key, value := range state.Failed {
		budget.failed[key] = value
	}
	return budget, state
}

func workerChatBudgetEncode(budget *agentToolBudget, reply workerChatBudgetState) ([]byte, error) {
	return json.Marshal(workerChatBudgetState{Calls: budget.calls, Failed: budget.failed, Repeats: budget.repeats, FailedTurns: budget.failedTurns, TurnCalls: budget.turnCalls, TurnFailed: budget.turnFailed, Stop: budget.stop, Nudged: reply.Nudged, Turns: reply.Turns})
}

// workerChatBudget reads a chat run's tool budget.
func (s *server) workerChatBudget(ctx context.Context, run store.AgentRun) *agentToolBudget {
	budget, _ := s.workerChatBudgetState(ctx, run)
	return budget
}

func (s *server) workerChatBudgetState(ctx context.Context, run store.AgentRun) (*agentToolBudget, workerChatBudgetState) {
	raw, _, found, err := s.agentRunStateOf(ctx, run.ID, workerChatBudgetKey)
	if err != nil || !found {
		raw = nil
	}
	return workerChatBudgetDecode(raw, s.workerChatBudgetMax(run))
}

// updateWorkerChatBudget changes a chat run's tool budget atomically across API servers: the parallel /mcp calls of
// a turn may reach different servers at once, and each counts against the one budget (a versioned state, so change
// runs again on the newer budget when another server stored first).
func (s *server) updateWorkerChatBudget(ctx context.Context, run store.AgentRun, change func(*agentToolBudget, *workerChatBudgetState)) {
	maxCalls := s.workerChatBudgetMax(run)
	err := s.updateAgentRunState(ctx, run, workerChatBudgetKey, func(raw []byte, _ bool) ([]byte, error) {
		budget, state := workerChatBudgetDecode(raw, maxCalls)
		change(budget, &state)
		return workerChatBudgetEncode(budget, state)
	})
	if err != nil {
		log.Printf("agent run %s: tool budget not saved: %v", run.ID, err)
	}
}

// workerChatToolBefore applies a worker run's tool budget to a /mcp call, as the in-process run does before each
// call: ok is false when the call is not run, content its result (an error). A built-in session run's connector
// calls are only counted (the run guard answers their repeats), as executeBuiltinApplication counts them.
func (s *server) workerChatToolBefore(ctx context.Context, run store.AgentRun, name string, args map[string]any) (content string, ok bool) {
	if _, budgeted := s.workerRunBudgetMax(run); !budgeted {
		return "", true
	}
	_, thread := workerThreadInfo(run)
	counted := thread && strings.HasPrefix(name, "external_")
	arguments, _ := json.Marshal(args)
	ok = true
	s.updateWorkerChatBudget(ctx, run, func(budget *agentToolBudget, _ *workerChatBudgetState) {
		if counted {
			content, ok = budget.count()
			return
		}
		content, ok = budget.before(domain.AgentToolCall{Name: name, Arguments: arguments})
	})
	return content, ok
}

// workerChatToolAfter records a chat run's /mcp call outcome in its tool budget.
func (s *server) workerChatToolAfter(ctx context.Context, run store.AgentRun, name string, args map[string]any, content string, failed bool) {
	if _, budgeted := s.workerRunBudgetMax(run); !budgeted {
		return
	}
	if _, thread := workerThreadInfo(run); thread && strings.HasPrefix(name, "external_") {
		return
	}
	arguments, _ := json.Marshal(args)
	s.updateWorkerChatBudget(ctx, run, func(budget *agentToolBudget, _ *workerChatBudgetState) {
		budget.after(domain.AgentToolCall{Name: name, Arguments: arguments}, content, failed)
	})
}

// workerChatGuardOp is the run guard of a chat run (agent_runtime_guard.go): no coding context or connector
// budget, but the reply's tool budget at each model turn and the outcome check at its last word, as the in-process
// reply applies them. The guard state keeps only whether the last word was nudged and given.
func (s *server) workerChatGuardOp(ctx context.Context, run store.AgentRun, info workerChatRunInfo, input workerRunGuardRequest) (workerRunGuardResponse, error) {
	response := workerRunGuardResponse{Enabled: true, DecisionRound: -1}
	maxTurns := info.MaxTurns
	if maxTurns <= 0 {
		maxTurns = s.agentMaxToolTurns()
	}
	switch input.Op {
	case "start":
		raw, err := json.Marshal(workerRunGuardState{})
		if err == nil {
			err = s.store.ReplaceAgentRunState(ctx, run, workerRunGuardKey, raw)
		}
		return response, err
	case "before", "after", "coding":
		return response, nil
	case "turn":
		// The worker counts the turns of its own run; the reply's earlier runs (before a resume) took TurnsBefore.
		round := info.TurnsBefore + input.Round
		s.updateWorkerChatBudget(ctx, run, func(budget *agentToolBudget, state *workerChatBudgetState) {
			response.Withhold, response.Nudge = false, ""
			if round > 0 {
				budget.endTurn()
			}
			if budget.stop == "" && round >= maxTurns {
				budget.stop = agentBudgetTurns
			}
			state.Turns = max(state.Turns, round+1)
			if budget.stop != "" {
				// The reply's last word: no more tools, what was done and what is left.
				response.Withhold = true
				if !state.Nudged {
					response.Nudge, state.Nudged = "\n\n"+agentToolBudgetNudge(budget.stop, maxTurns, info.MaxCalls), true
				}
			}
		})
		return response, nil
	case "finish":
	default:
		return response, store.ErrAgentRunInput
	}
	ledger := s.agentRunLedger(ctx, run)
	stopped := ""
	if input.Phase == "turnLimit" {
		s.updateWorkerChatBudget(ctx, run, func(budget *agentToolBudget, _ *workerChatBudgetState) {
			if budget.stop == "" {
				budget.stop = agentBudgetTurns
			}
			stopped = budget.stop
		})
	} else {
		stopped = s.workerChatBudget(ctx, run).stop
	}
	ungrounded := input.Phase == "reply" && info.Resume == nil && stopped == "" && s.agentUngroundedText(info.Request, input.Text, ledger)
	err := s.updateAgentRunState(ctx, run, workerRunGuardKey, func(raw []byte, found bool) ([]byte, error) {
		var state workerRunGuardState
		if found {
			_ = json.Unmarshal(raw, &state)
		}
		response = workerRunGuardResponse{Enabled: true, DecisionRound: -1}
		switch input.Phase {
		case "reply":
			switch {
			case ungrounded && !state.OutcomeNudged:
				// Not the last word yet: the model is told once that nothing ran.
				response.Nudge, state.OutcomeNudged = agentUngroundedOutcomeNudge, true
				return json.Marshal(state)
			case ungrounded:
				response.Reply = agentNothingExecutedText(info.Chinese)
			default:
				response.Reply = ledger.report(input.Text, info.Chinese)
			}
		case "turnLimit":
			response.Reply = agentToolBudgetFallback(stopped, info.MaxTurns, info.MaxCalls, info.Chinese, -1, true)
		default:
			response.Reply = input.Text
		}
		state.Finished = true
		return json.Marshal(state)
	})
	return response, err
}
