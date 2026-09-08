package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"flow/api/internal/domain"
)

type issueStatsDimensions struct {
	Team, Project, Assignee, State, Type string
	Priority, Archived                   int
}
type issueStatsKey struct {
	Scope, ID, State, Type string
	Priority, Archived     int
}

func (s *SQLiteStore) ensureIssueStats(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS issue_scope_counts(workspace_key VARCHAR(191) NOT NULL,scope_type VARCHAR(24) NOT NULL,scope_id VARCHAR(191) NOT NULL,state_id VARCHAR(191) NOT NULL,state_type VARCHAR(32) NOT NULL,priority INTEGER NOT NULL,archived INTEGER NOT NULL,total BIGINT NOT NULL,PRIMARY KEY(workspace_key,scope_type,scope_id,state_id,state_type,priority,archived))`,
		`CREATE TABLE IF NOT EXISTS issue_stats_migrations(workspace_key VARCHAR(191) PRIMARY KEY)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) migrateIssueStats(ctx context.Context) error {
	for key := range s.workspaces {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_stats_migrations WHERE workspace_key=?`, key).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		err = func() error {
			if _, err := tx.ExecContext(ctx, `DELETE FROM issue_scope_counts WHERE workspace_key=?`, key); err != nil {
				return err
			}
			for _, scope := range []struct{ name, column string }{{"workspace", "''"}, {"team", "team_id"}, {"project", "project_id"}, {"assignee", "assignee_id"}} {
				groupColumns := "workspace_key,state_id,state_type,priority,archived"
				if scope.column != "''" {
					groupColumns += "," + scope.column
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO issue_scope_counts(workspace_key,scope_type,scope_id,state_id,state_type,priority,archived,total) SELECT workspace_key,?,`+scope.column+`,state_id,state_type,priority,archived,COUNT(*) FROM issue_records WHERE workspace_key=? GROUP BY `+groupColumns, scope.name, key); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO issue_stats_migrations(workspace_key) VALUES(?) ON CONFLICT DO NOTHING`, key)
			return err
		}()
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func readIssueStats(ctx context.Context, tx *sqlTx, workspace, id string) (*issueStatsDimensions, error) {
	var d issueStatsDimensions
	err := tx.QueryRowContext(ctx, `SELECT team_id,project_id,assignee_id,state_id,state_type,priority,archived FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&d.Team, &d.Project, &d.Assignee, &d.State, &d.Type, &d.Priority, &d.Archived)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &d, err
}

func issueStatsOf(issue domain.Issue) *issueStatsDimensions {
	d := &issueStatsDimensions{Team: issue.Team.ID, State: issue.State.ID, Type: issue.State.Type, Priority: issue.Priority}
	if issue.Project != nil {
		d.Project = issue.Project.ID
	}
	if issue.Assignee != nil {
		d.Assignee = issue.Assignee.ID
	}
	if issue.ArchivedAt != nil {
		d.Archived = 1
	}
	return d
}

func addIssueStats(deltas map[issueStatsKey]int64, d *issueStatsDimensions, delta int64) {
	if d == nil {
		return
	}
	for _, scope := range []struct{ name, id string }{{"workspace", ""}, {"team", d.Team}, {"project", d.Project}, {"assignee", d.Assignee}} {
		deltas[issueStatsKey{scope.name, scope.id, d.State, d.Type, d.Priority, d.Archived}] += delta
	}
}

