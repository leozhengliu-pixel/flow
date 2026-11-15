package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW70Fixture struct {
	scaleP09Fixture
	view *SQLiteStore
}

func scaleP21SW70Setup(t *testing.T) scaleP21SW70Fixture {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	f.db.SetMaxOpenConns(1)
	return scaleP21SW70Fixture{scaleP09Fixture: f, view: view}
}

func (f scaleP21SW70Fixture) begin(t *testing.T) *sqlTx {
	t.Helper()
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	if _, err := tx.ExecContext(t.Context(), `DELETE FROM issue_records WHERE workspace_key=?`, f.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	return tx
}

func (f scaleP21SW70Fixture) put(t *testing.T, tx *sqlTx, workspace string, physical any, order int64, raw []byte) {
	t.Helper()
	identity := idgen.New("sw70-index-")
	values, err := issueRecordValues(workspace, domain.Issue{ID: identity, Identifier: identity, Title: "SQL index title"})
	if err != nil {
		t.Fatal(err)
	}
	values[1], values[len(values)-1] = physical, raw
	if _, err := tx.ExecContext(t.Context(), issueRecordInsert+issueRecordValuesSQL, values...); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET collection_order=? WHERE workspace_key=? AND id=?`, order, workspace, physical); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW70Usable(t *testing.T, tx *sqlTx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatal("legacy issue scan consumed its borrowed pool-one transaction", err)
	}
}

func scaleP21SW70Cleared(t *testing.T, row *legacyIssueRow, raw []byte) {
	t.Helper()
	if row == nil {
		return
	}
	if row.PhysicalID != "" || !reflect.DeepEqual(row.Record, storedIssueRecord{}) {
		t.Fatal("callback-only issue row retained its physical key or record")
	}
	for _, value := range raw {
		if value != 0 {
			t.Fatal("closed issue row retained a raw payload backing byte")
		}
	}
}

func (f scaleP21SW70Fixture) compare(t *testing.T, tx *sqlTx, workspace string, count int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	literalCtx := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	want, wantErr := f.readIssueRecordsWithBase(literalCtx, workspace, newRecordBase())
	type physicalRow struct {
		id    string
		order int64
		raw   []byte
	}
	physical := []physicalRow{}
	rows, err := tx.QueryContext(ctx, `SELECT id,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var item physicalRow
		if err := rows.Scan(&item.id, &item.order, &item.raw); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		physical = append(physical, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	visited, writes := 0, 0
	var previous *legacyIssueRow
	var previousRaw []byte
	scanCtx := WithSQLStatementObserver(ctx, func(query string) {
		upper := strings.ToUpper(strings.TrimSpace(query))
		if strings.HasPrefix(upper, "INSERT ") || strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ") {
			writes++
		}
	})
	gotErr := f.view.scanLegacyIssueRows(scanCtx, tx, workspace, func(row *legacyIssueRow) error {
		scaleP21SW70Cleared(t, previous, previousRaw)
		if visited >= len(physical) {
			t.Fatal("scanner invented an extra issue row")
		}
		expected := physical[visited]
		if row == nil || row.PhysicalID != expected.id || row.Record.order != expected.order || !bytes.Equal(row.Record.raw, expected.raw) {
			t.Fatal("literal physical ID, collection order or exact raw bytes differ", visited)
		}
		if wantErr == nil && !reflect.DeepEqual(row.Record.issue, want[visited]) {
			t.Fatal("literal complete issue or normalization differs", visited, row.Record.issue.ID, want[visited].ID)
		}
		if f.view.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("live callback issue has no admission")
		}
		scaleP21SW70Usable(t, tx)
		previous, previousRaw = row, row.Record.raw
		visited++
		return nil
	})
	if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() {
		t.Fatalf("literal/scanner errors differ: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
	}
	if (wantErr != nil) != (count < 0) || wantErr == nil && (visited != count || visited != len(want)) {
		t.Fatal("fixture count or expected literal error differs", visited, count, len(want), wantErr)
	}
	scaleP21SW70Cleared(t, previous, previousRaw)
	if writes != 0 || f.view.MetadataWorkUsedForTesting() != 0 || f.view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("legacy issue scan wrote SQL, retained row work or decoded workspace metadata", writes)
	}
	scaleP21SW70Usable(t, tx)
}

