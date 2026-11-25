package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

type scaleP21SW349RootFixture struct {
	store, foreign            *SQLiteStore
	tx                        *sqlTx
	root                      *metadataInlineRoot
	ctx                       context.Context
	raw, workspace            string
	queries                   int
	disk, descriptors, spools int64
	work                      int
}

func scaleP21SW349Root(t *testing.T, event string) *scaleP21SW349RootFixture {
	t.Helper()
	body := strings.TrimSuffix(strings.TrimPrefix(scaleP21SW339Root(t), "{"), "}")
	body = strings.Replace(body, `"settings":{"opaque":`,
		`"settings" : { "duplicate" : 1, "duplicate":2, "raw" : { "html" : "<>&", "nul" : "\u0000" }, "opaque" : `, 1)
	body = strings.Replace(body, `"viewerRole":"final"`, `"\u0076iewerRole" : "final"`, 1)
	raw := " \n{\n \"workspace\" : {\"id\":\"superseded\"},\n \"settings\" : {\"old\":true},\n" +
		` "_flowCollections" : {"projects":"null"}, ` + body + "\n}\r\n "
	if !json.Valid([]byte(raw)) || len(raw) <= 2*MetadataRecordPageMaxBytes {
		t.Fatal("root reuse fixture lost its large, whitespace/duplicate JSON shape")
	}
	base, view := scaleP21SW229Fixture(t, raw)
	t.Cleanup(view.releaseMetadataLogicalSources)
	f := &scaleP21SW349RootFixture{
		store: view, foreign: base.SQLiteStore, raw: raw, workspace: base.data.Workspace.URLKey,
		disk: metadataLogicalDiskUsed.Load(), descriptors: metadataLogicalDescriptorsUsed.Load(),
		spools: recordSpoolBytes.Load(), work: view.MetadataWorkUsedForTesting(),
	}
	f.ctx = WithSQLStatementObserver(withIntegrationDeliveryStream(t.Context(), event), func(query string) {
		if scaleP21SW229RootQuery(query) {
			f.queries++
		}
	})
	// rootShapes is a writer step: on PostgreSQL and MySQL it pins the root
	// with SELECT ... FOR UPDATE, which a read-only transaction refuses.
	options := metadataReadOptions(view.dialect)
	if view.dialect != "sqlite" {
		options = &sql.TxOptions{Isolation: options.Isolation}
	}
	var err error
	f.tx, err = view.db.BeginTx(f.ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.tx.Rollback() })
	f.root, err = view.scanMetadataInlineRoot(f.ctx, f.tx, f.workspace, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.root.Close)
	if f.queries == 0 {
		t.Fatal("root reuse fixture did not capture the actual SQL root")
	}
	return f
}

func (f *scaleP21SW349RootFixture) assertReleased(t *testing.T) {
	t.Helper()
	path := f.root.spool.file.Name()
	f.root.Close()
	f.root.Close()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) ||
		metadataLogicalDiskUsed.Load() != f.disk || metadataLogicalDescriptorsUsed.Load() != f.descriptors ||
		recordSpoolBytes.Load() != f.spools || f.store.MetadataWorkUsedForTesting() != f.work {
		t.Fatal("integration root reuse retained a file, descriptor, spool or owner allowance", err)
	}
}

type scaleP21SW349ReaderAt struct {
	input io.ReaderAt
	reads int
}

func (r *scaleP21SW349ReaderAt) ReadAt(output []byte, offset int64) (int, error) {
	r.reads++
	return r.input.ReadAt(output, offset)
}

