package store

import (
	"testing"

	"flow/api/internal/domain"
)

func TestCanonicalWorkflowStateColorsMatchReferenceDefaults(t *testing.T) {
	want := map[string]string{
		"state_backlog":   "#BEC2C8",
		"state_todo":      "#E2E2E2",
		"state_progress":  "lch(80% 90 85)",
		"state_done":      "lch(48% 59.31 288.43)",
		"state_canceled":  "#95A2B3",
		"state_duplicate": "#95A2B3",
	}
	for _, state := range canonicalWorkflowStates() {
		if want[state.ID] != state.Color {
			t.Fatalf("%s colour = %q, want %q", state.ID, state.Color, want[state.ID])
		}
	}
}

func TestNormalizeMigratesOnlyOldDefaultWorkflowColors(t *testing.T) {
	data := localSQLiteFixture()
	for index := range data.States {
		switch data.States[index].ID {
		case "state_backlog":
			data.States[index].Color = "#6B6F76"
		case "state_progress":
			data.States[index].Color = "#F2C94C"
		case "state_todo":
			data.States[index].Color = "#26B5CE"
		}
	}
	data.States = append(data.States, domain.WorkflowState{ID: "state_team_progress", TeamID: "team_test", Name: "In Progress", Color: "#F2C94C", Type: "started"})

	normalize(&data)

	colors := map[string]string{}
	for _, state := range data.States {
		colors[state.ID] = state.Color
	}
	if colors["state_backlog"] != "#BEC2C8" {
		t.Fatalf("old backlog default should move to the reference colour, got %q", colors["state_backlog"])
	}
	if colors["state_progress"] != "lch(80% 90 85)" {
		t.Fatalf("old In Progress default should move to the reference colour, got %q", colors["state_progress"])
	}
	if colors["state_todo"] != "#26B5CE" {
		t.Fatalf("a workspace-chosen colour must be kept, got %q", colors["state_todo"])
	}
	if colors["state_team_progress"] != "#F2C94C" {
		t.Fatalf("a team-specific status must not be rewritten, got %q", colors["state_team_progress"])
	}
}

func TestNormalizeMigratesLegacyProjectProgressDefault(t *testing.T) {
	data := localSQLiteFixture()
	for index := range data.ProjectStatuses {
		if data.ProjectStatuses[index].ID == "ps_progress" {
			data.ProjectStatuses[index].Color = "#F2C94C"
		}
	}

	normalize(&data)

	for _, status := range data.ProjectStatuses {
		if status.ID == "ps_progress" && status.Color != "lch(80% 90 85)" {
			t.Fatalf("old project In Progress default should move to the reference colour, got %q", status.Color)
		}
	}
}
