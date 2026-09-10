package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) WorkspaceSettingsMetadata(workspace string) (domain.Bootstrap, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok {
		return domain.Bootstrap{}, false
	}
	return cloneBootstrap(domain.Bootstrap{Workspace: data.Workspace, WorkspaceSettings: data.WorkspaceSettings}), true
}

func featureFlagsOnly(payload any) bool {
	raw, err := json.Marshal(payload)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 1 {
		return false
	}
	var flags map[string]bool
	return json.Unmarshal(fields["featureFlags"], &flags) == nil && len(flags) > 0
}

// Feature toggles must not copy document bodies, review patches, project
// histories, or any of the issue/discussion collections just to save a boolean.
// The workspace-preferences callback only needs settings and intake validation.
func (s *SQLiteStore) mutateFeatureFlags(ctx context.Context, workspace string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	var event domain.DomainEvent
	apply := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		current, ok := s.workspaces[workspace]
		if !ok {
			return ErrAuthForbidden
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
		rows, err := tx.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings'`+lock, workspace)
		if err != nil {
			return err
		}
		before := map[string]json.RawMessage{}
		for rows.Next() {
			var key string
			var value []byte
			if err := rows.Scan(&key, &value); err != nil {
				rows.Close()
				return err
			}
			before[key] = value
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(before) == 0 {
			return fmt.Errorf("workspace settings records missing")
		}
		raw, err := json.Marshal(before)
		if err != nil {
			return err
		}
		next := domain.Bootstrap{Workspace: current.Workspace, Viewer: current.Viewer, EmailIntakeAddresses: current.EmailIntakeAddresses}
		if actor, ok := actorFromContext(ctx); ok {
			next.Viewer = actor
		}
		if err := json.Unmarshal(raw, &next.WorkspaceSettings); err != nil {
			return err
		}
		id, err := mutate(&next)
		if err != nil {
			return err
		}
		raw, err = json.Marshal(next.WorkspaceSettings)
		if err != nil {
			return err
		}
		if len(raw) > s.maxStateBytes {
			return fmt.Errorf("workspace settings exceed %d bytes", s.maxStateBytes)
		}
		var after map[string]json.RawMessage
		if err := json.Unmarshal(raw, &after); err != nil {
			return err
		}
		for key, value := range after {
			if bytes.Equal(before[key], value) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'workspaceSettings',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, key, []byte(value)); err != nil {
				return err
			}
		}
		eventRaw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		event = domain.DomainEvent{ID: fmt.Sprintf("evt_%d", now.UnixNano()), Type: "workspace_preferences.updated", AggregateID: id, Payload: eventRaw, CreatedAt: now}
		if _, err := tx.ExecContext(ctx, `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at) VALUES(?,?,?,?,?,?)`, event.ID, event.Type, event.AggregateID, []byte(event.Payload), []byte(event.PreviousValues), event.CreatedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		current.WorkspaceSettings = next.WorkspaceSettings
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
		return err
	}
	if sink := s.webhook(); sink != nil {
		sink(workspace, event)
	}
	if sink := s.realtime(); sink != nil {
		actor, _ := actorFromContext(ctx)
		sink(workspace, domain.RealtimeEvent{ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, ActorID: actor.ID, ClientID: realtimeClientFromContext(ctx), Payload: event.Payload, CreatedAt: event.CreatedAt})
	}
	return nil
}
