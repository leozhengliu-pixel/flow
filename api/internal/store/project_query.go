package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"maps"
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
	project.Milestones = slices.Clone(project.Milestones)
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
	for _, notification := range data.Notifications {
		if notification.RecipientID == data.Viewer.ID && notification.ReadAt == nil && notification.ArchivedAt == nil && notification.DeletedAt == nil && (notification.SnoozedUntil == nil || !notification.SnoozedUntil.After(time.Now())) {
			data.InboxUnreadCount++
		}
	}
	for _, review := range data.Reviews {
		if review.Status != "merged" && review.Status != "closed" && slices.Contains(review.ReviewerIDs, data.Viewer.ID) {
			data.ReviewCount++
		}
	}
	favoriteIDs := map[string]map[string]bool{}
	for _, favorite := range data.Favorites {
		ids := favoriteIDs[favorite.ResourceType]
		if ids == nil {
			ids = map[string]bool{}
			favoriteIDs[favorite.ResourceType] = ids
		}
		ids[favorite.ResourceID] = true
	}
	favoriteProjects := slices.DeleteFunc(slices.Clone(data.Projects), func(item domain.Project) bool { return !favoriteIDs["project"][item.ID] })
	for index := range favoriteProjects {
		favoriteProjects[index] = ProjectListProjection(favoriteProjects[index])
	}
	favoriteIssues := slices.DeleteFunc(slices.Clone(data.Issues), func(item domain.Issue) bool { return !favoriteIDs["issue"][item.ID] })
	for index := range favoriteIssues {
		favoriteIssues[index] = issueListProjection(favoriteIssues[index])
	}
	favoriteDocuments := slices.DeleteFunc(slices.Clone(data.Documents), func(item domain.Document) bool { return !favoriteIDs["document"][item.ID] })
	for index := range favoriteDocuments {
		favoriteDocuments[index].Content = ""
		favoriteDocuments[index].ContentState = ""
		favoriteDocuments[index].ContentData = nil
		favoriteDocuments[index].Revisions = nil
		favoriteDocuments[index].Permissions = nil
	}
	favoriteCustomers := slices.DeleteFunc(slices.Clone(data.Customers), func(item domain.Customer) bool { return !favoriteIDs["customer"][item.ID] })
	favoriteReleases := slices.DeleteFunc(slices.Clone(data.Releases), func(item domain.Release) bool { return !favoriteIDs["release"][item.ID] })
	neededPipelines := maps.Clone(favoriteIDs["release_pipeline"])
	if neededPipelines == nil {
		neededPipelines = map[string]bool{}
	}
	for _, release := range favoriteReleases {
		neededPipelines[release.PipelineID] = true
	}
	favoritePipelines := slices.DeleteFunc(slices.Clone(data.ReleasePipelines), func(item domain.ReleasePipeline) bool { return !neededPipelines[item.ID] })
	for index := range data.Projects {
		data.Projects[index] = ProjectListProjection(data.Projects[index])
	}
	data.Projects = []domain.Project{}
	data.Issues = []domain.Issue{}
	data.Comments = map[string][]domain.Comment{}
	data.Activities = map[string][]domain.ActivityEvent{}
	data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	data.ProviderJobs = []domain.ProviderJob{}
	data.Customers = favoriteCustomers
	data.States = []domain.WorkflowState{}
	// Current and upcoming cycles remain because they are part of sidebar navigation.
	data.IssueTemplates = []domain.IssueTemplate{}
	data.DocumentTemplates = []domain.DocumentTemplate{}
	data.Documents = favoriteDocuments
	data.CustomerRequests = []domain.CustomerRequest{}
	data.Releases = favoriteReleases
	data.ReleasePipelines = favoritePipelines
	data.CustomEmojis = []domain.CustomEmoji{}
	data.Asks = []domain.Ask{}
	data.Loops = []domain.Loop{}
	data.SLARules = []domain.SLARule{}
	data.IssueSLAs = []domain.IssueSLA{}
	data.SLAEvents = []domain.SLAEvent{}
	data.Drafts = []domain.Draft{}
	data.AuditLog = []domain.AuditLogEntry{}
	data.Trash = []domain.TrashEntry{}
	data.ImportJobs = []domain.ImportJob{}
	data.ExportJobs = []domain.ExportJob{}
	data.MigrationJobs = []domain.MigrationJob{}
	data.ProjectRelations = []domain.ProjectRelation{}
	data.InitiativeRelations = []domain.InitiativeRelation{}
	data.DocumentContentDrafts = []domain.DocumentContentDraft{}
	data.CustomerStatuses = []domain.CustomerStatus{}
	data.CustomerTiers = []domain.CustomerTier{}
	data.ReleaseNotes = []domain.ReleaseNote{}
	data.ReleaseHistory = []domain.ReleaseHistory{}
	data.TeamResourceSections = []domain.TeamResourceSection{}
	data.TeamPinnedResources = []domain.TeamPinnedResource{}
	data.AgentActivities = []domain.AgentActivity{}
	data.AIConversations = []domain.AIConversation{}
	data.AIPromptProgress = []domain.AIPromptProgress{}
	data.IssueSuggestions = []domain.IssueSuggestion{}
	data.Notifications = []domain.Notification{}
	data.NotificationPreferences = map[string]domain.NotificationPreferences{}
	data.NotificationDeliveries = []domain.NotificationDelivery{}
	data.PushSubscriptions = []domain.PushSubscription{}
	data.TriageResponsibilities = []domain.TriageResponsibility{}
	data.TriageRoutingRules = []domain.TriageRoutingRule{}
	data.TriageAssignments = []domain.TriageAssignment{}
	data.WorkflowDefinitions = []domain.WorkflowDefinition{}
	data.WorkflowRuns = []domain.WorkflowRun{}
	data.EmailIntakeAddresses = []domain.EmailIntakeAddress{}
	data.EmailIntakeMessages = []domain.EmailIntakeMessage{}
	data.APIKeys = []domain.APIKey{}
	data.Passkeys = []domain.Passkey{}
	data.PasskeyRegistrationChallenges = []domain.PasskeyRegistrationChallenge{}
	data.OAuthApplications = []domain.OAuthApplication{}
	data.OAuthAuthorizations = []domain.OAuthAuthorization{}
	data.Webhooks = []domain.Webhook{}
	data.IntegrationConnections = []domain.IntegrationConnection{}
	data.IdentityProviders = []domain.IdentityProvider{}
	data.IntegrationDeliveries = []domain.IntegrationDelivery{}
	data.GitAutomationStates = []domain.GitAutomationState{}
	data.TargetBranches = []domain.TargetBranch{}
	data.Reviews = []domain.CodeReview{}
	data.AgentSessions = []domain.AgentSession{}
	data.Projects = favoriteProjects
	data.Issues = favoriteIssues
	viewerTeams := map[string]bool{}
	data.TeamParents = make(map[string]string, len(data.TeamSettings))
	for teamID, settings := range data.TeamSettings {
		if settings.ParentTeamID != "" {
			data.TeamParents[teamID] = settings.ParentTeamID
		}
	}
	data.TeamMembers = slices.DeleteFunc(data.TeamMembers, func(member domain.TeamMember) bool {
		if member.UserID == data.Viewer.ID {
			viewerTeams[member.TeamID] = true
			return false
		}
		return true
	})
	for changed := true; changed; {
		changed = false
		for teamID := range viewerTeams {
			parentID := data.TeamSettings[teamID].ParentTeamID
			if parentID != "" && !viewerTeams[parentID] {
				viewerTeams[parentID] = true
				changed = true
			}
		}
	}
	teamSettings := make(map[string]domain.TeamSettings, len(viewerTeams))
	for teamID := range viewerTeams {
		if settings, ok := data.TeamSettings[teamID]; ok {
			teamSettings[teamID] = settings
		}
	}
	data.TeamSettings = teamSettings
	data.Members = []domain.WorkspaceMember{}
	data.Invitations = []domain.Invitation{}
	data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool {
		return label.ResourceType != "project" && !favoriteIDs["label"][label.ID]
	})
	data.LabelGroups = slices.DeleteFunc(data.LabelGroups, func(group domain.LabelGroup) bool { return group.ResourceType != "project" })
	data.SavedViews = slices.DeleteFunc(data.SavedViews, func(view domain.SavedView) bool { return view.Resource != "projects" && !favoriteIDs["view"][view.ID] })
	data.IssueCollectionPaged = true
}

