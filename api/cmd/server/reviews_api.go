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
	var updated domain.CodeReview
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.updated", id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		review := data.Reviews[index]
		previousIssueIDs := slices.Clone(review.IssueIDs)
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
			review.Status = *input.Status
			now := time.Now().UTC()
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
	var updated domain.CodeReview
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.submitted", id, input, func(data *domain.Bootstrap) error {
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
		} else if input.Decision == "requestChanges" {
			review.Status, eventType = "inReview", "changes_requested"
		}
		now := time.Now().UTC()
		review.Events = append(review.Events, domain.ReviewEvent{ID: fmt.Sprintf("review_event_%d", now.UnixNano()), Type: eventType, Body: strings.TrimSpace(input.Body), Actor: actor, CreatedAt: now})
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
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Body) == "" {
		writeError(w, http.StatusBadRequest, "comment body is required")
		return
	}
	id, actor := r.PathValue("id"), requestActor(s, r)
	var updated domain.CodeReview
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.commented", id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		now := time.Now().UTC()
		review := data.Reviews[index]
		review.Events = append(review.Events, domain.ReviewEvent{ID: fmt.Sprintf("review_event_%d", now.UnixNano()), Type: "commented", Body: strings.TrimSpace(input.Body), Actor: actor, CreatedAt: now})
		review.UpdatedAt = now
		data.Reviews[index], updated = review, review
		return nil
	})
	respondMutation(w, err, http.StatusCreated, updated)
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
