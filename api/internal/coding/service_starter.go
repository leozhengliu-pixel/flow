package coding

import (
	"context"
	"slices"
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
// its select elicitation, ordered and preselected by the trigger's ranking
// (EnvironmentSuggestions).
func (s ServiceStarter) StartCodingSession(ctx context.Context, request TriggerStartRequest) (domain.CodingSession, bool, error) {
	if s.Service == nil {
		return domain.CodingSession{}, false, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	resourceType, resourceID := request.AnchorResourceType, request.AnchorResourceID
	if resourceType == "issue" {
		resourceType, resourceID = "", ""
	}
	candidates, preferred := rankedCandidates(request)
	session, created, err := s.Service.Start(ctx, StartInput{
		WorkspaceKey: request.WorkspaceKey, Source: request.Source, SourceID: request.SourceID, IssueID: request.IssueID,
		AnchorID: request.AnchorTaskID, ResourceType: resourceType, ResourceID: resourceID,
		Prompt: request.Prompt, TurnKind: request.TurnKind, EnvironmentID: request.EnvironmentID,
		EnvironmentCandidates: candidates, PreferredEnvironmentID: preferred,
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

// rankedCandidates turns the trigger's suggestions for an ambiguous
// repository into ranked environment ids and the preselected one: the top
// suggestion when it has a signal and leads the runner-up.
func rankedCandidates(request TriggerStartRequest) ([]string, string) {
	if request.EnvironmentID != "" || len(request.EnvironmentSuggestions) == 0 {
		return nil, ""
	}
	ids := make([]string, 0, len(request.EnvironmentSuggestions))
	for _, suggestion := range request.EnvironmentSuggestions {
		if suggestion.EnvironmentID != "" && !slices.Contains(ids, suggestion.EnvironmentID) {
			ids = append(ids, suggestion.EnvironmentID)
		}
	}
	top := request.EnvironmentSuggestions[0]
	if top.Score > 0 && (len(request.EnvironmentSuggestions) == 1 || top.Score > request.EnvironmentSuggestions[1].Score) {
		return ids, top.EnvironmentID
	}
	return ids, ""
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
	return s.Service.Prompt(ctx, workspaceKey, sessionID, PromptInput{Prompt: turn.Prompt, AuthorID: turn.AuthorID, Attachments: turn.Attachments, Kind: turn.Kind, TurnID: turn.ID, Scope: turn.Scope})
}
