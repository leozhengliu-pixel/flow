package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// The users collection keeps people after they are removed from or
// suspended in the workspace, so their names stay on the issues, comments and
// history they are part of. A write may keep such a reference, but it may not
// add one: a new assignee, delegate, subscriber, project lead or member,
// initiative owner or triage owner who is no longer a member is refused, and
// former members get no new notifications. Membership comes from the store
// (Bootstrap.FormerMembers, read from workspace_memberships on first use).

// errFormerMember is the 400 a write gets for newly naming a former member.
var errFormerMember error = formerMemberError{}

type formerMemberError struct{}

func (formerMemberError) Error() string {
	return "This person is no longer a member of the workspace"
}
func (formerMemberError) Unwrap() error { return errInvalid }

// formerMember reports whether id names someone removed from or suspended in
// the workspace. Outside a store write (no lookup) nobody is.
func formerMember(data *domain.Bootstrap, id string) bool {
	return id != "" && data != nil && data.FormerMembers != nil && data.FormerMembers()[id]
}

// formerMemberOutsideWrite reports, for a request check that resolves people
// against the viewer's directory (which leaves removed people out), whether
// id names a former member: the check lets it through so the write refuses it
// with errFormerMember instead of a 403 that reads as a permission problem.
func (s *server) formerMemberOutsideWrite(ctx context.Context, workspace, id string) bool {
	if id == "" {
		return false
	}
	former, err := s.store.FormerMembers(ctx, workspace)
	return err == nil && former[id]
}

// errFormerMemberMention is the 400 a write gets for content that newly
// mentions a former member.
var errFormerMemberMention error = formerMemberMentionError{}

type formerMemberMentionError struct{}

func (formerMemberMentionError) Error() string {
	return "This person is no longer a member of the workspace and can't be mentioned"
}
func (formerMemberMentionError) Unwrap() error { return errInvalid }

// refuseNewFormerMemberMentions refuses editor content (after) that adds a
// mention node of a former member. Mentions the content already had (before)
// stay, so history and an edit that keeps them render and save as they are;
// a plain-text @handle is text and never refused.
func refuseNewFormerMemberMentions(data *domain.Bootstrap, before, after any) error {
	mentioned := []string{}
	collectMentionIDs(after, &mentioned)
	if len(mentioned) == 0 {
		return nil
	}
	kept := []string{}
	collectMentionIDs(before, &kept)
	for _, id := range mentioned {
		if !slices.Contains(kept, id) && formerMember(data, id) {
			return errFormerMemberMention
		}
	}
	return nil
}

// Imported replies must survive a former person's mention. Keep their label,
// but not the identity that would create a new mention or notification.
func formerMemberMentionsAsText(data *domain.Bootstrap, document map[string]any) map[string]any {
	var walk func(any) any
	walk = func(value any) any {
		switch node := value.(type) {
		case map[string]any:
			ids := []string{}
			copy := map[string]any{}
			for key, child := range node {
				if key != "content" {
					copy[key] = child
				}
			}
			collectMentionIDs(copy, &ids)
			if slices.ContainsFunc(ids, func(id string) bool { return formerMember(data, id) }) {
				attrs, _ := node["attrs"].(map[string]any)
				label, _ := attrs["label"].(string)
				if label == "" {
					label = ids[0]
				}
				text := map[string]any{"type": "text", "text": "@" + strings.TrimPrefix(label, "@")}
				if marks, ok := node["marks"]; ok {
					text["marks"] = marks
				}
				return text
			}
			for key, child := range node {
				copy[key] = walk(child)
			}
			return copy
		case []any:
			children := make([]any, len(node))
			for index, child := range node {
				children[index] = walk(child)
			}
			return children
		}
		return value
	}
	if document == nil {
		return nil
	}
	return walk(document).(map[string]any)
}

// refuseNewFormerMemberDescriptionMentions applies the mention rule to an
// issue write that replaces the description. Only request writes check it:
// templates, recurring issues and automations copy stored content as it is.
func refuseNewFormerMemberDescriptionMentions(data *domain.Bootstrap, issue *domain.Issue, input domain.IssueUpdateInput) error {
	if input.DescriptionData == nil && input.DescriptionState == nil && input.ContentState == nil {
		return nil
	}
	var previous any
	state := ""
	documentID := ""
	if issue != nil {
		documentID = "document_content_" + issue.ID
	}
	if issue != nil && issue.DocumentContent != nil {
		previous = issue.DocumentContent.ContentData
		state = issue.DocumentContent.ContentState
		documentID = firstNonEmpty(issue.DocumentContent.ID, documentID)
	}
	if issue != nil {
		var document any
		if json.Unmarshal([]byte(issue.DescriptionState), &document) == nil {
			previous = []any{previous, document}
		}
	}
	if input.ContentState != nil {
		if err := refuseFormerMemberStateMentions(data, previous, state, *input.ContentState, documentID); err != nil {
			return err
		}
	}
	if input.DescriptionState != nil {
		var document any
		if json.Unmarshal([]byte(*input.DescriptionState), &document) == nil {
			if err := refuseFormerMemberRichMentions(data, previous, state, document, documentID); err != nil {
				return err
			}
		}
	}
	return refuseFormerMemberRichMentions(data, previous, state, input.DescriptionData, documentID)
}

// refuseNewFormerMembers refuses ids that name a former member, except the
// ones already referenced (kept): a write that leaves an existing reference
// as it is never fails for it.
func refuseNewFormerMembers(data *domain.Bootstrap, kept []string, ids ...string) error {
	for _, id := range ids {
		if id != "" && !slices.Contains(kept, id) && formerMember(data, id) {
			return errFormerMember
		}
	}
	return nil
}
