package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21ColdTeamFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, string, string) {
	t.Helper()
	f, view, workspace := scaleP21ColdWriteFixture(t)
	team := idgen.New("p21-cold-team-")
	if err := f.MutateWorkspace(t.Context(), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: team, Name: "warm\x00team", Key: "COLD", Color: "before"})
		data.TeamSettings[team] = bulkTeamSettings(team)
		data.CycleSettings[team] = domain.CycleSettings{DurationWeeks: 2}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	return f, view, workspace, team
}

func scaleP21RunColdTeam(ctx context.Context, view *SQLiteStore, workspace, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	return view.withColdMutation(ctx, workspace, event, payload, mutate)
}

func scaleP21ColdTeamNative(t *testing.T, view *SQLiteStore, workspace string) domain.Bootstrap {
	t.Helper()
	var result domain.Bootstrap
	if err := view.withColdMetadataRead(t.Context(), func(_ context.Context, facade *SQLiteStore) error {
		value, ok := facade.WorkspaceMetadata(workspace)
		if !ok {
			t.Error("missing installed cold workspace")
		}
		result = cloneBootstrap(value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21ColdTeamDispatchDifferential(t *testing.T) {
	for _, mode := range []string{"created", "updated", "settings", "deleted", "teams-import", "users-import", "projects-import", "untracked"} {
		t.Run(mode, func(t *testing.T) {
			f, view, workspace, team := scaleP21ColdTeamFixture(t)
			event, aggregate, payload := "team.settings_updated", team, any(map[string]any{"timezone": "Asia/Shanghai"})
			change := func(data *domain.Bootstrap) error {
				value := data.TeamSettings[team]
				value.Timezone, value.Description = "Asia/Shanghai", "legal\x00description"
				data.TeamSettings[team] = value
				return nil
			}
			switch mode {
			case "created":
				event, aggregate, payload = "team.created", team+"-new", nil
				change = func(data *domain.Bootstrap) error {
					data.Teams = append(data.Teams, domain.Team{ID: aggregate, Key: "NEW", Name: "created"})
					data.TeamSettings[aggregate] = bulkTeamSettings(aggregate)
					data.Labels = append(data.Labels, domain.IssueLabel{ID: aggregate + "-label", Name: "label", Scope: aggregate})
					return nil
				}
			case "updated":
				event, payload = "team.updated", map[string]any{"name": "updated"}
				change = func(data *domain.Bootstrap) error {
					data.Teams[domain.TeamIndex(data, team)].Name = "updated\x00name"
					return nil
				}
			case "deleted":
				event, payload = "team.deleted", nil
				change = func(data *domain.Bootstrap) error { return ApplyTeamDeletion(data, team) }
			case "teams-import":
				event, payload = "alm.org_teams_imported", nil
				change = func(data *domain.Bootstrap) error {
					data.Teams[domain.TeamIndex(data, team)].Name = "imported"
					data.Teams = append(data.Teams, domain.Team{ID: team + "-import", Name: "new", Key: "IMP"})
					return nil
				}
			case "users-import":
				event, payload = "alm.users_imported", nil
				change = func(data *domain.Bootstrap) error {
					data.Users = append(data.Users, domain.User{ID: team + "-user", Name: "imported user"})
					return nil
				}
			case "projects-import":
				event, payload = "alm.projects_imported", nil
				change = func(data *domain.Bootstrap) error {
					data.Projects = append(data.Projects, domain.Project{ID: team + "-project", Name: "imported project", TeamIDs: []string{team}})
					return nil
				}
			case "untracked":
				change = func(data *domain.Bootstrap) error {
					data.Settings["cold-team-untracked"] = map[string]any{"native": int64(9007199254740993)}
					return nil
				}
			}
			f.mu.RLock()
			original := cloneBootstrap(f.workspaces[workspace])
			f.mu.RUnlock()
			saved := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), aggregate)
			oldSQL, coldSQL, coldCalls, oldEvents, coldEvents := 0, 0, 0, 0, 0
			var inputs []domain.Bootstrap
			callback := func(cold bool) func(*domain.Bootstrap) (string, error) {
				return func(data *domain.Bootstrap) (string, error) {
					if cold {
						if coldCalls >= len(inputs) || !reflect.DeepEqual(cloneBootstrap(*data), inputs[coldCalls]) {
							t.Error("cold callback input differs from original team dispatcher")
						}
						coldCalls++
						if view.writeMu.TryLock() {
							view.writeMu.Unlock()
							t.Error("cold team callback escaped parent write lock")
						}
					} else {
						inputs = append(inputs, cloneBootstrap(*data))
					}
					return aggregate, change(data)
				}
			}
			f.SetRealtimeSink(func(string, domain.RealtimeEvent) { oldEvents++ })
			if err := f.MutateWorkspaceWithAggregate(WithSQLStatementObserver(ctx, func(string) { oldSQL++ }), workspace, event, payload, callback(false)); err != nil {
				t.Fatal(err)
			}
			wantSQL := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			wantNative, _ := f.WorkspaceMetadata(workspace)
			scaleP21ColdPreferenceRestore(t, f, workspace, saved, original)
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			view.SetRealtimeSink(func(key string, _ domain.RealtimeEvent) {
				coldEvents++
				source, err := view.retainMetadataLogicalSource(ctx, key)
				if err != nil {
					t.Error(err)
					return
				}
				defer source.Release()
				installed, err := source.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "teams", ID: aggregate}}}, 2<<20)
				if mode == "updated" && (err != nil || len(installed.Teams) != 1 || installed.Teams[0].Name != "updated\x00name") {
					t.Error("realtime sink observed stale parent", installed.Teams, err)
				}
				if len(view.metadataResidency.coldSlot) != 1 {
					t.Error("cold slot released before synchronous sink")
				}
			})
			if err := scaleP21RunColdTeam(WithSQLStatementObserver(ctx, func(string) { coldSQL++ }), view, workspace, event, payload, callback(true)); err != nil {
				t.Fatal(err)
			}
			if got := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace); !reflect.DeepEqual(got, wantSQL) {
				t.Fatal("cold team canonical/search rows differ")
			}
			if got := scaleP21ColdTeamNative(t, view, workspace); !reflect.DeepEqual(got, cloneBootstrap(wantNative)) {
				t.Fatal("cold final native image differs from original")
			}
			if coldCalls != len(inputs) || oldEvents != coldEvents || mode != "untracked" && coldEvents != 1 {
				t.Fatal("callback/event counts changed", len(inputs), coldCalls, oldEvents, coldEvents)
			}
			t.Logf("SQL statements original=%d cold=%d; one final commit-stamp SELECT", oldSQL, coldSQL)
			if coldSQL != oldSQL+1 {
				t.Fatal("cold team exceeded fixed SQL allowance", oldSQL, coldSQL)
			}
		})
	}
}

