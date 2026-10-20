package coding

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// The executors behind ActProvision and ActPublish. Both are idempotent:
// provisioning reuses a live sandbox the session already owns, and
// publishing looks the PR/MR up by head branch before creating it.

// taskText is what Auto routes on: the session title and its first prompt.
func (s *Service) taskText(ctx context.Context, session domain.CodingSession) string {
	text := session.DisplayTitle
	if items, err := s.store.CodingInbox(ctx, session.ID, 0); err == nil {
		for _, item := range items {
			if item.Kind == InboxTurn && item.Turn != nil {
				return text + "\n" + item.Turn.Prompt
			}
		}
	}
	return text
}

// provision resolves everything a sandbox needs and creates (or binds) it.
func (s *Service) provision(ctx context.Context, session domain.CodingSession, sandboxes []sandbox.Record) ProvisionOutcome {
	info, err := s.workspaces.CodingWorkspace(ctx, session.WorkspaceKey)
	if err != nil {
		return ProvisionOutcome{Code: domain.CodingErrInternal, Message: err.Error(), Transient: true}
	}
	policy := NormalizeAgentSettings(info.Settings.CodingAgentSettings)
	env, selection, outcome := s.resolveEnvironment(ctx, session)
	if outcome != nil {
		return *outcome
	}
	if len(selection) > 0 {
		return ProvisionOutcome{Selection: selection}
	}
	if session.Source == domain.CodingSourceLoop || session.LoopID != "" {
		if !LoopAllowed(policy, env.ID) {
			return ProvisionOutcome{Code: domain.CodingErrRepoNotInPolicy, Message: "Loops may not start coding sessions on " + env.Repository + "."}
		}
	}
	harness := session.Harness
	if harness == "" {
		harness = env.Harness
	}
	harness = NormalizeHarness(harness)
	selectionModel := session.ModelSelection
	if selectionModel == "" {
		selectionModel = env.Model
	}
	model, ok := ResolveModel(s.cfg.LLM, harness, selectionModel)
	if !ok {
		return ProvisionOutcome{Code: domain.CodingErrModelUnavailable, Message: fmt.Sprintf("No model is available for %s (%s).", harness, selectionModel)}
	}
	modelReason, escalation := "", EscalationModel(s.cfg.LLM, harness, model)
	if selectionModel == "" || selectionModel == domain.CodingModelAuto {
		if session.Model != "" && slices.Contains(ModelCatalog(s.cfg.LLM, harness), session.Model) {
			// A routed session keeps its model (and an Auto fallback) across sandboxes.
			model, modelReason, escalation = session.Model, session.ModelReason, EscalationModel(s.cfg.LLM, harness, session.Model)
		} else if route, routed := RouteAutoModel(s.cfg.LLM, harness, s.taskText(ctx, session)); routed && route.Model != "" {
			model, modelReason, escalation = route.Model, route.Reason, route.Escalation
		}
	}
	codeHost := env.CodeHost
	if codeHost == "" {
		codeHost = domain.CodeHostGitHub
	}
	repo := codehost.RepoRef{CodeHost: codeHost, HostName: env.HostName, FullName: env.Repository, ID: env.RepositoryID}
	host, ok := s.hosts[codeHost]
	if !ok {
		return ProvisionOutcome{Code: codehost.ErrorCode(codeHost, codehost.ErrNotConnected), Message: "No " + codeHost + " code host is configured."}
	}
	if connected, err := host.Connected(ctx, session.WorkspaceKey); err != nil {
		return hostFailure(host, codeHost, err)
	} else if !connected {
		return ProvisionOutcome{Code: codehost.ErrorCode(codeHost, codehost.ErrNotConnected), Message: "The workspace has no " + codeHost + " connection for coding sessions."}
	}
	identity := codehost.UserIdentity{FlowUserID: session.CreatorID}
	if s.directory != nil {
		if user, err := s.directory.CodingUser(ctx, session.WorkspaceKey, session.CreatorID); err == nil {
			identity.Login, identity.Email = user.Login, user.Email
		}
	}
	if account, ok := s.memberAccount(ctx, session.WorkspaceKey, session.CreatorID, codeHost, env.HostName); ok && account.Login != "" {
		identity.Login = account.Login
	}
	if err := host.CheckUserAccess(ctx, session.WorkspaceKey, repo, identity); err != nil && !errors.Is(err, codehost.ErrNotImplemented) {
		return hostFailure(host, codeHost, err)
	}
	signingFormat, signingKey := "", ""
	if policy.CommitSigningEnabled && s.ResolveCommitSigning != nil {
		format, key, err := s.ResolveCommitSigning(ctx, session.WorkspaceKey, session.CreatorID)
		signingFormat, signingKey = format, key
		if err == nil && format == "" {
			return ProvisionOutcome{Code: domain.CodingErrMissingCommitSigningKey, Message: "Signed commits are required: upload a commit signing key in your account settings."}
		}
		if err != nil {
			if code, ok := codedError(err); ok {
				return ProvisionOutcome{Code: code, Message: err.Error()}
			}
			return ProvisionOutcome{Code: domain.CodingErrInternal, Message: err.Error(), Transient: true}
		}
	} else if policy.CommitSigningEnabled {
		if s.signing == nil {
			return ProvisionOutcome{Code: domain.CodingErrMissingCommitSigningKey, Message: "Signed commits are required but commit signing is not available."}
		}
		if _, key, err := s.signing.SigningKeyMaterial(ctx, session.CreatorID); err != nil || len(key) == 0 {
			if err != nil && !errors.Is(err, ErrNotFound) && !isNotFound(err) {
				return ProvisionOutcome{Code: domain.CodingErrInternal, Message: err.Error(), Transient: true}
			}
			return ProvisionOutcome{Code: domain.CodingErrMissingCommitSigningKey, Message: "Signed commits are required: upload a commit signing key in your account settings."}
		}
	}
	size := env.SandboxSize
	if _, ok := s.cfg.Sizes[size]; !ok || size == "" {
		size = domain.CodingSizeMedium
	}
	region := policy.PinnedRegion
	if region == "" {
		region = s.cfg.DefaultRegion
	}
	snapshotID, workspace, volumeRef := "", sandbox.VolumeSource{}, ""
	if snapshot, ok, err := s.resolveSnapshot(ctx, session, env); err != nil {
		return ProvisionOutcome{Code: domain.CodingErrInternal, Message: err.Error(), Transient: true}
	} else if ok {
		if snapshot.Strategy != sandbox.StrategyVolumeSnapshot {
			snapshotID = snapshot.ID
			workspace.ArchiveURL = strings.TrimRight(s.cfg.RuntimeURL, "/") + "/internal/coding-runtime/snapshots/" + snapshot.ID + "/archive"
		} else if s.RestoreSnapshot != nil && snapshot.StorageRef != "" {
			// A VolumeSnapshot restores into a new PVC named for the sandbox,
			// so it is restored below once the sandbox id exists. Without a
			// restore hook the session runs setup inline.
			snapshotID, volumeRef = snapshot.ID, snapshot.StorageRef
		}
	}
	apply := func(target *domain.CodingSession) {
		target.EnvironmentID = env.ID
		target.CodeHost, target.HostName, target.Repository = codeHost, env.HostName, env.Repository
		if target.BaseRef == "" {
			target.BaseRef = env.DefaultBranch
		}
		if target.BaseRef == "" {
			target.BaseRef = "main"
		}
		target.Harness, target.Model, target.SandboxSize, target.Region, target.SnapshotID = harness, model, size, region, snapshotID
		target.ModelReason, target.EscalationModel = modelReason, escalation
		target.SigningFormat, target.SigningPublicKey = signingFormat, signingKey
		if target.ModelSelection == "" {
			target.ModelSelection = domain.CodingModelAuto
		}
	}
	// Idempotency: a live session sandbox written by an earlier pass whose
	// session update was lost is reused instead of creating another one.
	for index := len(sandboxes) - 1; index >= 0; index-- {
		record := sandboxes[index]
		if record.Kind != sandbox.KindWarm && record.Desired == sandbox.DesiredRunning && liveSandbox(record) {
			return ProvisionOutcome{SandboxID: record.ID, Apply: apply}
		}
	}
	if snapshotID == "" || workspace.ArchiveURL != "" {
		// A warm runner restores the archive itself after binding (through
		// the archive route, which needs the sandbox bound to the snapshot).
		if bound, err := s.store.BindWarmSandbox(ctx, size, region, session.ID, snapshotID); err == nil && bound.ID != "" {
			return ProvisionOutcome{SandboxID: bound.ID, Apply: apply}
		} else if err != nil && !errors.Is(err, ErrNotFound) && !isNotFound(err) {
			return ProvisionOutcome{Code: domain.CodingErrSandboxStartFailed, Message: err.Error(), Transient: true}
		}
	}
	now := s.now().UTC()
	record := sandbox.Record{
		ID: NewID(), WorkspaceKey: session.WorkspaceKey, SessionID: session.ID, SnapshotID: snapshotID, Kind: sandbox.KindSession,
		Desired: sandbox.DesiredRunning, Phase: sandbox.PhasePending, Image: s.cfg.Image, Size: size, Region: region, Workspace: workspace,
		Env:       map[string]string{sandbox.EnvMode: ModeSession, sandbox.EnvFlowSandbox: "1", sandbox.EnvLinearCompat: "1", sandbox.EnvRuntimeURL: s.cfg.RuntimeURL},
		CreatedAt: now,
	}
	if volumeRef != "" {
		restored, err := s.RestoreSnapshot(ctx, volumeRef, record.ID, session.WorkspaceKey)
		if err != nil {
			return ProvisionOutcome{Code: domain.CodingErrSandboxStartFailed, Message: err.Error(), Transient: true}
		}
		record.Workspace = restored
	}
	if s.cfg.Timeouts.MaxSession > 0 {
		record.DeadlineAt = now.Add(s.cfg.Timeouts.MaxSession)
	}
	if err := s.store.CreateCodingSandbox(ctx, record); err != nil {
		return ProvisionOutcome{Code: domain.CodingErrSandboxStartFailed, Message: err.Error(), Transient: true}
	}
	return ProvisionOutcome{SandboxID: record.ID, Apply: apply}
}

