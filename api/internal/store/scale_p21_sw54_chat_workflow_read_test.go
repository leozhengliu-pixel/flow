package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW54WorkflowWant(data domain.Bootstrap, request ChatDisplayRequest, keys []string, stateTeam *string) domain.Bootstrap {
	result := scaleP21ChatDisplayProjection(data, request)
	for _, key := range keys {
		settings, saved := data.TeamSettings[key]
		if !saved {
			continue
		}
		if result.TeamSettings == nil {
			result.TeamSettings = map[string]domain.TeamSettings{}
		}
		selected := result.TeamSettings[key]
		selected.TeamID, selected.ParentTeamID = settings.TeamID, settings.ParentTeamID
		selected.InheritWorkflowStatuses, selected.DefaultStateID = settings.InheritWorkflowStatuses, settings.DefaultStateID
		selected.InheritIssueEstimation, selected.EstimateType = settings.InheritIssueEstimation, settings.EstimateType
		result.TeamSettings[key] = selected
	}
	result.States = []domain.WorkflowState{}
	if stateTeam != nil {
		for _, state := range data.States {
			if state.TeamID == *stateTeam {
				result.States = append(result.States, domain.WorkflowState{
					ID: state.ID, TeamID: state.TeamID, Name: state.Name, Position: state.Position, Default: state.Default,
				})
			}
		}
	}
	return result
}

func scaleP21SW54WorkflowCheck(t *testing.T, view *SQLiteStore, workspace string, request ChatDisplayRequest, teamID string, want domain.Bootstrap) int {
	t.Helper()
	statements := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
	read, found, err := view.BeginChatDisplayRead(ctx, workspace, request)
	if err != nil || !found {
		t.Fatal("begin workflow display", found, err)
	}
	defer read.Close()
	if err := read.LoadWorkflow(ctx, teamID); err != nil {
		t.Fatal("load workflow", err)
	}
	if !reflect.DeepEqual(read.Data, want) {
		t.Fatalf("workflow fields/order/saved-key presence changed for %q: got %d states/%d settings keys, want %d/%d", teamID, len(read.Data.States), len(read.Data.TeamSettings), len(want.States), len(want.TeamSettings))
	}
	if statements != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("workflow projection used SQL or decoded full metadata", statements, view.WorkspaceMetadataDecodes())
	}
	held := view.metadataResidency.work.used
	if held == 0 {
		t.Fatal("workflow projection has no work owner")
	}
	read.Close()
	read.Close()
	if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || view.metadataResidency.work.used != 0 {
		t.Fatal("workflow close did not clear/release")
	}
	if err := read.LoadWorkflow(t.Context(), teamID); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed workflow reader accepted reuse", err)
	}
	return held
}