func TestScaleP21SW349IntegrationRootFieldMapMatchesOriginal(t *testing.T) {
	for _, event := range []string{"integration_delivery.claimed", "integration_delivery.retry"} {
		t.Run(event, func(t *testing.T) {
			f := scaleP21SW349Root(t, event)
			captured, held := f.queries, f.store.MetadataWorkUsedForTesting()
			reader := &scaleP21SW349ReaderAt{input: f.root.source.source}
			f.root.source.source = reader
			owner := &ResourceRead{store: f.store}
			defer owner.Close()
			got, err := f.root.legacySessionFields(f.ctx, owner)
			if err != nil || len(got) == 0 || reader.reads != 0 || f.queries != captured {
				t.Fatal("range reuse rescanned the captured root or lost its fields", err, reader.reads, f.queries)
			}
			if admitted := f.store.MetadataWorkUsedForTesting() - held; admitted <= 0 || admitted > 256<<10 {
				t.Fatal("field descriptors were not owned by a fixed admitted allowance", admitted)
			}
			for name, part := range got {
				span, found := f.root.fields[name]
				if !found || part.input != f.root.source || part.start != span.offset || part.size != span.size {
					t.Fatal("field reuse changed an already-validated input range", name, part.start, part.size)
				}
			}
			originalOwner := &ResourceRead{store: f.store}
			defer originalOwner.Close()
			original, err := legacySessionJSONObject(f.ctx, originalOwner,
				legacySessionJSONPart{f.root.source, 0, f.root.source.size})
			if err != nil || !reflect.DeepEqual(got, original) {
				t.Fatal("reused field map differs from the original JSONObject scan", err)
			}
			if reader.reads == 0 {
				t.Fatal("large fixture failed to witness the original root rescan")
			}
			var want, output bytes.Buffer
			if err := legacySessionWriteJSONObject(f.ctx, originalOwner, original, &want); err != nil {
				t.Fatal(err)
			}
			if err := legacySessionWriteJSONObject(f.ctx, owner, got, &output); err != nil {
				t.Fatal(err)
			}
			var native map[string]json.RawMessage
			if err := json.Unmarshal([]byte(f.raw), &native); err != nil {
				t.Fatal(err)
			}
			nativeOutput, err := json.Marshal(native)
			if err != nil || !bytes.Equal(output.Bytes(), want.Bytes()) || !bytes.Equal(output.Bytes(), nativeOutput) {
				t.Fatal("root reuse changed sorted output, opaque raw values or duplicate-key semantics", err)
			}
			if string(native["viewerRole"]) != `"final"` {
				t.Fatal("escaped duplicate known key did not keep its final occurrence")
			}
			shapes, err := f.root.shapes()
			if err != nil || !reflect.DeepEqual(shapes, map[string]string{"projects": "array", "integrationConnections": "array"}) {
				t.Fatal("duplicate manifest changed the final root shape", shapes, err)
			}
			if f.queries != captured {
				t.Fatal("field mapping or encoding returned to SQL")
			}
			originalOwner.Close()
			owner.Close()
			if f.store.MetadataWorkUsedForTesting() != held {
				t.Fatal("closing map owners retained work or consumed the captured root")
			}
			scaleP21SW229AssertRootBytes(t, f.root, f.raw)
			f.assertReleased(t)
		})
	}
}

func TestScaleP21SW349IntegrationRootFieldMapGuards(t *testing.T) {
	f := scaleP21SW349Root(t, "integration_delivery.claimed")
	captured := f.queries
	for _, mode := range []string{"nil-owner", "nil-store-owner", "foreign-owner", "closed-owner", "canceled", "work-denied"} {
		t.Run(mode, func(t *testing.T) {
			ctx := f.ctx
			owner := &ResourceRead{store: f.store}
			want := error(ErrMetadataRecordQuery)
			switch mode {
			case "nil-owner":
				owner = nil
			case "nil-store-owner":
				owner.store = nil
			case "foreign-owner":
				owner.store = f.foreign
			case "closed-owner":
				owner.Close()
				want = errMetadataLogicalClosed
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx, want = canceled, context.Canceled
			case "work-denied":
				previous := f.store.metadataResidency.work.limit
				f.store.metadataResidency.work.limit = f.store.MetadataWorkUsedForTesting()
				defer func() { f.store.metadataResidency.work.limit = previous }()
				want = ErrMetadataWorkBudget
			}
			defer owner.Close()
			before := f.store.MetadataWorkUsedForTesting()
			fields, err := f.root.legacySessionFields(ctx, owner)
			if fields != nil || !errors.Is(err, want) || f.queries != captured ||
				f.store.MetadataWorkUsedForTesting() != before {
				t.Fatal("invalid field-map owner/context was admitted or reached SQL", mode, err, want)
			}
			scaleP21SW229AssertRootBytes(t, f.root, f.raw)
		})
	}
	owner := &ResourceRead{store: f.store}
	defer owner.Close()
	var missing *metadataInlineRoot
	if fields, err := missing.legacySessionFields(f.ctx, owner); fields != nil || !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("nil root returned field descriptors", err)
	}
	f.assertReleased(t)
	if fields, err := f.root.legacySessionFields(f.ctx, owner); fields != nil || !errors.Is(err, ErrMetadataRecordQuery) ||
		f.store.MetadataWorkUsedForTesting() != f.work || f.queries != captured {
		t.Fatal("closed root returned fields or retained admission", err)
	}
}

