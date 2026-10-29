package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"flow/api/internal/idgen"
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
// revisionBotActor is the attribution of an edit made by the request's viewer
// ("Last edit by <User> via Flow" when Flow's agent wrote it).
func revisionBotActor(author domain.User) *domain.ActorBot {
	if author.Via == nil {
		return nil
	}
	copy := *author.Via
	return &copy
}

// documentHasBody reports whether a document holds a body worth keeping as a
// version (an empty editor document does not).
func documentHasBody(document domain.Document) bool {
	if strings.TrimSpace(document.Content) != "" {
		return true
	}
	content, _ := document.ContentData["content"].([]any)
	return len(content) > 0
}

// keepDocumentBaseline stores the pre-edit state of a document that has a
// body but no versions yet (created with a body, imported, or written before
// versions were kept) as its oldest version, before the first edit replaces
// it. Versions are post-edit snapshots, so without this the original body
// would be lost. The baseline keeps the state's own time and the creator (the
// only known editor) and has no StartedAt, so it is never merged into an
// editing burst. Call it before recordDocumentRevision with the state from
// before the write.
func keepDocumentBaseline(document *domain.Document, before domain.Document) {
	if len(document.Revisions) > 0 || !documentHasBody(before) {
		return
	}
	at := before.UpdatedAt
	if at.IsZero() {
		at = before.CreatedAt
	}
	var botActor *domain.ActorBot
	if before.BotActor != nil {
		copy := *before.BotActor
		botActor = &copy
	}
	authorIDs := []string{}
	if before.Creator.ID != "" {
		authorIDs = append(authorIDs, before.Creator.ID)
	}
	// The id is fresh (two documents last saved at the same instant, an
	// import for one, used to share "revision_<their time>"); CreatedAt keeps
	// the baseline's own time.
	document.Revisions = []domain.DocumentRevision{{ID: idgen.New("revision_"), DocumentID: document.ID, Title: before.Title, Content: before.Content, ContentState: before.ContentState, ContentData: before.ContentData, Author: before.Creator, BotActor: botActor, AuthorIDs: authorIDs, CreatedAt: at}}
}

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
				latest.Author, latest.CreatedAt, latest.BotActor = author, now, revisionBotActor(author)
				latest.AuthorIDs = appendUnique(latest.AuthorIDs, author.ID)
				return false
			}
		}
	}
	started := now
	revision := domain.DocumentRevision{ID: idgen.New("revision_"), DocumentID: document.ID, Title: document.Title, Content: document.Content, ContentState: document.ContentState, ContentData: document.ContentData, Author: author, BotActor: revisionBotActor(author), AuthorIDs: []string{author.ID}, CreatedAt: now, StartedAt: &started}
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
	return documentScope(ctx, id, false, issueIDs...)
}

// documentLinkMutationScope also loads the issue the document is attached
// to, so a write that changes its links can read that issue's team.
func documentLinkMutationScope(ctx context.Context, id string, issueIDs ...*string) context.Context {
	return documentScope(ctx, id, true, issueIDs...)
}

