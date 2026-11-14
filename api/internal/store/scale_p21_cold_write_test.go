package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21ColdWriteFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, string) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["cold-write-native"] = map[string]any{
			"wide": int64(9007199254740993), "text": "warm\x00value",
			"nil": []string(nil), "empty": []string{},
		}
		data.Settings["cold-write-target"] = map[string]any{"value": "before"}
		data.Webhooks = []domain.Webhook{{ID: "cold-write-webhook", Enabled: true, URL: "https://example.test/cold"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	return f, view, workspace
}

func scaleP21ColdWriteRow(t *testing.T, f scaleP09Fixture, workspace string) []byte {
	t.Helper()
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='cold-write-target'`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScaleP21ColdFullWriteNativePublication(t *testing.T) {
	f, view, workspace := scaleP21ColdWriteFixture(t)
	original, _ := f.WorkspaceMetadata(workspace)
	var oldInput any
	oldQueries, coldQueries := 0, 0
	oldContext := WithSQLStatementObserver(t.Context(), func(string) { oldQueries++ })
	if err := f.mutateFull(oldContext, workspace, "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
		oldInput = cloneSnapshotValue(reflect.ValueOf(data.Settings["cold-write-native"])).Interface()
		data.Settings["cold-write-target"] = map[string]any{"value": "after"}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	want := scaleP21ColdWriteRow(t, f, workspace)
	// Reset the literal owner, then transfer the identical native preimage.
	if err := f.mutateFull(t.Context(), workspace, "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
		data.Settings["cold-write-target"] = original.Settings["cold-write-target"]
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	var events int
	var webhooks int
	view.SetWebhookSink(func(key string, event domain.DomainEvent) {
		webhooks++
		source, err := view.retainMetadataLogicalSource(t.Context(), key)
		if err != nil {
			t.Error(err)
			return
		}
		defer source.Release()
		data, err := source.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "cold-write-target"}}}, 2<<20)
		if err != nil || !reflect.DeepEqual(data.Settings["cold-write-target"], map[string]any{"value": "after"}) {
			t.Error("webhook observed stale parent", err, data.Settings)
		}
	})
	// The sink's queue probe must be cancellable, not an unbounded wait.
	view.SetRealtimeSink(func(key string, event domain.RealtimeEvent) {
		events++
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		if err := view.withColdMetadataRead(ctx, func(context.Context, *SQLiteStore) error { return nil }); !errors.Is(err, context.DeadlineExceeded) {
			t.Error("sink did not retain cold gate", err)
		}
		source, err := view.retainMetadataLogicalSource(t.Context(), key)
		if err != nil {
			t.Error(err)
			return
		}
		defer source.Release()
		data, err := source.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "cold-write-target"}}}, 2<<20)
		if err != nil || !reflect.DeepEqual(data.Settings["cold-write-target"], map[string]any{"value": "after"}) {
			t.Error("sink observed stale parent", err, data.Settings)
		}
	})
	coldContext := WithSQLStatementObserver(t.Context(), func(string) { coldQueries++ })
	if err := view.withColdFullMutation(coldContext, workspace, "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
		if view.writeMu.TryLock() {
			view.writeMu.Unlock()
			t.Error("cold callback did not hold parent write lock")
		}
		if !reflect.DeepEqual(data.Settings["cold-write-native"], oldInput) {
			t.Error("cold callback changed native preimage")
		}
		data.Settings["cold-write-target"] = map[string]any{"value": "after"}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(scaleP21ColdWriteRow(t, f, workspace), want) || events != 1 || webhooks != 1 {
		t.Fatal("canonical write or publication count differs", events, webhooks)
	}
	t.Logf("primary SQL statements original=%d cold=%d; cold publication adds one final commit-stamp SELECT", oldQueries, coldQueries)
	if oldQueries == 0 || coldQueries != oldQueries+1 {
		t.Fatal("cold publication exceeded fixed one-statement SQL allowance", oldQueries, coldQueries)
	}
	view.mu.RLock()
	policy := view.workspaces[workspace]
	view.mu.RUnlock()
	if policy.Settings != nil || policy.Users != nil || policy.Documents != nil {
		t.Fatal("parent retained full cold graph")
	}
	if len(view.metadataResidency.coldSlot) != 0 || !view.writeMu.TryLock() {
		t.Fatal("cold gate or shared write lock leaked")
	}
	view.writeMu.Unlock()
}

func TestScaleP21ColdFullWriteRollbackAndRejectedDispatch(t *testing.T) {
	f, view, workspace := scaleP21ColdWriteFixture(t)
	want := scaleP21ColdWriteRow(t, f, workspace)
	var eventCount int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	prior, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer prior.Release()
	for _, mode := range []string{"callback", "policy", "unavailable", "external", "commit"} {
		t.Run(mode, func(t *testing.T) {
			limit := view.metadataResidency.policyLimit
			prepared := false
			previousHooks := f.db.commitHooks.Load()
			if mode == "policy" {
				view.metadataResidency.policyLimit = 1
			}
			if mode == "commit" {
				f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
					if tx.metadataCommitStamps[workspace] != nil {
						read := tx.ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
						prepared = read.facade.coldWrite.prepared != nil
						tx.writeErr = errors.New("cold prepared commit rejected")
					}
				}})
			}
			defer func() {
				view.metadataResidency.policyLimit = limit
				f.db.commitHooks.Store(previousHooks)
			}()
			err := view.withColdFullMutation(t.Context(), workspace, "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
				data.Settings["cold-write-target"] = map[string]any{"value": mode}
				if mode == "callback" {
					return "", errors.New("callback rollback")
				}
				if mode == "unavailable" {
					data.Settings["cold-write-native"] = time.Now().In(time.FixedZone("unaudited", 123))
				}
				if mode == "external" {
					data.Notifications = []domain.Notification{{ID: "must-roll-back", RecipientID: f.data.Viewer.ID}}
				}
				return "", nil
			})
			if err == nil {
				t.Fatal("invalid candidate committed")
			}
			if mode == "commit" && !prepared {
				t.Fatal("commit rejection did not exercise prepared publication")
			}
			if !bytes.Equal(want, scaleP21ColdWriteRow(t, f, workspace)) {
				t.Fatal("rollback changed target row")
			}
			var count int
			if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&count); err != nil || count != eventCount {
				t.Fatal("rollback published domain event", err, count)
			}
			current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			same := current == prior
			current.Release()
			if !same || len(view.metadataResidency.coldSlot) != 0 {
				t.Fatal("failed publication replaced source or leaked gate")
			}
		})
	}
	for _, input := range []struct {
		event   string
		payload any
	}{
		{"workspace_preferences.updated", map[string]any{"featureFlags": map[string]bool{"issues": true}}},
		{"user_settings.updated", map[string]any{"language": "en"}},
		{"issue.updated", nil},
		{"loop.output_contract", nil},
	} {
		if err := view.withColdFullMutation(t.Context(), workspace, input.event, input.payload, func(*domain.Bootstrap) (string, error) {
			t.Fatal("unsupported dispatch callback ran")
			return "", nil
		}); !errors.Is(err, ErrBoundedMetadataCoverage) {
			t.Fatal("unsupported dispatch accepted", input.event, err)
		}
	}
}

func TestScaleP21ColdFullWriteRefreshAndStamps(t *testing.T) {
	f, view, workspace := scaleP21ColdWriteFixture(t)
	err := view.withColdMetadataRead(t.Context(), func(ctx context.Context, facade *SQLiteStore) error {
		for index := 1; index <= 2; index++ {
			if err := view.MutateWorkspaceScoped(ctx, workspace, "application_policy.updated", "", nil, MetadataRecordScope{
				Write: []RecordKey{{Kind: "settings", ID: "cold-write-target"}},
			}, func(data *domain.Bootstrap) error {
				data.Settings["cold-write-target"] = map[string]any{"hot": index}
				return nil
			}); err != nil {
				return err
			}
			if err := view.withColdFullMutation(ctx, workspace, "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
				raw, _ := json.Marshal(data.Settings["cold-write-target"])
				want, _ := json.Marshal(map[string]any{"hot": index})
				if !bytes.Equal(raw, want) {
					t.Errorf("cold writer ignored latest HOT postimage: %s", raw)
				}
				data.Settings["cold-write-target"] = map[string]any{"cold": index}
				return "", nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	if source.Stamp().revision != -1 {
		t.Fatal("unknown source acquired an exact SQL stamp")
	}
	stored, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	resident, _, ok := view.residentStamp(workspace)
	if !ok || resident != stored || source.Stamp() == resident {
		t.Fatal("unknown source and stored policy stamp were conflated", source.Stamp(), resident, stored)
	}
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.withColdFullMutation(t.Context(), workspace, "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
		data.Settings["cold-write-target"] = map[string]any{"exact": true}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	exact, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer exact.Release()
	stored, err = readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || exact.Stamp() != stored {
		t.Fatal("exact source lost commit stamp", err, exact.Stamp(), stored)
	}
	if !view.writeMu.TryLock() {
		t.Fatal("shared mutex leaked")
	}
	view.writeMu.Unlock()
}

func TestScaleP21ColdFullWriteOffAndMutex(t *testing.T) {
	f, _, workspace := scaleP21ColdWriteFixture(t)
	ctx := context.WithValue(t.Context(), struct{ name string }{"off"}, true)
	called := false
	want := errors.New("literal full callback")
	if err := f.withColdFullMutation(ctx, workspace, "application_policy.updated", nil, func(*domain.Bootstrap) (string, error) {
		called = true
		return "", want
	}); !called || err != want {
		t.Fatal("off full writer changed callback error", err)
	}
	var owner coldWriteMutex
	facade := coldWriteMutex{shared: &owner}
	if !owner.TryLock() || facade.TryLock() {
		t.Fatal("delegating mutex does not share owner")
	}
	owner.Unlock()
	if !facade.TryLock() || owner.TryLock() {
		t.Fatal("facade lock did not exclude owner")
	}
	facade.Unlock()
}

func TestScaleP21ColdFullWriteConflictRefresh(t *testing.T) {
	f, view, workspace := scaleP21ColdWriteFixture(t)
	// A same-version writer commits after this view captured its native base.
	if err := f.mutateFull(t.Context(), workspace, "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
		data.Settings["cold-write-target"] = map[string]any{"value": "peer"}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	var inputs []string
	if err := view.withColdFullMutation(t.Context(), workspace, "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
		inputs = append(inputs, data.Settings["cold-write-target"].(map[string]any)["value"].(string))
		data.Settings["cold-write-target"] = map[string]any{"value": "mine"}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inputs, []string{"before", "peer"}) {
		t.Fatal("retry discarded the original partial conflict refresh", inputs)
	}
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	data, err := source.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "cold-write-target"}}}, 2<<20)
	if err != nil || !reflect.DeepEqual(data.Settings["cold-write-target"], map[string]any{"value": "mine"}) {
		t.Fatal("retry installed wrong postimage", err, data.Settings)
	}
}

type scaleP21ColdWriteCoordinator func(context.Context, string, func() error) error

func (run scaleP21ColdWriteCoordinator) WithWorkspaceLock(ctx context.Context, workspace string, apply func() error) error {
	return run(ctx, workspace, apply)
}

func TestScaleP21ColdFullWriteDefaultCoordinatorBinding(t *testing.T) {
	f, view, workspace := scaleP21ColdWriteFixture(t)
	second := scaleP21SW0SelectedExtraWorkspace(t, f, workspace+"-cold-default").Workspace.URLKey
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, second); err != nil {
		t.Fatal(err)
	}
	view.mu.Lock()
	view.lastWorkspaceKey = workspace
	view.mu.Unlock()
	var locked, mutated string
	view.SetWorkspaceCoordinator(scaleP21ColdWriteCoordinator(func(ctx context.Context, key string, apply func() error) error {
		locked = key
		// Default switches after original selection but before acquiring its
		// workspace lock. The protected mutation must stay bound to that key.
		view.mu.Lock()
		view.lastWorkspaceKey = second
		view.mu.Unlock()
		return apply()
	}))
	if err := view.withColdFullMutation(t.Context(), "", "application_policy.updated", nil, func(data *domain.Bootstrap) (string, error) {
		mutated = data.Workspace.URLKey
		data.Settings["cold-write-target"] = map[string]any{"value": "locked-default"}
		return "", nil
	}); err != nil {
		t.Fatal(err)
	}
	if locked != workspace || mutated != locked {
		t.Fatal("default refresh changed the coordinator-protected key", locked, mutated)
	}
	var target map[string]any
	if err := json.Unmarshal(scaleP21ColdWriteRow(t, f, workspace), &target); err != nil || target["value"] != "locked-default" {
		t.Fatal("mutation escaped original default workspace", err, target)
	}
	var rows int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='cold-write-target'`, second).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("new default was changed under old coordinator key", err, rows)
	}
}
