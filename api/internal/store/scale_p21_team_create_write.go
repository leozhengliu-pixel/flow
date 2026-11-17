package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// TeamCreateWrite declares the inputs of the team creation callback.
type TeamCreateWrite struct {
	TeamID string
	// SourceTeamIDs are the parent and copy-from teams. The callback may copy
	// their settings, cycle settings and (inherited) workflow statuses.
	SourceTeamIDs []string
	// Development (AUTH_DISABLED) callbacks keep workspace and team members in
	// the workspace document and need users, members and team members whole.
	Development bool
}

// teamCreateWrittenFields are the only fields mutateTeamMetadata persists for
// team.created besides the member root arrays.
var teamCreateWrittenFields = []string{"teams", "teamSettings", "cycleSettings", "states", "labels", "cycles"}

// CreateTeamRecords runs the unchanged team creation callback on the bounded
// store with the result mutateTeamMetadata gives it: complete teams and team
// settings, the shared and source-team workflow statuses in collection order,
// the source teams' cycle settings and the viewer's team memberships. It
// persists through the literal persistTeamMetadata (the same rows, member
// root arrays, event and account state) and installs the post-image through
// the resident publication, without hydrating the workspace.
func (s *SQLiteStore) CreateTeamRecords(ctx context.Context, workspace string, payload any, request TeamCreateWrite, mutate func(*domain.Bootstrap) error) error {
	if !s.BoundedMetadataEnabled() || mutate == nil || request.TeamID == "" {
		return ErrBoundedMetadataCoverage
	}
	const eventType = "team.created"
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	// MutateWorkspace's aggregate hint and its audit collector.
	ctx = withMutationAggregateID(ctx, request.TeamID)
	ctx, collector, callback := collectMutationAudit(ctx, workspace, func(data *domain.Bootstrap) (string, error) {
		return request.TeamID, mutate(data)
	})
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	var event domain.DomainEvent
	var realtime json.RawMessage
	apply := func(ctx context.Context) error {
		event, realtime = domain.DomainEvent{}, nil
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace %q: %w", workspace, errors.New("not found"))
		}
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(ctx, workspace); err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
		}
		stored, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		defer stored.Release()
		// The original callback sees the installed snapshot, whose members
		// carry their users' current display values (the load's display
		// pass); complete member lists are written back as handed.
		display, err := s.refreshLegacyDisplayImage(ctx, stored)
		if err != nil {
			return err
		}
		defer display.Release()
		inputs, complete, err := s.loadTeamCreateInputs(ctx, display, workspace, request)
		if err != nil {
			return err
		}
		defer inputs.close()
		data := inputs.data
		storedViewer, storedRole := data.Viewer, data.ViewerRole
		domain.EnsureTeamDirectory(&data)
		snap := snapshotTeamMutation(eventType, request.TeamID, &data)
		if actor, ok := actorFromContext(ctx); ok {
			data.Viewer = actor
			if role, status, roleErr := s.WorkspaceRole(ctx, data.Workspace.ID, actor.ID); roleErr == nil && status == "active" {
				data.ViewerRole = role
			}
			// The original replaces the actor in its users without persisting it.
			if index := slices.IndexFunc(data.Users, func(user domain.User) bool { return user.ID == actor.ID }); index >= 0 {
				data.Users[index] = actor
			} else {
				data.Users = append(data.Users, actor)
			}
		}
		aggregateID, err := callback(&data)
		data.Viewer, data.ViewerRole = storedViewer, storedRole
		if aggregateID == "" {
			aggregateID = snap.hintID
		}
		if err != nil {
			return err
		}
		upserts, err := collectDirtyTeamRecords(eventType, aggregateID, &snap, data)
		if err != nil {
			return err
		}
		fields := []string{}
		for _, change := range upserts {
			if !slices.Contains(teamCreateWrittenFields, change.field) {
				return fmt.Errorf("%w: team creation writes %s", ErrBoundedMetadataCoverage, change.field)
			}
			if !slices.Contains(fields, change.field) {
				fields = append(fields, change.field)
			}
		}
		membersChanged := len(data.Members) != snap.membersLen
		teamMembersChanged := len(data.TeamMembers) != snap.teamMembersLen
		// Members are written whole: they must have been handed whole (or,
		// for team members, be the empty list the callback filled).
		if membersChanged && !complete["members"] {
			return fmt.Errorf("%w: team creation changed workspace members", ErrBoundedMetadataCoverage)
		}
		if teamMembersChanged && !complete["teamMembers"] && !inputs.teamMembersEmpty {
			return fmt.Errorf("%w: team creation changed selected team members", ErrBoundedMetadataCoverage)
		}
		if len(upserts) == 0 && !membersChanged && !teamMembersChanged {
			return nil
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		realtime = enrichRealtimePayload(payloadRaw, teamMutationEntity(&data, aggregateID, snap.teamsLen), eventType)
		var publication *legacyResidentPublication
		defer func() { publication.Close() }()
		prepare := func(tx *sqlTx) error {
			if s.metadataResidency.requireCoverage {
				if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
					return err
				}
			}
			// persistTeamMetadata wrote inline fields whole from data: each
			// must have been handed complete (a field turned inline since
			// the inputs were read is retried with it complete).
			inline, err := s.inlineMetadataFields(ctx, tx, workspace, fields...)
			if err != nil {
				return err
			}
			for _, name := range inline {
				if !complete[name] {
					return fmt.Errorf("%w: %s became inline", ErrConcurrentWrite, name)
				}
			}
			stamp, err := readMetadataStamp(ctx, tx, workspace)
			if err != nil {
				return err
			}
			if stamp.workspaceID != data.Workspace.ID {
				return ErrResourceAccessChanged
			}
			exact := stored.Stamp() == stamp
			if s.coordinator != nil && !exact {
				return ErrConcurrentWrite
			}
			candidate, err := teamCreateCandidate(ctx, stored, data, upserts, inline, membersChanged, teamMembersChanged)
			if err != nil {
				return err
			}
			publication, err = s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, stamp, exact, true)
			if err != nil {
				candidate.Release()
			}
			return err
		}
		if _, err := s.persistTeamMetadata(ctx, workspace, data, &event, upserts, membersChanged, teamMembersChanged, prepare); err != nil {
			return err
		}
		return publication.Install()
	}
	err := mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if errors.Is(err, ErrNoMutation) {
		err = nil
	}
	if err != nil {
		return err
	}
	if event.ID != "" {
		s.publishMutation(ctx, workspace, event, realtime)
	}
	// MutateWorkspaceWithAggregate records audit entries a callback staged.
	if len(collector.pending) == 0 {
		return nil
	}
	pending := collector.pending
	collector.pending = nil
	workspaceID := collector.workspaceID
	if workspaceID == "" {
		workspaceID, err = s.WorkspaceIDForKey(ctx, workspace)
	}
	if err != nil {
		slog.Error("audit entries not recorded", "workspace", workspace, "event", eventType, "error", err)
		return nil
	}
	if auditErr := s.AppendAuditEntries(context.WithValue(ctx, auditCollectorKey{}, (*auditCollector)(nil)), workspaceID, pending...); auditErr != nil {
		slog.Error("audit entries not recorded", "workspace", workspace, "event", eventType, "error", auditErr)
	}
	return nil
}

