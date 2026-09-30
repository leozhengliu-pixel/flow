package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

// MutationScope names the issue and content records a workspace mutation can
// read or change. A handler attaches one with WithMutationScope when its
// callback only needs a bounded slice of the record-backed collections: the
// store then loads just those records instead of every issue, comment,
// activity and notification in the workspace. Everything else the callback
// sees (projects, releases, documents, ...) is the same metadata snapshot the
// full path uses, and the persisted result, domain event, webhook previous
// values and realtime payload are produced the same way.
//
// The callback must not delete issues, may create them only when the scope
// sets CreateIssues, and must only read or change comments, activities and
// notifications owned by the scoped resources or the loaded issues. It may
// append records for other owners; those are inserted. Loaded issues are
// presented in collection order, like the full path, so callbacks that number
// or order what they append produce the same result.
type MutationScope struct {
	// IssueIDs are issue records to load by id. Unknown ids are ignored.
	IssueIDs []string
	// IssueIdentifiers are issue records to load by identifier (TEAM-123).
	IssueIdentifiers []string
	// ProjectIssues loads the issue records assigned to these projects.
	ProjectIssues []string
	// IssueColumns loads the issue records whose indexed column matches one of
	// the values (team_id, state_id, cycle_id, parent_id, assignee_id, project_id).
	IssueColumns []IssueColumnScope
	// IssueAttributes loads the issue records with one of the values for a
	// sparse indexed attribute (for example projectMilestoneId).
	IssueAttributes []IssueColumnScope
	// LabelIssues loads the issue records carrying one of these labels.
	LabelIssues []string
	// IssueDataContains loads the issue records whose stored JSON contains one
	// of these fragments. It scans issue payloads in the database, so it is
	// only for rare background work without an index to use.
	IssueDataContains []string
	// IssueFilter, when set, keeps only the matching records loaded through
	// ProjectIssues, IssueColumns, IssueAttributes, LabelIssues and
	// IssueDataContains (records named by id or identifier are always kept).
	IssueFilter func(domain.Issue) bool
	// IssueContains, when set, skips those filtered records whose stored JSON
	// does not contain it before decoding them (IssueFilter still decides).
	IssueContains string
	// StatusAutomation also loads what status workflow automations can touch
	// when an issue named by id or identifier changes status: its parent, its
	// sub-issues, its siblings, and the first and last issue of every status
	// of its team (for "move to top/bottom" ordering).
	StatusAutomation bool
	// AllIssues declares that the callback may touch any issue: the mutation
	// runs on the full workspace path.
	AllIssues bool
	// CreateIssues lets the callback add issues. New records keep the
	// callback's order relative to the loaded ones: records placed before
	// them are prepended to the workspace collection, others appended.
	CreateIssues bool
	// Resources own comments, activities and notifications the callback may
	// read or change, in addition to the loaded issues.
	Resources []string
	// Resolve extends the scope from the locked metadata snapshot, for scopes
	// that depend on current state (for example a release's issues).
	Resolve func(domain.Bootstrap) MutationScope
	// Expand extends the scope once more from the loaded issues (in
	// collection order), for scopes that depend on their current values.
	Expand func([]domain.Issue) MutationScope
}

// IssueColumnScope names an indexed issue column (or sparse attribute field)
// and the values to match.
type IssueColumnScope struct {
	Column string
	Values []string
}

