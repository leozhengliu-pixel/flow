package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A chat reply is saved to the conversation while it is written, not only when it ends: the stored copy (status
// "streaming") carries the text so far, the tool calls with their results and any approval or connector question
// waiting for the owner. A reloaded page or another tab of the owner reads it like any other message and answers its
// approval or question through the usual routes; the run replaces it with the finished reply (or the failed or stopped
// one). Saves are coalesced on one goroutine per reply: part boundaries save at once, streamed text at most once per
// agentLiveSaveInterval, and a reply that is waiting refreshes UpdatedAt every agentLiveHeartbeat so other servers
// can tell a live reply from one whose server went away.

const (
	agentMessageStreaming  = "streaming"
	agentLiveSaveInterval  = time.Second
	agentLiveHeartbeat     = time.Minute
	agentLiveStaleAfter    = 3 * time.Minute
	agentLiveSweepInterval = time.Minute
	agentReplyInterrupted  = "The reply was interrupted before it finished. Retry to run it again."
)

var errAgentLiveSkip = errors.New("agent live reply: nothing to save")

type agentLiveReply struct {
	s         *server
	ctx       context.Context
	workspace string
	sessionID string
	template  domain.AgentMessage
	// baseText names the text parts of the last full snapshot; only the run's goroutine (observe) uses it.
	baseText map[string]bool

	mu sync.Mutex
	// base is the last full snapshot of the parts (a deep copy, never changed afterwards); texts holds newer text for
	// its text parts, so a streamed token costs no copy of the reply. dirty means base is not saved yet.
	base      []domain.AgentMessagePart
	texts     map[string]string
	dirty     bool
	written   *domain.AgentMessage
	lastWrite time.Time
	gone      bool // the conversation no longer holds the reply (it was edited or deleted meanwhile)
	finalized bool

	wake     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	haltOnce sync.Once
}

// startAgentLiveReply begins saving the reply messageID of session sessionID while it runs.
func (s *server) startAgentLiveReply(r *http.Request, sessionID, messageID string, started time.Time) *agentLiveReply {
	live := &agentLiveReply{
		s:         s,
		ctx:       store.WithMetadataFields(context.WithoutCancel(r.Context()), "agentSessions"),
		workspace: workspaceKey(r),
		sessionID: sessionID,
		template:  domain.AgentMessage{ID: messageID, Role: "assistant", CreatedAt: started.UTC()},
		wake:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	s.agentLiveReplies.Store(messageID, live)
	go live.run()
	return live
}

// observe records the reply's parts. force (a part boundary) saves a full snapshot at once; otherwise only the text
// of known text parts changed, and the newest text is saved within agentLiveSaveInterval.
func (l *agentLiveReply) observe(parts []domain.AgentMessagePart, force bool) {
	if l == nil {
		return
	}
	if !force {
		texts := map[string]string{}
		for _, part := range parts {
			if part.Type != "text" {
				continue
			}
			if !l.baseText[part.ID] {
				force = true
				break
			}
			texts[part.ID] = part.Text
		}
		if !force {
			l.mu.Lock()
			l.texts = texts
			l.mu.Unlock()
			return
		}
	}
	// A deep copy: the run keeps changing its parts (and the values they point to) after this.
	raw, err := json.Marshal(parts)
	if err != nil {
		return
	}
	snapshot := []domain.AgentMessagePart{}
	if json.Unmarshal(raw, &snapshot) != nil {
		return
	}
	l.baseText = map[string]bool{}
	for _, part := range snapshot {
		if part.Type == "text" {
			l.baseText[part.ID] = true
		}
	}
	l.mu.Lock()
	l.base, l.texts, l.dirty = snapshot, nil, true
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

func agentLiveText(parts []domain.AgentMessagePart) string {
	var text strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	return strings.TrimSpace(text.String())
}

// next returns the message to save now, if any: a new snapshot or newer text, else a heartbeat for a waiting reply.
func (l *agentLiveReply) next(now time.Time) *domain.AgentMessage {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.gone {
		return nil
	}
	var parts []domain.AgentMessagePart
	switch {
	case l.dirty || l.texts != nil:
		parts = append([]domain.AgentMessagePart(nil), l.base...)
		for index := range parts {
			if text, ok := l.texts[parts[index].ID]; ok {
				parts[index].Text = text
			}
		}
	case l.written != nil && now.Sub(l.lastWrite) >= agentLiveHeartbeat:
		parts = l.written.Parts
	default:
		return nil
	}
	l.dirty, l.texts = false, nil
	message := l.template
	message.Parts = parts
	if message.Parts == nil {
		message.Parts = []domain.AgentMessagePart{}
	}
	message.Content = agentLiveText(parts)
	message.Status = agentMessageStreaming
	stamp := now.UTC()
	message.UpdatedAt = &stamp
	return &message
}

func (l *agentLiveReply) run() {
	defer close(l.done)
	ticker := time.NewTicker(agentLiveSaveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-l.wake:
		case <-ticker.C:
		}
		if message := l.next(time.Now()); message != nil {
			l.write(*message)
		}
	}
}

func (l *agentLiveReply) write(message domain.AgentMessage) {
	l.mu.Lock()
	created := l.written != nil
	l.mu.Unlock()
	gone := false
	err := l.s.store.MutateWorkspace(l.ctx, l.workspace, "agent.message_updated", l.sessionID, nil, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, l.sessionID)
		if err != nil {
			gone = true
			return errAgentLiveSkip
		}
		for index := range session.Messages {
			if session.Messages[index].ID != message.ID {
				continue
			}
			if session.Messages[index].Status != agentMessageStreaming {
				return errAgentLiveSkip
			}
			session.Messages[index] = message
			return nil
		}
		if created {
			// The conversation was edited past this reply; do not bring it back.
			gone = true
			return errAgentLiveSkip
		}
		session.Messages = append(session.Messages, message)
		return nil
	})
	l.mu.Lock()
	defer l.mu.Unlock()
	if gone {
		l.gone = true
	}
	if err == nil {
		l.written, l.lastWrite = &message, time.Now()
	} else if !errors.Is(err, errAgentLiveSkip) {
		log.Printf("save streaming agent reply %s: %v", message.ID, err)
	}
}

