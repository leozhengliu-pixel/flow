package main

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Uninstalling an application revokes its tokens, cancels its active agent
// sessions, removes its managed webhook and takes its app user out of the
// workspace; installing it again works and reuses the app user.
func TestApplicationUninstallRevokesAndRemoves(t *testing.T) {
	s, handler, seed, _ := webhookTestServer(t)
	receiver := newWebhookReceiver(t, http.StatusOK)
	app, _ := installableApplication(t, handler, receiver.server.URL, []string{"Issue", "OAuthApp"})
	install := func() domain.ApplicationInstallation {
		return requestJSON[struct {
			Application domain.ApplicationInstallation `json:"application"`
		}](t, handler, http.MethodPost, "/api/application-installations?workspace=test-workspace", map[string]any{
			"clientId": app.ClientID, "teamIds": []string{seed.Teams[0].ID}, "scopes": []string{"read", "write", "app:assignable"}, "active": true,
		}, http.StatusOK).Application
	}
	installed := install()
	s.reconcileApplicationWebhooks(context.Background(), "test-workspace")
	if _, ok := managedWebhook(s, app.ClientID); !ok {
		t.Fatal("managed webhook missing after install")
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "For the app", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	delegated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issue-records/"+issue.ID+"?workspace=test-workspace", map[string]any{"delegateId": installed.UserID}, http.StatusOK)
	now := time.Now().UTC()
	err := s.store.MutateWorkspace(context.Background(), "test-workspace", "test.oauth_grant", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthAuthorizations = append(data.OAuthAuthorizations, domain.OAuthAuthorization{ID: "grant_uninstall", ClientID: app.ClientID, ClientName: app.Name, UserID: seed.Viewer.ID, Scopes: []string{"read"}, CreatedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The built-in agent and unknown installations cannot be uninstalled.
	requestJSON[any](t, handler, http.MethodDelete, "/api/application-installations/installation_missing?workspace=test-workspace", nil, http.StatusNotFound)
	requestJSON[any](t, handler, http.MethodDelete, "/api/application-installations/"+installed.ID+"?workspace=test-workspace", nil, http.StatusNoContent)

	if items := requestJSON[[]domain.ApplicationInstallation](t, handler, http.MethodGet, "/api/application-installations?workspace=test-workspace", nil, http.StatusOK); slices.ContainsFunc(items, func(item domain.ApplicationInstallation) bool { return item.ID == installed.ID }) {
		t.Fatal("uninstalled application still listed")
	}
	if _, ok := managedWebhook(s, app.ClientID); ok {
		t.Fatal("managed webhook kept after uninstall")
	}
	receiver.waitFor(t, func(envelope map[string]any, _ http.Header) bool {
		return envelope["type"] == "OAuthApp" && envelope["action"] == "revoked"
	})
	task, err := s.store.AgentTask(t.Context(), "test-workspace", delegated.AgentSessionID)
	if err != nil || task.Status != "canceled" {
		t.Fatalf("active session not canceled: %+v %v", task, err)
	}
	data, _ := s.store.WorkspaceMetadataFields("test-workspace", "oauthAuthorizations")
	if index := slices.IndexFunc(data.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.ID == "grant_uninstall" }); index < 0 || data.OAuthAuthorizations[index].RevokedAt == nil {
		t.Fatal("workspace authorization of the application not revoked")
	}
	members, err := s.store.ListMembers(t.Context(), seed.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(members, func(member domain.WorkspaceMember) bool { return member.User.ID == installed.UserID }) {
		t.Fatal("app user still a workspace member")
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap?workspace=test-workspace", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.Users, func(user domain.User) bool { return user.ID == installed.UserID && user.Active }) {
		t.Fatal("app user still offered as an active user")
	}
	other := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues?workspace=test-workspace", map[string]any{"title": "No app", "teamId": seed.Teams[0].ID}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPatch, "/api/issue-records/"+other.ID+"?workspace=test-workspace", map[string]any{"delegateId": installed.UserID}, http.StatusBadRequest)

	again := install()
	if again.UserID != installed.UserID || !again.Active {
		t.Fatalf("reinstall did not reuse the app user: %+v", again)
	}
}

func TestBuiltinApplicationCannotBeUninstalled(t *testing.T) {
	s, handler, data, _ := applicationFixture(t)
	if _, err := s.store.EnsureBuiltinApplication(t.Context(), data.Workspace.URLKey, true); err != nil {
		t.Fatal(err)
	}
	items := requestJSON[[]domain.ApplicationInstallation](t, handler, http.MethodGet, "/api/application-installations", nil, http.StatusOK)
	index := slices.IndexFunc(items, func(item domain.ApplicationInstallation) bool { return item.Builtin })
	if index < 0 {
		t.Fatal("built-in application missing")
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/application-installations/"+items[index].ID, nil, http.StatusConflict)
}
