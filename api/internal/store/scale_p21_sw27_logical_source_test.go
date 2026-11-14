package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW27WarmLogicalFixture(t *testing.T) (scaleP09Fixture, domain.Bootstrap, copyStamp) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	stamp := time.Date(2026, 11, 13, 12, 34, 56, 789, time.UTC)
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21_sw27.source_seed", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = []domain.IntegrationConnection{{
			ID: idgen.New("sw27-connection-"), Provider: "git\x00hub",
			Name: string([]byte{'n', 0xff, 'm'}), Status: "configured",
			Scopes: nil, Channels: []string{}, CreatedAt: stamp, UpdatedAt: stamp,
			Config: map[string]string{"warm": "value\x00with\x00separators"},
		}}
		data.Cycles = []domain.Cycle{{ID: idgen.New("sw27-cycle-"), TeamID: data.Teams[0].ID}}
		delete(data.UserSettings, data.Viewer.ID)
		data.Settings["sw27Typed"] = map[string]any{
			"integer":      int(7),
			"largeInteger": int64(9007199254740993),
			"unsigned":     uint64(18446744073709551615),
			"bytes":        []byte{0, 255, 1},
			"nilSlice":     []string(nil),
			"emptySlice":   []string{},
			"nilMap":       map[string]any(nil),
			"emptyMap":     map[string]any{},
			"time":         stamp,
		}
		return nil
	}); err != nil {
		t.Fatal("accepted typed metadata producer", err)
	}
	warm, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("literal installed metadata accessor lost owned workspace")
	}
	storedStamp, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	return f, warm, storedStamp
}

func scaleP21SW27AssertWarmSource(t *testing.T, data domain.Bootstrap) {
	t.Helper()
	if len(data.IntegrationConnections) != 1 ||
		data.IntegrationConnections[0].Provider != "git\x00hub" ||
		data.IntegrationConnections[0].Name != string([]byte{'n', 0xff, 'm'}) ||
		data.IntegrationConnections[0].Scopes != nil ||
		data.IntegrationConnections[0].Channels == nil ||
		len(data.Cycles) != 1 || data.Cycles[0].Resources != nil || data.Cycles[0].Insight != nil {
		t.Fatal("warm installed typed state or nil/empty collection shape changed")
	}
	if _, exists := data.UserSettings[data.Viewer.ID]; exists {
		t.Fatal("warm callback's explicit preference removal was normalized")
	}
	values, ok := data.Settings["sw27Typed"].(map[string]any)
	if !ok {
		t.Fatalf("warm settings concrete map changed: %T", data.Settings["sw27Typed"])
	}
	want := map[string]any{
		"integer":      int(7),
		"largeInteger": int64(9007199254740993),
		"unsigned":     uint64(18446744073709551615),
		"bytes":        []byte{0, 255, 1},
		"nilSlice":     []string(nil),
		"emptySlice":   []string{},
		"nilMap":       map[string]any(nil),
		"emptyMap":     map[string]any{},
		"time":         time.Date(2026, 11, 13, 12, 34, 56, 789, time.UTC),
	}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("warm concrete interface graph changed: got=%#v want=%#v", values, want)
	}
}

