package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Every fixture owns its workspace even on a shared persistent database.
type scaleP21SW16TypedAttachmentFixture struct {
	scaleP09Fixture
	probe *SQLiteStore
	ids   []string
}

type scaleP21SW16TypedAttachmentOutcome struct {
	Rows     map[string][]string
	Last     string
	Complete int
}

func scaleP21SW16TypedAttachmentSeed(t *testing.T, count int) scaleP21SW16TypedAttachmentFixture {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	prefix := idgen.New("sw16-typed-attachment-")
	issues := make([]domain.Issue, count)
	ids := make([]string, count)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := range issues {
		ids[i] = fmt.Sprintf("%s-%03d", prefix, i)
		issues[i] = domain.Issue{
			ID: ids[i], Identifier: fmt.Sprintf("SW16-%d", i), Title: "Typed attachment source",
			Team: domain.Team{ID: prefix + "-team", Key: "SW16"}, State: domain.WorkflowState{ID: prefix + "-state"},
			Creator: f.data.Viewer, CreatedAt: stamp, UpdatedAt: stamp,
			Attachments: []domain.Attachment{{ID: "same-attachment-id", URL: fmt.Sprintf("https://example.test/%d", i)}},
		}
	}
	if err := f.ImportIssues(t.Context(), f.data.Workspace.URLKey, issues); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, probe} {
		source.mu.Lock()
		source.workspaces = map[string]domain.Bootstrap{f.data.Workspace.URLKey: source.workspaces[f.data.Workspace.URLKey]}
		source.mu.Unlock()
	}
	return scaleP21SW16TypedAttachmentFixture{f, probe, ids}
}

