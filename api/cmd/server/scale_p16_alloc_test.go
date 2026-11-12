//go:build loadtest

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The fixture, authentication and response encoding are included in each
// request measurement; seeding, warmup and GC are outside the timed region.
func TestScaleP16WriteAllocations(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	database := newScaleP04Database(t)
	data, _ := database.repo.BootstrapFor(database.key)
	if err := database.repo.SeedSyntheticMetadata(t.Context(), database.key, store.SyntheticMetadata{
		Projects: 20000, ProjectBytes: 2048, TeamIDs: []string{data.Teams[0].ID},
	}); err != nil {
		t.Fatal(err)
	}
	handler := database.handler(t, true)
	path := "/api/projects/" + data.Projects[0].ID
	type measurement struct {
		Mode       string  `json:"mode"`
		Pair       int     `json:"pair"`
		BytesPerOp uint64  `json:"bytesPerOp"`
		P95MS      float64 `json:"p95Ms"`
	}
	var report []measurement
	modes := []string{"off", "on"}
	if mode := os.Getenv("FLOW_P16_MEASURE_MODE"); mode != "" {
		modes = []string{mode}
	}
	for pair := range 3 {
		for _, mode := range modes {
			t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", mode)
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			write := func(i int) {
				requestJSON[domain.Project](t, handler, http.MethodPatch, path, map[string]string{"name": fmt.Sprintf("P16 %s %d %d", mode, pair, i)}, http.StatusOK)
			}
			write(-1)
			var allocated uint64
			var elapsed []float64
			for i := range 5 {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				start := time.Now()
				write(i)
				elapsed = append(elapsed, float64(time.Since(start))/float64(time.Millisecond))
				runtime.ReadMemStats(&after)
				allocated += after.TotalAlloc - before.TotalAlloc
			}
			slices.Sort(elapsed)
			report = append(report, measurement{mode, pair, allocated / 5, elapsed[len(elapsed)-1]})
		}
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("20,000 unrelated projects, whole HTTP request costs:\n%s", raw)
	if path := os.Getenv("FLOW_P16_ALLOCATION_REPORT"); path != "" {
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
