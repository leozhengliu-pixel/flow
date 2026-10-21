package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// Session triggers from issues and pull/merge requests (docs/coding-sessions-design.md §15).
//
// Owner: track L (triggers & PR follow-ups). Keep the route list in sync with
// publicapi.InternalRoutes.
func (s *server) registerCodingTriggerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/issues/{id}/coding-session", s.startIssueCodingSession)
	// Issue writes under /api/issues are rewritten to /api/issue-records
	// before dispatch (boundedLegacyIssueWrites), so both paths are served.
	mux.HandleFunc("POST /api/issue-records/{id}/coding-session", s.issueRecordAlias(s.startIssueCodingSession))
	mux.HandleFunc("POST /api/reviews/{id}/coding-session", s.startReviewCodingSession)
}

// codingTriggerStatus maps a trigger error code to an HTTP status.
func codingTriggerStatus(code domain.CodingErrorCode) int {
	switch code {
	case domain.CodingErrPermissionDenied, domain.CodingErrPersonalAccessDenied:
		return http.StatusForbidden
	case domain.CodingErrSandboxDisabled:
		return http.StatusServiceUnavailable
	case domain.CodingErrQuotaExceeded:
		return http.StatusPaymentRequired
	case domain.CodingErrApprovalExpired:
		return http.StatusGone
	case domain.CodingErrNoPolicyRepos, domain.CodingErrRepoNotInPolicy, domain.CodingErrNoCodeAccess:
		return http.StatusConflict
	case domain.CodingErrInternal:
		return http.StatusBadRequest
	default:
		return http.StatusConflict
	}
}

// writeCodingTriggerError answers a trigger failure with {error, code}.
func writeCodingTriggerError(w http.ResponseWriter, err error) {
	if code := coding.TriggerErrorCode(err); code != "" {
		var triggerError *coding.TriggerError
		errors.As(err, &triggerError)
		writeJSON(w, codingTriggerStatus(code), map[string]any{"error": triggerError.Message, "code": code})
		return
	}
	if errors.Is(err, coding.ErrNotFound) {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if errors.Is(err, coding.ErrVersionConflict) {
		// A concurrent change to the session's agent task: nothing was
		// created, and retrying with the same key is safe.
		writeJSON(w, http.StatusConflict, map[string]any{"error": "The session changed while starting; try again.", "code": "conflict"})
		return
	}
	writeError(w, http.StatusInternalServerError, "Coding session could not be started")
}

// codingNotificationOnly reports whether err only says a notification
// failed after the trigger or decision committed, and logs it. The caller
// answers with the committed outcome (TriggerResult.NotificationPending
// tells the client); repeating the request posts the notification once.
func codingNotificationOnly(err error) bool {
	if !coding.NotificationFailed(err) {
		return false
	}
	log.Printf("%v (the action committed; repeating it posts the notification)", err)
	return true
}

func writeCodingTriggerResult(w http.ResponseWriter, result coding.TriggerResult) {
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}

// codingTriggerRequestContext checks the workspace policy for an HTTP
// trigger and returns the acting user and the workspace metadata.
func (s *server) codingTriggerRequestContext(w http.ResponseWriter, r *http.Request) (domain.User, domain.Bootstrap, bool) {
	workspace := workspaceKey(r)
	data := s.workspaceData(r)
	settings, ok := s.store.WorkspaceMetadataFields(workspace, "workspaceSettings", "asks", "emailIntakeMessages", "reviews", "teams")
	if !ok {
		writeError(w, http.StatusNotFound, "Workspace not found")
		return domain.User{}, data, false
	}
	if !workspaceFeatureEnabled(settings.WorkspaceSettings, "coding-sessions") || !s.codingEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Coding sessions are not enabled.", "code": domain.CodingErrSandboxDisabled})
		return domain.User{}, data, false
	}
	if err := agentWorkspacePolicy(settings.WorkspaceSettings, data.ViewerRole); err != nil || data.ViewerRole == "guest" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "You cannot start coding sessions in this workspace.", "code": domain.CodingErrPermissionDenied})
		return domain.User{}, data, false
	}
	if data.Workspace.URLKey == "" {
		data.Workspace = settings.Workspace
	}
	data.WorkspaceSettings = settings.WorkspaceSettings
	data.Asks, data.EmailIntakeMessages = settings.Asks, settings.EmailIntakeMessages
	if len(data.Reviews) == 0 {
		// Reviews only rank repositories here; the review route reads the
		// viewer's own projection (startReviewCodingSession).
		data.Reviews = settings.Reviews
	}
	if len(data.Teams) == 0 {
		data.Teams = settings.Teams
	}
	return requestActor(s, r), data, true
}