func TestScaleP21SW349IntegrationRootBorrowAndRootShapesReuse(t *testing.T) {
	f := scaleP21SW349Root(t, "integration_delivery.claimed")
	captured, held := f.queries, f.store.MetadataWorkUsedForTesting()
	ctx := withMetadataInlineRootCapture(f.ctx, f.root)
	borrowed, err := f.store.scanMetadataInlineRoot(ctx, f.tx, f.workspace, int64(len(f.raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer borrowed.Close()
	if borrowed == f.root || borrowed.spool != nil || !reflect.DeepEqual(borrowed.fields, f.root.fields) {
		t.Fatal("borrowed root did not preserve parent ranges with its own reader")
	}
	scaleP21SW229AssertRootBytes(t, borrowed, f.raw)
	borrowed.Close()
	borrowed.Close()
	if f.queries != captured || f.store.MetadataWorkUsedForTesting() != held {
		t.Fatal("borrow reread SQL, retained its allowance or released parent work")
	}
	scaleP21SW229AssertRootBytes(t, f.root, f.raw)

	plan := &legacyMetadataWritePlan{
		store: f.store, tx: f.tx, workspace: f.workspace, work: &ResourceRead{store: f.store},
	}
	defer func() {
		if plan.inlineRoot != nil {
			plan.inlineRoot.Close()
		}
		plan.work.Close()
	}()
	shapes, err := plan.rootShapes(ctx)
	if err != nil || !reflect.DeepEqual(shapes, map[string]string{"projects": "array", "integrationConnections": "array"}) {
		t.Fatal("rootShapes did not use the captured root's manifest", shapes, err)
	}
	if plan.inlineRoot != nil {
		t.Fatal("rootShapes retained its borrowed nil-spool root as a new capture")
	}
	if f.queries != captured || f.root.closed || f.root.spool == nil {
		t.Fatal("rootShapes reread SQL or closed the original capture")
	}
	plan.work.Close()
	if f.store.MetadataWorkUsedForTesting() != held {
		t.Fatal("rootShapes retained a borrowed reader or plan allowance")
	}
	scaleP21SW229AssertRootBytes(t, f.root, f.raw)
	f.assertReleased(t)
}

func TestScaleP21SW349IntegrationRootCaptureRefusals(t *testing.T) {
	f := scaleP21SW349Root(t, "integration_delivery.retry")
	captured, held := f.queries, f.store.MetadataWorkUsedForTesting()
	ctx := withMetadataInlineRootCapture(f.ctx, f.root)
	rejectShapes := func(t *testing.T, ctx context.Context, work int) {
		t.Helper()
		plan := &legacyMetadataWritePlan{
			store: f.store, tx: f.tx, workspace: f.workspace, work: &ResourceRead{store: f.store},
		}
		defer func() {
			if plan.inlineRoot != nil {
				plan.inlineRoot.Close()
			}
			plan.work.Close()
		}()
		shapes, err := plan.rootShapes(ctx)
		plan.work.Close()
		if shapes != nil || !errors.Is(err, ErrMetadataRecordQuery) || plan.inlineRoot != nil ||
			f.queries != captured || f.store.MetadataWorkUsedForTesting() != work {
			t.Fatal("rootShapes accepted an invalid capture, retained work or reread the SQL root", err)
		}
	}
	for _, test := range []struct {
		name, workspace string
		store           *SQLiteStore
		tx              *sqlTx
		size            int64
	}{
		{"foreign-store", f.workspace, f.foreign, f.tx, int64(len(f.raw))},
		{"foreign-transaction", f.workspace, f.store, &sqlTx{database: f.store.db, dialect: f.store.dialect}, int64(len(f.raw))},
		{"foreign-workspace", f.workspace + "-foreign", f.store, f.tx, int64(len(f.raw))},
		{"foreign-size", f.workspace, f.store, f.tx, int64(len(f.raw)) + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, err := test.store.scanMetadataInlineRoot(ctx, test.tx, test.workspace, test.size)
			if root != nil {
				defer root.Close()
			}
			if root != nil || !errors.Is(err, ErrMetadataRecordQuery) || f.queries != captured ||
				f.store.MetadataWorkUsedForTesting() != held {
				t.Fatal("invalid capture binding was borrowed or fell back to SQL", test.name, err)
			}
			// Keep the plan's SQL binding valid while making the captured
			// descriptor disagree in exactly the selected dimension.
			capture := &metadataInlineRoot{
				ctx: f.ctx, source: f.root.source, fields: f.root.fields,
				store: test.store, tx: test.tx, workspace: test.workspace, spool: f.root.spool,
			}
			if test.size != int64(len(f.raw)) {
				capture.source = &metadataInlineRootReader{ctx: f.ctx, source: f.root.spool, size: test.size}
			}
			rejectShapes(t, withMetadataInlineRootCapture(f.ctx, capture), held)
		})
	}
	borrowed, err := f.store.scanMetadataInlineRoot(ctx, f.tx, f.workspace, int64(len(f.raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer borrowed.Close()
	if root, err := f.store.scanMetadataInlineRoot(withMetadataInlineRootCapture(f.ctx, borrowed),
		f.tx, f.workspace, int64(len(f.raw))); root != nil || !errors.Is(err, ErrMetadataRecordQuery) {
		if root != nil {
			root.Close()
		}
		t.Fatal("a borrowed nil-spool root was recaptured", err)
	}
	borrowed.Close()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if root, err := f.store.scanMetadataInlineRoot(canceled, f.tx, f.workspace, int64(len(f.raw))); root != nil ||
		!errors.Is(err, context.Canceled) || f.queries != captured || f.store.MetadataWorkUsedForTesting() != held {
		if root != nil {
			root.Close()
		}
		t.Fatal("canceled capture reuse was admitted or reached SQL", err)
	}
	scaleP21SW229AssertRootBytes(t, f.root, f.raw)
	f.assertReleased(t)
	rejectShapes(t, ctx, f.work)
	if root, err := f.store.scanMetadataInlineRoot(ctx, f.tx, f.workspace, int64(len(f.raw))); root != nil ||
		!errors.Is(err, ErrMetadataRecordQuery) || f.queries != captured || f.store.MetadataWorkUsedForTesting() != f.work {
		if root != nil {
			root.Close()
		}
		t.Fatal("closed capture was reused or fell back to SQL", err)
	}
}
