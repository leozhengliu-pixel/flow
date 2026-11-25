package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// ErrIssueBatchWriteRefused reports a batch update the bounded writer does
// not reproduce, before any write: a CRDT snapshot input, a renumbering
// batch without an actor, or a callback whose effects leave the update
// closure. It wraps ErrBoundedMetadataCoverage.
var ErrIssueBatchWriteRefused = fmt.Errorf("%w: issue batch writer refused the input", ErrBoundedMetadataCoverage)

type issueBatchUpdateKey struct{}

// ContextWithIssueBatchUpdate routes a bounded issue.batch_updated mutation
// of the scoped issues (WithIssueRecordMutations) to the dedicated writer.
// update is the input the callback applies to every scoped issue; it
// declares the callback's reference closure. The callback, event and payload
// are unchanged.
func ContextWithIssueBatchUpdate(ctx context.Context, update domain.IssueUpdateInput) context.Context {
	return context.WithValue(ctx, issueBatchUpdateKey{}, update)
}

func issueBatchUpdateFromContext(ctx context.Context) (domain.IssueUpdateInput, bool) {
	update, ok := ctx.Value(issueBatchUpdateKey{}).(domain.IssueUpdateInput)
	return update, ok
}

// issueBatchInputs is the reference closure of a batch update callback:
// the selected inputs of an update of every scoped issue (selectIssueInputs)
// plus, with their source ordinals, the family's suggestions and SLAs, and
// the attached documents of moved issues.
type issueBatchInputs struct {
	*issueHotInputs
	suggestions []int
	slas        []int
	slaEvents   int
}

// issueBatchStates selects the workflow states the callback can resolve,
// as loadIssueWorkflowStates does for one update: every loaded issue's
// team, and the destination team, following inherited workflows; the
// workspace states when one of those teams has none of its own.
func (r *issueHotInputs) issueBatchStates(ctx context.Context, data *domain.Bootstrap, input domain.IssueUpdateInput) error {
	needed := map[string]bool{}
	addTeam := func(team string) error {
		for steps := 0; steps <= len(data.TeamSettings); steps++ {
			settings := data.TeamSettings[team]
			if !settings.InheritWorkflowStatuses || settings.ParentTeamID == "" {
				if _, exists := needed[team]; !exists {
					if err := r.reserve(ctx, 512); err != nil {
						return err
					}
					needed[team] = false
				}
				return nil
			}
			team = settings.ParentTeamID
		}
		return nil
	}
	for _, issue := range data.Issues {
		if err := addTeam(issue.Team.ID); err != nil {
			return err
		}
	}
	if input.TeamID != nil {
		if err := addTeam(*input.TeamID); err != nil {
			return err
		}
	}
	index, _, _ := metadataLogicalField("states")
	collection := r.source.fields[index]
	if err := collection.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		team, err := issueHotField(ctx, record, "TeamID")
		if err != nil {
			return err
		}
		for id := range needed {
			equal, err := team.StringEqual(ctx, id)
			if err != nil {
				return err
			}
			if equal {
				needed[id] = true
			}
		}
		return nil
	}); err != nil {
		return err
	}
	shared := false
	for _, specific := range needed {
		shared = shared || !specific
	}
	return r.selectSlice(ctx, "states", func(record metadataLogicalNode) (bool, error) {
		team, err := issueHotField(ctx, record, "TeamID")
		if err != nil {
			return false, err
		}
		if shared {
			if wanted, err := team.StringEqual(ctx, ""); err != nil || wanted {
				return wanted, err
			}
		}
		for id, specific := range needed {
			if !specific {
				continue
			}
			if wanted, err := team.StringEqual(ctx, id); err != nil || wanted {
				return wanted, err
			}
		}
		return false, nil
	})
}

// ordinals returns the source ordinals of the records of field the match
// selects, in collection order, without decoding them.
func (r *issueHotInputs) ordinals(ctx context.Context, field string, match func(metadataLogicalNode) (bool, error)) ([]int, error) {
	index, _, _ := metadataLogicalField(field)
	var result []int
	err := r.source.fields[index].Children(ctx, func(ordinal reflect.Value, record metadataLogicalNode) error {
		wanted, err := match(record)
		if err != nil || !wanted {
			return err
		}
		if err := r.reserve(ctx, 64); err != nil {
			return err
		}
		result = append(result, int(ordinal.Int()))
		return nil
	})
	return result, err
}

