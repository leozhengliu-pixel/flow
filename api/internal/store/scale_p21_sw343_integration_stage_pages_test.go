package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func scaleP21SW343Target(workspace, workspaceID, field string, size int) metadataSQLStageTarget {
	target := scaleP21SW333Target(workspace, workspaceID, field, size)
	if !target.workspaceState {
		target.key = "sw343-" + workspaceID
	}
	return target
}

func TestScaleP21SW343IntegrationStagePages(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	view := scaleP21ResidentProbe(f.SQLiteStore)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	const ownedChunk = 1 << 20
	const eventChunk = 256 << 10
	const logicalChunk = 16 << 10
	if metadataSQLIntegrationOwnedChunkBytes != ownedChunk || integrationDeliveryPageWorkBytes != 8<<20 ||
		MetadataRecordPageMaxBytes != eventChunk || metadataRecordChunkBytes != logicalChunk {
		t.Fatal("private SQL coalescing changed its owner, physical reads or logical pages")
	}
	t.Run("selection", func(t *testing.T) {
		for _, test := range []struct {
			event, field string
			private      bool
			size, chunk  int
		}{
			{"integration_delivery.claimed", "integrationDeliveries", true, ownedChunk + 1, ownedChunk},
			{"integration_delivery.retry", "integrationConnections", true, ownedChunk + 1, ownedChunk},
			{"integration_delivery.retry", "", true, ownedChunk + 1, ownedChunk},
			{"integration_delivery.retry", "integrationDeliveries", true, 19, ownedChunk},
			{"integration_delivery.retry", "integrationConnections", true, 0, ownedChunk},
			{"integration_delivery.claimed", "integrationDeliveries", false, ownedChunk + 1, eventChunk},
			{"integration_delivery.retry", "integrationConnections", false, ownedChunk + 1, eventChunk},
			{"integration_delivery.retry", "", false, ownedChunk + 1, eventChunk},
			{"", "integrationDeliveries", false, ownedChunk + 1, logicalChunk},
			{"integration_delivery.other", "integrationDeliveries", false, ownedChunk + 1, logicalChunk},
			{"integration_delivery.retry", "agentSessions", true, ownedChunk + 1, logicalChunk},
			{"integration_delivery.retry", "projects", true, ownedChunk + 1, logicalChunk},
		} {
			t.Run(fmt.Sprintf("%s/%s/private%t/size%d", test.event, test.field, test.private, test.size), func(t *testing.T) {
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				ctx := withIntegrationDeliveryStream(t.Context(), test.event)
				if test.private {
					var closePages func()
					ctx, closePages, err = view.integrationDeliveryPageContext(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer closePages()
					if view.MetadataWorkUsedForTesting() != integrationDeliveryPageWorkBytes {
						t.Fatal("SQL pages lack their complete private owner")
					}
				}
				chunk, err := integrationDeliverySQLChunkBytes(ctx, tx)
				if err != nil || chunk != 0 && chunk != ownedChunk || test.private != (chunk == ownedChunk) {
					t.Fatal("SQL owner selection", chunk, err, test.private)
				}
				readPage, err := integrationDeliveryCanonicalPageBytes(ctx, tx)
				if err != nil || readPage != 0 && readPage != eventChunk || test.private != (readPage == eventChunk) {
					t.Fatal("SQL append scope changed the physical reader", readPage, err)
				}
				stage, err := beginMetadataSQLWriteStage(ctx, tx, scaleP21SW343Target(workspace, workspaceID, test.field, test.size))
				if err != nil {
					t.Fatal(err)
				}
				defer stage.Close(context.WithoutCancel(ctx))
				writer, err := stage.writer(ctx, "data")
				if err != nil || stage.appendChunkBytes != test.chunk || cap(writer.buffer) != min(test.size, test.chunk) {
					t.Fatal("target scope or small-buffer allocation changed", stage.appendChunkBytes, test.chunk, err)
				}
			})
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("SQL page selection retained private work")
		}
	})
	t.Run("invalid-owner-before-sql", func(t *testing.T) {
		foreign := scaleP09Database(t)
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		foreignTx, err := foreign.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer foreignTx.Rollback()
		ctx, closePages, err := view.integrationDeliveryPageContext(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"))
		if err != nil {
			t.Fatal(err)
		}
		defer closePages()
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		closed, closeClosed, err := view.integrationDeliveryPageContext(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"))
		if err != nil {
			t.Fatal(err)
		}
		closeClosed()
		oneByteOwner := &ResourceRead{store: view}
		if err := oneByteOwner.reserve(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
		defer oneByteOwner.Close()
		for _, test := range []struct {
			name string
			ctx  context.Context
			tx   *sqlTx
			err  error
		}{
			{"wrong-token-type", context.WithValue(ctx, integrationDeliveryPagesKey{}, struct{}{}), tx, ErrBoundedMetadataCoverage},
			{"unadmitted-owner", context.WithValue(ctx, integrationDeliveryPagesKey{}, &ResourceRead{store: view}), tx, ErrBoundedMetadataCoverage},
			{"one-byte-owner", context.WithValue(ctx, integrationDeliveryPagesKey{}, oneByteOwner), tx, ErrBoundedMetadataCoverage},
			{"wrapper-without-receipt", context.WithValue(ctx, integrationDeliveryPagesKey{}, &integrationDeliveryPageOwner{work: oneByteOwner}), tx, ErrBoundedMetadataCoverage},
			{"wrapper-without-work", context.WithValue(ctx, integrationDeliveryPagesKey{}, &integrationDeliveryPageOwner{admitted: true}), tx, ErrBoundedMetadataCoverage},
			{"closed", closed, tx, ErrBoundedMetadataCoverage},
			{"cancelled", cancelled, tx, context.Canceled},
			{"foreign-database", ctx, foreignTx, ErrBoundedMetadataCoverage},
			{"nil-transaction", ctx, nil, ErrBoundedMetadataCoverage},
			{"missing-event", context.WithValue(ctx, integrationDeliveryStreamKey{}, ""), tx, ErrBoundedMetadataCoverage},
		} {
			t.Run(test.name, func(t *testing.T) {
				queries := 0
				ctx := WithSQLStatementObserver(test.ctx, func(string) { queries++ })
				if _, err := integrationDeliverySQLChunkBytes(ctx, test.tx); !errors.Is(err, test.err) {
					t.Fatal("private SQL helper accepted an invalid owner", err, test.err)
				}
				billed, owners, stages := 0, 0, 0
				if test.tx != nil {
					billed, owners, stages = test.tx.writtenBytes, len(test.tx.metadataSQLStageOwners), test.tx.metadataSQLStages
				}
				stage, err := beginMetadataSQLWriteStage(ctx, test.tx, scaleP21SW343Target(workspace, workspaceID, "integrationDeliveries", ownedChunk+1))
				if stage != nil || !errors.Is(err, test.err) || queries != 0 {
					t.Fatal("invalid private SQL stage reached SQL", stage != nil, err, queries)
				}
				if test.tx != nil && (test.tx.writtenBytes != billed || len(test.tx.metadataSQLStageOwners) != owners || test.tx.metadataSQLStages != stages) {
					t.Fatal("rejected private SQL owner changed accounting or stage ownership")
				}
			})
		}
		closePages()
		oneByteOwner.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("invalid-owner checks retained private admission")
		}
	})
	t.Run("owner-lifetime-after-admission", func(t *testing.T) {
		for _, operation := range []string{"allocate", "write", "exec", "replace"} {
			t.Run(operation, func(t *testing.T) {
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				queries := 0
				ctx := WithSQLStatementObserver(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"), func(string) { queries++ })
				ctx, closePages, err := view.integrationDeliveryPageContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer closePages()
				stage, err := beginMetadataSQLWriteStage(ctx, tx, scaleP21SW343Target(workspace, workspaceID, "integrationDeliveries", ownedChunk+1))
				if err != nil {
					t.Fatal(err)
				}
				defer stage.Close(context.WithoutCancel(ctx))
				var writer *metadataSQLStageWriter
				if operation == "write" {
					writer, err = stage.writer(ctx, "data")
					if err != nil {
						t.Fatal(err)
					}
				}
				if operation == "replace" {
					var closeReplacement func()
					ctx, closeReplacement, err = view.integrationDeliveryPageContext(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer closeReplacement()
				} else {
					closePages()
				}
				billed, beforeQueries := tx.writtenBytes, queries
				switch operation {
				case "allocate", "replace":
					writer, err = stage.writer(ctx, "data")
					if writer != nil {
						t.Fatal("invalidated private owner allocated a writer")
					}
				case "write":
					_, err = writer.Write([]byte("x"))
					if len(writer.buffer) != 0 {
						t.Fatal("closed private owner accepted new buffered bytes")
					}
				case "exec":
					_, err = stage.exec(ctx, metadataSQLStageAppendData, stage.id, stage.chunks[0], "x")
				}
				if !errors.Is(err, ErrBoundedMetadataCoverage) || queries != beforeQueries || tx.writtenBytes != billed || stage.written[0] != 0 {
					t.Fatal("invalidated stage owner reached SQL or changed admission", operation, err, queries, beforeQueries)
				}
				if err := stage.Close(context.WithoutCancel(ctx)); err != nil || !stage.closed || tx.metadataSQLStages != 0 {
					t.Fatal("invalidated private owner prevented stage cleanup", err)
				}
			})
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("stage lifetime checks retained private admission")
		}
	})
	t.Run("immutable-chunk-declaration", func(t *testing.T) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		ctx, closePages, err := view.integrationDeliveryPageContext(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"))
		if err != nil {
			t.Fatal(err)
		}
		defer closePages()
		queries := 0
		ctx = WithSQLStatementObserver(ctx, func(string) { queries++ })
		stage, err := beginMetadataSQLWriteStage(ctx, tx, scaleP21SW343Target(workspace, workspaceID, "integrationDeliveries", 2*ownedChunk))
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close(context.WithoutCancel(ctx))
		billed, initialQueries := tx.writtenBytes, queries
		for _, forgedChunk := range []int{logicalChunk, eventChunk, ownedChunk + 1} {
			stage.appendChunkBytes = forgedChunk
			if _, err := stage.writer(ctx, "data"); !errors.Is(err, ErrMetadataRecordQuery) || queries != initialQueries {
				t.Fatal("private chunk declaration was mutable", forgedChunk, err, queries)
			}
		}
		stage.appendChunkBytes = ownedChunk
		if _, err := stage.exec(ctx, metadataSQLStageAppendData, stage.id, stage.chunks[0], strings.Repeat("x", ownedChunk+1)); !errors.Is(err, ErrBoundedMetadataCoverage) ||
			tx.writtenBytes != billed || stage.written[0] != 0 || queries != initialQueries {
			t.Fatal("oversized private SQL argument changed admission", err, queries, tx.writtenBytes, billed)
		}
	})
	t.Run("utf8-boundary-and-physical-row", func(t *testing.T) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		ctx, closePages, err := view.integrationDeliveryPageContext(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"))
		if err != nil {
			t.Fatal(err)
		}
		defer closePages()
		raw := []byte(`{"body":"` + strings.Repeat("x", ownedChunk-2-len(`{"body":"`)) + "\U0001f600" + strings.Repeat("y", ownedChunk+17) + `"}`)
		appends := 0
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if strings.HasPrefix(query, "INSERT INTO bounded_metadata_write_chunks(operation_id,part,seq,chunk) VALUES(?,0,") {
				appends++
			}
		})
		target := scaleP21SW343Target(workspace, workspaceID, "integrationDeliveries", len(raw))
		target.order = 17
		stage, err := beginMetadataSQLWriteStage(ctx, tx, target)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close(context.WithoutCancel(ctx))
		writer, err := stage.writer(ctx, "data")
		if err != nil {
			t.Fatal(err)
		}
		for start := 0; start < len(raw); start += logicalChunk {
			if _, err := writer.Write(raw[start:min(start+logicalChunk, len(raw))]); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.flush(true); err != nil || appends != 3 {
			t.Fatal("private append changed UTF-8 boundary coalescing", appends, err)
		}
		if err := stage.writeMetadataRow(ctx); err != nil {
			t.Fatal(err)
		}
		var got []byte
		var order int
		if err := tx.QueryRowContext(t.Context(), `SELECT data,collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`,
			workspace, target.field, target.key).Scan(&got, &order); err != nil || !bytes.Equal(got, raw) || order != target.order {
			t.Fatal("private SQL chunks changed physical bytes or collection order", err, order)
		}
	})
	t.Run("cancel-on-actual-second-append", func(t *testing.T) {
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		ctx, closePages, err := view.integrationDeliveryPageContext(withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry"))
		if err != nil {
			t.Fatal(err)
		}
		defer closePages()
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		appends := 0
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if strings.HasPrefix(query, "INSERT INTO bounded_metadata_write_chunks(operation_id,part,seq,chunk) VALUES(?,0,") {
				appends++
				if appends == 2 {
					cancel()
				}
			}
		})
		stage, err := beginMetadataSQLWriteStage(ctx, tx, scaleP21SW343Target(workspace, workspaceID, "integrationDeliveries", 3*ownedChunk))
		if err != nil {
			t.Fatal(err)
		}
		writer, err := stage.writer(ctx, "data")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(bytes.Repeat([]byte{'x'}, 3*ownedChunk)); !errors.Is(err, context.Canceled) || appends != 2 || stage.written[0] != ownedChunk {
			t.Fatal("private append missed cancellation after its first successful append", appends, stage.written[0], err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		var rows int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage WHERE operation_id=?`, stage.id).Scan(&rows); err != nil || rows != 0 {
			t.Fatal("cancelled private SQL stage survived rollback", rows, err)
		}
	})
	t.Run("literal-row-and-root-byte-bills", func(t *testing.T) {
		raw := []byte(`{"body":"` + strings.Repeat("x", ownedChunk+37) + `"}`)
		for _, field := range []string{"integrationDeliveries", ""} {
			target := scaleP21SW343Target(workspace, workspaceID, field, len(raw))
			bill := len(metadataSQLStageLegacyRowSQL) + len(workspace) + len(field) + len(target.key) + 16 + len(raw)
			if target.workspaceState {
				bill = len(metadataSQLStageLegacyRootSQL) + len(workspaceID) + len(raw) + len(target.updatedAt) + len(workspace)
			}
			write := func(staged bool, limit int) (int, bool) {
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				tx.maxTransactionBytes = limit
				ctx := withIntegrationDeliveryStream(t.Context(), "integration_delivery.retry")
				if !staged {
					if target.workspaceState {
						_, err = tx.ExecContext(ctx, metadataSQLStageLegacyRootSQL, workspaceID, raw, target.updatedAt, workspace)
					} else {
						_, err = tx.ExecContext(ctx, metadataSQLStageLegacyRowSQL, workspace, field, target.key, target.order, raw)
					}
				} else {
					var closePages func()
					ctx, closePages, err = view.integrationDeliveryPageContext(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer closePages()
					var stage *metadataSQLWriteStage
					stage, err = beginMetadataSQLWriteStage(ctx, tx, target)
					if err == nil {
						defer stage.Close(context.WithoutCancel(ctx))
						var writer *metadataSQLStageWriter
						writer, err = stage.writer(ctx, "data")
						if err == nil {
							_, err = writer.Write(raw)
						}
						if err == nil {
							err = writer.flush(true)
						}
						if err == nil && target.workspaceState {
							err = stage.writeWorkspaceState(ctx)
						} else if err == nil {
							err = stage.writeMetadataRow(ctx)
						}
						if err == nil {
							err = stage.Close(ctx)
						}
					}
				}
				if err != nil && !errors.Is(err, ErrTransactionWriteBudget) {
					t.Fatal(err)
				}
				return tx.writtenBytes, errors.Is(err, ErrTransactionWriteBudget)
			}
			for _, delta := range []int{-1, 0, 1} {
				wantBill, wantRefused := write(false, bill+delta)
				gotBill, gotRefused := write(true, bill+delta)
				if gotBill != wantBill || gotRefused != wantRefused || gotRefused != (delta < 0) {
					t.Fatal("private SQL append changed literal transaction billing", field, delta, gotBill, wantBill, gotRefused, wantRefused)
				}
			}
		}
	})
}
