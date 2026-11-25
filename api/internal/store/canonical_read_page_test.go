package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestScaleP21SW337CanonicalReadPages(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	workspace := f.data.Workspace.URLKey
	key := RecordKey{Kind: "integrationDeliveries", ID: "sw337-" + f.data.Workspace.ID}
	raw := []byte(`{"payload":"` + strings.Repeat("x\u4e2d\U0001f600", 70000) + `"}`)
	if len(raw) <= 2*MetadataRecordPageMaxBytes {
		t.Fatal("reader fixture is not larger than two maximum pages")
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,?,?,0,?)`,
		workspace, key.Kind, key.ID, raw); err != nil {
		t.Fatal(err)
	}
	reader := func(ctx context.Context, page int) *canonicalMetadataReader {
		return &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: key, size: int64(len(raw)), pageBytes: page}
	}
	observe := func(ctx context.Context, queries *int, after func()) context.Context {
		return WithSQLStatementObserver(ctx, func(query string) {
			if strings.HasPrefix(query, "SELECT SUBSTR(") {
				*queries++
				if after != nil {
					after()
				}
			}
		})
	}
	collect := func(r *canonicalMetadataReader) ([]byte, error) {
		var got []byte
		var small [257]byte
		for {
			n, err := r.Read(small[:])
			got = append(got, small[:n]...)
			if err != nil {
				return got, err
			}
			if n == 0 {
				return got, io.ErrNoProgress
			}
		}
	}
	for _, page := range []int{0, MetadataRecordPageMaxBytes} {
		t.Run(fmt.Sprintf("exact/page=%d", page), func(t *testing.T) {
			queries := 0
			r := reader(observe(t.Context(), &queries, nil), page)
			got, err := collect(r)
			width := page
			if width == 0 {
				width = metadataRecordChunkBytes
			}
			expected := (len(raw) + width - 1) / width
			if err != io.EOF || !bytes.Equal(got, raw) || queries != expected {
				t.Fatal("canonical page changed bytes, EOF or query count", page, len(got), queries, expected, err)
			}
			before := queries
			if n, err := r.Read(make([]byte, 3)); n != 0 || err != io.EOF || queries != before {
				t.Fatal("EOF fetched an extra page", n, err, queries, before)
			}
			t.Logf("payload_bytes=%d page_bytes=%d caller_buffer_bytes=257 page_queries=%d", len(raw), width, queries)
		})
		t.Run(fmt.Sprintf("capture/page=%d", page), func(t *testing.T) {
			queries := 0
			r := reader(observe(t.Context(), &queries, nil), page)
			limit := MetadataRecordPageMaxBytes + 73
			r.boundedCapture, r.captureLeft = true, limit
			got, err := collect(r)
			width := page
			if width == 0 {
				width = metadataRecordChunkBytes
			}
			expected := (limit + width - 1) / width
			if !errors.Is(err, ErrMetadataCatalogGap) || !bytes.Equal(got, raw[:limit]) ||
				r.captureLeft != 0 || !r.captureExceeded || r.offset != int64(limit) || queries != expected {
				t.Fatal("capture boundary grew to a full page or read beyond its budget", len(got), r.captureLeft, r.offset, queries, expected, err)
			}
			if n, err := r.Read(make([]byte, 3)); n != 0 || !errors.Is(err, ErrMetadataCatalogGap) || queries != expected {
				t.Fatal("exhausted capture performed extra SQL", n, err, queries, expected)
			}
		})
		t.Run(fmt.Sprintf("cancel-second-page/page=%d", page), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			queries := 0
			r := reader(observe(ctx, &queries, func() {
				if queries == 2 {
					cancel()
				}
			}), page)
			got, err := collect(r)
			width := page
			if width == 0 {
				width = metadataRecordChunkBytes
			}
			if !errors.Is(err, context.Canceled) || queries != 2 || !bytes.Equal(got, raw[:width]) {
				t.Fatal("mid-page SQL cancellation changed the completed prefix", page, len(got), queries, err)
			}
		})
		t.Run(fmt.Sprintf("cancel-cached-bytes/page=%d", page), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			queries := 0
			r := reader(observe(ctx, &queries, nil), page)
			var one [1]byte
			if n, err := r.Read(one[:]); n != 1 || err != nil || one[0] != raw[0] || len(r.buffer) == 0 {
				t.Fatal("fixture did not leave a buffered page", n, err)
			}
			cancel()
			n, err := r.Read(one[:])
			if page == 0 {
				if n != 1 || err != nil || one[0] != raw[1] {
					t.Fatal("default reader cancellation behavior changed", n, err)
				}
			} else if n != 0 || !errors.Is(err, context.Canceled) {
				t.Fatal("scoped reader served cached bytes after cancellation", n, err)
			}
			if queries != 1 {
				t.Fatal("cached cancellation issued SQL", queries)
			}
		})
	}
	t.Run("scoped-cancel-before-first-page", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		queries := 0
		r := reader(observe(ctx, &queries, nil), MetadataRecordPageMaxBytes)
		if n, err := r.Read(make([]byte, 7)); n != 0 || !errors.Is(err, context.Canceled) || queries != 0 {
			t.Fatal("pre-canceled scoped read performed SQL or returned bytes", n, err, queries)
		}
	})
	t.Run("empty-capture-and-small-positive-page", func(t *testing.T) {
		queries := 0
		r := reader(observe(t.Context(), &queries, nil), MetadataRecordPageMaxBytes)
		r.boundedCapture = true
		if n, err := r.Read(make([]byte, 7)); n != 0 || !errors.Is(err, ErrMetadataCatalogGap) || queries != 0 {
			t.Fatal("empty capture fetched SQL", n, err, queries)
		}
		r = reader(observe(t.Context(), &queries, nil), 31)
		var small [7]byte
		if n, err := r.Read(small[:]); n != len(small) || err != nil || !bytes.Equal(small[:], raw[:len(small)]) ||
			r.offset != 31 || queries != 1 {
			t.Fatal("positive page size was ignored", n, err, r.offset, queries)
		}
	})
	t.Run("invalid-page-before-sql-or-cached-bytes", func(t *testing.T) {
		for _, page := range []int{-1, MetadataRecordPageMaxBytes + 1} {
			queries := 0
			r := reader(observe(t.Context(), &queries, nil), page)
			r.buffer = []byte("cached")
			if n, err := r.Read(make([]byte, 7)); n != 0 || !errors.Is(err, ErrMetadataPageBudget) || queries != 0 {
				t.Fatal("invalid page bound reached SQL or cached data", page, n, err, queries)
			}
		}
	})
}
