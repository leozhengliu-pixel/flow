package coding

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Trigger glue (docs/coding-sessions-design.md §15, track L). Every trigger
// (delegation, agent chat, comment mention, Loop action, PR/MR follow-up,
// "Fix this PR") goes through Triggers, which classifies intent, picks the
// environment, applies the Loop repository policy and spend limit, gates
// untrusted external sources behind member approval, and then hands a
// TriggerStartRequest to the session service (SessionStarter).

// TriggerError is a trigger failure with a stable session error code.
type TriggerError struct {
	Code    domain.CodingErrorCode `json:"code"`
	Message string                 `json:"message"`
}

func (e *TriggerError) Error() string {
	if e.Message == "" {
		return "coding trigger: " + string(e.Code)
	}
	return "coding trigger: " + string(e.Code) + ": " + e.Message
}

// TriggerErrorCode extracts the code of a TriggerError ("" otherwise).
func TriggerErrorCode(err error) domain.CodingErrorCode {
	var triggerError *TriggerError
	if errors.As(err, &triggerError) {
		return triggerError.Code
	}
	return ""
}

// NotificationError is a notification (an elicitation or a decision on the
// session's anchor, an Inbox notice) that failed after the action it reports
// was committed. The trigger's result still describes that action: callers
// answer with it and log the failure. Every notification is keyed, so
// repeating the trigger or the decision delivers it once.
type NotificationError struct {
	Step string
	Err  error
}

func (e *NotificationError) Error() string {
	return "coding trigger: " + e.Step + " notification: " + e.Err.Error()
}

func (e *NotificationError) Unwrap() error { return e.Err }

// NotificationFailed reports whether err only says a notification failed
// after the action committed.
func NotificationFailed(err error) bool {
	var notificationError *NotificationError
	return errors.As(err, &notificationError)
}

// Turn kinds used by triggers (Turn.Kind).
const (
	TurnKindInitial   = "initial"
	TurnKindFollowUp  = "followUp"
	TurnKindPRComment = "prComment"
	TurnKindFixPR     = "fixPr"
	TurnKindRebase    = "rebase"
	TurnKindLint      = "lint"
	TurnKindTest      = "test"
)

// Approval states (CodingSession.ApprovalState).
const (
	ApprovalRequired = "required"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
)

// TriggerStartRequest is what a trigger asks the session service to start.
// It carries the design's StartInput fields (Source, SourceID, IssueID,
// Prompt, EnvironmentID, Harness, Model, CreatorID, LoopID) plus what the
// trigger decided about the initial phase.
type TriggerStartRequest struct {
	WorkspaceKey  string
	Source        string
	SourceID      string
	IssueID       string
	TeamID        string
	Title         string
	Prompt        string
	TurnKind      string
	EnvironmentID string
	Harness       string
	Model         string
	CreatorID     string
	LoopID        string
	// AnchorTaskID reuses an existing AgentTask (a delegation or mention
	// task) as the session anchor; empty creates one.
	AnchorTaskID string
	// AnchorAppUserID is the built-in Flow agent's app user (anchor actor).
	AnchorAppUserID string
	// AnchorResourceType is "issue", "agentChat", "pullRequest" or "loop".
	AnchorResourceType string
	AnchorResourceID   string
	// Branch fields: PR follow-ups work on the PR head (ExistingBranch).
	BranchName     string
	BaseRef        string
	ExistingBranch bool
	CodeHost       string
	Repository     string
	PullRequest    *domain.SessionPullRequest
	// RequireApproval starts in awaitingApproval (untrusted external source).
	RequireApproval bool
	ExternalSource  string
	// AwaitEnvironment starts in awaitingUser with a select elicitation
	// listing EnvironmentSuggestions (ambiguous repository).
	AwaitEnvironment       bool
	EnvironmentSuggestions []EnvironmentSuggestion
	IdempotencyKey         string
}

