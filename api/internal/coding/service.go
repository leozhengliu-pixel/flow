package coding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// FeatureFlag is the workspace feature flag that turns coding sessions on.
const FeatureFlag = "coding-sessions"

// AnchorTrigger marks AgentTasks created as coding session anchors.
const AnchorTrigger = "codingSession"

// Inbox item kinds.
const (
	InboxTurn   = "turn"
	InboxStop   = "stop"
	InboxSignal = "signal"
)

// Turn kinds.
const (
	TurnInitial   = "initial"
	TurnFollowUp  = "followUp"
	TurnPRComment = "prComment"
	TurnFixPR     = "fixPr"
	TurnRestart   = "restart"
)

// Options configures a Service.
type Options struct {
	Config     config.CodingConfig
	Store      CoreStore
	Anchors    AnchorStore
	Workspaces WorkspaceSource
	// Optional collaborators (other tracks); nil disables what needs them.
	Environments  EnvironmentReader
	Snapshots     SnapshotResolver
	PullRequests  PullRequestStore
	Artifacts     ArtifactReader
	SigningKeys   SigningKeys
	Signer        Signer
	Directory     Directory
	Hosts         map[string]codehost.Host
	Guard         SpendGuard
	Metrics       *Metrics
	Now           func() time.Time
	ProviderReady func() bool
}

// Service is the coding session core: the user-facing actions (§4.3), the
// runner runtime (runtime.go) and the session reconciler (reconcile.go) share it.
type Service struct {
	cfg        config.CodingConfig
	step       StepConfig
	store      CoreStore
	anchors    AnchorStore
	workspaces WorkspaceSource
	envs       EnvironmentReader
	snapshots  SnapshotResolver
	prs        PullRequestStore
	artifacts  ArtifactReader
	signing    SigningKeys
	signer     Signer
	directory  Directory
	hosts      map[string]codehost.Host
	guard      SpendGuard
	metrics    *Metrics
	now        func() time.Time
	ready      func() bool

	inbox  *notifier
	wakeCh chan struct{}

	// SnapshotBootstrap answers GET /bootstrap for snapshot and debug
	// sandboxes (track K); nil refuses them.
	SnapshotBootstrap func(ctx context.Context, grant SandboxGrant) (BootstrapResponse, error)
	// BrowserGuidance is the browser-testing guidance added to the bootstrap
	// guidance when browser testing is on (track I's text).
	BrowserGuidance func() string
	// HostGuidance adds code-host specific guidance (track J's
	// gitlab.AgentGuidance for GitLab environments); nil adds nothing.
	HostGuidance func(session domain.CodingSession) string
	// ResolveCommitSigning resolves the user's signing format and public key
	// when signed commits are required (track E's resolveCodingCommitSigning).
	// An error carrying CodingErrorCode() or Code() fails provisioning with
	// that code without retry (missingCommitSigningKey); other errors are retried.
	// DecoratePullRequestBody adds sections to the PR/MR body before it is
	// created (track I's coding.MergeArtifactSection with the session's
	// screenshots); nil leaves the body as is.
	DecoratePullRequestBody func(ctx context.Context, session domain.CodingSession, body string) string
	ResolveCommitSigning    func(ctx context.Context, workspaceKey, userID string) (format, publicKey string, err error)
	// RestoreSnapshot restores a volumesnapshot snapshot into a new volume
	// for a sandbox (track A's SnapshotStrategy.Restore); nil runs setup
	// inline instead of restoring a VolumeSnapshot. Archive snapshots need no
	// hook (the runner downloads them).
	RestoreSnapshot func(ctx context.Context, storageRef, sandboxID, workspaceKey string) (sandbox.VolumeSource, error)
	// ApprovalExpired is called once when an awaitingApproval session ends
	// with approvalExpired (track L's "approval expired" notification). The
	// session reconciler is the only owner of that transition; the hook runs
	// after the version-checked commit, so a lost race never notifies twice.
	ApprovalExpired func(ctx context.Context, session domain.CodingSession)
	// Members resolves the session creator's linked GitHub / GitLab account:
	// with one the session pushes, commits and opens its PR/MR as the member;
	// without one (or nil) it uses the workspace credential.
	Members MemberCodeHost
}

// codedError extracts an explicit session error code from an error.
func codedError(err error) (domain.CodingErrorCode, bool) {
	var withCode interface{ CodingErrorCode() domain.CodingErrorCode }
	if errors.As(err, &withCode) {
		return withCode.CodingErrorCode(), true
	}
	var withStringCode interface{ CodingErrorCode() string }
	if errors.As(err, &withStringCode) && withStringCode.CodingErrorCode() != "" {
		return domain.CodingErrorCode(withStringCode.CodingErrorCode()), true
	}
	var withString interface{ Code() string }
	if errors.As(err, &withString) && withString.Code() != "" {
		return domain.CodingErrorCode(withString.Code()), true
	}
	return "", false
}

// SessionCredentialRevoker is implemented by code hosts that mint
// revocable per-session credentials (track J's (*gitlab.Host).RevokeSessionCredentials).
// The session core calls it when a session ends.
type SessionCredentialRevoker interface {
	RevokeSessionCredentials(ctx context.Context, workspaceKey, sessionID string) error
}

// NewService builds the session core.
func NewService(options Options) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	ready := options.ProviderReady
	if ready == nil {
		ready = func() bool {
			return options.Config.Enabled && options.Config.Provider != "" && options.Config.Provider != config.CodingProviderDisabled
		}
	}
	hosts := options.Hosts
	if hosts == nil {
		hosts = map[string]codehost.Host{}
	}
	return &Service{
		cfg: options.Config, step: NewStepConfig(options.Config), store: options.Store, anchors: options.Anchors, workspaces: options.Workspaces,
		envs: options.Environments, snapshots: options.Snapshots, prs: options.PullRequests, artifacts: options.Artifacts, signing: options.SigningKeys,
		signer: options.Signer, directory: options.Directory, hosts: hosts, guard: options.Guard, metrics: options.Metrics, now: now, ready: ready,
		inbox: newNotifier(), wakeCh: make(chan struct{}, 1),
	}
}

