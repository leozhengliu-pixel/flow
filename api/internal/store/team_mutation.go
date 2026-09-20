package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"flow/api/internal/domain"
)

type mutationAggregateIDKey struct{}

func withMutationAggregateID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, mutationAggregateIDKey{}, id)
}

func mutationAggregateID(ctx context.Context) string {
	id, _ := ctx.Value(mutationAggregateIDKey{}).(string)
	return id
}

type teamMutationSnapshot struct {
	hintID string

	teamsLen, statesLen, labelsLen, cyclesLen int
	usersLen, projectsLen, membersLen         int
	teamMembersLen                            int

	actorIndex    int
	actorAppended bool
	oldActor      domain.User

	teamIndex  int
	oldTeam    domain.Team
	oldTeamKey string

	hadSettings bool
	oldSettings domain.TeamSettings
	oldParent   string

	oldLabels map[int]domain.IssueLabel

	cycleIDs         []string
	oldCycleSettings map[string]domain.CycleSettings

	oldTeams          []domain.Team
	oldUsers          []domain.User
	oldProjects       []domain.Project
	oldImportSettings map[string]domain.TeamSettings
}

// These events cannot share the generic clone: that path copies every team and
// rewrites every metadata row, so a single write would be O(teams).
func catalogImportMutation(event string) bool {
	switch event {
	case "alm.org_teams_imported", "alm.users_imported", "alm.projects_imported":
		return true
	}
	return false
}

func metadataTeamMutation(event string, payload any) bool {
	if catalogImportMutation(event) {
		return true
	}
	switch event {
	case "team.created", "team.settings_updated":
		return true
	case "team.updated":
		return metadataFieldsOnly(payload, "name", "color", "icon")
	}
	return false
}

func (s *SQLiteStore) mutateTeamMetadata(ctx context.Context, workspaceKey, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if workspaceKey == "" {
		s.mu.RLock()
		workspaceKey = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	var realtimePayload json.RawMessage
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
		domain.EnsureTeamDirectory(&current)
		next := current
		snap := snapshotTeamMutation(eventType, mutationAggregateID(ctx), &next)
		originalViewerRole, originalViewer := next.ViewerRole, next.Viewer
		if actor, ok := actorFromContext(ctx); ok {
			next.Viewer = actor
			if role, status, roleErr := s.WorkspaceRole(ctx, next.Workspace.ID, actor.ID); roleErr == nil && status == "active" {
				next.ViewerRole = role
			}
			if index := slices.IndexFunc(next.Users, func(user domain.User) bool { return user.ID == actor.ID }); index >= 0 {
				snap.oldActor = next.Users[index]
				snap.actorIndex = index
				next.Users[index] = actor
			} else {
				snap.actorIndex = len(next.Users)
				snap.actorAppended = true
				next.Users = append(next.Users, actor)
			}
		}
		aggregateID, err := mutate(&next)
		next.ViewerRole = originalViewerRole
		next.Viewer = originalViewer
		if aggregateID == "" {
			aggregateID = snap.hintID
		}
		if err != nil {
			rollbackTeamMutation(&next, &snap, aggregateID)
			return err
		}
		previousValues := json.RawMessage(nil)
		if webhookEnabled {
			previousValues = teamMutationPreviousValues(eventType, aggregateID, &snap, next)
		}
		upserts, err := collectDirtyTeamRecords(eventType, aggregateID, &snap, next)
		if err != nil {
			rollbackTeamMutation(&next, &snap, aggregateID)
			return err
		}
		membersChanged := len(next.Members) != snap.membersLen
		teamMembersChanged := len(next.TeamMembers) != snap.teamMembersLen
		if len(upserts) > 0 || membersChanged || teamMembersChanged {
			payloadRaw, err := json.Marshal(payload)
			if err != nil {
				rollbackTeamMutation(&next, &snap, aggregateID)
				return err
			}
			event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, PreviousValues: previousValues, CreatedAt: time.Now().UTC()}
			realtimePayload = enrichRealtimePayload(payloadRaw, teamMutationEntity(&next, aggregateID, snap.teamsLen), eventType)
			if err := s.persistTeamMetadata(ctx, workspaceKey, next, &event, upserts, membersChanged, teamMembersChanged); err != nil {
				rollbackTeamMutation(&next, &snap, aggregateID)
				return err
			}
		}
		noteTeamMutationIndexes(eventType, aggregateID, &snap, &next)
		next = collectionMetadata(next)
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
		if errors.Is(err, ErrNoMutation) {
			return nil
		}
		return err
	}
	if event.ID == "" {
		return nil
	}
	s.invalidateHotCache(ctx, workspaceKey, eventType, event.AggregateID)
	if sink := s.webhook(); sink != nil {
		sink(workspaceKey, event)
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		if len(realtimePayload) == 0 {
			realtimePayload = event.Payload
		}
		sink(workspaceKey, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: realtimePayload, CreatedAt: event.CreatedAt})
	}
	return nil
}