// InitialPhase is the phase a started session begins in.
func (r TriggerStartRequest) InitialPhase() domain.CodingSessionPhase {
	switch {
	case r.RequireApproval:
		return domain.CodingPhaseAwaitingApproval
	case r.AwaitEnvironment:
		return domain.CodingPhaseAwaitingUser
	default:
		return domain.CodingPhaseQueued
	}
}

// SessionStarter is the session service as triggers use it (track B's
// coding.Service implements it through a thin adapter; StoreStarter is the
// store-backed implementation).
type SessionStarter interface {
	// StartCodingSession creates the session (idempotent by
	// IdempotencyKey); created is false when it already existed.
	StartCodingSession(ctx context.Context, request TriggerStartRequest) (session domain.CodingSession, created bool, err error)
	// PromptCodingSession appends a follow-up turn and wakes the session.
	PromptCodingSession(ctx context.Context, workspaceKey, sessionID string, turn Turn) (domain.CodingSession, error)
}

// TriggerSessions is the part of SessionStore triggers read and update.
type TriggerSessions interface {
	CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)
	ListCodingSessions(ctx context.Context, workspaceKey string, filter SessionFilter) ([]domain.CodingSession, error)
	UpdateCodingSession(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error)
}

// TriggerEnvironments lists environments (EnvironmentStore subset).
type TriggerEnvironments interface {
	ListCodingEnvironments(ctx context.Context, workspaceKey string, includeArchived bool) ([]domain.CodingEnvironment, error)
}

// TriggerNotifier tells people about sessions that wait for them. Every
// method must be idempotent: triggers may call it again after a retry.
type TriggerNotifier interface {
	// ApprovalRequested notifies the team's members that an external source
	// wants to start a session and posts the approve/reject elicitation.
	ApprovalRequested(ctx context.Context, session domain.CodingSession, request TriggerStartRequest) error
	// EnvironmentSelectionRequested posts the repository select elicitation.
	EnvironmentSelectionRequested(ctx context.Context, session domain.CodingSession, suggestions []EnvironmentSuggestion) error
	// ApprovalResolved records the decision (approved, rejected, expired).
	ApprovalResolved(ctx context.Context, session domain.CodingSession, decision, userID string) error
}

// NopNotifier ignores every notification.
type NopNotifier struct{}

func (NopNotifier) ApprovalRequested(context.Context, domain.CodingSession, TriggerStartRequest) error {
	return nil
}
func (NopNotifier) EnvironmentSelectionRequested(context.Context, domain.CodingSession, []EnvironmentSuggestion) error {
	return nil
}
func (NopNotifier) ApprovalResolved(context.Context, domain.CodingSession, string, string) error {
	return nil
}

// Triggers is the trigger service.
type Triggers struct {
	Starter      SessionStarter
	Sessions     TriggerSessions
	Environments TriggerEnvironments
	Spend        SpendReader
	Notifier     TriggerNotifier
	Intent       IntentModel
	Config       config.CodingConfig
	Now          func() time.Time
	// Approver applies an approval decision through the session state
	// machine (Service.Approve); nil updates the session row directly
	// (trigger tests without a Service).
	Approver func(ctx context.Context, workspaceKey, sessionID, userID string, reject bool) (domain.CodingSession, error)
}

func (t *Triggers) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Triggers) notifier() TriggerNotifier {
	if t.Notifier == nil {
		return NopNotifier{}
	}
	return t.Notifier
}

// IssueTrigger starts or continues a session for an issue, a chat, a
// comment or a Loop run.
type IssueTrigger struct {
	WorkspaceKey string
	Settings     domain.CodingAgentSettings
	SpendLimits  domain.SpendLimitSettings
	// Source is a domain.CodingSource* value.
	Source   string
	SourceID string
	// Issue is nil for an agent chat without an issue.
	Issue *IssueContext
	// Request is the user's words, used for intent classification and as
	// the prompt when Prompt is empty.
	Request string
	// Prompt is the full task prompt for the harness.
	Prompt string
	// CheckIntent applies the reference rule (chat and comment triggers).
	CheckIntent bool
	// RecentEnvironmentIDs are the team's recently used environments.
	RecentEnvironmentIDs []string
	EnvironmentID        string
	Harness              string
	Model                string
	CreatorID            string
	LoopID               string
	AnchorTaskID         string
	AnchorAppUserID      string
	AnchorResourceType   string
	AnchorResourceID     string
	IdempotencyKey       string
	// ContextText adds chat context (earlier messages) to the repository
	// ranking text.
	ContextText string
}

