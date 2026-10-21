package coding

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// PR/MR follow-ups (docs/coding-sessions-design.md §15, track L). Verified,
// normalized code host webhooks (codehost.Host.ParseWebhook, tracks D and J)
// are mapped here to session follow-ups:
//
//   - a comment or inline review comment that mentions FLOW_CODING_PR_MENTION
//     on a session-linked PR/MR becomes an inbox turn carrying the comment,
//     the file and line and the diff hunk; "@flow rebase|lint|test|fix" pick
//     the matching quick action;
//   - a "changes requested" review on a Flow PR becomes a turn without a mention;
//   - a mention on an unlinked PR/MR (same repository, not a fork, an
//     environment exists) starts a pullRequest session on the PR head branch;
//   - a commenter who is not a Flow member is an external source: unless
//     trusted, the request becomes an approval-gated session on the PR branch;
//   - merged/closed updates the PR link; a merge ends an idle session; a
//     close without merging archives the session (a busy one is stopped)
//     and the Archiver unassigns the agent from the issue;
//   - failing CI is surfaced ("Fix failing checks"), never retried automatically.

// FlowCommentMarker is embedded in comments Flow posts, so Flow never reacts
// to its own comments.
const FlowCommentMarker = "<!-- flow-coding -->"

// WebhookAction is what the dispatcher did with an event.
type WebhookAction string

const (
	WebhookIgnored        WebhookAction = "ignored"
	WebhookFollowUp       WebhookAction = "followUp"
	WebhookNewSession     WebhookAction = "newSession"
	WebhookApproval       WebhookAction = "approvalRequired"
	WebhookPRState        WebhookAction = "pullRequestState"
	WebhookChecksFailed   WebhookAction = "checksFailed"
	WebhookRefused        WebhookAction = "refused"
	WebhookSessionEnded   WebhookAction = "sessionEnded"
	WebhookSessionArchive WebhookAction = "sessionArchived"
	WebhookNothingToRoute WebhookAction = "noSession"
)

