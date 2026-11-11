package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// SLA metadata is separate from the issue record. Bind a compact relation once
// so every cursor comparison reads the same current deadlines as its page.
func (s *SQLiteStore) issueDueSort(ctx context.Context, query IssueRecordQuery, prefix string, args []any) (string, []any, string) {
	_, data, _ := s.workspaceReadSource(ctx, query.Workspace)
	settings, _ := data.Settings["sla"].(map[string]any)
	enabled, configured := settings["enabled"].(bool)
	if !configured {
		enabled = true
	}
	type deadline struct {
		IssueID string `json:"issue_id"`
		DueAt   string `json:"due_at"`
	}
	deadlines := map[string]string{}
	if enabled {
		for _, sla := range data.IssueSLAs {
			if sla.Status != "removed" {
				deadlines[sla.IssueID] = sla.DueAt.UTC().Format("2006-01-02T15:04:05.000000000")
			}
		}
	}
	ids := make([]string, 0, len(deadlines))
	for id := range deadlines {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	rows := make([]deadline, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, deadline{IssueID: id, DueAt: deadlines[id]})
	}
	raw, _ := json.Marshal(rows)
	relation := "SELECT json_extract(value,'$.issue_id') AS issue_id,json_extract(value,'$.due_at') AS due_at FROM json_each(?)"
	switch s.dialect {
	case "postgres":
		relation = "SELECT issue_id,due_at FROM jsonb_to_recordset(CAST(? AS jsonb)) AS due_rows(issue_id text,due_at text)"
	case "mysql":
		relation = "SELECT issue_id,due_at FROM JSON_TABLE(?, '$[*]' COLUMNS(issue_id VARCHAR(191) PATH '$.issue_id',due_at VARCHAR(29) PATH '$.due_at')) AS due_rows"
	}
	prefix = withIssueCTE(prefix, "sla_due AS ("+relation+")")
	args = append(args, string(raw))
	date := "(SELECT a.value FROM issue_attribute_records a WHERE a.workspace_key=i.workspace_key AND a.issue_id=i.id AND a.field='dueDate')"
	expressions := []string{}
	if enabled {
		deadline := "COALESCE((SELECT d.due_at FROM sla_due d WHERE d.issue_id=i.id)," + s.canonicalDueSortTime(s.jsonText("i.data", "slaBreachesAt")) + ")"
		expressions = append(expressions, "CASE WHEN i.state_type='canceled' THEN NULL ELSE "+deadline+" END")
	}
	expressions = append(expressions, s.canonicalDueSortTime(date))
	sentinel := "'9999-12-31T23:59:59.999999999'"
	if query.Direction == "desc" {
		sentinel = "'0000-01-01T00:00:00.000000000'"
	}
	expressions = append(expressions, sentinel)
	return prefix, args, "COALESCE(" + strings.Join(expressions, ",") + ")"
}

func (s *SQLiteStore) canonicalDueSortTime(value string) string {
	concat := func(parts ...string) string {
		if s.dialect == "mysql" {
			return "CONCAT(" + strings.Join(parts, ",") + ")"
		}
		return "(" + strings.Join(parts, "||") + ")"
	}
	value = "NULLIF(" + value + ",'')"
	return "CASE WHEN LENGTH(" + value + ")=10 THEN " + concat(value, "'T00:00:00.000000000'") + " WHEN SUBSTR(" + value + ",20,1)='.' THEN SUBSTR(" + concat("REPLACE("+value+",'Z','')", "'000000000'") + ",1,29) ELSE " + concat("SUBSTR("+value+",1,19)", "'.000000000'") + " END"
}
