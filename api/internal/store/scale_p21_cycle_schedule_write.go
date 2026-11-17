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

type scopedImageWriteKind uint8

const (
	scopedImageDocumentUpdate scopedImageWriteKind = iota + 1
	scopedImageCycleSchedule
	scopedImageAdvancedSchedule
	scopedImageAgentReply
	scopedImageCommentActionResolve
)

// Only closed document-update, cycle and advanced schedule declarations enter this
// executor. Each owns its literal resolver and native occurrence overlay.
type scopedImageWriteProfile interface {
	kind() scopedImageWriteKind
	resolve(context.Context, *ResourceRead, *metadataLogicalSource) (MutationScope, error)
	inputs(context.Context, *ResourceRead, *metadataLogicalSource) (domain.Bootstrap, error)
	validate(domain.Bootstrap, domain.Bootstrap) error
	image(context.Context, *ResourceRead, *metadataLogicalSource, domain.Bootstrap, domain.Bootstrap) (*metadataLogicalSource, error)
	extraFields() []string
	reserveCallback(context.Context, *ResourceRead, domain.Bootstrap, any) error
}

// scopedImageIssueFamily is a profile whose declared scope also loads the
// status-automation family and may create issues. issueFamily runs inside the
// record transaction, after the direct IDs resolve and before any record
// loads, and returns every issue ID to load.
type scopedImageIssueFamily interface {
	issueFamily(ctx context.Context, tx *sqlTx, workspace string, owner *ResourceRead, rows *issueMutationRows, source *metadataLogicalSource, scope MutationScope, ids []string) ([]string, error)
}

type scopedImageAttempt struct {
	owner   *ResourceRead
	rows    *issueMutationRows
	content *issueChildContentRows
	lookups *issueEditorLookups
	data    domain.Bootstrap
	before  domain.Bootstrap
	once    sync.Once
}

func (r *scopedImageAttempt) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.data, r.before = domain.Bootstrap{}, domain.Bootstrap{}
		if r.lookups != nil {
			r.lookups.close()
		}
		if r.content != nil {
			r.content.Close()
		}
		if r.rows != nil {
			r.rows.Close()
		}
		r.owner.Close()
	})
}

func (r *scopedImageAttempt) value(ctx context.Context, node metadataLogicalNode) (reflect.Value, error) {
	return cycleScheduleNativeValue(ctx, r.owner, node)
}

func scopedImageVisit(ctx context.Context, source *metadataLogicalSource, name string, visit func(int, metadataLogicalNode) error) error {
	index, _, ok := metadataLogicalField(name)
	if !ok || source == nil {
		return ErrMetadataRecordQuery
	}
	return source.fields[index].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		return visit(int(ordinal.Int()), node)
	})
}

func scopedImageRestoreMetadata(ctx context.Context, source, stored *metadataLogicalSource) (*metadataLogicalSource, error) {
	return source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch name {
		case "viewerRole", "members", "teamMembers":
			index, _, _ := metadataLogicalField(name)
			return metadataAggregateStoredNode{value: stored.fields[index]}, true, nil
		}
		return metadataAggregateStoredNode{value: node}, false, nil
	})
}

// Compression receives complete native reference records. A profile's narrow
// callback headers must never become replacement canonical references.
func (r *scopedImageAttempt) persistenceInputs(ctx context.Context, source *metadataLogicalSource) (domain.Bootstrap, error) {
	data := r.data
	for _, name := range []string{"users", "teams", "states", "labels", "viewerRole", "members", "teamMembers"} {
		index, _, _ := metadataLogicalField(name)
		value, err := r.value(ctx, source.fields[index])
		if err != nil {
			return data, err
		}
		reflect.ValueOf(&data).Elem().Field(index).Set(value)
	}
	data.ViewerTeamIDs = nil
	data.Projects = nil
	index, _, _ := metadataLogicalField("projects")
	absent, err := source.fields[index].isNil(ctx)
	if err != nil {
		return data, err
	}
	if !absent {
		data.Projects = []domain.Project{}
	}
	err = scopedImageVisit(ctx, source, "projects", func(_ int, node metadataLogicalNode) error {
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Project]().Size())*2+64); err != nil {
			return err
		}
		value, err := cycleScheduleProjectNode(ctx, r.owner, node, "ID", "Name", "Icon", "Color")
		if err == nil {
			data.Projects = append(data.Projects, value.Interface().(domain.Project))
		}
		return err
	})
	return data, err
}

