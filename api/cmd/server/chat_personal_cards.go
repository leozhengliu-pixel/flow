package main

import (
	"fmt"
	"net/url"

	"flow/api/internal/domain"
)

// chatPersonalSummary is a personal notification as the member reads it in
// their bot conversation: written from the notification itself in the
// member's Flow language, people named by chatPersonLabel, the issue's status
// as its team names it and the time in the member's time zone. The email
// keeps its own summary (notificationSummary).
func chatPersonalSummary(language, zone string, data *domain.Bootstrap, root string, notification domain.Notification) (notificationEmailItem, error) {
	item := notificationEmailItem{}
	actor := firstNonEmpty(chatPersonLabel(&notification.Actor, data.Users), chatT(language, "comment_heading_unknown"))
	payload := notification.Payload
	if payload == nil {
		payload = &domain.NotificationPayload{}
	}
	switch {
	case notification.ReviewID != "":
		review, err := reviewByID(*data, notification.ReviewID)
		if err != nil {
			return item, fmt.Errorf("review is unavailable")
		}
		item.Title, item.URL, item.Line = review.Title, root+"/review/"+url.PathEscape(review.SlugID), chatT(language, "notify.review", actor)
	case notification.Title != "" && notification.IssueID == "":
		// Updates, documents, agent runs and Pulse summaries carry their
		// subject's name; the line is written from the notification's kind.
		item.Title, item.URL = notification.Title, root+"/inbox"
		item.Line = chatPersonalOwnLine(language, actor, notification, payload)
	default:
		issue, err := issueByID(data, notification.IssueID)
		if err != nil || issue == nil {
			return item, fmt.Errorf("notification source is unavailable")
		}
		item.Title, item.URL = issue.Identifier+" "+issue.Title, root+"/issue/"+url.PathEscape(issue.Identifier)
		switch {
		case notification.Type == agentReplyNotificationType:
			item.Line = chatPersonalOwnLine(language, actor, notification, payload)
		case notification.Type == "assignment":
			item.Line = chatT(language, "notify.assignment", actor)
		case notification.Type == "mention":
			item.Line = chatT(language, "notify.mention", actor)
		case notification.Type == "comment":
			item.Line = chatT(language, "notify.comment", actor)
		case notification.Category == "statusChanges" && issue.State.Name != "":
			item.Line = chatT(language, "notify.status", actor, issue.State.Name)
		default:
			item.Line = chatT(language, "notify.activity", actor, issue.Identifier)
		}
	}
	at := notification.UpdatedAt
	if at.IsZero() {
		at = notification.CreatedAt
	}
	if !at.IsZero() {
		item.Line += "\n" + chatFormatTime(language, at, zone)
	}
	return item, nil
}

// chatPersonalOwnLine writes the line of a notification that is not about an
// issue change: a document, an update, an agent's reply. A kind it does not
// know keeps the notification's own text.
func chatPersonalOwnLine(language, actor string, notification domain.Notification, payload *domain.NotificationPayload) string {
	withExcerpt := func(line string) string {
		if payload.Excerpt == "" {
			return line
		}
		return chatT(language, "notify.with_excerpt", line, chatTruncateText(payload.Excerpt, 300))
	}
	switch notification.Type {
	case agentReplyNotificationType:
		key := "notify.agent_replied"
		switch payload.AgentActivity {
		case "finished":
			key = "notify.agent_finished"
		case "elicitation":
			key = "notify.agent_waiting"
		case "error":
			key = "notify.agent_error"
		}
		return withExcerpt(chatT(language, key, actor))
	case notificationUpdateComment:
		return chatT(language, "notify.update_comment", actor)
	case notificationUpdateReaction:
		return chatT(language, "notify.update_reaction", actor)
	case notificationDocumentCommentReaction:
		if payload.Emoji != "" {
			return chatT(language, "notify.document_reaction_emoji", actor, payload.Emoji)
		}
		return chatT(language, "notify.document_reaction", actor)
	case notificationDocumentMention, notificationDocumentCommentMention, notificationDocumentNewComment:
		return withExcerpt(chatT(language, "notify."+notification.Type, actor))
	case notificationDocumentThreadResolved, notificationDocumentChanges, notificationDocumentMoved, notificationDocumentDeleted,
		notificationDocumentRestored, notificationDocumentAddedAsOwner, notificationDocumentRemovedAsOwner, notificationDocumentSubscribed, notificationDocumentUnsubscribed:
		return chatT(language, "notify."+notification.Type, actor)
	}
	if notification.Text != "" {
		return notification.Text
	}
	return chatT(language, "notify.open_to_read")
}
