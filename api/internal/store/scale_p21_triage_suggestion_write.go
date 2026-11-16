package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// ErrIssueSuggestionWriteRefused reports an input the bounded issue suggestion
// writer does not reproduce, such as a team move with renumbering. It wraps
// ErrBoundedMetadataCoverage; nothing is written.
var ErrIssueSuggestionWriteRefused = fmt.Errorf("%w: issue suggestion writer refused the input", ErrBoundedMetadataCoverage)

// IssueSuggestionWrite declares the installed records an issue suggestion
// callback may apply beyond the scoped issues' own references and the targets
// of their stored suggestions: user, project and label IDs the caller already
// chose. Complete exposes every user and label and every project's triage
// fields, for callbacks that score the whole workspace.
type IssueSuggestionWrite struct {
	Users, Projects, Labels []string
	Complete                bool
}

type issueSuggestionWriteKey struct{}

// ContextWithIssueSuggestionWrite routes a bounded issue-record mutation whose
// callback changes the scoped issues and their issueSuggestions rows to the
// dedicated writer. The callback, event and payload are unchanged.
func ContextWithIssueSuggestionWrite(ctx context.Context, input IssueSuggestionWrite) context.Context {
	return context.WithValue(ctx, issueSuggestionWriteKey{}, input)
}

func issueSuggestionWriteFromContext(ctx context.Context) (IssueSuggestionWrite, bool) {
	input, ok := ctx.Value(issueSuggestionWriteKey{}).(IssueSuggestionWrite)
	return input, ok
}

type legacyMetadataExtraFieldsKey struct{}

func legacyMetadataExtraFields(ctx context.Context) []string {
	names, _ := ctx.Value(legacyMetadataExtraFieldsKey{}).([]string)
	return names
}

// triageProjectFields are every project field a suggestion callback reads:
// references and progress summaries, existence, archive state and scoring.
var triageProjectFields = []string{"ID", "Name", "Icon", "Color", "ArchivedAt", "Lead", "TeamIDs", "Summary", "Description"}

type issueSuggestionInputs struct {
	*issueHotInputs
	ordinals []int
}

func (r *issueHotInputs) completeField(ctx context.Context, name string) error {
	index, _, found := metadataLogicalField(name)
	if !found {
		return ErrMetadataRecordQuery
	}
	node := r.source.fields[index]
	if node.cost > (metadataWorkBytes-4096)/8 {
		return ErrMetadataWorkBudget
	}
	if err := r.reserve(ctx, node.cost*8+4096); err != nil {
		return err
	}
	value, err := node.Value(ctx, int(node.cost))
	if err != nil {
		return err
	}
	reflect.ValueOf(&r.data).Elem().Field(index).Set(value)
	return nil
}