// merge adds extra's sources to scope. Filters in extra replace scope's.
func (scope MutationScope) merge(extra MutationScope) MutationScope {
	scope.IssueIDs = append(slices.Clone(scope.IssueIDs), extra.IssueIDs...)
	scope.IssueIdentifiers = append(slices.Clone(scope.IssueIdentifiers), extra.IssueIdentifiers...)
	scope.ProjectIssues = append(slices.Clone(scope.ProjectIssues), extra.ProjectIssues...)
	scope.IssueColumns = append(slices.Clone(scope.IssueColumns), extra.IssueColumns...)
	scope.IssueAttributes = append(slices.Clone(scope.IssueAttributes), extra.IssueAttributes...)
	scope.LabelIssues = append(slices.Clone(scope.LabelIssues), extra.LabelIssues...)
	scope.IssueDataContains = append(slices.Clone(scope.IssueDataContains), extra.IssueDataContains...)
	scope.Resources = append(slices.Clone(scope.Resources), extra.Resources...)
	scope.StatusAutomation = scope.StatusAutomation || extra.StatusAutomation
	scope.CreateIssues = scope.CreateIssues || extra.CreateIssues
	scope.AllIssues = scope.AllIssues || extra.AllIssues
	if extra.IssueFilter != nil {
		scope.IssueFilter = extra.IssueFilter
	}
	if extra.IssueContains != "" {
		scope.IssueContains = extra.IssueContains
	}
	if extra.Expand != nil {
		scope.Expand = extra.Expand
	}
	return scope
}

// maxScopedIssues bounds the records a scoped mutation loads. Larger scopes
// fall back to the full workspace path, which remains correct.
const maxScopedIssues = 20000

type mutationScopeKey struct{}

var fullMutationsForced atomic.Bool

// ForceFullMutationsForTesting routes every mutation that would use a
// metadata-only or scoped fast path through the full workspace path, so tests
// can check that both produce the same result. It returns a restore func.
func ForceFullMutationsForTesting(force bool) func() {
	previous := fullMutationsForced.Swap(force)
	return func() { fullMutationsForced.Store(previous) }
}

// WithMutationScope attaches a bounded record scope to a workspace mutation.
func WithMutationScope(ctx context.Context, scope MutationScope) context.Context {
	return context.WithValue(ctx, mutationScopeKey{}, scope)
}

func mutationScopeFromContext(ctx context.Context) (MutationScope, bool) {
	if fullMutationsForced.Load() {
		return MutationScope{}, false
	}
	scope, ok := ctx.Value(mutationScopeKey{}).(MutationScope)
	return scope, ok
}

var errScopeTooLarge = errors.New("mutation scope exceeds the scoped record limit")

