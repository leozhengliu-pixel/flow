package store

import (
	"context"

	"flow/api/internal/domain"
)

// PopulateReleaseProgress refreshes release completion counts from the issue
// projection when the caller already loaded issues, or from bounded indexed
// queries when the bootstrap intentionally omits the issue collection.
func (s *SQLiteStore) PopulateReleaseProgress(ctx context.Context, data *domain.Bootstrap) error {
	if len(data.Issues) > 0 {
		applyReleaseProgress(data.Releases, issueStateTypesFromIssues(data.Issues))
		return nil
	}
	if data.Workspace.URLKey == "" {
		if metadata, ok := s.WorkspaceMetadata(""); ok {
			data.Workspace.URLKey = metadata.Workspace.URLKey
		}
	}
	ids := releaseIssueIDs(data.Releases)
	states, err := s.issueRecordStateTypes(ctx, data.Workspace.URLKey, ids)
	if err != nil {
		return err
	}
	applyReleaseProgress(data.Releases, states)
	return nil
}

func issueStateTypesFromIssues(issues []domain.Issue) map[string]string {
	states := make(map[string]string, len(issues))
	for _, issue := range issues {
		if issue.ArchivedAt == nil {
			states[issue.ID] = issue.State.Type
		}
	}
	return states
}

func releaseIssueIDs(releases []domain.Release) []string {
	seen := map[string]bool{}
	ids := []string{}
	for _, release := range releases {
		for _, id := range release.IssueIDs {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

func applyReleaseProgress(releases []domain.Release, states map[string]string) {
	for index := range releases {
		seen := map[string]bool{}
		total, completed := 0, 0
		for _, issueID := range releases[index].IssueIDs {
			if issueID == "" || seen[issueID] {
				continue
			}
			stateType, ok := states[issueID]
			if !ok {
				continue
			}
			seen[issueID] = true
			total++
			if stateType == "completed" || stateType == "canceled" {
				completed++
			}
		}
		releases[index].IssueCount = total
		releases[index].CompletedCount = completed
	}
}

func (s *SQLiteStore) issueRecordStateTypes(ctx context.Context, workspace string, ids []string) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		query := IssueRecordQuery{
			Workspace: workspace,
			Archived:  "false",
			Filter:    IssueFilter{Field: "id", Values: ids[start:end]},
		}
		where, args, err := issueRecordWhere(query)
		if err != nil {
			return nil, err
		}
		prefix, prefixArgs := issueAccessCTE(query)
		rows, err := s.db.QueryContext(ctx, prefix+"SELECT i.id,i.state_type FROM issue_records i WHERE "+where, append(prefixArgs, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, stateType string
			if err := rows.Scan(&id, &stateType); err != nil {
				rows.Close()
				return nil, err
			}
			result[id] = stateType
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return result, nil
}
