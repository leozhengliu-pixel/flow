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

func boundedIssueDiscussionEvent(event string) bool {
	switch event {
	case "comment.created", "comment.updated", "comment.resolved", "comment.unresolved",
		"comment.deleted", "comment.reaction_toggled", "comment.thread_subscription":
		return true
	}
	return false
}

type boundedDiscussionAttempt struct {
	owner   *ResourceRead
	rows    *issueMutationRows
	inputs  *issueHotInputs
	lookups *issueEditorLookups
	data    *domain.Bootstrap
	once    sync.Once
}

func (r *boundedDiscussionAttempt) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.data != nil {
			*r.data = domain.Bootstrap{}
			r.data = nil
		}
		if r.lookups != nil {
			r.lookups.close()
		}
		if r.inputs != nil {
			r.inputs.close()
		}
		if r.rows != nil {
			r.rows.Close()
		}
		r.owner.Close()
	})
}

func (r *boundedDiscussionAttempt) reserveValue(ctx context.Context, value any) error {
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return err
	}
	return r.owner.reserve(ctx, uint64(cost))
}

func (r *boundedDiscussionAttempt) reserveContent(ctx context.Context, data domain.Bootstrap, payload any) error {
	for _, value := range []any{
		data.Issues, data.Comments, data.Activities, data.Notifications, data.NotificationDeliveries, payload,
	} {
		if err := r.reserveValue(ctx, value); err != nil {
			return err
		}
	}
	return nil
}

// Notification callbacks allocate containers before their exact output exists.
// Count every possible directory/subscriber/thread participant conservatively;
// body and encoding work are admitted separately from these fixed records.
func (r *boundedDiscussionAttempt) reserveCallback(ctx context.Context, data domain.Bootstrap, payload any) error {
	if err := r.reserveContent(ctx, data, payload); err != nil {
		return err
	}
	recipients := uint64(len(data.Users)) + uint64(len(data.ThreadSubscriptions))
	var issueBytes uint64
	for _, issue := range data.Issues {
		recipients += uint64(len(issue.SubscriberIDs))
		if uint64(len(issue.ID)) > metadataWorkBytes-issueBytes {
			return ErrMetadataWorkBudget
		}
		issueBytes += uint64(len(issue.ID))
	}
	for _, comments := range data.Comments {
		recipients += uint64(len(comments))
	}
	fixed := uint64(reflect.TypeFor[domain.Notification]().Size())*2 +
		uint64(reflect.TypeFor[domain.NotificationDelivery]().Size())*8 + 8192
	if issueBytes > (metadataWorkBytes-fixed)/32 {
		return ErrMetadataWorkBudget
	}
	fixed += issueBytes * 32
	if recipients > (metadataWorkBytes-(64<<10))/fixed {
		return ErrMetadataWorkBudget
	}
	return r.owner.reserve(ctx, recipients*fixed+64<<10)
}

