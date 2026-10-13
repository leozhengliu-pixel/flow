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

const agentReplyNotificationType = "agentReply"

// notifyAgentReply is the store's agent reply listener.
func (s *server) notifyAgentReply(ctx context.Context, task domain.AgentTask, activity domain.AgentActivity) {
	if task.ResourceType != "" && task.ResourceType != "issue" || task.IssueID == "" {
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
	// The write is the agent's own, scoped to the issue's records; it must not
	// be cut short by the agent run's request ending.
	write := store.ContextWithActor(store.WithIssueRecordMutations(context.WithoutCancel(ctx), task.IssueID), agent)
	now := time.Now().UTC()
	err = s.store.MutateWorkspace(write, task.WorkspaceKey, "agent_task.replied", task.IssueID, map[string]string{"taskId": task.ID, "activityId": activity.ID}, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, task.IssueID)
		if err != nil {
			return err
		}
		appendAgentReplyNotifications(data, *issue, task, activity, agent, prompters, now)
		return nil
	})
	if err != nil {
		log.Printf("agent reply notification task=%s: %v", task.ID, err)
		return
	}
	s.dispatchNotificationEmails(write, task.WorkspaceKey)
}

// appendAgentReplyNotifications writes or refreshes each recipient's
// notification for the session's thread.
func appendAgentReplyNotifications(data *domain.Bootstrap, issue domain.Issue, task domain.AgentTask, activity domain.AgentActivity, agent domain.User, prompters []string, now time.Time) {
	recipients := append([]string{task.CreatorID}, prompters...)
	muted := map[string]bool{}
	if task.CommentID != "" {
		if rootID, ok := threadRootID(data.Comments[issue.ID], task.CommentID); ok {
			var watchers []string
			watchers, muted = threadAudience(data, issue.ID, rootID)
			recipients = append(recipients, watchers...)
		}
	}
	excerpt := agentReplyExcerpt(activity.Body)
	agentName := firstNonEmpty(agent.DisplayName, agent.Name, "Agent")
	seen := map[string]bool{}
	for _, recipientID := range recipients {
		if recipientID == "" || seen[recipientID] || muted[recipientID] || recipientID == agent.ID {
			continue
		}
		seen[recipientID] = true
		recipient := userByID(data, recipientID)
		if recipient == nil || recipient.App {
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
				ID: "notification_agent_" + task.ID + "_" + recipientID, RecipientID: recipientID, IssueID: issue.ID,
				Category: "comments", GroupKey: groupKey, CreatedAt: now,
			})
			index = len(data.Notifications) - 1
		}
		notification := &data.Notifications[index]
		notification.Type = agentReplyNotificationType
		notification.SourceType, notification.SourceID = "agentSession", task.ID
		notification.CommentID, notification.ActivityID = task.CommentID, activity.ID
		notification.Actor, notification.LatestActorIDs = agent, []string{agent.ID}
		notification.OccurrenceCount++
		notification.Title = strings.TrimSpace(issue.Identifier + " " + issue.Title)
		notification.Text = agentReplyNotificationText(agentName, activity.Type, excerpt)
		notification.Payload = &domain.NotificationPayload{Excerpt: excerpt, AgentActivity: activity.Type}
		// Every new reply brings the thread back to the top of the Inbox, unread.
		notification.ReadAt, notification.ArchivedAt, notification.DeletedAt, notification.SnoozedUntil = nil, nil, nil, nil
		notification.UpdatedAt = now
		enqueueNotificationDeliveries(data, *notification, preferences)
	}
}

// agentReplyNotificationText is the notification line in English; the web
// app localizes it from the type, payload.agentActivity and the excerpt.
func agentReplyNotificationText(agent, kind, excerpt string) string {
	prefix := agent + " replied"
	switch kind {
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
