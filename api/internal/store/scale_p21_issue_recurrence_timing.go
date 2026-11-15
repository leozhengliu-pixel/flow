package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type issueRecurrenceTimingMutationKey struct{}

// ContextWithIssueRecurrenceTimingMutation declares the existing ticker's
// timing-only callback without replacing its captured clock or event payload.
func ContextWithIssueRecurrenceTimingMutation(ctx context.Context) context.Context {
	return context.WithValue(ctx, issueRecurrenceTimingMutationKey{}, true)
}

func issueRecurrenceTimingMutation(ctx context.Context) bool {
	marked, _ := ctx.Value(issueRecurrenceTimingMutationKey{}).(bool)
	return marked
}

func recurrenceTimingScope(ctx context.Context) ([]string, bool) {
	scope, ok := mutationScopeFromContext(ctx)
	if !ok || UsesIssueRecordMutations(ctx) {
		return nil, false
	}
	ids := scope.IssueIDs
	scope.IssueIDs = nil
	if !reflect.DeepEqual(scope, MutationScope{}) {
		return nil, false
	}
	aggregate := mutationAggregateID(ctx)
	if aggregate == "" {
		return nil, false
	}
	expected := []string{aggregate}
	if trimmed := strings.TrimSpace(aggregate); trimmed != "" && trimmed != aggregate {
		expected = append(expected, trimmed)
	}
	return ids, slices.Equal(ids, expected)
}

func (s *SQLiteStore) mutateBoundedIssueRecurrenceTiming(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	return s.mutateBoundedScopedIssuePoint(ctx, workspace, event, payload, scopedIssueRecurrenceTimingProfile, mutate)
}

func validateScopedIssueRecurrenceTiming(before, after domain.Bootstrap) error {
	refuse := func() error {
		return fmt.Errorf("%w: recurrence timing callback changed undeclared inputs", ErrBoundedMetadataCoverage)
	}
	if len(before.Issues) != len(after.Issues) {
		return refuse()
	}
	for index, original := range before.Issues {
		next := after.Issues[index]
		original.DueDate, original.NextOccurrenceAt = next.DueDate, next.NextOccurrenceAt
		original.Version, original.UpdatedAt = next.Version, next.UpdatedAt
		if !reflect.DeepEqual(original, next) {
			return refuse()
		}
	}
	if len(changedMetadataFields(collectionMetadata(before), collectionMetadata(after))) != 0 ||
		!reflect.DeepEqual(before.Viewer.Via, after.Viewer.Via) ||
		!reflect.DeepEqual(before.Comments, after.Comments) || !reflect.DeepEqual(before.Activities, after.Activities) ||
		!reflect.DeepEqual(before.Notifications, after.Notifications) || !reflect.DeepEqual(before.NotificationDeliveries, after.NotificationDeliveries) {
		return refuse()
	}
	return nil
}

// The ticker retains its native result. Its temporary framed issue image uses
// the same RFC3339 representation the original record writer persists.
func recurrenceTimingFrameData(ctx context.Context, data domain.Bootstrap) (domain.Bootstrap, error) {
	inputs, _ := ctx.Value(metadataTimestampInputsKey{}).(*MetadataTimestampInputs)
	if inputs == nil {
		return domain.Bootstrap{}, ErrMetadataRecordQuery
	}
	data.Issues = slices.Clone(data.Issues)
	for index := range data.Issues {
		value := data.Issues[index].NextOccurrenceAt
		if value == nil {
			continue
		}
		parsed, err := inputs.ParseRFC3339(value.Format(time.RFC3339Nano))
		if err != nil {
			return domain.Bootstrap{}, err
		}
		data.Issues[index].NextOccurrenceAt = &parsed
	}
	return data, nil
}