func snapshotTeamMutation(eventType, hintID string, data *domain.Bootstrap) teamMutationSnapshot {
	snap := teamMutationSnapshot{
		hintID:         hintID,
		teamsLen:       len(data.Teams),
		statesLen:      len(data.States),
		labelsLen:      len(data.Labels),
		cyclesLen:      len(data.Cycles),
		usersLen:       len(data.Users),
		projectsLen:    len(data.Projects),
		membersLen:     len(data.Members),
		teamMembersLen: len(data.TeamMembers),
		actorIndex:     -1,
		teamIndex:      -1,
	}
	if catalogImportMutation(eventType) {
		snapshotImportCatalog(&snap, eventType, data)
		return snap
	}
	if hintID == "" {
		return snap
	}
	if eventType == "team.updated" || eventType == "team.settings_updated" {
		if index := domain.TeamIndex(data, hintID); index >= 0 {
			snap.teamIndex = index
			snap.oldTeam = data.Teams[index]
			snap.oldTeamKey = data.Teams[index].Key
		}
	}
	if settings, ok := data.TeamSettings[hintID]; ok {
		snap.hadSettings = true
		snap.oldSettings = cloneMutationTeamSettings(settings)
		snap.oldParent = settings.ParentTeamID
	}
	if eventType != "team.settings_updated" {
		return snap
	}
	snap.oldParent = data.TeamSettings[hintID].ParentTeamID
	scopes := append([]string{hintID}, domain.TeamDescendantIDs(data, hintID)...)
	snap.oldLabels = snapshotScopedLabels(data, scopes)
	snap.cycleIDs = scopes
	if data.CycleSettings != nil {
		snap.oldCycleSettings = make(map[string]domain.CycleSettings, len(scopes))
		for _, id := range scopes {
			if settings, ok := data.CycleSettings[id]; ok {
				snap.oldCycleSettings[id] = settings
			}
		}
	}
	return snap
}

func snapshotImportCatalog(snap *teamMutationSnapshot, eventType string, data *domain.Bootstrap) {
	switch eventType {
	case "alm.org_teams_imported":
		snap.oldTeams = slices.Clone(data.Teams)
		if len(data.TeamSettings) > 0 {
			snap.oldImportSettings = make(map[string]domain.TeamSettings, len(data.TeamSettings))
			for id, settings := range data.TeamSettings {
				snap.oldImportSettings[id] = settings
			}
		}
	case "alm.users_imported":
		snap.oldUsers = slices.Clone(data.Users)
	case "alm.projects_imported":
		snap.oldProjects = slices.Clone(data.Projects)
	}
}

