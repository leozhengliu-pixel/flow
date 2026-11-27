package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW185CandidateSpoolOwnership(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	read, err := s.BeginIssueSuggestionCandidates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	path := read.file.Name()
	issues := []domain.Issue{
		{ID: "duplicate", Title: "First snapshot", Number: 7, Team: domain.Team{ID: "team"}},
		{ID: "duplicate", Title: "Later snapshot", Number: 8, Team: domain.Team{ID: "team"}},
	}
	if err := read.Start(t.Context(), "requested"); err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if err := read.Append(t.Context(), issue); err != nil {
			t.Fatal(err)
		}
	}
	if err := read.Finish(); err != nil {
		t.Fatal(err)
	}
	scores, err := read.BeginScores(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var got []domain.Issue
	if err := read.Walk(t.Context(), "requested", func(issue domain.Issue) (bool, error) {
		if err := scores.Keep(t.Context(), issue); err != nil {
			return false, err
		}
		got = append(got, issue)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(issues)
	after, _ := json.Marshal(got)
	if string(before) != string(after) || read.Number("requested") != 8 || !reflect.DeepEqual(read.Teams([]string{"requested"}), []string{"team"}) {
		t.Fatal("spooling changed duplicate encounter order, numbers, or teams")
	}
	projectInput := domain.Issue{
		ID: "projected", Identifier: "PROJ-9", Title: "Unused display fallback", Description: strings.Repeat("Unused after numeric scoring.", 10000),
		Team: domain.Team{ID: "team", Name: "Unused name"}, Project: &domain.ProjectSummary{ID: "project", Name: "Unused name"},
		Assignee: &domain.User{ID: "user", Name: "Unused name"}, Labels: []domain.IssueLabel{{ID: "label", Description: "Unused body"}},
	}
	used := s.MetadataWorkUsedForTesting()
	projected, err := scores.Project(t.Context(), projectInput)
	if err != nil || projected.ID != projectInput.ID || projected.Identifier != projectInput.Identifier ||
		projected.Description != "" || projected.Title != "" || projected.Team.ID != "team" ||
		projected.Project.ID != "project" || projected.Assignee.ID != "user" || projected.Labels[0].ID != "label" {
		t.Fatal("score projection lost a downstream input or retained unused bodies", err)
	}
	if delta := s.MetadataWorkUsedForTesting() - used; delta <= 0 || delta > 64<<10 {
		t.Fatalf("scoring projection retained unrelated body decoding: %d", delta)
	}
	scores.Close()
	if err := scores.Keep(t.Context(), issues[0]); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed scoring owner accepted another value", err)
	}
	mutation := issues[0]
	mutation.Relations = []domain.IssueRelation{{ID: "relation", RelatedIssueID: "scoped"}}
	var held int
	for index := 0; index < 20; index++ {
		if err := read.SetMutation(t.Context(), mutation); err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			held = s.MetadataWorkUsedForTesting()
		} else if s.MetadataWorkUsedForTesting() != held {
			t.Fatal("replaced candidate mutation accumulated admission")
		}
	}
	if value, found := read.Mutation("duplicate"); !found || !reflect.DeepEqual(value, mutation) {
		t.Fatal("candidate first-occurrence mutation was lost")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := read.Walk(cancelled, "requested", func(domain.Issue) (bool, error) {
		t.Fatal("cancelled candidate walk reached visitor")
		return false, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled walk", err)
	}
	read.Close()
	read.Close()
	if err := read.Walk(t.Context(), "requested", func(domain.Issue) (bool, error) { return true, nil }); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closed candidate spool accepted another walk", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) || s.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("candidate spool or admission survived Close: stat=%v held=%d", err, s.MetadataWorkUsedForTesting())
	}
}

func TestIssueSuggestionTransitionBatchesRowsAndJournal(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	f.db.boundedCatalog.Store(true)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	const field = "issueSuggestions"
	count := MetadataRecordBatchMaxKeys*2 + 1
	data := domain.Bootstrap{}
	for i := range count {
		data.IssueSuggestions = append(data.IssueSuggestions, domain.IssueSuggestion{
			ID: fmt.Sprintf("suggestion-%03d", i), IssueID: "issue", Type: "label",
		})
	}
	// The catalog still streams a canonical body above the batch read cap.
	data.IssueSuggestions[count-1].Metadata = map[string]any{"reasons": strings.Repeat("body", metadataRecordChunkBytes)}
	target, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Release()
	spec, _ := metadataFieldByName(field)
	change := legacyMetadataWriteField{field: spec, next: target.fields[spec.index]}
	var upserts, moves, deletes, catalogWrites int
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		switch {
		case strings.HasPrefix(query, "INSERT INTO workspace_metadata_records("):
			upserts++
		case strings.HasPrefix(query, "UPDATE workspace_metadata_records SET collection_order="):
			moves++
		case strings.HasPrefix(query, "DELETE FROM workspace_metadata_records "):
			deletes++
		case strings.HasPrefix(query, "INSERT INTO metadata_catalog("):
			catalogWrites++
		}
	})
	before := scaleP09Head(t, f)
	write := func(run func(*legacyMetadataWritePlan) error) {
		t.Helper()
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		plan := &legacyMetadataWritePlan{store: view, tx: tx, workspace: workspace, workspaceID: workspaceID}
		if err := run(plan); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	write(func(plan *legacyMetadataWritePlan) error {
		return plan.writeArrayTransition(ctx, &change)
	})
	if upserts != 3 || catalogWrites > 4 {
		t.Fatalf("%d suggestions used %d canonical and %d catalog upserts", count, upserts, catalogWrites)
	}
	page, err := f.ReadCommittedChanges(ctx, workspaceID, before, 10, 256<<10)
	if err != nil || page.ResyncRequired || len(page.Transactions) != 1 || len(page.Transactions[0].Records) < count {
		t.Fatalf("suggestion journal page lost its deltas: %+v, %v", page, err)
	}
	for i, suggestion := range data.IssueSuggestions {
		version, deleted, err := f.RecordVersion(ctx, workspace, RecordKey{field, suggestion.ID})
		if err != nil || version != "1" || deleted {
			t.Fatalf("insert %s version = %q, deleted = %t, err = %v", suggestion.ID, version, deleted, err)
		}
		var order, size int
		var digest string
		var raw, summary []byte
		if err := f.db.QueryRowContext(ctx, `SELECT m.collection_order,c.body_bytes,c.version,m.data,c.summary
			FROM workspace_metadata_records m JOIN metadata_catalog c ON c.workspace_key=m.workspace_key AND c.field=m.field AND c.record_key=m.record_key
			WHERE m.workspace_key=? AND m.field=? AND m.record_key=?`, workspace, field, suggestion.ID).Scan(&order, &size, &digest, &raw, &summary); err != nil {
			t.Fatal(err)
		}
		var projected struct{ ID, IssueID string }
		if err := json.Unmarshal(summary, &projected); err != nil || projected.ID != suggestion.ID || projected.IssueID != suggestion.IssueID ||
			order != i || size != len(raw) || digest != metadataFingerprint(raw) {
			t.Fatalf("canonical or catalog row %d changed: order=%d size=%d summary=%s err=%v", i, order, size, summary, err)
		}
		change.oldOrder = append(change.oldOrder, i)
		change.order = append(change.order, count-1-i)
		change.rows = append(change.rows, legacyMetadataWriteRow{
			id: suggestion.ID, before: metadataLogicalNode{typ: reflect.TypeFor[domain.IssueSuggestion]()},
		})
	}
	retain := data.IssueSuggestions[count-1].ID
	write(func(plan *legacyMetadataWritePlan) error {
		return plan.writeArrayReorder(ctx, &change)
	})
	if moves != 2 || upserts != 3 {
		t.Fatalf("reordering %d suggestions used %d order updates and %d extra upserts", count, moves, upserts-3)
	}
	var mismatches int
	if err := f.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records m
		JOIN metadata_catalog c ON c.workspace_key=m.workspace_key AND c.field=m.field AND c.record_key=m.record_key
		WHERE m.workspace_key=? AND m.field=? AND m.collection_order<>c.collection_order`, workspace, field).Scan(&mismatches); err != nil || mismatches != 0 {
		t.Fatalf("%d catalog ordinals differ from the canonical order: %v", mismatches, err)
	}
	var firstID string
	if err := f.db.QueryRowContext(ctx, `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order LIMIT 1`,
		workspace, field).Scan(&firstID); err != nil || firstID != retain {
		t.Fatalf("first suggestion after reorder = %q, want %q: %v", firstID, retain, err)
	}
	page, err = f.ReadCommittedChanges(ctx, workspaceID, page.Next, 10, 256<<10)
	if err != nil || page.ResyncRequired || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != count-1 {
		t.Fatalf("suggestion journal lost order updates: %+v, %v", page, err)
	}
	write(func(plan *legacyMetadataWritePlan) error {
		return plan.deleteArrayDropped(ctx, &change, map[string]bool{retain: true})
	})
	for table, want := range map[string]int{"workspace_metadata_records": 1, "metadata_catalog": 1, "metadata_catalog_relations": 2} {
		var rows int
		if err := f.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE workspace_key=? AND field=?", workspace, field).Scan(&rows); err != nil || rows != want {
			t.Fatalf("%s retained %d rows, want %d: %v", table, rows, want, err)
		}
	}
	if deletes != 2 {
		t.Fatalf("removed %d suggestions in %d statements", count-1, deletes)
	}
	page, err = f.ReadCommittedChanges(ctx, workspaceID, page.Next, 10, 256<<10)
	if err != nil || page.ResyncRequired || len(page.Transactions) != 1 || len(page.Transactions[0].Records) != count-1 {
		t.Fatalf("suggestion journal lost deletions: %+v, %v", page, err)
	}
	for _, delta := range page.Transactions[0].Records {
		version := "3"
		if delta.Key.ID == data.IssueSuggestions[count/2].ID {
			version = "2" // The middle row did not move.
		}
		if !delta.Deleted || delta.Version != version || delta.Key.ID == retain {
			t.Fatalf("unexpected removal delta: %+v", delta)
		}
	}
}

func TestScaleP21SW185InlineFieldMaterializationOwnership(t *testing.T) {
	before := domain.Bootstrap{IssueSuggestions: []domain.IssueSuggestion{{ID: "duplicate"}, {ID: "duplicate"}}}
	after := cloneBootstrap(before)
	for index := 0; index < 1000; index++ {
		after.IssueSuggestions = append(after.IssueSuggestions, domain.IssueSuggestion{
			ID: fmt.Sprintf("suggestion-%d", index), IssueID: fmt.Sprintf("issue-%d", index), Type: "label",
			Metadata: map[string]any{"reasons": []string{strings.Repeat("<>&\x00", 256)}, "rank": index},
		})
	}
	left, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer left.Release()
	right, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer right.Release()
	field, found := metadataFieldByName("issueSuggestions")
	if !found {
		t.Fatal("suggestions field missing")
	}
	f := &legacyMetadataWriteField{field: field, full: true, before: left.fields[field.index], next: right.fields[field.index]}
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	plan := &legacyMetadataWritePlan{store: s}
	if (f.before.cost+f.next.cost)*64 <= uint64(metadataWorkBytes()) {
		t.Fatal("fixture does not exceed the previous full-field blanket admission")
	}
	work := &ResourceRead{store: s}
	a, b, err := plan.materializeField(t.Context(), f, work)
	if err != nil {
		work.Close()
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Interface(), before.IssueSuggestions) || !reflect.DeepEqual(b.Interface(), after.IssueSuggestions) {
		t.Fatal("materialization changed duplicate IDs, escaping, or native metadata values")
	}
	if held := s.MetadataWorkUsedForTesting(); held == 0 || held >= metadataWorkBytes() {
		t.Fatalf("materialized values lost their allowance: %d", held)
	} else {
		t.Logf("old_blanket_bytes=%d live_admitted_bytes=%d", (f.before.cost+f.next.cost)*64, held)
	}
	work.Close()
	if s.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("materialized field retained admission after release")
	}
	for _, test := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"cancelled", func() context.Context { ctx, cancel := context.WithCancel(t.Context()); cancel(); return ctx }(), context.Canceled},
		{"saturated", t.Context(), ErrMetadataWorkBudget},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "saturated" {
				s.metadataResidency.work.limit = 1
			}
			work := &ResourceRead{store: s}
			a, b, err := plan.materializeField(test.ctx, f, work)
			work.Close()
			if !errors.Is(err, test.want) || a.IsValid() || b.IsValid() || s.MetadataWorkUsedForTesting() != 0 {
				t.Fatalf("failure escaped owned lifetime: a=%t b=%t error=%v held=%d", a.IsValid(), b.IsValid(), err, s.MetadataWorkUsedForTesting())
			}
		})
	}
}

// The batched suggestion statements bind several rows in one statement. Each
// one moves the workspace's metadata revision, journaled or not, so other
// servers retire the metadata they cached before it.
func TestIssueSuggestionBatchedWritesMoveTheMetadataRevision(t *testing.T) {
	for _, journal := range []string{"off", "on"} {
		t.Run("journal "+journal, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_JOURNAL", journal)
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			ids := []string{"suggestion-a", "suggestion-b", "suggestion-c"}
			revision := func() int64 {
				t.Helper()
				value, err := f.cacheRevision(t.Context(), f.db, workspace, cacheKindMetadata)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			write := func(name string, run func(*legacyArrayWrites, *legacyMetadataWritePlan) error) {
				t.Helper()
				before := revision()
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				plan := &legacyMetadataWritePlan{tx: tx, workspace: workspace}
				if err := run(&legacyArrayWrites{plan: plan, field: "issueSuggestions"}, plan); err != nil {
					t.Fatal(name, err)
				}
				want := map[cacheRevisionKey]bool{{workspace, cacheKindMetadata}: true}
				if !reflect.DeepEqual(tx.cacheRevisions, want) {
					t.Fatalf("%s queued revisions %v, want %v", name, tx.cacheRevisions, want)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if after := revision(); after <= before {
					t.Fatalf("%s left the metadata revision at %d (was %d)", name, after, before)
				}
			}
			write("batched upsert", func(w *legacyArrayWrites, _ *legacyMetadataWritePlan) error {
				for order, id := range ids {
					w.args = append(w.args, workspace, w.field, id, order, []byte(`{"id":"`+id+`","issueId":"issue","type":"label"}`))
				}
				return w.flushUpserts(t.Context())
			})
			write("batched move", func(w *legacyArrayWrites, _ *legacyMetadataWritePlan) error {
				for order, id := range ids {
					w.moves = append(w.moves, legacyArrayMove{id, len(ids) - order})
				}
				return w.flushMoves(t.Context())
			})
			write("batched delete", func(_ *legacyArrayWrites, plan *legacyMetadataWritePlan) error {
				spec, _ := metadataFieldByName("issueSuggestions")
				change := legacyMetadataWriteField{field: spec}
				for index, id := range ids {
					change.oldOrder = append(change.oldOrder, index)
					change.rows = append(change.rows, legacyMetadataWriteRow{id: id})
				}
				return plan.deleteArrayDropped(t.Context(), &change, map[string]bool{})
			})
		})
	}
}
