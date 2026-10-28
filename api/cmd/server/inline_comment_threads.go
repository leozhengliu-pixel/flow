package main

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Inline (anchored) comment threads on project and initiative descriptions.
// Like a document thread, the first comment carries the anchor: anchorId names
// the selection the thread was started on and quotedText holds its text, so the
// client can find it again in the description. Replies are one level deep and
// carry no anchor. Inline threads live in the entity's comment list next to
// its page-level comments (which have no anchor).

const (
	maxCommentAnchorLength = 128
	maxCommentQuoteLength  = 4000
)

var errCommentAnchor = errors.New("anchorId and quotedText belong on a thread's first comment")

// normalizeCommentAnchor trims the anchor fields of a new comment and checks
// their limits; a reply cannot carry an anchor.
func normalizeCommentAnchor(input *domain.CommentCreateInput) error {
	input.AnchorID, input.QuotedText = strings.TrimSpace(input.AnchorID), strings.TrimSpace(input.QuotedText)
	if len(input.AnchorID) > maxCommentAnchorLength || len(input.QuotedText) > maxCommentQuoteLength {
		return errCommentAnchor
	}
	if input.QuotedText != "" && input.AnchorID == "" {
		return errCommentAnchor
	}
	if input.ParentID != nil && (input.AnchorID != "" || input.QuotedText != "") {
		return errCommentAnchor
	}
	return nil
}

// threadReplyParent resolves a reply's parent to its thread root (threads are
// one level deep). It returns errNotFound for an unknown parent.
func threadReplyParent(comments []domain.Comment, parentID *string) (*string, error) {
	if parentID == nil {
		return nil, nil
	}
	rootID, ok := threadRootID(comments, *parentID)
	if !ok {
		return nil, errNotFound
	}
	return &rootID, nil
}

// commentAnchorTaken reports whether another thread already uses anchorID.
func commentAnchorTaken(comments []domain.Comment, anchorID string) bool {
	return anchorID != "" && slices.ContainsFunc(comments, func(item domain.Comment) bool { return item.AnchorID == anchorID })
}

// writeCommentCreateError answers the anchor-specific failures of a comment
// create; it returns false when err is not one of them.
func writeCommentCreateError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errCommentAnchor):
		writeError(w, http.StatusBadRequest, errCommentAnchor.Error())
	case errors.Is(err, errConflict):
		writeError(w, http.StatusConflict, "a thread already uses this anchor")
	default:
		return false
	}
	return true
}

// deleteCommentThread removes a comment and, when it starts a thread, every
// reply to it. It reports whether the comment existed.
func deleteCommentThread(comments *[]domain.Comment, commentID string) bool {
	before := len(*comments)
	*comments = slices.DeleteFunc(*comments, func(comment domain.Comment) bool {
		return comment.ID == commentID || (comment.ParentID != nil && *comment.ParentID == commentID)
	})
	return len(*comments) != before
}

// resolveInitiativeComment resolves or re-opens the thread containing
// commentID. Any member may resolve a thread.
func resolveInitiativeComment(initiative *domain.Initiative, commentID string, input domain.CommentUpdateInput) (domain.Comment, error) {
	rootID, ok := threadRootID(initiative.Comments, commentID)
	if !ok {
		return domain.Comment{}, errNotFound
	}
	index := slices.IndexFunc(initiative.Comments, func(comment domain.Comment) bool { return comment.ID == rootID })
	comment := &initiative.Comments[index]
	if input.ExpectedVersion != nil && comment.Version != *input.ExpectedVersion {
		return *comment, errConflict
	}
	if !applyCommentPatch(comment, domain.CommentUpdateInput{Resolved: input.Resolved, ThreadSummary: input.ThreadSummary}, initiative.Comments, false) {
		return *comment, store.ErrNoMutation
	}
	initiative.UpdatedAt = time.Now().UTC()
	return *comment, nil
}

func initiativeThreadSubscription(item domain.ThreadSubscription, initiativeID, rootID string) bool {
	return item.InitiativeID == initiativeID && item.IssueID == "" && item.ProjectID == "" && item.CommentID == rootID
}

func removeInitiativeThreadSubscriptions(data *domain.Bootstrap, initiativeID, rootID string) {
	data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
		return initiativeThreadSubscription(item, initiativeID, rootID)
	})
}

// setInitiativeThreadSubscription is "Subscribe to thread" / "Unsubscribe from
// thread" on an initiative comment thread; the record keys on the thread root.
func (s *server) setInitiativeThreadSubscription(w http.ResponseWriter, r *http.Request) {
	var input domain.ThreadSubscriptionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.State != "subscribed" && input.State != "muted" {
		writeError(w, http.StatusBadRequest, "state must be subscribed or muted")
		return
	}
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	var saved domain.ThreadSubscription
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.comment_thread_subscription", id, map[string]string{"commentId": commentID, "state": input.State}, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		rootID, ok := threadRootID(initiative.Comments, commentID)
		if !ok {
			return errNotFound
		}
		now := time.Now().UTC()
		index := slices.IndexFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
			return item.UserID == data.Viewer.ID && initiativeThreadSubscription(item, initiative.ID, rootID)
		})
		if index >= 0 {
			data.ThreadSubscriptions[index].State, data.ThreadSubscriptions[index].UpdatedAt = input.State, now
			saved = data.ThreadSubscriptions[index]
			return nil
		}
		saved = domain.ThreadSubscription{ID: idgen.New("thread_subscription_"), UserID: data.Viewer.ID, InitiativeID: initiative.ID, CommentID: rootID, State: input.State, CreatedAt: now, UpdatedAt: now}
		data.ThreadSubscriptions = append(data.ThreadSubscriptions, saved)
		return nil
	})
	respondMutation(w, err, http.StatusOK, saved)
}

func (s *server) clearInitiativeThreadSubscription(w http.ResponseWriter, r *http.Request) {
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "initiative.comment_thread_subscription", id, map[string]string{"commentId": commentID, "state": ""}, func(data *domain.Bootstrap) error {
		initiative, err := initiativeByID(data, id)
		if err != nil {
			return err
		}
		rootID, ok := threadRootID(initiative.Comments, commentID)
		if !ok {
			return errNotFound
		}
		data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
			return item.UserID == data.Viewer.ID && initiativeThreadSubscription(item, initiative.ID, rootID)
		})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