// Config returns the coding configuration.
func (s *Service) Config() config.CodingConfig { return s.cfg }

// StepConfig returns the state machine configuration.
func (s *Service) StepConfig() StepConfig { return s.step }

// Wake makes the reconciler run soon (an optimization: no state is in memory).
func (s *Service) Wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// StartInput starts a session (design §15: every trigger calls Start).
type StartInput struct {
	WorkspaceKey string
	Source       string
	SourceID     string
	IssueID      string
	// AnchorID reuses an existing AgentTask (a delegation) as the anchor.
	AnchorID string
	// ResourceType/ResourceID anchor a session without an issue (agent chat).
	ResourceType  string
	ResourceID    string
	Prompt        string
	Attachments   []string
	TurnKind      string
	EnvironmentID string
	// EnvironmentCandidates / PreferredEnvironmentID carry the trigger's
	// ranking when the repository is ambiguous (EnvironmentID empty).
	EnvironmentCandidates  []string
	PreferredEnvironmentID string
	Harness                string
	Model                  string
	ReasoningEffort        string
	BaseRef                string
	BranchName             string
	ExistingBranch         bool
	CreatorID              string
	LoopID                 string
	// RequireApproval starts in awaitingApproval (untrusted external source).
	RequireApproval bool
	IdempotencyKey  string
	Title           string
}

var validSources = []string{domain.CodingSourceIssue, domain.CodingSourceAgentChat, domain.CodingSourceComment, domain.CodingSourceLoop, domain.CodingSourcePullRequest, domain.CodingSourceEnvironmentSetup, domain.CodingSourceExternal}

// enabledFor reports whether coding sessions are on for a workspace.
func (s *Service) enabledFor(info WorkspaceInfo) bool {
	return s.cfg.Enabled && info.Settings.FeatureFlags[FeatureFlag]
}

// Start creates a session and its anchor. created is false when the
// idempotency key matched an existing session.
func (s *Service) Start(ctx context.Context, in StartInput) (domain.CodingSession, bool, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.WorkspaceKey == "" || in.CreatorID == "" || in.Prompt == "" || !slices.Contains(validSources, in.Source) || !ValidHarness(in.Harness) || !ValidEffort(in.ReasoningEffort) || len(in.Prompt) > 64<<10 || len(in.IdempotencyKey) > 191 {
		return domain.CodingSession{}, false, ErrInvalidInput
	}
	info, err := s.workspaces.CodingWorkspace(ctx, in.WorkspaceKey)
	if err != nil {
		return domain.CodingSession{}, false, err
	}
	if !s.enabledFor(info) {
		return domain.CodingSession{}, false, ErrDisabled
	}
	policy := NormalizeAgentSettings(info.Settings.CodingAgentSettings)
	harness := in.Harness
	if harness == "" {
		harness = policy.Harness
	}
	harness = NormalizeHarness(harness)
	selection := strings.TrimSpace(in.Model)
	if selection == "" {
		selection = policy.Model
	}
	if _, ok := ResolveModel(s.cfg.LLM, harness, selection); !ok && selection != domain.CodingModelAuto && len(ModelCatalog(s.cfg.LLM, harness)) > 0 {
		return domain.CodingSession{}, false, fmt.Errorf("%w: model %q is not in the %s catalog", ErrInvalidInput, selection, harness)
	}
	effort := in.ReasoningEffort
	if effort == "" {
		effort = policy.ReasoningEffort
	}
	agentUser, err := s.store.BuiltinAgentUserID(ctx, in.WorkspaceKey)
	if err != nil {
		return domain.CodingSession{}, false, err
	}
	if agentUser == "" {
		return domain.CodingSession{}, false, ErrNoAgent
	}
	issue := IssueContext{ID: in.IssueID}
	if in.IssueID != "" && s.directory != nil {
		if issue, err = s.directory.CodingIssue(ctx, in.WorkspaceKey, in.IssueID); err != nil {
			return domain.CodingSession{}, false, err
		}
	}
	now := s.now().UTC()
	id := NewID()
	anchorID := in.AnchorID
	if anchorID == "" {
		anchorID = "coding_" + id
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = issue.Title
	}
	if title == "" {
		title = firstLine(in.Prompt, 80)
	}
	member, linked := s.creatorAccount(ctx, in.WorkspaceKey, in.CreatorID, in.EnvironmentID)
	branch := strings.TrimSpace(in.BranchName)
	if branch == "" {
		prefix := s.cfg.BranchPrefix
		if linked && member.Username != "" {
			// Pushed as the member: their branch, under their own prefix.
			prefix = member.Username + "/"
		}
		branch = BranchName(prefix, issue.Identifier, title, id)
	}
	session := domain.CodingSession{
		ID: id, WorkspaceKey: in.WorkspaceKey, AgentSessionID: anchorID, IssueID: in.IssueID, EnvironmentID: in.EnvironmentID, CreatorID: in.CreatorID,
		Source: in.Source, SourceID: in.SourceID, Harness: harness, ModelSelection: selection, ReasoningEffort: NormalizeEffort(effort),
		DisplayTitle: title, BranchName: branch, BaseRef: strings.TrimSpace(in.BaseRef), ExistingBranch: in.ExistingBranch, LoopID: in.LoopID,
		CreatedAt: now, UpdatedAt: now, Version: 1, NextReconcileAt: now,
	}
	if s.Members != nil {
		session.PushIdentity = &domain.CodingPushIdentity{Via: domain.CodeHostAccountActorBot}
		if linked {
			session.PushIdentity = &domain.CodingPushIdentity{Via: domain.CodeHostAccountActorMember, CodeHost: member.CodeHost, HostName: member.HostName, Login: member.Login}
		}
	}
	if session.EnvironmentID == "" && len(in.EnvironmentCandidates) > 0 {
		session.EnvironmentCandidates = slices.Clone(in.EnvironmentCandidates)
		if slices.Contains(in.EnvironmentCandidates, in.PreferredEnvironmentID) {
			session.PreferredEnvironmentID = in.PreferredEnvironmentID
		}
	}
	phase := domain.CodingPhaseQueued
	if in.RequireApproval {
		phase, session.ApprovalState = domain.CodingPhaseAwaitingApproval, "required"
	}
	s.step.Enter(&session, phase, now)
	var quota error
	if s.guard != nil && !in.RequireApproval {
		if quota = s.guard.Check(ctx, in.WorkspaceKey, in.CreatorID, in.LoopID); quota != nil && !errors.Is(quota, ErrQuotaExceeded) {
			return domain.CodingSession{}, false, quota
		}
	}
	if quota != nil {
		// Session creation fails at once with quotaExceeded (design §20).
		session.ErrorCode, session.Error = domain.CodingErrQuotaExceeded, quota.Error()
		s.step.Enter(&session, domain.CodingPhaseDone, now)
	}
	anchor := domain.AgentTask{ID: anchorID, WorkspaceKey: in.WorkspaceKey, IssueID: in.IssueID, TeamID: issue.TeamID, AppUserID: agentUser, CreatorID: in.CreatorID, Status: "pending", Prompt: in.Prompt, Trigger: AnchorTrigger, Version: 1, CreatedAt: now, UpdatedAt: now, CodingSessionID: id}
	if in.IssueID == "" {
		anchor.ResourceType, anchor.ResourceID = in.ResourceType, in.ResourceID
		if anchor.ResourceType == "" {
			anchor.ResourceType = domain.CodingSourceAgentChat
		}
		if anchor.ResourceID == "" {
			anchor.ResourceID = in.SourceID
		}
	}
	saved, created, err := s.store.CreateCodingSession(ctx, session, anchor, in.IdempotencyKey)
	if err != nil {
		return domain.CodingSession{}, false, err
	}
	if err := s.ensureInitialTurn(ctx, saved, in); err != nil {
		return saved, created, err
	}
	if created && saved.Phase == domain.CodingPhaseDone {
		s.afterTransition(ctx, session, saved, true)
	}
	s.Wake()
	return saved, created, nil
}

