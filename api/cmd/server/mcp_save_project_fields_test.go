package main

import (
	"strings"
	"testing"
)

// save_project's state and targetDate must change the project (they used to
// come back 200 with nothing changed). State resolves by status name, id or
// type; an unknown state or a malformed date is an error, never a silent no-op.
func TestMCPSaveProjectStateAndTargetDate(t *testing.T) {
	f := newMCPContractFixture(t)
	project := f.data.Projects[0]
	find := func() (statusID, targetDate string) {
		t.Helper()
		for _, item := range f.repository.Bootstrap().Projects {
			if item.ID == project.ID {
				if item.TargetDate != nil {
					targetDate = *item.TargetDate
				}
				return item.Status.ID, targetDate
			}
		}
		t.Fatal("project missing")
		return "", ""
	}
	statuses := f.repository.Bootstrap().ProjectStatuses
	if len(statuses) < 2 {
		t.Fatalf("fixture statuses: %v", statuses)
	}
	before, _ := find()
	var target = statuses[0]
	for _, status := range statuses {
		if status.ID != before {
			target = status
			break
		}
	}
	receipt := mcpObject(t, f, "save_project", map[string]any{"id": project.ID, "state": target.Name, "targetDate": "2026-10-13"})
	statusID, date := find()
	if statusID != target.ID || date != "2026-10-13" {
		t.Fatalf("after save by name: status %q (want %q) targetDate %q; receipt %v", statusID, target.ID, date, receipt)
	}
	// By id and by type.
	mcpObject(t, f, "save_project", map[string]any{"id": project.ID, "state": statuses[0].ID})
	if statusID, _ := find(); statusID != statuses[0].ID {
		t.Fatalf("by id: %q", statusID)
	}
	mcpObject(t, f, "save_project", map[string]any{"id": project.ID, "state": "completed"})
	if statusID, _ := find(); statusID != "ps_completed" {
		t.Fatalf("by type: %q", statusID)
	}
	// Timestamps are cut to the date; "" clears it.
	mcpObject(t, f, "save_project", map[string]any{"id": project.ID, "targetDate": "2026-11-02T10:00:00Z"})
	if _, date := find(); date != "2026-11-02" {
		t.Fatalf("timestamp date: %q", date)
	}
	mcpObject(t, f, "save_project", map[string]any{"id": project.ID, "targetDate": ""})
	if _, date := find(); date != "" {
		t.Fatalf("cleared date: %q", date)
	}
	for args, want := range map[string]map[string]any{
		"not found; use one of":    {"id": project.ID, "state": "运行中"},
		"must be a project status": {"id": project.ID, "state": ""},
		"must be a date":           {"id": project.ID, "targetDate": "13/10/2026"},
	} {
		if message := mcpToolError(t, f, "save_project", want); !strings.Contains(message, args) {
			t.Errorf("%v: %s", want, message)
		}
	}
	if statusID, date := find(); statusID != "ps_completed" || date != "" {
		t.Fatalf("a rejected save changed the project: %q %q", statusID, date)
	}
}
