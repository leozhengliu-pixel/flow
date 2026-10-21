package coding

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
	"flow/api/internal/sandbox"
)

// The runner-facing runtime (design §7.2, /internal/coding-runtime/*). Every
// call is authenticated with the sandbox token and checked against the
// session the sandbox currently serves: a sandbox from an earlier start
// attempt, or of a finished session, gets ErrGone.

// MaxEventsPerRequest bounds POST /events.
const MaxEventsPerRequest = 100

// HeartbeatInterval is how often the runner heartbeats.
const HeartbeatInterval = 15 * time.Second

// Authenticate resolves a sandbox token.
func (s *Service) Authenticate(ctx context.Context, token string) (SandboxGrant, error) {
	if !strings.HasPrefix(token, SandboxTokenPrefix) || len(token) > 512 {
		return SandboxGrant{}, ErrTokenInvalid
	}
	grant, err := s.store.AuthenticateSandboxToken(ctx, token)
	if err != nil {
		return SandboxGrant{}, err
	}
	if !grant.ExpiresAt.IsZero() && !s.now().Before(grant.ExpiresAt) {
		return SandboxGrant{}, ErrTokenInvalid
	}
	return grant, nil
}

// sessionFor returns the live session a sandbox serves.
func (s *Service) sessionFor(ctx context.Context, grant SandboxGrant) (domain.CodingSession, error) {
	if grant.SessionID == "" {
		if grant.Kind == sandbox.KindWarm {
			return domain.CodingSession{}, ErrNotBound
		}
		return domain.CodingSession{}, ErrForbidden
	}
	session, err := s.store.CodingSession(ctx, grant.WorkspaceKey, grant.SessionID)
	if err != nil {
		return session, err
	}
	if session.Phase == domain.CodingPhaseDone || session.SandboxID != grant.SandboxID {
		return session, ErrGone
	}
	return session, nil
}

// Bootstrap answers GET /bootstrap. A warm sandbox long-polls until it is
// bound (ErrNotBound when wait elapses). The first call of a session sandbox
// moves starting → setup (or → running when a snapshot was restored) and
// delivers the first pending turn; repeated calls return the same turn.
func (s *Service) Bootstrap(ctx context.Context, token string, wait time.Duration) (BootstrapResponse, error) {
	deadline := s.now().Add(min(wait, 60*time.Second))
	for {
		grant, err := s.Authenticate(ctx, token)
		if err != nil {
			return BootstrapResponse{}, err
		}
		if grant.Kind == sandbox.KindSnapshot || grant.Kind == sandbox.KindDebug {
			if s.SnapshotBootstrap != nil {
				return s.SnapshotBootstrap(ctx, grant)
			}
			return BootstrapResponse{}, ErrForbidden
		}
		session, err := s.sessionFor(ctx, grant)
		pendingBind := errors.Is(err, ErrNotBound) || errors.Is(err, ErrGone) && (session.Phase == domain.CodingPhaseProvisioning || session.Phase == domain.CodingPhaseQueued)
		if err == nil {
			return s.bootstrapSession(ctx, grant, session)
		}
		if !pendingBind {
			return BootstrapResponse{}, err
		}
		if !s.now().Before(deadline) {
			return BootstrapResponse{}, ErrNotBound
		}
		select {
		case <-ctx.Done():
			return BootstrapResponse{}, ErrNotBound
		case <-time.After(time.Second):
		}
	}
}

func (s *Service) bootstrapSession(ctx context.Context, grant SandboxGrant, session domain.CodingSession) (BootstrapResponse, error) {
	turn, err := s.currentOrNextTurn(ctx, session)
	if err != nil {
		return BootstrapResponse{}, err
	}
	if session.Phase == domain.CodingPhaseStarting {
		restored := session.SnapshotID != ""
		session, err = s.update(ctx, session.WorkspaceKey, session.ID, func(current *domain.CodingSession) error {
			if current.SandboxID != grant.SandboxID {
				return ErrGone
			}
			if current.Phase != domain.CodingPhaseStarting {
				return nil // a concurrent bootstrap already moved it
			}
			now := s.now().UTC()
			current.SandboxAttempts = 0
			if turn != nil {
				current.CurrentTurnID = turn.ID
			}
			if restored {
				s.step.Enter(current, domain.CodingPhaseRunning, now)
			} else {
				s.step.Enter(current, domain.CodingPhaseSetup, now)
			}
			return nil
		})
		if err != nil {
			return BootstrapResponse{}, err
		}
	}
	return s.buildBootstrap(ctx, grant, session, turn)
}

