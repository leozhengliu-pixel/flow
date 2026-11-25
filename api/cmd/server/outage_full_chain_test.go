package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const outageRoleRead = "SELECT role,status FROM workspace_memberships "

type outageMutationCase struct {
	name         string
	request      outageRequest
	healthy      int
	authDisabled bool
}

type outageMutationHandlers struct {
	authenticated, development http.Handler
}

func newOutageMutationHandlers(t *testing.T, repo *store.SQLiteStore) outageMutationHandlers {
	return outageMutationHandlers{
		newHandler(&server{store: repo, coordinationContext: t.Context(), deliverySchedulerInterval: time.Hour}),
		newHandler(&server{store: repo, coordinationContext: t.Context(), deliverySchedulerInterval: time.Hour, authDisabled: true}),
	}
}

func (h outageMutationHandlers) forCase(tc outageMutationCase) http.Handler {
	if tc.authDisabled {
		return h.development
	}
	return h.authenticated
}

func TestFullChainDatabaseOutageSQLite(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		t.Run("bounded="+strconv.FormatBool(bounded), func(t *testing.T) {
			config := store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "outage.db")}
			repo := openOutageStore(t, config, bounded)
			handlers := newOutageMutationHandlers(t, repo)
			cases := fullChainOutageFixture(t, repo, handlers.authenticated)
			db := openOutageVerificationDB(t, config)
			ready := checkHealthyOutageMutations(t, db, handlers, cases)
			t.Run("writer-deadline", func(t *testing.T) {
				checkWriterOutage(t, handlers, cases, bounded, 250*time.Millisecond, func(ctx context.Context) { <-ctx.Done() }, nil)
			})
			t.Run("closed-pool", func(t *testing.T) {
				if err := repo.CloseDatabasePoolForTesting(); err != nil {
					t.Fatal("could not close the request database pool")
				}
				checkFullChainOutage(t, handlers, cases, 5*time.Second, false)
			})
			if outageMutationCounts(t, db) != ready {
				t.Fatal("an outage request persisted a mutation")
			}
		})
	}
}

