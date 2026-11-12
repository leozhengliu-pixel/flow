package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func realtimeValueBytes(value reflect.Value, budget, depth int) (int, bool) {
	if !value.IsValid() {
		return 0, true
	}
	if depth > 32 || budget < 0 {
		return 0, false
	}
	if value.Type() == reflect.TypeFor[time.Time]() {
		return 32, budget >= 32
	}
	used := 32
	switch value.Kind() {
	case reflect.String:
		used += value.Len() * 2
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			size, ok := realtimeValueBytes(value.Elem(), budget-used, depth+1)
			return used + size, ok
		}
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			used += value.Len() * 2
			break
		}
		for i := 0; i < value.Len(); i++ {
			size, ok := realtimeValueBytes(value.Index(i), budget-used, depth+1)
			used += size
			if !ok || used > budget {
				return used, false
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			size, ok := realtimeValueBytes(value.Field(i), budget-used, depth+1)
			used += size
			if !ok || used > budget {
				return used, false
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			for _, item := range []reflect.Value{iter.Key(), iter.Value()} {
				size, ok := realtimeValueBytes(item, budget-used, depth+1)
				used += size
				if !ok || used > budget {
					return used, false
				}
			}
		}
	}
	return used, used <= budget
}

func cloneRealtimeValue[T any](value T) (T, int, error) {
	var owned T
	if _, ok := realtimeValueBytes(reflect.ValueOf(value), 128<<10, 0); !ok {
		return owned, 0, store.ErrEventSourceBudget
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return owned, 0, err
	}
	if err := json.Unmarshal(raw, &owned); err != nil {
		return owned, 0, err
	}
	return owned, len(raw)*3 + 512, nil
}