type teamCreateInputs struct {
	*issueHotInputs
	// teamMembersEmpty: the stored team members are empty, so the selected
	// (empty) list is the complete one.
	teamMembersEmpty bool
}

// loadTeamCreateInputs selects the callback's inputs from one installed
// image. complete names the fields handed whole: the policy fields, every
// written field the root keeps inline (persisted whole) and, in development,
// the member directories.
func (s *SQLiteStore) loadTeamCreateInputs(ctx context.Context, source *metadataLogicalSource, workspace string, request TeamCreateWrite) (_ *teamCreateInputs, complete map[string]bool, err error) {
	if err := source.Retain(); err != nil {
		return nil, nil, err
	}
	read := &teamCreateInputs{issueHotInputs: &issueHotInputs{store: s, source: source}}
	defer func() {
		if err != nil {
			read.close()
		}
	}()
	complete = map[string]bool{}
	inline, err := s.inlineMetadataFields(ctx, s.db, workspace, teamCreateWrittenFields...)
	if err != nil {
		return nil, nil, err
	}
	names := []string{"workspace", "viewer", "viewerRole", "teams", "teamSettings"}
	names = append(names, inline...)
	if request.Development {
		names = append(names, "users", "members", "teamMembers")
	}
	for _, name := range names {
		if complete[name] {
			continue
		}
		if err := read.completeField(ctx, name); err != nil {
			return nil, nil, err
		}
		complete[name] = true
	}
	data := &read.data
	sources := map[string]bool{}
	// The statuses the callback's statesForTeam may reach: each source team
	// and the ancestors it inherits statuses from, plus the shared ones.
	statesTeams := map[string]bool{"": true}
	for _, id := range request.SourceTeamIDs {
		if id == "" {
			continue
		}
		if err := read.addKey(ctx, sources, id); err != nil {
			return nil, nil, err
		}
		seen := map[string]bool{}
		for current := id; !seen[current]; {
			seen[current] = true
			if err := read.addKey(ctx, statesTeams, current); err != nil {
				return nil, nil, err
			}
			settings := data.TeamSettings[current]
			if !settings.InheritWorkflowStatuses || settings.ParentTeamID == "" {
				break
			}
			current = settings.ParentTeamID
		}
	}
	if !complete["states"] {
		if err := read.selectSlice(ctx, "states", func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "TeamID", statesTeams)
		}); err != nil {
			return nil, nil, err
		}
	}
	if !complete["cycleSettings"] && len(sources) > 0 {
		if err := read.selectMap(ctx, "cycleSettings", sources); err != nil {
			return nil, nil, err
		}
	}
	if !complete["teamMembers"] {
		// Signed in, the callback reads team members only for the viewer's
		// role (the first of their memberships in a team's ancestry) and
		// fills an empty list from the membership tables. Hand it the
		// viewer's memberships in order, behind a placeholder no team
		// matches when only other members' rows exist.
		viewer := data.Viewer.ID
		if actor, ok := actorFromContext(ctx); ok {
			viewer = actor.ID
		}
		index, _, _ := metadataLogicalField("teamMembers")
		node := source.fields[index]
		count := 0
		if err := node.Children(ctx, func(reflect.Value, metadataLogicalNode) error {
			count++
			return nil
		}); err != nil {
			return nil, nil, err
		}
		read.teamMembersEmpty = count == 0
		if count > 0 {
			viewers, err := read.keys(ctx, viewer)
			if err != nil {
				return nil, nil, err
			}
			if err := read.selectSlice(ctx, "teamMembers", func(record metadataLogicalNode) (bool, error) {
				return issueHotMatches(ctx, record, "UserID", viewers)
			}); err != nil {
				return nil, nil, err
			}
			if len(data.TeamMembers) == 0 {
				data.TeamMembers = []domain.TeamMember{{}}
			}
		}
	}
	return read, complete, nil
}