func fullChainOutageFixture(t *testing.T, repo *store.SQLiteStore, handler http.Handler) []outageMutationCase {
	t.Helper()
	metadata, ok := repo.WorkspaceSettingsMetadata("")
	if !ok {
		t.Fatal("workspace fixture is missing")
	}
	workspace, workspaceID := metadata.Workspace.URLKey, metadata.Workspace.ID
	_, adminToken := outageSession(t, repo)
	adminCookie := outageCookie(workspace, adminToken)
	teams := outageResponse[connection[domain.Team]](t, handler, outageRequest{http.MethodGet, "/api/teams", "", adminCookie}, http.StatusOK).Nodes
	if len(teams) == 0 {
		t.Fatal("workspace fixture has no team")
	}
	if err := repo.UpdateMemberRole(t.Context(), workspaceID, "usr_member", "member"); err != nil {
		t.Fatal("could not set fixture member role")
	}
	_, memberToken, err := repo.Login(t.Context(), "member@example.test", "test-password")
	if err != nil || memberToken == "" {
		t.Fatal("could not sign in the workspace member")
	}
	restricted, restrictedToken, err := repo.LoginExternal(t.Context(), "restricted@example.test", "Restricted member", "", true)
	if err != nil || restrictedToken == "" {
		t.Fatal("could not sign in the team member")
	}
	if err := repo.EnsureWorkspaceMembership(t.Context(), workspaceID, restricted.User.ID); err != nil {
		t.Fatal("could not join the workspace")
	}
	private := outageResponse[domain.Team](t, handler, outageRequest{http.MethodPost, "/api/workspaces/" + workspace + "/teams",
		`{"name":"Outage private team","key":"OPT","private":true}`, adminCookie}, http.StatusCreated)
	if err := repo.SetTeamMembership(t.Context(), workspaceID, private.ID, restricted.User.ID, "member", true); err != nil {
		t.Fatal("could not join the private team")
	}
	createIssue := func(team string) domain.Issue {
		return outageResponse[domain.Issue](t, handler, outageRequest{http.MethodPost, "/api/issue-records",
			fmt.Sprintf(`{"title":"Outage issue","teamId":%q}`, team), adminCookie}, http.StatusCreated)
	}
	publicIssue, privateIssue := createIssue(teams[0].ID), createIssue(private.ID)
	app := outageResponse[domain.OAuthApplication](t, handler, outageRequest{http.MethodPost, "/api/oauth-applications",
		`{"name":"Outage OAuth client","scopes":["read","write"],"redirectUris":["https://client.example.test/callback"]}`, adminCookie}, http.StatusCreated)
	_, readOnlyKey := createOutageAPIKey(t, handler, adminCookie, "Outage read-only credential")
	bearer := func(secret string) func(*http.Request) {
		return func(r *http.Request) {
			r.Header.Set("X-Workspace-Key", workspace)
			outageBearer(secret)(r)
		}
	}

	var cases []outageMutationCase
	for _, principal := range []struct {
		name, token, issue, selectedTeam string
	}{
		{"admin", adminToken, publicIssue.ID, ""},
		{"member", memberToken, publicIssue.ID, ""},
		{"team-member", restrictedToken, privateIssue.ID, private.ID},
	} {
		cookie := outageCookie(workspace, principal.token)
		keyInput := apiKeyCreateInput{Name: "Outage fixture credential", Scopes: []string{"write"}}
		if principal.selectedTeam != "" {
			keyInput.TeamRestriction, keyInput.TeamIDs = "selected", []string{principal.selectedTeam}
		}
		secret := outageResponse[struct{ Secret string }](t, handler,
			outageRequest{http.MethodPost, "/api/api-keys", outageFixtureJSON(t, keyInput), cookie}, http.StatusCreated).Secret
		if secret == "" {
			t.Fatal("personal credential was not issued")
		}
		oauth := issueOutageOAuthToken(t, handler, cookie, workspace, app)
		for _, credential := range []struct {
			name    string
			headers func(*http.Request)
			keyCode int
		}{
			{"cookie", cookie, http.StatusCreated},
			{"api-key", bearer(secret), http.StatusForbidden},
			{"oauth", bearer(oauth), http.StatusForbidden},
		} {
			prefix := principal.name + "/" + credential.name + "/"
			for _, alias := range []string{"issue-records", "issues"} {
				cases = append(cases, outageMutationCase{prefix + alias, outageComment(alias, principal.issue, credential.headers), http.StatusCreated, false})
			}
			cases = append(cases, outageMutationCase{prefix + "personal-key", outageRequest{http.MethodPost, "/api/api-keys",
				`{"name":"Outage request","scopes":["read"]}`, credential.headers}, credential.keyCode, false})
		}
		if principal.selectedTeam != "" {
			for _, alias := range []string{"issue-records", "issues"} {
				cases = append(cases, outageMutationCase{"denied/key-team/" + alias, outageComment(alias, publicIssue.ID, bearer(secret)), http.StatusForbidden, false})
			}
		}
	}
	for _, alias := range []string{"issue-records", "issues"} {
		cases = append(cases,
			outageMutationCase{"denied/member-team/" + alias, outageComment(alias, privateIssue.ID, outageCookie(workspace, memberToken)), http.StatusForbidden, false},
			outageMutationCase{"denied/key-scope/" + alias, outageComment(alias, publicIssue.ID, bearer(readOnlyKey)), http.StatusUnauthorized, false},
			outageMutationCase{"auth-disabled/" + alias, outageComment(alias, publicIssue.ID, adminCookie), http.StatusCreated, true})
	}
	return append(cases, outageMutationCase{"auth-disabled/personal-key", outageRequest{http.MethodPost, "/api/api-keys",
		`{"name":"Outage request (development)","scopes":["read"]}`, adminCookie}, http.StatusCreated, true})
}

func outageComment(alias, issue string, headers func(*http.Request)) outageRequest {
	return outageRequest{http.MethodPost, "/api/" + alias + "/" + issue + "/comments", `{"body":"Outage request"}`, headers}
}

func outageFixtureJSON(t *testing.T, input any) string {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal("could not encode fixture")
	}
	return string(body)
}

