package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// agentTaskDetailOriginal restores the authorization GET /api/agent-tasks/{id}
// had in legacy metadata mode before the route was delegated to its handler:
// resourceAllowedLegacy built the viewer's whole workspace projection behind
// the shared projection gate and, having no agent-task rule, allowed the
// request once that projection loaded. Bounded metadata never took it.
func (s *server) agentTaskDetailOriginal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.store.BoundedMetadataEnabled() {
			refuse := func() {
				if !writeProjectionRefusal(w, r) {
					writeError(w, http.StatusForbidden, "This resource is outside your teams")
				}
			}
			if !acquireProjection(r) {
				refuse()
				return
			}
			if _, err := s.store.PagedWorkspaceMetadata(r.Context(), workspaceKey(r), authUser(r).ID); err != nil {
				noteAuthorizationBusy(r, err)
				refuse()
				return
			}
		}
		next(w, r)
	}
}

// agentTaskDetailStack is the authentication and workspace authorization the
// router applies, around the detail handler as it is now (original false) or
// with the pre-fix projection (original true).
func agentTaskDetailStack(s *server, original bool) http.Handler {
	mux := http.NewServeMux()
	handler := s.getApplicationAgentTask
	if original {
		handler = s.agentTaskDetailOriginal(handler)
	}
	mux.HandleFunc("GET /api/agent-tasks/{id}", handler)
	return s.projectionGate.holdProjections(s.authenticate(mux))
}

type agentTaskDetailPrincipal struct {
	name    string
	cookies []*http.Cookie
	bearer  string
	// mcp is set for principals the MCP tool is also read with.
	mcp *mcpActor
}

type agentTaskDetailFixture struct {
	scaleP03PlatformFixture
	principals []agentTaskDetailPrincipal
	tasks      map[string]string // name -> task id
	issueID    string
	reply      string
}