// loadIssueSuggestionInputs selects, from one installed image, the policy
// fields, the scoped issues' suggestions (with their source ordinals) and the
// users, labels, projects and states those issues and suggestions reference.
// Users keep the complete ordered directory as ID/Active headers with complete
// values in place for referenced users, so first-match lookups are unchanged.
func (s *SQLiteStore) loadIssueSuggestionInputs(ctx context.Context, source *metadataLogicalSource, data *domain.Bootstrap, ids []string, input IssueSuggestionWrite) (_ *issueSuggestionInputs, err error) {
	if err := source.Retain(); err != nil {
		return nil, err
	}
	read := &issueSuggestionInputs{issueHotInputs: &issueHotInputs{store: s, source: source}}
	defer func() {
		if err != nil {
			read.close()
		}
	}()
	if err := read.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	for _, name := range []string{"teams", "teamSettings", "workspaceSettings", "teamParents"} {
		if err := read.completeField(ctx, name); err != nil {
			return nil, err
		}
	}
	scope, err := read.keys(ctx, ids...)
	if err != nil {
		return nil, err
	}
	index, _, _ := metadataLogicalField("issueSuggestions")
	node := source.fields[index]
	absent, err := node.isNil(ctx)
	if err != nil {
		return nil, err
	}
	if !absent {
		read.data.IssueSuggestions = []domain.IssueSuggestion{}
	}
	if err := node.Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
		matched, err := issueHotMatches(ctx, record, "IssueID", scope)
		if err != nil || !matched {
			return err
		}
		if record.cost > (metadataWorkBytes-512)/8 {
			return ErrMetadataWorkBudget
		}
		if err := read.reserve(ctx, record.cost*8+512); err != nil {
			return err
		}
		value, err := record.Value(ctx, int(record.cost))
		if err == nil {
			read.data.IssueSuggestions = append(read.data.IssueSuggestions, value.Interface().(domain.IssueSuggestion))
			read.ordinals = append(read.ordinals, int(ordinal.Int()))
		}
		return err
	}); err != nil {
		return nil, err
	}
	users, err := read.keys(ctx, input.Users...)
	if err != nil {
		return nil, err
	}
	labels, err := read.keys(ctx, input.Labels...)
	if err != nil {
		return nil, err
	}
	projects, err := read.keys(ctx, input.Projects...)
	if err != nil {
		return nil, err
	}
	states, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, issue := range data.Issues {
		if err := read.addKey(ctx, states, issue.State.ID); err != nil {
			return nil, err
		}
		for _, user := range []*domain.User{&issue.Creator, issue.Assignee, issue.Delegate} {
			if user != nil {
				if err := read.addKey(ctx, users, user.ID); err != nil {
					return nil, err
				}
			}
		}
		for _, label := range issue.Labels {
			if err := read.addKey(ctx, labels, label.ID); err != nil {
				return nil, err
			}
		}
		if issue.Project != nil {
			if err := read.addKey(ctx, projects, issue.Project.ID); err != nil {
				return nil, err
			}
		}
	}
	for _, item := range read.data.IssueSuggestions {
		for _, pair := range []struct {
			keys map[string]bool
			id   string
		}{{users, item.SuggestedUserID}, {labels, item.SuggestedLabelID}, {projects, item.SuggestedProjectID}} {
			if pair.id != "" {
				if err := read.addKey(ctx, pair.keys, pair.id); err != nil {
					return nil, err
				}
			}
		}
	}
	all := func(metadataLogicalNode) (bool, error) { return true, nil }
	match := func(keys map[string]bool) func(metadataLogicalNode) (bool, error) {
		if input.Complete {
			return all
		}
		return func(record metadataLogicalNode) (bool, error) { return issueHotMatches(ctx, record, "ID", keys) }
	}
	for _, name := range []string{"users", "labels", "projects", "states"} {
		if err := read.discussionCollectionShape(ctx, name); err != nil {
			return nil, err
		}
	}
	if input.Complete {
		if err := read.selectSlice(ctx, "users", all); err != nil {
			return nil, err
		}
	} else {
		if err := read.selectSlice(ctx, "users", all, "ID", "Active"); err != nil {
			return nil, err
		}
		headers := read.data.Users
		if headers != nil {
			read.data.Users = []domain.User{}
		}
		if err := read.selectSlice(ctx, "users", match(users)); err != nil {
			return nil, err
		}
		full, selected := read.data.Users, 0
		for index, user := range headers {
			if !users[user.ID] {
				continue
			}
			if selected >= len(full) || full[selected].ID != user.ID {
				return nil, ErrBoundedMetadataCoverage
			}
			headers[index] = full[selected]
			selected++
		}
		if selected != len(full) {
			return nil, ErrBoundedMetadataCoverage
		}
		read.data.Users = headers
	}
	if err := read.selectSlice(ctx, "labels", match(labels)); err != nil {
		return nil, err
	}
	if err := read.selectSlice(ctx, "projects", match(projects), triageProjectFields...); err != nil {
		return nil, err
	}
	if err := read.selectSlice(ctx, "states", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", states)
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input2 := reflect.ValueOf(read.data)
	target := reflect.ValueOf(data).Elem()
	for _, name := range []string{"teams", "teamSettings", "workspaceSettings", "teamParents", "issueSuggestions", "users", "labels", "projects", "states"} {
		index, _, _ := metadataLogicalField(name)
		target.Field(index).Set(input2.Field(index))
	}
	return read, nil
}