func (s *SQLiteStore) scopedImageIssueIDs(ctx context.Context, tx *sqlTx, workspace string, scope MutationScope, owner *ResourceRead) ([]string, error) {
	checked := scope
	checked.IssueIDs, checked.Resources, checked.IssueColumns = nil, nil, nil
	if !reflect.DeepEqual(checked, MutationScope{}) {
		return nil, ErrBoundedMetadataCoverage
	}
	if len(scope.IssueIDs) > maxScopedIssues {
		return nil, errScopeTooLarge
	}
	if err := owner.reserve(ctx, uint64(len(scope.IssueIDs))*256+64<<10); err != nil {
		return nil, err
	}
	direct := slices.Clone(scope.IssueIDs)
	selected := []string{}
	for _, column := range scope.IssueColumns {
		if column.Column != "cycle_id" {
			return nil, ErrBoundedMetadataCoverage
		}
		if uint64(len(column.Values)) > (metadataWorkBytes-4096)/64 {
			return nil, ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, uint64(len(column.Values))*64+4096); err != nil {
			return nil, err
		}
		values := slices.DeleteFunc(slices.Clone(column.Values), func(value string) bool { return value == "" })
		for start := 0; start < len(values); start += 500 {
			where, args := bindList(column.Column, values[start:min(start+500, len(values))])
			from := " FROM issue_records WHERE workspace_key=? AND " + where
			args = append([]any{workspace}, args...)
			var count, size int64
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+owner.byteLength("id")+"),0)"+from, args...).Scan(&count, &size); err != nil {
				return nil, err
			}
			if count < 0 || size < 0 {
				return nil, ErrMetadataRecordQuery
			}
			if count > int64(maxScopedIssues-len(selected)) {
				return nil, errScopeTooLarge
			}
			if uint64(size) > (metadataWorkBytes-(64<<10)-uint64(count)*512)/8 {
				return nil, ErrMetadataWorkBudget
			}
			if err := owner.reserve(ctx, uint64(size)*8+uint64(count)*512+64<<10); err != nil {
				return nil, err
			}
			rows, err := tx.QueryContext(ctx, "SELECT id"+from, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return nil, err
				}
				selected = append(selected, id)
				if len(selected) > maxScopedIssues {
					rows.Close()
					return nil, errScopeTooLarge
				}
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return nil, err
			}
		}
	}
	ids := append(direct, selected...)
	if len(ids) > maxScopedIssues {
		return nil, errScopeTooLarge
	}
	return ids, nil
}

func scopedImageEditorDocument(profile scopedImageWriteProfile, data domain.Bootstrap) string {
	if document, ok := profile.(interface{ editorDocument(domain.Bootstrap) string }); ok {
		return document.editorDocument(data)
	}
	return ""
}