func issueOutageOAuthToken(t *testing.T, handler http.Handler, cookie func(*http.Request), workspace string, app domain.OAuthApplication) string {
	t.Helper()
	body := outageFixtureJSON(t, map[string]any{"clientId": app.ClientID, "redirectUri": oauthTestRedirect,
		"responseType": "code", "scope": "read write", "workspaceKey": workspace, "approve": true})
	decision := outageResponse[map[string]string](t, handler, outageRequest{http.MethodPost, "/api/oauth/authorization-request", body, cookie}, http.StatusOK)
	redirect, err := url.Parse(decision["redirect"])
	if err != nil || redirect.Query().Get("code") == "" {
		t.Fatal("authorization did not issue a code")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")}, "redirect_uri": {oauthTestRedirect}}
	token := outageResponse[oauthTokenPair](t, handler, outageOAuth("/oauth/token", form, app.ClientID, app.ClientSecret), http.StatusOK)
	if token.AccessToken == "" {
		t.Fatal("OAuth access token was not issued")
	}
	return token.AccessToken
}

func checkHealthyOutageMutations(t *testing.T, db *sql.DB, handlers outageMutationHandlers, cases []outageMutationCase) [5]int {
	t.Helper()
	before := outageMutationCounts(t, db)
	if !t.Run("healthy", func(t *testing.T) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				request := tc.request
				request.body = strings.Replace(request.body, "Outage request", "Healthy probe", 1)
				w := request.serve(t.Context(), handlers.forCase(tc))
				if w.Code != tc.healthy || w.Header().Get("Retry-After") != "" {
					t.Fatalf("got status %d, want %d without Retry-After", w.Code, tc.healthy)
				}
			})
		}
	}) {
		t.Fatal("could not establish healthy controls for the outage matrix")
	}
	ready := outageMutationCounts(t, db)
	for i := range ready {
		if ready[i] <= before[i] {
			t.Fatal("healthy operations did not write their expected records")
		}
	}
	return ready
}

func checkFullChainOutage(t *testing.T, handlers outageMutationHandlers, cases []outageMutationCase, timeout time.Duration, wantDeadline bool) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			assertRetryableOutage(t, tc.request.serve(ctx, handlers.forCase(tc)))
			if wantDeadline && ctx.Err() != context.DeadlineExceeded {
				t.Fatal("request did not reach the database deadline")
			}
		})
	}
}

func checkWriterOutage(t *testing.T, handlers outageMutationHandlers, cases []outageMutationCase, bounded bool, timeout time.Duration, failRead func(context.Context), recoverDB func()) {
	t.Helper()
	for _, tc := range cases {
		if tc.healthy != http.StatusCreated {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			writer, query := "mutateIssueScope", outageRoleRead
			if strings.HasSuffix(tc.name, "/personal-key") {
				writer = "mutateFull"
				if bounded {
					writer = "mutateWorkspaceScopedWithAggregate"
				}
			} else if bounded {
				// The HTTP wrapper sends both comment aliases to this writer.
				writer, query = "mutateBoundedIssueDiscussion", "FROM workspace_memberships "
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			if recoverDB != nil {
				defer recoverDB()
			}
			w := serveAtWriterRead(t, ctx, handlers.forCase(tc), tc.request, writer, query, func() { failRead(ctx) })
			assertDatabaseOutage(t, w)
		})
	}
}

func serveAtWriterRead(t *testing.T, ctx context.Context, handler http.Handler, request outageRequest, writer, queryFragment string, atRead func()) *httptest.ResponseRecorder {
	t.Helper()
	reachedRead, attemptedWrite := false, false
	ctx = store.WithSQLStatementObserver(ctx, func(query string) {
		for _, verb := range []string{"INSERT ", "UPDATE ", "DELETE ", "REPLACE "} {
			attemptedWrite = attemptedWrite || strings.HasPrefix(strings.TrimSpace(query), verb)
		}
		if !reachedRead && strings.Contains(query, queryFragment) && outageInWriter(writer) {
			reachedRead = true
			atRead()
		}
	})
	w := request.serve(ctx, handler)
	if !reachedRead {
		t.Fatalf("request returned %d before the writer's authorization read", w.Code)
	}
	if attemptedWrite {
		t.Error("a refused authorization read reached a write")
	}
	return w
}