// ensureInitialTurn enqueues the first turn (repairing a retried Start whose
// enqueue did not land).
func (s *Service) ensureInitialTurn(ctx context.Context, session domain.CodingSession, in StartInput) error {
	items, err := s.store.CodingInbox(ctx, session.ID, 0)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Kind == InboxTurn {
			return nil
		}
	}
	kind := in.TurnKind
	if kind == "" {
		kind = TurnInitial
	}
	_, err = s.store.EnqueueCodingInbox(ctx, session.WorkspaceKey, session.ID, InboxItem{Kind: InboxTurn, Turn: &Turn{ID: "turn_" + session.ID, Prompt: in.Prompt, AuthorID: in.CreatorID, Attachments: in.Attachments, Kind: kind}})
	return err
}

// BranchName builds the session branch: prefix + issue identifier + title slug + a unique suffix.
func BranchName(prefix, identifier, title, id string) string {
	if prefix == "" {
		prefix = "flow/"
	}
	parts := []string{}
	if identifier != "" {
		parts = append(parts, slug(identifier, 20))
	}
	if text := slug(title, 40); text != "" {
		parts = append(parts, text)
	}
	suffix := id
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	parts = append(parts, suffix)
	return prefix + strings.Join(parts, "-")
}

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func slug(text string, limit int) string {
	value := strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(text), "-"), "-")
	if len(value) > limit {
		value = strings.TrimRight(value[:limit], "-")
	}
	return value
}

func firstLine(text string, limit int) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if runes := []rune(line); len(runes) > limit {
		line = string(runes[:limit-1]) + "…"
	}
	return line
}

// Get returns a session with its sandboxes, PRs/MRs, commits and artifacts.
func (s *Service) Get(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error) {
	session, err := s.store.CodingSession(ctx, workspaceKey, id)
	if err != nil {
		return session, err
	}
	return s.enrich(ctx, session), nil
}

// GetByAnchor returns the session anchored to an AgentTask.
func (s *Service) GetByAnchor(ctx context.Context, workspaceKey, agentSessionID string) (domain.CodingSession, error) {
	return s.store.CodingSessionByAgentSession(ctx, workspaceKey, agentSessionID)
}

// List lists sessions.
func (s *Service) List(ctx context.Context, workspaceKey string, filter SessionFilter) ([]domain.CodingSession, error) {
	return s.store.ListCodingSessions(ctx, workspaceKey, filter)
}

func (s *Service) enrich(ctx context.Context, session domain.CodingSession) domain.CodingSession {
	if records, err := s.store.CodingSandboxes(ctx, session.WorkspaceKey, session.ID); err == nil {
		session.Sandboxes = make([]domain.CodingSandboxRecord, 0, len(records))
		for _, record := range records {
			session.Sandboxes = append(session.Sandboxes, SandboxView(session, record))
		}
	}
	if s.prs != nil {
		if prs, err := s.prs.SessionPullRequests(ctx, session.WorkspaceKey, session.ID); err == nil && len(prs) > 0 {
			session.PullRequests = prs
			latest := prs[0]
			for _, pr := range prs[1:] {
				if pr.UpdatedAt.After(latest.UpdatedAt) {
					latest = pr
				}
			}
			session.LatestPullRequest = &latest
		}
	}
	if commits, err := s.store.CodingCommits(ctx, session.ID); err == nil && len(commits) > 0 {
		session.CommitsByTurnActivityID = map[string][]domain.CodingCommit{}
		for _, commit := range commits {
			session.CommitsByTurnActivityID[commit.TurnActivityID] = append(session.CommitsByTurnActivityID[commit.TurnActivityID], commit)
		}
	}
	if s.artifacts != nil {
		if artifacts, err := s.artifacts.CodingArtifacts(ctx, session.WorkspaceKey, session.ID); err == nil {
			session.Artifacts = artifacts
		}
	}
	return session
}