func (s *SQLiteStore) loadIssueBatchInputs(ctx context.Context, source *metadataLogicalSource, data *domain.Bootstrap, targets []domain.Issue, input domain.IssueUpdateInput) (_ *issueBatchInputs, err error) {
	if err := source.Retain(); err != nil {
		return nil, err
	}
	read := &issueBatchInputs{issueHotInputs: &issueHotInputs{store: s, source: source}}
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
	data.Teams, data.TeamSettings, data.WorkspaceSettings, data.TeamParents = read.data.Teams, read.data.TeamSettings, read.data.WorkspaceSettings, read.data.TeamParents
	if err := read.selectIssueInputs(ctx, data, targets, input); err != nil {
		return nil, err
	}
	if err := read.issueBatchStates(ctx, data, input); err != nil {
		return nil, err
	}
	loaded, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, issue := range data.Issues {
		if err := read.addKey(ctx, loaded, issue.ID); err != nil {
			return nil, err
		}
		if issue.Project != nil {
			if err := read.addKey(ctx, projects, issue.Project.ID); err != nil {
				return nil, err
			}
		}
	}
	if input.ProjectID != nil {
		if err := read.addKey(ctx, projects, *input.ProjectID); err != nil {
			return nil, err
		}
	}
	if err := read.selectSlice(ctx, "projects", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", projects)
	}); err != nil {
		return nil, err
	}
	// selectIssueInputs selected the family's SLAs; their ordinals let the
	// callback's edits replace them in place.
	if read.slas, err = read.ordinals(ctx, "issueSlas", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "IssueID", loaded)
	}); err != nil {
		return nil, err
	}
	if len(read.slas) != len(read.data.IssueSLAs) {
		return nil, ErrBoundedMetadataCoverage
	}
	// recordSLAEvent numbers an event by the length of the complete list.
	if events, err := read.ordinals(ctx, "slaEvents", func(metadataLogicalNode) (bool, error) { return true, nil }); err != nil {
		return nil, err
	} else {
		read.slaEvents = len(events)
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
		matched, err := issueHotMatches(ctx, record, "IssueID", loaded)
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
			read.suggestions = append(read.suggestions, int(ordinal.Int()))
		}
		return err
	}); err != nil {
		return nil, err
	}
	if read.move != nil {
		moved, err := read.keys(ctx)
		if err != nil {
			return nil, err
		}
		for _, target := range targets {
			if target.Team.ID != read.moveTeam {
				if err := read.addKey(ctx, moved, target.ID); err != nil {
					return nil, err
				}
			}
		}
		if err := read.selectIssueTeamMoveDocuments(ctx, read.move, moved, nil); err != nil {
			return nil, err
		}
	}
	return read, ctx.Err()
}

// install puts the closure into the callback input.
func (r *issueBatchInputs) install(data *domain.Bootstrap) {
	input := reflect.ValueOf(r.data)
	target := reflect.ValueOf(data).Elem()
	for _, name := range []string{
		"users", "notificationPreferences", "pushSubscriptions", "userSettings",
		"cycleSettings", "cycles", "labels", "labelGroups", "settings",
		"issueSlas", "slaRules", "members", "states", "projects", "issueSuggestions",
	} {
		index, _, _ := metadataLogicalField(name)
		target.Field(index).Set(input.Field(index))
	}
	if r.move != nil {
		data.Documents = r.data.Documents
	}
	data.SLAEvents = []domain.SLAEvent{}
}

type boundedIssueBatchAttempt struct {
	*boundedDiscussionAttempt
	content *issueChildContentRows
	batch   *issueBatchInputs
}

