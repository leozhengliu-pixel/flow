package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21IssueWorkflowStatesNativeSelection(t *testing.T) {
	shared := domain.WorkflowState{ID: "same", Name: "Shared\x00name", Type: "completed", Position: 9}
	parent := domain.WorkflowState{ID: "same", TeamID: "parent", Name: "Parent", Position: -1}
	reserved := domain.WorkflowState{ID: "same", TeamID: "specific", Name: "Reserved", Reserved: true}
	next := domain.WorkflowState{ID: "next", TeamID: "new-team", Name: "Next", Default: true}
	settings := map[string]domain.TeamSettings{
		"child": {InheritWorkflowStatuses: true, ParentTeamID: "parent"},
		"loop1": {InheritWorkflowStatuses: true, ParentTeamID: "loop2"},
		"loop2": {InheritWorkflowStatuses: true, ParentTeamID: "loop1"},
	}
	newTeam := "new-team"
	input := domain.Bootstrap{TeamSettings: settings, Issues: []domain.Issue{
		{Team: domain.Team{ID: "child"}}, {Team: domain.Team{ID: "specific"}},
		{Team: domain.Team{ID: "fallback"}}, {Team: domain.Team{ID: "loop1"}},
	}}
	baseline := 0
	for _, repetitions := range []int{10000, 100000} {
		states := []domain.WorkflowState{
			{ID: "unrelated", TeamID: strings.Repeat("unrelated", repetitions), Description: strings.Repeat("body", repetitions)},
			next, shared, parent, reserved, parent,
			{ID: "ignored-child", TeamID: "child"},
			{ID: "ignored-loop", TeamID: "loop1"},
		}
		for i := 0; i < repetitions/100; i++ {
			states = append(states, domain.WorkflowState{ID: fmt.Sprint("unrelated-state-", i), TeamID: fmt.Sprint("unrelated-team-", i)})
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {
			Workspace: domain.Workspace{URLKey: "logical"}, States: states,
		}})
		read, err := view.loadIssueWorkflowStates(t.Context(), "physical", &input, domain.IssueUpdateInput{TeamID: &newTeam})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(read.states, []domain.WorkflowState{next, shared, parent, reserved, parent}) {
			t.Fatal("inheritance/default/specific suppression or installed duplicate/order changed", read.states)
		}
		held := view.metadataResidency.work.used
		if baseline == 0 {
			baseline = held
		}
		if held != baseline || held <= 64<<10 || held > 256<<10 {
			t.Fatal("unrelated state body affected owned work", held)
		}
		read.states[0].Name = "changed"
		read.close()
		read.close()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("state selection leaked work")
		}
		again, err := view.loadIssueWorkflowStates(t.Context(), "physical", &input, domain.IssueUpdateInput{TeamID: &newTeam})
		if err != nil || again.states[0].Name != "Next" {
			t.Fatal("callback states alias installed source", err)
		}
		again.close()
		t.Logf("unrelated_repetitions=%d held_work=%d", repetitions, held)
	}
}

func TestScaleP21IssueWorkflowStatesBudgetAndCancellation(t *testing.T) {
	for _, states := range [][]domain.WorkflowState{
		{{ID: "large", TeamID: "target", Description: strings.Repeat("body", 100000)}},
		make([]domain.WorkflowState, 51),
	} {
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"workspace": {States: states}})
		input := domain.Bootstrap{Issues: []domain.Issue{{Team: domain.Team{ID: "target"}}}}
		read, err := view.loadIssueWorkflowStates(t.Context(), "workspace", &input, nil)
		if err != nil || len(read.states) != len(states) {
			t.Fatal("legitimate large descriptions or >50 states were rejected", err)
		}
		read.close()
		view.metadataResidency.work.limit = 128 << 10
		if _, err := view.loadIssueWorkflowStates(t.Context(), "workspace", &input, nil); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("oversized relevant states were silently accepted", err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("budget error leaked work")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := view.loadIssueWorkflowStates(ctx, "workspace", &input, nil); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation changed", err)
		}
		for _, limit := range []int{1, 90 << 10} {
			view.metadataResidency.work.limit = limit
			if _, err := view.loadIssueWorkflowStates(t.Context(), "workspace", &input, nil); !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("exhausted work accepted", limit, err)
			}
			if view.metadataResidency.work.used != 0 {
				t.Fatal("partial allowance leaked", limit)
			}
		}
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"workspace": {}})
	view.metadataResidency.work.limit = 68 << 10
	input := domain.Bootstrap{}
	for i := 0; i < 100; i++ {
		input.Issues = append(input.Issues, domain.Issue{Team: domain.Team{ID: fmt.Sprint("team-", i)}})
	}
	if _, err := view.loadIssueWorkflowStates(t.Context(), "workspace", &input, nil); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
		t.Fatal("affected-team map escaped work admission", err, view.metadataResidency.work.used)
	}
}

