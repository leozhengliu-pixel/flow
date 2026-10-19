package coding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// StoreStarter is a SessionStarter on the frozen store contracts
// (SessionStore and ChangeRequestStore). It writes the session row and its
// AgentTask anchor in one CreateCodingSession call (idempotent by key),
// links the PR/MR for PR-sourced sessions, and enqueues the initial turn.
// The session reconciler (track B) does everything after that. When track
// B's coding.Service lands, the server can wrap Service.Start instead; the
// trigger code depends only on the SessionStarter interface.
type StoreStarter struct {
	Sessions     StarterSessions
	PullRequests StarterPullRequests // optional
	Config       config.CodingConfig
	// DefaultHarness and DefaultModel come from the workspace settings when
	// the request does not name them.
	DefaultHarness string
	DefaultModel   string
	Now            func() time.Time
	NewID          func() string
}

// StarterSessions is the SessionStore subset StoreStarter needs.
type StarterSessions interface {
	CreateCodingSession(ctx context.Context, session domain.CodingSession, anchor domain.AgentTask, idempotencyKey string) (domain.CodingSession, bool, error)
	CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)
	UpdateCodingSession(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error)
	EnqueueCodingInbox(ctx context.Context, workspaceKey, sessionID string, item InboxItem) (int64, error)
}

// StarterPullRequests links PRs/MRs to sessions.
type StarterPullRequests interface {
	UpsertSessionPullRequest(ctx context.Context, workspaceKey string, pullRequest domain.SessionPullRequest) error
}

func (s *StoreStarter) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *StoreStarter) newID() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return newTriggerID(s.now())
}

// StartCodingSession implements SessionStarter.
func (s *StoreStarter) StartCodingSession(ctx context.Context, request TriggerStartRequest) (domain.CodingSession, bool, error) {
	if s.Sessions == nil {
		return domain.CodingSession{}, false, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	now := s.now()
	id := s.newID()
	anchorID := request.AnchorTaskID
	if anchorID == "" {
		anchorID = "cs_" + id
	}
	key := request.IdempotencyKey
	if key == "" {
		key = "session:" + id
	}
	phase := request.InitialPhase()
	harness := firstNonEmpty(request.Harness, s.DefaultHarness, domain.CodingHarnessCodex)
	model := firstNonEmpty(request.Model, s.DefaultModel, domain.CodingModelAuto)
	session := domain.CodingSession{
		ID: id, WorkspaceKey: request.WorkspaceKey, AgentSessionID: anchorID, IssueID: request.IssueID,
		EnvironmentID: request.EnvironmentID, CreatorID: request.CreatorID, Source: request.Source, SourceID: request.SourceID,
		State: PhasePolicies(s.Config)[phase].State, Phase: phase, Harness: harness, ModelSelection: model,
		DisplayTitle: request.Title, CodeHost: request.CodeHost, Repository: request.Repository,
		BranchName: request.BranchName, BaseRef: request.BaseRef, CreatedAt: now, UpdatedAt: now,
	}
	if request.RequireApproval {
		session.ApprovalState = ApprovalRequired
	}
	if request.PullRequest != nil {
		pullRequest := *request.PullRequest
		pullRequest.SessionID = id
		session.PullRequests = []domain.SessionPullRequest{pullRequest}
		session.LatestPullRequest = &pullRequest
	}
	anchor := domain.AgentTask{
		ID: anchorID, WorkspaceKey: request.WorkspaceKey, IssueID: request.IssueID, TeamID: request.TeamID,
		AppUserID: request.AnchorAppUserID, CreatorID: request.CreatorID, Status: anchorStatus(phase),
		Prompt: request.Prompt, InitialPrompt: request.Prompt, Trigger: "coding:" + request.Source,
		ResourceType: request.AnchorResourceType, ResourceID: request.AnchorResourceID, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	created, isNew, err := s.Sessions.CreateCodingSession(ctx, session, anchor, key)
	if err != nil {
		return domain.CodingSession{}, false, fmt.Errorf("coding: create session: %w", err)
	}
	if request.PullRequest != nil && s.PullRequests != nil {
		pullRequest := *request.PullRequest
		pullRequest.SessionID = created.ID
		if pullRequest.CreatedAt.IsZero() {
			pullRequest.CreatedAt = now
		}
		pullRequest.UpdatedAt = now
		if err := s.PullRequests.UpsertSessionPullRequest(ctx, request.WorkspaceKey, pullRequest); err != nil {
			return created, isNew, fmt.Errorf("coding: link pull request: %w", err)
		}
	}
	if isNew {
		// The initial turn waits in the inbox until the session runs; its id
		// is deterministic so a retry after a partial failure is harmless.
		turn := Turn{ID: turnID(created.ID, "initial"), Prompt: request.Prompt, AuthorID: request.CreatorID, Kind: firstNonEmpty(request.TurnKind, TurnKindInitial)}
		if _, err := s.Sessions.EnqueueCodingInbox(ctx, request.WorkspaceKey, created.ID, InboxItem{Kind: "turn", Turn: &turn}); err != nil {
			return created, isNew, fmt.Errorf("coding: enqueue initial turn: %w", err)
		}
	}
	return created, isNew, nil
}

// PromptCodingSession implements SessionStarter: it enqueues the turn and,
// for a finished session, re-queues it (done → queued). Running, idle and
// waiting sessions pick the turn up from the inbox (FIFO per session).
func (s *StoreStarter) PromptCodingSession(ctx context.Context, workspaceKey, sessionID string, turn Turn) (domain.CodingSession, error) {
	if s.Sessions == nil {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	if strings.TrimSpace(turn.Prompt) == "" {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrInternal, Message: "A prompt is required."}
	}
	if turn.Kind == "" {
		turn.Kind = TurnKindFollowUp
	}
	current, err := s.Sessions.CodingSession(ctx, workspaceKey, sessionID)
	if err != nil {
		return domain.CodingSession{}, err
	}
	if notApproved(current) {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "This session was not approved; start a new one."}
	}
	if _, err := s.Sessions.EnqueueCodingInbox(ctx, workspaceKey, sessionID, InboxItem{Kind: "turn", Turn: &turn}); err != nil {
		return domain.CodingSession{}, fmt.Errorf("coding: enqueue turn: %w", err)
	}
	now := s.now()
	return s.Sessions.UpdateCodingSession(ctx, workspaceKey, sessionID, func(session *domain.CodingSession) error {
		if session.Phase != domain.CodingPhaseDone {
			return nil
		}
		if notApproved(*session) {
			return nil // the turn stays undelivered; the session is closed for good
		}
		session.Phase = domain.CodingPhaseQueued
		session.State = domain.CodingSessionPending
		session.ErrorCode = ""
		session.Error = ""
		session.EndedAt = nil
		session.UpdatedAt = now
		return nil
	})
}

func notApproved(session domain.CodingSession) bool {
	return session.Phase == domain.CodingPhaseDone && (session.ApprovalState == ApprovalRejected || session.ErrorCode == domain.CodingErrApprovalExpired)
}

func anchorStatus(phase domain.CodingSessionPhase) string {
	switch phase {
	case domain.CodingPhaseAwaitingApproval, domain.CodingPhaseAwaitingUser:
		return "awaitingInput"
	default:
		return "pending"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