func writeIssueStats(ctx context.Context, tx *sqlTx, workspace string, deltas map[issueStatsKey]int64) error {
	type entry struct {
		key   issueStatsKey
		order string
	}
	keys := []entry{}
	for key, delta := range deltas {
		if delta != 0 {
			keys = append(keys, entry{key, fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%d", key.Scope, key.ID, key.State, key.Type, key.Priority, key.Archived)})
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].order < keys[j].order })
	for start := 0; start < len(keys); start += 250 {
		var tuples []string
		var args []any
		for _, entry := range keys[start:min(start+250, len(keys))] {
			key := entry.key
			tuples = append(tuples, "(?,?,?,?,?,?,?,?)")
			args = append(args, workspace, key.Scope, key.ID, key.State, key.Type, key.Priority, key.Archived, deltas[key])
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO issue_scope_counts(workspace_key,scope_type,scope_id,state_id,state_type,priority,archived,total) VALUES `+strings.Join(tuples, ",")+` ON CONFLICT(workspace_key,scope_type,scope_id,state_id,state_type,priority,archived) DO UPDATE SET total=issue_scope_counts.total+excluded.total`, args...); err != nil {
			return err
		}
	}
	return nil
}

// Use small transactional counters only for queries that exactly match their
// dimensions. Arbitrary filters and inherited-share scopes retain SQL aggregation.
func (s *SQLiteStore) issueGroupsFromStats(ctx context.Context, q IssueRecordQuery) ([]IssueRecordGroup, bool, error) {
	if q.Access != nil && !q.Access.Admin || q.Text != "" || len(q.StateIDs) > 0 || q.AllowedTeamIDs != nil {
		return nil, false, nil
	}
	if q.GroupBy != "status" && q.GroupBy != "priority" && q.GroupBy != "none" {
		return nil, false, nil
	}
	var clauses []string
	var args []any
	var addFilter func(IssueFilter) bool
	addFilter = func(f IssueFilter) bool {
		if len(f.Or) > 0 {
			return false
		}
		for _, child := range f.And {
			if !addFilter(child) {
				return false
			}
		}
		if f.Field == "" {
			return true
		}
		if f.Field != "status" && f.Field != "statusType" && f.Field != "priority" {
			return false
		}
		remaining := 100
		condition, values, err := compileIssueFilter(f, 0, &remaining)
		if err != nil {
			return false
		}
		clauses = append(clauses, condition)
		args = append(args, values...)
		return true
	}
	if !addFilter(q.Filter) {
		return nil, false, nil
	}
	scope, ids := "workspace", []string{""}
	if len(q.TeamIDs) > 0 {
		scope, ids = "team", q.TeamIDs
	}
	if len(q.ProjectIDs) > 0 {
		if scope != "workspace" {
			return nil, false, nil
		}
		scope, ids = "project", q.ProjectIDs
	}
	base, baseArgs := bindList("i.scope_id", ids)
	where := `i.workspace_key=? AND i.scope_type=? AND ` + base + ` AND i.total>0`
	values := append([]any{q.Workspace, scope}, baseArgs...)
	if q.Archived != "all" {
		archived := 0
		if q.Archived == "true" {
			archived = 1
		}
		where += " AND i.archived=?"
		values = append(values, archived)
	}
	if len(clauses) > 0 {
		where += " AND " + strings.Join(clauses, " AND ")
		values = append(values, args...)
	}
	column := "state_id"
	if q.GroupBy == "priority" {
		column = "priority"
	}
	if q.GroupBy == "none" {
		column = "'all'"
	}
	selectColumn := "i." + column
	if q.GroupBy == "none" {
		selectColumn = column
	}
	statement := fmt.Sprintf("SELECT %s,SUM(i.total) FROM issue_scope_counts i WHERE %s GROUP BY %s ORDER BY %s", selectColumn, where, selectColumn, selectColumn)
	if q.GroupBy == "none" {
		statement = "SELECT 'all',COALESCE(SUM(i.total),0) FROM issue_scope_counts i WHERE " + where
	}
	rows, err := s.db.QueryContext(ctx, statement, values...)
	if err != nil {
		return nil, true, err
	}
	defer rows.Close()
	groups := []IssueRecordGroup{}
	for rows.Next() {
		var group IssueRecordGroup
		if err := rows.Scan(&group.Value, &group.Count); err != nil {
			return nil, true, err
		}
		if group.Count > 0 {
			groups = append(groups, group)
		}
	}
	return groups, true, rows.Err()
}
