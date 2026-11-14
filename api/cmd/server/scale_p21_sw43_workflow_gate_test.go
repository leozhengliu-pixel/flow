package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW43WorkflowGateInstalledHeaders(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		root, child, retired := idgen.New("sw43-gate-root-"), idgen.New("sw43-gate-child-"), idgen.New("sw43-gate-retired-")
		stamp := time.Date(2026, 11, 13, 1, 2, 3, 4, time.UTC)
		for _, spec := range []struct {
			name                            string
			own, inherit, retire, duplicate bool
			large, cycle, restore           bool
		}{
			{name: "no-own-states"},
			{name: "ordinary", own: true},
			{name: "inherited-child", own: true, inherit: true},
			{name: "retired-child", own: true, inherit: true, retire: true},
			{name: "duplicate-team-order", own: true, inherit: true, duplicate: true},
			{name: "duplicate-last-active", own: true, inherit: true, duplicate: true, restore: true},
			{name: "active-cycle", own: true, cycle: true},
			{name: "legal-large-unneeded-headers", own: true, large: true},
		} {
			t.Run(spec.name, func(t *testing.T) {
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "teamSettings", "states"), workspace, "team.settings_updated", "", nil, func(data *domain.Bootstrap) error {
					data.Teams = []domain.Team{
						{ID: root, Key: "ROOT"},
						{ID: child, Key: "CHILD"},
						{ID: retired, Key: "RETIRED", RetiredAt: &stamp},
					}
					if spec.retire {
						data.Teams[1].RetiredAt = &stamp
					}
					if spec.duplicate {
						data.Teams = append(data.Teams, domain.Team{ID: child, Key: "LAST", RetiredAt: &stamp})
					}
					if spec.restore {
						data.Teams = append(data.Teams, domain.Team{ID: child, Key: "ACTIVE"})
					}
					data.TeamSettings = map[string]domain.TeamSettings{
						root:     {TeamID: root},
						child:    {TeamID: child, ParentTeamID: root, InheritWorkflowStatuses: spec.inherit},
						retired:  {TeamID: retired, ParentTeamID: root, InheritWorkflowStatuses: true},
						"orphan": {TeamID: "orphan", ParentTeamID: root, InheritWorkflowStatuses: true},
					}
					if spec.cycle {
						data.TeamSettings[root] = domain.TeamSettings{TeamID: root, ParentTeamID: child}
					}
					data.States = nil
					for index := range 80 {
						data.States = append(data.States, domain.WorkflowState{ID: fmt.Sprintf("sw43-unrelated-state-%03d", index), TeamID: "other-team", Name: "Unrelated", Type: "started"})
					}
					if spec.own {
						data.States = append(data.States, domain.WorkflowState{ID: "sw43-owned-state", TeamID: root, Name: "Owned", Type: "started"})
					}
					if spec.large {
						data.States[0].Name = strings.Repeat("Unneeded body ", 300000)
						data.Teams[2].Name = strings.Repeat("Unneeded team body ", 200000)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				changedType := "started"
				for _, input := range []domain.WorkflowStateMutationInput{{}, {Type: &changedType}} {
					old, err := workflowStateEditMetadataOnly(repo, workspace, root, input)
					if err != nil {
						t.Fatal(err)
					}
					for range 6 {
						next, err := workflowStateEditMetadataOnly(probe, workspace, root, input)
						if err != nil || next != old {
							t.Fatalf("header predicate changed: old=%t bounded=%t err=%v", old, next, err)
						}
					}
				}
				if probe.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("predicate decoded complete metadata")
				}
			})
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "states"), workspace, "workflow_state.updated", "", nil, func(data *domain.Bootstrap) error {
			data.States = []domain.WorkflowState{{ID: "sw43-native-state", TeamID: root + "\x00", Name: "Native"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, probe} {
			value, err := workflowStateEditMetadataOnly(source, workspace, root+"\x00", domain.WorkflowStateMutationInput{})
			if err != nil || !value {
				t.Fatal("warm exact TeamID was replaced by canonical bytes", value, err)
			}
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, probe} {
			value, err := workflowStateEditMetadataOnly(source, workspace, root+"\x00", domain.WorkflowStateMutationInput{})
			if err != nil || value {
				t.Fatal("cold canonical transition changed", value, err)
			}
			value, err = workflowStateEditMetadataOnly(source, workspace+"-missing", root, domain.WorkflowStateMutationInput{})
			if err != nil || value {
				t.Fatal("missing workspace predicate changed", value, err)
			}
		}
	}
}

func TestScaleP21SW43WorkflowGateDiskHierarchy(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		fixture := scaleP21M2Setup(t, repo)
		workspace := fixture.data.Workspace.URLKey
		root := idgen.New("sw43-disk-root-")
		// These exact native IDs make the withdrawn all-header reduction
		// exceed its 64 MiB allowance, without any large decoded field.
		huge := strings.Repeat("native-key-", (12<<20)/11) + "\x00suffix"
		if 2*len(huge)*3 <= 64<<20 {
			t.Fatal("fixture does not exceed the withdrawn retained graph cap")
		}
		probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, inherit := range []bool{false, true} {
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "states", "teamSettings"), workspace, "team.settings_updated", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = []domain.Team{{ID: root, Key: "ROOT"}, {ID: huge}, {ID: huge}, {ID: huge}}
				data.TeamSettings = map[string]domain.TeamSettings{
					root: {TeamID: root},
					huge: {TeamID: huge, ParentTeamID: root, InheritWorkflowStatuses: inherit},
				}
				data.States = []domain.WorkflowState{{ID: "sw43-disk-state", TeamID: root, Name: "Owned"}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			old, err := workflowStateEditMetadataOnly(repo, workspace, root, domain.WorkflowStateMutationInput{})
			if err != nil {
				t.Fatal(err)
			}
			next, err := workflowStateEditMetadataOnly(probe, workspace, root, domain.WorkflowStateMutationInput{})
			if err != nil || next != old || next == inherit {
				t.Fatalf("large native hierarchy changed: old=%t new=%t inherit=%t err=%v", old, next, inherit, err)
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("disk hierarchy decoded complete metadata")
		}
	}
}
