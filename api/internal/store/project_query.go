package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// ProjectRecordQuery is the bounded directory contract used by the projects
// page. Rich descriptions, updates, comments and resources stay on the detail
// path; directory responses contain only fields needed to render and filter a
// row.
type ProjectRecordQuery struct {
	Workspace      string
	TeamIDs        []string
	Search         string
	Archived       string
	Cursor         string
	Limit          int
	IncludeTotal   bool
	AllowedTeamIDs []string
	Admin          bool
	Filters        []ProjectDirectoryFilter
}

type ProjectDirectoryFilter struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

type ProjectRecordPage struct {
	Items      []domain.Project `json:"items"`
	NextCursor string           `json:"nextCursor,omitempty"`
	HasMore    bool             `json:"hasMore"`
	Total      int64            `json:"total"`
}

type projectDirectoryCursor struct {
	Offset int `json:"offset"`
}

func encodeProjectCursor(offset int) string {
	raw, _ := json.Marshal(projectDirectoryCursor{Offset: offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeProjectCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0
	}
	var value projectDirectoryCursor
	if json.Unmarshal(raw, &value) != nil || value.Offset < 0 {
		return 0
	}
	return value.Offset
}

// ProjectListProjection removes unbounded detail fields while preserving the
// stable list model (including milestone names used by the list display).
func ProjectListProjection(project domain.Project) domain.Project {
	project.Description = ""
	project.Resources = nil
	project.Comments = nil
	project.DescriptionRevisions = nil
	project.IssueCountHistory = nil
	project.ScopeHistory = nil
	project.CompletedScopeHistory = nil
	project.InProgressScopeHistory = nil
	project.ProgressHistory = nil
	project.UpdateSchedule = nil
	for index := range project.Milestones {
		project.Milestones[index].Description = ""
	}
	return project
}

// ProjectListBootstrapProjection is applied only to an explicitly requested
// project-directory bootstrap. It prevents the browser from downloading issue
// bodies and project detail histories before the first list row is visible.
func ProjectListBootstrapProjection(data *domain.Bootstrap) {
	if data == nil {
		return
	}
	for index := range data.Projects {
		data.Projects[index] = ProjectListProjection(data.Projects[index])
	}
	data.Projects = []domain.Project{}
	data.Issues = []domain.Issue{}
	data.Comments = map[string][]domain.Comment{}
	data.Activities = map[string][]domain.ActivityEvent{}
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	data.IssueCollectionPaged = true
}

func (s *SQLiteStore) QueryProjectDirectory(ctx context.Context, query ProjectRecordQuery) (ProjectRecordPage, error) {
	page := ProjectRecordPage{Items: []domain.Project{}, Total: -1}
	if strings.TrimSpace(query.Workspace) == "" {
		return page, ErrIssueQuery
	}
	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	offset := decodeProjectCursor(query.Cursor)
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' ORDER BY collection_order,record_key`, query.Workspace)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	teamFilter := make(map[string]bool, len(query.TeamIDs))
	for _, id := range query.TeamIDs {
		teamFilter[id] = true
	}
	allowed := make(map[string]bool, len(query.AllowedTeamIDs))
	for _, id := range query.AllowedTeamIDs {
		allowed[id] = true
	}
	search := strings.ToLower(strings.TrimSpace(query.Search))
	matched := 0
	returned := 0
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return page, err
		}
		var project domain.Project
		if err := json.Unmarshal(raw, &project); err != nil {
			return page, err
		}
		if query.Archived != "all" && (project.ArchivedAt != nil) != (query.Archived == "true") {
			continue
		}
		if len(teamFilter) > 0 {
			found := false
			for _, id := range project.TeamIDs {
				if teamFilter[id] {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if !query.Admin && query.AllowedTeamIDs != nil {
			found := len(project.TeamIDs) == 0
			for _, id := range project.TeamIDs {
				if allowed[id] {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if search != "" && !strings.Contains(strings.ToLower(project.Name), search) && !strings.Contains(strings.ToLower(project.SlugID), search) && !strings.Contains(strings.ToLower(project.Summary), search) {
			continue
		}
		if !projectDirectoryFiltersMatch(project, query.Filters) {
			continue
		}
		if matched < offset {
			matched++
			continue
		}
		matched++
		if returned < limit {
			page.Items = append(page.Items, ProjectListProjection(project))
			returned++
		} else {
			page.HasMore = true
			if !query.IncludeTotal {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	page.HasMore = page.HasMore || matched > offset+returned
	if query.IncludeTotal {
		page.Total = int64(matched)
	}
	if page.HasMore {
		page.NextCursor = encodeProjectCursor(offset + len(page.Items))
	}
	return page, nil
}

func projectDirectoryFiltersMatch(project domain.Project, filters []ProjectDirectoryFilter) bool {
	for _, filter := range filters {
		matched := false
		for _, value := range filter.Values {
			if projectDirectoryValueMatches(project, filter.Field, value) {
				matched = true
				break
			}
		}
		if strings.EqualFold(filter.Operator, "isNot") {
			matched = !matched
		}
		if !matched {
			return false
		}
	}
	return true
}

func projectDirectoryValueMatches(project domain.Project, field, value string) bool {
	switch field {
	case "status":
		return project.Status.ID == value || project.Status.Name == value || project.Status.Type == value
	case "priority":
		priority := map[int]string{0: "none", 1: "urgent", 2: "high", 3: "medium", 4: "low"}[project.Priority]
		return priority == value
	case "lead":
		return project.Lead != nil && project.Lead.ID == value || project.Lead == nil && value == ""
	case "members":
		return slices.Contains(project.MemberIDs, value)
	case "health":
		return strings.ReplaceAll(strings.ToLower(project.Health), "_", "-") == strings.ToLower(value) || map[string]string{"onTrack": "on-track", "atRisk": "at-risk", "offTrack": "off-track", "noUpdate": "no-update"}[project.Health] == value
	case "dates":
		if value == "has-target" {
			return project.TargetDate != nil && *project.TargetDate != ""
		}
		if value == "no-target" {
			return project.TargetDate == nil || *project.TargetDate == ""
		}
		return value == "overdue" && project.TargetDate != nil && *project.TargetDate != "" && *project.TargetDate < time.Now().UTC().Format("2006-01-02")
	case "milestones":
		return slices.ContainsFunc(project.Milestones, func(item domain.ProjectMilestone) bool { return item.ID == value || item.Name == value })
	case "labels":
		return value == "" && len(project.LabelIDs) == 0 || slices.Contains(project.LabelIDs, value)
	case "teams":
		return slices.Contains(project.TeamIDs, value)
	case "project":
		return project.ID == value || project.SlugID == value
	}
	return false
}