func snapshotScopedLabels(data *domain.Bootstrap, scopes []string) map[int]domain.IssueLabel {
	if data.LabelIndex == nil || len(data.Labels) == 0 {
		return nil
	}
	result := map[int]domain.IssueLabel{}
	for _, scope := range scopes {
		for _, index := range data.LabelIndex[scope] {
			if index >= 0 && index < len(data.Labels) {
				result[index] = data.Labels[index]
			}
		}
	}
	return result
}

func rollbackTeamMutation(next *domain.Bootstrap, snap *teamMutationSnapshot, aggregateID string) {
	restoreCatalogByID(next.Teams, snap.oldTeams, func(item domain.Team) string { return item.ID })
	restoreCatalogByID(next.Users, snap.oldUsers, func(item domain.User) string { return item.ID })
	restoreCatalogByID(next.Projects, snap.oldProjects, func(item domain.Project) string { return item.ID })
	if next.TeamSettings != nil {
		for id, settings := range snap.oldImportSettings {
			next.TeamSettings[id] = settings
		}
	}
	if snap.teamIndex >= 0 && snap.teamIndex < len(next.Teams) {
		next.Teams[snap.teamIndex] = snap.oldTeam
	}
	if snap.actorIndex >= 0 && !snap.actorAppended && snap.actorIndex < len(next.Users) {
		next.Users[snap.actorIndex] = snap.oldActor
	}
	settingsKey := snap.hintID
	if settingsKey == "" {
		settingsKey = aggregateID
	}
	if next.TeamSettings != nil {
		if snap.hadSettings && settingsKey != "" {
			next.TeamSettings[settingsKey] = snap.oldSettings
		} else if settingsKey != "" {
			delete(next.TeamSettings, settingsKey)
		}
		for i := snap.teamsLen; i < len(next.Teams); i++ {
			id := next.Teams[i].ID
			if !snap.hadSettings || id != settingsKey {
				delete(next.TeamSettings, id)
			}
		}
	}
	if next.CycleSettings != nil {
		for _, id := range snap.cycleIDs {
			if old, ok := snap.oldCycleSettings[id]; ok {
				next.CycleSettings[id] = old
			} else {
				delete(next.CycleSettings, id)
			}
		}
		if settingsKey != "" {
			if _, ok := snap.oldCycleSettings[settingsKey]; !ok && !cycleIDKnown(snap.cycleIDs, settingsKey) {
				delete(next.CycleSettings, settingsKey)
			}
		}
		for i := snap.teamsLen; i < len(next.Teams); i++ {
			id := next.Teams[i].ID
			if _, ok := snap.oldCycleSettings[id]; !ok {
				delete(next.CycleSettings, id)
			}
		}
	}
	for index, label := range snap.oldLabels {
		if index >= 0 && index < len(next.Labels) {
			next.Labels[index] = label
		}
	}
}

func restoreCatalogByID[T any](items []T, previous []T, idOf func(T) string) {
	if len(previous) == 0 || len(items) == 0 {
		return
	}
	index := make(map[string]int, len(items))
	for i, item := range items {
		if id := idOf(item); id != "" {
			index[id] = i
		}
	}
	for _, old := range previous {
		if i, ok := index[idOf(old)]; ok {
			items[i] = old
		}
	}
}

