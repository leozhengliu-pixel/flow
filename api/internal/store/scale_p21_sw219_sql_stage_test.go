package store

import "testing"

func TestScaleP21SW219WorkflowSQLStageDeclaration(t *testing.T) {
	base := metadataSQLStageTarget{
		workspace: "workspace", field: "workflowRuns", key: "run",
		dataBytes: 2, searchBytes: -1,
	}
	if !base.valid() {
		t.Fatal("workflow stream cannot stage its no-search row")
	}
	for _, test := range []struct {
		name string
		edit func(*metadataSQLStageTarget)
	}{
		{"search", func(row *metadataSQLStageTarget) { row.searchBytes = 0 }},
		{"empty-key", func(row *metadataSQLStageTarget) { row.key = "" }},
		{"empty-workspace", func(row *metadataSQLStageTarget) { row.workspace = "" }},
		{"workspace-identity", func(row *metadataSQLStageTarget) { row.workspaceID = "identity" }},
		{"workspace-clock", func(row *metadataSQLStageTarget) { row.updatedAt = "clock" }},
		{"root", func(row *metadataSQLStageTarget) { row.workspaceState = true }},
		{"parent-map", func(row *metadataSQLStageTarget) { row.projectUpdateParent = true }},
		{"other-field", func(row *metadataSQLStageTarget) { row.field = "workflowDefinitions" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := base
			test.edit(&target)
			if target.valid() {
				t.Fatal("workflow stage accepted an unrelated declaration")
			}
		})
	}
}
