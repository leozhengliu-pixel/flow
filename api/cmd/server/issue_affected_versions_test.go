package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestIssueUpdateResponseCarriesTheVersionsOfIssuesItChanged(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	teamID := bootstrap.Teams[0].ID
	state := func(stateType string) domain.WorkflowState {
		index := slices.IndexFunc(bootstrap.States, func(item domain.WorkflowState) bool { return item.Type == stateType && !item.Reserved })
		if index < 0 {
			t.Fatalf("missing %s state", stateType)
		}
		return bootstrap.States[index]
	}
	todo, done := state("unstarted"), state("completed")
	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"autoCloseParents": true, "autoCloseSubIssues": true}, http.StatusOK)
	create := func(title, parentID string) domain.Issue {
		input := map[string]any{"title": title, "teamId": teamID, "stateId": todo.ID}
		if parentID != "" {
			input["parentId"] = parentID
		}
		return requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", input, http.StatusCreated)
	}
	storedVersion := func(id string) int64 {
		current := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
		return findIssue(t, current.Issues, id).Version
	}

	parent := create("Parent", "")
	first, last := create("First", parent.ID), create("Last", parent.ID)
	parent = findIssue(t, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Issues, parent.ID)

	// Closing the first of two sub-issues changes nothing else.
	closedFirst := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+first.ID, map[string]any{"stateId": done.ID, "expectedVersion": first.Version}, http.StatusOK)
	if len(closedFirst.AffectedIssues) != 0 {
		t.Fatalf("an edit that changed no other issue reported %v", closedFirst.AffectedIssues)
	}

	// Closing the last one closes the parent, which raises the parent's version.
	closedLast := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+last.ID, map[string]any{"stateId": done.ID, "expectedVersion": last.Version}, http.StatusOK)
	want := []domain.IssueVersion{{ID: parent.ID, Version: storedVersion(parent.ID)}}
	if want[0].Version != parent.Version+1 || !slices.Equal(closedLast.AffectedIssues, want) {
		t.Fatalf("affected issues = %v, want %v (the parent was at %d)", closedLast.AffectedIssues, want, parent.Version)
	}
	// The reported version is the one an edit of the parent must send.
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+parent.ID, map[string]any{"title": "Renamed", "expectedVersion": want[0].Version}, http.StatusOK)

	// Closing a parent closes its open sub-issues.
	other := create("Other parent", "")
	child := create("Open child", other.ID)
	child = findIssue(t, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Issues, child.ID)
	other = findIssue(t, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Issues, other.ID)
	closedParent := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+other.ID, map[string]any{"stateId": done.ID, "expectedVersion": other.Version}, http.StatusOK)
	wantChild := []domain.IssueVersion{{ID: child.ID, Version: storedVersion(child.ID)}}
	if wantChild[0].Version != child.Version+1 || !slices.Equal(closedParent.AffectedIssues, wantChild) {
		t.Fatalf("affected issues = %v, want %v", closedParent.AffectedIssues, wantChild)
	}

	// A batch reports the issues outside it that it changed, on its first issue.
	batchParent := create("Batch parent", "")
	batchChild := create("Batch child", batchParent.ID)
	batchParent = findIssue(t, requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Issues, batchParent.ID)
	batch := requestJSON[[]domain.Issue](t, handler, http.MethodPost, "/api/issues/batch", map[string]any{"issueIds": []string{batchChild.ID}, "update": map[string]any{"stateId": done.ID}}, http.StatusOK)
	wantBatch := []domain.IssueVersion{{ID: batchParent.ID, Version: storedVersion(batchParent.ID)}}
	if len(batch) != 1 || wantBatch[0].Version != batchParent.Version+1 || !slices.Equal(batch[0].AffectedIssues, wantBatch) {
		t.Fatalf("batch affected issues = %v, want %v", batch, wantBatch)
	}
}
