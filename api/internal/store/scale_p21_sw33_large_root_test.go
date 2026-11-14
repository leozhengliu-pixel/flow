package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW33LargeRoot(t *testing.T) (scaleP09Fixture, *SQLiteStore, int) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "members"), workspace, "scale_p21_sw33.large_inline", "", nil, func(data *domain.Bootstrap) error {
		data.Members = nil
		for range 1024 {
			id := idgen.New("sw33-member-")
			data.Members = append(data.Members, domain.WorkspaceMember{
				User: domain.User{ID: id, Name: "Member \u65e5" + strings.Repeat("m", 48), DisplayName: "Display \u6708" + strings.Repeat("d", 48),
					Email: id + "@example.test", AvatarURL: "https://example.test/" + strings.Repeat("a", 256), Active: true},
				Role: "member", Status: "active", JoinedAt: time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC),
			})
		}
		return nil
	}); err != nil {
		t.Fatal("literal accepted inline producer", err)
	}
	var size int
	if err := f.db.QueryRowContext(t.Context(), "SELECT LENGTH(data) FROM workspace_states WHERE workspace_key=?", workspace).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size <= MetadataRecordPageMaxBytes || size >= f.maxStateBytes {
		t.Fatalf("root does not exercise an accepted large inline record: bytes=%d", size)
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, viewer: f.viewer,
		workspaces: map[string]domain.Bootstrap{}, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	t.Cleanup(view.releaseMetadataLogicalSources)
	return f, view, size
}

func TestScaleP21SW33LargeInlineRootColdDifferential(t *testing.T) {
	f, view, size := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	legacySQL := 0
	legacyContext := WithSQLStatementObserver(t.Context(), func(string) { legacySQL++ })
	legacy, legacyStamp, err := f.loadWorkspaceStamped(legacyContext, workspace)
	if err != nil {
		t.Fatal("literal full cold oracle", err)
	}
	boundedSQL, rootChunks := 0, 0
	boundedContext := WithSQLStatementObserver(t.Context(), func(query string) {
		boundedSQL++
		if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
			rootChunks++
		}
	})
	ctx, release, err := view.admitMetadataWork(boundedContext, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	policy, stamp, _, source, err := view.readMetadataLogicalWorkspace(ctx, tx, workspace)
	if err != nil {
		t.Fatal("streamed cold root", err)
	}
	defer source.Release()
	scope := MetadataRecordScope{Fields: []string{"members"}}
	selectedContext, releaseSelected, err := view.tryAdmitAdditionalMetadataWork(ctx, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSelected()
	actual, err := source.Select(selectedContext, scope, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	expected := metadataRecordSnapshot(legacy, scope, metadataRecordFields(scope))
	if stamp != legacyStamp || !reflect.DeepEqual(actual, expected) || len(actual.Members) != 1024 {
		t.Fatalf("large-root cold values changed: stamp=%v/%v members=%d/%d", stamp, legacyStamp, len(actual.Members), len(expected.Members))
	}
	if !reflect.DeepEqual(policy.Teams, legacy.Teams) || !reflect.DeepEqual(policy.TeamSettings, legacy.TeamSettings) ||
		!reflect.DeepEqual(policy.Workspace, legacy.Workspace) || !reflect.DeepEqual(policy.Viewer, legacy.Viewer) {
		t.Fatal("large root changed resident core or team policy")
	}
	releaseSelected()
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 2<<20 {
		t.Fatal("cold root retained transient work or invoked full metadata decode")
	}
	t.Logf("literal producer accepted %d-byte inline root; full cold and bounded logical cold compare exactly for 1024 complete members", size)
	t.Logf("SQL statements old=%d bounded=%d root SUBSTR chunks=%d; whole-member comparison owns a separate 16MiB lease", legacySQL, boundedSQL, rootChunks)
}

func TestScaleP21SW33LargeInlineRootPeerReload(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	// This isolated view does not certify the unrelated scalar-catalog
	// backfill. A real peer write must still force its normal cold reload.
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	warm, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	warmStamp := warm.Stamp()
	warm.Release()
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "members"), workspace, "scale_p21_sw33.peer_inline", "", nil, func(data *domain.Bootstrap) error {
		data.Members[0].User.Name += " peer"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy, stamp, err := f.loadWorkspaceStamped(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if stamp == warmStamp {
		t.Fatal("peer producer did not require cold reload")
	}
	if err := view.reloadResidentWorkspace(t.Context(), workspace); err != nil {
		t.Fatal("actual bounded test-view peer reload", err)
	}
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	ctx, release, err := view.admitMetadataWork(t.Context(), 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	scope := MetadataRecordScope{Fields: []string{"members"}}
	actual, err := source.Select(ctx, scope, 16<<20)
	expected := metadataRecordSnapshot(legacy, scope, metadataRecordFields(scope))
	if err != nil || source.Stamp() != stamp || !reflect.DeepEqual(actual, expected) {
		release()
		t.Fatal("actual reload installed a different generation or member source", err)
	}
	release()
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("actual reload leaked admission or used full hydration")
	}
}

func TestScaleP21SW33LargeInlineRootCancellation(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	chunks := 0
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
			chunks++
			if chunks == 4 {
				cancel()
			}
		}
	})
	ctx, release, err := view.admitMetadataWork(ctx, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		release()
		t.Fatal(err)
	}
	_, _, _, source, err := view.readMetadataLogicalWorkspace(ctx, tx, f.data.Workspace.URLKey)
	if source != nil {
		source.Release()
		t.Error("cancelled root published a logical source")
	}
	tx.Rollback()
	release()
	if !errors.Is(err, context.Canceled) || chunks < 4 || view.metadataResidency.work.used != 0 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatalf("cancelled large root leaked work/source or lost cancellation: chunks=%d err=%v", chunks, err)
	}
}

func TestScaleP21SW33LargeInlineRootRepeatedRecognizedProperties(t *testing.T) {
	f, view, _ := scaleP21SW33LargeRoot(t)
	workspace := f.data.Workspace.URLKey
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	repeated := []byte("{" + strings.Repeat(`"viewerRole":"earlier-role",`, 300) + string(raw[1:]))
	if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", repeated, workspace); err != nil {
		t.Fatal(err)
	}
	legacy, stamp, err := f.loadWorkspaceStamped(t.Context(), workspace)
	if err != nil {
		t.Fatal("literal duplicate-property cold oracle", err)
	}
	ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, actualStamp, _, source, err := view.readMetadataLogicalWorkspace(ctx, tx, workspace)
	if err != nil {
		t.Fatal("recognized duplicate keys exceeded a false cardinality limit", err)
	}
	defer source.Release()
	selected, releaseSelected, err := view.tryAdmitAdditionalMetadataWork(ctx, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSelected()
	scope := MetadataRecordScope{Fields: []string{"members"}}
	actual, err := source.Select(selected, scope, 16<<20)
	expected := metadataRecordSnapshot(legacy, scope, metadataRecordFields(scope))
	if err != nil || actualStamp != stamp || !reflect.DeepEqual(actual, expected) || actual.ViewerRole == "earlier-role" {
		t.Fatal("duplicate recognized-root precedence differs from the full decoder", err)
	}
}
