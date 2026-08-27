package store

import "flow/api/internal/domain"

// refreshResourceCounts derives aggregate counts from the relationships that
// own them. Stored counters are only denormalized read models.
func refreshResourceCounts(data *domain.Bootstrap) {
	counts := make(map[string]int, len(data.Labels))
	projectIssueCounts := make(map[string]int, len(data.Projects))

	for _, issue := range data.Issues {
		if issue.ArchivedAt != nil {
			continue
		}
		incrementLabelCounts(counts, labelIDs(issue.Labels))
		if issue.Project != nil {
			projectIssueCounts[issue.Project.ID]++
		}
	}
	for _, project := range data.Projects {
		if project.ArchivedAt != nil {
			continue
		}
		incrementLabelCounts(counts, project.LabelIDs)
	}
	for _, initiative := range data.Initiatives {
		incrementLabelCounts(counts, initiative.LabelIDs)
	}

	labelsByID := make(map[string]domain.IssueLabel, len(data.Labels))
	for index := range data.Labels {
		data.Labels[index].IssueCount = counts[data.Labels[index].ID]
		labelsByID[data.Labels[index].ID] = data.Labels[index]
	}
	for issueIndex := range data.Issues {
		for labelIndex := range data.Issues[issueIndex].Labels {
			if label, ok := labelsByID[data.Issues[issueIndex].Labels[labelIndex].ID]; ok {
				data.Issues[issueIndex].Labels[labelIndex] = label
			}
		}
	}
	for index := range data.Projects {
		data.Projects[index].IssueCount = projectIssueCounts[data.Projects[index].ID]
	}
}

func incrementLabelCounts(counts map[string]int, ids []string) {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		counts[id]++
	}
}

func labelIDs(labels []domain.IssueLabel) []string {
	ids := make([]string, 0, len(labels))
	for _, label := range labels {
		ids = append(ids, label.ID)
	}
	return ids
}