func TestScaleP21SW27LiteralLogicalLifecycleBaseline(t *testing.T) {
	f, warm, stamp := scaleP21SW27WarmLogicalFixture(t)
	scaleP21SW27AssertWarmSource(t, warm)
	workspace := f.data.Workspace.URLKey
	var connectionRaw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='integrationConnections' AND record_key=?`,
		workspace, warm.IntegrationConnections[0].ID).Scan(&connectionRaw); err != nil {
		t.Fatal(err)
	}
	var canonical domain.IntegrationConnection
	if err := json.Unmarshal(connectionRaw, &canonical); err != nil {
		t.Fatal(err)
	}
	if canonical.Provider != "github" || canonical.Name == warm.IntegrationConnections[0].Name {
		t.Fatal("fixture no longer distinguishes accepted typed state from canonical JSON")
	}
	cold, coldStamp, err := f.loadWorkspaceStamped(t.Context(), workspace)
	if err != nil || coldStamp != stamp {
		t.Fatal("literal cold load", coldStamp, stamp, err)
	}
	if len(cold.Cycles) != 1 || cold.Cycles[0].Resources == nil || cold.Cycles[0].Insight == nil ||
		cold.IntegrationConnections[0].Scopes == nil {
		t.Fatal("literal cold normalization no longer synthesizes collection defaults")
	}
	if _, exists := cold.UserSettings[warm.Viewer.ID]; !exists {
		t.Fatal("literal cold normalization no longer synthesizes user settings")
	}
	if reflect.DeepEqual(cold.Settings["sw27Typed"], warm.Settings["sw27Typed"]) {
		t.Fatal("canonical JSON unexpectedly preserved the accepted typed interface graph")
	}
	known, exact := f.copyRevisionOf(workspace)
	expectedSnapshot := cold
	if exact && known.workspaceID == stamp.workspaceID && known.revision == stamp.revision {
		expectedSnapshot = warm
	}
	snapshotStarted := time.Now().UTC()
	snapshotCtx, release, err := f.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal("literal request snapshot", err)
	}
	defer release()
	snapshot := snapshotCtx.Value(workspaceReadKey{}).(workspaceRead)
	if !exact || known.workspaceID != stamp.workspaceID || known.revision != stamp.revision {
		observed := snapshot.metadata.UserSettings[warm.Viewer.ID]
		if observed.UpdatedAt.Before(snapshotStarted) || observed.UpdatedAt.After(time.Now().UTC()) {
			t.Fatal("literal cold handoff did not create its missing preference during normalization")
		}
		// Each independent old cold load synthesizes this timestamp at load
		// time; every business/default field remains in the exact comparison.
		expected := expectedSnapshot.UserSettings[warm.Viewer.ID]
		expected.UpdatedAt = observed.UpdatedAt
		expectedSnapshot.UserSettings[warm.Viewer.ID] = expected
	}
	fields := []string{"integrationConnections", "cycles", "settings", "userSettings"}
	if !reflect.DeepEqual(metadataFieldSnapshot(snapshot.metadata, fields), metadataFieldSnapshot(expectedSnapshot, fields)) {
		t.Fatal("literal request snapshot changed its installed-stamp versus cold-load source decision")
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections[0].Status = "newer-generation"
		return nil
	}); err != nil {
		t.Fatal("next installed generation", err)
	}
	_, pinned, found := f.workspaceReadSource(snapshotCtx, workspace)
	if !found || pinned.IntegrationConnections[0].Status != "configured" {
		t.Fatal("literal request handoff stopped retaining its earlier generation")
	}
	live, found := f.WorkspaceMetadata(workspace)
	if !found || live.IntegrationConnections[0].Status != "newer-generation" {
		t.Fatal("literal live accessor did not advance separately from its handoff")
	}
	before, err := json.Marshal(metadataFieldSnapshot(snapshot.metadata, fields))
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(metadataFieldSnapshot(pinned, fields))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("literal pinned source changed after a newer installed generation", err)
	}
	var measurement runtime.MemStats
	runtime.ReadMemStats(&measurement)
	allocated, mallocs := measurement.TotalAlloc, measurement.Mallocs
	for range 10 {
		owned, ok := f.WorkspaceMetadata(workspace)
		if !ok || owned.IntegrationConnections[0].Status != "newer-generation" {
			t.Fatal("literal baseline selection failed")
		}
	}
	runtime.ReadMemStats(&measurement)
	t.Logf("literal installed metadata read baseline: bytes/op=%d allocs/op=%d metadata-decodes=%d",
		(measurement.TotalAlloc-allocated)/10, (measurement.Mallocs-mallocs)/10, f.WorkspaceMetadataDecodes())
	t.Logf("literal request snapshot used exact installed source=%v", exact && known.workspaceID == stamp.workspaceID && known.revision == stamp.revision)
}

func scaleP21SW27CancelledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

func TestScaleP21SW27CapturedLogicalSourceWarmAndReloaded(t *testing.T) {
	f, warm, stamp := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	cold, coldStamp, err := f.loadWorkspaceStamped(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	scope := MetadataRecordScope{Fields: []string{"integrationConnections", "cycles", "settings", "userSettings"}}
	for _, fixture := range []struct {
		name  string
		data  domain.Bootstrap
		stamp copyStamp
	}{
		{"accepted-warm", warm, stamp},
		{"literal-reloaded-normalized", cold, coldStamp},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			source, err := captureMetadataLogicalSource(t.Context(), fixture.data, fixture.stamp)
			if err != nil {
				t.Fatal("capture literal logical source", err)
			}
			defer source.Release()
			if source.Stamp() != fixture.stamp {
				t.Fatal("capture lost its installed generation stamp", source.Stamp(), fixture.stamp)
			}
			actual, err := source.Select(t.Context(), scope, 2<<20)
			if err != nil {
				t.Fatal("select exact logical input", err)
			}
			expected := metadataRecordSnapshot(fixture.data, scope, metadataRecordFields(scope))
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("bounded source replaced literal %s values with another representation:\nactual=%#v\nexpected=%#v", fixture.name, actual.Settings, expected.Settings)
			}
			if fixture.name == "accepted-warm" {
				scaleP21SW27AssertWarmSource(t, actual)
			}
			actual.Settings["sw27Typed"].(map[string]any)["integer"] = "caller-owned"
			actual.IntegrationConnections[0].Config["warm"] = "caller-owned"
			reread, err := source.Select(t.Context(), scope, 2<<20)
			if err != nil || !reflect.DeepEqual(reread, expected) {
				t.Fatal("returned mutable map retained or changed source ownership", err)
			}
		})
	}
}

func TestScaleP21SW27CapturedLogicalSourceRetainedGeneration(t *testing.T) {
	f, warm, stamp := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	scope := MetadataRecordScope{Fields: []string{"integrationConnections", "cycles", "settings", "userSettings"}}
	source, err := captureMetadataLogicalSource(t.Context(), warm, stamp)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			source.Release()
		}
	}()
	if err := source.Retain(); err != nil {
		t.Fatal(err)
	}
	source.Release()
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections", "settings"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections[0].Status = "newer-current-generation"
		data.Settings["sw27Typed"] = "newer-current-generation"
		return nil
	}); err != nil {
		t.Fatal("peer/current source mutation", err)
	}
	newStamp, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || newStamp.revision <= stamp.revision {
		t.Fatal("test did not advance canonical source beyond retained generation", newStamp, stamp, err)
	}
	actual, err := source.Select(t.Context(), scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	scaleP21SW27AssertWarmSource(t, actual)
	if actual.IntegrationConnections[0].Status != "configured" || source.Stamp() != stamp {
		t.Fatal("retained old handoff followed latest canonical rows")
	}
	source.Release()
	released = true
	if err := source.Retain(); err == nil {
		t.Fatal("retired logical generation was resurrected")
	}
	if _, err := source.Select(t.Context(), scope, 2<<20); err == nil {
		t.Fatal("retired logical generation remained readable after final release")
	}
}

func TestScaleP21SW27CapturedLogicalSourceLargeAndCancelled(t *testing.T) {
	f, warm, stamp := scaleP21SW27WarmLogicalFixture(t)
	decodes := f.WorkspaceMetadataDecodes()
	warm.Settings["sw27Large"] = strings.Repeat("legal body ", 1<<20)
	source, err := captureMetadataLogicalSource(t.Context(), warm, stamp)
	if err != nil {
		t.Fatal("legal large unrelated value rejected at capture", err)
	}
	defer source.Release()
	scope := MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Typed"}}}
	actual, err := source.Select(t.Context(), scope, 256<<10)
	if err != nil || !reflect.DeepEqual(actual.Settings["sw27Typed"], warm.Settings["sw27Typed"]) {
		t.Fatal("unrelated legal large record prevented bounded point selection", err)
	}
	if _, exists := actual.Settings["sw27Large"]; exists {
		t.Fatal("bounded point selection included unrelated large body")
	}
	if _, err := source.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Large"}}}, 256<<10); err == nil {
		t.Fatal("large selected value bypassed decoded-work budget")
	}
	cancelled := scaleP21SW27CancelledContext(t)
	if _, err := source.Select(cancelled, scope, 256<<10); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled logical selection changed cancellation", err)
	}
	before := recordSpoolBytes.Load()
	cancelledSource, err := captureMetadataLogicalSource(cancelled, warm, stamp)
	if cancelledSource != nil || !errors.Is(err, context.Canceled) || recordSpoolBytes.Load() != before {
		t.Fatal("cancelled source capture allocated or leaked storage", cancelledSource != nil, err, recordSpoolBytes.Load(), before)
	}
	if f.WorkspaceMetadataDecodes() != decodes {
		t.Fatal("logical point read decoded full canonical metadata")
	}
}

func TestScaleP21SW27LogicalSourceIntegratedWarmHandoff(t *testing.T) {
	f, warm, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal("capture owner-installed logical view", err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	scope := MetadataRecordScope{Fields: []string{"integrationConnections", "cycles", "settings", "userSettings"}}
	expected := metadataRecordSnapshot(warm, scope, metadataRecordFields(scope))
	read := func(label string) {
		t.Helper()
		actual, found, err := view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
		if err != nil || !found || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s bounded accessor did not read actual owner-installed state: found=%v error=%v settings=%#v", label, found, err, actual.Settings)
		}
		scaleP21SW27AssertWarmSource(t, actual)
	}
	read("initial")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "integrationConnections", "settings"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections[0].Provider = "peer-current"
		data.Settings["sw27Typed"] = map[string]any{"new": true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read("retained after canonical/current-owner advance")
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("integrated logical source decoded full metadata or leaked request admission")
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	offExpected, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("old store lost workspace")
	}
	actual, found, err := f.WorkspaceMetadataScoped(scaleP21SW27CancelledContext(t), workspace, MetadataRecordScope{Fields: []string{"not-a-real-field"}})
	if err != nil || !found || !reflect.DeepEqual(actual, offExpected) {
		t.Fatal("flag-off store stopped forwarding literal original accessor before context/scope validation", found, err)
	}
}

func TestScaleP21SW27LogicalSourceStreamsLegalLargeBytes(t *testing.T) {
	raw := append(bytes.Repeat([]byte{'a', 0, 255, 128, 'z'}, 1<<20), []byte("tail")...)
	body := string(raw)
	data := domain.Bootstrap{Settings: map[string]any{"largeString": body, "largeBytes": raw}}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: "sw27-stream", revision: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	visited := 0
	err = source.VisitFields(t.Context(), func(name string, root metadataLogicalNode) error {
		if name != "settings" {
			return nil
		}
		return root.Children(t.Context(), func(key reflect.Value, value metadataLogicalNode) error {
			if key.String() != "largeString" && key.String() != "largeBytes" {
				return nil
			}
			return value.Children(t.Context(), func(_ reflect.Value, primitive metadataLogicalNode) error {
				if _, err := primitive.Value(t.Context(), 256<<10); !errors.Is(err, ErrMetadataWorkBudget) {
					t.Fatal("large primitive materialized outside decoded budget", err)
				}
				hash := sha256.New()
				var count int64
				if err := primitive.StreamBytes(t.Context(), func(input io.Reader) error {
					var err error
					count, err = io.CopyBuffer(hash, input, make([]byte, 32<<10))
					return err
				}); err != nil {
					return err
				}
				want := sha256.Sum256(raw)
				if count != int64(len(raw)) || !bytes.Equal(hash.Sum(nil), want[:]) {
					t.Fatal("legal large primitive stream truncated or sanitized bytes", count, len(raw))
				}
				cancelled, cancel := context.WithCancel(t.Context())
				defer cancel()
				err := primitive.StreamBytes(cancelled, func(input io.Reader) error {
					buffer := make([]byte, 16)
					if _, err := io.ReadFull(input, buffer); err != nil {
						return err
					}
					cancel()
					_, err := input.Read(buffer)
					return err
				})
				if !errors.Is(err, context.Canceled) {
					t.Fatal("large primitive stream ignored midstream cancellation", err)
				}
				visited++
				return nil
			})
		})
	})
	if err != nil || visited != 2 {
		t.Fatal("string and byte slice streams were not both exercised", visited, err)
	}
}

func TestScaleP21SW27LogicalSourceForkPreservesUnselectedGraph(t *testing.T) {
	_, warm, stamp := scaleP21SW27WarmLogicalFixture(t)
	source, err := captureMetadataLogicalSource(t.Context(), warm, stamp)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	scope := MetadataRecordScope{Write: []RecordKey{
		{Kind: "integrationConnections", ID: warm.IntegrationConnections[0].ID},
		{Kind: "settings", ID: "sw27Changed"},
	}}
	before, err := source.Select(t.Context(), scope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	next := cloneBootstrap(before)
	next.IntegrationConnections[0].Status = "updated\x00typed"
	next.Settings["sw27Changed"] = int16(-123)
	stamp.revision++
	fork, err := source.Fork(t.Context(), before, next, scope, stamp)
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Release()
	readScope := MetadataRecordScope{Fields: []string{"integrationConnections", "cycles", "settings", "userSettings"}}
	actual, err := fork.Select(t.Context(), readScope, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	expected := metadataRecordSnapshot(warm, readScope, metadataRecordFields(readScope))
	expected.IntegrationConnections[0].Status = "updated\x00typed"
	expected.Settings["sw27Changed"] = int16(-123)
	if !reflect.DeepEqual(actual, expected) || fork.Stamp() != stamp {
		t.Fatal("logical point fork lost unrelated typed state or its publication stamp")
	}
	old, err := source.Select(t.Context(), readScope, 2<<20)
	if err != nil || !reflect.DeepEqual(old, metadataRecordSnapshot(warm, readScope, metadataRecordFields(readScope))) {
		t.Fatal("fork mutated retained before generation", err)
	}
	delete(next.Settings, "sw27Changed")
	next.IntegrationConnections = nil
	deleted, err := fork.Fork(t.Context(), before, next, scope, copyStamp{workspaceID: stamp.workspaceID, revision: stamp.revision + 1})
	if err != nil {
		t.Fatal("logical point deletion", err)
	}
	defer deleted.Release()
	actual, err = deleted.Select(t.Context(), readScope, 2<<20)
	delete(expected.Settings, "sw27Changed")
	expected.IntegrationConnections = []domain.IntegrationConnection{}
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal("point delete changed unrelated fields or nonnil collection membership", err)
	}
}

func TestScaleP21SW27LogicalSourceAcceptedOpaqueTimestampHold(t *testing.T) {
	f, _, stamp := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	fixed := time.Date(2026, 11, 13, 9, 8, 7, 6, time.FixedZone("sw27-owned-zone", 3456))
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw27OpaqueTime"] = fixed
		return nil
	}); err != nil {
		t.Fatal("literal accepted native timestamp producer", err)
	}
	literal, found := f.WorkspaceMetadata(workspace)
	if !found || literal.Settings["sw27OpaqueTime"] != fixed {
		t.Fatal("literal accessor changed accepted opaque native time")
	}
	unsupported, err := captureMetadataLogicalSource(t.Context(), literal, stamp)
	if err != nil || unsupported == nil {
		t.Fatal("unselected opaque timestamp prevented other source records", err)
	}
	defer unsupported.Release()
	if _, err := unsupported.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27OpaqueTime"}}}, 256<<10); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("selected opaque timestamp did not retain an explicit activation blocker", err)
	}
	if _, err := unsupported.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Typed"}}}, 256<<10); err != nil {
		t.Fatal("unrelated opaque timestamp blocked a supported typed point read", err)
	}
	t.Log("coverage hold: arbitrary native time.Location accepted by legacy writes still requires an audited detached representation")
}

func scaleP21SW27CompareRawLogicalRecovery(t *testing.T, f scaleP09Fixture) {
	t.Helper()
	view := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, metadataResidency: newMetadataResidency()}
	view.metadataResidency.requireCoverage = false
	ctx, release, err := view.admitMetadataWork(t.Context(), 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	workspace := f.data.Workspace.URLKey
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	oracle, err := f.decodeWorkspaceMetadata(ctx, workspace, raw, tx)
	if err != nil {
		t.Fatal("literal raw metadata decode", err)
	}
	source, err := view.readRawMetadataLogicalSource(ctx, tx, workspace)
	if err != nil {
		t.Fatal("bounded raw metadata recovery", err)
	}
	defer source.Release()
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil || source.Stamp() != stamp {
		t.Fatal("raw source did not retain the same SQL snapshot stamp", source.Stamp(), stamp, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	actual := reflect.New(reflect.TypeFor[domain.Bootstrap]()).Elem()
	if err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		index, _, exists := metadataLogicalField(name)
		if !exists || name == "-" {
			return nil
		}
		value, err := node.Value(ctx, 64<<20)
		if err != nil {
			return err
		}
		actual.Field(index).Set(value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual.Interface(), cloneBootstrap(oracle)) {
		expected := reflect.ValueOf(cloneBootstrap(oracle))
		for index := 0; index < actual.NumField(); index++ {
			if !reflect.DeepEqual(actual.Field(index).Interface(), expected.Field(index).Interface()) {
				t.Errorf("raw source field %s changed: got=%#v want=%#v", actual.Type().Field(index).Name, actual.Field(index).Interface(), expected.Field(index).Interface())
			}
		}
		t.Fatal("bounded raw source differs from literal full raw hydration")
	}
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("bounded raw source decoded full workspace")
	}
}

func TestScaleP21SW27RawLogicalRecoveryMatchesLegacyRows(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	scaleP21SW27CompareRawLogicalRecovery(t, f)
}

func TestScaleP21SW27RawLogicalRecoveryRepeatedInlineAndUpdates(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=?`, workspace); err != nil {
		t.Fatal(err)
	}
	root := `{"workspace":{"id":"first","name":"retained"},"WORKSPACE":{"id":"final"},"users":[{"id":"a","name":"first"},{"id":"b","name":"second"},{"id":"c","name":"third"}],"USERS":[{"id":"short"}],"users":[{"id":"new"},{"id":"second-new"},{"id":"third-new"}],"settings":{"old":{"before":true}},"SETTINGS":null,"settings":{"kept":9,"":true},"projectUpdates":{"parent":[{"id":"inline","body":"retained"}],"empty":[]},"projectDisplayDefault":{"kept": 3,"html":"<body>"},"workspaceSettings":{"allowedDomains":["first","retained"],"name":"before"},"WORKSPACESETTINGS":{"allowedDomains":["changed"]},"_flowCollections":{"users":"array","projectUpdates":"updates","settings":"map","projectDisplayDefault":"map","workspaceSettings":"map"}}`
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, []byte(root), workspace); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ kind, key, raw string }{
		{"users", "canonical", `{"id":"canonical","name":"appended"}`},
		{"settings", "canonical", `{"typed":2,"empty":[]}`},
		{"projectUpdates", "first", `{"parent":"parent","item":{"id":"update","body":"appended"}}`},
		{"projectUpdates", "nil", `{"parent":"nil","null":true}`},
		{"projectUpdates", "empty", `{"parent":"empty"}`},
		{"projectDisplayDefault", "canonical", `{"nested": "<new>"}`},
		{"projectDisplayDefault", "kept", `5`},
		{"workspaceSettings", "name", `"canonical"`},
	} {
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?)`, workspace, row.kind, row.key, []byte(row.raw)); err != nil {
			t.Fatal(err)
		}
	}
	scaleP21SW27CompareRawLogicalRecovery(t, f)
}

func TestScaleP21SW27RawLogicalRecoveryCancellationAndQuotas(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	view := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, metadataResidency: newMetadataResidency()}
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
	for _, mode := range []string{"mid-recovery-cancel", "source-disk-quota"} {
		t.Run(mode, func(t *testing.T) {
			startDisk, startDescriptors, startSpool := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
			request := ctx
			var injected int64
			restore := func() {
				if injected != 0 {
					metadataLogicalDiskUsed.Add(-injected)
					injected = 0
				}
			}
			defer restore()
			if mode == "mid-recovery-cancel" {
				request = &scaleP21SW27CancelAfterChecks{Context: ctx, limit: 20}
			} else {
				injected = metadataLogicalDiskBytes - metadataLogicalDiskUsed.Load()
				if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, injected, metadataLogicalDiskBytes) {
					injected = 0
					t.Fatal("could not reserve remaining logical disk quota")
				}
			}
			source, err := view.readRawMetadataLogicalSource(request, tx, f.data.Workspace.URLKey)
			if source != nil {
				source.Release()
				t.Fatal("failed recovery published a partial logical source")
			}
			want := context.Canceled
			if mode == "source-disk-quota" {
				want = ErrRecordSpoolBudget
			}
			restore()
			if !errors.Is(err, want) {
				t.Fatal("raw recovery failure changed cancellation/quota reason", mode, err)
			}
			if metadataLogicalDiskUsed.Load() != startDisk || metadataLogicalDescriptorsUsed.Load() != startDescriptors ||
				recordSpoolBytes.Load() != startSpool || view.metadataResidency.work.used != 2<<20 {
				t.Fatal("failed raw recovery leaked source, scratch database, spool, or work allowance")
			}
		})
	}
}

func TestScaleP21SW27RawLogicalRecoveryLegalLargeRecord(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	for _, size := range []int{5 << 20, 8 << 20} {
		raw, err := json.Marshal(domain.User{ID: f.data.Viewer.ID, Name: strings.Repeat("x", size)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='users' AND record_key=?`, raw, workspace, f.data.Viewer.ID); err != nil {
			t.Fatal(err)
		}
		scaleP21SW27CompareRawLogicalRecovery(t, f)
	}
	t.Run("decoded-object-budget", func(t *testing.T) {
		dense := []byte("[" + strings.Repeat("0,", 2100000) + "0]")
		peer, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Rollback()
		if err := writeMetadataRecordRow(t.Context(), peer, workspace, metadataRecordKey{"settings", "sw27Dense"}, metadataRecord{data: dense}, nil); err != nil {
			t.Fatal(err)
		}
		if err := peer.Commit(); err != nil {
			t.Fatal(err)
		}
		view := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, metadataResidency: newMetadataResidency()}
		ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
		if err != nil {
			release()
			t.Fatal(err)
		}
		before := metadataLogicalDiskUsed.Load()
		source, err := view.readRawMetadataLogicalSource(ctx, tx, workspace)
		if source != nil {
			source.Release()
			t.Error("record beyond explicit raw recovery bound was published")
		}
		tx.Rollback()
		release()
		if !errors.Is(err, ErrMetadataWorkBudget) && !errors.Is(err, ErrMetadataPageBudget) || view.WorkspaceMetadataDecodes() != 0 ||
			view.metadataResidency.work.used != 0 || metadataLogicalDiskUsed.Load() != before {
			t.Fatal("oversized raw recovery lost explicit budget failure or leaked resources", err)
		}
		t.Logf("encoded dense record bytes=%d exceeds decoded-object process budget before allocation", len(dense))
	})
}

