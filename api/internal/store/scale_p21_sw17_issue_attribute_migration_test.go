package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type sw17AttributeOutcome struct {
	Rows       [][]string
	Last       string
	Complete   int
	Checkpoint bool
	Recurrence bool
}

func sw17AttributeOutcomeFor(t *testing.T, f scaleP09Fixture) sw17AttributeOutcome {
	t.Helper()
	out := sw17AttributeOutcome{Rows: [][]string{}}
	rows, err := f.db.QueryContext(t.Context(), `SELECT issue_id,field,value FROM issue_attribute_records WHERE workspace_key=? ORDER BY issue_id,field,value`, f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		row := make([]string, 3)
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		out.Rows = append(out.Rows, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = f.db.QueryRowContext(t.Context(), `SELECT last_id,complete FROM issue_attribute_migrations WHERE workspace_key=?`, f.data.Workspace.URLKey).Scan(&out.Last, &out.Complete)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	out.Checkpoint = err == nil
	var count int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM recurrence_instance_migrations WHERE workspace_key=?`, f.data.Workspace.URLKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	out.Recurrence = count > 0
	return out
}

func sw17AttributeSourceHash(t *testing.T, f scaleP09Fixture) [32]byte {
	t.Helper()
	digest := sha256.New()
	rows, err := f.db.QueryContext(t.Context(), `SELECT id,data FROM issue_records WHERE workspace_key=? ORDER BY id`, f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		digest.Write([]byte(id))
		digest.Write(raw)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return [32]byte(digest.Sum(nil))
}

func sw17AttributeProbe(f scaleP09Fixture, legacy *SQLiteStore) *SQLiteStore {
	return &SQLiteStore{
		db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes,
		workspaces: legacy.workspaces, metadataResidency: newMetadataResidency(),
	}
}

func TestScaleP21SW17AttributeMigrationDifferential(t *testing.T) {
	f, legacy := sw17AttributeFixture(t)
	probe := sw17AttributeProbe(f, legacy)
	workspace := f.data.Workspace.URLKey
	before := sw17AttributeSourceHash(t, f)
	outside := workspace + "-outside"
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_attribute_records(workspace_key,issue_id,field,value) VALUES(?,'outside','estimate','99')`, outside); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `DELETE FROM issue_attribute_records WHERE workspace_key=?`, outside); err != nil {
			t.Error(err)
		}
	})
	for _, job := range []struct {
		name string
		run  func(*SQLiteStore, context.Context) error
	}{
		{"attributes", (*SQLiteStore).migrateIssueAttributes},
		{"recurrence", (*SQLiteStore).migrateRecurrenceInstances},
	} {
		for _, phase := range []string{"fresh", "completed", "resume", "older-version"} {
			t.Run(job.name+"/"+phase, func(t *testing.T) {
				var outcomes []sw17AttributeOutcome
				for _, source := range []*SQLiteStore{legacy, probe} {
					sw17ResetAttributeMigration(t, f)
					if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_attribute_records(workspace_key,issue_id,field,value) VALUES(?,'sw17-attribute-0001','stale-field','remove'),(?,'orphan','orphan-field','keep')`, workspace, workspace); err != nil {
						t.Fatal(err)
					}
					last, complete := "sw17-attribute-0009", 0
					if phase == "older-version" {
						complete = issueAttributeVersion - 1
						last = "sw17-attribute-0400"
					} else if phase == "completed" {
						complete = issueAttributeVersion
					}
					if phase != "fresh" {
						if job.name == "attributes" {
							if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_attribute_migrations(workspace_key,last_id,complete) VALUES(?,?,?)`, workspace, last, complete); err != nil {
								t.Fatal(err)
							}
						} else if phase == "completed" {
							if _, err := f.db.ExecContext(t.Context(), `INSERT INTO recurrence_instance_migrations(workspace_key) VALUES(?)`, workspace); err != nil {
								t.Fatal(err)
							}
						}
					}
					statements := 0
					ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
					if err := job.run(source, ctx); err != nil {
						t.Fatal("migration failed", source == probe, err)
					}
					outcome := sw17AttributeOutcomeFor(t, f)
					if phase == "fresh" && len(outcome.Rows) < 5000 {
						t.Fatal("fixture did not exercise a complete multi-batch index migration", len(outcome.Rows))
					}
					outcomes = append(outcomes, outcome)
					t.Logf("job=%s phase=%s bounded=%v statements=%d rows=%d", job.name, phase, source == probe, statements, len(outcome.Rows))
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("index or checkpoint differs: old rows=%d native rows=%d old=%+v native=%+v", len(outcomes[0].Rows), len(outcomes[1].Rows), outcomes[0], outcomes[1])
				}
			})
		}
	}
	if sw17AttributeSourceHash(t, f) != before {
		t.Fatal("index migration changed canonical issue bodies")
	}
	var outsideValue string
	if err := f.db.QueryRowContext(t.Context(), `SELECT value FROM issue_attribute_records WHERE workspace_key=? AND issue_id='outside' AND field='estimate'`, outside).Scan(&outsideValue); err != nil || outsideValue != "99" {
		t.Fatal("index migration changed another workspace", err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("migration decoded full metadata or leaked work")
	}
}