// TriggerOutcome says what a trigger did.
type TriggerOutcome string

const (
	OutcomeStarted           TriggerOutcome = "started"
	OutcomeContinued         TriggerOutcome = "continued"
	OutcomeEnvironmentChosen TriggerOutcome = "environmentChosen"
	OutcomeNotStarted        TriggerOutcome = "notStarted"
)

// TriggerResult is the outcome of a trigger.
type TriggerResult struct {
	Outcome  TriggerOutcome        `json:"outcome"`
	Session  *domain.CodingSession `json:"session,omitempty"`
	Created  bool                  `json:"created"`
	Intent   *IntentDecision       `json:"intent,omitempty"`
	Pick     *EnvironmentPick      `json:"environment,omitempty"`
	Approval bool                  `json:"approvalRequired"`
	Message  string                `json:"message,omitempty"`
	// NotificationPending is set when the session was started but its
	// approval or repository request could not be posted yet; repeating
	// the trigger posts it.
	NotificationPending bool `json:"notificationPending,omitempty"`
}

// activeSessionStates are the states of sessions that can take a follow-up
// without a new session (done sessions are continued too; see Continue).
var activeSessionStates = []domain.CodingSessionState{
	domain.CodingSessionPending, domain.CodingSessionActive, domain.CodingSessionAwaitingInput, domain.CodingSessionComplete,
}