func TestScaleP21ColdTeamMergedAndSkippedPostimage(t *testing.T) {
	for _, mode := range []string{"merged", "skipped"} {
		t.Run(mode, func(t *testing.T) {
			f, view, workspace, team := scaleP21ColdTeamFixture(t)
			f.mu.RLock()
			original := cloneBootstrap(f.workspaces[workspace])
			f.mu.RUnlock()
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "merged" {
				peer := original.Teams[domain.TeamIndex(&original, team)]
				peer.Color = "peer-color"
				fresh, err := time.Parse(time.RFC3339Nano, "2026-11-14T01:02:03.000000123+07:31")
				if err != nil {
					t.Fatal(err)
				}
				peer.CreatedAt = &fresh
				scaleP09Put(t, tx, workspace, "teams", team, peer)
			} else if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND record_key=?`, workspace, team); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			saved := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), team)
			change := func(data *domain.Bootstrap) (string, error) {
				data.Teams[domain.TeamIndex(data, team)].Name = "mine"
				return team, nil
			}
			payload := map[string]any{"name": "mine"}
			if err := f.MutateWorkspaceWithAggregate(ctx, workspace, "team.updated", payload, change); err != nil {
				t.Fatal(err)
			}
			want, _ := f.WorkspaceMetadata(workspace)
			wantSQL := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			scaleP21ColdPreferenceRestore(t, f, workspace, saved, original)
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			prepared := false
			hooks := f.db.commitHooks.Load()
			f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
				if tx.metadataCommitStamps[workspace] == nil {
					return
				}
				read := tx.ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
				publication := read.facade.coldWrite.prepared
				prepared = publication != nil
				if publication == nil {
					return
				}
				value, err := publication.source.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{Kind: "teams", ID: team}}}, 2<<20)
				if err != nil || mode == "merged" && (len(value.Teams) != 1 || value.Teams[0].Name != "mine" || value.Teams[0].Color != "peer-color") || mode == "skipped" && len(value.Teams) != 0 {
					t.Error("precommit source missed final merged/skipped result", value.Teams, err)
				}
			}})
			defer f.db.commitHooks.Store(hooks)
			if err := scaleP21RunColdTeam(ctx, view, workspace, "team.updated", payload, change); err != nil {
				t.Fatal(err)
			}
			if !prepared || !reflect.DeepEqual(scaleP21ColdTeamNative(t, view, workspace), cloneBootstrap(want)) ||
				!reflect.DeepEqual(scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace), wantSQL) {
				t.Fatal("cold installed speculative team instead of final row image", prepared)
			}
		})
	}
}

func TestScaleP21ColdTeamFailurePublication(t *testing.T) {
	f, view, workspace, team := scaleP21ColdTeamFixture(t)
	before := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
	prior, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Release()
	for _, mode := range []string{"callback", "noop", "prepare", "commit", "cancel", "cyclic"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), team))
			defer cancel()
			limit, hooks := view.metadataResidency.policyLimit, f.db.commitHooks.Load()
			prepared := false
			defer func() {
				view.metadataResidency.policyLimit = limit
				f.db.commitHooks.Store(hooks)
			}()
			if mode == "prepare" {
				view.metadataResidency.policyLimit = 1
			}
			if mode == "commit" {
				f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
					if tx.metadataCommitStamps[workspace] != nil {
						read := tx.ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
						prepared = read.facade.coldWrite.prepared != nil
						tx.writeErr = errors.New("team commit rejected")
					}
				}})
			}
			callbackErr := errors.New("team callback rejected")
			err := scaleP21RunColdTeam(ctx, view, workspace, "team.updated", map[string]any{"name": "failed"}, func(data *domain.Bootstrap) (string, error) {
				data.Teams[domain.TeamIndex(data, team)].Name = "failed"
				switch mode {
				case "callback":
					return "", callbackErr
				case "noop":
					return "", ErrNoMutation
				case "cancel":
					cancel()
				case "cyclic":
					cycle := map[string]any{}
					cycle["self"] = cycle
					data.Settings["cold-team-cycle"] = cycle
				}
				return team, nil
			})
			if mode == "noop" && err != nil || mode != "noop" && err == nil || mode == "callback" && !errors.Is(err, callbackErr) {
				t.Fatal("team callback failure semantics changed", err)
			}
			if mode == "commit" && !prepared {
				t.Fatal("commit rejected before source preparation")
			}
			current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			same := current == prior
			current.Release()
			if !same || !reflect.DeepEqual(scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace), before) ||
				len(view.metadataResidency.coldSlot) != 0 || !view.writeMu.TryLock() {
				t.Fatal("failed team write changed parent/SQL or leaked locks")
			}
			view.writeMu.Unlock()
		})
	}
}

func TestScaleP21ColdTeamLatestParentAndDefaultBinding(t *testing.T) {
	f, view, workspace, team := scaleP21ColdTeamFixture(t)
	second := scaleP21SW0SelectedExtraWorkspace(t, f, idgen.New("p21-cold-team-second-")).Workspace.URLKey
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, second); err != nil {
		t.Fatal(err)
	}
	view.mu.Lock()
	view.lastWorkspaceKey = workspace
	view.mu.Unlock()
	ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), team)
	if err := view.withColdMetadataRead(ctx, func(bound context.Context, facade *SQLiteStore) error {
		if err := view.MutateWorkspaceScoped(bound, workspace, "application_policy.updated", "", nil, MetadataRecordScope{
			Write: []RecordKey{{Kind: "settings", ID: "cold-write-target"}},
		}, func(data *domain.Bootstrap) error {
			data.Settings["cold-write-target"] = map[string]any{"value": "intervening-hot"}
			return nil
		}); err != nil {
			return err
		}
		var locked string
		facade.coordinator = scaleP21ColdWriteCoordinator(func(_ context.Context, key string, apply func() error) error {
			locked = key
			view.mu.Lock()
			view.lastWorkspaceKey = second
			view.mu.Unlock()
			return apply()
		})
		return scaleP21RunColdTeam(bound, view, "", "team.updated", map[string]any{"name": "latest"}, func(data *domain.Bootstrap) (string, error) {
			if locked != workspace || data.Workspace.URLKey != workspace {
				t.Error("default target escaped acquired coordinator key", locked, data.Workspace.URLKey)
			}
			raw, _ := json.Marshal(data.Settings["cold-write-target"])
			if string(raw) != `{"value":"intervening-hot"}` {
				t.Error("team callback missed latest HOT source", string(raw))
			}
			data.Teams[domain.TeamIndex(data, team)].Name = "latest"
			return team, nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	data := scaleP21ColdTeamNative(t, view, workspace)
	if !slices.ContainsFunc(data.Teams, func(value domain.Team) bool { return value.ID == team && value.Name == "latest" }) {
		t.Fatal("team write installed into wrong workspace")
	}
	raw, _ := json.Marshal(data.Settings["cold-write-target"])
	if string(raw) != `{"value":"intervening-hot"}` || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("team publication lost native source or loaded SQL copy", string(raw), view.WorkspaceMetadataDecodes())
	}
}
