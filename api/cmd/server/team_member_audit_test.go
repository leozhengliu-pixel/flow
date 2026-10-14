package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type teamMemberAuditFixture struct {
	server     *httptest.Server
	repository *store.SQLiteStore
	admin      *http.Client
	bootstrap  domain.Bootstrap
	teamID     string
}

func newTeamMemberAuditFixture(t *testing.T) *teamMemberAuditFixture {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "team-member-audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	host := httptest.NewServer(newHandler(&server{store: repository, uploadPath: t.TempDir()}))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, http.MethodPost, host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	bootstrap := authRequest[domain.Bootstrap](t, admin, http.MethodGet, host.URL+"/api/bootstrap", nil, "test-workspace", http.StatusOK)
	return &teamMemberAuditFixture{server: host, repository: repository, admin: admin, bootstrap: bootstrap, teamID: bootstrap.Teams[0].ID}
}

// auditFor returns the team_member audit entries about userID, newest first,
// through the audit log API (the same data the audit log page and stream use).
func (f *teamMemberAuditFixture) auditFor(t *testing.T, userID string) []domain.AuditLogEntry {
	t.Helper()
	return authRequest[[]domain.AuditLogEntry](t, f.admin, http.MethodGet, f.server.URL+"/api/workspace/audit-log?resourceType=team_member&resourceId="+userID, nil, "test-workspace", http.StatusOK)
}

func (f *teamMemberAuditFixture) apiKey(t *testing.T, name string) string {
	t.Helper()
	key := authRequest[struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}](t, f.admin, http.MethodPost, f.server.URL+"/api/api-keys", map[string]any{"name": name, "scopes": []string{"read", "write"}}, "test-workspace", http.StatusCreated)
	if key.Secret == "" {
		t.Fatal("API key secret missing")
	}
	return key.Secret
}

func bearerJSON(t *testing.T, method, url, secret string, input any, wantStatus int) []byte {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("X-Workspace-Key", "test-workspace")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status %d, want %d: %s", method, url, response.StatusCode, wantStatus, raw)
	}
	return raw
}

func decodeJSONBody[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return value
}

func requireAudit(t *testing.T, entry domain.AuditLogEntry, action string, want map[string]any) {
	t.Helper()
	if entry.Action != action || entry.ResourceType != "team_member" {
		t.Fatalf("audit entry %s %s/%s, want %s team_member: %+v", entry.ID, entry.Action, entry.ResourceType, action, entry)
	}
	for key, value := range want {
		if entry.Metadata[key] != value {
			t.Fatalf("audit entry %s metadata[%s] = %#v, want %#v (metadata %v)", entry.ID, key, entry.Metadata[key], value, entry.Metadata)
		}
	}
}