func (s *SQLiteStore) QueryProjectDirectory(ctx context.Context, query ProjectRecordQuery) (ProjectRecordPage, error) {
	page := ProjectRecordPage{Items: []domain.Project{}, Total: -1}
	if strings.TrimSpace(query.Workspace) == "" {
		return page, ErrIssueQuery
	}
	if cached, ok := s.cacheGetProjectQuery(ctx, query); ok {
		return cached, nil
	}
	if projects, ok := s.projectSnapshot(query.Workspace); ok {
		page = filterProjectDirectory(projects, query)
		s.cacheSetProjectQuery(ctx, query, page)
		return page, nil
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
	s.cacheSetProjectQuery(ctx, query, page)
	return page, nil
}

func (s *SQLiteStore) projectSnapshot(workspace string) ([]domain.Project, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok || data.Projects == nil {
		return nil, false
	}
	return data.Projects, true
}

func filterProjectDirectory(projects []domain.Project, query ProjectRecordQuery) ProjectRecordPage {
	page := ProjectRecordPage{Items: []domain.Project{}, Total: -1}
	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	offset := decodeProjectCursor(query.Cursor)
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
	for _, project := range projects {
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
	page.HasMore = page.HasMore || matched > offset+returned
	if query.IncludeTotal {
		page.Total = int64(matched)
	}
	if page.HasMore {
		page.NextCursor = encodeProjectCursor(offset + len(page.Items))
	}
	return page
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
