package main

import (
	"flow/api/internal/domain"
	"flow/api/internal/store"
	"path/filepath"
	"testing"
)

func TestDetailMutationsRespectFeatureFlags(t *testing.T) {
	repo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "features.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	data.WorkspaceSettings.FeatureFlags = map[string]bool{"initiatives": false, "customer-requests": false}
	project := data.Projects[0]
	if err = applyProjectUpdate(&data, &project, domain.ProjectMutationInput{Initiatives: []string{"initiative"}}); err == nil {
		t.Fatal("disabled initiatives accepted")
	}
	if err = applyProjectUpdate(&data, &project, domain.ProjectMutationInput{Customers: []string{"customer"}}); err == nil {
		t.Fatal("disabled customers accepted")
	}
	issue := data.Issues[0]
	cycle := data.Cycles[0].ID
	data.CycleSettings[issue.Team.ID] = domain.CycleSettings{Enabled: false}
	if _, err = applyUpdate(&data, &issue, domain.IssueUpdateInput{CycleID: &cycle}); err == nil {
		t.Fatal("disabled cycle accepted")
	}
	empty := ""
	if _, err = applyUpdate(&data, &issue, domain.IssueUpdateInput{CycleID: &empty}); err != nil {
		t.Fatal("cannot clear historical cycle:", err)
	}
	name := "Existing project still editable"
	if err = applyProjectUpdate(&data, &project, domain.ProjectMutationInput{Name: &name}); err != nil {
		t.Fatal("unrelated project change blocked:", err)
	}
}
