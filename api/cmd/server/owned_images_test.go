package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func uploadWorkspaceLogoFile(t *testing.T, client *http.Client, baseURL, workspace, filename string, payload []byte, wantStatus int) string {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", filename)
	_, _ = part.Write(payload)
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/api/workspaces/"+workspace+"/logo", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != wantStatus {
		t.Fatalf("logo upload %s = %d, want %d: %s", filename, response.StatusCode, wantStatus, raw)
	}
	var updated domain.Bootstrap
	_ = json.Unmarshal(raw, &updated)
	return updated.Workspace.LogoURL
}

// TestWorkspaceLogoReadPolicy uploads a workspace logo in every accepted
// format and reads it back: the workspace's members read it, another
// workspace's users and anonymous requests do not, and the invite-link page
// gets a short-lived signed URL for people who are not members yet.
func TestWorkspaceLogoReadPolicy(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	if err := s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	member := authClient(t)
	authRequest[map[string]any](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", 200)
	outsider, _ := verifiedAuthClient(t, host.URL, "Outside admin", "outside-logo@example.test")
	authRequest[domain.Bootstrap](t, outsider, http.MethodPost, host.URL+"/api/workspaces", map[string]string{"name": "Outside", "urlKey": "outside"}, "", http.StatusCreated)
	anonymous := &http.Client{}

	formats := []struct {
		filename, contentType string
		payload               []byte
	}{
		{"logo.svg", "image/svg+xml", []byte(safeOAuthAppIconSVG)},
		{"logo.png", "image/png", oauthAppIconRaster(t, "png")},
		{"logo.jpg", "image/jpeg", oauthAppIconRaster(t, "jpeg")},
		{"logo.webp", "image/webp", oauthAppIconRaster(t, "webp")},
	}
	previous := ""
	logo := ""
	for _, format := range formats {
		logo = uploadWorkspaceLogoFile(t, admin, host.URL, workspace, format.filename, format.payload, http.StatusOK)
		if !strings.HasPrefix(logo, "/uploads/workspace_logo_") {
			t.Fatalf("%s logo URL = %q", format.filename, logo)
		}
		for name, client := range map[string]*http.Client{"admin": admin, "member": member} {
			got := fetchOAuthAppIcon(t, client, host.URL+logo)
			if got.status != http.StatusOK || !bytes.Equal(got.body, format.payload) {
				t.Fatalf("%s for %s = %d, %d bytes (uploaded %d)", format.filename, name, got.status, len(got.body), len(format.payload))
			}
			assertOAuthAppIconHeaders(t, format.filename, got.header, format.contentType, len(format.payload))
		}
		if previous != "" {
			if got := fetchOAuthAppIcon(t, admin, host.URL+previous); got.status != http.StatusNotFound {
				t.Errorf("replaced logo %s = %d", previous, got.status)
			}
		}
		previous = logo
	}
	for _, item := range []struct {
		name   string
		client *http.Client
		want   int
	}{{"admin", admin, 200}, {"member", member, 200}, {"other workspace", outsider, 404}, {"anonymous", anonymous, 401}} {
		if got := fetchOAuthAppIcon(t, item.client, host.URL+logo); got.status != item.want {
			t.Errorf("workspace logo for %s = %d, want %d", item.name, got.status, item.want)
		}
	}

	// The invite-link page shows the logo to people who are not members (and
	// may not be signed in): the preview signs that one object.
	authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, host.URL+"/api/workspace/preferences", map[string]any{"inviteLinksEnabled": true}, workspace, http.StatusOK)
	link := authRequest[domain.WorkspaceInviteLink](t, admin, http.MethodPost, host.URL+"/api/workspaces/"+workspace+"/invite-link", map[string]any{}, workspace, http.StatusOK)
	preview := authRequest[struct {
		Workspace domain.Workspace `json:"workspace"`
	}](t, anonymous, http.MethodGet, host.URL+"/api/invite-links/preview/"+link.Token, nil, "", http.StatusOK)
	if !strings.HasPrefix(preview.Workspace.LogoURL, "/api/files/signed/") {
		t.Fatalf("invite preview logo = %q", preview.Workspace.LogoURL)
	}
	signed := fetchOAuthAppIcon(t, anonymous, host.URL+preview.Workspace.LogoURL)
	if signed.status != http.StatusOK || !bytes.Equal(signed.body, formats[3].payload) {
		t.Fatalf("signed invite logo = %d, %d bytes", signed.status, len(signed.body))
	}
	assertOAuthAppIconHeaders(t, "signed logo", signed.header, "image/webp", len(formats[3].payload))
	if got := fetchOAuthAppIcon(t, anonymous, host.URL+strings.Replace(preview.Workspace.LogoURL, "signature=", "signature=0", 1)); got.status != http.StatusForbidden {
		t.Errorf("tampered signed logo = %d", got.status)
	}

	// A logo URL can only name this workspace's own uploaded logo; private
	// attachments and other workspaces' logos stay where they are.
	attachment := uploadAuthAttachment(t, admin, host.URL, data.Issues[0].ID, workspace, http.StatusCreated)
	authRequest[map[string]any](t, admin, http.MethodPatch, host.URL+"/api/workspaces/"+workspace, map[string]any{"logoUrl": attachment.URL}, workspace, http.StatusBadRequest)
	authRequest[map[string]any](t, outsider, http.MethodPatch, host.URL+"/api/workspaces/outside", map[string]any{"logoUrl": logo}, "outside", http.StatusBadRequest)
	authRequest[domain.Bootstrap](t, admin, http.MethodPatch, host.URL+"/api/workspaces/"+workspace, map[string]any{"logoUrl": logo}, workspace, http.StatusOK)
	if got := fetchOAuthAppIcon(t, outsider, host.URL+attachment.URL); got.status != http.StatusNotFound {
		t.Errorf("other workspace read a private attachment: %d", got.status)
	}
	if got := fetchOAuthAppIcon(t, admin, host.URL+attachment.URL); got.status != http.StatusOK {
		t.Errorf("attachment for its workspace admin = %d", got.status)
	}

	// Removing the logo deletes it.
	authRequest[domain.Bootstrap](t, admin, http.MethodDelete, host.URL+"/api/workspaces/"+workspace+"/logo", nil, workspace, http.StatusOK)
	if got := fetchOAuthAppIcon(t, admin, host.URL+logo); got.status != http.StatusNotFound {
		t.Errorf("removed logo = %d", got.status)
	}
	if _, err := s.store.FileUploadByObject(t.Context(), strings.TrimPrefix(logo, "/uploads/")); !errors.Is(err, store.ErrFileUploadNotFound) {
		t.Errorf("removed logo owner row = %v", err)
	}
}

