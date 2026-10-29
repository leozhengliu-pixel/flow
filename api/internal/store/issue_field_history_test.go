package store

import (
	"errors"
	"testing"

	"flow/api/internal/domain"
)

func TestIssueFieldCASFailsClosedWithoutHistory(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			issue := createPeerIssue(t, first, workspace, "cas_history_"+raceStamp(), template)
			expected := issue.Version
			updated, err := first.UpdateIssueRecord(t.Context(), workspace, issue.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, item *domain.Issue) error { item.Title = "New title"; return nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := first.db.ExecContext(t.Context(), `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=?`, workspace, issueFieldHistoryKind, issue.ID); err != nil {
				t.Fatal(err)
			}
			priority := 4
			input := domain.IssueUpdateInput{Priority: &priority}
			callback := func(_ *domain.Bootstrap, item *domain.Issue) error { item.Priority = priority; return nil }
			if _, err := second.UpdateIssueRecord(t.Context(), workspace, issue.ID, &expected, IssueMutationScope{Payload: input}, callback); !errors.Is(err, ErrIssueVersion) {
				t.Fatalf("accepted missing history: %v", err)
			}
			if _, err := second.UpdateIssueRecord(t.Context(), workspace, issue.ID, &updated.Version, IssueMutationScope{Payload: input}, callback); err != nil {
				t.Fatal(err)
			}
			if _, err := first.UpdateIssueRecord(t.Context(), workspace, issue.ID, &expected, IssueMutationScope{Payload: domain.IssueUpdateInput{Icon: new("sparkle")}}, callback); !errors.Is(err, ErrIssueVersion) {
				t.Fatalf("accepted a version before the history baseline: %v", err)
			}
		})
	}
}

func TestIssueFieldCASAcrossTwoStores(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			issue := createPeerIssue(t, first, workspace, "field_cas_"+raceStamp(), template)
			expected := issue.Version
			_, err := first.UpdateIssueRecord(t.Context(), workspace, issue.ID, &expected, IssueMutationScope{}, func(_ *domain.Bootstrap, item *domain.Issue) error {
				item.Title = "Changed title"
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			priority := (issue.Priority + 1) % 5
			input := domain.IssueUpdateInput{Priority: &priority, ExpectedVersion: &expected}
			updated, err := second.UpdateIssueRecord(t.Context(), workspace, issue.ID, &expected, IssueMutationScope{Payload: input}, func(_ *domain.Bootstrap, item *domain.Issue) error {
				item.Priority = priority
				return nil
			})
			if err != nil || updated.Version != expected+2 || updated.Title != "Changed title" || updated.Priority != priority {
				t.Fatalf("disjoint stale patch: %+v %v", updated, err)
			}
			_, err = first.UpdateIssueRecord(t.Context(), workspace, issue.ID, &expected, IssueMutationScope{Payload: input}, func(_ *domain.Bootstrap, item *domain.Issue) error { item.Priority = priority; return nil })
			if !errors.Is(err, ErrIssueVersion) {
				t.Fatalf("same-field stale patch: %v", err)
			}
		})
	}
}
