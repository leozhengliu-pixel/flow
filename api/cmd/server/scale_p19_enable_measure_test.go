package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The transfer fixture is opt-in; normal and shared-database runs use the
// smaller differential fixtures in scale_p19_enable_test.go.
func TestScaleP19EnableTransferMeasurement(t *testing.T) {
	if os.Getenv("FLOW_SCALE_P19_MEASURE") == "" {
		t.Skip("set FLOW_SCALE_P19_MEASURE to measure the 23,210-project fixture")
	}
	scaleP19Options(t)
	config := scaleP03DatabaseConfig(t)
	config.MaxTransactionBytes = 512 << 20
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	err = repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p19.seeded", "", nil, func(data *domain.Bootstrap) error {
		data.Projects, data.Initiatives = nil, nil
		now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		for i := range 23210 {
			data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p19-%05d", i), SlugID: fmt.Sprintf("p19-%05d", i), Name: fmt.Sprintf("Project %05d", 23210-i), Summary: strings.Repeat("s", 256), Description: strings.Repeat("b", 1024), TeamIDs: []string{data.Teams[0].ID}, Status: data.ProjectStatuses[i%len(data.ProjectStatuses)], Position: float64(i), Health: "noUpdate", CreatedAt: now, UpdatedAt: now})
		}
		for i := range 1000 {
			ids := []string{}
			for j := i; j < len(data.Projects); j += 1000 {
				ids = append(ids, data.Projects[j].ID)
			}
			data.Initiatives = append(data.Initiatives, domain.Initiative{ID: fmt.Sprintf("p19-i-%04d", i), SlugID: fmt.Sprintf("p19-i-%04d", i), Name: fmt.Sprintf("Initiative %04d", i), Description: strings.Repeat("i", 1024), Status: "active", ProjectIDs: ids, ParentInitiativeIDs: []string{}, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now})
		}
		data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		for i := range 2000 {
			data.ProjectUpdates["p19-00000"] = append(data.ProjectUpdates["p19-00000"], domain.ProjectUpdate{ID: fmt.Sprintf("p19-u-%04d", i), ProjectID: "p19-00000", Body: strings.Repeat("u", 2048), User: data.Viewer, Health: "onTrack", CreatedAt: now.Add(-time.Duration(i) * time.Hour)})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	type measurement struct {
		Path         string  `json:"path"`
		Bytes        int     `json:"bytes"`
		Milliseconds float64 `json:"milliseconds"`
	}
	pairs := [][]measurement{}
	projectQuery := scaleP19ProjectQuery()
	projectQuery.Display.Layout = "timeline"
	projectQuery.Display.Ordering = "Name"
	initiativeQuery := scaleP19ProjectQuery()
	initiativeQuery.View = "active"
	for pair := range 3 {
		rows := []measurement{}
		for _, path := range []string{"/api/bootstrap?issues=none", "/api/projects/p19-00000/updates", "/api/sync/manifest?surface=projects-page", scaleP19Path("projects", projectQuery, "", 100), scaleP19Path("initiatives", initiativeQuery, "", 100), "/api/projects/p19-00000/updates?window=1&limit=20"} {
			start := time.Now()
			w := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, f.data.Workspace.URLKey)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: status %d: %.400s", path, w.Code, w.Body.String())
			}
			if len(rows) >= 2 && w.Body.Len() > 256<<10 {
				t.Fatal("new response exceeded its byte budget")
			}
			row := measurement{Path: path, Bytes: w.Body.Len(), Milliseconds: float64(time.Since(start).Microseconds()) / 1000}
			rows = append(rows, row)
			t.Logf("pair=%d path=%s bytes=%d elapsed_ms=%.3f", pair+1, path, row.Bytes, row.Milliseconds)
		}
		if rows[2].Bytes+rows[3].Bytes+rows[4].Bytes > 768<<10 {
			t.Fatal("manifest plus two windows exceeded first-screen budget")
		}
		pairs = append(pairs, rows)
	}
	if err := os.MkdirAll("/tmp/codex-scale-p19-enable", 0755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(map[string]any{"method": "Three interleaved unchanged legacy endpoint / opt-in query-window pairs on the same 23,210-project, 1,000-initiative and 2,000-update fixture; transfer and request latency only, not a concurrency SLO", "pairs": pairs}, "", "  ")
	if err := os.WriteFile("/tmp/codex-scale-p19-enable/transfer.json", raw, 0644); err != nil {
		t.Fatal(err)
	}
}
