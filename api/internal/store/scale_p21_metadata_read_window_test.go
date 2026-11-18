package store

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// A large root is read in a bounded number of whole-row SQL reads (each
// SUBSTR loads the complete row): about 32 up to 8 MiB, then one per
// 256 KiB window. The window is admitted work independent of the row size,
// released with the transaction, and a write in the transaction drops it.
func TestScaleP21MetadataReadWindowLinearRoot(t *testing.T) {
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
		var copied bytes.Buffer
		// Plain 16 KiB reads, as the scanners and root spool copies issue
		// (a bytes.Buffer destination would choose its own read sizes).
		if _, err := io.CopyBuffer(struct{ io.Writer }{&copied}, io.NewSectionReader(reader, 0, reader.size), make([]byte, metadataRecordChunkBytes)); err != nil {
			t.Fatal(err)
		}
		copyReads := reads
		// A short reread just behind the scan position (a key after its
		// skipped value) stays in the retained windows.
		key := make([]byte, 8)
		if _, err := reader.ReadAt(key, reader.size-16); err != nil || !bytes.Equal(key, root[len(root)-16:len(root)-8]) {
			t.Fatal("reread differs", err)
		}
		if len(root) > metadataReadWindowMinRow && reads != copyReads {
			t.Fatal("a reread inside the last window queried SQL again")
		}
		if !bytes.Equal(copied.Bytes(), root) {
			t.Fatal("windowed root read changed bytes", size)
		}
		pieces := (len(root) + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes
		bound := pieces
		if len(root) > metadataReadWindowMinRow {
			width := metadataReadWindowWidth(int64(len(root)))
			bound = (len(root) + width - 1) / width
			if view.MetadataWorkUsedForTesting() != before+2*width {
				t.Fatal("read windows are not admitted work", view.MetadataWorkUsedForTesting()-before, width)
			}
		}
		if copyReads > bound {
			t.Fatalf("root of %d bytes took %d whole-row reads, bound %d", len(root), copyReads, bound)
		}
		// A write drops the windows: the next read sees the new row.
		changed := bytes.Replace(root, []byte("rrrr"), []byte("wwww"), 1)
		if _, err := tx.ExecContext(ctx, "UPDATE workspace_states SET data=? WHERE workspace_key=?", changed, workspace); err != nil {
			t.Fatal(err)
		}
		if view.MetadataWorkUsedForTesting() != before {
			t.Fatal("a write kept the read windows admitted")
		}
		again := make([]byte, len(changed))
		if _, err := reader.ReadAt(again, 0); err != nil || !bytes.Equal(again, changed) {
			t.Fatal("read after a write in the transaction returned stale bytes", err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if view.MetadataWorkUsedForTesting() != before {
			t.Fatal("rollback kept the read windows admitted")
		}
		t.Logf("root=%d bytes: %d whole-row reads (fixed %d-byte pieces would take %d)", len(root), copyReads, metadataRecordChunkBytes, pieces)
	}
}
