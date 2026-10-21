package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Coding-session trigger wiring (docs/coding-sessions-design.md §15, track
// L): the store adapters, the notifier that posts approval and repository
// elicitations, the commenter resolver, and dispatchCodingWebhook, which
// github_app.go (track D) and code_webhooks.go (track J) call with verified,
// normalized webhook events.

// codingTriggerStore returns the store the trigger code uses: the codingstore
// adapter, which implements every view below
// (coding_integration_contracts_test.go asserts it). Tests replace it with a fake.
var codingTriggerStore = func(s *server) any { return s.codingStore() }

// codingTriggerStores are the store views triggers need.
type codingTriggerStores struct {
	sessions     coding.StarterSessions
	triggers     coding.TriggerSessions
	environments coding.TriggerEnvironments
	webhooks     coding.WebhookSessions
	pullRequests coding.StarterPullRequests
	spend        coding.SpendReader
}

func (s *server) codingTriggerStores() (codingTriggerStores, bool) {
	raw := codingTriggerStore(s)
	var stores codingTriggerStores
	var ok bool
	if stores.sessions, ok = raw.(coding.StarterSessions); !ok {
		return stores, false
	}
	if stores.triggers, ok = raw.(coding.TriggerSessions); !ok {
		return stores, false
	}
	if stores.environments, ok = raw.(coding.TriggerEnvironments); !ok {
		return stores, false
	}
	stores.webhooks, _ = raw.(coding.WebhookSessions)
	stores.pullRequests, _ = raw.(coding.StarterPullRequests)
	stores.spend, _ = raw.(coding.SpendReader)
	return stores, true
}

// codingTriggers builds the trigger service for a workspace; ok is false
// when coding sessions are off or the coding store is not available.
func (s *server) codingTriggers(workspace string) (*coding.Triggers, bool) {
	if !s.codingEnabled() {
		return nil, false
	}
	stores, ok := s.codingTriggerStores()
	if !ok {
		return nil, false
	}
	settings := domain.CodingAgentSettings{}
	if data, found := s.store.WorkspaceMetadataFields(workspace, "workspaceSettings"); found {
		settings = data.WorkspaceSettings.CodingAgentSettings
	}
	triggers := &coding.Triggers{
		Starter: s.codingSessionStarter(stores, settings), Sessions: stores.triggers, Environments: stores.environments, Spend: stores.spend,
		Notifier: &codingTriggerNotifier{s: s}, Config: s.coding.config,
	}
	if s.coding.service != nil {
		// One approval state machine: the session core's (track B).
		triggers.Approver = s.coding.service.Approve
	}
	return triggers, true
}

// codingSessionStarter starts trigger sessions through the session core
// (coding.ServiceStarter over Service.Start / Service.Prompt); the
// store-level StoreStarter is only a fallback for trigger tests that run
// without a session service.
func (s *server) codingSessionStarter(stores codingTriggerStores, settings domain.CodingAgentSettings) coding.SessionStarter {
	if s.coding.service != nil {
		return coding.ServiceStarter{Service: s.coding.service, PullRequests: stores.pullRequests}
	}
	return &coding.StoreStarter{
		Sessions: stores.sessions, PullRequests: stores.pullRequests, Config: s.coding.config,
		DefaultHarness: normalizeCodingHarness(settings.Harness), DefaultModel: settings.Model,
	}
}

// normalizeCodingHarness reads the legacy harness values (auto → codex,
// claude → claudeCode).
func normalizeCodingHarness(value string) string {
	switch strings.TrimSpace(value) {
	case "", "auto":
		return domain.CodingHarnessCodex
	case "claude":
		return domain.CodingHarnessClaudeCode
	default:
		return strings.TrimSpace(value)
	}
}

// builtinAgentUserID is the workspace's built-in Flow agent user ("" when
// it is not installed).
func (s *server) builtinAgentUserID(ctx context.Context, workspace string) string {
	applications, err := s.store.ListApplications(ctx, workspace)
	if err != nil {
		return ""
	}
	for _, application := range applications {
		if application.Builtin {
			return application.UserID
		}
	}
	return ""
}

// codingWebhookDispatcher builds the webhook dispatcher.
func (s *server) codingWebhookDispatcher() (*coding.WebhookDispatcher, bool) {
	if !s.codingEnabled() {
		return nil, false
	}
	stores, ok := s.codingTriggerStores()
	if !ok || stores.webhooks == nil {
		return nil, false
	}
	notifier := &codingTriggerNotifier{s: s}
	return &coding.WebhookDispatcher{
		Sessions:   stores.webhooks,
		Starter:    s.codingSessionStarter(stores, domain.CodingAgentSettings{}),
		Commenters: codingCommenterResolver{s: s},
		Hosts:      s.coding.hosts,
		Settings: func(_ context.Context, workspace string) (domain.CodingAgentSettings, error) {
			data, found := s.store.WorkspaceMetadataFields(workspace, "workspaceSettings")
			if !found {
				return domain.CodingAgentSettings{}, errNotFound
			}
			return data.WorkspaceSettings.CodingAgentSettings, nil
		},
		Notifier:        notifier,
		CINotifier:      notifier,
		Archiver:        notifier,
		AnchorAppUserID: s.builtinAgentUserID,
		Config:          s.coding.config,
	}, true
}

// codingWorkspacesForInstallation lists the workspaces a GitHub App
// installation serves (track D's store), when the store provides it.
type codingWorkspacesForInstallation interface {
	WorkspacesForInstallation(ctx context.Context, codeHost, hostName, installationID string) ([]string, error)
}

// dispatchCodingWebhook hands a verified, normalized code host event to the
// session layer. workspace is the receiving workspace when the caller knows
// it (GitLab connection webhooks); "" lets the installation decide (GitHub
// App). Errors are returned for logging; the caller still answers 2xx once
// the delivery is stored, because redelivering a half-applied event is safe
// (every step is idempotent) but retries from the host are not needed.
func (s *server) dispatchCodingWebhook(ctx context.Context, workspace string, event codehost.WebhookEvent) error {
	dispatcher, ok := s.codingWebhookDispatcher()
	if !ok {
		return nil
	}
	var workspaces []string
	if workspace != "" {
		workspaces = []string{workspace}
	} else if event.InstallationID != "" {
		if lookup, ok := codingTriggerStore(s).(codingWorkspacesForInstallation); ok {
			keys, err := lookup.WorkspacesForInstallation(ctx, event.CodeHost, event.HostName, event.InstallationID)
			if err != nil {
				return fmt.Errorf("coding webhook: installation workspaces: %w", err)
			}
			workspaces = keys
		}
	}
	// Only workspaces with coding sessions on take part.
	workspaces = slices.DeleteFunc(workspaces, func(key string) bool {
		data, found := s.store.WorkspaceMetadataFields(key, "workspaceSettings")
		return !found || !workspaceFeatureEnabled(data.WorkspaceSettings, "coding-sessions")
	})
	if workspace == "" && event.InstallationID != "" && len(workspaces) == 0 {
		return nil
	}
	result, err := dispatcher.Dispatch(ctx, workspaces, event)
	if err != nil {
		if !coding.NotificationFailed(err) {
			return err
		}
		// The state change committed; only a notification failed. Log it
		// once and answer success so the host does not redeliver (a
		// redelivery posts the keyed notification only once anyway).
		log.Printf("coding webhook %s %s: %v (committed %v sessions=%v; repeating the delivery posts the notification)", event.CodeHost, event.Type, err, result.Actions, result.SessionIDs)
		return nil
	}
	if len(result.SessionIDs) > 0 {
		log.Printf("coding webhook %s %s: %v sessions=%v", event.CodeHost, event.Type, result.Actions, result.SessionIDs)
	}
	return nil
}

// codingCommenterResolver maps a code host login (or email) to a workspace
// member, the way the review sync matches reviewers: the login equals the
// user's name, display name or email local part, or the email matches.
type codingCommenterResolver struct{ s *server }

func (r codingCommenterResolver) ResolveCommenter(ctx context.Context, workspace, codeHost, hostName, login, email string) (coding.Commenter, error) {
	data, ok := r.s.store.WorkspaceMetadataFields(workspace, "members", "users")
	if !ok {
		return coding.Commenter{}, nil
	}
	// A member who linked this host account is matched exactly first.
	if login != "" && codeHost != "" {
		if user, linked := r.s.codeHostMemberLogins(ctx, &data, codeHost, hostName, login)[strings.ToLower(strings.TrimSpace(login))]; linked {
			for _, member := range data.Members {
				if member.User.ID == user.ID {
					return coding.Commenter{UserID: user.ID, Member: codingMemberRole(member.Role)}, nil
				}
			}
			if len(data.Members) == 0 && r.s.authDisabled {
				return coding.Commenter{UserID: user.ID, Member: true}, nil
			}
		}
	}
	login = strings.ToLower(strings.TrimSpace(login))
	email = strings.ToLower(strings.TrimSpace(email))
	matches := func(user domain.User) bool {
		local, _, _ := strings.Cut(strings.ToLower(user.Email), "@")
		return email != "" && strings.EqualFold(user.Email, email) ||
			login != "" && (strings.EqualFold(user.Name, login) || strings.EqualFold(user.DisplayName, login) || local == login)
	}
	for _, member := range data.Members {
		if member.User.App || (member.Status != "" && member.Status != "active") {
			continue
		}
		if matches(member.User) {
			return coding.Commenter{UserID: member.User.ID, Member: codingMemberRole(member.Role)}, nil
		}
	}
	if len(data.Members) == 0 && r.s.authDisabled {
		// Single-user mode without memberships: every workspace user is a member.
		for _, user := range data.Users {
			if !user.App && matches(user) {
				return coding.Commenter{UserID: user.ID, Member: true}, nil
			}
		}
	}
	return coding.Commenter{}, nil
}

// codingMemberRole reports whether a workspace role may approve and trigger
// sessions as a member (guests and apps are external).
func codingMemberRole(role string) bool {
	switch role {
	case "owner", "admin", "member", "":
		return true
	default:
		return false
	}
}

// Elicitation option values posted on the session anchor. The answer comes
// back as the prompt activity's body (the selected option's value).
const (
	codingApproveValuePrefix     = "coding:approve:"
	codingRejectValuePrefix      = "coding:reject:"
	codingEnvironmentValuePrefix = "coding:environment:"
)

// codingTriggerNotifier posts elicitations and outcomes on the session's
// AgentTask anchor and notifies the team's members of approval requests.
type codingTriggerNotifier struct{ s *server }

// errCodingAnchorRefused is an anchor whose state refuses the activity (a
// stopped or revoked thread): retrying cannot change that, so it is
// reported at once instead of as a version conflict.
var errCodingAnchorRefused = errors.New("coding: the session's agent task refused the activity")

// appendAnchorActivity posts an activity on the anchor once: key is stored
// in the activity metadata and an activity with the same key is not posted
// again (notifications are retried with the trigger). A version conflict
// (another writer) is retried; an anchor whose state refuses the activity
// is errCodingAnchorRefused at once, or nothing when the session is done
// (its outcome is already on the anchor).
func (n *codingTriggerNotifier) appendAnchorActivity(ctx context.Context, session domain.CodingSession, key string, activity domain.AgentActivity) error {
	if session.AgentSessionID == "" {
		return nil
	}
	if activity.Metadata == nil {
		activity.Metadata = map[string]any{}
	}
	activity.Metadata["codingKey"] = key
	activity.Metadata["codingSessionId"] = session.ID
	for attempt := 0; attempt < 3; attempt++ {
		task, err := n.s.store.AgentTask(ctx, session.WorkspaceKey, session.AgentSessionID)
		if err != nil {
			return nil // the anchor is written by the session store; nothing to post on yet
		}
		existing, err := n.s.store.AgentActivities(ctx, session.WorkspaceKey, task.ID, "")
		if err != nil {
			return err
		}
		if slices.ContainsFunc(existing, func(item domain.AgentActivity) bool { return item.Metadata["codingKey"] == key }) {
			return nil
		}
		if activity.ActorID == "" {
			activity.ActorID = task.AppUserID
		}
		_, err = n.s.store.AppendAgentActivity(ctx, session.WorkspaceKey, task.ID, task.Version, activity)
		if err == nil {
			return nil
		}
		if !errors.Is(err, store.ErrIssueVersion) {
			return err
		}
		if again, readErr := n.s.store.AgentTask(ctx, session.WorkspaceKey, task.ID); readErr == nil && again.Version == task.Version {
			// Not a race: the anchor's state machine refused the activity.
			if session.Phase == domain.CodingPhaseDone {
				log.Printf("coding session %s: %s notice not posted on the ended anchor %s (%s)", session.ID, key, task.ID, task.Status)
				return nil
			}
			return fmt.Errorf("%w: %s on %s (%s)", errCodingAnchorRefused, activity.Type, task.ID, task.Status)
		}
	}
	return errConflict
}

func (n *codingTriggerNotifier) ApprovalRequested(ctx context.Context, session domain.CodingSession, request coding.TriggerStartRequest) error {
	source := firstNonEmpty(request.ExternalSource, "an external source")
	body := "A coding session was requested from " + source + ". A team member must approve it before Flow runs code."
	if err := n.appendAnchorActivity(ctx, session, "approval", domain.AgentActivity{
		Type: "elicitation", Body: body, Signal: "select",
		SignalMetadata: map[string]any{"options": []any{
			map[string]any{"label": "Approve", "value": codingApproveValuePrefix + session.ID},
			map[string]any{"label": "Reject", "value": codingRejectValuePrefix + session.ID},
		}, "kind": "codingApproval"},
	}); err != nil {
		return err
	}
	return n.notifyApprovers(ctx, session, request, source)
}

// notifyApprovers adds an Inbox notification for every member of the
// issue's team (every workspace member when there is no team). The id is
// deterministic, so a retry does not notify twice.
func (n *codingTriggerNotifier) notifyApprovers(ctx context.Context, session domain.CodingSession, request coding.TriggerStartRequest, source string) error {
	now := time.Now()
	title := firstNonEmpty(session.DisplayTitle, request.Title, "Coding session")
	return n.s.store.MutateWorkspace(ctx, session.WorkspaceKey, "coding.approval_requested", session.ID, map[string]any{"sessionId": session.ID}, func(data *domain.Bootstrap) error {
		recipients := map[string]bool{}
		teamID := request.TeamID
		for _, member := range data.TeamMembers {
			if teamID != "" && member.TeamID == teamID {
				recipients[member.UserID] = true
			}
		}
		roles := map[string]string{}
		for _, member := range data.Members {
			roles[member.User.ID] = member.Role
			if teamID == "" && codingMemberRole(member.Role) && !member.User.App {
				recipients[member.User.ID] = true
			}
		}
		ids := make([]string, 0, len(recipients))
		for id := range recipients {
			if codingMemberRole(roles[id]) {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, recipientID := range ids {
			notificationID := "notification_coding_approval_" + session.ID + "_" + recipientID
			if slices.ContainsFunc(data.Notifications, func(item domain.Notification) bool { return item.ID == notificationID }) {
				continue
			}
			preferences, ok := data.NotificationPreferences[recipientID]
			if !ok {
				preferences = defaultPreferences(recipientID)
			}
			if !preferences.Inbox.Enabled || !categoryEnabled(preferences.Inbox, "mentions") {
				continue
			}
			notification := domain.Notification{
				ID: notificationID, RecipientID: recipientID, Type: "codingApproval", Category: "mentions",
				SourceType: "agentSession", SourceID: session.AgentSessionID, IssueID: session.IssueID,
				GroupKey: recipientID + ":coding-approval:" + session.ID, OccurrenceCount: 1, Title: title,
				Text:      "Approve a coding session requested from " + source,
				Payload:   &domain.NotificationPayload{Excerpt: firstLineOf(request.Prompt), AgentActivity: "elicitation"},
				CreatedAt: now, UpdatedAt: now,
			}
			data.Notifications = append(data.Notifications, notification)
			enqueueNotificationDeliveries(data, notification, preferences)
		}
		return nil
	})
}

func firstLineOf(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	if len([]rune(text)) > 160 {
		text = string([]rune(text)[:159]) + "…"
	}
	return text
}

func (n *codingTriggerNotifier) EnvironmentSelectionRequested(ctx context.Context, session domain.CodingSession, suggestions []coding.EnvironmentSuggestion) error {
	options := make([]any, 0, min(len(suggestions), 6))
	for _, suggestion := range suggestions {
		if len(options) == 6 {
			break
		}
		label := firstNonEmpty(suggestion.Name, suggestion.Repository)
		if suggestion.Repository != "" && suggestion.Repository != label {
			label += " (" + suggestion.Repository + ")"
		}
		options = append(options, map[string]any{"label": label, "value": codingEnvironmentValuePrefix + suggestion.EnvironmentID})
	}
	return n.appendAnchorActivity(ctx, session, "environment", domain.AgentActivity{
		Type: "elicitation", Body: "Which repository should this coding session use?", Signal: "select",
		SignalMetadata: map[string]any{"options": options, "kind": "codingEnvironment"},
	})
}

func (n *codingTriggerNotifier) ApprovalResolved(ctx context.Context, session domain.CodingSession, decision, userID string) error {
	body := "Coding session approved."
	switch decision {
	case coding.ApprovalRejected:
		body = "Coding session rejected."
	case "expired":
		body = "Nobody approved this coding session in time."
	}
	if userID != "" {
		if data, ok := n.s.store.WorkspaceMetadataFields(session.WorkspaceKey, "users"); ok {
			if user := userByID(&data, userID); user != nil {
				body = strings.TrimSuffix(body, ".") + " by " + firstNonEmpty(user.DisplayName, user.Name) + "."
			}
		}
	}
	return n.appendAnchorActivity(ctx, session, "approval:"+decision, domain.AgentActivity{Type: "action", Body: body, Metadata: map[string]any{"decision": decision}})
}

// notifyCodingApprovalExpired is the session core's ApprovalExpired hook:
// track L's "approval expired" notice on the anchor. The notice is keyed
// (approval:expired), so a repeated call posts nothing new.
func (s *server) notifyCodingApprovalExpired(ctx context.Context, session domain.CodingSession) {
	if err := (&codingTriggerNotifier{s: s}).ApprovalResolved(ctx, session, "expired", ""); err != nil {
		log.Printf("coding sessions: approval expired notice for %s: %v", session.ID, err)
	}
}

func (n *codingTriggerNotifier) ChecksFailed(ctx context.Context, session domain.CodingSession, pullRequest domain.SessionPullRequest, status codehost.CIStatus) error {
	body := fmt.Sprintf("Checks failed on #%d", pullRequest.Number)
	if status.Summary != "" {
		body += ": " + status.Summary
	}
	body += ". Use \"Fix failing checks\" to start a follow-up."
	return n.appendAnchorActivity(ctx, session, fmt.Sprintf("checks:%d:%s", pullRequest.Number, firstNonEmpty(pullRequest.HeadSHA, status.URL)), domain.AgentActivity{Type: "action", Body: body, URL: status.URL, Metadata: map[string]any{"kind": "checksFailed", "pullRequest": pullRequest.Number}})
}

// Activity of a session archived because its PR/MR was closed.
const (
	activityCodingSessionArchived = "issue.coding_session_archived"
	// actorBotTypeIntegration attributes a change to a code host ("GitHub unassigned Flow").
	actorBotTypeIntegration = "integration"
)

// SessionArchived unassigns the agent from the session's issue when its
// PR/MR was closed without merging, attributed to the code host, and records
// that the session was archived.
func (n *codingTriggerNotifier) SessionArchived(ctx context.Context, session domain.CodingSession, codeHost string) error {
	if session.IssueID == "" {
		return nil
	}
	agentID := n.s.builtinAgentUserID(ctx, session.WorkspaceKey)
	hostName := "GitHub"
	if codeHost == domain.CodeHostGitLab {
		hostName = "GitLab"
	}
	issueID := session.IssueID
	return n.s.store.MutateWorkspace(withIssueScope(ctx, &issueID), session.WorkspaceKey, "coding_session.archived", session.ID, map[string]any{"sessionId": session.ID, "codeHost": codeHost}, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, issueID)
		if err != nil {
			return nil // the issue is gone
		}
		if slices.ContainsFunc(data.Activities[issueID], func(event domain.ActivityEvent) bool {
			return event.Type == activityCodingSessionArchived && event.Metadata["sessionId"] == session.ID
		}) {
			return nil // a redelivered close
		}
		actor := data.Viewer
		if agent := userByID(data, agentID); agent != nil {
			actor = *agent
		}
		now := time.Now().UTC()
		if agentID != "" && issue.Delegate != nil && issue.Delegate.ID == agentID {
			empty := ""
			changes, err := applyUpdate(data, issue, domain.IssueUpdateInput{DelegateID: &empty})
			if err == nil {
				issue.UpdatedAt = now
				changes["previousDelegate"], changes["source"] = agentID, codeHost
				data.Activities[issueID] = append(data.Activities[issueID], domain.ActivityEvent{
					ID: fmt.Sprintf("activity_%d", now.UnixNano()), Type: "issue.updated", CreatedAt: now, Actor: actor, Metadata: changes,
					BotActor: &domain.ActorBot{ID: codeHost, Type: actorBotTypeIntegration, UserDisplayName: hostName},
				})
			}
		}
		appendActivity(data, issueID, activityCodingSessionArchived, actor, map[string]string{"sessionId": session.ID, "agentSessionId": session.AgentSessionID, "agentId": agentID})
		return nil
	})
}

// handleCodingElicitationAnswer applies an answer to a coding elicitation
// posted on an anchor (Approve/Reject or a repository choice). The anchor's
// activity route (track B's hook in application_agents.go) calls it with the
// prompt body; handled is false for every other answer.
func (s *server) handleCodingElicitationAnswer(ctx context.Context, workspace, userID, role, answer string) (bool, error) {
	answer = strings.TrimSpace(answer)
	var sessionID string
	decision := coding.ApprovalDecision("")
	switch {
	case strings.HasPrefix(answer, codingApproveValuePrefix):
		sessionID, decision = strings.TrimPrefix(answer, codingApproveValuePrefix), coding.DecisionApprove
	case strings.HasPrefix(answer, codingRejectValuePrefix):
		sessionID, decision = strings.TrimPrefix(answer, codingRejectValuePrefix), coding.DecisionReject
	case strings.HasPrefix(answer, codingEnvironmentValuePrefix):
		return false, nil // answered on the issue route with the session's issue (see startIssueCodingSession)
	default:
		return false, nil
	}
	triggers, ok := s.codingTriggers(workspace)
	if !ok {
		return true, &coding.TriggerError{Code: domain.CodingErrSandboxDisabled, Message: "Coding sessions are not available."}
	}
	_, err := triggers.DecideApproval(ctx, workspace, sessionID, userID, codingMemberRole(role), decision)
	if codingNotificationOnly(err) {
		err = nil // the decision committed; only its notice is missing
	}
	return true, err
}

// linkedRepositoriesForIssue lists the repositories of the issue's PRs/MRs:
// synced code reviews, then GitHub/GitLab link attachments.
func linkedRepositoriesForIssue(data domain.Bootstrap, issue domain.Issue) []coding.LinkedRepository {
	var linked []coding.LinkedRepository
	seen := map[string]bool{}
	add := func(codeHost, hostName, repository string) {
		repository = strings.Trim(repository, "/")
		key := strings.ToLower(codeHost + "|" + repository)
		if repository == "" || seen[key] {
			return
		}
		seen[key] = true
		linked = append(linked, coding.LinkedRepository{CodeHost: codeHost, HostName: hostName, Repository: repository})
	}
	for _, review := range data.Reviews {
		if slices.Contains(review.IssueIDs, issue.ID) && review.RepositoryOwner != "" && review.RepositoryName != "" {
			hostName := ""
			if parsed, err := url.Parse(review.URL); err == nil {
				hostName = parsed.Host
			}
			add(review.Provider, hostName, review.RepositoryOwner+"/"+review.RepositoryName)
		}
	}
	for _, attachment := range issue.Attachments {
		if attachment.Provider != domain.CodeHostGitHub && attachment.Provider != domain.CodeHostGitLab {
			continue
		}
		parsed, err := url.Parse(firstNonEmpty(attachment.ProviderURL, attachment.LinkbackURL))
		if err != nil || parsed.Host == "" {
			continue
		}
		path := parsed.Path
		for _, marker := range []string{"/-/merge_requests/", "/pull/", "/merge_requests/"} {
			if index := strings.Index(path, marker); index > 0 {
				add(attachment.Provider, parsed.Host, path[:index])
				break
			}
		}
	}
	return linked
}

// issueExternalOrigin decides whether an issue came from an external
// source: an integration intake (an Ask from email, Slack, Intercom…), an
// email intake message, or an issue created by an API application (not the
// built-in Flow agent).
func issueExternalOrigin(data domain.Bootstrap, issue domain.Issue) coding.ExternalOrigin {
	for _, ask := range data.Asks {
		if ask.IssueID == issue.ID && ask.Source != "" && ask.Source != "web" && ask.Source != "loop" && ask.Source != "manual" {
			return coding.IntegrationOrigin(ask.Source)
		}
	}
	for _, message := range data.EmailIntakeMessages {
		if message.IssueID == issue.ID {
			return coding.IntegrationOrigin("email")
		}
	}
	if issue.BotActor != nil && issue.BotActor.Type == domain.ActorBotTypeOAuthApplication {
		return coding.ExternalOrigin{External: true, Source: "appUser:" + issue.BotActor.ID, Label: issue.BotActor.Name}
	}
	if issue.Creator.App && !issue.Creator.BuiltinAgent {
		return coding.ExternalOrigin{External: true, Source: "appUser:" + issue.Creator.ID, Label: firstNonEmpty(issue.Creator.DisplayName, issue.Creator.Name)}
	}
	return coding.ExternalOrigin{}
}
