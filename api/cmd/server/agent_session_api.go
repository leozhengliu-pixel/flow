package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Agent session API additions that follow the reference app's agent interaction docs:
// sessions an agent opens itself (on an issue or a comment), external URLs
// and a plan on a session, and the AgentSessionEvent payload with
// promptContext, previousComments, guidance and a signed timestamp.

type agentSessionUpdateInput struct {
	ExternalURLs        *[]domain.AgentExternalURL `json:"externalUrls,omitempty"`
	AddedExternalURLs   []domain.AgentExternalURL  `json:"addedExternalUrls,omitempty"`
	RemovedExternalURLs []string                   `json:"removedExternalUrls,omitempty"`
	// ExternalLink is the deprecated single link; it replaces externalUrls.
	ExternalLink *string                 `json:"externalLink,omitempty"`
	Plan         *[]domain.AgentPlanStep `json:"plan,omitempty"`
}

const (
	maxAgentExternalURLs = 20
	maxAgentPlanSteps    = 50
)

func validAgentExternalURLs(values []domain.AgentExternalURL) error {
	if len(values) > maxAgentExternalURLs {
		return fmt.Errorf("a session can link at most %d external URLs", maxAgentExternalURLs)
	}
	seen := map[string]bool{}
	for _, item := range values {
		if !validOAuthMetadataURI(item.URL) || len(item.URL) > 2048 {
			return fmt.Errorf("external URLs must use HTTPS")
		}
		if len(item.Label) > 200 {
			return fmt.Errorf("external URL labels must not exceed 200 characters")
		}
		if seen[item.URL] {
			return fmt.Errorf("external URLs must be unique")
		}
		seen[item.URL] = true
	}
	return nil
}

func validAgentPlan(steps []domain.AgentPlanStep) error {
	if len(steps) > maxAgentPlanSteps {
		return fmt.Errorf("a plan can have at most %d steps", maxAgentPlanSteps)
	}
	for _, step := range steps {
		if strings.TrimSpace(step.Content) == "" || len(step.Content) > 1000 {
			return fmt.Errorf("plan steps need content of at most 1000 characters")
		}
		if !slices.Contains([]string{"pending", "inProgress", "completed", "canceled"}, step.Status) {
			return fmt.Errorf("plan step status must be pending, inProgress, completed or canceled")
		}
	}
	return nil
}

