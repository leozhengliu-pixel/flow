package store

import (
	"context"
	"fmt"
	"reflect"

	"flow/api/internal/domain"
)

type issueRelationMutationKey struct{}

// ContextWithIssueRelationMutation declares the existing scoped relation
// callback. The original payload, scope, and transaction semantics are retained.
func ContextWithIssueRelationMutation(ctx context.Context) context.Context {
	return context.WithValue(ctx, issueRelationMutationKey{}, true)
}

func boundedIssueRelationEvent(ctx context.Context, event string) bool {
	marked, _ := ctx.Value(issueRelationMutationKey{}).(bool)
	return marked && UsesIssueRecordMutations(ctx) && !IsIssueDiscussionMutation(ctx) && event == "issue.relation_added"
}

func (s *SQLiteStore) mutateBoundedIssueRelation(ctx context.Context, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	return s.mutateBoundedScopedIssuePoint(ctx, workspace, event, payload, scopedIssueRelationProfile, mutate)
}

// This follows mutateIssueScope's relation-only selection: source, target, then
// both stored parent chains. It shares its 64-level and 1000-record limits.
func loadScopedIssueRelationRows(ctx context.Context, rows *issueMutationRows, records map[string]storedIssueRecord, payload any) error {
	input, supported := payload.(domain.RelationCreateInput)
	if !supported {
		return fmt.Errorf("%w: unsupported relation input", ErrBoundedMetadataCoverage)
	}
	target, err := rows.load(ctx, []string{input.RelatedIssueID}, true)
	if err != nil {
		return err
	}
	for id, row := range target {
		records[id] = row
	}
	if input.Type == "parent_of" || input.Type == "sub_issue_of" {
		for depth := 0; ; depth++ {
			var parents []string
			for _, row := range records {
				if row.issue.ParentID != nil && *row.issue.ParentID != "" {
					if _, found := records[*row.issue.ParentID]; !found {
						parents = append(parents, *row.issue.ParentID)
					}
				}
			}
			if len(parents) == 0 {
				break
			}
			if depth == 64 {
				return fmt.Errorf("issue ancestor limit exceeded")
			}
			ancestors, err := rows.load(ctx, parents, true)
			if err != nil {
				return err
			}
			if len(ancestors) == 0 {
				break
			}
			for id, row := range ancestors {
				records[id] = row
			}
		}
	}
	if len(records) > 1000 {
		return fmt.Errorf("issue mutation scope exceeds 1000 records")
	}
	return nil
}

func validateScopedIssueRelation(before, after domain.Bootstrap) error {
	refuse := func() error {
		return fmt.Errorf("%w: relation callback changed undeclared inputs", ErrBoundedMetadataCoverage)
	}
	if len(before.Issues) != len(after.Issues) {
		return refuse()
	}
	ids := make(map[string]bool, len(before.Issues))
	for index, original := range before.Issues {
		next := after.Issues[index]
		ids[original.ID] = true
		original.Relations, original.ParentID, original.SubIssueIDs = next.Relations, next.ParentID, next.SubIssueIDs
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
	if len(before.Activities) != 0 {
		return refuse()
	}
	for id, items := range after.Activities {
		if !ids[id] {
			return refuse()
		}
		for _, item := range items {
			if item.Type != "issue.relation_added" {
				return refuse()
			}
		}
	}
	return nil
}

func writeScopedIssueRelationContent(ctx context.Context, tx *sqlTx, workspace string, data domain.Bootstrap) error {
	// The literal relation writer starts with empty content and only appends
	// these activities, preserving all durable comments and history.
	for resource, items := range data.Activities {
		for _, item := range items {
			if err := writeContentRecord(ctx, tx, workspace, "activity", resource, item); err != nil {
				return err
			}
		}
	}
	return nil
}
