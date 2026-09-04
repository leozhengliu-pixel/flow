package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"flow/api/internal/domain"
)

func TestAPIKeyAllowsRequestNilScopesRetainsFullWriteAccess(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/projects", nil)
	if !apiKeyAllowsRequest(request, domain.APIKey{Scopes: nil}) {
		t.Fatal("legacy key with nil scopes lost write access")
	}
}

func TestNormalizeAPIKeyScopesPreservesExplicitEmpty(t *testing.T) {
	scopes := normalizeAPIKeyScopes([]string{})
	if scopes == nil || len(scopes) != 0 {
		t.Fatal("an explicit empty scopes array must remain deny-all, not become full access")
	}
	if normalizeAPIKeyScopes(nil) != nil {
		t.Fatal("an omitted scopes value must remain the nil/full-access representation")
	}
}

func TestAPIKeyUpdateScopesDistinguishesOmittedNullAndEmpty(t *testing.T) {
	missing, present, err := apiKeyUpdateScopes(nil)
	if err != nil || present || missing != nil {
		t.Fatalf("missing scopes = %#v, present=%v, err=%v", missing, present, err)
	}
	null, present, err := apiKeyUpdateScopes(json.RawMessage("null"))
	if err != nil || !present || null != nil {
		t.Fatalf("null scopes = %#v, present=%v, err=%v", null, present, err)
	}
	empty, present, err := apiKeyUpdateScopes(json.RawMessage("[]"))
	if err != nil || !present || empty == nil || len(empty) != 0 {
		t.Fatalf("empty scopes = %#v, present=%v, err=%v", empty, present, err)
	}
}

func TestMCPGranularWriteScopesDoNotMutateSecondaryResources(t *testing.T) {
	issueTool := flowMCPTool{Name: "save_issue", Access: "write"}
	createIssues := domain.APIKey{Scopes: []string{"create_issues"}}
	for _, field := range []string{"links", "blockedBy", "blocks", "relatedTo", "removeBlockedBy", "removeBlocks", "removeRelatedTo", "duplicateOf", "estimate", "addReleases", "removeReleases", "setReleases"} {
		if mcpToolAllowed(issueTool, createIssues, false, map[string]any{field: []any{"resource_1"}}) {
			t.Fatalf("create_issues key was allowed to mutate issue secondary field %q", field)
		}
	}
	if !mcpToolAllowed(issueTool, createIssues, false, map[string]any{"title": "new issue"}) {
		t.Fatal("create_issues key was denied a basic issue mutation")
	}
	if !mcpToolAllowed(issueTool, domain.APIKey{Scopes: []string{"write"}}, false, map[string]any{"links": []any{"resource_1"}}) {
		t.Fatal("write key was denied issue secondary mutation")
	}

	commentTool := flowMCPTool{Name: "save_comment", Access: "write"}
	createComments := domain.APIKey{Scopes: []string{"create_comments"}}
	if !mcpToolAllowed(commentTool, createComments, false, map[string]any{"issueId": "issue_1", "body": "hello"}) {
		t.Fatal("create_comments key was denied an issue comment")
	}
	for _, field := range []string{"id", "parentId", "projectId", "initiativeId", "documentId", "milestoneId", "statusUpdateId", "statusUpdateType"} {
		if mcpToolAllowed(commentTool, createComments, false, map[string]any{field: "resource_1", "body": "hello"}) {
			t.Fatalf("create_comments key was allowed to target comment field %q", field)
		}
	}
	if !mcpToolAllowed(commentTool, domain.APIKey{Scopes: []string{"write"}}, false, map[string]any{"projectId": "project_1", "body": "hello"}) {
		t.Fatal("write key was denied a cross-resource comment mutation")
	}
}
