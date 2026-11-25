package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW332EscapedMembershipAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Run("finite-opaque-post-boundary", func(t *testing.T) {
		for _, path := range []string{
			"/api/agent/sessions/a%2Fb/messages/stream",
			"/%61pi/%61gent/%73essions/a%2fb/%6dessages/%73tream",
			"/api/agent/sessions/%2F/messages/stream",
			"/api/agent/sessions/a%252Fb/messages/stream",
			"/api/agent/sessions/a%2Fb/approvals/c%2Fd",
			"/api/agent/sessions/%2F/approvals/%2F",
			"/api/workspaces/a%2Fb/teams", "/api/workspaces/%2F/teams",
			"/api/workspaces/%2Fa/teams", "/api/workspaces/a%2F/teams",
			"/%61pi/%77orkspaces/a%2fb/%74eams",
			"/api/workspaces/id%2Fteams/teams", "/api/workspaces/id%2Fteams%2Fprivate/teams",
		} {
			if !scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(http.MethodPost, path, nil)) {
				t.Fatal("registered opaque POST shape not selected", path)
			}
			for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
				if scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(method, path, nil)) {
					t.Fatal("opaque POST selector captured another method", method, path)
				}
			}
		}
		for _, path := range []string{
			"/api/agent/sessions//messages/stream", "/api/agent/sessions/id/messages/stream/",
			"/api/agent/sessions/id/messages", "/api/agent/sessions/id/messages/stream/child",
			"/api/agent/sessions/id/approvals/", "/api/agent/sessions//approvals/id",
			"/api/agent/sessions/id/approvals/id/child", "/api/agent/sessions/id/elicitations/id",
			"/api/agent/sessions/id/stop", "/api/agent/sessions%2Fextra/id/messages/stream",
			"/api/workspaces//teams", "/api/workspaces/id/teams/",
			"/api/workspaces/id/teams/child", "/api/workspaces%2Fextra/id/teams",
		} {
			if scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(http.MethodPost, path, nil)) {
				t.Fatal("opaque selector captured another predicate", path)
			}
		}
		for _, path := range []string{"/api/workflows/a%2Fb/run", "/%61pi/workflows/a%2Fb/%72un", "/api/workflows/%2F/run"} {
			if !scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(http.MethodPost, path, nil)) {
				t.Fatal("existing escaped workflow fix changed", path)
			}
		}
		// Upload basename-invalid names remain an audit limitation. This
		// batch does not widen the existing upload selector.
		for _, path := range []string{"/uploads/a%2Fb", "/uploads/%2F", "/uploads/a/b"} {
			if scaleP21SW306MembershipAuthorizationRequest(httptest.NewRequest(http.MethodGet, path, nil)) {
				t.Fatal("opaque POST change widened uploads", path)
			}
		}
	})
	t.Run("literal-workspace-team-source-pin", func(t *testing.T) {
		parsed, err := parser.ParseFile(token.NewFileSet(), "auth.go", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		text := func(node ast.Node) string {
			var buffer bytes.Buffer
			if err := format.Node(&buffer, token.NewFileSet(), node); err != nil {
				t.Fatal(err)
			}
			return buffer.String()
		}
		predicates := 0
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "resourceAllowedLegacy" {
				continue
			}
			for _, statement := range function.Body.List {
				selection, ok := statement.(*ast.SwitchStmt)
				if !ok || selection.Tag == nil || text(selection.Tag) != "parts[1]" {
					continue
				}
				for _, statement := range selection.Body.List {
					clause := statement.(*ast.CaseClause)
					for _, value := range clause.List {
						if text(value) == `"workspaces"` {
							predicates++
							if len(clause.Body) != 1 || text(clause.Body[0]) != "if len(parts) >= 5 && parts[3] == \"teams\" {\n\treturn teamRowAllowed(s.store.ResolveTeamID(workspace, parts[4]))\n}" {
								t.Fatal("literal workspace team predicate or alias resolution changed")
							}
						}
					}
				}
			}
		}
		if predicates != 1 {
			t.Fatal("literal workspace team predicate absent or duplicated", predicates)
		}
	})
	type output struct {
		ID        string `json:"id"`
		Approval  string `json:"approvalId"`
		Workspace string `json:"workspaceKey"`
	}
	type outcome struct {
		Allowed bool
		Status  int
		Body    string
		Pattern string
	}
	f := scaleP21SW10Setup(t)
	repo := f.repository
	t.Run(f.reader.config.Driver, func(t *testing.T) {
		foreign := scaleP03Fixture(t, repo)
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw332-")
		privateTeam, publicAlias, privateAlias := "", prefix+"PUBLIC", prefix+"PRIVATE"
		data, found := repo.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("M workspace missing")
		}
		for _, project := range data.Projects {
			if project.ID == "m2-unrelated" && len(project.TeamIDs) == 1 {
				privateTeam = project.TeamIDs[0]
			}
		}
		if privateTeam == "" {
			t.Fatal("M private team missing")
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw332_aliases", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Teams {
				switch data.Teams[index].ID {
				case f.public:
					data.Teams[index].Key = publicAlias
				case privateTeam:
					data.Teams[index].Key = privateAlias
				}
			}
			for _, id := range []string{f.public, privateTeam} {
				settings := data.TeamSettings[id]
				settings.TeamID, settings.SettingsPermission = id, "owners"
				data.TeamSettings[id] = settings
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		f.reader.key = workspace
		tokens := map[string]string{}
		for _, principal := range f.principals {
			_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
			if err != nil {
				t.Fatal(err)
			}
			tokens[principal.user.ID] = token
		}
		patterns := map[string]string{
			"messages":  "POST /api/agent/sessions/{id}/messages/stream",
			"approvals": "POST /api/agent/sessions/{id}/approvals/{approvalId}",
			"teams":     "POST /api/workspaces/{workspaceKey}/teams",
		}
		mux := http.NewServeMux()
		for _, pattern := range patterns {
			// Domain handlers retain their own target checks. This leaf records
			// the exact opaque values that survive workspace policy and routing.
			mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
				value := output{ID: r.PathValue("id"), Approval: r.PathValue("approvalId")}
				if strings.HasPrefix(r.Pattern, "POST /api/workspaces/") {
					value.Workspace = r.PathValue("workspaceKey")
				}
				writeJSON(w, http.StatusOK, value)
			})
		}
		pairs, successes, refusals, legacySQL, boundedSQL := 0, 0, 0, 0, 0
		privateAdminAllows, partialSetPublicAllows, partialSetPrivateDenials := 0, 0, 0
		for index, row := range []struct {
			name, kind, ref, approval, gate string
			encoded, foreign                bool
		}{
			{"messages-exact", "messages", prefix + "session", "", "", false, false},
			{"messages-slash", "messages", prefix + "session/child", "", "", false, false},
			{"messages-url-alias", "messages", prefix + "session/child", "", "", true, false},
			{"messages-root", "messages", "/", "", "", false, false},
			{"messages-literal-percent", "messages", prefix + "session%2Fchild", "", "", false, false},
			{"approvals-both-slash", "approvals", prefix + "session/child", prefix + "approval/child", "", false, false},
			{"approvals-root", "approvals", "/", "/", "", true, false},
			{"approvals-literal-percent", "approvals", prefix + "session%2Fchild", prefix + "approval%2Fchild", "", false, false},
			{"workspace-exact", "teams", workspace, "", "", false, false},
			{"workspace-opaque-suffix", "teams", workspace + "/opaque", "", "", true, false},
			{"workspace-root", "teams", "/", "", "", false, false},
			{"workspace-leading-slash", "teams", "/" + workspace, "", "", false, false},
			{"workspace-trailing-slash", "teams", workspace + "/", "", "", false, false},
			{"agent-foreign-header", "messages", prefix + "session/child", "", "", false, true},
			{"workspace-foreign-path", "teams", foreign.data.Workspace.URLKey + "/opaque", "", "", false, false},
			{"decoded-admin-gate", "messages", prefix + "session/members/child", "", "admin", false, false},
			{"decoded-label-gate", "messages", prefix + "session/labels/child", "", "admin", false, false},
			{"team-create-admin-gate", "teams", workspace + "/opaque", "", "team-admins", false, false},
			{"agent-ai-disabled", "messages", prefix + "session/child", "", "ai-off", false, false},
			{"agent-guest-policy", "approvals", prefix + "session/child", prefix + "approval/child", "guest-agent", false, false},
			{"workspace-team-public-id", "teams", workspace + "/teams/" + f.public, "", "public-row", false, false},
			{"workspace-team-public-alias", "teams", workspace + "/teams/" + strings.ToLower(publicAlias), "", "public-row", true, false},
			{"workspace-team-private-id", "teams", workspace + "/teams/" + privateTeam, "", "private-row", false, false},
			{"workspace-team-private-alias", "teams", workspace + "/teams/" + strings.ToLower(privateAlias), "", "private-row", true, false},
			{"workspace-team-missing", "teams", workspace + "/teams/" + prefix + "missing", "", "missing-row", false, false},
			{"workspace-team-empty-ref", "teams", workspace + "/teams/", "", "missing-row", false, false},
		} {
			t.Run(row.name, func(t *testing.T) {
				t.Setenv("FLOW_RECORD_ACCESS", []string{"off", "shadow", "on"}[index%3])
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw332_policy", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.TeamCreatePermission = "members"
					if row.gate == "team-admins" {
						data.WorkspaceSettings.TeamCreatePermission = "admins"
					}
					data.WorkspaceSettings.LabelPermission = "admins"
					data.WorkspaceSettings.HIPAACompliance = false
					data.WorkspaceSettings.PreventGuestAgents = row.gate == "guest-agent"
					if data.WorkspaceSettings.FeatureFlags == nil {
						data.WorkspaceSettings.FeatureFlags = map[string]bool{}
					}
					data.WorkspaceSettings.FeatureFlags["ai-agent"] = row.gate != "ai-off"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for _, phase := range []string{"warm", "reloaded"} {
					t.Run(phase, func(t *testing.T) {
						f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
							var want outcome
							beforeSQL := 0
							for _, source := range []*store.SQLiteStore{repo, f.probe} {
								if source.BoundedMetadataEnabled() {
									if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
										t.Fatal(err)
									}
								}
								if phase == "reloaded" {
									scaleP21SW201ReloadSource(t, f.reader, source)
								}
								request := f.literalRequest(user, key, nil)
								path := "/api/agent/sessions/" + url.PathEscape(row.ref) + "/messages/stream"
								expected := output{ID: row.ref}
								if row.kind == "approvals" {
									path = "/api/agent/sessions/" + url.PathEscape(row.ref) + "/approvals/" + url.PathEscape(row.approval)
									expected.Approval = row.approval
								} else if row.kind == "teams" {
									path = "/api/workspaces/" + url.PathEscape(row.ref) + "/teams"
									expected = output{Workspace: row.ref}
								}
								if row.encoded {
									path = strings.Replace(path, "/api/", "/%61pi/", 1)
									path = strings.Replace(path, "/agent/sessions/", "/%61gent/%73essions/", 1)
									path = strings.Replace(path, "/workspaces/", "/%77orkspaces/", 1)
									path = strings.Replace(path, "/messages/stream", "/%6dessages/%73tream", 1)
									path = strings.Replace(path, "/approvals/", "/%61pprovals/", 1)
									if strings.HasSuffix(path, "/teams") {
										path = strings.TrimSuffix(path, "/teams") + "/%74eams"
									}
									path = strings.ReplaceAll(path, "%2F", "%2f")
								}
								var err error
								request.URL, err = url.Parse(path)
								if err != nil {
									t.Fatal(err)
								}
								request.Method = http.MethodPost
								// Workspace path values belong to the real mux leaf. Retain
								// the literal middleware's decoded path/header selection.
								request.SetPathValue("workspaceKey", "")
								request.SetPathValue("workspace", "")
								if row.foreign {
									request.Header.Set("X-Workspace-Key", foreign.data.Workspace.URLKey)
								}
								request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
								request, release := beginRequestResourceReads(request)
								service := &server{store: source}
								if !source.BoundedMetadataEnabled() {
									if _, handled := service.boundedMembershipAuthorization(request, workspace, user.ID); handled {
										release()
										t.Fatal("opaque membership adapter changed flag-off behavior")
									}
								}
								statements := 0
								request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
								recorder := httptest.NewRecorder()
								allowed := service.authorizeWorkspaceRequest(recorder, request, user)
								pattern := ""
								if allowed {
									_, pattern = mux.Handler(request)
									mux.ServeHTTP(recorder, request)
								}
								got := outcome{allowed, recorder.Code, recorder.Body.String(), pattern}
								guest := user.ID == f.principals[3].user.ID
								admin := user.ID == f.principals[0].user.ID || user.ID == f.principals[1].user.ID
								required := !row.foreign && row.name != "workspace-foreign-path"
								if row.kind == "teams" || row.gate == "guest-agent" {
									required = required && !guest
								}
								if row.gate == "admin" || row.gate == "team-admins" {
									required = required && admin
								}
								if strings.HasSuffix(row.gate, "-row") {
									target := f.public
									if row.gate == "private-row" {
										target = privateTeam
									}
									required = required && admin && row.gate != "missing-row"
									if key != nil && apiKeyTeamRestrictionSelected(*key) {
										required = required && slices.Contains(key.TeamIDs, target)
									}
								}
								if row.gate == "ai-off" {
									required = false
								}
								var payload output
								leafMatches := recorder.Code == http.StatusOK && pattern == patterns[row.kind] && json.Unmarshal(recorder.Body.Bytes(), &payload) == nil && payload == expected
								if row.name == "messages-root" || row.name == "approvals-root" || row.name == "workspace-root" {
									// GoMux rejects "/" as a single wildcard after unescaping.
									leafMatches = recorder.Code == http.StatusNotFound && recorder.Body.String() == "404 page not found\n" && pattern == ""
								}
								if allowed != required || allowed && !leafMatches || !allowed && recorder.Code != http.StatusForbidden {
									release()
									t.Fatalf("required=%t exact opaque output=%+v got=%+v", required, expected, got)
								}
								if !source.BoundedMetadataEnabled() {
									want, beforeSQL = got, statements
								} else {
									pairs++
									legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+statements
									if !reflect.DeepEqual(want, got) {
										release()
										t.Fatalf("opaque membership policy/output differs: literal=%+v bounded=%+v", want, got)
									}
									if allowed {
										successes++
										if row.gate == "private-row" && user.ID == f.principals[0].user.ID {
											privateAdminAllows++
										}
										if row.gate == "public-row" && key != nil && apiKeyTeamRestrictionSelected(*key) && len(key.TeamIDs) != 0 {
											partialSetPublicAllows++
										}
									} else {
										refusals++
										if row.gate == "private-row" && admin && key != nil && apiKeyTeamRestrictionSelected(*key) && len(key.TeamIDs) != 0 {
											partialSetPrivateDenials++
										}
									}
									if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
										release()
										t.Fatal("opaque membership auth created a complete paged handoff")
									}
								}
								release()
								release()
								if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
									t.Fatal("opaque membership auth leaked work or decoded complete metadata")
								}
							}
						})
					})
				}
			})
		}
		if pairs != 1040 || successes == 0 || refusals == 0 || privateAdminAllows == 0 || partialSetPublicAllows == 0 || partialSetPrivateDenials == 0 {
			t.Fatal("opaque membership M table lost required controls", pairs, successes, refusals)
		}
		t.Logf("SW332 opaque POST policy/output pairs=%d SQL literal/bounded=%d/%d success/refusal=%d/%d", pairs, legacySQL, boundedSQL, successes, refusals)
	})
}