// SandboxView is a sandbox row as the session JSON shows it (Linear sandboxes[]).
func SandboxView(session domain.CodingSession, record sandbox.Record) domain.CodingSandboxRecord {
	status := "requested"
	switch record.Phase {
	case sandbox.PhaseStarting:
		status = "creating"
	case sandbox.PhaseRunning:
		status = "running"
	case sandbox.PhaseSucceeded:
		status = "stopped"
	case sandbox.PhaseFailed:
		status = "failed"
	case sandbox.PhaseMissing:
		status = "stopped"
		if record.Desired == sandbox.DesiredRunning {
			status = "lost"
		}
	}
	if record.Desired == sandbox.DesiredStopped && liveSandbox(record) {
		status = "stopping"
	}
	view := domain.CodingSandboxRecord{ID: record.ID, SessionID: record.SessionID, Kind: string(record.Kind), Status: status, Region: record.Region, Size: record.Size, Repository: session.Repository, BranchName: session.BranchName, BaseRef: session.BaseRef, CreatedAt: record.CreatedAt, StartedAt: record.StartedAt, EndedAt: record.EndedAt}
	view.LogsURL = "/api/coding/sessions/" + session.ID + "/sandboxes/" + record.ID + "/logs"
	if record.Phase == sandbox.PhaseFailed {
		view.ErrorCode = SandboxErrorCode(record.Reason)
	}
	return view
}

// update applies mutate to a session (optimistic concurrency, retried by the
// store) and runs the post-commit effects of the change.
func (s *Service) update(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error) {
	var before domain.CodingSession
	after, err := s.store.UpdateCodingSession(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
		before = cloneSession(*current)
		return mutate(current)
	})
	if err != nil {
		return after, err
	}
	s.afterTransition(ctx, before, after, false)
	return after, nil
}

// commit writes a decided session over the one it was computed from (CAS on
// the version) and runs the post-commit effects.
func (s *Service) commit(ctx context.Context, before, decided domain.CodingSession) (domain.CodingSession, error) {
	after, err := s.store.UpdateCodingSession(ctx, before.WorkspaceKey, before.ID, func(current *domain.CodingSession) error {
		if current.Version != before.Version {
			return ErrVersionConflict
		}
		version := current.Version
		*current = cloneSession(decided)
		current.Version = version
		return nil
	})
	if err != nil {
		return before, err
	}
	s.afterTransition(ctx, before, after, false)
	return after, nil
}

func cloneSession(session domain.CodingSession) domain.CodingSession {
	session.Plan = slices.Clone(session.Plan)
	session.ExternalURLs = slices.Clone(session.ExternalURLs)
	session.EnvironmentCandidates = slices.Clone(session.EnvironmentCandidates)
	session.Diffs = slices.Clone(session.Diffs)
	session.PullRequests = slices.Clone(session.PullRequests)
	session.SubscriberIDs = slices.Clone(session.SubscriberIDs)
	session.UnsubscribedIDs = slices.Clone(session.UnsubscribedIDs)
	return session
}

// SetSubscription subscribes the user to the session's notifications or
// unsubscribes them ("Subscribe to session"). It is idempotent and changes
// no phase, so it runs no transition effects.
func (s *Service) SetSubscription(ctx context.Context, workspaceKey, id, userID string, subscribed bool) (domain.CodingSession, error) {
	if strings.TrimSpace(userID) == "" {
		return domain.CodingSession{}, ErrInvalidInput
	}
	current, err := s.store.CodingSession(ctx, workspaceKey, id)
	if err != nil {
		return current, err
	}
	if domain.CodingSessionSubscribed(current, userID) == subscribed {
		return s.enrich(ctx, current), nil
	}
	updated, err := s.store.UpdateCodingSession(ctx, workspaceKey, id, func(session *domain.CodingSession) error {
		session.SubscriberIDs = slices.DeleteFunc(slices.Clone(session.SubscriberIDs), func(item string) bool { return item == userID })
		session.UnsubscribedIDs = slices.DeleteFunc(slices.Clone(session.UnsubscribedIDs), func(item string) bool { return item == userID })
		switch {
		case subscribed && userID != session.CreatorID:
			session.SubscriberIDs = append(session.SubscriberIDs, userID)
		case !subscribed:
			session.UnsubscribedIDs = append(session.UnsubscribedIDs, userID)
		}
		return nil
	})
	if err != nil {
		return updated, err
	}
	return s.enrich(ctx, updated), nil
}