// halt stops saving and waits for a save in flight, so the finished reply is written after the last snapshot.
func (l *agentLiveReply) halt() {
	l.haltOnce.Do(func() {
		close(l.stop)
		<-l.done
	})
}

// finishAgentLiveReply is called right before the reply's final form is saved.
func (s *server) finishAgentLiveReply(messageID string) {
	value, ok := s.agentLiveReplies.Load(messageID)
	if !ok {
		return
	}
	live := value.(*agentLiveReply)
	live.halt()
	live.mu.Lock()
	live.finalized = true
	live.mu.Unlock()
}

// release ends the reply's tracking when the run returns. A run that ended without saving its reply (an unexpected
// error) must not leave a reply that looks like it is still being written: it is saved as interrupted.
func (l *agentLiveReply) release() {
	if l == nil {
		return
	}
	l.halt()
	l.mu.Lock()
	written, finalized, gone := l.written, l.finalized, l.gone
	l.mu.Unlock()
	if written != nil && !finalized && !gone {
		_ = l.s.store.MutateWorkspace(l.ctx, l.workspace, "agent.message_completed", l.sessionID, nil, func(data *domain.Bootstrap) error {
			session, err := ownedAgentSession(data, l.sessionID)
			if err != nil {
				return err
			}
			if !interruptAgentLiveMessages(session, func(message domain.AgentMessage) bool { return message.ID == written.ID }) {
				return errAgentLiveSkip
			}
			return nil
		})
	}
	l.s.agentLiveReplies.Delete(l.template.ID)
}

// interruptAgentLiveMessage turns a reply that stopped being written into a failed one: unfinished tool calls get an
// "interrupted" result (so the next message is a valid conversation), waiting approvals and questions expire, and an
// error row offers Retry.
func interruptAgentLiveMessage(message *domain.AgentMessage) {
	settleInterruptedAgentParts(message.Parts, false)
	for index := range message.Parts {
		part := &message.Parts[index]
		if part.Status == "running" || part.Status == "pending" {
			part.Status = "error"
		}
		if part.ToolCall != nil && (part.ToolCall.Status == "running" || part.ToolCall.Status == "pending") {
			call := *part.ToolCall
			call.Status, call.ApprovalID = "error", ""
			if call.Error == "" {
				call.Error = agentReplyInterrupted
			}
			part.ToolCall = &call
		}
	}
	message.Parts = append(message.Parts, domain.AgentMessagePart{ID: message.ID + "_error", Type: "error", Text: agentReplyInterrupted, Status: "error"})
	message.Status = ""
	message.UpdatedAt = nil
}

