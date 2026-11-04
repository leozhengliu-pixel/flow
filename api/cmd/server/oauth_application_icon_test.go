package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

const safeOAuthAppIconSVG = `<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg" width="256" height="256" viewBox="0 0 256 256"><defs><linearGradient id="g"><stop offset="0" stop-color="#5e6ad2"/></linearGradient></defs><rect width="256" height="256" rx="48" fill="url(#g)"/><circle cx="128" cy="128" r="64" style="fill:#fff"/></svg>`

// A 1x1 lossless WebP image.
const oauthAppIconWebP = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func oauthAppIconRaster(t *testing.T, format string) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := range 4 {
		for y := range 4 {
			picture.Set(x, y, color.RGBA{R: 94, G: 106, B: 210, A: 255})
		}
	}
	var out bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&out, picture)
	case "jpeg":
		err = jpeg.Encode(&out, picture, nil)
	case "webp":
		raw, decodeErr := base64.StdEncoding.DecodeString(oauthAppIconWebP)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return raw
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func uploadOAuthAppIconFile(t *testing.T, client *http.Client, baseURL, workspace, filename string, payload []byte, wantStatus int) string {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("file", filename)
	_, _ = part.Write(payload)
	_ = form.Close()
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/api/oauth-applications/icon", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("X-Workspace-Key", workspace)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != wantStatus {
		t.Fatalf("upload %s = %d, want %d: %s", filename, response.StatusCode, wantStatus, raw)
	}
	var uploaded map[string]string
	_ = json.Unmarshal(raw, &uploaded)
	return uploaded["url"]
}

type fetchedIcon struct {
	status int
	header http.Header
	body   []byte
}

