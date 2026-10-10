package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// ---------------------------------------------------------------- slugs

// newDocumentSlugSuffix is the 12-hex random part of a document slug
// ("untitled-1a2b3c4d5e6f"), kept when the document is renamed.
func newDocumentSlugSuffix() string {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return strings.TrimPrefix(newCollaborationID(), "collab_")[:12]
	}
	return hex.EncodeToString(random)
}

// documentSlugID builds `<title-slug>-<suffix>`; an empty title becomes
// `untitled-<suffix>`.
func documentSlugID(title, suffix string) string {
	base := slug(title)
	if runes := []rune(base); len(runes) > 60 {
		base = strings.Trim(string(runes[:60]), "-")
	}
	if base == "" {
		base = "untitled"
	}
	return base + "-" + suffix
}

// documentSlugSuffix is the stable random tail of an existing slug.
func documentSlugSuffix(slugID string) string {
	if index := strings.LastIndex(slugID, "-"); index >= 0 && index < len(slugID)-1 {
		return slugID[index+1:]
	}
	return newDocumentSlugSuffix()
}

// renameDocumentSlug moves the slug to follow a new title (keeping its
// random suffix) and remembers the old slug so existing links keep
// resolving (documentByID and GET /api/documents/{id} accept it).
func renameDocumentSlug(document *domain.Document) {
	previous := document.SlugID
	next := documentSlugID(document.Title, documentSlugSuffix(previous))
	if next == previous {
		return
	}
	document.SlugID = next
	if previous != "" && !slices.Contains(document.PreviousSlugIDs, previous) {
		document.PreviousSlugIDs = append(document.PreviousSlugIDs, previous)
		if len(document.PreviousSlugIDs) > 20 {
			document.PreviousSlugIDs = document.PreviousSlugIDs[len(document.PreviousSlugIDs)-20:]
		}
	}
	document.PreviousSlugIDs = slices.DeleteFunc(document.PreviousSlugIDs, func(value string) bool { return value == next })
}

// ---------------------------------------------------------------- history

const (
	// A new version starts after this much idle time...
	documentRevisionIdleWindow = 5 * time.Minute
	// ...or once a burst has run this long, so all-day editing still
	// leaves restorable checkpoints.
	documentRevisionMaxBurst = time.Hour
	// Co-editors saving within this window share a version.
	documentRevisionCoeditWindow = 30 * time.Second
	maxDocumentRevisions         = 100
)

// recordDocumentRevision stores the document's current (post-update) state
// as its newest version. Saves in the same editing burst — same author, less
// than documentRevisionIdleWindow apart — update that version in place, so
// history holds one entry per burst and revisions[0] is always the current
// state. It reports whether a new version started.
func recordDocumentRevision(document *domain.Document, author domain.User, now time.Time, force bool) bool {
	if len(document.Revisions) > 0 && !force {
		latest := &document.Revisions[0]
		unchanged := latest.Title == document.Title && latest.Content == document.Content && latest.ContentState == document.ContentState
		if unchanged && latest.StartedAt != nil {
			return false
		}
		// Versions written before post-update snapshots (no StartedAt) hold a
		// previous state; they are never overwritten.
		if latest.StartedAt != nil && now.Sub(*latest.StartedAt) < documentRevisionMaxBurst {
			sameAuthor := latest.Author.ID == author.ID && now.Sub(latest.CreatedAt) < documentRevisionIdleWindow
			coediting := latest.Author.ID != author.ID && now.Sub(latest.CreatedAt) < documentRevisionCoeditWindow
			if sameAuthor || coediting {
				latest.Title, latest.Content, latest.ContentState, latest.ContentData = document.Title, document.Content, document.ContentState, document.ContentData
				latest.Author, latest.CreatedAt = author, now
				latest.AuthorIDs = appendUnique(latest.AuthorIDs, author.ID)
				return false
			}
		}
	}
	started := now
	revision := domain.DocumentRevision{ID: fmt.Sprintf("revision_%d", now.UnixNano()), DocumentID: document.ID, Title: document.Title, Content: document.Content, ContentState: document.ContentState, ContentData: document.ContentData, Author: author, AuthorIDs: []string{author.ID}, CreatedAt: now, StartedAt: &started}
	document.Revisions = append([]domain.DocumentRevision{revision}, document.Revisions...)
	if len(document.Revisions) > maxDocumentRevisions {
		document.Revisions = document.Revisions[:maxDocumentRevisions]
	}
	return true
}

// ---------------------------------------------------------------- scopes

