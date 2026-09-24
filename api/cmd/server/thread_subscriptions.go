package main

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// Comment thread subscriptions (Linear "Subscribe to thread"). A thread is a
// root comment plus its replies. Participants — the root author and everyone
// who replied — are subscribed implicitly; an explicit "subscribed" record
// adds a watcher, and "muted" silences the thread for that user even when they
// follow the whole issue.

func threadRootID(comments []domain.Comment, commentID string) (string, bool) {
	for depth := 0; depth < 16; depth++ {
		index := slices.IndexFunc(comments, func(item domain.Comment) bool { return item.ID == commentID })
		if index < 0 {
			return "", false
		}
		if comments[index].ParentID == nil || *comments[index].ParentID == "" {
			return comments[index].ID, true
		}
		commentID = *comments[index].ParentID
	}
	return "", false
}

func threadSubscriptionState(data *domain.Bootstrap, userID, issueID, rootID string) string {
	index := slices.IndexFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
		return item.UserID == userID && item.IssueID == issueID && item.CommentID == rootID
	})
	if index < 0 {
		return ""
	}
	return data.ThreadSubscriptions[index].State
}

// threadAudience returns thread watchers (participants and explicit
// subscribers, minus muted users) and the users who muted the thread.
func threadAudience(data *domain.Bootstrap, issueID, rootID string) (watchers []string, muted map[string]bool) {
	muted = map[string]bool{}
	for _, item := range data.ThreadSubscriptions {
		if item.IssueID == issueID && item.CommentID == rootID && item.State == "muted" {
			muted[item.UserID] = true
		}
	}
	add := func(userID string) {
		if userID != "" && !muted[userID] && !slices.Contains(watchers, userID) {
			watchers = append(watchers, userID)
		}
	}
	for _, comment := range data.Comments[issueID] {
		if root, ok := threadRootID(data.Comments[issueID], comment.ID); ok && root == rootID {
			add(comment.User.ID)
		}
	}
	for _, item := range data.ThreadSubscriptions {
		if item.IssueID == issueID && item.CommentID == rootID && item.State == "subscribed" {
			add(item.UserID)
		}
	}
	return watchers, muted
}

func (s *server) setThreadSubscription(w http.ResponseWriter, r *http.Request) {
	var input domain.ThreadSubscriptionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.State != "subscribed" && input.State != "muted" {
		writeError(w, http.StatusBadRequest, "state must be subscribed or muted")
		return
	}
	issueID, commentID := r.PathValue("id"), r.PathValue("commentId")
	var saved domain.ThreadSubscription
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "comment.thread_subscription", issueID, map[string]string{"commentId": commentID, "state": input.State}, func(data *domain.Bootstrap) error {
		if _, err := issueByID(data, issueID); err != nil {
			return err
		}
		rootID, ok := threadRootID(data.Comments[issueID], commentID)
		if !ok {
			return errNotFound
		}
		now := time.Now().UTC()
		index := slices.IndexFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
			return item.UserID == data.Viewer.ID && item.IssueID == issueID && item.CommentID == rootID
		})
		if index >= 0 {
			data.ThreadSubscriptions[index].State, data.ThreadSubscriptions[index].UpdatedAt = input.State, now
			saved = data.ThreadSubscriptions[index]
			return nil
		}
		saved = domain.ThreadSubscription{ID: fmt.Sprintf("thread_subscription_%d", now.UnixNano()), UserID: data.Viewer.ID, IssueID: issueID, CommentID: rootID, State: input.State, CreatedAt: now, UpdatedAt: now}
		data.ThreadSubscriptions = append(data.ThreadSubscriptions, saved)
		return nil
	})
	respondMutation(w, err, http.StatusOK, saved)
}

func (s *server) clearThreadSubscription(w http.ResponseWriter, r *http.Request) {
	issueID, commentID := r.PathValue("id"), r.PathValue("commentId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "comment.thread_subscription", issueID, map[string]string{"commentId": commentID, "state": ""}, func(data *domain.Bootstrap) error {
		rootID, ok := threadRootID(data.Comments[issueID], commentID)
		if !ok {
			return errNotFound
		}
		data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
			return item.UserID == data.Viewer.ID && item.IssueID == issueID && item.CommentID == rootID
		})
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}
