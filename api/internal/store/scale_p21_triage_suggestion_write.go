package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// ErrIssueSuggestionWriteRefused reports an input the bounded issue suggestion
// writer does not reproduce, such as a callback that changes unrelated
// metadata. It wraps ErrBoundedMetadataCoverage; nothing is written.
var ErrIssueSuggestionWriteRefused = fmt.Errorf("%w: issue suggestion writer refused the input", ErrBoundedMetadataCoverage)

// issueSuggestionWriteAttempts counts the transaction attempts of the bounded
// issue suggestion writer (IssueSuggestionWriteAttemptsForTesting).
var issueSuggestionWriteAttempts atomic.Int64

// IssueSuggestionWriteAttemptsForTesting returns how many transaction attempts
// the bounded issue suggestion writer has started in this process: a write
// that lost a race to another server and ran again counts twice.
func IssueSuggestionWriteAttemptsForTesting() int64 {
	if !testing.Testing() {
		return 0
	}
	return issueSuggestionWriteAttempts.Load()
}

// IssueSuggestionWrite declares the installed records an issue suggestion
// callback may apply beyond the scoped issues' own references and the targets
// of their stored suggestions: user, project and label IDs the caller already
// chose. Complete exposes every user and label and every project's triage
// fields, for callbacks that score the whole workspace. Under
// WithIssueRenumbering, Teams are the teams the callback may move a scoped
// issue into besides those the store derives (the issues' own teams, their
// suggestions' targets and their projects' teams), such as the teams of
// scoring candidates.
type IssueSuggestionWrite struct {
	Users, Projects, Labels, Teams []string
	Complete                       bool
	// HistoryHeads declares a callback that reads and edits the scoped
	// issues' history (comments, activities, notifications) only through
	// RecordIssueActivity. It then receives no history: RecordIssueActivity
	// loads an issue's latest activity and comment on demand, and the writer
	// syncs every scoped issue's history in bounded chunks with the
	// callback's timeline edits applied, as the full callback would leave it.
	HistoryHeads bool
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
	// move is the team-move closure, selected under WithIssueRenumbering.
	move *issueTeamMove
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
// A renumbering write also selects the team-move closure (issueTeamMove).
func (s *SQLiteStore) loadIssueSuggestionInputs(ctx context.Context, source *metadataLogicalSource, data *domain.Bootstrap, ids []string, input IssueSuggestionWrite, renumber bool) (_ *issueSuggestionInputs, err error) {
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
	if renumber {
		read.move, err = read.prepareIssueTeamMove(ctx, data.Issues, read.data.IssueSuggestions, input.Teams, labels, input.Complete)
		if err != nil {
			return nil, err
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
	labelMatch := match(labels)
	if read.move != nil && !input.Complete {
		labelMatch = func(record metadataLogicalNode) (bool, error) {
			matched, err := issueHotMatches(ctx, record, "ID", labels)
			if err != nil || matched {
				return matched, err
			}
			return read.move.label(ctx, read.issueHotInputs, record)
		}
	}
	if err := read.selectSlice(ctx, "labels", labelMatch); err != nil {
		return nil, err
	}
	if err := read.selectSlice(ctx, "projects", match(projects), triageProjectFields...); err != nil {
		return nil, err
	}
	if err := read.selectSlice(ctx, "states", func(record metadataLogicalNode) (bool, error) {
		matched, err := issueHotMatches(ctx, record, "ID", states)
		if err != nil || matched || read.move == nil {
			return matched, err
		}
		return read.move.state(ctx, record)
	}); err != nil {
		return nil, err
	}
	fields := []string{"teams", "teamSettings", "workspaceSettings", "teamParents", "issueSuggestions", "users", "labels", "projects", "states"}
	if read.move != nil {
		if err := read.selectIssueTeamMoveGroups(ctx, read.move); err != nil {
			return nil, err
		}
		if err := read.selectIssueTeamMoveDocuments(ctx, read.move, scope, triageProjectFields); err != nil {
			return nil, err
		}
		fields = append(fields, "labelGroups", "documents")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input2 := reflect.ValueOf(read.data)
	target := reflect.ValueOf(data).Elem()
	for _, name := range fields {
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
// legacy contract. Under WithIssueRenumbering it also owns the team-move
// closure (issueTeamMove): the locked sequence, the target workflow states,
// same-named labels, and the attached documents, whose derived team scope is
// persisted with the image.
func (s *SQLiteStore) mutateBoundedIssueSuggestions(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	input, _ := issueSuggestionWriteFromContext(ctx)
	ids, _ := ctx.Value(issueRecordMutationContext{}).([]string)
	renumber := issueRenumbering(ctx)
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
		issueSuggestionWriteAttempts.Add(1)
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
		if renumber {
			// Like the legacy writers, lock the sequence before any other
			// row, in the order issue creation takes them.
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
		// The rows keep only the stored payloads: the issue write compares
		// them and progress decodes its preimages from them again. The
		// decoded preimages live only until the selected image encodes them.
		attempt.rows = s.newIssueMutationRows(tx, workspace)
		attempt.rows.rawOnly = true
		records, err := attempt.rows.load(ctx, ids, true)
		if err != nil {
			return err
		}
		// The payload map and key list; the decoded issues are admitted by
		// their measured size where they are decoded.
		if err := attempt.owner.reserve(ctx, uint64(len(records))*256+64<<10); err != nil {
			return err
		}
		rawBefore := make(map[string][]byte, len(records))
		keys := make([]string, 0, len(records))
		for _, id := range ids {
			record, found := records[id]
			if !found || slices.Contains(keys, id) {
				continue
			}
			rawBefore[id] = record.raw
			keys = append(keys, id)
		}
		preimages := &ResourceRead{store: s}
		defer func() { preimages.Close() }()
		if data.Issues, err = decodeIssueSuggestionPreimages(ctx, preimages, keys, rawBefore, nil); err != nil {
			return err
		}
		data.Comments = map[string][]domain.Comment{}
		data.Activities = map[string][]domain.ActivityEvent{}
		data.Notifications = []domain.Notification{}
		data.NotificationDeliveries = []domain.NotificationDelivery{}
		attempt.content = s.newIssueChildContentRows(tx, workspace)
		if len(keys) != 0 && !input.HistoryHeads {
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
		// The selected image owns encoded copies of the SQL issues and child
		// content; the callback reads the display values decoded below.
		data.Issues, data.Comments, data.Activities = nil, nil, nil
		data.Notifications, data.NotificationDeliveries = nil, nil
		preimages.Close()
		attempt.content.Close()
		attempt.content = nil
		display, err := s.refreshLegacyDisplayImage(ctx, selected)
		if err != nil {
			return err
		}
		defer display.Release()
		decoded := []string{"issues", "comments", "activities", "notifications", "notificationDeliveries"}
		if input.HistoryHeads {
			// The scoped history stays in SQL: heads load it per chunk
			// for the callback and the sync writes it back per chunk.
			decoded = decoded[:1]
			data.Comments, data.Activities = map[string][]domain.Comment{}, map[string][]domain.ActivityEvent{}
			data.Notifications, data.NotificationDeliveries = []domain.Notification{}, []domain.NotificationDelivery{}
		}
		// The display values stay admitted by their decoded size until the
		// callback's results are measured (see below).
		decodedWork := &boundedDiscussionAttempt{owner: &ResourceRead{store: s}}
		attempt.owner.release = append(attempt.owner.release, decodedWork.owner.Close)
		for _, name := range decoded {
			index, _, _ := metadataLogicalField(name)
			value, err := decodedWork.collectionValue(ctx, display.fields[index])
			if err != nil {
				return err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
		attempt.suggestions, err = s.loadIssueSuggestionInputs(ctx, display, &data, keys, input, renumber)
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
		if err := attempt.reserveValue(ctx, payload); err != nil {
			return err
		}
		callbackCost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(data), 0)
		if err != nil {
			return err
		}
		if callbackCost > (metadataWorkBytes-(64<<10))/3 {
			return ErrMetadataWorkBudget
		}
		callbackWork := &ResourceRead{store: s}
		attempt.owner.release = append(attempt.owner.release, callbackWork.Close)
		if err := callbackWork.reserve(ctx, callbackCost*2); err != nil {
			return err
		}
		_, releaseBeforeCallback, err := s.tryAdmitAdditionalMetadataWork(ctx, int(callbackCost+64<<10))
		if err != nil {
			return err
		}
		defer releaseBeforeCallback()
		beforeCallback := cloneBootstrap(data)
		if data.Viewer.Via != nil {
			beforeCallback.Viewer.Via = cloneSnapshotValue(reflect.ValueOf(data.Viewer.Via)).Interface().(*domain.ActorBot)
		}
		data.FormerMembers = attempt.lookups.formerMembers
		data.CollaborationUpdates = attempt.lookups.collaborationUpdates
		var history *issueSuggestionHistoryHeads
		if input.HistoryHeads {
			history = s.newIssueSuggestionHistoryHeads(ctx, tx, workspace, cloned, keys)
			defer history.close()
			data.ActivityHistoryHeads = map[string]domain.ActivityEvent{}
			data.ActivityHistoryLoader = history.load
		}
		if !registerMetadataOutputClose(ctx, attempt.Close) {
			return ErrBoundedMetadataCoverage
		}
		transferred = true
		traceMark(ctx, "load")
		aggregate, callbackErr := mutate(&data)
		data.FormerMembers, data.CollaborationUpdates = nil, nil
		historyErr := data.ActivityHistoryError
		data.ActivityHistoryLoader, data.ActivityHistoryError = nil, nil
		if err := attempt.lookups.check(); err != nil {
			return err
		}
		traceMark(ctx, "mutate")
		if callbackErr != nil {
			return callbackErr
		}
		if historyErr != nil {
			return historyErr
		}
		move := attempt.suggestions.move
		var timeline map[string][]domain.ActivityEvent
		var recorded map[string]domain.ActivityEvent
		if history != nil {
			// The callback saw history heads only: check and keep its
			// timeline edits, then validate the rest as the full shape.
			if timeline, err = history.check(data, scope, move != nil); err != nil {
				return err
			}
			recorded = data.ActivityHistoryHeads
			data.Comments, data.Activities = beforeCallback.Comments, beforeCallback.Activities
			data.ActivityHistoryHeads = nil
		}
		if err := validateIssueSuggestionCallback(beforeCallback, data, scope, move != nil); err != nil {
			return err
		}
		var documentEdits map[int]domain.Document
		if move != nil {
			if documentEdits, err = move.check(beforeCallback, data); err != nil {
				return err
			}
		}
		dropped, edits, appended, err := issueSuggestionPatch(beforeCallback.IssueSuggestions, data.IssueSuggestions,
			attempt.suggestions.ordinals, beforeCallback.Issues, data.Issues, scope)
		if err != nil {
			return err
		}
		// The private differential preimage has no users after validation and
		// patch construction; callback results remain owned by attempt.
		beforeCallback = domain.Bootstrap{}
		releaseBeforeCallback()
		if _, err := s.writeIssueRecordBatch(ctx, tx, workspace, data.Issues, data, rawBefore); err != nil {
			return err
		}
		if renumber {
			if err := advanceIssueNumberSequence(ctx, tx, workspace, data.Issues); err != nil {
				return err
			}
		}
		writeCtx := context.WithValue(ctx, legacyContentWriteBudgetKey{}, s)
		if history != nil {
			if err := history.sync(writeCtx, data, recorded, timeline, aggregate); err != nil {
				return err
			}
			history.close()
		} else if err := func() error {
			releaseContent, err := reserveIssueBatchContentEncoding(ctx, s, data)
			if err != nil {
				return err
			}
			defer releaseContent()
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
			return nil
		}(); err != nil {
			return err
		}
		oldDeliveries = nil
		patch, releasePatch, err := s.issueSuggestionPatchNode(ctx, display, dropped, edits, appended)
		if err != nil {
			return err
		}
		defer releasePatch()
		documents, releaseDocuments, err := s.issueTeamMoveDocumentPatch(ctx, display, documentEdits)
		if err != nil {
			return err
		}
		defer releaseDocuments()
		requested, err := boundedDiscussionEventSource(ctx, display, data)
		if err != nil {
			return err
		}
		defer requested.Release()
		restored, err := requested.Transform(ctx, func(name string, node metadataLogicalNode) (metadataAggregateNode, bool, error) {
			switch name {
			case "viewer", "viewerRole", "members", "teamMembers":
				index, _, _ := metadataLogicalField(name)
				return metadataAggregateStoredNode{stored.fields[index]}, true, nil
			case "issueSuggestions":
				if patch != nil {
					return patch, true, nil
				}
			case "documents":
				if documents != nil {
					return documents, true, nil
				}
			}
			return metadataAggregateStoredNode{node}, false, nil
		})
		if err != nil {
			return err
		}
		defer restored.Release()
		// The patches and the requested image own encoded copies of the
		// callback's suggestion and document results, which end here. The
		// rest of its inputs and results stay admitted by their measured
		// size for as long as the attempt holds them, in place of the
		// display decoding, input selection and callback allowances.
		data.IssueSuggestions, data.Documents = nil, nil
		retained, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(data), 0)
		if err != nil {
			return err
		}
		decodedWork.owner.Close()
		callbackWork.Close()
		attempt.suggestions.close()
		if err := attempt.owner.reserve(ctx, retained+64<<10); err != nil {
			return err
		}
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
			// The preimages decoded again from the same stored payloads,
			// after the callback's private preimage was released.
			preimages = &ResourceRead{store: s}
			defer preimages.Close()
			// Progress reads the preimages of project issues only (their
			// contributions); the others' teams only select unused settings.
			loaded, err := decodeIssueSuggestionPreimages(ctx, preimages, keys, rawBefore, func(issue domain.Issue) bool {
				return issue.Project != nil && issue.Project.ID != ""
			})
			if err != nil {
				return err
			}
			progress, err = s.prepareScopedIssueReactionProgress(ctx, restored, loaded, data.Issues)
			if err != nil {
				return err
			}
			defer progress.Close()
		}
		// Progress was the final user of the original issue preimages.
		// Keep the callback/result owner live, but release raw SQL preimages
		// before materializing an inline metadata field for persistence.
		rawBefore, records = nil, nil
		attempt.rows.Close()
		attempt.rows = nil
		extraFields := []string{"issueSuggestions"}
		if documents != nil {
			extraFields = append(extraFields, "documents")
		}
		persistCtx := context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, extraFields)
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

// validateIssueSuggestionCallback admits only the scoped writer's issue and
// timeline effects plus issueSuggestions edits: the same scoped issues, appended
// activities, unchanged comments and notifications, and no other metadata.
// A team move (move) may also renumber issues and edit attached documents,
// which issueTeamMove.check admits, and RecordIssueActivity may merge its
// issue.updated entry into the latest one or drop that entry.
func validateIssueSuggestionCallback(before, after domain.Bootstrap, scope map[string]bool, move bool) error {
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
		if !move && (issue.Team.ID != old.Team.ID || issue.Number != old.Number || issue.Identifier != old.Identifier) {
			return refuse("moved an issue")
		}
	}
	for _, field := range changedMetadataFields(collectionMetadata(before), collectionMetadata(after)) {
		if field.name != "issueSuggestions" && (!move || field.name != "documents") {
			return refuse("changed metadata " + field.name)
		}
	}
	if !reflect.DeepEqual(before.Comments, after.Comments) || !reflect.DeepEqual(before.Notifications, after.Notifications) ||
		!reflect.DeepEqual(before.NotificationDeliveries, after.NotificationDeliveries) {
		return refuse("changed comments or notifications")
	}
	for key, items := range after.Activities {
		previous := before.Activities[key]
		if !scope[key] || !appendedActivityHistory(previous, items, move) {
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

// appendedActivityHistory reports whether items is previous with entries
// appended. With merge, one earlier issue.updated entry may also be edited in
// place (same ID) or removed, as RecordIssueActivity merges an edit into the
// latest one; syncContentRecords persists either like the legacy writer.
func appendedActivityHistory(previous, items []domain.ActivityEvent, merge bool) bool {
	equal := func(a, b []domain.ActivityEvent) bool { return len(a) == 0 && len(b) == 0 || reflect.DeepEqual(a, b) }
	if len(items) >= len(previous) && equal(previous, items[:len(previous)]) {
		return true
	}
	if !merge {
		return false
	}
	for index := range previous {
		if index < len(items) && reflect.DeepEqual(previous[index], items[index]) {
			continue
		}
		if previous[index].Type != "issue.updated" {
			return false
		}
		rest := previous[index+1:]
		if index < len(items) && items[index].ID == previous[index].ID && items[index].Type == "issue.updated" &&
			len(items) >= len(previous) && equal(rest, items[index+1:len(previous)]) {
			return true
		}
		return len(items) >= len(previous)-1 && equal(rest, items[index:len(previous)-1])
	}
	return false
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
	release = func() {
		frames.Close()
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
		// The callback owns value; the builder retains only its spooled
		// frame, so decoding/encoding scratch ends after this one record.
		work := &ResourceRead{store: s}
		defer work.Close()
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

// decodeIssueSuggestionPreimages decodes the stored payloads of keys in order
// as issueMutationRows decodes a row: the decoding work is admitted while it
// runs, and each decoded issue stays admitted on owner by its measured size.
func decodeIssueSuggestionPreimages(ctx context.Context, owner *ResourceRead, keys []string, raw map[string][]byte, keep func(domain.Issue) bool) ([]domain.Issue, error) {
	size := uint64(reflect.TypeFor[domain.Issue]().Size())
	issues := []domain.Issue{}
	fixed := size*2 + 4096
	for _, id := range keys {
		body := raw[id]
		work, err := metadataAggregatePayloadJSONWork(ctx, body)
		if err != nil {
			return nil, err
		}
		if work > metadataWorkBytes-fixed {
			return nil, ErrMetadataWorkBudget
		}
		_, release, err := owner.store.tryAdmitAdditionalMetadataWork(ctx, int(work+fixed))
		if err != nil {
			return nil, err
		}
		var issue domain.Issue
		err = json.Unmarshal(body, &issue)
		release()
		if err != nil {
			return nil, err
		}
		normalizeIssueRecord(&issue)
		if keep != nil && !keep(issue) {
			continue
		}
		retained, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(issue), 0)
		if err != nil {
			return nil, err
		}
		// The issue and its slot; the slice grows by doubling, so the
		// abandoned backing arrays are covered by a second slot.
		if err := owner.reserve(ctx, retained+size*2); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, ctx.Err()
}