// teamCreateCandidate is the installed post-image: the stored image with the
// written rows (merged as SQL merged them), inline fields and changed member
// root arrays replaced whole.
func teamCreateCandidate(ctx context.Context, stored *metadataLogicalSource, data domain.Bootstrap, upserts []metadataRecordChange, inline []string, membersChanged, teamMembersChanged bool) (*metadataLogicalSource, error) {
	scope := MetadataRecordScope{Fields: slices.Clone(inline)}
	if membersChanged {
		scope.Fields = append(scope.Fields, "members")
	}
	if teamMembersChanged {
		scope.Fields = append(scope.Fields, "teamMembers")
	}
	orders := map[metadataRecordKey]int{}
	rows := []metadataRecordChange{}
	for _, change := range upserts {
		if slices.Contains(inline, change.field) {
			continue
		}
		rows = append(rows, change)
		scope.Write = append(scope.Write, RecordKey{Kind: change.field, ID: change.key})
		orders[metadataRecordKey{field: change.field, key: change.key}] = change.order
	}
	before := domain.Bootstrap{Workspace: data.Workspace, Viewer: data.Viewer, ViewerRole: data.ViewerRole}
	forked, err := stored.Fork(ctx, before, data, scope, stored.Stamp(), orders)
	if err != nil {
		return nil, err
	}
	merged, err := metadataLogicalMergedPostimage(ctx, forked, rows)
	forked.Release()
	if err != nil {
		return nil, err
	}
	candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
	merged.Release()
	return candidate, err
}