// interruptAgentLiveMessages interrupts session's streaming replies that match; it reports whether any did.
func interruptAgentLiveMessages(session *domain.AgentSession, match func(domain.AgentMessage) bool) bool {
	changed := false
	for index := range session.Messages {
		message := &session.Messages[index]
		if message.Status != agentMessageStreaming || !match(*message) {
			continue
		}
		interruptAgentLiveMessage(message)
		changed = true
	}
	return changed
}

func (s *server) startAgentLiveReplySweeper() {
	if !s.agentLiveSweepStarted.CompareAndSwap(false, true) {
		return
	}
	go func() {
		ctx := s.store.WorkerContext()
		// Replies saved as streaming by an earlier run of this server were cut off by the restart.
		s.sweepAgentLiveReplies(ctx, time.Now())
		ticker := time.NewTicker(agentLiveSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepAgentLiveReplies(ctx, time.Now())
			}
		}
	}()
}

// agentLiveReplyAbandoned reports whether a stored streaming reply has no server writing it. A single server knows
// all its runs; with several servers (Redis coordination) a reply belongs to a peer until its heartbeat goes stale.
func (s *server) agentLiveReplyAbandoned(message domain.AgentMessage, now time.Time) bool {
	if message.Status != agentMessageStreaming {
		return false
	}
	if _, local := s.agentLiveReplies.Load(message.ID); local {
		return false
	}
	if s.coordinator == nil {
		return true
	}
	return message.UpdatedAt == nil || now.Sub(*message.UpdatedAt) > agentLiveStaleAfter
}

// sweepAgentLiveReplies saves abandoned streaming replies as interrupted; it returns how many it changed.
func (s *server) sweepAgentLiveReplies(ctx context.Context, now time.Time) int {
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
			abandoned := false
			for _, message := range session.Messages {
				if s.agentLiveReplyAbandoned(message, now) {
					abandoned = true
					break
				}
			}
			if !abandoned {
				continue
			}
			sessionID, owner := session.ID, session.UserID
			mutationCtx := store.WithMetadataFields(store.ContextWithActor(ctx, domain.User{ID: owner}), "agentSessions")
			err := s.store.MutateWorkspace(mutationCtx, key, "agent.message_completed", sessionID, nil, func(next *domain.Bootstrap) error {
				for index := range next.AgentSessions {
					current := &next.AgentSessions[index]
					if current.ID != sessionID {
						continue
					}
					if interruptAgentLiveMessages(current, func(message domain.AgentMessage) bool { return s.agentLiveReplyAbandoned(message, now) }) {
						swept++
						return nil
					}
				}
				return errAgentLiveSkip
			})
			if err != nil && !errors.Is(err, errAgentLiveSkip) && ctx.Err() == nil {
				log.Printf("interrupt agent reply in session %s: %v", sessionID, err)
			}
		}
	}
	return swept
}

// storedAgentLivePart finds the part of session's streaming reply that waits on approval or elicitation id.
func (s *server) storedAgentLivePart(r *http.Request, session domain.AgentSession, id string) (domain.AgentMessagePart, bool) {
	session = s.withAgentBodies(r, session)
	for _, message := range session.Messages {
		if message.Status != agentMessageStreaming {
			continue
		}
		for _, part := range message.Parts {
			if part.ToolCall != nil && part.ToolCall.ApprovalID == id && part.Status == "pending" {
				return part, true
			}
			if part.Type == "elicitation" && part.Elicitation != nil && part.ID == id && part.Status == "pending" {
				return part, true
			}
		}
	}
	return domain.AgentMessagePart{}, false
}

func agentLiveEventForcesSave(eventType string) bool {
	switch eventType {
	case "text.delta", "tool.delta":
		return false
	}
	return true
}
