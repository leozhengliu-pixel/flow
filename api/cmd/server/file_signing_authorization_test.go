package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Signed download links are issued only after authentication and file-read
// authorization, bind the signer, and are authorized again when they are used.
func TestSignedFileLinksFollowReadAuthorization(t *testing.T) {
	for _, mode := range []struct{ name, bounded, hydration string }{
		{"legacy reads, buffered signing", "off", "off"},
		{"legacy reads, streaming signing", "off", "on"},
		{"bounded reads, streaming signing", "on", "on"},
	} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_HYDRATION", mode.hydration)
			if mode.hydration == "on" {
				t.Setenv("FLOW_RECORD_JOURNAL", "on")
			}
			runSignedFileScenario(t, mode.bounded == "on")
		})
	}
}

func runSignedFileScenario(t *testing.T, bounded bool) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "signed-files.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	serving := repository
	if bounded {
		if serving, err = repository.BoundedMetadataViewForTesting(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	service := &server{store: serving, uploadPath: t.TempDir()}
	host := httptest.NewServer(newTestHandler(t, service))
	defer host.Close()
	const workspace = "test-workspace"
	url := func(path string) string { return host.URL + path }

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, url("/api/auth/login"), map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	join := func(name, email string) (*http.Client, domain.User) {
		client, user := verifiedAuthClient(t, host.URL, name, email)
		invitation := authRequest[[]domain.Invitation](t, admin, http.MethodPost, url("/api/workspaces/"+workspace+"/invitations"), map[string]any{"emails": []string{user.Email}, "role": "member"}, "", http.StatusCreated)
		authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, url("/api/invitations/accept"), map[string]string{"token": invitation[0].Token}, "", http.StatusOK)
		return client, user
	}
	insider, insiderUser := join("Insider", "signed-insider@example.com")
	outsider, outsiderUser := join("Outsider", "signed-outsider@example.com")
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, url("/api/bootstrap"), nil, workspace, http.StatusOK)
	publicTeam := bootstrap.Teams[0]
	private := authRequest[domain.Team](t, admin, http.MethodPost, url("/api/workspaces/"+workspace+"/teams"), map[string]any{"name": "Signed private", "key": "SPV", "private": true}, "", http.StatusCreated)
	authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+insiderUser.ID), map[string]any{"member": true}, "", http.StatusNoContent)
	authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+publicTeam.ID+"/members/"+outsiderUser.ID), map[string]any{"member": true}, "", http.StatusNoContent)

	// The insider attaches a file to an issue of the private team.
	issue := authRequest[domain.Issue](t, insider, http.MethodPost, url("/api/issue-records"), map[string]any{"title": "Private files", "teamId": private.ID}, workspace, http.StatusCreated)
	attachment := uploadIssueFile(t, insider, host.URL, workspace, issue.ID, "secret.txt", "private bytes")
	name := strings.TrimPrefix(attachment.URL, "/uploads/")

	fetch := func(client *http.Client, target string, headers map[string]string) (int, string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Workspace-Key", workspace)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(raw)
	}
	anonymous := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	expireIn := map[string]string{"X-Flow-File-Urls-Expire-In": "600"}
	signedIn := func(body string) string {
		t.Helper()
		body = strings.ReplaceAll(body, `\u0026`, "&")
		start := strings.Index(body, host.URL+"/api/files/signed/")
		if start < 0 {
			t.Fatalf("no signed URL in %.400s", body)
		}
		end := start
		for end < len(body) && !strings.ContainsRune(`"\ )<>`, rune(body[end])) {
			end++
		}
		return strings.ReplaceAll(body[start:end], `&`, "&")
	}

	// Legitimate: a reader of the record gets a working link from the header
	// rewrite and from the sign endpoint.
	code, body := fetch(insider, url("/api/issue-records/"+issue.ID), expireIn)
	if code != http.StatusOK || !strings.Contains(body, "signature=") {
		t.Fatalf("insider issue read = %d, links not signed: %.400s", code, body)
	}
	link := signedIn(body)
	if code, got := fetch(anonymous, link, nil); code != http.StatusOK || got != "private bytes" {
		t.Fatalf("legitimate signed download = %d %q", code, got)
	}
	explicit := authRequest[struct {
		Files []signedFile `json:"files"`
	}](t, insider, http.MethodGet, url("/api/files/sign?expiresIn=600&url="+neturl(attachment.URL)), nil, workspace, http.StatusOK)
	if code, got := fetch(anonymous, explicit.Files[0].SignedURL, nil); code != http.StatusOK || got != "private bytes" {
		t.Fatalf("sign endpoint download = %d %q", code, got)
	}

	// A name written into a record the caller can read is not a grant: the
	// outsider guesses the name, puts it in their own issue and asks for links.
	own := authRequest[domain.Issue](t, outsider, http.MethodPost, url("/api/issue-records"), map[string]any{"title": "Guess", "teamId": publicTeam.ID, "description": "see ![x](/uploads/" + name + ") and /uploads/" + name}, workspace, http.StatusCreated)
	code, body = fetch(outsider, url("/api/issue-records/"+own.ID), expireIn)
	if code != http.StatusOK || strings.Contains(body, "signature=") || !strings.Contains(body, "/uploads/"+name) {
		t.Fatalf("outsider read = %d, an unreadable name was signed or dropped: %.500s", code, body)
	}
	if code, _ := fetch(outsider, url("/uploads/"+name), nil); code != http.StatusNotFound {
		t.Fatalf("outsider direct download = %d", code)
	}
	authRequest[any](t, outsider, http.MethodGet, url("/api/files/sign?url="+neturl(attachment.URL)), nil, workspace, http.StatusNotFound)

	// Tampering with who the link was signed for, or expiry, ends it.
	for _, edit := range []func(string) string{
		func(link string) string { return strings.Replace(link, "sub=", "sub=x", 1) },
		func(link string) string { return strings.Replace(link, "&sub=", "&xsub=", 1) },
		func(link string) string { return strings.Replace(link, "signature=", "signature=0", 1) },
	} {
		if code, _ := fetch(anonymous, edit(link), nil); code != http.StatusForbidden && code != http.StatusNotFound {
			t.Fatalf("tampered link = %d", code)
		}
	}
	signingKey, err := service.fileSigningKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := fetch(anonymous, signedFileURLFor(signingKey, host.URL, name, time.Now().Add(-time.Minute), fileSigner{Subject: insiderUser.ID, Workspace: workspace}), nil); code != http.StatusForbidden {
		t.Fatalf("expired link = %d", code)
	}
	// A link that names no signer does not reach an attachment.
	if code, _ := fetch(anonymous, signedFileURL(signingKey, host.URL, name, time.Now().Add(time.Hour)), nil); code != http.StatusForbidden {
		t.Fatalf("link without a signer = %d", code)
	}

	// An API key limited to other teams cannot fetch or sign the file; one
	// that reaches the team can. Revoking the key ends links it signed.
	makeKey := func(client *http.Client, teams []string) (string, string) {
		t.Helper()
		input := map[string]any{"name": "Files key " + time.Now().Format("150405.000000"), "scopes": []string{"read"}}
		if teams != nil {
			input["teamIds"] = teams
		}
		created := authRequest[struct {
			Secret string `json:"secret"`
			Key    struct {
				ID string `json:"id"`
			} `json:"key"`
		}](t, client, http.MethodPost, url("/api/api-keys"), input, workspace, http.StatusCreated)
		return created.Secret, created.Key.ID
	}
	restricted, _ := makeKey(insider, []string{publicTeam.ID})
	bearer := func(secret string) map[string]string { return map[string]string{"Authorization": "Bearer " + secret} }
	if code, _ := fetch(anonymous, url("/uploads/"+name), bearer(restricted)); code != http.StatusNotFound {
		t.Fatalf("restricted key download = %d", code)
	}
	if code, _ := fetch(anonymous, url("/api/files/sign?url="+neturl(attachment.URL)), bearer(restricted)); code != http.StatusNotFound {
		t.Fatalf("restricted key sign = %d", code)
	}
	full, fullID := makeKey(insider, nil)
	if code, got := fetch(anonymous, url("/uploads/"+name), bearer(full)); code != http.StatusOK || got != "private bytes" {
		t.Fatalf("key download = %d %q", code, got)
	}
	signedByKey := struct {
		Files []signedFile `json:"files"`
	}{}
	code, body = fetch(anonymous, url("/api/files/sign?expiresIn=600&url="+neturl(attachment.URL)), bearer(full))
	if code != http.StatusOK || !strings.Contains(body, "signature=") {
		t.Fatalf("key sign = %d %.300s", code, body)
	}
	_ = signedByKey
	keyLink := signedIn(strings.ReplaceAll(body, `&`, "&"))
	if code, got := fetch(anonymous, keyLink, nil); code != http.StatusOK || got != "private bytes" {
		t.Fatalf("key signed download = %d %q", code, got)
	}
	authRequest[any](t, insider, http.MethodDelete, url("/api/api-keys/"+fullID), nil, workspace, http.StatusNoContent)
	if code, _ := fetch(anonymous, keyLink, nil); code != http.StatusNotFound {
		t.Fatalf("link of a revoked key = %d", code)
	}

	// Revocation: losing access to the team ends links the member holds.
	teamLink := link
	authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+insiderUser.ID), map[string]any{"member": false}, "", http.StatusNoContent)
	if code, _ := fetch(anonymous, teamLink, nil); code != http.StatusNotFound {
		t.Fatalf("link after the signer left the team = %d", code)
	}
	authRequest[any](t, admin, http.MethodPut, url("/api/workspaces/"+workspace+"/teams/"+private.ID+"/members/"+insiderUser.ID), map[string]any{"member": true}, "", http.StatusNoContent)
	if code, got := fetch(anonymous, teamLink, nil); code != http.StatusOK || got != "private bytes" {
		t.Fatalf("link after access returned = %d %q", code, got)
	}

	// Revocation: deleting the record ends the links.
	authRequest[any](t, insider, http.MethodDelete, url("/api/issues/"+issue.ID), nil, workspace, http.StatusNoContent)
	if code, _ := fetch(anonymous, link, nil); code != http.StatusNotFound {
		t.Fatalf("link after the issue was deleted = %d", code)
	}
	if code, _ := fetch(anonymous, explicit.Files[0].SignedURL, nil); code != http.StatusNotFound {
		t.Fatalf("sign endpoint link after the issue was deleted = %d", code)
	}
}

