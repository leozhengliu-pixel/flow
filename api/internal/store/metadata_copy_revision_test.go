package store

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

// copyRevisionPair is two servers on one database with the coordinator, and a
// workspace whose copy on the first is current.
func copyRevisionPair(t *testing.T) (*SQLiteStore, *SQLiteStore, string) {
	t.Helper()
	pair := peerStoreSets(t)["sqlite"]
	workspace, _ := peerWorkspace(t, pair[0])
	for _, repo := range pair {
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
	}
	return pair[0], pair[1], workspace
}

// countDecodes runs step and returns how many workspace metadata documents
// repo decoded meanwhile.
func countDecodes(t *testing.T, repo *SQLiteStore, step func()) int64 {
	t.Helper()
	before := repo.WorkspaceMetadataDecodes()
	step()
	return repo.WorkspaceMetadataDecodes() - before
}

// renameStoredProject renames a project the way a writer without the
// workspace lock does: one record transaction, which moves the metadata
// revision.
func renameStoredProject(t *testing.T, repo *SQLiteStore, workspace, id, name string) {
	t.Helper()
	ctx := context.Background()
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var project map[string]any
	if err := json.Unmarshal(raw, &project); err != nil {
		t.Fatal(err)
	}
	project["name"] = name
	raw, _ = json.Marshal(project)
	if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`, raw, workspace, id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func copyProjectName(t *testing.T, repo *SQLiteStore, workspace, id string) string {
	t.Helper()
	data, ok := repo.WorkspaceMetadata(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
	if index < 0 {
		return ""
	}
	return data.Projects[index].Name
}

// reloadFromRows decodes the workspace from the database even when this
// server's copy is current (a reload then keeps the copy).
func reloadFromRows(t *testing.T, repo *SQLiteStore, workspace string) {
	t.Helper()
	repo.mu.Lock()
	repo.forgetCopyRevisionLocked(workspace)
	repo.mu.Unlock()
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
}

func hasCopyRevision(repo *SQLiteStore, workspace string) bool {
	_, ok := repo.copyRevisionOf(workspace)
	return ok
}

// Reloads and the coordinator's reload before a write decode the workspace
// only when its metadata changed; this server's own writes leave a copy
// that is still current.
func TestUnchangedMetadataRevisionSkipsTheDecode(t *testing.T) {
	first, _, workspace := copyRevisionPair(t)
	if n := countDecodes(t, first, func() {
		for range 3 {
			if err := first.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
	}); n != 0 {
		t.Fatalf("reloads of an unchanged workspace decoded %d times", n)
	}
	var project string
	if n := countDecodes(t, first, func() { project = raceProject(t, first, workspace) }); n != 0 {
		t.Fatalf("a write on a current copy decoded %d times", n)
	}
	if !hasCopyRevision(first, workspace) {
		t.Fatal("the write's copy has no revision")
	}
	if n := countDecodes(t, first, func() {
		raceProject(t, first, workspace)
		if err := first.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := first.ReloadAllWorkspaces(t.Context()); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Fatalf("writes and reloads after this server's own write decoded %d times", n)
	}
	// The copy kept is the stored one.
	stored := storedProject(t, first, workspace, project)
	if name := copyProjectName(t, first, workspace, project); name != stored.Name {
		t.Fatalf("copy has project %q, database %q", name, stored.Name)
	}
}

// A change another server commits is decoded by the next reload and by the
// next write here.
func TestMetadataChangeOnAnotherServerIsReloaded(t *testing.T) {
	first, second, workspace := copyRevisionPair(t)
	project := raceProject(t, first, workspace)
	renameStoredProject(t, second, workspace, project, "Renamed elsewhere")
	if n := countDecodes(t, first, func() {
		if err := first.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
	}); n != 1 {
		t.Fatalf("reload after another server's change decoded %d times, want 1", n)
	}
	if name := copyProjectName(t, first, workspace, project); name != "Renamed elsewhere" {
		t.Fatalf("copy after reload has %q", name)
	}

	// The next write reloads too, and keeps the other server's change.
	other := raceProject(t, second, workspace)
	if n := countDecodes(t, first, func() { raceProject(t, first, workspace) }); n != 1 {
		t.Fatalf("write after another server's change decoded %d times, want 1", n)
	}
	if copyProjectName(t, first, workspace, other) == "" {
		t.Fatal("the write's copy lost the other server's project")
	}

	// So does a read snapshot, and every-workspace reload.
	renameStoredProject(t, second, workspace, project, "Renamed again")
	if n := countDecodes(t, first, func() {
		ctx, release, err := first.BeginWorkspaceRead(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		_, data, _ := first.workspaceReadSource(ctx, workspace)
		release()
		index := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project })
		if index < 0 || data.Projects[index].Name != "Renamed again" {
			t.Fatal("the read snapshot misses the other server's change")
		}
	}); n != 1 {
		t.Fatalf("read snapshot after a change decoded %d times, want 1", n)
	}
	if n := countDecodes(t, first, func() {
		if err := first.ReloadAllWorkspaces(t.Context()); err != nil {
			t.Fatal(err)
		}
	}); n != 1 {
		t.Fatalf("reload of every workspace decoded %d times, want 1 (the changed one)", n)
	}
	if name := copyProjectName(t, first, workspace, project); name != "Renamed again" {
		t.Fatalf("copy after reloading every workspace has %q", name)
	}
	// Current again: the read snapshot shares the copy.
	if n := countDecodes(t, first, func() {
		_, release, err := first.BeginWorkspaceRead(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}); n != 0 {
		t.Fatalf("read snapshot of a current copy decoded %d times", n)
	}
}

type interleaveKey struct{}

// Another server's transaction commits while this server writes, before or
// after this server's own transaction: the copy this write installs must not
// claim a revision that counts the other change. The next check reloads.
func TestInterleavedCommitLeavesNoStaleRevision(t *testing.T) {
	t.Run("before the write commits", func(t *testing.T) {
		first, second, workspace := copyRevisionPair(t)
		project := raceProject(t, first, workspace)
		err := first.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "project.created", "interleaved", domain.ProjectMutationInput{}, func(data *domain.Bootstrap) error {
			// The base is loaded; another server's record write commits now.
			renameStoredProject(t, second, workspace, project, "Renamed meanwhile")
			added := data.Projects[0]
			added.ID, added.SlugID, added.Name = "interleaved_project", "interleaved_project", "Interleaved"
			data.Projects = append(data.Projects, added)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if hasCopyRevision(first, workspace) {
			t.Fatal("the write recorded a revision although another server committed in between")
		}
		if n := countDecodes(t, first, func() {
			if err := first.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}); n != 1 {
			t.Fatalf("reload decoded %d times, want 1", n)
		}
		if name := copyProjectName(t, first, workspace, project); name != "Renamed meanwhile" {
			t.Fatalf("copy has %q", name)
		}
		if copyProjectName(t, first, workspace, "interleaved_project") == "" {
			t.Fatal("copy lost the write")
		}
	})
	t.Run("after the write commits", func(t *testing.T) {
		first, second, workspace := copyRevisionPair(t)
		project := raceProject(t, first, workspace)
		var once sync.Once
		first.db.commitHooks.Store(&commitHooks{afterCommit: func(tx *sqlTx) {
			if tx.context().Value(interleaveKey{}) != nil {
				once.Do(func() { renameStoredProject(t, second, workspace, project, "Renamed after") })
			}
		}})
		t.Cleanup(func() { first.db.commitHooks.Store(nil) })
		raceProject(t, first, workspace) // a current base
		ctx := context.WithValue(t.Context(), interleaveKey{}, true)
		if err := first.MutateWorkspace(WithMetadataFields(ctx, "projects"), workspace, "project.updated", project, domain.ProjectMutationInput{}, func(data *domain.Bootstrap) error {
			index := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project })
			data.Projects[index].Description = "written here"
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		first.db.commitHooks.Store(nil)
		// The copy may carry the revision the write reached, but the stored
		// one is past it: the next check reloads.
		if n := countDecodes(t, first, func() {
			if err := first.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}); n != 1 {
			t.Fatalf("reload decoded %d times, want 1", n)
		}
		data, _ := first.WorkspaceMetadata(workspace)
		index := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project })
		if data.Projects[index].Name != "Renamed after" || data.Projects[index].Description != "written here" {
			t.Fatalf("copy has %q / %q", data.Projects[index].Name, data.Projects[index].Description)
		}
	})
}

// A copy changed outside the exact paths has no revision, so the next
// check reloads it (the behaviour before revisions were recorded).
func TestOtherCopyChangesForgetTheRevision(t *testing.T) {
	first, _, workspace := copyRevisionPair(t)
	first.writeMu.Lock()
	data, _ := first.storedWorkspace(workspace)
	first.installWorkspace(workspace, data, false)
	first.writeMu.Unlock()
	if hasCopyRevision(first, workspace) {
		t.Fatal("installWorkspace kept the revision")
	}
	if n := countDecodes(t, first, func() {
		if err := first.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
	}); n != 1 {
		t.Fatalf("reload decoded %d times, want 1", n)
	}
	if !hasCopyRevision(first, workspace) {
		t.Fatal("the reload recorded no revision")
	}
}
