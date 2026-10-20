package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type reviewInput struct {
	Title       *string   `json:"title,omitempty"`
	Status      *string   `json:"status,omitempty"`
	ReviewerIDs *[]string `json:"reviewerIds,omitempty"`
	IssueIDs    *[]string `json:"issueIds,omitempty"`
	Favorite    *bool     `json:"favorite,omitempty"`
	Draft       *bool     `json:"draft,omitempty"`
	BranchState *string   `json:"branchState,omitempty"`
	MergeMethod *string   `json:"mergeMethod,omitempty"`
}

func (s *server) listReviews(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.workspaceData(r).Reviews)
}

func (s *server) getReview(w http.ResponseWriter, r *http.Request) {
	review, err := reviewByID(s.workspaceData(r), r.PathValue("id"))
	respondMutation(w, err, http.StatusOK, review)
}

func (s *server) updateReview(w http.ResponseWriter, r *http.Request) {
	var input reviewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	id := r.PathValue("id")
	var statusPostedAs *domain.ReviewPostedAs
	statusActor := requestActor(s, r)
	if input.Status != nil {
		if !slices.Contains([]string{"open", "inReview", "approved", "merged", "closed"}, *input.Status) {
			writeError(w, 400, "invalid review status")
			return
		}
		if slices.Contains([]string{"open", "merged", "closed"}, *input.Status) {
			if input.Title != nil || input.ReviewerIDs != nil || input.IssueIDs != nil || input.Favorite != nil || input.Draft != nil || input.BranchState != nil {
				writeError(w, 400, "submit repository status changes separately from metadata edits")
				return
			}
			data := s.workspaceData(r)
			current, err := reviewByID(data, id)
			if err != nil {
				respondMutation(w, err, 200, nil)
				return
			}
			method := mergeMethod(data.UserSettings[data.Viewer.ID].MergeStrategy)
			if input.MergeMethod != nil {
				method = *input.MergeMethod
			}
			if !slices.Contains([]string{"merge", "squash", "rebase"}, method) {
				writeError(w, 400, "invalid merge method")
				return
			}
			if current.Status != *input.Status {
				posted, err := s.syncReviewStatusAs(r.Context(), data, current, statusActor, *input.Status, method)
				if err != nil {
					writeError(w, http.StatusBadGateway, err.Error())
					return
				}
				statusPostedAs = posted
			}
		}
	}
	var updated domain.CodeReview
	if input.ReviewerIDs != nil && len(*input.ReviewerIDs) > 0 {
		data := s.workspaceData(r)
		review, err := reviewByID(data, id)
		if err != nil {
			respondMutation(w, err, 200, nil)
			return
		}
		if !validateResourceIDs(&data, "user", *input.ReviewerIDs) {
			writeError(w, 400, "invalid reviewer")
			return
		}
		if data.UserSettings[data.Viewer.ID].AutoConvertDrafts && review.Draft {
			if input.Title != nil || input.Status != nil || input.Draft != nil || input.IssueIDs != nil || input.Favorite != nil || input.BranchState != nil {
				writeError(w, 400, "submit draft review requests separately from metadata edits")
				return
			}
			if err := s.markReviewReady(r.Context(), data, review); err != nil {
				writeError(w, 502, err.Error())
				return
			}
		}
	}
	var mergedNow bool
	var mergedWorkspace string
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), favoriteMutationEvent("review.updated", input), id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		review := data.Reviews[index]
		previousIssueIDs := slices.Clone(review.IssueIDs)
		mergedNow, mergedWorkspace = input.Status != nil && *input.Status == "merged" && review.Status != "merged", data.Workspace.URLKey
		if input.Title != nil {
			review.Title = strings.TrimSpace(*input.Title)
			if review.Title == "" {
				return errInvalid
			}
		}
		if input.Status != nil {
			if !slices.Contains([]string{"open", "inReview", "approved", "merged", "closed"}, *input.Status) {
				return errInvalid
			}
			now := time.Now().UTC()
			if statusPostedAs != nil && review.Status != *input.Status {
				// Merged / closed / reopened on the code host as this identity.
				review.Events = append(review.Events, domain.ReviewEvent{ID: fmt.Sprintf("review_event_%d", now.UnixNano()), Type: map[string]string{"merged": "merged", "closed": "closed", "open": "reopened"}[*input.Status], Actor: statusActor, PostedAs: statusPostedAs, CreatedAt: now})
			}
			review.Status = *input.Status
			if review.Status == "merged" {
				review.MergedAt, review.ClosedAt = &now, nil
			} else if review.Status == "closed" {
				review.ClosedAt, review.MergedAt = &now, nil
			} else {
				review.MergedAt, review.ClosedAt = nil, nil
			}
		}
		if input.ReviewerIDs != nil {
			ids := normalizedStrings(*input.ReviewerIDs)
			if len(ids) != len(*input.ReviewerIDs) || !validateResourceIDs(data, "user", ids) {
				return errInvalid
			}
			review.ReviewerIDs = ids
			if len(ids) > 0 && data.UserSettings[data.Viewer.ID].AutoConvertDrafts {
				review.Draft = false
			}
		}
		if input.IssueIDs != nil {
			ids := normalizedStrings(*input.IssueIDs)
			if len(ids) != len(*input.IssueIDs) || !validateResourceIDs(data, "issue", ids) {
				return errInvalid
			}
			review.IssueIDs = ids
			for _, issueID := range ids {
				if !slices.Contains(previousIssueIDs, issueID) {
					appendActivity(data, issueID, "issue.review_linked", data.Viewer, map[string]string{"reviewId": review.ID, "reviewTitle": review.Title, "reviewNumber": fmt.Sprint(review.Number), "repository": review.RepositoryOwner + "/" + review.RepositoryName, "url": review.URL})
				}
			}
			for _, issueID := range previousIssueIDs {
				if !slices.Contains(ids, issueID) {
					appendActivity(data, issueID, "issue.review_unlinked", data.Viewer, map[string]string{"reviewId": review.ID, "reviewTitle": review.Title, "reviewNumber": fmt.Sprint(review.Number), "repository": review.RepositoryOwner + "/" + review.RepositoryName})
				}
			}
		}
		if input.Favorite != nil {
			review.Favorite = *input.Favorite
			setFavoriteRecord(data, "review", review.ID, *input.Favorite)
		}
		if input.Draft != nil {
			review.Draft = *input.Draft
		}
		if input.BranchState != nil {
			if !slices.Contains([]string{"upToDate", "behind", "conflicted"}, *input.BranchState) {
				return errInvalid
			}
			review.BranchState = *input.BranchState
		}
		review.UpdatedAt = time.Now().UTC()
		data.Reviews[index], updated = review, review
		return nil
	})
	if err == nil && mergedNow {
		// The session state "Merged": stamped after the review commits.
		err = s.store.MarkAgentSessionsMerged(r.Context(), mergedWorkspace, updated.IssueIDs, time.Now())
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) submitReview(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Decision string `json:"decision"`
		Body     string `json:"body"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !slices.Contains([]string{"approve", "comment", "requestChanges"}, input.Decision) {
		writeError(w, http.StatusBadRequest, "invalid review decision")
		return
	}
	id, actor := r.PathValue("id"), requestActor(s, r)
	data := s.workspaceData(r)
	current, err := reviewByID(data, id)
	if err != nil {
		respondMutation(w, err, 200, nil)
		return
	}
	if current.Status == "merged" || current.Status == "closed" {
		writeError(w, http.StatusConflict, "closed reviews cannot be submitted")
		return
	}
	if input.Decision == "approve" && data.UserSettings[actor.ID].AutoConvertDrafts {
		if err := s.markReviewReady(r.Context(), data, current); err != nil {
			writeError(w, 502, err.Error())
			return
		}
	}
	// Post the review to GitHub/GitLab as the member (their linked account,
	// else the workspace integration).
	postedAs, remoteRef, err := s.postReviewDecision(r.Context(), data, current, actor, input.Decision, input.Body)
	if err != nil {
		// The code host refusing the decision (for example approving your
		// own pull request) is not an outage.
		status := http.StatusBadGateway
		if code := reviewRemoteStatus(err); code == http.StatusForbidden || code == http.StatusNotFound || code == http.StatusUnprocessableEntity {
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, err.Error())
		return
	}
	var updated domain.CodeReview
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.submitted", id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		review := data.Reviews[index]
		if review.Status == "merged" || review.Status == "closed" {
			return errConflict
		}
		eventType := "review_commented"
		if input.Decision == "approve" {
			review.Status, eventType = "approved", "approved"
			if data.UserSettings[actor.ID].AutoConvertDrafts {
				review.Draft = false
			}
		} else if input.Decision == "requestChanges" {
			review.Status, eventType = "inReview", "changes_requested"
		}
		now := time.Now().UTC()
		review.Events = append(review.Events, domain.ReviewEvent{ID: fmt.Sprintf("review_event_%d", now.UnixNano()), Type: eventType, Body: strings.TrimSpace(input.Body), Actor: actor, PostedAs: postedAs, Remote: remoteRef, CreatedAt: now})
		review.UpdatedAt = now
		data.Reviews[index], updated = review, review
		return nil
	})
	if err == errConflict {
		writeError(w, http.StatusConflict, "closed reviews cannot be submitted")
		return
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) commentOnReview(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Body      string `json:"body"`
		Path      string `json:"path,omitempty"`
		Line      int    `json:"line,omitempty"`
		Side      string `json:"side,omitempty"`
		StartLine int    `json:"startLine,omitempty"`
		StartSide string `json:"startSide,omitempty"`
		ParentID  string `json:"parentId,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "comment body is required")
		return
	}
	side, startSide, ok := reviewCommentSides(input.Side, input.StartSide)
	if !ok || input.Line < 0 || input.StartLine < 0 || (input.StartLine > 0 && input.Line == 0) {
		writeError(w, http.StatusBadRequest, "invalid comment position")
		return
	}
	id, actor := r.PathValue("id"), requestActor(s, r)
	data := s.workspaceData(r)
	current, err := reviewByID(data, id)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	// Saved in Flow first, then written back as the member (their linked
	// account, else the workspace credentials); see reviewRemoteAs.
	remote, remoteErr := s.reviewRemoteAs(r.Context(), data, current, actor, false)
	var updated domain.CodeReview
	var eventID string
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.commented", id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		review := data.Reviews[index]
		event := domain.ReviewEvent{ID: fmt.Sprintf("review_event_%d", now.UnixNano()), Type: "commented", Body: strings.TrimSpace(input.Body), Path: strings.TrimSpace(input.Path), Line: input.Line, Actor: actor, CreatedAt: now}
		if event.Path != "" && event.Line > 0 {
			event.Side = side
			if input.StartLine > 0 && (input.StartLine != input.Line || startSide != side) {
				event.StartLine, event.StartSide = input.StartLine, startSide
			}
		}
		if parentID := strings.TrimSpace(input.ParentID); parentID != "" {
			// Replies join the thread root and inherit its code location.
			root := reviewThreadRoot(review, parentID)
			if root < 0 {
				return errInvalid
			}
			parent := review.Events[root]
			event.ParentID, event.Path, event.Line, event.Resolved = parent.ID, parent.Path, parent.Line, parent.Resolved
			event.Side, event.StartLine, event.StartSide = parent.Side, parent.StartLine, parent.StartSide
		}
		switch {
		case remote != nil:
			event.Sync = &domain.ReviewEventSync{State: "pending", Op: "create", AttemptedAt: now, ActorID: actor.ID}
		case remoteErr != nil:
			event.Sync = &domain.ReviewEventSync{State: "failed", Op: "create", Error: reviewSyncErrorMessage(review.Provider, remoteErr), AttemptedAt: now, ActorID: actor.ID}
		}
		eventID = event.ID
		review.Events = append(review.Events, event)
		review.UpdatedAt = now
		data.Reviews[index], updated = review, review
		return nil
	})
	if err == nil && remote != nil {
		// The comment is kept either way; a failure is recorded on it.
		if written, writeErr := s.writeBackReviewChange(r.Context(), workspaceKey(r), remote, updated.ID, eventID); writeErr == nil {
			updated = written
		}
	}
	respondMutation(w, err, http.StatusCreated, updated)
}

