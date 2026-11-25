package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21CallWithOutputOwner(s *server, w http.ResponseWriter, r *http.Request, handler func(*server, http.ResponseWriter, *http.Request)) {
	if s.store.BoundedMetadataEnabled() {
		var close func()
		r, close = beginRequestResourceReads(r)
		defer close()
	}
	handler(s, w, r)
}

func TestScaleP21RequestStoreOutputOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, project := f.data.Workspace.URLKey, idgen.New("request-output-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.request_output", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = append(data.Projects, domain.Project{ID: project, Description: "owned output", TeamIDs: []string{data.Teams[0].ID}})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		request, close := beginRequestResourceReads(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))
		defer close()
		data, allowed, err := view.ResourceReadMetadata(request.Context(), access, "projects", project)
		if err != nil || !allowed || len(data.Projects) != 1 {
			t.Fatal("request scope did not register store output ownership", allowed, err)
		}
		cancel()
		if _, err := json.Marshal(data); err != nil || view.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("cancellation released output before encoding", err)
		}
		close()
		close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("request scope did not release store output")
		}
	}
}
