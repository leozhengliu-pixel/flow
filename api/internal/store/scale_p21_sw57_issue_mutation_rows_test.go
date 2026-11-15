package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW57IssueMutationRows(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw57-rows-")
	at := time.Date(2026, 11, 14, 1, 2, 3, 400, time.FixedZone("stored", 5400))
	base := domain.Issue{Title: "Complete <title>\x00\u51c6", Description: "Original body", Version: 7,
		Team: f.data.Teams[0], State: domain.WorkflowState{ID: "stored-state", Name: "Stored state"}, Creator: f.data.Viewer,
		Labels:    []domain.IssueLabel{{ID: "stored-label", Name: "Full label", Description: "Full description"}},
		Reactions: map[string][]string{"like": {"actor", "actor"}}, SubscriberIDs: []string{"subscriber"},
		Relations:   []domain.IssueRelation{{ID: "relation", RelatedIssueID: "other", Type: "blocks"}},
		Attachments: []domain.Attachment{{ID: "attachment", Title: "Full attachment"}},
		CreatedAt:   at, UpdatedAt: at}
	ids, issues := []string{}, []domain.Issue{}
	for index := range 205 {
		issue := base
		issue.ID, issue.Identifier, issue.Number = fmt.Sprintf("%s%03d", prefix, index), fmt.Sprintf("ROW-%d", index), index+1
		ids, issues = append(ids, issue.ID), append(issues, issue)
	}
	if err := f.ImportIssues(t.Context(), workspace, issues); err != nil {
		t.Fatal(err)
	}
	foreign, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW57 foreign", idgen.New("sw57-foreign-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.DeleteWorkspace(context.Background(), foreign.Workspace.URLKey) })
	foreignIssue := base
	foreignIssue.ID, foreignIssue.Title = prefix+"foreign", "Foreign body"
	if err := f.ImportIssues(t.Context(), foreign.Workspace.URLKey, []domain.Issue{foreignIssue}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	begin := func(t *testing.T) *sqlTx {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		return tx
	}
	usable := func(t *testing.T, tx *sqlTx) {
		t.Helper()
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("loader closed or consumed the borrowed transaction", err)
		}
	}
	compare := func(t *testing.T, tx *sqlTx, selected []string, lock bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		want, wantErr := f.loadIssueRecordsByID(ctx, tx, workspace, selected, lock)
		read := view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		got, gotErr := read.load(ctx, selected, lock)
		if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() {
			t.Fatalf("legacy/bounded load error differs: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatal("decoded issue, physical map key, exact raw payload or collection order differs", len(want), len(got))
		}
		if gotErr == nil && len(got) > 0 && view.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("mutation preimages escaped without admission")
		}
		if gotErr != nil && got != nil {
			t.Fatal("failed load exposed a partial mutation map")
		}
		read.Close()
		read.Close()
		if len(got) != 0 || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("explicit close did not clear returned map and release work")
		}
		usable(t, tx)
	}
	huge := base
	huge.ID, huge.Description = "json-owned-large", strings.Repeat("Complete selected huge body. ", 1<<16)
	hugeRaw, err := json.Marshal(huge)
	if err != nil {
		t.Fatal(err)
	}
	utf8Raw := append([]byte(`{"id":"json-own","title":"`), byte(0xff))
	utf8Raw = append(utf8Raw, []byte(`"}`)...)
	for _, generation := range []string{"warm", "reloaded"} {
		t.Run(generation, func(t *testing.T) {
			if generation == "reloaded" {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			for _, test := range []struct {
				name string
				ids  []string
				raw  []byte
			}{
				{name: "nil-selection"},
				{name: "empty-selection", ids: []string{}},
				{name: "duplicate-empty-missing-foreign", ids: []string{ids[0], "", ids[0], prefix + "missing", foreignIssue.ID}},
				{name: "over200", ids: append(append([]string{}, ids...), "", ids[0], prefix+"missing", foreignIssue.ID)},
				{name: "physical-json-identity-normalization", ids: []string{ids[0], "json-own"}, raw: []byte(" \n" + `{"id":"first","ID":"json-own","version":0,"labels":null,"subscriberIds":null,"subIssueIds":null,"relations":null,"attachments":null,"reactions":null,"unknown":{"raw":"retained"}}` + "\n ")},
				{name: "selected-huge-complete-body", ids: []string{ids[0]}, raw: hugeRaw},
				{name: "json-null", ids: []string{ids[0]}, raw: []byte(" \nnull\t")},
				{name: "invalid-utf8", ids: []string{ids[0]}, raw: utf8Raw},
				{name: "empty-json-error", ids: []string{ids[0]}, raw: []byte{}},
				{name: "syntax-error", ids: []string{ids[0], ids[1]}, raw: []byte(`{"id":`)},
				{name: "typed-field-error", ids: []string{ids[0]}, raw: []byte(`{"id":"json-own","version":"bad"}`)},
				{name: "array-error", ids: []string{ids[0]}, raw: []byte(`[]`)},
				{name: "trailing-json-error", ids: []string{ids[0]}, raw: []byte(`{"id":"json-own"} false`)},
			} {
				for _, lock := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/lock=%v", test.name, lock), func(t *testing.T) {
						tx := begin(t)
						if test.raw != nil {
							if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET data=?,collection_order=-19 WHERE workspace_key=? AND id=?`, test.raw, workspace, ids[0]); err != nil {
								t.Fatal(err)
							}
						}
						compare(t, tx, test.ids, lock)
					})
				}
			}
		})
	}
	t.Run("sqlite-text-blob-nul-keys", func(t *testing.T) {
		if f.dialect != "sqlite" {
			t.Skip("SQLite storage-class and NUL identity fixture")
		}
		tx := begin(t)
		if _, err := tx.Tx.ExecContext(t.Context(), `UPDATE record_journal_context SET tx_id='' WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		for index, physical := range []any{"", prefix + "\x00text", []byte(prefix + "\x00blob"), prefix + "same", []byte(prefix + "same")} {
			raw := []byte(fmt.Sprintf(`{"id":"json-%d","title":"physical storage class"}`, index))
			if _, err := tx.Tx.ExecContext(t.Context(), `UPDATE issue_records SET id=?,data=?,collection_order=? WHERE workspace_key=? AND id=?`, physical, raw, -index, workspace, ids[index]); err != nil {
				t.Fatal(err)
			}
		}
		selected := []string{"", prefix + "\x00text", prefix + "\x00blob", prefix + "same", prefix + "same", ids[0]}
		for _, lock := range []bool{false, true} {
			compare(t, tx, selected, lock)
		}
	})
	t.Run("explicit-owner-lifetime", func(t *testing.T) {
		tx := begin(t)
		want, err := f.loadIssueRecordsByID(t.Context(), tx, workspace, ids[:1], false)
		if err != nil {
			t.Fatal(err)
		}
		read := view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		first, err := read.load(t.Context(), ids[:1], false)
		if err != nil {
			t.Fatal(err)
		}
		second, err := read.load(t.Context(), ids[:1], true)
		if err != nil || !reflect.DeepEqual(first, want) || !reflect.DeepEqual(second, want) {
			t.Fatal("repeated borrowed-tx loads changed the preimage", err)
		}
		alias := first
		changed := first[ids[0]]
		changed.raw[0] ^= 1
		changed.issue.Title = "Caller-owned edit"
		changed.issue.Labels[0].Name = "Caller-owned label"
		changed.issue.Reactions["like"][0] = "Caller-owned reaction"
		first[ids[0]] = changed
		if !reflect.DeepEqual(second, want) {
			t.Fatal("independent loads alias raw or decoded mutable state")
		}
		ctx, cancel := context.WithCancel(t.Context())
		held := view.MetadataWorkUsedForTesting()
		cancel()
		failed, err := read.load(ctx, ids[1:2], false)
		if !errors.Is(err, context.Canceled) || failed != nil || len(alias) != 1 || !bytes.Equal(second[ids[0]].raw, want[ids[0]].raw) || view.MetadataWorkUsedForTesting() < held {
			t.Fatal("cancellation discarded still-owned mutation preimages", err)
		}
		read.Close()
		if len(first) != 0 || len(alias) != 0 || len(second) != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("close did not clear every returned map alias")
		}
		if _, err := read.load(t.Context(), ids[:1], false); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("closed loader accepted a new load", err)
		}
		usable(t, tx)
	})
	t.Run("admission-error-cleanup", func(t *testing.T) {
		tx := begin(t)
		if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, hugeRaw, workspace, ids[0]); err != nil {
			t.Fatal(err)
		}
		budget := view.metadataResidency.work
		limit := budget.limit
		defer func() { budget.limit = limit }()
		for _, ceiling := range []int{1, 256 << 10} {
			budget.limit = ceiling
			read := view.newIssueMutationRows(tx, workspace)
			got, err := read.load(t.Context(), ids[:1], false)
			if !errors.Is(err, ErrMetadataWorkBudget) || got != nil {
				read.Close()
				t.Fatal("under-admitted selected body escaped", ceiling, err)
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("failed admission leaked after explicit close")
			}
			usable(t, tx)
		}
		budget.limit = limit
		read := view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		first, err := read.load(t.Context(), ids[1:2], false)
		if err != nil {
			t.Fatal(err)
		}
		held := view.MetadataWorkUsedForTesting()
		budget.limit = held + 1
		if got, err := read.load(t.Context(), ids[:1], false); !errors.Is(err, ErrMetadataWorkBudget) || got != nil || len(first) != 1 || view.MetadataWorkUsedForTesting() < held {
			t.Fatal("later admission failure discarded a successful map", err)
		}
		read.Close()
		if len(first) != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("later failure retained a map or work after close")
		}
		usable(t, tx)
	})
	for _, drift := range []string{"grow", "grow-empty", "delete", "cancel"} {
		t.Run("header-payload-"+drift, func(t *testing.T) {
			tx := begin(t)
			if drift == "grow-empty" {
				if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, []byte{}, workspace, ids[0]); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			changed := false
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if changed || !strings.Contains(query, " AS ordinal") {
					return
				}
				changed = true
				if drift == "cancel" {
					cancel()
					return
				}
				query, args := `DELETE FROM issue_records WHERE workspace_key=? AND id=?`, []any{workspace, ids[0]}
				if drift == "grow" || drift == "grow-empty" {
					query, args = `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, []any{hugeRaw, workspace, ids[0]}
				}
				if _, err := tx.Tx.ExecContext(t.Context(), query, args...); err != nil {
					t.Fatal(err)
				}
			})
			read := view.newIssueMutationRows(tx, workspace)
			defer read.Close()
			got, err := read.load(ctx, ids[:1], false)
			want := error(io.ErrUnexpectedEOF)
			if drift == "cancel" {
				want = context.Canceled
			}
			if !changed || !errors.Is(err, want) || got != nil || view.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("post-header row change escaped its admitted payload bound", drift, err)
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("post-header failure leaked after explicit close")
			}
			usable(t, tx)
		})
	}
	t.Run("invalid-input-and-sql-errors", func(t *testing.T) {
		tx := begin(t)
		read := view.newIssueMutationRows(tx, workspace)
		if got, err := read.load(t.Context(), make([]string, 1001), false); !errors.Is(err, ErrIssueQuery) || got != nil {
			read.Close()
			t.Fatal("selection bound ignored", err)
		}
		read.Close()
		for _, read := range []*issueMutationRows{view.newIssueMutationRows(nil, workspace), (*SQLiteStore)(nil).newIssueMutationRows(tx, workspace)} {
			got, err := read.load(t.Context(), ids[:1], false)
			read.Close()
			if !errors.Is(err, ErrBoundedMetadataCoverage) || got != nil {
				t.Fatal("invalid borrowed loader accepted input", err)
			}
		}
		usable(t, tx)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		read = view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		if got, err := read.load(t.Context(), ids[:1], false); !errors.Is(err, sql.ErrTxDone) || got != nil {
			t.Fatal("borrowed transaction SQL error changed", err)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("invalid input or SQL error leaked work")
		}
	})
}