func openOutageVerificationDB(t *testing.T, config store.DatabaseConfig) *sql.DB {
	t.Helper()
	driver, dsn := config.Driver, config.URL
	switch driver {
	case "postgres":
		driver = "pgx"
	case "sqlite":
		dsn = config.Path
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal("could not configure verification database")
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func outageMutationCounts(t *testing.T, db *sql.DB) [5]int {
	t.Helper()
	var counts [5]int
	for i, query := range []string{
		`SELECT COUNT(*) FROM workspace_content_records WHERE kind='comment'`,
		`SELECT COUNT(*) FROM workspace_metadata_records WHERE field='apiKeys'`,
		`SELECT COUNT(*) FROM api_key_lookup`,
		`SELECT COUNT(*) FROM domain_events WHERE event_type IN ('comment.created','api_key.created')`,
		`SELECT COUNT(*) FROM domain_events`,
	} {
		if err := db.QueryRowContext(t.Context(), query).Scan(&counts[i]); err != nil {
			t.Fatal("could not verify persisted mutations")
		}
	}
	return counts
}

func TestLegacyDatabaseOutageDuringWriteAuthorization(t *testing.T) {
	t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	config := store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "legacy-outage.db")}
	repo := openOutageStore(t, config, false)
	handler := newHandler(&server{
		store: repo, coordinationContext: t.Context(), deliverySchedulerInterval: time.Hour,
		coding: &codingServices{service: new(coding.Service)},
	})
	metadata, ok := repo.WorkspaceSettingsMetadata("")
	if !ok {
		t.Fatal("workspace fixture is missing")
	}
	workspace := metadata.Workspace.URLKey
	const logoURL = "https://example.test/outage-logo.png"
	metadata.Workspace.LogoURL = logoURL
	if _, err := repo.UpdateWorkspace(t.Context(), workspace, metadata.Workspace); err != nil {
		t.Fatal("could not seed the workspace logo")
	}
	admin, token := outageSession(t, repo)
	cookie := outageCookie(workspace, token)
	const outsiderEmail, outsiderPassword = "outsider@example.test", "test-password"
	outsider, verification, err := repo.Register(t.Context(), "Outsider", outsiderEmail, outsiderPassword)
	if err != nil || repo.VerifyEmail(t.Context(), verification) != nil {
		t.Fatal("could not verify the non-member account")
	}
	_, outsiderToken, err := repo.Login(t.Context(), outsiderEmail, outsiderPassword)
	if err != nil || outsiderToken == "" {
		t.Fatal("could not create the non-member session")
	}
	if _, _, err := repo.WorkspaceRole(t.Context(), metadata.Workspace.ID, outsider.ID); !errors.Is(err, store.ErrAuthForbidden) {
		t.Fatal("non-member fixture has unexpected workspace access")
	}
	outsiderCookie := outageCookie(workspace, outsiderToken)
	if err := repo.EnsureWorkspaceMembership(t.Context(), metadata.Workspace.ID, outsider.ID); err != nil {
		t.Fatal("could not prepare the former member")
	}
	if err := repo.RemoveMember(t.Context(), metadata.Workspace.ID, outsider.ID); err != nil {
		t.Fatal("could not remove the former member")
	}
	teams := outageResponse[connection[domain.Team]](t, handler, outageRequest{http.MethodGet, "/api/teams", "", cookie}, http.StatusOK).Nodes
	if len(teams) == 0 {
		t.Fatal("workspace fixture has no team")
	}
	teamPath := "/api/teams/" + teams[0].ID
	workspacePath := "/api/workspaces/" + workspace
	workspaceTeamPath := workspacePath + "/teams/" + teams[0].ID
	createIssue := outageRequest{http.MethodPost, "/api/issue-records",
		fmt.Sprintf(`{"title":"Outage issue","teamId":%q}`, teams[0].ID), cookie}
	issue := outageResponse[domain.Issue](t, handler, createIssue, http.StatusCreated)
	const reviewPath = "/api/reviews/outage-review"
	if err := repo.MutateWorkspace(t.Context(), workspace, "review.outage_fixture", "outage-review", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.SCIMEnabled = true
		data.Reviews = append(data.Reviews, domain.CodeReview{
			ID: "outage-review", Title: "Outage review", Status: "open", IssueIDs: []string{issue.ID},
			Events: []domain.ReviewEvent{{ID: "outage-thread", Type: "commented", Actor: admin}},
		})
		return nil
	}); err != nil {
		t.Fatal("could not seed the review and member policy")
	}
	project := outageResponse[domain.Project](t, handler, outageRequest{http.MethodPost, "/api/projects",
		fmt.Sprintf(`{"name":"Outage project","teamIds":[%q]}`, teams[0].ID), cookie}, http.StatusCreated)
	app := outageResponse[domain.OAuthApplication](t, handler, outageRequest{http.MethodPost, "/api/oauth-applications",
		`{"name":"Outage application","scopes":["read","write"],"redirectUris":["https://client.example.test/callback"]}`, cookie}, http.StatusCreated)
	member, memberToken, err := repo.Login(t.Context(), "member@example.test", "test-password")
	if err != nil || memberToken == "" {
		t.Fatal("could not sign in the member")
	}
	issueOutageOAuthToken(t, handler, outageCookie(workspace, memberToken), workspace, app)
	apps := outageResponse[[]authorizedApplication](t, handler, outageRequest{http.MethodGet, "/api/oauth/authorized-applications", "", cookie}, http.StatusOK)
	var grantID string
	for _, item := range apps {
		for _, grant := range item.Authorizations {
			if item.ClientID == app.ClientID && grant.UserID == member.User.ID {
				grantID = grant.ID
			}
		}
	}
	if grantID == "" {
		t.Fatal("could not look up the member's authorization")
	}
	chat := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "feishu", ConnectionID: "outage-chat", AppID: "outage-app",
		TenantKey: "outage-tenant", Connected: true}
	if err := repo.PutChatIntegration(t.Context(), chat); err != nil {
		t.Fatal("could not create the chat integration")
	}
	const integrationState = "outage-integration-state"
	if err := repo.MutateWorkspace(t.Context(), workspace, "integration.oauth_started", "slack", nil, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{
			ID: "outage-integration", Provider: "slack", Status: "oauth_pending",
			ConnectedBy: admin.ID, OAuthState: integrationState, OAuthStartedAt: &now,
		})
		return nil
	}); err != nil {
		t.Fatal("could not create the pending integration")
	}
	private := outageResponse[domain.Team](t, handler, outageRequest{http.MethodPost, workspacePath + "/teams",
		`{"name":"Outage private team","key":"OPT","private":true}`, cookie}, http.StatusCreated)
	moveIssue := outageRequest{http.MethodPatch, "/api/issue-records/" + issue.ID,
		fmt.Sprintf(`{"teamId":%q}`, private.ID), cookie}
	chatBinding := outageRequest{http.MethodPost, "/api/integrations/feishu/chat/bindings",
		fmt.Sprintf(`{"chatId":"outage-group","teamId":%q,"authorize":true}`, private.ID), cookie}
	const loopRunPath = "/api/loops/loop_outage/runs/run_outage"
	db := openOutageVerificationDB(t, config)
	const roleRead = outageRoleRead
	const teamIDsRead = "SELECT team_id FROM team_memberships "
	const teamMembersRead = "SELECT m.team_id,m.user_id,m.role,m.joined_at,"
	const membersRead = "SELECT u.id,u.email,u.name,u.display_name,u.avatar_url,u.email_verified_at,u.active,u.deleted_at,m.role,m.status,"
	for _, tc := range []struct {
		name, writer, query string
		request             outageRequest
	}{
		{"issue-record-comment", "mutateIssueScope", roleRead, outageComment("issue-records", issue.ID, cookie)},
		{"issue-comment", "mutateIssueScope", roleRead, outageComment("issues", issue.ID, cookie)},
		{"issue-move-member-directory", "loadMemberAccess", membersRead, moveIssue},
		{"personal-key", "mutateFull", roleRead, outageRequest{http.MethodPost, "/api/api-keys", `{"name":"Outage request","scopes":["read"]}`, cookie}},
		{"personal-key-teams", "mutateFull", teamIDsRead, outageRequest{http.MethodPost, "/api/api-keys", `{"name":"Outage request","scopes":["read"]}`, cookie}},
		{"issue-create", "createIssueRecords", roleRead, createIssue},
		{"issue-create-default-team", "createIssue", "FROM workspace_memberships ", outageRequest{http.MethodPost, "/api/issue-records", `{"title":"Outage issue"}`, cookie}},
		{"issue-edit", "UpdateIssueRecord", roleRead, outageRequest{http.MethodPatch, "/api/issue-records/" + issue.ID, `{"title":"Outage edit"}`, cookie}},
		{"issue-permissions", "mutateIssueScope", roleRead, outageRequest{http.MethodPut, "/api/issues/" + issue.ID + "/permissions", `{"permissions":[]}`, cookie}},
		{"private-export", "createExport", roleRead, outageRequest{http.MethodPost, "/api/exports", `{"includePrivate":true}`, cookie}},
		{"review-edit", "updateReview", roleRead, outageRequest{http.MethodPatch, reviewPath, `{"title":"Outage edit"}`, cookie}},
		{"review-submit", "submitReview", roleRead, outageRequest{http.MethodPost, reviewPath + "/submit", `{"decision":"approve"}`, cookie}},
		{"review-comment", "commentOnReview", roleRead, outageRequest{http.MethodPost, reviewPath + "/comments", `{"body":"Outage review comment"}`, cookie}},
		{"review-resolve", "resolveReviewThread", roleRead, outageRequest{http.MethodPatch, reviewPath + "/comments/outage-thread", `{"resolved":true}`, cookie}},
		{"project-record", "mutateProjectRecordSelection", teamIDsRead, outageRequest{http.MethodPatch, "/api/projects/" + project.ID, `{"summary":"Outage edit"}`, cookie}},
		{"scoped-workflow-state", "mutateScoped", roleRead, outageRequest{http.MethodPost, teamPath + "/states", `{"name":"Outage state","type":"started"}`, cookie}},
		{"team-create", "mutateTeamMetadata", roleRead, outageRequest{http.MethodPost, workspacePath + "/teams", `{"name":"Outage team","key":"OUT"}`, cookie}},
		{"team-create-members", "createTeam", teamMembersRead, outageRequest{http.MethodPost, workspacePath + "/teams", `{"name":"Outage team","key":"OUT"}`, cookie}},
		{"team-edit", "updateTeam", teamMembersRead, outageRequest{http.MethodPatch, workspaceTeamPath, `{"name":"Outage team"}`, cookie}},
		{"team-settings", "updateStructuredTeamSettings", teamMembersRead, outageRequest{http.MethodPatch, teamPath + "/settings", `{"timezone":"Etc/UTC"}`, cookie}},
		{"account-settings", "mutateUserSettingsRecord", roleRead, outageRequest{http.MethodPatch, "/api/account/settings", `{"timezone":"Etc/UTC"}`, cookie}},
		{"last-workspace", "SetLastWorkspace", roleRead, outageRequest{http.MethodPut, "/api/account/last-workspace", fmt.Sprintf(`{"workspaceKey":%q}`, workspace), cookie}},
		{"team-member-actor", "changeTeamMember", roleRead, outageRequest{http.MethodPut, teamPath + "/members/usr_member", `{"role":"member"}`, cookie}},
		{"team-member-target", "changeTeamMembership", roleRead, outageRequest{http.MethodPut, teamPath + "/members/usr_member", `{"role":"member"}`, cookie}},
		{"team-member-projection", "publicMetadata", membersRead, outageRequest{http.MethodPut, teamPath + "/members/usr_member", `{"role":"member"}`, cookie}},
		{"team-member-sync", "syncTeamMemberships", membersRead, outageRequest{http.MethodPost, teamPath + "/members/sync", `{"members":[]}`, cookie}},
		{"workspace-member-role", "UpdateMemberRole", "SELECT role FROM workspace_memberships ", outageRequest{http.MethodPatch, workspacePath + "/members/usr_member", `{"role":"member"}`, cookie}},
		{"workspace-member-role-audit", "updateMemberRole", roleRead, outageRequest{http.MethodPatch, workspacePath + "/members/usr_member", `{"username":"outage","role":"member"}`, cookie}},
		{"workspace-member-identity", "updateMemberRole", membersRead, outageRequest{http.MethodPatch, workspacePath + "/members/usr_member", `{"username":"outage","displayName":"Outage member"}`, cookie}},
		{"workspace-member-username", "updateMemberRole", "SELECT COUNT(*) FROM workspace_memberships ", outageRequest{http.MethodPatch, workspacePath + "/members/usr_member", `{"username":"outage"}`, cookie}},
		{"scim-member-protection", "refuseSCIMManagedMember", "FROM workspace_memberships m WHERE ", outageRequest{http.MethodPatch, workspacePath + "/members/usr_member", `{"role":"member"}`, cookie}},
		{"last-admin-protection", "ensureAdminRemains", roleRead, outageRequest{http.MethodPost, workspacePath + "/members/usr_member/suspend", "", cookie}},
		{"owner-invite", "createInvitation", roleRead, outageRequest{http.MethodPost, workspacePath + "/invitations", `{"emails":["invite@example.test"],"role":"owner"}`, cookie}},
		{"team-invite", "inviteTeamDenied", roleRead, outageRequest{http.MethodPost, workspacePath + "/invitations", fmt.Sprintf(`{"emails":["invite@example.test"],"teamIds":[%q]}`, teams[0].ID), cookie}},
		{"workspace-logo", "deleteWorkspaceLogo", roleRead, outageRequest{http.MethodDelete, workspacePath + "/logo", "", cookie}},
		{"workspace-logo-upload", "uploadWorkspaceLogo", roleRead, outageRequest{http.MethodPost, workspacePath + "/logo", "", cookie}},
		{"release-pipeline", "requestWorkspaceRole", roleRead, outageRequest{http.MethodPost, "/api/release-pipelines", `{"name":"Outage pipeline"}`, cookie}},
		{"target-branch", "requestWorkspaceRole", teamMembersRead, outageRequest{http.MethodPost, "/api/target-branches", fmt.Sprintf(`{"teamId":%q,"repository":"example","branch":"main"}`, teams[0].ID), cookie}},
		{"coding-environment", "codingEnvironmentAccess", roleRead, outageRequest{http.MethodPost, "/api/coding/environments", `{}`, cookie}},
		{"coding-session", "codingViewerFor", roleRead, outageRequest{http.MethodPost, "/api/coding/sessions", `{}`, cookie}},
		{"loop-cancel", "loopViewerData", roleRead, outageRequest{http.MethodPost, loopRunPath + "/cancel", "", cookie}},
		{"loop-reply", "loopViewerData", teamMembersRead, outageRequest{http.MethodPost, loopRunPath + "/replies", `{"body":"Outage reply"}`, cookie}},
		{"chat-session", "chatSettingsMetadata", roleRead, chatBinding},
		{"chat-team-owner", "chatTeamOwner", roleRead, chatBinding},
		{"chat-private-authorizer", "chatBindingPermitted", roleRead, chatBinding},
		{"integration-oauth-finish", "finishIntegrationOAuth", roleRead, outageRequest{http.MethodPost, "/api/integrations/slack/oauth/finish",
			outageFixtureJSON(t, map[string]string{"code": "outage-code", "state": integrationState}), cookie}},
		{"member-erasure", "eraseMember", roleRead, outageRequest{http.MethodPost, workspacePath + "/members/usr_member/erase", `{"confirmation":"member@example.test"}`, cookie}},
		{"former-member-erasure", "readFormerMembers", "SELECT 1 FROM workspace_memberships ", outageRequest{http.MethodPost, workspacePath + "/members/" + outsider.ID + "/erase", `{"confirmation":"outsider@example.test"}`, cookie}},
		{"member-erasure-workspace-count", "OtherWorkspaceMemberships", "FROM workspace_memberships WHERE ", outageRequest{http.MethodPost, workspacePath + "/members/usr_member/erase", `{"confirmation":"member@example.test"}`, cookie}},
		{"application-installation", "installApplicationTx", roleRead, outageRequest{http.MethodPost, "/api/application-installations",
			fmt.Sprintf(`{"clientId":%q,"teamIds":[%q],"scopes":["read"],"active":true}`, app.ClientID, teams[0].ID), cookie}},
		{"application-installation-projection", "configureApplicationInstallation", roleRead, outageRequest{http.MethodPost, "/api/application-installations",
			fmt.Sprintf(`{"clientId":%q,"teamIds":[%q],"scopes":["read"],"active":true}`, app.ClientID, teams[0].ID), cookie}},
		{"application-installation-team-directory", "configureApplicationInstallation", teamMembersRead, outageRequest{http.MethodPost, "/api/application-installations",
			fmt.Sprintf(`{"clientId":%q,"teamIds":[%q],"scopes":["read"],"active":true}`, app.ClientID, teams[0].ID), cookie}},
		{"oauth-authorization-revocation", "RevokeOAuthAuthorizationRecords", roleRead, outageRequest{http.MethodDelete, "/api/oauth/authorizations/" + grantID, "", cookie}},
		{"developer-token", "developerTokenApplication", roleRead, outageRequest{http.MethodPost, "/api/oauth-applications/" + app.ID + "/developer-token", `{"actor":"user"}`, cookie}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := outageMutationCounts(t, db)
			denied := tc.request
			denied.headers = outsiderCookie
			if w := denied.serve(t.Context(), handler); w.Code != http.StatusForbidden || w.Header().Get("Retry-After") != "" {
				t.Fatalf("non-member got status %d, want 403 without Retry-After", w.Code)
			}
			if outageMutationCounts(t, db) != before {
				t.Fatal("a forbidden request persisted a mutation")
			}
			// The query also runs in middleware. Stall it only in the writer,
			// after the real credential and resource authorization succeeded.
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			w := serveAtWriterRead(t, ctx, handler, tc.request, tc.writer, tc.query, func() { <-ctx.Done() })
			if strings.HasPrefix(tc.name, "workspace-logo") {
				if current, ok := repo.WorkspaceSettingsMetadata(workspace); !ok || current.Workspace.LogoURL != logoURL {
					t.Error("a failed authorization read changed the workspace logo")
				}
			}
			if outageMutationCounts(t, db) != before {
				t.Error("a failed authorization read persisted a mutation")
			}
			assertDatabaseOutage(t, w)
		})
	}
	for _, tc := range []struct {
		name, writer string
		request      outageRequest
	}{
		{"issue-record-comment", "mutateIssueScope", outageComment("issue-records", issue.ID, cookie)},
		{"issue-comment", "mutateIssueScope", outageComment("issues", issue.ID, cookie)},
		{"personal-key", "mutateFull", outageRequest{http.MethodPost, "/api/api-keys", `{"name":"Outage request","scopes":["read"]}`, cookie}},
	} {
		t.Run(tc.name+"-suspended-at-write", func(t *testing.T) {
			setStatus := func(status string) {
				t.Helper()
				if _, err := db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status=? WHERE workspace_id=? AND user_id=?`,
					status, metadata.Workspace.ID, admin.ID); err != nil {
					t.Fatal("could not update fixture membership")
				}
			}
			before := outageMutationCounts(t, db)
			defer setStatus("active")
			w := serveAtWriterRead(t, t.Context(), handler, tc.request, tc.writer, roleRead, func() { setStatus("suspended") })
			if w.Code != http.StatusForbidden || w.Header().Get("Retry-After") != "" {
				t.Fatalf("suspended member got status %d, want 403 without Retry-After", w.Code)
			}
			if outageMutationCounts(t, db) != before {
				t.Fatal("a suspended member persisted a mutation")
			}
		})
	}
}

func outageInWriter(writer string) bool {
	var pcs [48]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, "."+writer) {
			return true
		}
		if !more {
			return false
		}
	}
}
