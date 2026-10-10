package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Document notification types (the reference app's names). They are inbox
// records owned by the document (SourceType "document", SourceID the
// document id, IssueID empty), so a mutation scoped to the document's
// resources loads exactly the records these producers aggregate into.
const (
	notificationDocumentMention          = "documentMention"
	notificationDocumentCommentMention   = "documentCommentMention"
	notificationDocumentNewComment       = "documentNewComment"
	notificationDocumentThreadResolved   = "documentThreadResolved"
	notificationDocumentCommentReaction  = "documentCommentReaction"
	notificationDocumentChanges          = "documentChanges"
	notificationDocumentMoved            = "documentMoved"
	notificationDocumentDeleted          = "documentDeleted"
	notificationDocumentRestored         = "documentRestored"
	notificationDocumentAddedAsOwner     = "documentAddedAsOwner"
	notificationDocumentRemovedAsOwner   = "documentRemovedAsOwner"
	notificationDocumentSubscribed       = "documentSubscribed"
	notificationDocumentUnsubscribed     = "documentUnsubscribed"
	documentChangesNotificationQuietTime = 30 * time.Minute
)

// documentNotificationCategory maps a type to the notification settings
// category that switches it on or off (inbox, email and desktop).
func documentNotificationCategory(kind string) string {
	switch kind {
	case notificationDocumentMention, notificationDocumentCommentMention:
		return "mentions"
	case notificationDocumentNewComment, notificationDocumentThreadResolved:
		return "comments"
	case notificationDocumentCommentReaction:
		return "reactions"
	case notificationDocumentSubscribed, notificationDocumentUnsubscribed:
		return "subscriptions"
	default:
		return "documents"
	}
}

func documentNotificationText(kind, actor, detail string) string {
	switch kind {
	case notificationDocumentMention:
		return actor + " mentioned you in the document"
	case notificationDocumentCommentMention:
		return actor + " mentioned you in a comment"
	case notificationDocumentNewComment:
		return actor + " commented on the document"
	case notificationDocumentThreadResolved:
		return actor + " resolved a comment thread"
	case notificationDocumentCommentReaction:
		return actor + " reacted " + detail + " to your comment"
	case notificationDocumentChanges:
		return actor + " edited the document"
	case notificationDocumentMoved:
		if detail != "" {
			return actor + " moved the document to " + detail
		}
		return actor + " moved the document"
	case notificationDocumentDeleted:
		return actor + " deleted the document"
	case notificationDocumentRestored:
		return actor + " restored the document"
	case notificationDocumentAddedAsOwner:
		return actor + " made you an owner of the document"
	case notificationDocumentRemovedAsOwner:
		return actor + " removed you as an owner of the document"
	case notificationDocumentSubscribed:
		return actor + " subscribed you to the document"
	case notificationDocumentUnsubscribed:
		return actor + " unsubscribed you from the document"
	}
	return actor + " updated the document"
}

// memberDirectory is the workspace's member list read before a mutation.
// The stored workspace snapshot only lists people who wrote something, so
// recipients and subscribers are resolved against the membership instead.
type memberDirectory struct {
	users map[string]domain.User
	roles map[string]string
}

// memberDirectoryTTL bounds how stale the cached member list may be.
// Document saves arrive every few seconds while people type; the list
// changes rarely, so one membership query per workspace serves them all.
const memberDirectoryTTL = 30 * time.Second

type cachedMemberDirectory struct {
	directory memberDirectory
	loadedAt  time.Time
}

var memberDirectories sync.Map // workspace id -> cachedMemberDirectory

func (s *server) documentMemberDirectory(r *http.Request) memberDirectory {
	directory := memberDirectory{users: map[string]domain.User{}, roles: map[string]string{}}
	if s.authDisabled {
		return directory
	}
	metadata, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "workspace")
	if !ok || metadata.Workspace.ID == "" {
		return directory
	}
	key := fmt.Sprintf("%p:%s", s.store, metadata.Workspace.ID)
	if cached, ok := memberDirectories.Load(key); ok && time.Since(cached.(cachedMemberDirectory).loadedAt) < memberDirectoryTTL {
		return cached.(cachedMemberDirectory).directory
	}
	members, err := s.store.ListMembers(r.Context(), metadata.Workspace.ID)
	if err != nil {
		return directory
	}
	defer func() {
		memberDirectories.Store(key, cachedMemberDirectory{directory: directory, loadedAt: time.Now()})
	}()
	for _, member := range members {
		if member.Status != "" && member.Status != "active" {
			continue
		}
		directory.users[member.User.ID] = member.User
		directory.roles[member.User.ID] = member.Role
	}
	return directory
}