func (s *server) buildPulseEventSource(ctx context.Context, workspace string, source *EventSource) error {
	snapshot, ok := s.store.PulseSources(workspace)
	if !ok {
		return store.ErrAuthForbidden
	}
	all := s.realtime.pulseRulesFor(workspace, snapshot)
	compact := &store.PulseFeedSnapshot{ProjectUpdates: map[string][]domain.ProjectUpdate{}, InitiativeUpdates: map[string][]domain.InitiativeUpdate{}}
	ids := []string{}
	id := source.event.AggregateID
	rootKind := "initiatives"
	if strings.HasPrefix(source.event.Type, "project") {
		rootKind = "projects"
	}
	rootScopes, err := s.store.ReadRealtimeMetadataScopes(ctx, workspace, rootKind, []string{id})
	if err != nil {
		return err
	}
	if _, exists := rootScopes[id]; !exists {
		source.pulse = compact
		source.rules = newPulseRules(compact)
		return nil
	}
	if strings.HasPrefix(source.event.Type, "project") {
		if root := all.projects[id]; root != nil {
			item, cost, err := cloneRealtimeValue(*root)
			if err != nil {
				return err
			}
			var current domain.Project
			if err := json.Unmarshal(rootScopes[id], &current); err != nil {
				return err
			}
			item.TeamIDs, item.DependencyIDs, item.Initiatives = current.TeamIDs, current.DependencyIDs, current.Initiatives
			source.bytes += cost
			compact.Projects = append(compact.Projects, item)
			ids = append(ids, item.DependencyIDs...)
			ids = append(ids, item.ID)
			if strings.HasPrefix(source.event.Type, "project.update_") {
				updates, cost, err := cloneRealtimeValue(snapshot.ProjectUpdates[id])
				if err != nil {
					return err
				}
				source.bytes += cost
				compact.ProjectUpdates[id] = updates
			}
		}
	} else if root := all.initiatives[id]; root != nil {
		item, cost, err := cloneRealtimeValue(*root)
		if err != nil {
			return err
		}
		var current domain.Initiative
		if err := json.Unmarshal(rootScopes[id], &current); err != nil {
			return err
		}
		item.LeadTeamID, item.ContributingTeamIDs, item.ProjectIDs, item.ParentInitiativeIDs = current.LeadTeamID, current.ContributingTeamIDs, current.ProjectIDs, current.ParentInitiativeIDs
		source.bytes += cost
		compact.Initiatives = append(compact.Initiatives, item)
		parents := all.initiativeParents[id]
		if len(parents) > 4096 {
			return store.ErrEventSourceBudget
		}
		for _, parentID := range parents {
			if parent := all.initiatives[parentID]; parent != nil {
				compact.Initiatives = append(compact.Initiatives, domain.Initiative{ID: parent.ID, LeadTeamID: parent.LeadTeamID, ContributingTeamIDs: slices.Clone(parent.ContributingTeamIDs), ProjectIDs: slices.Clone(all.initiativeProjects[parent.ID])})
			}
		}
		parentScopes, err := s.store.ReadRealtimeMetadataScopes(ctx, workspace, "initiatives", parents)
		if err != nil {
			return err
		}
		for i := 1; i < len(compact.Initiatives); i++ {
			item := &compact.Initiatives[i]
			raw, exists := parentScopes[item.ID]
			if !exists {
				continue
			}
			if err := json.Unmarshal(raw, item); err != nil {
				return err
			}
		}
		compact.Initiatives = slices.DeleteFunc(compact.Initiatives, func(item domain.Initiative) bool { _, exists := parentScopes[item.ID]; return item.ID != id && !exists })
		for _, item := range compact.Initiatives {
			ids = append(ids, item.ProjectIDs...)
			ids = append(ids, all.initiativeProjects[item.ID]...)
		}
		compact.InitiativeRelations = []domain.InitiativeRelation{}
		for _, parent := range parents {
			compact.InitiativeRelations = append(compact.InitiativeRelations, domain.InitiativeRelation{ID: parent, Type: "parent", InitiativeID: id, RelatedInitiativeID: parent})
		}
		if strings.HasPrefix(source.event.Type, "initiative.update_") {
			updates, cost, err := cloneRealtimeValue(snapshot.InitiativeUpdates[id])
			if err != nil {
				return err
			}
			source.bytes += cost
			compact.InitiativeUpdates[id] = updates
		}
	}
	teams, cost, err := s.store.ReadRealtimeProjectTeams(ctx, workspace, ids)
	if err != nil {
		return err
	}
	source.bytes += cost
	for i := range compact.Projects {
		if current, ok := teams[compact.Projects[i].ID]; ok {
			compact.Projects[i].TeamIDs = current
		}
	}
	for project, teamIDs := range teams {
		if !slices.ContainsFunc(compact.Projects, func(item domain.Project) bool { return item.ID == project }) {
			compact.Projects = append(compact.Projects, domain.Project{ID: project, TeamIDs: teamIDs})
		}
	}
	source.pulse = compact
	source.rules = newPulseRules(compact)
	// Keep project links in both directions, as the existing feed rules do.
	projectScopes, err := s.store.ReadRealtimeMetadataScopes(ctx, workspace, "projects", ids)
	if err != nil {
		return err
	}
	for _, item := range compact.Initiatives {
		links := slices.Clone(item.ProjectIDs)
		for _, candidate := range all.initiativeProjects[item.ID] {
			var project domain.Project
			if raw, exists := projectScopes[candidate]; exists {
				if err := json.Unmarshal(raw, &project); err != nil {
					return err
				}
				if slices.Contains(project.Initiatives, item.ID) && !slices.Contains(links, candidate) {
					links = append(links, candidate)
				}
			}
		}
		source.rules.initiativeProjects[item.ID] = links
	}
	return nil
}

