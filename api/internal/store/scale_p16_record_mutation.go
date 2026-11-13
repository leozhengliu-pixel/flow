package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type projectRecordSelectionKey struct{}
type ProjectRecordSelector func(domain.Bootstrap) (MetadataRecordScope, bool)

func WithProjectRecordSelection(ctx context.Context, selectScope ProjectRecordSelector) context.Context {
	return context.WithValue(ctx, projectRecordSelectionKey{}, selectScope)
}

func projectRecordSelectionEnabled(ctx context.Context) bool {
	_, ok := ctx.Value(projectRecordSelectionKey{}).(ProjectRecordSelector)
	return ok && RecordHydrationEnabled() && !fullMutationsForced.Load() && !metadataRecordVerificationEnabled(ctx)
}

func (s *SQLiteStore) mutateProjectRecordSelection(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	var realtimePayload json.RawMessage
	selectScope := ctx.Value(projectRecordSelectionKey{}).(ProjectRecordSelector)
	webhookEnabled := s.webhookConfigured() && s.webhookNeeded(ctx, workspace)
	apply := func(ctx context.Context) error {
		if !metadataOnlyMutation(eventType, payload) && eventType != "project.deleted" {
			return errProjectRecordFallback
		}
		contentScope, scopedContent := mutationScopeFromContext(ctx)
		if scopedContent && eventType != "project.deleted" && !scaleP16ResourceScopeOnly(contentScope) {
			return errProjectRecordFallback
		}
		s.lockWorkspaceWrites()
		defer s.unlockWorkspaceWrites()
		if s.coordinator != nil {
			if err := s.refreshWorkspaceCopyLocked(ctx, workspace); err != nil {
				return err
			}
		}
		stored, ok := s.workspaces[workspace]
		if !ok {
			return ErrAuthForbidden
		}
		scope, selected := selectScope(stored)
		if !selected {
			return errProjectRecordFallback
		}
		if err := validateMetadataRecordScope(scope); err != nil {
			return errProjectRecordFallback
		}
		if err := validateMetadataRecordSelection(stored, scope); err != nil {
			return err
		}
		base, known := s.copyRevisionLocked(workspace)
		ctx, copyWrite := s.watchMetadataWriteFrom(ctx, workspace, base, known)
		before := scaleP16ProjectSnapshot(stored, scope)
		next := cloneCallbackInput(before)
		refreshDisplayReferences(&next)
		originalRole := next.ViewerRole
		if actor, ok := actorFromContext(ctx); ok {
			next.Viewer = actor
			if role, status, err := s.WorkspaceRole(ctx, stored.Workspace.ID, actor.ID); err == nil && status == "active" {
				next.ViewerRole = role
				next.ViewerTeamIDs = s.actorTeamIDs(ctx, stored.Workspace.ID, actor.ID)
			}
			if i := slices.IndexFunc(next.Users, func(u domain.User) bool { return u.ID == actor.ID }); i >= 0 {
				next.Users[i] = actor
			} else {
				next.Users = append(next.Users, actor)
			}
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var resourceKeys []string
		var loadedDeliveries map[string]bool
		if scopedContent && eventType != "project.deleted" {
			resourceKeys = scopedResourceKeys(contentScope.Resources, nil)
			loadedDeliveries, err = scaleP16LoadResourceContent(ctx, tx, workspace, resourceKeys, &next)
			if err != nil {
				return err
			}
		}
		next.FormerMembers = s.formerMemberLookup(ctx, tx, &next)
		next.CollaborationUpdates = collaborationUpdatesLookup(ctx, tx, workspace)
		aggregate, err := mutate(&next)
		next.ViewerRole, next.ViewerTeamIDs, next.FormerMembers, next.CollaborationUpdates = originalRole, nil, nil, nil
		if err != nil {
			return err
		}
		scaleP16RestoreReadRecords(&next, before, scope)
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		event = domain.DomainEvent{ID: idgen.New("evt_"), Type: eventType, AggregateID: aggregate, Payload: raw, CreatedAt: time.Now().UTC()}
		if webhookEnabled {
			event.PreviousValues = aggregatePreviousValues(before, next, aggregate)
		}
		realtimePayload = enrichRealtimePayload(raw, aggregateJSONValue(next, aggregate), eventType)
		var cascades []ProjectRecordCascade
		if plan, ok := ctx.Value(projectRecordCascadesKey{}).(ProjectRecordCascadePlan); ok {
			cascades = plan(&stored, &before, &next)
		}
		for _, cascade := range cascades {
			if err := s.scaleP16RunCascade(ctx, tx, workspace, &stored, cascade); err != nil {
				return err
			}
		}
		event.CreatedAt = time.Now().UTC()
		if scopedContent && eventType != "project.deleted" {
			if _, err := s.persistScopedRecords(ctx, tx, workspace, next, nil, nil, false, resourceKeys, loadedDeliveries); err != nil {
				return err
			}
		}
		if _, _, err := s.rebaseMetadata(ctx, tx, workspace, &before, &next, changedMetadataFields(before, next), nil); err != nil {
			return err
		}
		if scopedContent && eventType == "project.updated" {
			states := progressStates(next)
			for index := range next.Projects {
				project := &next.Projects[index]
				for _, old := range before.Projects {
					if old.ID == project.ID && optionalValue(old.StartDate) != optionalValue(project.StartDate) {
						if _, err := rebuildProjectProgress(ctx, tx, workspace, &next, project, states, time.Now().UTC()); err != nil {
							return err
						}
					}
				}
			}
		}
		changes, root, err := scaleP16RecordChanges(before, next)
		if err != nil {
			return err
		}
		if err := scaleP16WriteChanges(ctx, tx, workspace, newIssueReferences(next), changes); err != nil {
			return err
		}
		if len(root) > 0 {
			if err := scaleP16WriteRoot(ctx, tx, workspace, root); err != nil {
				return err
			}
		}
		previous, err := storedEventPreviousValues(ctx, tx, workspace, eventType, aggregate, event.PreviousValues)
		if err != nil {
			return err
		}
		if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event.ID, Type: eventType, AggregateID: aggregate, Payload: raw, PreviousValues: previous, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), WorkspaceID: next.Workspace.ID, WorkspaceKey: workspace}); err != nil {
			return err
		}
		viewer, _ := json.Marshal(s.viewer)
		if err := writeAccountMetadata(ctx, tx, workspace, viewer); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		invalidateCopy := func(err error) error {
			s.setWorkspaceLocked(workspace, stored)
			s.lastWorkspaceKey = workspace
			slog.Error("project record copy invalidated after commit", "error", err)
			return nil
		}
		for _, cascade := range cascades {
			if err := s.scaleP16InstallCascade(ctx, workspace, &stored, cascade); err != nil {
				return invalidateCopy(err)
			}
		}
		if err := scaleP16InstallChanges(&stored, next, changes); err != nil {
			return invalidateCopy(err)
		}
		for _, key := range scope.Write {
			if key.Kind == "projects" && slices.Contains(scope.Fields, "projectUpdates") {
				if updates, ok := next.ProjectUpdates[key.ID]; ok {
					stored.ProjectUpdates[key.ID] = updates
				} else {
					delete(stored.ProjectUpdates, key.ID)
				}
			}
			if key.Kind == "initiatives" && slices.Contains(scope.Fields, "initiativeUpdates") {
				if updates, ok := next.InitiativeUpdates[key.ID]; ok {
					stored.InitiativeUpdates[key.ID] = updates
				} else {
					delete(stored.InitiativeUpdates, key.ID)
				}
			}
		}
		s.installWrittenWorkspaceLocked(workspace, stored, copyWrite)
		s.lastWorkspaceKey = workspace
		return nil
	}
	err := mutationAttempts(ctx, fullMutationAttempts, func(ctx context.Context) error {
		if s.coordinator != nil {
			return s.coordinator.WithWorkspaceLock(ctx, workspace, func() error { return apply(ctx) })
		}
		return apply(ctx)
	}, s.refreshConflictedMetadata(ctx, workspace))
	if errors.Is(err, errProjectRecordFallback) {
		ctx = context.WithValue(ctx, projectRecordSelectionKey{}, nil)
		if scope, scoped := metadataRecordScopeFromContext(ctx); scoped {
			ctx = WithMetadataFields(ctx, metadataRecordFields(scope)...)
			callback := metadataRecordMutationAdapter(ctx, mutate)
			// The collector is already inside this fallback's adapter. Restore
			// its original staging buffer just as the normal outer collector does.
			mutate = func(data *domain.Bootstrap) (string, error) {
				audit := data.AuditLog
				id, err := callback(data)
				data.AuditLog = audit
				return id, err
			}
		}
		return s.dispatchMutation(ctx, workspace, eventType, payload, mutate)
	}
	if errors.Is(err, ErrNoMutation) {
		return nil
	}
	if err != nil {
		return err
	}
	s.dropMetadataCache(ctx, workspace)
	s.publishMutation(ctx, workspace, event, realtimePayload)
	return nil
}