func (r *scopedImageAttempt) issueRecords(ctx context.Context, ids []string) (map[string]storedIssueRecord, error) {
	if err := r.owner.reserve(ctx, uint64(len(ids))*256+64<<10); err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(ids))
	pending := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			pending = append(pending, id)
		}
	}
	records := map[string]storedIssueRecord{}
	for start := 0; start < len(pending); start += 1000 {
		batch, err := r.rows.load(ctx, pending[start:min(start+1000, len(pending))], true)
		if err != nil {
			return nil, err
		}
		if len(pending) <= 1000 {
			records = batch
			break
		}
		unit := uint64(reflect.TypeFor[storedIssueRecord]().Size())*2 + 512
		if uint64(len(batch)) > (metadataWorkBytes-(64<<10))/unit {
			return nil, ErrMetadataWorkBudget
		}
		if err := r.owner.reserve(ctx, uint64(len(batch))*unit+64<<10); err != nil {
			return nil, err
		}
		for id, record := range batch {
			records[id] = record
		}
	}
	if len(pending) > 1000 {
		r.owner.mu.Lock()
		r.owner.release = append(r.owner.release, func() { clear(records) })
		r.owner.mu.Unlock()
	}
	// Order, two issue slices and the callback's lookup maps precede its
	// independently admitted deep baseline clone.
	unit := uint64(reflect.TypeFor[domain.Issue]().Size())*8 + 2048
	if uint64(len(records)) > (metadataWorkBytes-(64<<10))/unit {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, uint64(len(records))*unit+64<<10); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *SQLiteStore) mutateBoundedScopedImage(ctx context.Context, workspace, eventType string, payload any, profile scopedImageWriteProfile, mutate func(*domain.Bootstrap) (string, error)) error {
	if s == nil || !s.BoundedMetadataEnabled() || profile == nil || mutate == nil || !metadataOutputOwnerPresent(ctx) ||
		(profile.kind() != scopedImageDocumentUpdate && profile.kind() != scopedImageCycleSchedule && profile.kind() != scopedImageAdvancedSchedule &&
			profile.kind() != scopedImageAgentReply && profile.kind() != scopedImageCommentActionResolve) ||
		UsesIssueRecordMutations(ctx) || issueRenumbering(ctx) {
		return ErrBoundedMetadataCoverage
	}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	ctx = withIssueRecordWriteAdmission(ctx, s)
	ctx, finishTimestamps, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finishTimestamps()
	webhookEnabled := false
	if s.webhookConfigured() {
		webhookEnabled, err = s.boundedLegacyWebhookNeeded(ctx, workspace)
		if err != nil {
			return err
		}
	}
	var committed domain.DomainEvent
	var realtime json.RawMessage
	var changedIssues []string
	written := false
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		traceStart(ctx, "scoped")
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), workspace); err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
		}
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace %q: not found", workspace)
		}
		unbind, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbind()
		attempt := &scopedImageAttempt{owner: &ResourceRead{store: s}}
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
		current, err := metadataLogicalInstalledCandidate(ctx, stored)
		if err != nil {
			return err
		}
		defer current.Release()
		cloned, err := cloneMetadataLogicalSnapshot(ctx, current)
		if err != nil {
			return err
		}
		defer cloned.Release()
		scope, err := profile.resolve(ctx, attempt.owner, cloned)
		if err != nil {
			return err
		}
		family, familyScope := profile.(scopedImageIssueFamily)
		if scope.Resolve != nil || scope.Expand != nil || (scope.StatusAutomation || scope.CreateIssues) && !familyScope {
			return ErrBoundedMetadataCoverage
		}
		createIssues := familyScope && scope.CreateIssues
		if scope.AllIssues {
			return errScopeTooLarge
		}
		workspaceField, _, _ := metadataLogicalField("workspace")
		workspaceValue, err := attempt.value(ctx, cloned.fields[workspaceField])
		if err != nil {
			return err
		}
		attempt.data.Workspace = workspaceValue.Interface().(domain.Workspace)
		actor, hasActor := actorFromContext(ctx)
		actor = mutationInputActor(actor)
		var role string
		var teams []string
		applyRole := false
		if hasActor {
			role, teams, applyRole, err = s.boundedLegacyActorDirectory(ctx, attempt.data.Workspace.ID, actor.ID, attempt.owner)
			if err != nil {
				return err
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
		if baseStamp.workspaceID != attempt.data.Workspace.ID {
			return ErrResourceAccessChanged
		}
		// A family profile resolves its status-automation and created issues itself (below); the direct
		// selection sees only the declared issues.
		direct := scope
		if familyScope {
			direct.StatusAutomation, direct.CreateIssues = false, false
		}
		var ids []string
		if selector, ok := profile.(interface {
			selectIssueIDs(context.Context, *sqlTx, string, MutationScope, *ResourceRead) ([]string, error)
		}); ok {
			ids, err = selector.selectIssueIDs(ctx, tx, workspace, direct, attempt.owner)
		} else {
			ids, err = s.scopedImageIssueIDs(ctx, tx, workspace, direct, attempt.owner)
		}
		if err != nil {
			return err
		}
		attempt.rows = s.newIssueMutationRows(tx, workspace)
		if familyScope {
			// The profile's own in-transaction family and issue sequence,
			// resolved exactly where the scoped writer resolves them.
			if ids, err = family.issueFamily(ctx, tx, workspace, attempt.owner, attempt.rows, cloned, scope, ids); err != nil {
				return err
			}
		}
		records, err := attempt.issueRecords(ctx, ids)
		if err != nil {
			return err
		}
		if filter, ok := profile.(interface {
			filterIssueRecords(MutationScope, map[string]storedIssueRecord)
		}); ok {
			filter.filterIssueRecords(scope, records)
		}
		order := sortedKeys(records)
		sort.SliceStable(order, func(i, j int) bool { return records[order[i]].order < records[order[j]].order })
		loaded, rawBefore := map[string]domain.Issue{}, map[string][]byte{}
		loadedSlice := make([]domain.Issue, 0, len(order))
		attempt.data.Issues = []domain.Issue{}
		for _, id := range order {
			record := records[id]
			loaded[id], rawBefore[id] = record.issue, record.raw
			loadedSlice = append(loadedSlice, record.issue)
			attempt.data.Issues = append(attempt.data.Issues, record.issue)
		}
		keys := scopedResourceKeys(scope.Resources, loaded)
		attempt.content = s.newIssueChildContentRows(tx, workspace)
		content, err := attempt.content.loadMutationScope(ctx, keys)
		if err != nil {
			return err
		}
		attempt.data.Comments, attempt.data.Activities = content.Comments, content.Activities
		attempt.data.Notifications, attempt.data.NotificationDeliveries = content.Notifications, content.NotificationDeliveries
		oldDeliveries := map[string]bool{}
		for _, delivery := range attempt.data.NotificationDeliveries {
			oldDeliveries[delivery.ID] = true
		}
		beforeImage, err := boundedDiscussionEventSource(ctx, cloned, attempt.data)
		if err != nil {
			return err
		}
		defer beforeImage.Release()
		display, err := s.refreshLegacyDisplayImage(ctx, beforeImage)
		if err != nil {
			return err
		}
		defer display.Release()
		var actorPointer *domain.User
		if hasActor {
			actorPointer = &actor
		}
		actorImage, err := s.prepareLegacyActorImage(ctx, display, actorPointer)
		if err != nil {
			return err
		}
		defer actorImage.Close()
		prepared := actorImage.Source()
		// Re-select complete values after both literal display passes and the
		// actor substitution; the pre-callback source is never normalized.
		attempt.data, err = profile.inputs(ctx, attempt.owner, prepared)
		if err != nil {
			return err
		}
		for _, name := range []string{"issues", "comments", "activities", "notifications", "notificationDeliveries"} {
			index, _, _ := metadataLogicalField(name)
			value, err := attempt.value(ctx, prepared.fields[index])
			if err != nil {
				return err
			}
			reflect.ValueOf(&attempt.data).Elem().Field(index).Set(value)
		}
		if applyRole {
			attempt.data.ViewerRole = role
		}
		if hasActor {
			attempt.data.ViewerTeamIDs = teams
		}
		if err := reserveCycleScheduleClone(ctx, attempt.owner, attempt.data); err != nil {
			return err
		}
		attempt.before = cloneCallbackInput(attempt.data)
		documentID := scopedImageEditorDocument(profile, attempt.data)
		inputs := &issueHotInputs{store: s, source: prepared}
		attempt.lookups, err = newIssueEditorLookups(ctx, inputs, tx, workspace, attempt.data.Workspace.ID,
			domain.Issue{DocumentContent: &domain.DocumentContent{ID: documentID}})
		if err != nil {
			return err
		}
		attempt.lookups.install(&attempt.data)
		budget := &boundedDiscussionAttempt{owner: attempt.owner}
		if err := profile.reserveCallback(ctx, attempt.owner, attempt.data, payload); err != nil {
			return err
		}
		traceMark(ctx, "load")
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		aggregate, callbackErr := mutate(&attempt.data)
		attempt.data.FormerMembers, attempt.data.CollaborationUpdates, attempt.data.CollaborationWork = nil, nil, nil
		if err := attempt.lookups.check(); err != nil {
			return err
		}
		if callbackErr != nil {
			return callbackErr
		}
		if aggregate != mutationAggregateID(ctx) {
			return ErrBoundedMetadataCoverage
		}
		if err := profile.validate(attempt.before, attempt.data); err != nil {
			return err
		}
		metadata, err := profile.image(ctx, attempt.owner, prepared, attempt.before, attempt.data)
		if err != nil {
			return err
		}
		defer metadata.Release()
		restored, err := scopedImageRestoreMetadata(ctx, metadata, stored)
		if err != nil {
			return err
		}
		defer restored.Release()
		requested, err := boundedDiscussionEventSource(ctx, restored, attempt.data)
		if err != nil {
			return err
		}
		defer requested.Release()
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		var previous json.RawMessage
		if webhookEnabled {
			publication, err := s.prepareMetadataAggregatePrevious(ctx, metadataAggregateImage{Source: beforeImage}, metadataAggregateImage{Source: requested}, aggregate)
			if err != nil {
				return err
			}
			attempt.owner.release = append(attempt.owner.release, publication.Close)
			previous = publication.PreviousValues
		}
		eventPublication, err := s.prepareMetadataAggregatePublication(ctx, metadataAggregateImage{Source: beforeImage}, metadataAggregateImage{Source: requested}, aggregate, false, payloadRaw, eventType)
		if err != nil {
			return err
		}
		attempt.owner.release = append(attempt.owner.release, eventPublication.Close)
		nextRealtime := eventPublication.RealtimePayload
		event := domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate,
			Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
		persistData, err := attempt.persistenceInputs(ctx, requested)
		if err != nil {
			return err
		}
		writeContext := context.WithValue(ctx, legacyContentWriteBudgetKey{}, s)
		releaseEncoding, err := reserveIssueBatchContentEncoding(ctx, s, persistData)
		if err != nil {
			return err
		}
		defer releaseEncoding()
		if createIssues {
			// The scoped writer reads a created issue's stored row (normally
			// none) inside persistence; the bounded owned reader supplies it.
			created := []string{}
			for _, issue := range attempt.data.Issues {
				if _, found := rawBefore[issue.ID]; !found && !slices.Contains(created, issue.ID) {
					created = append(created, issue.ID)
				}
			}
			if len(created) != 0 {
				existing, err := attempt.rows.load(ctx, created, true)
				if err != nil {
					return err
				}
				for _, id := range created {
					rawBefore[id] = existing[id].raw
				}
			}
		}
		changed, err := s.persistScopedRecords(writeContext, tx, workspace, persistData, loaded, rawBefore, createIssues, keys, oldDeliveries)
		if err != nil {
			return err
		}
		releaseEncoding()
		progress, err := s.prepareScopedIssueReactionProgress(ctx, requested, loadedSlice, attempt.data.Issues)
		if err != nil {
			return fmt.Errorf("scoped image progress: %w", err)
		}
		defer progress.Close()
		persistContext := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, profile.extraFields())
		if err := s.persistLegacyImportInputs(persistContext, tx, workspace, requested); err != nil {
			return fmt.Errorf("scoped image import inputs: %w", err)
		}
		merged, err := s.persistLegacyMetadataImageWithProgress(persistContext, tx, workspace, stored, requested, progress)
		if err != nil {
			return fmt.Errorf("scoped image metadata: %w", err)
		}
		defer merged.Release()
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, baseStamp, stored.Stamp() == baseStamp, true)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		if err := budget.reserveValue(ctx, s.viewer); err != nil {
			return err
		}
		viewerRaw, _ := json.Marshal(s.viewer)
		if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
			if err := budget.reserveValue(ctx, attempt.data.Viewer); err != nil {
				return err
			}
			viewerRaw, _ = json.Marshal(attempt.data.Viewer)
		}
		if err := writeAccountMetadata(ctx, tx, workspace, viewerRaw); err != nil {
			return err
		}
		storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, event.Type, aggregate, previous)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{
			ID: event.ID, Type: eventType, AggregateID: aggregate, Payload: payloadRaw,
			PreviousValues: storedPrevious, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano),
			WorkspaceID: baseStamp.workspaceID, WorkspaceKey: workspace,
		}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		written = true
		if err := publication.Install(); err != nil {
			return err
		}
		changedIssues, committed, realtime = changed, event, nextRealtime
		traceMark(ctx, "commit")
		return nil
	}
	err = mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if written {
		s.dropMetadataCache(ctx, workspace)
	}
	if errors.Is(err, errScopeTooLarge) {
		return s.mutateBoundedScopedImageFull(ctx, workspace, eventType, payload, profile, mutate)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(changedIssues) != 0 {
		s.invalidateIssueRecords(ctx, workspace, changedIssues)
	}
	s.publishMutation(ctx, workspace, committed, realtime)
	return nil
}

