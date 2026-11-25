package store

import (
	"bytes"
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

type coldPreferenceRow struct {
	field, key string
	order      int
	data       []byte
}

type coldPreferenceSnapshot struct {
	root   []byte
	rows   []coldPreferenceRow
	search map[string]string
}

func scaleP21ColdPreferenceSnapshot(t *testing.T, repo *SQLiteStore, workspace string) coldPreferenceSnapshot {
	t.Helper()
	result := coldPreferenceSnapshot{search: map[string]string{}}
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&result.root); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.db.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var row coldPreferenceRow
		if err := rows.Scan(&row.field, &row.key, &row.order, &row.data); err != nil {
			t.Fatal(err)
		}
		result.rows = append(result.rows, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	rows, err = repo.db.QueryContext(t.Context(), `SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=? ORDER BY field,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var field, key, content string
		if err := rows.Scan(&field, &key, &content); err != nil {
			t.Fatal(err)
		}
		result.search[field+"/"+key] = content
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21ColdPreferenceRestore(t *testing.T, f scaleP09Fixture, workspace string, saved coldPreferenceSnapshot, data domain.Bootstrap) {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=?`, workspace); err != nil {
		t.Fatal(err)
	}
	for _, row := range saved.rows {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,?,?)`, workspace, row.field, row.key, row.order, row.data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, saved.root, workspace); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.setWorkspaceLocked(workspace, data)
	f.mu.Unlock()
}

type coldPreferenceCase struct {
	name, event, aggregate string
	payload                any
	change                 func(*domain.Bootstrap)
}

func scaleP21ColdPreferenceCases(user string) []coldPreferenceCase {
	stamp := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	return []coldPreferenceCase{
		{"user-settings", "user_settings.updated", user, map[string]any{"language": "zh"}, func(data *domain.Bootstrap) {
			value := data.UserSettings[user]
			value.UserID, value.Language, value.UpdatedAt = user, "zh", stamp
			value.EnabledCodingTools = []string{}
			data.UserSettings[user] = value
		}},
		{"favorite-add", "favorite.added", "cold-preference-release", map[string]any{"type": "release"}, func(data *domain.Bootstrap) {
			data.Favorites = append(data.Favorites, domain.Favorite{
				ID: "cold-preference-added", UserID: user, ResourceType: "release",
				ResourceID: "cold-preference-release", Tab: "changelog", CreatedAt: stamp,
			})
		}},
		{"favorite-remove", "favorite.removed", "cold-preference-release", map[string]any{"type": "release", "tab": "releases"}, func(data *domain.Bootstrap) {
			data.Favorites = slices.DeleteFunc(data.Favorites, func(item domain.Favorite) bool { return item.ID == "cold-preference-tab" })
		}},
		{"feature-flags", "workspace_preferences.updated", "workspace", map[string]any{"featureFlags": map[string]bool{"pulse": false}}, func(data *domain.Bootstrap) {
			data.WorkspaceSettings.FeatureFlags["pulse"] = false
			data.WorkspaceSettings.UpdatedAt = stamp
		}},
	}
}

func scaleP21ColdPreferenceFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, string) {
	t.Helper()
	f, view, workspace := scaleP21ColdWriteFixture(t)
	if err := f.MutateWorkspace(t.Context(), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		stamp := time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC)
		data.Favorites = []domain.Favorite{
			{ID: "cold-preference-root", UserID: f.data.Viewer.ID, ResourceType: "release", ResourceID: "cold-preference-release", CreatedAt: stamp},
			{ID: "cold-preference-tab", UserID: f.data.Viewer.ID, ResourceType: "release", ResourceID: "cold-preference-release", Tab: "releases", CreatedAt: stamp.Add(time.Second)},
			{ID: "cold-preference-other", UserID: f.data.Viewer.ID, ResourceType: "release_pipeline", ResourceID: "unrelated-pipeline", CreatedAt: stamp.Add(2 * time.Second)},
		}
		data.WorkspaceSettings.FeatureFlags["pulse"] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	return f, view, workspace
}

func TestScaleP21ColdPreferencesDispatchDifferential(t *testing.T) {
	for index := range scaleP21ColdPreferenceCases("") {
		t.Run(scaleP21ColdPreferenceCases("")[index].name, func(t *testing.T) {
			f, view, workspace := scaleP21ColdPreferenceFixture(t)
			test := scaleP21ColdPreferenceCases(f.data.Viewer.ID)[index]
			f.mu.RLock()
			original := cloneBootstrap(f.workspaces[workspace])
			f.mu.RUnlock()
			saved := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			type effects struct {
				webhooks []domain.DomainEvent
				realtime []domain.RealtimeEvent
			}
			var oldEffects, coldEffects effects
			f.SetWebhookSink(func(_ string, event domain.DomainEvent) { oldEffects.webhooks = append(oldEffects.webhooks, event) })
			f.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { oldEffects.realtime = append(oldEffects.realtime, event) })
			var input domain.Bootstrap
			calls := 0
			callback := func(cold bool) func(*domain.Bootstrap) (string, error) {
				return func(data *domain.Bootstrap) (string, error) {
					calls++
					if cold {
						if !reflect.DeepEqual(cloneBootstrap(*data), input) {
							t.Error("cold callback input differs from original public dispatch")
						}
						if view.writeMu.TryLock() {
							view.writeMu.Unlock()
							t.Error("cold callback did not hold parent write lock")
						}
					} else {
						input = cloneBootstrap(*data)
					}
					test.change(data)
					data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{
						ID: idgen.New("p21-cold-pref-audit-"), Action: "p21.cold.preferences." + test.name,
						ResourceType: "preferences", ResourceID: test.aggregate, Actor: f.data.Viewer,
						CreatedAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
					})
					return test.aggregate, nil
				}
			}
			ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), test.aggregate)
			if err := f.MutateWorkspaceWithAggregate(ctx, workspace, test.event, test.payload, callback(false)); err != nil {
				t.Fatal(err)
			}
			want := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			wantNative, _ := f.WorkspaceMetadata(workspace)
			scaleP21ColdPreferenceRestore(t, f, workspace, saved, original)
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			view.SetWebhookSink(func(_ string, event domain.DomainEvent) { coldEffects.webhooks = append(coldEffects.webhooks, event) })
			// A sink checks installed parent state without recursively entering
			// the occupied cold gate.
			view.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
				coldEffects.realtime = append(coldEffects.realtime, event)
				if len(view.metadataResidency.coldSlot) != 1 {
					t.Error("publication released cold slot before sink")
				}
				read, err := view.retainMetadataLogicalSource(ctx, workspace)
				if err != nil {
					t.Error(err)
					return
				}
				defer read.Release()
				if read.Stamp().workspaceID != f.data.Workspace.ID {
					t.Error("sink observed stale parent identity")
				}
			})
			if err := view.withColdMutation(ctx, workspace, test.event, test.payload, callback(true)); err != nil {
				t.Fatal(err)
			}
			got := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			if !reflect.DeepEqual(got, want) || calls != 2 {
				t.Fatal("cold dispatch changed canonical/search rows or callback count", calls)
			}
			if err := view.withColdMetadataRead(ctx, func(_ context.Context, facade *SQLiteStore) error {
				native, _ := facade.WorkspaceMetadata(workspace)
				if !reflect.DeepEqual(native, wantNative) {
					t.Error("parent native installed postimage differs from original")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, events := range []*effects{&oldEffects, &coldEffects} {
				for i := range events.webhooks {
					events.webhooks[i].ID, events.webhooks[i].CreatedAt = "", time.Time{}
				}
				for i := range events.realtime {
					events.realtime[i].ID, events.realtime[i].CreatedAt = "", time.Time{}
				}
			}
			if !reflect.DeepEqual(oldEffects, coldEffects) || len(coldEffects.webhooks) != 1 || len(coldEffects.realtime) != 1 {
				t.Fatal("cold dispatch changed webhook/realtime effects", oldEffects, coldEffects)
			}
			var audits int
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND action=?`, f.data.Workspace.ID, "p21.cold.preferences."+test.name).Scan(&audits); err != nil || audits != 2 {
				t.Fatal("public audit collector lost committed pending entries", audits, err)
			}
			if view.WorkspaceMetadataDecodes() != 0 || len(view.metadataResidency.coldSlot) != 0 || !view.writeMu.TryLock() {
				t.Fatal("cold dispatch decoded SQL metadata or leaked gate/write lock")
			}
			view.writeMu.Unlock()
		})
	}
}