func documentScope(ctx context.Context, id string, linkedIssue bool, issueIDs ...*string) context.Context {
	return store.WithMutationScope(ctx, store.MutationScope{IssueIDs: scopeIDs(issueIDs...), Resolve: func(data domain.Bootstrap) store.MutationScope {
		if document, err := documentByID(&data, id); err == nil {
			scope := store.MutationScope{Resources: []string{document.ID}}
			if linkedIssue && document.IssueID != "" {
				scope.IssueIDs = []string{document.IssueID}
			}
			return scope
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
		writeJSON(w, http.StatusOK, s.withDocumentVersions(r, *document))
		return
	}
	if document, entry, ok := trashedDocument(&data, id); ok && documentRole(s, data, document) != "none" {
		deletedAt := entry.DeletedAt
		document.DeletedAt = &deletedAt
		writeJSON(w, http.StatusOK, s.withDocumentVersions(r, document))
		return
	}
	writeError(w, http.StatusNotFound, "document not found")
}

// withDocumentVersions loads the bodies of a document's offloaded versions for
// a response that carries them (the document API and MCP send versions
// whole; the workspace data and realtime events do not).
func (s *server) withDocumentVersions(r *http.Request, document domain.Document) domain.Document {
	return s.documentWithVersions(r.Context(), workspaceKey(r), document)
}

func (s *server) documentWithVersions(ctx context.Context, workspace string, document domain.Document) domain.Document {
	if !slices.ContainsFunc(document.Revisions, func(item domain.DocumentRevision) bool { return len(item.Offloaded) > 0 }) {
		return document
	}
	document.Revisions = slices.Clone(document.Revisions)
	if err := s.store.LoadDocumentRevisionBodies(ctx, workspace, document.Revisions); err != nil {
		log.Printf("load document versions document=%s: %v", document.ID, err)
	}
	return document
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
	respondMutation(w, err, http.StatusOK, s.withDocumentVersions(r, restored))
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
			if data.ThreadSubscriptions[index].State == input.State {
				saved = data.ThreadSubscriptions[index]
				return store.ErrNoMutation
			}
			data.ThreadSubscriptions[index].State, data.ThreadSubscriptions[index].UpdatedAt = input.State, now
			saved = data.ThreadSubscriptions[index]
			return nil
		}
		saved = domain.ThreadSubscription{ID: idgen.New("thread_subscription_"), UserID: data.Viewer.ID, IssueID: document.ID, CommentID: rootID, State: input.State, CreatedAt: now, UpdatedAt: now}
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

// ---------------------------------------------------------------- version import

type documentRevisionImportInput struct {
	Title        *string        `json:"title"`
	Content      *string        `json:"content"`
	ContentState string         `json:"contentState"`
	ContentData  map[string]any `json:"contentData"`
	CreatedAt    *time.Time     `json:"createdAt"`
	AuthorID     string         `json:"authorId"`
}

// importDocumentRevision (POST /api/workspace/documents/{id}/revisions/import,
// workspace admins) adds a preserved earlier state of a document, such as a
// backup of a body an edit replaced, to its history as a restorable version.
// The document itself (title, body, version, links) is not changed. The
// version is placed by its createdAt, which must be older than the
// document's current state; importing the same title, body and time again
// returns the version already stored (200 instead of 201).
func (s *server) importDocumentRevision(w http.ResponseWriter, r *http.Request) {
	var input documentRevisionImportInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Content == nil || input.CreatedAt == nil || input.CreatedAt.IsZero() {
		writeError(w, http.StatusBadRequest, "content and createdAt are required")
		return
	}
	id := r.PathValue("id")
	at := input.CreatedAt.UTC()
	var result domain.DocumentRevision
	imported := false
	err := s.store.MutateWorkspace(documentMutationScope(r.Context(), id), workspaceKey(r), "document.revision_imported", id, map[string]any{"createdAt": at, "authorId": input.AuthorID}, func(data *domain.Bootstrap) error {
		if !s.authDisabled && !workspaceAdminRole(data.ViewerRole) {
			return store.ErrAuthForbidden
		}
		document, err := documentByID(data, id)
		if err != nil {
			return err
		}
		title := document.Title
		if input.Title != nil {
			title = strings.TrimSpace(*input.Title)
		}
		sum := sha256.Sum256([]byte(at.Format(time.RFC3339Nano) + "\x00" + title + "\x00" + *input.Content))
		revisionID := "revision_imported_" + hex.EncodeToString(sum[:12])
		if index := slices.IndexFunc(document.Revisions, func(item domain.DocumentRevision) bool { return item.ID == revisionID }); index >= 0 {
			result = document.Revisions[index]
			return nil
		}
		newest := document.UpdatedAt
		if len(document.Revisions) > 0 && document.Revisions[0].CreatedAt.Before(newest) {
			newest = document.Revisions[0].CreatedAt
		}
		if !at.Before(newest) {
			return fmt.Errorf("%w: createdAt must be older than the document's current state (%s)", errInvalid, newest.Format(time.RFC3339))
		}
		author := document.Creator
		if input.AuthorID != "" {
			user := userByID(data, input.AuthorID)
			if user == nil {
				return fmt.Errorf("%w: authorId is not a workspace user", errInvalid)
			}
			author = *user
		}
		// Versions are newest first.
		position := slices.IndexFunc(document.Revisions, func(item domain.DocumentRevision) bool { return item.CreatedAt.Before(at) })
		if position < 0 {
			position = len(document.Revisions)
		}
		if position >= maxDocumentRevisions {
			return fmt.Errorf("%w: the document's history is full and this version is older than all of it", errConflict)
		}
		authorIDs := []string{}
		if author.ID != "" {
			authorIDs = append(authorIDs, author.ID)
		}
		result = domain.DocumentRevision{ID: revisionID, DocumentID: document.ID, Title: title, Content: *input.Content, ContentState: input.ContentState, ContentData: input.ContentData, Author: author, AuthorIDs: authorIDs, CreatedAt: at}
		document.Revisions = slices.Insert(document.Revisions, position, result)
		if len(document.Revisions) > maxDocumentRevisions {
			document.Revisions = document.Revisions[:maxDocumentRevisions]
		}
		appendAudit(data, "revision_imported", "document", document.ID, map[string]any{"revisionId": revisionID, "createdAt": at, "authorId": author.ID, "title": title})
		imported = true
		return nil
	})
	status := http.StatusOK
	if imported {
		status = http.StatusCreated
	}
	respondMutation(w, err, status, map[string]any{"imported": imported, "revision": result})
}