func TestScaleP21SW17AttributeMigrationRollback(t *testing.T) {
	f, legacy := sw17AttributeFixture(t)
	probe := sw17AttributeProbe(f, legacy)
	before := sw17AttributeSourceHash(t, f)
	type faultKey struct{}
	injected := errors.New("sw17 migration precommit failure")
	t.Cleanup(func() { f.db.commitHooks.Store(nil) })
	for _, job := range []struct {
		name string
		run  func(*SQLiteStore, context.Context) error
	}{
		{"attributes", (*SQLiteStore).migrateIssueAttributes},
		{"recurrence", (*SQLiteStore).migrateRecurrenceInstances},
	} {
		t.Run(job.name, func(t *testing.T) {
			var outcomes []sw17AttributeOutcome
			for _, source := range []*SQLiteStore{legacy, probe} {
				sw17ResetAttributeMigration(t, f)
				commits, failAt := 0, 1
				if job.name == "attributes" {
					failAt = 3
				}
				f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
					if tagged, _ := tx.context().Value(faultKey{}).(bool); !tagged {
						return
					}
					commits++
					if commits == failAt {
						tx.writeErr = injected
					}
				}})
				err := job.run(source, context.WithValue(t.Context(), faultKey{}, true))
				f.db.commitHooks.Store(nil)
				if !errors.Is(err, injected) || commits != failAt {
					t.Fatal("late precommit failure changed", source == probe, commits, err)
				}
				outcomes = append(outcomes, sw17AttributeOutcomeFor(t, f))
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("late error changed atomic/checkpoint state: old=%+v native=%+v", outcomes[0], outcomes[1])
			}
			if job.name == "attributes" && (outcomes[0].Last != "sw17-attribute-0499" || outcomes[0].Complete != 0) {
				t.Fatal("attribute migration lost its committed 250-record checkpoints")
			}
			if job.name == "recurrence" && (len(outcomes[0].Rows) != 0 || outcomes[0].Recurrence) {
				t.Fatal("recurrence migration lost whole-workspace rollback")
			}
		})
	}
	if sw17AttributeSourceHash(t, f) != before || probe.metadataResidency.work.used != 0 {
		t.Fatal("failed migration changed source or leaked work")
	}
}