type issueCodingSessionInput struct {
	Prompt        string `json:"prompt"`
	EnvironmentID string `json:"environmentId"`
	Harness       string `json:"harness"`
	Model         string `json:"model"`
}

// startIssueCodingSession starts (or continues) a session for an issue; the
// UI's "Start coding session" and the repository choice of a waiting session
// use it. Body: {prompt?, environmentId?, harness?, model?}; header
// Idempotency-Key makes retries safe.
func (s *server) startIssueCodingSession(w http.ResponseWriter, r *http.Request) {
	actor, data, ok := s.codingTriggerRequestContext(w, r)
	if !ok {
		return
	}
	var input issueCodingSessionInput
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	workspace := firstNonEmpty(workspaceKey(r), data.Workspace.URLKey)
	issue, err := s.store.IssueRecord(r.Context(), workspace, r.PathValue("id"))
	if err != nil {
		if issue, err = s.issueByIdentifier(r.Context(), workspace, r.PathValue("id")); err != nil {
			writeError(w, http.StatusNotFound, "Issue not found")
			return
		}
	}
	if issuePermissionRank(issueRole(s, data, issue)) < issuePermissionRank("editor") {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "You cannot change this issue.", "code": domain.CodingErrPermissionDenied})
		return
	}
	triggers, ok := s.codingTriggers(workspace)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Coding sessions are not available yet.", "code": domain.CodingErrSandboxDisabled})
		return
	}
	trigger := s.issueCodingTrigger(r.Context(), workspace, data, issue, actor)
	trigger.Request = strings.TrimSpace(input.Prompt)
	trigger.Prompt = coding.IssuePrompt(*trigger.Issue, input.Prompt)
	trigger.EnvironmentID = strings.TrimSpace(input.EnvironmentID)
	trigger.Harness, trigger.Model = strings.TrimSpace(input.Harness), strings.TrimSpace(input.Model)
	trigger.IdempotencyKey = issueCodingIdempotencyKey(trigger, issue.ID, r.Header.Get("Idempotency-Key"))
	result, err := triggers.StartForIssue(r.Context(), trigger)
	if err != nil && !codingNotificationOnly(err) {
		writeCodingTriggerError(w, err)
		return
	}
	writeCodingTriggerResult(w, result)
}

// issueCodingIdempotencyKey is the session key of an issue trigger. A
// delegated issue has one session per delegation (its AgentTask anchor),
// whoever starts it: the UI's explicit start, the agent's tool or a retry
// all share "delegation:<task>", so they converge on one session even when
// they race. Otherwise the client's Idempotency-Key scopes retries.
func issueCodingIdempotencyKey(trigger coding.IssueTrigger, issueID, header string) string {
	if trigger.AnchorTaskID != "" {
		return "delegation:" + trigger.AnchorTaskID
	}
	if key := strings.TrimSpace(header); key != "" {
		return "issue:" + issueID + ":" + key
	}
	return trigger.IdempotencyKey
}

// issueCodingTrigger fills an IssueTrigger from an issue: origin, linked
// repositories, labels, team, the team's recent environments, and the
// delegation task as anchor when the issue is delegated to the Flow agent.
func (s *server) issueCodingTrigger(ctx context.Context, workspace string, data domain.Bootstrap, issue domain.Issue, actor domain.User) coding.IssueTrigger {
	labels := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		labels = append(labels, label.Name)
	}
	issueContext := &coding.IssueContext{
		ID: issue.ID, Identifier: issue.Identifier, Title: issue.Title, Description: issue.Description,
		TeamID: issue.Team.ID, TeamKey: issue.Team.Key, TeamName: issue.Team.Name, Labels: labels,
		LinkedRepositories: linkedRepositoriesForIssue(data, issue), Origin: issueExternalOrigin(data, issue),
	}
	if data.Workspace.URLKey != "" && issue.Identifier != "" {
		issueContext.URL = strings.TrimRight(s.loopAppURL(), "/") + "/" + url.PathEscape(data.Workspace.URLKey) + "/issue/" + url.PathEscape(issue.Identifier)
	}
	trigger := coding.IssueTrigger{
		WorkspaceKey: workspace, Settings: data.WorkspaceSettings.CodingAgentSettings, SpendLimits: data.WorkspaceSettings.SpendLimits,
		Source: domain.CodingSourceIssue, SourceID: issue.ID, Issue: issueContext, CreatorID: actor.ID,
		AnchorAppUserID: s.builtinAgentUserID(ctx, workspace), RecentEnvironmentIDs: s.recentTeamEnvironments(ctx, workspace, issue.Team.ID),
	}
	if issue.Delegate != nil && issue.Delegate.BuiltinAgent && issue.AgentSessionID != "" {
		trigger.AnchorTaskID = issue.AgentSessionID
		trigger.IdempotencyKey = "delegation:" + issue.AgentSessionID
	}
	return trigger
}