// documentMutationScope loads a document's comments and inbox records (and
// the issues the write names), so document writes can notify subscribers
// without loading the workspace's issues or content records.
func documentMutationScope(ctx context.Context, id string, issueIDs ...*string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{IssueIDs: scopeIDs(issueIDs...), Resolve: func(data domain.Bootstrap) store.MutationScope {
		if document, err := documentByID(&data, id); err == nil {
			return store.MutationScope{Resources: []string{document.ID}}
		}
		return store.MutationScope{Resources: []string{id}}
	}})
}

// ---------------------------------------------------------------- reads

// getDocument returns one document by id, slug or a previous slug. A
// document in "Recently deleted" is returned with deletedAt set (to the
// people who could open it), so links show the deleted state instead of 404.
func (s *server) getDocument(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	id := r.PathValue("id")
	if document, err := documentByID(&data, id); err == nil {
		if documentRole(s, data, *document) == "none" {
			writeError(w, http.StatusNotFound, "document not found")
			return
		}
		writeJSON(w, http.StatusOK, document)
		return
	}
	if document, entry, ok := trashedDocument(&data, id); ok && documentRole(s, data, document) != "none" {
		deletedAt := entry.DeletedAt
		document.DeletedAt = &deletedAt
		writeJSON(w, http.StatusOK, document)
		return
	}
	writeError(w, http.StatusNotFound, "document not found")
}

func trashedDocument(data *domain.Bootstrap, id string) (domain.Document, domain.TrashEntry, bool) {
	for _, entry := range data.Trash {
		if entry.ResourceType != "document" {
			continue
		}
		var document domain.Document
		if json.Unmarshal(entry.Payload, &document) != nil {
			continue
		}
		if document.ID == id || document.SlugID == id || slices.Contains(document.PreviousSlugIDs, id) {
			return document, entry, true
		}
	}
	return domain.Document{}, domain.TrashEntry{}, false
}

// restoreDeletedDocument backs "Restore document" on a deleted document and
// in "Recently deleted": the document returns with its comments, history,
// permissions and collaboration log intact.
func (s *server) restoreDeletedDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var restored domain.Document
	directory := s.documentMemberDirectory(r)
	err := s.store.MutateWorkspace(documentMutationScope(r.Context(), id), workspaceKey(r), "document.restored", id, nil, func(data *domain.Bootstrap) error {
		if _, err := documentByID(data, id); err == nil {
			return errConflict
		}
		document, entry, ok := trashedDocument(data, id)
		if !ok {
			return errNotFound
		}
		if !workspaceAdminRole(data.ViewerRole) && entry.DeletedBy.ID != data.Viewer.ID && documentRole(s, *data, document) != "owner" {
			return store.ErrAuthForbidden
		}
		document.DeletedAt = nil
		document.UpdatedAt = time.Now().UTC()
		document.Version++
		data.Documents = append([]domain.Document{document}, data.Documents...)
		syncDocumentProjectResources(data, document)
		data.Trash = slices.DeleteFunc(data.Trash, func(item domain.TrashEntry) bool { return item.ID == entry.ID })
		appendAudit(data, "restored", "document", document.ID, nil)
		appendDocumentNotifications(s, data, document, documentNotice{directory: directory, kind: notificationDocumentRestored, recipients: documentSubscribers(data, document)}, document.UpdatedAt)
		restored = document
		return nil
	})
	if errors.Is(err, errConflict) {
		writeError(w, http.StatusConflict, "document is not deleted")
		return
	}
	respondMutation(w, err, http.StatusOK, restored)
}

// purgeDocumentContent drops what a permanently deleted document left
// outside the workspace metadata: its collaboration update log. (Its
// comments are content records owned by the document id and are removed in
// the purging mutation.)
func (s *server) purgeDocumentContent(ctx context.Context, workspace string, documentIDs []string) {
	for _, id := range documentIDs {
		if err := s.store.DeleteDocumentCollaborationDocument(ctx, workspace, id); err != nil {
			log.Printf("purge collaboration log document=%s: %v", id, err)
		}
	}
}

// ---------------------------------------------------------------- subscribers

type documentSubscriberInput struct {
	UserIDs []string `json:"userIds"`
}

// listDocumentSubscribers returns the users subscribed to a document.
func (s *server) listDocumentSubscribers(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	document, err := documentByID(&data, r.PathValue("id"))
	if err != nil || documentRole(s, data, *document) == "none" {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}
	writeJSON(w, http.StatusOK, subscriberUsers(&data, s.documentMemberDirectory(r), *document))
}

// addDocumentSubscribers subscribes members (the viewer or others) to a
// document; people subscribed by someone else are told so in their inbox.
func (s *server) addDocumentSubscribers(w http.ResponseWriter, r *http.Request) {
	var input documentSubscriberInput
	if !decodeJSON(w, r, &input) {
		return
	}
	s.changeDocumentSubscribers(w, r, normalizedStrings(input.UserIDs), true)
}