func TestScaleP21SW17AttributeMigrationTypedRows(t *testing.T) {
	f, legacy := sw17AttributeFixture(t)
	probe := sw17AttributeProbe(f, legacy)
	workspace := f.data.Workspace.URLKey
	ids := []string{"sw17-attribute-0000", "sw17-attribute-0001", "sw17-attribute-0002", "sw17-attribute-0501"}
	for _, id := range ids {
		var saved []byte
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&saved); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := f.db.ExecContext(context.Background(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, saved, workspace, id); err != nil {
				t.Error(err)
			}
		})
	}
	longID := strings.Repeat("关联", 120)
	raw := []byte(`{"id":"sw17-attribute-0001","description":"` + strings.Repeat("中", 100000) +
		`","RecurrenceSeriesID":"series","recurrenceSeriesId":null,"recurrenceOccurrence":"2026-12-01","estimate":2,"Estimate":null,"labels":[{"id":"first"}],"Labels":[{}],"relations":[{"type":"blocks"}],"Relations":[{}],"suggestedRelatedIds":["` +
		longID + `"],"triagedAt":"2026-11-13T11:22:33.123456789+09:00"}`)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, workspace, ids[1]); err != nil {
		t.Fatal(err)
	}
	if f.dialect == "sqlite" {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=CAST(data AS TEXT) WHERE workspace_key=? AND id=?`, workspace, ids[1]); err != nil {
			t.Fatal(err)
		}
	}
	// The SQL LIKE is only a candidate filter; a nested property must not
	// turn a non-recurring typed issue into an instance.
	falsePositive := []byte(`{"id":"sw17-attribute-0000","future":{"recurrenceSeriesId":"nested"},"estimate":7}`)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, falsePositive, workspace, ids[0]); err != nil {
		t.Fatal(err)
	}
	// Preserve legacy batch union semantics even when imported row identity
	// differs from its typed body ID.
	duplicate := []byte(`{"id":"sw17-attribute-0001","recurrenceSeriesId":"series","recurrenceOccurrence":"2026-12-02","estimate":9,"dueDate":"2026-12-20"}`)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, duplicate, workspace, ids[2]); err != nil {
		t.Fatal(err)
	}
	// This duplicate falls in another selected recurrence batch. Its final
	// values depend on preserving the source cursor's actual order.
	lastDuplicate := []byte(`{"id":"sw17-attribute-0001","recurrenceSeriesId":"last-series","recurrenceOccurrence":"2026-12-03","estimate":11}`)
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, lastDuplicate, workspace, ids[3]); err != nil {
		t.Fatal(err)
	}
	before := sw17AttributeSourceHash(t, f)
	for _, job := range []struct {
		name string
		run  func(*SQLiteStore, context.Context) error
	}{
		{"attributes", (*SQLiteStore).migrateIssueAttributes},
		{"recurrence", (*SQLiteStore).migrateRecurrenceInstances},
	} {
		t.Run(job.name, func(t *testing.T) {
			var outcomes []sw17AttributeOutcome
			for _, source := range []*SQLiteStore{legacy, probe} {
				sw17ResetAttributeMigration(t, f)
				if err := job.run(source, t.Context()); err != nil {
					t.Fatal(err)
				}
				outcomes = append(outcomes, sw17AttributeOutcomeFor(t, f))
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("typed source/batch fidelity differs: old rows=%d native rows=%d", len(outcomes[0].Rows), len(outcomes[1].Rows))
			}
		})
	}
	if f.dialect == "sqlite" {
		t.Run("recurrence-reverse-unordered", func(t *testing.T) {
			f.db.SetMaxOpenConns(1)
			defer f.db.SetMaxOpenConns(4)
			if _, err := f.db.ExecContext(t.Context(), `PRAGMA reverse_unordered_selects=ON`); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := f.db.ExecContext(context.Background(), `PRAGMA reverse_unordered_selects=OFF`); err != nil {
					t.Error(err)
				}
			}()
			var outcomes []sw17AttributeOutcome
			for _, source := range []*SQLiteStore{legacy, probe} {
				sw17ResetAttributeMigration(t, f)
				if err := source.migrateRecurrenceInstances(t.Context()); err != nil {
					t.Fatal(err)
				}
				outcomes = append(outcomes, sw17AttributeOutcomeFor(t, f))
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("native recurrence changed duplicate winners under reverse source order")
			}
		})
	}
	if sw17AttributeSourceHash(t, f) != before || probe.metadataResidency.work.used != 0 {
		t.Fatal("typed migration changed canonical rows or leaked work")
	}
}

func TestScaleP21SW17AttributeMigrationAdmission(t *testing.T) {
	f, legacy := sw17AttributeFixture(t)
	probe := sw17AttributeProbe(f, legacy)
	workspace := f.data.Workspace.URLKey
	f.db.SetMaxOpenConns(1)
	defer f.db.SetMaxOpenConns(4)
	for _, job := range []struct {
		name string
		run  func(*SQLiteStore, context.Context) error
	}{
		{"attributes", (*SQLiteStore).migrateIssueAttributes},
		{"recurrence", (*SQLiteStore).migrateRecurrenceInstances},
	} {
		t.Run(job.name, func(t *testing.T) {
			sw17ResetAttributeMigration(t, f)
			probe.metadataResidency.work.limit = 4 << 20
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			err := job.run(probe, ctx)
			cancel()
			if !errors.Is(err, ErrMetadataWorkBudget) {
				t.Fatal("constrained snapshot job did not fail admission", err)
			}
			state := sw17AttributeOutcomeFor(t, f)
			if len(state.Rows) != 0 || state.Recurrence || state.Last != "" || probe.metadataResidency.work.used != 0 {
				t.Fatal("failed admission committed partial work or leaked leases")
			}
			probe.metadataResidency.work.limit = metadataWorkBytes
			if err := job.run(probe, t.Context()); err != nil {
				t.Fatal("migration failed to resume after admission failure", err)
			}
			if probe.metadataResidency.work.used != 0 {
				t.Fatal("successful migration leaked leases")
			}
			sw17ResetAttributeMigration(t, f)
			cancelled, stop := context.WithCancel(t.Context())
			stop()
			if err := job.run(probe, cancelled); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled job hid its error", err)
			}
		})
	}
	t.Run("recurrence-spool-budget", func(t *testing.T) {
		sw17ResetAttributeMigration(t, f)
		used := recordSpoolBytes.Load()
		t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", fmt.Sprint(used+64))
		if err := probe.migrateRecurrenceInstances(t.Context()); !errors.Is(err, ErrRecordSpoolBudget) {
			t.Fatal("spool exhaustion did not fail explicitly", err)
		}
		state := sw17AttributeOutcomeFor(t, f)
		if len(state.Rows) != 0 || state.Recurrence || recordSpoolBytes.Load() != used || probe.metadataResidency.work.used != 0 {
			t.Fatal("spool failure committed index rows/marker or leaked reservations")
		}
	})
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := probe.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	err = probe.sw17ReadAttributeIssue(ctx, tx, workspace, sw17AttributeHeader{id: "sw17-attribute-0000", size: 100}, func(context.Context, domain.Issue) error {
		t.Fatal("failed SQL reached issue visitor")
		return nil
	})
	release()
	if !errors.Is(err, sql.ErrTxDone) || probe.metadataResidency.work.used != 0 || probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("SQL failure became empty data or leaked work", err)
	}
	// Keep malformed typed JSON private to this transaction. Another test
	// process must never encounter it during its own startup migration.
	tx, err = f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var original []byte
	id := "sw17-attribute-0001"
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&original); err != nil {
		t.Fatal(err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(original, &values); err != nil {
		t.Fatal(err)
	}
	values["estimate"] = json.RawMessage(`"not-a-number"`)
	invalid, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, invalid, workspace, id); err != nil {
		t.Fatal(err)
	}
	var old domain.Issue
	oldErr := json.Unmarshal(invalid, &old)
	ctx, release, err = probe.admitMetadataWork(t.Context(), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	nativeErr := probe.sw17ReadAttributeIssue(ctx, tx, workspace, sw17AttributeHeader{id: id, size: int64(len(invalid))}, func(context.Context, domain.Issue) error {
		t.Fatal("malformed issue reached attribute visitor")
		return nil
	})
	release()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var oldTyped, nativeTyped *json.UnmarshalTypeError
	if !errors.As(oldErr, &oldTyped) || !errors.As(nativeErr, &nativeTyped) || probe.metadataResidency.work.used != 0 {
		t.Fatal("typed decode errors or resource release changed", oldErr, nativeErr)
	}
}

func TestScaleP21SW17AttributeMigrationMeasurement(t *testing.T) {
	f, legacy := sw17AttributeFixture(t)
	probe := sw17AttributeProbe(f, legacy)
	for _, job := range []struct {
		name string
		run  func(*SQLiteStore, context.Context) error
	}{
		{"attributes", (*SQLiteStore).migrateIssueAttributes},
		{"recurrence", (*SQLiteStore).migrateRecurrenceInstances},
	} {
		for _, source := range []*SQLiteStore{legacy, probe} {
			sw17ResetAttributeMigration(t, f)
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if err := job.run(source, t.Context()); err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			t.Logf("%s bounded=%v 503 issues allocated-bytes=%d allocations=%d", job.name, source == probe, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
		}
	}
}

func sw17AttributeFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	// Other packages open this same persistent database in CI. Their startup
	// migrations must not advance the owned checkpoints being compared here.
	if f.startupLock != nil {
		backend, err := f.startupLock(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if backend != nil {
			lock, err := acquireStartupLock(t.Context(), backend, startupLockOptions{name: "SW17 attribute migration oracle"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(lock.release)
		}
	}
	issues := make([]domain.Issue, 503)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	estimate, due := 3.5, "2026-12-01"
	for index := range issues {
		issue := domain.Issue{
			ID: fmt.Sprintf("sw17-attribute-%04d", index), Title: "Attribute source", Description: strings.Repeat("unrelated issue body ", 800),
			Number: index + 1, Identifier: fmt.Sprintf("SW17-%d", index+1),
			Estimate: &estimate, DueDate: &due, CompletedAt: &stamp, TriagedAt: &stamp,
			TemplateID: "template", ExternalSource: "fixture", Delegate: &f.data.Viewer,
			Labels:               []domain.IssueLabel{{ID: "first-label"}, {ID: "second-label"}},
			Relations:            []domain.IssueRelation{{Type: "blocks"}, {Type: "related"}},
			SuggestedAssigneeIDs: []string{"suggested-user"}, SuggestedLabelIDs: []string{"suggested-label"},
			SuggestedProjectIDs: []string{"suggested-project"}, SuggestedTeamIDs: []string{"suggested-team"},
			SuggestedDuplicateIDs: []string{"suggested-duplicate"}, SuggestedRelatedIDs: []string{"suggested-related"},
			AgentSessionID: "session", AddedToCycle: "cycle", AutoClosed: true,
			CreatedAt: stamp, UpdatedAt: stamp,
		}
		if index%2 == 1 {
			issue.RecurrenceSeriesID, issue.RecurrenceOccurrence = "series", fmt.Sprintf("2026-12-%02d", index%28+1)
		}
		if index == 251 {
			issue.Description = strings.Repeat("x", 3<<20)
		}
		issues[index] = issue
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := f.importIssueRecordBatch(t.Context(), tx, f.data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Restrict the migration catalog to this test's owned workspace even in
	// the shared persistent/external test database.
	legacy := &SQLiteStore{
		db: f.db, dialect: f.dialect, maxStateBytes: f.maxStateBytes,
		workspaces: map[string]domain.Bootstrap{f.data.Workspace.URLKey: {Workspace: f.data.Workspace}},
	}
	return f, legacy
}

func sw17ResetAttributeMigration(t *testing.T, f scaleP09Fixture) {
	t.Helper()
	for _, table := range []string{"issue_attribute_records", "issue_attribute_migrations", "recurrence_instance_migrations"} {
		if _, err := f.db.ExecContext(t.Context(), "DELETE FROM "+table+" WHERE workspace_key=?", f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScaleP21SW17AttributeMigrationBaseline(t *testing.T) {
	f, legacy := sw17AttributeFixture(t)
	for _, job := range []struct {
		name string
		run  func(context.Context) error
	}{{"attributes", legacy.migrateIssueAttributes}, {"recurrence", legacy.migrateRecurrenceInstances}} {
		sw17ResetAttributeMigration(t, f)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if err := job.run(t.Context()); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		t.Logf("legacy %s 503 issues allocated-bytes=%d allocations=%d", job.name, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
	}
}
