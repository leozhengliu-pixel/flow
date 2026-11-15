package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

type scaleP21SW101Outcome struct {
	Role    string
	Teams   []string
	Applied bool
}

func scaleP21SW101Literal(ctx context.Context, repository *SQLiteStore, workspaceID, userID string) scaleP21SW101Outcome {
	role, status, err := repository.WorkspaceRole(ctx, workspaceID, userID)
	if err != nil || status != "active" {
		return scaleP21SW101Outcome{}
	}
	return scaleP21SW101Outcome{Role: role, Teams: repository.actorTeamIDs(ctx, workspaceID, userID), Applied: true}
}

func scaleP21SW101Compare(t *testing.T, ctx context.Context, legacy, bounded *SQLiteStore, workspaceID, userID string) {
	t.Helper()
	want := scaleP21SW101Literal(ctx, legacy, workspaceID, userID)
	owner := &ResourceRead{store: bounded}
	role, teams, applied, err := bounded.boundedLegacyActorDirectory(ctx, workspaceID, userID, owner)
	got := scaleP21SW101Outcome{Role: role, Teams: teams, Applied: applied}
	if err != nil || !reflect.DeepEqual(got, want) {
		owner.Close()
		t.Fatalf("best-effort actor directory changed: got=%#v want=%#v err=%v", got, want, err)
	}
	if applied && bounded.MetadataWorkUsedForTesting() == 0 {
		owner.Close()
		t.Fatal("callback role/team values have no retained work owner")
	}
	owner.Close()
	owner.Close()
	if bounded.MetadataWorkUsedForTesting() != 0 || bounded.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("actor directory retained work or decoded workspace metadata")
	}
}

