package main

import (
	"context"
	"log"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Agent replies in the Inbox: when an agent answers in a thread
// (a response, a question it waits on, or an error), the person who started
// the session and the thread's participants and subscribers get one
// notification per thread — "Flow replied: <first line>" — that is updated in
// place and marked unread again on every new reply instead of piling up.
// Threads on issues, documents and projects (the project's own comments)
// work the same way; only the record the notification points at differs.

const agentReplyNotificationType = "agentReply"

// notifyAgentReply is the store's agent reply listener.
func (s *server) notifyAgentReply(ctx context.Context, task domain.AgentTask, activity domain.AgentActivity) {
	resourceType, resourceID := firstNonEmpty(task.ResourceType, "issue"), task.ResourceID
	if resourceType == "issue" {
		resourceID = task.IssueID
	}
	if resourceID == "" || resourceType != "issue" && resourceType != "document" && resourceType != "project" {
		return
	}
	app, err := s.store.ApplicationByUser(ctx, task.AppUserID)
	if err != nil {
		return
	}
	agent := app.User()
	prompters := []string{}
	for after, page := "", 0; page < 20; page++ {
		history, err := s.store.AgentActivities(ctx, task.WorkspaceKey, task.ID, after)
		if err != nil {
			break
		}
		for _, item := range history {
			if item.Type == "prompt" && item.ActorID != "" {
				prompters = appendUnique(prompters, item.ActorID)
			}
		}
		if len(history) < 100 {
			break
		}
		after = history[len(history)-1].ID
	}
	coding := s.codingSessionForAnchor(ctx, task)
	// The write is the agent's own, scoped to the resource's records; it must
	// not be cut short by the agent run's request ending.
	write := context.WithoutCancel(ctx)
	if resourceType == "issue" {
		write = store.WithIssueRecordMutations(write, resourceID)
	} else {
		write = store.WithMutationScope(write, store.MutationScope{Resources: []string{resourceID}})
	}
	write = store.ContextWithActor(write, agent)
	directory := memberDirectory{}
	if resourceType != "issue" {
		directory = s.workspaceMemberDirectory(write, task.WorkspaceKey)
	}
	now := time.Now().UTC()
	err = s.store.MutateWorkspace(write, task.WorkspaceKey, "agent_task.replied", resourceID, map[string]string{"taskId": task.ID, "activityId": activity.ID}, func(data *domain.Bootstrap) error {
		thread, err := agentReplyThreadFor(s, data, directory, resourceType, resourceID, task.CommentID)
		if err != nil {
			return err
		}
		withCodingSessionAudience(&thread, coding)
		appendAgentReplyNotifications(data, thread, task, activity, agent, prompters, now)
		return nil
	})
	if err != nil {
		log.Printf("agent reply notification task=%s: %v", task.ID, err)
		return
	}
	s.dispatchNotificationEmails(write, task.WorkspaceKey)
}

// codingSessionForAnchor is the coding session a task anchors, if any.
func (s *server) codingSessionForAnchor(ctx context.Context, task domain.AgentTask) *domain.CodingSession {
	if task.CodingSessionID == "" || s.coding == nil || s.coding.service == nil {
		return nil
	}
	session, err := s.coding.service.Get(ctx, task.WorkspaceKey, task.CodingSessionID)
	if err != nil {
		return nil
	}
	return &session
}

// withCodingSessionAudience applies "Subscribe to session" to a coding
// session's thread: subscribers hear its replies (completion, questions,
// errors) and members who unsubscribed, the creator included, do not.
func withCodingSessionAudience(thread *agentReplyThread, session *domain.CodingSession) {
	if session == nil {
		return
	}
	if thread.muted == nil {
		thread.muted = map[string]bool{}
	}
	for _, id := range session.UnsubscribedIDs {
		thread.muted[id] = true
	}
	for _, id := range session.SubscriberIDs {
		if !thread.muted[id] {
			thread.watchers = appendUnique(thread.watchers, id)
		}
	}
}

// agentReplyThread is where an agent session's thread lives, as its Inbox
// notification records it: the issue, or the document or project that owns
// the notification (IssueID empty), plus the thread's audience.
type agentReplyThread struct {
	issueID, sourceType, sourceID, projectID string
	title, documentSlugID                    string
	watchers                                 []string
	muted                                    map[string]bool
	// user resolves a recipient; canOpen reports whether they can open the
	// resource (documents carry their own access list).
	user    func(id string) *domain.User
	canOpen func(id string) bool
}

func agentReplyThreadFor(s *server, data *domain.Bootstrap, directory memberDirectory, resourceType, resourceID, commentID string) (agentReplyThread, error) {
	thread := agentReplyThread{muted: map[string]bool{}, user: func(id string) *domain.User { return directory.user(data, id) }, canOpen: func(string) bool { return true }}
	switch resourceType {
	case "document":
		document, err := documentByID(data, resourceID)
		if err != nil {
			return thread, err
		}
		thread.sourceType, thread.sourceID = "document", document.ID
		thread.title, thread.documentSlugID = documentDisplayTitle(document.Title), document.SlugID
		comments := data.Comments[document.ID]
		if rootID, ok := threadRootID(comments, commentID); ok && commentID != "" {
			thread.watchers, thread.muted = documentThreadAudience(data, document.ID, comments, rootID)
		}
		thread.canOpen = func(id string) bool { return documentRoleFor(s, data, directory, *document, id) != "none" }
	case "project":
		project, err := fullProjectByID(data, resourceID)
		if err != nil {
			return thread, err
		}
		thread.sourceType, thread.sourceID, thread.projectID, thread.title = "project", project.ID, project.ID, project.Name
		if rootID, ok := threadRootID(project.Comments, commentID); ok && commentID != "" {
			thread.watchers, thread.muted = projectThreadAudience(data, *project, rootID)
		}
	default:
		issue, err := issueByID(data, resourceID)
		if err != nil {
			return thread, err
		}
		thread.issueID, thread.sourceType = issue.ID, "agentSession"
		thread.title = strings.TrimSpace(issue.Identifier + " " + issue.Title)
		if rootID, ok := threadRootID(data.Comments[issue.ID], commentID); ok && commentID != "" {
			thread.watchers, thread.muted = threadAudience(data, issue.ID, rootID)
		}
		thread.user = func(id string) *domain.User { return userByID(data, id) }
	}
	return thread, nil
}

// projectThreadAudience is threadAudience for a project comment thread: its
// participants and explicit subscribers, minus the users who muted it.
func projectThreadAudience(data *domain.Bootstrap, project domain.Project, rootID string) (watchers []string, muted map[string]bool) {
	muted = map[string]bool{}
	for _, item := range data.ThreadSubscriptions {
		if item.ProjectID == project.ID && item.IssueID == "" && item.CommentID == rootID && item.State == "muted" {
			muted[item.UserID] = true
		}
	}
	for _, comment := range project.Comments {
		if root, ok := threadRootID(project.Comments, comment.ID); ok && root == rootID && !muted[comment.User.ID] {
			watchers = appendUnique(watchers, comment.User.ID)
		}
	}
	for _, item := range data.ThreadSubscriptions {
		if item.ProjectID == project.ID && item.IssueID == "" && item.CommentID == rootID && item.State == "subscribed" && !muted[item.UserID] {
			watchers = appendUnique(watchers, item.UserID)
		}
	}
	return watchers, muted
}

// appendAgentReplyNotifications writes or refreshes each recipient's
// notification for the session's thread.
func appendAgentReplyNotifications(data *domain.Bootstrap, thread agentReplyThread, task domain.AgentTask, activity domain.AgentActivity, agent domain.User, prompters []string, now time.Time) {
	recipients := append(append([]string{task.CreatorID}, prompters...), thread.watchers...)
	excerpt := agentReplyExcerpt(activity.Body)
	agentName := firstNonEmpty(agent.DisplayName, agent.Name, "Agent")
	seen := map[string]bool{}
	for _, recipientID := range recipients {
		if recipientID == "" || seen[recipientID] || thread.muted[recipientID] || recipientID == agent.ID {
			continue
		}
		seen[recipientID] = true
		recipient := thread.user(recipientID)
		if recipient == nil || recipient.App || !thread.canOpen(recipientID) {
			continue
		}
		preferences, ok := data.NotificationPreferences[recipientID]
		if !ok {
			preferences = defaultPreferences(recipientID)
		}
		if !preferences.Inbox.Enabled || !categoryEnabled(preferences.Inbox, "comments") {
			continue
		}
		groupKey := recipientID + ":agent-session:" + task.ID
		index := slices.IndexFunc(data.Notifications, func(item domain.Notification) bool { return item.GroupKey == groupKey })
		if index < 0 {
			data.Notifications = append(data.Notifications, domain.Notification{
				ID: "notification_agent_" + task.ID + "_" + recipientID, RecipientID: recipientID, IssueID: thread.issueID, ProjectID: thread.projectID,
				Category: "comments", GroupKey: groupKey, CreatedAt: now,
			})
			index = len(data.Notifications) - 1
		}
		notification := &data.Notifications[index]
		notification.Type = agentReplyNotificationType
		notification.SourceType, notification.SourceID = thread.sourceType, firstNonEmpty(thread.sourceID, task.ID)
		notification.CommentID, notification.ActivityID = task.CommentID, activity.ID
		notification.Actor, notification.LatestActorIDs = agent, []string{agent.ID}
		notification.OccurrenceCount++
		notification.Title = thread.title
		kind := agentReplyKind(task, activity)
		notification.Text = agentReplyNotificationText(agentName, kind, excerpt)
		notification.Payload = &domain.NotificationPayload{Excerpt: excerpt, AgentActivity: kind, DocumentSlugID: thread.documentSlugID}
		// Every new reply brings the thread back to the top of the Inbox, unread.
		notification.ReadAt, notification.ArchivedAt, notification.DeletedAt, notification.SnoozedUntil = nil, nil, nil, nil
		notification.UpdatedAt = now
		enqueueNotificationDeliveries(data, *notification, preferences)
	}
}

// agentReplyKind is the notification's kind: the activity type, except that
// the final response of a delegated coding session (started by delegating
// the issue, not by a comment) is "finished".
func agentReplyKind(task domain.AgentTask, activity domain.AgentActivity) string {
	if activity.Type == "response" && task.CodingSessionID != "" && task.CommentID == "" {
		return "finished"
	}
	return activity.Type
}

// agentReplyNotificationText is the notification line in English; the web
// app localizes it from the type, payload.agentActivity and the excerpt.
func agentReplyNotificationText(agent, kind, excerpt string) string {
	prefix := agent + " replied"
	switch kind {
	case "finished":
		prefix = agent + " finished"
	case "elicitation":
		prefix = agent + " is waiting for your input"
	case "error":
		prefix = agent + " ran into an error"
	}
	if excerpt == "" {
		return prefix
	}
	return prefix + ": " + excerpt
}

var (
	agentSuggestionsFence  = regexp.MustCompile("(?s)(^|\n)[ \t]*```suggestions[^\n]*\n.*?(\n[ \t]*```[ \t]*(\n|$)|$)")
	agentReplyMarkdownLink = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	agentLinePrefix        = regexp.MustCompile(`^(#{1,6}\s+|>\s*|[-*+]\s+|\d+[.)]\s+)+`)
	agentEmphasis          = regexp.MustCompile("(\\*\\*|__|`)")
)

// stripAgentSuggestions removes a trailing ```suggestions block (follow-up
// chips only the chat panel shows) from an agent reply.
func stripAgentSuggestions(body string) string {
	return strings.TrimSpace(agentSuggestionsFence.ReplaceAllString(body, "\n"))
}

// agentReplyExcerpt is the reply's first line of text, without Markdown and
// the suggestions block, shortened for an Inbox row.
func agentReplyExcerpt(body string) string {
	for _, line := range strings.Split(stripAgentSuggestions(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		line = agentReplyMarkdownLink.ReplaceAllString(line, "$1")
		line = agentLinePrefix.ReplaceAllString(line, "")
		line = strings.TrimSpace(agentEmphasis.ReplaceAllString(line, ""))
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > 140 {
			line = string([]rune(line)[:139]) + "…"
		}
		return line
	}
	return ""
}
