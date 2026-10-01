package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

// userSettingsRecordMutation reports a personal settings write that only
// changes the actor's own userSettings entry. Username changes stay on the
// workspace path, which validates uniqueness against a fresh snapshot.
func userSettingsRecordMutation(eventType string, payload any) bool {
	if eventType != "user_settings.updated" || fullMutationsForced.Load() {
		return false
	}
	raw, err := json.Marshal(payload)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil {
		return false
	}
	_, username := fields["username"]
	return !username
}

// mutateUserSettingsRecord applies a personal settings write by rewriting only
// the changed userSettings metadata records, instead of cloning and
// re-encoding the whole workspace snapshot.
func (s *SQLiteStore) mutateUserSettingsRecord(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	var realtimePayload json.RawMessage
	apply := func() error {
		s.lockWorkspaceWrites()
		defer s.unlockWorkspaceWrites()
		current, ok := s.workspaces[workspace]
		if !ok {
			return fmt.Errorf("workspace %q: %w", workspace, errors.New("not found"))
		}
		actor, hasActor := actorFromContext(ctx)
		role := ""
		if hasActor {
			if value, status, err := s.WorkspaceRole(ctx, current.Workspace.ID, actor.ID); err == nil && status == "active" {
				role = value
			}
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lock := ""
		if s.dialect != "sqlite" {
			lock = " FOR UPDATE"
		}
		next := current
		next.UserSettings = maps.Clone(current.UserSettings)
		if next.UserSettings == nil {
			next.UserSettings = map[string]domain.UserSettings{}
		}
		// Another instance may have written this user's settings; start from
		// the stored record.
		if id := mutationAggregateID(ctx); id != "" {
			var raw []byte
			err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='userSettings' AND record_key=?`+lock, workspace, id).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil {
				var stored domain.UserSettings
				if err := json.Unmarshal(raw, &stored); err != nil {
					return err
				}
				next.UserSettings[id] = stored
			}
		}
		before := maps.Clone(next.UserSettings)
		if hasActor {
			next.Viewer = actor
			if role != "" {
				next.ViewerRole = role
			}
		}
		aggregateID, err := mutate(&next)
		if err != nil {
			return err
		}
		for key, value := range next.UserSettings {
			if previous, ok := before[key]; ok && reflect.DeepEqual(previous, value) {
				continue
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'userSettings',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET collection_order=excluded.collection_order,data=excluded.data`, workspace, key, raw); err != nil {
				return err
			}
			if _, existed := before[key]; !existed {
				if err := ensureOAuthMetadataShape(ctx, tx, workspace, "userSettings", "map", lock); err != nil {
					return err
				}
			}
			if err := writeCustomerFilterRecord(ctx, tx, workspace, "userSettings", key, raw); err != nil {
				return err
			}
			if err := syncMetadataSearchDocument(ctx, tx, workspace, "userSettings", key, raw); err != nil {
				return err
			}
		}
		for key := range before {
			if _, ok := next.UserSettings[key]; !ok {
				if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='userSettings' AND record_key=?`, workspace, key); err != nil {
					return err
				}
			}
		}
		payloadRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: eventType, AggregateID: aggregateID, Payload: payloadRaw, CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), []byte(nil), now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		realtimePayload = enrichRealtimePayload(payloadRaw, aggregateJSONValue(next, aggregateID), eventType)
		if err := tx.Commit(); err != nil {
			return err
		}
		s.dropMetadataCache(ctx, workspace)
		current.UserSettings = next.UserSettings
		s.workspaces[workspace] = current
		s.lastWorkspaceKey = workspace
		return nil
	}
	var err error
	if s.coordinator != nil {
		err = s.coordinator.WithWorkspaceLock(ctx, workspace, apply)
	} else {
		err = apply()
	}
	if err != nil {
		if errors.Is(err, ErrNoMutation) {
			return nil
		}
		return err
	}
	s.publishMutation(ctx, workspace, event, realtimePayload)
	return nil
}