func cycleIDKnown(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func noteTeamMutationIndexes(eventType, aggregateID string, snap *teamMutationSnapshot, next *domain.Bootstrap) {
	switch eventType {
	case "team.created", "alm.org_teams_imported":
		domain.NoteTeamsAppended(next, snap.teamsLen)
	case "team.updated":
		if snap.teamIndex >= 0 && snap.teamIndex < len(next.Teams) {
			if key := next.Teams[snap.teamIndex].Key; key != snap.oldTeamKey {
				domain.NoteTeamKeyChanged(next, aggregateID, snap.oldTeamKey, key)
			}
		}
	case "team.settings_updated":
		parent := ""
		if settings, ok := next.TeamSettings[aggregateID]; ok {
			parent = settings.ParentTeamID
		}
		if parent != snap.oldParent {
			domain.NoteTeamParentChanged(next, aggregateID, snap.oldParent, parent)
		}
	}
	if next.LabelIndex == nil {
		next.LabelIndex = map[string][]int{}
	}
	for i := snap.labelsLen; i < len(next.Labels); i++ {
		if scope := next.Labels[i].Scope; scope != "" {
			next.LabelIndex[scope] = append(next.LabelIndex[scope], i)
		}
	}
}

func teamMutationEntity(next *domain.Bootstrap, aggregateID string, teamsLen int) any {
	if next.TeamByID != nil && len(next.TeamByID) == len(next.Teams) {
		if index, ok := next.TeamByID[aggregateID]; ok && index >= 0 && index < len(next.Teams) {
			return next.Teams[index]
		}
	}
	for i := teamsLen; i < len(next.Teams); i++ {
		if next.Teams[i].ID == aggregateID {
			return next.Teams[i]
		}
	}
	if index, ok := next.TeamByID[aggregateID]; ok && index >= 0 && index < teamsLen && index < len(next.Teams) {
		return next.Teams[index]
	}
	return aggregateJSONValue(*next, aggregateID)
}

func teamMutationPreviousValues(eventType, aggregateID string, snap *teamMutationSnapshot, next domain.Bootstrap) json.RawMessage {
	if eventType == "team.created" || snap.teamIndex < 0 || snap.teamIndex >= len(next.Teams) {
		return nil
	}
	previous := domain.Bootstrap{Teams: []domain.Team{snap.oldTeam}}
	updated := domain.Bootstrap{Teams: []domain.Team{next.Teams[snap.teamIndex]}}
	if snap.hadSettings {
		previous.TeamSettings = map[string]domain.TeamSettings{aggregateID: snap.oldSettings}
		if settings, ok := next.TeamSettings[aggregateID]; ok {
			updated.TeamSettings = map[string]domain.TeamSettings{aggregateID: settings}
		}
	}
	return aggregatePreviousValues(previous, updated, aggregateID)
}

func cloneMutationTeamSettings(settings domain.TeamSettings) domain.TeamSettings {
	if len(settings.SlackNotifications) > 0 {
		settings.SlackNotifications = maps.Clone(settings.SlackNotifications)
	}
	if len(settings.PRAutomations) > 0 {
		settings.PRAutomations = maps.Clone(settings.PRAutomations)
	}
	if len(settings.ReleaseAutomations) > 0 {
		settings.ReleaseAutomations = slices.Clone(settings.ReleaseAutomations)
	}
	if len(settings.TriageRules) > 0 {
		settings.TriageRules = slices.Clone(settings.TriageRules)
	}
	if len(settings.AgentSkills) > 0 {
		settings.AgentSkills = slices.Clone(settings.AgentSkills)
	}
	return settings
}

func collectDirtyTeamRecords(eventType, aggregateID string, snap *teamMutationSnapshot, next domain.Bootstrap) ([]metadataRecordChange, error) {
	var upserts []metadataRecordChange
	appendRecord := func(field, key string, insert bool, value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		upserts = append(upserts, metadataRecordChange{field: field, key: key, insert: insert, raw: raw})
		return nil
	}
	appendRange := func(field string, start int, idOf func(int) string, valueOf func(int) any) error {
		for i := start; i < lenForField(next, field); i++ {
			if err := appendRecord(field, idOf(i), true, valueOf(i)); err != nil {
				return err
			}
		}
		return nil
	}
	switch eventType {
	case "team.created", "alm.org_teams_imported":
		if eventType == "alm.org_teams_imported" {
			if err := collectExistingTeamImportUpdates(snap, next, appendRecord); err != nil {
				return nil, err
			}
		}
		for i := snap.teamsLen; i < len(next.Teams); i++ {
			team := next.Teams[i]
			if eventType == "team.created" && aggregateID != "" && team.ID != aggregateID {
				continue
			}
			if err := appendRecord("teams", team.ID, true, team); err != nil {
				return nil, err
			}
			if settings, ok := next.TeamSettings[team.ID]; ok {
				if err := appendRecord("teamSettings", team.ID, false, settings); err != nil {
					return nil, err
				}
			}
			if settings, ok := next.CycleSettings[team.ID]; ok {
				if err := appendRecord("cycleSettings", team.ID, false, settings); err != nil {
					return nil, err
				}
			}
		}
		if err := appendRange("states", snap.statesLen, func(i int) string { return next.States[i].ID }, func(i int) any { return next.States[i] }); err != nil {
			return nil, err
		}
		if err := appendRange("labels", snap.labelsLen, func(i int) string { return next.Labels[i].ID }, func(i int) any { return next.Labels[i] }); err != nil {
			return nil, err
		}
		if err := appendRange("cycles", snap.cyclesLen, func(i int) string { return next.Cycles[i].ID }, func(i int) any { return next.Cycles[i] }); err != nil {
			return nil, err
		}
	case "alm.users_imported":
		if err := collectExistingUserImportUpdates(snap, next, appendRecord); err != nil {
			return nil, err
		}
		if err := appendRange("users", snap.usersLen, func(i int) string { return next.Users[i].ID }, func(i int) any { return next.Users[i] }); err != nil {
			return nil, err
		}
	case "alm.projects_imported":
		if err := collectExistingProjectImportUpdates(snap, next, appendRecord); err != nil {
			return nil, err
		}
		if err := appendRange("projects", snap.projectsLen, func(i int) string { return next.Projects[i].ID }, func(i int) any { return next.Projects[i] }); err != nil {
			return nil, err
		}
	case "team.updated":
		index := snap.teamIndex
		if index < 0 || index >= len(next.Teams) {
			index = domain.TeamIndex(&next, aggregateID)
		}
		if index >= 0 && index < len(next.Teams) {
			team := next.Teams[index]
			if err := appendRecord("teams", team.ID, false, team); err != nil {
				return nil, err
			}
		}
		if settings, ok := next.TeamSettings[aggregateID]; ok {
			if !snap.hadSettings || !metadataTeamSettingsEqual(snap.oldSettings, settings) {
				if err := appendRecord("teamSettings", aggregateID, false, settings); err != nil {
					return nil, err
				}
			}
		}
	default:
		if settings, ok := next.TeamSettings[aggregateID]; ok {
			if !snap.hadSettings || !metadataTeamSettingsEqual(snap.oldSettings, settings) {
				if err := appendRecord("teamSettings", aggregateID, false, settings); err != nil {
					return nil, err
				}
			}
		}
		if err := appendRange("states", snap.statesLen, func(i int) string { return next.States[i].ID }, func(i int) any { return next.States[i] }); err != nil {
			return nil, err
		}
		index := snap.teamIndex
		if index >= 0 && index < len(next.Teams) && !metadataTeamEqual(snap.oldTeam, next.Teams[index]) {
			team := next.Teams[index]
			if err := appendRecord("teams", team.ID, false, team); err != nil {
				return nil, err
			}
		}
		for i, old := range snap.oldLabels {
			if i >= 0 && i < len(next.Labels) && !metadataLabelEqual(old, next.Labels[i]) {
				label := next.Labels[i]
				if err := appendRecord("labels", label.ID, false, label); err != nil {
					return nil, err
				}
			}
		}
		if err := appendRange("labels", snap.labelsLen, func(i int) string { return next.Labels[i].ID }, func(i int) any { return next.Labels[i] }); err != nil {
			return nil, err
		}
		if err := appendRange("cycles", snap.cyclesLen, func(i int) string { return next.Cycles[i].ID }, func(i int) any { return next.Cycles[i] }); err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, id := range append([]string{aggregateID}, snap.cycleIDs...) {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			settings, ok := next.CycleSettings[id]
			if !ok {
				continue
			}
			old, existed := snap.oldCycleSettings[id]
			if existed && old == settings {
				continue
			}
			if err := appendRecord("cycleSettings", id, false, settings); err != nil {
				return nil, err
			}
		}
	}
	return upserts, nil
}

func collectExistingTeamImportUpdates(snap *teamMutationSnapshot, next domain.Bootstrap, appendRecord func(string, string, bool, any) error) error {
	if len(snap.oldTeams) == 0 {
		return nil
	}
	previous := make(map[string]domain.Team, len(snap.oldTeams))
	for _, team := range snap.oldTeams {
		previous[team.ID] = team
	}
	limit := snap.teamsLen
	if limit > len(next.Teams) {
		limit = len(next.Teams)
	}
	for i := 0; i < limit; i++ {
		team := next.Teams[i]
		old, ok := previous[team.ID]
		if !ok {
			continue
		}
		if !metadataTeamEqual(old, team) {
			if err := appendRecord("teams", team.ID, false, team); err != nil {
				return err
			}
		}
		settings, hasSettings := next.TeamSettings[team.ID]
		oldSettings, hadSettings := snap.oldImportSettings[team.ID]
		if hasSettings && (!hadSettings || !metadataTeamSettingsEqual(oldSettings, settings)) {
			if err := appendRecord("teamSettings", team.ID, false, settings); err != nil {
				return err
			}
		}
	}
	return nil
}

func collectExistingUserImportUpdates(snap *teamMutationSnapshot, next domain.Bootstrap, appendRecord func(string, string, bool, any) error) error {
	if len(snap.oldUsers) == 0 {
		return nil
	}
	previous := make(map[string]domain.User, len(snap.oldUsers))
	for _, user := range snap.oldUsers {
		previous[user.ID] = user
	}
	limit := snap.usersLen
	if limit > len(next.Users) {
		limit = len(next.Users)
	}
	for i := 0; i < limit; i++ {
		user := next.Users[i]
		old, ok := previous[user.ID]
		if !ok || metadataUserEqual(old, user) {
			continue
		}
		if err := appendRecord("users", user.ID, false, user); err != nil {
			return err
		}
	}
	return nil
}

func collectExistingProjectImportUpdates(snap *teamMutationSnapshot, next domain.Bootstrap, appendRecord func(string, string, bool, any) error) error {
	if len(snap.oldProjects) == 0 {
		return nil
	}
	previous := make(map[string]domain.Project, len(snap.oldProjects))
	for _, project := range snap.oldProjects {
		previous[project.ID] = project
	}
	limit := snap.projectsLen
	if limit > len(next.Projects) {
		limit = len(next.Projects)
	}
	for i := 0; i < limit; i++ {
		project := next.Projects[i]
		old, ok := previous[project.ID]
		if !ok || metadataProjectEqual(old, project) {
			continue
		}
		if err := appendRecord("projects", project.ID, false, project); err != nil {
			return err
		}
	}
	return nil
}

func lenForField(next domain.Bootstrap, field string) int {
	switch field {
	case "states":
		return len(next.States)
	case "labels":
		return len(next.Labels)
	case "cycles":
		return len(next.Cycles)
	case "users":
		return len(next.Users)
	case "projects":
		return len(next.Projects)
	default:
		return 0
	}
}

func (s *SQLiteStore) persistTeamMetadata(ctx context.Context, workspaceKey string, next domain.Bootstrap, event *domain.DomainEvent, upserts []metadataRecordChange, membersChanged, teamMembersChanged bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if event != nil && event.Type == "team.settings_updated" {
		var change map[string]json.RawMessage
		if json.Unmarshal(event.Payload, &change) == nil && change["parentTeamId"] != nil {
			if err := syncTeamAncestorMembers(ctx, tx, next, event.AggregateID); err != nil {
				return err
			}
		}
	}
	if err := writeMetadataRecordUpserts(ctx, tx, workspaceKey, upserts); err != nil {
		return err
	}
	if membersChanged {
		raw, err := json.Marshal(next.Members)
		if err != nil {
			return err
		}
		if err := writeRootCollectionArray(ctx, tx, workspaceKey, "members", raw); err != nil {
			return err
		}
	}
	if teamMembersChanged {
		raw, err := json.Marshal(next.TeamMembers)
		if err != nil {
			return err
		}
		if err := writeRootCollectionArray(ctx, tx, workspaceKey, "teamMembers", raw); err != nil {
			return err
		}
	}
	if event != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), []byte(event.PreviousValues), event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	viewerRaw, _ := json.Marshal(s.viewer)
	if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
		viewerRaw, _ = json.Marshal(next.Viewer)
	}
	if err := writeAccountMetadata(ctx, tx, workspaceKey, viewerRaw); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.cacheMetadataUpserts(ctx, workspaceKey, upserts)
	return nil
}

