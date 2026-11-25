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

func TestScaleP21IssueProjectInputsNativeSelection(t *testing.T) {
	at := time.Now()
	project := domain.Project{ID: "selected", Name: "First", CreatedAt: at, Milestones: []domain.ProjectMilestone{{ID: "milestone", Name: "Selected"}}}
	duplicate := project
	duplicate.Name = "Second"
	document := domain.Document{ID: "attached", IssueID: "target", IssueTeamID: "old", CreatedAt: at, ProjectIDs: []string{"document-project"}, Content: "selected body"}
	documentDuplicate := document
	documentDuplicate.IssueID = "other"
	target := domain.Issue{ID: "target", Project: &domain.ProjectSummary{ID: project.ID}}
	input := domain.IssueUpdateInput{ProjectID: new("destination"), ProjectMilestoneID: new("milestone")}
	var held int
	for _, count := range []int{10, 100} {
		stored := domain.Bootstrap{
			Projects:  []domain.Project{project, duplicate, {ID: "destination", Name: "Destination"}, {ID: "document-project", TeamIDs: []string{"linked"}}},
			Documents: []domain.Document{document, documentDuplicate},
		}
		for index := range count {
			stored.Projects = append(stored.Projects, domain.Project{ID: fmt.Sprint("unrelated-", index), Description: strings.Repeat("unrelated project body ", count*100)})
			stored.Documents = append(stored.Documents, domain.Document{ID: fmt.Sprint("unrelated-", index), IssueID: "other", Content: strings.Repeat("unrelated document body ", count*100)})
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": stored})
		data := domain.Bootstrap{Issues: []domain.Issue{target}}
		read, err := view.loadIssueProjectInputs(t.Context(), "physical", &data, target, input)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(read.data.Projects, stored.Projects[:3]) || read.data.Documents != nil {
			t.Fatal("native project order, duplicate IDs, timestamps or omission changed")
		}
		if err := read.loadIssueDocuments(t.Context(), map[string]bool{target.ID: true}); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(read.data.Projects, stored.Projects[:4]) || !reflect.DeepEqual(read.data.Documents, stored.Documents[:2]) {
			t.Fatal("attached document/project native closure changed")
		}
		work := view.metadataResidency.work.used
		if held == 0 {
			held = work
		}
		if work != held {
			t.Fatal("unrelated project/document growth changed held work", held, work)
		}
		var baseline domain.Bootstrap
		read.installProjects(&data, &baseline)
		data.Projects[0].Milestones[0].Name = "callback"
		data.Documents[0].ProjectIDs[0] = "callback"
		if baseline.Projects[0].Milestones[0].Name != "Selected" || baseline.Documents[0].ProjectIDs[0] != "document-project" {
			t.Fatal("native callback inputs alias their P04 baseline")
		}
		read.close()
		if view.metadataResidency.work.used != 0 {
			t.Fatal("project owner leaked admission")
		}
		t.Logf("unrelated_records=%d held_work=%d", count, held)
	}
}

func TestScaleP21IssueProjectInputsManyAndBudget(t *testing.T) {
	var stored domain.Bootstrap
	var issues []domain.Issue
	for index := range 60 {
		id := fmt.Sprint("project-", index)
		stored.Projects = append(stored.Projects, domain.Project{ID: id})
		issues = append(issues, domain.Issue{ID: fmt.Sprint("issue-", index), Project: &domain.ProjectSummary{ID: id}})
	}
	stored.Projects[0].Description = strings.Repeat("large selected body ", 50000)
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"workspace": stored})
	data := domain.Bootstrap{Issues: issues}
	read, err := view.loadIssueProjectInputs(t.Context(), "workspace", &data, issues[0], domain.IssueUpdateInput{})
	if err != nil || !reflect.DeepEqual(read.data.Projects, stored.Projects) {
		t.Fatal("legal large/more-than-50 project input rejected", err)
	}
	read.close()
	view.metadataResidency.work.limit = 256 << 10
	if _, err := view.loadIssueProjectInputs(t.Context(), "workspace", &data, issues[0], domain.IssueUpdateInput{}); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
		t.Fatal("partial project budget failure leaked or succeeded", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.loadIssueProjectInputs(ctx, "workspace", &data, issues[0], domain.IssueUpdateInput{}); !errors.Is(err, context.Canceled) {
		t.Fatal("project cancellation changed", err)
	}
	// These selectors share the admitted editor and team-move closures;
	// the route differentials prove validation rather than opaque refusal.
	for _, input := range []domain.IssueUpdateInput{
		{ContentState: new("opaque")}, {TeamID: new("destination"), ContentState: new("")},
		{TeamID: new("destination")}, {DescriptionState: new("opaque")},
	} {
		if !issueProjectInputsSupported(issues[0], input) {
			t.Fatal("team move or admitted editor closure not available")
		}
	}
}

