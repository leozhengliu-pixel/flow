package main

import (
	"fmt"
	"reflect"

	"flow/api/internal/domain"
)

func checkArchivedIssueUpdate(issue domain.Issue, input domain.IssueUpdateInput) error {
	if issue.ArchivedAt == nil || issueUpdateIsNoop(issue, input) {
		return nil
	}
	// Restoration is a separate write, so combining it with edits cannot bypass the archive.
	if input.Archived != nil && !*input.Archived && reflect.DeepEqual(input, domain.IssueUpdateInput{Archived: input.Archived, ExpectedVersion: input.ExpectedVersion}) {
		return nil
	}
	return fmt.Errorf("%w: restore this archived issue before editing it", errInvalid)
}
