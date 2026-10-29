package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const chatDMReplyJobKind = "dm_reply"

type chatDMReplyJob struct {
	Command   chatInboundCommand `json:"command"`
	UserID    string             `json:"userId"`
	Workspace string             `json:"workspace"`
	SessionID string             `json:"sessionId,omitempty"`
	ThreadKey string             `json:"threadKey"`
	MessageID string             `json:"messageId,omitempty"`
	CardID    string             `json:"cardId,omitempty"`
	Chinese   bool               `json:"chinese,omitempty"`
	Started   bool               `json:"started,omitempty"`
	Final     *chatDMReplyView   `json:"final,omitempty"`
}

// The mutex orders checkpoints, heartbeats and platform writes. Every write
// renews the claim first and has a timeout shorter than its one-minute lease.
type chatDMReplyLease struct {
	mu      sync.Mutex
	s       *server
	ctx     context.Context
	cancel  context.CancelFunc
	job     domain.ChatJob
	payload chatDMReplyJob
}

func (l *chatDMReplyLease) checkpoint(ctx context.Context, thread string) (bool, error) {
	raw, err := json.Marshal(l.payload)
	if err != nil {
		return false, err
	}
	return l.s.store.CheckpointChatReplyJob(ctx, &l.job, raw, thread, time.Now())
}

func (l *chatDMReplyLease) update(change func(*chatDMReplyJob), thread string, deliver func(context.Context) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, cancel := context.WithTimeout(l.ctx, 10*time.Second)
	defer cancel()
	if change != nil {
		change(&l.payload)
	}
	ok, err := l.checkpoint(ctx, thread)
	if err != nil {
		l.cancel()
		return err
	}
	if !ok {
		return errChatDMThreadBusy
	}
	if deliver != nil {
		return deliver(ctx)
	}
	return nil
}

var errChatDMThreadBusy = errors.New("chat thread is answering another message")

func chatDMReplyThreadKey(dm chatDMRequest, sessionID string) string {
	if sessionID != "" {
		return chatKey(dm.target.Workspace, "session", sessionID)
	}
	root := firstNonEmpty(chatString(dm.command.Values, "rootId"), chatString(dm.command.Values, "parentId"), dm.command.MessageID)
	return chatKey(dm.record.ConnectionID, root, "message")
}

func (l *chatDMReplyLease) sessionStarted(sessionID, messageID string) error {
	return l.update(func(p *chatDMReplyJob) {
		p.SessionID, p.MessageID = sessionID, messageID
	}, chatKey(l.payload.Workspace, "session", sessionID), nil)
}

func (s *server) startChatDMReplyJobs(ctx context.Context, workspace string) error {
	for range 4 {
		jobs, err := s.store.ClaimChatJobs(ctx, workspace, chatDMReplyJobKind, 1, time.Now())
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		job := jobs[0]
		chatDMReplies.Add(1)
		go func() {
			defer chatDMReplies.Done()
			progress := &chatDeliveryProgress{}
			parent := context.WithValue(context.WithoutCancel(ctx), chatDeliveryProgressKey{}, progress)
			runCtx, cancel := context.WithTimeout(parent, chatDMRunLimit)
			defer cancel()
			stopWorker := context.AfterFunc(s.store.WorkerContext(), cancel)
			defer stopWorker()
			if s.coordinationContext != nil {
				stop := context.AfterFunc(s.coordinationContext, cancel)
				defer stop()
			}
			lease := &chatDMReplyLease{s: s, ctx: runCtx, cancel: cancel, job: job}
			if err := json.Unmarshal(job.Payload, &lease.payload); err != nil {
				_ = s.store.FinishChatJob(runCtx, job, "failed", time.Time{}, "Invalid DM reply job")
				return
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				ticker := time.NewTicker(10 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-runCtx.Done():
						return
					case <-ticker.C:
						if lease.update(nil, "", nil) != nil {
							return
						}
					}
				}
			}()
			err := s.processChatDMReplyJob(lease)
			// Cancellation leaves the processing job for a successor. Never
			// acknowledge a run whose ownership or server lifetime ended.
			lease.mu.Lock()
			if runCtx.Err() == nil {
				status, runAt, message := "completed", time.Time{}, ""
				if err != nil {
					status, runAt, message = "pending", time.Now().Add(15*time.Second), "DM reply delivery failed"
					if lease.payload.CardID == "" && chatJobFinal(err, lease.job) {
						status = "failed"
					}
				}
				var platform *chatPlatformError
				if errors.As(err, &platform) && !platform.Retryable || err == nil && progress.sent {
					healthCtx, healthCancel := context.WithTimeout(runCtx, 5*time.Second)
					_ = s.chatDeliveryHealth(healthCtx, lease.job, platform, err == nil)
					healthCancel()
				}
				if finishErr := s.store.FinishChatJob(runCtx, lease.job, status, runAt, message); finishErr != nil {
					log.Printf("Chat DM reply completion: %v", finishErr)
				} else if status == "completed" || lease.payload.Final != nil || !lease.payload.Started {
					_ = s.store.ReleaseChatReplyLeases(runCtx, lease.job)
				}
			}
			lease.mu.Unlock()
			cancel()
			<-done
		}()
	}
	return nil
}

