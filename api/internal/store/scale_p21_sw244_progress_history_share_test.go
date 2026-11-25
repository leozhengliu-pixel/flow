package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Issue #244: snapshot clones share project progress histories, which no
// writer changes in place, instead of copying them.
func TestScaleP21SW244ProgressHistoriesAreShared(t *testing.T) {
	history := []domain.ProjectProgressHistoryPoint{{Date: time.Unix(0, 0).UTC(), Value: 1}, {Date: time.Unix(86400, 0).UTC(), Value: 2}}
	source := domain.Bootstrap{Projects: []domain.Project{{ID: "project", Name: "Project", TeamIDs: []string{"team"}, ProgressHistory: history, ScopeHistory: history[:1]}}}
	clone := cloneBootstrap(source)
	project := clone.Projects[0]
	if &project.ProgressHistory[0] != &history[0] || cap(project.ProgressHistory) != len(history) {
		t.Fatalf("progress history was copied or kept spare capacity: cap=%d", cap(project.ProgressHistory))
	}
	if cap(project.ScopeHistory) != 1 {
		t.Fatalf("a shared prefix kept the source capacity: cap=%d", cap(project.ScopeHistory))
	}
	// Other containers stay owned.
	clone.Projects[0].TeamIDs[0] = "changed"
	clone.Projects[0].Name = "Changed"
	if source.Projects[0].TeamIDs[0] != "team" || source.Projects[0].Name != "Project" {
		t.Fatal("clone shares project containers other than the histories")
	}
	// Appending through a clone never writes into the shared array.
	clone.Projects[0].ScopeHistory = append(clone.Projects[0].ScopeHistory, domain.ProjectProgressHistoryPoint{Value: 9})
	if history[1].Value != 2 {
		t.Fatal("append through a clone wrote into the shared history")
	}
	// The hydrated read clone shares them as well, including the buffered
	// large-collection path, and concurrent clones and readers do not race.
	large := make([]domain.Project, 4096)
	for i := range large {
		large[i] = domain.Project{ID: "p", ProgressHistory: history}
	}
	source.Projects = large
	var wait sync.WaitGroup
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			data := CloneHydratedReadSnapshot(context.Background(), source)
			total := 0.0
			for _, item := range data.Projects {
				total += item.ProgressHistory[len(item.ProgressHistory)-1].Value
				item.ProgressHistory = append(item.ProgressHistory, domain.ProjectProgressHistoryPoint{Value: 3})
			}
			if total != float64(2*len(large)) {
				t.Errorf("history total = %v", total)
			}
		}()
	}
	wait.Wait()
	if len(history) != 2 || history[1].Value != 2 {
		t.Fatal("concurrent clones changed the shared history")
	}
}