func (f scaleP21SW16TypedAttachmentFixture) reset(t *testing.T) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	for _, table := range []string{"issue_attachment_records", "issue_attachment_migrations"} {
		if _, err := f.db.ExecContext(t.Context(), "DELETE FROM "+table+" WHERE workspace_key=?", workspace); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{f.ids[0], "sw16-owned-orphan"} {
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_attachment_records(workspace_key,issue_id,url_hash) VALUES(?,?,?)`, workspace, id, attachmentKey("stale")); err != nil {
			t.Fatal(err)
		}
	}
}

func (f scaleP21SW16TypedAttachmentFixture) outcome(t *testing.T) scaleP21SW16TypedAttachmentOutcome {
	t.Helper()
	result := scaleP21SW16TypedAttachmentOutcome{Rows: map[string][]string{}}
	workspace := f.data.Workspace.URLKey
	rows, err := f.db.QueryContext(t.Context(), `SELECT issue_id,url_hash FROM issue_attachment_records WHERE workspace_key=? ORDER BY issue_id,url_hash`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		result.Rows[id] = append(result.Rows[id], hash)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(t.Context(), `SELECT last_id,complete FROM issue_attachment_migrations WHERE workspace_key=?`, workspace).Scan(&result.Last, &result.Complete); err != nil {
		t.Fatal(err)
	}
	return result
}

func (f scaleP21SW16TypedAttachmentFixture) body(t *testing.T, i int, raw []byte) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, raw, f.data.Workspace.URLKey, f.ids[i]); err != nil {
		t.Fatal(err)
	}
}

func (f scaleP21SW16TypedAttachmentFixture) sourceHash(t *testing.T) [32]byte {
	t.Helper()
	rows, err := f.db.QueryContext(t.Context(), `SELECT id,version,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY id`, f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for rows.Next() {
		var id string
		var version, order int64
		var data []byte
		if err := rows.Scan(&id, &version, &order, &data); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(hash, "%q:%d:%d:", id, version, order)
		_, _ = hash.Write(data)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return [32]byte(hash.Sum(nil))
}

func (f scaleP21SW16TypedAttachmentFixture) run(ctx context.Context, bounded bool) error {
	if bounded {
		return f.probe.scaleP21SW16MigrateTypedAttachmentIndex(ctx)
	}
	return f.SQLiteStore.migrateAttachmentIndex(ctx)
}

func (f scaleP21SW16TypedAttachmentFixture) clean(t *testing.T) {
	t.Helper()
	if f.probe.WorkspaceMetadataDecodes() != 0 || f.probe.metadataResidency.work.used != 0 {
		t.Fatal("typed attachment migration decoded full workspace or retained work")
	}
}

func TestScaleP21SW16TypedAttachmentDifferential(t *testing.T) {
	f := scaleP21SW16TypedAttachmentSeed(t, 140)
	var body map[string]json.RawMessage
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, f.data.Workspace.URLKey, f.ids[65]).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(saved, &body); err != nil {
		t.Fatal(err)
	}
	body["description"], _ = json.Marshal(strings.Repeat("large legal issue body ", 110000))
	url := "https://example.test/" + strings.Repeat("escaped\"\\\u6c49\u5b57\U0001f680/", 130000)
	body["attachments"], _ = json.Marshal([]domain.Attachment{{URL: url}, {URL: url}})
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	f.body(t, 65, raw)
	before := f.sourceHash(t)
	var expected scaleP21SW16TypedAttachmentOutcome
	for pair := 0; pair < 3; pair++ {
		for _, bounded := range []bool{false, true} {
			f.reset(t)
			runtime.GC()
			var start, end runtime.MemStats
			runtime.ReadMemStats(&start)
			sql, fullRows := 0, 0
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				sql++
				if bounded && strings.Contains(query, "SELECT data FROM issue_records") {
					fullRows++
				}
			})
			if err := f.run(ctx, bounded); err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&end)
			t.Logf("pair=%d bounded=%t bytes=%d allocs=%d SQL=%d", pair, bounded, end.TotalAlloc-start.TotalAlloc, end.Mallocs-start.Mallocs, sql)
			got := f.outcome(t)
			if !bounded {
				expected = got
			} else if !reflect.DeepEqual(got, expected) {
				t.Fatal("typed source index/checkpoint differs")
			}
			if fullRows != 0 || got.Complete != 1 || got.Last != f.ids[139] || !reflect.DeepEqual(got.Rows[f.ids[65]], []string{attachmentKey(url)}) {
				t.Fatalf("typed large-body migration lost bytes/checkpoint or bypassed gated transfer: %+v", got)
			}
			if err := f.run(t.Context(), bounded); err != nil || !reflect.DeepEqual(f.outcome(t), got) {
				t.Fatal("completed migration changed on repeat", err)
			}
			f.clean(t)
		}
	}
	if f.sourceHash(t) != before {
		t.Fatal("migration changed canonical rows/version/order")
	}
}

func TestScaleP21SW16TypedAttachmentCheckpointIdentity(t *testing.T) {
	f := scaleP21SW16TypedAttachmentSeed(t, 140)
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, f.data.Workspace.URLKey, f.ids[63]).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, decoded string
	}{
		{"repeat", f.ids[0]},
		{"skip", f.ids[90]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(domain.Issue{ID: tc.decoded, Attachments: []domain.Attachment{{URL: "https://example.test/decoded-collision"}}})
			if err != nil {
				t.Fatal(err)
			}
			f.body(t, 63, raw)
			var prior scaleP21SW16TypedAttachmentOutcome
			for _, bounded := range []bool{false, true} {
				f.reset(t)
				if err := f.run(t.Context(), bounded); err != nil {
					t.Fatal(err)
				}
				got := f.outcome(t)
				if !bounded {
					prior = got
				} else if !reflect.DeepEqual(got, prior) {
					t.Fatalf("legacy decoded checkpoint quirk changed: old=%+v new=%+v", prior, got)
				}
				if got.Complete != 1 || got.Last != f.ids[139] {
					t.Fatal("legacy checkpoint lost")
				}
				if tc.name == "repeat" && !reflect.DeepEqual(got.Rows[f.ids[0]], []string{attachmentKey("https://example.test/decoded-collision")}) {
					t.Fatal("later repeated page did not replace earlier duplicate-ID union")
				}
				if tc.name == "skip" {
					if _, exists := got.Rows[f.ids[65]]; exists {
						t.Fatal("legacy skipped source row was indexed")
					}
				}
				f.clean(t)
			}
		})
	}
	f.body(t, 63, saved)
	// Two decoded identities on the same physical page are unioned by the
	// original diff writer rather than one source replacing the other.
	raw, _ := json.Marshal(domain.Issue{ID: f.ids[0], Attachments: []domain.Attachment{{URL: "https://example.test/same-page"}}})
	f.body(t, 1, raw)
	var prior scaleP21SW16TypedAttachmentOutcome
	for _, bounded := range []bool{false, true} {
		f.reset(t)
		if err := f.run(t.Context(), bounded); err != nil {
			t.Fatal(err)
		}
		got := f.outcome(t)
		if !bounded {
			prior = got
		} else if !reflect.DeepEqual(got, prior) {
			t.Fatal("same-page duplicate decoded IDs differ")
		}
		if len(got.Rows[f.ids[0]]) != 2 {
			t.Fatal("same-page duplicate decoded ID discarded an attachment")
		}
		f.clean(t)
	}
}

func TestScaleP21SW16TypedAttachmentJSONSemantics(t *testing.T) {
	f := scaleP21SW16TypedAttachmentSeed(t, 140)
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, f.data.Workspace.URLKey, f.ids[65]).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE issue_records SET data=? WHERE workspace_key=? AND id=?`, saved, f.data.Workspace.URLKey, f.ids[65]); err != nil {
			t.Error("restore owned malformed source before fixture cleanup", err)
		}
	})
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"null", `null`, true},
		{"missing", `{"attachments":[null,{},{"url":null},{"url":"null"}]}`, true},
		{"alias", `{"ID":%q,"id":null,"ATTACHMENTS":[{"URL":"first","u\u0072l":"last"},{"url":null},null]}`, true},
		{"duplicate", `{"id":%q,"attachments":[{"url":"first"}],"attachments":[{"url":"second"}]}`, true},
		{"null-collection", `{"id":%q,"attachments":null}`, true},
		{"title-type", `{"id":%q,"title":7,"attachments":[{"url":"unused"}]}`, false},
		{"time-type", `{"id":%q,"createdAt":"invalid","attachments":[]}`, false},
		{"creator-type", `{"id":%q,"attachments":[{"url":"unused","creator":7}]}`, false},
		{"url-type", `{"id":%q,"attachments":[{"url":7}]}`, false},
		{"slice-type", `{"id":%q,"labels":{}}`, false},
		{"reaction-type", `{"id":%q,"reactions":{"x":7}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.source
			if strings.Contains(raw, "%q") {
				raw = fmt.Sprintf(raw, f.ids[65])
			}
			f.body(t, 65, []byte(raw))
			before := f.sourceHash(t)
			var prior scaleP21SW16TypedAttachmentOutcome
			priorError := ""
			for _, bounded := range []bool{false, true} {
				f.reset(t)
				err := f.run(t.Context(), bounded)
				text := ""
				if err != nil {
					text = err.Error()
				}
				got := f.outcome(t)
				if !bounded {
					prior, priorError = got, text
				} else if text != priorError || !reflect.DeepEqual(got, prior) {
					t.Fatalf("complete typed JSON semantics differ: errors=%q/%q old=%+v new=%+v", priorError, text, prior, got)
				}
				if tc.valid && err != nil || !tc.valid && err == nil {
					t.Fatalf("unexpected typed JSON result: %v", err)
				}
				if !tc.valid && (got.Last != f.ids[63] || got.Complete != 0) {
					t.Fatal("failed typed decode changed committed first-page checkpoint")
				}
				f.clean(t)
			}
			if f.sourceHash(t) != before {
				t.Fatal("typed decode changed canonical source")
			}
		})
	}
}

func TestScaleP21SW16TypedAttachmentAtomicity(t *testing.T) {
	f := scaleP21SW16TypedAttachmentSeed(t, 140)
	type faultKey struct{}
	injected := errors.New("sw16 typed attachment precommit")
	t.Cleanup(func() { f.db.commitHooks.Store(nil) })
	for _, failAt := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			var prior scaleP21SW16TypedAttachmentOutcome
			for _, bounded := range []bool{false, true} {
				f.reset(t)
				commits := 0
				f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
					if tagged, _ := tx.context().Value(faultKey{}).(bool); tagged {
						commits++
						if commits == failAt {
							tx.writeErr = injected
						}
					}
				}})
				err := f.run(context.WithValue(t.Context(), faultKey{}, true), bounded)
				f.db.commitHooks.Store(nil)
				if !errors.Is(err, injected) || commits != failAt {
					t.Fatalf("precommit injection missed: %v, commits=%d", err, commits)
				}
				got := f.outcome(t)
				if !bounded {
					prior = got
				} else if !reflect.DeepEqual(got, prior) {
					t.Fatal("page transaction rollback/checkpoint differs")
				}
				f.clean(t)
				if err := f.run(t.Context(), bounded); err != nil || f.outcome(t).Complete != 1 {
					t.Fatal("migration did not resume after failed commit", err)
				}
			}
		})
	}
	var prior scaleP21SW16TypedAttachmentOutcome
	for _, bounded := range []bool{false, true} {
		f.reset(t)
		limit := f.db.maxTransactionBytes
		f.db.maxTransactionBytes = 128
		err := f.run(t.Context(), bounded)
		f.db.maxTransactionBytes = limit
		if !errors.Is(err, ErrTransactionWriteBudget) {
			t.Fatal("write budget was bypassed", err)
		}
		got := f.outcome(t)
		if !bounded {
			prior = got
		} else if !reflect.DeepEqual(got, prior) {
			t.Fatal("write-budget rollback differs")
		}
		f.clean(t)
		if err := f.run(t.Context(), bounded); err != nil {
			t.Fatal("write-budget recovery failed", err)
		}
	}
	f.reset(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.run(ctx, true); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled bounded migration succeeded", err)
	}
	f.clean(t)
	if err := f.run(t.Context(), true); err != nil {
		t.Fatal("cancelled migration failed recovery", err)
	}
	// Already-current indexes must not be deleted and reinserted: a budget
	// that admits the marker alone still admits the original no-op diff.
	for _, bounded := range []bool{false, true} {
		if _, err := f.db.ExecContext(t.Context(), `UPDATE issue_attachment_migrations SET last_id='',complete=0 WHERE workspace_key=?`, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		prior := f.outcome(t)
		writes := 0
		ctx := WithSQLStatementObserver(t.Context(), func(query string) {
			if strings.HasPrefix(query, "DELETE FROM issue_attachment_records") || strings.HasPrefix(query, "INSERT INTO issue_attachment_records") {
				writes++
			}
		})
		if err := f.run(ctx, bounded); err != nil || writes != 0 {
			t.Fatal("current index performed needless row writes", err, writes)
		}
		if !reflect.DeepEqual(f.outcome(t).Rows, prior.Rows) {
			t.Fatal("current index changed")
		}
		f.clean(t)
	}
}

func TestScaleP21SW16TypedAttachmentAdmission(t *testing.T) {
	f := scaleP21SW16TypedAttachmentSeed(t, 1)
	var saved []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM issue_records WHERE workspace_key=? AND id=?`, f.data.Workspace.URLKey, f.ids[0]).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	visited, transferred := false, false
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "THEN data ELSE NULL END FROM issue_records") {
			transferred = true
		}
	})
	err = f.probe.scaleP21SW16ReadAttachmentIssue(ctx, tx, f.data.Workspace.URLKey, scaleP21SW16AttachmentHeader{id: f.ids[0], size: metadataWorkBytes}, func(context.Context, domain.Issue) error {
		visited = true
		return nil
	})
	_ = tx.Rollback()
	if !errors.Is(err, ErrMetadataWorkBudget) || visited || transferred {
		t.Fatal("oversized header transferred or decoded its source", err)
	}
	f.clean(t)
	// Small empty objects still expand to a large typed slice.
	dense := []byte(`{"id":"dense","attachments":[` + strings.Repeat(`{},`, 250000) + `{}]}`)
	f.body(t, 0, dense)
	f.reset(t)
	bodyReads := 0
	ctx = WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "THEN data ELSE NULL END FROM issue_records") {
			bodyReads++
		}
	})
	if err := f.run(ctx, true); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("dense structural decode bypassed admission", err)
	}
	got := f.outcome(t)
	if got.Last != "" || got.Complete != 0 || len(got.Rows) != 2 || bodyReads != 1 {
		t.Fatal("dense structural rejection changed index/checkpoint or hydration order", got, bodyReads)
	}
	f.clean(t)
	f.body(t, 0, saved)
	f.reset(t)
	limit := f.probe.metadataResidency.work.limit
	f.probe.metadataResidency.work.limit = 2<<20 + 128<<10
	bodyReads = 0
	err = f.run(ctx, true)
	f.probe.metadataResidency.work.limit = limit
	if !errors.Is(err, ErrMetadataWorkBudget) || bodyReads != 0 {
		t.Fatal("unavailable admission transferred source body", err, bodyReads)
	}
	f.clean(t)
	if err := f.run(t.Context(), true); err != nil || f.outcome(t).Complete != 1 {
		t.Fatal("admission rejection failed recovery", err)
	}
}

