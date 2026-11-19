package store

import (
	"context"
	"fmt"
	"log/slog"

	"flow/api/internal/domain"
)

// cycleSettingsDefaultsMigrationVersion records the one-time upgrade of cycle
// settings stored before the capacity setting existed.
const cycleSettingsDefaultsMigrationVersion = 1013

// migrateCycleSettingsDefaults gives the cycle settings that were stored
// before the capacity setting existed (their record has no "capacity" key at
// all) the defaults those teams always ran with: capacity 4, cycles created
// and issues migrated automatically. This used to be rewritten on every load,
// which also overwrote a stored autoCreate=false next to capacity 0.
//
// A record that carries a capacity key keeps every stored value, 0 included.
// The record keys come from SQL (the domain value cannot tell an absent key
// from 0) and the rewrite goes through the normal metadata writer, which
// stores the key, so the migration is idempotent and the load path rewrites
// nothing.
func (s *SQLiteStore) migrateCycleSettingsDefaults(ctx context.Context) error {
	total := 0
	for _, key := range s.WorkspaceKeys() {
		legacy, err := s.legacyCycleSettingsTeams(ctx, key)
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		if len(legacy) == 0 {
			continue
		}
		changed := 0
		scoped := WithMetadataFields(WithoutIssueRecordMutations(ctx), "cycleSettings")
		err = s.rerunBackgroundWrite(ctx, "cycle settings defaults migration", key, func() error {
			changed = 0
			return s.MutateWorkspaceScoped(scoped, key, "cycle.settings_defaults_migrated", "", nil, MetadataRecordScope{Fields: []string{"cycleSettings"}}, func(data *domain.Bootstrap) error {
				for _, teamID := range legacy {
					settings, ok := data.CycleSettings[teamID]
					if !ok || settings.DurationWeeks <= 0 || settings.Capacity != 0 {
						continue
					}
					settings.Capacity = 4
					settings.AutoCreate = true
					settings.AutoMigrate = true
					data.CycleSettings[teamID] = settings
					changed++
				}
				if changed == 0 {
					return ErrNoMutation
				}
				return nil
			})
		})
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		total += changed
	}
	if total > 0 {
		slog.Info("gave legacy cycle settings their defaults", "teams", total)
	}
	return nil
}

// legacyCycleSettingsTeams lists the teams whose stored cycle settings have no
// capacity key.
func (s *SQLiteStore) legacyCycleSettingsTeams(ctx context.Context, workspace string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+s.metadataRecordKeySQL("record_key")+` FROM workspace_metadata_records WHERE workspace_key=? AND field='cycleSettings' AND `+s.jsonText("data", "capacity")+` IS NULL ORDER BY record_key`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var teams []string
	for rows.Next() {
		var team string
		if err := rows.Scan(&team); err != nil {
			return nil, err
		}
		teams = append(teams, team)
	}
	return teams, rows.Err()
}
