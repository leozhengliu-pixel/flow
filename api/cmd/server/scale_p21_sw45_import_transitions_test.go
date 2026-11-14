package main

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW45ImportOperations() []scaleP21M3PointOperation {
	var operations []scaleP21M3PointOperation
	for _, spec := range []struct {
		name, status string
		handler      func(*server, http.ResponseWriter, *http.Request)
	}{
		{"cancel", "running", (*server).cancelImport},
		{"retry", "failed", (*server).retryImport},
		{"resume", "cancelled", (*server).resumeImport},
	} {
		operations = append(operations, scaleP21M3PointOperation{
			name: "sw45-import-" + spec.name, kind: "importJobs", id: "sw45-import", body: `{}`,
			seed: func(data *domain.Bootstrap, actor domain.User) {
				stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				data.ImportJobs = []domain.ImportJob{
					{
						ID: "sw45-import", UserID: actor.ID, Status: spec.status, Filename: "Before",
						Headers: []string{"Title", "Owner"}, Mapping: map[string]string{"title": "Title"},
						Rows: []map[string]string{{"Title": "Retained input"}}, Error: "Failure",
						Errors: []string{"row failed"}, RowErrors: []domain.ImportRowError{{Row: 2, Message: "row failed"}},
						Checkpoint: 3, Progress: 4, Imported: 2, RetryCount: 1, CreatedAt: stamp, UpdatedAt: stamp,
					},
					{ID: "sw45-other-import", UserID: actor.ID, Status: "mapping", Filename: "Unrelated", Rows: []map[string]string{{"Title": "Unrelated uploaded input"}}},
				}
			},
			handler:   spec.handler,
			otherKeys: []store.RecordKey{{Kind: "importJobs", ID: "sw45-other-import"}},
		})
	}
	return operations
}

func TestScaleP21SW45ImportTransitions(t *testing.T) {
	scaleP21M3PointDifferential(t, scaleP21SW45ImportOperations())
}

func TestScaleP21SW45ImportTransitionBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		for _, operation := range scaleP21SW45ImportOperations() {
			if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21_sw45.seed", "", nil, func(data *domain.Bootstrap) error {
				operation.seed(data, f.data.Viewer)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			statements := 0
			request := scaleP21M3Request(f.data.Workspace.URLKey, f.data.Viewer, nil, operation.body)
			request.SetPathValue("id", operation.id)
			request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			started := time.Now()
			response := httptest.NewRecorder()
			scaleP21M3Call(&server{store: repo}, response, request, operation.handler)
			elapsed := time.Since(started)
			runtime.ReadMemStats(&after)
			if response.Code != http.StatusOK {
				t.Fatalf("%s baseline status=%d body=%s", operation.name, response.Code, response.Body.String())
			}
			t.Logf("%s bounded=false allocated=%d sql=%d elapsed=%s retained-input-response=%t", operation.name,
				after.TotalAlloc-before.TotalAlloc, statements, elapsed, strings.Contains(response.Body.String(), "Retained input"))
		}
	}
}