func (s *SQLiteStore) scopedImageFullSource(ctx context.Context, workspace string, stored *metadataLogicalSource) (_ *metadataLogicalSource, _ *legacyRecordBase, err error) {
	base, err := s.newLegacyRecordBase(ctx, stored)
	if err != nil {
		return nil, nil, err
	}
	var current *metadataLogicalSource
	defer func() {
		if err != nil {
			base.Close()
			if current != nil {
				current.Release()
			}
		}
	}()
	current, err = cloneMetadataLogicalSnapshot(ctx, stored)
	if err != nil {
		return nil, nil, err
	}
	capture := contextWithLegacyRecordBase(ctx, base)
	for _, family := range [][2]string{
		{"issues", "issue"}, {"", "import"}, {"activities", "activity"},
		{"comments", "comment"}, {"notifications", "notification"},
		{"notificationDeliveries", "delivery"},
	} {
		if family[1] == "import" {
			next, prepareErr := s.prepareLegacyImportImage(capture, workspace, current)
			if prepareErr != nil {
				err = prepareErr
				return nil, nil, err
			}
			current.Release()
			current = next
			continue
		}
		index, _, _ := metadataLogicalField(family[0])
		missing, checkErr := current.fields[index].isNil(capture)
		if checkErr != nil {
			err = checkErr
			return nil, nil, err
		}
		if !missing {
			continue
		}
		var tx *sqlTx
		closeSnapshot := func() {}
		if held, ok := capture.Value(workspaceReadKey{}).(workspaceRead); ok && held.database == s.db {
			tx = held.reader
			if tx == nil {
				err = ErrMetadataRecordQuery
				return nil, nil, err
			}
		} else {
			tx, err = s.db.BeginTx(capture, metadataReadOptions(s.dialect))
			if err != nil {
				return nil, nil, err
			}
			closeSnapshot = func() { _ = tx.Rollback() }
		}
		var next *metadataLogicalSource
		if family[1] == "issue" {
			next, err = s.prepareLegacyIssueImage(capture, tx, workspace, current)
		} else {
			next, err = s.prepareLegacyContentImage(capture, tx, workspace, current, family[1])
		}
		closeSnapshot()
		if err != nil {
			return nil, nil, err
		}
		current.Release()
		current = next
	}
	return current, base, nil
}

