package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type scaleP21SW237Row struct {
	physical string
	raw      []byte
	order    int
	blob     bool
	textData bool
}

func scaleP21SW237Seed(t *testing.T, f scaleP09Fixture, rows []scaleP21SW237Row) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM issue_records WHERE workspace_key=?`, workspace); err != nil {
		t.Fatal(err)
	}
	for index, row := range rows {
		values, err := issueRecordValues(workspace, domain.Issue{
			ID: row.physical, Identifier: fmt.Sprintf("SW237-%d", index+1), Number: index + 1, Title: "Owned SQL locator",
		})
		if err != nil {
			t.Fatal(err)
		}
		id := any(row.physical)
		if row.blob {
			if f.dialect != "sqlite" {
				t.Fatal("BLOB physical-ID fixture requires SQLite")
			}
			id = []byte(row.physical)
		}
		values[1] = id
		if _, err := tx.ExecContext(t.Context(), issueRecordInsert+issueRecordValuesSQL+issueRecordUpdate, values...); err != nil {
			t.Fatal(err)
		}
		raw := any(row.raw)
		if row.textData {
			raw = string(row.raw)
		}
		if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET data=?,collection_order=? WHERE workspace_key=? AND id=?`,
			raw, row.order, workspace, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW237View(f scaleP09Fixture) *SQLiteStore {
	return &SQLiteStore{db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes, metadataResidency: newMetadataResidency()}
}

