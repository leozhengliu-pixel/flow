package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// These tests exercise migrated handler boundaries on a policy-only store.
// The remaining HTTP authorization adapter stays separately registered as a
// blocker; this fixture must not bypass the production activation guard.
func TestScaleP21ScopedAccessorWorkedExamples(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	enableConnectorSecrets(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		public, private, child := idgen.New("p21-public-"), idgen.New("p21-private-"), idgen.New("p21-child-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
			data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
			data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
			data.Settings["applicationPolicies"] = []applicationPolicy{{ID: "p21-shared", Name: "Shared", Kind: "oauth", Shared: true, Status: "approved", OwnerID: data.Viewer.ID}}
			data.Projects = append(data.Projects, domain.Project{ID: "p21-unrelated", Description: strings.Repeat("unrelated body ", 30000)})
			data.WorkspaceSettings.RestrictFileUploads = true
			data.WorkspaceSettings.AllowedFileExtensions = []string{"txt"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		principals := []struct {
			name string
			user domain.User
		}{{"admin-outside-private", f.data.Viewer}}
		for _, spec := range []struct {
			name, role string
			teams      []string
		}{
			{"admin", "admin", []string{private}}, {"member-private-outsider", "member", []string{public}},
			{"guest-explicit", "guest", []string{public}}, {"sub-team-inheritance", "member", []string{private}},
		} {
			_, user := scaleP03Join(t, f, spec.name, idgen.New("p21-example-")+"@example.test", spec.role, spec.teams...)
			principals = append(principals, struct {
				name string
				user domain.User
			}{spec.name, user})
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		legacy, bounded := &server{store: repo}, &server{store: probe}
		request := func(principal domain.User, key *domain.APIKey, method, path string, body any) *http.Request {
			var raw []byte
			if body != nil {
				raw, _ = json.Marshal(body)
			}
			r := httptest.NewRequest(method, path, bytes.NewReader(raw))
			r.Header.Set("X-Workspace-Key", workspace)
			ctx := context.WithValue(r.Context(), authUserContextKey{}, principal)
			ctx = store.ContextWithActor(ctx, principal)
			if key != nil {
				ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
			}
			return r.WithContext(ctx)
		}
		call := func(s *server, r *http.Request, handler func(*server, http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			if strings.Contains(r.URL.Path, "/audit-log-stream") {
				core, ok := s.store.WorkspaceSettingsMetadata(workspace)
				role, status, err := s.store.WorkspaceRole(r.Context(), core.Workspace.ID, authUser(r).ID)
				if !ok || err != nil || status != "active" || !workspaceAdminRole(role) {
					writeError(w, http.StatusForbidden, "workspace administrator access required")
					return w
				}
			}
			handler(s, w, r)
			return w
		}
		for _, principal := range principals {
			for _, credential := range []struct {
				name string
				key  *domain.APIKey
			}{
				{"session", nil},
				{"unrestricted", &domain.APIKey{ID: "p21-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
				{"selected-empty", &domain.APIKey{ID: "p21-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
				{"selected-set", &domain.APIKey{ID: "p21-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{public}}},
			} {
				t.Run(principal.name+"/"+credential.name, func(t *testing.T) {
					body := map[string]any{"id": "p21-shared", "name": "Shared", "kind": "oauth", "status": "approved", "shared": true}
					for _, operation := range []struct {
						method, path string
						body         any
						handler      func(*server, http.ResponseWriter, *http.Request)
					}{
						{http.MethodGet, "/api/application-policies", nil, (*server).listApplicationPolicies},
						{http.MethodPut, "/api/application-policies", body, (*server).saveApplicationPolicy},
						{http.MethodPost, "/api/applications/p21-missing/uninstall", nil, (*server).uninstallApplicationInstallation},
						{http.MethodGet, "/api/workspace/audit-log-stream", nil, (*server).getAuditStream},
					} {
						reset := func() {
							if operation.method != http.MethodPut {
								return
							}
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
							if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.policy_reset", "", nil, func(data *domain.Bootstrap) error {
								data.Settings["applicationPolicies"] = []applicationPolicy{{ID: "p21-shared", Name: "Before", Kind: "oauth", Shared: true, Status: "approved", OwnerID: f.data.Viewer.ID}}
								return nil
							}); err != nil {
								t.Fatal(err)
							}
						}
						reset()
						a := request(principal.user, credential.key, operation.method, operation.path, operation.body)
						a.SetPathValue("id", "p21-missing")
						before := call(legacy, a, operation.handler)
						// Reset the comparison's settings through the legacy writer;
						// the bounded view intentionally retains no settings body.
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						reset()
						b := request(principal.user, credential.key, operation.method, operation.path, operation.body)
						b.SetPathValue("id", "p21-missing")
						after := call(bounded, b, operation.handler)
						if before.Code != after.Code || !reflect.DeepEqual(scaleP21ExampleJSON(t, before.Body.Bytes()), scaleP21ExampleJSON(t, after.Body.Bytes())) {
							t.Fatalf("%s %s changed: status=%d/%d body=%s/%s", operation.method, operation.path, before.Code, after.Code, before.Body.Bytes(), after.Body.Bytes())
						}
					}
					var auditResults [][]int
					for _, service := range []*server{legacy, bounded} {
						codes := []int{}
						created := call(service, request(principal.user, credential.key, http.MethodPost, "/api/workspace/audit-log-stream", map[string]any{"url": "https://example.test/audit", "secret": "p21-matrix-secret-012345"}), (*server).createAuditStream)
						codes = append(codes, created.Code)
						if created.Code == http.StatusCreated {
							updated := call(service, request(principal.user, credential.key, http.MethodPatch, "/api/workspace/audit-log-stream", map[string]any{"enabled": false}), (*server).updateAuditStream)
							codes = append(codes, updated.Code)
							deleted := call(service, request(principal.user, credential.key, http.MethodDelete, "/api/workspace/audit-log-stream", nil), (*server).deleteAuditStream)
							codes = append(codes, deleted.Code)
						}
						auditResults = append(auditResults, codes)
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					if !reflect.DeepEqual(auditResults[0], auditResults[1]) {
						t.Fatalf("audit stream permissions differ: %v", auditResults)
					}
					for _, file := range []string{"report.txt", "script.exe", "photo.png"} {
						input := "plain text"
						if file == "photo.png" {
							input = "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 600)
						}
						left, leftErr := legacy.checkUploadPolicy(workspace, file, strings.NewReader(input))
						right, rightErr := bounded.checkUploadPolicy(workspace, file, strings.NewReader(input))
						if (leftErr == nil) != (rightErr == nil) {
							t.Fatal("upload policy changed")
						}
						if leftErr == nil {
							a, _ := io.ReadAll(left)
							b, _ := io.ReadAll(right)
							if !bytes.Equal(a, b) {
								t.Fatal("upload preview consumed data")
							}
						}
					}
				})
			}
		}
		// Audit stream exercises the aggregate creator, complete field delete,
		// audit staging, public secret stripping and the background point edit.
		var createdID string
		for _, service := range []*server{legacy, bounded} {
			r := request(f.data.Viewer, nil, http.MethodPost, "/api/workspace/audit-log-stream", map[string]any{"url": "https://example.test/audit", "secret": "p21-fixture-secret-012345"})
			w := call(service, r, (*server).createAuditStream)
			if w.Code != http.StatusCreated {
				t.Fatalf("audit create status=%d: %s", w.Code, w.Body.Bytes())
			}
			var created domain.Webhook
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			createdID = created.ID
			service.mutateAuditStream(workspace, createdID, func(item *domain.Webhook) { item.AuditCursorID = "p21-cursor"; item.AuditCursorSeq = 7 })
			status := call(service, request(f.data.Viewer, nil, http.MethodGet, "/api/workspace/audit-log-stream", nil), (*server).getAuditStream)
			if status.Code != http.StatusOK || !bytes.Contains(status.Body.Bytes(), []byte("p21-cursor")) || bytes.Contains(status.Body.Bytes(), []byte("p21-fixture-secret")) {
				t.Fatalf("audit read or secret redaction failed: %s", status.Body.Bytes())
			}
			deleted := call(service, request(f.data.Viewer, nil, http.MethodDelete, "/api/workspace/audit-log-stream", nil), (*server).deleteAuditStream)
			if deleted.Code != http.StatusNoContent {
				t.Fatalf("audit delete failed: %s", deleted.Body.Bytes())
			}
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("worked example decoded a workspace")
		}
		member := principals[len(principals)-1].user
		stale, err := probe.ResolveAccess(t.Context(), workspace, member.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replay"} {
			t.Run(phase, func(t *testing.T) {
				if err := probe.ValidateAccess(t.Context(), stale); err == nil {
					t.Fatal("revoked authority survived")
				}
				for _, service := range []*server{legacy, bounded} {
					if _, err := service.store.ResolveAccess(t.Context(), workspace, member.ID, nil); err == nil {
						t.Fatal("revoked principal retained scoped settings access")
					}
				}
			})
		}
	}
}

func scaleP21ExampleJSON(t *testing.T, raw []byte) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if object, ok := result.(map[string]any); ok {
		delete(object, "updatedAt")
	}
	return result
}