func (s *server) processChatDMReplyJob(l *chatDMReplyLease) error {
	l.mu.Lock()
	p, job := l.payload, l.job
	l.mu.Unlock()
	record, err := s.chatJobIntegration(l.ctx, job)
	if err != nil || !record.Connected {
		return err
	}
	identity, linked, err := s.chatDMLinked(l.ctx, record, p.Command.OpenID)
	if err != nil {
		return err
	}
	dm := chatDMRequest{record: record, identity: identity, command: p.Command, job: job, zh: p.Chinese}
	targets, err := s.chatDMTargets(l.ctx, record, identity)
	if err != nil {
		return err
	}
	target, found := chatDMFindTarget(targets, p.Workspace)
	dm.target = target
	if !linked || identity.UserID != p.UserID || !found {
		view := chatDMReplyView{Status: "stopped", Failure: chatDMText(p.Chinese, "Stopped: this chat account is no longer linked to Flow.", "已停止：这个聊天账号已不再关联 Flow。")}
		return s.chatDMDeliverReplyFinal(l, dm, view)
	}
	if handled, err := s.chatDMAuthorize(l.ctx, &dm); handled {
		if err != nil || p.CardID == "" {
			return err
		}
		return s.chatDMDeliverReplyFinal(l, dm, chatDMReplyView{Status: "stopped", Failure: chatDMText(dm.zh, "This conversation is no longer available here. Open Flow to continue.", "此对话已无法在这里继续，请在 Flow 中打开。")})
	}
	if p.Final != nil {
		return s.chatDMDeliverReplyFinal(l, dm, *p.Final)
	}
	if p.Started {
		if workerChatMessage(p.MessageID) {
			if err := l.update(nil, chatKey(p.Workspace, "session", p.SessionID), nil); err != nil {
				return err
			}
			return s.chatDMResumeWorkerReply(l, dm, p)
		}
		if data, ok := s.store.WorkspaceMetadataFields(p.Workspace, "agentSessions"); ok {
			for _, session := range data.AgentSessions {
				if session.ID != p.SessionID || session.UserID != p.UserID {
					continue
				}
				loaded, err := s.store.LoadAgentSessionBodies(l.ctx, p.Workspace, session)
				if err != nil {
					return err
				}
				for _, message := range loaded.Messages {
					if message.ID != p.MessageID || message.Status == agentMessageStreaming {
						continue
					}
					view := chatDMFinalView(domain.AgentSession{Messages: []domain.AgentMessage{message}}, dm.zh)
					if view.Status == "done" {
						view.Link = s.chatDMWorkspaceURL(p.Workspace, "/agent/"+url.PathEscape(loaded.SlugID))
						return s.chatDMDeliverReplyFinal(l, dm, view)
					}
				}
			}
		}
		// Do not replay a possibly side-effecting agent call after a crash.
		// The explicit Retry is itself a deduplicated job.
		if err := s.chatDMInterruptSavedReply(l, dm); err != nil {
			return err
		}
		view := chatDMReplyView{Status: "interrupted", Retry: "chat_action_" + chatKey(l.job.ID, "retry")}
		return s.chatDMDeliverReplyFinal(l, dm, view)
	}
	if err := l.update(nil, p.ThreadKey, nil); err != nil {
		if !errors.Is(err, errChatDMThreadBusy) {
			return err
		}
		view := chatDMReplyView{Status: "stopped", Failure: chatDMText(dm.zh, "Flow is still answering your previous message in this conversation. Reply again when it has finished.", "Flow 还在回复这段对话中的上一条消息，请等它完成后再回复。")}
		return s.chatDMDeliverReplyFinal(l, dm, view)
	}
	text, resources, _ := chatDMContent(chatString(p.Command.Values, "messageType"), chatRawString(p.Command.Values, "content"))
	attachments, problem, err := s.chatDMAttachments(l.ctx, record, p.Command.MessageID, resources, dm.zh)
	if err != nil {
		return err
	}
	if problem != "" {
		return s.chatDMDeliverReplyFinal(l, dm, chatDMReplyView{Status: "failed", Failure: problem})
	}
	if strings.TrimSpace(text) == "" {
		text = chatDMText(dm.zh, "See the attached file.", "请看附件。")
	}
	if err := s.chatDMEnsureReplyCard(l, dm, chatDMReplyView{Status: "working"}); err != nil {
		return err
	}
	if err := l.update(func(p *chatDMReplyJob) { p.Started = true }, "", nil); err != nil {
		return err
	}
	path := "/api/agent/sessions/stream"
	var body any = agentSessionInput{Message: text, Location: "page", Attachments: attachments}
	if p.SessionID != "" {
		path = "/api/agent/sessions/" + url.PathEscape(p.SessionID) + "/messages/stream"
		body = agentMessageInput{Message: text, Attachments: attachments}
	}
	r := &chatDMReply{s: s, dm: dm, lease: l, cardID: l.payload.CardID, sessionID: p.SessionID, wake: make(chan struct{}, 1), approvalCards: map[string]chatDMApprovalCardState{}, resolved: map[string]string{}}
	r.run(l.ctx, http.MethodPost, path, body)
	return r.deliveryErr
}

