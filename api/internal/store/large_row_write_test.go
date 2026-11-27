package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Large metadata rows written by the bounded legacy metadata writer
// (persistLegacyMetadataImage) against the literal delta writer, at the
// sizes the literal writer accepts in one transaction and just above them.

const largeRowWriteTx = 32 << 20

type largeRowWriteCase struct {
	name   string
	fields []string
	seed   func(*domain.Bootstrap)
	write  func(*domain.Bootstrap)
	peer   func(t *testing.T, tx *sqlTx, workspace string, base domain.Bootstrap)
	// refused: the literal writer refuses this size; both must refuse alike.
	refused bool
	// bounded: the bounded writer's admitted work stays within 5 rows +
	// 8 MiB and its Go heap growth within heapRows (default 6) rows + 32 MiB,
	// a row being the largest encoded record of the case. Unbounded cases
	// are the existing full-field kernel (a collection's first record) and
	// the typed merge of an offloaded document both writers changed.
	bounded  bool
	heapRows int
}

var largeRowWriteAt = time.Date(2026, 11, 17, 3, 4, 5, 600, time.UTC)

// body is a legal multi-byte body with HTML, control and
// separator characters that JSON escapes; size is its UTF-8 length.
func largeRowWriteBody(seed string, size int) string {
	unit := seed + " <body>&\u00e9\u4e16\t\u2028; "
	text := strings.Repeat(unit, size/len(unit)+1)[:size]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func largeRowWriteIDs(prefix string, count int) []string {
	ids := make([]string, count)
	for index := range ids {
		ids[index] = fmt.Sprintf("%s-%07d", prefix, index)
	}
	return ids
}

func largeRowWriteBase(f scaleFixture) domain.Bootstrap {
	user := f.data.Viewer
	user.Name, user.DisplayName = "Directory current name", "Directory current display"
	stale := user
	stale.Name = "Stale embedded name"
	return domain.Bootstrap{
		Workspace: f.data.Workspace, Viewer: user, ViewerRole: "owner",
		Users: []domain.User{user},
		Loops: []domain.Loop{
			{ID: "sw296-small-loop", Name: "Small loop", Status: "published", Instructions: "Reply.", Creator: user, CreatedAt: largeRowWriteAt, UpdatedAt: largeRowWriteAt, ConnectorIDs: []string{}},
			{ID: "sw296-loop", Name: "Large loop", Status: "published", Level: "workspace", TriggerType: "schedule",
				TriggerConfig: map[string]any{"unit": "day", "interval": float64(1)}, Instructions: "Small instructions.",
				InstructionsData: map[string]any{"type": "doc"}, ConnectorIDs: []string{"connector"}, Creator: stale,
				CreatedAt: largeRowWriteAt, UpdatedAt: largeRowWriteAt, PublishedAt: &largeRowWriteAt},
		},
		Releases: []domain.Release{
			{ID: "sw296-release", SlugID: "large-release", Name: "Large release", Status: "planned", PipelineID: "pipeline", Stage: "Open",
				ProjectIDs: []string{}, IssueIDs: []string{"issue-a"}, SubscriberIDs: []string{}, Resources: []domain.ReleaseResource{},
				Creator: stale, CreatedAt: largeRowWriteAt, UpdatedAt: largeRowWriteAt},
			{ID: "sw296-other-release", SlugID: "other-release", Name: "Other release", ProjectIDs: []string{}, IssueIDs: []string{}, SubscriberIDs: []string{},
				Resources: []domain.ReleaseResource{}, Creator: user, CreatedAt: largeRowWriteAt, UpdatedAt: largeRowWriteAt},
		},
		Documents: []domain.Document{
			{ID: "sw296-document", SlugID: "large-document", Title: "Large document", Content: "Small content.", Creator: stale,
				ProjectIDs: []string{}, TeamIDs: []string{}, SubscriberIDs: []string{}, CreatedAt: largeRowWriteAt, UpdatedAt: largeRowWriteAt, Version: 3},
		},
		Projects: []domain.Project{
			{ID: "sw296-project", Name: "Large project", Description: "Small description.", Lead: &stale, CreatedAt: largeRowWriteAt, UpdatedAt: largeRowWriteAt},
		},
	}
}

func largeRowWriteLoop(data *domain.Bootstrap) *domain.Loop       { return &data.Loops[1] }
func largeRowWriteRelease(data *domain.Bootstrap) *domain.Release { return &data.Releases[0] }

func largeRowWritePut(t *testing.T, tx *sqlTx, workspace, field, key string, value any, order int) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{field, key}, metadataRecord{data: raw, order: order}, nil); err != nil {
		t.Fatal(err)
	}
}

