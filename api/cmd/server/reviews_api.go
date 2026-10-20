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
	postedAs, err := s.postReviewActivity(r.Context(), data, current, actor, map[string]string{"approve": "approve", "requestChanges": "requestChanges", "comment": "comment"}[input.Decision], input.Body, "", 0)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
		review.Events = append(review.Events, domain.ReviewEvent{ID: fmt.Sprintf("review_event_%d", now.UnixNano()), Type: eventType, Body: strings.TrimSpace(input.Body), Actor: actor, PostedAs: postedAs, CreatedAt: now})
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
		Body     string `json:"body"`
		Path     string `json:"path,omitempty"`
		Line     int    `json:"line,omitempty"`
		ParentID string `json:"parentId,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "comment body is required")
		return
	}
	id, actor := r.PathValue("id"), requestActor(s, r)
	snapshot := s.workspaceData(r)
	current, err := reviewByID(snapshot, id)
	if err != nil {
		respondMutation(w, err, http.StatusCreated, nil)
		return
	}
	path, line, kind := strings.TrimSpace(input.Path), input.Line, "comment"
	if parentID := strings.TrimSpace(input.ParentID); parentID != "" {
		root := reviewThreadRoot(current, parentID)
		if root < 0 {
			respondMutation(w, errInvalid, http.StatusCreated, nil)
			return
		}
		path, line = "", 0 // replies are posted as conversation comments
	}
	if path != "" && line > 0 {
		kind = "inlineComment"
	}
	postedAs, err := s.postReviewActivity(r.Context(), snapshot, current, actor, kind, input.Body, path, line)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	var updated domain.CodeReview
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.commented", id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		review := data.Reviews[index]
		event := domain.ReviewEvent{ID: fmt.Sprintf("review_event_%d", now.UnixNano()), Type: "commented", Body: strings.TrimSpace(input.Body), Path: strings.TrimSpace(input.Path), Line: input.Line, Actor: actor, PostedAs: postedAs, CreatedAt: now}
		if parentID := strings.TrimSpace(input.ParentID); parentID != "" {
			// Replies join the thread root and inherit its code location.
			root := reviewThreadRoot(review, parentID)
			if root < 0 {
				return errInvalid
			}
			event.ParentID, event.Path, event.Line, event.Resolved = review.Events[root].ID, review.Events[root].Path, review.Events[root].Line, review.Events[root].Resolved
		}
		review.Events = append(review.Events, event)
		review.UpdatedAt = now
		data.Reviews[index], updated = review, review
		return nil
	})
	respondMutation(w, err, http.StatusCreated, updated)
}

// resolveReviewThread marks a review comment thread (its root and replies)
// as resolved or unresolved.
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
	id, commentID := r.PathValue("id"), r.PathValue("commentId")
	var updated domain.CodeReview
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.thread_resolved", id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		review := data.Reviews[index]
		root := reviewThreadRoot(review, commentID)
		if root < 0 {
			return errNotFound
		}
		rootID := review.Events[root].ID
		review.Events = slices.Clone(review.Events)
		for i := range review.Events {
			if review.Events[i].ID == rootID || review.Events[i].ParentID == rootID {
				review.Events[i].Resolved = *input.Resolved
			}
		}
		review.UpdatedAt = time.Now().UTC()
		data.Reviews[index], updated = review, review
		return nil
	})
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
