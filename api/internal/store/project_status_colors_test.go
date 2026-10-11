package store

import (
	"testing"

	"flow/api/internal/domain"
)

func TestNormalizeKeepsEditedProjectStatusColours(t *testing.T) {
	data := localSQLiteFixture()
	data.ProjectStatuses = []domain.ProjectStatus{
		{ID: "ps_backlog", Name: "Backlog", Color: "#E79D4F", Type: "backlog"},
		{ID: "ps_planned", Name: "Next up", Color: "#26b5ce", Type: "planned"},
		{ID: "ps_progress", Name: "In Progress", Color: "", Type: "started"},
		{ID: "ps_completed", Name: "Completed", Color: "#5E6AD2", Type: "completed"},
		{ID: "ps_canceled", Name: "Canceled", Color: "#8A8F98", Type: "canceled"},
	}
	if len(data.Projects) == 0 {
		t.Fatal("fixture has no projects")
	}
	data.Projects[0].Status = domain.ProjectStatus{ID: "ps_planned", Name: "Planned", Color: "#A8A8AA", Type: "planned"}

	normalize(&data)

	colours := map[string]string{}
	for _, status := range data.ProjectStatuses {
		colours[status.ID] = status.Color
	}
	if colours["ps_backlog"] != "#F2994A" {
		t.Fatalf("the previous built-in backlog colour should move to the current default, got %q", colours["ps_backlog"])
	}
	if colours["ps_planned"] != "#26b5ce" {
		t.Fatalf("a colour the workspace chose must be kept, got %q", colours["ps_planned"])
	}
	if colours["ps_progress"] != "#F2C94C" {
		t.Fatalf("a missing colour should fall back to the default, got %q", colours["ps_progress"])
	}
	if status := data.Projects[0].Status; status.Name != "Next up" || status.Color != "#26b5ce" {
		t.Fatalf("a project's embedded status should follow the edited status, got %#v", status)
	}
}
