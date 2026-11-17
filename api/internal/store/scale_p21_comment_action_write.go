package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// CommentActionProposal declares the two discussion writes of comment action
// generation on one issue comment: the claim (comment.actions_started, no
// Proposed) and the proposal append (comment.actions_updated) of exactly the
// inert proposals the callback may add, in order.
type CommentActionProposal struct {
	IssueID, CommentID string
	Proposed           []domain.CommentAction
}

type commentActionProposalKey struct{}

// ContextWithCommentActionProposal routes the bounded claim and append, both
// on WithIssueDiscussionMutation(issue, comment), to their closed writer.
func ContextWithCommentActionProposal(ctx context.Context, proposal CommentActionProposal) context.Context {
	return context.WithValue(ctx, commentActionProposalKey{}, proposal)
}

func commentActionProposalFromContext(ctx context.Context) (CommentActionProposal, bool) {
	proposal, ok := ctx.Value(commentActionProposalKey{}).(CommentActionProposal)
	return proposal, ok && proposal.IssueID != "" && proposal.CommentID != ""
}

func commentActionProposalEvent(ctx context.Context, event string) bool {
	proposal, ok := commentActionProposalFromContext(ctx)
	return ok && (event == "comment.actions_started" && len(proposal.Proposed) == 0 || event == "comment.actions_updated" && len(proposal.Proposed) != 0)
}

