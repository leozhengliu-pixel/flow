package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A bounded start installs a source and resident policy for every workspace.
// Their budgets are settings now: a start that cannot fit refuses up front
// and names the setting and the size it needs, and the same database starts
// once the budget is raised.
func TestBoundedRolloutStartupBudgets(t *testing.T) {
	rolloutEnv(t)
	path := filepath.Join(t.TempDir(), "budget.db")
	repo, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	workspaces := rolloutWorkspaces(t, repo, 8)
	if prepared, err := repo.PrepareBoundedMetadata(t.Context()); err != nil || !prepared.Ready {
		t.Fatalf("prepare %+v (%v)", prepared, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "on")

	t.Setenv("FLOW_METADATA_SOURCE_BYTES", fmt.Sprint(metadataSourceReloadHeadroom+metadataLogicalDescriptorBytes))
	if _, err := OpenSQLite(path); !errors.Is(err, ErrMetadataWorkBudget) || !strings.Contains(err.Error(), "FLOW_METADATA_SOURCE_BYTES") || !strings.Contains(err.Error(), fmt.Sprintf("%d workspaces need", len(workspaces))) {
		t.Fatalf("start under a small source budget: %v", err)
	}
	t.Setenv("FLOW_METADATA_SOURCE_BYTES", "")

	t.Setenv("FLOW_METADATA_POLICY_BYTES", "200000")
	if _, err := OpenSQLite(path); !errors.Is(err, ErrMetadataWorkBudget) || !strings.Contains(err.Error(), "FLOW_METADATA_POLICY_BYTES") || !strings.Contains(err.Error(), fmt.Sprintf("of %d workspaces", len(workspaces))) {
		t.Fatalf("start under a small policy budget: %v", err)
	}

	t.Setenv("FLOW_METADATA_POLICY_BYTES", fmt.Sprint(32<<20))
	repo, err = OpenSQLite(path)
	if err != nil {
		t.Fatalf("start with the budgets raised: %v", err)
	}
	defer repo.Close()
	if repo.metadataResidency.policyLimit != 32<<20 {
		t.Fatalf("policy budget %d", repo.metadataResidency.policyLimit)
	}
	for _, workspace := range workspaces {
		if _, ok := repo.metadataResidency.policy[workspace]; !ok {
			t.Fatalf("workspace %s is not resident", workspace)
		}
		source, err := repo.retainMetadataLogicalSource(t.Context(), workspace)
		if err != nil {
			t.Fatalf("workspace %s has no installed source: %v", workspace, err)
		}
		source.Release()
	}
}
