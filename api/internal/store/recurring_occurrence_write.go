package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type recurringOccurrenceWriteKey struct{}

// ContextWithRecurringOccurrenceWrite declares the literal recurring-issue
// occurrence callback for sourceID. The caller's recurringIssueScope (source,
// its children and the candidate instances of the series) stays the scope
// declaration; the callback, event and payload are unchanged.
func ContextWithRecurringOccurrenceWrite(ctx context.Context, sourceID string) context.Context {
	return context.WithValue(ctx, recurringOccurrenceWriteKey{}, sourceID)
}

func recurringOccurrenceSource(ctx context.Context) (string, bool) {
	source, found := ctx.Value(recurringOccurrenceWriteKey{}).(string)
	return source, found && source != ""
}

func boundedRecurringOccurrenceEvent(ctx context.Context, event string) bool {
	_, selected := recurringOccurrenceSource(ctx)
	return selected && event == "issue.created" && !UsesIssueRecordMutations(ctx) && !IsIssueDiscussionMutation(ctx)
}

// mutateBoundedRecurringOccurrence runs the occurrence callback in the scoped
// image writer, in one transaction: the source rewrite, the new instance and
// its recreated sub-issues, their activities and SLA effects. Over the scoped
// limit it is the full image writer, as the literal scoped writer falls back
// to the full writer.
func (s *SQLiteStore) mutateBoundedRecurringOccurrence(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	sourceID, selected := recurringOccurrenceSource(ctx)
	scope, scoped := mutationScopeFromContext(ctx)
	if !s.BoundedMetadataEnabled() || !selected || !scoped || event != "issue.created" || mutate == nil ||
		!scope.CreateIssues || scope.Expand == nil || scope.Resolve != nil || !slices.Equal(scope.IssueIDs, []string{sourceID}) {
		return fmt.Errorf("%w: recurring occurrence outside its declaration", ErrBoundedMetadataCoverage)
	}
	checked := scope
	checked.IssueIDs, checked.Expand, checked.CreateIssues = nil, nil, false
	if !reflect.DeepEqual(checked, MutationScope{}) {
		return fmt.Errorf("%w: recurring occurrence scope has undeclared fields", ErrBoundedMetadataCoverage)
	}
	return s.mutateBoundedScopedImage(ctx, workspace, event, payload, &recurringOccurrenceImageProfile{sourceID: sourceID, scope: scope}, mutate)
}