func TestScaleP21IssueProjectProgressNativeStatePrecedence(t *testing.T) {
	at := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	states := []domain.WorkflowState{
		{ID: "duplicate", TeamID: "selected", Type: "started", Description: "callback"},
		{ID: "historical", Type: "backlog"},
		{ID: "duplicate", TeamID: "unrelated", Type: "completed", Description: strings.Repeat("unneeded state body ", 50000)},
		{ID: "historical", TeamID: "unrelated", Type: "canceled"},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"workspace": {States: states}})
	inputs := &issueHotInputs{store: view}
	var err error
	inputs.source, err = view.retainMetadataLogicalSource(t.Context(), "workspace")
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.close()
	work := &issueProgressWork{inputs: inputs, states: map[string]domain.WorkflowState{}}
	issues := []progressIssue{{id: "target", state: states[0]}}
	if err := work.currentStates(t.Context(), issues); err != nil || issues[0].state.Type != "completed" {
		t.Fatal("global last-duplicate current state precedence changed", err)
	}
	events := map[string][]domain.ActivityEvent{"target": {{Type: "issue.updated", CreatedAt: at, Metadata: map[string]string{"stateBeforeId": " historical "}}}}
	if err := work.historicalStates(t.Context(), events); err != nil || work.states["historical"].Type != "canceled" {
		t.Fatal("global last-duplicate historical state precedence changed", err)
	}
	if view.metadataResidency.work.used > 64<<10 {
		t.Fatal("unneeded workflow state bodies were decoded")
	}
	if err := work.admitGrid(t.Context(), at.AddDate(-9999, 0, 0), at); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("large output grid was allocated without admission", err)
	}
}

func TestScaleP21IssueProjectInputsWarmReload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	project := domain.Project{ID: "p21-selected-reload-project", Name: "Selected", CreatedAt: time.Now().UTC(), Description: strings.Repeat("legal selected project ", 10000)}
	target := domain.Issue{ID: "p21-selected-reload-issue", Project: &domain.ProjectSummary{ID: project.ID}}
	document := domain.Document{ID: "p21-selected-reload-document", IssueID: target.ID, ProjectIDs: []string{project.ID}, CreatedAt: project.CreatedAt, Content: "Selected"}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.issue_inputs_reload_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, project)
		data.Documents = append(data.Documents, document)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	for _, reload := range []bool{false, true} {
		if reload {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
		}
		stored, _ := f.storedWorkspace(workspace)
		data := domain.Bootstrap{Issues: []domain.Issue{target}}
		read, err := view.loadIssueProjectInputs(t.Context(), workspace, &data, target, domain.IssueUpdateInput{})
		if err != nil {
			t.Fatal(err)
		}
		if err := read.loadIssueDocuments(t.Context(), map[string]bool{target.ID: true}); err != nil {
			read.close()
			t.Fatal(err)
		}
		for _, value := range stored.Projects {
			if value.ID == project.ID && !reflect.DeepEqual(read.data.Projects, []domain.Project{value}) {
				t.Fatal("warm/reloaded native project input changed", reload)
			}
		}
		for _, value := range stored.Documents {
			if value.ID == document.ID && !reflect.DeepEqual(read.data.Documents, []domain.Document{value}) {
				t.Fatal("warm/reloaded native document input changed", reload)
			}
		}
		read.close()
		if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
			t.Fatal("project/document selection decoded full metadata or leaked admission", reload)
		}
	}
}