type metadataRecordChange struct {
	field  string
	key    string
	insert bool
	order  int
	raw    json.RawMessage
}

func writeMetadataRecordUpserts(ctx context.Context, tx *sqlTx, workspace string, upserts []metadataRecordChange) error {
	bases := map[string]int{}
	for i, change := range upserts {
		var existing sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, change.field, change.key).Scan(&existing)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		order := 0
		if existing.Valid {
			order = int(existing.Int64)
		} else {
			base, known := bases[change.field]
			if !known {
				var highest sql.NullInt64
				if err := tx.QueryRowContext(ctx, `SELECT MAX(collection_order) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, change.field).Scan(&highest); err != nil {
					return err
				}
				base = -1
				if highest.Valid {
					base = int(highest.Int64)
				}
			}
			base++
			bases[change.field] = base
			order = base
		}
		upserts[i].order = order
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, change.field, change.key, order, []byte(change.raw)); err != nil {
			return err
		}
		if err := syncMetadataSearchDocument(ctx, tx, workspace, change.field, change.key, []byte(change.raw)); err != nil {
			return err
		}
	}
	return nil
}

func writeRootCollectionArray(ctx context.Context, tx *sqlTx, workspace, field string, raw json.RawMessage) error {
	var encoded []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&encoded); err != nil {
		return err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &root); err != nil {
		return err
	}
	root[field] = raw
	if manifest, known := root[metadataCollectionsKey]; known {
		var shapes map[string]string
		if json.Unmarshal(manifest, &shapes) == nil {
			if _, exists := shapes[field]; exists {
				delete(shapes, field)
				root[metadataCollectionsKey], _ = json.Marshal(shapes)
			}
		}
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspace_states SET data=?,updated_at=? WHERE workspace_key=?`, encoded, time.Now().UTC().Format(time.RFC3339Nano), workspace)
	return err
}

func equalTimePointer(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func metadataTeamEqual(a, b domain.Team) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Key == b.Key && a.Color == b.Color && a.Icon == b.Icon &&
		a.Private == b.Private && a.ExternalSource == b.ExternalSource && equalTimePointer(a.RetiredAt, b.RetiredAt) &&
		equalTimePointer(a.CreatedAt, b.CreatedAt) && equalTimePointer(a.UpdatedAt, b.UpdatedAt)
}

func metadataUserEqual(a, b domain.User) bool {
	return a.ID == b.ID && a.UserID == b.UserID && a.Name == b.Name && a.DisplayName == b.DisplayName &&
		a.JobTitle == b.JobTitle && a.Email == b.Email && a.AvatarURL == b.AvatarURL && a.Active == b.Active &&
		a.EmailVerified == b.EmailVerified && a.App == b.App && a.BuiltinAgent == b.BuiltinAgent &&
		a.OAuthClientID == b.OAuthClientID && slices.Equal(a.AppScopes, b.AppScopes) && slices.Equal(a.AppTeamIDs, b.AppTeamIDs) &&
		equalTimePointer(a.OutOfOfficeUntil, b.OutOfOfficeUntil)
}

func metadataProjectEqual(a, b domain.Project) bool {
	return a.ID == b.ID && a.Name == b.Name && a.SlugID == b.SlugID && a.Summary == b.Summary &&
		a.Description == b.Description && a.Icon == b.Icon && a.Color == b.Color && a.Priority == b.Priority &&
		a.Health == b.Health && a.Status.ID == b.Status.ID && a.Status.Name == b.Status.Name &&
		a.Status.Type == b.Status.Type && slices.Equal(a.TeamIDs, b.TeamIDs) && slices.Equal(a.MemberIDs, b.MemberIDs) &&
		equalStringPointer(a.StartDate, b.StartDate) && equalStringPointer(a.TargetDate, b.TargetDate)
}

func equalStringPointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func metadataLabelEqual(a, b domain.IssueLabel) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Color == b.Color && a.Description == b.Description &&
		a.IssueCount == b.IssueCount && a.Scope == b.Scope && a.ResourceType == b.ResourceType &&
		a.GroupID == b.GroupID && a.CreatorID == b.CreatorID && a.CreatedAt.Equal(b.CreatedAt) &&
		equalTimePointer(a.LastAppliedAt, b.LastAppliedAt) && equalTimePointer(a.ArchivedAt, b.ArchivedAt)
}

func metadataTeamSettingsEqual(a, b domain.TeamSettings) bool {
	return a.TeamID == b.TeamID && a.Description == b.Description && a.Timezone == b.Timezone &&
		a.EstimateType == b.EstimateType && a.DefaultStateID == b.DefaultStateID &&
		a.DefaultPriority == b.DefaultPriority && a.IssueEmailEnabled == b.IssueEmailEnabled &&
		a.DetailedHistory == b.DetailedHistory && a.Access == b.Access &&
		a.MembershipRestriction == b.MembershipRestriction && a.SettingsPermission == b.SettingsPermission &&
		a.LabelPermission == b.LabelPermission && a.TemplatePermission == b.TemplatePermission &&
		a.AgentSkillPermission == b.AgentSkillPermission && a.LoopPermission == b.LoopPermission &&
		a.MemberPermission == b.MemberPermission && a.SlackChannelID == b.SlackChannelID &&
		a.SlackChannelName == b.SlackChannelName && maps.Equal(a.SlackNotifications, b.SlackNotifications) &&
		maps.Equal(a.PRAutomations, b.PRAutomations) && a.AutoCloseParents == b.AutoCloseParents &&
		a.AutoCloseSubIssues == b.AutoCloseSubIssues && a.AutoCloseStale == b.AutoCloseStale &&
		a.StaleMonths == b.StaleMonths && a.StaleStatusID == b.StaleStatusID &&
		a.AutoArchiveMonths == b.AutoArchiveMonths && a.ProgressOrder == b.ProgressOrder &&
		slices.Equal(a.ReleaseAutomations, b.ReleaseAutomations) && a.TriageEnabled == b.TriageEnabled &&
		a.TriageRequirePriority == b.TriageRequirePriority && a.TriageAction == b.TriageAction &&
		slices.Equal(a.TriageRules, b.TriageRules) && slices.Equal(a.AgentSkills, b.AgentSkills) &&
		a.ProjectUpdatePrompt == b.ProjectUpdatePrompt && a.ResolvedSummaries == b.ResolvedSummaries &&
		a.ShowInitiatives == b.ShowInitiatives && a.InheritIssueEstimation == b.InheritIssueEstimation &&
		a.InheritWorkflowStatuses == b.InheritWorkflowStatuses && a.InheritProjectStatuses == b.InheritProjectStatuses &&
		a.InheritCycles == b.InheritCycles && a.ParentTeamID == b.ParentTeamID
}