var errProjectRecordFallback = errors.New("project callback requires canonical inputs")

func scaleP16RestoreReadRecords(after *domain.Bootstrap, before domain.Bootstrap, scope MetadataRecordScope) {
	for _, key := range scope.Read {
		if metadataRecordSelected(scope, key.Kind, key.ID, true) {
			continue
		}
		field, _, ok := metadataRecordType(key.Kind)
		if !ok {
			continue
		}
		old, next := reflect.ValueOf(&before).Elem().Field(field.index), reflect.ValueOf(after).Elem().Field(field.index)
		if old.Kind() == reflect.Slice {
			for i := 0; i < old.Len(); i++ {
				id, _ := metadataRecordValueKey(old.Index(i))
				if id != key.ID {
					continue
				}
				found := false
				for j := 0; j < next.Len(); j++ {
					id, _ := metadataRecordValueKey(next.Index(j))
					if id == key.ID {
						next.Index(j).Set(old.Index(i))
						found = true
						break
					}
				}
				if !found {
					next.Set(reflect.Append(next, old.Index(i)))
				}
			}
		} else if old.Kind() == reflect.Map && !next.IsNil() {
			k := reflect.ValueOf(key.ID).Convert(old.Type().Key())
			next.SetMapIndex(k, old.MapIndex(k))
		}
	}
}