// recentTeamEnvironments lists the environments of the team's recent
// sessions, newest first.
func (s *server) recentTeamEnvironments(ctx context.Context, workspace, teamID string) []string {
	stores, ok := s.codingTriggerStores()
	if !ok || teamID == "" {
		return nil
	}
	sessions, err := stores.triggers.ListCodingSessions(ctx, workspace, coding.SessionFilter{Limit: 30})
	if err != nil {
		return nil
	}
	teams := map[string]string{}
	var ordered []domain.CodingSession
	for _, session := range sessions {
		if session.EnvironmentID == "" || session.IssueID == "" {
			continue
		}
		if _, seen := teams[session.IssueID]; !seen {
			team := ""
			if issue, err := s.store.IssueRecord(ctx, workspace, session.IssueID); err == nil {
				team = issue.Team.ID
			}
			teams[session.IssueID] = team
		}
		if teams[session.IssueID] == teamID {
			ordered = append(ordered, session)
		}
	}
	// Newest first, one entry per environment.
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j].CreatedAt.After(ordered[j-1].CreatedAt); j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	var ids []string
	seen := map[string]bool{}
	for _, session := range ordered {
		if !seen[session.EnvironmentID] {
			seen[session.EnvironmentID] = true
			ids = append(ids, session.EnvironmentID)
		}
	}
	return ids
}

type reviewCodingSessionInput struct {
	Kind             string `json:"kind"`
	Prompt           string `json:"prompt"`
	EnvironmentID    string `json:"environmentId"`
	ConfirmForcePush bool   `json:"confirmForcePush"`
}

// startReviewCodingSession is "Fix this PR/MR" and the review page's quick
// actions: {kind: fix|followUp|rebase|lint|test, prompt?, environmentId?,
// confirmForcePush?}. It continues the session linked to the PR/MR or starts
// a pullRequest session on its head branch, and links the PR to it.
func (s *server) startReviewCodingSession(w http.ResponseWriter, r *http.Request) {
	actor, data, ok := s.codingTriggerRequestContext(w, r)
	if !ok {
		return
	}
	var input reviewCodingSessionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	review, err := reviewByID(s.workspaceData(r), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "Review not found")
		return
	}
	workspace := firstNonEmpty(workspaceKey(r), data.Workspace.URLKey)
	triggers, ok := s.codingTriggers(workspace)
	stores, storesOK := s.codingTriggerStores()
	if !ok || !storesOK || stores.webhooks == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Coding sessions are not available yet.", "code": domain.CodingErrSandboxDisabled})
		return
	}
	trigger := coding.ChangeRequestTrigger{
		WorkspaceKey: workspace, Action: strings.TrimSpace(input.Kind), Prompt: input.Prompt, ConfirmForcePush: input.ConfirmForcePush,
		ChangeRequest: changeRequestFromReview(review), ReviewID: review.ID, EnvironmentID: strings.TrimSpace(input.EnvironmentID),
		CreatorID: actor.ID, AnchorAppUserID: s.builtinAgentUserID(r.Context(), workspace),
	}
	if len(review.IssueIDs) > 0 {
		trigger.IssueID = review.IssueIDs[0]
		if issue, err := s.store.IssueRecord(r.Context(), workspace, trigger.IssueID); err == nil {
			trigger.TeamID = issue.Team.ID
		}
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		trigger.IdempotencyKey = "review:" + review.ID + ":" + key
	}
	result, err := triggers.StartForChangeRequest(r.Context(), stores.webhooks, trigger)
	if err != nil && !codingNotificationOnly(err) {
		writeCodingTriggerError(w, err)
		return
	}
	writeCodingTriggerResult(w, result)
}

// changeRequestFromReview converts a synced CodeReview to the code host
// shape (the host name comes from the review URL).
func changeRequestFromReview(review domain.CodeReview) codehost.ChangeRequest {
	hostName := ""
	if parsed, err := url.Parse(review.URL); err == nil {
		hostName = parsed.Host
	}
	codeHost := strings.ToLower(review.Provider)
	if hostName == "" {
		hostName = map[string]string{domain.CodeHostGitHub: "github.com", domain.CodeHostGitLab: "gitlab.com"}[codeHost]
	}
	return codehost.ChangeRequest{
		Repo:   codehost.RepoRef{CodeHost: codeHost, HostName: hostName, FullName: strings.Trim(review.RepositoryOwner+"/"+review.RepositoryName, "/")},
		Number: review.Number, ExternalID: review.ExternalID, URL: review.URL, Title: review.Title, State: review.Status,
		Draft: review.Draft, HeadBranch: review.HeadBranch, BaseBranch: review.BaseBranch, AuthorLogin: firstNonEmpty(review.Author.Name),
	}
}
