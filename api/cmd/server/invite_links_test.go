package main

import (
	"net/http"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestWorkspaceInviteLinkJoinAndAlreadyMember(t *testing.T) {
	handler, _ := enterpriseTestServer(t)
	prefs := requestJSON[domain.WorkspaceSettings](t, handler, http.MethodPatch, "/api/workspace/preferences?workspace=test-workspace", map[string]any{
		"inviteLinksEnabled": true,
	}, http.StatusOK)
	if !prefs.InviteLinksEnabled {
		t.Fatal("expected invite links enabled")
	}
	link := requestJSON[domain.WorkspaceInviteLink](t, handler, http.MethodPost, "/api/workspaces/test-workspace/invite-link", map[string]any{}, http.StatusOK)
	if link.Token == "" || !link.Enabled {
		t.Fatalf("unexpected link %#v", link)
	}
	preview := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/invite-links/preview/"+link.Token, nil, http.StatusOK)
	if preview["alreadyMember"] != true {
		t.Fatalf("viewer should already be a member: %#v", preview)
	}
	membership := requestJSON[domain.WorkspaceMembership](t, handler, http.MethodPost, "/api/invite-links/join", map[string]any{
		"token": link.Token,
	}, http.StatusOK)
	if membership.Workspace.URLKey != "test-workspace" {
		t.Fatalf("unexpected membership %#v", membership)
	}
	requestJSON[map[string]any](t, handler, http.MethodGet, "/api/invite-links/preview/missing-token", nil, http.StatusNotFound)
}

func TestSCIMTokenRotateEndpoint(t *testing.T) {
	handler, _ := enterpriseTestServer(t)
	created := requestJSON[store.SCIMToken](t, handler, http.MethodPost, "/api/scim/tokens?workspace=test-workspace", map[string]any{
		"name": "primary",
	}, http.StatusCreated)
	if created.ID == "" || created.Secret == "" {
		t.Fatalf("missing token %#v", created)
	}
	rotated := requestJSON[store.SCIMToken](t, handler, http.MethodPost, "/api/scim/tokens/"+created.ID+"/rotate?workspace=test-workspace", map[string]any{}, http.StatusCreated)
	if rotated.Secret == "" {
		t.Fatalf("expected rotated secret %#v", rotated)
	}
	if rotated.ID == created.ID {
		t.Fatal("rotated token should have a new id")
	}
}
