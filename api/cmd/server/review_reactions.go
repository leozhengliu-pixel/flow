package main

import (
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

func (s *server) toggleReviewReaction(w http.ResponseWriter, r *http.Request) {
	var input domain.ReactionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Emoji = strings.TrimSpace(input.Emoji)
	if input.Emoji == "" || len(input.Emoji) > 128 {
		writeError(w, http.StatusBadRequest, "emoji is required")
		return
	}
	id := r.PathValue("id")
	var updated domain.CodeReview
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "review.reaction_toggled", id, input, func(data *domain.Bootstrap) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		review := &data.Reviews[index]
		if review.Reactions == nil {
			review.Reactions = map[string][]string{}
		}
		users := review.Reactions[input.Emoji]
		if slices.Contains(users, data.Viewer.ID) {
			users = removeString(users, data.Viewer.ID)
		} else {
			users = append(users, data.Viewer.ID)
		}
		if len(users) == 0 {
			delete(review.Reactions, input.Emoji)
		} else {
			review.Reactions[input.Emoji] = users
		}
		updated = *review
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) createReviewCommentAttachment(w http.ResponseWriter, r *http.Request) {
	s.storeCommentAttachment(w, r, "review", func(data *domain.Bootstrap, id string, attachment domain.Attachment) error {
		index := reviewIndex(*data, id)
		if index < 0 {
			return errNotFound
		}
		data.Reviews[index].CommentAttachments = append(data.Reviews[index].CommentAttachments, attachment)
		return nil
	})
}
