package store

import (
	"context"
	"database/sql"
	"strconv"
)

// issueEstimateSettingsChunk bounds the bound parameters of one team settings read.
const issueEstimateSettingsChunk = 200

// issueEstimateSettings is what estimate totals need of a team's settings
// (the web estimates module's estimatePoints and resolvedTeamSettings).
type issueEstimateSettings struct {
	estimateType     string
	allowZero        bool
	countUnestimated *bool
	inherit          bool
	parentTeamID     string
}

// usesEstimates matches the web's teamUsesEstimates.
func (s issueEstimateSettings) usesEstimates() bool {
	return s.estimateType != "" && s.estimateType != "notUsed"
}

// points matches the web's estimatePoints: an unestimated issue (no, a
// negative or an unparsable estimate, or 0 when the team does not allow zero
// estimates) counts as 1 point unless "Count unestimated issues" is off.
func (s issueEstimateSettings) points(estimate sql.NullString) float64 {
	value, err := strconv.ParseFloat(estimate.String, 64)
	if !estimate.Valid || err != nil || value < 0 || value == 0 && !s.allowZero {
		if s.countUnestimated != nil && !*s.countUnestimated {
			return 0
		}
		return 1
	}
	return value
}

// QueryIssueGroupEstimatePoints is the estimate point total of each group of
// query's grouping, from one SQL aggregate over the same rows (and the same
// visibility) as the group counts, grouped by group value, team and estimate.
// Each issue counts with its team's estimate settings (a sub-team inheriting
// its parent's estimation uses the parent's); issues of teams that do not use
// estimates count nothing, and a group without any issue of a team that uses
// them has no total. A "labels" or customer grouping counts an issue in each
// of its groups. Nested queries count every issue in its own group.
func (s *SQLiteStore) QueryIssueGroupEstimatePoints(ctx context.Context, query IssueRecordQuery) (map[string]float64, error) {
	query = query.withoutGroupValue()
	query.Cursor, query.Nested = "", false
	if err := s.applyIssueRecordScope(ctx, &query); err != nil {
		return nil, err
	}
	query = withoutRedundantIssueAccess(query)
	statement, args, err := s.issueGroupEstimateStatement(query)
	if err != nil {
		return nil, err
	}
	type bucket struct {
		value, team string
		estimate    sql.NullString
		count       int64
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	buckets := []bucket{}
	teams := map[string]bool{}
	for rows.Next() {
		var item bucket
		var value, team sql.NullString
		if err := rows.Scan(&value, &team, &item.estimate, &item.count); err != nil {
			rows.Close()
			return nil, err
		}
		item.value, item.team = value.String, team.String
		if query.GroupBy == "cases" {
			var ok bool
			if item.value, ok = issueGroupCaseValue(query.GroupCases, item.value); !ok {
				rows.Close()
				return nil, ErrIssueQuery
			}
		}
		buckets = append(buckets, item)
		teams[item.team] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	settings, err := s.issueEstimateTeamSettings(ctx, query.Workspace, teams)
	if err != nil {
		return nil, err
	}
	resolved := make(map[string]issueEstimateSettings, len(teams))
	for team := range teams {
		if value, ok := resolveIssueEstimateSettings(settings, team, map[string]bool{}); ok && value.usesEstimates() {
			resolved[team] = value
		}
	}
	totals := map[string]float64{}
	for _, item := range buckets {
		team, ok := resolved[item.team]
		if !ok {
			continue
		}
		totals[item.value] += float64(item.count) * team.points(item.estimate)
	}
	return totals, nil
}

// issueGroupEstimateStatement is the (group value, team, estimate, issue
// count) aggregate of query's grouping. Group values come from the same
// expressions as the group counts (flatIssueGroupsBySQL); a "labels" or
// customer grouping adds its "No label" / "No customer" rows in the same
// statement.
func (s *SQLiteStore) issueGroupEstimateStatement(query IssueRecordQuery) (string, []any, error) {
	where, args, err := issueRecordWhere(query)
	if err != nil {
		return "", nil, err
	}
	if query.SearchText != "" {
		selection, values := s.issueTextSelection(query.Workspace, query.SearchText)
		where += " AND i.id IN (" + selection + ")"
		args = append(args, values...)
	}
	prefix, prefixArgs := issueAccessCTE(query)
	const estimate = " LEFT JOIN issue_attribute_records est ON est.workspace_key=i.workspace_key AND est.issue_id=i.id AND est.field='estimate'"
	part := func(key, joins string, joinArgs []any, extra string, extraArgs []any) (string, []any) {
		statement := "SELECT " + key + " AS group_key,i.team_id AS team_id,est.value AS estimate,i.id AS issue_id FROM issue_records i" + joins + estimate + " WHERE " + where + extra
		return statement, append(append(append([]any{}, joinArgs...), args...), extraArgs...)
	}
	var inner string
	var innerArgs []any
	switch query.GroupBy {
	case "none":
		inner, innerArgs = part("'all'", "", nil, "", nil)
	case "cases":
		expression, expressionArgs, err := issueGroupCaseExpression(query.GroupCases)
		if err != nil {
			return "", nil, err
		}
		inner, innerArgs = part(expression, "", nil, "", nil)
		innerArgs = append(expressionArgs, innerArgs...)
	case "labels":
		if len(query.GroupLabelIDs) > MaxIssueGroupLabels {
			return "", nil, ErrIssueQuery
		}
		join := " JOIN issue_label_records l ON l.workspace_key=i.workspace_key AND l.issue_id=i.id"
		var joinArgs []any
		if len(query.GroupLabelIDs) > 0 {
			clause, values := bindList("l.label_id", query.GroupLabelIDs)
			join += " AND " + clause
			joinArgs = values
		}
		labeled, labeledArgs := part("l.label_id", join, joinArgs, "", nil)
		none, noneArgs := labelGroupClause("", query.GroupLabelIDs)
		unlabeled, unlabeledArgs := part("''", "", nil, " AND "+none, noneArgs)
		inner, innerArgs = labeled+" UNION ALL "+unlabeled, append(labeledArgs, unlabeledArgs...)
	case "customer":
		join := " JOIN customer_request_filter_records r ON r.workspace_key=i.workspace_key AND r.issue_id=i.id LEFT JOIN customer_filter_records c ON c.workspace_key=r.workspace_key AND c.customer_id=r.customer_id"
		requested, requestedArgs := part("COALESCE(c.customer_id,'"+UnknownCustomerGroup+"')", join, nil, "", nil)
		none, noneArgs := customerGroupClause("")
		unrequested, unrequestedArgs := part("''", "", nil, " AND "+none, noneArgs)
		inner, innerArgs = requested+" UNION ALL "+unrequested, append(requestedArgs, unrequestedArgs...)
	default:
		column, err := issueGroupColumn(query.GroupBy)
		joins := ""
		if attribute := issueGroupAttribute(query.GroupBy); attribute != "" {
			// The field comes from the fixed allowlist, never from input SQL.
			joins = " LEFT JOIN issue_attribute_records g ON g.workspace_key=i.workspace_key AND g.issue_id=i.id AND g.field='" + attribute + "'"
			column = "COALESCE(g.value,'')"
		} else if err != nil {
			return "", nil, err
		} else {
			column = "i." + column
		}
		inner, innerArgs = part(column, joins, nil, "", nil)
	}
	// An issue is in a label's or customer's group once, however many rows
	// the join gives it.
	count := "COUNT(*)"
	if query.GroupBy == "labels" || query.GroupBy == "customer" {
		count = "COUNT(DISTINCT grouped.issue_id)"
	}
	statement := prefix + "SELECT grouped.group_key,grouped.team_id,grouped.estimate," + count + " FROM (" + inner + ") grouped GROUP BY grouped.group_key,grouped.team_id,grouped.estimate"
	return statement, append(append([]any{}, prefixArgs...), innerArgs...), nil
}

// issueEstimateTeamSettings reads the estimate settings of teams and of the
// parents they inherit estimation from, from the team settings records (as
// the label group metadata reads do), a chunk of teams per statement.
func (s *SQLiteStore) issueEstimateTeamSettings(ctx context.Context, workspace string, teams map[string]bool) (map[string]issueEstimateSettings, error) {
	result := map[string]issueEstimateSettings{}
	requested := map[string]bool{}
	pending := make([]string, 0, len(teams))
	for team := range teams {
		if team != "" {
			pending = append(pending, team)
			requested[team] = true
		}
	}
	flag := func(value sql.NullString) bool { return value.String == "1" || value.String == "true" }
	for len(pending) > 0 {
		next := []string{}
		for start := 0; start < len(pending); start += issueEstimateSettingsChunk {
			where, args := bindList("record_key", pending[start:min(start+issueEstimateSettingsChunk, len(pending))])
			rows, err := s.db.QueryContext(ctx, "SELECT record_key,"+s.jsonText("data", "estimateType")+","+s.jsonText("data", "estimateAllowZero")+","+s.jsonText("data", "estimateCountUnestimated")+","+s.jsonText("data", "inheritIssueEstimation")+","+s.jsonText("data", "parentTeamId")+" FROM workspace_metadata_records WHERE workspace_key=? AND field='teamSettings' AND "+where, append([]any{workspace}, args...)...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				var estimateType, allowZero, countUnestimated, inherit, parent sql.NullString
				if err := rows.Scan(&id, &estimateType, &allowZero, &countUnestimated, &inherit, &parent); err != nil {
					rows.Close()
					return nil, err
				}
				settings := issueEstimateSettings{estimateType: estimateType.String, allowZero: flag(allowZero), inherit: flag(inherit), parentTeamID: parent.String}
				if countUnestimated.Valid {
					value := flag(countUnestimated)
					settings.countUnestimated = &value
				}
				result[id] = settings
				if settings.inherit && settings.parentTeamID != "" && !requested[settings.parentTeamID] {
					requested[settings.parentTeamID] = true
					next = append(next, settings.parentTeamID)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
		pending = next
	}
	return result, nil
}

// resolveIssueEstimateSettings matches the web's resolvedTeamSettings: a team
// inheriting issue estimation takes its parent's resolved estimate settings.
func resolveIssueEstimateSettings(settings map[string]issueEstimateSettings, team string, seen map[string]bool) (issueEstimateSettings, bool) {
	current, ok := settings[team]
	if !ok || seen[team] {
		return current, ok
	}
	seen[team] = true
	if !current.inherit || current.parentTeamID == "" {
		return current, true
	}
	if parent, ok := resolveIssueEstimateSettings(settings, current.parentTeamID, seen); ok {
		current.estimateType, current.allowZero, current.countUnestimated = parent.estimateType, parent.allowZero, parent.countUnestimated
	}
	return current, true
}