func TestScaleP21SW70LegacyIssueScanLiteral(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 11, 14, 1, 2, 3, 400, time.FixedZone("stored", 5400))
	complete := domain.Issue{ID: "semantic-complete", Title: "Complete <title>\x00\u51c6", Description: "Complete body", Version: 0,
		Team: f.data.Teams[0], State: domain.WorkflowState{ID: "state", Name: "Complete state"}, Creator: f.data.Viewer,
		Labels:        []domain.IssueLabel{{ID: "label", Name: "Full label", Description: "Full description"}},
		SubscriberIDs: []string{"first", "first", "second"}, SubIssueIDs: []string{},
		Reactions: map[string][]string{"like": {"actor", "actor"}}, Relations: []domain.IssueRelation{{ID: "relation", RelatedIssueID: "other", Type: "blocks"}},
		Attachments: []domain.Attachment{{ID: "attachment", Title: "Full attachment"}}, CreatedAt: at, UpdatedAt: at}
	full, err := json.Marshal(complete)
	if err != nil {
		t.Fatal(err)
	}
	large := complete
	large.Description = strings.Repeat("Complete legal issue body. ", 1<<15)
	largeRaw, err := json.Marshal(large)
	if err != nil {
		t.Fatal(err)
	}
	invalidUTF8 := append([]byte(`{"id":"semantic-utf8","title":"`), byte(0xff))
	invalidUTF8 = append(invalidUTF8, []byte(`"}`)...)
	for _, test := range []struct {
		name  string
		raw   []byte
		count int
	}{
		{"complete-order-and-physical-identity", full, 3},
		{"normalized-nil-and-zero-version", []byte(" \n" + `{"id":"semantic-normalized","version":0,"labels":null,"subscriberIds":null,"subIssueIds":null,"relations":null,"attachments":null,"reactions":null,"unknown":{"preservedRaw":true}}` + "\t "), 3},
		{"case-insensitive-last-id", []byte(`{"id":"earlier","ID":"semantic-final","version":-2}`), 3},
		{"large-complete-selected-body", largeRaw, 3},
		{"null-record", []byte(" \nnull\t"), 3},
		{"invalid-utf8", invalidUTF8, 3},
		{"empty-json", []byte{}, -1},
		{"malformed-json", []byte(`{"id":`), -1},
		{"typed-field-error", []byte(`{"id":"semantic-error","version":"bad"}`), -1},
		{"array-error", []byte(`[]`), -1},
		{"trailing-json", []byte(`{"id":"semantic-error"} false`), -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := f.begin(t)
			f.put(t, tx, workspace, "physical-z", 2, test.raw)
			f.put(t, tx, workspace, "physical-m", 1, []byte(`{"id":"same-semantic","title":"second tied physical key"}`))
			f.put(t, tx, workspace, "physical-a", 1, []byte(`{"id":"same-semantic","title":"first tied physical key"}`))
			f.put(t, tx, workspace+"-foreign", "physical-foreign", -99, []byte(`{"id":`))
			f.compare(t, tx, workspace, test.count)
		})
	}
	t.Run("empty-and-missing-workspace", func(t *testing.T) {
		tx := f.begin(t)
		f.compare(t, tx, workspace, 0)
		f.compare(t, tx, workspace+"-missing", 0)
		f.compare(t, tx, "", 0)
	})
	t.Run("sqlite-native-physical-storage-classes", func(t *testing.T) {
		if f.dialect != "sqlite" {
			t.Skip("SQLite TEXT/BLOB and NUL physical identities")
		}
		tx := f.begin(t)
		if _, err := tx.Tx.ExecContext(t.Context(), `UPDATE record_journal_context SET tx_id='' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		for index, physical := range []any{"", "nul\x00text", []byte("nul\x00blob"), "same", []byte("same")} {
			f.put(t, tx, workspace, physical, 0, []byte(fmt.Sprintf(`{"id":"semantic-%d","title":"native physical identity"}`, index)))
		}
		f.compare(t, tx, workspace, 5)
	})
}

func TestScaleP21SW70LegacyIssueScanConstantWork(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	raw := []byte(`{"id":"semantic-constant","title":"Same width complete record","description":"Full description","reactions":{"like":["one","one"]}}`)
	peaks := []int{}
	for _, count := range []int{101, 1010} {
		t.Run(fmt.Sprintf("rows-%d", count), func(t *testing.T) {
			tx := f.begin(t)
			for index := range count {
				f.put(t, tx, workspace, fmt.Sprintf("physical-%04d", index), int64(index%7), raw)
			}
			ctx, release, err := f.view.admitMetadataWork(t.Context(), 64<<10)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			baseline, peak, visited, queries := f.view.MetadataWorkUsedForTesting(), 0, 0, 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if strings.Contains(strings.ToLower(query), "from issue_records") {
					queries++
				}
			})
			var previous *legacyIssueRow
			var previousRaw []byte
			err = f.view.scanLegacyIssueRows(ctx, tx, workspace, func(row *legacyIssueRow) error {
				scaleP21SW70Cleared(t, previous, previousRaw)
				used := f.view.MetadataWorkUsedForTesting()
				if used <= baseline || row.Record.issue.ID != "semantic-constant" || !bytes.Equal(row.Record.raw, raw) {
					t.Fatal("row borrowed caller allowance or changed complete payload", used, baseline)
				}
				peak = max(peak, used-baseline)
				previous, previousRaw = row, row.Record.raw
				visited++
				return nil
			})
			if err != nil || visited != count || f.view.MetadataWorkUsedForTesting() != baseline {
				t.Fatal("unlimited scan truncated rows or retained callback work", visited, count, err)
			}
			if queries == 0 || queries >= count/4 {
				t.Fatal("small-row scan regressed to individual row queries", queries, count)
			}
			scaleP21SW70Cleared(t, previous, previousRaw)
			peaks = append(peaks, peak)
			release()
			if f.view.MetadataWorkUsedForTesting() != 0 || f.view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("row-count growth leaked work or hydrated metadata")
			}
			scaleP21SW70Usable(t, tx)
			t.Logf("rows=%d peak_row_work=%d issue_queries=%d", count, peak, queries)
		})
	}
	if len(peaks) != 2 || peaks[0] == 0 || peaks[0] != peaks[1] {
		t.Fatal("10x rows increased scanner peak admission", peaks)
	}
}

func TestScaleP21SW70LegacyIssueScanOutputLifetime(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	raw := []byte(`{"id":"semantic-owned","title":"Owned complete output","reactions":{"like":["actor"]}}`)
	for _, callbackFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("callback-error-%t", callbackFailure), func(t *testing.T) {
			tx := f.begin(t)
			for index := range 2 {
				f.put(t, tx, workspace, fmt.Sprintf("physical-%d", index), int64(index), raw)
			}
			var releases []func()
			ctx, cancel := context.WithCancel(WithMetadataOutputOwner(t.Context(), func(close func()) bool {
				releases = append(releases, close)
				t.Cleanup(close)
				return true
			}))
			defer cancel()
			var owned []*legacyIssueRow
			var rawAliases [][]byte
			stop := errors.New("stop after retained row")
			err := f.view.scanLegacyIssueRows(ctx, tx, workspace, func(row *legacyIssueRow) error {
				if len(owned) > 0 && (owned[0].Record.issue.ID != "semantic-owned" || !bytes.Equal(owned[0].Record.raw, raw)) {
					t.Fatal("next callback invalidated transferred output")
				}
				if err := row.TransferOutput(ctx); err != nil {
					return err
				}
				owned, rawAliases = append(owned, row), append(rawAliases, row.Record.raw)
				if callbackFailure {
					return stop
				}
				return nil
			})
			expected := 2
			if callbackFailure {
				expected = 1
				if !errors.Is(err, stop) {
					t.Fatal("callback error lost after output transfer", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if len(owned) != expected || len(releases) != expected {
				t.Fatal("output registration count differs", len(owned), len(releases), expected)
			}
			held := f.view.MetadataWorkUsedForTesting()
			cancel()
			if held == 0 || f.view.MetadataWorkUsedForTesting() != held {
				t.Fatal("cancellation freed still-encoding issue output")
			}
			for index, row := range owned {
				if row.Record.issue.ID != "semantic-owned" || !bytes.Equal(row.Record.raw, raw) {
					t.Fatal("callback completion discarded transferred issue output", index)
				}
				if _, err := json.Marshal(row.Record.issue); err != nil {
					t.Fatal("retained issue output cannot encode", err)
				}
			}
			for index, release := range releases {
				release()
				release()
				scaleP21SW70Cleared(t, owned[index], rawAliases[index])
				if index+1 < len(owned) && (owned[index+1].Record.issue.ID != "semantic-owned" || f.view.MetadataWorkUsedForTesting() == 0) {
					t.Fatal("closing one transferred row freed another")
				}
			}
			if f.view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("output completion retained row work")
			}
			scaleP21SW70Usable(t, tx)
		})
	}
	for _, registration := range []string{"absent", "nil", "rejected"} {
		t.Run("output-owner-"+registration, func(t *testing.T) {
			tx := f.begin(t)
			f.put(t, tx, workspace, "physical", 0, raw)
			ctx := t.Context()
			if registration == "nil" {
				ctx = WithMetadataOutputOwner(ctx, nil)
			} else if registration == "rejected" {
				ctx = WithMetadataOutputOwner(ctx, func(func()) bool { return false })
			}
			var captured *legacyIssueRow
			var rawAlias []byte
			err := f.view.scanLegacyIssueRows(ctx, tx, workspace, func(row *legacyIssueRow) error {
				captured, rawAlias = row, row.Record.raw
				return row.TransferOutput(ctx)
			})
			if !errors.Is(err, ErrBoundedMetadataCoverage) || captured == nil || f.view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("missing/rejected output ownership escaped or leaked", registration, err)
			}
			scaleP21SW70Cleared(t, captured, rawAlias)
			scaleP21SW70Usable(t, tx)
		})
	}
	heldOutputs := []int{}
	for _, size := range []int{2000, 20000} {
		t.Run(fmt.Sprintf("small-output-unmatched-body-%d", size), func(t *testing.T) {
			tx := f.begin(t)
			f.put(t, tx, workspace, "physical-first", 0, raw)
			unmatched, err := json.Marshal(domain.Issue{ID: "unmatched", Description: strings.Repeat("Unmatched complete issue body. ", size)})
			if err != nil {
				t.Fatal(err)
			}
			f.put(t, tx, workspace, "physical-other", 1, unmatched)
			var release func()
			ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
				if release != nil {
					t.Fatal("unmatched row registered an output lifetime")
				}
				release = close
				t.Cleanup(close)
				return true
			})
			var output, discarded *legacyIssueRow
			var outputRaw, discardedRaw []byte
			err = f.view.scanLegacyIssueRows(ctx, tx, workspace, func(row *legacyIssueRow) error {
				if row.PhysicalID == "physical-first" {
					output, outputRaw = row, row.Record.raw
					return row.TransferOutput(ctx)
				}
				discarded, discardedRaw = row, row.Record.raw
				return nil
			})
			if err != nil || release == nil || output == nil || discarded == nil || output.Record.issue.ID != "semantic-owned" || !bytes.Equal(output.Record.raw, raw) {
				t.Fatal("small retained row changed during unmatched large-body scan", err)
			}
			defer release()
			scaleP21SW70Cleared(t, discarded, discardedRaw)
			heldOutputs = append(heldOutputs, f.view.MetadataWorkUsedForTesting())
			release()
			scaleP21SW70Cleared(t, output, outputRaw)
			if f.view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("retained small output leaked its allowance")
			}
			scaleP21SW70Usable(t, tx)
		})
	}
	if len(heldOutputs) != 2 || heldOutputs[0] == 0 || heldOutputs[0] != heldOutputs[1] {
		t.Fatal("10x unmatched body remained charged to a transferred small row", heldOutputs)
	}
}

func TestScaleP21SW70LegacyIssueScanFailureRelease(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	raw := []byte(`{"id":"semantic-failure","description":"Complete selected body"}`)
	for _, failure := range []string{"cancel-before", "cancel-in-callback", "callback-error", "quota"} {
		t.Run(failure, func(t *testing.T) {
			tx := f.begin(t)
			for index := range 2 {
				f.put(t, tx, workspace, fmt.Sprintf("physical-%d", index), int64(index), raw)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			budget, limit := f.view.metadataResidency.work, f.view.metadataResidency.work.limit
			defer func() { budget.limit = limit }()
			stop := errors.New("callback stopped scan")
			var want error = stop
			if failure == "cancel-before" || failure == "cancel-in-callback" {
				want = context.Canceled
			}
			if failure == "cancel-before" {
				cancel()
			} else if failure == "quota" {
				budget.limit, want = 1, ErrMetadataWorkBudget
			}
			visited := 0
			var previous *legacyIssueRow
			var previousRaw []byte
			err := f.view.scanLegacyIssueRows(ctx, tx, workspace, func(row *legacyIssueRow) error {
				visited++
				previous, previousRaw = row, row.Record.raw
				if failure == "cancel-in-callback" {
					cancel()
					return nil
				}
				return stop
			})
			budget.limit = limit
			expected := 1
			if failure == "cancel-before" || failure == "quota" {
				expected = 0
			}
			if !errors.Is(err, want) || visited != expected || f.view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("failed scan changed its error, continued callbacks or retained work", failure, visited, err)
			}
			scaleP21SW70Cleared(t, previous, previousRaw)
			scaleP21SW70Usable(t, tx)
		})
	}
	t.Run("closed-borrowed-transaction", func(t *testing.T) {
		tx := f.begin(t)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
		_, wantErr := f.readIssueRecordsWithBase(ctx, workspace, newRecordBase())
		visited := 0
		gotErr := f.view.scanLegacyIssueRows(t.Context(), tx, workspace, func(*legacyIssueRow) error {
			visited++
			return nil
		})
		if wantErr == nil || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) || gotErr.Error() != wantErr.Error() || visited != 0 || f.view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("closed borrowed transaction error or cleanup differs", wantErr, gotErr, visited)
		}
	})
}
