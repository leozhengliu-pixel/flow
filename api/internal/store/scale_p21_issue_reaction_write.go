package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type boundedIssueReactionAttempt struct {
	*boundedDiscussionAttempt
	content          *issueChildContentRows
	attachmentInputs *issueHotInputs
	once             sync.Once
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
		if r.attachmentInputs != nil {
			r.attachmentInputs.close()
			r.attachmentInputs = nil
		}
	})
}

// This adapter retains mutateIssueScope's canonical issue/content preimage.
// It intentionally has neither a discussion marker nor the full writer's
// resource-count, PreviousValues, account-metadata or last-workspace effects.
func (s *SQLiteStore) mutateBoundedScopedIssuePoint(ctx context.Context, workspace, eventType string, payload any, profile scopedIssuePointProfile, mutate func(*domain.Bootstrap) (string, error)) error {
	ids, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	timing := profile == scopedIssueRecurrenceTimingProfile
	validScope := UsesIssueRecordMutations(ctx) && len(ids) == 1 && ids[0] != ""
	if timing {
		ids, validScope = recurrenceTimingScope(ctx)
	}
	if s == nil || !s.BoundedMetadataEnabled() || !validScope || IsIssueDiscussionMutation(ctx) ||
		!scopedIssuePointEvent(ctx, eventType, profile) || mutate == nil || issueRenumbering(ctx) ||
		!metadataOutputOwnerPresent(ctx) {
		return fmt.Errorf("%w: unsupported bounded scoped issue reaction", ErrBoundedMetadataCoverage)
	}
	attachment := profile == scopedIssueAttachmentProfile
	relation := profile == scopedIssueRelationProfile
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
		if _, actor := actorFromContext(ctx); actor && !timing {
			return ErrAuthForbidden
		}
		return ctx.Err()
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	var err error
	webhookEnabled := false
	if timing && s.webhookConfigured() {
		webhookEnabled, err = s.boundedLegacyWebhookNeeded(ctx, workspace)
		if err != nil {
			return err
		}
	}
	if timing {
		ctx, _ = s.WithMetadataTimestampInputs(ctx)
		var release func()
		ctx, release, err = s.tryAdmitAdditionalMetadataWork(ctx, metadataTimestampInputBytes)
		if err != nil {
			return err
		}
		defer release()
	}
	ctx, finishTimestampInputs, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finishTimestampInputs()
	var event domain.DomainEvent
	var realtime json.RawMessage
	metadataWritten := false
	committed := false
	var changedIssues []string
	traceStart(ctx, "issue")
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		if timing && s.coordinator != nil {
			reload := context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
			if err := s.reloadResidentWorkspace(reload, workspace); err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
		}
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
			if timing {
				role, teams, applyRole, err := s.boundedLegacyActorDirectory(ctx, data.Workspace.ID, actor.ID, attempt.owner)
				if err != nil {
					return err
				}
				if applyRole {
					data.ViewerRole = role
				}
				data.ViewerTeamIDs = teams
			} else {
				role, status, err := s.WorkspaceRole(ctx, data.Workspace.ID, actor.ID)
				if err != nil || status != "active" {
					return ErrAuthForbidden
				}
				data.ViewerRole = role
			}
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
		if relation {
			if err := loadScopedIssueRelationRows(ctx, attempt.rows, records, payload); err != nil {
				return err
			}
		}
		if err := attempt.owner.reserve(ctx, uint64(len(records))*4096+64<<10); err != nil {
			return err
		}
		data.Issues = make([]domain.Issue, 0, len(records))
		rawBefore := make(map[string][]byte, len(records))
		loaded := make([]domain.Issue, 0, len(records))
		keys := make([]string, 0, len(records))
		order := make([]string, 0, len(records))
		for id := range records {
			order = append(order, id)
		}
		if timing {
			slices.Sort(order)
			sort.SliceStable(order, func(i, j int) bool { return records[order[i]].order < records[order[j]].order })
		}
		for _, id := range order {
			record := records[id]
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
		if len(keys) != 0 && !relation {
			var content domain.Bootstrap
			if timing {
				content, err = attempt.content.loadMutationScope(ctx, keys)
			} else {
				content, err = attempt.content.load(ctx, keys[0])
			}
			if err != nil {
				return err
			}
			data.Comments, data.Activities = content.Comments, content.Activities
			data.Notifications, data.NotificationDeliveries = content.Notifications, content.NotificationDeliveries
		}
		beforeFrameData := data
		if timing {
			beforeFrameData, err = recurrenceTimingFrameData(ctx, data)
			if err != nil {
				return err
			}
		}
		selected, err := boundedDiscussionEventSource(ctx, cloned, beforeFrameData)
		if err != nil {
			return err
		}
		defer selected.Release()
		display, err := s.refreshLegacyDisplayImage(ctx, selected)
		if err != nil {
			return err
		}
		defer display.Release()
		if timing {
			var actor *domain.User
			if value, present := actorFromContext(ctx); present {
				actor = &value
			}
			prepared, err := s.prepareLegacyActorImage(ctx, display, actor)
			if err != nil {
				return err
			}
			defer prepared.Close()
			display = prepared.Source()
		}
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
		var replyAudience map[string]bool
		if profile == scopedIssueAgentReplyProfile {
			replyAudience, err = s.loadAgentReplyIssueInputs(ctx, attempt.owner, attempt.inputs.source, &data)
			if err != nil {
				return err
			}
		}
		if timing {
			teams, err := attempt.inputs.keys(ctx)
			if err != nil {
				return err
			}
			for _, issue := range data.Issues {
				if err := attempt.inputs.addKey(ctx, teams, issue.Team.ID); err != nil {
					return err
				}
			}
			if err := attempt.inputs.selectMap(ctx, "teamSettings", teams, "TeamID", "Timezone", "ResolvedSummaries"); err != nil {
				return err
			}
			data.TeamSettings = attempt.inputs.data.TeamSettings
		}
		var target domain.Issue
		if len(data.Issues) > 0 {
			target = data.Issues[0]
		}
		if attachment && eventType == "attachment.created" && !issueAttachmentUpsertMutation(ctx) {
			attempt.attachmentInputs, err = s.loadIssueAttachmentInputs(ctx, workspace, &data, target)
			if err != nil {
				return err
			}
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
		if timing {
			err = validateScopedIssueRecurrenceTiming(beforeCallback, data)
		} else if relation {
			err = validateScopedIssueRelation(beforeCallback, data)
		} else if attachment {
			err = validateScopedIssueAttachment(beforeCallback, data, eventType, issueAttachmentUpsertMutation(ctx))
		} else if profile == scopedIssueAgentReplyProfile {
			err = validateAgentReplyNotifications(beforeCallback, data, replyAudience)
		} else {
			err = validateScopedIssueReaction(beforeCallback, data)
		}
		if err != nil {
			return err
		}
		frameData := data
		if timing {
			frameData, err = recurrenceTimingFrameData(ctx, data)
			if err != nil {
				return err
			}
		}
		if err := attempt.reserveContent(ctx, data, payload); err != nil {
			return err
		}
		written, err := s.writeIssueRecordBatch(ctx, tx, workspace, data.Issues, data, rawBefore)
		if err != nil {
			return err
		}
		if timing {
			changedIssues = written
		}
		writeCtx := context.WithValue(ctx, legacyContentWriteBudgetKey{}, s)
		if relation {
			if err := writeScopedIssueRelationContent(writeCtx, tx, workspace, data); err != nil {
				return err
			}
		} else {
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
		}
		requested, err := boundedDiscussionEventSource(ctx, display, frameData)
		if err != nil {
			return err
		}
		defer requested.Release()
		restored, err := requested.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if name == "viewer" && !timing || name == "viewerRole" {
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
			if timing && name == "viewer" {
				continue
			}
			index, _, _ := metadataLogicalField(name)
			value, err := attempt.owner.node(ctx, stored.fields[index], false)
			if err != nil {
				return err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
		var previous json.RawMessage
		if webhookEnabled {
			eventPublication, err := s.prepareMetadataAggregatePrevious(ctx, metadataAggregateImage{Source: selected}, metadataAggregateImage{Source: restored}, aggregate)
			if err != nil {
				return err
			}
			attempt.owner.release = append(attempt.owner.release, eventPublication.Close)
			previous = eventPublication.PreviousValues
		}
		var progress *scopedIssueReactionProgress
		if progressEvent(eventType) {
			progress, err = s.prepareScopedIssueReactionProgress(ctx, restored, loaded, data.Issues)
			if err != nil {
				return err
			}
			defer progress.Close()
		}
		var merged *metadataLogicalSource
		if progress != nil {
			merged, err = s.persistLegacyMetadataImageWithProgress(ctx, tx, workspace, stored, restored, progress)
		} else {
			merged, err = s.persistLegacyMetadataImage(ctx, tx, workspace, stored, restored)
		}
		if err != nil {
			return err
		}
		defer merged.Release()
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
		eventSource, err := boundedDiscussionEventSource(ctx, merged, frameData)
		if err != nil {
			return err
		}
		defer eventSource.Release()
		eventPublication, err := s.prepareMetadataAggregatePublication(ctx, metadataAggregateImage{Source: metadataBefore},
			metadataAggregateImage{Source: eventSource}, aggregate, false, payloadRaw, eventType)
		if err != nil {
			return err
		}
		attempt.owner.release = append(attempt.owner.release, eventPublication.Close)
		realtime = eventPublication.RealtimePayload
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, baseStamp, stored.Stamp() == baseStamp, timing)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		if timing {
			if err := attempt.reserveValue(ctx, s.viewer); err != nil {
				return err
			}
			viewerRaw, _ := json.Marshal(s.viewer)
			if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
				index, _, _ := metadataLogicalField("viewer")
				viewer, err := attempt.owner.node(ctx, merged.fields[index], false)
				if err != nil {
					return err
				}
				if err := attempt.reserveValue(ctx, viewer.Interface()); err != nil {
					return err
				}
				viewerRaw, _ = json.Marshal(viewer.Interface())
			}
			if err := writeAccountMetadata(ctx, tx, workspace, viewerRaw); err != nil {
				return err
			}
			previous, err = storedEventPreviousValues(ctx, tx, workspace, event.Type, aggregate, previous)
			if err != nil {
				return err
			}
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{
			ID: event.ID, Type: event.Type, AggregateID: aggregate, Payload: payloadRaw, PreviousValues: previous,
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
		committed = true
		return nil
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if timing && s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if metadataWritten || timing && committed {
		s.dropMetadataCache(ctx, workspace)
	}
	if err != nil {
		if errors.Is(err, ErrNoMutation) {
			return nil
		}
		return err
	}
	if timing && len(changedIssues) != 0 {
		s.invalidateIssueRecords(ctx, workspace, changedIssues)
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
