package store

import (
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A label change records its delta as JSON arrays (domain.ActivityMetadata).
// Rebuilding a project's progress replays its issues' activity and must read
// such an entry, not fail every later progress-changing write of the project.
func TestProjectProgressRebuildReadsLabelDeltaActivity(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f, issue := scaleP21IssuePersistenceFixture(t, []domain.Project{{ID: "p21-project", Name: "Selected"}}, nil)
	workspace := f.data.Workspace.URLKey
	labeled, err := f.UpdateIssueRecord(t.Context(), workspace, issue.ID, &issue.Version, IssueMutationScope{}, func(data *domain.Bootstrap, current *domain.Issue) error {
		current.Labels = []domain.IssueLabel{{ID: "p21-label", Name: "Label"}}
		data.Activities[current.ID] = append(data.Activities[current.ID], domain.ActivityEvent{
			ID: "p21-label-activity", Type: "issue.updated", CreatedAt: time.Now().UTC(), Actor: current.Creator,
			Metadata: domain.ActivityMetadata{"labels": "p21-label", "labelsBefore": "", "addedLabelIds": `["p21-label"]`, "removedLabelIds": `[]`},
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The fixture project has no progress series yet: completing the issue
	// rebuilds it from the issues' records and activity.
	completed, err := f.UpdateIssueRecord(t.Context(), workspace, issue.ID, &labeled.Version, IssueMutationScope{}, func(data *domain.Bootstrap, current *domain.Issue) error {
		previous := current.State
		current.State = domain.WorkflowState{ID: "p21-completed", Name: "Completed", Type: "completed", TeamID: current.Team.ID}
		now := time.Now().UTC()
		current.CompletedAt = &now
		data.Activities[current.ID] = append(data.Activities[current.ID], domain.ActivityEvent{
			ID: "p21-state-activity", Type: "issue.updated", CreatedAt: now, Actor: current.Creator,
			Metadata: domain.ActivityMetadata{"state": "Completed", "stateBefore": previous.Name, "stateBeforeId": previous.ID, "stateBeforeType": previous.Type},
		})
		return nil
	})
	if err != nil {
		t.Fatal("progress rebuild failed on a label-change activity:", err)
	}
	if completed.State.Type != "completed" {
		t.Fatal("issue not completed", completed.State)
	}
	activities, err := progressActivities(t.Context(), f.db, workspace, []string{issue.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Only the state transition is replayed; the label delta is skipped.
	if got := activities[issue.ID]; len(got) != 1 || got[0].Metadata["stateBeforeId"] != issue.State.ID {
		t.Fatal("replayed transitions", got)
	}
	data, ok := f.BootstrapFor(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	for _, project := range data.Projects {
		if project.ID == "p21-project" && len(project.ProgressHistory) == 0 {
			t.Fatal("project progress was not rebuilt")
		}
	}
}