// currentOrNextTurn returns the turn the runner should run: the current
// one, or the oldest undelivered turn (which becomes current).
func (s *Service) currentOrNextTurn(ctx context.Context, session domain.CodingSession) (*Turn, error) {
	items, err := s.store.CodingInbox(ctx, session.ID, 0)
	if err != nil {
		return nil, err
	}
	if session.CurrentTurnID != "" {
		for _, item := range items {
			if item.Kind == InboxTurn && item.Turn != nil && item.Turn.ID == session.CurrentTurnID {
				turn := *item.Turn
				return &turn, nil
			}
		}
	}
	pending, err := s.store.UndeliveredCodingInbox(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range pending {
		if item.Kind == InboxTurn && item.Turn != nil {
			if err := s.store.MarkCodingInboxDelivered(ctx, session.ID, item.Seq, s.now().UTC()); err != nil {
				return nil, err
			}
			turn := *item.Turn
			return &turn, nil
		}
	}
	return nil, nil
}

// SetupSecrets answers GET /setup-secrets: once per sandbox, in setup only.
func (s *Service) SetupSecrets(ctx context.Context, token string) (SetupSecretsResponse, error) {
	grant, err := s.Authenticate(ctx, token)
	if err != nil {
		return SetupSecretsResponse{}, err
	}
	workspaceKey, environmentID := grant.WorkspaceKey, grant.EnvironmentID
	switch {
	case grant.Kind == sandbox.KindSnapshot:
		// A snapshot build runs the prepare script once with the secrets
		// (track K); debug sandboxes never receive them.
		if environmentID == "" || s.envs == nil {
			return SetupSecretsResponse{}, ErrForbidden
		}
	case grant.SessionID == "":
		return SetupSecretsResponse{}, ErrForbidden
	default:
		session, err := s.sessionFor(ctx, grant)
		if err != nil {
			return SetupSecretsResponse{}, err
		}
		if session.Phase != domain.CodingPhaseSetup || session.EnvironmentID == "" || s.envs == nil {
			return SetupSecretsResponse{}, ErrForbidden
		}
		workspaceKey, environmentID = session.WorkspaceKey, session.EnvironmentID
	}
	first, err := s.store.MarkSetupSecretsUsed(ctx, token)
	if err != nil {
		return SetupSecretsResponse{}, err
	}
	if !first {
		return SetupSecretsResponse{}, ErrAlreadyUsed
	}
	values, err := s.envs.CodingEnvironmentSecretValues(ctx, workspaceKey, environmentID)
	if err != nil {
		return SetupSecretsResponse{}, err
	}
	if values == nil {
		values = []domain.CodingEnvVar{}
	}
	return SetupSecretsResponse{Variables: values}, nil
}

// Heartbeat answers POST /heartbeat: it records liveness, finishes setup
// when the runner reports it is running, and tells the runner to stop.
func (s *Service) Heartbeat(ctx context.Context, token string, request HeartbeatRequest) (HeartbeatResponse, error) {
	grant, err := s.Authenticate(ctx, token)
	if err != nil {
		return HeartbeatResponse{}, err
	}
	if err := s.store.TouchCodingSandboxHeartbeat(ctx, grant.SandboxID, s.now().UTC()); err != nil {
		return HeartbeatResponse{}, err
	}
	if grant.SessionID == "" {
		// Snapshot, debug and warm sandboxes: stop when Flow no longer wants
		// them (a canceled snapshot build, a closed terminal, a recycled warm sandbox).
		record, err := s.store.CodingSandbox(ctx, grant.SandboxID)
		return HeartbeatResponse{Stop: err == nil && record.Desired == sandbox.DesiredStopped}, nil
	}
	session, err := s.sessionFor(ctx, grant)
	if errors.Is(err, ErrGone) {
		return HeartbeatResponse{Stop: true}, nil
	}
	if err != nil {
		return HeartbeatResponse{}, err
	}
	if request.Phase == "running" && session.Phase == domain.CodingPhaseSetup {
		if session, err = s.finishSetup(ctx, session); err != nil {
			return HeartbeatResponse{}, err
		}
	}
	stop := session.StopRequested || session.Phase == domain.CodingPhaseStopping || session.Phase == domain.CodingPhaseDone
	if record, err := s.store.CodingSandbox(ctx, grant.SandboxID); err == nil && record.Desired == sandbox.DesiredStopped {
		stop = true
	}
	return HeartbeatResponse{Stop: stop}, nil
}

func (s *Service) finishSetup(ctx context.Context, session domain.CodingSession) (domain.CodingSession, error) {
	return s.update(ctx, session.WorkspaceKey, session.ID, func(current *domain.CodingSession) error {
		if current.Phase != domain.CodingPhaseSetup {
			return nil
		}
		s.step.Enter(current, domain.CodingPhaseRunning, s.now().UTC())
		return nil
	})
}

// Inbox answers GET /inbox?after=&wait=: items after the runner's cursor.
// While a turn is open, the next turn is held back (turns run one at a
// time, FIFO); when the runner is idle (or waiting for an answer) the next
// turn is delivered and the session moves to running.
func (s *Service) Inbox(ctx context.Context, token string, after int64, wait time.Duration) (InboxResponse, error) {
	deadline := s.now().Add(min(wait, 30*time.Second))
	for {
		grant, err := s.Authenticate(ctx, token)
		if err != nil {
			return InboxResponse{}, err
		}
		changed := s.inbox.wait(grant.SessionID)
		session, err := s.sessionFor(ctx, grant)
		if errors.Is(err, ErrGone) {
			// A finished session's runner only needs to hear stop.
			return InboxResponse{Items: []InboxItem{{Seq: after + 1, Kind: InboxStop}}}, nil
		}
		if err != nil {
			return InboxResponse{}, err
		}
		items, err := s.inboxItems(ctx, session, after)
		if err != nil || len(items) > 0 || !s.now().Before(deadline) {
			if items == nil {
				items = []InboxItem{}
			}
			return InboxResponse{Items: items}, err
		}
		poll := time.NewTimer(time.Second) // other replicas enqueue without notifying this one
		select {
		case <-ctx.Done():
			poll.Stop()
			return InboxResponse{Items: []InboxItem{}}, nil
		case <-changed:
		case <-poll.C:
		}
		poll.Stop()
	}
}

func (s *Service) inboxItems(ctx context.Context, session domain.CodingSession, after int64) ([]InboxItem, error) {
	all, err := s.store.CodingInbox(ctx, session.ID, after)
	if err != nil {
		return nil, err
	}
	acceptTurn := session.CurrentTurnID == "" && (session.Phase == domain.CodingPhaseIdle || session.Phase == domain.CodingPhaseAwaitingUser || session.Phase == domain.CodingPhaseRunning) ||
		session.Phase == domain.CodingPhaseAwaitingUser
	undelivered := map[int64]bool{}
	if pending, err := s.store.UndeliveredCodingInbox(ctx, session.ID); err != nil {
		return nil, err
	} else {
		for _, item := range pending {
			undelivered[item.Seq] = true
		}
	}
	items := []InboxItem{}
	for _, item := range all {
		if item.Kind != InboxTurn {
			// A stop addressed to an earlier sandbox of the session is not for this runner.
			if target, _ := item.Data["sandboxId"].(string); item.Kind == InboxStop && target != "" && target != session.SandboxID {
				continue
			}
			items = append(items, item)
			continue
		}
		if !undelivered[item.Seq] {
			continue // delivered earlier (with the bootstrap or a previous poll)
		}
		if item.Turn == nil || !acceptTurn || session.StopRequested {
			break
		}
		if err := s.store.MarkCodingInboxDelivered(ctx, session.ID, item.Seq, s.now().UTC()); err != nil {
			return nil, err
		}
		turnID := item.Turn.ID
		if _, err := s.update(ctx, session.WorkspaceKey, session.ID, func(current *domain.CodingSession) error {
			if current.SandboxID != session.SandboxID || current.StopRequested {
				return ErrGone
			}
			now := s.now().UTC()
			if current.CurrentTurnID == "" {
				current.CurrentTurnID = turnID
			}
			if current.Phase == domain.CodingPhaseIdle || current.Phase == domain.CodingPhaseAwaitingUser {
				s.step.Enter(current, domain.CodingPhaseRunning, now)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		items = append(items, item)
		break // one turn at a time
	}
	return items, nil
}

// TurnStatus values of TurnCompleteRequest.
const (
	TurnSuccess       = "success"
	TurnError         = "error"
	TurnStopped       = "stopped"
	TurnAwaitingInput = "awaitingInput"
)

// CompleteTurn answers POST /turns/{turnId}/complete. It is idempotent: a
// repeated call for the last completed turn returns the session unchanged.
func (s *Service) CompleteTurn(ctx context.Context, token, turnID string, request TurnCompleteRequest) (domain.CodingSession, error) {
	grant, err := s.Authenticate(ctx, token)
	if err != nil {
		return domain.CodingSession{}, err
	}
	session, err := s.store.CodingSession(ctx, grant.WorkspaceKey, grant.SessionID)
	if err != nil {
		return session, err
	}
	if session.SandboxID != grant.SandboxID && session.Phase != domain.CodingPhaseDone {
		return session, ErrGone
	}
	if turnID == session.LastTurnID && session.CurrentTurnID != turnID {
		return session, nil
	}
	if session.Phase == domain.CodingPhaseDone {
		// The session ended (timeout, stop) while the turn finished: keep its commits.
		return session, s.saveCommits(ctx, session, turnID, request)
	}
	if turnID != session.CurrentTurnID {
		return session, fmt.Errorf("%w: turn %s is not the session's current turn", ErrConflict, turnID)
	}
	switch request.Status {
	case TurnSuccess, TurnError, TurnStopped, TurnAwaitingInput:
	default:
		return session, ErrInvalidInput
	}
	if err := s.saveCommits(ctx, session, turnID, request); err != nil {
		return session, err
	}
	sandboxes, err := s.store.CodingSandboxes(ctx, session.WorkspaceKey, session.ID)
	if err != nil {
		return session, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		decision := s.completeDecision(session, sandboxes, turnID, request)
		s.runSandboxActions(ctx, decision)
		saved, err := s.commit(ctx, session, decision.Session)
		if !errors.Is(err, ErrVersionConflict) {
			return saved, err
		}
		if session, err = s.store.CodingSession(ctx, grant.WorkspaceKey, grant.SessionID); err != nil {
			return session, err
		}
		if session.CurrentTurnID != turnID {
			return session, nil
		}
	}
	return session, ErrVersionConflict
}

func (s *Service) saveCommits(ctx context.Context, session domain.CodingSession, turnID string, request TurnCompleteRequest) error {
	if len(request.Commits) == 0 {
		return nil
	}
	commits := make([]domain.CodingCommit, 0, len(request.Commits))
	for _, commit := range request.Commits {
		if commit.SHA == "" || len(commit.SHA) > 64 {
			continue
		}
		commit.TurnActivityID = turnID
		if commit.Branch == "" {
			commit.Branch = session.BranchName
		}
		if commit.CreatedAt.IsZero() {
			commit.CreatedAt = s.now().UTC()
		}
		commits = append(commits, commit)
	}
	return s.store.AddCodingCommits(ctx, session.ID, commits)
}

// completeDecision is the state change of a turn completion.
func (s *Service) completeDecision(session domain.CodingSession, sandboxes []sandbox.Record, turnID string, request TurnCompleteRequest) Decision {
	now := s.now().UTC()
	d := Decision{Session: cloneSession(session)}
	ns := &d.Session
	ns.LastTurnID, ns.CurrentTurnID = turnID, ""
	if request.HarnessSessionID != "" {
		ns.HarnessResumeID = request.HarnessSessionID
	}
	if summary := strings.TrimSpace(request.Summary); summary != "" {
		ns.Summary = summary
	}
	if title := strings.TrimSpace(request.DisplayTitle); title != "" {
		ns.DisplayTitle = firstLine(title, 120)
	}
	if len(request.Diff) > 0 {
		ns.Diffs = request.Diff
	}
	if request.HeadSHA != "" {
		ns.HeadSHA = request.HeadSHA
	}
	if request.Pushed {
		ns.Pushed = true
		ns.BranchPushed = true
		ns.ExistingBranch = true
	}
	switch {
	case ns.StopRequested || ns.Phase == domain.CodingPhaseStopping:
		// Step finishes the stop once the sandbox drained.
	case request.Status == TurnSuccess && ns.Pushed:
		s.step.Enter(ns, domain.CodingPhasePublishing, now)
	case request.Status == TurnSuccess:
		s.step.Enter(ns, domain.CodingPhaseIdle, now)
	case request.Status == TurnAwaitingInput:
		s.step.Enter(ns, domain.CodingPhaseAwaitingUser, now)
	case request.Status == TurnStopped:
		ns.StopRequested = true
		s.step.Enter(ns, domain.CodingPhaseStopping, now)
	default:
		code, message := domain.CodingErrHarnessFailed, "The agent failed."
		if request.Error != nil {
			if request.Error.Code != "" {
				code = request.Error.Code
			}
			if request.Error.Message != "" {
				message = request.Error.Message
			}
		}
		if ns.Phase == domain.CodingPhaseSetup && code == domain.CodingErrHarnessFailed {
			code = domain.CodingErrEnvironmentSetup
		}
		s.step.Fail(&d, sandboxes, code, message, now)
	}
	ns.NextReconcileAt = now
	return d
}

// credentialSubject is what a sandbox may get repository credentials for:
// its session, or for a snapshot build the environment's repository (the
// build clones it before any session exists). The returned session only
// carries the workspace, code host and repository fields.
func (s *Service) credentialSubject(ctx context.Context, grant SandboxGrant) (domain.CodingSession, error) {
	if grant.Kind != sandbox.KindSnapshot {
		return s.sessionFor(ctx, grant)
	}
	if grant.EnvironmentID == "" || s.envs == nil {
		return domain.CodingSession{}, ErrForbidden
	}
	environment, err := s.envs.CodingEnvironment(ctx, grant.WorkspaceKey, grant.EnvironmentID)
	if err != nil {
		return domain.CodingSession{}, err
	}
	codeHost := environment.CodeHost
	if codeHost == "" {
		codeHost = domain.CodeHostGitHub
	}
	return domain.CodingSession{WorkspaceKey: grant.WorkspaceKey, CodeHost: codeHost, HostName: environment.HostName, Repository: environment.Repository}, nil
}

// Credentials answers POST /credentials with a short-lived repository-scoped
// credential minted by the session's code host (git credential helper, gh/glab).
func (s *Service) Credentials(ctx context.Context, token string, request CredentialRequest) (CredentialResponse, error) {
	grant, err := s.Authenticate(ctx, token)
	if err != nil {
		return CredentialResponse{}, err
	}
	session, err := s.credentialSubject(ctx, grant)
	if err != nil {
		return CredentialResponse{}, err
	}
	if session.Repository == "" {
		return CredentialResponse{}, ErrForbidden
	}
	if request.Purpose != "" && request.Purpose != "git" && request.Purpose != "cli" {
		return CredentialResponse{}, ErrInvalidInput
	}
	if relayPath, relay := gitRelayCredentialPath(request.Path); relay {
		// The push URL through Flow's git relay (the runtime URL, which is
		// plain HTTP inside the cluster): only a placeholder, never a token.
		if request.Protocol != "" && request.Protocol != "https" && request.Protocol != "http" {
			return CredentialResponse{}, ErrForbidden
		}
		return s.relayCredential(ctx, token, grant, request, relayPath)
	}
	if request.Protocol != "" && request.Protocol != "https" {
		return CredentialResponse{}, ErrForbidden
	}
	if request.Host != "" && session.HostName != "" && !strings.EqualFold(request.Host, session.HostName) {
		return CredentialResponse{}, ErrForbidden
	}
	if path := strings.TrimSuffix(strings.Trim(request.Path, "/"), ".git"); path != "" && !strings.EqualFold(path, session.Repository) {
		return CredentialResponse{}, ErrForbidden
	}
	host, ok := s.hosts[session.CodeHost]
	if !ok {
		return CredentialResponse{}, codehost.ErrNotConnected
	}
	access := codehost.AccessWrite
	if grant.Kind == sandbox.KindSnapshot {
		access = codehost.AccessRead // a snapshot build only clones
	}
	// Straight to the host: always the workspace's repository-scoped
	// credential. A creator's own token never reaches the sandbox; their
	// pushes go through the git relay (git_relay.go), which adds it server
	// side. A done session never gets here (sessionFor returns ErrGone).
	credential, err := host.MintCredential(ctx, codehost.CredentialRequest{WorkspaceKey: session.WorkspaceKey, SessionID: session.ID, SandboxID: grant.SandboxID, Repo: codehost.RepoRef{CodeHost: session.CodeHost, HostName: session.HostName, FullName: session.Repository}, Access: access})
	if err != nil {
		return CredentialResponse{}, err
	}
	response := CredentialResponse{Username: credential.Username, Password: credential.Password, ExpiresAt: credential.ExpiresAt}
	if request.Purpose == "cli" {
		response.CLIEnv = credential.CLIEnv
	}
	return response, nil
}

// Sign signs a commit payload with the session creator's key through the
// Signer (track E). Track E's /sign route calls it.
func (s *Service) Sign(ctx context.Context, token string, request SignRequest) (SignResponse, error) {
	grant, err := s.Authenticate(ctx, token)
	if err != nil {
		return SignResponse{}, err
	}
	session, err := s.sessionFor(ctx, grant)
	if err != nil {
		return SignResponse{}, err
	}
	if s.signer == nil {
		return SignResponse{}, fmt.Errorf("%w: commit signing is not available", ErrForbidden)
	}
	if request.Format != "ssh" && request.Format != "openpgp" || len(request.Payload) == 0 || len(request.Payload) > 16<<20 {
		return SignResponse{}, ErrInvalidInput
	}
	signature, err := s.signer.SignCommit(ctx, session.WorkspaceKey, session.CreatorID, request.Format, request.Payload)
	if err != nil {
		return SignResponse{}, err
	}
	return SignResponse{Signature: signature}, nil
}

// HarnessStateKey is the object key of a session's saved harness state.
func (s *Service) HarnessStateKey(ctx context.Context, token string) (string, error) {
	grant, err := s.Authenticate(ctx, token)
	if err != nil {
		return "", err
	}
	if _, err := s.sessionFor(ctx, grant); err != nil {
		return "", err
	}
	// A flat key, like coding artifacts': the local object store driver
	// refuses keys with directories.
	return "coding-harness_" + grant.WorkspaceKey + "_" + grant.SessionID + ".tar.zst", nil
}

// AuthenticateLLMCaller implements llmproxy.Authenticator: the token must
// belong to the running sandbox of a live session.
func (s *Service) AuthenticateLLMCaller(ctx context.Context, token string) (llmproxy.Caller, error) {
	grant, err := s.Authenticate(ctx, token)
	if err != nil {
		return llmproxy.Caller{}, err
	}
	session, err := s.sessionFor(ctx, grant)
	if err != nil {
		return llmproxy.Caller{}, ErrTokenInvalid
	}
	switch session.Phase {
	case domain.CodingPhaseSetup, domain.CodingPhaseRunning, domain.CodingPhaseAwaitingUser, domain.CodingPhasePublishing, domain.CodingPhaseIdle, domain.CodingPhaseStopping:
	default:
		return llmproxy.Caller{}, ErrTokenInvalid
	}
	return llmproxy.Caller{WorkspaceKey: session.WorkspaceKey, SessionID: session.ID, SandboxID: grant.SandboxID, UserID: session.CreatorID, LoopID: session.LoopID, Harness: session.Harness, Model: session.Model}, nil
}

var _ llmproxy.Authenticator = (*Service)(nil)