func TestScaleP21SW27RawLogicalRecoveryPhysicalOrder(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	if f.dialect != "sqlite" {
		t.Skip("mixed TEXT/BLOB physical record identities are a SQLite storage case")
	}
	workspace := f.data.Workspace.URLKey
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=?`, workspace); err != nil {
		t.Fatal(err)
	}
	root := []byte(`{"_flowCollections":{"users":"array"}}`)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, root, workspace); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		key any
		id  string
	}{
		{"zz", "text-last-by-bytes"},
		{[]byte("aa"), "blob-first-by-bytes"},
		{"same", "text-same-bytes"},
		{[]byte("same"), "blob-same-bytes"},
		{"", "empty-key"},
	} {
		raw, err := json.Marshal(domain.User{ID: row.id, Name: row.id})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'users',?,0,?)`, workspace, row.key, raw); err != nil {
			t.Fatal(err)
		}
	}
	scaleP21SW27CompareRawLogicalRecovery(t, f)
}

func TestScaleP21SW27RawLogicalRecoveryOrphanOffloadValidation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=?`, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'documents','orphan-invalid',0,?)`, workspace, []byte(`{"body":"document","revisions":[{"_offloaded":3}]}`)); err != nil {
		t.Fatal(err)
	}
	view := &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, metadataResidency: newMetadataResidency()}
	for _, manifest := range []string{`{}`, `{"future":"array"}`} {
		root := []byte(`{"_flowCollections":` + manifest + `}`)
		if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, root, workspace); err != nil {
			t.Fatal(err)
		}
		ctx, release, err := view.admitMetadataWork(t.Context(), 2<<20)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
		if err != nil {
			release()
			t.Fatal(err)
		}
		_, before := f.decodeWorkspaceMetadata(ctx, workspace, root, tx)
		source, after := view.readRawMetadataLogicalSource(ctx, tx, workspace)
		if source != nil {
			source.Release()
		}
		tx.Rollback()
		release()
		if (before == nil) != (after == nil) || before != nil && before.Error() != after.Error() {
			t.Fatal("raw recovery changed orphan/unmanifested canonical offload validation", manifest, before, after)
		}
		if manifest != `{}` && before == nil {
			t.Fatal("fixture did not preserve legacy validation of an orphan offloaded row")
		}
	}
}

