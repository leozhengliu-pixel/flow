package main

import (
	"slices"

	"flow/api/internal/domain"
)

// Comment writes and their domain events. Every write path (the REST routes,
// MCP and the agent tools built on it, chat thread replies, which call the
// REST route) records a comment write under the event its resource's REST
// route uses, with the resource as the aggregate and the comment's ID in the
// payload:
//
//	issue (and milestone)  comment.created / comment.updated / comment.deleted
//	project                project.commented / project.comment_updated / project.comment_deleted
//	initiative             initiative.commented / initiative.comment_updated / initiative.comment_deleted
//	document               document.comment_created / document.comment_updated / document.comment_deleted
//	project update         project.update_commented / project.update_comment_updated / project.update_comment_deleted
//	initiative update      initiative.update_commented / initiative.update_comment_updated / initiative.update_comment_deleted
//
// A reply is a created comment with its parent comment in the payload. The
// consumers (chat group sync, webhooks, realtime, Slack, loops) key on
// these names and read the comment through the aggregate.

// commentEventPayload is the payload of a comment write recorded by MCP. The
// REST routes record their input with the comment's ID beside it
// (commentCreatedPayload, commentUpdatedPayload).
type commentEventPayload struct {
	CommentID string  `json:"commentId,omitempty"`
	ParentID  *string `json:"parentId,omitempty"`
	// UpdateID names the project or initiative update a comment is on.
	UpdateID string `json:"updateId,omitempty"`
	Body     string `json:"body,omitempty"`
}

type commentCreatedPayload struct {
	*domain.CommentCreateInput
	CommentID string `json:"commentId"`
}

type commentUpdatedPayload struct {
	domain.CommentUpdateInput
	CommentID string `json:"commentId"`
}

// mcpCommentEvent is the event type and aggregate of an MCP comment write:
// operation is create (targetID is the resource), or reply, update or delete
// (targetID is a comment). updateID names the update a comment is on.
func mcpCommentEvent(data domain.Bootstrap, targetID, operation string) (eventType, aggregateID, updateID string) {
	verb := map[string]string{"create": "created", "reply": "created", "update": "updated", "delete": "deleted"}[operation]
	named := func(kind, resource, update string) (string, string, string) {
		switch kind {
		case "project", "initiative":
			if verb == "created" {
				return kind + ".commented", resource, update
			}
			return kind + ".comment_" + verb, resource, update
		case "project_update", "initiative_update":
			prefix := kind[:len(kind)-len("_update")]
			if verb == "created" {
				return prefix + ".update_commented", resource, update
			}
			return prefix + ".update_comment_" + verb, resource, update
		case "document":
			return "document.comment_" + verb, resource, update
		}
		return "comment." + verb, resource, update
	}
	contentKind := func(resource string) string {
		if slices.ContainsFunc(data.Documents, func(document domain.Document) bool { return document.ID == resource }) {
			return "document"
		}
		return "issue"
	}
	if operation == "create" {
		if issue, err := mcpFindIssue(data, targetID); err == nil {
			return named("issue", issue.ID, "")
		}
		// The resources the write itself finds (mutateAnyComment), by ID.
		for _, project := range data.Projects {
			if equalFoldAny(targetID, project.ID, project.SlugID) {
				return named("project", project.ID, "")
			}
		}
		for _, initiative := range data.Initiatives {
			if equalFoldAny(targetID, initiative.ID, initiative.SlugID) {
				return named("initiative", initiative.ID, "")
			}
		}
		for _, document := range data.Documents {
			if equalFoldAny(targetID, document.ID, document.SlugID) {
				return named("document", document.ID, "")
			}
		}
		for projectID, updates := range data.ProjectUpdates {
			if slices.ContainsFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == targetID }) {
				return named("project_update", projectID, targetID)
			}
		}
		for initiativeID, updates := range data.InitiativeUpdates {
			if slices.ContainsFunc(updates, func(update domain.InitiativeUpdate) bool { return update.ID == targetID }) {
				return named("initiative_update", initiativeID, targetID)
			}
		}
		// A milestone keeps its comments under its own ID.
		return named("issue", targetID, "")
	}
	has := func(comments []domain.Comment) bool {
		return slices.ContainsFunc(comments, func(comment domain.Comment) bool { return comment.ID == targetID })
	}
	for resource, comments := range data.Comments {
		if has(comments) {
			return named(contentKind(resource), resource, "")
		}
	}
	for _, project := range data.Projects {
		if has(project.Comments) {
			return named("project", project.ID, "")
		}
	}
	for _, initiative := range data.Initiatives {
		if has(initiative.Comments) {
			return named("initiative", initiative.ID, "")
		}
	}
	for projectID, updates := range data.ProjectUpdates {
		for _, update := range updates {
			if has(update.Comments) {
				return named("project_update", projectID, update.ID)
			}
		}
	}
	for initiativeID, updates := range data.InitiativeUpdates {
		for _, update := range updates {
			if has(update.Comments) {
				return named("initiative_update", initiativeID, update.ID)
			}
		}
	}
	return named("issue", targetID, "")
}