func TestTeamMemberChangesAreAuditedOnEveryPath(t *testing.T) {
	f := newTeamMemberAuditFixture(t)
	membershipURL := func(teamID, userID string) string {
		return f.server.URL + "/api/workspaces/test-workspace/teams/" + teamID + "/members/" + userID
	}

	// Invitation naming the team: origin and source invitation.
	invited := inviteTeamPermissionActor(t, f.server.URL, f.admin, "Invited", "invited@example.test", "member", []string{f.teamID})
	entries := f.auditFor(t, invited.user.ID)
	if len(entries) != 1 {
		t.Fatalf("invitation audit entries = %+v", entries)
	}
	requireAudit(t, entries[0], "added", map[string]any{"teamId": f.teamID, "origin": "invitation", "source": "invitation", "reason": "invitation_accepted", "roleAfter": "member", "userEmail": "invited@example.test"})
	if entries[0].Actor.ID != invited.user.ID || entries[0].Metadata["invitationId"] == nil || entries[0].Metadata["inviterId"] != f.bootstrap.Viewer.ID {
		t.Fatalf("invitation audit actor/metadata = %+v", entries[0])
	}

	// Team settings (session): add, role change, remove — origin manual, source manual.
	person := inviteTeamPermissionActor(t, f.server.URL, f.admin, "Person", "person@example.test", "member", nil)
	authRequest[any](t, f.admin, http.MethodPut, membershipURL(f.teamID, person.user.ID), map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	authRequest[any](t, f.admin, http.MethodPut, membershipURL(f.teamID, person.user.ID), map[string]any{"member": true, "role": "owner"}, "", http.StatusNoContent)
	authRequest[any](t, f.admin, http.MethodPut, membershipURL(f.teamID, person.user.ID), map[string]any{"member": false, "role": "member"}, "", http.StatusNoContent)
	entries = f.auditFor(t, person.user.ID)
	if len(entries) != 3 {
		t.Fatalf("team settings audit entries = %+v", entries)
	}
	requireAudit(t, entries[2], "added", map[string]any{"origin": "manual", "source": "manual", "roleAfter": "member", "userName": "Person"})
	requireAudit(t, entries[1], "role_changed", map[string]any{"roleBefore": "member", "roleAfter": "owner", "source": "manual"})
	requireAudit(t, entries[0], "removed", map[string]any{"roleBefore": "owner", "origin": "manual", "source": "manual"})
	if entries[0].Actor.ID != f.bootstrap.Viewer.ID || entries[0].Metadata["teamKey"] != f.bootstrap.Teams[0].Key {
		t.Fatalf("removal actor/team = %+v", entries[0])
	}

	// Joining and leaving a team yourself is marked self-service. Leaving your
	// last active team is refused (the reference app: "You should be a member of at least
	// one active team") and writes no audit entry.
	authRequest[any](t, person.client, http.MethodPut, membershipURL(f.teamID, person.user.ID), map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	if code, message := teamMembershipRequest(t, person.client, membershipURL(f.teamID, person.user.ID), false, "member"); code != http.StatusConflict || message != errLastActiveTeam.Error() {
		t.Fatalf("leaving the last active team = %d %q", code, message)
	}
	if entries = f.auditFor(t, person.user.ID); entries[0].Action != "added" {
		t.Fatalf("refused leave was audited: %+v", entries[0])
	}
	second := authRequest[domain.Team](t, f.admin, http.MethodPost, f.server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Second", "key": "SEC"}, "", http.StatusCreated)
	authRequest[any](t, f.admin, http.MethodPut, membershipURL(second.ID, person.user.ID), map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	authRequest[any](t, person.client, http.MethodPut, membershipURL(f.teamID, person.user.ID), map[string]any{"member": false, "role": "member"}, "", http.StatusNoContent)
	entries = f.auditFor(t, person.user.ID)
	requireAudit(t, entries[2], "added", map[string]any{"selfService": true, "source": "manual"})
	requireAudit(t, entries[0], "removed", map[string]any{"selfService": true})

	// Creating a team makes the creator its owner: audited.
	created := authRequest[domain.Team](t, f.admin, http.MethodPost, f.server.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "Audited", "key": "AUD"}, "", http.StatusCreated)
	entries = f.auditFor(t, f.bootstrap.Viewer.ID)
	requireAudit(t, entries[0], "added", map[string]any{"teamId": created.ID, "roleAfter": "owner", "reason": "team_created", "origin": "manual"})

	// An API key adds with origin api unless it declares sync; the key is named.
	secret := f.apiKey(t, "HR sync")
	worker := inviteTeamPermissionActor(t, f.server.URL, f.admin, "Worker", "worker@example.test", "member", nil)
	member := decodeJSONBody[domain.TeamMember](t, bearerJSON(t, http.MethodPut, f.server.URL+"/api/teams/"+f.teamID+"/members/"+worker.user.ID, secret, map[string]any{"role": "member"}, http.StatusOK))
	if member.Origin != "api" {
		t.Fatalf("API key membership origin = %q", member.Origin)
	}
	entries = f.auditFor(t, worker.user.ID)
	requireAudit(t, entries[0], "added", map[string]any{"origin": "api", "source": "api", "apiKeyName": "HR sync"})
	if entries[0].Metadata["apiKeyId"] == nil {
		t.Fatalf("API key id missing: %v", entries[0].Metadata)
	}

	// Removing a workspace member audits each team membership it removes.
	authRequest[any](t, f.admin, http.MethodPut, membershipURL(created.ID, worker.user.ID), map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	authRequest[any](t, f.admin, http.MethodDelete, f.server.URL+"/api/workspaces/test-workspace/members/"+worker.user.ID, nil, "", http.StatusNoContent)
	entries = f.auditFor(t, worker.user.ID)
	removedTeams := []string{}
	for _, entry := range entries {
		if entry.Action == "removed" {
			requireAudit(t, entry, "removed", map[string]any{"reason": "workspace_member_removed", "source": "manual"})
			removedTeams = append(removedTeams, entry.Metadata["teamId"].(string))
		}
	}
	slices.Sort(removedTeams)
	wantTeams := []string{f.teamID, created.ID}
	slices.Sort(wantTeams)
	if !slices.Equal(removedTeams, wantTeams) {
		t.Fatalf("workspace removal audited teams %v, want %v", removedTeams, wantTeams)
	}

	// The audit stream delivers whatever is in the log; the entries have
	// distinct ids so its cursor orders them.
	seen := map[string]bool{}
	for _, entry := range authRequest[[]domain.AuditLogEntry](t, f.admin, http.MethodGet, f.server.URL+"/api/workspace/audit-log?resourceType=team_member", nil, "test-workspace", http.StatusOK) {
		if seen[entry.ID] {
			t.Fatalf("duplicate audit id %s", entry.ID)
		}
		seen[entry.ID] = true
	}
	types := authRequest[[]auditEntryTypeCount](t, f.admin, http.MethodGet, f.server.URL+"/api/workspace/audit-log/types", nil, "test-workspace", http.StatusOK)
	if !slices.ContainsFunc(types, func(item auditEntryTypeCount) bool { return item.Type == "team_member.removed" && item.Count > 0 }) {
		t.Fatalf("audit types = %+v", types)
	}
}

func TestTeamMemberRemovalAPIHonorsOriginAndDryRun(t *testing.T) {
	f := newTeamMemberAuditFixture(t)
	secret := f.apiKey(t, "Directory sync")
	synced := inviteTeamPermissionActor(t, f.server.URL, f.admin, "Synced", "synced@example.test", "member", nil)
	manual := inviteTeamPermissionActor(t, f.server.URL, f.admin, "Manual", "manual@example.test", "member", nil)
	teamURL := f.server.URL + "/api/teams/" + f.teamID + "/members"
	bearerJSON(t, http.MethodPut, teamURL+"/"+synced.user.ID, secret, map[string]any{"origin": "sync"}, http.StatusOK)
	authRequest[any](t, f.admin, http.MethodPut, f.server.URL+"/api/workspaces/test-workspace/teams/"+f.teamID+"/members/"+manual.user.ID, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)

	listed := decodeJSONBody[[]domain.TeamMember](t, bearerJSON(t, http.MethodGet, teamURL, secret, nil, http.StatusOK))
	origins := map[string]string{}
	for _, member := range listed {
		origins[member.UserID] = member.Origin
	}
	if origins[synced.user.ID] != "sync" || origins[manual.user.ID] != "manual" || origins[f.bootstrap.Viewer.ID] != "manual" {
		t.Fatalf("listed origins = %v", origins)
	}
	entries := f.auditFor(t, synced.user.ID)
	requireAudit(t, entries[0], "added", map[string]any{"origin": "sync", "source": "sync", "authenticatedAs": "api"})

	// Dry run: lists the removal, writes nothing (membership and audit unchanged).
	before := len(f.auditFor(t, synced.user.ID))
	dry := decodeJSONBody[domain.TeamMemberRemoval](t, bearerJSON(t, http.MethodDelete, teamURL+"/"+synced.user.ID+"?origin=sync&dryRun=true", secret, nil, http.StatusOK))
	if !dry.DryRun || len(dry.Removed) != 1 || dry.Removed[0].UserID != synced.user.ID || dry.Removed[0].Origin != "sync" || dry.Removed[0].UserName != "Synced" || len(dry.Kept) != 0 {
		t.Fatalf("dry run = %+v", dry)
	}
	if _, found, _ := f.repository.TeamMembership(t.Context(), f.bootstrap.Workspace.ID, f.teamID, synced.user.ID); !found {
		t.Fatal("dry run removed the member")
	}
	if after := len(f.auditFor(t, synced.user.ID)); after != before {
		t.Fatalf("dry run wrote audit entries: %d -> %d", before, after)
	}

	// A sync-scoped removal of a manual member is refused with its real origin.
	conflict := decodeJSONBody[map[string]any](t, bearerJSON(t, http.MethodDelete, teamURL+"/"+manual.user.ID+"?origin=sync", secret, nil, http.StatusConflict))
	if conflict["code"] != "TEAM_MEMBER_ORIGIN_MISMATCH" || conflict["origin"] != "manual" {
		t.Fatalf("origin mismatch response = %v", conflict)
	}
	dryKept := decodeJSONBody[domain.TeamMemberRemoval](t, bearerJSON(t, http.MethodDelete, teamURL+"/"+manual.user.ID+"?origin=sync&dryRun=true", secret, nil, http.StatusOK))
	if len(dryKept.Removed) != 0 || len(dryKept.Kept) != 1 || dryKept.Kept[0].Reason != "origin_mismatch" || dryKept.Kept[0].Origin != "manual" {
		t.Fatalf("dry-run kept = %+v", dryKept)
	}
	// Owners are never removed by origin-scoped removals.
	ownerConflict := decodeJSONBody[map[string]any](t, bearerJSON(t, http.MethodDelete, teamURL+"/"+f.bootstrap.Viewer.ID+"?origin=manual", secret, nil, http.StatusConflict))
	if ownerConflict["code"] != "TEAM_OWNER_PROTECTED" {
		t.Fatalf("owner protection response = %v", ownerConflict)
	}
	bearerJSON(t, http.MethodDelete, teamURL+"/"+manual.user.ID+"?origin=bogus", secret, nil, http.StatusBadRequest)

	removed := decodeJSONBody[domain.TeamMemberRemoval](t, bearerJSON(t, http.MethodDelete, teamURL+"/"+synced.user.ID+"?origin=sync", secret, nil, http.StatusOK))
	if removed.DryRun || len(removed.Removed) != 1 {
		t.Fatalf("removal = %+v", removed)
	}
	entries = f.auditFor(t, synced.user.ID)
	requireAudit(t, entries[0], "removed", map[string]any{"origin": "sync", "source": "sync", "apiKeyName": "Directory sync"})
	if _, found, _ := f.repository.TeamMembership(t.Context(), f.bootstrap.Workspace.ID, f.teamID, manual.user.ID); !found {
		t.Fatal("manual member was removed")
	}
	// Without an origin the removal is explicit and removes any member.
	bearerJSON(t, http.MethodDelete, teamURL+"/"+manual.user.ID, secret, nil, http.StatusOK)
	requireAudit(t, f.auditFor(t, manual.user.ID)[0], "removed", map[string]any{"origin": "manual", "source": "api"})
}

func TestTeamMemberSyncEndpointNeverRemovesManualMembersByDefault(t *testing.T) {
	f := newTeamMemberAuditFixture(t)
	secret := f.apiKey(t, "HR_ORG")
	stale := inviteTeamPermissionActor(t, f.server.URL, f.admin, "Stale", "stale@example.test", "member", nil)
	manual := inviteTeamPermissionActor(t, f.server.URL, f.admin, "Hand added", "hand-added@example.test", "member", nil)
	hired := inviteTeamPermissionActor(t, f.server.URL, f.admin, "Hired", "hired@example.test", "member", nil)
	teamURL := f.server.URL + "/api/teams/" + f.teamID + "/members"
	bearerJSON(t, http.MethodPut, teamURL+"/"+stale.user.ID, secret, map[string]any{"origin": "sync"}, http.StatusOK)
	authRequest[any](t, f.admin, http.MethodPut, f.server.URL+"/api/workspaces/test-workspace/teams/"+f.teamID+"/members/"+manual.user.ID, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)

	input := map[string]any{"members": []string{"HIRED@example.test", "nobody@example.test"}, "dryRun": true}
	dry := decodeJSONBody[domain.TeamMemberSyncResult](t, bearerJSON(t, http.MethodPost, teamURL+"/sync", secret, input, http.StatusOK))
	if !dry.DryRun || len(dry.Added) != 1 || dry.Added[0].UserID != hired.user.ID || dry.Added[0].Origin != "sync" {
		t.Fatalf("dry-run added = %+v", dry.Added)
	}
	if len(dry.Removed) != 1 || dry.Removed[0].UserID != stale.user.ID {
		t.Fatalf("dry-run removed = %+v", dry.Removed)
	}
	kept := map[string]string{}
	for _, item := range dry.Kept {
		kept[item.UserID] = item.Reason
	}
	if kept[manual.user.ID] != "origin_mismatch" || kept[f.bootstrap.Viewer.ID] != "origin_mismatch" && kept[f.bootstrap.Viewer.ID] != "owner" {
		t.Fatalf("dry-run kept = %+v", dry.Kept)
	}
	if len(dry.Skipped) != 1 || dry.Skipped[0].Reason != "unknown_user" {
		t.Fatalf("dry-run skipped = %+v", dry.Skipped)
	}
	if _, found, _ := f.repository.TeamMembership(t.Context(), f.bootstrap.Workspace.ID, f.teamID, hired.user.ID); found {
		t.Fatal("dry run added a member")
	}
	if _, found, _ := f.repository.TeamMembership(t.Context(), f.bootstrap.Workspace.ID, f.teamID, stale.user.ID); !found {
		t.Fatal("dry run removed a member")
	}
	if entries := f.auditFor(t, hired.user.ID); len(entries) != 0 {
		t.Fatalf("dry run audited: %+v", entries)
	}

	input["dryRun"] = false
	applied := decodeJSONBody[domain.TeamMemberSyncResult](t, bearerJSON(t, http.MethodPost, teamURL+"/sync", secret, input, http.StatusOK))
	if len(applied.Added) != 1 || len(applied.Removed) != 1 {
		t.Fatalf("sync result = %+v", applied)
	}
	if _, found, _ := f.repository.TeamMembership(t.Context(), f.bootstrap.Workspace.ID, f.teamID, manual.user.ID); !found {
		t.Fatal("sync removed a manually added member")
	}
	if _, found, _ := f.repository.TeamMembership(t.Context(), f.bootstrap.Workspace.ID, f.teamID, f.bootstrap.Viewer.ID); !found {
		t.Fatal("sync removed the team owner")
	}
	requireAudit(t, f.auditFor(t, stale.user.ID)[0], "removed", map[string]any{"origin": "sync", "source": "sync", "reason": "team_member_sync"})
	requireAudit(t, f.auditFor(t, hired.user.ID)[0], "added", map[string]any{"origin": "sync", "source": "sync", "reason": "team_member_sync"})

	// Manual members are removed only when the caller says so explicitly.
	explicit := decodeJSONBody[domain.TeamMemberSyncResult](t, bearerJSON(t, http.MethodPost, teamURL+"/sync", secret, map[string]any{"members": []string{hired.user.ID}, "removeOrigins": []string{"sync", "manual"}, "dryRun": true}, http.StatusOK))
	if !slices.ContainsFunc(explicit.Removed, func(item domain.TeamMemberChange) bool { return item.UserID == manual.user.ID }) {
		t.Fatalf("explicit manual removal dry run = %+v", explicit)
	}
	if !slices.ContainsFunc(explicit.Kept, func(item domain.TeamMemberKept) bool {
		return item.UserID == f.bootstrap.Viewer.ID && item.Reason == "owner"
	}) {
		t.Fatalf("owner must be kept: %+v", explicit.Kept)
	}
	bearerJSON(t, http.MethodPost, teamURL+"/sync", secret, map[string]any{"members": []string{}, "origin": "scim"}, http.StatusBadRequest)
	bearerJSON(t, http.MethodPost, teamURL+"/sync", secret, map[string]any{"origin": "sync"}, http.StatusBadRequest)
}

func TestSCIMTeamMembershipChangesAreAuditedWithTheConnection(t *testing.T) {
	handler, repository := enterpriseTestServer(t)
	seed, _ := repository.BootstrapFor("test-workspace")
	teamID := seed.Teams[0].ID
	if err := repository.MutateWorkspace(t.Context(), "test-workspace", "scim.test.configured", "workspace", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.SCIMTeamGroupMapping = map[string]string{teamID: "engineering"}
		data.WorkspaceSettings.SCIMDefaultRole = "member"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	token := requestJSON[store.SCIMToken](t, handler, http.MethodPost, "/api/scim/tokens?workspace=test-workspace", map[string]any{"name": "Okta"}, http.StatusCreated)
	var created map[string]any
	if err := json.Unmarshal(scimRequest(t, handler, http.MethodPost, "/scim/v2/test-workspace/Users", token.Secret, map[string]any{"externalId": "e-1", "userName": "e1", "displayName": "Provisioned"}, http.StatusCreated), &created); err != nil {
		t.Fatal(err)
	}
	userID := created["id"].(string)
	var group map[string]any
	if err := json.Unmarshal(scimRequest(t, handler, http.MethodPost, "/scim/v2/test-workspace/Groups", token.Secret, map[string]any{"externalId": "idp-eng", "displayName": "engineering", "members": []map[string]string{{"value": "e-1"}}}, http.StatusCreated), &group); err != nil {
		t.Fatal(err)
	}
	scimRequest(t, handler, http.MethodPatch, "/scim/v2/test-workspace/Groups/"+group["id"].(string), token.Secret, map[string]any{"displayName": "engineering", "members": []map[string]string{}}, http.StatusOK)
	data, _ := repository.WorkspaceMetadata("test-workspace")
	var entries []domain.AuditLogEntry
	for _, entry := range data.AuditLog {
		if entry.ResourceType == "team_member" && entry.ResourceID == userID {
			entries = append(entries, entry)
		}
	}
	if len(entries) != 2 {
		t.Fatalf("SCIM audit entries = %+v", entries)
	}
	requireAudit(t, entries[1], "added", map[string]any{"origin": "scim", "source": "scim", "reason": "scim_group_sync", "scimTokenId": token.ID, "scimTokenName": "Okta", "teamId": teamID})
	requireAudit(t, entries[0], "removed", map[string]any{"origin": "scim", "source": "scim", "scimGroupName": "engineering"})
	if entries[0].Actor.ID != "scim" || entries[0].BotActor == nil || entries[0].BotActor.Type != "scim" || entries[0].BotActor.ID != token.ID {
		t.Fatalf("SCIM actor = %+v bot=%+v", entries[0].Actor, entries[0].BotActor)
	}
}

func TestMCPSaveTeamMembershipChangesAreAudited(t *testing.T) {
	f := newMCPContractFixture(t)
	created := mcpObject(t, f, "save_team", map[string]any{"name": "Platform", "key": "plt", "members": []string{"Test member"}})
	teamID := created["id"].(string)
	mcpObject(t, f, "save_team", map[string]any{"id": "PLT", "removeMembers": []string{"Test member"}})
	data, _ := f.repository.WorkspaceMetadata(f.data.Workspace.URLKey)
	actions := []string{}
	for _, entry := range data.AuditLog {
		if entry.ResourceType == "team_member" && entry.ResourceID == "usr_member" && entry.Metadata["teamId"] == teamID {
			if entry.Metadata["source"] != "api" || entry.Metadata["apiKeyId"] != "contract-key" {
				t.Fatalf("MCP audit metadata = %v", entry.Metadata)
			}
			actions = append(actions, entry.Action)
		}
	}
	if !slices.Equal(actions, []string{"removed", "added"}) {
		t.Fatalf("MCP save_team audit actions = %v", actions)
	}
}

func TestDevelopmentModeTeamMemberChangesAreAudited(t *testing.T) {
	handler, repository := enterpriseTestServer(t)
	seed, _ := repository.BootstrapFor("test-workspace")
	teamID := seed.Teams[0].ID
	var userID string
	for _, user := range seed.Users {
		if user.ID != seed.Viewer.ID {
			userID = user.ID
			break
		}
	}
	if userID == "" {
		t.Skip("fixture has a single user")
	}
	requestJSON[any](t, handler, http.MethodPut, "/api/workspaces/test-workspace/teams/"+teamID+"/members/"+userID, map[string]any{"member": false}, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodPut, "/api/workspaces/test-workspace/teams/"+teamID+"/members/"+userID, map[string]any{"member": true, "origin": "sync"}, http.StatusNoContent)
	data, _ := repository.WorkspaceMetadata("test-workspace")
	actions := []string{}
	for _, entry := range data.AuditLog {
		if entry.ResourceType == "team_member" && entry.ResourceID == userID {
			actions = append(actions, entry.Action+":"+entry.Metadata["origin"].(string))
		}
	}
	if len(actions) < 1 || actions[0] != "added:sync" {
		t.Fatalf("development audit = %v", actions)
	}
}