func TestScaleP21SW27RawLogicalRecoveryJSONFixedZones(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	raw := json.RawMessage(`{"id":"sw27-json-zone","provider":"test","createdAt":"2026-11-13T01:02:03.000000004+05:45","updatedAt":"2026-11-13T01:02:03+00:00","lastWebhookAt":"2026-11-13T01:02:03-03:30"}`)
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"integrationConnections", "sw27-json-zone"}, metadataRecord{data: raw}, nil); err != nil {
		t.Fatal(err)
	}
	if err := ensureOAuthMetadataShape(t.Context(), tx, workspace, "integrationConnections", "array", ""); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	scaleP21SW27CompareRawLogicalRecovery(t, f)
}

func TestScaleP21SW27LogicalSourceNativeMonotonicTime(t *testing.T) {
	f, _, stamp := scaleP21SW27WarmLogicalFixture(t)
	first := time.Now()
	second := first.Add(37*time.Second + 123*time.Nanosecond)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), f.data.Workspace.URLKey, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw27Monotonic"] = []time.Time{first, second}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	literal, found := f.WorkspaceMetadata(f.data.Workspace.URLKey)
	if !found || !reflect.DeepEqual(literal.Settings["sw27Monotonic"], []time.Time{first, second}) {
		t.Fatal("literal warm writer dropped native monotonic time state")
	}
	source, err := captureMetadataLogicalSource(t.Context(), literal, stamp)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	actual, err := source.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Monotonic"}}}, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	times, ok := actual.Settings["sw27Monotonic"].([]time.Time)
	if !ok || len(times) != 2 || times[0] != first || times[1] != second || times[1].Sub(times[0]) != second.Sub(first) {
		t.Fatal("typed spool changed native time equality, location identity, or monotonic delta", times)
	}
}