func TestScaleP21SW101LegacyActorDirectoryDatabase(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	bounded, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	workspaceID, userID := f.data.Workspace.ID, f.data.Viewer.ID
	for _, fixture := range []struct {
		name, role, status  string
		emptyTeams, missing bool
	}{
		{name: "active-admin", role: "admin", status: "active"},
		{name: "active-member", role: "member", status: "active"},
		{name: "active-guest", role: "guest", status: "active"},
		{name: "active-empty-team-slice", role: "member", status: "active", emptyTeams: true},
		{name: "inactive-membership", role: "member", status: "suspended"},
		{name: "missing-membership", missing: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if fixture.missing {
				_, err = tx.ExecContext(t.Context(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID)
			} else {
				_, err = tx.ExecContext(t.Context(), `UPDATE workspace_memberships SET role=?,status=? WHERE workspace_id=? AND user_id=?`, fixture.role, fixture.status, workspaceID, userID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if fixture.emptyTeams {
				if _, err := tx.ExecContext(t.Context(), `DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, userID); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: f.data.Workspace.URLKey, reader: tx})
			scaleP21SW101Compare(t, ctx, f.SQLiteStore, bounded, workspaceID, userID)
			var one int
			if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
				t.Fatal("actor directory consumed borrowed transaction", err)
			}
		})
	}
	for _, pair := range [][2]string{{workspaceID + "-foreign", userID}, {workspaceID, userID + "-missing"}, {"", userID}, {workspaceID, ""}} {
		scaleP21SW101Compare(t, t.Context(), f.SQLiteStore, bounded, pair[0], pair[1])
	}
	closed, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Rollback(); err != nil {
		t.Fatal(err)
	}
	failed := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, reader: closed})
	scaleP21SW101Compare(t, failed, f.SQLiteStore, bounded, workspaceID, userID)
}

type scaleP21SW101Script struct {
	role, status                  driver.Value
	roleError, teamError, teamEOF error
	missing                       bool
	teams                         []driver.Value
}

type scaleP21SW101Connector struct{ script scaleP21SW101Script }

func (c scaleP21SW101Connector) Driver() driver.Driver { return c }
func (c scaleP21SW101Connector) Open(string) (driver.Conn, error) {
	return c.Connect(context.Background())
}
func (c scaleP21SW101Connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &scaleP21SW101Connection{script: c.script}, nil
}

type scaleP21SW101Connection struct{ script scaleP21SW101Script }

func (*scaleP21SW101Connection) Close() error { return nil }
func (*scaleP21SW101Connection) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected actor-directory transaction")
}
func (*scaleP21SW101Connection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected actor-directory prepare")
}

func scaleP21SW101Length(value driver.Value) driver.Value {
	switch value := value.(type) {
	case nil:
		return nil
	case string:
		return int64(len(value))
	case []byte:
		return int64(len(value))
	default:
		return int64(len(fmt.Sprint(value)))
	}
}

func (c *scaleP21SW101Connection) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.Contains(query, "FROM workspace_memberships") {
		if c.script.roleError != nil {
			return nil, c.script.roleError
		}
		guarded := strings.Contains(query, "CASE WHEN")
		rows := &scaleP21SW101Rows{columns: []string{"role", "status"}}
		if guarded {
			rows.columns = []string{"size", "role", "status"}
		}
		if c.script.missing {
			return rows, nil
		}
		values := []driver.Value{c.script.role, c.script.status}
		if guarded {
			var size driver.Value
			left, right := scaleP21SW101Length(c.script.role), scaleP21SW101Length(c.script.status)
			if left != nil && right != nil {
				size = left.(int64) + right.(int64)
			}
			if size != nil && size.(int64) > MetadataRecordPageMaxBytes {
				values = []driver.Value{nil, nil}
			}
			values = append([]driver.Value{size}, values...)
		}
		rows.values = [][]driver.Value{values}
		return rows, nil
	}
	if strings.Contains(query, "FROM team_memberships") {
		if c.script.teamError != nil {
			return nil, c.script.teamError
		}
		guarded := strings.Contains(query, "CASE WHEN")
		rows := &scaleP21SW101Rows{columns: []string{"team_id"}, endError: c.script.teamEOF}
		if guarded {
			rows.columns = []string{"size", "team_id"}
		}
		for _, id := range c.script.teams {
			values := []driver.Value{id}
			if guarded {
				size := scaleP21SW101Length(id)
				if size != nil && size.(int64) > MetadataRecordPageMaxBytes {
					values = []driver.Value{nil}
				}
				values = append([]driver.Value{size}, values...)
			}
			rows.values = append(rows.values, values)
		}
		return rows, nil
	}
	return nil, fmt.Errorf("unexpected actor-directory query")
}

type scaleP21SW101Rows struct {
	columns  []string
	values   [][]driver.Value
	position int
	endError error
}

func (r *scaleP21SW101Rows) Columns() []string { return r.columns }
func (*scaleP21SW101Rows) Close() error        { return nil }
func (r *scaleP21SW101Rows) Next(target []driver.Value) error {
	if r.position >= len(r.values) {
		if r.endError != nil {
			return r.endError
		}
		return io.EOF
	}
	copy(target, r.values[r.position])
	r.position++
	return nil
}

func scaleP21SW101Stores(t *testing.T, script scaleP21SW101Script) (*SQLiteStore, *SQLiteStore) {
	t.Helper()
	database := sql.OpenDB(scaleP21SW101Connector{script: script})
	t.Cleanup(func() { _ = database.Close() })
	wrapped := &sqlDatabase{DB: database, dialect: "sqlite"}
	return &SQLiteStore{db: wrapped, dialect: "sqlite"}, &SQLiteStore{db: wrapped, dialect: "sqlite", metadataResidency: newMetadataResidency()}
}

func TestScaleP21SW101LegacyActorDirectoryBestEffort(t *testing.T) {
	failure := errors.New("ordinary membership driver failure")
	for _, fixture := range []struct {
		name   string
		script scaleP21SW101Script
	}{
		{"empty-teams", scaleP21SW101Script{role: "member", status: "active"}},
		{"ordered-duplicates-empty-null", scaleP21SW101Script{role: "guest", status: "active", teams: []driver.Value{"z", "a", "z", "", nil, []byte("bytes")}}},
		{"empty-role", scaleP21SW101Script{role: "", status: "active", teams: []driver.Value{"team"}}},
		{"native-role", scaleP21SW101Script{role: "管理员", status: "active", teams: []driver.Value{"私有", "public"}}},
		{"inactive", scaleP21SW101Script{role: "member", status: "inactive", teams: []driver.Value{"unused"}}},
		{"missing", scaleP21SW101Script{missing: true}},
		{"null-role-scan", scaleP21SW101Script{role: nil, status: "active"}},
		{"null-status-scan", scaleP21SW101Script{role: "member", status: nil}},
		{"role-query-error", scaleP21SW101Script{roleError: failure}},
		{"team-query-error", scaleP21SW101Script{role: "member", status: "active", teamError: failure}},
		{"team-eof-error", scaleP21SW101Script{role: "member", status: "active", teams: []driver.Value{"discard-partial"}, teamEOF: failure}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			legacy, bounded := scaleP21SW101Stores(t, fixture.script)
			scaleP21SW101Compare(t, t.Context(), legacy, bounded, "workspace", "actor")
		})
	}
}

func TestScaleP21SW101LegacyActorDirectoryCancellationAndAdmission(t *testing.T) {
	_, bounded := scaleP21SW101Stores(t, scaleP21SW101Script{role: "member", status: "active", teams: []driver.Value{"one", "two", "one"}})
	for _, stage := range []string{"before", "role-query", "team-query"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if stage == "before" {
				cancel()
			}
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if stage == "role-query" && strings.Contains(query, "FROM workspace_memberships") ||
					stage == "team-query" && strings.Contains(query, "FROM team_memberships") {
					cancel()
				}
			})
			owner := &ResourceRead{store: bounded}
			_, _, _, err := bounded.boundedLegacyActorDirectory(ctx, "workspace", "actor", owner)
			owner.Close()
			if !errors.Is(err, context.Canceled) || bounded.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("actor directory swallowed cancellation or retained work", stage, err)
			}
		})
	}
	limit := bounded.metadataResidency.work.limit
	t.Cleanup(func() { bounded.metadataResidency.work.limit = limit })
	for _, budget := range []int{1, 2 << 20, 2<<20 + 64<<10} {
		bounded.metadataResidency.work.limit = budget
		owner := &ResourceRead{store: bounded}
		_, _, _, err := bounded.boundedLegacyActorDirectory(t.Context(), "workspace", "actor", owner)
		owner.Close()
		if !errors.Is(err, ErrMetadataWorkBudget) || bounded.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("actor directory partial admission retained work", budget, err)
		}
	}
	bounded.metadataResidency.work.limit = limit
	parent, release, err := bounded.tryAdmitAdditionalMetadataWork(t.Context(), 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	retained := &ResourceRead{store: bounded}
	role, teams, applied, err := bounded.boundedLegacyActorDirectory(parent, "workspace", "actor", retained)
	release()
	if err != nil || !applied || role != "member" || !reflect.DeepEqual(teams, []string{"one", "two", "one"}) || bounded.MetadataWorkUsedForTesting() == 0 {
		retained.Close()
		t.Fatal("actor values borrowed the parent work allowance", err)
	}
	teams[0] = "caller-owned"
	independent := &ResourceRead{store: bounded}
	_, fresh, _, err := bounded.boundedLegacyActorDirectory(t.Context(), "workspace", "actor", independent)
	if err != nil || !reflect.DeepEqual(fresh, []string{"one", "two", "one"}) {
		retained.Close()
		independent.Close()
		t.Fatal("actor team slice aliases another lookup", err)
	}
	retained.Close()
	independent.Close()
	if bounded.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("independent actor outputs retained work")
	}
	_, tooLarge := scaleP21SW101Stores(t, scaleP21SW101Script{role: strings.Repeat("role", MetadataRecordPageMaxBytes/4+1), status: "active"})
	owner := &ResourceRead{store: tooLarge}
	_, _, _, err = tooLarge.boundedLegacyActorDirectory(t.Context(), "workspace", "actor", owner)
	owner.Close()
	if !errors.Is(err, ErrMetadataWorkBudget) || tooLarge.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("oversized role escaped actual-row admission", err)
	}
	_, tooLarge = scaleP21SW101Stores(t, scaleP21SW101Script{role: "member", status: "active", teams: []driver.Value{strings.Repeat("team", MetadataRecordPageMaxBytes/4+1)}})
	owner = &ResourceRead{store: tooLarge}
	_, _, _, err = tooLarge.boundedLegacyActorDirectory(t.Context(), "workspace", "actor", owner)
	owner.Close()
	if !errors.Is(err, ErrMetadataWorkBudget) || tooLarge.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("oversized team escaped actual-row admission", err)
	}
	closed := &ResourceRead{store: bounded}
	closed.Close()
	if _, _, _, err := bounded.boundedLegacyActorDirectory(t.Context(), "workspace", "actor", closed); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("closed actor output owner remained usable", err)
	}
	if _, _, _, err := bounded.boundedLegacyActorDirectory(t.Context(), "workspace", "actor", nil); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("actor directory accepted no output owner", err)
	}
	foreign := &ResourceRead{store: &SQLiteStore{metadataResidency: newMetadataResidency()}}
	if _, _, _, err := bounded.boundedLegacyActorDirectory(t.Context(), "workspace", "actor", foreign); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("actor directory accepted another store's output owner", err)
	}
	foreign.Close()
}
