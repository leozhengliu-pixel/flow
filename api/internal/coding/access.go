package coding

import (
	"context"
	"errors"
	"fmt"

	"flow/api/internal/domain"
)

// Who may start and run a session. A session runs as its creator: the
// creator must be an active, non-guest member of the workspace (a guest only
// when the session was approved after an external request), must be able to
// read the issue the session works on, and the environment must exist in the
// workspace and not be archived. Flow has no per-environment use ACL
// (environment owners gate management only), so "may use the environment"
// is that workspace-level rule. The same checks run again before every
// sandbox is provisioned, so a trigger, loop or restart that fires after the
// creator lost access is refused at run time.

func (s *Service) actorDirectory() ActorDirectory {
	if s.directory == nil {
		return nil
	}
	directory, _ := s.directory.(ActorDirectory)
	return directory
}

// issueFor reads an issue with the actor's access when the directory knows
// how, and as the server otherwise.
func (s *Service) issueFor(ctx context.Context, workspaceKey, actorID, issueID string) (IssueContext, error) {
	if directory := s.actorDirectory(); directory != nil {
		return directory.CodingIssueFor(ctx, workspaceKey, actorID, issueID)
	}
	return s.directory.CodingIssue(ctx, workspaceKey, issueID)
}

// checkEnvironment refuses an environment that is missing, in another
// workspace (lookups are workspace-scoped) or archived.
func (s *Service) checkEnvironment(ctx context.Context, workspaceKey, environmentID string) error {
	if s.envs == nil || environmentID == "" {
		return nil
	}
	env, err := s.envs.CodingEnvironment(ctx, workspaceKey, environmentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, domain.ErrCodingNotFound) || isNotFound(err) {
			return ErrNotFound
		}
		return err
	}
	if env.ArchivedAt != nil {
		return fmt.Errorf("%w: the environment is archived", ErrInvalidInput)
	}
	return nil
}

// authorizeStart runs the checks that must pass before a session exists,
// before anything is built from the issue or sent to a model. It returns the
// issue as the actor reads it.
func (s *Service) authorizeStart(ctx context.Context, in StartInput) (IssueContext, error) {
	issue := IssueContext{ID: in.IssueID}
	directory := s.actorDirectory()
	if directory != nil {
		if err := directory.CodingActorAllowed(ctx, in.WorkspaceKey, in.CreatorID, in.RequireApproval); err != nil {
			return issue, err
		}
	}
	if in.Source != domain.CodingSourceEnvironmentSetup {
		if err := s.checkEnvironment(ctx, in.WorkspaceKey, in.EnvironmentID); err != nil {
			return issue, err
		}
	}
	if in.IssueID != "" && s.directory != nil {
		found, err := s.issueFor(ctx, in.WorkspaceKey, in.CreatorID, in.IssueID)
		if err != nil {
			return issue, err
		}
		issue = found
	}
	if directory != nil && in.SourceID != "" {
		if err := directory.CodingSourceReadable(ctx, in.WorkspaceKey, in.CreatorID, in.Source, in.SourceID, in.IssueID); err != nil {
			return issue, err
		}
	}
	return issue, nil
}

// creatorStillAllowed repeats the actor checks for a session that is about to
// get a sandbox (environment secrets and git credentials follow only after it
// passes). nil means the creator may still run it.
func (s *Service) creatorStillAllowed(ctx context.Context, session domain.CodingSession) *ProvisionOutcome {
	directory := s.actorDirectory()
	if directory == nil || session.CreatorID == "" {
		return nil
	}
	denied := func(message string) *ProvisionOutcome {
		return &ProvisionOutcome{Code: domain.CodingErrPermissionDenied, Message: message}
	}
	if err := directory.CodingActorAllowed(ctx, session.WorkspaceKey, session.CreatorID, session.ApprovalState == "approved"); err != nil {
		if errors.Is(err, ErrForbidden) {
			return denied("The person this session runs as can no longer use this workspace's coding environments.")
		}
		return &ProvisionOutcome{Code: domain.CodingErrInternal, Message: err.Error(), Transient: true}
	}
	if session.IssueID != "" {
		if _, err := directory.CodingIssueFor(ctx, session.WorkspaceKey, session.CreatorID, session.IssueID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return denied("The person this session runs as can no longer read its issue.")
			}
			return &ProvisionOutcome{Code: domain.CodingErrInternal, Message: err.Error(), Transient: true}
		}
	}
	return nil
}

// activeCandidates keeps the trigger's ranked environments that still exist
// and are not archived.
func (s *Service) activeCandidates(ctx context.Context, workspaceKey string, ids []string) []string {
	if s.envs == nil || len(ids) == 0 {
		return ids
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if s.checkEnvironment(ctx, workspaceKey, id) == nil {
			kept = append(kept, id)
		}
	}
	return kept
}

// CreatorAllowed is creatorStillAllowed for callers outside provisioning (a
// prompt, a model request): ErrForbidden when the person the session runs as
// can no longer use it, nil when they may (or the check cannot be made now,
// which only transient errors cause and which must not end a session).
func (s *Service) CreatorAllowed(ctx context.Context, session domain.CodingSession) error {
	if outcome := s.creatorStillAllowed(ctx, session); outcome != nil && !outcome.Transient {
		return ErrForbidden
	}
	return nil
}