func (r *boundedIssueBatchAttempt) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.batch != nil {
			r.batch.close()
			r.batch = nil
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

// collectionValue decodes a slice or map collection one entry at a time:
// each entry's decoding work is admitted while it runs and released after,
// and the decoded entry is admitted by its measured size for as long as the
// attempt holds it, so a batch of many issues does not admit the decoding
// work of every issue at once.
func (r *boundedDiscussionAttempt) collectionValue(ctx context.Context, node metadataLogicalNode) (reflect.Value, error) {
	absent, err := node.isNil(ctx)
	if err != nil {
		return reflect.Value{}, err
	}
	if absent {
		return reflect.Zero(node.typ), nil
	}
	entry := func(child metadataLogicalNode) (reflect.Value, error) {
		if child.cost > (metadataWorkBytes-4096)/8 {
			return reflect.Value{}, ErrMetadataWorkBudget
		}
		_, release, err := r.owner.store.tryAdmitAdditionalMetadataWork(ctx, int(child.cost*8+4096))
		if err != nil {
			return reflect.Value{}, err
		}
		value, err := child.Value(ctx, int(child.cost))
		defer release()
		if err != nil {
			return reflect.Value{}, err
		}
		retained, err := metadataAggregateNativeCost(ctx, value, 0)
		if err != nil {
			return reflect.Value{}, err
		}
		width := uint64(value.Type().Size())
		if width > (metadataWorkBytes-512)/2 {
			return reflect.Value{}, ErrMetadataWorkBudget
		}
		// Append/map growth copies entry slots, while nested containers and
		// immutable bodies keep their already-owned backing allocations.
		relocation := width*2 + 512
		if retained > metadataWorkBytes-relocation {
			return reflect.Value{}, ErrMetadataWorkBudget
		}
		return value, r.owner.reserve(ctx, retained+relocation)
	}
	switch node.typ.Kind() {
	case reflect.Slice:
		result := reflect.MakeSlice(node.typ, 0, 0)
		err := node.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error {
			value, err := entry(child)
			if err == nil {
				result = reflect.Append(result, value)
			}
			return err
		})
		return result, err
	case reflect.Map:
		result := reflect.MakeMap(node.typ)
		err := node.MapEntries(ctx, func(key, child metadataLogicalNode) error {
			name, err := entry(key)
			if err != nil {
				return err
			}
			value, err := entry(child)
			if err == nil {
				result.SetMapIndex(name, value)
			}
			return err
		})
		return result, err
	}
	return r.owner.node(ctx, node, false)
}