func scaleP16ProjectSnapshot(source domain.Bootstrap, scope MetadataRecordScope) domain.Bootstrap {
	fields := metadataRecordFields(scope)
	scope.Fields = slices.DeleteFunc(slices.Clone(scope.Fields), func(field string) bool { return field == "projectUpdates" || field == "initiativeUpdates" })
	result := metadataRecordSnapshot(source, scope, fields)
	for _, key := range scope.Write {
		if key.Kind == "projects" && slices.Contains(fields, "projectUpdates") {
			result.ProjectUpdates = map[string][]domain.ProjectUpdate{}
			if updates, ok := source.ProjectUpdates[key.ID]; ok {
				result.ProjectUpdates[key.ID] = cloneSnapshotValue(reflect.ValueOf(updates)).Interface().([]domain.ProjectUpdate)
			}
		}
		if key.Kind == "initiatives" && slices.Contains(fields, "initiativeUpdates") {
			result.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
			if updates, ok := source.InitiativeUpdates[key.ID]; ok {
				result.InitiativeUpdates[key.ID] = cloneSnapshotValue(reflect.ValueOf(updates)).Interface().([]domain.InitiativeUpdate)
			}
		}
	}
	return result
}

func scaleP16WriteChanges(ctx context.Context, tx *sqlTx, workspace string, refs issueReferences, changes []metadataRecordChange) error {
	slices.SortFunc(changes, func(a, b metadataRecordChange) int {
		if c := strings.Compare(a.field, b.field); c != 0 {
			return c
		}
		return strings.Compare(a.key, b.key)
	})
	for index := range changes {
		change := &changes[index]
		key := metadataRecordKey{change.field, change.key}
		if change.drop {
			if err := deleteMetadataRecordRow(ctx, tx, workspace, key); err != nil {
				return err
			}
			continue
		}
		raw, write, err := rebaseMetadataRecordWrite(ctx, tx, workspace, refs, change.field, change.key, change.base, change.raw)
		if err != nil {
			return err
		}
		if !write {
			change.skipped = true
			continue
		}
		change.merged = !bytes.Equal(raw, change.raw)
		change.raw = raw
		var order int
		var previous []byte
		err = tx.QueryRowContext(ctx, `SELECT collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, change.field, change.key).Scan(&order, &previous)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			change.order = order
		} else if !updateMetadataField(change.field) {
			bound, offset := "MAX", 1
			if change.field == "trash" || change.field == "favorites" {
				bound, offset = "MIN", -1
			}
			var existing sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT `+bound+`(collection_order) FROM workspace_metadata_records WHERE workspace_key=? AND field=?`, workspace, change.field).Scan(&existing); err != nil {
				return err
			}
			change.order = 0
			if existing.Valid {
				change.order = int(existing.Int64) + offset
			}
		}
		// Use the canonical writer: project mentions, customer filters, offload,
		// search, journal versions and authorization epochs share its hooks.
		if err := writeMetadataRecordRow(ctx, tx, workspace, key, metadataRecord{data: raw, order: change.order}, previous); err != nil {
			return err
		}
	}
	return nil
}

func scaleP16ResourceScopeOnly(scope MutationScope) bool {
	if len(scope.Resources) > MetadataRecordBatchMaxKeys {
		return false
	}
	scope.Resources = nil
	return reflect.DeepEqual(scope, MutationScope{})
}

