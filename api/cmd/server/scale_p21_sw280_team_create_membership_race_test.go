package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// A team creation commits the team, then adds its creator as owner in a
// separate membership transaction. On SQLite that transaction reads before
// it writes; when another connection holds the write lock at its first
// write, SQLite refuses the upgrade at once (SQLITE_BUSY, a lost write race:
// ErrConcurrentWrite). The creation used to answer that as 409 although the
// team was committed, and the client's retry then found the key taken (400).
// The race is injected deterministically: another connection takes the
// write lock after the membership transaction's reads and gives it back when
// the transaction runs again.

type scaleP21SW280Race struct {
	t        *testing.T
	database *sql.DB
	conn     *sql.Conn
	writes   int
}

// hook runs after the membership transaction's reads, before its first
// write: the first time it takes the write lock, the second time it gives
// it back.
func (race *scaleP21SW280Race) hook() {
	race.writes++
	switch race.writes {
	case 1:
		conn, err := race.database.Conn(context.Background())
		if err != nil {
			race.t.Error(err)
			return
		}
		if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
			_ = conn.Close()
			race.t.Error(err)
			return
		}
		race.conn = conn
	case 2:
		race.release()
	}
}

func (race *scaleP21SW280Race) release() {
	if race.conn == nil {
		return
	}
	if _, err := race.conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
		race.t.Error(err)
	}
	if err := race.conn.Close(); err != nil {
		race.t.Error(err)
	}
	race.conn = nil
}

func scaleP21SW280Differential(t *testing.T, cases []scaleP21TeamCreateCase) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	if f.reader.config.Driver != "sqlite" {
		t.Skip("the injected lost write race is SQLite's refused read-to-write upgrade")
	}
	database, err := sql.Open("sqlite", f.reader.config.Path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	database.SetMaxOpenConns(1)
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(test.setup)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				race := &scaleP21SW280Race{t: t, database: database}
				defer race.release()
				g := f
				g.wrap = func(ctx context.Context) context.Context {
					return store.WithTeamMembershipWriteHookForTesting(ctx, race.hook)
				}
				w := g.create(service, f.data.Viewer, test.body, test.development, false)
				if w.Code != http.StatusCreated {
					t.Fatalf("committed creation answered %d %s", w.Code, w.Body.String())
				}
				if race.writes != 2 {
					t.Fatalf("membership transaction ran %d times, want the lost race and its rerun", race.writes)
				}
				return w
			})
			for index, got := range outcomes {
				source := []string{"legacy", "warm", "reloaded"}[index]
				if got.Code != http.StatusCreated {
					t.Fatalf("%s: committed creation answered %d %s", source, got.Code, got.Body)
				}
				var team domain.Team
				if err := json.Unmarshal(got.Body, &team); err != nil || team.ID == "" || team.Key != test.body["key"] {
					t.Fatalf("%s: response is not the created team: %s", source, got.Body)
				}
				// The creator's owner membership (and any ancestor
				// membership it brings) is written and audited once, by
				// the rerun.
				owners := 0
				for _, row := range got.State.Memberships {
					if fmt.Sprint(row[0]) == team.ID && fmt.Sprint(row[1]) == f.data.Viewer.ID && fmt.Sprint(row[2]) == "owner" {
						owners++
					}
				}
				audited := map[string]int{}
				for _, row := range got.State.Audit {
					var metadata struct {
						TeamID string `json:"teamId"`
						UserID string `json:"userId"`
					}
					if json.Unmarshal([]byte(fmt.Sprint(row[9])), &metadata) == nil && metadata.TeamID != "" && metadata.UserID == f.data.Viewer.ID {
						audited[metadata.TeamID]++
					}
				}
				if owners != 1 || audited[team.ID] != 1 {
					t.Fatalf("%s: creator owner memberships %d, audit entries %v", source, owners, audited)
				}
				for teamID, count := range audited {
					if count != 1 {
						t.Fatalf("%s: team %s membership audited %d times", source, teamID, count)
					}
				}
				if test.check != nil {
					test.check(t, got)
				}
			}
		})
	}
}

func TestScaleP21SW280TeamCreateMembershipRace(t *testing.T) {
	scaleP21SW280Differential(t, []scaleP21TeamCreateCase{
		{name: "top-level", body: map[string]any{"name": "Race", "key": "RCE"}},
		{name: "copy", body: map[string]any{"name": "Race copy", "key": "RCC", "copyFromTeamId": "tm-parent"}},
		{name: "private", body: map[string]any{"name": "Race private", "key": "RCP", "private": true}},
	})
}

// The rerun also adds the creator to the new sub-team's ancestors it is not
// on yet, each once (the checks above), as the inherited audit entries show.
func TestScaleP21SW281TeamCreateSubTeamMembershipRace(t *testing.T) {
	scaleP21SW280Differential(t, []scaleP21TeamCreateCase{
		{name: "sub-team", body: map[string]any{"name": "Race sub", "key": "RCS", "parentTeamId": "tm-sub"}, check: func(t *testing.T, got scaleP21TeamCreateOutcome) {
			ancestors := map[string]bool{}
			for _, row := range got.State.Audit {
				if text := fmt.Sprint(row[9]); strings.Contains(text, `"inherited":true`) {
					var metadata struct {
						TeamID string `json:"teamId"`
					}
					_ = json.Unmarshal([]byte(text), &metadata)
					ancestors[metadata.TeamID] = true
				}
			}
			if !ancestors["tm-sub"] || !ancestors["tm-parent"] {
				t.Fatalf("inherited ancestor memberships audited for %v, want tm-sub and tm-parent", ancestors)
			}
		}},
	})
}