// WebhookResult reports the dispatch outcome (for logs and tests).
type WebhookResult struct {
	Actions    []WebhookAction `json:"actions"`
	SessionIDs []string        `json:"sessionIds,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	// NotificationPending is set when the state change was committed but a
	// notification about it failed (returned as a NotificationError); the
	// keyed notification is posted once when the delivery is repeated.
	NotificationPending bool `json:"notificationPending,omitempty"`
}

func (r *WebhookResult) add(action WebhookAction, sessionID string) {
	r.Actions = append(r.Actions, action)
	if sessionID != "" {
		r.SessionIDs = append(r.SessionIDs, sessionID)
	}
}

// Commenter is the PR/MR commenter as Flow sees them.
type Commenter struct {
	UserID string
	Member bool
}

// CommenterResolver maps a host login (and email) to a Flow member through
// the linked host identity or the email.
type CommenterResolver interface {
	ResolveCommenter(ctx context.Context, workspaceKey, codeHost, hostName, login, email string) (Commenter, error)
}

// WebhookSessions is the store access the dispatcher needs.
type WebhookSessions interface {
	TriggerSessions
	SessionsForChangeRequest(ctx context.Context, codeHost, hostName, repository string, number int) ([]domain.CodingSession, error)
	UpsertSessionPullRequest(ctx context.Context, workspaceKey string, pullRequest domain.SessionPullRequest) error
	CodingEnvironmentForRepository(ctx context.Context, workspaceKey, codeHost, hostName, repository string) (domain.CodingEnvironment, error)
}

// WebhookNotifier surfaces CI failures on session PRs (optional).
type WebhookNotifier interface {
	ChecksFailed(ctx context.Context, session domain.CodingSession, pullRequest domain.SessionPullRequest, status codehost.CIStatus) error
}

// WebhookArchiver hears about sessions archived because their PR/MR was
// closed (optional): the server unassigns the agent from the issue and
// records both on the issue's activity.
type WebhookArchiver interface {
	SessionArchived(ctx context.Context, session domain.CodingSession, codeHost string) error
}

// WebhookDispatcher maps webhook events to session follow-ups.
type WebhookDispatcher struct {
	Sessions   WebhookSessions
	Starter    SessionStarter
	Commenters CommenterResolver
	Hosts      map[codehost.Kind]codehost.Host
	// Settings returns the workspace's coding policy (trusted sources).
	Settings func(ctx context.Context, workspaceKey string) (domain.CodingAgentSettings, error)
	Notifier TriggerNotifier
	// CINotifier is optional.
	CINotifier WebhookNotifier
	// Archiver is optional.
	Archiver WebhookArchiver
	// AnchorAppUserID returns the built-in Flow agent's app user.
	AnchorAppUserID func(ctx context.Context, workspaceKey string) string
	Config          config.CodingConfig
	Now             func() time.Time
}

func (d *WebhookDispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *WebhookDispatcher) mention() string {
	if strings.TrimSpace(d.Config.PRMention) == "" {
		return "@flow"
	}
	return strings.TrimSpace(d.Config.PRMention)
}

func (d *WebhookDispatcher) branchPrefix() string {
	if d.Config.BranchPrefix == "" {
		return "flow/"
	}
	return d.Config.BranchPrefix
}

// MentionCommand is a parsed "@flow …" request.
type MentionCommand struct {
	Mentioned bool
	// Kind is a Turn kind: prComment, fixPr, rebase, lint or test.
	Kind string
	// Text is the comment with the mention removed.
	Text string
}

// ParseMention finds the mention (case-insensitive, as a whole word) and
// the quick action that follows it.
func ParseMention(body, mention string) MentionCommand {
	mention = strings.TrimSpace(mention)
	if mention == "" || body == "" {
		return MentionCommand{}
	}
	pattern := regexp.MustCompile(`(?i)(^|[^\w@/.-])` + regexp.QuoteMeta(mention) + `\b`)
	location := pattern.FindStringSubmatchIndex(body)
	if location == nil {
		return MentionCommand{}
	}
	text := strings.TrimSpace(pattern.ReplaceAllString(body, "$1"))
	after := strings.ToLower(strings.TrimLeft(body[location[1]:], " \t,:"))
	command := MentionCommand{Mentioned: true, Kind: TurnKindPRComment, Text: text}
	firstWords := strings.Join(strings.Fields(after), " ")
	switch {
	case hasCommandPrefix(firstWords, "rebase"):
		command.Kind = TurnKindRebase
	case hasCommandPrefix(firstWords, "fix lint", "lint"):
		command.Kind = TurnKindLint
	case hasCommandPrefix(firstWords, "add tests", "add test", "write tests", "test", "tests"):
		command.Kind = TurnKindTest
	case hasCommandPrefix(firstWords, "fix this pr", "fix this mr", "fix checks", "fix the checks", "fix ci", "fix the build", "fix"):
		if firstWords == "fix" || len(strings.Fields(firstWords)) <= 3 {
			command.Kind = TurnKindFixPR
		}
	}
	return command
}

func hasCommandPrefix(text string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if text == prefix || strings.HasPrefix(text, prefix+" ") || strings.HasPrefix(text, prefix+".") || strings.HasPrefix(text, prefix+"!") {
			return true
		}
	}
	return false
}

// FollowUpPrompt builds the turn prompt for a quick action or a comment.
func FollowUpPrompt(kind string, pullRequest *codehost.ChangeRequest, request string, event *codehost.WebhookEvent) string {
	var builder strings.Builder
	label := "the pull request"
	if pullRequest != nil {
		noun := "PR"
		if pullRequest.Repo.CodeHost == domain.CodeHostGitLab {
			noun = "MR"
		}
		label = fmt.Sprintf("%s #%d", noun, pullRequest.Number)
		if pullRequest.URL != "" {
			label += " (" + pullRequest.URL + ")"
		}
	}
	branch := ""
	base := ""
	if pullRequest != nil {
		branch, base = pullRequest.HeadBranch, pullRequest.BaseBranch
	}
	switch kind {
	case TurnKindFixPR:
		builder.WriteString("Fix " + label + ": make the failing checks pass and address every unresolved review thread. Read CI with `gh pr checks` / `glab ci status` and the review comments, fix the causes, and push to the same branch.")
	case TurnKindRebase:
		builder.WriteString("Rebase the branch " + branch + " of " + label + " onto the latest " + firstNonEmpty(base, "base branch") + ", resolve any conflicts, run the tests, and push with --force-with-lease.")
	case TurnKindLint:
		builder.WriteString("Run the project's linters and formatters on " + label + ", fix every reported problem, and push the fixes to the same branch.")
	case TurnKindTest:
		builder.WriteString("Add or update tests for the changes in " + label + " so the new behaviour is covered, make sure the suite passes, and push to the same branch.")
	case TurnKindPRComment:
		who := "a reviewer"
		if event != nil && event.AuthorLogin != "" {
			who = "@" + event.AuthorLogin
		}
		builder.WriteString("Review feedback on " + label + " from " + who + ". Address it on the same branch and push.")
		if event != nil && event.CommentPath != "" {
			location := event.CommentPath
			if event.CommentLine > 0 {
				location = fmt.Sprintf("%s:%d", location, event.CommentLine)
			}
			builder.WriteString("\n\nFile: " + location)
		}
		if event != nil && strings.TrimSpace(event.CommentDiffHunk) != "" {
			builder.WriteString("\n\n```diff\n" + strings.TrimSpace(event.CommentDiffHunk) + "\n```")
		}
		if event != nil && event.CommentURL != "" {
			builder.WriteString("\n\nComment: " + event.CommentURL)
		}
	default:
		builder.WriteString("Follow-up on " + label + ".")
	}
	if request = strings.TrimSpace(request); request != "" {
		builder.WriteString("\n\nRequest:\n" + request)
	}
	return builder.String()
}

// RebaseAllowed reports whether a rebase (a force push) may run on branch:
// only Flow branches, unless a person confirmed it explicitly.
func RebaseAllowed(branch, prefix string, confirmed bool) bool {
	return confirmed || (prefix != "" && strings.HasPrefix(branch, prefix))
}

// Dispatch routes one verified event. workspaceKeys limits it to the given
// workspaces (the GitLab connection's workspace, or the App installation's
// workspaces); sessions already linked to the PR/MR are routed whatever
// their workspace when the list is empty.
func (d *WebhookDispatcher) Dispatch(ctx context.Context, workspaceKeys []string, event codehost.WebhookEvent) (WebhookResult, error) {
	result := WebhookResult{}
	if d.Sessions == nil {
		result.add(WebhookIgnored, "")
		result.Reason = "coding sessions unavailable"
		return result, nil
	}
	switch event.Type {
	case codehost.EventChangeRequestClosed, codehost.EventChangeRequestMerged, codehost.EventChangeRequestUpdate:
		return d.dispatchState(ctx, workspaceKeys, event)
	case codehost.EventComment, codehost.EventReviewComment, codehost.EventReview:
		return d.dispatchComment(ctx, workspaceKeys, event)
	case codehost.EventCIStatus:
		return d.dispatchCI(ctx, workspaceKeys, event)
	default:
		result.add(WebhookIgnored, "")
		result.Reason = "event type " + string(event.Type)
		return result, nil
	}
}

func (d *WebhookDispatcher) linkedSessions(ctx context.Context, workspaceKeys []string, event codehost.WebhookEvent) ([]domain.CodingSession, error) {
	if event.ChangeRequest == nil {
		return nil, nil
	}
	repo := event.ChangeRequest.Repo
	if repo.FullName == "" {
		repo = event.Repo
	}
	sessions, err := d.Sessions.SessionsForChangeRequest(ctx, firstNonEmpty(repo.CodeHost, event.CodeHost), firstNonEmpty(repo.HostName, event.HostName), repo.FullName, event.ChangeRequest.Number)
	if err != nil {
		return nil, fmt.Errorf("coding webhook: linked sessions: %w", err)
	}
	if len(workspaceKeys) == 0 {
		return sessions, nil
	}
	allowed := map[string]bool{}
	for _, key := range workspaceKeys {
		allowed[key] = true
	}
	kept := sessions[:0]
	for _, session := range sessions {
		if allowed[session.WorkspaceKey] {
			kept = append(kept, session)
		}
	}
	return kept, nil
}

func pullRequestLink(sessionID string, event codehost.WebhookEvent, now time.Time) domain.SessionPullRequest {
	change := event.ChangeRequest
	repo := change.Repo
	if repo.FullName == "" {
		repo = event.Repo
	}
	return domain.SessionPullRequest{
		SessionID: sessionID, CodeHost: firstNonEmpty(repo.CodeHost, event.CodeHost), HostName: firstNonEmpty(repo.HostName, event.HostName),
		Repository: repo.FullName, Number: change.Number, ExternalID: change.ExternalID, URL: change.URL, Title: change.Title,
		State: change.State, Draft: change.Draft, HeadBranch: change.HeadBranch, BaseBranch: change.BaseBranch, HeadSHA: change.HeadSHA,
		UpdatedAt: now,
	}
}

func (d *WebhookDispatcher) dispatchState(ctx context.Context, workspaceKeys []string, event codehost.WebhookEvent) (WebhookResult, error) {
	result := WebhookResult{}
	sessions, err := d.linkedSessions(ctx, workspaceKeys, event)
	if err != nil || len(sessions) == 0 {
		if err == nil {
			result.add(WebhookNothingToRoute, "")
		}
		return result, err
	}
	now := d.now()
	state := event.ChangeRequest.State
	switch event.Type {
	case codehost.EventChangeRequestMerged:
		state = "merged"
	case codehost.EventChangeRequestClosed:
		if state == "" || state == "open" {
			state = "closed"
		}
	}
	var errs []error
	for _, session := range sessions {
		link := pullRequestLink(session.ID, event, now)
		link.State = firstNonEmpty(state, link.State, "open")
		if err := d.Sessions.UpsertSessionPullRequest(ctx, session.WorkspaceKey, link); err != nil {
			errs = append(errs, err)
			continue
		}
		result.add(WebhookPRState, session.ID)
		ended, archived := false, false
		updated, err := d.Sessions.UpdateCodingSession(ctx, session.WorkspaceKey, session.ID, func(current *domain.CodingSession) error {
			ended, archived = false, false
			closes := event.Type == codehost.EventChangeRequestClosed && link.State == "closed" && current.ArchivedAt == nil && closesSession(*current, link)
			for index := range current.PullRequests {
				pr := &current.PullRequests[index]
				if pr.Number == link.Number && strings.EqualFold(pr.Repository, link.Repository) {
					pr.State, pr.HeadSHA, pr.UpdatedAt = link.State, firstNonEmpty(link.HeadSHA, pr.HeadSHA), now
				}
			}
			if current.LatestPullRequest != nil && current.LatestPullRequest.Number == link.Number && strings.EqualFold(current.LatestPullRequest.Repository, link.Repository) {
				current.LatestPullRequest.State = link.State
			}
			// A merged PR ends an idle session (the work is done); a busy
			// session finishes its turn first.
			if event.Type == codehost.EventChangeRequestMerged && current.Phase == domain.CodingPhaseIdle {
				current.Phase = domain.CodingPhaseDone
				current.State = domain.CodingSessionComplete
				current.EndedAt = &now
				ended = true
			}
			// A PR closed without merging archives its session: an idle one
			// ends, a busy one is stopped (its work has nowhere to go).
			if closes {
				at := now
				current.ArchivedAt = &at
				archived = true
				switch current.Phase {
				case domain.CodingPhaseDone:
				case domain.CodingPhaseIdle:
					current.Phase = domain.CodingPhaseDone
					current.State = domain.CodingSessionComplete
					current.EndedAt = &now
					ended = true
				default:
					current.StopRequested = true
					current.NextReconcileAt = now
				}
			}
			current.UpdatedAt = now
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ended {
			result.add(WebhookSessionEnded, session.ID)
		}
		if archived {
			result.add(WebhookSessionArchive, session.ID)
			if d.Archiver != nil {
				if err := d.Archiver.SessionArchived(ctx, updated, firstNonEmpty(link.CodeHost, event.CodeHost)); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	return result, errors.Join(errs...)
}

// closesSession reports whether closing link archives the session: the PR/MR
// was not merged and no other PR/MR linked to the session is still open.
func closesSession(session domain.CodingSession, link domain.SessionPullRequest) bool {
	same := func(pr domain.SessionPullRequest) bool {
		return pr.Number == link.Number && strings.EqualFold(pr.Repository, link.Repository)
	}
	for _, pr := range session.PullRequests {
		if same(pr) && pr.State == "merged" || !same(pr) && pr.State == "open" {
			return false
		}
	}
	return session.LatestPullRequest == nil || !same(*session.LatestPullRequest) || session.LatestPullRequest.State != "merged"
}

func (d *WebhookDispatcher) dispatchCI(ctx context.Context, workspaceKeys []string, event codehost.WebhookEvent) (WebhookResult, error) {
	result := WebhookResult{}
	if event.CI == nil || event.CI.State != "failure" {
		result.add(WebhookIgnored, "")
		result.Reason = "checks did not fail"
		return result, nil
	}
	sessions, err := d.linkedSessions(ctx, workspaceKeys, event)
	if err != nil || len(sessions) == 0 {
		if err == nil {
			result.add(WebhookNothingToRoute, "")
		}
		return result, err
	}
	var errs []error
	for _, session := range sessions {
		result.add(WebhookChecksFailed, session.ID)
		if d.CINotifier != nil {
			if err := d.CINotifier.ChecksFailed(ctx, session, pullRequestLink(session.ID, event, d.now()), *event.CI); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) > 0 {
		// The failed checks are recorded as routed; only the notices failed.
		result.NotificationPending = true
		return result, &NotificationError{Step: "checks failed", Err: errors.Join(errs...)}
	}
	return result, nil
}

func (d *WebhookDispatcher) dispatchComment(ctx context.Context, workspaceKeys []string, event codehost.WebhookEvent) (WebhookResult, error) {
	result := WebhookResult{}
	if event.AuthorIsBot || strings.Contains(event.CommentBody, FlowCommentMarker) {
		result.add(WebhookIgnored, "")
		result.Reason = "bot or Flow comment"
		return result, nil
	}
	if event.ChangeRequest == nil {
		result.add(WebhookIgnored, "")
		result.Reason = "comment is not on a pull/merge request"
		return result, nil
	}
	linked, err := d.linkedSessions(ctx, workspaceKeys, event)
	if err != nil {
		return result, err
	}
	command := ParseMention(event.CommentBody, d.mention())
	changesRequested := event.Type == codehost.EventReview && strings.EqualFold(event.ReviewState, "changes_requested")
	switch {
	case command.Mentioned:
	case changesRequested && (len(linked) > 0 || strings.HasPrefix(event.ChangeRequest.HeadBranch, d.branchPrefix())):
		command = MentionCommand{Kind: TurnKindPRComment, Text: strings.TrimSpace(event.CommentBody)}
	default:
		result.add(WebhookIgnored, "")
		result.Reason = "no mention"
		return result, nil
	}
	if command.Kind == TurnKindRebase && !flowBranch(event.ChangeRequest.HeadBranch, d.branchPrefix(), linked) {
		d.reply(ctx, workspaceKeys, linked, event, "Flow only rebases its own `"+d.branchPrefix()+"` branches from a comment, because a rebase force-pushes. Use \"Rebase\" on the review page in Flow and confirm the force push.")
		result.add(WebhookRefused, "")
		result.Reason = "rebase needs confirmation"
		return result, nil
	}
	prompt := FollowUpPrompt(command.Kind, event.ChangeRequest, command.Text, &event)

	// Pick the workspace and the session to continue: the newest open session
	// linked to the PR/MR (an idle or finished one is resumed too).
	target, hasTarget := latestFollowUpTarget(linked)
	workspaceKey := ""
	if hasTarget {
		workspaceKey = target.WorkspaceKey
	} else {
		workspaceKey, err = d.workspaceForUnlinked(ctx, workspaceKeys, event)
		if err != nil {
			return result, err
		}
		if workspaceKey == "" {
			result.add(WebhookNothingToRoute, "")
			result.Reason = "no workspace has an environment for this repository"
			return result, nil
		}
	}
	commenter := Commenter{}
	if d.Commenters != nil {
		commenter, err = d.Commenters.ResolveCommenter(ctx, workspaceKey, event.CodeHost, event.HostName, event.AuthorLogin, event.AuthorEmail)
		if err != nil {
			return result, fmt.Errorf("coding webhook: commenter: %w", err)
		}
	}
	origin := ExternalOrigin{}
	if !commenter.Member {
		origin = ExternalOrigin{External: true, Source: commenterSource(event), Label: "@" + event.AuthorLogin}
		settings := domain.CodingAgentSettings{}
		if d.Settings != nil {
			if settings, err = d.Settings(ctx, workspaceKey); err != nil {
				return result, fmt.Errorf("coding webhook: settings: %w", err)
			}
		}
		if IsTrustedSource(origin.Source, settings.TrustedExternalSources) {
			origin.External = false
		}
	}
	turnKey := firstNonEmpty(event.CommentID, event.DeliveryID)
	if hasTarget && !origin.External {
		updated, err := d.Starter.PromptCodingSession(ctx, workspaceKey, target.ID, Turn{
			ID: turnID(workspaceKey, target.ID, "webhook", event.CodeHost, turnKey), Prompt: prompt, AuthorID: commenter.UserID, Kind: command.Kind,
		})
		if err != nil {
			return result, err
		}
		result.add(WebhookFollowUp, updated.ID)
		return result, nil
	}

	// A new session: an unlinked PR/MR, or an external commenter whose
	// request must be approved first (it continues on the PR branch).
	if event.ChangeRequest.HeadRepository != "" && !strings.EqualFold(event.ChangeRequest.HeadRepository, firstNonEmpty(event.ChangeRequest.Repo.FullName, event.Repo.FullName)) {
		result.add(WebhookRefused, "")
		result.Reason = "pull request from a fork"
		return result, nil
	}
	if !hasTarget && command.Kind == TurnKindRebase && !RebaseAllowed(event.ChangeRequest.HeadBranch, d.branchPrefix(), false) {
		result.add(WebhookRefused, "")
		return result, nil
	}
	repo := event.ChangeRequest.Repo
	if repo.FullName == "" {
		repo = event.Repo
	}
	environment, err := d.Sessions.CodingEnvironmentForRepository(ctx, workspaceKey, firstNonEmpty(repo.CodeHost, event.CodeHost), firstNonEmpty(repo.HostName, event.HostName), repo.FullName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			result.add(WebhookNothingToRoute, "")
			result.Reason = "no environment for this repository"
			return result, nil
		}
		return result, fmt.Errorf("coding webhook: environment: %w", err)
	}
	link := pullRequestLink("", event, d.now())
	appUser := ""
	if d.AnchorAppUserID != nil {
		appUser = d.AnchorAppUserID(ctx, workspaceKey)
	}
	creator := commenter.UserID
	if creator == "" && hasTarget {
		creator = target.CreatorID
	}
	start := TriggerStartRequest{
		WorkspaceKey: workspaceKey, Source: domain.CodingSourcePullRequest, SourceID: firstNonEmpty(event.ChangeRequest.ExternalID, fmt.Sprintf("%s#%d", repo.FullName, event.ChangeRequest.Number)),
		Title: firstNonEmpty(event.ChangeRequest.Title, fmt.Sprintf("#%d", event.ChangeRequest.Number)), Prompt: prompt, TurnKind: command.Kind,
		EnvironmentID: environment.ID, CreatorID: creator, AnchorAppUserID: appUser, AnchorResourceType: "pullRequest",
		AnchorResourceID: link.ExternalID, BranchName: event.ChangeRequest.HeadBranch, BaseRef: event.ChangeRequest.BaseBranch,
		ExistingBranch: true, CodeHost: environment.CodeHost, Repository: environment.Repository, PullRequest: &link,
		RequireApproval: origin.External, ExternalSource: origin.Source,
		IdempotencyKey: "webhook:" + event.CodeHost + ":" + firstNonEmpty(event.HostName, repo.HostName) + ":" + repo.FullName + ":" + turnKey,
	}
	if hasTarget {
		start.IssueID = target.IssueID
	}
	session, created, err := d.Starter.StartCodingSession(ctx, start)
	if err != nil {
		return result, err
	}
	if session.Phase == domain.CodingPhaseAwaitingApproval {
		result.add(WebhookApproval, session.ID)
		if d.Notifier != nil {
			if err := d.Notifier.ApprovalRequested(ctx, session, start); err != nil {
				// The session exists either way; the keyed request is
				// posted once when the delivery is repeated.
				result.NotificationPending = true
				return result, &NotificationError{Step: "approval", Err: err}
			}
		}
		return result, nil
	}
	if created {
		result.add(WebhookNewSession, session.ID)
	} else {
		result.add(WebhookFollowUp, session.ID)
	}
	return result, nil
}

// latestFollowUpTarget is the newest linked session that can take a turn:
// not rejected or expired (a done session is re-queued by the prompt).
func latestFollowUpTarget(sessions []domain.CodingSession) (domain.CodingSession, bool) {
	var best domain.CodingSession
	found := false
	for _, session := range sessions {
		if notApproved(session) || session.Phase == domain.CodingPhaseAwaitingApproval {
			continue
		}
		if !found || session.CreatedAt.After(best.CreatedAt) {
			best, found = session, true
		}
	}
	return best, found
}

// workspaceForUnlinked is the only candidate workspace with an environment
// for the repository; "" when none or several.
func (d *WebhookDispatcher) workspaceForUnlinked(ctx context.Context, workspaceKeys []string, event codehost.WebhookEvent) (string, error) {
	repo := event.ChangeRequest.Repo
	if repo.FullName == "" {
		repo = event.Repo
	}
	found := ""
	for _, key := range workspaceKeys {
		_, err := d.Sessions.CodingEnvironmentForRepository(ctx, key, firstNonEmpty(repo.CodeHost, event.CodeHost), firstNonEmpty(repo.HostName, event.HostName), repo.FullName)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("coding webhook: environment: %w", err)
		}
		if found != "" {
			return "", nil // ambiguous: several workspaces use this repository
		}
		found = key
	}
	return found, nil
}

// commenterSource is the external source key of a non-member commenter:
// "<codeHost>:<owner>/<repo>", so trusting "github:acme" trusts comments on
// every acme repository.
func commenterSource(event codehost.WebhookEvent) string {
	repo := event.Repo.FullName
	if event.ChangeRequest != nil && event.ChangeRequest.Repo.FullName != "" {
		repo = event.ChangeRequest.Repo.FullName
	}
	return strings.ToLower(firstNonEmpty(event.CodeHost, "codehost") + ":" + repo)
}

// reply posts a short Flow comment on the PR/MR (best effort).
func (d *WebhookDispatcher) reply(ctx context.Context, workspaceKeys []string, linked []domain.CodingSession, event codehost.WebhookEvent, body string) {
	host := d.Hosts[event.CodeHost]
	if host == nil || event.ChangeRequest == nil {
		return
	}
	workspaceKey := ""
	if len(linked) > 0 {
		workspaceKey = linked[0].WorkspaceKey
	} else if len(workspaceKeys) > 0 {
		workspaceKey = workspaceKeys[0]
	}
	repo := event.ChangeRequest.Repo
	if repo.FullName == "" {
		repo = event.Repo
	}
	_ = host.Comment(ctx, workspaceKey, repo, event.ChangeRequest.Number, body+"\n\n"+FlowCommentMarker)
}