func neturl(value string) string { return url.QueryEscape(value) }

func uploadIssueFile(t *testing.T, client *http.Client, base, workspace, issueID, filename, content string) domain.Attachment {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, content)
	_ = writer.Close()
	request, err := http.NewRequest(http.MethodPost, base+"/api/issues/"+issueID+"/attachments", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("X-Workspace-Key", workspace)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d %s", response.StatusCode, raw)
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(raw, &attachment); err != nil || attachment.URL == "" {
		t.Fatalf("attachment = %s %v", raw, err)
	}
	return attachment
}

// The streaming signer decides per name exactly as the buffered one.
func TestSignedFileStreamingMatchesBufferedWithPermission(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	allowed := map[string]bool{"image.png": true, "test file.png": true, "chunk.png": true, "a.png": true, "a b": true}
	signing := &hydratedURLSigning{
		key: []byte("p15-test-key"), base: "https://flow.test", expires: time.Unix(1900000000, 0),
		signer: fileSigner{Subject: "user_1", Workspace: "w", KeyID: "key_1"},
		allow:  func(name string) bool { return allowed[name] },
	}
	bodies := []string{
		`{"url":"/uploads/image.png","denied":"/uploads/nope.png","text":"![x](/uploads/test%20file.png) /uploads/a.png?x=y /uploads/other.png","api":"/api/files/uploads/token"}`,
		`{"raw":"` + strings.Repeat("x", 32765) + `/uploads/chunk.png /uploads/hidden.png","long":"/uploads/` + strings.Repeat("long", 1<<18) + `"}`,
		`{"a":"https://assets.test/uploads/a%20b","b":"/uploads/bad%ZZ.png","c":"/uploads/image.png"}`,
	}
	for i, body := range bodies {
		source, err := newLegacySpool()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(source, body); err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if err := rewriteHydratedJSONSource(t.Context(), source, &got, signing); err != nil {
			t.Fatal(err)
		}
		removeLegacySpool(source)
		want := rewriteUploadURLsFor([]byte(body), signing)
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("fixture %d differs\n got %.600s\nwant %.600s", i, got.Bytes(), want)
		}
		if strings.Contains(string(want), "nope.png") && strings.Contains(string(want), "signed/nope.png") || strings.Contains(string(want), "signed/hidden.png") {
			t.Fatalf("fixture %d signed a refused name: %.600s", i, want)
		}
	}
}
