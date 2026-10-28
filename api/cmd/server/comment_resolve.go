package main

import (
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func commentUpdateHasBody(input domain.CommentUpdateInput) bool {
	return strings.TrimSpace(input.Body) != ""
}

func commentUpdateIsResolveOnly(input domain.CommentUpdateInput) bool {
	return input.Resolved != nil && !commentUpdateHasBody(input)
}

// applyCommentPatch applies an edit or (un)resolve and reports whether it
// changed the comment. An edit that repeats the comment's current text and
// state leaves it as it was (version and editedAt included); the caller then
// writes nothing.
func applyCommentPatch(comment *domain.Comment, input domain.CommentUpdateInput, siblings []domain.Comment, summariesEnabled bool) bool {
	before := *comment
	now := time.Now().UTC()
	if commentUpdateHasBody(input) {
		comment.Body = strings.TrimSpace(input.Body)
		comment.BodyData = input.BodyData
		comment.EditedAt = &now
	}
	if input.Resolved != nil {
		comment.Resolved = *input.Resolved
		if !*input.Resolved {
			comment.ThreadSummary = nil
		} else if input.ThreadSummary != nil {
			comment.ThreadSummary = input.ThreadSummary
		} else if summariesEnabled && comment.ThreadSummary == nil {
			comment.ThreadSummary = &domain.CommentThreadSummary{Content: buildResolvedThreadSummary(*comment, siblings)}
		}
	}
	if input.ThreadSummary != nil && (input.Resolved == nil || *input.Resolved) {
		comment.ThreadSummary = input.ThreadSummary
	}
	if unchangedEdit(before, *comment, "EditedAt") {
		*comment = before
		return false
	}
	comment.Version++
	return true
}

func buildResolvedThreadSummary(root domain.Comment, siblings []domain.Comment) string {
	parts := []string{strings.TrimSpace(root.Body)}
	for _, item := range siblings {
		if item.ParentID != nil && *item.ParentID == root.ID {
			parts = append(parts, strings.TrimSpace(item.Body))
		}
	}
	joined := strings.Join(filterNonEmpty(parts), " · ")
	return truncateRunes(joined, 280)
}

func filterNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit-1]) + "…"
}

func teamResolvedThreadSummaries(data *domain.Bootstrap, teamID string) bool {
	if data == nil || data.TeamSettings == nil {
		return false
	}
	settings, ok := data.TeamSettings[teamID]
	if !ok {
		return false
	}
	return settings.ResolvedSummaries
}