// StartForIssue runs a trigger for an issue (or an issue-less chat).
func (t *Triggers) StartForIssue(ctx context.Context, trigger IssueTrigger) (TriggerResult, error) {
	if t.Starter == nil || t.Environments == nil {
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	request := strings.TrimSpace(trigger.Request)
	prompt := strings.TrimSpace(trigger.Prompt)
	if prompt == "" {
		prompt = request
	}
	if trigger.CheckIntent {
		decision := ClassifyCodingIntent(ctx, request, t.Intent)
		if !decision.StartsCoding() {
			return TriggerResult{Outcome: OutcomeNotStarted, Intent: &decision, Message: NoCodeGuidance(decision)}, nil
		}
	}
	if prompt == "" && trigger.Issue != nil {
		prompt = IssuePrompt(*trigger.Issue, "")
	}
	if prompt == "" {
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrInternal, Message: "A prompt is required."}
	}

	// An active session on the same issue takes the request as a follow-up
	// (or as the environment choice it is waiting for).
	if trigger.Issue != nil && trigger.Issue.ID != "" && t.Sessions != nil {
		if result, handled, err := t.continueIssueSession(ctx, trigger, prompt); handled || err != nil {
			return result, err
		}
	}

	environments, err := t.Environments.ListCodingEnvironments(ctx, trigger.WorkspaceKey, false)
	if err != nil {
		return TriggerResult{}, fmt.Errorf("coding trigger: environments: %w", err)
	}
	if trigger.Source == domain.CodingSourceLoop {
		if trigger.EnvironmentID != "" && !LoopEnvironmentAllowed(trigger.Settings, trigger.EnvironmentID) {
			return TriggerResult{}, &TriggerError{Code: domain.CodingErrRepoNotInPolicy, Message: "This repository is not open to Loops (Settings › Coding sessions › Repository access for Loops)."}
		}
		environments = LoopEnvironments(trigger.Settings, environments)
		if len(activeEnvironments(environments)) == 0 {
			return TriggerResult{}, &TriggerError{Code: domain.CodingErrRepoNotInPolicy, Message: "No repository is open to Loops (Settings › Coding sessions › Repository access for Loops)."}
		}
		if err := CheckLoopSpend(ctx, t.Spend, trigger.WorkspaceKey, trigger.LoopID, trigger.SpendLimits, t.now()); err != nil {
			return TriggerResult{}, err
		}
	}

	input := EnvironmentSuggestionInput{
		Environments: environments, ExplicitEnvironmentID: trigger.EnvironmentID,
		Text: strings.Join([]string{request, trigger.ContextText}, "\n"), RecentEnvironmentIDs: trigger.RecentEnvironmentIDs,
	}
	if issue := trigger.Issue; issue != nil {
		input.Text = strings.Join([]string{issue.Title, issue.Description, input.Text}, "\n")
		input.LinkedRepositories = issue.LinkedRepositories
		input.Labels = issue.Labels
		input.TeamKey, input.TeamName = issue.TeamKey, issue.TeamName
	}
	pick := PickEnvironment(input)
	switch pick.ErrorCode {
	case domain.CodingErrNoPolicyRepos:
		return TriggerResult{Pick: &pick}, &TriggerError{Code: pick.ErrorCode, Message: "No coding environment exists yet. Create one in Settings › Coding sessions."}
	case domain.CodingErrRepoNotInPolicy:
		return TriggerResult{Pick: &pick}, &TriggerError{Code: pick.ErrorCode, Message: "That environment is archived or does not exist."}
	}
	if pick.Ambiguous && trigger.Source == domain.CodingSourceLoop {
		// A Loop runs unattended: nobody answers a select elicitation, so it
		// takes the best-ranked allowed repository when one has any signal.
		if len(pick.Suggestions) > 0 && pick.Suggestions[0].Score > 0 && (len(pick.Suggestions) == 1 || pick.Suggestions[0].Score > pick.Suggestions[1].Score) {
			pick = EnvironmentPick{EnvironmentID: pick.Suggestions[0].EnvironmentID, Reason: "ranked", Suggestions: pick.Suggestions}
		} else {
			return TriggerResult{Pick: &pick}, &TriggerError{Code: domain.CodingErrRepoNotInPolicy, Message: "The loop could not tell which repository to use; name it in the loop instructions or narrow Repository access for Loops."}
		}
	}

	start := TriggerStartRequest{
		WorkspaceKey: trigger.WorkspaceKey, Source: trigger.Source, SourceID: trigger.SourceID, Prompt: prompt,
		TurnKind: TurnKindInitial, EnvironmentID: pick.EnvironmentID, Harness: trigger.Harness, Model: trigger.Model,
		CreatorID: trigger.CreatorID, LoopID: trigger.LoopID, AnchorTaskID: trigger.AnchorTaskID,
		AnchorAppUserID: trigger.AnchorAppUserID, AnchorResourceType: trigger.AnchorResourceType,
		AnchorResourceID: trigger.AnchorResourceID, IdempotencyKey: trigger.IdempotencyKey,
		AwaitEnvironment: pick.Ambiguous, EnvironmentSuggestions: pick.Suggestions,
	}
	if issue := trigger.Issue; issue != nil {
		start.IssueID, start.TeamID, start.Title = issue.ID, issue.TeamID, issue.Title
		if issue.Origin.External && !IsTrustedSource(issue.Origin.Source, trigger.Settings.TrustedExternalSources) {
			start.RequireApproval = true
			start.ExternalSource = issue.Origin.Source
		}
	}
	if start.Title == "" {
		start.Title = firstLine(request, 80)
	}
	if start.AnchorResourceType == "" {
		start.AnchorResourceType = anchorResourceType(trigger.Source, trigger.Issue != nil)
	}
	return t.start(ctx, start, &pick)
}

