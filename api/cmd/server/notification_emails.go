package main

import (
	"fmt"
	"net/url"
	"strings"

	"flow/api/internal/domain"
)

// notificationEmailItem is one inbox notification in an email: Title links to its source and Line says what
// happened, next to the actor's avatar (Icon).
type notificationEmailItem struct {
	Title, URL, Icon, Line string
}

// notificationEmailItem describes notification for email, or false when its source no longer exists.
func (s *server) notificationEmailItem(data *domain.Bootstrap, key string, notification domain.Notification) (notificationEmailItem, error) {
	root := s.mailer.appURL + "/" + url.PathEscape(key)
	item := notificationEmailItem{Icon: s.mailer.notificationIcon(notification.Actor)}
	actor := inviterDisplayName(notification.Actor)
	switch {
	case notification.ReviewID != "":
		review, err := reviewByID(*data, notification.ReviewID)
		if err != nil {
			return item, fmt.Errorf("review is unavailable")
		}
		item.Title, item.URL, item.Line = review.Title, root+"/review/"+url.PathEscape(review.SlugID), actor+" updated the review"
	case notification.Title != "" && notification.IssueID == "":
		// Pulse summaries, update activity and agent runs carry their own title and text.
		item.Title, item.URL, item.Line = notification.Title, root+"/inbox", notification.Text
		if item.Line == "" {
			item.Line = "Open Flow to read it."
		}
	default:
		issue, err := issueByID(data, notification.IssueID)
		if err != nil || issue == nil {
			return item, fmt.Errorf("notification source is unavailable")
		}
		item.Title, item.URL = issue.Identifier+" "+issue.Title, root+"/issue/"+url.PathEscape(issue.Identifier)
		switch notification.Type {
		case "assignment":
			item.Line = actor + " assigned the issue to you"
		case "mention":
			item.Line = actor + " mentioned you"
		case "comment":
			item.Line = actor + " commented on the issue"
		default:
			item.Line = actor + " updated " + issue.Identifier
		}
	}
	return item, nil
}

// notificationIcon is the avatar shown beside a notification: the actor's picture when it is a public image,
// Flow's mark for Flow itself, agents and apps, and none otherwise.
func (m *smtpMailer) notificationIcon(actor domain.User) string {
	if strings.HasPrefix(actor.AvatarURL, "https://") {
		return actor.AvatarURL
	}
	if actor.ID == "" || actor.App || actor.BuiltinAgent {
		return m.emailAsset("flow-mark-128.png")
	}
	return ""
}

// notificationMessage is the unread-notification email: "You have an unread notification on Flow.", each
// notification as a linked title and a line, "Open your Inbox", and Unsubscribe to the email notification settings.
// hidden leaves out every notification's content (sensitive data protection).
func (m *smtpMailer) notificationMessage(to, key, subject string, items []notificationEmailItem, hidden bool) (mailMessage, error) {
	root := m.appURL + "/" + url.PathEscape(key)
	inbox, settings := root+"/inbox", root+"/settings/account/notifications/email"
	heading := "You have an unread notification on Flow."
	summary := "You have 1 unread notification on Flow."
	if len(items) != 1 {
		heading = fmt.Sprintf("You have %d unread notifications on Flow.", len(items))
		summary = heading
	}
	if hidden {
		subject = "Flow notification"
	}
	var text strings.Builder
	text.WriteString(summary + "\n\n")
	blocks := []emailBlock{{Kind: "hr"}}
	if !hidden {
		for _, item := range items {
			fmt.Fprintf(&text, "%s\n%s\n%s\n\n", headerText(item.Title), headerText(item.Line), item.URL)
			blocks = append(blocks, emailBlock{Kind: "notice", Label: item.Title, URL: item.URL, Icon: item.Icon, HTML: emailHTML("%s", item.Line)})
		}
	}
	text.WriteString("Open your Inbox: " + inbox + "\n\nUnsubscribe from these emails: " + settings + "\n")
	blocks = append(blocks, emailBlock{Kind: "button", Label: "Open your Inbox", URL: inbox})
	html, err := m.renderEmail(emailLayout{Title: subject, Preheader: summary, Heading: emailHTML("%s", heading), Blocks: blocks, Unsubscribe: settings})
	if err != nil {
		return mailMessage{}, err
	}
	return mailMessage{To: to, Subject: headerText(subject), Text: text.String(), HTML: html, ListUnsubscribe: settings}, nil
}

func (m *smtpMailer) sendNotificationEmail(to, key, subject string, items []notificationEmailItem, hidden bool) error {
	message, err := m.notificationMessage(to, key, subject, items, hidden)
	if err != nil {
		return err
	}
	return m.deliver(message)
}