// issueSuggestionPatch maps the callback's selected suggestion rows back onto
// the complete collection with the legacy list semantics: rows of issues whose
// suggestions were regenerated (SuggestionsGeneratedAt changed) are removed
// and the callback's rows for them are appended in callback order; other
// selected rows keep their positions and may only be edited in place or
// deleted. Any other shape is refused.
func issueSuggestionPatch(before, after []domain.IssueSuggestion, ordinals []int, beforeIssues, afterIssues []domain.Issue, scope map[string]bool) (dropped map[int]bool, edits map[int]domain.IssueSuggestion, appended []domain.IssueSuggestion, err error) {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: suggestion callback %s", ErrIssueSuggestionWriteRefused, reason)
	}
	regenerated := map[string]bool{}
	previous := map[string]domain.Issue{}
	for _, issue := range beforeIssues {
		previous[issue.ID] = issue
	}
	for _, issue := range afterIssues {
		old := previous[issue.ID]
		if (old.SuggestionsGeneratedAt == nil) != (issue.SuggestionsGeneratedAt == nil) ||
			old.SuggestionsGeneratedAt != nil && !old.SuggestionsGeneratedAt.Equal(*issue.SuggestionsGeneratedAt) {
			regenerated[issue.ID] = true
		}
	}
	dropped, edits = map[int]bool{}, map[int]domain.IssueSuggestion{}
	var kept []int
	var keptBefore []domain.IssueSuggestion
	for index, item := range before {
		if regenerated[item.IssueID] {
			dropped[ordinals[index]] = true
			continue
		}
		kept = append(kept, ordinals[index])
		keptBefore = append(keptBefore, item)
	}
	var keptAfter []domain.IssueSuggestion
	tail := false
	for _, item := range after {
		if !scope[item.IssueID] {
			return nil, nil, nil, refuse("added a suggestion outside its issue scope")
		}
		if regenerated[item.IssueID] {
			tail = true
			appended = append(appended, item)
			continue
		}
		if tail {
			return nil, nil, nil, refuse("reordered retained suggestions")
		}
		keptAfter = append(keptAfter, item)
	}
	switch {
	case len(keptAfter) == len(keptBefore):
		for index, item := range keptAfter {
			if item.ID != keptBefore[index].ID || item.IssueID != keptBefore[index].IssueID {
				return nil, nil, nil, refuse("replaced a retained suggestion")
			}
			if !reflect.DeepEqual(item, keptBefore[index]) {
				edits[kept[index]] = item
			}
		}
	case len(keptAfter) < len(keptBefore):
		next := 0
		for index, item := range keptBefore {
			if next < len(keptAfter) && reflect.DeepEqual(item, keptAfter[next]) {
				next++
				continue
			}
			dropped[kept[index]] = true
		}
		if next != len(keptAfter) {
			return nil, nil, nil, refuse("edited and deleted retained suggestions together")
		}
	default:
		return nil, nil, nil, refuse("added suggestions without regenerating their issue")
	}
	return dropped, edits, appended, nil
}

type boundedIssueSuggestionAttempt struct {
	*boundedDiscussionAttempt
	content     *issueChildContentRows
	suggestions *issueSuggestionInputs
}

func (r *boundedIssueSuggestionAttempt) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.suggestions != nil {
			r.suggestions.close()
			r.suggestions = nil
		}
		if r.content != nil {
			r.content.Close()
			r.content = nil
		}
		if r.data != nil {
			*r.data = domain.Bootstrap{}
			r.data = nil
		}
		if r.lookups != nil {
			r.lookups.close()
		}
		if r.rows != nil {
			r.rows.Close()
		}
		r.owner.Close()
	})
}

