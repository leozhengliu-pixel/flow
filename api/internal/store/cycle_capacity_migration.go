package store

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"

	"flow/api/internal/domain"
)

// cycleCapacityOverrideMigrationVersion records the one-time move of typed
// cycle capacities to capacityOverride.
const cycleCapacityOverrideMigrationVersion = 1007

// legacyCycleCapacityDefault is the capacity every generated cycle used to
// store before capacity became automatic.
const legacyCycleCapacityDefault = 4

// migrateCycleCapacityOverrides keeps the capacities people typed in Edit
// cycle when capacity becomes automatic (computed from the team's velocity).
//
// Before, every cycle stored a capacity: generated cycles copied the team's
// cycle capacity setting (4 unless changed) and Edit cycle overwrote it. A
// stored value cannot tell a default from a typed one, so the record is read
// like this:
//   - 4 (the old default) or the team's capacity setting: a default copy, left
//     unset, which means automatic;
//   - 0 or less: never meaningful, left unset;
//   - anything else: typed in Edit cycle, kept as capacityOverride.
//
// The stored capacity itself is left as it is (it is not read any more), and
// cycles that already have an override are skipped, so the migration is
// idempotent.
func (s *SQLiteStore) migrateCycleCapacityOverrides(ctx context.Context) error {
	total := 0
	for _, key := range s.WorkspaceKeys() {
		count, err := s.migrateWorkspaceCycleCapacity(ctx, key)
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		total += count
	}
	if total > 0 {
		slog.Info("kept typed cycle capacities as overrides", "cycles", total)
	}
	return nil
}

// legacyCycleCapacityOverride is the override a stored pre-automatic
// capacity stands for, if any.
func legacyCycleCapacityOverride(cycle domain.Cycle, settings domain.CycleSettings) (int, bool) {
	if cycle.CapacityOverride != nil || cycle.Capacity <= 0 {
		return 0, false
	}
	value := int(math.Round(cycle.Capacity))
	if value == legacyCycleCapacityDefault || (settings.Capacity > 0 && value == settings.Capacity) {
		return 0, false
	}
	return value, true
}

func (s *SQLiteStore) migrateWorkspaceCycleCapacity(ctx context.Context, key string) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before, ok := s.storedWorkspace(key)
	if !ok {
		return 0, nil
	}
	changed := map[int]int{}
	for index, cycle := range before.Cycles {
		if value, ok := legacyCycleCapacityOverride(cycle, before.CycleSettings[cycle.TeamID]); ok {
			changed[index] = value
		}
	}
	if len(changed) == 0 {
		return 0, nil
	}
	after := before
	after.Cycles = slices.Clone(before.Cycles)
	for index, value := range changed {
		override := value
		// UpdatedAt stays: the migration records an existing choice, it is not an edit.
		after.Cycles[index].CapacityOverride = &override
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := s.persistMetadataTx(ctx, tx, key, before, &after); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.installWorkspace(key, after, false)
	s.dropMetadataCache(ctx, key)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, key) {
		s.startupCacheDrops = append(s.startupCacheDrops, key)
	}
	s.mu.Unlock()
	return len(changed), nil
}
