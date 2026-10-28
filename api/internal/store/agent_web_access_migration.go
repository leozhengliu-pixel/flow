package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// agentWebAccessMigrationVersion records the one-time move of Flow Agent's web access to the explicit
// agentWebAccess setting.
const agentWebAccessMigrationVersion = 1002

// migrateAgentWebAccess gives every workspace an explicit web access choice where the stored settings show one, so
// web access becoming default-on does not override an admin who turned it off.
//
// Before web access became default-on it was an opt-in flag (agentWebSearch, false by default), and the settings
// pages saved the whole settings object, so a stored false cannot tell "never enabled" from "enabled, then turned
// off". The workspace records are read like this:
//   - agentWebSearchDisabled true: an admin turned web access off after it became default-on. Kept off.
//   - agentWebSearch true: an admin turned it on. Kept on.
//   - agentWebSearch false: turning it off needs an earlier save that turned it on, and the event log keeps the saves
//     (workspace_preferences.updated payloads). Events do not name their workspace, so a workspace keeps web access
//     off when the log shows web access was turned on at some point and the workspace's settings were saved after
//     that (the save that turned it off, or a later one). Every other workspace never had it on and is left unset,
//     which means on.
//
// Workspaces that already have an explicit value are left alone, so the migration is idempotent.
func (s *SQLiteStore) migrateAgentWebAccess(ctx context.Context) error {
	enabledAt, err := s.agentWebSearchFirstEnabled(ctx)
	if err != nil {
		return err
	}
	kept := 0
	for _, key := range s.WorkspaceKeys() {
		var changed, off bool
		err := s.rerunBackgroundWrite(ctx, "agent web access migration", key, func() (err error) {
			changed, off, err = s.migrateWorkspaceAgentWebAccess(ctx, key, enabledAt)
			return err
		})
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		if changed && off {
			kept++
		}
	}
	if kept > 0 {
		slog.Info("kept Flow Agent web access off where an admin had turned it off", "workspaces", kept)
	}
	return nil
}

// agentWebSearchFirstEnabled is when a settings save first turned agent web search on (zero when none did).
func (s *SQLiteStore) agentWebSearchFirstEnabled(ctx context.Context) (time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload,created_at FROM domain_events WHERE event_type=?`, "workspace_preferences.updated")
	if err != nil {
		return time.Time{}, fmt.Errorf("read workspace preference events: %w", err)
	}
	defer rows.Close()
	var first time.Time
	for rows.Next() {
		var payload []byte
		var created string
		if err := rows.Scan(&payload, &created); err != nil {
			return time.Time{}, err
		}
		if !agentWebSearchEnabledIn(payload) {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, created)
		if err != nil {
			continue
		}
		if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	return first, rows.Err()
}

// agentWebSearchEnabledIn reports whether a preferences payload turned agent web search on.
func agentWebSearchEnabledIn(payload []byte) bool {
	var fields struct {
		AgentWebSearch *bool `json:"agentWebSearch"`
		AgentWebAccess *bool `json:"agentWebAccess"`
	}
	if json.Unmarshal(payload, &fields) != nil {
		return false
	}
	return fields.AgentWebSearch != nil && *fields.AgentWebSearch || fields.AgentWebAccess != nil && *fields.AgentWebAccess
}

// agentWebAccessFromRecord is the explicit web access a workspace's stored settings show, if any.
func agentWebAccessFromRecord(settings domain.WorkspaceSettings, enabledAt time.Time) (bool, bool) {
	switch {
	case settings.AgentWebAccess != nil:
		return false, false
	case settings.AgentWebSearchDisabled:
		return false, true
	case settings.AgentWebSearch:
		return true, true
	case !enabledAt.IsZero() && settings.UpdatedAt.After(enabledAt):
		return false, true
	}
	return false, false
}

func (s *SQLiteStore) migrateWorkspaceAgentWebAccess(ctx context.Context, key string, enabledAt time.Time) (changed, off bool, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before, ok := s.storedWorkspace(key)
	if !ok {
		return false, false, nil
	}
	enabled, explicit := agentWebAccessFromRecord(before.WorkspaceSettings, enabledAt)
	if !explicit {
		return false, false, nil
	}
	after := before
	// UpdatedAt stays: the migration records an existing choice, it is not an edit.
	after.WorkspaceSettings.SetAgentWebAccess(enabled)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	if err := s.persistMetadataTx(ctx, tx, key, before, &after); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	s.installWorkspace(key, after, false)
	s.dropMetadataCache(ctx, key)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, key) {
		s.startupCacheDrops = append(s.startupCacheDrops, key)
	}
	s.mu.Unlock()
	return true, !enabled, nil
}
