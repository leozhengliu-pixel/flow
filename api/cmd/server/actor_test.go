package main

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW17RequestActorDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, workspace := range []string{f.data.Workspace.URLKey, idgen.New("sw17-missing-")} {
				r := f.request(user, key, nil)
				r.Header.Set("X-Workspace-Key", workspace)
				r.SetPathValue("workspaceKey", workspace)
				old := requestActor(&server{store: repo}, r)
				current := requestActor(&server{store: f.probe}, r)
				if !reflect.DeepEqual(old, user) || !reflect.DeepEqual(current, user) {
					t.Fatal("an explicit authenticated actor changed with the workspace representation")
				}
			}
		})
		for _, workspace := range []string{f.data.Workspace.URLKey, idgen.New("sw17-missing-"), ""} {
			r := httptest.NewRequest("GET", "/api/scale-p21-sw17", nil)
			r.Header.Set("X-Workspace-Key", workspace)
			r.SetPathValue("workspaceKey", workspace)
			r.SetPathValue("workspace", workspace)
			old := requestActor(&server{store: repo, authDisabled: true}, r)
			current := requestActor(&server{store: f.probe, authDisabled: true}, r)
			if !reflect.DeepEqual(old, current) {
				t.Fatal("the auth-disabled stored viewer or missing-workspace result changed")
			}
		}
		r := f.request(f.data.Viewer, nil, nil)
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		if got := requestActor(&server{store: f.probe}, r.WithContext(ctx)); !reflect.DeepEqual(got, f.data.Viewer) {
			t.Fatal("an explicit actor unexpectedly depended on a storage query")
		}
		r = httptest.NewRequest("GET", "/api/scale-p21-sw17", nil)
		r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
		ctx, cancel = context.WithCancel(r.Context())
		cancel()
		if got := requestActor(&server{store: f.probe, authDisabled: true}, r.WithContext(ctx)); got.ID != "" {
			t.Fatal("cancelled bounded identity lookup returned an actor")
		}
		original := scaleP21M2UnrelatedHash(t, t.Context(), repo, f.data.Workspace.URLKey)
		user := requestActor(&server{store: f.probe}, f.request(f.data.Viewer, nil, nil))
		user.DisplayName = "Caller-owned actor"
		if scaleP21M2UnrelatedHash(t, t.Context(), repo, f.data.Workspace.URLKey) != original || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("actor lookup changed unrelated metadata or decoded complete metadata")
		}
	}
}
