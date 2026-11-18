package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func scaleP21SW229Fixture(t *testing.T, raw string) (scaleP09Fixture, *SQLiteStore) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", saved, workspace); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.db.ExecContext(t.Context(), "UPDATE workspace_states SET data=? WHERE workspace_key=?", []byte(raw), workspace); err != nil {
		t.Fatal(err)
	}
	return f, view
}

func scaleP21SW229RootQuery(query string) bool {
	value := strings.ToLower(strings.TrimSpace(query))
	return strings.HasPrefix(value, "select substr(") && strings.Contains(value, "from workspace_states")
}

func scaleP21SW229AssertRootBytes(t *testing.T, root *metadataInlineRoot, raw string) {
	t.Helper()
	buffer := make([]byte, metadataRecordChunkBytes)
	for offset := int64(0); offset < int64(len(raw)); {
		count := int(min(int64(len(buffer)), int64(len(raw))-offset))
		if n, err := root.source.ReadAt(buffer[:count], offset); err != nil || n != count ||
			!bytes.Equal(buffer[:count], []byte(raw[offset:offset+int64(count)])) {
			t.Fatal("captured root changed complete native SQL bytes", offset, n, err)
		}
		offset += int64(count)
	}
}

func TestScaleP21SW229InlineRootCopiesOnceAndBorrows(t *testing.T) {
	for _, size := range []int{32, 3 << 20} {
		t.Run(map[bool]string{true: "large", false: "small"}[size > 32], func(t *testing.T) {
			body, err := json.Marshal(strings.Repeat("x", size))
			if err != nil {
				t.Fatal(err)
			}
			raw := `{"workspace":{"id":"stored","name":"First"},"settings":{"opaque":` + string(body) +
				`},"viewerRole":"earlier","viewerRole":"final","_flowCollections":{"projects":"array"}}`
			f, view := scaleP21SW229Fixture(t, raw)
			workspace := f.data.Workspace.URLKey
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				if scaleP21SW229RootQuery(query) {
					queries++
				}
			})
			tx, err := view.db.BeginTx(ctx, metadataReadOptions(view.dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			disk, work, decodes := metadataLogicalDiskUsed.Load(), view.MetadataWorkUsedForTesting(), view.WorkspaceMetadataDecodes()
			root, err := view.scanMetadataInlineRoot(ctx, tx, workspace, int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			path := root.spool.file.Name()
			expected := (len(raw) + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes
			if queries != expected || metadataLogicalDiskUsed.Load()-disk != int64(len(raw)) {
				t.Fatal("root capture did not make exactly one bounded SQL copy", queries, expected)
			}
			ownedWork := view.MetadataWorkUsedForTesting() - work
			if ownedWork <= 0 || ownedWork > 256<<10 || view.WorkspaceMetadataDecodes() != decodes {
				t.Fatal("body size changed fixed root work or caused native hydration")
			}
			for range 3 {
				scaleP21SW229AssertRootBytes(t, root, raw)
				shapes, err := root.shapes()
				if err != nil || !reflect.DeepEqual(shapes, map[string]string{"projects": "array"}) {
					t.Fatal("captured manifest differs", shapes, err)
				}
			}
			borrowCtx := withMetadataInlineRootCapture(ctx, root)
			borrowed, err := view.scanMetadataInlineRoot(borrowCtx, tx, workspace, int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			if borrowed == root || !reflect.DeepEqual(root.fields, borrowed.fields) {
				borrowed.Close()
				t.Fatal("borrowed root lost its original occurrence ranges")
			}
			scaleP21SW229AssertRootBytes(t, borrowed, raw)
			borrowed.Close()
			borrowed.Close()
			if queries != expected || view.MetadataWorkUsedForTesting()-work != ownedWork {
				t.Fatal("borrow reread SQL or released the original owner's work", queries)
			}
			scaleP21SW229AssertRootBytes(t, root, raw)
			root.Close()
			root.Close()
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) ||
				metadataLogicalDiskUsed.Load() != disk || view.MetadataWorkUsedForTesting() != work {
				t.Fatal("root Close retained its file, disk or work", err)
			}
		})
	}
}

func TestScaleP21SW229InlineRootMatchesExistingScanner(t *testing.T) {
	for _, test := range []struct {
		name, raw string
	}{
		{"known", `{"workspace":{"id":"first"},"workspace":{"id":"last"},"settings":null,"_flowCollections":{}}`},
		{"escaped-key", `{"\u0077orkspace":{"id":"stored"},"settings":{"html":"<>&","zero":"\u0000"},"_flowCollections":{"projects":"array"}}`},
		{"unknown", `{"workspace":{},"newUnknownField":[1,2,3]}`},
		{"case-sensitive-unknown", `{"Workspace":{"id":"stored"}}`},
		{"trailing", `{"workspace":{}} false`},
		{"truncated", `{"workspace":{"id":"stored"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, view := scaleP21SW229Fixture(t, test.raw)
			workspace := f.data.Workspace.URLKey
			tx, err := view.db.BeginTx(t.Context(), metadataReadOptions(view.dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// This is the original SQL-backed source with the unchanged
			// scanner, retained as an independent reader differential.
			snapshot := &metadataCollectionSnapshot{store: view, tx: tx, workspace: workspace}
			old := &metadataInlineRoot{
				ctx: t.Context(), fields: map[string]metadataCollectionRange{},
				source: &metadataInlineRootReader{
					ctx: t.Context(), size: int64(len(test.raw)),
					source: &metadataSnapshotReaderAt{ctx: t.Context(), snapshot: snapshot, size: int64(len(test.raw))},
				},
			}
			oldErr := old.scan()
			disk, work := metadataLogicalDiskUsed.Load(), view.MetadataWorkUsedForTesting()
			current, currentErr := view.scanMetadataInlineRoot(t.Context(), tx, workspace, int64(len(test.raw)))
			if (oldErr == nil) != (currentErr == nil) ||
				oldErr != nil && (oldErr.Error() != currentErr.Error() ||
					errors.Is(oldErr, ErrBoundedMetadataCoverage) != errors.Is(currentErr, ErrBoundedMetadataCoverage)) {
				if current != nil {
					current.Close()
				}
				t.Fatal("detaching root changed the existing scanner's result", oldErr, currentErr)
			}
			if oldErr == nil {
				if current == nil || !reflect.DeepEqual(old.fields, current.fields) {
					if current != nil {
						current.Close()
					}
					t.Fatal("detaching root changed exact occurrence ranges")
				}
				current.Close()
			} else if current != nil {
				current.Close()
				t.Fatal("failed root validation exposed a partially captured root")
			}
			if metadataLogicalDiskUsed.Load() != disk || view.MetadataWorkUsedForTesting() != work {
				t.Fatal("scanner success/error retained the detached root")
			}
		})
	}
}

func TestScaleP21SW229InlineRootCancellationAndFailures(t *testing.T) {
	raw := `{"workspace":{"id":"stored"},"settings":{"opaque":"` + strings.Repeat("x", 3*metadataRecordChunkBytes) + `"}}`
	f, view := scaleP21SW229Fixture(t, raw)
	workspace := f.data.Workspace.URLKey
	for _, moment := range []string{"before", "during-copy", "after-capture", "short-read", "work-denial", "disk-denial"} {
		t.Run(moment, func(t *testing.T) {
			cancelled, cancel := context.WithCancel(t.Context())
			defer cancel()
			queries := 0
			ctx := WithSQLStatementObserver(cancelled, func(query string) {
				if scaleP21SW229RootQuery(query) {
					queries++
					if moment == "during-copy" && queries == 2 {
						cancel()
					}
				}
			})
			tx, err := view.db.BeginTx(t.Context(), metadataReadOptions(view.dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			disk, work := metadataLogicalDiskUsed.Load(), view.MetadataWorkUsedForTesting()
			size, want := int64(len(raw)), error(nil)
			switch moment {
			case "before":
				cancel()
				want = context.Canceled
			case "during-copy":
				want = context.Canceled
			case "short-read":
				size++
				want = io.ErrUnexpectedEOF
			case "work-denial":
				previous := view.metadataResidency.work.limit
				view.metadataResidency.work.limit = work + 1
				defer func() { view.metadataResidency.work.limit = previous }()
				want = ErrMetadataWorkBudget
			case "disk-denial":
				metadataLogicalDiskUsed.Add(metadataLogicalDiskBytes)
				defer metadataLogicalDiskUsed.Add(-metadataLogicalDiskBytes)
				disk += metadataLogicalDiskBytes
				want = ErrRecordSpoolBudget
			}
			root, err := view.scanMetadataInlineRoot(ctx, tx, workspace, size)
			if moment == "after-capture" {
				if err != nil || root == nil {
					t.Fatal(err)
				}
				captured := queries
				cancel()
				if _, err := root.source.ReadAt(make([]byte, 1), 0); !errors.Is(err, context.Canceled) || queries != captured {
					root.Close()
					t.Fatal("detached root ignored cancellation or returned to SQL", err)
				}
				root.Close()
			} else if !errors.Is(err, want) || root != nil {
				if root != nil {
					root.Close()
				}
				t.Fatal("root capture changed its admission/read error", err, want)
			}
			if metadataLogicalDiskUsed.Load() != disk || view.MetadataWorkUsedForTesting() != work {
				t.Fatal("cancelled or failed capture retained disk or work")
			}
		})
	}
}

func TestScaleP21SW229InlineRootCaptureBindingAndLifetime(t *testing.T) {
	raw := `{"workspace":{"id":"stored","name":"Before"},"_flowCollections":{}}`
	f, view := scaleP21SW229Fixture(t, raw)
	workspace := f.data.Workspace.URLKey
	queries := 0
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if scaleP21SW229RootQuery(query) {
			queries++
		}
	})
	tx, err := view.db.BeginTx(ctx, metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	root, err := view.scanMetadataInlineRoot(ctx, tx, workspace, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	captured, borrowCtx := queries, withMetadataInlineRootCapture(ctx, root)
	for _, test := range []struct {
		name, workspace string
		store           *SQLiteStore
		tx              *sqlTx
		size            int64
	}{
		{"foreign-store", workspace, f.SQLiteStore, tx, int64(len(raw))},
		{"foreign-transaction", workspace, view, &sqlTx{database: view.db, dialect: view.dialect}, int64(len(raw))},
		{"foreign-workspace", workspace + "-foreign", view, tx, int64(len(raw))},
		{"foreign-size", workspace, view, tx, int64(len(raw)) + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			borrowed, err := test.store.scanMetadataInlineRoot(borrowCtx, test.tx, test.workspace, test.size)
			if !errors.Is(err, ErrMetadataRecordQuery) || borrowed != nil || queries != captured {
				if borrowed != nil {
					borrowed.Close()
				}
				t.Fatal("root capture escaped its store/transaction/workspace/size binding", err)
			}
		})
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// A successful capture is detached from SQL and keeps the original bytes
	// even after its caller ends that transaction.
	scaleP21SW229AssertRootBytes(t, root, raw)
	if queries != captured {
		t.Fatal("detached root retained a SQL reader")
	}
	root.Close()
	borrowed, err := view.scanMetadataInlineRoot(borrowCtx, tx, workspace, int64(len(raw)))
	if !errors.Is(err, ErrMetadataRecordQuery) || borrowed != nil || queries != captured {
		if borrowed != nil {
			borrowed.Close()
		}
		t.Fatal("closed root capture was reused", err)
	}
}
