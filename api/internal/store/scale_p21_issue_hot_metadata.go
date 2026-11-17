package store

import (
	"fmt"
	"slices"

	"flow/api/internal/domain"
)

// refuseBoundedHotIssueMetadata is persistIssueMetadata on a bounded store.
// The legacy writer changes metadata only for the progress of projects an
// issue of the write belongs to (issueProgressChanges ignores issues without
// a project) and for the inherited scopes of documents when the issue moved
// team (documents is non-nil). Neither input exists on the non-project hot
// path, where the legacy writer returns no metadata; anything else is refused
// instead of being skipped against the policy-only resident copy. A team
// change is detected on the issues themselves as well, since the bounded
// callback's document selection may be empty.
func refuseBoundedHotIssueMetadata(before, after []domain.Issue, documents []domain.Document) error {
	teams := make(map[string]string, len(before))
	for _, issue := range before {
		teams[issue.ID] = issue.Team.ID
	}
	moved := slices.ContainsFunc(after, func(issue domain.Issue) bool {
		team, ok := teams[issue.ID]
		return ok && team != issue.Team.ID
	})
	if documents != nil || moved {
		return fmt.Errorf("%w: issue team move outside the project-input writer", ErrBoundedMetadataCoverage)
	}
	for _, issues := range [][]domain.Issue{before, after} {
		for _, issue := range issues {
			if issue.Project != nil && issue.Project.ID != "" {
				return fmt.Errorf("%w: project issue outside the project-input writer", ErrBoundedMetadataCoverage)
			}
		}
	}
	return nil
}
