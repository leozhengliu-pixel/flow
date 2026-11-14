package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW45ImportTransitionFailures(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, operation := range scaleP21SW45ImportOperations() {
			for _, failure := range []string{"foreign-owner", "invalid-status", "cancelled"} {
				var responses []any
				for _, bounded := range []bool{false, true} {
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw45.failure", "", nil, func(data *domain.Bootstrap) error {
						operation.seed(data, f.data.Viewer)
						if failure == "foreign-owner" {
							data.ImportJobs[0].UserID += "-another-person"
						}
						if failure == "invalid-status" {
							data.ImportJobs[0].Status = "completed"
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					current := repo
					if bounded {
						var err error
						current, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
					}
					before := scaleP21M3Fingerprint(t, current, workspace, "importJobs")
					published := 0
					current.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
					current.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
					request := scaleP21M3Request(workspace, f.data.Viewer, nil, operation.body)
					request.SetPathValue("id", operation.id)
					if failure == "cancelled" {
						ctx, cancel := context.WithCancel(request.Context())
						cancel()
						request = request.WithContext(ctx)
					}
					response := httptest.NewRecorder()
					operation.handler(&server{store: current}, response, request)
					current.SetWebhookSink(nil)
					current.SetRealtimeSink(nil)
					if published != 0 || scaleP21M3Fingerprint(t, current, workspace, "importJobs") != before {
						t.Fatalf("%s/%s bounded=%t changed SQL or published", operation.name, failure, bounded)
					}
					if failure == "foreign-owner" && response.Code != http.StatusNotFound ||
						failure == "invalid-status" && response.Code != http.StatusBadRequest {
						t.Fatalf("%s/%s status=%d", operation.name, failure, response.Code)
					}
					responses = append(responses, []any{response.Code, scaleP21M3JSON(t, response.Body.Bytes())})
				}
				if !reflect.DeepEqual(responses[0], responses[1]) {
					t.Fatalf("%s/%s error boundary differs: %v", operation.name, failure, responses)
				}
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw45.large", "", nil, func(data *domain.Bootstrap) error {
				operation.seed(data, f.data.Viewer)
				data.ImportJobs[0].Headers = []string{strings.Repeat("oversized header ", 40000)}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			before := scaleP21M3Fingerprint(t, probe, workspace, "importJobs")
			published := 0
			probe.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
			probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
			request := scaleP21M3Request(workspace, f.data.Viewer, nil, operation.body)
			request.SetPathValue("id", operation.id)
			response := httptest.NewRecorder()
			operation.handler(&server{store: probe}, response, request)
			probe.SetWebhookSink(nil)
			probe.SetRealtimeSink(nil)
			if response.Code < http.StatusBadRequest ||
				!strings.Contains(response.Body.String(), "budget") ||
				published != 0 || scaleP21M3Fingerprint(t, probe, workspace, "importJobs") != before {
				t.Fatalf("%s oversized point was truncated, committed, or lacked a budget error: status=%d", operation.name, response.Code)
			}
			if probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("failed point write decoded full metadata")
			}
		}
	}
}
