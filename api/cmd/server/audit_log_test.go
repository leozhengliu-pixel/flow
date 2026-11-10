package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type auditEventsFixture struct {
	t          *testing.T
	repository *store.SQLiteStore
	host       *httptest.Server
	admin      *http.Client
}

func newAuditEventsFixture(t *testing.T) auditEventsFixture {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	t.Cleanup(host.Close)
	return auditEventsFixture{t: t, repository: repository, host: host, admin: authClient(t)}
}

func (f auditEventsFixture) login(client *http.Client, email, password string, status int) {
	f.t.Helper()
	authRequest[any](f.t, client, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": email, "password": password}, "", status)
}

// find returns the newest entry of the workspace (or instance) log with the
// resource type and action.
func findAudit(entries []domain.AuditLogEntry, resourceType, action string) (domain.AuditLogEntry, bool) {
	index := slices.IndexFunc(entries, func(entry domain.AuditLogEntry) bool {
		return entry.ResourceType == resourceType && entry.Action == action
	})
	if index < 0 {
		return domain.AuditLogEntry{}, false
	}
	return entries[index], true
}

// Sign-in (success and failure, known and unknown accounts), sign-out,
// password changes, session revocation, API key create/rotate/revoke, admin
// role changes and export downloads are audited with the client address
// (truncated) and the credential that acted; only admins read the log.
func TestAuditLogRecordsAccountAndCredentialEvents(t *testing.T) {
	f := newAuditEventsFixture(t)
	ctx := context.Background()
	f.login(f.admin, "admin@example.test", "wrong-password", http.StatusUnauthorized)
	f.login(authClient(t), "nobody@example.test", "wrong-password", http.StatusUnauthorized)
	f.login(f.admin, "admin@example.test", "test-password", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, f.admin, http.MethodGet, f.host.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	admin := bootstrap.Viewer
	member := bootstrap.Users[slices.IndexFunc(bootstrap.Users, func(user domain.User) bool { return user.Email == "member@example.test" })]

	key := authRequest[struct {
		Key    domain.APIKey `json:"key"`
		Secret string        `json:"secret"`
	}](t, f.admin, http.MethodPost, f.host.URL+"/api/api-keys", map[string]any{"name": "Audit key", "scopes": []string{"read"}}, "test-workspace", http.StatusCreated)
	authRequest[any](t, f.admin, http.MethodPost, f.host.URL+"/api/api-keys/"+key.Key.ID+"/rotate-secret", nil, "test-workspace", http.StatusOK)
	authRequest[any](t, f.admin, http.MethodDelete, f.host.URL+"/api/api-keys/"+key.Key.ID, nil, "test-workspace", http.StatusNoContent)

	role, _, err := f.repository.WorkspaceRole(ctx, bootstrap.Workspace.ID, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	nextRole := "member"
	if role == "member" {
		nextRole = "admin"
	}
	authRequest[any](t, f.admin, http.MethodPatch, f.host.URL+"/api/workspaces/test-workspace/members/"+member.ID, map[string]any{"role": nextRole}, "test-workspace", http.StatusOK)

	// A wrong current password is refused (the status it answers with is not
	// what this test checks).
	wrong, _ := http.NewRequest(http.MethodPost, f.host.URL+"/api/account/change-password", strings.NewReader(`{"currentPassword":"not-it","newPassword":"another-pass"}`))
	wrong.Header.Set("Content-Type", "application/json")
	wrong.Header.Set("X-Workspace-Key", "test-workspace")
	if refused, err := f.admin.Do(wrong); err != nil || refused.StatusCode < 400 {
		t.Fatalf("wrong current password accepted: %v", err)
	} else {
		refused.Body.Close()
	}
	authRequest[any](t, f.admin, http.MethodPost, f.host.URL+"/api/account/change-password", map[string]string{"currentPassword": "test-password", "newPassword": "another-pass"}, "test-workspace", http.StatusOK)
	authRequest[any](t, f.admin, http.MethodDelete, f.host.URL+"/api/account/sessions/others", nil, "test-workspace", http.StatusNoContent)

	if err := f.repository.MutateWorkspace(ctx, "test-workspace", "test.export_ready", "export_audit", nil, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		data.ExportJobs = append(data.ExportJobs, domain.ExportJob{ID: "export_audit", UserID: admin.ID, Format: "csv", Status: "completed", Filename: "issues.csv", CreatedAt: now, CompletedAt: &now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, f.host.URL+"/api/exports/export_audit/download", nil)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	response, err := f.admin.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export download status %d", response.StatusCode)
	}

	entries := auditLogOf(t, f.repository, "test-workspace")
	failed, ok := findAudit(entries, "account", "sign_in_failed")
	if !ok || failed.Result != store.AuditResultFailure || failed.Actor.ID != admin.ID || failed.Metadata["reason"] != "invalid_credentials" || failed.IP != "127.0.0.0" {
		t.Fatalf("failed sign-in = %+v", failed)
	}
	signedIn, ok := findAudit(entries, "account", "signed_in")
	if !ok || signedIn.Result != store.AuditResultSuccess || signedIn.Metadata["method"] != "password" || signedIn.ActorType != store.AuditActorUser || signedIn.UserAgent == "" {
		t.Fatalf("sign-in = %+v", signedIn)
	}
	for _, want := range [][2]string{{"api_key", "created"}, {"api_key", "secret_rotated"}, {"api_key", "revoked"}, {"workspace_member", "role_changed"}, {"account", "sessions_revoked"}, {"export", "downloaded"}} {
		entry, ok := findAudit(entries, want[0], want[1])
		if !ok || entry.Actor.ID != admin.ID {
			t.Fatalf("%s.%s not audited: %+v", want[0], want[1], entry)
		}
	}
	if created, _ := findAudit(entries, "api_key", "created"); created.ResourceID != key.Key.ID || created.Metadata["name"] != "Audit key" || strings.Contains(strings.ToLower(created.Metadata["prefix"].(string)), key.Secret) {
		t.Fatalf("api key entry = %+v", created)
	}
	if changed, _ := findAudit(entries, "workspace_member", "role_changed"); changed.ResourceID != member.ID || changed.Metadata["roleBefore"] != role || changed.Metadata["roleAfter"] != nextRole {
		t.Fatalf("role change = %+v", changed)
	}
	passwordChanges := 0
	for _, entry := range entries {
		if entry.Action == "password_changed" {
			passwordChanges++
			if entry.Metadata["password"] != nil || entry.Metadata["newPassword"] != nil {
				t.Fatalf("password entry keeps a secret: %+v", entry)
			}
		}
	}
	if passwordChanges != 2 {
		t.Fatalf("password changes audited = %d", passwordChanges)
	}
	instance, err := f.repository.ListAuditEntries(ctx, store.InstanceAuditWorkspace, store.AuditQuery{})
	if err != nil || len(instance) != 1 || instance[0].Metadata["email"] != "nobody@example.test" || instance[0].Result != store.AuditResultFailure {
		t.Fatalf("instance log = %+v, %v", instance, err)
	}

	// Sign-out, then: a member is refused the log; the admin reads it, its
	// types, a CSV export and the chain check.
	authRequest[any](t, f.admin, http.MethodPost, f.host.URL+"/api/auth/logout", nil, "", http.StatusNoContent)
	if _, ok := findAudit(auditLogOf(t, f.repository, "test-workspace"), "account", "signed_out"); !ok {
		t.Fatal("sign-out not audited")
	}
	memberClient := authClient(t)
	f.login(memberClient, "member@example.test", "test-password", http.StatusOK)
	if role, _, _ := f.repository.WorkspaceRole(ctx, bootstrap.Workspace.ID, member.ID); role == "admin" || role == "owner" {
		authRequest[any](t, f.admin, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "another-pass"}, "", http.StatusOK)
		authRequest[any](t, f.admin, http.MethodPatch, f.host.URL+"/api/workspaces/test-workspace/members/"+member.ID, map[string]any{"role": "member"}, "test-workspace", http.StatusOK)
	}
	for _, path := range []string{"/api/workspace/audit-log", "/api/workspace/audit-log/types", "/api/workspace/audit-log/verify", "/api/workspace/audit-log?format=csv"} {
		authRequest[any](t, memberClient, http.MethodGet, f.host.URL+path, nil, "test-workspace", http.StatusForbidden)
	}
	authRequest[any](t, f.admin, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "another-pass"}, "", http.StatusOK)
	listed := authRequest[[]domain.AuditLogEntry](t, f.admin, http.MethodGet, f.host.URL+"/api/workspace/audit-log?type=api_key.created", nil, "test-workspace", http.StatusOK)
	if len(listed) != 1 || listed[0].ResourceID != key.Key.ID || listed[0].Seq == 0 {
		t.Fatalf("filtered list = %+v", listed)
	}
	page := authRequest[struct {
		Nodes    []domain.AuditLogEntry `json:"nodes"`
		PageInfo struct {
			HasNextPage bool `json:"hasNextPage"`
		} `json:"pageInfo"`
	}](t, f.admin, http.MethodGet, f.host.URL+"/api/workspace/audit-log?first=2&resourceType=account", nil, "test-workspace", http.StatusOK)
	if len(page.Nodes) != 2 || !page.PageInfo.HasNextPage || page.Nodes[0].ResourceType != "account" {
		t.Fatalf("connection = %+v", page)
	}
	report := authRequest[domain.AuditChainReport](t, f.admin, http.MethodGet, f.host.URL+"/api/workspace/audit-log/verify", nil, "test-workspace", http.StatusOK)
	if !report.Valid || report.Entries < 10 {
		t.Fatalf("verify = %+v", report)
	}
	csvRequest, _ := http.NewRequest(http.MethodGet, f.host.URL+"/api/workspace/audit-log?format=csv&resourceType=api_key", nil)
	csvRequest.Header.Set("X-Workspace-Key", "test-workspace")
	csvResponse, err := f.admin.Do(csvRequest)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(csvResponse.Body)
	csvResponse.Body.Close()
	if !strings.HasPrefix(csvResponse.Header.Get("Content-Type"), "text/csv") || !strings.HasPrefix(string(body), "id,seq,created_at,actor_type") || strings.Count(string(body), "\n") != 4 {
		t.Fatalf("csv = %s %q", csvResponse.Header.Get("Content-Type"), body)
	}
}

// Granting and revoking a third-party OAuth application are audited.
func TestAuditLogRecordsOAuthGrantsAndRevocations(t *testing.T) {
	f := newAuditEventsFixture(t)
	f.login(f.admin, "admin@example.test", "test-password", http.StatusOK)
	registered := authRequest[domain.OAuthClient](t, f.admin, http.MethodPost, f.host.URL+"/oauth/register", map[string]any{
		"client_name": "Audit client", "redirect_uris": []string{"http://127.0.0.1:43119/callback"}, "token_endpoint_auth_method": "none",
	}, "", http.StatusCreated)
	verifier := strings.Repeat("v", 48)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	authRequest[map[string]string](t, f.admin, http.MethodPost, f.host.URL+"/api/oauth/authorization-request", map[string]any{
		"clientId": registered.ClientID, "redirectUri": registered.RedirectURIs[0], "responseType": "code", "scope": "read", "state": "s", "codeChallenge": challenge, "codeChallengeMethod": "S256", "workspaceKey": "test-workspace", "approve": true,
	}, "", http.StatusOK)
	granted, ok := findAudit(auditLogOf(t, f.repository, "test-workspace"), "oauth_authorization", "authorized")
	if !ok || granted.Metadata["clientId"] != registered.ClientID || granted.Metadata["clientName"] != "Audit client" {
		t.Fatalf("grant = %+v", granted)
	}
	authRequest[any](t, f.admin, http.MethodDelete, f.host.URL+"/api/oauth/authorizations/"+url.PathEscape(granted.ResourceID), nil, "test-workspace", http.StatusNoContent)
	revoked, ok := findAudit(auditLogOf(t, f.repository, "test-workspace"), "oauth_authorization", "revoked")
	if !ok || revoked.ResourceID != granted.ResourceID || revoked.Metadata["clientId"] != registered.ClientID {
		t.Fatalf("revocation = %+v", revoked)
	}
}

// Each write call an agent makes with Flow's tools is audited (a call that
// never ran is not); a failed one records the failure.
func TestAgentWritesAreAudited(t *testing.T) {
	f := newAuditEventsFixture(t)
	s := &server{store: f.repository}
	data, _ := f.repository.WorkspaceMetadata("test-workspace")
	ledger := &agentWriteLedger{audit: s.agentWriteAudit(context.Background(), "test-workspace", data.Viewer, "agent_session", "session_1")}
	ledger.record(context.Background(), domain.AgentToolCall{Name: "save_issue", Arguments: []byte(`{"id":"ENG-1","title":"x"}`), Result: []byte(`{}`)}, agentWriteSucceeded, "")
	ledger.record(context.Background(), domain.AgentToolCall{Name: "save_issue", Arguments: []byte(`{"id":"ENG-2"}`)}, agentWriteFailed, "permission denied")
	ledger.record(context.Background(), domain.AgentToolCall{Name: "save_issue", Arguments: []byte(`{"id":"ENG-3"}`)}, agentWriteNotRun, "declined")
	ledger.record(context.Background(), domain.AgentToolCall{Name: "list_issues", Arguments: []byte(`{}`)}, agentWriteSucceeded, "")
	entries := auditLogOf(t, f.repository, "test-workspace")
	writes := []domain.AuditLogEntry{}
	for _, entry := range entries {
		if entry.Action == "tool_write" {
			writes = append(writes, entry)
		}
	}
	if len(writes) != 2 {
		t.Fatalf("agent writes audited = %+v", writes)
	}
	failed, succeeded := writes[0], writes[1]
	if succeeded.ActorType != store.AuditActorAgent || succeeded.ResourceID != "session_1" || succeeded.Metadata["tool"] != "save_issue" || succeeded.Metadata["target"] != "ENG-1" || succeeded.Result != store.AuditResultSuccess || succeeded.Actor.ID != data.Viewer.ID {
		t.Fatalf("succeeded write = %+v", succeeded)
	}
	if failed.Result != store.AuditResultFailure || failed.Metadata["reason"] != "permission denied" {
		t.Fatalf("failed write = %+v", failed)
	}
}

func TestTruncatedAuditIP(t *testing.T) {
	for raw, want := range map[string]string{"203.0.113.77": "203.0.113.0", "::ffff:198.51.100.9": "198.51.100.0", "2001:db8:1234:5678::1": "2001:db8:1234::", "not-an-ip": ""} {
		if got := truncatedAuditIP(raw); got != want {
			t.Errorf("truncatedAuditIP(%q) = %q, want %q", raw, got, want)
		}
	}
}
