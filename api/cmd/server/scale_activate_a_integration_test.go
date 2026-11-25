package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleActivateADifferentFields(left, right []byte) []string {
	var a, b any
	_ = json.Unmarshal(left, &a)
	_ = json.Unmarshal(right, &b)
	fields := []string{}
	var walk func(string, any, any)
	walk = func(path string, a, b any) {
		if reflect.DeepEqual(a, b) {
			return
		}
		switch a := a.(type) {
		case map[string]any:
			other, ok := b.(map[string]any)
			if ok {
				for key, value := range a {
					walk(path+"."+key, value, other[key])
				}
				return
			}
		case []any:
			other, ok := b.([]any)
			if ok && len(a) == len(other) {
				for index := range a {
					walk(fmt.Sprintf("%s[%d]", path, index), a[index], other[index])
				}
				return
			}
		}
		fields = append(fields, path)
	}
	walk("$", a, b)
	slices.Sort(fields)
	return fields
}

func TestScaleActivateALegacyReadRollback(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := apiKeyScopeFixture(t, repo)
			workspace := f.data.Workspace.URLKey
			paths := []string{
				"/api/bootstrap", "/api/issue-records/bootstrap?projection=issue-detail",
				"/api/projects?limit=1&includeTotal=true", "/api/projects/scope-old-project",
				"/api/projects/scope-hidden-project", "/api/initiatives/scope-initiative",
				"/api/documents/scope-document", "/api/notifications?limit=50",
				"/api/issue-records/scope-issue", "/api/realtime/changes?since=0",
			}
			credentials := []struct{ name, token string }{{"cookie", ""}}
			for _, spec := range []struct {
				name, restriction string
				teams             []string
			}{
				{"unrestricted", "", nil}, {"selected-empty", "selected", []string{}},
				{"selected-set", "selected", []string{"scope-public"}},
			} {
				token, _ := apiKeyScopeCredential(t, f, "user", spec.restriction, spec.teams, []string{"read", "initiative:read"})
				credentials = append(credentials, struct{ name, token string }{spec.name, token})
			}
			// Drain first-use telemetry before fixing the comparison timestamps.
			for _, credential := range credentials {
				if credential.token != "" {
					scaleP11WarmCredential(t, repo, workspace, credential.token)
				}
			}
			// The shared credential fixture includes a pre-migration document and
			// unused keys. Materialize stable defaults before exact HTTP comparison.
			now := time.Now().UTC()
			if err := repo.MutateWorkspace(t.Context(), workspace, "test.activate_a_fixture", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Documents {
					document := &data.Documents[index]
					document.CreatedAt, document.UpdatedAt = now, now
					document.Permissions = []domain.DocumentPermission{{ID: "activation-owner", DocumentID: document.ID, SubjectType: "user", SubjectID: document.Creator.ID, Role: "owner", CreatedAt: now, UpdatedAt: now}}
				}
				for index := range data.APIKeys {
					data.APIKeys[index].LastUsedAt = &now
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, credential := range credentials {
				t.Run(credential.name, func(t *testing.T) {
					for _, path := range paths {
						t.Setenv("FLOW_SYNC_MANIFEST", "off")
						t.Setenv("FLOW_INDEXED_READS", "off")
						want := scaleP03Read(t, f, f.admin, credential.token, http.MethodGet, path, workspace)
						for _, mode := range []string{"on", "off"} {
							t.Setenv("FLOW_SYNC_MANIFEST", mode)
							t.Setenv("FLOW_INDEXED_READS", mode)
							got := scaleP03Read(t, f, f.admin, credential.token, http.MethodGet, path, workspace)
							if got.Code != want.Code || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
								t.Fatalf("%s mode=%s status=%d/%d changed legacy response fields=%v", path, mode, got.Code, want.Code, scaleActivateADifferentFields(want.Body.Bytes(), got.Body.Bytes()))
							}
						}
					}
				})
			}
		})
	}
}

func TestScaleActivateAManifestKeepsIncompleteProtocolDisabled(t *testing.T) {
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	t.Setenv("FLOW_SYNC_V2", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			response := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/sync/manifest", workspace)
			var manifest syncManifestResponse
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &manifest) != nil {
				t.Fatalf("manifest status=%d", response.Code)
			}
			if slices.Contains(manifest.Capabilities, "sync-v2") || manifest.Collections["projects"].Ready {
				t.Fatal("uncovered writer or incomplete sync protocol was advertised")
			}
			for path, want := range map[string]int{
				"/api/sync/changes?after=0":        http.StatusNotFound,
				"/api/sync/records?kind=projects":  http.StatusServiceUnavailable,
				"/api/sync/records?kind=documents": http.StatusBadRequest,
			} {
				if got := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace); got.Code != want {
					t.Fatalf("%s status=%d, want %d", path, got.Code, want)
				}
			}
			for _, flag := range []string{"FLOW_SYNC_MANIFEST", "FLOW_INDEXED_READS"} {
				t.Setenv(flag, "off")
				path := "/api/sync/manifest"
				if flag == "FLOW_INDEXED_READS" {
					path = "/api/sync/records?kind=projects"
				}
				if got := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace); got.Code != http.StatusNotFound {
					t.Fatalf("%s rollback status=%d", flag, got.Code)
				}
				t.Setenv(flag, "on")
			}
		})
	}
}