func (source *EventSource) pulseProjection(data domain.Bootstrap, query store.IssueRecordQuery) (domain.RealtimeEvent, bool) {
	event := source.event
	viewer := newPulseViewerFrom(&store.PulseFeedSnapshot{Teams: data.Teams, TeamSettings: data.TeamSettings}, data.Viewer.ID, data.ViewerRole, data.TeamMembers, nil, query.AllowedTeamIDs)
	scopeChanged := realtimeScopeChanged(event)
	extra := map[string]any{}
	if scopeChanged {
		extra["scopeChanged"] = true
	}
	if strings.HasPrefix(event.Type, "project") {
		project := source.rules.projects[event.AggregateID]
		if project == nil {
			if event.Type == "project.deleted" {
				return anonymousRealtimeEvent(event), true
			}
			return event, false
		}
		if !source.rules.projectVisible(viewer, project) {
			if scopeChanged {
				return anonymousRealtimeEvent(event), true
			}
			return event, false
		}
		view := realtimeProjectView(source.rules, viewer, *project)
		if strings.HasPrefix(event.Type, "project.update_") {
			updates := append([]domain.ProjectUpdate{}, source.pulse.ProjectUpdates[project.ID]...)
			for i := range updates {
				updates[i].Snapshot = nil
			}
			extra["updates"] = updates
		}
		event.Payload = realtimeEntityPayload(event, view, extra)
		return event, true
	}
	initiative := source.rules.initiatives[event.AggregateID]
	if initiative == nil {
		if event.Type == "initiative.deleted" {
			return anonymousRealtimeEvent(event), true
		}
		return event, false
	}
	if !source.rules.initiativeVisible(viewer, initiative) {
		if scopeChanged {
			return anonymousRealtimeEvent(event), true
		}
		return event, false
	}
	view := realtimeInitiativeView(source.rules, viewer, *initiative)
	if strings.HasPrefix(event.Type, "initiative.update_") {
		updates := append([]domain.InitiativeUpdate{}, source.pulse.InitiativeUpdates[initiative.ID]...)
		for i := range updates {
			updates[i].Snapshot = nil
		}
		extra["updates"] = updates
	}
	event.Payload = realtimeEntityPayload(event, view, extra)
	return event, true
}

func (source *EventSource) metadataProjectionKey(data domain.Bootstrap, query store.IssueRecordQuery) string {
	if source.kind == "policy" {
		event, visible := source.policyProjection(data, query)
		if !visible {
			return "hidden"
		}
		if event.AggregateID == "" && event.ActorID == "" {
			return "anonymous"
		}
		return "visible"
	}
	if source.kind == "pulse" {
		viewer := newPulseViewerFrom(&store.PulseFeedSnapshot{Teams: data.Teams, TeamSettings: data.TeamSettings}, data.Viewer.ID, data.ViewerRole, data.TeamMembers, nil, query.AllowedTeamIDs)
		var fields any
		if strings.HasPrefix(source.event.Type, "project") {
			project := source.rules.projects[source.event.AggregateID]
			if project == nil || !source.rules.projectVisible(viewer, project) {
				return "hidden"
			}
			view := realtimeProjectView(source.rules, viewer, *project)
			fields = struct{ Teams, Dependencies []string }{view.TeamIDs, view.DependencyIDs}
		} else {
			initiative := source.rules.initiatives[source.event.AggregateID]
			if initiative == nil || !source.rules.initiativeVisible(viewer, initiative) {
				return "hidden"
			}
			view := realtimeInitiativeView(source.rules, viewer, *initiative)
			fields = struct {
				Lead                     string
				Teams, Projects, Parents []string
			}{view.LeadTeamID, view.ContributingTeamIDs, view.ProjectIDs, view.ParentInitiativeIDs}
		}
		raw, _ := json.Marshal(fields)
		sum := sha256.Sum256(raw)
		return string(sum[:])
	}
	return ""
}

func (s *server) buildPolicyEventSource(ctx context.Context, workspace string, source *EventSource) error {
	if source.event.Type == "coding.approval_requested" {
		return errEventSourceUnsupported
	}
	source.kind = "policy"
	rule, _ := realtimeRuleFor(source.event.Type)
	if rule.visible == nil || rule.anonymous {
		return nil
	}
	if rule.payloadTeam {
		var payload struct {
			TeamID string `json:"teamId"`
			State  struct {
				TeamID string `json:"teamId"`
			} `json:"state"`
		}
		_ = json.Unmarshal(source.event.Payload, &payload)
		if id := cmp.Or(payload.TeamID, payload.State.TeamID); id != "" {
			source.policyRecord.TeamID = id
			source.policyFound = true
		}
	}
	if !source.policyFound && rule.teamAggregate && source.policy.HasTeam(source.event.AggregateID) {
		source.policyRecord.TeamID = source.event.AggregateID
		source.policyFound = true
	}
	for _, field := range rule.fields {
		if source.policyFound {
			break
		}
		batch, err := s.store.ReadCanonicalRecords(ctx, workspace, []store.RecordKey{{Kind: field, ID: source.event.AggregateID}}, "detail")
		if err != nil {
			return err
		}
		if len(batch.Items) == 0 || batch.Items[0].Missing {
			continue
		}
		if batch.Items[0].Omitted {
			return store.ErrEventSourceBudget
		}
		if err := json.Unmarshal(batch.Items[0].Data, &source.policyRecord); err != nil {
			return err
		}
		source.bytes += len(batch.Items[0].Data) * 3
		source.policyFound = true
	}
	if source.policyRecord.ProjectID != "" {
		teams, cost, err := s.store.ReadRealtimeProjectTeams(ctx, workspace, []string{source.policyRecord.ProjectID})
		if err != nil {
			return err
		}
		source.bytes += cost
		source.rules = &pulseRules{projects: map[string]*domain.Project{}}
		for id, ids := range teams {
			source.rules.projects[id] = &domain.Project{ID: id, TeamIDs: ids}
		}
	}
	ids := slices.Clone(source.policyRecord.IssueIDs)
	if source.policyRecord.IssueID != "" {
		ids = append(ids, source.policyRecord.IssueID)
	}
	if len(ids) > 0 {
		var err error
		source.issues, err = s.store.ReadRealtimeIssueAccess(ctx, workspace, ids, source.version)
		if err != nil {
			return err
		}
		source.bytes += source.issues.Bytes()
	}
	return nil
}