func (t *Triggers) start(ctx context.Context, start TriggerStartRequest, pick *EnvironmentPick) (TriggerResult, error) {
	session, created, err := t.Starter.StartCodingSession(ctx, start)
	if errors.Is(err, domain.ErrCodingAnchorRevoked) {
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "The agent's access to this delegation was withdrawn; delegate the issue again to start a coding session."}
	}
	if err != nil {
		return TriggerResult{}, err
	}
	result := TriggerResult{Outcome: OutcomeStarted, Session: &session, Created: created, Pick: pick, Approval: start.RequireApproval}
	// Notifications are idempotent, so a retried trigger (same idempotency
	// key, created=false) re-sends nothing new but repairs a lost one. The
	// session exists either way: a failed notification is reported with it.
	switch session.Phase {
	case domain.CodingPhaseAwaitingApproval:
		result.Approval = true
		if err := t.notifier().ApprovalRequested(ctx, session, start); err != nil {
			result.NotificationPending = true
			return result, &NotificationError{Step: "approval", Err: err}
		}
	case domain.CodingPhaseAwaitingUser:
		if session.EnvironmentID == "" {
			if err := t.notifier().EnvironmentSelectionRequested(ctx, session, start.EnvironmentSuggestions); err != nil {
				result.NotificationPending = true
				return result, &NotificationError{Step: "environment elicitation", Err: err}
			}
		}
	}
	return result, nil
}

func (t *Triggers) continueIssueSession(ctx context.Context, trigger IssueTrigger, prompt string) (TriggerResult, bool, error) {
	sessions, err := t.Sessions.ListCodingSessions(ctx, trigger.WorkspaceKey, SessionFilter{IssueID: trigger.Issue.ID, States: activeSessionStates, Limit: 20})
	if err != nil {
		return TriggerResult{}, false, fmt.Errorf("coding trigger: sessions: %w", err)
	}
	session, ok := latestOpenSession(sessions)
	if !ok {
		return TriggerResult{}, false, nil
	}
	if session.Phase == domain.CodingPhaseAwaitingApproval {
		// A retried trigger repairs an approval request whose notification
		// failed (notifications are keyed, so a delivered one is not repeated).
		request := TriggerStartRequest{WorkspaceKey: trigger.WorkspaceKey, IssueID: trigger.Issue.ID, TeamID: trigger.Issue.TeamID, Title: trigger.Issue.Title, Prompt: prompt, ExternalSource: trigger.Issue.Origin.Source}
		if err := t.notifier().ApprovalRequested(ctx, session, request); err != nil {
			return TriggerResult{Outcome: OutcomeNotStarted, Session: &session, Approval: true, NotificationPending: true}, true, &NotificationError{Step: "approval", Err: err}
		}
	}
	if trigger.AnchorTaskID != "" && session.AgentSessionID == trigger.AnchorTaskID && trigger.EnvironmentID == "" {
		// The same anchor again (a retried delegation): nothing new to do.
		return TriggerResult{Outcome: OutcomeStarted, Session: &session}, true, nil
	}
	if session.Phase == domain.CodingPhaseAwaitingUser && session.EnvironmentID == "" {
		if trigger.EnvironmentID == "" {
			return TriggerResult{Outcome: OutcomeNotStarted, Session: &session, Message: "This session is waiting for a repository choice."}, true, nil
		}
		updated, err := t.SelectEnvironment(ctx, trigger.WorkspaceKey, session.ID, trigger.EnvironmentID, trigger.CreatorID, trigger.Settings, trigger.Source == domain.CodingSourceLoop)
		if err != nil {
			return TriggerResult{}, true, err
		}
		return TriggerResult{Outcome: OutcomeEnvironmentChosen, Session: &updated}, true, nil
	}
	if session.Phase == domain.CodingPhaseAwaitingApproval {
		return TriggerResult{Outcome: OutcomeNotStarted, Session: &session, Approval: true, Message: "This session is waiting for a member's approval."}, true, nil
	}
	if trigger.Source == domain.CodingSourceLoop && !LoopEnvironmentAllowed(trigger.Settings, session.EnvironmentID) {
		return TriggerResult{}, true, &TriggerError{Code: domain.CodingErrRepoNotInPolicy, Message: "The issue's coding session uses a repository that is not open to Loops."}
	}
	updated, err := t.Starter.PromptCodingSession(ctx, trigger.WorkspaceKey, session.ID, Turn{
		ID: turnID(trigger.WorkspaceKey, session.ID, trigger.IdempotencyKey, prompt), Prompt: prompt, AuthorID: trigger.CreatorID, Kind: TurnKindFollowUp,
	})
	if err != nil {
		return TriggerResult{}, true, err
	}
	return TriggerResult{Outcome: OutcomeContinued, Session: &updated}, true, nil
}