// loadCommentActionProposalInputs completes the discussion inputs with the
// closure of commentActionsAllowed and validateCommentAction: the viewer's
// settings and team memberships, workspace settings, complete teams and team
// settings, the workflow states and labels of the issue's team and every
// ancestor (plus workspace states), complete records of proposed assignees
// and every comment action of the comment (the global duplicate predicate).
func (s *SQLiteStore) loadCommentActionProposalInputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, data *domain.Bootstrap, proposal CommentActionProposal) error {
	if err := source.Retain(); err != nil {
		return err
	}
	inputs := &issueHotInputs{store: s, source: source}
	owner.mu.Lock()
	owner.release = append(owner.release, inputs.close)
	owner.mu.Unlock()
	if err := inputs.reserve(ctx, 64<<10); err != nil {
		return err
	}
	for _, name := range []string{"teams", "teamSettings", "workspaceSettings"} {
		index, _, _ := metadataLogicalField(name)
		value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
		if err != nil {
			return err
		}
		reflect.ValueOf(data).Elem().Field(index).Set(value)
	}
	viewer, err := inputs.keys(ctx, data.Viewer.ID)
	if err != nil {
		return err
	}
	if err := inputs.selectMap(ctx, "userSettings", viewer); err != nil {
		return err
	}
	data.UserSettings = inputs.data.UserSettings
	if err := inputs.selectSlice(ctx, "teamMembers", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "UserID", viewer)
	}); err != nil {
		return err
	}
	data.TeamMembers = inputs.data.TeamMembers
	comment, err := inputs.keys(ctx, proposal.CommentID)
	if err != nil {
		return err
	}
	if err := inputs.discussionCollectionShape(ctx, "commentActions"); err != nil {
		return err
	}
	if err := inputs.selectSlice(ctx, "commentActions", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "CommentID", comment)
	}); err != nil {
		return err
	}
	data.CommentActions = inputs.data.CommentActions
	if len(proposal.Proposed) == 0 || len(data.Issues) == 0 {
		return ctx.Err()
	}
	issue := data.Issues[0]
	teams, err := inputs.keys(ctx, issue.Team.ID, "")
	if err != nil {
		return err
	}
	for parent := data.TeamSettings[issue.Team.ID].ParentTeamID; parent != "" && !teams[parent]; parent = data.TeamSettings[parent].ParentTeamID {
		if err := inputs.addKey(ctx, teams, parent); err != nil {
			return err
		}
	}
	states, err := inputs.keys(ctx, issue.State.ID)
	if err != nil {
		return err
	}
	labels, err := inputs.keys(ctx)
	if err != nil {
		return err
	}
	for _, label := range issue.Labels {
		if err := inputs.addKey(ctx, labels, label.ID); err != nil {
			return err
		}
	}
	assignees, err := inputs.keys(ctx)
	if err != nil {
		return err
	}
	for _, action := range proposal.Proposed {
		if action.Arguments.StateID != nil {
			if err := inputs.addKey(ctx, states, *action.Arguments.StateID); err != nil {
				return err
			}
		}
		if action.Arguments.LabelIDs != nil {
			for _, id := range *action.Arguments.LabelIDs {
				if err := inputs.addKey(ctx, labels, id); err != nil {
					return err
				}
			}
		}
		if action.Arguments.AssigneeID != nil && *action.Arguments.AssigneeID != "" {
			if err := inputs.addKey(ctx, assignees, *action.Arguments.AssigneeID); err != nil {
				return err
			}
		}
	}
	// Workflow states: the referenced IDs (reference refresh and the exact
	// match) and every state of the team, its ancestors and the workspace
	// (the team-specific fallback), in source order.
	selectedStates := []domain.WorkflowState{}
	stateIndex, _, _ := metadataLogicalField("states")
	if absent, err := source.fields[stateIndex].isNil(ctx); err != nil {
		return err
	} else if absent {
		selectedStates = nil
	}
	if err := source.fields[stateIndex].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		byID, err := issueHotMatches(ctx, record, "ID", states)
		if err != nil {
			return err
		}
		byTeam, err := issueHotMatches(ctx, record, "TeamID", teams)
		if err != nil || !byID && !byTeam {
			return err
		}
		value, err := cycleScheduleNativeValue(ctx, owner, record)
		if err == nil {
			selectedStates = append(selectedStates, value.Interface().(domain.WorkflowState))
		}
		return err
	}); err != nil {
		return err
	}
	data.States = selectedStates
	selectedLabels, err := agentReplySelectNodes(ctx, owner, source, "labels", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", labels)
	})
	if err != nil {
		return err
	}
	data.Labels = selectedLabels.Interface().([]domain.IssueLabel)
	if len(assignees) != 0 {
		full := &issueHotInputs{store: s, source: source}
		if err := source.Retain(); err != nil {
			return err
		}
		owner.mu.Lock()
		owner.release = append(owner.release, full.close)
		owner.mu.Unlock()
		if err := full.selectSlice(ctx, "users", func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "ID", assignees)
		}); err != nil {
			return err
		}
		selected := 0
		for index, user := range data.Users {
			if !assignees[user.ID] {
				continue
			}
			if selected >= len(full.data.Users) || full.data.Users[selected].ID != user.ID {
				return ErrBoundedMetadataCoverage
			}
			data.Users[index] = full.data.Users[selected]
			selected++
		}
		if selected != len(full.data.Users) {
			return ErrBoundedMetadataCoverage
		}
	}
	return ctx.Err()
}