func (source *EventSource) policyProjection(data domain.Bootstrap, query store.IssueRecordQuery) (domain.RealtimeEvent, bool) {
	event := source.event
	if event.Type == "connected" || event.Type == "workspace.resync_required" {
		return event, true
	}
	rule, _ := realtimeRuleFor(event.Type)
	viewer := &realtimeViewer{s: &server{}, data: data, query: query, event: event, source: source}
	return projectPolicyRecord(viewer, rule, source.policyRecord, source.policyFound)
}

func sharedEventType(kind string) bool {
	if ephemeralRealtimeEvent(kind) && kind != "workspace.resync_required" {
		return false
	}
	for _, prefix := range []string{"favorite.", "favorite_folder.", "subscription.", "notifications.", "notification.", "draft.", "drafts.", "ai.", "document.draft_"} {
		if strings.HasPrefix(kind, prefix) && kind != "document.draft_published" {
			return false
		}
	}
	return kind != "workspace_preferences.updated" && kind != "coding.approval_requested"
}

func (source *EventSource) authorizationFingerprint() string {
	var value any
	switch source.kind {
	case "issue", "batch":
		return source.issues.AuthorizationFingerprint()
	case "document":
		if source.document == nil {
			return "missing"
		}
		item := source.document
		value = struct {
			Creator, Issue, IssueTeam  string
			Teams, Projects, Inherited []string
			Permissions                []domain.DocumentPermission
			Closed                     []string
			Actor                      domain.User
		}{item.Creator.ID, item.IssueID, item.IssueTeamID, item.TeamIDs, item.ProjectIDs, item.InheritedTeamIDs, item.Permissions, source.policy.DocumentClosedTeams(*item), item.Creator}
	case "pulse":
		projects := map[string][]string{}
		initiatives := map[string]domain.Initiative{}
		type actors struct {
			Creator     domain.User
			Lead, Owner *domain.User
		}
		users := map[string]actors{}
		for id, item := range source.rules.projects {
			projects[id] = item.TeamIDs
			user := actors{Lead: item.Lead}
			if item.Creator != nil {
				user.Creator = *item.Creator
			}
			users[id] = user
		}
		for id, item := range source.rules.initiatives {
			initiatives[id] = domain.Initiative{ID: id, LeadTeamID: item.LeadTeamID, ContributingTeamIDs: item.ContributingTeamIDs, ProjectIDs: item.ProjectIDs}
			users[id] = actors{Creator: item.Creator, Owner: item.Owner}
		}
		value = struct {
			Projects       map[string][]string
			Initiatives    map[string]domain.Initiative
			Links, Parents map[string][]string
			Actors         map[string]actors
		}{projects, initiatives, source.rules.initiativeProjects, source.rules.initiativeParents, users}
	case "policy":
		issues := ""
		if source.issues != nil {
			issues = source.issues.AuthorizationFingerprint()
		}
		projects := map[string][]string{}
		if source.rules != nil {
			for id, item := range source.rules.projects {
				projects[id] = item.TeamIDs
			}
		}
		value = struct {
			Record   realtimeRecord
			Found    bool
			Issues   string
			Projects map[string][]string
		}{source.policyRecord, source.policyFound, issues, projects}
	default:
		return source.kind
	}
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return string(sum[:])
}