// latestOpenSession is the newest session that is not done.
func latestOpenSession(sessions []domain.CodingSession) (domain.CodingSession, bool) {
	var best domain.CodingSession
	found := false
	for _, session := range sessions {
		if session.Phase == domain.CodingPhaseDone {
			continue
		}
		if !found || session.CreatedAt.After(best.CreatedAt) {
			best, found = session, true
		}
	}
	return best, found
}

// SelectEnvironment answers the repository elicitation: the session leaves
// awaitingUser for queued with the chosen environment. Repeating the same
// choice is a no-op; a different choice after the session moved on fails.
func (t *Triggers) SelectEnvironment(ctx context.Context, workspaceKey, sessionID, environmentID, userID string, settings domain.CodingAgentSettings, loop bool) (domain.CodingSession, error) {
	if t.Sessions == nil || t.Environments == nil {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	environments, err := t.Environments.ListCodingEnvironments(ctx, workspaceKey, false)
	if err != nil {
		return domain.CodingSession{}, fmt.Errorf("coding trigger: environments: %w", err)
	}
	var chosen *domain.CodingEnvironment
	for index := range environments {
		if environments[index].ID == environmentID && environments[index].ArchivedAt == nil {
			chosen = &environments[index]
		}
	}
	if chosen == nil || (loop && !LoopEnvironmentAllowed(settings, environmentID)) {
		return domain.CodingSession{}, &TriggerError{Code: domain.CodingErrRepoNotInPolicy, Message: "That environment cannot be used."}
	}
	return t.Sessions.UpdateCodingSession(ctx, workspaceKey, sessionID, func(session *domain.CodingSession) error {
		if session.EnvironmentID == environmentID && session.Phase != domain.CodingPhaseAwaitingUser {
			return nil
		}
		if session.Phase != domain.CodingPhaseAwaitingUser || session.EnvironmentID != "" {
			return &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "This session is not waiting for a repository choice."}
		}
		session.EnvironmentID = chosen.ID
		session.CodeHost = chosen.CodeHost
		session.Repository = chosen.Repository
		session.Phase = domain.CodingPhaseQueued
		session.State = domain.CodingSessionPending
		session.UpdatedAt = t.now()
		return nil
	})
}

func anchorResourceType(source string, hasIssue bool) string {
	switch {
	case hasIssue:
		return "issue"
	case source == domain.CodingSourceAgentChat:
		return "agentChat"
	case source == domain.CodingSourcePullRequest:
		return "pullRequest"
	case source == domain.CodingSourceLoop:
		return "loop"
	default:
		return source
	}
}

// IssuePrompt is the initial task prompt for an issue.
func IssuePrompt(issue IssueContext, request string) string {
	var builder strings.Builder
	title := issue.Title
	if issue.Identifier != "" {
		title = issue.Identifier + ": " + issue.Title
	}
	builder.WriteString("Work on the issue " + strings.TrimSpace(title) + ".")
	if issue.URL != "" {
		builder.WriteString("\nIssue: " + issue.URL)
	}
	if description := strings.TrimSpace(issue.Description); description != "" {
		builder.WriteString("\n\nDescription:\n" + description)
	}
	if request = strings.TrimSpace(request); request != "" {
		builder.WriteString("\n\nRequest:\n" + request)
	}
	builder.WriteString("\n\nMake the change, add or update tests where it makes sense, and keep the diff focused. Flow opens the draft pull request for you.")
	return builder.String()
}

// turnID is a deterministic turn id, so a retried trigger enqueues the same
// turn (the inbox and activity stores dedupe on it).
func turnID(parts ...string) string {
	return "turn_" + shortHash(strings.Join(parts, "\x00"))
}

