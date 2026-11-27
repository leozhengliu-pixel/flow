package store

import (
	"context"
	"database/sql"
)

// MaxIssueGroupLabels bounds the labels a "labels" grouping is limited to
// (one label group's labels).
const MaxIssueGroupLabels = 1000

// issueGroupLabelChunk bounds the bound parameters of one label metadata read.
const issueGroupLabelChunk = 500

// IssueGroupLabel is what ordering label groups needs of a label: its name,
// scope ("" or "workspace", else a team) and its label group's name.
type IssueGroupLabel struct {
	Name, Scope, GroupID, GroupName string
}

// LabelGroupIssueLabelIDs is the active issue labels of a label group (at
// most MaxIssueGroupLabels), read from the label metadata records.
func (s *SQLiteStore) LabelGroupIssueLabelIDs(ctx context.Context, workspace, groupID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field='labels' AND "+s.jsonText("data", "groupId")+"=? AND COALESCE("+s.jsonText("data", "resourceType")+",'issue')='issue' AND "+s.jsonText("data", "archivedAt")+" IS NULL ORDER BY record_key LIMIT ?", workspace, groupID, MaxIssueGroupLabels)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// IssueGroupLabels reads the labels of label group values (and their label
// groups' names) for ordering; unknown labels are left out.
func (s *SQLiteStore) IssueGroupLabels(ctx context.Context, workspace string, ids []string) (map[string]IssueGroupLabel, error) {
	labels := make(map[string]IssueGroupLabel, len(ids))
	groupNames := map[string]string{}
	read := func(field string, keys []string, scan func(key, name, scope, group string)) error {
		for start := 0; start < len(keys); start += issueGroupLabelChunk {
			clause, args := bindList("record_key", keys[start:min(start+issueGroupLabelChunk, len(keys))])
			rows, err := s.db.QueryContext(ctx, "SELECT record_key,COALESCE("+s.jsonText("data", "name")+",''),COALESCE("+s.jsonText("data", "scope")+",''),COALESCE("+s.jsonText("data", "groupId")+",'') FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND "+clause, append([]any{workspace, field}, args...)...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var key, name, scope, group string
				if err := rows.Scan(&key, &name, &scope, &group); err != nil {
					rows.Close()
					return err
				}
				scan(key, name, scope, group)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if err := rows.Err(); err != nil {
				return err
			}
		}
		return nil
	}
	groups := []string{}
	if err := read("labels", ids, func(key, name, scope, group string) {
		labels[key] = IssueGroupLabel{Name: name, Scope: scope, GroupID: group}
		if _, ok := groupNames[group]; group != "" && !ok {
			groupNames[group] = ""
			groups = append(groups, group)
		}
	}); err != nil {
		return nil, err
	}
	if err := read("labelGroups", groups, func(key, name, _, _ string) { groupNames[key] = name }); err != nil {
		return nil, err
	}
	for id, label := range labels {
		label.GroupName = groupNames[label.GroupID]
		labels[id] = label
	}
	return labels, nil
}

// issueLabelRelation is the label index of an issue row aliased i, limited
// to labelIDs when there are any.
func issueLabelRelation(issue string, labelIDs []string) (string, []any) {
	relation := "SELECT 1 FROM issue_label_records l WHERE l.workspace_key=i.workspace_key AND l.issue_id=" + issue
	if len(labelIDs) == 0 {
		return relation, nil
	}
	clause, values := bindList("l.label_id", labelIDs)
	return relation + " AND " + clause, values
}

// labelGroupClause scopes a page to one label group: "" is "No label" (none
// of the grouping's labels).
func labelGroupClause(value string, labelIDs []string) (string, []any) {
	relation, args := issueLabelRelation("i.id", labelIDs)
	if value == "" {
		return "NOT EXISTS (" + relation + ")", args
	}
	return "EXISTS (" + relation + " AND l.label_id=?)", append(args, value)
}

// labelIssueGroups counts issues per label through the label index (an
// issue counts in each of its labels' groups), plus the "No label" group.
func (s *SQLiteStore) labelIssueGroups(ctx context.Context, query IssueRecordQuery, where string, args []any) ([]IssueRecordGroup, error) {
	if len(query.GroupLabelIDs) > MaxIssueGroupLabels {
		return nil, ErrIssueQuery
	}
	prefix, prefixArgs := issueAccessCTE(query)
	join := "JOIN issue_label_records l ON l.workspace_key=i.workspace_key AND l.issue_id=i.id"
	var joinArgs []any
	if len(query.GroupLabelIDs) > 0 {
		clause, values := bindList("l.label_id", query.GroupLabelIDs)
		join += " AND " + clause
		joinArgs = values
	}
	values := append(append(append([]any{}, prefixArgs...), joinArgs...), args...)
	rows, err := s.db.QueryContext(ctx, prefix+"SELECT l.label_id,COUNT(DISTINCT i.id) FROM issue_records i "+join+" WHERE "+where+" GROUP BY l.label_id ORDER BY l.label_id", values...)
	if err != nil {
		return nil, err
	}
	groups, err := scanIssueGroupCounts(rows)
	if err != nil {
		return nil, err
	}
	none, noneArgs := labelGroupClause("", query.GroupLabelIDs)
	var count int64
	if err := s.db.QueryRowContext(ctx, prefix+"SELECT COUNT(*) FROM issue_records i WHERE "+where+" AND "+none, append(append(append([]any{}, prefixArgs...), args...), noneArgs...)...).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		groups = append(groups, IssueRecordGroup{Value: "", Count: count})
	}
	return groups, nil
}

func scanIssueGroupCounts(rows *sql.Rows) ([]IssueRecordGroup, error) {
	defer rows.Close()
	groups := []IssueRecordGroup{}
	for rows.Next() {
		var group IssueRecordGroup
		if err := rows.Scan(&group.Value, &group.Count); err != nil {
			return nil, err
		}
		if group.Count > 0 {
			groups = append(groups, group)
		}
	}
	return groups, rows.Err()
}
