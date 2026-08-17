package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db               *sql.DB
	mu               sync.RWMutex
	workspaces       map[string]domain.Bootstrap
	lastWorkspaceKey string
	viewer           domain.User
	realtimeSink     func(string, domain.RealtimeEvent)
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.loadOrSeed(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.ensureAuthSeed(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) SetRealtimeSink(sink func(string, domain.RealtimeEvent)) {
	s.mu.Lock()
	s.realtimeSink = sink
	s.mu.Unlock()
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS workspace_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  data BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_states (
  workspace_key TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL UNIQUE,
  data BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS account_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  last_workspace_key TEXT NOT NULL DEFAULT '',
  viewer BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS domain_events (
  id TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,
  aggregate_id TEXT NOT NULL,
  payload BLOB NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS domain_events_aggregate_idx ON domain_events(aggregate_id, created_at);
CREATE TABLE IF NOT EXISTS auth_users (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL COLLATE NOCASE UNIQUE,
  name TEXT NOT NULL,
  display_name TEXT NOT NULL,
  avatar_url TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  email_verified_at TEXT,
  active INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_sessions (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS auth_sessions_user_idx ON auth_sessions(user_id, expires_at);
CREATE TABLE IF NOT EXISTS auth_account_state (
  user_id TEXT PRIMARY KEY REFERENCES auth_users(id) ON DELETE CASCADE,
  last_workspace_key TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_tokens (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at TEXT,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workspace_memberships (
  workspace_id TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  joined_at TEXT NOT NULL,
  last_seen_at TEXT,
  PRIMARY KEY(workspace_id, user_id)
);
CREATE INDEX IF NOT EXISTS workspace_memberships_user_idx ON workspace_memberships(user_id, status);
CREATE TABLE IF NOT EXISTS team_memberships (
  workspace_id TEXT NOT NULL,
  team_id TEXT NOT NULL,
  user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
  role TEXT NOT NULL DEFAULT 'member',
  joined_at TEXT NOT NULL,
  PRIMARY KEY(workspace_id, team_id, user_id)
);
CREATE TABLE IF NOT EXISTS workspace_invitations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  email TEXT NOT NULL COLLATE NOCASE,
  role TEXT NOT NULL,
  team_ids BLOB NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  inviter_id TEXT NOT NULL REFERENCES auth_users(id),
  status TEXT NOT NULL DEFAULT 'pending',
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  accepted_at TEXT
);
CREATE INDEX IF NOT EXISTS workspace_invitations_email_idx ON workspace_invitations(email, status);
CREATE TABLE IF NOT EXISTS search_history (
  user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  query TEXT NOT NULL,
  use_count INTEGER NOT NULL DEFAULT 1,
  last_used_at TEXT NOT NULL,
  PRIMARY KEY(user_id, workspace_id, query)
);
CREATE INDEX IF NOT EXISTS search_history_recent_idx ON search_history(user_id, workspace_id, last_used_at DESC);
CREATE TABLE IF NOT EXISTS recently_viewed (
  user_id TEXT NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  last_viewed_at TEXT NOT NULL,
  PRIMARY KEY(user_id, workspace_id, resource_type, resource_id)
);
CREATE INDEX IF NOT EXISTS recently_viewed_recent_idx ON recently_viewed(user_id, workspace_id, last_viewed_at DESC);
`)
	return err
}

func (s *SQLiteStore) loadOrSeed(ctx context.Context) error {
	s.workspaces = map[string]domain.Bootstrap{}
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,data FROM workspace_states ORDER BY updated_at ASC`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(raw, &data); err != nil {
			rows.Close()
			return err
		}
		normalize(&data)
		s.workspaces[key] = data
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(s.workspaces) > 0 {
		var viewerRaw []byte
		_ = s.db.QueryRowContext(ctx, `SELECT last_workspace_key,viewer FROM account_state WHERE id = 1`).Scan(&s.lastWorkspaceKey, &viewerRaw)
		_ = json.Unmarshal(viewerRaw, &s.viewer)
		if _, ok := s.workspaces[s.lastWorkspaceKey]; !ok {
			s.lastWorkspaceKey = firstWorkspaceKey(s.workspaces)
		}
		if s.viewer.ID == "" {
			s.viewer = s.workspaces[s.lastWorkspaceKey].Viewer
		}
		return nil
	}

	var raw []byte
	err = s.db.QueryRowContext(ctx, `SELECT data FROM workspace_state WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		data := Seed()
		normalize(&data)
		s.workspaces[data.Workspace.URLKey] = data
		s.lastWorkspaceKey = data.Workspace.URLKey
		s.viewer = data.Viewer
		return s.persistWorkspace(ctx, data.Workspace.URLKey, data, nil)
	}
	if err != nil {
		return err
	}
	var data domain.Bootstrap
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	ensureCanonicalWorkflowStates(&data)
	ensureCanonicalLabels(&data)
	ensureCanonicalNotifications(&data)
	ensureCanonicalInitiatives(&data)
	ensureCanonicalCycles(&data)
	normalize(&data)
	s.workspaces[data.Workspace.URLKey] = data
	s.lastWorkspaceKey = data.Workspace.URLKey
	s.viewer = data.Viewer
	return s.persistWorkspace(ctx, data.Workspace.URLKey, data, nil)
}

func ensureCanonicalCycles(data *domain.Bootstrap) bool {
	if len(data.Cycles) > 0 || len(data.Teams) == 0 || len(data.Issues) == 0 {
		return false
	}
	now := time.Now().UTC()
	start := cycleWeekStart(now)
	teamID := data.Teams[0].ID
	data.Cycles = []domain.Cycle{
		{ID: "cycle_47", Number: 47, Name: "Cycle 47", TeamID: teamID, StartsAt: start.AddDate(0, 0, -14), EndsAt: start.AddDate(0, 0, -1), Status: "completed", Capacity: 4, CreatedAt: start.AddDate(0, 0, -42), UpdatedAt: now},
		{ID: "cycle_48", Number: 48, Name: "Cycle 48", TeamID: teamID, StartsAt: start, EndsAt: start.AddDate(0, 0, 13), Status: "current", Capacity: 4, Favorite: true, CreatedAt: start.AddDate(0, 0, -28), UpdatedAt: now},
		{ID: "cycle_49", Number: 49, Name: "Cycle 49", TeamID: teamID, StartsAt: start.AddDate(0, 0, 14), EndsAt: start.AddDate(0, 0, 27), Status: "upcoming", Capacity: 4, CreatedAt: start.AddDate(0, 0, -14), UpdatedAt: now},
		{ID: "cycle_50", Number: 50, Name: "Cycle 50", TeamID: teamID, StartsAt: start.AddDate(0, 0, 28), EndsAt: start.AddDate(0, 0, 41), Status: "upcoming", Capacity: 4, CreatedAt: now, UpdatedAt: now},
	}
	data.CycleSettings = map[string]domain.CycleSettings{teamID: {Enabled: true, DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2}}
	assignments := []string{"cycle_48", "cycle_48", "cycle_48", "cycle_47", "cycle_49"}
	for index := range data.Issues {
		if index >= len(assignments) {
			break
		}
		if data.Issues[index].CycleID == nil {
			data.Issues[index].CycleID = stringPointer(assignments[index])
		}
	}
	return true
}

func ensureCanonicalInitiatives(data *domain.Bootstrap) bool {
	if len(data.Initiatives) > 0 || len(data.Projects) == 0 {
		return false
	}
	now := time.Now().UTC()
	target := now.AddDate(0, 2, 0).Format("2006-01-02")
	project := &data.Projects[0]
	initiative := domain.Initiative{
		ID: "initiative_operational_excellence", Name: "Operational excellence", SlugID: "operational-excellence",
		Summary: "Make core workflows dependable at production scale", Description: "Coordinate the active reliability projects and keep their outcomes visible across the workspace.",
		Icon: "Initiative", Color: "#d15f64", Status: "active", Priority: 2, PriorityLabel: "High", Health: "onTrack",
		Owner: &data.Viewer, ProjectIDs: []string{project.ID}, LabelIDs: []string{}, Resources: []domain.InitiativeResource{}, Comments: []domain.Comment{},
		TargetDate: &target, CreatedAt: now.AddDate(0, -2, 0), UpdatedAt: now,
	}
	data.Initiatives = []domain.Initiative{initiative}
	project.Initiatives = append(project.Initiatives, initiative.ID)
	if data.InitiativeUpdates == nil {
		data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	}
	data.InitiativeUpdates[initiative.ID] = []domain.InitiativeUpdate{{
		ID: "initiative_update_operational_excellence_1", InitiativeID: initiative.ID,
		Body: "The reliability program is moving forward with the current project scope and target intact.", Health: "onTrack",
		CreatedAt: now.AddDate(0, 0, -3), User: data.Viewer, Comments: []domain.Comment{}, Reactions: map[string][]string{},
	}}
	return true
}

func ensureCanonicalWorkflowStates(data *domain.Bootstrap) bool {
	existing := make(map[string]struct{}, len(data.States))
	for _, state := range data.States {
		existing[state.ID] = struct{}{}
	}
	changed := false
	for _, state := range canonicalWorkflowStates() {
		if _, ok := existing[state.ID]; ok {
			continue
		}
		data.States = append(data.States, state)
		changed = true
	}
	return changed
}

func ensureCanonicalLabels(data *domain.Bootstrap) bool {
	existing := make(map[string]domain.IssueLabel, len(data.Labels))
	for _, label := range data.Labels {
		existing[label.ID] = label
	}
	changed := false
	for _, label := range canonicalLabels() {
		current, ok := existing[label.ID]
		if !ok {
			data.Labels = append(data.Labels, label)
			existing[label.ID] = label
			changed = true
			continue
		}
		if current.Description == "" || current.IssueCount == 0 || current.Scope == "" {
			for i := range data.Labels {
				if data.Labels[i].ID == label.ID {
					data.Labels[i].Description = label.Description
					data.Labels[i].IssueCount = label.IssueCount
					data.Labels[i].Scope = label.Scope
				}
			}
			changed = true
		}
	}
	return changed
}

// ensureCanonicalNotifications provides the one-time Inbox projection for
// persisted workspaces created before Notification existed. IDs are derived
// from a real source record, so subsequent application starts are idempotent
// and never overwrite a user's notification lifecycle state.
func ensureCanonicalNotifications(data *domain.Bootstrap) bool {
	canonical := projectNotifications(data)
	existing := make(map[string]int, len(data.Notifications))
	for i, notification := range data.Notifications {
		existing[notification.ID] = i
	}

	changed := false
	for _, expected := range canonical {
		index, ok := existing[expected.ID]
		if !ok {
			data.Notifications = append(data.Notifications, expected)
			changed = true
			continue
		}
		if reconcileNotification(&data.Notifications[index], expected) {
			changed = true
		}
	}
	return changed
}

func reconcileNotification(current *domain.Notification, expected domain.Notification) bool {
	changed := false
	setString := func(target *string, value string) {
		if *target == "" && value != "" {
			*target = value
			changed = true
		}
	}
	setString(&current.RecipientID, expected.RecipientID)
	setString(&current.Type, expected.Type)
	setString(&current.SourceType, expected.SourceType)
	setString(&current.SourceID, expected.SourceID)
	setString(&current.IssueID, expected.IssueID)
	setString(&current.CommentID, expected.CommentID)
	setString(&current.ActivityID, expected.ActivityID)
	if current.Actor.ID == "" && expected.Actor.ID != "" {
		current.Actor = expected.Actor
		changed = true
	}
	if current.CreatedAt.IsZero() {
		current.CreatedAt = expected.CreatedAt
		changed = true
	}
	if current.UpdatedAt.IsZero() {
		current.UpdatedAt = expected.UpdatedAt
		changed = true
	}
	return changed
}

func projectNotifications(data *domain.Bootstrap) []domain.Notification {
	notifications := make([]domain.Notification, 0)
	for _, issue := range data.Issues {
		if issue.ArchivedAt != nil {
			continue
		}
		comments := data.Comments[issue.ID]
		for _, comment := range comments {
			notifications = append(notifications, domain.Notification{
				ID:          "notification_comment_" + comment.ID,
				RecipientID: data.Viewer.ID,
				Type:        "comment",
				SourceType:  "comment",
				SourceID:    comment.ID,
				IssueID:     issue.ID,
				CommentID:   comment.ID,
				Actor:       comment.User,
				CreatedAt:   comment.CreatedAt,
				UpdatedAt:   comment.CreatedAt,
			})
		}
		for _, activity := range data.Activities[issue.ID] {
			if activity.Type == "comment.created" && hasComment(comments, activity.Metadata["commentId"]) {
				continue
			}
			notifications = append(notifications, domain.Notification{
				ID:          "notification_activity_" + activity.ID,
				RecipientID: data.Viewer.ID,
				Type:        activityNotificationType(activity),
				SourceType:  "activity",
				SourceID:    activity.ID,
				IssueID:     issue.ID,
				ActivityID:  activity.ID,
				Actor:       activity.Actor,
				CreatedAt:   activity.CreatedAt,
				UpdatedAt:   activity.CreatedAt,
			})
		}
	}
	return notifications
}

func hasComment(comments []domain.Comment, id string) bool {
	for _, comment := range comments {
		if comment.ID == id {
			return true
		}
	}
	return false
}

func activityNotificationType(activity domain.ActivityEvent) string {
	if activity.Type == "comment.created" {
		return "comment"
	}
	if activity.Type == "issue.updated" && activity.Metadata["assignee"] != "" {
		return "assignment"
	}
	return "activity"
}

func normalize(data *domain.Bootstrap) {
	for index := range data.States {
		if data.States[index].TeamID == "" && data.States[index].ID == "state_backlog" {
			data.States[index].Default = true
		}
		if data.States[index].TeamID == "" && data.States[index].ID == "state_duplicate" {
			data.States[index].Reserved = true
		}
	}
	if data.Settings == nil {
		data.Settings = map[string]any{}
	}
	if data.Members == nil {
		data.Members = []domain.WorkspaceMember{}
	}
	if data.TeamMembers == nil {
		data.TeamMembers = []domain.TeamMember{}
	}
	if data.Invitations == nil {
		data.Invitations = []domain.Invitation{}
	}
	if data.Cycles == nil {
		data.Cycles = []domain.Cycle{}
	}
	if data.CycleSettings == nil {
		data.CycleSettings = map[string]domain.CycleSettings{}
	}
	if data.TeamSettings == nil {
		data.TeamSettings = map[string]domain.TeamSettings{}
	}
	if data.IssueTemplates == nil {
		data.IssueTemplates = []domain.IssueTemplate{}
	}
	if data.ProjectTemplates == nil {
		data.ProjectTemplates = []domain.ProjectTemplate{}
	}
	if data.Documents == nil {
		data.Documents = []domain.Document{}
	}
	if data.CustomerRequests == nil {
		data.CustomerRequests = []domain.CustomerRequest{}
	}
	if data.Releases == nil {
		data.Releases = []domain.Release{}
	}
	if data.Asks == nil {
		data.Asks = []domain.Ask{}
	}
	if data.SLARules == nil {
		data.SLARules = []domain.SLARule{}
	}
	if data.IssueSLAs == nil {
		data.IssueSLAs = []domain.IssueSLA{}
	}
	if data.SLAEvents == nil {
		data.SLAEvents = []domain.SLAEvent{}
	}
	if data.Drafts == nil {
		data.Drafts = []domain.Draft{}
	}
	if data.Favorites == nil {
		data.Favorites = []domain.Favorite{}
	}
	if data.Subscriptions == nil {
		data.Subscriptions = []domain.Subscription{}
	}
	if data.AuditLog == nil {
		data.AuditLog = []domain.AuditLogEntry{}
	}
	if data.Trash == nil {
		data.Trash = []domain.TrashEntry{}
	}
	if data.ImportJobs == nil {
		data.ImportJobs = []domain.ImportJob{}
	}
	if data.ExportJobs == nil {
		data.ExportJobs = []domain.ExportJob{}
	}
	for _, team := range data.Teams {
		settings := data.TeamSettings[team.ID]
		if settings.TeamID == "" {
			settings = domain.TeamSettings{TeamID: team.ID, Timezone: "Etc/UTC", EstimateType: "notUsed", DefaultStateID: defaultStateID(data, team.ID)}
			data.TeamSettings[team.ID] = settings
		}
		cycle := data.CycleSettings[team.ID]
		if cycle.DurationWeeks > 0 && cycle.Capacity == 0 {
			cycle.Capacity = 4
			cycle.AutoCreate = true
			cycle.AutoMigrate = true
			data.CycleSettings[team.ID] = cycle
		}
	}
	if len(data.ProjectStatuses) == 0 {
		data.ProjectStatuses = canonicalProjectStatuses()
	}
	if data.SavedViews == nil {
		data.SavedViews = []domain.SavedView{}
	}
	if data.Comments == nil {
		data.Comments = map[string][]domain.Comment{}
	}
	if data.Activities == nil {
		data.Activities = map[string][]domain.ActivityEvent{}
	}
	if data.Notifications == nil {
		data.Notifications = []domain.Notification{}
	}
	if data.NotificationPreferences == nil {
		data.NotificationPreferences = map[string]domain.NotificationPreferences{}
	}
	if data.NotificationDeliveries == nil {
		data.NotificationDeliveries = []domain.NotificationDelivery{}
	}
	for _, user := range data.Users {
		if _, ok := data.NotificationPreferences[user.ID]; !ok {
			data.NotificationPreferences[user.ID] = defaultNotificationPreferences(user.ID)
		}
	}
	if data.Initiatives == nil {
		data.Initiatives = []domain.Initiative{}
	}
	if data.InitiativeUpdates == nil {
		data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	}
	for i := range data.Initiatives {
		if data.Initiatives[i].LabelIDs == nil {
			data.Initiatives[i].LabelIDs = []string{}
		}
		if data.Initiatives[i].ProjectIDs == nil {
			data.Initiatives[i].ProjectIDs = []string{}
		}
		if data.Initiatives[i].Resources == nil {
			data.Initiatives[i].Resources = []domain.InitiativeResource{}
		}
		if data.Initiatives[i].Comments == nil {
			data.Initiatives[i].Comments = []domain.Comment{}
		}
	}
	for initiativeID := range data.InitiativeUpdates {
		for i := range data.InitiativeUpdates[initiativeID] {
			if data.InitiativeUpdates[initiativeID][i].Comments == nil {
				data.InitiativeUpdates[initiativeID][i].Comments = []domain.Comment{}
			}
			if data.InitiativeUpdates[initiativeID][i].Reactions == nil {
				data.InitiativeUpdates[initiativeID][i].Reactions = map[string][]string{}
			}
		}
	}
	for i := range data.Notifications {
		if data.Notifications[i].OccurrenceCount < 1 {
			data.Notifications[i].OccurrenceCount = 1
		}
		if data.Notifications[i].Category == "" {
			data.Notifications[i].Category = legacyNotificationCategory(data.Notifications[i].Type)
		}
		if data.Notifications[i].GroupKey == "" {
			data.Notifications[i].GroupKey = data.Notifications[i].RecipientID + ":" + data.Notifications[i].IssueID + ":" + data.Notifications[i].Category
		}
		if len(data.Notifications[i].LatestActorIDs) == 0 && data.Notifications[i].Actor.ID != "" {
			data.Notifications[i].LatestActorIDs = []string{data.Notifications[i].Actor.ID}
		}
		if data.Notifications[i].FavoritedAt != nil {
			data.Notifications[i].Favorite = true
		}
		if data.Notifications[i].Favorite && data.Notifications[i].FavoritedAt == nil {
			now := time.Now().UTC()
			data.Notifications[i].FavoritedAt = &now
		}
	}
	for issueID := range data.Comments {
		for i := range data.Comments[issueID] {
			if data.Comments[issueID][i].Version < 1 {
				data.Comments[issueID][i].Version = 1
			}
			if data.Comments[issueID][i].Reactions == nil {
				data.Comments[issueID][i].Reactions = map[string][]string{}
			}
		}
	}
	for i := range data.Issues {
		if data.Issues[i].Version < 1 {
			data.Issues[i].Version = 1
		}
		if data.Issues[i].Reactions == nil {
			data.Issues[i].Reactions = map[string][]string{}
		}
		if data.Issues[i].Labels == nil {
			data.Issues[i].Labels = []domain.IssueLabel{}
		}
		if data.Issues[i].SubscriberIDs == nil {
			data.Issues[i].SubscriberIDs = []string{}
		}
		if data.Issues[i].SubIssueIDs == nil {
			data.Issues[i].SubIssueIDs = []string{}
		}
		if data.Issues[i].Relations == nil {
			data.Issues[i].Relations = []domain.IssueRelation{}
		}
		if data.Issues[i].Attachments == nil {
			data.Issues[i].Attachments = []domain.Attachment{}
		}
	}
}

func defaultStateID(data *domain.Bootstrap, teamID string) string {
	for _, state := range data.States {
		if (state.TeamID == "" || state.TeamID == teamID) && state.Default {
			return state.ID
		}
	}
	for _, state := range data.States {
		if (state.TeamID == "" || state.TeamID == teamID) && state.Type == "backlog" {
			return state.ID
		}
	}
	return ""
}

func defaultNotificationPreferences(userID string) domain.NotificationPreferences {
	categories := map[string]bool{"assignments": true, "statusChanges": true, "comments": true, "mentions": true, "reactions": true, "subscriptions": true, "documents": true, "updates": true, "reminders": true, "loops": true, "integrations": true, "billing": true, "customerRequests": true, "triage": true}
	clone := func() map[string]bool {
		result := make(map[string]bool, len(categories))
		for key, value := range categories {
			result[key] = value
		}
		return result
	}
	return domain.NotificationPreferences{UserID: userID, Inbox: domain.NotificationChannelPreferences{Enabled: true, Categories: clone()}, Email: domain.NotificationChannelPreferences{Enabled: true, Categories: clone()}, Desktop: domain.NotificationChannelPreferences{Enabled: true, Categories: clone()}, EmailFormat: "digest", DelayLowPriority: true, ImmediateUrgent: true, SoundEnabled: true, UpdatedAt: time.Now().UTC()}
}

func legacyNotificationCategory(value string) string {
	switch value {
	case "assignment":
		return "assignments"
	case "mention":
		return "mentions"
	case "comment":
		return "comments"
	default:
		return "statusChanges"
	}
}

func (s *SQLiteStore) Bootstrap() domain.Bootstrap {
	data, _ := s.BootstrapFor("")
	return data
}

func (s *SQLiteStore) BootstrapFor(workspaceKey string) (domain.Bootstrap, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspaceKey == "" {
		workspaceKey = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return domain.Bootstrap{}, false
	}
	raw, _ := json.Marshal(data)
	var clone domain.Bootstrap
	_ = json.Unmarshal(raw, &clone)
	return clone, true
}

func (s *SQLiteStore) Account() domain.AccountBootstrap {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := domain.AccountBootstrap{Viewer: s.viewer, Workspaces: []domain.WorkspaceMembership{}, LastWorkspaceKey: s.lastWorkspaceKey}
	for _, data := range s.workspaces {
		joined := data.Workspace.CreatedAt
		if joined.IsZero() {
			joined = time.Now().UTC()
		}
		result.Workspaces = append(result.Workspaces, domain.WorkspaceMembership{Workspace: data.Workspace, Role: "Admin", JoinedAt: joined, IssueCount: len(data.Issues)})
	}
	slices.SortFunc(result.Workspaces, func(a, b domain.WorkspaceMembership) int {
		return strings.Compare(strings.ToLower(a.Workspace.Name), strings.ToLower(b.Workspace.Name))
	})
	return result
}

func (s *SQLiteStore) Mutate(ctx context.Context, eventType, aggregateID string, payload any, mutate func(*domain.Bootstrap) error) error {
	return s.MutateWorkspace(ctx, "", eventType, aggregateID, payload, mutate)
}

func (s *SQLiteStore) MutateWorkspace(ctx context.Context, workspaceKey, eventType, aggregateID string, payload any, mutate func(*domain.Bootstrap) error) error {
	return s.MutateWorkspaceWithAggregate(ctx, workspaceKey, eventType, payload, func(data *domain.Bootstrap) (string, error) {
		return aggregateID, mutate(data)
	})
}

// MutateWithAggregate lets create operations derive their aggregate ID inside
// the same serialized transaction that allocates the entity ID.
func (s *SQLiteStore) MutateWithAggregate(ctx context.Context, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	return s.MutateWorkspaceWithAggregate(ctx, "", eventType, payload, mutate)
}

func (s *SQLiteStore) MutateWorkspaceWithAggregate(ctx context.Context, workspaceKey, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	s.mu.Lock()
	var event domain.DomainEvent
	err := func() error {
		defer s.mu.Unlock()
		if workspaceKey == "" {
			workspaceKey = s.lastWorkspaceKey
		}
		current, ok := s.workspaces[workspaceKey]
		if !ok {
			return fmt.Errorf("workspace %q: %w", workspaceKey, errors.New("not found"))
		}
		raw, _ := json.Marshal(current)
		var next domain.Bootstrap
		if err := json.Unmarshal(raw, &next); err != nil {
			return err
		}
		if actor, ok := actorFromContext(ctx); ok {
			next.Viewer = actor
			if index := slices.IndexFunc(next.Users, func(user domain.User) bool { return user.ID == actor.ID }); index >= 0 {
				next.Users[index] = actor
			} else {
				next.Users = append(next.Users, actor)
			}
		}
		aggregateID, err := mutate(&next)
		if err != nil {
			return err
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		if err := s.persistWorkspace(ctx, workspaceKey, next, &event); err != nil {
			return err
		}
		s.workspaces[workspaceKey] = next
		s.lastWorkspaceKey = workspaceKey
		return nil
	}()
	if err != nil {
		return err
	}
	if s.realtimeSink != nil {
		actor, _ := actorFromContext(ctx)
		s.realtimeSink(workspaceKey, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: event.Payload, CreatedAt: event.CreatedAt})
	}
	return nil
}

func (s *SQLiteStore) persist(ctx context.Context, data domain.Bootstrap, event *domain.DomainEvent) error {
	return s.persistWorkspace(ctx, data.Workspace.URLKey, data, event)
}

func (s *SQLiteStore) persistWorkspace(ctx context.Context, workspaceKey string, data domain.Bootstrap, event *domain.DomainEvent) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_states(workspace_key,workspace_id,data,updated_at) VALUES(?,?,?,?) ON CONFLICT(workspace_key) DO UPDATE SET workspace_id=excluded.workspace_id,data=excluded.data,updated_at=excluded.updated_at`, workspaceKey, data.Workspace.ID, raw, now); err != nil {
		return err
	}
	viewerRaw, _ := json.Marshal(s.viewer)
	if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
		viewerRaw, _ = json.Marshal(data.Viewer)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, workspaceKey, viewerRaw, now); err != nil {
		return err
	}
	if event != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func firstWorkspaceKey(workspaces map[string]domain.Bootstrap) string {
	keys := make([]string, 0, len(workspaces))
	for key := range workspaces {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func (s *SQLiteStore) CreateWorkspace(ctx context.Context, name, urlKey, region string) (domain.Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.workspaces[urlKey]; exists {
		return domain.Bootstrap{}, fmt.Errorf("workspace key already exists")
	}
	viewer := s.viewer
	if actor, ok := actorFromContext(ctx); ok {
		viewer = actor
	}
	data := EmptyWorkspace(name, urlKey, region, viewer)
	event := &domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: "workspace.created", AggregateID: data.Workspace.ID, Payload: json.RawMessage(fmt.Sprintf(`{"urlKey":%q}`, urlKey)), CreatedAt: time.Now().UTC()}
	if err := s.persistWorkspace(ctx, urlKey, data, event); err != nil {
		return domain.Bootstrap{}, err
	}
	s.workspaces[urlKey] = data
	s.lastWorkspaceKey = urlKey
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?)`, data.Workspace.ID, viewer.ID, "admin", "active", now, now)
	_, _ = s.db.ExecContext(ctx, `INSERT INTO auth_account_state(user_id,last_workspace_key,updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,updated_at=excluded.updated_at`, viewer.ID, urlKey, now)
	if len(data.Teams) > 0 {
		_, _ = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?)`, data.Workspace.ID, data.Teams[0].ID, viewer.ID, "owner", now)
	}
	return data, nil
}

func (s *SQLiteStore) UpdateWorkspace(ctx context.Context, workspaceKey string, workspace domain.Workspace) (domain.Bootstrap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return domain.Bootstrap{}, fmt.Errorf("workspace not found")
	}
	if workspace.URLKey == "" {
		workspace.URLKey = workspaceKey
	}
	if workspace.URLKey != workspaceKey {
		if _, exists := s.workspaces[workspace.URLKey]; exists {
			return domain.Bootstrap{}, fmt.Errorf("workspace key already exists")
		}
	}
	workspace.ID = data.Workspace.ID
	workspace.CreatedAt = data.Workspace.CreatedAt
	data.Workspace = workspace
	for index := range data.Teams {
		if len(data.Teams) == 1 && strings.EqualFold(data.Teams[index].Name, s.workspaces[workspaceKey].Workspace.Name) {
			data.Teams[index].Name = workspace.Name
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(data)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_states WHERE workspace_key = ?`, workspaceKey); err != nil {
		return domain.Bootstrap{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_states(workspace_key,workspace_id,data,updated_at) VALUES(?,?,?,?)`, workspace.URLKey, workspace.ID, raw, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Bootstrap{}, err
	}
	viewerRaw, _ := json.Marshal(s.viewer)
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, workspace.URLKey, viewerRaw, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Bootstrap{}, err
	}
	payload, _ := json.Marshal(workspace)
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, fmt.Sprintf("evt_%d", now.UnixNano()), "workspace.updated", workspace.ID, payload, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Bootstrap{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Bootstrap{}, err
	}
	if workspace.URLKey != workspaceKey {
		_, _ = s.db.ExecContext(ctx, `UPDATE auth_account_state SET last_workspace_key=?,updated_at=? WHERE last_workspace_key=?`, workspace.URLKey, time.Now().UTC().Format(time.RFC3339Nano), workspaceKey)
	}
	delete(s.workspaces, workspaceKey)
	s.workspaces[workspace.URLKey] = data
	s.lastWorkspaceKey = workspace.URLKey
	return data, nil
}

func (s *SQLiteStore) DeleteWorkspace(ctx context.Context, workspaceKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return fmt.Errorf("workspace not found")
	}
	delete(s.workspaces, workspaceKey)
	s.lastWorkspaceKey = firstWorkspaceKey(s.workspaces)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_states WHERE workspace_key = ?`, workspaceKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_memberships WHERE workspace_id = ?`, data.Workspace.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_memberships WHERE workspace_id = ?`, data.Workspace.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_invitations WHERE workspace_id = ?`, data.Workspace.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_account_state SET last_workspace_key='',updated_at=? WHERE last_workspace_key=?`, time.Now().UTC().Format(time.RFC3339Nano), workspaceKey); err != nil {
		return err
	}
	now := time.Now().UTC()
	viewerRaw, _ := json.Marshal(s.viewer)
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, s.lastWorkspaceKey, viewerRaw, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at) VALUES(?,?,?,?,?)`, fmt.Sprintf("evt_%d", now.UnixNano()), "workspace.deleted", data.Workspace.ID, []byte(`{}`), now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) Events(ctx context.Context, aggregateID string) ([]domain.DomainEvent, error) {
	query := `SELECT id,event_type,aggregate_id,payload,created_at FROM domain_events`
	args := []any{}
	if aggregateID != "" {
		query += ` WHERE aggregate_id = ?`
		args = append(args, aggregateID)
	}
	query += ` ORDER BY created_at ASC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.DomainEvent
	for rows.Next() {
		var event domain.DomainEvent
		var created string
		if err := rows.Scan(&event.ID, &event.Type, &event.AggregateID, &event.Payload, &created); err != nil {
			return nil, err
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, event)
	}
	return events, rows.Err()
}
