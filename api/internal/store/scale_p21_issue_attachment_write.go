package store

import (
	"context"
	"fmt"
	"reflect"

	"flow/api/internal/domain"
)

type issueAttachmentMutationKey struct{}

type issueAttachmentProfile struct {
	upsert bool
}

type scopedIssuePointProfile uint8

const (
	scopedIssueReactionProfile scopedIssuePointProfile = iota
	scopedIssueAttachmentProfile
	scopedIssueRecurrenceTimingProfile
)

// ContextWithIssueAttachmentMutation declares the existing attachment callback.
// Its event and payload keep their original transaction and realtime contract.
func ContextWithIssueAttachmentMutation(ctx context.Context) context.Context {
	return context.WithValue(ctx, issueAttachmentMutationKey{}, issueAttachmentProfile{})
}

// ContextWithIssueAttachmentUpsertMutation retains the caller's original event
// even when its earlier URL lookup differs from the transaction's lookup.
func ContextWithIssueAttachmentUpsertMutation(ctx context.Context) context.Context {
	return context.WithValue(ctx, issueAttachmentMutationKey{}, issueAttachmentProfile{upsert: true})
}

func issueAttachmentMutation(ctx context.Context) bool {
	_, marked := ctx.Value(issueAttachmentMutationKey{}).(issueAttachmentProfile)
	return marked
}

func issueAttachmentUpsertMutation(ctx context.Context) bool {
	profile, _ := ctx.Value(issueAttachmentMutationKey{}).(issueAttachmentProfile)
	return profile.upsert
}

func boundedIssueAttachmentEvent(ctx context.Context, event string) bool {
	if !issueAttachmentMutation(ctx) || IsIssueDiscussionMutation(ctx) {
		return false
	}
	if UsesIssueRecordMutations(ctx) {
		return event == "attachment.created" || event == "attachment.updated"
	}
	return !issueAttachmentUpsertMutation(ctx) && event == "issue.link_created"
}

func (s *SQLiteStore) mutateBoundedIssueReaction(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	return s.mutateBoundedScopedIssuePoint(ctx, workspace, event, payload, scopedIssueReactionProfile, mutate)
}

func (s *SQLiteStore) mutateBoundedIssueAttachment(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	return s.mutateBoundedScopedIssuePoint(ctx, workspace, event, payload, scopedIssueAttachmentProfile, mutate)
}

func scopedIssuePointEvent(ctx context.Context, event string, profile scopedIssuePointProfile) bool {
	switch profile {
	case scopedIssueReactionProfile:
		return event == "issue.reaction_toggled"
	case scopedIssueAttachmentProfile:
		return boundedIssueAttachmentEvent(ctx, event)
	case scopedIssueRecurrenceTimingProfile:
		return issueRecurrenceTimingMutation(ctx) && event == "issue.updated"
	}
	return false
}

func validateScopedIssueAttachment(before, after domain.Bootstrap, event string, upsert bool) error {
	refuse := func() error {
		return fmt.Errorf("%w: attachment callback changed undeclared inputs", ErrBoundedMetadataCoverage)
	}
	if len(before.Issues) != len(after.Issues) {
		return refuse()
	}
	ids := make(map[string]bool, len(before.Issues))
	for index, original := range before.Issues {
		next := after.Issues[index]
		ids[original.ID] = true
		original.Attachments = next.Attachments
		if event == "attachment.created" && !upsert {
			original.Description, original.DescriptionState = next.Description, next.DescriptionState
			original.DocumentContent, original.CycleID = next.DocumentContent, next.CycleID
		}
		if !reflect.DeepEqual(original, next) {
			return refuse()
		}
	}
	if len(changedMetadataFields(collectionMetadata(before), collectionMetadata(after))) != 0 ||
		!reflect.DeepEqual(before.Comments, after.Comments) ||
		!reflect.DeepEqual(before.Notifications, after.Notifications) ||
		!reflect.DeepEqual(before.NotificationDeliveries, after.NotificationDeliveries) {
		return refuse()
	}
	if event == "attachment.updated" && !upsert {
		if !reflect.DeepEqual(before.Activities, after.Activities) {
			return refuse()
		}
		return nil
	}
	for key, previous := range before.Activities {
		next, present := after.Activities[key]
		if !present || len(next) < len(previous) || len(previous) != 0 && !reflect.DeepEqual(previous, next[:len(previous)]) {
			return refuse()
		}
	}
	for key, next := range after.Activities {
		previous := before.Activities[key]
		if len(next) < len(previous) {
			return refuse()
		}
		for _, activity := range next[len(previous):] {
			if !ids[key] || activity.Type != "issue.link_created" && (upsert || activity.Type != "attachment.created") {
				return refuse()
			}
		}
	}
	return nil
}

// Markdown embed uses the proven ordinary-update selection for cycle defaults,
// recurrence and user settings. The original attachment event payload is not
// converted to IssueUpdateInput.
func (s *SQLiteStore) loadIssueAttachmentInputs(ctx context.Context, workspace string, data *domain.Bootstrap, target domain.Issue) (*issueHotInputs, error) {
	if len(data.Issues) == 0 {
		return nil, nil
	}
	input := domain.IssueUpdateInput{Description: &target.Description}
	read, err := s.loadIssueSelectedInputs(ctx, workspace, data, target, input)
	if err != nil {
		return nil, err
	}
	var baseline domain.Bootstrap
	read.install(data, &baseline)
	return read, nil
}