func (s *server) removeDocumentSubscriber(w http.ResponseWriter, r *http.Request) {
	s.changeDocumentSubscribers(w, r, []string{r.PathValue("userId")}, false)
}

func (s *server) changeDocumentSubscribers(w http.ResponseWriter, r *http.Request, userIDs []string, subscribed bool) {
	id := r.PathValue("id")
	if len(userIDs) == 0 {
		writeError(w, http.StatusBadRequest, "userIds is required")
		return
	}
	eventType := "document.subscribers_added"
	if !subscribed {
		eventType = "document.subscriber_removed"
	}
	var users []domain.User
	directory := s.documentMemberDirectory(r)
	err := s.store.MutateWorkspace(documentMutationScope(r.Context(), id), workspaceKey(r), eventType, id, map[string]any{"userIds": userIDs}, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		role := documentRole(s, *data, *document)
		// Anyone who can open the document may change their own
		// subscription; changing other people's takes comment access.
		others := slices.ContainsFunc(userIDs, func(userID string) bool { return userID != data.Viewer.ID })
		if role == "none" || others && !canCommentDocument(role) {
			return store.ErrAuthForbidden
		}
		changed := []string{}
		for _, userID := range userIDs {
			if directory.user(data, userID) == nil {
				return errInvalid
			}
			if subscribed && documentRoleFor(s, data, directory, *document, userID) == "none" {
				return errInvalid
			}
			was := slices.Contains(documentSubscribers(data, *document), userID)
			setDocumentSubscription(data, "document", document.ID, userID, subscribed)
			if subscribed && !slices.ContainsFunc(data.Subscriptions, func(item domain.Subscription) bool {
				return item.UserID == userID && item.ResourceType == "document" && item.ResourceID == document.ID
			}) {
				now := time.Now().UTC()
				data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: "subscription_" + strings.TrimPrefix(newCollaborationID(), "collab_"), UserID: userID, ResourceType: "document", ResourceID: document.ID, CreatedAt: now})
			}
			if !subscribed {
				data.Subscriptions = slices.DeleteFunc(data.Subscriptions, func(item domain.Subscription) bool {
					return item.UserID == userID && item.ResourceType == "document" && item.ResourceID == document.ID && !slices.Contains(item.Events, pulseSubscriptionEvent) && len(item.OptOutEvents) == 0
				})
			}
			if was != subscribed {
				changed = append(changed, userID)
			}
		}
		if len(changed) == 0 {
			users = subscriberUsers(data, directory, *document)
			return store.ErrNoMutation
		}
		kind := notificationDocumentSubscribed
		if !subscribed {
			kind = notificationDocumentUnsubscribed
		}
		appendDocumentNotifications(s, data, *document, documentNotice{directory: directory, kind: kind, recipients: changed}, time.Now().UTC())
		users = subscriberUsers(data, directory, *document)
		return nil
	})
	if errors.Is(err, store.ErrNoMutation) {
		err = nil
	}
	respondMutation(w, err, http.StatusOK, users)
}

func subscriberUsers(data *domain.Bootstrap, directory memberDirectory, document domain.Document) []domain.User {
	users := []domain.User{}
	for _, id := range documentSubscribers(data, document) {
		if user := directory.user(data, id); user != nil {
			users = append(users, *user)
		}
	}
	return users
}

// ---------------------------------------------------------------- threads

// documentThreadAudience is who follows a document comment thread: its
// participants and explicit subscribers, minus members who unsubscribed
// from it ("muted", which also silences document-level subscriptions).
func documentThreadAudience(data *domain.Bootstrap, documentID string, comments []domain.Comment, rootID string) ([]string, map[string]bool) {
	muted := map[string]bool{}
	for _, item := range data.ThreadSubscriptions {
		if item.IssueID == documentID && item.CommentID == rootID && item.State == "muted" {
			muted[item.UserID] = true
		}
	}
	watchers := []string{}
	for _, userID := range documentThreadParticipants(comments, rootID) {
		if !muted[userID] {
			watchers = appendUnique(watchers, userID)
		}
	}
	for _, item := range data.ThreadSubscriptions {
		if item.IssueID == documentID && item.CommentID == rootID && item.State == "subscribed" {
			watchers = appendUnique(watchers, item.UserID)
		}
	}
	return watchers, muted
}