type scaleP21SW27CancelAfterChecks struct {
	context.Context
	checks, limit int
}

func (ctx *scaleP21SW27CancelAfterChecks) Err() error {
	ctx.checks++
	if ctx.checks >= ctx.limit {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestScaleP21SW27LogicalSourceCaptureQuotasAndCleanup(t *testing.T) {
	diskBefore, descriptorsBefore := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	data := domain.Bootstrap{Settings: map[string]any{"small": "value"}}
	t.Run("descriptor-admission", func(t *testing.T) {
		reserved := int64(metadataLogicalDescriptorLimit) - metadataLogicalDescriptorsUsed.Load()
		if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, reserved, metadataLogicalDescriptorLimit) {
			t.Fatal("test could not reserve remaining descriptor quota")
		}
		defer metadataLogicalDescriptorsUsed.Add(-reserved)
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
		if source != nil || !errors.Is(err, ErrMetadataWorkBudget) || metadataLogicalDiskUsed.Load() != diskBefore {
			t.Fatal("saturated descriptor quota created a source or uncharged disk", source != nil, err)
		}
	})
	t.Run("disk-admission", func(t *testing.T) {
		reserved := int64(metadataLogicalDiskBytes) - metadataLogicalDiskUsed.Load()
		if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, reserved, metadataLogicalDiskBytes) {
			t.Fatal("test could not reserve remaining disk quota")
		}
		defer metadataLogicalDiskUsed.Add(-reserved)
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
		if source != nil || !errors.Is(err, ErrMetadataWorkBudget) || metadataLogicalDescriptorsUsed.Load() != descriptorsBefore {
			t.Fatal("saturated disk quota retained a partial source descriptor", source != nil, err)
		}
	})
	t.Run("mid-capture-cancel", func(t *testing.T) {
		ctx := &scaleP21SW27CancelAfterChecks{Context: t.Context(), limit: 12}
		source, err := captureMetadataLogicalSource(ctx, data, copyStamp{})
		if source != nil || !errors.Is(err, context.Canceled) || ctx.checks < ctx.limit {
			t.Fatal("capture ignored cancellation after storage creation", source != nil, err, ctx.checks)
		}
	})
	if metadataLogicalDiskUsed.Load() != diskBefore || metadataLogicalDescriptorsUsed.Load() != descriptorsBefore {
		t.Fatal("failed source capture leaked process accounting", metadataLogicalDiskUsed.Load(), diskBefore,
			metadataLogicalDescriptorsUsed.Load(), descriptorsBefore)
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal("admission did not recover after failures", err)
	}
	if metadataLogicalDiskUsed.Load() <= diskBefore || metadataLogicalDescriptorsUsed.Load() != descriptorsBefore+metadataLogicalDescriptorBytes {
		t.Fatal("successful source was not fully charged")
	}
	if err := source.Retain(); err != nil {
		t.Fatal(err)
	}
	source.Release()
	if metadataLogicalDescriptorsUsed.Load() != descriptorsBefore+metadataLogicalDescriptorBytes {
		t.Fatal("releasing one handoff prematurely dropped source accounting")
	}
	source.Release()
	if metadataLogicalDiskUsed.Load() != diskBefore || metadataLogicalDescriptorsUsed.Load() != descriptorsBefore {
		t.Fatal("final source release did not return all process quotas")
	}
}

