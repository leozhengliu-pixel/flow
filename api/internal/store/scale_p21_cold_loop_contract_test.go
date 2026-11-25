package store

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21ColdLoopFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, string, string) {
	t.Helper()
	f, view, workspace := scaleP21ColdWriteFixture(t)
	loopID := idgen.New("p21-cold-contract-")
	if err := f.MutateWorkspace(t.Context(), workspace, "loop.created", loopID, nil, func(data *domain.Bootstrap) error {
		data.Loops = []domain.Loop{
			{ID: loopID, Name: "Contract loop", Instructions: "cached instructions", ConnectorIDs: []string{}, TriggerConfig: map[string]any{"wide": int64(9007199254740993)}},
			{ID: loopID + "-other", Name: "Unrelated loop", Instructions: "unrelated\x00instructions", TriggerConfig: map[string]any{"native": int64(7)}},
		}
		data.LoopVersions = []domain.LoopVersion{
			{ID: loopID + "-v1", LoopID: loopID, Version: 1, Current: true},
			{ID: loopID + "-v2", LoopID: loopID, Version: 2},
			{ID: loopID + "-other-v1", LoopID: loopID + "-other", Version: 1},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	return f, view, workspace, loopID
}

func scaleP21ColdLoopMutation(repo *SQLiteStore, ctx context.Context, workspace string, mutate func(*domain.Bootstrap) (string, error)) error {
	return repo.withColdMutation(ctx, workspace, "loop.output_contract", map[string]any{"source": "model"}, mutate)
}

func scaleP21ColdLoopChange(data *domain.Bootstrap, stamp time.Time) {
	data.Loops[0].OutputContract = &domain.LoopOutputContract{
		Source: "model", ReadOnly: true, InstructionsHash: "current\x00hash",
		Outputs: []domain.LoopContractOutput{}, ExtractedAt: &stamp,
	}
	for index := range data.LoopVersions {
		data.LoopVersions[index].OutputContract = data.Loops[0].OutputContract
	}
}

func TestScaleP21ColdLoopContractDispatchDifferential(t *testing.T) {
	for _, offset := range []int{0, 345 * 60} {
		t.Run(fmt.Sprintf("offset-%d", offset), func(t *testing.T) {
			f, view, workspace, loopID := scaleP21ColdLoopFixture(t)
			f.mu.RLock()
			original := cloneBootstrap(f.workspaces[workspace])
			f.mu.RUnlock()
			// The original callback starts from canonical locked records, not
			// the stale instructions in either installed source.
			var raw []byte
			if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='loops' AND record_key=?`, workspace, loopID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var fresh domain.Loop
			if err := json.Unmarshal(raw, &fresh); err != nil {
				t.Fatal(err)
			}
			fresh.Instructions = "fresh\x00instructions"
			raw, err := json.Marshal(fresh)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='loops' AND record_key=?`, raw, workspace, loopID); err != nil {
				t.Fatal(err)
			}
			saved := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			stamp := time.Date(2026, 11, 13, 12, 0, 0, 0, time.FixedZone("", offset))
			var input domain.Bootstrap
			var oldEvents, coldEvents []domain.DomainEvent
			var oldRealtime, coldRealtime []domain.RealtimeEvent
			f.SetWebhookSink(func(_ string, event domain.DomainEvent) { oldEvents = append(oldEvents, event) })
			f.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { oldRealtime = append(oldRealtime, event) })
			callback := func(cold bool) func(*domain.Bootstrap) (string, error) {
				return func(data *domain.Bootstrap) (string, error) {
					if len(data.Loops) != 1 || len(data.LoopVersions) != 2 || data.Loops[0].Instructions != fresh.Instructions ||
						len(data.Users)+len(data.Teams)+len(data.Settings)+len(data.Issues) != 0 {
						t.Error("loop callback did not retain original canonical narrow inputs")
					}
					// The original setMetadataRecords builds this selected
					// subset from a map. Compare its keyed values, preserving
					// exact order checks for the installed collections below.
					snapshot := cloneBootstrap(*data)
					slices.SortFunc(snapshot.LoopVersions, func(a, b domain.LoopVersion) int { return cmp.Compare(a.ID, b.ID) })
					if cold {
						if !reflect.DeepEqual(input, snapshot) {
							t.Error("cold callback input differs")
						}
						if view.writeMu.TryLock() {
							view.writeMu.Unlock()
							t.Error("cold callback did not hold parent write lock")
						}
					} else {
						input = snapshot
					}
					scaleP21ColdLoopChange(data, stamp)
					data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{
						ID: idgen.New("p21-cold-loop-audit-"), Action: "p21.cold.loop.changed", Actor: f.data.Viewer,
						CreatedAt: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
					})
					return loopID, nil
				}
			}
			ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), loopID)
			if err := f.MutateWorkspaceWithAggregate(ctx, workspace, "loop.output_contract", map[string]any{"source": "model"}, callback(false)); err != nil {
				t.Fatal(err)
			}
			want := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
			wantNative, _ := f.WorkspaceMetadata(workspace)
			scaleP21ColdPreferenceRestore(t, f, workspace, saved, original)
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			view.SetWebhookSink(func(_ string, event domain.DomainEvent) { coldEvents = append(coldEvents, event) })
			view.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
				coldRealtime = append(coldRealtime, event)
				source, err := view.retainMetadataLogicalSource(ctx, workspace)
				if err != nil {
					t.Error(err)
					return
				}
				defer source.Release()
				data, err := source.Select(ctx, MetadataRecordScope{Fields: []string{"loops", "loopVersions"}}, 2<<20)
				if err != nil || data.Loops[0].OutputContract == nil || data.Loops[0].OutputContract.Source != "model" {
					t.Error("sink observed stale parent source", err)
				}
				if source.Stamp().revision != -1 || len(view.metadataResidency.coldSlot) != 1 {
					t.Error("loop install invented exact revision or released cold slot")
				}
			})
			if err := scaleP21ColdLoopMutation(view, ctx, workspace, callback(true)); err != nil {
				t.Fatal(err)
			}
			if got := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace); !reflect.DeepEqual(got, want) {
				t.Fatal("cold loop write changed canonical/search rows")
			}
			if err := view.withColdMetadataRead(ctx, func(_ context.Context, facade *SQLiteStore) error {
				got, _ := facade.WorkspaceMetadata(workspace)
				if !reflect.DeepEqual(got, wantNative) {
					t.Error("cold installed native postimage differs")
				}
				if _, exact := facade.copyRevisionOf(workspace); exact {
					t.Error("loop installer changed original unknown revision")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, events := range [][]domain.DomainEvent{oldEvents, coldEvents} {
				for index := range events {
					events[index].ID, events[index].CreatedAt = "", time.Time{}
				}
			}
			for _, events := range [][]domain.RealtimeEvent{oldRealtime, coldRealtime} {
				for index := range events {
					events[index].ID, events[index].CreatedAt = "", time.Time{}
				}
			}
			if len(oldEvents) != 1 || !reflect.DeepEqual(oldEvents, coldEvents) || !reflect.DeepEqual(oldRealtime, coldRealtime) {
				t.Fatal("loop webhook/realtime effects changed")
			}
			var audits int
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND action='p21.cold.loop.changed'`, f.data.Workspace.ID).Scan(&audits); err != nil || audits != 2 {
				t.Fatal("loop committed audit entries differ", audits, err)
			}
		})
	}
}

func TestScaleP21ColdLoopContractRollbackAndNoopAudit(t *testing.T) {
	f, view, workspace, loopID := scaleP21ColdLoopFixture(t)
	ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), loopID)
	// The original writer canonical-round-trips the target's wide integer
	// interface value. Settle that real legacy change before testing no-op.
	if err := f.MutateWorkspaceWithAggregate(ctx, workspace, "loop.output_contract", nil, func(*domain.Bootstrap) (string, error) {
		return loopID, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	saved := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace)
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	var eventCount int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"callback", "err-noop", "unrelated", "insert", "remove", "prepare", "commit", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			descriptorBytes, diskBytes := metadataLogicalDescriptorsUsed.Load(), metadataLogicalDiskUsed.Load()
			ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), loopID)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			limit, hooks := view.metadataResidency.policyLimit, f.db.commitHooks.Load()
			prepared := false
			if mode == "prepare" {
				view.metadataResidency.policyLimit = 1
			}
			if mode == "commit" {
				f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
					if tx.metadataCommitStamps[workspace] != nil {
						read := tx.ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
						prepared = read.facade.coldWrite.prepared != nil
						tx.writeErr = errors.New("loop commit rejected")
					}
				}})
			}
			defer func() {
				view.metadataResidency.policyLimit = limit
				f.db.commitHooks.Store(hooks)
			}()
			err := scaleP21ColdLoopMutation(view, ctx, workspace, func(data *domain.Bootstrap) (string, error) {
				scaleP21ColdLoopChange(data, time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
				switch mode {
				case "callback":
					return "", errors.New("callback rejected")
				case "err-noop":
					return "", ErrNoMutation
				case "unrelated":
					data.Users = []domain.User{{ID: "unrelated"}}
				case "insert":
					data.LoopVersions = append(data.LoopVersions, domain.LoopVersion{ID: "unexpected", LoopID: loopID})
				case "remove":
					data.Loops = nil
				case "cancel":
					cancel()
				}
				return loopID, nil
			})
			if mode == "err-noop" && err != nil || mode != "err-noop" && err == nil {
				t.Fatal("loop rejection semantics changed", err)
			}
			if mode == "commit" && !prepared {
				t.Fatal("commit failure did not exercise prepared publication")
			}
			if got := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace); !reflect.DeepEqual(got, saved) {
				t.Fatal("failed loop transaction changed rows")
			}
			current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			same := current == source
			current.Release()
			if !same || len(view.metadataResidency.coldSlot) != 0 || !view.writeMu.TryLock() {
				t.Fatal("failed loop publication changed source or leaked locks")
			}
			view.writeMu.Unlock()
			if metadataLogicalDescriptorsUsed.Load() != descriptorBytes || metadataLogicalDiskUsed.Load() != diskBytes {
				t.Fatal("failed loop publication leaked source/JSON receipt storage")
			}
		})
	}
	noop := func(data *domain.Bootstrap) (string, error) {
		data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{
			ID: idgen.New("p21-cold-loop-noop-"), Action: "p21.cold.loop.noop", Actor: f.data.Viewer,
			CreatedAt: time.Now().UTC(),
		})
		return loopID, nil
	}
	if err := f.MutateWorkspaceWithAggregate(ctx, workspace, "loop.output_contract", nil, noop); err != nil {
		t.Fatal(err)
	}
	if err := scaleP21ColdLoopMutation(view, ctx, workspace, noop); err != nil {
		t.Fatal(err)
	}
	var count, audits int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&count); err != nil || count != eventCount {
		t.Fatal("no-change loop callback published an event", count, eventCount, err)
	}
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND action='p21.cold.loop.noop'`, f.data.Workspace.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("original no-change pending audit fallback was lost", audits, err)
	}
	if got := scaleP21ColdPreferenceSnapshot(t, f.SQLiteStore, workspace); !reflect.DeepEqual(got, saved) {
		t.Fatal("no-change loop callback changed canonical rows")
	}
	t.Run("forced-full-dispatch", func(t *testing.T) {
		forced := fullMutationsForced.Swap(true)
		defer fullMutationsForced.Store(forced)
		err := scaleP21ColdLoopMutation(view, ctx, workspace, func(*domain.Bootstrap) (string, error) {
			t.Error("guard admitted unintegrated forced-full loop dispatch")
			return loopID, nil
		})
		if !errors.Is(err, ErrBoundedMetadataCoverage) {
			t.Fatal("forced-full loop dispatch lost coverage guard", err)
		}
	})
}

func TestScaleP21ColdLoopContractLatestParentAndDefaultBinding(t *testing.T) {
	f, view, workspace, loopID := scaleP21ColdLoopFixture(t)
	second := scaleP21SW0SelectedExtraWorkspace(t, f, idgen.New("p21-cold-loop-second-")).Workspace.URLKey
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, second); err != nil {
		t.Fatal(err)
	}
	view.mu.Lock()
	view.lastWorkspaceKey = workspace
	view.mu.Unlock()
	ctx := withMutationAggregateID(ContextWithActor(t.Context(), f.data.Viewer), loopID)
	if err := view.withColdMetadataRead(ctx, func(bound context.Context, facade *SQLiteStore) error {
		if err := view.MutateWorkspaceScoped(bound, workspace, "application_policy.updated", "", nil, MetadataRecordScope{
			Write: []RecordKey{{Kind: "settings", ID: "cold-write-target"}},
		}, func(data *domain.Bootstrap) error {
			data.Settings["cold-write-target"] = map[string]any{"value": "hot-before-loop"}
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
		return scaleP21ColdLoopMutation(view, bound, "", func(data *domain.Bootstrap) (string, error) {
			if locked != workspace || data.Workspace.URLKey != locked {
				t.Error("loop refresh changed coordinator-protected default", locked, data.Workspace.URLKey)
			}
			scaleP21ColdLoopChange(data, time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
			return loopID, nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.withColdMetadataRead(ctx, func(_ context.Context, facade *SQLiteStore) error {
		data, _ := facade.WorkspaceMetadata(workspace)
		if !reflect.DeepEqual(data.Settings["cold-write-target"], map[string]any{"value": "hot-before-loop"}) {
			t.Error("loop install dropped intervening HOT metadata")
		}
		other, _ := facade.WorkspaceMetadata(second)
		if len(other.Loops) != 0 {
			t.Error("loop mutation escaped default binding")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