// setDocumentThreadSubscription backs "Subscribe to / Unsubscribe from
// thread" on a document comment (state "subscribed" or "muted"); DELETE
// returns to the default (participants follow their threads).
func (s *server) setDocumentThreadSubscription(w http.ResponseWriter, r *http.Request) {
	var input domain.ThreadSubscriptionInput
	if r.Method == http.MethodPut {
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.State != "subscribed" && input.State != "muted" {
			writeError(w, http.StatusBadRequest, "state must be subscribed or muted")
			return
		}
	}
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	var saved domain.ThreadSubscription
	err := s.store.MutateWorkspace(documentContentScope(r.Context(), id), workspaceKey(r), "document.comment_thread_subscription", id, map[string]string{"commentId": commentID, "state": input.State}, func(data *domain.Bootstrap) error {
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		if documentRole(s, *data, *document) == "none" {
			return store.ErrAuthForbidden
		}
		rootID, ok := threadRootID(data.Comments[document.ID], commentID)
		if !ok {
			return errNotFound
		}
		mine := func(item domain.ThreadSubscription) bool {
			return item.UserID == data.Viewer.ID && item.IssueID == document.ID && item.CommentID == rootID
		}
		if input.State == "" {
			data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, mine)
			return nil
		}
		now := time.Now().UTC()
		if index := slices.IndexFunc(data.ThreadSubscriptions, mine); index >= 0 {
			data.ThreadSubscriptions[index].State, data.ThreadSubscriptions[index].UpdatedAt = input.State, now
			saved = data.ThreadSubscriptions[index]
			return nil
		}
		saved = domain.ThreadSubscription{ID: fmt.Sprintf("thread_subscription_%d", now.UnixNano()), UserID: data.Viewer.ID, IssueID: document.ID, CommentID: rootID, State: input.State, CreatedAt: now, UpdatedAt: now}
		data.ThreadSubscriptions = append(data.ThreadSubscriptions, saved)
		return nil
	})
	if r.Method == http.MethodDelete {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	respondMutation(w, err, http.StatusOK, saved)
}

// ---------------------------------------------------------------- access

// defaultDocumentGrants gives a new document the reference app's default
// access: a team document is editable by its teams' members, any other
// document by everyone in the workspace. They are ordinary grants, shown and
// changed in "People with access".
func defaultDocumentGrants(document domain.Document, now time.Time) []domain.DocumentPermission {
	grants := []domain.DocumentPermission{}
	if len(document.TeamIDs) == 0 {
		return append(grants, domain.DocumentPermission{ID: "document_permission_workspace_", DocumentID: document.ID, SubjectType: "workspace", SubjectID: "", Role: "editor", CreatedAt: now, UpdatedAt: now})
	}
	for _, teamID := range document.TeamIDs {
		grants = append(grants, domain.DocumentPermission{ID: "document_permission_team_" + teamID, DocumentID: document.ID, SubjectType: "team", SubjectID: teamID, Role: "editor", CreatedAt: now, UpdatedAt: now})
	}
	return grants
}

// moveDocumentTeamGrants keeps a moved document's team access with its
// teams: grants to teams it left move to the teams it joined (a team keeps
// an existing grant of its own).
func moveDocumentTeamGrants(document *domain.Document, previousTeamIDs []string, now time.Time) {
	added := slices.DeleteFunc(slices.Clone(document.TeamIDs), func(teamID string) bool { return slices.Contains(previousTeamIDs, teamID) })
	removed := slices.DeleteFunc(slices.Clone(previousTeamIDs), func(teamID string) bool { return slices.Contains(document.TeamIDs, teamID) })
	if len(removed) == 0 {
		return
	}
	role := ""
	document.Permissions = slices.DeleteFunc(document.Permissions, func(permission domain.DocumentPermission) bool {
		if permission.SubjectType == "team" && slices.Contains(removed, permission.SubjectID) {
			if documentRoleRank(permission.Role) > documentRoleRank(role) {
				role = permission.Role
			}
			return true
		}
		return false
	})
	if role == "" {
		return
	}
	for _, teamID := range added {
		if slices.ContainsFunc(document.Permissions, func(permission domain.DocumentPermission) bool {
			return permission.SubjectType == "team" && permission.SubjectID == teamID
		}) {
			continue
		}
		document.Permissions = append(document.Permissions, domain.DocumentPermission{ID: "document_permission_team_" + teamID, DocumentID: document.ID, SubjectType: "team", SubjectID: teamID, Role: role, CreatedAt: now, UpdatedAt: now})
	}
}

// startCollaborationGeneration replaces the document's realtime (Yjs) state:
// editors joined to the previous generation are told to reload. It returns
// the replaced generation's id, whose update log the caller drops.
func startCollaborationGeneration(document *domain.Document) string {
	previous := document.CollaborationID
	if previous == "" {
		previous = document.ID
	}
	document.ContentState = ""
	document.ContentVersion++
	document.CollaborationID = fmt.Sprintf("%s_v%d", document.ID, document.ContentVersion)
	return previous
}
