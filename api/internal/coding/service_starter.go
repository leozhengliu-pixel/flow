package coding

import (
	"context"
	"time"

	"flow/api/internal/domain"
)

// ServiceStarter is the production SessionStarter (track L's triggers): a
// thin adapter over the session core, so every trigger-started session goes
// through Service.Start (spend check, anchor, initial turn, state machine)
// and every follow-up through Service.Prompt. StoreStarter remains for
// trigger tests that run without a Service.
type ServiceStarter struct {
	Service *Service
	// PullRequests links the PR/MR of PR-sourced sessions (optional).
	PullRequests StarterPullRequests
}

var _ SessionStarter = ServiceStarter{}

// StartCodingSession implements SessionStarter. An ambiguous repository
// (AwaitEnvironment) leaves EnvironmentID empty: provisioning then asks with
// its own select elicitation.
func (s ServiceStarter) StartCodingSession(ctx context.Context, request TriggerStartRequest) (domain.CodingSession, bool, error) {
	if s.Service == nil {
		return domain.CodingSession{}, false, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	resourceType, resourceID := request.AnchorResourceType, request.AnchorResourceID
	if resourceType == "issue" {
		resourceType, resourceID = "", ""
	}
	session, created, err := s.Service.Start(ctx, StartInput{
		WorkspaceKey: request.WorkspaceKey, Source: request.Source, SourceID: request.SourceID, IssueID: request.IssueID,
		AnchorID: request.AnchorTaskID, ResourceType: resourceType, ResourceID: resourceID,
		Prompt: request.Prompt, TurnKind: request.TurnKind, EnvironmentID: request.EnvironmentID,
		Harness: request.Harness, Model: request.Model, BaseRef: request.BaseRef, BranchName: request.BranchName,
		ExistingBranch: request.ExistingBranch, CreatorID: request.CreatorID, LoopID: request.LoopID,
		RequireApproval: request.RequireApproval, IdempotencyKey: request.IdempotencyKey, Title: request.Title,
	})
	if err != nil {
		return session, created, err
	}
	if request.PullRequest != nil && s.PullRequests != nil {
		pullRequest := *request.PullRequest
		pullRequest.SessionID = session.ID
		now := time.Now().UTC()
		if pullRequest.CreatedAt.IsZero() {
			pullRequest.CreatedAt = now
		}
		pullRequest.UpdatedAt = now
		if err := s.PullRequests.UpsertSessionPullRequest(ctx, request.WorkspaceKey, pullRequest); err != nil {
			return session, created, err
		}
	}
	return session, created, nil
}

// PromptCodingSession implements SessionStarter. The trigger's deterministic
// turn id makes a redelivered event a no-op.
func (s ServiceStarter) PromptCodingSession(ctx context.Context, workspaceKey, sessionID string, turn Turn) (domain.CodingSession, error) {
	if s.Service == nil {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	current, err := s.Service.store.CodingSession(ctx, workspaceKey, sessionID)
	if err != nil {
		return current, err
	}
	if notApproved(current) {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "This session was not approved; start a new one."}
	}
	return s.Service.Prompt(ctx, workspaceKey, sessionID, PromptInput{Prompt: turn.Prompt, AuthorID: turn.AuthorID, Attachments: turn.Attachments, Kind: turn.Kind, TurnID: turn.ID})
}