// mutateBoundedIssueBatch is mutateIssueScope for issue.batch_updated over
// owned installed reads: the scoped issues and, when the update changes the
// state or parent, their parents, the children of both and the state's
// sort-order neighbours in each team, loaded exactly as the legacy writer
// loads them; their content rows; and the closure of the update applied to
// every scoped issue (issueBatchInputs). Under WithIssueRenumbering it locks
// the sequence first and reads the live member rows of the people the
// callback can name (issueMoveAudience). The callback, its activities,
// notifications and deliveries, project progress, the display refresh, the
// suggestion, SLA, SLA event and attached document edits, the event, its
// payload and realtime value and the publication follow the legacy contract.
func (s *SQLiteStore) mutateBoundedIssueBatch(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	update, _ := issueBatchUpdateFromContext(ctx)
	ids, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	renumber := issueRenumbering(ctx)
	if s == nil || !s.BoundedMetadataEnabled() || !UsesIssueRecordMutations(ctx) || IsIssueDiscussionMutation(ctx) ||
		eventType != "issue.batch_updated" || mutate == nil || !metadataOutputOwnerPresent(ctx) {
		return fmt.Errorf("%w: unsupported bounded issue batch mutation", ErrBoundedMetadataCoverage)
	}
	if len(ids) == 0 || len(ids) > 1000 {
		return fmt.Errorf("invalid issue mutation scope")
	}
	if _, actor := actorFromContext(ctx); renumber && !actor {
		return fmt.Errorf("%w: renumbering batch without an actor", ErrIssueBatchWriteRefused)
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
		// The legacy writer resolves live membership before its first
		// context-returning SQL operation.
		if _, actor := actorFromContext(ctx); actor {
			return ErrAuthForbidden
		}
		return ctx.Err()
	}
	needsFamily := update.StateID != nil || update.ParentID != nil
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	ctx = withIssueRecordWriteAdmission(ctx, s)
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
		attempt := &boundedIssueBatchAttempt{boundedDiscussionAttempt: &boundedDiscussionAttempt{owner: &ResourceRead{store: s}}}
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
		actor, hasActor := actorFromContext(ctx)
		if hasActor {
			if err := attempt.reserveValue(ctx, actor); err != nil {
				return err
			}
			if actor.Via != nil {
				if err := attempt.reserveValue(ctx, actor.Via); err != nil {
					return err
				}
			}
			data.Viewer = mutationInputActor(actor)
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
		if renumber {
			// Like the legacy writer, lock the sequence before any other row.
			if data.NextIssueNumber, err = s.issueTeamMoveSequence(ctx, tx, workspace, attempt.owner, cloned); err != nil {
				return err
			}
		}
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
		loaded, rawBefore, order, err := s.loadIssueBatchRows(ctx, tx, workspace, attempt.rows, ids, update, needsFamily)
		if err != nil {
			return err
		}
		if err := attempt.owner.reserve(ctx, uint64(len(loaded))*4096+64<<10); err != nil {
			return err
		}
		data.Issues = make([]domain.Issue, 0, len(order))
		before := make([]domain.Issue, 0, len(order))
		for _, id := range order {
			data.Issues = append(data.Issues, loaded[id])
			before = append(before, loaded[id])
		}
		keys := slices.Clone(order)
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
		traceMark(ctx, "batch-content")
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
			value, err := attempt.collectionValue(ctx, display.fields[index])
			if err != nil {
				return err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
		// Transform copied the complete SQL preimages into immutable frames;
		// collectionValue owns independent displayed native values now.
		attempt.content.Close()
		attempt.content = nil
		traceMark(ctx, "batch-display")
		var targets []domain.Issue
		for _, id := range ids {
			if index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == id }); index >= 0 {
				targets = append(targets, data.Issues[index])
			}
		}
		attempt.batch, err = s.loadIssueBatchInputs(ctx, display, &data, targets, update)
		if err != nil {
			return err
		}
		attempt.inputs = attempt.batch.issueHotInputs
		attempt.batch.install(&data)
		if renumber {
			// Team moves see the current memberships; the stored ones are
			// put back before the write, like the legacy writer.
			users := []string{data.Viewer.ID}
			for _, user := range data.Users {
				users = append(users, user.ID)
			}
			if data.Members, data.TeamMembers, err = attempt.inputs.issueMoveAudience(ctx, tx, data.Workspace.ID, users); err != nil {
				return err
			}
		}
		var first domain.Issue
		if len(targets) > 0 {
			first = targets[0]
		}
		attempt.lookups, err = newIssueEditorLookups(ctx, attempt.inputs, tx, workspace, data.Workspace.ID, first)
		if err != nil {
			return err
		}
		attempt.lookups.allowDocuments(targets)
		attempt.lookups.liveMembers = renumber && hasActor
		traceMark(ctx, "batch-inputs")
		if err := attempt.reserveValue(ctx, payload); err != nil {
			return err
		}
		releaseCallbackEncoding, err := reserveIssueBatchCallbackEncoding(ctx, s, data, payload)
		if err != nil {
			return err
		}
		defer releaseCallbackEncoding()
		traceMark(ctx, "batch-encoding")
		callbackCost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(data), 0)
		if err != nil {
			return err
		}
		if callbackCost > metadataWorkBytes {
			return ErrMetadataWorkBudget
		}
		// Collection/input owners already retain data and its growth-copy
		// capacity. Keep one additional native envelope for callback growth;
		// the independent comparison clone below owns only copied containers.
		if err := attempt.owner.reserve(ctx, callbackCost); err != nil {
			return err
		}
		comparison := &ResourceRead{store: s}
		defer comparison.Close()
		cloneCost, err := cycleScheduleCloneValueWork(ctx, reflect.ValueOf(data), false, 0)
		if err != nil {
			return err
		}
		if data.Viewer.Via != nil {
			extra, err := cycleScheduleCloneValueWork(ctx, reflect.ValueOf(data.Viewer.Via), false, 0)
			if err != nil {
				return err
			}
			if extra > metadataWorkBytes-cloneCost {
				return ErrMetadataWorkBudget
			}
			cloneCost += extra
		}
		if cloneCost > metadataWorkBytes-(64<<10) {
			return ErrMetadataWorkBudget
		}
		if err := comparison.reserve(ctx, cloneCost+64<<10); err != nil {
			return err
		}
		beforeCallback := cloneBootstrap(data)
		if data.Viewer.Via != nil {
			beforeCallback.Viewer.Via = cloneSnapshotValue(reflect.ValueOf(data.Viewer.Via)).Interface().(*domain.ActorBot)
		}
		attempt.lookups.install(&data)
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		traceMark(ctx, "load")
		aggregate, callbackErr := mutate(&data)
		releaseCallbackEncoding()
		data.FormerMembers, data.CollaborationUpdates, data.CollaborationWork = nil, nil, nil
		if err := attempt.lookups.check(); err != nil {
			return err
		}
		traceMark(ctx, "mutate")
		if callbackErr != nil {
			return callbackErr
		}
		scope := map[string]bool{}
		for _, id := range keys {
			scope[id] = true
		}
		move := attempt.batch.move
		if err := validateIssueBatchCallback(beforeCallback, data, scope, move != nil); err != nil {
			return err
		}
		var documentEdits map[int]domain.Document
		if move != nil {
			if documentEdits, err = move.check(beforeCallback, data); err != nil {
				return err
			}
		}
		dropped, edits, appended, err := issueSuggestionPatch(beforeCallback.IssueSuggestions, data.IssueSuggestions,
			attempt.batch.suggestions, beforeCallback.Issues, data.Issues, scope)
		if err != nil {
			return err
		}
		slaEdits, slaAppended, err := issueBatchSLAPatch(beforeCallback.IssueSLAs, data.IssueSLAs, attempt.batch.slas, scope)
		if err != nil {
			return err
		}
		beforeCallback = domain.Bootstrap{}
		comparison.Close()
		events, err := issueBatchSLAEvents(data.SLAEvents, attempt.batch.slaEvents)
		if err != nil {
			return err
		}
		if _, err := s.writeIssueRecordBatch(ctx, tx, workspace, data.Issues, data, rawBefore); err != nil {
			return err
		}
		if renumber {
			if err := advanceIssueNumberSequence(ctx, tx, workspace, data.Issues); err != nil {
				return err
			}
		}
		writeCtx := context.WithValue(ctx, legacyContentWriteBudgetKey{}, s)
		releaseContentEncoding, err := reserveIssueBatchContentEncoding(ctx, s, data)
		if err != nil {
			return err
		}
		defer releaseContentEncoding()
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
		releaseContentEncoding()
		patches := map[string]metadataAggregateNode{}
		suggestionPatch, releaseSuggestions, err := s.issueSuggestionPatchNode(ctx, display, dropped, edits, appended)
		if err != nil {
			return err
		}
		defer releaseSuggestions()
		if suggestionPatch != nil {
			patches["issueSuggestions"] = suggestionPatch
		}
		documents, releaseDocuments, err := s.issueTeamMoveDocumentPatch(ctx, display, documentEdits)
		if err != nil {
			return err
		}
		defer releaseDocuments()
		if documents != nil {
			patches["documents"] = documents
		}
		slaPatch, releaseSLAs, err := issueBatchSlicePatchNode(ctx, s, display, "issueSlas", nil, slaEdits, slaAppended)
		if err != nil {
			return err
		}
		defer releaseSLAs()
		if slaPatch != nil {
			patches["issueSlas"] = slaPatch
		}
		eventPatch, releaseEvents, err := issueBatchSlicePatchNode[domain.SLAEvent](ctx, s, display, "slaEvents", nil, nil, events)
		if err != nil {
			return err
		}
		defer releaseEvents()
		if eventPatch != nil {
			patches["slaEvents"] = eventPatch
		}
		requested, err := boundedDiscussionEventSource(ctx, display, data)
		if err != nil {
			return err
		}
		defer requested.Release()
		restored, err := requested.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			// The legacy writer puts the stored members back after a
			// renumbering callback, before the display refresh is saved.
			if name == "viewer" || name == "viewerRole" || name == "members" && renumber && hasActor {
				index, _, _ := metadataLogicalField(name)
				return metadataAggregateStoredNode{stored.fields[index]}, true, nil
			}
			if patch, ok := patches[name]; ok {
				return patch, true, nil
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
		progress, err := s.prepareScopedIssueReactionProgress(ctx, restored, before, data.Issues)
		if err != nil {
			return err
		}
		defer progress.Close()
		extraFields := []string{}
		for name := range patches {
			extraFields = append(extraFields, name)
		}
		slices.Sort(extraFields)
		persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, extraFields)
		merged, err := s.persistLegacyMetadataImageWithProgress(persistCtx, tx, workspace, stored, restored, progress)
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

