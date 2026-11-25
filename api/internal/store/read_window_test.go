package store

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// A large root read inside a read scope takes a bounded number of whole-row
// SQL reads (each SUBSTR loads the complete row): about 32 up to 8 MiB, then
// one per 256 KiB window. The window is admitted work independent of the row
// size, returned when the scope ends, and a write in the transaction
// invalidates it. Outside a scope, and for readers with explicit pages, the
// reads keep their pages.
func TestScaleP21SW560ScopedReadWindows(t *testing.T) {
	f, view, _ := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	for _, size := range []int{metadataReadWindowMinRow - 64, metadataReadWindowMinRow + 1, 6 << 20, 12 << 20} {
		root := []byte(`{"settings":{"body":"` + strings.Repeat("r", size) + `"}}`)
		if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", root, workspace); err != nil {
			t.Fatal(err)
		}
		reads := 0
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
				reads++
			}
		})
		before := view.MetadataWorkUsedForTesting()
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := &metadataCollectionSnapshot{store: view, tx: tx, workspace: workspace}
		reader := &metadataSnapshotReaderAt{ctx: ctx, snapshot: snapshot, size: int64(len(root))}
		// Plain 16 KiB reads, as the scanners and root spool copies issue
		// (a bytes.Buffer destination would choose its own read sizes).
		copyRoot := func(reader *metadataSnapshotReaderAt) []byte {
			var copied bytes.Buffer
			if _, err := io.CopyBuffer(struct{ io.Writer }{&copied}, io.NewSectionReader(reader, 0, reader.size), make([]byte, metadataRecordChunkBytes)); err != nil {
				t.Fatal(err)
			}
			return copied.Bytes()
		}
		pages := (len(root) + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes
		// Outside a scope: the fixed pages and no admission.
		if got := copyRoot(reader); !bytes.Equal(got, root) || reads != pages || view.MetadataWorkUsedForTesting() != before {
			t.Fatal("an unscoped read changed bytes, pages or admission", size, reads, pages)
		}
		reads = 0
		endRead := snapshot.readScope()
		nested := snapshot.readScope()
		if got := copyRoot(reader); !bytes.Equal(got, root) {
			t.Fatal("windowed root read changed bytes", size)
		}
		nested()
		nested()
		copyReads := reads
		// A short reread just behind the scan position (a key after its
		// skipped value) stays in the retained window.
		key := make([]byte, 8)
		if _, err := reader.ReadAt(key, reader.size-16); err != nil || !bytes.Equal(key, root[len(root)-16:len(root)-8]) {
			t.Fatal("reread differs", err)
		}
		bound := pages
		if len(root) > metadataReadWindowMinRow {
			if reads != copyReads {
				t.Fatal("a reread inside the last window queried SQL again")
			}
			width := metadataReadWindowWidth(int64(len(root)))
			bound = (len(root) + width - 1) / width
			if view.MetadataWorkUsedForTesting() != before+2*width {
				t.Fatal("the window is not admitted work or a nested scope released it", view.MetadataWorkUsedForTesting()-before, width)
			}
		} else if view.MetadataWorkUsedForTesting() != before {
			t.Fatal("a small row admitted a window")
		}
		if copyReads > bound {
			t.Fatalf("root of %d bytes took %d whole-row reads, bound %d", len(root), copyReads, bound)
		}
		// Explicit pages are kept inside a scope.
		reads = 0
		paged := &metadataSnapshotReaderAt{ctx: ctx, snapshot: snapshot, size: int64(len(root)), pageBytes: metadataRecordChunkBytes}
		if got := copyRoot(paged); !bytes.Equal(got, root) || reads != pages {
			t.Fatal("a reader with explicit pages read windows", reads, pages)
		}
		// A write invalidates the window: the next read sees the new row.
		changed := bytes.Replace(root, []byte("rrrr"), []byte("wwww"), 1)
		if _, err := tx.ExecContext(ctx, "UPDATE workspace_states SET data=? WHERE workspace_key=?", changed, workspace); err != nil {
			t.Fatal(err)
		}
		again := make([]byte, len(changed))
		if _, err := reader.ReadAt(again, 0); err != nil || !bytes.Equal(again, changed) {
			t.Fatal("read after a write in the transaction returned stale bytes", err)
		}
		endRead()
		endRead()
		if view.MetadataWorkUsedForTesting() != before || snapshot.windows.slot.data != nil {
			t.Fatal("the end of the read kept the window or its admission")
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		t.Logf("root=%d bytes: %d whole-row reads in a scope (fixed %d-byte pages take %d)", len(root), copyReads, metadataRecordChunkBytes, pages)
	}
}

// A public collection read releases its window when it returns, and a work
// budget that cannot admit a window keeps the fixed pages with equal bytes.
func TestScaleP21SW560CollectionReadReleasesWindows(t *testing.T) {
	f, view, raw := scaleP21SW188InlineProjects(t)
	workspace := f.data.Workspace.URLKey
	pages := (len(raw) + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes
	for _, admitted := range []bool{true, false} {
		reads := 0
		observed := WithSQLStatementObserver(t.Context(), func(query string) {
			if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
				reads++
			}
		})
		lease := 2 << 20
		if !admitted {
			lease = view.metadataResidency.work.limit - view.MetadataWorkUsedForTesting()
		}
		ctx, release, err := view.admitMetadataWork(observed, lease)
		if err != nil {
			t.Fatal(err)
		}
		base := view.MetadataWorkUsedForTesting()
		tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.MetadataCollectionsInSnapshot(ctx, tx, workspace, "projects")
		if err != nil {
			t.Fatal(err)
		}
		scanned := reads
		if view.MetadataWorkUsedForTesting() != base {
			t.Fatal("the root scan kept its window after it returned")
		}
		var projects bytes.Buffer
		windowed := 0
		if err := read.VisitArray("projects", func(_ context.Context, record MetadataArrayRecord) error {
			_, err := io.Copy(&projects, record.Reader)
			if view.MetadataWorkUsedForTesting() > base {
				windowed++
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if admitted != (windowed == 2) {
			t.Fatal("large records inside the visit did not follow the window admission", admitted, windowed)
		}
		if view.MetadataWorkUsedForTesting() != base {
			t.Fatal("the visit kept its window after it returned")
		}
		if err := read.Close(); err != nil {
			t.Fatal(err)
		}
		tx.Rollback()
		release()
		if !bytes.Contains(raw, projects.Bytes()[:64]) || projects.Len() < 2*32000*5 {
			t.Fatal("visited projects differ", projects.Len())
		}
		if admitted && scanned >= pages || !admitted && scanned < pages {
			t.Fatal("the root scan's whole-row reads do not follow its admission", admitted, scanned, pages)
		}
		t.Logf("admitted=%t root=%d bytes: root scan took %d whole-row reads (fixed pages %d)", admitted, len(raw), scanned, pages)
	}
}