func largeRowWriteOrder(t *testing.T, tx *sqlTx, workspace, field, key string) int {
	t.Helper()
	var order int
	if err := tx.QueryRowContext(t.Context(), `SELECT collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, key).Scan(&order); err != nil {
		t.Fatal(err)
	}
	return order
}

// largeRowWriteSeed commits base as the canonical metadata in its own
// transaction, as an earlier literal write left it.
func largeRowWriteSeed(t *testing.T, f scaleFixture, base domain.Bootstrap) {
	t.Helper()
	// The release issue index takes one row per linked issue: a release
	// with very many issues grew over several transactions.
	const step = 50_000
	var grown []string
	if len(base.Releases) > 0 && len(base.Releases[0].IssueIDs) > step {
		grown = base.Releases[0].IssueIDs
		base.Releases = slices.Clone(base.Releases)
		base.Releases[0].IssueIDs = grown[:step]
	}
	seedOnce(t, f, base)
	for count := 2 * step; grown != nil; count += step {
		release := base.Releases[0]
		release.IssueIDs = grown[:min(count, len(grown))]
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		largeRowWritePut(t, tx, base.Workspace.URLKey, "releases", release.ID, release, largeRowWriteOrder(t, tx, base.Workspace.URLKey, "releases", release.ID))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if count >= len(grown) {
			break
		}
	}
}

func seedOnce(t *testing.T, f scaleFixture, base domain.Bootstrap) {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: base.Workspace.URLKey, reader: tx})
	if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=?`, base.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	raw, err := f.encodeWorkspaceMetadata(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeWorkspaceMetadata(ctx, tx, base.Workspace.URLKey, base.Workspace.ID, raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type largeRowWriteResult struct {
	err     error
	keys    []metadataRecordKey
	image   legacyActorWriteMetadata
	rebased map[metadataRecordKey]bool
	want    domain.Bootstrap
	work    int
	heap    uint64
}

func largeRowWriteBegin(t *testing.T, f scaleFixture, workspace string) (*sqlTx, context.Context) {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx, context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
}

// largeRowWriteRun runs one case: the literal delta writer and then the
// bounded writer on the same committed preimage (warm captured or reloaded
// from SQL), each in its own rolled-back transaction after the same peer.
func largeRowWriteRun(t *testing.T, f scaleFixture, view *SQLiteStore, test largeRowWriteCase, reload bool) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	base := largeRowWriteBase(f)
	if test.seed != nil {
		test.seed(&base)
	}
	// Canonical rows were written from loaded (normalized) images.
	normalize(&base)
	largeRowWriteSeed(t, f, base)
	var before *metadataLogicalSource
	if reload {
		f.mu.Lock()
		f.forgetCopyRevisionLocked(workspace)
		f.mu.Unlock()
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
		var err error
		if before, err = view.retainMetadataLogicalSource(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(before.Release)
	} else {
		before = legacyCountWriteCapture(t, base)
	}
	scope := fullScope(t, before)
	data, err := before.Select(t.Context(), scope, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	next := cloneBootstrap(data)
	test.write(&next)
	after := legacyCountWriteCapture(t, next)

	// Literal oracle.
	oracle, ctx := largeRowWriteBegin(t, f, workspace)
	if test.peer != nil {
		test.peer(t, oracle, workspace, data)
	}
	preimage := metadataImage(t, oracle, workspace)
	literal := largeRowWriteResult{want: cloneBootstrap(next)}
	original := cloneBootstrap(data)
	_, literal.err = f.writeWorkspaceMetadataDelta(ctx, oracle, workspace, original, &literal.want)
	literal.keys, _ = conflictedMetadataKeys(literal.err)
	literal.image = metadataImage(t, oracle, workspace)
	literal.rebased = maps.Clone(oracle.rebasedMetadata)
	literalSide := sideImage(t, oracle, workspace)
	if err := oracle.Rollback(); err != nil {
		t.Fatal(err)
	}
	if test.refused != (literal.err != nil && errors.Is(literal.err, ErrTransactionWriteBudget)) {
		t.Fatalf("literal transaction budget outcome changed: refused=%t err=%v", test.refused, literal.err)
	}
	row := 0
	for _, image := range []domain.Bootstrap{data, next} {
		for _, records := range []any{image.Loops, image.LoopVersions, image.Releases, image.Documents, image.Projects} {
			value := reflect.ValueOf(records)
			for index := range value.Len() {
				raw, err := json.Marshal(value.Index(index).Interface())
				if err != nil {
					t.Fatal(err)
				}
				row = max(row, len(raw))
			}
		}
	}

	// Bounded writer.
	tx, ctx := largeRowWriteBegin(t, f, workspace)
	if test.peer != nil {
		test.peer(t, tx, workspace, data)
	}
	if got := metadataImage(t, tx, workspace); !reflect.DeepEqual(got, preimage) {
		t.Fatal("literal and bounded writers started from different SQL preimages")
	}
	ctx = context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, test.fields)
	// A low GC target keeps the sampled heap close to the live heap, so the
	// growth bound measures retained bytes rather than uncollected garbage.
	defer debug.SetGCPercent(debug.SetGCPercent(5))
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	baseline, peakHeap, peakWork := stats.HeapAlloc, stats.HeapAlloc, 0
	sample := func() {
		peakWork = max(peakWork, view.MetadataWorkUsedForTesting())
		runtime.ReadMemStats(&stats)
		peakHeap = max(peakHeap, stats.HeapAlloc)
	}
	statements := 0
	ctx = WithSQLStatementObserver(ctx, func(string) {
		statements++
		if statements%8 == 0 {
			sample()
		}
	})
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	result, gotErr := view.persistLegacyMetadataImage(ctx, tx, workspace, before, after)
	sample()
	bounded := largeRowWriteResult{err: gotErr, work: peakWork, heap: peakHeap - baseline}
	bounded.keys, _ = conflictedMetadataKeys(gotErr)
	bounded.image = metadataImage(t, tx, workspace)
	bounded.rebased = maps.Clone(tx.rebasedMetadata)
	boundedSide := sideImage(t, tx, workspace)
	if test.refused {
		// Both refuse; the bounded writer may stop at its work budget
		// before it reaches the transaction budget. Nothing is written.
		if result != nil {
			result.Release()
		}
		if !errors.Is(gotErr, ErrTransactionWriteBudget) && !errors.Is(gotErr, ErrMetadataWorkBudget) {
			t.Fatal("over-limit write was not refused", gotErr)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if used := view.MetadataWorkUsedForTesting(); used != 0 {
			t.Fatal("refused write retained work", used)
		}
		t.Logf("%s reload=%t literal_err=%v bounded_err=%v", test.name, reload, literal.err, gotErr)
		return
	}
	if (literal.err == nil) != (gotErr == nil) || literal.err != nil && literal.err.Error() != gotErr.Error() || reflect.TypeOf(literal.err) != reflect.TypeOf(gotErr) {
		if result != nil {
			result.Release()
		}
		t.Fatalf("errors differ: literal %T %v / bounded %T %v", literal.err, literal.err, gotErr, gotErr)
	}
	if !reflect.DeepEqual(literal.keys, bounded.keys) {
		t.Fatal("conflict keys differ", literal.keys, bounded.keys)
	}
	if !reflect.DeepEqual(literal.image, bounded.image) {
		sqlDiff(t, bounded.image, literal.image)
		if result != nil {
			result.Release()
		}
		t.Fatal("canonical root, rows or orders differ")
	}
	for name, rows := range literalSide.Tables {
		if !reflect.DeepEqual(rows, boundedSide.Tables[name]) {
			t.Fatalf("side table %s differs: literal %d rows, bounded %d rows", name, len(rows), len(boundedSide.Tables[name]))
		}
	}
	if len(literalSide.Tables) != len(boundedSide.Tables) || gotErr == nil && literalSide.Written != boundedSide.Written {
		t.Fatal("side tables or the transaction write charge differ", literalSide.Written, boundedSide.Written)
	}
	if !reflect.DeepEqual(literal.rebased, bounded.rebased) {
		t.Fatal("rebase markers differ", literal.rebased, bounded.rebased)
	}
	if gotErr == nil {
		if result == nil {
			t.Fatal("successful write returned no postimage")
		}
		got, err := result.Select(t.Context(), scope, 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, literal.want) {
			for index := range reflect.TypeFor[domain.Bootstrap]().NumField() {
				if !reflect.DeepEqual(reflect.ValueOf(got).Field(index).Interface(), reflect.ValueOf(literal.want).Field(index).Interface()) {
					t.Log("postimage field differs:", reflect.TypeFor[domain.Bootstrap]().Field(index).Name)
				}
			}
			result.Release()
			t.Fatal("bounded postimage differs from the literal rebased target")
		}
		if view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded writer decoded complete metadata")
		}
		result.Release()
	} else if result != nil {
		result.Release()
		t.Fatal("failed write returned a postimage")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if used := view.MetadataWorkUsedForTesting(); used != 0 {
		t.Fatal("bounded writer retained work", used)
	}
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("bounded writer leaked spools")
	}
	heapRows := test.heapRows
	if heapRows == 0 {
		heapRows = 6
	}
	if test.bounded && bounded.work > 5*row+8<<20 {
		t.Fatalf("admitted work %d exceeds 5 rows + 8 MiB (row %d)", bounded.work, row)
	}
	if test.bounded && bounded.heap > uint64(heapRows*row+32<<20) {
		t.Fatalf("heap growth %d exceeds %d rows + 32 MiB (row %d)", bounded.heap, heapRows, row)
	}
	t.Logf("%s reload=%t literal_err=%v row=%d work=%d heap=%d statements=%d", test.name, reload, literal.err, row, bounded.work, bounded.heap, statements)
}

// side is every index, blob and journal row the metadata row
// writer maintains next to the rows, plus the transaction's write charge.
type largeRowWriteSide struct {
	Tables  map[string][][]string
	Written int
}

func sideImage(t *testing.T, tx *sqlTx, workspace string) largeRowWriteSide {
	t.Helper()
	// The root statement's updated_at argument is RFC3339Nano text, whose
	// length depends on the clock; each charge excludes its own stamp.
	var updatedAt string
	if err := tx.QueryRowContext(t.Context(), `SELECT updated_at FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	side := largeRowWriteSide{Tables: map[string][][]string{}, Written: tx.writtenBytes - len(updatedAt)}
	for name, query := range map[string]string{
		"search":    `SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=? ORDER BY field,record_key`,
		"releases":  `SELECT release_id,issue_id,pipeline_id,stage,status,released_at FROM release_issue_filter_records WHERE workspace_key=? ORDER BY release_id,issue_id`,
		"customers": `SELECT customer_id,owner_id,status_value,tier_value FROM customer_filter_records WHERE workspace_key=? ORDER BY customer_id`,
		"requests":  `SELECT request_id,issue_id,customer_id,project_id FROM customer_request_filter_records WHERE workspace_key=? ORDER BY request_id`,
		"keys":      `SELECT key_id,secret_hash FROM api_key_lookup WHERE workspace_key=? ORDER BY key_id`,
		"blobs":     `SELECT blob_key,size,orphaned_at FROM metadata_blobs WHERE workspace_key=? ORDER BY blob_key`,
		"journal":   `SELECT field,record_key,source FROM record_journal_dirty WHERE workspace_key=? ORDER BY field,record_key,source`,
	} {
		rows, err := tx.QueryContext(t.Context(), query, workspace)
		if err != nil {
			t.Fatal(name, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			targets := make([]any, len(columns))
			for index := range values {
				targets[index] = &values[index]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			row := make([]string, len(values))
			for index, value := range values {
				row[index] = fmt.Sprint(value.Valid, ":", value.String)
			}
			side.Tables[name] = append(side.Tables[name], row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return side
}

func largeRowWriteCases() []largeRowWriteCase {
	// Legal maxima: an HTTP body holds at most 16 MiB, and the literal writer
	// writes a row (plus its search document) within the 32 MiB transaction
	// budget. A run start writes the loop and its v1 snapshot in one
	// transaction: 11 MiB instructions fit, 13 MiB are refused by both. The
	// release issue index grows one row per linked issue, so the
	// many-issue release is seeded over several transactions.
	// This fixture indexes search text uncapped, so searchable rows carry
	// their body twice.
	loopMax, versionMax, searchableMax, stampMax := 15<<20, 7<<20, 7<<20, 11<<20
	ids := 300_000
	stamp := func(data *domain.Bootstrap) {
		loop := largeRowWriteLoop(data)
		at := largeRowWriteAt.Add(time.Hour)
		loop.LastRunAt, loop.Version, loop.VersionID = &at, 1, loop.ID+"_v1"
		data.LoopVersions = append(data.LoopVersions, domain.LoopVersion{ID: loop.ID + "_v1", LoopID: loop.ID, Version: 1, PublishedAt: largeRowWriteAt,
			PublishedBy: data.Users[0], ChangeSummary: []string{"published"}, Definition: domain.LoopDefinition{Name: loop.Name, Instructions: loop.Instructions,
				InstructionsData: loop.InstructionsData, ConnectorIDs: slices.Clone(loop.ConnectorIDs)}})
	}
	// Another loop's version: the stamp appends to an existing collection.
	withVersion := func(seed func(*domain.Bootstrap)) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			seed(data)
			data.LoopVersions = []domain.LoopVersion{{ID: "sw296-small-loop_v1", LoopID: "sw296-small-loop", Version: 1, PublishedAt: largeRowWriteAt,
				PublishedBy: data.Users[0], ChangeSummary: []string{"published"}, Definition: domain.LoopDefinition{Name: "Small loop", Instructions: "Reply."}}}
		}
	}
	touch := func(data *domain.Bootstrap) {
		loop := largeRowWriteLoop(data)
		at := largeRowWriteAt.Add(time.Hour)
		loop.LastRunAt = &at
	}
	instructions := func(size int) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			largeRowWriteLoop(data).Instructions = largeRowWriteBody("legal large instructions", size)
		}
	}
	releaseSeed := func(count, description int) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			release := largeRowWriteRelease(data)
			release.IssueIDs = largeRowWriteIDs("issue", count)
			release.Description = largeRowWriteBody("release notes", description)
		}
	}
	addIssue := func(data *domain.Bootstrap) {
		release := largeRowWriteRelease(data)
		release.IssueIDs = append(release.IssueIDs, "added-issue")
		release.UpdatedAt = largeRowWriteAt.Add(time.Minute)
	}
	peerLoop := func(edit func(*domain.Loop)) func(*testing.T, *sqlTx, string, domain.Bootstrap) {
		return func(t *testing.T, tx *sqlTx, workspace string, base domain.Bootstrap) {
			loop := *largeRowWriteLoop(&base)
			edit(&loop)
			largeRowWritePut(t, tx, workspace, "loops", loop.ID, loop, largeRowWriteOrder(t, tx, workspace, "loops", loop.ID))
		}
	}
	peerRelease := func(edit func(*domain.Release)) func(*testing.T, *sqlTx, string, domain.Bootstrap) {
		return func(t *testing.T, tx *sqlTx, workspace string, base domain.Bootstrap) {
			release := *largeRowWriteRelease(&base)
			release.IssueIDs = slices.Clone(release.IssueIDs)
			edit(&release)
			largeRowWritePut(t, tx, workspace, "releases", release.ID, release, largeRowWriteOrder(t, tx, workspace, "releases", release.ID))
		}
	}
	deleted := func(field, key string) func(*testing.T, *sqlTx, string, domain.Bootstrap) {
		return func(t *testing.T, tx *sqlTx, workspace string, _ domain.Bootstrap) {
			if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{field, key}); err != nil {
				t.Fatal(err)
			}
		}
	}
	documentSeed := func(size int) func(*domain.Bootstrap) {
		return func(data *domain.Bootstrap) {
			document := &data.Documents[0]
			document.Content = largeRowWriteBody("legal document body", size)
			document.Revisions = []domain.DocumentRevision{
				{ID: "current", DocumentID: document.ID, Title: document.Title, Content: document.Content, Author: data.Users[0], CreatedAt: largeRowWriteAt},
				{ID: "older", DocumentID: document.ID, Title: document.Title, Content: largeRowWriteBody("older version", 4096), Author: data.Users[0], CreatedAt: largeRowWriteAt},
			}
		}
	}
	return []largeRowWriteCase{
		// Loop run start: the loop's stamp plus its v1 snapshot (a second
		// large row).
		{name: "loop-stamp-v1-at-max", fields: []string{"loops", "loopVersions"}, seed: withVersion(instructions(stampMax)), write: stamp, bounded: true},
		{name: "loop-stamp-first-version-at-max", fields: []string{"loops", "loopVersions"}, seed: instructions(stampMax), write: stamp},
		{name: "loop-stamp-v1-over", fields: []string{"loops", "loopVersions"}, seed: withVersion(instructions(13 << 20)), write: stamp, refused: true},
		{name: "loop-touch-at-max", fields: []string{"loops"}, seed: instructions(loopMax), write: touch, bounded: true},
		{name: "loop-touch-peer-renamed", fields: []string{"loops"}, seed: instructions(4 << 20), write: touch, bounded: true,
			peer: peerLoop(func(loop *domain.Loop) { loop.Name = "Peer renamed loop" })},
		{name: "loop-touch-peer-conflict", fields: []string{"loops"}, seed: instructions(4 << 20), write: touch, bounded: true,
			peer: peerLoop(func(loop *domain.Loop) {
				at := largeRowWriteAt.Add(2 * time.Hour)
				loop.LastRunAt = &at
			})},
		{name: "loop-touch-peer-instructions", fields: []string{"loops"}, seed: instructions(4 << 20), write: touch, bounded: true,
			peer: peerLoop(func(loop *domain.Loop) { loop.Instructions = largeRowWriteBody("peer instructions", 4<<20) })},
		{name: "loop-touch-peer-stale-display", fields: []string{"loops"}, seed: instructions(4 << 20), write: touch, bounded: true,
			peer: peerLoop(func(loop *domain.Loop) { loop.Creator.Name = "Older stored creator name" })},
		{name: "loop-touch-peer-deleted", fields: []string{"loops"}, seed: instructions(4 << 20), write: touch, bounded: true, peer: deleted("loops", "sw296-loop")},
		{name: "loop-edit-instructions-at-max", fields: []string{"loops"}, seed: instructions(loopMax), write: instructions(loopMax - 7), bounded: true},
		{name: "loop-edit-instructions-peer-renamed", fields: []string{"loops"}, seed: instructions(4 << 20), write: instructions(4<<20 - 3), bounded: true,
			peer: peerLoop(func(loop *domain.Loop) { loop.Name = "Peer renamed loop" })},
		{name: "loop-edit-instructions-peer-instructions-conflict", fields: []string{"loops"}, seed: instructions(4 << 20), write: instructions(4<<20 - 3), bounded: true,
			peer: peerLoop(func(loop *domain.Loop) { loop.Instructions = largeRowWriteBody("peer instructions", 4<<20) })},
		{name: "loop-edit-instructions-peer-same-instructions", fields: []string{"loops"}, seed: instructions(4 << 20), write: instructions(4<<20 - 3), bounded: true,
			peer: peerLoop(func(loop *domain.Loop) { loop.Instructions = largeRowWriteBody("legal large instructions", 4<<20-3) })},
		{name: "loop-added-at-max", fields: []string{"loops"}, write: func(data *domain.Bootstrap) {
			loop := *largeRowWriteLoop(data)
			loop.ID, loop.Instructions = "sw296-added-loop", largeRowWriteBody("added loop", loopMax)
			data.Loops = append(data.Loops, loop)
		}, bounded: true},
		{name: "loop-removed-at-max", fields: []string{"loops"}, seed: instructions(loopMax), write: func(data *domain.Bootstrap) {
			data.Loops = data.Loops[:1]
		}, bounded: true},
		// Releases: many issue IDs (the row grows by one ID per linked
		// issue) and a large description.
		{name: "release-add-issue-many-ids", fields: []string{"releases"}, seed: releaseSeed(ids, 64<<10), write: addIssue, bounded: true, heapRows: 16},
		{name: "release-add-issue-large-description", fields: []string{"releases"}, seed: releaseSeed(1000, searchableMax), write: addIssue, bounded: true},
		{name: "release-add-issue-peer-added", fields: []string{"releases"}, seed: releaseSeed(50_000, 4<<20), write: addIssue, bounded: true,
			peer: peerRelease(func(release *domain.Release) { release.IssueIDs = append(release.IssueIDs, "peer-issue") })},
		{name: "release-add-issue-peer-description", fields: []string{"releases"}, seed: releaseSeed(1000, 4<<20), write: addIssue, bounded: true,
			peer: peerRelease(func(release *domain.Release) { release.Description = largeRowWriteBody("peer notes", 4<<20) })},
		{name: "release-add-issue-peer-status-conflict", fields: []string{"releases"}, seed: releaseSeed(1000, 4<<20), write: func(data *domain.Bootstrap) {
			addIssue(data)
			largeRowWriteRelease(data).Status = "started"
		}, bounded: true, peer: peerRelease(func(release *domain.Release) { release.Status = "completed" })},
		{name: "release-add-issue-peer-deleted", fields: []string{"releases"}, seed: releaseSeed(50_000, 1<<20), write: addIssue, peer: deleted("releases", "sw296-release")},
		{name: "release-removed", fields: []string{"releases"}, seed: releaseSeed(ids, 1<<20), write: func(data *domain.Bootstrap) {
			data.Releases = data.Releases[1:]
		}},
		// Documents are offloaded rows: version bodies move to blobs.
		{name: "document-title-at-max", fields: []string{"documents"}, seed: documentSeed(versionMax), write: func(data *domain.Bootstrap) {
			data.Documents[0].Title, data.Documents[0].Version = "Renamed large document", 4
		}, bounded: true},
		// updateDocument's 4 MiB body edit: the current version moves with
		// the content and the previous body is offloaded.
		{name: "document-content-edit-4mib", fields: []string{"documents"}, seed: documentSeed(4 << 20), write: func(data *domain.Bootstrap) {
			document := &data.Documents[0]
			previous := document.Revisions[0]
			document.Content, document.Version = largeRowWriteBody("edited document body", 4<<20), 4
			document.Revisions = append([]domain.DocumentRevision{{ID: "edited", DocumentID: document.ID, Title: document.Title, Content: document.Content,
				Author: data.Users[0], CreatedAt: largeRowWriteAt.Add(time.Hour)}, previous}, document.Revisions[1:]...)
		}, bounded: true},
		{name: "document-title-peer-conflict", fields: []string{"documents"}, seed: documentSeed(2 << 20), write: func(data *domain.Bootstrap) {
			data.Documents[0].Title = "Renamed large document"
		}, peer: func(t *testing.T, tx *sqlTx, workspace string, base domain.Bootstrap) {
			document := base.Documents[0]
			document.Title = "Peer renamed document"
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"documents", document.ID}, metadataRecord{data: raw, order: largeRowWriteOrder(t, tx, workspace, "documents", document.ID)}, nil); err != nil {
				t.Fatal(err)
			}
		}},
		// Projects outside the progress closure take the generic kernel.
		{name: "project-renamed-large-description", seed: func(data *domain.Bootstrap) {
			data.Projects[0].Description = largeRowWriteBody("project description", searchableMax)
		}, write: func(data *domain.Bootstrap) { data.Projects[0].Name = "Renamed project" }, bounded: true},
	}
}

func TestLargeRowWriteDifferential(t *testing.T) {
	largeRowWrites(t, 0, false)
}

func TestLargeRowWriteDifferentialPart1Reloaded(t *testing.T) {
	largeRowWrites(t, 0, true)
}

func TestLargeRowWriteDifferentialPart2(t *testing.T) {
	largeRowWrites(t, 1, false, true)
}

func TestLargeRowWriteDifferentialPart3(t *testing.T) {
	largeRowWrites(t, 2, false)
}

func TestLargeRowWriteDifferentialPart3Reloaded(t *testing.T) {
	largeRowWrites(t, 2, true)
}

func TestLargeRowWriteDifferentialPart4(t *testing.T) {
	largeRowWrites(t, 3, false)
}

func TestLargeRowWriteDifferentialPart4Reloaded(t *testing.T) {
	largeRowWrites(t, 3, true)
}

func TestLargeRowWriteDifferentialPart5(t *testing.T) {
	largeRowWrites(t, 4, false, true)
}

func TestLargeRowWriteDifferentialReleaseAdd(t *testing.T) {
	largeRowWrites(t, 5, false, true)
}

func TestLargeRowWriteDifferentialReleaseRemove(t *testing.T) {
	largeRowWrites(t, 6, false, true)
}

func TestLargeRowWriteDifferentialDocumentTitle(t *testing.T) {
	largeRowWrites(t, 7, false, true)
}

func largeRowWrites(t *testing.T, part int, reloads ...bool) {
	t.Helper()
	f, view := legacyCountWriteFixture(t)
	f.db.SetMaxOpenConns(4)
	if metadataWorkBytes() != 256<<20 || f.db.maxTransactionBytes != largeRowWriteTx {
		t.Fatal("SW296 sizes assume the production work and transaction budgets", f.db.maxTransactionBytes)
	}
	for index, test := range largeRowWriteCases() {
		shard := index % 5
		// The 300,000-link fixtures each seed and write a large release
		// twice. Keep them apart from the other large-row tests.
		switch test.name {
		case "release-add-issue-many-ids":
			shard = 5
		case "release-removed":
			shard = 6
		case "document-title-at-max":
			// The largest offloaded document dominates the race shard.
			shard = 7
		}
		if shard != part {
			continue
		}
		for _, reload := range reloads {
			t.Run(fmt.Sprintf("%s/reload=%t", test.name, reload), func(t *testing.T) {
				largeRowWriteRun(t, f, view, test, reload)
			})
		}
	}
}
