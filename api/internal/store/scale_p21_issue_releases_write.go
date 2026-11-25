package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type issueReleasesMutationKey struct{}

type issueReleasesMutation struct {
	issueID   string
	requested []string
}

// ContextWithIssueReleasesMutation declares the literal issue-release callback
// for issueID. requested is the request's release ID list; it only widens the
// selected release occurrences, the callback still validates it.
func ContextWithIssueReleasesMutation(ctx context.Context, issueID string, requested []string) context.Context {
	return context.WithValue(ctx, issueReleasesMutationKey{}, issueReleasesMutation{issueID: issueID, requested: slices.Clone(requested)})
}

func issueReleasesFromContext(ctx context.Context) (issueReleasesMutation, bool) {
	input, found := ctx.Value(issueReleasesMutationKey{}).(issueReleasesMutation)
	return input, found && input.issueID != ""
}

// boundedIssueReleasesEvent selects the closed release profile of the
// scoped (issue-record) or the full legacy issue writer for the declared issue.
func boundedIssueReleasesEvent(ctx context.Context, event string) bool {
	input, marked := issueReleasesFromContext(ctx)
	return marked && event == "issue.releases_updated" && input.issueID == mutationAggregateID(ctx) && !IsIssueDiscussionMutation(ctx)
}

func (s *SQLiteStore) mutateBoundedIssueReleases(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if UsesIssueRecordMutations(ctx) {
		return s.mutateBoundedScopedIssuePoint(ctx, workspace, event, payload, scopedIssueReleasesProfile, mutate)
	}
	return s.mutateBoundedLegacyDiscussion(ctx, workspace, event, payload, mutate)
}

// validateScopedIssueReleases checks everything but the release inputs (which
// detach validated and removed) and the activities (detach allows exactly the
// one appended issue.releases_updated entry).
func validateScopedIssueReleases(before, after domain.Bootstrap) error {
	if !reflect.DeepEqual(before.Issues, after.Issues) ||
		len(changedMetadataFields(collectionMetadata(before), collectionMetadata(after))) != 0 ||
		!reflect.DeepEqual(before.Comments, after.Comments) ||
		!reflect.DeepEqual(before.Notifications, after.Notifications) ||
		!reflect.DeepEqual(before.NotificationDeliveries, after.NotificationDeliveries) {
		return fmt.Errorf("%w: release callback changed undeclared inputs", ErrBoundedMetadataCoverage)
	}
	return nil
}
