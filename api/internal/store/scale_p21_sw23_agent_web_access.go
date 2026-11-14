package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) migrateWorkspaceAgentWebAccessBounded(ctx context.Context, workspace string, enabledAt time.Time) (changed, off bool, err error) {
	ctx, release, err := s.admitMetadataWork(ctx, metadataPolicyBytes+8<<20)
	if err != nil {
		return false, false, err
	}
	defer release()
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, exists := s.storedWorkspace(workspace); !exists {
		return false, false, nil
	}
	tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	lock := ""
	if tx.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var root []byte
	if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`+lock, MetadataRecordPageMaxBytes, workspace).Scan(&root); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, nil
		}
		return false, false, err
	}
	if root == nil {
		return false, false, ErrMetadataPageBudget
	}
	_, shapes, err := residentMetadataRoot(root)
	if err != nil {
		return false, false, err
	}
	if shapes["workspaceSettings"] != "map" {
		return false, false, ErrBoundedMetadataCoverage
	}
	if s.metadataResidency.requireCoverage {
		if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
			return false, false, err
		}
	}
	keys := []string{"agentWebAccess", "agentWebSearch", "agentWebSearchDisabled", "updatedAt"}
	clause, values := bindList("record_key", keys)
	args := append([]any{MetadataRecordPageMaxBytes, workspace, "workspaceSettings"}, values...)
	rows, err := tx.QueryContext(ctx, `SELECT record_key,collection_order,CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause+lock, args...)
	if err != nil {
		return false, false, err
	}
	type property struct {
		raw   []byte
		order int
	}
	before := map[string]property{}
	var settings domain.WorkspaceSettings
	budget := newMetadataHydrationBudget()
	for rows.Next() {
		var name string
		var value property
		if err := rows.Scan(&name, &value.order, &value.raw); err != nil {
			rows.Close()
			return false, false, err
		}
		if value.raw == nil {
			rows.Close()
			return false, false, ErrMetadataPageBudget
		}
		if err := budget.consume(value.raw); err != nil {
			rows.Close()
			return false, false, err
		}
		raw, _ := json.Marshal(map[string]json.RawMessage{name: value.raw})
		if err := json.Unmarshal(raw, &settings); err != nil {
			rows.Close()
			return false, false, err
		}
		before[name] = value
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return false, false, rowErr
	}
	enabled, explicit := agentWebAccessFromRecord(settings, enabledAt)
	if !explicit {
		return false, false, nil
	}
	valuesByKey := map[string]bool{
		"agentWebAccess": enabled, "agentWebSearch": enabled, "agentWebSearchDisabled": !enabled,
	}
	for _, name := range keys[:3] {
		raw, _ := json.Marshal(valuesByKey[name])
		previous := before[name]
		if bytes.Equal(previous.raw, raw) {
			continue
		}
		if err := writeMetadataRecordRow(ctx, tx, workspace, metadataRecordKey{"workspaceSettings", name}, metadataRecord{data: raw, order: previous.order}, previous.raw); err != nil {
			return false, false, err
		}
	}
	// Stage a complete resident policy under its fixed process cap before
	// committing; the narrow property selection must never replace the policy.
	probe := &SQLiteStore{db: s.db, dialect: s.dialect, metadataResidency: newMetadataResidency()}
	probe.metadataResidency.requireCoverage = false
	policy, stamp, cost, err := probe.readResidentWorkspace(ctx, tx, workspace)
	if err != nil {
		return false, false, err
	}
	s.metadataResidency.mu.Lock()
	total := cost
	for key, bytes := range s.metadataResidency.policy {
		if key != workspace {
			total += bytes
		}
	}
	limit := s.metadataResidency.policyLimit
	s.metadataResidency.mu.Unlock()
	if total > limit {
		return false, false, ErrMetadataWorkBudget
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	if err := s.installResidentPolicy(workspace, policy, stamp, 0, cost); err != nil {
		return false, false, err
	}
	s.dropMetadataCache(ctx, workspace)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, workspace) {
		s.startupCacheDrops = append(s.startupCacheDrops, workspace)
	}
	s.mu.Unlock()
	return true, !enabled, nil
}