// mutateBoundedIssueDiscussion keeps the literal discussion callback and SQL
// transaction while selecting its installed metadata and owning all preimages.
// Every attempted callback remains charged through the containing response:
// captured result pointers cannot be proven reset when an attempt retries.
func (s *SQLiteStore) mutateBoundedIssueDiscussion(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	discussion, discussionOnly := ctx.Value(discussionMutationKey{}).(discussionMutation)
	ids, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	if !s.BoundedMetadataEnabled() || !discussionOnly || !boundedIssueDiscussionEvent(eventType) ||
		len(ids) != 1 || ids[0] == "" || mutate == nil || issueRenumbering(ctx) {
		return fmt.Errorf("%w: unsupported bounded issue discussion %s", ErrBoundedMetadataCoverage, eventType)
	}
	if !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	if eventType == "comment.thread_subscription" {
		ctx = context.WithValue(ctx, discussionThreadTargetKey{}, ids[0])
	}
	if eventType == "comment.thread_subscription" && ctx.Err() != nil {
		selected := workspace
		if selected == "" {
			s.mu.RLock()
			selected = s.lastWorkspaceKey
			s.mu.RUnlock()
		}
		if _, found := s.storedWorkspace(selected); !found {
			return fmt.Errorf("workspace not found")
		}
		// The original scoped writer resolves the actor's live membership
		// before its first context-returning database operation.
		if _, actor := actorFromContext(ctx); actor {
			return ErrAuthForbidden
		}
	}
	var err error
	ctx, finishTimestampInputs, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finishTimestampInputs()
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
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
		attempt := &boundedDiscussionAttempt{owner: &ResourceRead{store: s}}
		transferred := false
		defer func() {
			if !transferred {
				attempt.Close()
			}
		}()
		if err := attempt.owner.reserve(ctx, 128<<10); err != nil {
			return err
		}
		source, err := s.retainMetadataLogicalSource(ctx, workspace)
		if err != nil {
			return err
		}
		attempt.owner.source = source
		var data domain.Bootstrap
		attempt.data = &data
		workspaceField, _, _ := metadataLogicalField("workspace")
		value, err := attempt.owner.projectNode(ctx, source.fields[workspaceField], "ID", "URLKey")
		if err != nil {
			return err
		}
		data.Workspace = value.Interface().(domain.Workspace)
		viewerField, _, _ := metadataLogicalField("viewer")
		value, err = attempt.owner.node(ctx, source.fields[viewerField], false)
		if err != nil {
			return err
		}
		data.Viewer = value.Interface().(domain.User)
		roleField, _, _ := metadataLogicalField("viewerRole")
		value, err = attempt.owner.node(ctx, source.fields[roleField], false)
		if err != nil {
			return err
		}
		data.ViewerRole = value.String()
		nativeViewer, nativeRole := data.Viewer, data.ViewerRole
		if actor, ok := actorFromContext(ctx); ok {
			if err := attempt.reserveValue(ctx, actor); err != nil {
				return err
			}
			if actor.Via != nil {
				if err := attempt.reserveValue(ctx, actor.Via); err != nil {
					return err
				}
			}
			data.Viewer = mutationInputActor(actor)
			access := &DocumentRead{owner: attempt.owner}
			if err := access.documentProjectionRows(ctx, []string{"role", "status"},
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
		stored := make(map[string][]byte, len(records))
		for id, record := range records {
			data.Issues = append(data.Issues, record.issue)
			stored[id] = record.raw
		}
		data.Comments, err = attempt.rows.loadDiscussionComment(ctx, ids[0], discussion.CommentID)
		if err != nil {
			return err
		}
		data.Activities = map[string][]domain.ActivityEvent{}
		notifications, err := attempt.rows.activeDiscussionNotifications(ctx, ids[0])
		if err != nil {
			return err
		}
		data.Notifications = []domain.Notification{}
		for _, items := range notifications {
			if err := attempt.owner.reserve(ctx, uint64(len(items))*uint64(reflect.TypeFor[domain.Notification]().Size())*2+4096); err != nil {
				return err
			}
			data.Notifications = append(data.Notifications, items...)
		}
		data.NotificationDeliveries, err = attempt.rows.issueNotificationDeliveries(ctx, data.Notifications)
		if err != nil {
			return err
		}
		if err := attempt.owner.reserve(ctx, uint64(len(data.NotificationDeliveries))*512+4096); err != nil {
			return err
		}
		oldDeliveries := make(map[string]bool, len(data.NotificationDeliveries))
		for _, delivery := range data.NotificationDeliveries {
			oldDeliveries[delivery.ID] = true
		}
		attempt.inputs, err = s.loadIssueDiscussionInputs(ctx, workspace, &data)
		if err != nil {
			return err
		}
		metadataBefore := domain.Bootstrap{Workspace: data.Workspace, Viewer: nativeViewer, ViewerRole: nativeRole, ThreadSubscriptions: data.ThreadSubscriptions}
		if err := attempt.reserveValue(ctx, metadataBefore); err != nil {
			return err
		}
		metadataBefore = cloneBootstrap(metadataBefore)
		refreshDisplayReferences(&data)
		refreshIssueReferences(&data)
		traceMark(ctx, "load")
		var target domain.Issue
		if len(data.Issues) != 0 {
			target = data.Issues[0]
		}
		attempt.lookups, err = newIssueEditorLookups(ctx, attempt.inputs, tx, workspace, data.Workspace.ID, target)
		if err != nil {
			return err
		}
		data.FormerMembers = attempt.lookups.formerMembers
		data.CollaborationUpdates = attempt.lookups.collaborationUpdates
		if err := attempt.reserveCallback(ctx, data, payload); err != nil {
			return err
		}
		metadataInput := collectionMetadata(data)
		inputCost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(metadataInput), 0)
		if err != nil {
			return err
		}
		if inputCost > metadataWorkBytes/2 {
			return ErrMetadataWorkBudget
		}
		if err := attempt.owner.reserve(ctx, inputCost*2); err != nil {
			return err
		}
		metadataInput = cloneBootstrap(metadataInput)
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		aggregate, callbackErr := mutate(&data)
		data.FormerMembers, data.CollaborationUpdates = nil, nil
		if err := attempt.lookups.check(); err != nil {
			return err
		}
		traceMark(ctx, "mutate")
		if callbackErr != nil {
			return callbackErr
		}
		if err := validateDiscussionThreadEdits(metadataInput.ThreadSubscriptions, data.ThreadSubscriptions, ids[0]); err != nil {
			return err
		}
		for _, field := range changedMetadataFields(metadataInput, collectionMetadata(data)) {
			if field.name != "threadSubscriptions" {
				return fmt.Errorf("%w: discussion callback changed metadata %s", ErrBoundedMetadataCoverage, field.name)
			}
		}
		if len(data.Issues) != len(records) {
			return fmt.Errorf("%w: discussion callback changed its issue scope", ErrBoundedMetadataCoverage)
		}
		for _, issue := range data.Issues {
			if _, found := stored[issue.ID]; !found {
				return fmt.Errorf("%w: discussion callback replaced its issue", ErrBoundedMetadataCoverage)
			}
		}
		if err := attempt.reserveContent(ctx, data, payload); err != nil {
			return err
		}
		if _, err := s.writeIssueRecordBatch(ctx, tx, workspace, data.Issues, data, stored); err != nil {
			return err
		}
		if err := persistDiscussion(ctx, tx, workspace, ids[0], discussion.CommentID, data); err != nil {
			return err
		}
		for _, delivery := range data.NotificationDeliveries {
			delete(oldDeliveries, delivery.ID)
			if err := writeContentRecord(ctx, tx, workspace, "delivery", "", delivery); err != nil {
				return err
			}
		}
		for id := range oldDeliveries {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`, workspace, id); err != nil {
				return err
			}
		}
		data.Viewer, data.ViewerRole = nativeViewer, nativeRole
		metadataNext := domain.Bootstrap{Workspace: data.Workspace, Viewer: nativeViewer, ViewerRole: nativeRole, ThreadSubscriptions: data.ThreadSubscriptions}
		scope, err := boundedDiscussionMetadataScope(metadataBefore, metadataNext)
		if err != nil && discussionNeedsOrdinalScope(metadataBefore, metadataNext, ids[0], scope, err) {
			ctx = context.WithValue(ctx, discussionThreadTargetKey{}, ids[0])
			scope, err = MetadataRecordScope{}, nil
		}
		if err != nil {
			return err
		}
		publication, err := s.persistDiscussionMetadata(ctx, tx, workspace, attempt.inputs.source, metadataBefore, metadataNext, scope,
			baseStamp, attempt.inputs.source.Stamp() == baseStamp)
		if err != nil {
			return err
		}
		defer publication.Close()
		if err := attempt.reserveValue(ctx, payload); err != nil {
			return err
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		eventSource, err := boundedDiscussionEventSource(ctx, publication.candidate, data)
		if err != nil {
			return err
		}
		defer eventSource.Release()
		plan, err := planMetadataAggregatePayload(ctx, metadataAggregateImage{Source: attempt.inputs.source},
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
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: aggregate,
			Payload: payloadRaw, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: data.Workspace.ID, WorkspaceKey: workspace}); err != nil {
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

func boundedDiscussionMetadataScope(before, next domain.Bootstrap) (MetadataRecordScope, error) {
	changes, _, err := scaleP16RecordChanges(before, next)
	if err != nil {
		return MetadataRecordScope{}, err
	}
	scope := MetadataRecordScope{}
	for _, change := range changes {
		if change.field != "threadSubscriptions" {
			return scope, fmt.Errorf("%w: unsupported discussion metadata edit", ErrBoundedMetadataCoverage)
		}
		scope.Write = append(scope.Write, RecordKey{Kind: change.field, ID: change.key})
	}
	add := func(id string) {
		key := RecordKey{Kind: "threadSubscriptions", ID: id}
		if !slices.Contains(scope.Write, key) {
			scope.Write = append(scope.Write, key)
		}
	}
	if len(next.ThreadSubscriptions) < len(before.ThreadSubscriptions) {
		kept := 0
		for _, subscription := range before.ThreadSubscriptions {
			if kept < len(next.ThreadSubscriptions) && reflect.DeepEqual(subscription, next.ThreadSubscriptions[kept]) {
				kept++
			} else {
				add(subscription.ID)
			}
		}
	} else {
		for index, subscription := range next.ThreadSubscriptions {
			if index >= len(before.ThreadSubscriptions) || !reflect.DeepEqual(subscription, before.ThreadSubscriptions[index]) {
				add(subscription.ID)
			}
		}
	}
	if err := validateMetadataRecordScope(scope); err != nil {
		return scope, err
	}
	return scope, nil
}

func validateDiscussionThreadEdits(before, next []domain.ThreadSubscription, issueID string) error {
	if len(next) < len(before) {
		kept := 0
		for _, subscription := range before {
			if kept < len(next) && reflect.DeepEqual(subscription, next[kept]) {
				kept++
			}
		}
		if kept == len(next) {
			return nil
		}
		return fmt.Errorf("%w: thread deletion changed a surviving subscription", ErrBoundedMetadataCoverage)
	}
	for index, subscription := range next {
		if index < len(before) && reflect.DeepEqual(subscription, before[index]) {
			continue
		}
		if subscription.IssueID != issueID || subscription.State != "subscribed" && subscription.State != "muted" {
			return fmt.Errorf("%w: unsupported thread subscription", ErrBoundedMetadataCoverage)
		}
		if index < len(before) {
			original := before[index]
			original.State, original.UpdatedAt = subscription.State, subscription.UpdatedAt
			if !reflect.DeepEqual(original, subscription) {
				return fmt.Errorf("%w: thread edit changed subscription identity", ErrBoundedMetadataCoverage)
			}
		}
	}
	return nil
}

func boundedDiscussionEventSource(ctx context.Context, source *metadataLogicalSource, data domain.Bootstrap) (*metadataLogicalSource, error) {
	value := reflect.ValueOf(data)
	return source.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		switch name {
		case "issues", "comments", "activities", "notifications", "notificationDeliveries":
			index, _, _ := metadataLogicalField(name)
			return metadataAggregateNativeNode{value.Field(index)}, true, nil
		default:
			return metadataAggregateStoredNode{original}, false, nil
		}
	})
}