func (directory memberDirectory) user(data *domain.Bootstrap, id string) *domain.User {
	if user := userByID(data, id); user != nil {
		return user
	}
	if user, ok := directory.users[id]; ok {
		return &user
	}
	return nil
}

// documentRoleFor evaluates the document role another workspace member has,
// so notifications never reach someone who cannot open the document.
func documentRoleFor(s *server, data *domain.Bootstrap, directory memberDirectory, document domain.Document, userID string) string {
	user := directory.user(data, userID)
	if user == nil {
		return "none"
	}
	view := *data
	view.Viewer = *user
	view.ViewerRole = directory.roles[userID]
	if view.ViewerRole == "" {
		for _, member := range data.Members {
			if member.User.ID == userID {
				view.ViewerRole = member.Role
				break
			}
		}
	}
	return documentRole(s, view, document)
}

type documentNotice struct {
	directory  memberDirectory
	kind       string
	recipients []string
	comment    *domain.Comment
	detail     string // destination (moved) or emoji (reaction)
	excerpt    string
	quotedText string
}

// appendDocumentNotifications writes one inbox notification per recipient
// (never the actor, app users, members without access, or members whose
// settings switch the category off) and queues their email, desktop and
// push deliveries. Unread notifications of the same kind on the same
// document aggregate for six hours, like issue notifications.
func appendDocumentNotifications(s *server, data *domain.Bootstrap, document domain.Document, notice documentNotice, now time.Time) {
	actor := data.Viewer
	actorName := firstNonEmpty(actor.DisplayName, actor.Name)
	category := documentNotificationCategory(notice.kind)
	title := strings.TrimSpace(document.Title)
	if title == "" {
		title = "Untitled document"
	}
	seen := map[string]bool{}
	for _, recipientID := range notice.recipients {
		if recipientID == "" || seen[recipientID] || recipientID == actor.ID {
			continue
		}
		seen[recipientID] = true
		recipient := notice.directory.user(data, recipientID)
		if recipient == nil || recipient.App {
			continue
		}
		if documentRoleFor(s, data, notice.directory, document, recipientID) == "none" {
			continue
		}
		preferences, ok := data.NotificationPreferences[recipientID]
		if !ok {
			preferences = defaultPreferences(recipientID)
		}
		if !preferences.Inbox.Enabled || !categoryEnabled(preferences.Inbox, category) {
			continue
		}
		commentID := ""
		if notice.comment != nil {
			commentID = notice.comment.ID
		}
		payload := &domain.NotificationPayload{DocumentSlugID: document.SlugID, Excerpt: notice.excerpt, QuotedText: notice.quotedText}
		if notice.kind == notificationDocumentCommentReaction {
			payload.Emoji = notice.detail
		}
		text := documentNotificationText(notice.kind, actorName, notice.detail)
		groupKey := recipientID + ":document:" + document.ID + ":" + notice.kind
		if existing := aggregatableNotification(data, groupKey, now); existing != nil {
			if notice.kind == notificationDocumentChanges && existing.Actor.ID == actor.ID && now.Sub(existing.UpdatedAt) < documentChangesNotificationQuietTime {
				// The same editor's continued work is already announced.
				continue
			}
			existing.OccurrenceCount++
			existing.Actor, existing.Title, existing.Text, existing.Payload, existing.CommentID, existing.UpdatedAt = actor, title, text, payload, commentID, now
			existing.LatestActorIDs = appendUnique(existing.LatestActorIDs, actor.ID)
			if len(existing.LatestActorIDs) > 3 {
				existing.LatestActorIDs = existing.LatestActorIDs[len(existing.LatestActorIDs)-3:]
			}
			enqueueNotificationDeliveries(data, *existing, preferences)
			continue
		}
		notification := domain.Notification{
			ID:          fmt.Sprintf("notification_%s_%d_%s", notice.kind, now.UnixNano(), recipientID),
			RecipientID: recipientID, Type: notice.kind, SourceType: "document", SourceID: document.ID, CommentID: commentID,
			Actor: actor, Category: category, GroupKey: groupKey, OccurrenceCount: 1, LatestActorIDs: []string{actor.ID},
			Title: title, Text: text, Payload: payload, CreatedAt: now, UpdatedAt: now,
		}
		data.Notifications = append(data.Notifications, notification)
		enqueueNotificationDeliveries(data, notification, preferences)
	}
}

// documentSubscribers is everyone subscribed to the document: the
// document's subscriber list plus explicit subscription records.
func documentSubscribers(data *domain.Bootstrap, document domain.Document) []string {
	result := slices.Clone(document.SubscriberIDs)
	for _, subscription := range data.Subscriptions {
		if subscription.ResourceType == "document" && subscription.ResourceID == document.ID && subscription.UserID != "" {
			result = appendUnique(result, subscription.UserID)
		}
	}
	return result
}

