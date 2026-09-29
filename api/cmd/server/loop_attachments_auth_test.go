package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Loop prompt uploads with authentication on: only the uploader can see or use a
// pending upload; once it is on a loop, workspace members can open it and
// people outside the workspace still cannot.
func TestLoopAttachmentAccessWithAuthentication(t *testing.T) {
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	defer api.Close()

	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceSettings](t, admin, http.MethodPatch, api.URL+"/api/workspace/preferences", map[string]any{"featureFlags": map[string]bool{"loops": true}}, "test-workspace", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, api.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)

	register := func(name, email string) *http.Client {
		client := authClient(t)
		registered := authRequest[struct {
			VerificationToken string `json:"verificationToken"`
		}](t, client, http.MethodPost, api.URL+"/api/auth/register", map[string]string{"name": name, "email": email, "password": "initial-pass"}, "", http.StatusCreated)
		authRequest[any](t, client, http.MethodPost, api.URL+"/api/auth/verify-email", map[string]string{"token": registered.VerificationToken}, "", http.StatusOK)
		authRequest[domain.AuthSession](t, client, http.MethodPost, api.URL+"/api/auth/login", map[string]string{"email": email, "password": "initial-pass"}, "", http.StatusOK)
		return client
	}
	member := register("Member", "member@example.com")
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, api.URL+"/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"member@example.com"}, "role": "member", "teamIds": []string{bootstrap.Teams[0].ID}}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, member, http.MethodPost, api.URL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	outsider := register("Outsider", "outsider@example.com")

	upload := func(client *http.Client) domain.LoopAttachment {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, _ := form.CreateFormFile("file", "notes.txt")
		_, _ = part.Write([]byte("Weekly report format: themes first, then counts."))
		_ = form.Close()
		request, _ := http.NewRequest(http.MethodPost, api.URL+"/api/loops/attachments", &body)
		request.Header.Set("Content-Type", form.FormDataContentType())
		request.Header.Set("X-Workspace-Key", "test-workspace")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
			t.Fatalf("upload status %d: %s", response.StatusCode, raw)
		}
		var attachment domain.LoopAttachment
		if err := json.Unmarshal(raw, &attachment); err != nil || attachment.ID == "" || attachment.URL == "" {
			t.Fatalf("upload response %s", raw)
		}
		return attachment
	}
	status := func(client *http.Client, url string) int {
		t.Helper()
		response, err := client.Get(api.URL + url)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}

	attachment := upload(admin)
	if got := status(admin, attachment.URL); got != http.StatusOK {
		t.Fatalf("uploader cannot open pending upload: %d", got)
	}
	if got := status(member, attachment.URL); got == http.StatusOK {
		t.Fatal("another member opened someone else's pending upload")
	}
	if got := status(outsider, attachment.URL); got == http.StatusOK {
		t.Fatal("outsider opened a pending upload")
	}
	// Someone else's upload cannot be attached to your loop.
	authRequest[any](t, member, http.MethodPost, api.URL+"/api/loops", map[string]any{"prompt": "Weekly report", "attachmentIds": []string{attachment.ID}}, "test-workspace", http.StatusBadRequest)

	loop := authRequest[domain.Loop](t, admin, http.MethodPost, api.URL+"/api/loops", map[string]any{"prompt": "Weekly report", "attachmentIds": []string{attachment.ID}}, "test-workspace", http.StatusCreated)
	if len(loop.Attachments) != 1 || loop.Attachments[0].ID != attachment.ID {
		t.Fatalf("loop attachments = %#v", loop.Attachments)
	}
	if got := status(member, attachment.URL); got != http.StatusOK {
		t.Fatalf("workspace member cannot open a loop's attachment: %d", got)
	}
	if got := status(outsider, attachment.URL); got == http.StatusOK {
		t.Fatal("outsider opened a loop's attachment")
	}
	// An upload is used once.
	authRequest[any](t, admin, http.MethodPost, api.URL+"/api/loops", map[string]any{"prompt": "Again", "attachmentIds": []string{attachment.ID}}, "test-workspace", http.StatusBadRequest)
}
