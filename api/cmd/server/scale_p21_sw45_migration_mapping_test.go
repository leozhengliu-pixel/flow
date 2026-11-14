package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW45MigrationMappingOperation() scaleP21M3PointOperation {
	return scaleP21M3PointOperation{
		name: "sw45-migration-mapping", kind: "migrationJobs", id: "sw45-migration",
		body: `{"mappings":[{"entityType":"project","sourceId":"source","targetId":"target","targetName":"Mapped","action":"map"}]}`,
		seed: func(data *domain.Bootstrap, actor domain.User) {
			stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			data.MigrationJobs = []domain.MigrationJob{
				{
					ID: "sw45-migration", UserID: actor.ID, Filename: "Source", Source: "flow", Target: "flow", Status: "mapping",
					Counts: map[string]int{"projects": 1}, Errors: []string{"before"},
					Mappings: []domain.MigrationEntityMapping{{ID: "mapping", EntityType: "project", SourceID: "source", SourceName: "Source", Action: "create", Status: "pending", Error: "before"}},
					Bundle:   json.RawMessage(`{"input":"retained"}`), CreatedAt: stamp, UpdatedAt: stamp,
				},
				{ID: "sw45-other-migration", UserID: actor.ID, Status: "mapping", Filename: "Unrelated", Bundle: json.RawMessage(`{"input":"unrelated"}`)},
			}
		},
		handler:   (*server).updateMigrationMappings,
		otherKeys: []store.RecordKey{{Kind: "migrationJobs", ID: "sw45-other-migration"}},
	}
}

func TestScaleP21SW45MigrationMapping(t *testing.T) {
	normal := scaleP21SW45MigrationMappingOperation()
	invalid := normal
	invalid.name += "-invalid"
	invalid.seed = func(data *domain.Bootstrap, actor domain.User) {
		normal.seed(data, actor)
		data.MigrationJobs[0].Status = "completed"
	}
	unchanged := normal
	unchanged.name += "-unchanged"
	unchanged.body = `{"mappings":[{"entityType":"project","sourceId":"missing","targetId":"ignored","action":"map"}]}`
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{normal, invalid, unchanged})
}

func TestScaleP21SW45MigrationMappingBaseline(t *testing.T) {
	scaleP21SW45MigrationMappingMeasure(t, []bool{false})
}

func TestScaleP21SW45MigrationMappingMeasurement(t *testing.T) {
	scaleP21SW45MigrationMappingMeasure(t, []bool{false, true})
}

func scaleP21SW45MigrationMappingMeasure(t *testing.T, phases []bool) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		operation := scaleP21SW45MigrationMappingOperation()
		for _, bounded := range phases {
			if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21_sw45.mapping_seed", "", nil, func(data *domain.Bootstrap) error {
				operation.seed(data, f.data.Viewer)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			current := repo
			if bounded {
				var err error
				current, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
				if err != nil {
					t.Fatal(err)
				}
			}
			statements := 0
			request := scaleP21M3Request(f.data.Workspace.URLKey, f.data.Viewer, nil, operation.body)
			request.SetPathValue("id", operation.id)
			request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			started := time.Now()
			response := httptest.NewRecorder()
			scaleP21M3Call(&server{store: current}, response, request, operation.handler)
			elapsed := time.Since(started)
			runtime.ReadMemStats(&after)
			if response.Code != http.StatusOK {
				t.Fatal(response.Code, response.Body.String())
			}
			t.Logf("migration mapping bounded=%t allocated=%d sql=%d elapsed=%s", bounded, after.TotalAlloc-before.TotalAlloc, statements, elapsed)
		}
	}
}

func TestScaleP21SW45MigrationMappingFailureBoundary(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		operation := scaleP21SW45MigrationMappingOperation()
		for _, failure := range []string{"cancelled", "large"} {
			var responses []any
			for _, bounded := range []bool{false, true} {
				if failure == "large" && !bounded {
					continue
				}
				if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw45.mapping_failure", "", nil, func(data *domain.Bootstrap) error {
					operation.seed(data, f.data.Viewer)
					if failure == "large" {
						data.MigrationJobs[0].Mappings[0].SourceName = strings.Repeat("oversized source ", 40000)
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
				before := scaleP21M3Fingerprint(t, current, workspace, "migrationJobs")
				inputBefore := scaleP21SW45MigrationInputFingerprint(t, repo, workspace)
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
				if published != 0 || scaleP21M3Fingerprint(t, current, workspace, "migrationJobs") != before ||
					scaleP21SW45MigrationInputFingerprint(t, repo, workspace) != inputBefore {
					t.Fatalf("%s bounded=%t changed SQL/input or published", failure, bounded)
				}
				if failure == "large" && (response.Code < http.StatusBadRequest || !strings.Contains(response.Body.String(), "budget")) {
					t.Fatalf("large point lacks explicit budget failure: %d", response.Code)
				}
				responses = append(responses, []any{response.Code, scaleP21M3JSON(t, response.Body.Bytes())})
				if bounded && current.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("failed point write decoded full metadata")
				}
			}
			if failure == "cancelled" && !reflect.DeepEqual(responses[0], responses[1]) {
				t.Fatalf("cancelled error boundary differs: %v", responses)
			}
		}
	}
}