// newTriggerID returns a 26-character lowercase ULID (48-bit millisecond
// time, 80 random bits), the id format of coding rows.
func newTriggerID(now time.Time) string {
	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		panic(err)
	}
	var raw [16]byte
	ms := uint64(now.UnixMilli())
	for i := 5; i >= 0; i-- {
		raw[i] = byte(ms)
		ms >>= 8
	}
	copy(raw[6:], entropy[:])
	// 128 bits → 26 base32 characters (the first carries 3 bits).
	out := make([]byte, 26)
	var acc uint64
	bits := 0
	index := 25
	for i := 15; i >= 0; i-- {
		acc |= uint64(raw[i]) << bits
		bits += 8
		for bits >= 5 && index >= 0 {
			out[index] = crockford[acc&31]
			acc >>= 5
			bits -= 5
			index--
		}
	}
	if index >= 0 {
		out[index] = crockford[acc&31]
	}
	return string(out)
}

// ReviewSessions finds sessions and environments for a PR/MR
// (ChangeRequestStore and EnvironmentStore subsets).
type ReviewSessions interface {
	SessionsForChangeRequest(ctx context.Context, codeHost, hostName, repository string, number int) ([]domain.CodingSession, error)
	CodingEnvironmentForRepository(ctx context.Context, workspaceKey, codeHost, hostName, repository string) (domain.CodingEnvironment, error)
}

// Review quick actions (POST /api/reviews/{id}/coding-session).
const (
	ReviewActionFix      = "fix"
	ReviewActionFollowUp = "followUp"
	ReviewActionRebase   = "rebase"
	ReviewActionLint     = "lint"
	ReviewActionTest     = "test"
)

// ReviewActionTurnKind maps a review quick action to its turn kind.
func ReviewActionTurnKind(action string) (string, bool) {
	switch action {
	case ReviewActionFix:
		return TurnKindFixPR, true
	case ReviewActionFollowUp:
		return TurnKindFollowUp, true
	case ReviewActionRebase:
		return TurnKindRebase, true
	case ReviewActionLint:
		return TurnKindLint, true
	case ReviewActionTest:
		return TurnKindTest, true
	}
	return "", false
}

// ChangeRequestTrigger is "Fix this PR/MR" or a quick action on the review
// page.
type ChangeRequestTrigger struct {
	WorkspaceKey string
	Action       string
	Prompt       string
	// ConfirmForcePush allows a rebase on a branch Flow does not own.
	ConfirmForcePush bool
	ChangeRequest    codehost.ChangeRequest
	ReviewID         string
	IssueID          string
	TeamID           string
	EnvironmentID    string
	CreatorID        string
	AnchorAppUserID  string
	IdempotencyKey   string
}