func (s *Service) resolveSnapshot(ctx context.Context, session domain.CodingSession, env domain.CodingEnvironment) (domain.CodingEnvironmentSnapshot, bool, error) {
	// After a failed restore (startFailed clears SnapshotID) the retry runs setup inline.
	if s.snapshots == nil || session.SandboxAttempts > 0 && session.SnapshotID == "" {
		return domain.CodingEnvironmentSnapshot{}, false, nil
	}
	return s.snapshots.ResolveCodingSnapshot(ctx, session.WorkspaceKey, env)
}

// resolveEnvironment picks the session's environment: the explicit one, or
// the single active one. Several candidates ask the user (select).
func (s *Service) resolveEnvironment(ctx context.Context, session domain.CodingSession) (domain.CodingEnvironment, []domain.CodingEnvironment, *ProvisionOutcome) {
	fail := func(code domain.CodingErrorCode, message string, transient bool) (domain.CodingEnvironment, []domain.CodingEnvironment, *ProvisionOutcome) {
		return domain.CodingEnvironment{}, nil, &ProvisionOutcome{Code: code, Message: message, Transient: transient}
	}
	if s.envs == nil {
		return fail(domain.CodingErrNoPolicyRepos, "No coding environments are configured.", false)
	}
	if session.EnvironmentID != "" {
		env, err := s.envs.CodingEnvironment(ctx, session.WorkspaceKey, session.EnvironmentID)
		if errors.Is(err, ErrNotFound) || isNotFound(err) {
			return fail(domain.CodingErrNoPolicyRepos, "The session's environment no longer exists.", false)
		}
		if err != nil {
			return fail(domain.CodingErrInternal, err.Error(), true)
		}
		if env.ArchivedAt != nil && session.Phase == domain.CodingPhaseProvisioning && session.RunningMillis == 0 && session.LastTurnID == "" {
			return fail(domain.CodingErrRepoNotInPolicy, "The environment for "+env.Repository+" is archived.", false)
		}
		return env, nil, nil
	}
	envs, err := s.envs.ListCodingEnvironments(ctx, session.WorkspaceKey, false)
	if err != nil {
		return fail(domain.CodingErrInternal, err.Error(), true)
	}
	switch len(envs) {
	case 0:
		return fail(domain.CodingErrNoPolicyRepos, "Create a coding environment for a repository first.", false)
	case 1:
		return envs[0], nil, nil
	}
	return domain.CodingEnvironment{}, rankEnvironments(envs, session.EnvironmentCandidates), nil
}