func TestScaleP21IssueWorkflowStatesInstalledSnapshotChoice(t *testing.T) {
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {States: []domain.WorkflowState{{ID: "before"}}}})
	owner, found, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer owner.Close()
	borrowed, err := owner.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := captureMetadataLogicalSource(t.Context(), domain.Bootstrap{States: []domain.WorkflowState{{ID: "installed"}}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", replacement)
	input := domain.Bootstrap{Issues: []domain.Issue{{}}}
	read, err := view.loadIssueWorkflowStates(borrowed, "physical", &input, nil)
	if err != nil || len(read.states) != 1 || read.states[0].ID != "installed" {
		t.Fatal("request snapshot replaced the writer's installed generation", err)
	}
	read.close()
	if _, err := view.loadIssueWorkflowStates(t.Context(), "", &input, nil); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("physical empty key became a default-workspace lookup", err)
	}
}

func TestScaleP21IssueWorkflowStatesUpdateCommitAndRollback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	team := f.data.Teams[0]
	state := domain.WorkflowState{ID: "state_p21_issue_workflow", TeamID: team.ID, Name: "Done", Type: "completed"}
	issue := domain.Issue{ID: "issue_p21_workflow", Identifier: team.Key + "-1", Number: 1, Version: 1, Team: team, State: f.data.States[0], Creator: f.data.Viewer, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.workflow_seed", "", nil, func(data *domain.Bootstrap) error {
		data.States = append(data.States, state)
		data.Issues = append(data.Issues, issue)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	literal, _ := f.storedWorkspace(workspace)
	if _, err := f.UpdateIssueRecord(t.Context(), workspace, issue.ID, &issue.Version, IssueMutationScope{}, func(data *domain.Bootstrap, _ *domain.Issue) error {
		if !reflect.DeepEqual(data.States, literal.States) {
			t.Fatal("flag-off callback state inputs changed")
		}
		return ErrNoMutation
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	beforeDecodes := view.WorkspaceMetadataDecodes()
	heldAtCommit := false
	f.db.commitHooks.Store(&commitHooks{beforeCommit: func(*sqlTx) {
		heldAtCommit = view.metadataResidency.work.used > 0
	}})
	defer f.db.commitHooks.Store(nil)
	comparisons, differences := NarrowMutationVerificationCounts()
	scope := IssueMutationScope{Payload: domain.IssueUpdateInput{StateID: &state.ID}}
	updated, err := view.UpdateIssueRecord(t.Context(), workspace, issue.ID, &issue.Version, scope, func(data *domain.Bootstrap, current *domain.Issue) error {
		if view.metadataResidency.work.used == 0 {
			t.Fatal("callback states lack an owned allowance")
		}
		for _, candidate := range data.States {
			if candidate.ID == state.ID {
				current.State = candidate
				return nil
			}
		}
		t.Fatal("affected team states absent")
		return nil
	})
	if err != nil || updated.State != state || updated.Version != issue.Version+1 {
		t.Fatal("bounded workflow update failed", updated, err)
	}
	afterComparisons, afterDifferences := NarrowMutationVerificationCounts()
	if afterComparisons != comparisons+1 || afterDifferences != differences {
		t.Fatal("typed state update bypassed or failed P04 verification", afterComparisons-comparisons, afterDifferences-differences)
	}
	if !heldAtCommit || view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != beforeDecodes {
		t.Fatal("workflow lease/decode contract changed", heldAtCommit, view.metadataResidency.work.used)
	}
	sentinel := errors.New("callback rollback")
	_, err = view.UpdateIssueRecord(t.Context(), workspace, issue.ID, &updated.Version, IssueMutationScope{}, func(data *domain.Bootstrap, current *domain.Issue) error {
		data.States[0].Name = "uncommitted"
		current.Title = "uncommitted"
		return sentinel
	})
	if !errors.Is(err, sentinel) || view.metadataResidency.work.used != 0 {
		t.Fatal("callback rollback changed", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	_, err = view.UpdateIssueRecord(canceled, workspace, issue.ID, &updated.Version, IssueMutationScope{}, func(_ *domain.Bootstrap, current *domain.Issue) error {
		current.Title = "uncommitted"
		cancel()
		return nil
	})
	cancel()
	if !errors.Is(err, context.Canceled) || view.metadataResidency.work.used != 0 {
		t.Fatal("canceled callback committed or leaked work", err)
	}
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issue.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "uncommitted") {
		t.Fatal("callback failure committed issue mutation")
	}
}