// The scoped writer's over-limit path is the ordinary full writer, including
// global external-record repair and resource counts. It is not a larger
// scoped mutation and must not acquire a write transaction before callback.
func (s *SQLiteStore) mutateBoundedScopedImageFull(ctx context.Context, workspace, eventType string, payload any, profile scopedImageWriteProfile, mutate func(*domain.Bootstrap) (string, error)) error {
	webhookEnabled := false
	if s.webhookConfigured() {
		var err error
		webhookEnabled, err = s.boundedLegacyWebhookNeeded(ctx, workspace)
		if err != nil {
			return err
		}
	}
	var committed domain.DomainEvent
	var realtime json.RawMessage
	written := false
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		traceStart(ctx, "full")
		if s.coordinator != nil {
			if err := s.reloadResidentWorkspace(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), workspace); err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
		}
		if _, found := s.storedWorkspace(workspace); !found {
			return fmt.Errorf("workspace %q: not found", workspace)
		}
		unbind, err := s.bindMetadataTimestampInputs(ctx)
		if err != nil {
			return err
		}
		defer unbind()
		attempt := &scopedImageAttempt{owner: &ResourceRead{store: s}}
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
		beforeImage, base, err := s.scopedImageFullSource(ctx, workspace, stored)
		if err != nil {
			return err
		}
		defer beforeImage.Release()
		defer base.Close()
		display, err := s.refreshLegacyDisplayImage(ctx, beforeImage)
		if err != nil {
			return err
		}
		defer display.Release()
		actor, hasActor := actorFromContext(ctx)
		actor = mutationInputActor(actor)
		var actorPointer *domain.User
		if hasActor {
			actorPointer = &actor
		}
		actorImage, err := s.prepareLegacyActorImage(ctx, display, actorPointer)
		if err != nil {
			return err
		}
		defer actorImage.Close()
		prepared := actorImage.Source()
		attempt.data, err = profile.inputs(ctx, attempt.owner, prepared)
		if err != nil {
			return fmt.Errorf("full image inputs: %w", err)
		}
		for _, name := range []string{"issues", "comments", "activities", "notifications", "notificationDeliveries"} {
			index, _, _ := metadataLogicalField(name)
			value, err := attempt.value(ctx, prepared.fields[index])
			if err != nil {
				return fmt.Errorf("full image %s: %w", name, err)
			}
			reflect.ValueOf(&attempt.data).Elem().Field(index).Set(value)
		}
		if hasActor {
			role, teams, applyRole, err := s.boundedLegacyActorDirectory(ctx, attempt.data.Workspace.ID, actor.ID, attempt.owner)
			if err != nil {
				return err
			}
			if applyRole {
				attempt.data.ViewerRole, attempt.data.ViewerTeamIDs = role, teams
			}
		}
		if err := base.FingerprintLoaded(ctx, prepared); err != nil {
			return fmt.Errorf("full image record base: %w", err)
		}
		if err := reserveCycleScheduleClone(ctx, attempt.owner, attempt.data); err != nil {
			return fmt.Errorf("full image clone: %w", err)
		}
		attempt.before = cloneCallbackInput(attempt.data)
		inputs := &issueHotInputs{store: s, source: prepared}
		attempt.lookups, err = newLegacyIssueEditorLookups(ctx, inputs, workspace, attempt.data.Workspace.ID,
			domain.Issue{DocumentContent: &domain.DocumentContent{ID: scopedImageEditorDocument(profile, attempt.data)}})
		if err != nil {
			return err
		}
		attempt.lookups.install(&attempt.data)
		if err := profile.reserveCallback(ctx, attempt.owner, attempt.data, payload); err != nil {
			return fmt.Errorf("full image callback: %w", err)
		}
		traceMark(ctx, "load")
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		aggregate, callbackErr := mutate(&attempt.data)
		attempt.data.FormerMembers, attempt.data.CollaborationUpdates, attempt.data.CollaborationWork = nil, nil, nil
		if err := attempt.lookups.check(); err != nil {
			return err
		}
		if callbackErr != nil {
			return callbackErr
		}
		if aggregate != mutationAggregateID(ctx) {
			return ErrBoundedMetadataCoverage
		}
		if err := profile.validate(attempt.before, attempt.data); err != nil {
			return err
		}
		budget := &boundedDiscussionAttempt{owner: attempt.owner}
		metadata, err := profile.image(ctx, attempt.owner, prepared, attempt.before, attempt.data)
		if err != nil {
			return err
		}
		defer metadata.Release()
		restored, err := metadata.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if name == "viewerRole" {
				index, _, _ := metadataLogicalField(name)
				return metadataAggregateStoredNode{value: stored.fields[index]}, true, nil
			}
			return metadataAggregateStoredNode{value: node}, false, nil
		})
		if err != nil {
			return err
		}
		defer restored.Release()
		callback, err := boundedDiscussionEventSource(ctx, restored, attempt.data)
		if err != nil {
			return err
		}
		defer callback.Release()
		var previous json.RawMessage
		if webhookEnabled {
			publication, err := s.prepareMetadataAggregatePrevious(ctx, metadataAggregateImage{Source: beforeImage}, metadataAggregateImage{Source: callback}, aggregate)
			if err != nil {
				return err
			}
			attempt.owner.release = append(attempt.owner.release, publication.Close)
			previous = publication.PreviousValues
		}
		var progress *legacyProgressImage
		if progressEvent(eventType) {
			progress, err = s.prepareLegacyProgressImage(ctx, callback, time.Now().UTC())
			if err != nil {
				return err
			}
			defer progress.Close()
		}
		counted, err := s.refreshLegacyResourceCountsFromImage(ctx, callback)
		if err != nil {
			return err
		}
		defer counted.Release()
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		eventPublication, err := s.prepareMetadataAggregatePublication(ctx, metadataAggregateImage{Source: beforeImage}, metadataAggregateImage{Source: counted}, aggregate, false, payloadRaw, eventType)
		if err != nil {
			return err
		}
		attempt.owner.release = append(attempt.owner.release, eventPublication.Close)
		nextRealtime := eventPublication.RealtimePayload
		event := domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate,
			Payload: payloadRaw, PreviousValues: previous, CreatedAt: time.Now().UTC()}
		tx, err := s.db.BeginTx(ctx, nil)
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
		if baseStamp.workspaceID == "" || baseStamp.workspaceID != stored.Stamp().workspaceID {
			return ErrResourceAccessChanged
		}
		if err := base.LockForPersistence(ctx, tx, workspace, counted); err != nil {
			return err
		}
		if err := s.persistLegacyImportInputs(ctx, tx, workspace, counted); err != nil {
			return err
		}
		releaseEncoding, err := reserveIssueBatchContentEncoding(ctx, s, attempt.data)
		if err != nil {
			return err
		}
		defer releaseEncoding()
		if err := base.Persist(ctx, tx, workspace, counted); err != nil {
			return err
		}
		releaseEncoding()
		compact, err := counted.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			if name == "importJobs" || name == "migrationJobs" {
				return metadataLogicalCompactCollection{base: metadataAggregateStoredNode{value: node}, field: name}, true, nil
			}
			return metadataAggregateStoredNode{value: node}, false, nil
		})
		if err != nil {
			return err
		}
		defer compact.Release()
		persistContext := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, profile.extraFields())
		var progressPlan legacyMetadataProgress
		if progress != nil {
			progressPlan = progress
		}
		merged, err := s.persistLegacyMetadataImageWithProgress(persistContext, tx, workspace, stored, compact, progressPlan)
		if err != nil {
			return err
		}
		defer merged.Release()
		candidate, err := metadataLogicalInstalledCandidate(ctx, merged)
		if err != nil {
			return err
		}
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, candidate, baseStamp, stored.Stamp() == baseStamp, true)
		if err != nil {
			candidate.Release()
			return err
		}
		defer publication.Close()
		if err := budget.reserveValue(ctx, s.viewer); err != nil {
			return err
		}
		viewerRaw, _ := json.Marshal(s.viewer)
		if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
			if err := budget.reserveValue(ctx, attempt.data.Viewer); err != nil {
				return err
			}
			viewerRaw, _ = json.Marshal(attempt.data.Viewer)
		}
		if err := writeAccountMetadata(ctx, tx, workspace, viewerRaw); err != nil {
			return err
		}
		storedPrevious, err := storedEventPreviousValues(ctx, tx, workspace, eventType, aggregate, previous)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{
			ID: event.ID, Type: eventType, AggregateID: aggregate, Payload: payloadRaw,
			PreviousValues: storedPrevious, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano),
			WorkspaceID: baseStamp.workspaceID, WorkspaceKey: workspace,
		}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		written = true
		if err := publication.Install(); err != nil {
			return err
		}
		committed, realtime = event, nextRealtime
		traceMark(ctx, "commit")
		return nil
	}
	err := mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshConflictedLogicalMetadata(ctx, workspace))
	if written {
		s.dropMetadataCache(ctx, workspace)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	s.publishMutation(ctx, workspace, committed, realtime)
	return nil
}