func TestScaleP21SW27LogicalSourceUnknownOwnerDoesNotClaimPeerStamp(t *testing.T) {
	f, warm, originalStamp := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	peer, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Rollback()
	if err := writeMetadataRecordRow(t.Context(), peer, workspace, metadataRecordKey{"settings", "sw27PeerOnly"},
		metadataRecord{data: json.RawMessage(`"peer-canonical-only"`)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := peer.Commit(); err != nil {
		t.Fatal(err)
	}
	currentStamp, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil || currentStamp.revision <= originalStamp.revision {
		t.Fatal("peer source witness did not advance canonical metadata", currentStamp, originalStamp, err)
	}
	f.mu.Lock()
	f.forgetCopyRevisionLocked(workspace)
	f.mu.Unlock()
	literal, found := f.WorkspaceMetadata(workspace)
	if !found || !reflect.DeepEqual(literal.Settings, warm.Settings) {
		t.Fatal("old installed source unexpectedly followed independent peer write")
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		if !errors.Is(err, ErrBoundedMetadataCoverage) && !errors.Is(err, ErrConcurrentWrite) {
			t.Fatal("unknown source provenance failed without an explicit coverage/conflict reason", err)
		}
		return
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	if source.Stamp() == currentStamp {
		t.Fatal("unknown owner installed source was certified at a latest canonical stamp whose peer-only record it lacks")
	}
	selected, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"settings"}}, 2<<20)
	if err != nil || !reflect.DeepEqual(selected.Settings, warm.Settings) {
		t.Fatal("source provenance correction substituted latest SQL for literal installed data", err)
	}
}