// afterTransition writes the anchor activities a phase change implies and
// keeps the anchor's external URLs and plan in step. It runs after the
// session change committed, so a lost CAS never duplicates them.
func (s *Service) afterTransition(ctx context.Context, before, after domain.CodingSession, created bool) {
	if before.Phase != after.Phase || created {
		s.metrics.transition(ctx, before.Phase, after.Phase, after.PhaseStartedAt.Sub(before.PhaseStartedAt), after.ErrorCode)
		slog.Info("coding session phase", "session", after.ID, "from", before.Phase, "to", after.Phase, "error", after.ErrorCode)
	}
	if !slices.Equal(before.ExternalURLs, after.ExternalURLs) || !slices.Equal(before.Plan, after.Plan) {
		s.syncAnchor(ctx, after)
	}
	if before.Phase == after.Phase && !created {
		return
	}
	if before.Phase == domain.CodingPhaseAwaitingApproval && after.Phase == domain.CodingPhaseDone && after.ErrorCode == domain.CodingErrApprovalExpired && s.ApprovalExpired != nil {
		s.ApprovalExpired(ctx, after)
	}
	switch {
	case after.Phase == domain.CodingPhaseIdle && (before.Phase == domain.CodingPhaseRunning || before.Phase == domain.CodingPhasePublishing):
		s.anchorActivity(ctx, after, domain.AgentActivity{Type: "response", Body: responseBody(after)})
	case after.Phase == domain.CodingPhaseDone && after.ErrorCode != "":
		s.anchorActivity(ctx, after, domain.AgentActivity{Type: "error", Body: errorBody(after), Metadata: map[string]any{"errorCode": string(after.ErrorCode)}})
	case after.Phase == domain.CodingPhaseDone && after.State == domain.CodingSessionStale:
		s.anchorActivity(ctx, after, domain.AgentActivity{Type: "canceled", Body: "Coding session stopped."})
	}
	if after.Phase == domain.CodingPhaseStopping || after.Phase == domain.CodingPhaseDone {
		s.inbox.notify(after.ID)
	}
	if after.Phase == domain.CodingPhaseDone {
		s.revokeHostCredentials(ctx, after)
	}
	if after.Phase != domain.CodingPhaseDone {
		s.Wake()
	}
}

func responseBody(session domain.CodingSession) string {
	body := strings.TrimSpace(session.Summary)
	if body == "" {
		body = "Finished the turn."
		if session.LatestPullRequest == nil && len(session.Diffs) == 0 {
			body = "Finished the turn without changing any files."
		}
	}
	if pr := session.LatestPullRequest; pr != nil && !strings.Contains(body, pr.URL) {
		body += "\n\n" + pullRequestLabel(*pr) + ": " + pr.URL
	}
	return body
}

func errorBody(session domain.CodingSession) string {
	message := strings.TrimSpace(session.Error)
	if message == "" {
		message = "The coding session failed."
	}
	return message + " (" + string(session.ErrorCode) + ")"
}

// anchorActivity appends an agent activity to the session's anchor, retrying
// version conflicts. It returns the activity id ("" when the anchor refused it).
func (s *Service) anchorActivity(ctx context.Context, session domain.CodingSession, activity domain.AgentActivity) string {
	if s.anchors == nil {
		return ""
	}
	for attempt := 0; attempt < 5; attempt++ {
		task, err := s.anchors.AgentTask(ctx, session.WorkspaceKey, session.AgentSessionID)
		if err != nil {
			slog.Warn("coding session anchor missing", "session", session.ID, "anchor", session.AgentSessionID, "error", err)
			return ""
		}
		if activity.ActorID == "" && activity.Type != "prompt" {
			activity.ActorID = task.AppUserID
		}
		updated, err := s.anchors.AppendAgentActivity(ctx, session.WorkspaceKey, task.ID, task.Version, activity)
		if err == nil {
			return fmt.Sprintf("%020d", updated.Version)
		}
		if again, readErr := s.anchors.AgentTask(ctx, session.WorkspaceKey, task.ID); readErr != nil || again.Version == task.Version {
			// Not a race: the anchor's state machine refused the activity
			// (for example the session was already canceled from the thread).
			slog.Debug("coding session anchor refused activity", "session", session.ID, "type", activity.Type, "error", err)
			return ""
		}
	}
	return ""
}

func (s *Service) syncAnchor(ctx context.Context, session domain.CodingSession) {
	if s.anchors == nil {
		return
	}
	_, err := s.anchors.UpdateAgentTaskSession(ctx, session.WorkspaceKey, session.AgentSessionID, func(task *domain.AgentTask) error {
		task.Plan = slices.Clone(session.Plan)
		urls := slices.Clone(task.ExternalURLs)
		for _, link := range session.ExternalURLs {
			if !slices.ContainsFunc(urls, func(existing domain.AgentExternalURL) bool { return existing.URL == link.URL }) {
				urls = append(urls, link)
			}
		}
		task.ExternalURLs = urls
		return nil
	})
	if err != nil {
		slog.Warn("coding session anchor sync failed", "session", session.ID, "error", err)
	}
}

// PromptInput is a follow-up turn.
type PromptInput struct {
	Prompt      string
	AuthorID    string
	Attachments []string
	Kind        string
	// ActivityID is the prompt activity already written on the anchor (a
	// reply in the agent thread); when empty Prompt writes one.
	ActivityID string
	// TurnID is a caller-chosen, deterministic turn id (track L's triggers):
	// a turn the inbox already holds is not queued or posted again.
	TurnID string
	// Files are the composer attachments as the web app shows them (Flow
	// upload URLs); they go on the prompt activity's "attachments" metadata.
	// Attachments carries the runner-reachable (signed) URLs of the same files.
	Files []domain.AgentMessageAttachment
}

// promptActivity is the anchor's prompt activity for a follow-up.
func promptActivity(in PromptInput) domain.AgentActivity {
	activity := domain.AgentActivity{Type: "prompt", Body: in.Prompt, ActorID: in.AuthorID}
	if len(in.Files) > 0 {
		activity.Metadata = map[string]any{"attachments": in.Files}
	}
	return activity
}

