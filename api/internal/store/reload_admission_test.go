package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW216Budget(t *testing.T, view *SQLiteStore, limit, occupied int) (context.Context, func()) {
	t.Helper()
	budget := view.metadataResidency.work
	budget.mu.Lock()
	previous := budget.limit
	if budget.used != 0 {
		budget.mu.Unlock()
		t.Fatal("fixture already owns work")
	}
	budget.limit = limit
	budget.mu.Unlock()
	ctx, release, err := view.tryAdmitAdditionalMetadataWork(t.Context(), occupied)
	if err != nil {
		t.Fatal(err)
	}
	done := false
	finish := func() {
		if done {
			return
		}
		done = true
		release()
		budget.mu.Lock()
		used := budget.used
		budget.limit = previous
		budget.mu.Unlock()
		if used != 0 {
			t.Error("reload leaked independent work ownership", used)
		}
	}
	t.Cleanup(finish)
	return ctx, finish
}

func scaleP21SW216PeerConnection(t *testing.T, fixture scaleP09Fixture, before domain.Bootstrap, provider string) {
	t.Helper()
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	connection := before.IntegrationConnections[0]
	connection.Provider = provider
	raw, err := json.Marshal(connection)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMetadataRecordRow(t.Context(), tx, before.Workspace.URLKey,
		metadataRecordKey{"integrationConnections", connection.ID}, metadataRecord{data: raw}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW216ReloadActualPolicyWarmStaleCancellation(t *testing.T) {
	fixture, view, _ := scaleP21SW27KnownWarmReloadFixture(t)
	workspace := fixture.data.Workspace.URLKey
	scope := MetadataRecordScope{Fields: []string{"integrationConnections", "cycles"}}
	warm, found := fixture.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("warm literal workspace missing")
	}
	old, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	ctx, finish := scaleP21SW216Budget(t, view, 32<<20, 8<<20)
	defer finish()
	// The occupied output allowance cannot also serve as reload scratch.
	// Its 8MiB inherited lease is deliberately smaller than the former
	// fixed 194MiB request.
	if err := view.reloadResidentWorkspace(ctx, workspace); err != nil {
		t.Fatal("same-stamp warm reload required blanket maximum policy work", err)
	}
	current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if current != old {
		t.Error("same-stamp reload replaced exact warm native generation")
	}
	current.Release()
	if used := view.MetadataWorkUsedForTesting(); used != 8<<20 {
		t.Fatal("same-stamp reload borrowed or retained output ownership", used)
	}
	if err := view.ReloadWorkspace(ctx, workspace); err != nil {
		t.Fatal("public reload retained a blanket policy reservation", err)
	}
	if used := view.MetadataWorkUsedForTesting(); used != 8<<20 {
		t.Fatal("public reload borrowed or retained output ownership", used)
	}
	scaleP21SW216PeerConnection(t, fixture, warm, "sw216-same-version-peer")
	if err := fixture.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.reloadResidentWorkspace(ctx, workspace); err != nil {
		t.Fatal("tiny true policy failed with sufficient actual work", err)
	}
	stamp, err := readMetadataStamp(t.Context(), view.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer installed.Release()
	if installed == old || installed.Stamp() != stamp {
		t.Fatal("stale same-version reload lost source identity or stamp", installed.Stamp(), stamp)
	}
	literal, _ := fixture.WorkspaceMetadata(workspace)
	expected := metadataRecordSnapshot(literal, scope, metadataRecordFields(scope))
	actual, found, err := view.WorkspaceMetadataScoped(t.Context(), workspace, scope)
	if err != nil || !found || !reflect.DeepEqual(actual, expected) ||
		actual.IntegrationConnections[0].Provider != "sw216-same-version-peer" {
		t.Fatal("actual-admission cold normalization changed literal output", found, err)
	}
	literalPolicy, _ := fixture.WorkspaceSettingsMetadata(workspace)
	boundedPolicy, _ := view.WorkspaceSettingsMetadata(workspace)
	if !reflect.DeepEqual(literalPolicy, boundedPolicy) {
		t.Fatal("source and resident policy did not install as one generation")
	}
	retained, err := old.Select(t.Context(), scope, 2<<20)
	if err != nil || retained.IntegrationConnections[0].Provider != "warm\x00provider" ||
		retained.Cycles[0].Insight != nil {
		t.Fatal("new reload changed an earlier retained request generation", err)
	}
	if used := view.MetadataWorkUsedForTesting(); used != 8<<20 {
		t.Fatal("cold reload changed independently occupied work", used)
	}
	scaleP21SW216PeerConnection(t, fixture, literal, "sw216-cancelled-peer")
	disk, descriptors, spool := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := view.reloadResidentWorkspace(cancelled, workspace); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reload lost cancellation", err)
	}
	after, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if after != installed || after.Stamp() != stamp {
		t.Error("cancelled reload installed a partial/stale source")
	}
	after.Release()
	afterPolicy, _ := view.WorkspaceSettingsMetadata(workspace)
	if !reflect.DeepEqual(afterPolicy, boundedPolicy) || view.MetadataWorkUsedForTesting() != 8<<20 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
		recordSpoolBytes.Load() != spool {
		t.Fatal("cancelled reload changed policy or leaked process ownership")
	}
	finish()
}

func TestScaleP21SW216ReloadLargePolicyRefusesBeforeDecode(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	workspace := fixture.data.Workspace.URLKey
	view, err := fixture.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	before, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer before.Release()
	policyBefore, _ := view.WorkspaceSettingsMetadata(workspace)
	// Each individual normalized team fits comfortably in the free 8MiB.
	// The whole native policy does not: 128x64KiB names require at least
	// 16MiB of policy value ownership before directory topology.
	if err := fixture.MutateWorkspace(WithMetadataFields(t.Context(), "teams"), workspace, "scale_p21.sw216_policy", "", nil,
		func(data *domain.Bootstrap) error {
			for index := 0; index < 128; index++ {
				data.Teams = append(data.Teams, domain.Team{
					ID:   fmt.Sprintf("sw216-%s-%d", data.Workspace.ID, index),
					Name: strings.Repeat("p", 64<<10),
				})
			}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	ctx, finish := scaleP21SW216Budget(t, view, 32<<20, 24<<20)
	defer finish()
	owner := &ResourceRead{store: view}
	defer owner.Close()
	disk, descriptors, spool := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	calls := 0
	var declared uint64
	tx, err := view.db.BeginTx(ctx, metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	readCtx, releaseParser, err := view.metadataResidency.work.acquire(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseParser()
	data, stamp, cost, source, readErr := view.readMetadataLogicalWorkspaceAdmitted(
		readCtx, tx, workspace, func(work uint64) error {
			calls++
			declared = work
			return owner.reserve(ctx, work)
		})
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	releaseParser()
	if source != nil {
		source.Release()
	}
	if !errors.Is(readErr, ErrMetadataWorkBudget) || calls != 1 || declared <= 8<<20 ||
		!reflect.DeepEqual(data, domain.Bootstrap{}) || stamp != (copyStamp{}) || cost != 0 || source != nil {
		t.Fatal("large true policy was decoded before its denied reservation", readErr, calls, declared, stamp, cost)
	}
	owner.Close()
	if view.MetadataWorkUsedForTesting() != 24<<20 || metadataLogicalDiskUsed.Load() != disk ||
		metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spool {
		t.Fatal("denied policy decode retained work or uninstalled logical files")
	}
	if err := view.reloadResidentWorkspace(ctx, workspace); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("actual reload did not enforce true policy ownership", err)
	}
	current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if current != before || current.Stamp() != before.Stamp() {
		t.Error("denied reload replaced the installed source")
	}
	current.Release()
	policyAfter, _ := view.WorkspaceSettingsMetadata(workspace)
	if !reflect.DeepEqual(policyBefore, policyAfter) || view.MetadataWorkUsedForTesting() != 24<<20 {
		t.Fatal("denied reload partially installed policy or leaked ownership")
	}
	finish()
}

func TestScaleP21SW216ReloadLargeNormalizationBase(t *testing.T) {
	fixture, view, _ := scaleP21SW27KnownWarmReloadFixture(t)
	workspace := fixture.data.Workspace.URLKey
	welcome := strings.Repeat("supported welcome text ", 36<<10) + "end"
	if err := fixture.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21.sw216_base", "", nil,
		func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.WelcomeMessage = welcome
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	want, found := fixture.WorkspaceSettingsMetadata(workspace)
	if !found || want.WorkspaceSettings.WelcomeMessage != welcome {
		t.Fatal("literal writer did not retain the supported large setting")
	}
	ctx, finish := scaleP21SW216Budget(t, view, 32<<20, 8<<20)
	defer finish()
	if err := view.ReloadWorkspace(ctx, workspace); err != nil {
		t.Fatal("large normalization base borrowed the parser/output lease", err)
	}
	got, found := view.WorkspaceSettingsMetadata(workspace)
	if !found || !reflect.DeepEqual(got.WorkspaceSettings, want.WorkspaceSettings) {
		t.Fatal("large normalization base changed literal settings")
	}
	if used := view.MetadataWorkUsedForTesting(); used != 8<<20 {
		t.Fatal("normalization base leaked or released output work", used)
	}
}

func TestScaleP21SW216ReloadPolicyOnlyProbeKeepsLiteralAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	fixture := scaleP09Database(t)
	probe := &SQLiteStore{db: fixture.db, dialect: fixture.dialect, maxStateBytes: fixture.maxStateBytes,
		workspaces: map[string]domain.Bootstrap{}, metadataResidency: newMetadataResidency()}
	probe.metadataResidency.requireCoverage = false
	probe.metadataResidency.cacheLimit = 0
	probe.metadataResidency.work.limit = 32 << 20
	queries := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
	if err := probe.reloadResidentWorkspace(ctx, fixture.data.Workspace.URLKey); !errors.Is(err, ErrMetadataWorkBudget) || queries != 0 {
		t.Fatal("policy-only probe silently entered the installed-logical admission path", err, queries)
	}
	probe.metadataResidency.work.limit = metadataWorkBytes
	if err := probe.reloadResidentWorkspace(t.Context(), fixture.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	if probe.metadataResidency.logical[fixture.data.Workspace.URLKey] != nil ||
		probe.metadataResidency.requireCoverage || probe.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("policy-only probe gained a logical activation claim or leaked work")
	}
}