// rankEnvironments orders the selection by the trigger's ranking (best
// first); environments the ranking did not include follow in their listed
// order, and ranked ids that are gone are skipped.
func rankEnvironments(envs []domain.CodingEnvironment, candidates []string) []domain.CodingEnvironment {
	if len(candidates) == 0 {
		return envs
	}
	ranked := make([]domain.CodingEnvironment, 0, len(envs))
	for _, id := range candidates {
		if index := slices.IndexFunc(envs, func(env domain.CodingEnvironment) bool { return env.ID == id }); index >= 0 && !slices.ContainsFunc(ranked, func(env domain.CodingEnvironment) bool { return env.ID == id }) {
			ranked = append(ranked, envs[index])
		}
	}
	for _, env := range envs {
		if !slices.Contains(candidates, env.ID) {
			ranked = append(ranked, env)
		}
	}
	return ranked
}

func hostFailure(host codehost.Host, codeHost string, err error) ProvisionOutcome {
	code, message := codehost.DescribeError(host, codeHost, err)
	return ProvisionOutcome{Code: code, Message: message, Transient: errors.Is(err, codehost.ErrTransient)}
}

// isNotFound treats sql.ErrNoRows-style errors from other stores as not found.
func isNotFound(err error) bool {
	return err != nil && (errors.Is(err, codehost.ErrNotFound) || errors.Is(err, sandbox.ErrNotFound) || strings.Contains(err.Error(), "no rows"))
}

