package main

import "testing"

func TestMCPCommonToolsUseBoundedWorkspaceProjection(t *testing.T) {
	repository, actor, ctx := newMCPToolTestContext(t)
	service := &server{store: repository}
	data, err := service.mcpWorkspaceDataForTool(ctx, actor, "list_issues", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Issues) != 0 {
		t.Fatalf("list_issues workspace projection loaded %d issues", len(data.Issues))
	}
	issue := repository.Bootstrap().Issues[0]
	result, err := service.callFlowTool(ctx, actor, "get_issue", map[string]any{"id": issue.Identifier})
	if err != nil {
		t.Fatalf("get_issue point read: %v", err)
	}
	if result == nil {
		t.Fatal("get_issue returned nil")
	}
}
