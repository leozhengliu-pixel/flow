//go:build loadtest

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Seed and warmup are excluded; authentication, persistence, sequencing,
// event enrichment and HTTP encoding are included. Retained bytes are the
// live heap change after GC, independently of cumulative allocation bytes.
func TestScaleP16EnableEditMemory(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	db := newScaleP04Database(t)
	data, _ := db.repo.BootstrapFor(db.key)
	if err := db.repo.SeedSyntheticMetadata(t.Context(), db.key, store.SyntheticMetadata{
		Projects: 20000, ProjectBytes: 2048, TeamIDs: []string{data.Teams[0].ID},
	}); err != nil {
		t.Fatal(err)
	}
	handler := db.handler(t, true)
	initiative := requestJSON[domain.Initiative](t, handler, "POST", "/api/initiatives", map[string]any{"name": "Memory target", "projectIds": []string{data.Projects[0].ID}}, 201)
	type edit struct{ kind, path, field string }
	projectPath, initiativePath := "/api/projects/"+data.Projects[0].ID, "/api/initiatives/"+initiative.ID
	edits := []edit{{"project", projectPath, "name"}, {"initiative", initiativePath, "name"}}
	if os.Getenv("FLOW_P16_MEASURE_EDITS") == "all" {
		resource := requestJSON[domain.ProjectResource](t, handler, "POST", projectPath+"/resources", map[string]string{"url": "https://example.test/memory"}, 201)
		comment := requestJSON[domain.Comment](t, handler, "POST", projectPath+"/comments", map[string]string{"body": "Memory comment"}, 201)
		initiativeComment := requestJSON[domain.Comment](t, handler, "POST", initiativePath+"/comments", map[string]string{"body": "Memory comment"}, 201)
		update := requestJSON[domain.ProjectUpdate](t, handler, "POST", projectPath+"/updates", map[string]string{"body": "Memory update", "health": "onTrack"}, 201)
		edits = append(edits, edit{"project.summary", projectPath, "summary"}, edit{"project.description", projectPath, "description"}, edit{"project.date", projectPath, "startDate"}, edit{"project.resource", projectPath + "/resources/" + resource.ID, "title"}, edit{"project.comment", projectPath + "/comments/" + comment.ID, "body"}, edit{"project.update", projectPath + "/updates/" + update.ID, "body"}, edit{"initiative.summary", initiativePath, "summary"}, edit{"initiative.comment", initiativePath + "/comments/" + initiativeComment.ID, "body"})
	}
	type sample struct {
		Kind        string  `json:"kind"`
		Mode        string  `json:"mode"`
		Pair        int     `json:"pair"`
		Allocated   uint64  `json:"allocatedBytes"`
		Allocations uint64  `json:"allocations"`
		Retained    int64   `json:"retainedBytes"`
		LatencyMS   float64 `json:"latencyMs"`
	}
	var samples []sample
	dir := os.Getenv("FLOW_P16_ENABLE_REPORT_DIR")
	profile := func(name string) {
		if dir == "" {
			return
		}
		f, err := os.Create(filepath.Join(dir, name+".heap"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := pprof.WriteHeapProfile(f); err != nil {
			t.Fatal(err)
		}
	}
	for pair := range 5 {
		for _, mode := range []string{"off", "on"} {
			t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", mode)
			t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", mode)
			for _, target := range edits {
				write := func(i int) {
					value := fmt.Sprintf("Memory %s %d %d", mode, pair, i)
					if target.field == "startDate" {
						value = fmt.Sprintf("2026-11-%02d", i+2)
					}
					requestJSON[any](t, handler, http.MethodPatch, target.path, map[string]string{target.field: value}, http.StatusOK)
				}
				write(-1)
				runtime.GC()
				if pair == 0 && mode == "on" {
					profile(target.kind + "-before")
				}
				var before, after, retained runtime.MemStats
				runtime.ReadMemStats(&before)
				start := time.Now()
				write(pair)
				latency := time.Since(start)
				runtime.ReadMemStats(&after)
				runtime.GC()
				runtime.ReadMemStats(&retained)
				if pair == 0 && mode == "on" {
					profile(target.kind + "-after")
				}
				s := sample{target.kind, mode, pair + 1, after.TotalAlloc - before.TotalAlloc, after.Mallocs - before.Mallocs, int64(retained.HeapAlloc) - int64(before.HeapAlloc), float64(latency) / float64(time.Millisecond)}
				samples = append(samples, s)
				t.Logf("%+v", s)
				if os.Getenv("FLOW_P16_ENFORCE_MEMORY") == "1" && mode == "on" && (s.Allocated > 2<<20 || s.Retained > 2<<20) {
					t.Errorf("%s pair %d exceeds 2 MiB: %+v", target.kind, pair+1, s)
				}
			}
		}
	}
	if dir != "" {
		raw, err := json.MarshalIndent(samples, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "edit-memory.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