// publish verifies the pushed head and opens or updates the draft PR/MR.
func (s *Service) publish(ctx context.Context, session domain.CodingSession) PublishOutcome {
	if session.Repository == "" || session.BranchName == "" {
		return PublishOutcome{}
	}
	host, ok := s.hosts[session.CodeHost]
	if !ok {
		return PublishOutcome{Code: codehost.ErrorCode(session.CodeHost, codehost.ErrNotConnected), Message: "No " + session.CodeHost + " code host is configured."}
	}
	repo := codehost.RepoRef{CodeHost: session.CodeHost, HostName: session.HostName, FullName: session.Repository}
	failure := func(err error, fallback domain.CodingErrorCode) PublishOutcome {
		code, message := codehost.DescribeError(host, session.CodeHost, err)
		if code == domain.CodingErrInternal {
			code = fallback
		}
		return PublishOutcome{Code: code, Message: message, Transient: errors.Is(err, codehost.ErrTransient)}
	}
	head, err := host.BranchHead(ctx, session.WorkspaceKey, repo, session.BranchName)
	if errors.Is(err, codehost.ErrNotFound) {
		return PublishOutcome{Code: domain.CodingErrPushFailed, Message: "The branch " + session.BranchName + " was not pushed."}
	}
	if err != nil {
		return failure(err, domain.CodingErrPullRequestFailed)
	}
	if session.HeadSHA != "" && head != session.HeadSHA {
		return PublishOutcome{Code: domain.CodingErrPushFailed, Message: fmt.Sprintf("The branch head %s does not match the pushed commit %s.", short(head), short(session.HeadSHA))}
	}
	change, err := host.FindChangeRequest(ctx, session.WorkspaceKey, repo, session.BranchName)
	if err != nil && !errors.Is(err, codehost.ErrNotFound) {
		return failure(err, domain.CodingErrPullRequestFailed)
	}
	openedAs, openedVia := "", ""
	if change == nil {
		actor := s.memberActor(ctx, session, repo)
		created, err := host.CreateChangeRequest(ctx, session.WorkspaceKey, codehost.CreateChangeRequestInput{Repo: repo, Head: session.BranchName, Base: session.BaseRef, Title: pullRequestTitle(session), Body: s.pullRequestBody(ctx, session), Draft: true, ActAs: actor})
		if err != nil {
			return failure(err, domain.CodingErrPullRequestFailed)
		}
		change = &created
		openedAs, openedVia = created.AuthorLogin, domain.CodeHostAccountActorBot
		if created.OpenedByMember {
			openedVia = domain.CodeHostAccountActorMember
			if openedAs == "" && actor != nil {
				openedAs = actor.Login
			}
		}
	}
	now := s.now().UTC()
	pr := domain.SessionPullRequest{SessionID: session.ID, CodeHost: session.CodeHost, HostName: session.HostName, Repository: session.Repository, Number: change.Number, ExternalID: change.ExternalID, URL: change.URL, Title: change.Title, State: change.State, Draft: change.Draft, HeadBranch: change.HeadBranch, BaseBranch: change.BaseBranch, HeadSHA: head, OpenedAs: openedAs, OpenedVia: openedVia, CreatedAt: now, UpdatedAt: now}
	if pr.State == "" {
		pr.State = "open"
	}
	for _, existing := range session.PullRequests {
		if existing.Number == pr.Number && existing.CodeHost == pr.CodeHost {
			pr.CreatedAt = existing.CreatedAt
		}
	}
	if s.prs != nil {
		if err := s.prs.UpsertSessionPullRequest(ctx, session.WorkspaceKey, pr); err != nil {
			return PublishOutcome{Code: domain.CodingErrPullRequestFailed, Message: err.Error(), Transient: true}
		}
	}
	return PublishOutcome{PullRequest: &pr}
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func pullRequestTitle(session domain.CodingSession) string {
	if title := strings.TrimSpace(session.DisplayTitle); title != "" {
		return title
	}
	return "Flow coding session"
}

func (s *Service) pullRequestBody(ctx context.Context, session domain.CodingSession) string {
	var body strings.Builder
	if summary := strings.TrimSpace(session.Summary); summary != "" {
		body.WriteString(summary)
		body.WriteString("\n\n")
	}
	if session.IssueID != "" && s.directory != nil {
		if issue, err := s.directory.CodingIssue(ctx, session.WorkspaceKey, session.IssueID); err == nil {
			if issue.Identifier != "" {
				body.WriteString("Fixes " + issue.Identifier)
			}
			if issue.URL != "" {
				body.WriteString(" (" + issue.URL + ")")
			}
			body.WriteString("\n\n")
		}
	}
	body.WriteString("Opened by a Flow coding session. Review the changes before merging.")
	if s.DecoratePullRequestBody != nil {
		return s.DecoratePullRequestBody(ctx, session, body.String())
	}
	return body.String()
}
