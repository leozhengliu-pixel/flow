package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"flow/api/internal/domain"
)

type SQLiteStore struct {
	db               *sqlDatabase
	dialect          string
	mu               sync.RWMutex
	workspaces       map[string]domain.Bootstrap
	lastWorkspaceKey string
	viewer           domain.User
	realtimeSink     func(string, domain.RealtimeEvent)
	webhookSink      func(string, domain.DomainEvent)
	coordinator      WorkspaceCoordinator
	fixtureProfile   string
	fixturePassword  string
	maxStateBytes    int
}

// WorkspaceKeys returns a stable snapshot for background workers. Callers do
// not hold the store mutex while processing a workspace.
func (s *SQLiteStore) WorkspaceKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0, len(s.workspaces))
	for key := range s.workspaces {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type WorkspaceCoordinator interface {
	WithWorkspaceLock(context.Context, string, func() error) error
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	return OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path, MaxOpenConns: 1})
}

// OpenSQLiteTestFixture is reserved for automated tests. Production callers
// must use OpenSQLite or OpenDatabase so a fresh installation remains empty.
func OpenSQLiteTestFixture(path string) (*SQLiteStore, error) {
	return OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1})
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) SetRealtimeSink(sink func(string, domain.RealtimeEvent)) {
	s.mu.Lock()
	s.realtimeSink = sink
	s.mu.Unlock()
}

func (s *SQLiteStore) SetWebhookSink(sink func(string, domain.DomainEvent)) {
	s.mu.Lock()
	s.webhookSink = sink
	s.mu.Unlock()
}

func (s *SQLiteStore) webhookConfigured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.webhookSink != nil
}

func (s *SQLiteStore) webhookNeeded(workspaceKey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return false
	}
	for _, webhook := range data.Webhooks {
		if webhook.Enabled && strings.TrimSpace(webhook.URL) != "" {
			return true
		}
	}
	return false
}

func (s *SQLiteStore) webhook() func(string, domain.DomainEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.webhookSink
}

func (s *SQLiteStore) realtime() func(string, domain.RealtimeEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.realtimeSink
}

func (s *SQLiteStore) SetWorkspaceCoordinator(coordinator WorkspaceCoordinator) {
	s.mu.Lock()
	s.coordinator = coordinator
	s.mu.Unlock()
}

func (s *SQLiteStore) ReloadWorkspace(ctx context.Context, workspaceKey string) error {
	data, err := s.loadWorkspaceState(ctx, workspaceKey)
	if err != nil {
		return err
	}
	historyChanged := refreshProjectProgressHistories(&data, time.Now().UTC())
	if historyChanged {
		if err := s.persistWorkspace(ctx, workspaceKey, data, nil); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.workspaces[workspaceKey] = data
	s.mu.Unlock()
	return nil
}

func (s *SQLiteStore) ReloadAllWorkspaces(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,data FROM workspace_states`)
	if err != nil {
		return err
	}
	defer rows.Close()
	workspaces := map[string]domain.Bootstrap{}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return err
		}
		if len(raw) > s.maxStateBytes {
			return fmt.Errorf("workspace %q state exceeds %d bytes", key, s.maxStateBytes)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(raw, &data); err != nil {
			return err
		}
		normalize(&data)
		workspaces[key] = data
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.workspaces = workspaces
	if _, ok := workspaces[s.lastWorkspaceKey]; !ok {
		s.lastWorkspaceKey = firstWorkspaceKey(workspaces)
	}
	s.mu.Unlock()
	return nil
}

func (s *SQLiteStore) loadWorkspaceState(ctx context.Context, workspaceKey string) (domain.Bootstrap, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspaceKey).Scan(&raw); err != nil {
		return domain.Bootstrap{}, err
	}
	if len(raw) > s.maxStateBytes {
		return domain.Bootstrap{}, fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
	}
	var data domain.Bootstrap
	if err := json.Unmarshal(raw, &data); err != nil {
		return data, err
	}
	normalize(&data)
	return data, nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name VARCHAR(191) NOT NULL, applied_at VARCHAR(40) NOT NULL)`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	applied := map[int]bool{}
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema migrations: %w", err)
	}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return fmt.Errorf("scan schema migration: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close schema migration rows: %w", err)
	}
	migrations := []struct {
		version int
		name    string
		apply   func(context.Context) error
	}{
		{version: 1, name: "base schema", apply: func(ctx context.Context) error { return s.applyMigrationStatements(ctx, databaseMigrations(s.dialect)) }},
		{version: 2, name: "nullable external identity email", apply: s.makeAuthEmailNullable},
		{version: 3, name: "domain event previous values", apply: s.addDomainEventPreviousValues},
	}
	for _, migration := range migrations {
		if applied[migration.version] {
			continue
		}
		if err := migration.apply(ctx); err != nil {
			return fmt.Errorf("database migration %d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, migration.version, migration.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record database migration %d: %w", migration.version, err)
		}
	}
	return nil
}

