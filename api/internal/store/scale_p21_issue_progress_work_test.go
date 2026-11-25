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

func TestScaleP21IssueProjectProgressSQLAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	now := utcDay(time.Now().UTC())
	at := now.AddDate(0, 0, -15)
	start := at.Format("2006-01-02")
	project := domain.Project{ID: "p21-progress-admission", StartDate: &start}
	var issues []domain.Issue
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.issue_progress_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, project)
		for index := range 60 {
			issue := domain.Issue{ID: fmt.Sprint("p21-progress-admitted-", index), Identifier: fmt.Sprintf("%s-%d", data.Teams[0].Key, index+1), Number: index + 1, Version: 1, Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, CreatedAt: at, Description: strings.Repeat("unused issue body ", 2000), Project: &domain.ProjectSummary{ID: project.ID}}
			issues = append(issues, issue)
			data.Issues = append(data.Issues, issue)
			data.Activities[issue.ID] = []domain.ActivityEvent{{
				ID: fmt.Sprint("p21-progress-event-", index), Type: "issue.updated", CreatedAt: now,
				Metadata: map[string]string{"stateBeforeId": "historical", "stateBeforeType": "started", "estimateBefore": "3", "unrelated": strings.Repeat("legal selected transition field ", 100)},
			}}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stored, _ := f.storedWorkspace(workspace)
	stored.States = append(stored.States,
		domain.WorkflowState{ID: issues[0].State.ID, TeamID: "other", Type: "completed"},
		domain.WorkflowState{ID: "historical", Type: "backlog"},
		domain.WorkflowState{ID: "historical", TeamID: "other", Type: "canceled"},
	)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	source, err := captureMetadataLogicalSource(t.Context(), stored, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	inputs := &issueHotInputs{store: view, source: source}
	defer inputs.close()
	tx, err := f.db.BeginTx(t.Context(), f.recordTxOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	old := domain.Bootstrap{Projects: []domain.Project{project}, States: stored.States, TeamSettings: stored.TeamSettings}
	changed, err := refreshIssueProjectProgress(t.Context(), tx, workspace, &old, nil, issues[:1], now)
	if err != nil || !changed {
		t.Fatal("legacy progress rebuild failed", err)
	}
	native := domain.Bootstrap{Projects: []domain.Project{project}, States: f.data.States[:1], TeamSettings: stored.TeamSettings}
	work := &issueProgressWork{inputs: inputs}
	ctx := context.WithValue(t.Context(), issueProgressWorkKey{}, work)
	if changed, err := refreshIssueProjectProgress(ctx, tx, workspace, &native, nil, issues[:1], now); err != nil || !changed || !reflect.DeepEqual(native.Projects, old.Projects) {
		t.Fatal("owned SQL progress headers/history differ from legacy", err, native.Projects, old.Projects)
	}
	held := view.metadataResidency.work.used
	if held == 0 {
		t.Fatal("SQL headers, activities and output series were not admitted")
	}
	inputs.close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("SQL progress owner leaked work")
	}
	// Preflight refuses before retaining even the first project issue header.
	inputs.source, err = captureMetadataLogicalSource(t.Context(), stored, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.metadataResidency.work.limit = 4096
	work = &issueProgressWork{inputs: inputs, states: map[string]domain.WorkflowState{}}
	ctx = context.WithValue(t.Context(), issueProgressWorkKey{}, work)
	if _, err := projectProgressIssues(ctx, tx, workspace, project.ID, work.states); !errors.Is(err, ErrMetadataWorkBudget) || view.metadataResidency.work.used != 0 {
		t.Fatal("progress header budget was checked after allocation or leaked", err)
	}
	t.Logf("selected_issues=60 held_progress_work=%d", held)
}
