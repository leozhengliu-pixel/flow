package main

import (
	"fmt"
	"net/http"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// Project comment threads mirror issue comment threads (Linear "Subscribe to
// thread" / "Resolve thread"). The root author and every replier follow the
// thread implicitly; an explicit record either adds a watcher ("subscribed")
// or silences a participant ("muted"). Records are keyed by the thread root.

func projectThreadSubscriptionIndex(data *domain.Bootstrap, userID, projectID, rootID string) int {
	return slices.IndexFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
		return item.UserID == userID && item.ProjectID == projectID && item.IssueID == "" && item.CommentID == rootID
	})
}

func (s *server) setProjectThreadSubscription(w http.ResponseWriter, r *http.Request) {
	var input domain.ThreadSubscriptionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.State != "subscribed" && input.State != "muted" {
		writeError(w, http.StatusBadRequest, "state must be subscribed or muted")
		return
	}
	projectID, commentID := r.PathValue("id"), r.PathValue("commentId")
	var saved domain.ThreadSubscription
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.comment_thread_subscription", projectID, map[string]string{"commentId": commentID, "state": input.State}, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		rootID, ok := threadRootID(project.Comments, commentID)
		if !ok {
			return errNotFound
		}
		now := time.Now().UTC()
		if index := projectThreadSubscriptionIndex(data, data.Viewer.ID, projectID, rootID); index >= 0 {
			data.ThreadSubscriptions[index].State, data.ThreadSubscriptions[index].UpdatedAt = input.State, now
			saved = data.ThreadSubscriptions[index]
			return nil
		}
		saved = domain.ThreadSubscription{ID: fmt.Sprintf("thread_subscription_%d", now.UnixNano()), UserID: data.Viewer.ID, ProjectID: projectID, CommentID: rootID, State: input.State, CreatedAt: now, UpdatedAt: now}
		data.ThreadSubscriptions = append(data.ThreadSubscriptions, saved)
		return nil
	})
	respondMutation(w, err, http.StatusOK, saved)
}

func (s *server) clearProjectThreadSubscription(w http.ResponseWriter, r *http.Request) {
	projectID, commentID := r.PathValue("id"), r.PathValue("commentId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "project.comment_thread_subscription", projectID, map[string]string{"commentId": commentID, "state": ""}, func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, projectID)
		if err != nil {
			return err
		}
		rootID, ok := threadRootID(project.Comments, commentID)
		if !ok {
			return errNotFound
		}
		data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
			return item.UserID == data.Viewer.ID && item.ProjectID == projectID && item.IssueID == "" && item.CommentID == rootID
		})
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveProjectComment resolves or re-opens the thread containing commentID.
// Any member may resolve a thread (Linear does not restrict it to the author).
func resolveProjectComment(data *domain.Bootstrap, project *domain.Project, commentID string, input domain.CommentUpdateInput) (domain.Comment, error) {
	rootID, ok := threadRootID(project.Comments, commentID)
	if !ok {
		return domain.Comment{}, errNotFound
	}
	index := slices.IndexFunc(project.Comments, func(comment domain.Comment) bool { return comment.ID == rootID })
	comment := &project.Comments[index]
	if input.ExpectedVersion != nil && comment.Version != *input.ExpectedVersion {
		return *comment, errConflict
	}
	summaries := len(project.TeamIDs) > 0 && teamResolvedThreadSummaries(data, project.TeamIDs[0])
	applyCommentPatch(comment, domain.CommentUpdateInput{Resolved: input.Resolved, ThreadSummary: input.ThreadSummary}, project.Comments, summaries)
	project.UpdatedAt = time.Now().UTC()
	return *comment, nil
}

func removeProjectThreadSubscriptions(data *domain.Bootstrap, projectID, rootID string) {
	data.ThreadSubscriptions = slices.DeleteFunc(data.ThreadSubscriptions, func(item domain.ThreadSubscription) bool {
		return item.ProjectID == projectID && item.IssueID == "" && item.CommentID == rootID
	})
}