func TestScaleP21SW27LogicalSourceStaleUncoordinatedWriterDifferential(t *testing.T) {
	f, warm, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	ctx := WithMetadataFields(t.Context(), "settings")
	scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw27Counter"}}}
	seed := func() {
		t.Helper()
		if err := f.MutateWorkspace(ctx, workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
			data.Settings["sw27Counter"] = int(7)
			delete(data.Settings, "sw27PeerOnly")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	stale := func() {
		t.Helper()
		peer, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Rollback()
		if err := writeMetadataRecordRow(t.Context(), peer, workspace, metadataRecordKey{"settings", "sw27PeerOnly"},
			metadataRecord{data: json.RawMessage(`"peer-canonical-only"`)}, nil); err != nil {
			t.Fatal(err)
		}
		if err := peer.Commit(); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.forgetCopyRevisionLocked(workspace)
		f.mu.Unlock()
	}
	type outcome struct {
		rows    map[string]string
		counter any
		peer    bool
		event   string
	}
	var outputs []outcome
	for _, bounded := range []bool{false, true} {
		seed()
		stale()
		repo := f.SQLiteStore
		if bounded {
			var err error
			repo, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(repo.releaseMetadataLogicalSources)
		}
		var realtime domain.RealtimeEvent
		repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = event })
		if err := repo.MutateWorkspaceScoped(ctx, workspace, "integration.adapter_configured", "sw27Counter", map[string]any{"probe": true}, scope, func(data *domain.Bootstrap) error {
			counter, ok := data.Settings["sw27Counter"].(int)
			if !ok || counter != 7 {
				t.Fatalf("callback source changed accepted integer: bounded=%v value=%#v", bounded, data.Settings["sw27Counter"])
			}
			if _, exists := data.Settings["sw27PeerOnly"]; exists {
				t.Fatal("uncoordinated callback substituted latest peer data for its stale installed source")
			}
			data.Settings["sw27Counter"] = counter + 1
			return nil
		}); err != nil {
			t.Fatal("uncoordinated mutation changed accepted stale source behavior", bounded, err)
		}
		repo.SetRealtimeSink(nil)
		got := outcome{rows: map[string]string{}, event: string(realtime.Payload)}
		rows, err := f.db.QueryContext(t.Context(), `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key IN ('sw27Counter','sw27PeerOnly') ORDER BY record_key`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var key string
			var value []byte
			if err := rows.Scan(&key, &value); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			got.rows[key] = string(value)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if bounded {
			source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			selected, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"settings"}}, 2<<20)
			if err != nil {
				source.Release()
				t.Fatal(err)
			}
			got.counter = selected.Settings["sw27Counter"]
			_, got.peer = selected.Settings["sw27PeerOnly"]
			if source.Stamp().revision != -1 {
				t.Error("stale uncoordinated publication invented an exact source stamp", source.Stamp())
			}
			source.Release()
		} else {
			selected, found := repo.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("literal postwrite installed source missing")
			}
			got.counter = selected.Settings["sw27Counter"]
			_, got.peer = selected.Settings["sw27PeerOnly"]
		}
		outputs = append(outputs, got)
	}
	if !reflect.DeepEqual(outputs[0], outputs[1]) || outputs[0].counter != int(8) || outputs[0].peer ||
		outputs[0].rows["sw27Counter"] != "8" || outputs[0].rows["sw27PeerOnly"] != `"peer-canonical-only"` {
		t.Fatalf("legacy/bounded stale callback, canonical merge, event or installed state diverged: %#v", outputs)
	}
	if _, ok := warm.Settings["sw27Typed"].(map[string]any); !ok {
		t.Fatal("fixture lost unrelated accepted typed graph")
	}
}