// TestBackfillUploadOwners: icons and logos stored before owners were
// recorded get an owner from the record that uses them; an icon nothing (or
// more than one workspace) uses stays without one.
func TestBackfillUploadOwners(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	ctx := t.Context()
	workspace := data.Workspace.URLKey
	if err := s.store.UpdateMemberRole(ctx, data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	member := authClient(t)
	authRequest[map[string]any](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", 200)
	outsider, _ := verifiedAuthClient(t, host.URL, "Outside admin", "outside-backfill@example.test")
	authRequest[domain.Bootstrap](t, outsider, http.MethodPost, host.URL+"/api/workspaces", map[string]string{"name": "Outside", "urlKey": "outside"}, "", http.StatusCreated)

	storage, err := s.storage()
	if err != nil {
		t.Fatal(err)
	}
	png := oauthAppIconRaster(t, "png")
	legacyLogo := "workspace_logo_" + data.Workspace.ID + "_legacy.png"
	for _, name := range []string{"oauth_app_icon_legacy.png", "oauth_app_icon_shared.png", "oauth_app_icon_unused.png", legacyLogo} {
		if _, err := storage.Put(ctx, name, bytes.NewReader(png), "image/png"); err != nil {
			t.Fatal(err)
		}
	}
	addLegacyApp := func(key, id, logo string) {
		t.Helper()
		if err := s.store.MutateWorkspace(ctx, key, "oauth_application.created", id, nil, func(current *domain.Bootstrap) error {
			current.OAuthApplications = append(current.OAuthApplications, domain.OAuthApplication{ID: id, Name: id, ClientID: "flow_client_" + id, LogoURL: logo, CreatorID: current.Viewer.ID, RedirectURIs: []string{}, Scopes: []string{"read"}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	addLegacyApp(workspace, "legacy", "/uploads/oauth_app_icon_legacy.png")
	addLegacyApp(workspace, "shared_home", "/uploads/oauth_app_icon_shared.png")
	addLegacyApp("outside", "shared_outside", "/uploads/oauth_app_icon_shared.png")
	current, _ := s.store.WorkspaceMetadata(workspace)
	current.Workspace.LogoURL = "/uploads/" + legacyLogo
	if _, err := s.store.UpdateWorkspace(ctx, workspace, current.Workspace); err != nil {
		t.Fatal(err)
	}
	if got := fetchOAuthAppIcon(t, member, host.URL+"/uploads/"+legacyLogo); got.status != http.StatusNotFound {
		t.Fatalf("legacy logo before backfill = %d", got.status)
	}

	recorded, err := s.store.BackfillUploadOwners(ctx)
	if err != nil || recorded != 2 {
		t.Fatalf("backfill = %d, %v", recorded, err)
	}
	if again, err := s.store.BackfillUploadOwners(ctx); err != nil || again != 0 {
		t.Fatalf("second backfill = %d, %v", again, err)
	}
	icon, err := s.store.FileUploadByObject(ctx, "oauth_app_icon_legacy.png")
	if err != nil || icon.Purpose != store.FileUploadPurposeOAuthAppIcon || icon.WorkspaceKey != workspace || icon.UserID != current.Viewer.ID || icon.ContentType != "image/png" {
		t.Fatalf("icon owner = %+v, %v", icon, err)
	}
	logo, err := s.store.FileUploadByObject(ctx, legacyLogo)
	if err != nil || logo.Purpose != store.FileUploadPurposeWorkspaceLogo || logo.WorkspaceKey != workspace {
		t.Fatalf("logo owner = %+v, %v", logo, err)
	}
	for _, name := range []string{"oauth_app_icon_shared.png", "oauth_app_icon_unused.png"} {
		if _, err := s.store.FileUploadByObject(ctx, name); !errors.Is(err, store.ErrFileUploadNotFound) {
			t.Errorf("%s got an owner: %v", name, err)
		}
	}

	for _, item := range []struct {
		name   string
		client *http.Client
		path   string
		want   int
	}{
		{"member legacy logo", member, legacyLogo, 200},
		{"other workspace legacy logo", outsider, legacyLogo, 404},
		{"member legacy icon", member, "oauth_app_icon_legacy.png", 200},
		{"other workspace legacy icon", outsider, "oauth_app_icon_legacy.png", 404},
		{"admin unused icon", admin, "oauth_app_icon_unused.png", 404},
	} {
		got := fetchOAuthAppIcon(t, item.client, host.URL+"/uploads/"+item.path)
		if got.status != item.want || item.want == 200 && !bytes.Equal(got.body, png) {
			t.Errorf("%s = %d, want %d", item.name, got.status, item.want)
		}
	}
	// With an owner, another workspace's application can no longer adopt it.
	authRequest[map[string]any](t, outsider, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Borrowed", "logoUrl": "/uploads/oauth_app_icon_legacy.png"}, "outside", http.StatusBadRequest)
}