// documentMentionIDs lists the users mentioned in a document body: mention
// nodes in the editor document, plus @handles in Markdown-only bodies.
func documentMentionIDs(data *domain.Bootstrap, content string, contentData map[string]any) []string {
	result := []string{}
	if contentData != nil {
		collectMentionIDs(contentData, &result)
		return result
	}
	lower := strings.ToLower(content)
	for _, user := range data.Users {
		if user.Name != "" && strings.Contains(lower, "@"+strings.ToLower(user.Name)) || user.Email != "" && strings.Contains(lower, "@"+strings.ToLower(user.Email)) {
			result = appendUnique(result, user.ID)
		}
	}
	return result
}

// newDocumentMentions returns the users mentioned after a save who were not
// mentioned before it, so each mention notifies once.
func newDocumentMentions(data *domain.Bootstrap, beforeContent string, beforeData map[string]any, afterContent string, afterData map[string]any) []string {
	before := documentMentionIDs(data, beforeContent, beforeData)
	return slices.DeleteFunc(documentMentionIDs(data, afterContent, afterData), func(id string) bool { return slices.Contains(before, id) })
}

// commentMentionIDs lists the users a comment mentions.
func commentMentionIDs(data *domain.Bootstrap, comment domain.Comment) []string {
	result := []string{}
	collectMentionIDs(comment.BodyData, &result)
	lower := strings.ToLower(comment.Body)
	for _, user := range data.Users {
		if user.Name != "" && strings.Contains(lower, "@"+strings.ToLower(user.Name)) || user.Email != "" && strings.Contains(lower, "@"+strings.ToLower(user.Email)) {
			result = appendUnique(result, user.ID)
		}
	}
	return result
}

// documentThreadParticipants is the root comment's author plus everyone who
// replied in the thread.
func documentThreadParticipants(comments []domain.Comment, rootID string) []string {
	result := []string{}
	for _, comment := range comments {
		if comment.ID == rootID || comment.ParentID != nil && *comment.ParentID == rootID {
			result = appendUnique(result, comment.User.ID)
		}
	}
	return result
}

func notificationExcerpt(value string) string {
	value = strings.Join(strings.Fields(markdownToPlain(value)), " ")
	if utf8.RuneCountInString(value) <= 160 {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:157])) + "…"
}

// markdownToPlain drops the most common Markdown punctuation for excerpts.
func markdownToPlain(value string) string {
	replacer := strings.NewReplacer("**", "", "__", "", "`", "", "~~", "", "#", "", "> ", "")
	return replacer.Replace(value)
}

// documentDestinationName names where a moved document now lives.
func documentDestinationName(data *domain.Bootstrap, document domain.Document) string {
	for _, projectID := range document.ProjectIDs {
		if project, err := fullProjectByID(data, projectID); err == nil {
			return project.Name
		}
	}
	for _, teamID := range document.TeamIDs {
		for _, team := range data.Teams {
			if team.ID == teamID {
				return team.Name
			}
		}
	}
	return ""
}

// documentOwnerIDs lists the users holding the owner permission. A
// document without an explicit permission list is owned by its creator.
func documentOwnerIDs(document domain.Document) []string {
	if len(document.Permissions) == 0 {
		return []string{document.Creator.ID}
	}
	result := []string{}
	for _, permission := range document.Permissions {
		if permission.SubjectType == "user" && strings.EqualFold(permission.Role, "owner") {
			result = appendUnique(result, permission.SubjectID)
		}
	}
	return result
}

// notifyDocumentOwnerChanges tells users who gained or lost the owner role.
func notifyDocumentOwnerChanges(s *server, data *domain.Bootstrap, directory memberDirectory, before, after domain.Document, now time.Time) {
	previous, next := documentOwnerIDs(before), documentOwnerIDs(after)
	added := slices.DeleteFunc(slices.Clone(next), func(id string) bool { return slices.Contains(previous, id) })
	removed := slices.DeleteFunc(slices.Clone(previous), func(id string) bool { return slices.Contains(next, id) })
	if len(added) > 0 {
		appendDocumentNotifications(s, data, after, documentNotice{directory: directory, kind: notificationDocumentAddedAsOwner, recipients: added}, now)
	}
	if len(removed) > 0 {
		// A removed owner may have lost access entirely; tell them anyway
		// using the previous ACL so the notice is not filtered out.
		appendDocumentNotifications(s, data, before, documentNotice{directory: directory, kind: notificationDocumentRemovedAsOwner, recipients: removed}, now)
	}
}