// validateCommentActionProposal accepts the claim's single ActionsGeneratedAt
// stamp, or the append of declared proposals after every selected action.
func validateCommentActionProposal(before, after domain.Bootstrap, proposal CommentActionProposal, appending bool) error {
	refuse := func(what string) error {
		return fmt.Errorf("%w: comment action callback changed %s", ErrBoundedMetadataCoverage, what)
	}
	if !reflect.DeepEqual(before.Issues, after.Issues) || !reflect.DeepEqual(before.Activities, after.Activities) ||
		!reflect.DeepEqual(before.Notifications, after.Notifications) || !reflect.DeepEqual(before.NotificationDeliveries, after.NotificationDeliveries) {
		return refuse("issue content")
	}
	if appending {
		if !reflect.DeepEqual(before.Comments, after.Comments) {
			return refuse("comments")
		}
	} else {
		if len(before.Comments) != len(after.Comments) {
			return refuse("comments")
		}
		for key, comments := range before.Comments {
			next, found := after.Comments[key]
			if !found || len(next) != len(comments) {
				return refuse("comments")
			}
			for index, comment := range comments {
				if reflect.DeepEqual(comment, next[index]) {
					continue
				}
				stamped := comment
				stamped.ActionsGeneratedAt = next[index].ActionsGeneratedAt
				if key != proposal.IssueID || comment.ID != proposal.CommentID || comment.ActionsGeneratedAt != nil ||
					next[index].ActionsGeneratedAt == nil || !reflect.DeepEqual(stamped, next[index]) {
					return refuse("a comment")
				}
			}
		}
	}
	left, right := collectionMetadata(before), collectionMetadata(after)
	leftActions, rightActions := left.CommentActions, right.CommentActions
	left.CommentActions, right.CommentActions = nil, nil
	if len(changedMetadataFields(left, right)) != 0 || !reflect.DeepEqual(before.Viewer, after.Viewer) ||
		before.ViewerRole != after.ViewerRole || !reflect.DeepEqual(before.ViewerTeamIDs, after.ViewerTeamIDs) {
		return refuse("metadata")
	}
	if !appending {
		if !reflect.DeepEqual(leftActions, rightActions) {
			return refuse("comment actions")
		}
		return nil
	}
	if len(rightActions) < len(leftActions) {
		return refuse("existing comment actions")
	}
	for index, action := range leftActions {
		if !reflect.DeepEqual(action, rightActions[index]) {
			return refuse("existing comment actions")
		}
	}
	next := 0
	for _, action := range rightActions[len(leftActions):] {
		for next < len(proposal.Proposed) && !reflect.DeepEqual(proposal.Proposed[next], action) {
			next++
		}
		if next == len(proposal.Proposed) {
			return refuse("an undeclared comment action")
		}
		next++
	}
	return nil
}

type commentActionAppend struct {
	base, shape metadataLogicalNode
	actions     []domain.CommentAction
}

func (n commentActionAppend) nodeType() reflect.Type           { return n.base.typ }
func (n commentActionAppend) logicalBase() metadataLogicalNode { return n.shape }
func (n commentActionAppend) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n commentActionAppend) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	count := 0
	if err := n.base.Children(ctx, func(index reflect.Value, node metadataLogicalNode) error {
		count = int(index.Int()) + 1
		return visit(index, metadataAggregateStoredNode{node})
	}); err != nil {
		return err
	}
	for offset, action := range n.actions {
		if err := visit(reflect.ValueOf(count+offset), legacyDiscussionNativeRecord{reflect.ValueOf(action)}); err != nil {
			return err
		}
	}
	return nil
}