func scaleP21SW237Wire(t *testing.T, issue domain.Issue) []byte {
	t.Helper()
	raw, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScaleP21SW237IssueFrameScanOracleAndPhysicalOrder(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	records := make([]scaleP21SW237Row, 0, 126)
	for index := range 62 {
		records = append(records, scaleP21SW237Row{
			physical: fmt.Sprintf("physical-%03d", index),
			raw:      []byte(fmt.Sprintf(` { "id":"json-duplicate", "title":"text-%03d", "description":"unicode \u4e2d\u6587 <>& \u0000", "documentContent":{"content":"body","contentData":{"n":9007199254740993}} } `, index)),
			order:    0, textData: f.dialect == "sqlite" && index%2 == 0,
		})
	}
	if f.dialect == "sqlite" {
		for index := range 60 {
			records = append(records, scaleP21SW237Row{
				physical: fmt.Sprintf("physical-%03d", index), blob: true,
				raw: []byte(fmt.Sprintf(`{"id":"json-duplicate","title":"blob-%03d","description":"raw"}`, index)), order: 0,
			})
		}
		records = append(records, scaleP21SW237Row{
			physical: "physical-wide-text", textData: true, order: 1,
			raw: []byte(`{"id":"json-wide-text","description":"` + strings.Repeat("\u4e2d", 20000) + `"}`),
		})
	}
	records = append(records, scaleP21SW237Row{physical: "physical-later", raw: []byte(`{"id":"json-later","title":"later order"}`), order: 1})
	scaleP21SW237Seed(t, f, records)
	oracleBase := &recordBase{}
	oracle, err := f.readIssueRecordsWithBase(t.Context(), f.data.Workspace.URLKey, oracleBase)
	if err != nil {
		t.Fatal(err)
	}
	view := scaleP21SW237View(f)
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	physical, stored := []string{}, [][32]byte{}
	rows, err := tx.QueryContext(t.Context(), `SELECT id,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		physical = append(physical, id)
		stored = append(stored, sha256.Sum256(raw))
		if len(raw) > MetadataRecordPageMaxBytes {
			t.Fatal("small-row paging fixture crossed the streamed-row threshold")
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	bodyQueries, batchQueries, wholeQueries, count := 0, 0, 0, 0
	var borrowed metadataLogicalNode
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		lower := strings.ToLower(query)
		if strings.Contains(lower, "from issue_records") && strings.HasPrefix(lower, "select substr(") {
			bodyQueries++
			if f.dialect == "sqlite" && !strings.Contains(lower, "substr(cast(data as blob),?,?)") {
				t.Error("SQLite row payload used character rather than byte positions", query)
			}
			if !strings.Contains(lower, "where workspace_key=? and id=?") {
				t.Error("row payload lost its physical issue locator", query)
			}
		}
		if strings.Contains(lower, "from issue_records") && strings.HasPrefix(lower, "select 0 as ordinal,") {
			batchQueries++
			if strings.Count(lower, " as ordinal,") > legacyIssueScanPageRows {
				t.Error("small-row batch crossed the existing page width")
			}
		}
		if strings.Contains(lower, "from issue_records") && strings.HasPrefix(lower, "select id,data") {
			wholeQueries++
		}
	})
	err = view.scanLegacyIssueFrames(ctx, tx, f.data.Workspace.URLKey, func(row legacyIssueFrame) error {
		borrowed = row.Node
		if count >= len(oracle) || row.PhysicalID != physical[count] {
			t.Fatal("streamed frame changed physical SQL page/order identity", count, row.PhysicalID)
		}
		scaleP21SW234CompareFrame(t, view, row.Node, oracle[count], scaleP21SW237Wire(t, oracle[count]), true)
		if row.StoredHash != stored[count] {
			t.Fatal("raw receipt was keyed by the JSON ID instead of physical ID")
		}
		count++
		return nil
	})
	expectedBatches := (len(records) + legacyIssueScanPageRows - 1) / legacyIssueScanPageRows
	if err != nil || count != len(oracle) || bodyQueries != 0 || batchQueries != expectedBatches || wholeQueries != 0 {
		t.Fatal("small-row page batching changed or payloads were reread", err, count, len(oracle), bodyQueries, batchQueries, expectedBatches, wholeQueries)
	}
	if _, err := borrowed.Value(t.Context(), metadataWorkBytes); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("row frame escaped its borrowed callback lifetime", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
		t.Fatal("frame scan retained ownership or decoded whole workspace")
	}
}

func TestScaleP21SW237IssueImageUsesStreamedRawReceipts(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	records := []scaleP21SW237Row{
		{physical: "physical-first", raw: []byte(` {"id":"embedded-other","description":"first","labels":null} `), order: 0},
		{physical: "physical-second", raw: []byte(`{"id":"embedded-other","description":"second","version":0}`), order: 0},
	}
	if f.dialect == "sqlite" {
		records = append(records, scaleP21SW237Row{
			physical: "physical-first", blob: true,
			raw: []byte(" \n" + `{"id":"embedded-blob","description":"last physical string receipt"}`), order: 0,
		})
	}
	scaleP21SW237Seed(t, f, records)
	workspace := f.data.Workspace.URLKey
	oracleBase := &recordBase{}
	oracle, err := f.readIssueRecordsWithBase(t.Context(), workspace, oracleBase)
	if err != nil {
		t.Fatal(err)
	}
	view := scaleP21SW237View(f)
	data := cloneBootstrap(f.data)
	data.Issues = nil
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	base, err := view.newLegacyRecordBase(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	chunks, batches := 0, 0
	ctx := WithSQLStatementObserver(contextWithLegacyRecordBase(t.Context(), base), func(query string) {
		if strings.HasPrefix(strings.ToLower(query), "select substr(") && strings.Contains(query, "issue_records") {
			chunks++
		}
		if strings.HasPrefix(strings.ToLower(query), "select 0 as ordinal,") && strings.Contains(query, "issue_records") {
			batches++
		}
		if strings.HasPrefix(strings.ToLower(query), "select id,data") && strings.Contains(query, "issue_records") {
			t.Error("raw receipt capture caused a second full SQL payload read")
		}
	})
	image, err := view.prepareLegacyIssueImage(ctx, tx, workspace, source)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Release()
	field, _, _ := metadataLogicalField("issues")
	count := 0
	if err := image.fields[field].Children(t.Context(), func(index reflect.Value, node metadataLogicalNode) error {
		if index.Int() != int64(count) || count >= len(oracle) {
			t.Fatal("legacy image lost SQL order")
		}
		scaleP21SW234CompareFrame(t, view, node, oracle[count], scaleP21SW237Wire(t, oracle[count]), true)
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != len(oracle) || chunks != 0 || batches != 1 {
		t.Fatal("legacy image did not retain one small-row batch", count, len(oracle), chunks, batches)
	}
	for physical, want := range oracleBase.issues {
		got, found, err := base.fingerprint(t.Context(), "issue", "", physical)
		if err != nil || !found || got.stored != want.stored {
			t.Fatal("raw receipt differs from literal base, including TEXT/BLOB overwrite order", physical, found, err)
		}
	}
}

func TestScaleP21SW237HybridBatchBytesAndThreshold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	sized := func(size int) []byte {
		const prefix, suffix = `{"id":"embedded","description":"`, `"}`
		return []byte(prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix)
	}
	records := []scaleP21SW237Row{
		{physical: "a-0", raw: sized(80 << 10)},
		{physical: "a-1", raw: sized(80 << 10)},
		{physical: "a-2", raw: sized(80 << 10)},
		{physical: "b-0", raw: sized(80 << 10)},
		{physical: "c-exact", raw: sized(MetadataRecordPageMaxBytes)},
		{physical: "d-wide", textData: f.dialect == "sqlite",
			raw: []byte(`{"id":"embedded-wide","description":"` + strings.Repeat("\u4e2d", 100000) + `"}`)},
		{physical: "e-after", raw: []byte(`{"id":"after","description":"small after streamed row"}`)},
	}
	if len(records[4].raw) != MetadataRecordPageMaxBytes || len(records[5].raw) <= MetadataRecordPageMaxBytes {
		t.Fatal("hybrid threshold fixture did not straddle its existing boundary")
	}
	scaleP21SW237Seed(t, f, records)
	oracle, err := f.readIssueRecordsWithBase(t.Context(), f.data.Workspace.URLKey, &recordBase{})
	if err != nil {
		t.Fatal(err)
	}
	view := scaleP21SW237View(f)
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var batchWidths []int
	count, chunks := 0, 0
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		lower := strings.ToLower(query)
		if !strings.Contains(lower, "from issue_records") {
			return
		}
		if strings.HasPrefix(lower, "select 0 as ordinal,") {
			batchWidths = append(batchWidths, strings.Count(lower, " as ordinal,"))
		}
		if strings.HasPrefix(lower, "select substr(") {
			chunks++
			if f.dialect == "sqlite" && !strings.Contains(lower, "substr(cast(data as blob),?,?)") {
				t.Error("large UTF-8 TEXT row did not use byte offsets", query)
			}
		}
	})
	err = view.scanLegacyIssueFrames(ctx, tx, f.data.Workspace.URLKey, func(row legacyIssueFrame) error {
		if count >= len(records) || row.PhysicalID != records[count].physical || row.StoredHash != sha256.Sum256(records[count].raw) {
			t.Fatal("hybrid threshold routing changed row identity/order/raw receipt", count, row.PhysicalID)
		}
		scaleP21SW234CompareFrame(t, view, row.Node, oracle[count], scaleP21SW237Wire(t, oracle[count]), false)
		count++
		return nil
	})
	wantChunks := (len(records[5].raw) + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes
	if err != nil || count != len(records) || chunks != wantChunks || !reflect.DeepEqual(batchWidths, []int{3, 1, 1, 1}) {
		t.Fatal("hybrid routing changed existing contiguous byte batches or streamed small rows", err, count, chunks, wantChunks, batchWidths)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("hybrid routing retained work or decoded complete metadata")
	}
}

func TestScaleP21SW237IssueFrameScanSupportedEditorRow(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	raw, requestBytes, stateBytes := scaleP21SW234EditorProducerRow(t)
	records := []scaleP21SW237Row{
		{physical: "a-small", raw: []byte(`{"id":"small-before","description":"before"}`)},
		{physical: "physical-editor", raw: raw},
		{physical: "z-small", raw: []byte(`{"id":"small-after","description":"after"}`)},
	}
	scaleP21SW237Seed(t, f, records)
	oracleBase := &recordBase{}
	oracle, err := f.readIssueRecordsWithBase(t.Context(), f.data.Workspace.URLKey, oracleBase)
	if err != nil || len(oracle) != len(records) {
		t.Fatal("literal editor row oracle", err, len(oracle))
	}
	view := scaleP21SW237View(f)
	view.maxStateBytes = 1 << 20
	view.metadataResidency.work.limit = 16 << 20
	if len(raw) <= view.maxStateBytes {
		t.Fatal("editor fixture did not exceed the reduced metadata-root cap")
	}
	metadata := cloneBootstrap(f.data)
	metadata.Issues = oracle
	if root, err := view.encodeWorkspaceMetadata(metadata); err != nil || len(root) > view.maxStateBytes {
		t.Fatal("valid metadata root with separately stored large Issues was refused", err)
	}
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
	count, chunks, batches := 0, 0, 0
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.HasPrefix(strings.ToLower(query), "select substr(") && strings.Contains(query, "issue_records") {
			chunks++
		}
		if strings.HasPrefix(strings.ToLower(query), "select 0 as ordinal,") && strings.Contains(query, "issue_records") {
			batches++
		}
	})
	err = view.scanLegacyIssueFrames(ctx, tx, f.data.Workspace.URLKey, func(row legacyIssueFrame) error {
		if count >= len(records) || row.PhysicalID != records[count].physical || row.StoredHash != sha256.Sum256(records[count].raw) {
			t.Fatal("large editor row lost physical/raw identity")
		}
		scaleP21SW234CompareFrame(t, view, row.Node, oracle[count], scaleP21SW237Wire(t, oracle[count]), false)
		count++
		return nil
	})
	if err != nil || count != len(records) || chunks != (len(raw)+metadataRecordChunkBytes-1)/metadataRecordChunkBytes || batches != 2 {
		t.Fatal("supported editor row refused or changed contiguous small batching", err, count, chunks, batches)
	}
	func() {
		data := cloneBootstrap(f.data)
		data.Issues = nil
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID})
		if err != nil {
			t.Fatal(err)
		}
		defer source.Release()
		image, err := view.prepareLegacyIssueImage(t.Context(), tx, f.data.Workspace.URLKey, source)
		if err != nil {
			t.Fatal("large Issue image was incorrectly bounded by maxStateBytes", err)
		}
		defer image.Release()
		field, _, _ := metadataLogicalField("issues")
		count := 0
		if err := image.fields[field].Children(t.Context(), func(index reflect.Value, node metadataLogicalNode) error {
			if index.Int() != int64(count) || count >= len(oracle) {
				t.Fatal("large Issue image changed literal SQL order")
			}
			scaleP21SW234CompareFrame(t, view, node, oracle[count], scaleP21SW237Wire(t, oracle[count]), false)
			count++
			return nil
		}); err != nil || count != len(oracle) {
			t.Fatal("large Issue image differs from literal normalized rows", err, count, len(oracle))
		}
	}()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
		t.Fatal("large editor row retained ownership or decoded whole metadata")
	}
	t.Logf("HTTP_request_bytes=%d canonical_row_bytes=%d Ygo_state_bytes=%d SQL_chunks=%d small_batches=%d root_limit=%d work_limit=%d",
		requestBytes, len(raw), stateBytes, chunks, batches, view.maxStateBytes, view.metadataResidency.work.limit)
}

func TestScaleP21SW237IssueFrameScanCancellationIOAndChangedSize(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	raw := []byte(`{"id":"embedded","description":"` + strings.Repeat("large body ", 100000) + `"}`)
	view := scaleP21SW237View(f)
	sentinel := errors.New("sw237 callback failed")
	for _, scenario := range []string{"cancel-before", "cancel-during", "closed-tx", "grow-after-header", "shrink-after-header", "work-denied", "callback"} {
		t.Run(scenario, func(t *testing.T) {
			scaleP21SW237Seed(t, f, []scaleP21SW237Row{{physical: "physical-io", raw: raw}})
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			header := legacyIssueScanHeader{id: "physical-io", size: int64(len(raw))}
			if scenario == "grow-after-header" || scenario == "shrink-after-header" {
				next := append(bytes.Clone(raw), ' ')
				if scenario == "shrink-after-header" {
					next = []byte(`{"id":"shrank"}`)
				}
				if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`,
					next, f.data.Workspace.URLKey, header.id); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			chunks, callbacks := 0, 0
			if scenario == "cancel-before" {
				cancel()
			}
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if strings.HasPrefix(strings.ToLower(query), "select substr(") && strings.Contains(query, "issue_records") {
					chunks++
					if scenario == "cancel-during" && chunks == 3 {
						cancel()
					}
				}
			})
			if scenario == "closed-tx" {
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "work-denied" {
				limit := view.metadataResidency.work.limit
				view.metadataResidency.work.limit = 1
				defer func() { view.metadataResidency.work.limit = limit }()
			}
			disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
			err = view.visitLegacyIssueFrame(ctx, tx, f.data.Workspace.URLKey, header, func(legacyIssueFrame) error {
				callbacks++
				if scenario != "callback" {
					t.Fatal("failed/cancelled/changed row reached its callback", scenario)
				}
				return sentinel
			})
			switch scenario {
			case "cancel-before", "cancel-during":
				if !errors.Is(err, context.Canceled) || scenario == "cancel-before" && chunks != 0 || scenario == "cancel-during" && chunks != 3 {
					t.Fatal("cancellation did not stop at the real SQL boundary", err, chunks)
				}
			case "closed-tx":
				if !errors.Is(err, sql.ErrTxDone) {
					t.Fatal("SQL backing failure was hidden or reclassified", err)
				}
			case "grow-after-header", "shrink-after-header":
				if !errors.Is(err, io.ErrUnexpectedEOF) || callbacks != 0 {
					t.Fatal("changed row size was silently accepted", scenario, err, callbacks)
				}
			case "callback":
				if !errors.Is(err, sentinel) || callbacks != 1 {
					t.Fatal("callback error/lifetime changed", err, callbacks)
				}
			case "work-denied":
				if !errors.Is(err, ErrMetadataWorkBudget) || chunks != 0 || callbacks != 0 {
					t.Fatal("work denial copied SQL payload or exposed a row", err, chunks, callbacks)
				}
			}
			if view.MetadataWorkUsedForTesting() != 0 ||
				metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
				t.Fatal("failed frame scan retained native/disk/descriptor/spool ownership")
			}
		})
	}
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	recovered := 0
	if err := view.scanLegacyIssueFrames(t.Context(), tx, f.data.Workspace.URLKey, func(legacyIssueFrame) error {
		recovered++
		return nil
	}); err != nil || recovered != 1 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("frame scan did not recover after cancellation, IO, size-change and admission failures", err, recovered)
	}
}
