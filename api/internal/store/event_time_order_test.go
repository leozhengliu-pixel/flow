package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/idgen"
)

// eventTimeOrderStores returns a SQLite store and, when FLOW_TEST_DATABASE_DRIVER
// and FLOW_TEST_DATABASE_URL name one, a PostgreSQL or MySQL store.
func eventTimeOrderStores(t *testing.T) map[string]*SQLiteStore {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "event-time.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	stores := map[string]*SQLiteStore{"sqlite": repo}
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		external, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { external.Close() })
		stores[driver] = external
	}
	return stores
}

// insertLegacyDomainEvent writes a row as versions before fixed-width times
// did: created_at is RFC3339Nano text with trailing zeros trimmed.
func insertLegacyDomainEvent(t *testing.T, repo *SQLiteStore, workspaceID, id, eventType, aggregateID, createdAt string, seq any) {
	t.Helper()
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO domain_events(id,event_type,aggregate_id,payload,previous_values,created_at,workspace_id,seq,actor_id) VALUES(?,?,?,?,?,?,?,?,?)`, id, eventType, eventAggregateKey(aggregateID), []byte(`{}`), nil, createdAt, workspaceID, seq, nil); err != nil {
		t.Fatal(err)
	}
}

// writeTimedEvent records an event through insertDomainEvent, with its time
// as RFC3339Nano text like the mutation writers pass it.
func writeTimedEvent(t *testing.T, repo *SQLiteStore, workspaceID, id string, at time.Time) {
	t.Helper()
	tx, err := repo.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertDomainEvent(t.Context(), tx, domainEventRow{ID: id, Type: "issue.updated", AggregateID: "issue_tso", Payload: []byte(`{}`), CreatedAt: at.Format(time.RFC3339Nano), WorkspaceID: workspaceID}); err != nil {
		tx.Rollback()
		t.Fatalf("insert event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func cleanupEventTimeOrder(t *testing.T, repo *SQLiteStore, workspaceIDs ...string) {
	t.Cleanup(func() {
		for _, id := range workspaceIDs {
			_, _ = repo.db.ExecContext(context.Background(), `DELETE FROM domain_events WHERE workspace_id=?`, id)
			_, _ = repo.db.ExecContext(context.Background(), `DELETE FROM workspace_event_seq WHERE workspace_id=?`, id)
		}
	})
}

// Two events in the same second whose fractions have different lengths
// ("05.1Z", "05.12Z") sort out of time order as RFC3339Nano text, since 'Z'
// sorts after the digits and '.' before them. New rows are stored fixed width,
// and every read that orders domain events orders rows written before that by
// time.
func TestDomainEventTimesOrderChronologically(t *testing.T) {
	for name, repo := range eventTimeOrderStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			suffix := strings.ToLower(idgen.New(""))
			paged, deleted, numbered := "ws_tso_page_"+suffix, "ws_tso_del_"+suffix, "ws_tso_seq_"+suffix
			cleanupEventTimeOrder(t, repo, paged, deleted, numbered)

			// Chronological: d (.000) < a (.100) < b (.120) < c (.500) < e (next second).
			// As text: b < a < c < d < e.
			legacy := []struct{ id, at string }{
				{"evt_tso_b_" + suffix, "2026-01-02T03:04:05.12Z"},
				{"evt_tso_a_" + suffix, "2026-01-02T03:04:05.1Z"},
				{"evt_tso_c_" + suffix, "2026-01-02T03:04:05.5Z"},
				{"evt_tso_d_" + suffix, "2026-01-02T03:04:05Z"},
				{"evt_tso_e_" + suffix, "2026-01-02T03:04:06.05Z"},
			}
			want := []string{legacy[3].id, legacy[1].id, legacy[0].id, legacy[2].id, legacy[4].id}
			for index, row := range legacy {
				insertLegacyDomainEvent(t, repo, paged, row.id, "issue.updated", "issue_tso", row.at, int64(index+1))
			}
			for _, limit := range []int{1, 2, 3, 4, 100} {
				got, cursor := []string{}, ""
				for pages := 0; pages < 10; pages++ {
					events, next, err := repo.EventsPage(ctx, paged, "issue_tso", cursor, limit)
					if err != nil {
						t.Fatal(err)
					}
					if len(events) > limit {
						t.Fatalf("limit %d: page of %d", limit, len(events))
					}
					for _, event := range events {
						got = append(got, event.ID)
					}
					if cursor = next; cursor == "" {
						break
					}
				}
				if !slices.Equal(got, want) {
					t.Errorf("EventsPage limit %d = %v, want %v", limit, got, want)
				}
			}

			// The latest deletion request is the one scheduled last, not the
			// one whose text sorts last.
			insertLegacyDomainEvent(t, repo, deleted, "evt_tso_del0_"+suffix, "workspace.deletion_scheduled", deleted, "2026-01-02T03:04:05Z", int64(1))
			insertLegacyDomainEvent(t, repo, deleted, "evt_tso_del1_"+suffix, "workspace.deletion_scheduled", deleted, "2026-01-02T03:04:05.1Z", int64(2))
			insertLegacyDomainEvent(t, repo, deleted, "evt_tso_del2_"+suffix, "workspace.deletion_scheduled", deleted, "2026-01-02T03:04:05.12Z", int64(3))
			if ref := repo.deletionRequestRef(ctx, deleted); ref != "evt_tso_del2_"+suffix {
				t.Errorf("deletion request = %q, want the latest", ref)
			}

			// Events written before the change log are numbered in time order.
			for _, row := range legacy {
				insertLegacyDomainEvent(t, repo, numbered, row.id+"_n", "issue.updated", "issue_tso", row.at, nil)
			}
			if err := repo.backfillChangeLog(ctx); err != nil {
				t.Fatal(err)
			}
			rows, err := repo.db.QueryContext(ctx, `SELECT id FROM domain_events WHERE workspace_id=? ORDER BY seq`, numbered)
			if err != nil {
				t.Fatal(err)
			}
			bySeq := []string{}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				bySeq = append(bySeq, strings.TrimSuffix(id, "_n"))
			}
			rows.Close()
			if !slices.Equal(bySeq, want) {
				t.Errorf("backfilled seq order = %v, want %v", bySeq, want)
			}

			// New rows are stored fixed width, so their text sorts by time.
			written := repo.Bootstrap().Workspace.ID
			t.Cleanup(func() {
				_, _ = repo.db.ExecContext(context.Background(), `DELETE FROM domain_events WHERE id LIKE ?`, "evt_tso_w%_"+suffix)
			})
			base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			for index, at := range []time.Time{base.Add(120 * time.Millisecond), base.Add(100 * time.Millisecond), base} {
				writeTimedEvent(t, repo, written, "evt_tso_w"+string(rune('0'+index))+"_"+suffix, at)
			}
			rows, err = repo.db.QueryContext(ctx, `SELECT created_at FROM domain_events WHERE workspace_id=? AND id LIKE ? ORDER BY created_at`, written, "evt_tso_w%_"+suffix)
			if err != nil {
				t.Fatal(err)
			}
			stored := []string{}
			for rows.Next() {
				var at string
				if err := rows.Scan(&at); err != nil {
					t.Fatal(err)
				}
				stored = append(stored, at)
			}
			rows.Close()
			wantStored := []string{"2026-01-02T03:04:05.000000000Z", "2026-01-02T03:04:05.100000000Z", "2026-01-02T03:04:05.120000000Z"}
			if !slices.Equal(stored, wantStored) {
				t.Errorf("stored times = %v, want %v", stored, wantStored)
			}
		})
	}
}

// auth_email_events has no sequence: sign-in events export newest first by
// time, and new rows are stored fixed width.
func TestAccountEmailEventTimesOrderChronologically(t *testing.T) {
	for name, repo := range eventTimeOrderStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			if err := repo.ensureAccountEmailEvents(ctx); err != nil {
				t.Fatal(err)
			}
			userID := repo.Bootstrap().Viewer.ID
			kind := "tso_" + strings.ToLower(idgen.New(""))
			t.Cleanup(func() {
				_, _ = repo.db.ExecContext(context.Background(), `DELETE FROM auth_email_events WHERE user_id=? AND kind LIKE ?`, userID, kind+"%")
			})
			for _, row := range []struct{ key, at string }{
				{"b", "2026-01-02T03:04:05.12Z"},
				{"a", "2026-01-02T03:04:05.1Z"},
				{"d", "2026-01-02T03:04:05Z"},
			} {
				if _, err := repo.db.ExecContext(ctx, `INSERT INTO auth_email_events(user_id,kind,item_key,created_at) VALUES(?,?,?,?)`, userID, kind+"_"+row.key, "", row.at); err != nil {
					t.Fatal(err)
				}
			}
			events, err := repo.AccountSignInEvents(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, event := range events {
				if strings.HasPrefix(event.Kind, kind) {
					got = append(got, strings.TrimPrefix(event.Kind, kind+"_"))
				}
			}
			if want := []string{"b", "a", "d"}; !slices.Equal(got, want) {
				t.Errorf("sign-in events newest first = %v, want %v", got, want)
			}

			if _, err := repo.RecordAccountEmailEvent(ctx, userID, kind+"_new", "", time.Date(2026, 1, 2, 3, 4, 5, 100_000_000, time.UTC)); err != nil {
				t.Fatal(err)
			}
			var stored string
			if err := repo.db.QueryRowContext(ctx, `SELECT created_at FROM auth_email_events WHERE user_id=? AND kind=?`, userID, kind+"_new").Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != "2026-01-02T03:04:05.100000000Z" {
				t.Errorf("stored time = %q, want fixed width", stored)
			}

			// The export's cap falls inside a second: the newest of that
			// second is kept, not the one whose text sorts first.
			capped := "usr_tso_" + strings.ToLower(idgen.New(""))
			if _, err := repo.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,password_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, capped, capped+"@example.test", "Capped", "Capped", "", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = repo.db.ExecContext(context.Background(), `DELETE FROM auth_email_events WHERE user_id=?`, capped)
				_, _ = repo.db.ExecContext(context.Background(), `DELETE FROM auth_users WHERE id=?`, capped)
			})
			newer := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
			for index := range accountSignInEventLimit - 1 {
				if _, err := repo.RecordAccountEmailEvent(ctx, capped, "newer", strconv.Itoa(index), newer.Add(time.Duration(index)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			for _, row := range []struct{ key, at string }{{"b", "2026-01-02T03:04:05.12Z"}, {"a", "2026-01-02T03:04:05.1Z"}, {"d", "2026-01-02T03:04:05Z"}} {
				if _, err := repo.db.ExecContext(ctx, `INSERT INTO auth_email_events(user_id,kind,item_key,created_at) VALUES(?,?,?,?)`, capped, "boundary_"+row.key, "", row.at); err != nil {
					t.Fatal(err)
				}
			}
			events, err = repo.AccountSignInEvents(ctx, capped)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != accountSignInEventLimit || events[0].Kind != "newer" || events[len(events)-1].Kind != "boundary_b" {
				t.Errorf("capped export = %d events, last %+v; want %d ending with boundary_b", len(events), events[len(events)-1], accountSignInEventLimit)
			}
		})
	}
}