// loadIssueBatchRows loads the rows mutateIssueScope loads for a batch
// update: the scoped issues; with needsFamily their parents, then the
// children of the scoped issues and of every loaded issue's parent; with a
// state update the first and last issue in sort order of each scoped team
// in that state. Like the legacy writer, children and state neighbours
// replace an earlier copy with their row decoded as stored (not
// normalized), the limits and their errors are the same and the neighbour
// rows are read without a lock. order lists the scoped issues first, then
// the others by ID.
func (s *SQLiteStore) loadIssueBatchRows(ctx context.Context, tx *sqlTx, workspace string, rows *issueMutationRows, ids []string, update domain.IssueUpdateInput, needsFamily bool) (map[string]domain.Issue, map[string][]byte, []string, error) {
	loaded, raw := map[string]domain.Issue{}, map[string][]byte{}
	add := func(records map[string]storedIssueRecord) {
		for id, record := range records {
			loaded[id], raw[id] = record.issue, record.raw
		}
	}
	records, err := rows.load(ctx, ids, true)
	if err != nil {
		return nil, nil, nil, err
	}
	add(records)
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	if needsFamily {
		related := []string{}
		for _, id := range ids {
			if issue, ok := loaded[id]; ok && issue.ParentID != nil {
				related = append(related, *issue.ParentID)
			}
		}
		related = slices.DeleteFunc(related, func(id string) bool { _, seen := loaded[id]; return seen })
		records, err := rows.load(ctx, related, true)
		if err != nil {
			return nil, nil, nil, err
		}
		add(records)
	}
	if len(loaded) > 1000 {
		return nil, nil, nil, fmt.Errorf("issue mutation scope exceeds 1000 records")
	}
	// asStored replaces loaded copies by the rows decoded as stored.
	asStored := func(found []string, lock bool) error {
		records, err := rows.load(ctx, found, lock)
		if err != nil {
			return err
		}
		for _, id := range found {
			record, ok := records[id]
			if !ok {
				continue
			}
			var issue domain.Issue
			if err := json.Unmarshal(record.raw, &issue); err != nil {
				return err
			}
			loaded[id], raw[id] = issue, record.raw
		}
		return nil
	}
	selectIDs := func(query string, args ...any) ([]string, error) {
		result, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		var found []string
		for result.Next() {
			var id string
			if err := result.Scan(&id); err != nil {
				result.Close()
				return nil, err
			}
			found = append(found, id)
		}
		return found, errors.Join(result.Err(), result.Close())
	}
	if needsFamily {
		parentIDs := slices.Clone(ids)
		for _, id := range slices.Sorted(maps.Keys(loaded)) {
			if issue := loaded[id]; issue.ParentID != nil {
				parentIDs = append(parentIDs, *issue.ParentID)
			}
		}
		for _, id := range parentIDs {
			found, err := selectIDs(`SELECT id FROM issue_records WHERE workspace_key=? AND parent_id=? ORDER BY id LIMIT 1001`+lock, workspace, id)
			if err != nil {
				return nil, nil, nil, err
			}
			if err := asStored(found, true); err != nil {
				return nil, nil, nil, err
			}
		}
		if len(loaded) > 1000 {
			return nil, nil, nil, fmt.Errorf("issue deletion requires a batch job")
		}
	}
	if update.StateID != nil {
		teams := []string{}
		for _, id := range ids {
			if issue, ok := loaded[id]; ok && !slices.Contains(teams, issue.Team.ID) {
				teams = append(teams, issue.Team.ID)
			}
		}
		for _, team := range teams {
			for _, direction := range []string{"ASC", "DESC"} {
				found, err := selectIDs(`SELECT id FROM issue_records WHERE workspace_key=? AND team_id=? AND state_id=? ORDER BY sort_order `+direction+`,id `+direction+` LIMIT 1`, workspace, team, *update.StateID)
				if err != nil {
					return nil, nil, nil, err
				}
				if err := asStored(found, false); err != nil {
					return nil, nil, nil, err
				}
			}
		}
	}
	order := []string{}
	for _, id := range ids {
		if _, ok := loaded[id]; ok && !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(loaded)) {
		if !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	return loaded, raw, order, ctx.Err()
}

// validateIssueBatchCallback admits the batch update callback's issue and
// timeline effects plus the metadata a legacy batch persists: suggestions,
// SLAs, SLA events and, with a team move, the attached documents.
func validateIssueBatchCallback(before, after domain.Bootstrap, scope map[string]bool, move bool) error {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: batch callback %s", ErrIssueBatchWriteRefused, reason)
	}
	if len(before.Issues) != len(after.Issues) {
		return refuse("changed its issue scope")
	}
	for index, issue := range after.Issues {
		old := before.Issues[index]
		if !scope[issue.ID] || issue.ID != old.ID {
			return refuse("changed its issue scope")
		}
		if !move && (issue.Team.ID != old.Team.ID || issue.Number != old.Number || issue.Identifier != old.Identifier) {
			return refuse("moved an issue")
		}
	}
	allowed := map[string]bool{"issueSuggestions": true, "issueSlas": true, "slaEvents": true, "documents": move}
	for _, field := range changedMetadataFields(collectionMetadata(before), collectionMetadata(after)) {
		if !allowed[field.name] {
			return refuse("changed metadata " + field.name)
		}
	}
	for key := range after.Activities {
		if !scope[key] {
			return refuse("wrote activity outside its scope")
		}
	}
	for key := range after.Comments {
		if !scope[key] {
			return refuse("wrote comments outside its scope")
		}
	}
	for _, notification := range after.Notifications {
		if !scope[notification.IssueID] {
			return refuse("notified outside its scope")
		}
	}
	return nil
}