// Prompt queues a follow-up turn (§4.3): delivered to the warm runner, after
// the current turn, or by re-queueing a finished session.
func (s *Service) Prompt(ctx context.Context, workspaceKey, id string, in PromptInput) (domain.CodingSession, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" || len(in.Prompt) > 64<<10 {
		return domain.CodingSession{}, ErrInvalidInput
	}
	session, err := s.store.CodingSession(ctx, workspaceKey, id)
	if err != nil {
		return session, err
	}
	switch session.Phase {
	case domain.CodingPhaseAwaitingApproval, domain.CodingPhaseStopping:
		return session, ErrConflict
	}
	if s.guard != nil {
		if err := s.guard.Check(ctx, workspaceKey, session.CreatorID, session.LoopID); errors.Is(err, ErrQuotaExceeded) {
			if session.Phase == domain.CodingPhaseDone {
				return session, err
			}
			return s.FailSession(ctx, workspaceKey, id, domain.CodingErrQuotaExceeded, err.Error())
		} else if err != nil {
			return session, err
		}
	}
	if session.Phase == domain.CodingPhaseAwaitingUser && session.EnvironmentID == "" && session.SandboxID == "" {
		// The answer to the repository selection elicitation: it picks the
		// environment and the session provisions again; it is not a turn.
		env := s.matchEnvironment(ctx, workspaceKey, in.Prompt)
		if env == "" {
			return session, fmt.Errorf("%w: answer with one of the listed repositories", ErrInvalidInput)
		}
		if in.ActivityID == "" {
			s.anchorActivity(ctx, session, promptActivity(in))
		}
		return s.update(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
			if current.Phase != domain.CodingPhaseAwaitingUser {
				return ErrConflict
			}
			now := s.now().UTC()
			current.EnvironmentID, current.NextReconcileAt = env, now
			s.step.Enter(current, domain.CodingPhaseQueued, now)
			return nil
		})
	}
	if in.TurnID != "" {
		items, err := s.store.CodingInbox(ctx, id, 0)
		if err != nil {
			return session, err
		}
		for _, item := range items {
			if item.Kind == InboxTurn && item.Turn != nil && item.Turn.ID == in.TurnID {
				return session, nil // a redelivered trigger
			}
		}
	}
	turnID := in.ActivityID
	if turnID == "" {
		turnID = s.anchorActivity(ctx, session, promptActivity(in))
	}
	if in.TurnID != "" {
		turnID = in.TurnID
	}
	if turnID == "" {
		turnID = "turn_" + NewID()
	}
	kind := in.Kind
	if kind == "" {
		kind = TurnFollowUp
	}
	if _, err := s.store.EnqueueCodingInbox(ctx, workspaceKey, id, InboxItem{Kind: InboxTurn, Turn: &Turn{ID: turnID, Prompt: in.Prompt, AuthorID: in.AuthorID, Attachments: in.Attachments, Kind: kind}}); err != nil {
		return session, err
	}
	s.inbox.notify(id)
	updated, err := s.update(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
		now := s.now().UTC()
		current.NextReconcileAt = now
		switch current.Phase {
		case domain.CodingPhaseDone:
			s.resetForTurn(current, now)
		case domain.CodingPhaseAwaitingUser:
			if current.SandboxID == "" {
				s.resetForTurn(current, now)
			}
		}
		return nil
	})
	return updated, err
}

// resetForTurn re-queues a session for a new turn (continue / restart / a
// follow-up after the idle TTL): the pushed branch is checked out again.
func (s *Service) resetForTurn(current *domain.CodingSession, now time.Time) {
	current.ErrorCode, current.Error = "", ""
	current.StopRequested, current.StoppedBy = false, ""
	current.SandboxID, current.SandboxAttempts, current.CurrentTurnID = "", 0, ""
	current.Pushed, current.CurrentStep = false, nil
	if current.HeadSHA != "" || len(current.PullRequests) > 0 {
		current.ExistingBranch = true
	}
	s.step.Enter(current, domain.CodingPhaseQueued, now)
}

func (s *Service) matchEnvironment(ctx context.Context, workspaceKey, answer string) string {
	if s.envs == nil {
		return ""
	}
	envs, err := s.envs.ListCodingEnvironments(ctx, workspaceKey, false)
	if err != nil {
		return ""
	}
	answer = strings.TrimSpace(strings.SplitN(answer, "\n", 2)[0])
	for _, env := range envs {
		if strings.EqualFold(answer, env.ID) || strings.EqualFold(answer, env.Repository) || strings.EqualFold(answer, env.Name) {
			return env.ID
		}
	}
	return ""
}

// Stop requests a stop (§4.3). fromAnchor is true when the stop came from the
// agent thread, which already recorded it.
func (s *Service) Stop(ctx context.Context, workspaceKey, id, userID string, fromAnchor bool) (domain.CodingSession, error) {
	session, err := s.update(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
		if current.Phase == domain.CodingPhaseDone {
			return ErrNoChange
		}
		current.StopRequested = true
		if current.StoppedBy == "" {
			current.StoppedBy = userID
		}
		current.NextReconcileAt = s.now().UTC()
		return nil
	})
	if errors.Is(err, ErrNoChange) {
		return s.store.CodingSession(ctx, workspaceKey, id)
	}
	if err != nil {
		return session, err
	}
	if !fromAnchor {
		s.anchorActivity(ctx, session, domain.AgentActivity{Type: "prompt", Signal: "stop", ActorID: userID, Body: "Stop requested"})
	}
	s.inbox.notify(id)
	s.Wake()
	return session, nil
}

// ErrNoChange aborts an update that has nothing to do.
var ErrNoChange = errors.New("coding: no change")