func agentTaskDetailSetup(t *testing.T, repo *store.SQLiteStore) agentTaskDetailFixture {
	t.Helper()
	f := scaleP03Fixture(t, repo)
	ctx := t.Context()
	workspace, owner, public := f.data.Workspace.URLKey, f.data.Viewer, f.data.Teams[0].ID
	prefix := idgen.New("atd-")
	private := prefix + "-private"
	if err := repo.MutateWorkspace(ctx, workspace, "test.agent_task_detail", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: private, Key: "ATD", Name: "Private", Private: true})
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.Projects = append(data.Projects, domain.Project{ID: prefix + "-project", Name: "Anchor project", TeamIDs: []string{public}})
		data.Documents = append(data.Documents, domain.Document{ID: prefix + "-document", Title: "Anchor document", Creator: owner, TeamIDs: []string{public}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, private, owner.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse(f.host.URL)
	admin, adminUser := scaleP03Join(t, f, "Detail admin", prefix+"-admin@example.test", "admin", public)
	member, memberUser := scaleP03Join(t, f, "Detail member", prefix+"-member@example.test", "member", public)
	guest, guestUser := scaleP03Join(t, f, "Detail guest", prefix+"-guest@example.test", "guest", public)
	insider, insiderUser := scaleP03Join(t, f, "Detail private member", prefix+"-insider@example.test", "member", public)
	if err := repo.SetTeamMembership(ctx, f.data.Workspace.ID, private, insiderUser.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	publicIssue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Public agent issue", "teamId": public}, workspace, http.StatusCreated)
	privateIssue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Private agent issue", "teamId": private}, workspace, http.StatusCreated)
	app, appSecret := agentTaskPollCredential(t, repo, workspace, owner.ID, prefix, []string{public})

	keys := map[string]domain.APIKey{}
	for _, spec := range []struct {
		name, creator string
		teams         []string
		revoked       bool
	}{
		{"owner-key", owner.ID, nil, false},
		{"member-key", memberUser.ID, nil, false},
		{"guest-key", guestUser.ID, nil, false},
		{"selected-key", owner.ID, []string{public}, false},
		{"selected-empty-key", owner.ID, []string{}, false},
		{"revoked-key", owner.ID, nil, true},
	} {
		key := domain.APIKey{ID: prefix + "-" + spec.name, Name: spec.name, SecretHash: secretHash(prefix + "-secret-" + spec.name), CreatorID: spec.creator, Scopes: []string{"read"}, CreatedAt: time.Now().UTC()}
		if spec.teams != nil {
			key.TeamRestriction, key.TeamIDs = "selected", spec.teams
		}
		if spec.revoked {
			at := time.Now().UTC().Add(-time.Minute)
			key.RevokedAt = &at
		}
		if err := repo.MutateWorkspace(ctx, workspace, "api_key.created", key.ID, nil, func(data *domain.Bootstrap) error {
			data.APIKeys = append(data.APIKeys, key)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		keys[spec.name] = key
	}

	tasks := map[string]string{}
	reply := "The saved agent reply"
	for _, spec := range []struct {
		name, kind, ref, creator, appUser, coding string
	}{
		{"member-reply", "issue", publicIssue.ID, memberUser.ID, app.UserID, ""},
		{"owner-private", "issue", privateIssue.ID, owner.ID, app.UserID, ""},
		{"guest-own-coding", "issue", publicIssue.ID, guestUser.ID, app.UserID, prefix + "-guest-coding"},
		{"owner-coding", "issue", publicIssue.ID, owner.ID, app.UserID, prefix + "-owner-coding"},
		{"other-app", "issue", publicIssue.ID, owner.ID, prefix + "-other-app", ""},
		{"legacy-anchor", "", publicIssue.ID, memberUser.ID, app.UserID, ""},
		{"chat", codingAnchorAgentChat, prefix + "-chat", memberUser.ID, app.UserID, prefix + "-chat-coding"},
		{"loop", codingAnchorLoop, prefix + "-loop", owner.ID, app.UserID, prefix + "-loop-coding"},
		{"document", "document", prefix + "-document", owner.ID, app.UserID, ""},
		{"project", "project", prefix + "-project", owner.ID, app.UserID, ""},
	} {
		task := domain.AgentTask{ID: prefix + "-" + spec.name, WorkspaceKey: workspace, ResourceType: spec.kind, ResourceID: spec.ref, CreatorID: spec.creator, AppUserID: spec.appUser, CodingSessionID: spec.coding, Prompt: "Investigate"}
		if spec.kind == "issue" || spec.kind == "" {
			task.IssueID = spec.ref
		}
		created, err := repo.CreateAgentInitiatedTask(ctx, task)
		if err != nil {
			t.Fatalf("%s: %v", spec.name, err)
		}
		for _, activity := range []domain.AgentActivity{{ActorID: spec.appUser, Type: "thought", Body: "Reading the issue"}, {ActorID: spec.appUser, Type: "response", Body: reply}} {
			if created, err = repo.AppendAgentActivity(ctx, workspace, task.ID, created.Version, activity); err != nil {
				t.Fatalf("%s %s: %v", spec.name, activity.Type, err)
			}
		}
		tasks[spec.name] = task.ID
	}
	tasks["missing"] = prefix + "-missing"

	cookies := func(client *http.Client) []*http.Cookie { return client.Jar.Cookies(origin) }
	actor := func(user domain.User, key domain.APIKey) *mcpActor {
		return &mcpActor{WorkspaceKey: workspace, User: user, APIKey: key}
	}
	ownerUser := owner
	principals := []agentTaskDetailPrincipal{
		{name: "owner", cookies: cookies(f.admin), mcp: actor(ownerUser, keys["owner-key"])},
		{name: "admin-outside-private", cookies: cookies(admin), mcp: actor(adminUser, domain.APIKey{Scopes: []string{"read"}})},
		{name: "member-private-outsider", cookies: cookies(member), mcp: actor(memberUser, keys["member-key"])},
		{name: "guest", cookies: cookies(guest), mcp: actor(guestUser, keys["guest-key"])},
		{name: "private-member", cookies: cookies(insider)},
		{name: "owner-key", bearer: prefix + "-secret-owner-key"},
		{name: "selected-team-key", bearer: prefix + "-secret-selected-key", mcp: actor(ownerUser, keys["selected-key"])},
		{name: "selected-empty-key", bearer: prefix + "-secret-selected-empty-key", mcp: actor(ownerUser, keys["selected-empty-key"])},
		{name: "revoked-key", bearer: prefix + "-secret-revoked-key"},
		{name: "app", bearer: appSecret},
	}
	return agentTaskDetailFixture{scaleP03PlatformFixture: f, principals: principals, tasks: tasks, issueID: publicIssue.ID, reply: reply}
}

func agentTaskDetailRequest(f agentTaskDetailFixture, principal agentTaskDetailPrincipal, path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	for _, cookie := range principal.cookies {
		r.AddCookie(cookie)
	}
	if principal.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+principal.bearer)
	}
	return r
}

func agentTaskDetailService(repo *store.SQLiteStore, gate *metadataReadGate, t *testing.T) *server {
	return &server{store: repo, uploadPath: t.TempDir(), coordinationContext: t.Context(), deliverySchedulerInterval: time.Hour, projectionGate: gate}
}

func agentTaskDetailLegacyEnv(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_ACCESS", "off")
}

// A completed agent reply loads while every projection slot is taken: the
// detail read takes no projection admission, like the task list. The
// pre-fix authorization waited for a slot and answered 503 after the wait.
func TestAgentTaskDetailReadsWithoutWorkspaceProjection(t *testing.T) {
	agentTaskDetailLegacyEnv(t)
	for name, repo := range scaleP03ServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := agentTaskDetailSetup(t, repo)
			const wait = time.Second
			gate := newMetadataReadGate(1, 8, wait, 0)
			service := agentTaskDetailService(repo, gate, t)
			handler := newHandler(service)
			original := agentTaskDetailStack(service, true)
			member := f.principals[2]
			detail := "/api/agent-tasks/" + f.tasks["member-reply"]
			list := "/api/agent-tasks?issueId=" + url.QueryEscape(f.issueID)
			send := func(handler http.Handler, path string) (*httptest.ResponseRecorder, time.Duration, int64, int64) {
				served, rejected := gate.served.Load(), gate.rejected.Load()
				started := time.Now()
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, agentTaskDetailRequest(f, member, path))
				return w, time.Since(started), gate.served.Load() - served, gate.rejected.Load() - rejected
			}

			// Free gate: the pre-fix detail read takes one projection admission.
			w, elapsed, served, _ := send(original, detail)
			if w.Code != http.StatusOK || served != 1 {
				t.Fatalf("original detail with a free gate: HTTP %d, %d admissions", w.Code, served)
			}
			t.Logf("free gate   original detail: HTTP %d in %s, admissions=%d", w.Code, elapsed.Round(time.Millisecond), served)
			for _, path := range []string{list, detail} {
				w, elapsed, served, _ := send(handler, path)
				if w.Code != http.StatusOK || served != 0 {
					t.Fatalf("%s with a free gate: HTTP %d, %d admissions", path, w.Code, served)
				}
				t.Logf("free gate   %-15s HTTP %d in %s, admissions=%d", map[bool]string{true: "list:", false: "detail:"}[path == list], w.Code, elapsed.Round(time.Millisecond), served)
			}

			// Saturated gate: the only slot is held by a wide projection.
			gate.slots <- struct{}{}
			defer func() { <-gate.slots }()
			w, elapsed, _, rejected := send(original, detail)
			if w.Code != http.StatusServiceUnavailable || rejected != 1 || elapsed < wait || !strings.Contains(w.Body.String(), "busy loading") {
				t.Fatalf("original detail with a saturated gate: HTTP %d after %s, %d rejections", w.Code, elapsed, rejected)
			}
			t.Logf("saturated   original detail: HTTP %d in %s, rejected admissions=%d", w.Code, elapsed.Round(time.Millisecond), rejected)
			for _, path := range []string{list, detail} {
				w, elapsed, served, rejected := send(handler, path)
				if w.Code != http.StatusOK || served != 0 || rejected != 0 || elapsed >= wait {
					t.Fatalf("%s with a saturated gate: HTTP %d after %s (%d admitted, %d rejected): %s", path, w.Code, elapsed, served, rejected, w.Body.String())
				}
				if path == detail {
					var page struct {
						Session    domain.AgentTask       `json:"session"`
						Activities []domain.AgentActivity `json:"activities"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Session.Status != "complete" || len(page.Activities) == 0 || page.Activities[len(page.Activities)-1].Body != f.reply {
						t.Fatalf("saturated detail did not return the saved reply: %s", w.Body.String())
					}
				}
				t.Logf("saturated   %-15s HTTP %d in %s, admissions=%d rejected=%d", map[bool]string{true: "list:", false: "detail:"}[path == list], w.Code, elapsed.Round(time.Millisecond), served, rejected)
			}

			// The MCP session reader runs the same handler inside the MCP
			// request's projection scope.
			mcp := *member.mcp
			scope := &projectionHold{gate: gate}
			defer scope.release()
			ctx := context.WithValue(t.Context(), projectionHoldKey{}, scope)
			started := time.Now()
			task, activities, err := service.mcpAgentSession(ctx, mcp, f.tasks["member-reply"], "")
			if err != nil || task.Status != "complete" || len(activities) == 0 || activities[len(activities)-1].Body != f.reply || time.Since(started) >= wait {
				t.Fatalf("MCP session read with a saturated gate: %v after %s", err, time.Since(started))
			}
			t.Logf("saturated   MCP get_agent_session: ok in %s", time.Since(started).Round(time.Millisecond))
			_, err = agentTaskDetailOriginalMCP(ctx, service, mcp, f.tasks["member-reply"])
			if err == nil || !strings.Contains(err.Error(), "busy loading") {
				t.Fatalf("original MCP session read with a saturated gate: %v", err)
			}
		})
	}
}

// agentTaskDetailExpected pins outcomes that show the matrix exercises each
// rule (owner, private team, guest, key teams, application, coding anchors).
var agentTaskDetailExpected = map[string]int{
	"owner owner-private":                   200,
	"admin-outside-private owner-private":   404,
	"member-private-outsider owner-private": 404,
	"private-member owner-private":          200,
	"member-private-outsider member-reply":  200,
	"guest member-reply":                    200,
	"guest owner-coding":                    404,
	"guest guest-own-coding":                200,
	"guest chat":                            404,
	"member-private-outsider chat":          200,
	"admin-outside-private chat":            200,
	"selected-team-key member-reply":        200,
	"selected-team-key owner-private":       404,
	"selected-empty-key member-reply":       404,
	"revoked-key member-reply":              401,
	"app member-reply":                      200,
	"app other-app":                         404,
	"owner missing":                         404,
	"member-private-outsider legacy-anchor": 200,
	"member-private-outsider document":      200,
}

// agentTaskDetailBody drops the router's per-request error envelope (code,
// type, request id); the error text and every success body stay as written.
func agentTaskDetailBody(raw []byte) string {
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil || object["error"] == nil {
		return string(raw)
	}
	delete(object, "code")
	delete(object, "type")
	delete(object, "requestId")
	normalized, _ := json.Marshal(object)
	return string(normalized)
}

func agentTaskDetailOriginalMCP(ctx context.Context, s *server, actor mcpActor, id string) (any, error) {
	return s.invokeMCPRoute(ctx, actor, http.MethodGet, "/api/agent-tasks/"+url.PathEscape(id)+"?after=", map[string]string{"id": id}, nil, s.agentTaskDetailOriginal(s.getApplicationAgentTask))
}

// The scoped authorization answers every viewer, credential and task exactly
// as the pre-fix whole-projection authorization did, in both metadata modes,
// over HTTP and through the MCP session reader.
func TestAgentTaskDetailScopedAuthorizationDifferential(t *testing.T) {
	agentTaskDetailLegacyEnv(t)
	for name, repo := range scaleP03ServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := agentTaskDetailSetup(t, repo)
			bounded, err := repo.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []*store.SQLiteStore{repo, bounded} {
				t.Run(fmt.Sprintf("bounded=%t", source.BoundedMetadataEnabled()), func(t *testing.T) {
					service := agentTaskDetailService(source, newMetadataReadGate(4, 8, 5*time.Second, 0), t)
					stacks := map[string]http.Handler{"original": agentTaskDetailStack(service, true), "scoped": agentTaskDetailStack(service, false), "router": newHandler(service)}
					statuses := map[int]int{}
					compared := 0
					for _, principal := range f.principals {
						for task, id := range f.tasks {
							path := "/api/agent-tasks/" + id + "?after="
							results := map[string]string{}
							for stack, handler := range stacks {
								w := httptest.NewRecorder()
								handler.ServeHTTP(w, agentTaskDetailRequest(f, principal, path))
								results[stack] = fmt.Sprintf("%d %s", w.Code, agentTaskDetailBody(w.Body.Bytes()))
								if stack == "original" {
									statuses[w.Code]++
								}
							}
							if want, pinned := agentTaskDetailExpected[principal.name+" "+task]; pinned && !strings.HasPrefix(results["original"], fmt.Sprint(want)+" ") {
								t.Errorf("%s reading %s: original HTTP %s, want %d", principal.name, task, results["original"][:3], want)
							}
							if results["original"] != results["scoped"] || results["original"] != results["router"] {
								t.Errorf("%s reading %s:\noriginal %s\nscoped   %s\nrouter   %s", principal.name, task, results["original"], results["scoped"], results["router"])
							}
							compared++
							if principal.mcp == nil {
								continue
							}
							mcpResult := func(value any, err error) string {
								raw, _ := json.Marshal(value)
								if err != nil {
									return "error " + err.Error()
								}
								return string(raw)
							}
							before := mcpResult(agentTaskDetailOriginalMCP(t.Context(), service, *principal.mcp, id))
							after := mcpResult(service.invokeMCPRoute(t.Context(), *principal.mcp, http.MethodGet, "/api/agent-tasks/"+url.PathEscape(id)+"?after=", map[string]string{"id": id}, nil, service.getApplicationAgentTask))
							if before != after {
								t.Errorf("MCP %s reading %s:\noriginal %s\nscoped   %s", principal.name, task, before, after)
							}
							compared++
						}
					}
					// The matrix must reach every outcome it is meant to compare.
					for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusUnauthorized} {
						if statuses[status] == 0 {
							t.Errorf("no request answered HTTP %d: %v", status, statuses)
						}
					}
					t.Logf("compared %d reads; original statuses %v", compared, statuses)
				})
			}
		})
	}
}