// StartForChangeRequest continues the newest session linked to the PR/MR
// with the action's turn, or starts a pullRequest session on its head
// branch.
func (t *Triggers) StartForChangeRequest(ctx context.Context, reviews ReviewSessions, trigger ChangeRequestTrigger) (TriggerResult, error) {
	if t.Starter == nil || reviews == nil {
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	kind, ok := ReviewActionTurnKind(trigger.Action)
	if !ok {
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrInternal, Message: "Unknown action; use fix, followUp, rebase, lint or test."}
	}
	change := trigger.ChangeRequest
	if kind == TurnKindFollowUp && strings.TrimSpace(trigger.Prompt) == "" {
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrInternal, Message: "A follow-up needs a prompt."}
	}
	rebaseRefused := func(linked []domain.CodingSession) bool {
		return kind == TurnKindRebase && !trigger.ConfirmForcePush && !flowBranch(change.HeadBranch, firstNonEmpty(t.Config.BranchPrefix, "flow/"), linked)
	}
	if change.State == "merged" || change.State == "closed" {
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "This pull request is " + change.State + "."}
	}
	prompt := FollowUpPrompt(kind, &change, trigger.Prompt, nil)
	linked, err := reviews.SessionsForChangeRequest(ctx, change.Repo.CodeHost, change.Repo.HostName, change.Repo.FullName, change.Number)
	if err != nil {
		return TriggerResult{}, fmt.Errorf("coding trigger: linked sessions: %w", err)
	}
	linked = FilterSessions(linked, func(session domain.CodingSession) bool { return session.WorkspaceKey == trigger.WorkspaceKey })
	if rebaseRefused(linked) {
		// Flow's own branches: the deployment prefix, or a member branch of
		// a session linked to this PR/MR.
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrPermissionDenied, Message: "Rebasing force-pushes " + change.HeadBranch + ", which Flow did not create. Confirm the force push to continue."}
	}
	if target, ok := latestFollowUpTarget(linked); ok {
		updated, err := t.Starter.PromptCodingSession(ctx, trigger.WorkspaceKey, target.ID, Turn{
			ID: turnID(trigger.WorkspaceKey, target.ID, "review", trigger.IdempotencyKey, kind, prompt), Prompt: prompt, AuthorID: trigger.CreatorID, Kind: kind,
		})
		if err != nil {
			return TriggerResult{}, err
		}
		return TriggerResult{Outcome: OutcomeContinued, Session: &updated}, nil
	}
	if change.HeadRepository != "" && !strings.EqualFold(change.HeadRepository, change.Repo.FullName) {
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrNoCodeAccess, Message: "This pull request comes from a fork; Flow cannot push to it."}
	}
	environmentID := trigger.EnvironmentID
	environment, err := reviews.CodingEnvironmentForRepository(ctx, trigger.WorkspaceKey, change.Repo.CodeHost, change.Repo.HostName, change.Repo.FullName)
	switch {
	case err == nil && environmentID != "" && environment.ID != environmentID:
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrRepoNotInPolicy, Message: "That environment is for another repository."}
	case errors.Is(err, ErrNotFound):
		return TriggerResult{}, &TriggerError{Code: domain.CodingErrNoPolicyRepos, Message: "No coding environment exists for " + change.Repo.FullName + ". Create one in Settings › Coding sessions."}
	case err != nil:
		return TriggerResult{}, fmt.Errorf("coding trigger: environment: %w", err)
	}
	link := domain.SessionPullRequest{
		CodeHost: change.Repo.CodeHost, HostName: change.Repo.HostName, Repository: change.Repo.FullName, Number: change.Number,
		ExternalID: change.ExternalID, URL: change.URL, Title: change.Title, State: firstNonEmpty(change.State, "open"), Draft: change.Draft,
		HeadBranch: change.HeadBranch, BaseBranch: change.BaseBranch, HeadSHA: change.HeadSHA, ReviewID: trigger.ReviewID,
	}
	key := trigger.IdempotencyKey
	if key == "" {
		key = "review:" + trigger.ReviewID + ":" + kind + ":" + shortHash(prompt)
	}
	return t.start(ctx, TriggerStartRequest{
		WorkspaceKey: trigger.WorkspaceKey, Source: domain.CodingSourcePullRequest, SourceID: firstNonEmpty(trigger.ReviewID, change.ExternalID),
		IssueID: trigger.IssueID, TeamID: trigger.TeamID, Title: firstNonEmpty(change.Title, fmt.Sprintf("#%d", change.Number)), Prompt: prompt,
		TurnKind: kind, EnvironmentID: environment.ID, CreatorID: trigger.CreatorID, AnchorAppUserID: trigger.AnchorAppUserID,
		AnchorResourceType: "pullRequest", AnchorResourceID: firstNonEmpty(trigger.ReviewID, change.ExternalID),
		BranchName: change.HeadBranch, BaseRef: change.BaseBranch, ExistingBranch: true, CodeHost: environment.CodeHost,
		Repository: environment.Repository, PullRequest: &link, IdempotencyKey: key,
	}, nil)
}

// FilterSessions keeps the sessions keep returns true for.
func FilterSessions(sessions []domain.CodingSession, keep func(domain.CodingSession) bool) []domain.CodingSession {
	kept := make([]domain.CodingSession, 0, len(sessions))
	for _, session := range sessions {
		if keep(session) {
			kept = append(kept, session)
		}
	}
	return kept
}
