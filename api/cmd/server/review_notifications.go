package main

import (
	"regexp"
	"strings"

	"flow/api/internal/domain"
)

func reviewNotificationEnabled(settings domain.UserSettings, event externalCodeReviewEvent) bool {
	action := strings.ToLower(firstNonEmpty(event.ObjectAttributes.Action, event.Action))
	if action == "review_requested" {
		if event.RequestedTeam != nil {
			return settings.GithubTeamReviewRequests
		}
		return settings.ReviewRequests
	}
	if strings.Contains(action, "check") || strings.Contains(action, "merge_queue") || strings.Contains(action, "merge_group") {
		return settings.ChecksMergeQueue
	}
	comment := event.Comment.Body + "\n" + event.Review.Body
	if strings.TrimSpace(comment) == "" {
		return true
	}
	switch settings.ReviewCommentsFilter {
	case "Exclude Bots":
		return !strings.EqualFold(event.Sender.Type, "Bot") && !strings.HasSuffix(strings.ToLower(event.Sender.Login), "[bot]")
	case "Mentions only":
		if settings.Username == "" {
			return false
		}
		pattern := `(?i)(^|[^a-z0-9_])@` + regexp.QuoteMeta(settings.Username) + `($|[^a-z0-9_-])`
		return regexp.MustCompile(pattern).MatchString(comment)
	}
	return true
}
