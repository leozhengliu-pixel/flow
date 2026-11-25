package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW73EventPolicySourceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	credentials := []struct {
		name string
		key  *domain.APIKey
	}{
		{name: "session"},
		{name: "unrestricted", key: &domain.APIKey{ID: "sw73-all", CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "all"}},
		{name: "selected-empty", key: &domain.APIKey{ID: "sw73-empty", CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
		{name: "selected-set", key: &domain.APIKey{ID: "sw73-set", CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{"sw73-child"}}},
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Reuse the SW23 fixture/row helper; the typed projection's alias,
	// duplicate-field and physical-order corpus is already proved there.
	for _, row := range []scaleP21SW23MetadataRow{
		{Field: "teams", Key: "sw73-physical-parent", Order: -3, Data: []byte(`{"id":"sw73-parent","key":"PARENT","private":false,"Private":true,"externalSource":"null","name":"ignored parent"}`)},
		{Field: "teams", Key: "sw73-physical-child", Order: -2, Data: []byte(`{"ID":"sw73-child","key":"CHILD","private":false,"name":"ignored child"}`)},
		{Field: "teams", Key: "sw73-physical-public", Order: -1, Data: []byte(`{"id":"sw73-public","key":"PUBLIC","private":false}`)},
		{Field: "teams", Key: "sw73-physical-sparse", Order: 0, Data: []byte(`{"id":"sw73-sparse","key":"SPARSE"}`)},
		{Field: "teams", Key: "sw73-physical-default", Order: 1, Data: []byte(`{"id":"sw73-default","key":"DEFAULT"}`)},
		{Field: "teamSettings", Key: "sw73-parent", Data: []byte(`{"teamId":"sw73-parent","parentTeamId":"","access":"private","membershipRestriction":"members","settingsPermission":"admin","labelPermission":"members","memberPermission":"admin","description":"ignored guidance"}`)},
		{Field: "teamSettings", Key: "sw73-child", Data: []byte(`{"teamId":"sw73-child","parentTeamId":"sw73-parent","access":"public","membershipRestriction":"all","settingsPermission":"members","labelPermission":"members","memberPermission":"members"}`)},
		{Field: "teamSettings", Key: "sw73-public", Data: []byte(`{"teamId":"sw73-public","parentTeamId":"","access":"public","membershipRestriction":"all","settingsPermission":"members","labelPermission":"members","memberPermission":"members"}`)},
		{Field: "teamSettings", Key: "sw73-sparse", Data: []byte(`{"teamId":"sw73-sparse"}`)},
	} {
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{row.Field, row.Key}, metadataRecord{data: row.Data, order: row.Order}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for index, credential := range credentials {
		if credential.key == nil {
			continue
		}
		raw, err := json.Marshal(credential.key)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"apiKeys", credential.key.ID}, metadataRecord{data: raw, order: index}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var rootRaw []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&rootRaw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(rootRaw, &root); err != nil {
		t.Fatal(err)
	}
	root["teams"] = json.RawMessage(`[{"id":"sw73-inline","key":"INLINE","name":"Inline source prefix","private":true}]`)
	rootRaw, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, rootRaw, workspace); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, workspaces: map[string]domain.Bootstrap{}, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	defer view.releaseMetadataLogicalSources()
	version, err := f.RealtimeSourceVersion(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	beforeRoot, beforeRows := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace)
	document := domain.Document{InheritedTeamIDs: []string{"sw73-child", "sw73-public"}}
	type accessOutcome struct {
		Error, Workspace, Fingerprint string
		Access                        [4]string
		Version                       EventSourceVersion
		Policy, Query                 json.RawMessage
	}
	resolve := func(ctx context.Context, source *SQLiteStore, policy *EventPolicySource, key *domain.APIKey) accessOutcome {
		t.Helper()
		access, err := source.ResolveEventAccessFromSource(ctx, policy, f.data.Viewer.ID, key)
		if err != nil {
			return accessOutcome{Error: err.Error()}
		}
		data, query, err := access.RealtimePolicy()
		if err != nil {
			t.Fatal(err)
		}
		policyJSON, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		queryJSON, err := json.Marshal(query)
		if err != nil {
			t.Fatal(err)
		}
		return accessOutcome{
			Access:    [4]string{access.WorkspaceID, access.PrincipalID, access.CredentialScope, access.AuthzEpoch},
			Workspace: access.WorkspaceKey(), Version: access.SourceVersion(), Fingerprint: access.PermissionFingerprint(),
			Policy: policyJSON, Query: queryJSON,
		}
	}
	for _, test := range []struct {
		name                                            string
		uncertified, reload, oldVersion, unknownVersion bool
		previousWorkspace, emptyLabel, missing          bool
		borrowed, crossNamespace, legacyCurrent         bool
	}{
		{name: "current-copy", legacyCurrent: true},
		{name: "current-native-sparse-default-settings", legacyCurrent: true},
		{name: "current-root-inline-teams-prefix", legacyCurrent: true},
		{name: "uncertified-source", uncertified: true},
		{name: "uncertified-negative-version", uncertified: true, unknownVersion: true},
		{name: "independent-canonical-recovery", reload: true, legacyCurrent: true},
		{name: "retained-metadata-version", oldVersion: true},
		{name: "retained-previous-workspace-version", previousWorkspace: true},
		{name: "empty-workspace-id-label", emptyLabel: true},
		{name: "missing-workspace-namespace", missing: true},
		{name: "borrowed-snapshot", uncertified: true, borrowed: true},
		{name: "borrowed-cross-namespace-snapshot", uncertified: true, borrowed: true, crossNamespace: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if test.uncertified || test.reload {
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
			}
			// Mirror the actual native owner generation, including whether
			// it is certified. Canonical recovery below reloads independently.
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			if test.reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			requested := version
			if test.oldVersion {
				requested.Metadata--
			}
			if test.unknownVersion {
				requested.Metadata = -1
			}
			if test.previousWorkspace {
				requested.WorkspaceID += "-previous"
			}
			if test.emptyLabel {
				requested.WorkspaceID = ""
			}
			readWorkspace := workspace
			if test.missing {
				readWorkspace += "-missing"
			}
			ctx := t.Context()
			var snapshot *sqlTx
			if test.borrowed {
				f.db.SetMaxOpenConns(1)
				defer f.db.SetMaxOpenConns(4)
				snapshot, err = f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
				if err != nil {
					t.Fatal(err)
				}
				defer snapshot.Rollback()
				snapshotWorkspace := workspace
				if test.crossNamespace {
					snapshotWorkspace += "-other"
				}
				ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: snapshotWorkspace, reader: snapshot})
			}
			known, exact := f.copyRevisionOf(readWorkspace)
			current := exact && known.workspaceID == requested.WorkspaceID && known.revision == requested.Metadata
			if current != test.legacyCurrent {
				t.Fatal("event source fixture did not select the intended legacy branch")
			}
			if _, exact := view.copyRevisionOf(readWorkspace); exact {
				t.Fatal("bounded policy recovery certified a complete workspace copy")
			}
			view.mu.RLock()
			_, installed := view.workspaces[readWorkspace]
			view.metadataResidency.mu.Lock()
			logical := view.metadataResidency.logical[readWorkspace]
			nativeCurrent := installed && logical != nil && logical.Stamp().revision >= 0 &&
				logical.Stamp() == (copyStamp{workspaceID: requested.WorkspaceID, revision: requested.Metadata})
			view.metadataResidency.mu.Unlock()
			view.mu.RUnlock()
			if nativeCurrent != current {
				t.Fatal("event source fixture did not mirror the native installed source stamp")
			}
			oldSQL, newSQL := 0, 0
			old, oldErr := f.ReadEventPolicySource(WithSQLStatementObserver(ctx, func(string) { oldSQL++ }), readWorkspace, requested)
			got, gotErr := view.ReadEventPolicySource(WithSQLStatementObserver(ctx, func(string) { newSQL++ }), readWorkspace, requested)
			if (oldErr != nil) != (gotErr != nil) {
				t.Fatalf("event policy source error differential: legacy=%v bounded=%v", oldErr, gotErr)
			}
			if oldErr != nil {
				t.Fatal("event policy source fixture unexpectedly failed", oldErr, gotErr)
			}
			if current && (oldSQL != 0 || newSQL != 0) || !current && (oldSQL == 0 || newSQL == 0) {
				t.Fatal("event source changed native-versus-canonical construction", oldSQL, newSQL)
			}
			oldJSON, err := json.Marshal(old.metadata)
			if err != nil {
				t.Fatal(err)
			}
			gotJSON, err := json.Marshal(got.metadata)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(oldJSON, gotJSON) || !reflect.DeepEqual(old.metadata, got.metadata) {
				t.Fatalf("event source policy differs: legacy=%s bounded=%s", oldJSON, gotJSON)
			}
			if old.workspace != readWorkspace || got.workspace != readWorkspace || old.version != requested || got.version != requested ||
				old.Bytes() != len(oldJSON)*3+1024 || got.Bytes() != old.Bytes() {
				t.Fatal("event policy source namespace, supplied version or exact encoded byte charge changed")
			}
			closed := []string{"sw73-child"}
			if test.missing {
				closed = []string{"sw73-child", "sw73-public"}
				if got.metadata.Workspace.ID != requested.WorkspaceID || got.metadata.Workspace.URLKey != readWorkspace ||
					got.metadata.Teams != nil || got.metadata.TeamSettings == nil || len(got.metadata.TeamSettings) != 0 {
					t.Fatal("missing namespace did not retain its supplied label and allocated empty settings")
				}
			} else if !old.HasTeam("sw73-parent") || !got.HasTeam("sw73-child") || got.HasTeam("sw73-physical-parent") {
				t.Fatal("event source lost native team identity")
			}
			if current {
				if !got.HasTeam("sw73-inline") || len(got.metadata.Teams) == 0 || got.metadata.Teams[0].ID != "sw73-inline" ||
					got.metadata.TeamSettings["sw73-sparse"].Access != "public" ||
					got.metadata.TeamSettings["sw73-sparse"].MembershipRestriction != "open" ||
					got.metadata.TeamSettings["sw73-default"].Access != "public" {
					t.Fatal("native current source lost inline prefix or normalized sparse/default settings")
				}
			} else if !test.missing {
				_, defaultSettings := got.metadata.TeamSettings["sw73-default"]
				if got.HasTeam("sw73-inline") || got.metadata.TeamSettings["sw73-sparse"].Access != "" ||
					got.metadata.TeamSettings["sw73-sparse"].MembershipRestriction != "" || defaultSettings {
					t.Fatal("trusted SQL fallback injected native inline teams or synthesized settings")
				}
			}
			if !slices.Equal(old.DocumentClosedTeams(document), closed) ||
				!slices.Equal(got.DocumentClosedTeams(document), old.DocumentClosedTeams(document)) {
				t.Fatal("event source lost private-parent policy or native team identity")
			}
			for _, credential := range credentials {
				left := resolve(ctx, f.SQLiteStore, old, credential.key)
				right := resolve(ctx, view, got, credential.key)
				if !reflect.DeepEqual(left, right) {
					t.Fatalf("source access differs for %s: legacy=%+v bounded=%+v", credential.name, left, right)
				}
				wantAccess := !test.oldVersion && !test.unknownVersion && !test.previousWorkspace && !test.emptyLabel && !test.missing &&
					(!test.borrowed || !f.RealtimeEpochsReady())
				if (left.Error == "") != wantAccess {
					t.Fatal("source construction changed live authorization", credential.name, left.Error)
				}
			}
			if snapshot != nil {
				var one int
				if err := snapshot.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
					t.Fatal("event policy source closed its borrowed snapshot", err)
				}
			}
			if view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("event policy source decoded a full workspace or retained work")
			}
		})
	}
	afterRoot, afterRows := scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace)
	if !bytes.Equal(beforeRoot, afterRoot) || !reflect.DeepEqual(beforeRows, afterRows) {
		t.Fatal("event policy source read changed canonical metadata")
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	refreshContext := t.Context()
	t.Run("current-logical-source-survives-separate-policy-refresh", func(t *testing.T) {
		want, err := f.ReadEventPolicySource(t.Context(), workspace, version)
		if err != nil {
			t.Fatal(err)
		}
		view.mu.Lock()
		policy := cloneBootstrap(view.workspaces[workspace])
		if len(policy.Teams) == 0 {
			view.mu.Unlock()
			t.Fatal("separate policy fixture has no team")
		}
		policy.Teams[0].Key = "SEPARATELY-REFRESHED"
		settings := policy.TeamSettings["sw73-child"]
		settings.Access = "private"
		policy.TeamSettings["sw73-child"] = settings
		view.workspaces[workspace] = policy
		view.metadataResidency.mu.Lock()
		logical := view.metadataResidency.logical[workspace]
		stamp := logical.Stamp()
		view.metadataResidency.mu.Unlock()
		view.mu.Unlock()
		t.Cleanup(func() {
			if err := view.RefreshInstalledMetadataViewForTesting(refreshContext, f.SQLiteStore, workspace); err != nil {
				t.Error(err)
			}
		})
		if stamp != (copyStamp{workspaceID: version.WorkspaceID, revision: version.Metadata}) ||
			reflect.DeepEqual(resourcePolicyMetadata(policy), want.metadata) {
			t.Fatal("separate policy fixture did not diverge from the certified logical generation")
		}
		queries := 0
		got, err := view.ReadEventPolicySource(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), workspace, version)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.metadata, want.metadata) || got.Bytes() != want.Bytes() ||
			got.version != version || queries != 0 {
			t.Fatal("separate resident policy replaced the exact logical event generation", queries)
		}
		view.metadataResidency.mu.Lock()
		unchanged := view.metadataResidency.logical[workspace] == logical && logical.Stamp() == stamp
		view.metadataResidency.mu.Unlock()
		if !unchanged || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("logical generation changed or retained work after event construction")
		}
	})
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if source, err := view.ReadEventPolicySource(cancelled, workspace, version); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("bounded current source ignored cancellation", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	source, budgetErr := view.ReadEventPolicySource(t.Context(), workspace, version)
	view.metadataResidency.work.limit = limit
	if source != nil || !errors.Is(budgetErr, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("bounded current source escaped admission or leaked failed work", budgetErr)
	}
	budgetTeamID := f.data.Teams[0].ID
	oversizedKey := strings.Repeat("required policy key ", 18000)
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw73_source_budget", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID == budgetTeamID {
				data.Teams[index].Key = oversizedKey
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	installed, found := f.WorkspaceMetadataFields(workspace, "teams", "teamSettings")
	projected := resourcePolicyMetadata(installed)
	index := slices.IndexFunc(projected.Teams, func(team domain.Team) bool { return team.ID == budgetTeamID })
	if !found || index < 0 || projected.Teams[index].Key != oversizedKey || len(projected.Teams[index].Key) <= 256<<10 {
		t.Fatal("source budget fixture did not retain more than 256 KiB of selected policy key bytes")
	}
	version, err = f.RealtimeSourceVersion(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	beforeRoot, beforeRows = scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace)
	for _, repository := range []*SQLiteStore{f.SQLiteStore, view} {
		if source, err := repository.ReadEventPolicySource(t.Context(), workspace, version); source != nil || !errors.Is(err, ErrEventSourceBudget) {
			t.Fatal("oversized selected policy did not retain the source budget error", err)
		}
	}
	afterRoot, afterRows = scaleP21SW23MigrationRows(t, f.SQLiteStore, workspace)
	if !bytes.Equal(beforeRoot, afterRoot) || !reflect.DeepEqual(beforeRows, afterRows) ||
		view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("event source refusal changed canonical state, decoded full metadata or leaked work")
	}
}
