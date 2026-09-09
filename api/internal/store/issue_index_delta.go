package store

import (
	"context"
	"encoding/json"
	"strings"

	"flow/api/internal/domain"
)

// Index changes are set differences, not delete-and-reinsert operations. A
// title edit must not generate row events for every label and subscriber.
func syncIssueIndexes(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue) error {
	if err := writeIssueSearchIndex(ctx, tx, workspace, issues); err != nil {
		return err
	}
	if err := writeAttachmentIndex(ctx, tx, workspace, issues); err != nil {
		return err
	}
	specs := []struct {
		table      string
		columns    []string
		keyColumns int
		rows       [][]string
	}{
		{"issue_label_records", []string{"issue_id", "label_id"}, 2, nil},
		{"issue_subscriber_records", []string{"issue_id", "user_id"}, 2, nil},
		{"issue_permission_records", []string{"issue_id", "subject_type", "subject_id", "role"}, 3, nil},
	}
	for _, issue := range issues {
		for _, label := range issue.Labels {
			specs[0].rows = append(specs[0].rows, []string{issue.ID, label.ID})
		}
		for _, id := range issue.SubscriberIDs {
			specs[1].rows = append(specs[1].rows, []string{issue.ID, id})
		}
		for _, p := range issue.Permissions {
			if p.Role != "" && !strings.EqualFold(p.Role, "none") {
				specs[2].rows = append(specs[2].rows, []string{issue.ID, p.SubjectType, p.SubjectID, p.Role})
			}
		}
	}
	ids := make([]string, len(issues))
	for i, issue := range issues {
		ids[i] = issue.ID
	}
	for _, spec := range specs {
		if err := syncIssueIndexRows(ctx, tx, workspace, spec.table, spec.columns, spec.keyColumns, ids, spec.rows); err != nil {
			return err
		}
	}
	return writeIssueAttributes(ctx, tx, workspace, issues)
}

func syncIssueIndexRows(ctx context.Context, tx *sqlTx, workspace, table string, columns []string, keyColumns int, ids []string, desired [][]string) error {
	if len(ids) == 0 {
		return nil
	}
	key := func(row []string) string { raw, _ := json.Marshal(row[:keyColumns]); return string(raw) }
	previous := map[string][]string{}
	for start := 0; start < len(ids); start += 250 {
		where, args := bindList("issue_id", ids[start:min(start+250, len(ids))])
		rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+table+" WHERE workspace_key=? AND "+where, append([]any{workspace}, args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			row := make([]string, len(columns))
			dest := make([]any, len(columns))
			for i := range row {
				dest[i] = &row[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return err
			}
			previous[key(row)] = row
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	wanted := map[string][]string{}
	for _, row := range desired {
		wanted[key(row)] = row
	}
	changed := [][]string{}
	for k, row := range wanted {
		old, exists := previous[k]
		same := exists
		for i := keyColumns; same && i < len(columns); i++ {
			same = old[i] == row[i]
		}
		if !same {
			changed = append(changed, row)
		}
		delete(previous, k)
	}
	predicates := make([]string, keyColumns)
	for i, c := range columns[:keyColumns] {
		predicates[i] = c + "=?"
	}
	for _, row := range previous {
		args := []any{workspace}
		for _, v := range row[:keyColumns] {
			args = append(args, v)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE workspace_key=? AND "+strings.Join(predicates, " AND "), args...); err != nil {
			return err
		}
	}
	conflict := " ON CONFLICT DO NOTHING"
	if keyColumns < len(columns) {
		updates := []string{}
		for _, c := range columns[keyColumns:] {
			updates = append(updates, c+"=excluded."+c)
		}
		conflict = " ON CONFLICT(workspace_key," + strings.Join(columns[:keyColumns], ",") + ") DO UPDATE SET " + strings.Join(updates, ",")
	}
	for start := 0; start < len(changed); start += 200 {
		tuples := []string{}
		args := []any{}
		for _, row := range changed[start:min(start+200, len(changed))] {
			tuples = append(tuples, "(?"+strings.Repeat(",?", len(columns))+")")
			args = append(args, workspace)
			for _, v := range row {
				args = append(args, v)
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+"(workspace_key,"+strings.Join(columns, ",")+") VALUES "+strings.Join(tuples, ",")+conflict, args...); err != nil {
			return err
		}
	}
	return nil
}
