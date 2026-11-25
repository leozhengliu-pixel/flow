package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW27KnownWarmReloadFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, MetadataRecordScope) {
	t.Helper()
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections", "cycles", "userSettings"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections[0].Provider = "warm\x00provider"
		data.IntegrationConnections[0].Scopes = nil
		data.Cycles[0].Resources, data.Cycles[0].Insight = nil, nil
		preference := data.UserSettings[data.Viewer.ID]
		preference.UpdatedAt = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
		data.UserSettings[data.Viewer.ID] = preference
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	known, exact := f.copyRevisionOf(workspace)
	stored, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || !exact || known.workspaceID != stored.workspaceID || known.revision != stored.revision {
		t.Fatal("actual old metadata-only producer did not retain an exact generation for warm reload control", known, exact, stored, err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	return f, view, MetadataRecordScope{Fields: []string{"integrationConnections", "cycles", "userSettings"}}
}

func TestScaleP21SW27LogicalReloadWarmPeerAndRetainedHandoff(t *testing.T) {
	f, view, scope := scaleP21SW27KnownWarmReloadFixture(t)
	workspace := f.data.Workspace.URLKey
	full, found := f.WorkspaceMetadata(workspace)
	expected := metadataRecordSnapshot(full, scope, metadataRecordFields(scope))
	if !found || expected.IntegrationConnections[0].Provider != "warm\x00provider" || expected.Cycles[0].Insight != nil {
		t.Fatal("warm literal reload fixture lost typed source")
	}
	oldSource, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer oldSource.Release()
	reloadStarted := time.Now().UTC()
	for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal("same-stamp reload", err)
		}
	}
	actual, found, err := view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	if err != nil || !found || !reflect.DeepEqual(actual, expected) {
		t.Fatal("same-stamp bounded reload replaced actual native warm state", found, err)
	}
	still, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if still != oldSource {
		t.Error("same-stamp reload replaced the retained logical generation")
	}
	still.Release()
	peer, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Rollback()
	connection := expected.IntegrationConnections[0]
	connection.Provider, connection.Status = "peer-canonical", "peer-current"
	encoded, err := json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMetadataRecordRow(t.Context(), peer, workspace, metadataRecordKey{"integrationConnections", connection.ID}, metadataRecord{data: encoded}, nil); err != nil {
		t.Fatal(err)
	}
	if err := peer.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal("changed peer stamp reload", err)
		}
	}
	full, _ = f.WorkspaceMetadata(workspace)
	expected = metadataRecordSnapshot(full, scope, metadataRecordFields(scope))
	actual, found, err = view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	for userID, preference := range expected.UserSettings {
		var count int
		if queryErr := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='userSettings' AND record_key=?`, workspace, userID).Scan(&count); queryErr != nil {
			t.Fatal(queryErr)
		}
		if count != 0 {
			continue
		}
		observed := actual.UserSettings[userID]
		if preference.UpdatedAt.Before(reloadStarted) || observed.UpdatedAt.Before(reloadStarted) ||
			preference.UpdatedAt.After(time.Now().UTC()) || observed.UpdatedAt.After(time.Now().UTC()) {
			t.Fatal("missing preference did not get its own actual cold-load timestamp")
		}
		// Independent literal cold loads generate this timestamp. The SQL
		// absence and load windows prove the only aligned field is generated.
		preference.UpdatedAt = observed.UpdatedAt
		expected.UserSettings[userID] = preference
	}
	if err != nil || !found || !reflect.DeepEqual(actual, expected) ||
		actual.IntegrationConnections[0].Provider != "peer-canonical" || actual.Cycles[0].Insight == nil {
		got, want := reflect.ValueOf(actual), reflect.ValueOf(expected)
		for index := 0; index < got.NumField(); index++ {
			if !reflect.DeepEqual(got.Field(index).Interface(), want.Field(index).Interface()) {
				t.Logf("cold difference field=%s got=%#v want=%#v", got.Type().Field(index).Name, got.Field(index).Interface(), want.Field(index).Interface())
			}
		}
		t.Fatal("bounded peer reload differs from actual old cold normalization", found, err)
	}
	retained, err := oldSource.Select(t.Context(), scope, 2<<20)
	if err != nil || retained.IntegrationConnections[0].Provider != "warm\x00provider" || retained.Cycles[0].Insight != nil {
		t.Fatal("reload changed an earlier retained request generation", err)
	}
	current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := readMetadataStamp(t.Context(), view.db, workspace)
	if err != nil || current.Stamp() != stamp || current == oldSource {
		t.Error("cold publication lost actual SQL generation stamp", current.Stamp(), stamp, err)
	}
	current.Release()
	legacyPolicy, _ := f.WorkspaceSettingsMetadata(workspace)
	boundedPolicy, _ := view.WorkspaceSettingsMetadata(workspace)
	if !reflect.DeepEqual(legacyPolicy, boundedPolicy) || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("cold logical source and resident policy did not publish together without full decode")
	}
}

func TestScaleP21SW27LogicalReloadFailureLeavesPublishedPair(t *testing.T) {
	f, view, scope := scaleP21SW27KnownWarmReloadFixture(t)
	workspace := f.data.Workspace.URLKey
	policyBefore, found := view.WorkspaceSettingsMetadata(workspace)
	if !found {
		t.Fatal("published policy missing")
	}
	sourceBefore, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceBefore.Release()
	published, found, err := view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	if err != nil || !found {
		t.Fatal(err)
	}
	peer, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Rollback()
	if err := writeMetadataRecordRow(t.Context(), peer, workspace, metadataRecordKey{"settings", "sw27ReloadPeer"}, metadataRecord{data: json.RawMessage(`"advance"`)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := peer.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"cancelled", "source-disk-budget", "late-policy-budget"} {
		t.Run(mode, func(t *testing.T) {
			request := t.Context()
			want := context.Canceled
			lateBudgetApplied := false
			restorePolicyBudget := func() {}
			var injected int64
			restoreDiskBudget := func() {
				if injected != 0 {
					metadataLogicalDiskUsed.Add(-injected)
					injected = 0
				}
			}
			defer restoreDiskBudget()
			startDisk, startDescriptors, startSpool := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
			switch mode {
			case "cancelled":
				request = scaleP21SW27CancelledContext(t)
			case "source-disk-budget":
				injected = metadataLogicalDiskBytes - metadataLogicalDiskUsed.Load()
				if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, injected, metadataLogicalDiskBytes) {
					injected = 0
					t.Fatal("could not reserve remaining logical disk quota")
				}
				want = ErrRecordSpoolBudget
			case "late-policy-budget":
				view.metadataResidency.mu.Lock()
				previous := view.metadataResidency.policyLimit
				view.metadataResidency.mu.Unlock()
				restorePolicyBudget = func() {
					view.metadataResidency.mu.Lock()
					view.metadataResidency.policyLimit = previous
					view.metadataResidency.mu.Unlock()
				}
				defer restorePolicyBudget()
				request = WithSQLStatementObserver(request, func(query string) {
					if strings.Contains(query, "SELECT COALESCE(MAX(head),0) FROM workspace_record_journal_heads") {
						view.metadataResidency.mu.Lock()
						view.metadataResidency.policyLimit = 1
						view.metadataResidency.mu.Unlock()
						lateBudgetApplied = true
					}
				})
				want = ErrMetadataWorkBudget
			}
			if err := view.ReloadWorkspace(request, workspace); !errors.Is(err, want) {
				t.Fatal("reload failure reason changed", mode, err)
			}
			restorePolicyBudget()
			restoreDiskBudget()
			if mode == "late-policy-budget" && !lateBudgetApplied {
				t.Fatal("late budget injection did not reach the post-source journal-head phase")
			}
			if metadataLogicalDiskUsed.Load() != startDisk || metadataLogicalDescriptorsUsed.Load() != startDescriptors ||
				recordSpoolBytes.Load() != startSpool {
				t.Fatal("failed reload leaked logical disk, descriptors, or optional compatibility spool")
			}
			sourceAfter, err := view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			if sourceAfter != sourceBefore || sourceAfter.Stamp() != sourceBefore.Stamp() {
				t.Error("failed reload replaced the published logical source")
			}
			sourceAfter.Release()
			policyAfter, found := view.WorkspaceSettingsMetadata(workspace)
			if !found || !reflect.DeepEqual(policyAfter, policyBefore) {
				t.Fatal("failed reload published a partial policy")
			}
			actual, found, err := view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
			if err != nil || !found || !reflect.DeepEqual(actual, published) || view.metadataResidency.work.used != 0 {
				t.Fatal("failed reload changed retained callback input or leaked admission", err)
			}
		})
	}
}

func TestScaleP21SW27LogicalReloadPrivateScratchDirectory(t *testing.T) {
	f, view, _ := scaleP21SW27KnownWarmReloadFixture(t)
	workspace := f.data.Workspace.URLKey
	before, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("literal source missing")
	}
	connection := before.IntegrationConnections[0]
	connection.Provider, connection.Status = "tmp-directory-peer", "updated"
	raw, err := json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"integrationConnections", connection.ID}, metadataRecord{data: raw}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	optionalSpool := recordSpoolBytes.Load()
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "0")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing", "unavailable"))
	for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal("reload coupled installed metadata to optional spooling or current TMPDIR", err)
		}
	}
	scope := MetadataRecordScope{Fields: []string{"integrationConnections", "cycles"}}
	literal, _ := f.WorkspaceMetadata(workspace)
	expected := metadataRecordSnapshot(literal, scope, metadataRecordFields(scope))
	actual, found, err := view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	if err != nil || !found || !reflect.DeepEqual(actual, expected) || actual.IntegrationConnections[0].Provider != "tmp-directory-peer" {
		t.Fatal("private-directory reload changed old cold normalization", found, err)
	}
	if recordSpoolBytes.Load() != optionalSpool || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("private reload used optional download budget, decoded full metadata, or leaked work")
	}
}

func TestScaleP21SW27LogicalReloadWorkspaceKeyRecreation(t *testing.T) {
	f, view, scope := scaleP21SW27KnownWarmReloadFixture(t)
	workspace, oldID := f.data.Workspace.URLKey, f.data.Workspace.ID
	oldSource, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer oldSource.Release()
	if err := f.DeleteWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	recreated, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "Recreated owned workspace", workspace, "us")
	if err != nil {
		t.Fatal(err)
	}
	if recreated.Workspace.ID == oldID || recreated.Workspace.URLKey != workspace {
		t.Fatal("actual workspace recreation did not reuse only the physical key")
	}
	if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal("reload recreated workspace", err)
	}
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	stamp, err := readMetadataStamp(t.Context(), view.db, workspace)
	if err != nil || source.Stamp() != stamp || stamp.workspaceID != recreated.Workspace.ID {
		t.Fatal("reused workspace key kept the deleted workspace's logical stamp", source.Stamp(), stamp, err)
	}
	actual, found, err := view.WorkspaceMetadataScoped(t.Context(), "", scope)
	if err != nil || !found || actual.Workspace.ID != recreated.Workspace.ID || len(actual.IntegrationConnections) != 0 {
		t.Fatal("default workspace read leaked deleted workspace records across key reuse", found, err)
	}
	old, err := oldSource.Select(t.Context(), scope, 2<<20)
	if err != nil || old.Workspace.ID != oldID || old.IntegrationConnections[0].Provider != "warm\x00provider" {
		t.Fatal("workspace recreation changed an already retained old handoff", err)
	}
}