func (s *SQLiteStore) addDomainEventPreviousValues(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `ALTER TABLE domain_events ADD COLUMN previous_values TEXT`)
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "duplicate column") || strings.Contains(message, "already exists") {
			return nil
		}
	}
	return err
}

// backfillWorkspaceOwners upgrades existing installations without changing
// the role of every administrator. The oldest active administrator becomes
// the durable workspace owner; subsequent administrators remain admins.
func (s *SQLiteStore) backfillWorkspaceOwners(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT workspace_id FROM workspace_memberships`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var workspaceID string
	for rows.Next() {
		if err := rows.Scan(&workspaceID); err != nil {
			return err
		}
		var ownerCount int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id=? AND role='owner'`, workspaceID).Scan(&ownerCount); err != nil {
			return err
		}
		if ownerCount > 0 {
			continue
		}
		var userID string
		err := s.db.QueryRowContext(ctx, `SELECT user_id FROM workspace_memberships WHERE workspace_id=? AND role='admin' AND status='active' ORDER BY joined_at,user_id LIMIT 1`, workspaceID).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE workspace_memberships SET role='owner' WHERE workspace_id=? AND user_id=?`, workspaceID, userID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *SQLiteStore) applyMigrationStatements(ctx context.Context, statements []string) error {
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			message := strings.ToLower(err.Error())
			if s.dialect == "mysql" && (strings.Contains(message, "duplicate key name") || strings.Contains(message, "already exists")) {
				continue
			}
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) makeAuthEmailNullable(ctx context.Context) error {
	switch s.dialect {
	case "postgres":
		_, err := s.db.ExecContext(ctx, `ALTER TABLE auth_users ALTER COLUMN email DROP NOT NULL`)
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "does not exist") && !strings.Contains(strings.ToLower(err.Error()), "already allows null") {
			return err
		}
		return nil
	case "mysql":
		_, err := s.db.ExecContext(ctx, `ALTER TABLE auth_users MODIFY COLUMN email VARCHAR(320) NULL`)
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return err
		}
		return nil
	case "sqlite":
		rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(auth_users)`)
		if err != nil {
			return err
		}
		notNull := false
		for rows.Next() {
			var cid int
			var name, columnType string
			var required, primaryKey int
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &columnType, &required, &defaultValue, &primaryKey); err != nil {
				return err
			}
			if name == "email" {
				notNull = required == 1
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return rowErr
		}
		if !notNull {
			return nil
		}
		_, err = s.db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`)
		if err != nil {
			return err
		}
		defer s.db.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
		statements := []string{
			`CREATE TABLE auth_users_new (id TEXT PRIMARY KEY, email TEXT UNIQUE, name VARCHAR(320) NOT NULL, display_name VARCHAR(320) NOT NULL, avatar_url VARCHAR(2048) NOT NULL DEFAULT '', password_hash VARCHAR(255) NOT NULL, email_verified_at VARCHAR(40), active INTEGER NOT NULL DEFAULT 1, created_at VARCHAR(40) NOT NULL, updated_at VARCHAR(40) NOT NULL)`,
			`INSERT INTO auth_users_new(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) SELECT id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at FROM auth_users`,
			`DROP TABLE auth_users`,
			`ALTER TABLE auth_users_new RENAME TO auth_users`,
		}
		for _, statement := range statements {
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteStore) loadOrSeed(ctx context.Context) error {
	s.workspaces = map[string]domain.Bootstrap{}
	historyChanged := map[string]bool{}
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
		if len(raw) > s.maxStateBytes {
			rows.Close()
			return fmt.Errorf("workspace %q state exceeds %d bytes", key, s.maxStateBytes)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(raw, &data); err != nil {
			rows.Close()
			return err
		}
		normalize(&data)
		historyChanged[key] = refreshProjectProgressHistories(&data, time.Now().UTC())
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
		for key, data := range s.workspaces {
			if historyChanged[key] {
				if err := s.persistWorkspace(ctx, key, data, nil); err != nil {
					return err
				}
			}
		}
		return nil
	}

	var raw []byte
	err = s.db.QueryRowContext(ctx, `SELECT data FROM workspace_state WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		if strings.EqualFold(s.fixtureProfile, "test") {
			data := localSQLiteFixture()
			normalize(&data)
			refreshProjectProgressHistories(&data, time.Now().UTC())
			s.workspaces[data.Workspace.URLKey] = data
			s.lastWorkspaceKey = data.Workspace.URLKey
			s.viewer = data.Viewer
			return s.persistWorkspace(ctx, data.Workspace.URLKey, data, nil)
		}
		s.viewer = bootstrapViewer()
		s.lastWorkspaceKey = ""
		return nil
	}
	if err != nil {
		return err
	}
	var data domain.Bootstrap
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	normalize(&data)
	refreshProjectProgressHistories(&data, time.Now().UTC())
	s.workspaces[data.Workspace.URLKey] = data
	s.lastWorkspaceKey = data.Workspace.URLKey
	s.viewer = data.Viewer
	return s.persistWorkspace(ctx, data.Workspace.URLKey, data, nil)
}

