package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW254SizeScript struct {
	size       int64
	payloads   int
	maximums   int
	payloadErr error
	onSize     func()
	onPayload  func()
}

type scaleP21SW254Connector struct{ script *scaleP21SW254SizeScript }

func (c scaleP21SW254Connector) Driver() driver.Driver { return c }
func (c scaleP21SW254Connector) Open(string) (driver.Conn, error) {
	return c.Connect(context.Background())
}
func (c scaleP21SW254Connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &scaleP21SW254Conn{script: c.script}, nil
}

type scaleP21SW254Conn struct{ script *scaleP21SW254SizeScript }

func (*scaleP21SW254Conn) Close() error { return nil }
func (*scaleP21SW254Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected SW254 prepared statement")
}
func (*scaleP21SW254Conn) Begin() (driver.Tx, error) { return scaleP21SW254Tx{}, nil }
func (c *scaleP21SW254Conn) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.HasPrefix(query, "SELECT COALESCE(MAX(") || strings.HasPrefix(query, "SELECT LENGTH(") ||
		strings.HasPrefix(query, "SELECT OCTET_LENGTH(") {
		c.script.maximums++
		if c.script.onSize != nil {
			c.script.onSize()
		}
		return &scaleP21SW254Rows{columns: []string{"size"}, values: [][]driver.Value{{c.script.size}}}, nil
	}
	if strings.HasPrefix(query, "SELECT id,CASE WHEN ") {
		c.script.payloads++
		if c.script.onPayload != nil {
			c.script.onPayload()
		}
		if c.script.payloadErr != nil {
			return nil, c.script.payloadErr
		}
		// No real row is allocated in a virtual size-boundary test. A
		// separate database-backed root proves complete payload capture.
		return &scaleP21SW254Rows{columns: []string{"id", "data", "size"}}, nil
	}
	return nil, fmt.Errorf("unexpected SW254 query: %s", query)
}

type scaleP21SW254Tx struct{}

func (scaleP21SW254Tx) Commit() error   { return nil }
func (scaleP21SW254Tx) Rollback() error { return nil }

type scaleP21SW254Rows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *scaleP21SW254Rows) Columns() []string { return r.columns }
func (*scaleP21SW254Rows) Close() error        { return nil }
func (r *scaleP21SW254Rows) Next(values []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(values, r.values[r.index])
	r.index++
	return nil
}

