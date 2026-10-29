package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func (f *chatWorkflowFixture) preview(t *testing.T, openID, link, chatID string) map[string]any {
	t.Helper()
	envelope := map[string]any{
		"schema": "2.0",
		"header": map[string]string{"app_id": f.record.AppID, "tenant_key": f.record.TenantKey, "token": f.credentials.VerificationToken, "event_id": "evt_preview", "event_type": "url.preview.get"},
		"event": map[string]any{
			"operator": map[string]string{"tenant_key": f.record.TenantKey, "open_id": openID},
			"host":     "im_message",
			"context":  map[string]string{"url": link, "preview_token": "preview_token_test", "open_message_id": "om_preview", "open_chat_id": chatID},
		},
	}
	w := f.callback(t, envelope, time.Now(), true)
	if w.Code != http.StatusOK {
		t.Fatalf("preview callback: %d %s", w.Code, w.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func chatPreviewText(result map[string]any) string {
	raw, _ := json.Marshal(result)
	return string(raw)
}

func TestChatLinkPreviewFollowsViewerPermissionsAndGroupScope(t *testing.T) {
	f := newChatWorkflowFixture(t)
	f.s.allowedOrigin = "https://flow.example.test"
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	base := "https://flow.example.test/" + f.record.WorkspaceKey
	issueLink := base + "/issue/" + issue.Identifier + "/some-title"
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.preview", issue.Team.ID, nil, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		data.Documents = append(data.Documents, domain.Document{ID: "doc_preview", SlugID: "spec-0123456789ab", Title: "Export spec", Content: "# Plan\n\nShip the **export** fix", Creator: data.Viewer, TeamIDs: []string{issue.Team.ID}, ProjectIDs: []string{}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: now, UpdatedAt: now})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "initiative_preview", SlugID: "growth-0123456789ab", Name: "Growth", Summary: "Grow the base", Status: "active", LeadTeamID: issue.Team.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	project := data.Projects[0]

	// A linked member's own context: every kind expands.
	result := f.preview(t, f.identity.OpenID, issueLink, "")
	if text := chatPreviewText(result); !strings.Contains(text, issue.Identifier) || !strings.Contains(text, `"type":"raw"`) || !strings.Contains(text, "Status: "+issue.State.Name) || !strings.Contains(text, "Priority: ") || !strings.Contains(text, "Team: "+issue.Team.Name) || !strings.Contains(text, "Assignee: ") {
		t.Fatalf("issue preview: %s", text)
	}
	for _, tc := range []struct{ link, want string }{
		{base + "/project/" + project.SlugID + "/overview", project.Name},
		{base + "/document/spec-0123456789ab", "Ship the export fix"},
		{base + "/initiative/growth-0123456789ab/overview", "Grow the base"},
	} {
		if text := chatPreviewText(f.preview(t, f.identity.OpenID, tc.link, "")); !strings.Contains(text, tc.want) || !strings.Contains(text, "Open in Flow") {
			t.Fatalf("preview of %s: %s", tc.link, text)
		}
	}
	// Unlinked viewers get a hint only; other tenants, other workspaces and
	// other hosts get nothing.
	if text := chatPreviewText(f.preview(t, "ou_unlinked", issueLink, "")); strings.Contains(text, issue.Identifier) || !strings.Contains(text, "Connect your Flow account") {
		t.Fatalf("unlinked preview: %s", text)
	}
	if len(f.preview(t, f.identity.OpenID, "https://flow.example.test/other-workspace/issue/"+issue.Identifier, "")) != 0 || len(f.preview(t, f.identity.OpenID, "https://elsewhere.example/"+f.record.WorkspaceKey+"/issue/"+issue.Identifier, "")) != 0 || len(f.preview(t, f.identity.OpenID, base+"/issue/MISSING-1", "")) != 0 {
		t.Fatal("a link outside the workspace or to a missing issue expanded")
	}
	if len(f.s.chatLinkPreview(t.Context(), f.record, "tenant_other", f.identity.OpenID, issueLink, "")) != 0 {
		t.Fatal("another tenant's viewer got a preview")
	}

	// Public team content expands in any group.
	if text := chatPreviewText(f.preview(t, f.identity.OpenID, issueLink, "oc_unbound_group")); !strings.Contains(text, issue.Identifier) {
		t.Fatalf("public issue in a group: %s", text)
	}

	// The team becomes private: only the member's own context and a group
	// bound to the team with its owner's authorization expand it.
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "team.updated", issue.Team.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			if data.Teams[index].ID == issue.Team.ID {
				data.Teams[index].Private = true
				settings := data.TeamSettings[issue.Team.ID]
				settings.Access = "private"
				data.TeamSettings[issue.Team.ID] = settings
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if text := chatPreviewText(f.preview(t, f.identity.OpenID, issueLink, "")); !strings.Contains(text, issue.Identifier) {
		t.Fatalf("private issue in the member's own context: %s", text)
	}
	for _, link := range []string{issueLink, base + "/project/" + project.SlugID, base + "/document/spec-0123456789ab", base + "/initiative/growth-0123456789ab"} {
		if result := f.preview(t, f.identity.OpenID, link, "oc_unbound_group"); len(result) != 0 {
			t.Fatalf("private team content expanded in an unbound group (%s): %s", link, chatPreviewText(result))
		}
		// The public binding of the fixture does not authorize the private team.
		if result := f.preview(t, f.identity.OpenID, link, f.binding.ChatID); len(result) != 0 {
			t.Fatalf("private team content expanded under a public binding (%s): %s", link, chatPreviewText(result))
		}
	}
	private := f.binding
	private.ID, private.ChatID = "binding_private_preview", "oc_private_group"
	private.Authorization = &domain.ChatBindingAuthorization{UserID: f.identity.UserID, Role: domain.ChatBindingRoleTeamOwner, AuthorizedAt: time.Now().UTC(), PrivateTeam: true}
	if err := f.s.store.PutChatBinding(t.Context(), f.record.WorkspaceKey, private); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{issueLink, base + "/document/spec-0123456789ab"} {
		if text := chatPreviewText(f.preview(t, f.identity.OpenID, link, private.ChatID)); !strings.Contains(text, "Open in Flow") {
			t.Fatalf("private team content in its bound group (%s): %s", link, text)
		}
	}

	// A member outside the private team sees nothing, even in its group.
	member, _, err := f.s.store.LoginExternal(t.Context(), "chat-preview@example.test", "Preview member", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.EnsureWorkspaceMembership(t.Context(), data.Workspace.ID, member.User.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, member.User.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.SetTeamMembership(t.Context(), data.Workspace.ID, issue.Team.ID, member.User.ID, "", false); err != nil {
		t.Fatal(err)
	}
	if err = f.s.store.PutChatIdentity(t.Context(), f.record.WorkspaceKey, f.record.ConnectionID, domain.ChatIdentity{UserID: member.User.ID, OpenID: "ou_preview_member", TenantKey: f.record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, chatID := range []string{"", private.ChatID} {
		for _, link := range []string{issueLink, base + "/document/spec-0123456789ab", base + "/project/" + project.SlugID} {
			if result := f.preview(t, "ou_preview_member", link, chatID); len(result) != 0 {
				t.Fatalf("content the member cannot open expanded (%s in %q): %s", link, chatID, chatPreviewText(result))
			}
		}
	}

	// Sensitive data protection: nothing expands.
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.HIPAACompliance = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if result := f.preview(t, f.identity.OpenID, issueLink, ""); len(result) != 0 {
		t.Fatalf("preview with sensitive data protection: %s", chatPreviewText(result))
	}
}