// mutateBoundedIssueSuggestions keeps the literal issue-record callback of the
// scoped writer (mutateIssueScope) for callbacks that change the scoped issues,
// append their activities and edit, delete or regenerate their issueSuggestions
// rows. It persists issue and content rows as the scoped writer does and the
// metadata image through the legacy image writer, so the suggestion list,
// project progress, event, realtime payload and publication follow the
// legacy contract. Team moves (renumbering) are refused.
func (s *SQLiteStore) mutateBoundedIssueSuggestions(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	input, _ := issueSuggestionWriteFromContext(ctx)
	ids, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	if issueRenumbering(ctx) {
		return fmt.Errorf("%w: team moves renumber issues", ErrIssueSuggestionWriteRefused)
	}
	if s == nil || !s.BoundedMetadataEnabled() || !UsesIssueRecordMutations(ctx) || IsIssueDiscussionMutation(ctx) ||
		len(ids) == 0 || len(ids) > 1000 || mutate == nil || !metadataOutputOwnerPresent(ctx) {
		return fmt.Errorf("%w: unsupported bounded issue suggestion mutation", ErrBoundedMetadataCoverage)
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
		// The scoped writer resolves live membership before its first
		// context-returning SQL operation.
		if _, actor := actorFromContext(ctx); actor {
			return ErrAuthForbidden
		}
		return ctx.Err()
	}
	scope := map[string]bool{}
	for _, id := range ids {
		scope[id] = true
	}
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	ctx, finishTimestampInputs, err := s.startMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer finishTimestampInputs()
	var event domain.DomainEvent
	var realtime json.RawMessage
	metadataWritten := false
	traceStart(ctx, "issue")
	apply := func(ctx context.Context) error {
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
		attempt := &boundedIssueSuggestionAttempt{boundedDiscussionAttempt: &boundedDiscussionAttempt{owner: &ResourceRead{store: s}}}
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
		for _, id := range ids {
			record, found := records[id]
			if !found || slices.Contains(keys, id) {
				continue
			}
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
			content, err := attempt.content.loadMutationScope(ctx, keys)
			if err != nil {
				return err
			}
			data.Comments, data.Activities = content.Comments, content.Activities
			data.Notifications, data.NotificationDeliveries = content.Notifications, content.NotificationDeliveries
		}
		oldDeliveries := make(map[string]bool, len(data.NotificationDeliveries))
		for _, delivery := range data.NotificationDeliveries {
			oldDeliveries[delivery.ID] = true
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
		attempt.suggestions, err = s.loadIssueSuggestionInputs(ctx, display, &data, keys, input)
		if err != nil {
			return err
		}
		attempt.inputs = attempt.suggestions.issueHotInputs
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
		if err := validateIssueSuggestionCallback(beforeCallback, data, scope); err != nil {
			return err
		}
		dropped, edits, appended, err := issueSuggestionPatch(beforeCallback.IssueSuggestions, data.IssueSuggestions,
			attempt.suggestions.ordinals, beforeCallback.Issues, data.Issues, scope)
		if err != nil {
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
			delete(oldDeliveries, delivery.ID)
			if err := writeContentRecord(writeCtx, tx, workspace, "delivery", "", delivery); err != nil {
				return err
			}
		}
		for id := range oldDeliveries {
			if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`, workspace, id); err != nil {
				return err
			}
		}
		patch, releasePatch, err := s.issueSuggestionPatchNode(ctx, display, dropped, edits, appended)
		if err != nil {
			return err
		}
		defer releasePatch()
		requested, err := boundedDiscussionEventSource(ctx, display, data)
		if err != nil {
			return err
		}
		defer requested.Release()
		restored, err := requested.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			switch name {
			case "viewer", "viewerRole":
				index, _, _ := metadataLogicalField(name)
				return metadataAggregateStoredNode{stored.fields[index]}, true, nil
			case "issueSuggestions":
				if patch != nil {
					return patch, true, nil
				}
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
		var progress *scopedIssueReactionProgress
		if progressEvent(eventType) {
			progress, err = s.prepareScopedIssueReactionProgress(ctx, restored, loaded, data.Issues)
			if err != nil {
				return err
			}
			defer progress.Close()
		}
		persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"issueSuggestions"})
		var merged *metadataLogicalSource
		if progress != nil {
			merged, err = s.persistLegacyMetadataImageWithProgress(persistCtx, tx, workspace, stored, restored, progress)
		} else {
			merged, err = s.persistLegacyMetadataImage(persistCtx, tx, workspace, stored, restored)
		}
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
	}
	err = mutationAttempts(ctx, fullMutationAttempts, apply, s.refreshConflictedLogicalMetadata(ctx, workspace))
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

// validateIssueSuggestionCallback admits only the scoped writer's issue and
// timeline effects plus issueSuggestions edits: the same scoped issues, appended
// activities, unchanged comments and notifications, and no other metadata.
func validateIssueSuggestionCallback(before, after domain.Bootstrap, scope map[string]bool) error {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: suggestion callback %s", ErrIssueSuggestionWriteRefused, reason)
	}
	if len(before.Issues) != len(after.Issues) {
		return refuse("changed its issue scope")
	}
	seen := map[string]bool{}
	for index, issue := range after.Issues {
		if !scope[issue.ID] || seen[issue.ID] || issue.ID != before.Issues[index].ID {
			return refuse("changed its issue scope")
		}
		seen[issue.ID] = true
		old := before.Issues[index]
		if issue.Team.ID != old.Team.ID || issue.Number != old.Number || issue.Identifier != old.Identifier {
			return refuse("moved an issue")
		}
	}
	for _, field := range changedMetadataFields(collectionMetadata(before), collectionMetadata(after)) {
		if field.name != "issueSuggestions" {
			return refuse("changed metadata " + field.name)
		}
	}
	if !reflect.DeepEqual(before.Comments, after.Comments) || !reflect.DeepEqual(before.Notifications, after.Notifications) ||
		!reflect.DeepEqual(before.NotificationDeliveries, after.NotificationDeliveries) {
		return refuse("changed comments or notifications")
	}
	for key, items := range after.Activities {
		previous := before.Activities[key]
		if !scope[key] || len(items) < len(previous) || len(previous) != 0 && !reflect.DeepEqual(previous, items[:len(previous)]) {
			return refuse("rewrote activity history")
		}
	}
	for key, items := range before.Activities {
		if _, present := after.Activities[key]; !present && items != nil {
			return refuse("removed activity history")
		}
	}
	return nil
}

// issueSuggestionPatchNode returns the patched collection and a release for
// its frames, which the caller holds until its Transform consumers finish.
func (s *SQLiteStore) issueSuggestionPatchNode(ctx context.Context, source *metadataLogicalSource, dropped map[int]bool, edits map[int]domain.IssueSuggestion, appended []domain.IssueSuggestion) (_ metadataAggregateNode, release func(), err error) {
	release = func() {}
	if len(dropped) == 0 && len(edits) == 0 && len(appended) == 0 {
		return nil, release, nil
	}
	index, _, _ := metadataLogicalField("issueSuggestions")
	base := source.fields[index]
	frames, err := newMetadataLogicalBuilder(metadataLogicalSourceScratchContext(ctx, source))
	if err != nil {
		return nil, release, err
	}
	work := &ResourceRead{store: s}
	release = func() {
		frames.Close()
		work.Close()
	}
	defer func() {
		if err != nil {
			release()
			release = func() {}
		}
	}()
	for _, storage := range source.stores {
		if err := frames.inheritLocations(storage); err != nil {
			return nil, release, err
		}
	}
	if err := frames.inheritTimestampInputs(ctx); err != nil {
		return nil, release, err
	}
	nilValue, err := base.isNil(ctx)
	if err != nil {
		return nil, release, err
	}
	shape, err := legacyDiscussionShape(frames, base.typ, nilValue && len(appended) == 0)
	if err != nil {
		return nil, release, err
	}
	frame := func(value domain.IssueSuggestion) (metadataLogicalNode, error) {
		native := reflect.ValueOf(value)
		cost, err := metadataAggregateNativeCost(ctx, native, 0)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		if cost > (metadataWorkBytes-4096)/8 {
			return metadataLogicalNode{}, ErrMetadataWorkBudget
		}
		if err := work.reserve(ctx, cost*8+4096); err != nil {
			return metadataLogicalNode{}, err
		}
		node, err := frames.writeAggregateNode(legacyDiscussionNativeRecord{value: native}, 0)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		if node.blocked || node.unavailable {
			return metadataLogicalNode{}, ErrBoundedMetadataCoverage
		}
		return node, nil
	}
	patch := legacyDiscussionSlicePatch{base: base, shape: shape, replacements: map[int]metadataLogicalNode{}, dropped: dropped}
	for ordinal, value := range edits {
		node, err := frame(value)
		if err != nil {
			return nil, release, err
		}
		patch.replacements[ordinal] = node
	}
	for _, value := range appended {
		node, err := frame(value)
		if err != nil {
			return nil, release, err
		}
		patch.appended = append(patch.appended, node)
	}
	return patch, release, ctx.Err()
}