// reviewCommentSides validates the diff sides of a code comment (LEFT: the
// old file, RIGHT: the new file, the default).
func reviewCommentSides(side, startSide string) (string, string, bool) {
	side, startSide = strings.ToUpper(strings.TrimSpace(side)), strings.ToUpper(strings.TrimSpace(startSide))
	if side == "" {
		side = "RIGHT"
	}
	if startSide == "" {
		startSide = side
	}
	valid := func(value string) bool { return value == "LEFT" || value == "RIGHT" }
	return side, startSide, valid(side) && valid(startSide)
}

// resolveReviewThread marks a review comment thread (its root and replies)
// as resolved or unresolved, and resolves the code host thread.
func (s *server) resolveReviewThread(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Resolved *bool `json:"resolved"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Resolved == nil {
		writeError(w, http.StatusBadRequest, "resolved is required")
		return
	}
	id, commentID, actor := r.PathValue("id"), r.PathValue("commentId"), requestActor(s, r)
	data := s.workspaceData(r)
	current, err := reviewByID(data, id)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	remote, remoteErr := s.reviewRemoteAs(r.Context(), data, current, actor, false)
	op := "unresolve"
	if *input.Resolved {
		op = "resolve"
	}
	var updated domain.CodeReview
	var rootID string
	var push bool
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.thread_resolved", id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		review := data.Reviews[index]
		root := reviewThreadRoot(review, commentID)
		if root < 0 {
			return errNotFound
		}
		rootID = review.Events[root].ID
		review.Events = slices.Clone(review.Events)
		for i := range review.Events {
			if review.Events[i].ID == rootID || review.Events[i].ParentID == rootID {
				review.Events[i].Resolved = *input.Resolved
			}
		}
		now := time.Now().UTC()
		rootEvent := &review.Events[root]
		// Code threads that exist on the code host resolve there too; a
		// thread still being created carries its resolution with it.
		threadOnHost := rootEvent.Remote != nil && (rootEvent.Remote.Kind == reviewRemoteReviewComment || (rootEvent.Remote.Kind == reviewRemoteNote && rootEvent.Path != ""))
		if threadOnHost && (rootEvent.Sync == nil || rootEvent.Sync.Op != "create") {
			switch {
			case remote != nil:
				rootEvent.Sync, push = &domain.ReviewEventSync{State: "pending", Op: op, AttemptedAt: now, ActorID: actor.ID}, true
			case remoteErr != nil:
				rootEvent.Sync = &domain.ReviewEventSync{State: "failed", Op: op, Error: reviewSyncErrorMessage(review.Provider, remoteErr), AttemptedAt: now, ActorID: actor.ID}
			}
		}
		review.UpdatedAt = now
		data.Reviews[index], updated = review, review
		return nil
	})
	if err == nil && push {
		if written, writeErr := s.writeBackReviewChange(r.Context(), workspaceKey(r), remote, updated.ID, rootID); writeErr == nil {
			updated = written
		}
	}
	respondMutation(w, err, http.StatusOK, updated)
}

// reviewThreadRoot returns the index of the comment that starts the thread
// containing commentID, or -1 when commentID is not a review comment.
func reviewThreadRoot(review domain.CodeReview, commentID string) int {
	index := slices.IndexFunc(review.Events, func(event domain.ReviewEvent) bool { return event.ID == commentID })
	if index < 0 || !reviewCommentEvent(review.Events[index]) {
		return -1
	}
	if parent := review.Events[index].ParentID; parent != "" {
		if root := slices.IndexFunc(review.Events, func(event domain.ReviewEvent) bool { return event.ID == parent }); root >= 0 {
			return root
		}
	}
	return index
}

func reviewCommentEvent(event domain.ReviewEvent) bool {
	switch event.Type {
	case "commented", "review_commented", "approved", "changes_requested":
		// Review summaries (approve / request changes) can be replied to like comments.
		return true
	}
	return event.Path != ""
}

func reviewIndex(data domain.Bootstrap, id string) int {
	return slices.IndexFunc(data.Reviews, func(item domain.CodeReview) bool { return item.ID == id || item.SlugID == id })
}

func reviewByID(data domain.Bootstrap, id string) (domain.CodeReview, error) {
	index := reviewIndex(data, id)
	if index < 0 {
		return domain.CodeReview{}, errNotFound
	}
	return data.Reviews[index], nil
}