func scaleP16LoadResourceContent(ctx context.Context, tx *sqlTx, workspace string, keys []string, next *domain.Bootstrap) (map[string]bool, error) {
	var err error
	if next.Comments, err = scopedContentChunked[domain.Comment](ctx, tx, workspace, "comment", keys); err != nil {
		return nil, err
	}
	if next.Activities, err = scopedContentChunked[domain.ActivityEvent](ctx, tx, workspace, "activity", keys); err != nil {
		return nil, err
	}
	notifications, err := scopedContentChunked[domain.Notification](ctx, tx, workspace, "notification", keys)
	if err != nil {
		return nil, err
	}
	next.Notifications = []domain.Notification{}
	for _, key := range sortedKeys(notifications) {
		next.Notifications = append(next.Notifications, notifications[key]...)
	}
	if next.NotificationDeliveries, err = issueNotificationDeliveries(ctx, tx, workspace, next.Notifications); err != nil {
		return nil, err
	}
	loaded := map[string]bool{}
	for _, delivery := range next.NotificationDeliveries {
		loaded[delivery.ID] = true
	}
	return loaded, nil
}

// Split only the owned working set; never build an index of the resident
// collection. The canonical writers retain merging, search and journal hooks.
func scaleP16RecordChanges(before, after domain.Bootstrap) ([]metadataRecordChange, map[string]json.RawMessage, error) {
	a, err := json.Marshal(collectionMetadata(before))
	if err != nil {
		return nil, nil, err
	}
	b, err := json.Marshal(collectionMetadata(after))
	if err != nil {
		return nil, nil, err
	}
	aRoot, _, old, err := splitWorkspaceMetadata(a)
	if err != nil {
		return nil, nil, err
	}
	bRoot, _, next, err := splitWorkspaceMetadata(b)
	if err != nil {
		return nil, nil, err
	}
	var changes []metadataRecordChange
	for key, item := range old {
		value, exists := next[key]
		if !exists {
			changes = append(changes, metadataRecordChange{field: key.field, key: key.key, drop: true, base: item.data})
		} else if !bytes.Equal(item.data, value.data) {
			changes = append(changes, metadataRecordChange{field: key.field, key: key.key, base: item.data, raw: value.data})
		}
	}
	for key, item := range next {
		if _, exists := old[key]; !exists {
			changes = append(changes, metadataRecordChange{field: key.field, key: key.key, insert: true, raw: item.data, order: item.order})
		}
	}
	root := map[string]json.RawMessage{}
	for key, value := range bRoot {
		if key != metadataCollectionsKey && !bytes.Equal(aRoot[key], value) {
			root[key] = value
		}
	}
	return changes, root, nil
}

func scaleP16WriteRoot(ctx context.Context, tx *sqlTx, workspace string, changes map[string]json.RawMessage) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		return err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	for key, value := range changes {
		root[key] = value
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace)
	return err
}

// Readers copy under mu, as for team point writes. Install only committed
// records while holding mu; failed callbacks and SQL never alter the copy.
func scaleP16InstallChanges(stored *domain.Bootstrap, selected domain.Bootstrap, changes []metadataRecordChange) error {
	root := reflect.ValueOf(stored).Elem()
	for _, change := range changes {
		field, kind, ok := metadataRecordType(change.field)
		if !ok {
			continue
		}
		value := root.Field(field.index)
		if value.Kind() == reflect.Slice {
			index := -1
			for i := 0; i < value.Len(); i++ {
				if id, ok := metadataRecordValueKey(value.Index(i)); ok && id == change.key {
					index = i
					break
				}
			}
			if change.drop || change.skipped {
				if index >= 0 {
					reflect.Copy(value.Slice(index, value.Len()-1), value.Slice(index+1, value.Len()))
					value.Index(value.Len() - 1).SetZero()
					value.SetLen(value.Len() - 1)
				}
				continue
			}
			old := reflect.Value{}
			if index >= 0 {
				old = value.Index(index)
			}
			record, err := decodeMetadataRecord(kind, change.raw, old)
			if err != nil {
				return err
			}
			if index >= 0 {
				value.Index(index).Set(record)
			} else if change.field == "trash" {
				items := reflect.MakeSlice(value.Type(), value.Len()+1, value.Len()+1)
				items.Index(0).Set(record)
				reflect.Copy(items.Slice(1, items.Len()), value)
				value.Set(items)
			} else {
				value.Set(reflect.Append(value, record))
			}
		} else {
			raw := change.raw
			if change.drop || change.skipped {
				raw = nil
			}
			if err := setMetadataRecords(stored, map[metadataRecordKey][]byte{{change.field, change.key}: raw}); err != nil {
				return err
			}
		}
	}
	stored.Viewer = selected.Viewer
	return nil
}
