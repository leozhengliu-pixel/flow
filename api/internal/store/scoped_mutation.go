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
// The callback must not create or delete issues, and must only read or change
// comments, activities and notifications owned by the scoped resources or the
// loaded issues. It may append records for other owners; those are inserted.
type MutationScope struct {
	// IssueIDs are issue records to load by id. Unknown ids are ignored.
	IssueIDs []string
	// ProjectIssues loads the issue records assigned to these projects.
	ProjectIssues []string
	// IssueFilter, when set, keeps only the matching ProjectIssues records.
	IssueFilter func(domain.Issue) bool
	// IssueContains, when set, skips ProjectIssues records whose stored JSON
	// does not contain it before decoding them (IssueFilter still decides).
	IssueContains string
	// Resources own comments, activities and notifications the callback may
	// read or change, in addition to the loaded issues.
	Resources []string
	// Resolve extends the scope from the locked metadata snapshot, for scopes
	// that depend on current state (for example a release's issues).
	Resolve func(domain.Bootstrap) MutationScope
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
			extra := scope.Resolve(current)
			scope.IssueIDs = append(slices.Clone(scope.IssueIDs), extra.IssueIDs...)
			scope.ProjectIssues = append(slices.Clone(scope.ProjectIssues), extra.ProjectIssues...)
			scope.Resources = append(slices.Clone(scope.Resources), extra.Resources...)
			if extra.IssueFilter != nil {
				scope.IssueFilter = extra.IssueFilter
			}
			if extra.IssueContains != "" {
				scope.IssueContains = extra.IssueContains
			}
		}
		if len(scope.IssueIDs) > maxScopedIssues {
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
		loaded, err := s.loadScopedIssues(ctx, tx, workspaceKey, scope)
		if err != nil {
			return err
		}
		keys := scopedResourceKeys(scope.Resources, loaded)
		current.Issues = make([]domain.Issue, 0, len(loaded))
		for _, id := range sortedKeys(loaded) {
			current.Issues = append(current.Issues, loaded[id])
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
		if err := s.persistScopedRecords(ctx, tx, workspaceKey, next, loaded, keys, loadedDeliveries); err != nil {
			return err
		}
		// Keep project progress current for issues this write moved (release
		// completion automations) and rebuild it for new start dates.
		now := time.Now().UTC()
		before := make([]domain.Issue, 0, len(loaded))
		for _, id := range sortedKeys(loaded) {
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
	s.publishMutation(ctx, workspaceKey, event, realtimePayload)
	return nil
}

func (s *SQLiteStore) loadScopedIssues(ctx context.Context, tx *sqlTx, workspace string, scope MutationScope) (map[string]domain.Issue, error) {
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	loaded := map[string]domain.Issue{}
	decode := func(raw []byte) (domain.Issue, error) {
		var issue domain.Issue
		if err := json.Unmarshal(raw, &issue); err != nil {
			return issue, err
		}
		normalizeIssueRecord(&issue)
		return issue, nil
	}
	for _, id := range scope.IssueIDs {
		if _, seen := loaded[id]; seen || id == "" {
			continue
		}
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`+lock, workspace, id).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		issue, err := decode(raw)
		if err != nil {
			return nil, err
		}
		loaded[id] = issue
	}
	for _, projectID := range scope.ProjectIssues {
		if projectID == "" {
			continue
		}
		rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND project_id=?`+lock, workspace, projectID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return nil, err
			}
			if scope.IssueContains != "" && !bytes.Contains(raw, []byte(scope.IssueContains)) {
				continue
			}
			issue, err := decode(raw)
			if err != nil {
				rows.Close()
				return nil, err
			}
			if scope.IssueFilter != nil && !scope.IssueFilter(issue) {
				continue
			}
			loaded[issue.ID] = issue
			if len(loaded) > maxScopedIssues {
				rows.Close()
				return nil, errScopeTooLarge
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return loaded, nil
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

// persistScopedRecords writes the loaded issues that changed and syncs the
// scoped comments, activities, notifications and deliveries. Records outside
// the scope are only ever inserted, never deleted.
func (s *SQLiteStore) persistScopedRecords(ctx context.Context, tx *sqlTx, workspace string, data domain.Bootstrap, loaded map[string]domain.Issue, keys []string, loadedDeliveries map[string]bool) error {
	metadata := collectionMetadata(data)
	remaining := map[string]bool{}
	for _, issue := range data.Issues {
		if _, ok := loaded[issue.ID]; !ok {
			return fmt.Errorf("scoped mutation cannot create issue %q", issue.ID)
		}
		remaining[issue.ID] = true
		if err := s.writeIssueRecord(ctx, tx, workspace, issue, metadata); err != nil {
			return err
		}
	}
	for id := range loaded {
		if !remaining[id] {
			return fmt.Errorf("scoped mutation cannot delete issue %q", id)
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
			return err
		}
		if err := syncContentRecords(ctx, tx, workspace, "activity", activities, metadata, chunk); err != nil {
			return err
		}
		if err := syncContentRecords(ctx, tx, workspace, "notification", notifications, metadata, chunk); err != nil {
			return err
		}
	}
	for _, delivery := range data.NotificationDeliveries {
		delete(loadedDeliveries, delivery.ID)
		if err := writeContentRecord(ctx, tx, workspace, "delivery", "", delivery); err != nil {
			return err
		}
	}
	for id := range loadedDeliveries {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_content_records WHERE workspace_key=? AND kind='delivery' AND resource_id='' AND id=?`, workspace, id); err != nil {
			return err
		}
	}
	return nil
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
