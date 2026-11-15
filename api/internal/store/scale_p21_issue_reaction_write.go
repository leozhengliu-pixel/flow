package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type boundedIssueReactionAttempt struct {
	*boundedDiscussionAttempt
	content *issueChildContentRows
	once    sync.Once
}

func (r *boundedIssueReactionAttempt) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.boundedDiscussionAttempt.Close()
		if r.content != nil {
			r.content.Close()
			r.content = nil
		}
	})
}

// This adapter retains mutateIssueScope's canonical issue/content preimage.
// It intentionally has neither a discussion marker nor the full writer's
// resource-count, PreviousValues, account-metadata or last-workspace effects.
func (s *SQLiteStore) mutateBoundedIssueReaction(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	ids, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	if s == nil || !s.BoundedMetadataEnabled() || !UsesIssueRecordMutations(ctx) || IsIssueDiscussionMutation(ctx) ||
		eventType != "issue.reaction_toggled" || len(ids) != 1 || ids[0] == "" || mutate == nil || issueRenumbering(ctx) ||
		!metadataOutputOwnerPresent(ctx) {
		return fmt.Errorf("%w: unsupported bounded scoped issue reaction", ErrBoundedMetadataCoverage)
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	if ctx.Err() != nil {
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace not found")
		}
		// The literal scoped writer resolves live membership before its
		// first context-returning SQL operation.
		if _, actor := actorFromContext(ctx); actor {
			return ErrAuthForbidden
		}
		return ctx.Err()
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	var err error
	ctx, finishTimestampInputs, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finishTimestampInputs()
	var event domain.DomainEvent
	var realtime json.RawMessage
	metadataWritten := false
	traceStart(ctx, "issue")
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace not found")
		}
		unbindTimestampInputs, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbindTimestampInputs()
		attempt := &boundedIssueReactionAttempt{boundedDiscussionAttempt: &boundedDiscussionAttempt{owner: &ResourceRead{store: s}}}
		transferred := false
		defer func() {
			if !transferred {
				attempt.Close()
			}
		}()
		if err := attempt.owner.reserve(ctx, 128<<10); err != nil {
			return err
		}
		stored, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		attempt.owner.source = stored
		metadataBefore, err := metadataLogicalInstalledCandidate(ctx, stored)
		if err != nil {
			return err
		}
		defer metadataBefore.Release()
		cloned, err := cloneMetadataLogicalSnapshot(ctx, metadataBefore)
		if err != nil {
			return err
		}
		defer cloned.Release()
		var data domain.Bootstrap
		attempt.data = &data
		for _, name := range []string{"workspace", "viewer", "viewerRole"} {
			index, _, _ := metadataLogicalField(name)
			value, err := attempt.owner.node(ctx, cloned.fields[index], false)
			if err != nil {
				return err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
		if actor, found := actorFromContext(ctx); found {
			if err := attempt.reserveValue(ctx, actor); err != nil {
				return err
			}
			if actor.Via != nil {
				if err := attempt.reserveValue(ctx, actor.Via); err != nil {
					return err
				}
			}
			data.Viewer = mutationInputActor(actor)
			directory := &DocumentRead{owner: attempt.owner}
			if err := directory.documentProjectionRows(ctx, []string{"role", "status"},
				`FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, data.Workspace.ID, actor.ID); err != nil {
				return err
			}
			role, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, actor.ID)
			if err != nil || status != "active" {
				return ErrAuthForbidden
			}
			data.ViewerRole = role
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				return err
			}
		}
		baseStamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			return err
		}
		if data.Workspace.ID != baseStamp.workspaceID {
			return ErrResourceAccessChanged
		}
		attempt.rows = s.newIssueMutationRows(tx, workspace)
		records, err := attempt.rows.load(ctx, ids, true)
		if err != nil {
			return err
		}
		if err := attempt.owner.reserve(ctx, uint64(len(records))*4096+64<<10); err != nil {
			return err
		}
		data.Issues = make([]domain.Issue, 0, len(records))
		rawBefore := make(map[string][]byte, len(records))
		loaded := make([]domain.Issue, 0, len(records))
		keys := make([]string, 0, len(records))
		for id, record := range records {
			data.Issues = append(data.Issues, record.issue)
			loaded = append(loaded, record.issue)
			rawBefore[id] = record.raw
			keys = append(keys, id)
		}
		data.Comments = map[string][]domain.Comment{}
		data.Activities = map[string][]domain.ActivityEvent{}
		data.Notifications = []domain.Notification{}
		data.NotificationDeliveries = []domain.NotificationDelivery{}
		attempt.content = s.newIssueChildContentRows(tx, workspace)
		if len(keys) != 0 {
			content, err := attempt.content.load(ctx, keys[0])
			if err != nil {
				return err
			}
			data.Comments, data.Activities = content.Comments, content.Activities
			data.Notifications, data.NotificationDeliveries = content.Notifications, content.NotificationDeliveries
		}
		selected, err := boundedDiscussionEventSource(ctx, cloned, data)
		if err != nil {
			return err
		}
		defer selected.Release()
		display, err := s.refreshLegacyDisplayImage(ctx, selected)
		if err != nil {
			return err
		}
		defer display.Release()
		for _, name := range []string{"issues", "comments", "activities", "notifications", "notificationDeliveries"} {
			index, _, _ := metadataLogicalField(name)
			value, err := attempt.owner.node(ctx, display.fields[index], false)
			if err != nil {
				return err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
		attempt.inputs, err = s.loadIssueDiscussionInputs(ctx, workspace, &data)
		if err != nil {
			return err
		}
		var target domain.Issue
		if len(data.Issues) > 0 {
			target = data.Issues[0]
		}
		attempt.lookups, err = newIssueEditorLookups(ctx, attempt.inputs, tx, workspace, data.Workspace.ID, target)
		if err != nil {
			return err
		}
		if err := attempt.reserveContent(ctx, data, payload); err != nil {
			return err
		}
		callbackCost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(data), 0)
		if err != nil {
			return err
		}
		if callbackCost > (metadataWorkBytes-(64<<10))/3 {
			return ErrMetadataWorkBudget
		}
		if err := attempt.owner.reserve(ctx, callbackCost*3+64<<10); err != nil {
			return err
		}
		beforeCallback := cloneBootstrap(data)
		if data.Viewer.Via != nil {
			beforeCallback.Viewer.Via = cloneSnapshotValue(reflect.ValueOf(data.Viewer.Via)).Interface().(*domain.ActorBot)
		}
		data.FormerMembers = attempt.lookups.formerMembers
		data.CollaborationUpdates = attempt.lookups.collaborationUpdates
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		traceMark(ctx, "load")
		aggregate, callbackErr := mutate(&data)
		data.FormerMembers, data.CollaborationUpdates = nil, nil
		if err := attempt.lookups.check(); err != nil {
			return err
		}
		traceMark(ctx, "mutate")
		if callbackErr != nil {
			return callbackErr
		}
		if err := validateScopedIssueReaction(beforeCallback, data); err != nil {
			return err
		}
		if err := attempt.reserveContent(ctx, data, payload); err != nil {
			return err
		}
		if _, err := s.writeIssueRecordBatch(ctx, tx, workspace, data.Issues, data, rawBefore); err != nil {
			return err
		}
		writeCtx := context.WithValue(ctx, legacyContentWriteBudgetKey{}, s)
		if err := syncContentRecords(writeCtx, tx, workspace, "comment", data.Comments, data, keys); err != nil {
			return err
		}
		if err := syncContentRecords(writeCtx, tx, workspace, "activity", data.Activities, data, keys); err != nil {
			return err
		}
		if err := syncContentRecords(writeCtx, tx, workspace, "notification", notificationRecords(data.Notifications), data, keys); err != nil {
			return err
		}
		for _, delivery := range data.NotificationDeliveries {
			if err := writeContentRecord(writeCtx, tx, workspace, "delivery", "", delivery); err != nil {
				return err
			}
		}
		requested, err := boundedDiscussionEventSource(ctx, display, data)
		if err != nil {
			return err
		}
		defer requested.Release()
		restored, err := requested.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if name == "viewer" || name == "viewerRole" {
				index, _, _ := metadataLogicalField(name)
				return metadataAggregateStoredNode{stored.fields[index]}, true, nil
			}
			return metadataAggregateStoredNode{node}, false, nil
		})
		if err != nil {
			return err
		}
		defer restored.Release()
		for _, name := range []string{"viewer", "viewerRole"} {
			index, _, _ := metadataLogicalField(name)
			value, err := attempt.owner.node(ctx, stored.fields[index], false)
			if err != nil {
				return err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
		progress, err := s.prepareScopedIssueReactionProgress(ctx, restored, loaded, data.Issues)
		if err != nil {
			return err
		}
		defer progress.Close()
		merged, err := s.persistLegacyMetadataImageWithProgress(ctx, tx, workspace, stored, restored, progress)
		if err != nil {
			return err
		}
		defer merged.Release()
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		eventSource, err := boundedDiscussionEventSource(ctx, merged, data)
		if err != nil {
			return err
		}
		defer eventSource.Release()
		plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: metadataBefore},
			metadataAggregateImage{Source: eventSource}, aggregate, false, payloadRaw)
		if err != nil {
			return err
		}
		if err := attempt.owner.reserve(ctx, uint64(plan.RequiredBytes)); err != nil {
			return err
		}
		_, entity, err := plan.Values(ctx)
		if err != nil {
			return err
		}
		realtime = enrichRealtimePayload(payloadRaw, entity, eventType)
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, baseStamp, stored.Stamp() == baseStamp, false)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{
			ID: event.ID, Type: event.Type, AggregateID: aggregate, Payload: payloadRaw,
			CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: data.Workspace.ID, WorkspaceKey: workspace,
		}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if err := publication.Install(); err != nil {
			return err
		}
		traceMark(ctx, "persist")
		metadataWritten = tx.metadataWritten
		return nil
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if metadataWritten {
		s.dropMetadataCache(ctx, workspace)
	}
	if err != nil {
		if errors.Is(err, ErrNoMutation) {
			return nil
		}
		return err
	}
	s.publishMutation(ctx, workspace, event, realtime)
	return nil
}

func validateScopedIssueReaction(before, after domain.Bootstrap) error {
	if len(before.Issues) != len(after.Issues) {
		return fmt.Errorf("%w: scoped reaction changed its issue scope", ErrBoundedMetadataCoverage)
	}
	for index, original := range before.Issues {
		next := after.Issues[index]
		original.Version, original.UpdatedAt, original.Reactions = next.Version, next.UpdatedAt, next.Reactions
		if !reflect.DeepEqual(original, next) {
			return fmt.Errorf("%w: scoped reaction changed an unsupported issue field", ErrBoundedMetadataCoverage)
		}
	}
	if len(changedMetadataFields(collectionMetadata(before), collectionMetadata(after))) != 0 ||
		!reflect.DeepEqual(before.Comments, after.Comments) || !reflect.DeepEqual(before.Activities, after.Activities) ||
		!reflect.DeepEqual(before.Notifications, after.Notifications) || !reflect.DeepEqual(before.NotificationDeliveries, after.NotificationDeliveries) {
		return fmt.Errorf("%w: scoped reaction changed unrelated metadata or content", ErrBoundedMetadataCoverage)
	}
	return nil
}

type scopedIssueReactionProgress struct {
	store         *SQLiteStore
	inputs        *issueHotInputs
	before, after []domain.Issue
	projects      []string
}

func (*scopedIssueReactionProgress) legacyImportCompaction() bool { return true }

func (p *scopedIssueReactionProgress) Close() {
	if p != nil && p.inputs != nil {
		p.inputs.close()
		p.inputs = nil
	}
}

func scopedIssueReactionTeams(ctx context.Context, inputs *issueHotInputs, before, after []domain.Issue) error {
	teams, err := inputs.keys(ctx)
	if err != nil {
		return err
	}
	for _, issues := range [][]domain.Issue{before, after} {
		for _, issue := range issues {
			if err := inputs.addKey(ctx, teams, issue.Team.ID); err != nil {
				return err
			}
		}
	}
	if inputs.data.TeamSettings == nil {
		inputs.data.TeamSettings = map[string]domain.TeamSettings{}
	}
	return inputs.selectMap(ctx, "teamSettings", teams, "EstimateCountUnestimated")
}

func (s *SQLiteStore) prepareScopedIssueReactionProgress(ctx context.Context, source *metadataLogicalSource, before, after []domain.Issue) (_ *scopedIssueReactionProgress, err error) {
	if err := source.Retain(); err != nil {
		return nil, err
	}
	p := &scopedIssueReactionProgress{store: s, inputs: &issueHotInputs{store: s, source: source}, before: before, after: after}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	if err := p.inputs.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	projects, err := p.inputs.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, issues := range [][]domain.Issue{before, after} {
		for _, issue := range issues {
			if issue.Project != nil {
				if err := p.inputs.addKey(ctx, projects, issue.Project.ID); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := p.inputs.selectSlice(ctx, "projects", func(node metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, node, "ID", projects)
	}, legacyProgressProjectFields...); err != nil {
		return nil, err
	}
	if err := scopedIssueReactionTeams(ctx, p.inputs, before, after); err != nil {
		return nil, err
	}
	// This preserves stale nonempty histories on equal contributions, while
	// an empty history still enters the literal scoped progress machinery.
	p.projects = issueProgressProjects(p.inputs.data, before, after)
	return p, ctx.Err()
}

func (p *scopedIssueReactionProgress) HasProject(ctx context.Context, id string) (bool, error) {
	if p == nil || p.inputs == nil {
		return false, errMetadataLogicalClosed
	}
	return slices.Contains(p.projects, id), ctx.Err()
}

func (p *scopedIssueReactionProgress) ForEachProject(ctx context.Context, visit func(string) error) error {
	if p == nil || p.inputs == nil {
		return errMetadataLogicalClosed
	}
	for _, id := range p.projects {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func (p *scopedIssueReactionProgress) Rebuild(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, now time.Time) (_ *metadataLogicalSource, err error) {
	if p == nil || p.inputs == nil || tx == nil || tx.database != p.store.db || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(p.projects) == 0 {
		if err := source.Retain(); err != nil {
			return nil, err
		}
		return source, nil
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	inputs := &issueHotInputs{store: p.store, source: source}
	defer inputs.close()
	if err := inputs.reserve(ctx, 128<<10); err != nil {
		return nil, err
	}
	keys, err := inputs.keys(ctx, p.projects...)
	if err != nil {
		return nil, err
	}
	var ordinals []int
	index, _, _ := metadataLogicalField("projects")
	if err := source.fields[index].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		matched, err := issueHotMatches(ctx, node, "ID", keys)
		if err != nil || !matched {
			return err
		}
		cost, err := issueHotProjectionCost(ctx, node, legacyProgressProjectFields)
		if err != nil {
			return err
		}
		if err := inputs.reserve(ctx, cost+512); err != nil {
			return err
		}
		value, err := issueHotProjection(ctx, node, legacyProgressProjectFields, cost)
		if err != nil {
			return err
		}
		inputs.data.Projects = append(inputs.data.Projects, value.Interface().(domain.Project))
		ordinals = append(ordinals, int(ordinal.Int()))
		return nil
	}); err != nil {
		return nil, err
	}
	if err := scopedIssueReactionTeams(ctx, inputs, p.before, p.after); err != nil {
		return nil, err
	}
	data := inputs.data
	before := slices.Clone(data.Projects)
	work := &issueProgressWork{inputs: inputs, states: map[string]domain.WorkflowState{}, seen: map[string]bool{}}
	work.prepareTeams = func(ctx context.Context, issues []progressIssue) error {
		teams, err := inputs.keys(ctx)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			if err := inputs.addKey(ctx, teams, issue.teamID); err != nil {
				return err
			}
		}
		return inputs.selectMap(ctx, "teamSettings", teams, "EstimateCountUnestimated")
	}
	ctx = context.WithValue(ctx, issueProgressWorkKey{}, work)
	if _, err := refreshIssueProjectProgress(ctx, tx, workspace, &data, p.before, p.after, now); err != nil {
		return nil, err
	}
	values, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, err
	}
	defer values.Close()
	for _, storage := range source.stores {
		if err := values.inheritLocations(storage); err != nil {
			return nil, err
		}
	}
	patches := map[int]metadataLogicalNode{}
	for index, project := range data.Projects {
		if reflect.DeepEqual(before[index], project) {
			continue
		}
		patch, err := values.writeValue(reflect.ValueOf(project), 0, false)
		if err != nil {
			return nil, err
		}
		patches[ordinals[index]] = patch
	}
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if name == "projects" && len(patches) > 0 {
			return scopedIssueReactionProjectCollection{base: node, patches: patches}, true, nil
		}
		return metadataAggregateStoredNode{node}, false, nil
	})
}

type scopedIssueReactionProjectCollection struct {
	base    metadataLogicalNode
	patches map[int]metadataLogicalNode
}

func (n scopedIssueReactionProjectCollection) nodeType() reflect.Type           { return n.base.typ }
func (n scopedIssueReactionProjectCollection) logicalBase() metadataLogicalNode { return n.base }
func (n scopedIssueReactionProjectCollection) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n scopedIssueReactionProjectCollection) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.base.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if patch, found := n.patches[int(key.Int())]; found {
			return visit(key, legacyProgressProject{base: node, patch: patch})
		}
		return visit(key, metadataAggregateStoredNode{node})
	})
}
