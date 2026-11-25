package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"flow/api/internal/domain"
)

func TestScaleP21SW17AttributeMigrationLongUTF8Checkpoint(t *testing.T) {
	f, legacy := sw17AttributeFixture(t)
	probe := sw17AttributeProbe(f, legacy)
	workspace := f.data.Workspace.URLKey
	prefix := strings.Repeat("界", 180)
	issueID := func(index int) string { return fmt.Sprintf("%s-%04d", prefix, index) }
	if len(issueID(0)) <= 191 || utf8.RuneCountInString(issueID(0)) > 191 {
		t.Fatal("fixture must exceed 191 bytes while fitting the legacy character-width schema")
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for index := range 503 {
		estimate := float64(index + 1)
		raw, err := json.Marshal(domain.Issue{ID: issueID(index), Title: "Long checkpoint", Estimate: &estimate})
		if err != nil {
			t.Fatal(err)
		}
		result, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET id=?,data=?,list_data=? WHERE workspace_key=? AND id=?`,
			issueID(index), raw, raw, workspace, fmt.Sprintf("sw17-attribute-%04d", index))
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			t.Fatal("fixture did not rename exactly one canonical issue", changed, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	before := sw17AttributeSourceHash(t, f)
	type faultKey struct{}
	injected := errors.New("sw17 interrupted long-checkpoint migration")
	t.Cleanup(func() { f.db.commitHooks.Store(nil) })
	var oracle []sw17AttributeOutcome
	for _, source := range []*SQLiteStore{legacy, probe} {
		sw17ResetAttributeMigration(t, f)
		var outcomes []sw17AttributeOutcome
		for phase, failAt := range []int{2, 1, 0} {
			commits, statements := 0, 0
			f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
				if tagged, _ := tx.context().Value(faultKey{}).(bool); !tagged {
					return
				}
				commits++
				if failAt != 0 && commits == failAt {
					tx.writeErr = injected
				}
			}})
			ctx := WithSQLStatementObserver(context.WithValue(t.Context(), faultKey{}, true), func(string) { statements++ })
			err := source.migrateIssueAttributes(ctx)
			f.db.commitHooks.Store(nil)
			if failAt != 0 && (!errors.Is(err, injected) || commits != failAt) {
				t.Fatal("interruption did not occur at the requested checkpoint", phase, source == probe, commits, err)
			}
			if failAt == 0 && err != nil {
				t.Fatal("long checkpoint continuation failed", source == probe, err)
			}
			outcome := sw17AttributeOutcomeFor(t, f)
			outcomes = append(outcomes, outcome)
			wantLast, wantComplete, wantRows := issueID(249), 0, 250
			if failAt == 0 {
				wantLast, wantComplete, wantRows = issueID(502), issueAttributeVersion, 503
			}
			if outcome.Last != wantLast || outcome.Complete != wantComplete || len(outcome.Rows) != wantRows {
				t.Fatalf("wrong long-checkpoint phase=%d native=%v last=%q complete=%d rows=%d", phase, source == probe, outcome.Last, outcome.Complete, len(outcome.Rows))
			}
			for index, row := range outcome.Rows {
				if row[0] != issueID(index) || row[1] != "estimate" {
					t.Fatalf("ordered canonical continuation skipped or duplicated a key at phase=%d row=%d: %q", phase, index, row)
				}
			}
			if phase == 1 && !reflect.DeepEqual(outcomes[0], outcome) {
				t.Fatal("second-page interruption changed the retained long checkpoint or its committed rows")
			}
			if probe.metadataResidency.work.used != 0 {
				t.Fatal("long checkpoint path leaked a work lease")
			}
			t.Logf("long UTF8 checkpoint phase=%d bounded=%v statements=%d key-bytes=%d key-characters=%d rows=%d",
				phase, source == probe, statements, len(outcome.Last), utf8.RuneCountInString(outcome.Last), len(outcome.Rows))
		}
		if source == legacy {
			oracle = outcomes
		} else if !reflect.DeepEqual(oracle, outcomes) {
			t.Fatal("long UTF8 checkpoint interruptions/continuation differ from legacy")
		}
	}
	if sw17AttributeSourceHash(t, f) != before || probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("long checkpoint migration changed canonical source or decoded full metadata")
	}
}