func bootstrapViewer() domain.User {
	return domain.User{ID: "usr_local", Name: "Flow user", DisplayName: "Flow user", Active: true, EmailVerified: true}
}

func legacyReleaseStageStatus(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "in progress":
		return "inProgress"
	case "released":
		return "released"
	case "canceled", "cancelled":
		return "canceled"
	default:
		return "planned"
	}
}

func legacyReleaseSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, value), "-")
}

func normalize(data *domain.Bootstrap) {
	for projectIndex := range data.Projects {
		for resourceIndex := range data.Projects[projectIndex].Resources {
			if data.Projects[projectIndex].Resources[resourceIndex].PinnedTeamIDs == nil {
				data.Projects[projectIndex].Resources[resourceIndex].PinnedTeamIDs = []string{}
			}
		}
	}
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
	if data.UserSettings == nil {
		data.UserSettings = map[string]domain.UserSettings{}
	}
	for _, user := range data.Users {
		if _, ok := data.UserSettings[user.ID]; !ok {
			data.UserSettings[user.ID] = defaultUserSettings(user.ID)
		}
	}
	for userID, settings := range data.UserSettings {
		if settings.PulseSchedule == "" {
			settings.PulseSchedule = "never"
			data.UserSettings[userID] = settings
		}
		if settings.PersonalSettingsVersion < 1 {
			settings.PersonalSettingsVersion = 1
			settings.CodeReviewsEnabled = true
			settings.MergeStrategy = "Squash and merge"
			settings.CodeTheme = "Flow Light"
			settings.CodeFont = "12px, Regular, Default"
			settings.ReviewCommentsFilter = "Exclude Bots"
			settings.ReviewRequests = true
			settings.GithubTeamReviewRequests = true
			settings.ChecksMergeQueue = true
			settings.GitAttachmentFormat = "Title"
			settings.GitBranchMoveStarted = true
			settings.CodingToolMoveStarted = true
			settings.ChangelogUpdates = true
			settings.InviteAcceptedUpdates = true
			settings.PrivacyUpdates = true
			data.UserSettings[userID] = settings
		}
	}
	if data.WorkspaceSettings.SessionDurationDays == 0 {
		data.WorkspaceSettings = defaultWorkspaceSettings(data)
	}
	delete(data.WorkspaceSettings.FeatureFlags, "library")
	if data.WorkspaceSettings.AllowedDomains == nil {
		data.WorkspaceSettings.AllowedDomains = []string{}
	}
	if data.WorkspaceSettings.AICreditReloadThresholdCents == 0 {
		data.WorkspaceSettings.AICreditReloadThresholdCents = 500
	}
	if data.WorkspaceSettings.AICreditReloadAmountCents == 0 {
		data.WorkspaceSettings.AICreditReloadAmountCents = 2000
	}
	if data.LabelGroups == nil {
		data.LabelGroups = []domain.LabelGroup{}
	}
	if data.APIKeys == nil {
		data.APIKeys = []domain.APIKey{}
	}
	for index := range data.APIKeys {
		// A nil scope is the public API's full-access representation. Do not
		// materialize it as read/write: doing so silently strips admin access
		// from keys created before granular scopes were introduced.
		if data.APIKeys[index].TeamRestriction == "" {
			if len(data.APIKeys[index].TeamIDs) > 0 {
				data.APIKeys[index].TeamRestriction = "selected"
			} else {
				data.APIKeys[index].TeamRestriction = "all"
			}
		}
	}
	if data.OAuthApplications == nil {
		data.OAuthApplications = []domain.OAuthApplication{}
	}
	if data.OAuthAuthorizations == nil {
		data.OAuthAuthorizations = []domain.OAuthAuthorization{}
	}
	if data.IntegrationConnections == nil {
		data.IntegrationConnections = []domain.IntegrationConnection{}
	}
	if data.IdentityProviders == nil {
		data.IdentityProviders = []domain.IdentityProvider{}
	}
	if data.IntegrationDeliveries == nil {
		data.IntegrationDeliveries = []domain.IntegrationDelivery{}
	}
	for index := range data.Webhooks {
		if data.Webhooks[index].TeamRestriction == "" {
			if len(data.Webhooks[index].TeamIDs) > 0 {
				data.Webhooks[index].TeamRestriction = "selected"
			} else {
				data.Webhooks[index].TeamRestriction = "all"
			}
		}
	}
	if data.GitAutomationStates == nil {
		data.GitAutomationStates = []domain.GitAutomationState{}
	}
	if data.TargetBranches == nil {
		data.TargetBranches = []domain.TargetBranch{}
	}
	for i := range data.IntegrationConnections {
		if data.IntegrationConnections[i].Scopes == nil {
			data.IntegrationConnections[i].Scopes = []string{}
		}
		if data.IntegrationConnections[i].Channels == nil {
			data.IntegrationConnections[i].Channels = []string{}
		}
	}
	if data.CustomEmojis == nil {
		data.CustomEmojis = []domain.CustomEmoji{}
	}
	if data.AgentSessions == nil {
		data.AgentSessions = []domain.AgentSession{}
	}
	if data.AgentSkills == nil {
		data.AgentSkills = []domain.PersonalAgentSkill{}
	}
	if data.Reviews == nil {
		data.Reviews = []domain.CodeReview{}
	}
	for index := range data.Initiatives {
		if data.Initiatives[index].ParentInitiativeIDs == nil {
			data.Initiatives[index].ParentInitiativeIDs = []string{}
		}
	}
	for index := range data.Labels {
		if data.Labels[index].ResourceType == "" {
			data.Labels[index].ResourceType = "issue"
		}
	}
	for index := range data.IssueTemplates {
		if data.IssueTemplates[index].Scope == "" {
			data.IssueTemplates[index].Scope = "team"
		}
		if data.IssueTemplates[index].TemplateType == "" {
			data.IssueTemplates[index].TemplateType = "standard"
		}
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
	for index := range data.Cycles {
		if data.Cycles[index].Resources == nil {
			data.Cycles[index].Resources = []domain.CycleResource{}
		}
		if data.Cycles[index].Insight == nil {
			data.Cycles[index].Insight = map[string]string{"measure": "Issue count", "slice": "Status", "segment": "Priority"}
		}
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
	if data.DocumentTemplates == nil {
		data.DocumentTemplates = []domain.DocumentTemplate{}
	}
	if data.Documents == nil {
		data.Documents = []domain.Document{}
	}
	for index := range data.Documents {
		if data.Documents[index].Permissions == nil {
			data.Documents[index].Permissions = []domain.DocumentPermission{}
		}
		if data.Documents[index].Creator.ID != "" && !slices.ContainsFunc(data.Documents[index].Permissions, func(permission domain.DocumentPermission) bool {
			return permission.SubjectType == "user" && permission.SubjectID == data.Documents[index].Creator.ID
		}) {
			now := data.Documents[index].CreatedAt
			if now.IsZero() {
				now = time.Now().UTC()
			}
			data.Documents[index].Permissions = append(data.Documents[index].Permissions, domain.DocumentPermission{ID: "document_permission_" + data.Documents[index].Creator.ID, DocumentID: data.Documents[index].ID, SubjectType: "user", SubjectID: data.Documents[index].Creator.ID, Role: "owner", CreatedAt: now, UpdatedAt: now})
		}
	}
	if data.CustomerRequests == nil {
		data.CustomerRequests = []domain.CustomerRequest{}
	}
	if data.Releases == nil {
		data.Releases = []domain.Release{}
	}
	for index := range data.Releases {
		if data.Releases[index].SlugID == "" {
			base := legacyReleaseSlug(data.Releases[index].Name)
			if base == "" {
				base = "release"
			}
			data.Releases[index].SlugID = fmt.Sprintf("%s-%x", base, data.Releases[index].CreatedAt.UnixNano()&0xffffffffffff)
		}
		if data.Releases[index].Resources == nil {
			data.Releases[index].Resources = []domain.ReleaseResource{}
		}
	}
	if data.ReleasePipelines == nil {
		data.ReleasePipelines = []domain.ReleasePipeline{}
	}
	for index := range data.ReleasePipelines {
		if data.ReleasePipelines[index].SlugID == "" {
			base := legacyReleaseSlug(data.ReleasePipelines[index].Name)
			if base == "" {
				base = fmt.Sprintf("pipeline-%d", index+1)
			}
			candidate := base
			for suffix := 2; slices.ContainsFunc(data.ReleasePipelines[:index], func(item domain.ReleasePipeline) bool { return item.SlugID == candidate }); suffix++ {
				candidate = fmt.Sprintf("%s-%d", base, suffix)
			}
			data.ReleasePipelines[index].SlugID = candidate
		}
		if data.ReleasePipelines[index].StageStatuses == nil {
			data.ReleasePipelines[index].StageStatuses = map[string]string{}
		}
		for _, stage := range data.ReleasePipelines[index].Stages {
			if _, ok := data.ReleasePipelines[index].StageStatuses[stage]; !ok {
				data.ReleasePipelines[index].StageStatuses[stage] = legacyReleaseStageStatus(stage)
			}
		}
	}
	if data.Asks == nil {
		data.Asks = []domain.Ask{}
	}
	if data.Loops == nil {
		data.Loops = []domain.Loop{}
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
	if data.FavoriteFolders == nil {
		data.FavoriteFolders = []domain.FavoriteFolder{}
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
	if data.MigrationJobs == nil {
		data.MigrationJobs = []domain.MigrationJob{}
	}
	normalizeParity(data)
	if data.Webhooks == nil {
		data.Webhooks = []domain.Webhook{}
	}
	for _, team := range data.Teams {
		settings := data.TeamSettings[team.ID]
		if settings.TeamID == "" {
			settings = domain.TeamSettings{TeamID: team.ID, Timezone: "Etc/UTC", EstimateType: "notUsed", DefaultStateID: defaultStateID(data, team.ID)}
		}
		if settings.Access == "" {
			settings.Access = "public"
		}
		if settings.MembershipRestriction == "" {
			settings.MembershipRestriction = "open"
		}
		if settings.SettingsPermission == "" {
			settings.SettingsPermission = "allMembers"
		}
		if settings.LabelPermission == "" {
			settings.LabelPermission = "allMembers"
		}
		if settings.TemplatePermission == "" {
			settings.TemplatePermission = "allMembers"
		}
		if settings.AgentSkillPermission == "" {
			settings.AgentSkillPermission = "allMembers"
		}
		if settings.LoopPermission == "" {
			settings.LoopPermission = "allMembers"
		}
		if settings.MemberPermission == "" {
			settings.MemberPermission = "allMembers"
		}
		if settings.SlackNotifications == nil {
			settings.SlackNotifications = map[string]bool{}
		}
		if settings.PRAutomations == nil {
			settings.PRAutomations = map[string]string{}
		}
		if settings.StaleMonths == 0 {
			settings.StaleMonths = 6
		}
		if settings.AutoArchiveMonths == 0 {
			settings.AutoArchiveMonths = 6
		}
		if settings.ProgressOrder == "" {
			settings.ProgressOrder = "first"
		}
		if settings.TriageAction == "" {
			settings.TriageAction = "none"
		}
		if settings.ReleaseAutomations == nil {
			settings.ReleaseAutomations = []domain.TeamAutomationRule{}
		}
		if settings.TriageRules == nil {
			settings.TriageRules = []domain.TeamAutomationRule{}
		}
		if settings.AgentSkills == nil {
			settings.AgentSkills = []domain.TeamAgentSkill{}
		}
		data.TeamSettings[team.ID] = settings
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
	for index := range data.ProjectStatuses {
		switch data.ProjectStatuses[index].ID {
		case "ps_backlog":
			data.ProjectStatuses[index].Color = "#E79D4F"
		case "ps_planned":
			data.ProjectStatuses[index].Color = "#A8A8AA"
		case "ps_progress":
			data.ProjectStatuses[index].Color = "#E2B714"
		case "ps_completed":
			data.ProjectStatuses[index].Color = "#5E6AD2"
		case "ps_canceled":
			data.ProjectStatuses[index].Color = "#8A8F98"
		}
		if data.ProjectStatuses[index].Position == 0 && index > 0 {
			data.ProjectStatuses[index].Position = float64(index)
		}
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
	if data.PushSubscriptions == nil {
		data.PushSubscriptions = []domain.PushSubscription{}
	}
	if data.TriageResponsibilities == nil {
		data.TriageResponsibilities = []domain.TriageResponsibility{}
	}
	if data.TriageRoutingRules == nil {
		data.TriageRoutingRules = []domain.TriageRoutingRule{}
	}
	if data.TriageAssignments == nil {
		data.TriageAssignments = []domain.TriageAssignment{}
	}
	if data.WorkflowDefinitions == nil {
		data.WorkflowDefinitions = []domain.WorkflowDefinition{}
	}
	if data.WorkflowRuns == nil {
		data.WorkflowRuns = []domain.WorkflowRun{}
	}
	if data.EmailIntakeAddresses == nil {
		data.EmailIntakeAddresses = []domain.EmailIntakeAddress{}
	}
	if data.EmailIntakeMessages == nil {
		data.EmailIntakeMessages = []domain.EmailIntakeMessage{}
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
		if data.Initiatives[i].Creator.ID == "" {
			data.Initiatives[i].Creator = data.Viewer
		}
		if data.Initiatives[i].ContributingTeamIDs == nil {
			data.Initiatives[i].ContributingTeamIDs = []string{}
		}
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
		if data.Initiatives[i].DescriptionHistory == nil {
			data.Initiatives[i].DescriptionHistory = []domain.InitiativeDescriptionRevision{}
		}
		if data.Initiatives[i].UpdateSchedule.Cadence == "" {
			data.Initiatives[i].UpdateSchedule = domain.InitiativeUpdateSchedule{Cadence: "none", Weekday: 1, TimeRange: "09:00-12:00"}
		}
		if !data.Initiatives[i].NotificationRules.DescriptionChanges && !data.Initiatives[i].NotificationRules.NewUpdate && !data.Initiatives[i].NotificationRules.AllProjectUpdates {
			data.Initiatives[i].NotificationRules = domain.InitiativeNotificationRules{DescriptionChanges: true, NewUpdate: true}
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
	issueIndexes := make(map[string]int, len(data.Issues))
	for i := range data.Issues {
		issueIndexes[data.Issues[i].ID] = i
	}
	for i := range data.Issues {
		if data.Issues[i].ParentID == nil {
			continue
		}
		parentIndex, ok := issueIndexes[*data.Issues[i].ParentID]
		if !ok || parentIndex == i {
			data.Issues[i].ParentID = nil
			continue
		}
		if !slices.Contains(data.Issues[parentIndex].SubIssueIDs, data.Issues[i].ID) {
			data.Issues[parentIndex].SubIssueIDs = append(data.Issues[parentIndex].SubIssueIDs, data.Issues[i].ID)
		}
	}
	refreshResourceCounts(data)
}

// Saved view URLs use a stable, human-readable slug instead of the storage key.
// Keep old records addressable by deriving one once when they are loaded.
func ensureSavedViewSlugIDs(data *domain.Bootstrap) bool {
	seen := make(map[string]struct{}, len(data.SavedViews))
	changed := false
	for index := range data.SavedViews {
		view := &data.SavedViews[index]
		desired := savedViewSlugID(view.Name, view.ID)
		if view.SlugID == "" || strings.HasPrefix(view.SlugID, "view-") {
			if view.SlugID != desired {
				view.SlugID = desired
				changed = true
			}
		}
		candidate := view.SlugID
		for suffix := 2; ; suffix++ {
			if _, exists := seen[candidate]; !exists {
				break
			}
			candidate = fmt.Sprintf("%s-%d", view.SlugID, suffix)
		}
		if candidate != view.SlugID {
			view.SlugID = candidate
			changed = true
		}
		seen[view.SlugID] = struct{}{}
	}
	return changed
}

func savedViewSlugID(name, id string) string {
	base := slugUnicode(name)
	if base == "" {
		base = "view"
	}
	suffix := slugUnicode(strings.TrimPrefix(id, "view_"))
	if suffix == "" {
		return base
	}
	return base + "-" + suffix
}

func slugUnicode(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			builder.WriteRune(character)
			lastDash = false
		} else if builder.Len() > 0 && !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func defaultUserSettings(userID string) domain.UserSettings {
	return domain.UserSettings{UserID: userID, Language: "en-US", HomeView: "Flow Agent (default)", DisplayNames: "Full name", FirstDay: "Monday", Emoticons: true, SendComments: "Enter", FontSize: "Default", InterfaceTheme: "System preference", LightTheme: "Light", DarkTheme: "Dark", ReviewAutoAssign: true, BranchFormat: "{identifier}-{title}", PersonalSettingsVersion: 1, CodeReviewsEnabled: true, MergeStrategy: "Squash and merge", CodeTheme: "Flow Light", CodeFont: "12px, Regular, Default", ReviewCommentsFilter: "Exclude Bots", ReviewRequests: true, GithubTeamReviewRequests: true, ChecksMergeQueue: true, GitAttachmentFormat: "Title", GitBranchMoveStarted: true, CodingToolMoveStarted: true, ChangelogUpdates: true, InviteAcceptedUpdates: true, PrivacyUpdates: true, AgentEnabled: true, PulseSchedule: "never", UpdatedAt: time.Now().UTC()}
}

func defaultWorkspaceSettings(data *domain.Bootstrap) domain.WorkspaceSettings {
	return domain.WorkspaceSettings{FiscalMonth: "January", GuestsAllowed: true, SessionDurationDays: 30, InvitePermission: "admins", TeamCreatePermission: "members", LabelPermission: "members", TemplatePermission: "members", APIKeyPermission: "members", FeatureFlags: map[string]bool{"ai": true, "initiatives": true, "documents": true, "customer-requests": true, "releases": true, "pulse": true, "asks": true, "dashboards": true, "sidebar-teams": true, "sidebar-try": true, "recently-deleted": true, "audit-log": true, "emojis": true}, FeatureSettings: domain.FeatureSettings{InitiativeUpdateSchedule: "none", CustomerRevenueFormat: "annual", CustomerRevenueCurrency: "USD", CustomerManualEdits: true, CustomerStatuses: []domain.FeatureOption{{ID: "active", Name: "Active", Color: "#4cb782"}, {ID: "prospect", Name: "Prospect", Color: "#5e6ad2"}, {ID: "churned", Name: "Churned", Color: "#f2c94c"}, {ID: "lost", Name: "Lost", Color: "#eb5757"}}, CustomerTiers: []domain.FeatureOption{}, CustomerExcludedDomains: []string{}, CustomerGenericDomains: []string{}, PulseWorkspaceSchedule: "daily", AsksEmailAddresses: []string{}}, BillingEmail: data.Viewer.Email, Plan: "free", GoogleAuthEnabled: true, EmailAuthEnabled: true, InitiativePermission: "members", LoopPermission: "members", AgentGuidancePermission: "admins", AICreditReloadThresholdCents: 500, AICreditReloadAmountCents: 2000, UpdatedAt: time.Now().UTC()}
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
	if workspaceKey == "" {
		workspaceKey = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspaceKey]
	s.mu.RUnlock()
	if !ok {
		return domain.Bootstrap{}, false
	}
	// Workspace snapshots are replaced atomically by MutateWorkspace; they are
	// never edited in place. Clone after releasing the lock so JSON encoding and
	// resource-count derivation do not block writers or other readers.
	raw, _ := json.Marshal(data)
	var clone domain.Bootstrap
	_ = json.Unmarshal(raw, &clone)
	refreshResourceCounts(&clone)
	return clone, true
}

func (s *SQLiteStore) CycleForCalendar(id, token string) (domain.Cycle, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, data := range s.workspaces {
		for _, cycle := range data.Cycles {
			if cycle.ID == id && cycle.CalendarToken != "" && cycle.CalendarToken == token {
				return cycle, true
			}
		}
	}
	return domain.Cycle{}, false
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
	if workspaceKey == "" {
		s.mu.RLock()
		workspaceKey = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	webhookEnabled := s.webhookConfigured() && s.webhookNeeded(workspaceKey)
	apply := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.coordinator != nil {
			latest, err := s.loadWorkspaceState(ctx, workspaceKey)
			if err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
			s.workspaces[workspaceKey] = latest
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
		originalViewerRole := next.ViewerRole
		if actor, ok := actorFromContext(ctx); ok {
			next.Viewer = actor
			if role, status, roleErr := s.WorkspaceRole(ctx, next.Workspace.ID, actor.ID); roleErr == nil && status == "active" {
				// The canonical snapshot does not carry a viewer-specific role.
				// Populate it only while authorization-aware callbacks execute,
				// then restore the snapshot value before persistence.
				next.ViewerRole = role
			}
			if index := slices.IndexFunc(next.Users, func(user domain.User) bool { return user.ID == actor.ID }); index >= 0 {
				next.Users[index] = actor
			} else {
				next.Users = append(next.Users, actor)
			}
		}
		aggregateID, err := mutate(&next)
		next.ViewerRole = originalViewerRole
		if err != nil {
			return err
		}
		previousValues := json.RawMessage(nil)
		if webhookEnabled {
			previousValues = aggregatePreviousValues(current, next, aggregateID)
		}
		if progressEvent(eventType) {
			refreshProjectProgressHistories(&next, time.Now().UTC())
		}
		refreshResourceCounts(&next)
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, PreviousValues: previousValues, CreatedAt: time.Now().UTC()}
		if err := s.persistWorkspace(ctx, workspaceKey, next, &event); err != nil {
			return err
		}
		s.workspaces[workspaceKey] = next
		s.lastWorkspaceKey = workspaceKey
		return nil
	}
	var err error
	if s.coordinator != nil {
		err = s.coordinator.WithWorkspaceLock(ctx, workspaceKey, apply)
	} else {
		err = apply()
	}
	if err != nil {
		return err
	}
	if sink := s.webhook(); sink != nil {
		sink(workspaceKey, event)
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		sink(workspaceKey, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: event.Payload, CreatedAt: event.CreatedAt})
	}
	return nil
}

func aggregatePreviousValues(previous, next domain.Bootstrap, aggregateID string) json.RawMessage {
	if strings.TrimSpace(aggregateID) == "" {
		return nil
	}
	oldValue := aggregateJSONValue(previous, aggregateID)
	if oldValue == nil {
		return nil
	}
	newValue := aggregateJSONValue(next, aggregateID)
	if newValue == nil {
		encoded, _ := json.Marshal(oldValue)
		return encoded
	}
	oldMap, oldOK := oldValue.(map[string]any)
	newMap, newOK := newValue.(map[string]any)
	if !oldOK || !newOK {
		encoded, _ := json.Marshal(oldValue)
		return encoded
	}
	diff := map[string]any{}
	for key, value := range oldMap {
		if nextValue, exists := newMap[key]; !exists || !reflect.DeepEqual(value, nextValue) {
			diff[key] = value
		}
	}
	if len(diff) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(diff)
	return encoded
}

func aggregateJSONValue(data domain.Bootstrap, aggregateID string) any {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return findJSONObjectByID(value, aggregateID)
}

func findJSONObjectByID(value any, aggregateID string) any {
	switch item := value.(type) {
	case map[string]any:
		if id, ok := item["id"].(string); ok && id == aggregateID {
			return item
		}
		for _, child := range item {
			if found := findJSONObjectByID(child, aggregateID); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range item {
			if found := findJSONObjectByID(child, aggregateID); found != nil {
				return found
			}
		}
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
	if len(raw) > s.maxStateBytes {
		return fmt.Errorf("workspace state exceeds %d bytes", s.maxStateBytes)
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), []byte(event.PreviousValues), event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
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
	if s.coordinator == nil {
		return s.createWorkspace(ctx, name, urlKey, region)
	}
	var created domain.Bootstrap
	err := s.coordinator.WithWorkspaceLock(ctx, "__workspace_catalog__", func() error {
		if err := s.ReloadAllWorkspaces(ctx); err != nil {
			return err
		}
		var err error
		created, err = s.createWorkspace(ctx, name, urlKey, region)
		return err
	})
	if err == nil {
		s.publishWorkspaceEvent(ctx, created.Workspace.URLKey, "workspace.created", created.Workspace.ID, map[string]string{"urlKey": created.Workspace.URLKey})
	}
	return created, err
}

func (s *SQLiteStore) createWorkspace(ctx context.Context, name, urlKey, region string) (domain.Bootstrap, error) {
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
	_, _ = s.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id,user_id) DO UPDATE SET role=excluded.role,status=excluded.status,joined_at=excluded.joined_at,last_seen_at=excluded.last_seen_at`, data.Workspace.ID, viewer.ID, "owner", "active", now, now)
	_, _ = s.db.ExecContext(ctx, `INSERT INTO auth_account_state(user_id,last_workspace_key,updated_at) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,updated_at=excluded.updated_at`, viewer.ID, urlKey, now)
	if len(data.Teams) > 0 {
		_, _ = s.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT(workspace_id,team_id,user_id) DO UPDATE SET role=excluded.role,joined_at=excluded.joined_at`, data.Workspace.ID, data.Teams[0].ID, viewer.ID, "owner", now)
	}
	return data, nil
}

func (s *SQLiteStore) UpdateWorkspace(ctx context.Context, workspaceKey string, workspace domain.Workspace) (domain.Bootstrap, error) {
	if s.coordinator == nil {
		return s.updateWorkspace(ctx, workspaceKey, workspace)
	}
	var updated domain.Bootstrap
	err := s.coordinator.WithWorkspaceLock(ctx, "__workspace_catalog__", func() error {
		if err := s.ReloadAllWorkspaces(ctx); err != nil {
			return err
		}
		var err error
		updated, err = s.updateWorkspace(ctx, workspaceKey, workspace)
		return err
	})
	if err == nil {
		s.publishWorkspaceEvent(ctx, updated.Workspace.URLKey, "workspace.updated", updated.Workspace.ID, map[string]string{"previousUrlKey": workspaceKey, "urlKey": updated.Workspace.URLKey})
	}
	return updated, err
}

func (s *SQLiteStore) updateWorkspace(ctx context.Context, workspaceKey string, workspace domain.Workspace) (domain.Bootstrap, error) {
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
			// Do not mutate the slice shared by the immutable snapshot while
			// readers may be cloning it outside the store lock.
			data.Teams = slices.Clone(data.Teams)
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
	if s.coordinator == nil {
		return s.deleteWorkspace(ctx, workspaceKey)
	}
	var workspaceID string
	err := s.coordinator.WithWorkspaceLock(ctx, "__workspace_catalog__", func() error {
		if err := s.ReloadAllWorkspaces(ctx); err != nil {
			return err
		}
		if data, ok := s.BootstrapFor(workspaceKey); ok {
			workspaceID = data.Workspace.ID
		}
		return s.deleteWorkspace(ctx, workspaceKey)
	})
	if err == nil {
		s.publishWorkspaceEvent(ctx, workspaceKey, "workspace.deleted", workspaceID, map[string]string{"urlKey": workspaceKey})
	}
	return err
}

func (s *SQLiteStore) deleteWorkspace(ctx context.Context, workspaceKey string) error {
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

func (s *SQLiteStore) publishWorkspaceEvent(ctx context.Context, workspaceKey, eventType, aggregateID string, payload any) {
	sink := s.realtime()
	if sink == nil {
		return
	}
	raw, _ := json.Marshal(payload)
	actor, _ := actorFromContext(ctx)
	sink(workspaceKey, domain.RealtimeEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: aggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: raw, CreatedAt: time.Now().UTC()})
}

func (s *SQLiteStore) Events(ctx context.Context, aggregateID string) ([]domain.DomainEvent, error) {
	query := `SELECT id,event_type,aggregate_id,payload,previous_values,created_at FROM domain_events`
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
		var previous sql.NullString
		if err := rows.Scan(&event.ID, &event.Type, &event.AggregateID, &event.Payload, &previous, &created); err != nil {
			return nil, err
		}
		if previous.Valid && previous.String != "" {
			event.PreviousValues = json.RawMessage(previous.String)
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, event)
	}
	return events, rows.Err()
}