// updateApplicationAgentTask is agentSessionUpdate: the session's agent sets
// external URLs (whole list, or added/removed) and its plan. Setting a URL
// acknowledges the session like an activity does.
func (s *server) updateApplicationAgentTask(w http.ResponseWriter, r *http.Request) {
	task, data, err := s.authorizedAgentTask(r, r.PathValue("id"), true)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	if !data.Viewer.App || data.Viewer.ID != task.AppUserID {
		writeError(w, http.StatusForbidden, "Only the session application may update the session")
		return
	}
	var input agentSessionUpdateInput
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ExternalLink != nil {
		links := []domain.AgentExternalURL{}
		if link := strings.TrimSpace(*input.ExternalLink); link != "" {
			links = append(links, domain.AgentExternalURL{URL: link})
		}
		input.ExternalURLs = &links
	}
	if input.ExternalURLs != nil {
		if err := validAgentExternalURLs(*input.ExternalURLs); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validAgentExternalURLs(input.AddedExternalURLs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Plan != nil {
		if err := validAgentPlan(*input.Plan); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	var invalid error
	updated, err := s.store.UpdateAgentTaskSession(r.Context(), task.WorkspaceKey, task.ID, func(current *domain.AgentTask) error {
		urls := current.ExternalURLs
		if input.ExternalURLs != nil {
			urls = slices.Clone(*input.ExternalURLs)
		}
		urls = slices.DeleteFunc(slices.Clone(urls), func(item domain.AgentExternalURL) bool { return slices.Contains(input.RemovedExternalURLs, item.URL) })
		for _, item := range input.AddedExternalURLs {
			index := slices.IndexFunc(urls, func(existing domain.AgentExternalURL) bool { return existing.URL == item.URL })
			if index >= 0 {
				urls[index] = item
			} else {
				urls = append(urls, item)
			}
		}
		if err := validAgentExternalURLs(urls); err != nil {
			invalid = err
			return err
		}
		touchedURLs := input.ExternalURLs != nil || len(input.AddedExternalURLs) > 0 || len(input.RemovedExternalURLs) > 0
		current.ExternalURLs = urls
		if input.Plan != nil {
			current.Plan = slices.Clone(*input.Plan)
		}
		if touchedURLs && len(urls) > 0 {
			now := time.Now().UTC()
			current.LastActivityAt = &now
		}
		return nil
	})
	if invalid != nil {
		writeError(w, http.StatusBadRequest, invalid.Error())
		return
	}
	if err == store.ErrIssueVersion {
		writeError(w, http.StatusConflict, "Session changed; retry the update")
		return
	}
	updated.DeriveTimedState(time.Now())
	respondMutation(w, err, http.StatusOK, updated)
}

// createApplicationAgentTask is agentSessionCreateOnIssue and
// agentSessionCreateOnComment: an agent opens a session itself.
func (s *server) createApplicationAgentTask(w http.ResponseWriter, r *http.Request) {
	actor := requestActor(s, r)
	if !actor.App {
		writeError(w, http.StatusForbidden, "Only an application can open an agent session")
		return
	}
	var input struct {
		IssueID      string                    `json:"issueId"`
		CommentID    string                    `json:"commentId"`
		ExternalURLs []domain.AgentExternalURL `json:"externalUrls,omitempty"`
		ExternalLink string                    `json:"externalLink,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if (input.IssueID == "") == (input.CommentID == "") {
		writeError(w, http.StatusBadRequest, "Provide either issueId or commentId")
		return
	}
	if input.ExternalLink != "" {
		input.ExternalURLs = append(input.ExternalURLs, domain.AgentExternalURL{URL: input.ExternalLink})
	}
	if err := validAgentExternalURLs(input.ExternalURLs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	workspace := workspaceKey(r)
	if workspace == "" {
		if metadata, ok := s.store.WorkspaceMetadata(""); ok {
			workspace = metadata.Workspace.URLKey
		}
	}
	issueID := input.IssueID
	if input.CommentID != "" {
		resource, _, err := s.store.CommentResource(r.Context(), workspace, input.CommentID)
		if err != nil {
			writeError(w, http.StatusNotFound, "Comment not found")
			return
		}
		issueID = resource
	}
	data, err := s.agentIssueContext(r, []string{issueID})
	if err != nil || len(data.Issues) != 1 {
		writeError(w, http.StatusNotFound, "Issue not found")
		return
	}
	issue := data.Issues[0]
	app, err := s.store.ApplicationByUser(r.Context(), actor.ID)
	if err != nil || !app.Active || !slices.Contains(app.AccessTeamIDs(), issue.Team.ID) {
		writeError(w, http.StatusForbidden, "The application cannot access this issue's team")
		return
	}
	now := time.Now().UTC()
	task := domain.AgentTask{
		ID: fmt.Sprintf("agent_session_%d", now.UnixNano()), WorkspaceKey: workspace, IssueID: issue.ID, TeamID: issue.Team.ID, AppUserID: actor.ID,
		CreatorID: actor.ID, CommentID: input.CommentID, Prompt: issue.Title, ExternalURLs: input.ExternalURLs,
	}
	created, err := s.store.CreateAgentInitiatedTask(r.Context(), task)
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// agentSessionTarget is where AgentSessionEvent webhooks of an installation
// go: the installation's own URL (set by the installing admin), else the
// application's webhook when it subscribes to agent session events.
type agentSessionTarget struct {
	url, secret string
	webhook     *domain.Webhook
}

func (s *server) agentSessionTarget(ctx context.Context, app domain.ApplicationInstallation) (agentSessionTarget, error) {
	if app.WebhookURL != "" {
		secret, err := s.store.ApplicationWebhookSecret(ctx, app.ID)
		return agentSessionTarget{url: app.WebhookURL, secret: secret}, err
	}
	hooks, _ := s.store.WorkspaceMetadataFields(app.WorkspaceKey, "webhooks")
	for _, hook := range hooks.Webhooks {
		if hook.ManagedBy == "application" && hook.ApplicationClientID == app.ClientID && hook.Enabled && slices.Contains(hook.ResourceTypes, "agent_session_events") {
			item := hook
			return agentSessionTarget{url: hook.URL, webhook: &item}, nil
		}
	}
	return agentSessionTarget{}, nil
}

// sendAgentSessionEvent delivers a created or prompted event. It reports
// whether the agent accepted it (2xx within 5 seconds).
func (s *server) sendAgentSessionEvent(ctx context.Context, app domain.ApplicationInstallation, task domain.AgentTask, data domain.Bootstrap, target agentSessionTarget) bool {
	now := time.Now().UTC()
	body, eventID := s.agentSessionEventBody(ctx, app, task, data, target, now)
	if target.webhook != nil {
		// Through the application's webhook: logged in its delivery log and
		// signed with the application's webhook secret.
		delivery := domain.WebhookDelivery{
			ID: webhookDeliveryID(target.webhook.ID, fmt.Sprintf("%s:%d", eventID, now.UnixNano())), WorkspaceKey: app.WorkspaceKey, WebhookID: target.webhook.ID,
			SecretRef: webhookSecretRef(*target.webhook), Kind: domain.WebhookDeliveryKindEvent, EventID: eventID, EventType: "AgentSessionEvent",
			Action: agentSessionAction(task), URL: target.url, Payload: body, MaxAttempts: 1, CreatedAt: now, NextAttemptAt: &now,
		}
		if _, err := s.store.EnqueueWebhookDelivery(ctx, delivery); err != nil {
			return false
		}
		completed, claimed := s.runWebhookDelivery(ctx, delivery.ID, now)
		return claimed && completed.Status == domain.WebhookDeliverySucceeded
	}
	if !integrationEndpointSafe(ctx, target.url, s.authDisabled) {
		return false
	}
	timestamp := now.UnixMilli()
	signed := webhookBodyWithTimestamp(body, timestamp)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.url, bytes.NewReader(signed))
	if err != nil {
		return false
	}
	setWebhookHeaders(request.Header, domain.WebhookDelivery{ID: eventID, EventType: "AgentSessionEvent"}, timestamp, target.secret, signed)
	client := secureOutboundClient(webhookDeliveryTimeout)
	if s.authDisabled && safeLocalDevelopmentURL(target.url) {
		client = &http.Client{Timeout: webhookDeliveryTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func agentSessionAction(task domain.AgentTask) string {
	if task.Version > 1 {
		return "prompted"
	}
	return "created"
}

// agentSessionEventBody builds the AgentSessionEvent payload (without
// webhookTimestamp, added when sent).
func (s *server) agentSessionEventBody(ctx context.Context, app domain.ApplicationInstallation, task domain.AgentTask, data domain.Bootstrap, target agentSessionTarget, now time.Time) (json.RawMessage, string) {
	eventID := fmt.Sprintf("%s:%d", task.ID, task.Version)
	session := map[string]any{}
	if raw, err := json.Marshal(task); err == nil {
		_ = json.Unmarshal(raw, &session)
	}
	payload := map[string]any{
		"type": "AgentSessionEvent", "action": agentSessionAction(task), "eventId": eventID, "createdAt": now.Format("2006-01-02T15:04:05.000Z07:00"),
		"organizationId": data.Workspace.ID, "oauthClientId": app.ClientID, "appUserId": app.UserID,
	}
	if target.webhook != nil {
		payload["webhookId"] = target.webhook.ID
	} else {
		payload["webhookId"] = app.ID
	}
	var issue *domain.Issue
	var document *domain.Document
	var project *domain.Project
	if len(data.Issues) == 1 {
		issue = &data.Issues[0]
		payload["issue"] = data.Issues[0]
		session["issue"] = data.Issues[0]
	} else if task.ResourceType == "document" && len(data.Documents) == 1 {
		loaded := s.documentWithVersions(ctx, data.Workspace.URLKey, data.Documents[0])
		document = &loaded
		payload["document"] = loaded
		session["document"] = loaded
	} else if task.ResourceType == "project" && len(data.Projects) == 1 {
		project = &data.Projects[0]
		payload["project"] = data.Projects[0]
		session["project"] = data.Projects[0]
	}
	if creator := userByID(&data, task.CreatorID); creator != nil {
		session["creator"] = map[string]any{"id": creator.ID, "name": firstNonEmpty(creator.DisplayName, creator.Name), "email": creator.Email}
	}
	// The discussion the session started in: issue and document comments are
	// content records keyed by the resource; a project's own comments live on
	// the project record.
	var comments []domain.Comment
	switch {
	case issue != nil:
		comments, _, _ = s.store.IssueContent(ctx, task.WorkspaceKey, issue.ID)
	case document != nil:
		comments, _, _ = s.store.IssueContent(ctx, task.WorkspaceKey, document.ID)
	case project != nil:
		comments = agentProjectComments(&data, project.Comments)
	}
	var trigger *domain.Comment
	if task.CommentID != "" {
		if index := slices.IndexFunc(comments, func(item domain.Comment) bool { return item.ID == task.CommentID }); index >= 0 {
			trigger = &comments[index]
			session["comment"] = agentCommentPayload(*trigger)
		}
	}
	previous := agentPreviousComments(comments, trigger)
	payload["previousComments"] = previous
	guidanceTeamID := task.TeamID
	if issue != nil && issue.Team.ID != "" {
		guidanceTeamID = issue.Team.ID
	}
	guidance := agentGuidanceRules(data, guidanceTeamID)
	payload["guidance"] = guidance
	payload["agentSession"] = session
	if task.Version > 1 {
		if activity, ok := s.latestAgentPrompt(ctx, task); ok {
			payload["agentActivity"] = activity
		}
	}
	payload["promptContext"] = agentResourceContextXML(document, project) + agentPromptContextXML(issue, trigger, comments, guidance)
	raw, _ := json.Marshal(payload)
	return raw, eventID
}

// agentProjectComments copies a project's own comments with their authors
// resolved, the way IssueContent resolves issue and document comments.
func agentProjectComments(data *domain.Bootstrap, comments []domain.Comment) []domain.Comment {
	result := slices.Clone(comments)
	for i := range result {
		if user := userByID(data, result[i].User.ID); user != nil {
			result[i].User = *user
		}
	}
	return result
}

// agentResourceContextXML describes the document or project a session was
// started on, ahead of its threads, the way the issue block describes an issue.
func agentResourceContextXML(document *domain.Document, project *domain.Project) string {
	escape := html.EscapeString
	switch {
	case document != nil:
		out := fmt.Sprintf("<document id=\"%s\">\n<title>%s</title>\n", escape(document.ID), escape(document.Title))
		if content := strings.TrimSpace(document.Content); content != "" {
			out += fmt.Sprintf("<content>%s</content>\n", escape(truncateSettingsText(content, 8000)))
		}
		return out + "</document>\n"
	case project != nil:
		out := fmt.Sprintf("<project id=\"%s\">\n<name>%s</name>\n", escape(project.ID), escape(project.Name))
		if description := strings.TrimSpace(project.Description); description != "" {
			out += fmt.Sprintf("<description>%s</description>\n", escape(truncateSettingsText(description, 8000)))
		}
		return out + "</project>\n"
	}
	return ""
}

func agentCommentPayload(comment domain.Comment) map[string]any {
	return map[string]any{"id": comment.ID, "body": comment.Body, "userId": comment.User.ID, "userName": firstNonEmpty(comment.User.DisplayName, comment.User.Name), "parentId": comment.ParentID, "createdAt": comment.CreatedAt}
}

// agentPreviousComments are the comments of the triggering comment's thread
// before it (the whole thread when the session was not started from a
// comment is not included; delegation sessions get none).
func agentPreviousComments(comments []domain.Comment, trigger *domain.Comment) []map[string]any {
	result := []map[string]any{}
	if trigger == nil {
		return result
	}
	root := trigger.ID
	if trigger.ParentID != nil && *trigger.ParentID != "" {
		root = *trigger.ParentID
	}
	thread := []domain.Comment{}
	for _, comment := range comments {
		if comment.ID == trigger.ID {
			continue
		}
		if comment.ID == root || comment.ParentID != nil && *comment.ParentID == root {
			if !comment.CreatedAt.After(trigger.CreatedAt) {
				thread = append(thread, comment)
			}
		}
	}
	sort.SliceStable(thread, func(i, j int) bool { return thread[i].CreatedAt.Before(thread[j].CreatedAt) })
	if len(thread) > 50 {
		thread = thread[len(thread)-50:]
	}
	for _, comment := range thread {
		result = append(result, agentCommentPayload(comment))
	}
	return result
}

// agentGuidanceRules lists the guidance an agent should follow, like
// The reference app's `guidance`: the workspace's agent guidance (Settings › Agents),
// then the guidance of the session's team's parent teams and of the team
// itself (Team settings › Agents), most specific last. Team rules carry the
// team's id, key and name.
func agentGuidanceRules(data domain.Bootstrap, teamID string) []map[string]any {
	rules := []map[string]any{}
	for _, rule := range agentGuidanceChain(data, teamID) {
		item := map[string]any{"body": rule.Body, "origin": rule.Origin}
		if rule.Origin == "team" {
			item["teamId"], item["teamKey"], item["teamName"] = rule.TeamID, rule.TeamKey, rule.TeamName
		}
		rules = append(rules, item)
	}
	return rules
}

func (s *server) latestAgentPrompt(ctx context.Context, task domain.AgentTask) (domain.AgentActivity, bool) {
	var latest domain.AgentActivity
	found := false
	for after := ""; ; {
		page, err := s.store.AgentActivities(ctx, task.WorkspaceKey, task.ID, after)
		if err != nil || len(page) == 0 {
			break
		}
		for _, activity := range page {
			if activity.Type == "prompt" {
				latest, found = activity, true
			}
		}
		if len(page) < 100 {
			break
		}
		after = page[len(page)-1].ID
	}
	return latest, found
}

// agentPromptContextXML is the formatted context the reference app sends as
// promptContext: the issue, the thread that triggered the session, other
// threads and the guidance rules.
func agentPromptContextXML(issue *domain.Issue, trigger *domain.Comment, comments []domain.Comment, guidance []map[string]any) string {
	var out strings.Builder
	escape := html.EscapeString
	if issue != nil {
		fmt.Fprintf(&out, "<issue identifier=\"%s\">\n<title>%s</title>\n", escape(issue.Identifier), escape(issue.Title))
		if description := strings.TrimSpace(issue.Description); description != "" {
			fmt.Fprintf(&out, "<description>%s</description>\n", escape(truncateSettingsText(description, 8000)))
		}
		fmt.Fprintf(&out, "<team name=\"%s\"/>\n", escape(issue.Team.Name))
		for _, label := range issue.Labels {
			fmt.Fprintf(&out, "<label>%s</label>\n", escape(label.Name))
		}
		if issue.ParentID != nil && *issue.ParentID != "" {
			fmt.Fprintf(&out, "<parent-issue id=\"%s\"/>\n", escape(*issue.ParentID))
		}
		if issue.Project != nil {
			fmt.Fprintf(&out, "<project name=\"%s\"/>\n", escape(issue.Project.Name))
		}
		out.WriteString("</issue>\n")
	}
	writeThread := func(tag string, root string, items []domain.Comment) {
		fmt.Fprintf(&out, "<%s comment-id=\"%s\">\n", tag, escape(root))
		for _, comment := range items {
			fmt.Fprintf(&out, "<comment author=\"%s\" created-at=\"%s\">\n<user id=\"%s\"/>\n%s\n</comment>\n", escape(firstNonEmpty(comment.User.DisplayName, comment.User.Name)), comment.CreatedAt.UTC().Format(time.RFC3339), escape(comment.User.ID), escape(truncateSettingsText(comment.Body, 4000)))
		}
		fmt.Fprintf(&out, "</%s>\n", tag)
	}
	threads := map[string][]domain.Comment{}
	order := []string{}
	for _, comment := range comments {
		root := comment.ID
		if comment.ParentID != nil && *comment.ParentID != "" {
			root = *comment.ParentID
		}
		if _, ok := threads[root]; !ok {
			order = append(order, root)
		}
		threads[root] = append(threads[root], comment)
	}
	primary := ""
	if trigger != nil {
		primary = trigger.ID
		if trigger.ParentID != nil && *trigger.ParentID != "" {
			primary = *trigger.ParentID
		}
		writeThread("primary-directive-thread", primary, threads[primary])
	}
	others := 0
	for index := len(order) - 1; index >= 0 && others < 10; index-- {
		if order[index] == primary {
			continue
		}
		writeThread("other-thread", order[index], threads[order[index]])
		others++
	}
	if len(guidance) > 0 {
		out.WriteString("<guidance>\n")
		for _, rule := range guidance {
			origin, _ := rule["origin"].(string)
			body, _ := rule["body"].(string)
			if team, _ := rule["teamName"].(string); team != "" {
				fmt.Fprintf(&out, "<guidance-rule origin=\"%s\" team-name=\"%s\">%s</guidance-rule>\n", escape(origin), escape(team), escape(body))
			} else {
				fmt.Fprintf(&out, "<guidance-rule origin=\"%s\">%s</guidance-rule>\n", escape(origin), escape(body))
			}
		}
		out.WriteString("</guidance>\n")
	}
	return out.String()
}