// Restart re-queues a finished session with the same prompt history (§4.3).
// useDefaultModel is "Restart with default model" (modelSelection=auto).
func (s *Service) Restart(ctx context.Context, workspaceKey, id, userID string, useDefaultModel bool) (domain.CodingSession, error) {
	session, err := s.store.CodingSession(ctx, workspaceKey, id)
	if err != nil {
		return session, err
	}
	if session.Phase != domain.CodingPhaseDone || session.ApprovalState == "rejected" {
		return session, ErrConflict
	}
	if s.guard != nil {
		if err := s.guard.Check(ctx, workspaceKey, session.CreatorID, session.LoopID); err != nil {
			return session, err
		}
	}
	items, err := s.store.CodingInbox(ctx, id, 0)
	if err != nil {
		return session, err
	}
	var last *Turn
	for _, item := range items {
		if item.Kind == InboxTurn && item.Turn != nil {
			turn := *item.Turn
			last = &turn
		}
	}
	if last == nil {
		return session, ErrConflict
	}
	if pending, err := s.store.PendingCodingTurns(ctx, id); err != nil {
		return session, err
	} else if pending == 0 {
		turn := *last
		turn.ID = "turn_" + NewID()
		turn.Kind = TurnRestart
		turn.AuthorID = userID
		if _, err := s.store.EnqueueCodingInbox(ctx, workspaceKey, id, InboxItem{Kind: InboxTurn, Turn: &turn}); err != nil {
			return session, err
		}
	}
	updated, err := s.update(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
		if current.Phase != domain.CodingPhaseDone {
			return ErrConflict
		}
		if useDefaultModel {
			current.ModelSelection, current.Model = domain.CodingModelAuto, ""
		}
		now := s.now().UTC()
		current.NextReconcileAt = now
		s.resetForTurn(current, now)
		return nil
	})
	if err != nil {
		return updated, err
	}
	s.anchorActivity(ctx, updated, domain.AgentActivity{Type: "retry", ActorID: userID, Body: restartBody(useDefaultModel)})
	return updated, nil
}

// StartOver runs the session's task again from its first prompt with model
// ("Start over with <model>", offered with the routed model's next tier). A
// finished session that is still warm drains its sandbox first, because the
// harness runs with the model it started with.
func (s *Service) StartOver(ctx context.Context, workspaceKey, id, userID, model string) (domain.CodingSession, error) {
	session, err := s.store.CodingSession(ctx, workspaceKey, id)
	if err != nil {
		return session, err
	}
	catalog := ModelCatalog(s.cfg.LLM, session.Harness)
	if model = strings.TrimSpace(model); model == "" || model == domain.CodingModelAuto || len(catalog) > 0 && !slices.Contains(catalog, model) {
		return session, ErrInvalidInput
	}
	if session.ApprovalState == "rejected" || session.Phase != domain.CodingPhaseDone && session.Phase != domain.CodingPhaseIdle {
		return session, ErrConflict
	}
	if s.guard != nil {
		if err := s.guard.Check(ctx, workspaceKey, session.CreatorID, session.LoopID); err != nil {
			return session, err
		}
	}
	if session.Phase == domain.CodingPhaseIdle {
		sandboxes, err := s.store.CodingSandboxes(ctx, workspaceKey, id)
		if err != nil {
			return session, err
		}
		decision := Decision{Session: cloneSession(session)}
		s.step.finish(&decision, sandboxes, s.now().UTC())
		s.runSandboxActions(ctx, decision)
		if session, err = s.commit(ctx, session, decision.Session); err != nil {
			return session, err
		}
	}
	items, err := s.store.CodingInbox(ctx, id, 0)
	if err != nil {
		return session, err
	}
	var first *Turn
	for _, item := range items {
		if item.Kind == InboxTurn && item.Turn != nil {
			turn := *item.Turn
			first = &turn
			break
		}
	}
	if first == nil {
		return session, ErrConflict
	}
	if pending, err := s.store.PendingCodingTurns(ctx, id); err != nil {
		return session, err
	} else if pending == 0 {
		turn := *first
		turn.ID = "turn_" + NewID()
		turn.Kind = TurnRestart
		turn.AuthorID = userID
		if _, err := s.store.EnqueueCodingInbox(ctx, workspaceKey, id, InboxItem{Kind: InboxTurn, Turn: &turn}); err != nil {
			return session, err
		}
	}
	updated, err := s.update(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
		if current.Phase != domain.CodingPhaseDone {
			return ErrConflict
		}
		current.ModelSelection, current.Model, current.ModelReason = model, "", ""
		now := s.now().UTC()
		current.NextReconcileAt = now
		s.resetForTurn(current, now)
		return nil
	})
	if err != nil {
		return updated, err
	}
	s.anchorActivity(ctx, updated, domain.AgentActivity{Type: "retry", ActorID: userID, Body: "Started over with " + DescribeModel(s.cfg.LLM, model, slices.Index(catalog, model)).Label})
	return updated, nil
}

func restartBody(defaultModel bool) string {
	if defaultModel {
		return "Restarted with the default model"
	}
	return "Restarted"
}

// Approve approves (or rejects) a session from an untrusted external source.
func (s *Service) Approve(ctx context.Context, workspaceKey, id, userID string, reject bool) (domain.CodingSession, error) {
	return s.update(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
		if current.Phase != domain.CodingPhaseAwaitingApproval {
			return ErrConflict
		}
		now := s.now().UTC()
		current.ApprovedBy = userID
		current.NextReconcileAt = now
		if reject {
			current.ApprovalState = "rejected"
			current.StoppedBy = userID
			s.step.Enter(current, domain.CodingPhaseDone, now)
			return nil
		}
		current.ApprovalState = "approved"
		s.step.Enter(current, domain.CodingPhaseQueued, now)
		return nil
	})
}

// Dismiss ends a session that waits on people: it rejects a pending approval
// or stops a session waiting for an answer.
func (s *Service) Dismiss(ctx context.Context, workspaceKey, id, userID string) (domain.CodingSession, error) {
	session, err := s.store.CodingSession(ctx, workspaceKey, id)
	if err != nil {
		return session, err
	}
	switch session.Phase {
	case domain.CodingPhaseAwaitingApproval:
		return s.Approve(ctx, workspaceKey, id, userID, true)
	case domain.CodingPhaseAwaitingUser:
		return s.Stop(ctx, workspaceKey, id, userID, false)
	}
	return session, ErrConflict
}

