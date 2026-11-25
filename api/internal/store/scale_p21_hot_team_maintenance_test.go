package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21TeamMaintenanceProjection(data domain.Bootstrap) domain.Bootstrap {
	result := domain.Bootstrap{TeamSettings: map[string]domain.TeamSettings{}}
	for _, team := range data.Teams {
		result.Teams = append(result.Teams, domain.Team{ID: team.ID})
		if settings, found := data.TeamSettings[team.ID]; found {
			result.TeamSettings[team.ID] = domain.TeamSettings{
				AutoCloseStale: settings.AutoCloseStale, StaleMonths: settings.StaleMonths,
				AutoArchiveMonths: settings.AutoArchiveMonths, StaleStatusID: settings.StaleStatusID,
			}
		}
	}
	return result
}

func TestScaleP21HotTeamMaintenanceInstalledProjection(t *testing.T) {
	values := map[string]domain.Bootstrap{"physical": {
		Workspace: domain.Workspace{URLKey: "logical"},
		Teams: []domain.Team{
			{ID: "duplicate", Name: strings.Repeat("unrelated team ", 500000)},
			{ID: "native\x00id", Private: true}, {ID: "duplicate"}, {ID: ""}, {ID: "missing"},
		},
		TeamSettings: map[string]domain.TeamSettings{
			"duplicate":    {TeamID: "different", AutoCloseStale: true, StaleMonths: 7, StaleStatusID: "status\x00id", AutoArchiveMonths: -3, AgentGuidance: strings.Repeat("unrelated settings ", 400000), IssueViewDefaults: map[string]json.RawMessage{"opaque": json.RawMessage("not-json")}},
			"native\x00id": {AutoArchiveMonths: 19, StaleMonths: 0},
			"":             {AutoCloseStale: true, StaleMonths: 2},
			"orphan":       {AutoCloseStale: true, StaleMonths: 97, AgentGuidance: strings.Repeat("orphan ", 800000)},
		},
		Projects: []domain.Project{{ID: "must-remain-omitted"}},
	}}
	largeID := strings.Repeat("key", 400000)
	data := values["physical"]
	data.Teams = append(data.Teams, domain.Team{ID: largeID})
	data.TeamSettings[largeID] = domain.TeamSettings{AutoCloseStale: true, StaleMonths: 11}
	data.TeamSettings[strings.Repeat("unrelated-key", 200000)] = domain.TeamSettings{AutoArchiveMonths: 7}
	values["physical"] = data
	literal := &SQLiteStore{workspaces: values, lastWorkspaceKey: "physical"}
	view := scaleP21HotLogoStore(t, values)
	view.lastWorkspaceKey = "physical"
	for _, key := range []string{"physical", ""} {
		want, found := literal.WorkspaceMetadataFields(key, "teams", "teamSettings")
		read, gotFound, err := view.BeginTeamMaintenanceRead(t.Context(), key)
		if err != nil || gotFound != found {
			t.Fatal("installed/default registry selection changed", key, err)
		}
		if !reflect.DeepEqual(scaleP21TeamMaintenanceProjection(read.Data), scaleP21TeamMaintenanceProjection(want)) || read.Data.Projects != nil {
			t.Fatal("native order/key/scalars changed or projects leaked")
		}
		if view.metadataResidency.work.used == 0 {
			t.Fatal("partial view has no owned allowance")
		}
		read.Data.TeamSettings["duplicate"] = domain.TeamSettings{}
		read.Close()
		read.Close()
		if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.metadataResidency.work.used != 0 {
			t.Fatal("maintenance view did not release and clear")
		}
	}
	if _, found, err := view.BeginTeamMaintenanceRead(t.Context(), "logical"); err != nil || found {
		t.Fatal("logical URL substituted for physical registry key", found, err)
	}
	if _, found, err := view.BeginTeamMaintenanceRead(t.Context(), "absent"); err != nil || found {
		t.Fatal("missing registry changed", found, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginTeamMaintenanceRead(ctx, "physical"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation not propagated", err)
	}
	oldLimit := view.metadataResidency.work.limit
	for _, limit := range []int{1, 512 << 10} {
		view.metadataResidency.work.limit = limit
		if _, _, err := view.BeginTeamMaintenanceRead(t.Context(), "physical"); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("exhausted maintenance quota accepted", limit, err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("partial extraction leaked admission", limit)
		}
	}
	view.metadataResidency.work.limit = oldLimit
	if view.metadataResidency.work.used != 0 {
		t.Fatal("error path leaked work")
	}
}

func TestScaleP21HotTeamMaintenanceWarmReloadAndBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, size := range []int{10000, 100000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teamSettings"), workspace, "scale_p21.team_maintenance_seed", "", nil, func(data *domain.Bootstrap) error {
			id := data.Teams[0].ID
			settings := data.TeamSettings[id]
			settings.AutoCloseStale, settings.StaleMonths, settings.AutoArchiveMonths, settings.StaleStatusID = true, 6, 8, "native\x00status"
			settings.AgentGuidance = strings.Repeat("unrelated body ", size)
			data.TeamSettings[id] = settings
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var held int
		for _, reload := range []bool{false, true} {
			if reload {
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			want, _ := f.WorkspaceMetadataFields(workspace, "teams", "teamSettings")
			queries := 0
			read, found, err := view.BeginTeamMaintenanceRead(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), workspace)
			if err != nil || !found || !reflect.DeepEqual(scaleP21TeamMaintenanceProjection(read.Data), scaleP21TeamMaintenanceProjection(want)) {
				t.Fatal("warm/reloaded installed settings changed", reload, err)
			}
			held = view.metadataResidency.work.used
			if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || held > 256<<10 {
				t.Fatal("unrelated body affected maintenance read", queries, held)
			}
			read.Close()
			if view.metadataResidency.work.used != 0 {
				t.Fatal("reload read leaked work")
			}
		}
		t.Logf("unrelated_body_repetitions=%d held_work=%d SQL=0", size, held)
	}
}
