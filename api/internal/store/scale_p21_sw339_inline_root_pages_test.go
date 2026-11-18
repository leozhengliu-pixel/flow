package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func scaleP21SW339Root(t *testing.T) string {
	t.Helper()
	body, err := json.Marshal(strings.Repeat("x\u4e2d\U0001f600", 70000) + "\x00<>&")
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"workspace":{"id":"sw339-stored"},"settings":{"opaque":` + string(body) +
		`},"viewerRole":"earlier","viewerRole":"final","_flowCollections":{"projects":"array","integrationConnections":"array"}}`
	if len(raw) <= 2*MetadataRecordPageMaxBytes {
		t.Fatal("inline fixture is not larger than two maximum pages")
	}
	return raw
}

func TestScaleP21SW339InlineRootScopedPages(t *testing.T) {
	raw := scaleP21SW339Root(t)
	f, view := scaleP21SW229Fixture(t, raw)
	t.Cleanup(view.releaseMetadataLogicalSources)
	workspace := f.data.Workspace.URLKey
	var fields map[string]metadataCollectionRange
	for _, event := range []string{"", "integration_delivery.claimed", "integration_delivery.retry", "integration_delivery.other"} {
		t.Run("event="+event, func(t *testing.T) {
			queries := 0
			ctx := WithSQLStatementObserver(withIntegrationDeliveryStream(t.Context(), event), func(query string) {
				if scaleP21SW229RootQuery(query) {
					queries++
				}
			})
			tx, err := view.db.BeginTx(t.Context(), metadataReadOptions(view.dialect))
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			disk, spools, work := metadataLogicalDiskUsed.Load(), recordSpoolBytes.Load(), view.MetadataWorkUsedForTesting()
			root, err := view.scanMetadataInlineRoot(ctx, tx, workspace, int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			width := metadataRecordChunkBytes
			if event == "integration_delivery.claimed" || event == "integration_delivery.retry" {
				width = MetadataRecordPageMaxBytes
			}
			expectedQueries := (len(raw) + width - 1) / width
			allowance := 128<<10 + (width-metadataRecordChunkBytes)*4
			if queries != expectedQueries || view.MetadataWorkUsedForTesting()-work != allowance ||
				metadataLogicalDiskUsed.Load()-disk != int64(len(raw)) {
				t.Fatal("root pages changed query or held-memory bounds", queries, expectedQueries, view.MetadataWorkUsedForTesting()-work, allowance)
			}
			if fields == nil {
				fields = root.fields
			} else if !reflect.DeepEqual(root.fields, fields) {
				t.Fatal("SQL page choice changed last-occurrence field ranges")
			}
			scaleP21SW229AssertRootBytes(t, root, raw)
			shapes, err := root.shapes()
			if err != nil || !reflect.DeepEqual(shapes, map[string]string{"projects": "array", "integrationConnections": "array"}) {
				t.Fatal("SQL page choice changed manifest", shapes, err)
			}
			for _, page := range root.source.pages {
				if len(page.data) > metadataRecordChunkBytes {
					t.Fatal("SQL coalescing enlarged a file parser page", len(page.data))
				}
			}
			// Captures and their synchronous borrowers outlive the SQL read.
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			borrowCtx := withMetadataInlineRootCapture(ctx, root)
			borrowed, err := view.scanMetadataInlineRoot(borrowCtx, tx, workspace, int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			if view.MetadataWorkUsedForTesting()-work != allowance+64<<10 {
				borrowed.Close()
				t.Fatal("borrow did not own exactly its fixed descriptor/file pages")
			}
			scaleP21SW229AssertRootBytes(t, borrowed, raw)
			borrowed.Close()
			borrowed.Close()
			scaleP21SW229AssertRootBytes(t, root, raw)
			if queries != expectedQueries || view.MetadataWorkUsedForTesting()-work != allowance {
				t.Fatal("borrow reread SQL or released its parent's allowance", queries)
			}
			path := root.spool.file.Name()
			root.Close()
			root.Close()
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) ||
				metadataLogicalDiskUsed.Load() != disk || recordSpoolBytes.Load() != spools || view.MetadataWorkUsedForTesting() != work {
				t.Fatal("root release retained its file, spool or work", err)
			}
			if borrowed, err := view.scanMetadataInlineRoot(borrowCtx, tx, workspace, int64(len(raw))); borrowed != nil || !errors.Is(err, ErrMetadataRecordQuery) || queries != expectedQueries {
				if borrowed != nil {
					borrowed.Close()
				}
				t.Fatal("closed capture was reused or reached SQL", err, queries)
			}
			t.Logf("root_bytes=%d sql_page_bytes=%d sql_queries=%d held_work_bytes=%d parser_page_bytes=%d",
				len(raw), width, queries, allowance, metadataRecordChunkBytes)
		})
	}
}

func TestScaleP21SW339SnapshotReaderPageBoundaries(t *testing.T) {
	raw := scaleP21SW339Root(t)
	f, view := scaleP21SW229Fixture(t, raw)
	t.Cleanup(view.releaseMetadataLogicalSources)
	workspace := f.data.Workspace.URLKey
	tx, err := view.db.BeginTx(t.Context(), metadataReadOptions(view.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot := &metadataCollectionSnapshot{store: view, tx: tx, workspace: workspace}
	observed := func(ctx context.Context, queries *int, cancel context.CancelFunc) context.Context {
		return WithSQLStatementObserver(ctx, func(query string) {
			if scaleP21SW229RootQuery(query) {
				(*queries)++
				if *queries == 2 && cancel != nil {
					cancel()
				}
			}
		})
	}
	for _, page := range []int{0, MetadataRecordPageMaxBytes} {
		t.Run(fmt.Sprintf("partial-and-eof/page=%d", page), func(t *testing.T) {
			queries := 0
			r := &metadataSnapshotReaderAt{ctx: observed(t.Context(), &queries, nil), snapshot: snapshot, size: int64(len(raw)), pageBytes: page}
			var tail [37]byte
			n, err := r.ReadAt(tail[:], int64(len(raw)-19))
			if n != 19 || err != io.EOF || !bytes.Equal(tail[:n], []byte(raw[len(raw)-19:])) || queries != 1 {
				t.Fatal("partial final page changed bytes or EOF", n, err, queries)
			}
			for _, offset := range []int64{int64(len(raw)), int64(len(raw) + 1)} {
				if n, err := r.ReadAt(tail[:], offset); n != 0 || err != io.EOF || queries != 1 {
					t.Fatal("EOF performed SQL", n, err, queries)
				}
			}
			if n, err := r.ReadAt(nil, 0); n != 0 || err != nil || queries != 1 {
				t.Fatal("empty reader-at request performed SQL", n, err, queries)
			}
		})
		t.Run(fmt.Sprintf("cancel/page=%d", page), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			queries := 0
			r := &metadataSnapshotReaderAt{ctx: observed(ctx, &queries, cancel), snapshot: snapshot, size: int64(len(raw)), pageBytes: page}
			width := page
			if width == 0 {
				width = metadataRecordChunkBytes
			}
			output := make([]byte, 2*width+17)
			n, err := r.ReadAt(output, 0)
			if n != width || !errors.Is(err, context.Canceled) || queries != 2 || !bytes.Equal(output[:n], []byte(raw[:n])) {
				t.Fatal("mid-read cancellation changed completed prefix", n, width, err, queries)
			}
			if n, err := r.ReadAt(output[:1], 0); n != 0 || !errors.Is(err, context.Canceled) || queries != 2 {
				t.Fatal("pre-canceled reader reached SQL", n, err, queries)
			}
		})
	}
	t.Run("invalid-pages-and-offset-before-sql", func(t *testing.T) {
		queries := 0
		r := &metadataSnapshotReaderAt{ctx: observed(t.Context(), &queries, nil), snapshot: snapshot, size: int64(len(raw))}
		for _, page := range []int{-1, MetadataRecordPageMaxBytes + 1} {
			r.pageBytes = page
			if n, err := r.ReadAt(make([]byte, 7), 0); n != 0 || !errors.Is(err, ErrMetadataPageBudget) || queries != 0 {
				t.Fatal("invalid page reached SQL", page, n, err, queries)
			}
		}
		r.pageBytes = MetadataRecordPageMaxBytes
		if n, err := r.ReadAt(make([]byte, 7), -1); n != 0 || !errors.Is(err, ErrMetadataRecordQuery) || queries != 0 {
			t.Fatal("negative offset reached SQL", n, err, queries)
		}
	})
}