// FailSession ends a session with an error code (used by metering when a
// spend limit is reached mid-turn, and by the runtime for runner errors).
func (s *Service) FailSession(ctx context.Context, workspaceKey, id string, code domain.CodingErrorCode, message string) (domain.CodingSession, error) {
	session, err := s.store.CodingSession(ctx, workspaceKey, id)
	if err != nil {
		return session, err
	}
	for attempt := 0; ; attempt++ {
		if session.Phase == domain.CodingPhaseDone {
			return session, nil
		}
		sandboxes, err := s.store.CodingSandboxes(ctx, workspaceKey, id)
		if err != nil {
			return session, err
		}
		decision := Decision{Session: cloneSession(session)}
		s.step.Fail(&decision, sandboxes, code, message, s.now().UTC())
		s.runSandboxActions(ctx, decision)
		saved, err := s.commit(ctx, session, decision.Session)
		if !errors.Is(err, ErrVersionConflict) || attempt >= 4 {
			return saved, err
		}
		if session, err = s.store.CodingSession(ctx, workspaceKey, id); err != nil {
			return session, err
		}
	}
}

// SwitchModel records an Auto fallback made by the LLM proxy (§12).
func (s *Service) SwitchModel(ctx context.Context, workspaceKey, id, model string) (domain.CodingSession, error) {
	session, err := s.update(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
		if current.ModelSelection != domain.CodingModelAuto && current.ModelSelection != "" {
			return ErrConflict
		}
		if current.Model == model {
			return ErrNoChange
		}
		current.Model = model
		return nil
	})
	if errors.Is(err, ErrNoChange) {
		return s.store.CodingSession(ctx, workspaceKey, id)
	}
	if err == nil {
		s.anchorActivity(ctx, session, domain.AgentActivity{Type: "thought", Body: "Switched to " + model})
	}
	return session, err
}

// CreatePullRequest opens the draft PR/MR now for a session whose agent
// pushed but has none (the UI's "Create PR").
func (s *Service) CreatePullRequest(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error) {
	session, err := s.store.CodingSession(ctx, workspaceKey, id)
	if err != nil {
		return session, err
	}
	if session.Repository == "" || session.BranchName == "" || session.HeadSHA == "" && !session.ExistingBranch && len(session.CommitsByTurnActivityID) == 0 {
		if commits, _ := s.store.CodingCommits(ctx, id); len(commits) == 0 {
			return session, ErrConflict
		}
	}
	switch session.Phase {
	case domain.CodingPhaseIdle, domain.CodingPhaseDone, domain.CodingPhaseAwaitingUser:
	default:
		return session, ErrConflict
	}
	outcome := s.publish(ctx, session)
	if outcome.Code != "" {
		return session, fmt.Errorf("%w: %s (%s)", ErrConflict, outcome.Message, outcome.Code)
	}
	updated, err := s.update(ctx, workspaceKey, id, func(current *domain.CodingSession) error {
		if outcome.PullRequest != nil {
			mergePullRequest(current, *outcome.PullRequest)
		}
		if current.ErrorCode == domain.CodingErrPullRequestFailed || current.ErrorCode == domain.CodingErrPushFailed {
			current.ErrorCode, current.Error = "", ""
			current.State = TerminalState("", current.StopRequested)
		}
		return nil
	})
	return s.enrich(ctx, updated), err
}

// runSandboxActions performs the sandbox side effects of a decision (they are
// idempotent and run before the session change is written).
func (s *Service) runSandboxActions(ctx context.Context, decision Decision) {
	for _, action := range decision.Actions {
		switch action.Kind {
		case ActStopSandbox:
			if err := s.store.SetCodingSandboxDesired(ctx, action.SandboxID, sandbox.DesiredStopped); err != nil && !errors.Is(err, ErrNotFound) {
				slog.Warn("coding sandbox stop failed", "sandbox", action.SandboxID, "error", err)
			}
		case ActRevokeTokens:
			if err := s.store.RevokeSandboxTokens(ctx, action.SandboxID); err != nil {
				slog.Warn("coding sandbox token revoke failed", "sandbox", action.SandboxID, "error", err)
			}
		case ActEnqueueStop:
			if _, err := s.store.EnqueueCodingInbox(ctx, decision.Session.WorkspaceKey, decision.Session.ID, InboxItem{Kind: InboxStop, Data: map[string]any{"sandboxId": decision.Session.SandboxID}}); err != nil {
				slog.Warn("coding stop enqueue failed", "session", decision.Session.ID, "error", err)
			}
			s.inbox.notify(decision.Session.ID)
		}
	}
}

// notifier wakes long-polls waiting on one session's inbox.
type notifier struct {
	mu      sync.Mutex
	waiters map[string]chan struct{}
}

func newNotifier() *notifier { return &notifier{waiters: map[string]chan struct{}{}} }

func (n *notifier) wait(id string) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch, ok := n.waiters[id]
	if !ok {
		ch = make(chan struct{})
		n.waiters[id] = ch
	}
	return ch
}

func (n *notifier) notify(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ch, ok := n.waiters[id]; ok {
		close(ch)
		delete(n.waiters, id)
	}
}

// revokeHostCredentials revokes the code host credentials minted for a
// session that ended (GitLab project tokens); hosts without them are skipped.
func (s *Service) revokeHostCredentials(ctx context.Context, session domain.CodingSession) {
	revoker, ok := s.hosts[session.CodeHost].(SessionCredentialRevoker)
	if !ok {
		return
	}
	if err := revoker.RevokeSessionCredentials(ctx, session.WorkspaceKey, session.ID); err != nil {
		// GC sweeps leftover tokens (design §14); the session still ends.
		slog.Warn("coding session credential revoke failed", "session", session.ID, "error", err)
	}
}