// issueBatchSLAPatch maps the callback's SLA list onto the stored one:
// the selected rows keep their positions and may only change in place;
// rows the callback appended for scoped issues are appended.
func issueBatchSLAPatch(before, after []domain.IssueSLA, ordinals []int, scope map[string]bool) (map[int]domain.IssueSLA, []domain.IssueSLA, error) {
	refuse := func(reason string) error {
		return fmt.Errorf("%w: batch callback %s", ErrIssueBatchWriteRefused, reason)
	}
	if len(after) < len(before) {
		return nil, nil, refuse("removed an SLA")
	}
	edits := map[int]domain.IssueSLA{}
	for index, item := range before {
		next := after[index]
		if next.ID != item.ID || next.IssueID != item.IssueID {
			return nil, nil, refuse("replaced an SLA")
		}
		if !reflect.DeepEqual(next, item) {
			edits[ordinals[index]] = next
		}
	}
	appended := after[len(before):]
	for _, item := range appended {
		if !scope[item.IssueID] {
			return nil, nil, refuse("added an SLA outside its scope")
		}
	}
	return edits, appended, nil
}

// issueBatchSLAEvents numbers the appended SLA events as recordSLAEvent
// numbers them in the complete list: "sla_event_<stamp>_<length>".
func issueBatchSLAEvents(appended []domain.SLAEvent, stored int) ([]domain.SLAEvent, error) {
	result := make([]domain.SLAEvent, 0, len(appended))
	for index, item := range appended {
		cut := strings.LastIndexByte(item.ID, '_')
		if !strings.HasPrefix(item.ID, "sla_event_") || cut < len("sla_event_") || item.ID[cut+1:] != strconv.Itoa(index) {
			return nil, fmt.Errorf("%w: batch callback recorded an unnumbered SLA event", ErrIssueBatchWriteRefused)
		}
		item.ID = item.ID[:cut+1] + strconv.Itoa(stored+index)
		result = append(result, item)
	}
	return result, nil
}

// issueBatchSlicePatchNode returns field's collection with edits in place,
// dropped ordinals removed and appended records at the end, and a release
// for its frames, which the caller holds until its consumers finish.
func issueBatchSlicePatchNode[T any](ctx context.Context, s *SQLiteStore, source *metadataLogicalSource, field string, dropped map[int]bool, edits map[int]T, appended []T) (_ metadataAggregateNode, release func(), err error) {
	release = func() {}
	if len(dropped) == 0 && len(edits) == 0 && len(appended) == 0 {
		return nil, release, nil
	}
	index, _, _ := metadataLogicalField(field)
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
	frame := func(value T) (metadataLogicalNode, error) {
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
