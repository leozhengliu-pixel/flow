package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// allocatedBy reports every byte f allocates, garbage included, with the
// encoder pools emptied first so no earlier buffer is reused.
func allocatedBy(f func()) uint64 {
	runtime.GC()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// A stored issue as large as creation accepts (a description that escapes to
// about 12.9 MB) is rewritten by a bounded writer within the default work
// budget, and the decode charge covers what the decode allocates.
func TestIssueRecordRewriteWorkCoversAllocation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := journalScaleDatabase(t)
	at := time.Date(2026, 11, 27, 1, 2, 3, 0, time.UTC)
	issue := domain.Issue{
		ID: idgen.New("escaped-rewrite-"), Identifier: "ESC-1", Number: 19100, Version: 1,
		Title: "Escaped description", Description: strings.Repeat("<>&", 700<<10),
		Team: f.data.Teams[0], State: domain.WorkflowState{ID: "state_backlog", Type: "backlog"},
		Creator: f.data.Viewer, CreatedAt: at, UpdatedAt: at,
	}
	stored, err := issueRecordDetail(issue)
	if err != nil {
		t.Fatal(err)
	}
	next := issue
	generated := at.Add(time.Minute)
	next.SuggestionsGeneratedAt, next.SuggestionsSource, next.Version = &generated, "heuristic", 2

	// Each operation allocates no more than its charge.
	decodeWork, err := issueRecordDecodeWork(t.Context(), stored)
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.Issue
	decodeAlloc := allocatedBy(func() {
		if err := json.Unmarshal(stored, &decoded); err != nil {
			t.Fatal(err)
		}
	})
	encodeWork, err := metadataRecordEncodingWork(t.Context(), reflect.ValueOf(issueRecordStored(next)))
	if err != nil {
		t.Fatal(err)
	}
	var encoded []byte
	encodeAlloc := allocatedBy(func() {
		if encoded, err = issueRecordDetail(next); err != nil {
			t.Fatal(err)
		}
	})
	// Both charges include allocations that become garbage during the call.
	t.Logf("stored=%d decode alloc=%d charge=%d; encode alloc=%d (garbage included) work=%d retained=%d", len(stored), decodeAlloc, decodeWork, encodeAlloc, encodeWork, cap(encoded))
	if decodeAlloc > decodeWork {
		t.Fatal("the decode charge is below the decode's allocation")
	}
	if encodeAlloc > uint64(encodeWork) {
		t.Fatal("the encode charge is below the encoding's allocation")
	}

	view := &SQLiteStore{db: f.db, dialect: f.dialect, metadataResidency: newMetadataResidency()}
	if _, err := reserveIssueRecordEncoding(t.Context(), view, next, stored); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("the complete comparison envelope fits the budget (%v): the case no longer covers the rewrite", err)
	}
	workspace := f.data.Workspace.URLKey
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f.importIssueRecordBatch(t.Context(), tx, workspace, []domain.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	ctx := withIssueRecordWriteAdmission(t.Context(), view)
	previous := map[string][]byte{issue.ID: stored}
	written, err := view.writeIssueRecordBatch(ctx, tx, workspace, []domain.Issue{issue}, f.data, previous)
	if err != nil || len(written) != 0 {
		t.Fatalf("unchanged rewrite: written=%v err=%v", written, err)
	}
	// A stored payload that differs only in its encoding decodes and
	// compares equal.
	unnormalized, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	if string(unnormalized) == string(stored) {
		t.Fatal("the unnormalized payload is the stored one")
	}
	written, err = view.writeIssueRecordBatch(ctx, tx, workspace, []domain.Issue{issue}, f.data, map[string][]byte{issue.ID: unnormalized})
	if err != nil || len(written) != 0 {
		t.Fatalf("re-encoded rewrite: written=%v err=%v", written, err)
	}
	written, err = view.writeIssueRecordBatch(ctx, tx, workspace, []domain.Issue{next}, f.data, previous)
	if err != nil || len(written) != 1 {
		t.Fatalf("suggestion rewrite: written=%v err=%v", written, err)
	}
	peak, limit := view.MetadataWorkPeakForTesting(), view.MetadataWorkLimitForTesting()
	t.Logf("rewrite work peak=%d limit=%d", peak, limit)
	if view.MetadataWorkUsedForTesting() != 0 || peak > limit {
		t.Fatalf("rewrite work leaked or exceeded the budget: used=%d peak=%d", view.MetadataWorkUsedForTesting(), peak)
	}
	var raw []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issue.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if want, _ := issueRecordDetail(next); string(raw) != string(want) {
		t.Fatal("the rewritten row is not the next issue")
	}
}