func TestScaleP21SW27LogicalSourcePointForkDiskBoundMeasurement(t *testing.T) {
	for _, size := range []int{64 << 10, 4 << 20} {
		t.Run(idgen.New("size-"), func(t *testing.T) {
			startDisk, startDescriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			data := domain.Bootstrap{Settings: map[string]any{"target": int(7), "unrelated": strings.Repeat("x", size)}}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: "sw27-disk"})
			if err != nil {
				t.Fatal(err)
			}
			scope := MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "target"}}}
			before, err := source.Select(t.Context(), scope, 256<<10)
			if err != nil {
				source.Release()
				t.Fatal(err)
			}
			next := cloneBootstrap(before)
			next.Settings["target"] = int(8)
			beforeForkDisk, beforeForkDescriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			fork, err := source.Fork(t.Context(), before, next, scope, source.Stamp())
			if err != nil {
				source.Release()
				t.Fatal(err)
			}
			diskDelta, descriptorDelta := metadataLogicalDiskUsed.Load()-beforeForkDisk, metadataLogicalDescriptorsUsed.Load()-beforeForkDescriptors
			if diskDelta < int64(size) || diskDelta > int64(size+256<<10) || descriptorDelta < metadataLogicalDescriptorBytes {
				t.Fatal("point fork disk accounting no longer matches documented touched-field bound", size, diskDelta, descriptorDelta)
			}
			t.Logf("settings unrelated bytes=%d point fork extra disk bytes=%d descriptor bytes=%d SQL=0 (whole touched field copied)", size, diskDelta, descriptorDelta)
			fork.Release()
			source.Release()
			if metadataLogicalDiskUsed.Load() != startDisk || metadataLogicalDescriptorsUsed.Load() != startDescriptors {
				t.Fatal("measured fork retained source ownership after both generations released")
			}
		})
	}
}