func fetchOAuthAppIcon(t *testing.T, client *http.Client, iconURL string) fetchedIcon {
	t.Helper()
	response, err := client.Get(iconURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return fetchedIcon{status: response.StatusCode, header: response.Header, body: body}
}

// TestOAuthApplicationIconChain uploads an icon in every accepted format,
// sets it as the application's logo, installs the application and reads the
// image back as the people who see the application and its agent, checking
// the bytes and the response headers.
func TestOAuthApplicationIconChain(t *testing.T) {
	s, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	if err := s.store.UpdateMemberRole(t.Context(), data.Workspace.ID, "usr_member", "member"); err != nil {
		t.Fatal(err)
	}
	member := authClient(t)
	authRequest[map[string]any](t, member, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", 200)
	outsider, _ := verifiedAuthClient(t, host.URL, "Outside admin", "outside-admin@example.test")
	authRequest[domain.Bootstrap](t, outsider, http.MethodPost, host.URL+"/api/workspaces", map[string]string{"name": "Outside", "urlKey": "outside"}, "", http.StatusCreated)
	anonymous := &http.Client{}

	formats := []struct {
		filename, contentType string
		payload               []byte
	}{
		{"icon.svg", "image/svg+xml", []byte(safeOAuthAppIconSVG)},
		{"icon.png", "image/png", oauthAppIconRaster(t, "png")},
		{"icon.jpg", "image/jpeg", oauthAppIconRaster(t, "jpeg")},
		{"icon.webp", "image/webp", oauthAppIconRaster(t, "webp")},
	}
	urls := map[string]string{}
	for _, format := range formats {
		urls[format.filename] = uploadOAuthAppIconFile(t, admin, host.URL, workspace, format.filename, format.payload, http.StatusCreated)
		if !uploadedOAuthAppIcon(urls[format.filename]) {
			t.Fatalf("%s uploaded as %q", format.filename, urls[format.filename])
		}
	}

	// Upload → logoUrl → installation.
	svgURL := urls["icon.svg"]
	app := authRequest[domain.OAuthApplication](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Icon agent", "redirectUris": []string{oauthTestRedirect}, "scopes": domain.ApplicationScopes(), "logoUrl": svgURL}, workspace, 201)
	if app.LogoURL != svgURL {
		t.Fatalf("logoUrl = %q", app.LogoURL)
	}
	teamID := data.Teams[0].ID
	installed := authRequest[struct {
		Application domain.ApplicationInstallation `json:"application"`
	}](t, admin, "POST", host.URL+"/api/application-installations", map[string]any{"clientId": app.ClientID, "teamIds": []string{teamID}, "scopes": []string{"read", "write"}, "active": true}, workspace, 200).Application
	installations := authRequest[[]domain.ApplicationInstallation](t, admin, "GET", host.URL+"/api/application-installations", nil, workspace, 200)
	index := slices.IndexFunc(installations, func(item domain.ApplicationInstallation) bool { return item.ID == installed.ID })
	if index < 0 || installations[index].AvatarURL != svgURL {
		t.Fatalf("installation avatar = %+v", installations)
	}

	// The image bytes and headers, per format, for the admin.
	for _, format := range formats {
		got := fetchOAuthAppIcon(t, admin, host.URL+urls[format.filename])
		if got.status != http.StatusOK || !bytes.Equal(got.body, format.payload) {
			t.Fatalf("%s: GET = %d, %d bytes (uploaded %d)", format.filename, got.status, len(got.body), len(format.payload))
		}
		assertOAuthAppIconHeaders(t, format.filename, got.header, format.contentType, len(format.payload))
	}

	// Who reads the private application's icon: its workspace's admin and
	// members (they see the application and its agent); not another
	// workspace's user; anonymous requests are refused by the session check.
	for _, item := range []struct {
		name   string
		client *http.Client
		want   int
	}{{"admin", admin, 200}, {"member", member, 200}, {"other workspace", outsider, 404}, {"anonymous", anonymous, 401}} {
		if got := fetchOAuthAppIcon(t, item.client, host.URL+svgURL); got.status != item.want {
			t.Errorf("private app icon for %s = %d, want %d", item.name, got.status, item.want)
		}
	}
	// An icon no application uses yet is the uploading admin's preview.
	pngURL := urls["icon.png"]
	for _, item := range []struct {
		name   string
		client *http.Client
		want   int
	}{{"admin", admin, 200}, {"member", member, 404}, {"other workspace", outsider, 404}, {"anonymous", anonymous, 401}} {
		if got := fetchOAuthAppIcon(t, item.client, host.URL+pngURL); got.status != item.want {
			t.Errorf("unused icon for %s = %d, want %d", item.name, got.status, item.want)
		}
	}
	if got := fetchOAuthAppIcon(t, admin, host.URL+"/uploads/oauth_app_icon_missing.png"); got.status != http.StatusNotFound {
		t.Errorf("missing icon = %d", got.status)
	}

	// A public application's consent screen is shown to any signed-in
	// account, so any signed-in user may read its icon; anonymous may not.
	authRequest[domain.OAuthApplication](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+app.ID, map[string]any{"publicEnabled": true}, workspace, 200)
	query := url.Values{"client_id": {app.ClientID}, "redirect_uri": {oauthTestRedirect}, "response_type": {"code"}, "scope": {"read"}}
	consent := authRequest[struct {
		Client domain.OAuthClient `json:"client"`
	}](t, outsider, "GET", host.URL+"/api/oauth/authorization-request?"+query.Encode(), nil, "", 200)
	if consent.Client.LogoURI != svgURL {
		t.Fatalf("consent logo = %q", consent.Client.LogoURI)
	}
	if got := fetchOAuthAppIcon(t, outsider, host.URL+consent.Client.LogoURI); got.status != 200 || !bytes.Equal(got.body, []byte(safeOAuthAppIconSVG)) {
		t.Errorf("public app icon for another workspace = %d", got.status)
	}
	if got := fetchOAuthAppIcon(t, anonymous, host.URL+svgURL); got.status != 401 {
		t.Errorf("public app icon for anonymous = %d", got.status)
	}

	// A logo change updates the installed agent's avatar; the application's
	// identity, credentials and team grants stay.
	before := authRequest[domain.OAuthApplication](t, admin, "GET", host.URL+"/api/oauth-applications/"+app.ID, nil, workspace, 200)
	updated := authRequest[domain.OAuthApplication](t, admin, "PATCH", host.URL+"/api/oauth-applications/"+app.ID, map[string]any{"logoUrl": pngURL}, workspace, 200)
	if updated.LogoURL != pngURL || updated.ID != before.ID || updated.ClientID != before.ClientID || updated.Name != before.Name || !slices.Equal(updated.Scopes, before.Scopes) || !slices.Equal(updated.RedirectURIs, before.RedirectURIs) {
		t.Fatalf("logo change altered the application: before %+v after %+v", before, updated)
	}
	if stored, ok := s.store.OAuthApplicationClient(app.ClientID); !ok || stored.ClientID != app.ClientID {
		t.Fatal("application client lost")
	}
	if current := s.store.Bootstrap().OAuthApplications; !slices.ContainsFunc(current, func(item domain.OAuthApplication) bool {
		return item.ID == app.ID && item.ClientSecret == app.ClientSecret
	}) {
		t.Fatal("client secret changed with the logo")
	}
	installations = authRequest[[]domain.ApplicationInstallation](t, admin, "GET", host.URL+"/api/application-installations", nil, workspace, 200)
	index = slices.IndexFunc(installations, func(item domain.ApplicationInstallation) bool { return item.ID == installed.ID })
	if index < 0 || installations[index].AvatarURL != pngURL || installations[index].UserID != installed.UserID || !slices.Equal(installations[index].TeamIDs, []string{teamID}) || !slices.Equal(installations[index].Scopes, installed.Scopes) {
		t.Fatalf("installation after logo change = %+v (installed %+v)", installations, installed)
	}
	users := authRequest[domain.Bootstrap](t, admin, "GET", host.URL+"/api/bootstrap", nil, workspace, 200).Users
	if user := slices.IndexFunc(users, func(user domain.User) bool { return user.ID == installed.UserID }); user < 0 || users[user].AvatarURL != pngURL {
		t.Fatalf("agent user avatar not updated")
	}
	if got := fetchOAuthAppIcon(t, member, host.URL+pngURL); got.status != 200 || !bytes.Equal(got.body, formats[1].payload) {
		t.Errorf("new logo for member = %d", got.status)
	}
	if got := fetchOAuthAppIcon(t, outsider, host.URL+pngURL); got.status != 200 {
		t.Errorf("new public logo for another workspace = %d", got.status)
	}
	// The replaced SVG is no longer the application's logo.
	if got := fetchOAuthAppIcon(t, member, host.URL+svgURL); got.status != 404 {
		t.Errorf("replaced logo for member = %d", got.status)
	}
}

func assertOAuthAppIconHeaders(t *testing.T, name string, header http.Header, contentType string, size int) {
	t.Helper()
	if got := header.Get("Content-Type"); got != contentType {
		t.Errorf("%s Content-Type = %q, want %q", name, got, contentType)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("%s X-Content-Type-Options = %q", name, got)
	}
	if got := header.Get("Content-Disposition"); !strings.HasPrefix(got, "inline") || !strings.Contains(got, `filename="`) {
		t.Errorf("%s Content-Disposition = %q", name, got)
	}
	policy := header.Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'none'", "sandbox"} {
		if !strings.Contains(policy, directive) {
			t.Errorf("%s Content-Security-Policy = %q, missing %q", name, policy, directive)
		}
	}
	if strings.Contains(policy, "script-src") || strings.Contains(policy, "unsafe-eval") {
		t.Errorf("%s Content-Security-Policy allows scripts: %q", name, policy)
	}
	if got := header.Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Errorf("%s Cross-Origin-Resource-Policy = %q", name, got)
	}
	if got := header.Get("Cache-Control"); !strings.HasPrefix(got, "private") {
		t.Errorf("%s Cache-Control = %q", name, got)
	}
	if got := header.Get("Content-Length"); got != strconv.Itoa(size) {
		t.Errorf("%s Content-Length = %q, want %d", name, got, size)
	}
}

// TestOAuthApplicationIconRejectsUnsafeContent: unsafe SVGs and mislabelled
// images are refused at upload, and an application logo can only name an
// icon its own workspace uploaded (never another file under /uploads/).
func TestOAuthApplicationIconRejectsUnsafeContent(t *testing.T) {
	_, host, admin, data := applicationHTTPFixture(t)
	workspace := data.Workspace.URLKey
	for name, svg := range map[string]string{
		"script":          `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"event handler":   `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><rect width="1" height="1"/></svg>`,
		"nested handler":  `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1" onclick="alert(1)"/></svg>`,
		"javascript href": `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><a xlink:href="javascript:alert(1)"><rect width="1" height="1"/></a></svg>`,
		"external image":  `<svg xmlns="http://www.w3.org/2000/svg"><image href="https://evil.example.test/track.png" width="1" height="1"/></svg>`,
		"external use":    `<svg xmlns="http://www.w3.org/2000/svg"><use href="https://evil.example.test/sprite.svg#a"/></svg>`,
		"style url":       `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1" style="fill:url(https://evil.example.test/x)"/></svg>`,
		"style element":   `<svg xmlns="http://www.w3.org/2000/svg"><style>@import url(https://evil.example.test/x.css);</style></svg>`,
		"foreign object":  `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><iframe xmlns="http://www.w3.org/1999/xhtml" src="https://evil.example.test"/></foreignObject></svg>`,
		"entity":          `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg xmlns="http://www.w3.org/2000/svg">&x;</svg>`,
		"not svg root":    `<html><body>hi</body></html>`,
	} {
		t.Run(name, func(t *testing.T) {
			uploadOAuthAppIconFile(t, admin, host.URL, workspace, "icon.svg", []byte(svg), http.StatusBadRequest)
		})
	}
	uploadOAuthAppIconFile(t, admin, host.URL, workspace, "icon.png", []byte(safeOAuthAppIconSVG), http.StatusBadRequest)
	uploadOAuthAppIconFile(t, admin, host.URL, workspace, "icon.gif", []byte("GIF89a"), http.StatusBadRequest)

	// Another workspace's admin cannot adopt this workspace's icon, and no
	// application logo can point at an issue attachment.
	icon := uploadOAuthAppIconFile(t, admin, host.URL, workspace, "icon.svg", []byte(safeOAuthAppIconSVG), http.StatusCreated)
	attachment := uploadAuthAttachment(t, admin, host.URL, data.Issues[0].ID, workspace, http.StatusCreated)
	outsider, _ := verifiedAuthClient(t, host.URL, "Outside admin", "outside-icon@example.test")
	authRequest[domain.Bootstrap](t, outsider, http.MethodPost, host.URL+"/api/workspaces", map[string]string{"name": "Outside", "urlKey": "outside"}, "", http.StatusCreated)
	authRequest[map[string]any](t, outsider, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Borrowed icon", "logoUrl": icon, "publicEnabled": true}, "outside", 400)
	authRequest[map[string]any](t, outsider, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Attachment icon", "logoUrl": attachment.URL, "publicEnabled": true}, "outside", 400)
	authRequest[map[string]any](t, admin, "POST", host.URL+"/api/oauth-applications", map[string]any{"name": "Attachment icon", "logoUrl": attachment.URL, "publicEnabled": true}, workspace, 400)
	if got := fetchOAuthAppIcon(t, outsider, host.URL+icon); got.status != 404 {
		t.Errorf("other workspace read an unused icon: %d", got.status)
	}
	if got := fetchOAuthAppIcon(t, outsider, host.URL+attachment.URL); got.status != 404 {
		t.Errorf("other workspace read a private attachment: %d", got.status)
	}
}