func TestScaleP21ColdPreferencesFailuresAndNoop(t *testing.T) {
	for index := range scaleP21ColdPreferenceCases("") {
		t.Run(scaleP21ColdPreferenceCases("")[index].name, func(t *testing.T) {
			f, view, workspace := scaleP21ColdPreferenceFixture(t)
			test := scaleP21ColdPreferenceCases(f.data.Viewer.ID)[index]
			before := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			var count int
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			prior, err := view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer prior.Release()
			for _, mode := range []string{"callback", "noop", "prepare", "commit", "cancel"} {
				t.Run(mode, func(t *testing.T) {
					ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), test.aggregate)
					ctx, cancel := context.WithCancel(ctx)
					defer cancel()
					limit := view.metadataResidency.policyLimit
					hooks := f.db.commitHooks.Load()
					prepared := false
					if mode == "prepare" {
						view.metadataResidency.policyLimit = 1
					}
					if mode == "commit" {
						f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
							if tx.metadataCommitStamps[workspace] != nil {
								read := tx.ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
								prepared = read.facade.coldWrite.prepared != nil
								tx.writeErr = errors.New("preference commit rejected")
							}
						}})
					}
					defer func() {
						view.metadataResidency.policyLimit = limit
						f.db.commitHooks.Store(hooks)
					}()
					callbackError := errors.New("preference callback rejected")
					err := view.withColdMutation(ctx, workspace, test.event, test.payload, func(data *domain.Bootstrap) (string, error) {
						test.change(data)
						data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{
							ID: idgen.New("p21-cold-pref-failed-"), Action: "p21.cold.preferences.failed",
							Actor: f.data.Viewer, CreatedAt: time.Now().UTC(),
						})
						switch mode {
						case "callback":
							return "", callbackError
						case "noop":
							return "", ErrNoMutation
						case "cancel":
							cancel()
						}
						return test.aggregate, nil
					})
					if mode == "noop" && err != nil || mode != "noop" && err == nil || mode == "callback" && !errors.Is(err, callbackError) {
						t.Fatal("failure/no-op semantics changed", err)
					}
					if mode == "commit" && !prepared {
						t.Fatal("commit rejection missed prepared native publication")
					}
					if got := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace); !reflect.DeepEqual(got, before) {
						t.Fatal("failed write changed canonical/search rows")
					}
					current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					same := current == prior
					current.Release()
					if !same || len(view.metadataResidency.coldSlot) != 0 || !view.writeMu.TryLock() {
						t.Fatal("failed write changed parent or leaked locks")
					}
					view.writeMu.Unlock()
				})
			}
			var after, audits int
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&after); err != nil || after != count {
				t.Fatal("failed/no-op write published event", after, count, err)
			}
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND action='p21.cold.preferences.failed'`, f.data.Workspace.ID).Scan(&audits); err != nil || audits != 0 {
				t.Fatal("failed/no-op write retained pending audit", audits, err)
			}
		})
	}
}

func TestScaleP21ColdPreferencesLatestParentAndDefaultBinding(t *testing.T) {
	for index := range scaleP21ColdPreferenceCases("") {
		t.Run(scaleP21ColdPreferenceCases("")[index].name, func(t *testing.T) {
			f, view, workspace := scaleP21ColdPreferenceFixture(t)
			test := scaleP21ColdPreferenceCases(f.data.Viewer.ID)[index]
			second := scaleP21SW0SelectedExtraWorkspace(t, f, idgen.New("p21-cold-pref-second-")).Workspace.URLKey
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, second); err != nil {
				t.Fatal(err)
			}
			view.mu.Lock()
			view.lastWorkspaceKey = workspace
			view.mu.Unlock()
			ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), test.aggregate)
			err := view.withColdMetadataRead(ctx, func(bound context.Context, facade *SQLiteStore) error {
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
				return view.withColdMutation(bound, "", test.event, test.payload, func(data *domain.Bootstrap) (string, error) {
					if locked != workspace || data.Workspace.URLKey != workspace {
						t.Error("default refresh escaped coordinator-protected workspace", locked, data.Workspace.URLKey)
					}
					if test.name != "feature-flags" {
						raw, _ := json.Marshal(data.Settings["cold-write-target"])
						if !bytes.Equal(raw, []byte(`{"value":"intervening-hot"}`)) {
							t.Error("optimized callback missed intervening HOT source", string(raw))
						}
					}
					test.change(data)
					return test.aggregate, nil
				})
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := view.withColdMetadataRead(ctx, func(_ context.Context, facade *SQLiteStore) error {
				data, _ := facade.WorkspaceMetadata(workspace)
				raw, _ := json.Marshal(data.Settings["cold-write-target"])
				if !bytes.Equal(raw, []byte(`{"value":"intervening-hot"}`)) {
					t.Error("optimized install lost intervening HOT unrelated field", string(raw))
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScaleP21ColdPreferencesUserSettingsConflictRetry(t *testing.T) {
	f, view, workspace := scaleP21ColdPreferenceFixture(t)
	other := idgen.New("p21-cold-pref-other-")
	if err := f.MutateWorkspace(t.Context(), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.UserSettings[other] = domain.UserSettings{UserID: other, Language: "before"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), workspace, "user_settings.updated", other, map[string]any{"language": "peer"}, func(data *domain.Bootstrap) error {
		value := data.UserSettings[other]
		value.Language = "peer"
		data.UserSettings[other] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), f.data.Viewer.ID)
	var inputs []string
	if err := view.withColdMutation(ctx, workspace, "user_settings.updated", map[string]any{"language": "mine"}, func(data *domain.Bootstrap) (string, error) {
		inputs = append(inputs, data.UserSettings[other].Language)
		value := data.UserSettings[other]
		value.Language = "mine"
		data.UserSettings[other] = value
		data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{
			ID: idgen.New("p21-cold-pref-retry-"), Action: "p21.cold.preferences.retry",
			Actor: f.data.Viewer, CreatedAt: time.Now().UTC(),
		})
		return other, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inputs, []string{"before", "peer"}) {
		t.Fatal("cold refresh discarded original user-settings retry base", inputs)
	}
	if err := view.withColdMetadataRead(ctx, func(_ context.Context, facade *SQLiteStore) error {
		data, _ := facade.WorkspaceMetadata(workspace)
		if data.UserSettings[other].Language != "mine" {
			t.Error("retry installed wrong native postimage")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND action='p21.cold.preferences.retry'`, f.data.Workspace.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("retry duplicated or lost pending audit", audits, err)
	}
}