// Worker replies outlive the API request. Follow the saved reply rather than
// starting another run; polling also works when no peer events reach us.
func (s *server) chatDMResumeWorkerReply(l *chatDMReplyLease, dm chatDMRequest, p chatDMReplyJob) (err error) {
	r := &chatDMReply{s: s, dm: dm, lease: l, cardID: p.CardID, sessionID: p.SessionID, wake: make(chan struct{}, 1), approvalCards: map[string]chatDMApprovalCardState{}, resolved: map[string]string{}}
	done := make(chan struct{})
	go r.follow(l.ctx, done)
	defer func() {
		<-done
		if err == nil {
			err = r.deliveryErr
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	seen := map[string]bool{}
	for {
		data, ok := s.store.WorkspaceMetadataFields(p.Workspace, "agentSessions")
		if !ok {
			l.cancel()
			return errNotFound
		}
		for _, session := range data.AgentSessions {
			if session.ID != p.SessionID || session.UserID != p.UserID {
				continue
			}
			loaded, err := s.store.LoadAgentSessionBodies(l.ctx, p.Workspace, session)
			if err != nil {
				l.cancel()
				return err
			}
			for _, message := range loaded.Messages {
				if message.ID != p.MessageID {
					continue
				}
				view := chatDMFinalView(domain.AgentSession{Messages: []domain.AgentMessage{message}}, dm.zh)
				r.mu.Lock()
				r.text, r.steps, r.slug = view.Text, view.Steps, loaded.SlugID
				for _, part := range message.Parts {
					if call := part.ToolCall; call != nil && call.ApprovalID != "" && !seen[call.ApprovalID] && part.Status == "pending" {
						seen[call.ApprovalID] = true
						r.approvals = append(r.approvals, chatDMApprovalRequest{ID: call.ApprovalID, Label: chatDMToolLabel(call), Preview: chatDMApprovalPreview(call, dm.zh)})
					}
				}
				settled := message.Status != agentMessageStreaming
				if settled {
					r.final, r.finished = &loaded, true
				}
				r.mu.Unlock()
				r.poke()
				if settled {
					return nil
				}
				if run, info, ok := s.workerChatRunOf(l.ctx, p.Workspace, p.SessionID, p.MessageID); ok {
					s.expireWorkerChatRun(l.ctx, run, info, time.Now())
				}
			}
		}
		select {
		case <-l.ctx.Done():
			return l.ctx.Err()
		case <-done:
			return r.deliveryErr
		case <-ticker.C:
		}
	}
}

func (s *server) chatDMEnsureReplyCard(l *chatDMReplyLease, dm chatDMRequest, view chatDMReplyView) error {
	return l.update(nil, "", func(ctx context.Context) error {
		if l.payload.CardID != "" {
			return nil
		}
		id, _, err := s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, dm.job.DedupKey+":reply", chatDMReplyCard(view, dm.zh))
		if err != nil {
			return err
		}
		l.payload.CardID = id
		var owned bool
		owned, err = l.checkpoint(ctx, chatKey(dm.record.ConnectionID, id, "message"))
		if err == nil && !owned {
			err = errChatDMThreadBusy
		}
		return err
	})
}

func (s *server) chatDMDeliverReplyFinal(l *chatDMReplyLease, dm chatDMRequest, view chatDMReplyView) error {
	view.Text = chatTruncateText(view.Text, 12000)
	if err := s.chatDMEnsureReplyCard(l, dm, view); err != nil {
		return err
	}
	return l.update(func(p *chatDMReplyJob) { p.Final = &view }, "", func(ctx context.Context) error {
		if l.payload.SessionID != "" && view.Status != "stopped" && dm.identity.UserID == l.payload.UserID {
			thread := chatDMThread{Kind: "session", OpenID: dm.command.OpenID, WorkspaceKey: l.payload.Workspace, SessionID: l.payload.SessionID}
			s.chatDMRememberThread(ctx, dm.record, dm.identity.UserID, dm.command.MessageID, thread)
			s.chatDMRememberThread(ctx, dm.record, dm.identity.UserID, l.payload.CardID, thread)
		}
		if view.Retry != "" {
			action := chatDMAction{Kind: "dm_retry", OpenID: dm.command.OpenID, PersonalChatID: dm.command.ChatID, PersonalMessage: l.payload.CardID, WorkspaceKey: l.payload.Workspace, SessionID: l.payload.SessionID, ReplyJobID: l.job.ID, Chinese: dm.zh}
			raw, _ := json.Marshal(action)
			if err := s.store.PutChatAction(ctx, domain.ChatAction{ID: view.Retry, WorkspaceKey: dm.record.WorkspaceKey, ConnectionID: dm.record.ConnectionID, UserID: dm.identity.UserID, Payload: raw, ExpiresAt: time.Now().Add(chatDMStateLifetime)}); err != nil {
				return err
			}
		}
		return s.chatDMUpdate(ctx, dm.record, l.payload.CardID, chatDMReplyCard(view, dm.zh))
	})
}

func (s *server) chatDMInterruptSavedReply(l *chatDMReplyLease, dm chatDMRequest) error {
	err := l.update(nil, "", func(ctx context.Context) error {
		p := l.payload
		if p.SessionID == "" || p.MessageID == "" {
			return nil
		}
		s.stopAgentRun(p.Workspace, p.SessionID)
		mutationCtx := store.WithMetadataFields(store.ContextWithActor(ctx, domain.User{ID: p.UserID}), "agentSessions")
		return s.store.MutateWorkspace(mutationCtx, p.Workspace, "agent.message_completed", p.SessionID, nil, func(data *domain.Bootstrap) error {
			for index := range data.AgentSessions {
				session := &data.AgentSessions[index]
				if session.ID != p.SessionID || session.UserID != p.UserID {
					continue
				}
				if interruptAgentLiveMessages(session, func(message domain.AgentMessage) bool {
					return message.ID == p.MessageID && !workerChatMessage(message.ID)
				}) {
					return nil
				}
			}
			return store.ErrNoMutation
		})
	})
	if errors.Is(err, store.ErrNoMutation) {
		return nil
	}
	return err
}