// mutateBoundedCommentActionProposal is mutateIssueScope's discussion path for
// the comment action claim and proposal append: the issue record, the thread
// of the comment, its active discussion notifications and deliveries, and the
// closed callback inputs. The claim persists the stamped comment; the append
// adds the accepted proposals after every native comment action.
func (s *SQLiteStore) mutateBoundedCommentActionProposal(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	proposal, declared := commentActionProposalFromContext(ctx)
	discussion, discussionOnly := ctx.Value(discussionMutationKey{}).(discussionMutation)
	ids, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	appending := eventType == "comment.actions_updated"
	if !s.BoundedMetadataEnabled() || !declared || !discussionOnly || !commentActionProposalEvent(ctx, eventType) ||
		discussion.CommentID != proposal.CommentID || len(ids) != 1 || ids[0] != proposal.IssueID || mutate == nil || issueRenumbering(ctx) {
		return fmt.Errorf("%w: unsupported bounded comment action write %s", ErrBoundedMetadataCoverage, eventType)
	}
	if !metadataOutputOwnerPresent(ctx) {
		return ErrBoundedMetadataCoverage
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
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
		value, err := attempt.owner.node(ctx, source.fields[workspaceField], false)
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
		// commentActionsAllowed inherits grants from the ancestors, as the
		// generation read does; they follow the target as read-only inputs.
		if len(data.Issues) != 0 && issueAncestorInputs(ctx) {
			chain, err := loadIssueAncestorChain(data.Issues[0], func(id string) (storedIssueRecord, bool, error) {
				records, err := attempt.rows.load(ctx, []string{id}, false)
				record, found := records[id]
				return record, found, err
			})
			if err != nil {
				return err
			}
			if err := attempt.owner.reserve(ctx, uint64(len(chain))*4096); err != nil {
				return err
			}
			for _, record := range chain {
				data.Issues = append(data.Issues, record.issue)
				stored[record.issue.ID] = record.raw
			}
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
		if err := s.loadCommentActionProposalInputs(ctx, attempt.owner, attempt.inputs.source, &data, proposal); err != nil {
			return err
		}
		metadataBefore := domain.Bootstrap{Workspace: data.Workspace, Viewer: nativeViewer, ViewerRole: nativeRole}
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
		if err := attempt.reserveContent(ctx, data, payload); err != nil {
			return err
		}
		inputCost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(data), 0)
		if err != nil {
			return err
		}
		if inputCost > metadataWorkBytes/3 {
			return ErrMetadataWorkBudget
		}
		if err := attempt.owner.reserve(ctx, inputCost*3); err != nil {
			return err
		}
		for _, action := range proposal.Proposed {
			if err := attempt.reserveValue(ctx, action); err != nil {
				return err
			}
		}
		beforeCallback := cloneCallbackInput(data)
		data.FormerMembers = attempt.lookups.formerMembers
		data.CollaborationUpdates = attempt.lookups.collaborationUpdates
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
		if aggregate != proposal.IssueID {
			return fmt.Errorf("%w: comment action callback replaced its aggregate", ErrBoundedMetadataCoverage)
		}
		if err := validateCommentActionProposal(beforeCallback, data, proposal, appending); err != nil {
			return err
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
		var candidate *metadataLogicalSource
		var install func() error
		if appending {
			added := data.CommentActions[len(beforeCallback.CommentActions):]
			frames, err := newMetadataLogicalBuilder(ctx)
			if err != nil {
				return err
			}
			defer frames.Close()
			actionsIndex, _, _ := metadataLogicalField("commentActions")
			shape, err := legacyDiscussionShape(frames, source.fields[actionsIndex].typ, false)
			if err != nil {
				return err
			}
			target, err := source.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
				if name == "commentActions" {
					return commentActionAppend{base: node, shape: shape, actions: added}, true, nil
				}
				return metadataAggregateStoredNode{node}, false, nil
			})
			if err != nil {
				return err
			}
			defer target.Release()
			persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"commentActions"})
			merged, err := s.persistLegacyMetadataImage(persistCtx, tx, workspace, source, target)
			if err != nil {
				return err
			}
			defer merged.Release()
			next, err := metadataLogicalInstalledCandidate(ctx, merged)
			if err != nil {
				return err
			}
			publication, err := s.prepareLegacyResidentPublication(ctx, tx, workspace, next, baseStamp, source.Stamp() == baseStamp, false)
			if err != nil {
				next.Release()
				return err
			}
			defer publication.Close()
			candidate, install = merged, publication.Install
		} else {
			metadataNext := domain.Bootstrap{Workspace: data.Workspace, Viewer: nativeViewer, ViewerRole: nativeRole}
			publication, err := s.persistDiscussionMetadata(ctx, tx, workspace, attempt.inputs.source, metadataBefore, metadataNext,
				MetadataRecordScope{}, baseStamp, attempt.inputs.source.Stamp() == baseStamp)
			if err != nil {
				return err
			}
			defer publication.Close()
			candidate, install = publication.candidate, publication.Install
		}
		if err := attempt.reserveValue(ctx, payload); err != nil {
			return err
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: payloadRaw, CreatedAt: time.Now().UTC()}
		eventSource, err := boundedDiscussionEventSource(ctx, candidate, data)
		if err != nil {
			return err
		}
		defer eventSource.Release()
		eventPublication, err := s.prepareMetadataAggregatePublication(ctx, metadataAggregateImage{Source: attempt.inputs.source},
			metadataAggregateImage{Source: eventSource}, aggregate, false, payloadRaw, eventType)
		if err != nil {
			return err
		}
		attempt.owner.release = append(attempt.owner.release, eventPublication.Close)
		realtime = eventPublication.RealtimePayload
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: event.Type, AggregateID: aggregate,
			Payload: payloadRaw, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: data.Workspace.ID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if err := install(); err != nil {
			return err
		}
		traceMark(ctx, "persist")
		metadataWritten = tx.metadataWritten || appending
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