func TestScaleP21SW16TypedAttachmentImportedIdentity(t *testing.T) {
	f := scaleP21SW16TypedAttachmentSeed(t, 1)
	if f.dialect != "sqlite" {
		t.Skip("SQLite accepted importer identity retains a NUL column")
	}
	id := idgen.New("sw16-imported-typed-")
	issue := domain.Issue{
		ID: id + "\x00", Identifier: "SW16-NUL", Title: "Accepted importer",
		Team: domain.Team{ID: id + "-team", Key: "SW16"}, State: domain.WorkflowState{ID: id + "-state"},
		Creator: f.data.Viewer, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		Attachments: []domain.Attachment{{URL: "https://example.test/imported"}},
	}
	if err := f.ImportIssues(t.Context(), f.data.Workspace.URLKey, []domain.Issue{issue}); err != nil {
		t.Fatal("accepted source importer failed", err)
	}
	var prior scaleP21SW16TypedAttachmentOutcome
	for _, bounded := range []bool{false, true} {
		f.reset(t)
		if err := f.run(t.Context(), bounded); err != nil {
			t.Fatal(err)
		}
		got := f.outcome(t)
		if !bounded {
			prior = got
		} else if !reflect.DeepEqual(got, prior) {
			t.Fatal("accepted source typed identity/checkpoint differs", prior, got)
		}
		if !reflect.DeepEqual(got.Rows[id], []string{attachmentKey(issue.Attachments[0].URL)}) {
			t.Fatal("importer decoded identity was not indexed")
		}
		if _, exists := got.Rows[issue.ID]; exists {
			t.Fatal("physical unsanitized identity was indexed")
		}
		f.clean(t)
	}
}