func TestScaleP21SW54WorkflowLegacyWarmReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "states", "teamSettings"), workspace, "scale_p21.sw54_seed", "", nil, func(data *domain.Bootstrap) error {
		data.TeamSettings = map[string]domain.TeamSettings{
			"local":                {TeamID: "saved-local-id", ParentTeamID: "unused-parent", DefaultStateID: "local-default", Timezone: "Asia/Tokyo", Access: "private", MembershipRestriction: "owners"},
			"child":                {TeamID: "child", ParentTeamID: "parent", InheritWorkflowStatuses: true, DefaultStateID: "parent-default"},
			"parent":               {TeamID: "parent", DefaultStateID: "parent-first"},
			"estimate":             {TeamID: "estimate", ParentTeamID: "estimate-parent", InheritIssueEstimation: true, EstimateType: "notUsed"},
			"estimate-parent":      {TeamID: "estimate-parent", EstimateType: "fibonacci"},
			"union":                {TeamID: "union", ParentTeamID: "union-middle", InheritWorkflowStatuses: true, InheritIssueEstimation: true},
			"union-middle":         {ParentTeamID: "union-root", InheritWorkflowStatuses: true, InheritIssueEstimation: true},
			"union-root":           {TeamID: "union-root", EstimateType: "flow"},
			"defaulted":            {ParentTeamID: "unused-parent", InheritIssueEstimation: true, EstimateType: "tShirt"},
			"zero":                 {},
			"":                     {TeamID: "saved-empty-key", DefaultStateID: "global-default"},
			"cycle-a":              {TeamID: "cycle-a", ParentTeamID: "cycle-b", InheritWorkflowStatuses: true},
			"cycle-b":              {TeamID: "cycle-b", ParentTeamID: "cycle-a", InheritWorkflowStatuses: true},
			"estimate-cycle-a":     {TeamID: "estimate-cycle-a", ParentTeamID: "estimate-cycle-b", InheritIssueEstimation: true},
			"estimate-cycle-b":     {TeamID: "estimate-cycle-b", ParentTeamID: "estimate-cycle-a", InheritIssueEstimation: true},
			"missing-parent-child": {TeamID: "missing-parent-child", ParentTeamID: "missing-parent", InheritWorkflowStatuses: true},
			"unused-parent":        {TeamID: "unused-parent", EstimateType: "exponential", AgentGuidance: "not part of either ancestor walk"},
		}
		data.States = []domain.WorkflowState{
			{ID: "global-last-position", Name: "Global last", Position: 100},
			{ID: "local-first", TeamID: "local", Name: "Zulu", Position: 9, Color: "ignored", Type: "completed", Reserved: true},
			{ID: "parent-first", TeamID: "parent", Name: "Parent Z", Position: 5},
			{ID: "global-default", Name: "Global default", Position: -9, Default: true},
			{ID: "local-default", TeamID: "local", Name: "Alpha", Position: -3, Default: true},
			{ID: "parent-default", TeamID: "parent", Name: "Parent A", Position: -1, Default: true},
			{ID: "union-state", TeamID: "union-root", Name: "Union root"},
			{ID: "hidden-child", TeamID: "child", Name: "Must inherit instead"},
			{ID: "hidden-cycle", TeamID: "cycle-a", Name: "Cycle must be empty"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	local, parent, union, global := "local", "parent", "union-root", ""
	tests := []struct {
		name, team string
		keys       []string
		states     *string
		policy     bool
	}{
		{"local-default", "local", []string{"local"}, &local, false},
		{"inherited", "child", []string{"child", "parent"}, &parent, false},
		{"policy-merge", "child", []string{"child", "parent"}, &parent, true},
		{"estimate-only-global", "estimate", []string{"estimate", "estimate-parent"}, &global, false},
		{"union-raw-workflow-defaulted-estimate", "union", []string{"union", "union-middle", "union-root"}, &union, false},
		{"team-id-empty-defaults", "defaulted", []string{"defaulted"}, &global, false},
		{"missing-exact-key", "missing", nil, &global, false},
		{"saved-zero-key", "zero", []string{"zero"}, &global, false},
		{"empty-exact-key", "", []string{""}, &global, false},
		{"workflow-cycle", "cycle-a", []string{"cycle-a", "cycle-b"}, nil, false},
		{"estimate-cycle", "estimate-cycle-a", []string{"estimate-cycle-a", "estimate-cycle-b"}, &global, false},
		{"missing-ancestor-key", "missing-parent-child", []string{"missing-parent-child"}, &global, false},
	}
	for _, reload := range []bool{false, true} {
		if reload {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
		legacy, found := f.WorkspaceMetadataFields(workspace, "states", "teamSettings", "workspaceSettings", "teams", "userSettings")
		if !found {
			t.Fatal("legacy oracle workspace missing")
		}
		for _, test := range tests {
			t.Run(fmt.Sprintf("%s/reload=%t", test.name, reload), func(t *testing.T) {
				request := ChatDisplayRequest{TeamID: test.team, Policy: test.policy}
				want := scaleP21SW54WorkflowWant(legacy, request, test.keys, test.states)
				scaleP21SW54WorkflowCheck(t, view, workspace, request, test.team, want)
			})
		}
	}
}

func TestScaleP21SW54WorkflowNativeOrderAndNoCap(t *testing.T) {
	const team = "team\x00exact"
	states := []domain.WorkflowState{{ID: "global", Name: "Global"}}
	for index := range 137 {
		states = append(states, domain.WorkflowState{
			ID: fmt.Sprintf("state-%03d", index), TeamID: team, Name: fmt.Sprintf("State %03d", 137-index),
			Position: float64(137 - index), Default: index == 136,
		})
	}
	states[2].ID, states[3].ID = "duplicate", "duplicate"
	states[2].Name, states[3].Name = "First\x00duplicate", "Second duplicate"
	states[4].Position = math.Copysign(0, -1)
	data := domain.Bootstrap{
		States: states,
		TeamSettings: map[string]domain.TeamSettings{
			team:   {TeamID: "native\x00team", DefaultStateID: "state-136", ParentTeamID: "unfollowed"},
			"team": {TeamID: "prefix must not match"},
		},
	}
	literal := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": data}}
	legacy, found := literal.WorkspaceMetadataFields("physical", "states", "teamSettings")
	if !found {
		t.Fatal("native oracle missing")
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	request := ChatDisplayRequest{TeamID: team}
	stateTeam := team
	want := scaleP21SW54WorkflowWant(legacy, request, []string{team}, &stateTeam)
	scaleP21SW54WorkflowCheck(t, view, "physical", request, team, want)
	read, _, err := view.BeginChatDisplayRead(t.Context(), "physical", request)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	if err := read.LoadWorkflow(t.Context(), team); err != nil || len(read.Data.States) != 137 {
		t.Fatal("workflow capped before card sorting/default selection", len(read.Data.States), err)
	}
	if math.Float64bits(read.Data.States[3].Position) != math.Float64bits(states[4].Position) {
		t.Fatal("native state position changed")
	}
	read.Data.States[0].Name = "Caller changed"
	settings := read.Data.TeamSettings[team]
	settings.DefaultStateID = "Caller changed"
	read.Data.TeamSettings[team] = settings
	read.Close()
	scaleP21SW54WorkflowCheck(t, view, "physical", request, team, want)
}

func TestScaleP21SW54WorkflowIgnoredBodiesWarmReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	const team = "sw54-child"
	stateTeam := "sw54-parent"
	expected := map[bool]int{}
	for _, size := range []int{10000, 100000} {
		body := strings.Repeat("ignored body ", size)
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "states", "teamSettings"), workspace, "scale_p21.sw54_body_seed", "", nil, func(data *domain.Bootstrap) error {
			data.TeamSettings = map[string]domain.TeamSettings{
				team:      {TeamID: team, ParentTeamID: stateTeam, InheritWorkflowStatuses: true, DefaultStateID: "parent-default", Description: body, AgentGuidance: body},
				stateTeam: {TeamID: stateTeam, EstimateType: "flow", Description: body, AgentGuidance: body},
				"ignored": {TeamID: "ignored", ParentTeamID: body, DefaultStateID: body, EstimateType: body, Description: body, AgentGuidance: body},
			}
			data.States = []domain.WorkflowState{
				{ID: "parent-later", TeamID: stateTeam, Name: "Parent later", Position: 4, Description: body, Color: body, Type: body},
				{ID: "ignored", TeamID: body, Name: body, Description: body},
				{ID: "parent-default", TeamID: stateTeam, Name: "Parent default", Position: -2, Default: true, Description: body},
				{ID: "global", Name: body, Description: body},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			legacy, _ := f.WorkspaceMetadataFields(workspace, "states", "teamSettings", "workspaceSettings", "userSettings")
			request := ChatDisplayRequest{TeamID: team}
			want := scaleP21SW54WorkflowWant(legacy, request, []string{team, stateTeam}, &stateTeam)
			held := scaleP21SW54WorkflowCheck(t, view, workspace, request, team, want)
			if expected[reload] == 0 {
				expected[reload] = held
			}
			if held != expected[reload] {
				t.Fatal("ignored settings/state bodies increased owned work", size, reload, held, expected[reload])
			}
			t.Logf("body_repetitions=%d reload=%t workflow_work=%d", size, reload, held)
		}
	}
}

func TestScaleP21SW54WorkflowBudgetCancellationAndRelease(t *testing.T) {
	const team = "selected-team"
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {
		TeamSettings: map[string]domain.TeamSettings{team: {TeamID: team}},
		States:       []domain.WorkflowState{{ID: "selected", TeamID: team, Name: strings.Repeat("selected name ", 20000)}},
	}})
	statements := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
	read, _, err := view.BeginChatDisplayRead(ctx, "physical", ChatDisplayRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := read.LoadWorkflow(ctx, team); err != nil || len(read.Data.States) != 1 {
		read.Close()
		t.Fatal("legitimate large selected name rejected", err)
	}
	held := view.metadataResidency.work.used
	read.Close()
	limit := view.metadataResidency.work.limit
	for _, budget := range []int{1, held - 1} {
		view.metadataResidency.work.limit = budget
		read, _, err := view.BeginChatDisplayRead(ctx, "physical", ChatDisplayRequest{})
		if err == nil {
			err = read.LoadWorkflow(ctx, team)
		}
		if read != nil {
			read.Close()
		}
		if !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
			t.Fatal("workflow work limit escaped or leaked", budget, err)
		}
	}
	view.metadataResidency.work.limit = limit
	read, _, err = view.BeginChatDisplayRead(ctx, "physical", ChatDisplayRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	baseline := view.metadataResidency.work.used
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := read.LoadWorkflow(cancelled, team); !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != baseline {
		t.Fatal("canceled workflow admitted work or changed cancellation", err)
	}
	if err := read.LoadWorkflow(ctx, team); err != nil {
		t.Fatal("canceled stage damaged retained source", err)
	}
	var closeOutput func()
	outputCtx, outputCancel := context.WithCancel(WithMetadataOutputOwner(ctx, func(close func()) bool { closeOutput = close; return true }))
	defer outputCancel()
	if err := read.TransferOutput(outputCtx); err != nil || closeOutput == nil {
		t.Fatal("workflow output transfer failed", err)
	}
	outputCancel()
	if view.metadataResidency.work.used == 0 {
		t.Fatal("workflow output released before its owner")
	}
	closeOutput()
	closeOutput()
	if view.metadataResidency.work.used != 0 || statements != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("workflow release leaked or performed SQL/full metadata decode", statements)
	}
}

func TestScaleP21SW54WorkflowPinnedGeneration(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	const team, parent = "pinned-child", "pinned-parent"
	seed := func(name string) {
		t.Helper()
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "states", "teamSettings"), workspace, "scale_p21.sw54_generation_seed", "", nil, func(data *domain.Bootstrap) error {
			data.TeamSettings = map[string]domain.TeamSettings{
				team:   {TeamID: team, ParentTeamID: parent, InheritWorkflowStatuses: true, DefaultStateID: name, Timezone: name},
				parent: {TeamID: parent, EstimateType: name},
			}
			data.States = []domain.WorkflowState{{ID: name, TeamID: parent, Name: name, Default: true}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("Original")
	old, _ := f.WorkspaceMetadataFields(workspace, "states", "teamSettings", "workspaceSettings", "userSettings")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	owner, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer owner.Close()
	bound, err := owner.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	statements := 0
	bound = WithSQLStatementObserver(bound, func(string) { statements++ })
	replace := func(name string) {
		t.Helper()
		seed(name)
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
	}
	replace("Replacement")
	request := ChatDisplayRequest{TeamID: team}
	read, found, err := view.BeginChatDisplayRead(bound, workspace, request)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	owner.Close()
	replace("Latest")
	ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
	if err := read.LoadWorkflow(ctx, team); err != nil {
		t.Fatal(err)
	}
	stateTeam := parent
	if !reflect.DeepEqual(read.Data, scaleP21SW54WorkflowWant(old, request, []string{team, parent}, &stateTeam)) {
		t.Fatal("deferred workflow switched from its pinned generation")
	}
	read.Close()
	latest, _ := f.WorkspaceMetadataFields(workspace, "states", "teamSettings", "workspaceSettings", "userSettings")
	scaleP21SW54WorkflowCheck(t, view, workspace, request, team, scaleP21SW54WorkflowWant(latest, request, []string{team, parent}, &stateTeam))
	if statements != 0 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("pinned workflow leaked or performed SQL/full metadata decode", statements)
	}
}