func scaleP21SW254ScriptTx(t *testing.T, script *scaleP21SW254SizeScript) *sqlTx {
	t.Helper()
	db := sql.OpenDB(scaleP21SW254Connector{script})
	t.Cleanup(func() { _ = db.Close() })
	wrapped := &sqlDatabase{DB: db, dialect: "sqlite"}
	tx, err := wrapped.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestScaleP21SW254IssuePayloadTransferEnvelope(t *testing.T) {
	bound := int64((metadataWorkBytes - (64 << 10)) / 3)
	for _, test := range []struct {
		name string
		size int64
		want bool
	}{
		{"negative", -1, false}, {"minimum-signed", math.MinInt64, false},
		{"zero", 0, true}, {"below", bound - 1, true}, {"at", bound, true},
		{"above", bound + 1, false}, {"maximum-signed", math.MaxInt64, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			script := &scaleP21SW254SizeScript{size: test.size}
			script.onPayload = func() {
				want := int(test.size*3 + 64<<10)
				if repo.MetadataWorkUsedForTesting() != want {
					t.Error("payload SELECT lost its exact three-copy transfer envelope", repo.MetadataWorkUsedForTesting(), want)
				}
			}
			tx := scaleP21SW254ScriptTx(t, script)
			stage, err := sw26NewMigrationStage(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if _, err := stage.exec(t.Context(), 4096, `CREATE TABLE legacy_issue_payload(id BLOB PRIMARY KEY,data BLOB NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			base := &legacyRecordBase{store: repo, stage: stage}
			err = base.issuePayloadBatch(t.Context(), tx, "owned", []legacyRecordIssueHeader{{id: "target"}})
			if test.want && err != nil || !test.want && !errors.Is(err, ErrMetadataWorkBudget) ||
				script.maximums != 1 || script.payloads != map[bool]int{true: 1, false: 0}[test.want] ||
				repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("signed size gate changed transfer admission or leaked work", test.size, err, script.maximums, script.payloads)
			}
		})
	}
}

func TestScaleP21SW254IssueCurrentBodyEnvelope(t *testing.T) {
	bound := int64((metadataWorkBytes - (64 << 10)) / 4)
	reached := errors.New("SW254 admitted payload read")
	for _, test := range []struct {
		name, kind string
		size       int64
		want       bool
	}{
		{"issue-below", "issue", bound - 1, true}, {"issue-at", "issue", bound, true},
		{"issue-above", "issue", bound + 1, false}, {"issue-negative", "issue", -1, false},
		{"issue-minimum-signed", "issue", math.MinInt64, false}, {"issue-maximum-signed", "issue", math.MaxInt64, false},
		{"other-at-old-bound", "comment", metadataWorkBytes / 16, true},
		{"other-above-old-bound", "comment", metadataWorkBytes/16 + 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			script := &scaleP21SW254SizeScript{size: test.size, payloadErr: reached}
			script.onPayload = func() {
				want := int(test.size*4 + 64<<10)
				if repo.MetadataWorkUsedForTesting() != want {
					t.Error("current body read lost its exact four-copy envelope", repo.MetadataWorkUsedForTesting(), want)
				}
			}
			tx := scaleP21SW254ScriptTx(t, script)
			work := &ResourceRead{store: repo}
			_, found, err := (&legacyRecordBase{store: repo}).currentBody(t.Context(), tx, "owned", test.kind, "", "target", work)
			work.Close()
			if found || test.want && !errors.Is(err, reached) || !test.want && !errors.Is(err, ErrMetadataWorkBudget) ||
				script.maximums != 1 || script.payloads != map[bool]int{true: 1, false: 0}[test.want] ||
				repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("current body gate changed signed boundary or non-issue allowance", test.kind, test.size, err)
			}
		})
	}
}

func TestScaleP21SW254IssuePayloadCancellationAndRemainingQuota(t *testing.T) {
	for _, phase := range []string{"before", "after-size", "remaining-quota"} {
		t.Run(phase, func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			_, releaseParent, err := repo.tryAdmitAdditionalMetadataWork(t.Context(), 4096)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseParent()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			script := &scaleP21SW254SizeScript{size: 1 << 20}
			if phase == "before" {
				cancel()
			} else if phase == "after-size" {
				script.onSize = cancel
			}
			tx := scaleP21SW254ScriptTx(t, script)
			stage, err := sw26NewMigrationStage(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if _, err := stage.exec(t.Context(), 4096, `CREATE TABLE legacy_issue_payload(id BLOB PRIMARY KEY,data BLOB NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			parent := repo.MetadataWorkUsedForTesting()
			if phase == "remaining-quota" {
				repo.metadataResidency.work.limit = parent + int(script.size*3+64<<10) - 1
			}
			err = (&legacyRecordBase{store: repo, stage: stage}).issuePayloadBatch(ctx, tx, "owned", []legacyRecordIssueHeader{{id: "target"}})
			want := error(context.Canceled)
			if phase == "remaining-quota" {
				want = ErrMetadataWorkBudget
			}
			if !errors.Is(err, want) || script.payloads != 0 || repo.MetadataWorkUsedForTesting() != parent {
				t.Fatal("pre-transfer cancellation/denial changed parent ownership", err, script.payloads)
			}
			if phase == "remaining-quota" {
				repo.metadataResidency.work.limit++
				err = (&legacyRecordBase{store: repo, stage: stage}).issuePayloadBatch(t.Context(), tx, "owned", []legacyRecordIssueHeader{{id: "target"}})
				if err != nil || script.payloads != 1 || repo.MetadataWorkUsedForTesting() != parent {
					t.Fatal("same-store exact-envelope recovery failed", err, script.payloads)
				}
			}
			releaseParent()
			releaseParent()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("parent release leaked")
			}
		})
	}
}

func TestScaleP21SW254IssuePayloadOneSnapshotPer64Targets(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	for _, pressure := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "admission-flush"}[pressure], func(t *testing.T) {
			prefix := idgen.New("sw254-")
			data := scaleP21SW93Data(f, prefix, 0)
			seed := data.Issues[0]
			data.Issues = make([]domain.Issue, 65)
			for index := range data.Issues {
				item := seed
				item.ID, item.Identifier, item.Number, item.Version = fmt.Sprintf("%sissue-%02d", prefix, index), fmt.Sprintf("SW254-%02d", index), index+1, 10
				data.Issues[index] = item
			}
			// Target occurrences include the same ID within and across the
			// 64-target capture boundary; physical SQL remains deduplicated.
			data.Issues[31], data.Issues[64] = data.Issues[0], data.Issues[0]
			if f.dialect == "postgres" {
				// PostgreSQL rejects a key repeated in one UPSERT statement, so the
				// literal writer refuses a duplicate within one 64-issue batch
				// (SW93 duplicate-issues-one-batch proves the bounded writer
				// refuses it the same way). Keep the duplicate across the boundary.
				data.Issues[31] = seed
				data.Issues[31].ID, data.Issues[31].Identifier, data.Issues[31].Number, data.Issues[31].Version = fmt.Sprintf("%sissue-%02d", prefix, 31), "SW254-31", 32, 10
			}
			var expectedRows map[string][][]any
			var expectedHistory map[string]issueFieldHistory
			var expectedCounts map[string]map[issueStatsKey]int64
			for _, bounded := range []bool{false, true} {
				tx, ctx := scaleP21SW93Begin(t, f, data)
				workspace := data.Workspace.URLKey
				stamp := copyStamp{workspaceID: data.Workspace.ID, revision: -1}
				source, err := captureMetadataLogicalSource(ctx, data, stamp)
				if err != nil {
					t.Fatal(err)
				}
				base, err := view.newLegacyRecordBase(ctx, source)
				source.Release()
				if err != nil {
					t.Fatal(err)
				}
				defer base.Close()
				// The installed issue collection is explicitly nonnil: the
				// original writer does not acquire loaded-family row locks.
				if base.loaded["issue"] {
					t.Fatal("fixture accidentally selected loaded-family locking")
				}
				target := cloneBootstrap(data)
				target.Issues = make([]domain.Issue, 65)
				for index := range target.Issues {
					item := data.Issues[index]
					item.Title, item.Description, item.Version = fmt.Sprintf("Changed occurrence %02d", index), strings.Repeat("x", 64<<10), 11
					if index == 31 || index == 64 {
						item.Version = 12
					}
					target.Issues[index] = item
				}
				candidate, err := captureMetadataLogicalSource(ctx, target, stamp)
				if err != nil {
					t.Fatal(err)
				}
				payloads, flushes, lockedIssues := 0, 0, 0
				observed := WithSQLStatementObserver(ctx, func(query string) {
					if strings.HasPrefix(query, "SELECT id,CASE WHEN ") && strings.Contains(query, " FROM issue_records WHERE workspace_key=? AND id IN ") {
						payloads++
					}
					if strings.HasPrefix(query, contentRecordUpsertPrefix) {
						flushes++
					}
					if strings.Contains(strings.ToUpper(query), "FOR UPDATE") && strings.Contains(query, "issue_records") {
						lockedIssues++
					}
				})
				limit := view.metadataResidency.work.limit
				if bounded {
					if pressure {
						view.metadataResidency.work.limit = 8 << 20
					}
					err = base.Persist(observed, tx, workspace, candidate)
					view.metadataResidency.work.limit = limit
				} else {
					// A nonnil installed Issues collection uses the literal
					// writer without loaded-family fingerprints or row locks.
					err = f.replaceIssueRecords(observed, tx, workspace, target.Issues, target, nil)
				}
				candidate.Release()
				if err != nil {
					t.Fatal("legal 65-target writer must succeed", bounded, pressure, err)
				}
				rows, history, counts := scaleP21SW93Rows(t, tx, workspace), scaleP21SW218History(t, tx, workspace, ""), scaleP21SW93Counts(tx)
				if bounded {
					if payloads != 2 || lockedIssues != 0 || pressure && flushes <= 2 {
						t.Fatal("preimage SELECT recaptured after flush or target boundary changed", payloads, lockedIssues, flushes)
					}
					if !reflect.DeepEqual(rows, expectedRows) || !reflect.DeepEqual(history, expectedHistory) || !reflect.DeepEqual(counts, expectedCounts) {
						t.Fatal("65-target duplicate/flush result differs from literal rows/history/counts")
					}
					t.Logf("pressure=%t target_occurrences=65 payload_SELECTs=%d history_flushes=%d", pressure, payloads, flushes)
				} else {
					expectedRows, expectedHistory, expectedCounts = rows, history, counts
				}
				base.Close()
				base.Close()
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("target capture/flush retained work after rollback")
				}
			}
		})
	}
}