func (s *SQLiteStore) mutateScoped(ctx context.Context, workspaceKey, eventType string, payload any, scope MutationScope, mutate func(*domain.Bootstrap) (string, error)) error {
	if workspaceKey == "" {
		s.mu.RLock()
		workspaceKey = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	var realtimePayload json.RawMessage
	var changedIssues []string
	webhookEnabled := s.webhookConfigured() && s.webhookNeeded(workspaceKey)
	traceStart(ctx, "scoped")
	apply := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		if s.coordinator != nil {
			latest, err := s.loadWorkspaceState(ctx, workspaceKey)
			if err != nil {
				return fmt.Errorf("reload workspace before mutation: %w", err)
			}
			s.workspaces[workspaceKey] = latest
		}
		stored, ok := s.workspaces[workspaceKey]
		if !ok {
			return fmt.Errorf("workspace %q: %w", workspaceKey, errors.New("not found"))
		}
		current := cloneBootstrap(collectionMetadata(stored))
		if scope.Resolve != nil {
			scope = scope.merge(scope.Resolve(current))
		}
		if scope.AllIssues || len(scope.IssueIDs) > maxScopedIssues {
			return errScopeTooLarge
		}
		// Resolve the actor's role before opening the transaction: SQLite
		// pools can hold a single connection.
		actor, hasActor := actorFromContext(ctx)
		actorRole := ""
		if hasActor {
			if role, status, roleErr := s.WorkspaceRole(ctx, current.Workspace.ID, actor.ID); roleErr == nil && status == "active" {
				actorRole = role
			}
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		records, order, err := s.loadScopedIssues(ctx, tx, workspaceKey, scope, current)
		if err != nil {
			return err
		}
		if scope.Expand != nil {
			issues := make([]domain.Issue, 0, len(order))
			for _, id := range order {
				issues = append(issues, records[id].issue)
			}
			extra := scope.Expand(issues)
			extra.Resolve, extra.Expand = nil, nil
			more, _, err := s.loadScopedIssues(ctx, tx, workspaceKey, extra, current)
			if err != nil {
				return err
			}
			for id, record := range more {
				records[id] = record
			}
			if extra.AllIssues || len(records) > maxScopedIssues {
				return errScopeTooLarge
			}
			order = sortedKeys(records)
			sort.SliceStable(order, func(i, j int) bool { return records[order[i]].order < records[order[j]].order })
			scope.Resources = append(scope.Resources, extra.Resources...)
			scope.CreateIssues = scope.CreateIssues || extra.CreateIssues
		}
		loaded := make(map[string]domain.Issue, len(records))
		payloads := make(map[string][]byte, len(records))
		current.Issues = make([]domain.Issue, 0, len(records))
		for _, id := range order {
			loaded[id], payloads[id] = records[id].issue, records[id].raw
			current.Issues = append(current.Issues, records[id].issue)
		}
		keys := scopedResourceKeys(scope.Resources, loaded)
		nextIssueNumber := current.NextIssueNumber
		if scope.CreateIssues {
			// Number new issues from the durable sequence, as issue creation does.
			if err := writeIssueNumber(ctx, tx, workspaceKey, 0); err != nil {
				return err
			}
			lock := ""
			if s.dialect != "sqlite" {
				lock = " FOR UPDATE"
			}
			var last int
			if err := tx.QueryRowContext(ctx, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`+lock, workspaceKey).Scan(&last); err != nil {
				return err
			}
			nextIssueNumber = max(nextIssueNumber, last+1)
		}
		if current.Comments, err = scopedContentChunked[domain.Comment](ctx, tx, workspaceKey, "comment", keys); err != nil {
			return err
		}
		if current.Activities, err = scopedContentChunked[domain.ActivityEvent](ctx, tx, workspaceKey, "activity", keys); err != nil {
			return err
		}
		notifications, err := scopedContentChunked[domain.Notification](ctx, tx, workspaceKey, "notification", keys)
		if err != nil {
			return err
		}
		current.Notifications = []domain.Notification{}
		for _, key := range sortedKeys(notifications) {
			current.Notifications = append(current.Notifications, notifications[key]...)
		}
		if current.NotificationDeliveries, err = issueNotificationDeliveries(ctx, tx, workspaceKey, current.Notifications); err != nil {
			return err
		}
		loadedDeliveries := map[string]bool{}
		for _, delivery := range current.NotificationDeliveries {
			loadedDeliveries[delivery.ID] = true
		}
		// The loaded records are owned by this transaction; only webhook
		// before/after comparison needs a second copy.
		next := current
		if webhookEnabled {
			next = cloneBootstrap(current)
		}
		// Snapshot clones drop json:"-" fields such as the next issue number.
		next.NextIssueNumber = nextIssueNumber
		refreshDisplayReferences(&next)
		refreshIssueReferences(&next)
		originalViewerRole := next.ViewerRole
		if hasActor {
			next.Viewer = actor
			if actorRole != "" {
				next.ViewerRole = actorRole
			}
			if index := slices.IndexFunc(next.Users, func(user domain.User) bool { return user.ID == actor.ID }); index >= 0 {
				next.Users[index] = actor
			} else {
				next.Users = append(next.Users, actor)
			}
		}
		startDates := map[string]string{}
		for _, project := range next.Projects {
			startDates[project.ID] = optionalValue(project.StartDate)
		}
		traceMark(ctx, "load")
		aggregateID, err := mutate(&next)
		next.ViewerRole = originalViewerRole
		traceMark(ctx, "mutate")
		if err != nil {
			return err
		}
		previousValues := json.RawMessage(nil)
		if webhookEnabled {
			previousValues = aggregatePreviousValues(current, next, aggregateID)
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", time.Now().UnixNano()), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, PreviousValues: previousValues, CreatedAt: time.Now().UTC()}
		realtimePayload = enrichRealtimePayload(payloadRaw, aggregateJSONValue(next, aggregateID), eventType)
		written, err := s.persistScopedRecords(ctx, tx, workspaceKey, next, loaded, payloads, scope.CreateIssues, keys, loadedDeliveries)
		if err != nil {
			return err
		}
		changedIssues = written
		// Keep project progress current for issues this write moved (release
		// completion automations) and rebuild it for new start dates.
		now := time.Now().UTC()
		before := make([]domain.Issue, 0, len(loaded))
		for _, id := range order {
			before = append(before, loaded[id])
		}
		if _, err := refreshIssueProjectProgress(ctx, tx, workspaceKey, &next, before, next.Issues, now); err != nil {
			return err
		}
		states := progressStates(next)
		for index := range next.Projects {
			project := &next.Projects[index]
			if previous, ok := startDates[project.ID]; ok && previous != optionalValue(project.StartDate) {
				if _, err := rebuildProjectProgress(ctx, tx, workspaceKey, &next, project, states, now); err != nil {
					return err
				}
			}
		}
		metadata := collectionMetadata(next)
		if err := s.persistWorkspaceTx(ctx, tx, workspaceKey, &stored, metadata, nil, &event); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		traceMark(ctx, "persist")
		s.dropMetadataCache(ctx, workspaceKey)
		domain.RebuildTeamDirectory(&metadata)
		s.workspaces[workspaceKey] = metadata
		s.lastWorkspaceKey = workspaceKey
		return nil
	}
	var err error
	if s.coordinator != nil {
		err = s.coordinator.WithWorkspaceLock(ctx, workspaceKey, apply)
	} else {
		err = apply()
	}
	if errors.Is(err, errScopeTooLarge) {
		return s.mutateFull(ctx, workspaceKey, eventType, payload, mutate)
	}
	if err != nil {
		if errors.Is(err, ErrNoMutation) {
			return nil
		}
		return err
	}
	if len(changedIssues) > 0 {
		s.invalidateIssueRecords(ctx, workspaceKey, changedIssues)
	}
	s.publishMutation(ctx, workspaceKey, event, realtimePayload)
	return nil
}

// invalidateIssueRecords drops cached copies of issue records a non-issue
// event rewrote (for example cycle or label automations) and retires cached
// issue query pages.
func (s *SQLiteStore) invalidateIssueRecords(ctx context.Context, workspace string, ids []string) {
	cache := s.hotCache()
	if cache == nil {
		return
	}
	for _, id := range ids {
		s.cacheDropIssue(ctx, workspace, id)
	}
	_, _ = cache.CacheIncr(ctx, s.issueQueryGenKey(workspace))
}

// loadScopedIssues loads the scope's issue records inside tx. It resolves
// every source to ids through indexed lookups first, then reads the records in
// batches, and returns them with their ids in collection order (the order the
// full workspace path presents issues to callbacks).
func (s *SQLiteStore) loadScopedIssues(ctx context.Context, tx *sqlTx, workspace string, scope MutationScope, metadata domain.Bootstrap) (map[string]storedIssueRecord, []string, error) {
	direct := slices.Clone(scope.IssueIDs)
	filtered, scanned := []string{}, []string{}
	collect := func(target *[]string, query string, args ...any) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			*target = append(*target, id)
			if len(*target) > maxScopedIssues {
				return errScopeTooLarge
			}
		}
		return rows.Err()
	}
	chunks := func(values []string, each func([]string) error) error {
		values = slices.DeleteFunc(slices.Clone(values), func(value string) bool { return value == "" })
		for start := 0; start < len(values); start += 500 {
			if err := each(values[start:min(start+500, len(values))]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := chunks(scope.IssueIdentifiers, func(values []string) error {
		clause, args := bindList("identifier", values)
		return collect(&direct, `SELECT id FROM issue_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
	}); err != nil {
		return nil, nil, err
	}
	columns := slices.Clone(scope.IssueColumns)
	if len(scope.ProjectIssues) > 0 {
		columns = append(columns, IssueColumnScope{Column: "project_id", Values: scope.ProjectIssues})
	}
	for _, column := range columns {
		switch column.Column {
		case "team_id", "state_id", "cycle_id", "parent_id", "assignee_id", "project_id":
		default:
			return nil, nil, fmt.Errorf("unsupported scoped issue column %q", column.Column)
		}
		if err := chunks(column.Values, func(values []string) error {
			clause, args := bindList(column.Column, values)
			return collect(&filtered, `SELECT id FROM issue_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
		}); err != nil {
			return nil, nil, err
		}
	}
	for _, attribute := range scope.IssueAttributes {
		if !isIssueAttributeField(attribute.Column) {
			return nil, nil, fmt.Errorf("unsupported scoped issue attribute %q", attribute.Column)
		}
		if err := chunks(attribute.Values, func(values []string) error {
			clause, args := bindList("value", values)
			return collect(&filtered, `SELECT issue_id FROM issue_attribute_records WHERE workspace_key=? AND field=? AND `+clause, append([]any{workspace, attribute.Column}, args...)...)
		}); err != nil {
			return nil, nil, err
		}
	}
	if err := chunks(scope.LabelIssues, func(values []string) error {
		clause, args := bindList("label_id", values)
		return collect(&filtered, `SELECT issue_id FROM issue_label_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
	}); err != nil {
		return nil, nil, err
	}
	for _, fragment := range scope.IssueDataContains {
		if fragment == "" {
			continue
		}
		// LIKE may over-match (wildcards, case folding); IssueContains-style
		// verification below keeps only exact fragment matches.
		column, pattern := "data", any("%"+fragment+"%")
		switch s.dialect {
		case "sqlite":
			column = "CAST(data AS TEXT)"
		case "postgres":
			pattern = []byte("%" + fragment + "%")
		}
		if err := collect(&scanned, `SELECT id FROM issue_records WHERE workspace_key=? AND `+column+` LIKE ?`, workspace, pattern); err != nil {
			return nil, nil, err
		}
	}
	if len(direct)+len(filtered)+len(scanned) > maxScopedIssues {
		return nil, nil, errScopeTooLarge
	}
	records, err := s.loadIssueRecordsByID(ctx, tx, workspace, slices.Concat(direct, filtered, scanned), true)
	if err != nil {
		return nil, nil, err
	}
	keep := map[string]bool{}
	for _, id := range direct {
		keep[id] = true
	}
	accept := func(id string, fragmentRequired bool) {
		record, ok := records[id]
		if !ok || keep[id] {
			return
		}
		if fragmentRequired && !slices.ContainsFunc(scope.IssueDataContains, func(fragment string) bool {
			return fragment != "" && bytes.Contains(record.raw, []byte(fragment))
		}) {
			return
		}
		if scope.IssueContains != "" && !bytes.Contains(record.raw, []byte(scope.IssueContains)) {
			return
		}
		if scope.IssueFilter != nil && !scope.IssueFilter(record.issue) {
			return
		}
		keep[id] = true
	}
	for _, id := range filtered {
		accept(id, false)
	}
	for _, id := range scanned {
		accept(id, true)
	}
	for id := range records {
		if !keep[id] {
			delete(records, id)
		}
	}
	if scope.StatusAutomation {
		family, err := s.statusAutomationIssueIDs(ctx, tx, workspace, direct, records, metadata)
		if err != nil {
			return nil, nil, err
		}
		extra, err := s.loadIssueRecordsByID(ctx, tx, workspace, family, true)
		if err != nil {
			return nil, nil, err
		}
		for id, record := range extra {
			records[id] = record
		}
	}
	if len(records) > maxScopedIssues {
		return nil, nil, errScopeTooLarge
	}
	order := sortedKeys(records)
	sort.SliceStable(order, func(i, j int) bool { return records[order[i]].order < records[order[j]].order })
	return records, order, nil
}

// statusAutomationIssueIDs lists the parent, sub-issues and siblings of the
// named issues, and the first and last issue of each status their teams use.
func (s *SQLiteStore) statusAutomationIssueIDs(ctx context.Context, tx *sqlTx, workspace string, ids []string, records map[string]storedIssueRecord, metadata domain.Bootstrap) ([]string, error) {
	result := []string{}
	parents := []string{}
	teams := map[string]bool{}
	for _, id := range ids {
		record, ok := records[id]
		if !ok {
			continue
		}
		parents = append(parents, id)
		if record.issue.ParentID != nil && *record.issue.ParentID != "" {
			result = append(result, *record.issue.ParentID)
			parents = append(parents, *record.issue.ParentID)
		}
		teams[record.issue.Team.ID] = true
	}
	for start := 0; start < len(parents); start += 500 {
		clause, args := bindList("parent_id", parents[start:min(start+500, len(parents))])
		rows, err := tx.QueryContext(ctx, `SELECT id FROM issue_records WHERE workspace_key=? AND `+clause+` ORDER BY id LIMIT 5001`, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			result = append(result, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	for team := range teams {
		// A team's issues use its own statuses, the shared legacy statuses, or
		// (when inheriting) an ancestor's.
		owners := map[string]bool{team: true, "": true}
		seen := map[string]bool{team: true}
		for parent := metadata.TeamSettings[team].ParentTeamID; parent != "" && !seen[parent]; parent = metadata.TeamSettings[parent].ParentTeamID {
			seen[parent], owners[parent] = true, true
		}
		for _, state := range metadata.States {
			if !owners[state.TeamID] {
				continue
			}
			for _, direction := range []string{"ASC", "DESC"} {
				var id string
				err := tx.QueryRowContext(ctx, `SELECT id FROM issue_records WHERE workspace_key=? AND team_id=? AND state_id=? ORDER BY sort_order `+direction+`,id `+direction+` LIMIT 1`, workspace, team, state.ID).Scan(&id)
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				if err != nil {
					return nil, err
				}
				result = append(result, id)
			}
		}
	}
	return slices.DeleteFunc(result, func(id string) bool { _, loaded := records[id]; return loaded }), nil
}

func scopedResourceKeys(resources []string, issues map[string]domain.Issue) []string {
	seen := map[string]bool{}
	keys := []string{}
	for _, key := range resources {
		if key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	for _, id := range sortedKeys(issues) {
		if !seen[id] {
			seen[id] = true
			keys = append(keys, id)
		}
	}
	return keys
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func scopedContentChunked[T any](ctx context.Context, tx *sqlTx, workspace, kind string, keys []string) (map[string][]T, error) {
	result := map[string][]T{}
	for start := 0; start < len(keys); start += 500 {
		chunk, err := scopedContent[T](ctx, tx, workspace, kind, keys[start:min(start+500, len(keys))])
		if err != nil {
			return nil, err
		}
		for key, values := range chunk {
			result[key] = values
		}
	}
	return result, nil
}

// persistScopedRecords writes the loaded issues that changed (and, when the
// scope allows it, the issues the callback created) and syncs the scoped
// comments, activities, notifications and deliveries. Records outside the
// scope are only ever inserted, never deleted. It returns the issue ids it
// wrote.
func (s *SQLiteStore) persistScopedRecords(ctx context.Context, tx *sqlTx, workspace string, data domain.Bootstrap, loaded map[string]domain.Issue, stored map[string][]byte, create bool, keys []string, loadedDeliveries map[string]bool) ([]string, error) {
	metadata := collectionMetadata(data)
	remaining := map[string]bool{}
	created := []domain.Issue{}
	prepended := 0
	firstLoaded := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { _, ok := loaded[issue.ID]; return ok })
	for index, issue := range data.Issues {
		if remaining[issue.ID] {
			// Only the full path's collection replacement handles duplicates.
			return nil, errScopeTooLarge
		}
		remaining[issue.ID] = true
		if _, ok := loaded[issue.ID]; ok {
			continue
		}
		if !create {
			return nil, fmt.Errorf("scoped mutation cannot create issue %q", issue.ID)
		}
		if firstLoaded < 0 || index < firstLoaded {
			prepended++
		}
		created = append(created, issue)
	}
	for id := range loaded {
		if !remaining[id] {
			return nil, fmt.Errorf("scoped mutation cannot delete issue %q", id)
		}
	}
	written, err := s.writeIssueRecordBatch(ctx, tx, workspace, data.Issues, metadata, stored)
	if err != nil {
		return nil, err
	}
	if len(created) > 0 {
		// Mirror the full path's collection order: records the callback put in
		// front of every loaded one go below the current minimum, the rest
		// after the current maximum.
		var low, high sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MIN(collection_order),MAX(collection_order) FROM issue_records WHERE workspace_key=? AND id NOT IN (`+strings.TrimSuffix(strings.Repeat("?,", len(created)), ",")+`)`, append([]any{workspace}, issueIDArgs(created)...)...).Scan(&low, &high); err != nil {
			return nil, err
		}
		maxNumber := 0
		for index, issue := range created {
			position := high.Int64 + int64(index-prepended) + 1
			if index < prepended {
				position = low.Int64 - int64(prepended) + int64(index)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET collection_order=? WHERE workspace_key=? AND id=?`, position, workspace, issue.ID); err != nil {
				return nil, err
			}
			maxNumber = max(maxNumber, issue.Number)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO issue_collection_counts(workspace_key,total) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET total=issue_collection_counts.total+excluded.total`, workspace, len(created)); err != nil {
			return nil, err
		}
		if err := writeIssueNumber(ctx, tx, workspace, maxNumber); err != nil {
			return nil, err
		}
	}
	for start := 0; start < max(len(keys), 1); start += 500 {
		chunk := keys[start:min(start+500, len(keys))]
		comments, activities, notifications := data.Comments, data.Activities, notificationRecords(data.Notifications)
		if start > 0 {
			// New records outside every chunk are written with the first one.
			comments, activities, notifications = pick(comments, chunk), pick(activities, chunk), pick(notifications, chunk)
		} else if len(keys) > 500 {
			comments, activities, notifications = omit(comments, keys[500:]), omit(activities, keys[500:]), omit(notifications, keys[500:])
		}
		if err := syncContentRecords(ctx, tx, workspace, "comment", comments, metadata, chunk); err != nil {
			return nil, err
		}
		if err := syncContentRecords(ctx, tx, workspace, "activity", activities, metadata, chunk); err != nil {
			return nil, err
		}
		if err := syncContentRecords(ctx, tx, workspace, "notification", notifications, metadata, chunk); err != nil {
			return nil, err
		}
	}
	for _, delivery := range data.NotificationDeliveries {
		delete(loadedDeliveries, delivery.ID)
		if err := writeContentRecord(ctx, tx, workspace, "delivery", "", delivery); err != nil {
			return nil, err
		}
	}
	for id := range loadedDeliveries {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`, workspace, id); err != nil {
			return nil, err
		}
	}
	return written, nil
}

func issueIDArgs(issues []domain.Issue) []any {
	args := make([]any, len(issues))
	for index, issue := range issues {
		args[index] = issue.ID
	}
	return args
}

func pick[T any](values map[string][]T, keys []string) map[string][]T {
	result := map[string][]T{}
	for _, key := range keys {
		if items, ok := values[key]; ok {
			result[key] = items
		}
	}
	return result
}

func omit[T any](values map[string][]T, keys []string) map[string][]T {
	skip := map[string]bool{}
	for _, key := range keys {
		skip[key] = true
	}
	result := map[string][]T{}
	for key, items := range values {
		if !skip[key] {
			result[key] = items
		}
	}
	return result
}
