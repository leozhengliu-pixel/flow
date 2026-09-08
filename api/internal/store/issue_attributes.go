package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Optional properties are sparse indexed records. An unset property consumes
// no row, and filters never need to decode the issue JSON in the database.
var issueAttributeFields = map[string]bool{
	"projectMilestoneId": true, "estimate": true, "dueDate": true,
	"completedAt": true, "canceledAt": true, "startedAt": true,
	"templateId": true, "externalSource": true, "delegateId": true,
	"firstLabel": true,
}

func issueAttributes(issue domain.Issue) map[string]string {
	values := map[string]string{}
	for field, value := range map[string]*string{"projectMilestoneId": issue.ProjectMilestoneID, "dueDate": issue.DueDate} {
		if value != nil && *value != "" {
			values[field] = *value
		}
	}
	for field, value := range map[string]*time.Time{"completedAt": issue.CompletedAt, "canceledAt": issue.CanceledAt, "startedAt": issue.StartedAt} {
		if value != nil {
			values[field] = value.UTC().Format(issueRecordTimestamp)
		}
	}
	if issue.Estimate != nil {
		values["estimate"] = strconv.FormatFloat(*issue.Estimate, 'f', -1, 64)
	}
	if issue.TemplateID != "" {
		values["templateId"] = issue.TemplateID
	}
	if issue.ExternalSource != "" {
		values["externalSource"] = issue.ExternalSource
	}
	if issue.Delegate != nil {
		values["delegateId"] = issue.Delegate.ID
	}
	if len(issue.Labels) > 0 {
		values["firstLabel"] = issue.Labels[0].ID
	}
	return values
}

func writeIssueAttributes(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue) error {
	if len(issues) == 0 {
		return nil
	}
	ids := make([]string, len(issues))
	for i, issue := range issues {
		ids[i] = issue.ID
	}
	where, args := bindList("issue_id", ids)
	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_attribute_records WHERE workspace_key=? AND `+where, append([]any{workspace}, args...)...); err != nil {
		return err
	}
	var tuples []string
	var values []any
	flush := func() error {
		if len(tuples) == 0 {
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO issue_attribute_records(workspace_key,issue_id,field,value) VALUES `+strings.Join(tuples, ","), values...)
		tuples, values = nil, nil
		return err
	}
	for _, issue := range issues {
		for field, value := range issueAttributes(issue) {
			tuples = append(tuples, "(?,?,?,?)")
			values = append(values, workspace, issue.ID, field, value)
			if len(tuples) == 200 {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	return flush()
}

func (s *SQLiteStore) migrateIssueAttributes(ctx context.Context) error {
	for workspace := range s.workspaces {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_attribute_migrations(workspace_key,last_id,complete) VALUES(?,'',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
		for {
			var last string
			var complete int
			if err := s.db.QueryRowContext(ctx, `SELECT last_id,complete FROM issue_attribute_migrations WHERE workspace_key=?`, workspace).Scan(&last, &complete); err != nil {
				return err
			}
			if complete == 1 {
				break
			}
			// The checkpoint and sparse rows commit together, so interrupted upgrades
			// resume without retaining a workspace-sized array or restarting the scan.
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			err = func() error {
				rows, err := tx.QueryContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? AND id>? ORDER BY id LIMIT 250`, workspace, last)
				if err != nil {
					return err
				}
				batch := []domain.Issue{}
				for rows.Next() {
					var raw []byte
					var issue domain.Issue
					if err := rows.Scan(&raw); err != nil {
						rows.Close()
						return err
					}
					if err := json.Unmarshal(raw, &issue); err != nil {
						rows.Close()
						return err
					}
					batch = append(batch, issue)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				if err := writeIssueAttributes(ctx, tx, workspace, batch); err != nil {
					return err
				}
				if len(batch) > 0 {
					last = batch[len(batch)-1].ID
				}
				if len(batch) < 250 {
					complete = 1
				}
				_, err = tx.ExecContext(ctx, `UPDATE issue_attribute_migrations SET last_id=?,complete=? WHERE workspace_key=?`, last, complete, workspace)
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
	}
	return nil
}

func compileIssueAttribute(node IssueFilter) (string, []any, error) {
	op := strings.ToLower(node.Operator)
	if op == "" {
		op = "is"
	}
	values := append([]string(nil), node.Values...)
	for i, value := range values {
		if value == "" {
			continue
		}
		if strings.HasSuffix(node.Field, "At") || node.Field == "dueDate" {
			date, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				date, err = time.Parse("2006-01-02", value)
			}
			if err != nil {
				return "", nil, ErrIssueQuery
			}
			values[i] = date.UTC().Format(issueRecordTimestamp)
			if node.Field == "dueDate" {
				values[i] = date.UTC().Format("2006-01-02")
			}
		}
	}
	prefix := `SELECT 1 FROM issue_attribute_records a WHERE a.workspace_key=i.workspace_key AND a.issue_id=i.id AND a.field=?`
	args := []any{node.Field}
	var condition string
	switch op {
	case "isempty", "isnotempty":
		condition = "NOT EXISTS (" + prefix + ")"
		if op == "isnotempty" {
			condition = "EXISTS (" + prefix + ")"
		}
	case "is", "in", "isnot", "notin":
		clause, bound := bindList("a.value", values)
		condition = "EXISTS (" + prefix + " AND " + clause + ")"
		args = append(args, bound...)
		for _, value := range values {
			if value == "" {
				condition = "(" + condition + " OR NOT EXISTS (" + prefix + "))"
				args = append(args, node.Field)
				break
			}
		}
		if op == "isnot" || op == "notin" {
			condition = "NOT (" + condition + ")"
		}
	case "before", "after", "gt", "gte", "lt", "lte":
		if len(values) != 1 || values[0] == "" {
			return "", nil, ErrIssueQuery
		}
		column := "a.value"
		var value any = values[0]
		if node.Field == "estimate" {
			number, err := strconv.ParseFloat(values[0], 64)
			if err != nil {
				return "", nil, ErrIssueQuery
			}
			column, value = "CAST(a.value AS DECIMAL(20,6))", number
		}
		operator := map[string]string{"before": "<", "after": ">", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
		condition = "EXISTS (" + prefix + " AND " + column + operator + "?)"
		args = append(args, value)
	default:
		return "", nil, fmt.Errorf("%w: unsupported attribute operator", ErrIssueQuery)
	}
	return condition, args, nil
}
