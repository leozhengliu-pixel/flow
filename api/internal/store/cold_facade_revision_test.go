package store

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A COLD legacy write under the coordinator never reloads the complete
// workspace from SQL: its facade's copy is exact at the installed
// generation's revision, and a peer's commit since is applied to the
// installed generation incrementally before the facade syncs. Its result
// carries the peer's change and its publication equals a fresh reload.
func TestColdFacadeCopyRevisionSkipsCurrentReload(t *testing.T) {
	f, view, workspace := coldIncrementalFixture(t)
	view.coordinator = scaleColdWriteCoordinator(func(_ context.Context, _ string, apply func() error) error { return apply() })
	at := time.Date(2026, 11, 19, 13, 0, 0, 0, time.UTC)
	var loads atomic.Int64
	observe := func(ctx context.Context) context.Context {
		return WithSQLStatementObserver(ctx, func(query string) {
			// loadWorkspaceStamped's read of the complete workspace.
			if strings.Contains(query, metadataStampColumns+",data FROM workspace_states") {
				loads.Add(1)
			}
		})
	}
	write := func(label string, step int) int64 {
		t.Helper()
		before := loads.Load()
		ctx := WithMutationScope(observe(WithColdWork(t.Context(), "cold-revision-test")), MutationScope{})
		if err := view.coldLegacyMutation(ctx, workspace, "api_key.revoked", nil, func(data *domain.Bootstrap) (string, error) {
			data.APIKeys[step%len(data.APIKeys)].RevokedAt = &at
			data.APIKeys[step%len(data.APIKeys)].Name = fmt.Sprintf("revoked %d", step)
			return "", nil
		}); err != nil {
			t.Fatal(label, err)
		}
		coldIncrementalAssertExact(t, f, view, workspace, label)
		return loads.Load() - before
	}
	for step := range 3 {
		if reloads := write(fmt.Sprintf("current %d", step), step); reloads != 0 {
			t.Fatalf("a COLD write on the current generation reloaded the workspace %d times", reloads)
		}
	}
	// A peer's commit the replica has not reloaded: the write reloads first
	// and its result carries the peer's change.
	if err := f.MutateWorkspace(t.Context(), workspace, "api_key.updated", "", nil, func(data *domain.Bootstrap) error {
		data.APIKeys[0].Name = "peer rename"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The installed generation is brought up to the peer's commit
	// incrementally first, so the writer's copy is current again.
	if reloads := write("after a peer commit", 1); reloads != 0 {
		t.Fatalf("a COLD write over a peer's commit reloaded the complete workspace %d times", reloads)
	}
	got := scaleColdTeamNative(t, view, workspace)
	if got.APIKeys[0].Name != "peer rename" {
		t.Fatalf("the peer's change is missing: %q", got.APIKeys[0].Name)
	}
}
