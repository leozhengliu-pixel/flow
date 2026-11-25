package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// scaleP21SW266HoldBudget leaves less than one bootstrap admission of the
// source's work budget free, as concurrent large reads would.
func scaleP21SW266HoldBudget(t *testing.T, source *store.SQLiteStore) func() {
	t.Helper()
	holds := []*store.MetadataJSONRead{}
	release := func() {
		for _, hold := range holds {
			hold.Close()
		}
		holds = nil
	}
	for size := 16 << 20; size >= 16<<10; {
		hold, err := source.BeginMetadataJSONRead(context.Background())
		if err != nil {
			break
		}
		if err := hold.ReserveEncoded(context.Background(), size); err != nil {
			hold.Close()
			size /= 2
			continue
		}
		holds = append(holds, hold)
	}
	if len(holds) == 0 || source.MetadataWorkUsedForTesting() < (256<<20)-(1<<20) {
		release()
		t.Fatal("could not hold the work budget", source.MetadataWorkUsedForTesting())
	}
	return release
}

type scaleP21SW266Response struct {
	Status     int
	RetryAfter string
	Body       string
}

func scaleP21SW266Serve(t *testing.T, f scaleP21SW145Fixture, source *store.SQLiteStore, test scaleP21SW145Case) scaleP21SW266Response {
	t.Helper()
	service := &server{store: source, authDisabled: test.development}
	request := f.literalRequest(test.user, test.key, nil)
	request.Method, request.URL.Path = http.MethodGet, "/api/issue-records/bootstrap"
	request.Header.Set("X-Workspace-Key", test.workspace)
	request.SetPathValue("workspaceKey", test.workspace)
	request.SetPathValue("workspace", test.workspace)
	request.Header.Set("X-Flow-Projection", test.projection)
	request, closeOutput := beginRequestResourceReads(request)
	defer closeOutput()
	writer := httptest.NewRecorder()
	service.issueRecordsBootstrap(writer, request)
	return scaleP21SW266Response{writer.Code, writer.Header().Get("Retry-After"), writer.Body.String()}
}

func TestScaleP21SW266IssueBootstrapBoundedRefusals(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW145Seed(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
			if err != nil {
				t.Fatal(err)
			}
			cases := []scaleP21SW145Case{
				{name: "admin-private-outsider", user: f.principals[0].user},
				{name: "private-admin", user: f.principals[1].user},
				{name: "member", user: f.principals[2].user},
				{name: "guest-explicit", user: f.principals[3].user},
				{name: "inherited-selected", user: f.principals[4].user, key: &domain.APIKey{ID: "sw266-selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
				{name: "app-selected-empty", user: f.principals[0].user, key: &domain.APIKey{ID: "sw266-app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
				{name: "development", user: f.data.Viewer, development: true},
			}
			for _, test := range cases {
				for _, projection := range []string{"", "project-list", "issue-detail"} {
					test.workspace, test.projection = workspace, projection
					t.Run(fmt.Sprintf("reload=%t/%s/projection=%s", reload, test.name, projection), func(t *testing.T) {
						legacy := scaleP21SW266Serve(t, f, repo, test)
						if legacy.Status != http.StatusOK {
							t.Fatal("legacy bootstrap failed", legacy.Status, legacy.Body)
						}
						decodes := view.WorkspaceMetadataDecodes()
						release := scaleP21SW266HoldBudget(t, view)
						held := view.MetadataWorkUsedForTesting()
						refused := scaleP21SW266Serve(t, f, view, test)
						after := view.MetadataWorkUsedForTesting()
						release()
						var payload map[string]string
						if refused.Status != http.StatusServiceUnavailable || refused.RetryAfter != "2" ||
							json.Unmarshal([]byte(refused.Body), &payload) != nil || payload["error"] == "" {
							t.Fatalf("busy bootstrap is not a retryable 503: %+v", refused)
						}
						if after != held || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != decodes {
							t.Fatal("refused bootstrap retained work or decoded the workspace", held, after, view.MetadataWorkUsedForTesting())
						}
						// Free budget: the very same request is served, byte for byte.
						served := scaleP21SW266Serve(t, f, view, test)
						if !reflect.DeepEqual(served, legacy) || view.WorkspaceMetadataDecodes() != decodes || view.MetadataWorkUsedForTesting() != 0 {
							t.Fatalf("bounded bootstrap differs after the budget was released: status=%d/%d len=%d/%d", served.Status, legacy.Status, len(served.Body), len(legacy.Body))
						}
					})
				}
			}
		}
	}
}
