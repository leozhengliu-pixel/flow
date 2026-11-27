package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

// conflictSignalFixture seeds SQL with the project as persisting stores it
// (NUL stripped) and installs in the view the image of an owner that still
// holds the project natively, NUL included.
func conflictSignalFixture(t *testing.T) (scaleFixture, *SQLiteStore, string, domain.Bootstrap) {
	t.Helper()
	f, view := legacyCountWriteFixture(t)
	workspace := f.data.Workspace.URLKey
	base := largeRowWriteBase(f)
	base.Projects = []domain.Project{{ID: "conflict-signal-project", Name: "Signal", Description: "kept\x00 text", CreatedAt: largeRowWriteAt, UpdatedAt: largeRowWriteAt}}
	largeRowWriteSeed(t, f, base)
	f.mu.Lock()
	f.workspaces[workspace] = base
	f.mu.Unlock()
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	return f, view, workspace, base
}

func conflictSignalBehind(view *SQLiteStore, workspace string) bool {
	r := view.metadataResidency
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logical[workspace].behind.Load()
}

func conflictSignalProject(t *testing.T, view *SQLiteStore, workspace string) domain.Project {
	t.Helper()
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	data, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"projects"}}, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Projects) != 1 {
		t.Fatal("projects", len(data.Projects))
	}
	return data.Projects[0]
}

func conflictSignalRow(t *testing.T, f scaleFixture, workspace string) domain.Project {
	t.Helper()
	var raw []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, "conflict-signal-project").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := json.Unmarshal(raw, &project); err != nil {
		t.Fatal(err)
	}
	return project
}

// A conflict whose row differs from the installed record only by what
// persisting drops (a NUL character) is no evidence of another writer.
func TestScaleConflictSignalNormalisationOnlyIsNotBehind(t *testing.T) {
	f, view, workspace, _ := conflictSignalFixture(t)
	if conflictSignalBehind(view, workspace) {
		t.Fatal("fixture image starts behind")
	}
	if strings.Contains(conflictSignalRow(t, f, workspace).Description, "\x00") {
		t.Fatal("SQL row kept the NUL")
	}
	if !strings.Contains(conflictSignalProject(t, view, workspace).Description, "\x00") {
		t.Fatal("installed image lost the native NUL before the refresh")
	}
	keys := []metadataRecordKey{{"projects", "conflict-signal-project"}}
	if err := view.refreshConflictedLogicalMetadata(t.Context(), workspace)(&metadataConflictError{keys: keys}); err != nil {
		t.Fatal(err)
	}
	if conflictSignalBehind(view, workspace) {
		t.Fatal("a normalisation-only conflict marked the image behind")
	}
	if got, want := conflictSignalProject(t, view, workspace), conflictSignalRow(t, f, workspace); !reflect.DeepEqual(got, want) {
		t.Fatal("refreshed record differs from the literal refresh", got, want)
	}
}

// A row another writer changed is evidence: the image is marked behind and
// equals what a reload from SQL gives.
func TestScaleConflictSignalPeerWriteIsBehind(t *testing.T) {
	f, view, workspace, _ := conflictSignalFixture(t)
	peer := conflictSignalRow(t, f, workspace)
	peer.Name = "Peer renamed"
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	largeRowWritePut(t, tx, workspace, "projects", peer.ID, peer, largeRowWriteOrder(t, tx, workspace, "projects", peer.ID))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	keys := []metadataRecordKey{{"projects", peer.ID}}
	if err := view.refreshConflictedLogicalMetadata(t.Context(), workspace)(&metadataConflictError{keys: keys}); err != nil {
		t.Fatal(err)
	}
	if !conflictSignalBehind(view, workspace) {
		t.Fatal("a peer write did not mark the image behind")
	}
	if got := conflictSignalProject(t, view, workspace); !reflect.DeepEqual(got, conflictSignalRow(t, f, workspace)) || got.Name != "Peer renamed" {
		t.Fatal("refreshed record differs from the SQL row", got)
	}
}
