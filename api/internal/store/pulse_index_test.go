package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// B13: the Pulse index is rebuilt only when its inputs change (not after
// writes that merely copy the workspace), concurrent readers share one
// rebuild, and indexes of workspaces the store no longer holds are dropped.
func TestPulseIndexCacheRebuildsOnlyOnUpdateChanges(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	const workspace = "test-workspace"
	now := time.Now().UTC()
	err = repo.MutateWorkspace(WithMetadataFields(t.Context(), "projectUpdates"), workspace, "project.update_updated", "project_aut", nil, func(data *domain.Bootstrap) error {
		for i := range 50 {
			data.ProjectUpdates["project_aut"] = append(data.ProjectUpdates["project_aut"], domain.ProjectUpdate{ID: fmt.Sprintf("u%d", i), ProjectID: "project_aut", Body: "Body", CreatedAt: now.Add(-time.Duration(i) * time.Hour), Comments: []domain.Comment{}, Reactions: map[string][]string{}})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.PulseFeed(workspace); !ok {
		t.Fatal("no feed")
	}
	builds := repo.PulseIndexBuilds()
	// A full metadata write that copies every collection but changes no update.
	if err := repo.MutateWorkspace(t.Context(), workspace, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.FiscalMonth = "March"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() { defer wait.Done(); repo.PulseFeed(workspace) }()
	}
	wait.Wait()
	if got := repo.PulseIndexBuilds() - builds; got != 0 {
		t.Fatalf("index rebuilt %d times after a write that changed no update", got)
	}
	// A comment changes the index (comment count / Popular score).
	if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "projectUpdates"), workspace, "project.update_commented", "project_aut", nil, func(data *domain.Bootstrap) error {
		data.ProjectUpdates["project_aut"][3].Comments = append(data.ProjectUpdates["project_aut"][3].Comments, domain.Comment{ID: "c"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			feed, _ := repo.PulseFeed(workspace)
			for _, entry := range feed.Recent {
				if entry.UpdateID == "u3" && entry.Comments != 1 {
					t.Errorf("stale index entry %#v", entry)
				}
			}
		}()
	}
	wait.Wait()
	if got := repo.PulseIndexBuilds() - builds; got != 1 {
		t.Fatalf("concurrent readers after a comment built the index %d times, want 1", got)
	}
	repo.pulseIndexes.mu.Lock()
	repo.pulseIndexes.entries["gone-workspace"] = &pulseIndex{ready: make(chan struct{})}
	repo.pulseIndexes.mu.Unlock()
	repo.prunePulseIndexes()
	repo.pulseIndexes.mu.Lock()
	_, kept := repo.pulseIndexes.entries["gone-workspace"]
	repo.pulseIndexes.mu.Unlock()
	if kept {
		t.Fatal("index of a removed workspace was kept")
	}
}

func BenchmarkPulseIndexFingerprint(b *testing.B) {
	updates := map[string][]domain.ProjectUpdate{}
	for p := range 300 {
		for u := range 20 {
			updates[fmt.Sprintf("p%d", p)] = append(updates[fmt.Sprintf("p%d", p)], domain.ProjectUpdate{ID: fmt.Sprintf("u%d_%d", p, u), Body: "We finished the API migration and started the rollout to the first customers. Next week we focus on performance.", Comments: make([]domain.Comment, 2), Reactions: map[string][]string{"+1": {"a", "b"}}})
		}
	}
	b.ResetTimer()
	for range b.N {
		pulseIndexFingerprint(updates, nil, nil)
	}
}
