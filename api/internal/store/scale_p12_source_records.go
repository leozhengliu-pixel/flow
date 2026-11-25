package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"flow/api/internal/domain"
)

// A project reference needs four display fields. Reading its full body here
// would make a small issue event scale with an unrelated project's history.
func (s *SQLiteStore) realtimeReferenceRows(ctx context.Context, workspace string, ids []string) (issueReferences, error) {
	refs := newIssueReferences(domain.Bootstrap{})
	keys := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			keys = append(keys, id)
			seen[id] = true
		}
	}
	if len(keys) > eventSourceMaxReferences {
		return refs, ErrEventSourceBudget
	}
	function := "JSON_OBJECT"
	if s.dialect == "postgres" {
		function = "JSON_BUILD_OBJECT"
	}
	fields := []string{}
	for _, name := range []string{"id", "name", "icon", "color"} {
		fields = append(fields, "'"+name+"',"+s.jsonText("data", name))
	}
	project := function + "(" + strings.Join(fields, ",") + ")"
	if s.dialect == "postgres" {
		project = "CONVERT_TO(CAST(" + project + " AS TEXT),'UTF8')"
	}
	value := "CASE WHEN field='projects' THEN " + project + " ELSE data END"
	used := 0
	for start := 0; start < len(keys); start += 200 {
		where, args := bindList("record_key", keys[start:min(start+200, len(keys))])
		rows, err := s.db.QueryContext(ctx, `SELECT field,CASE WHEN LENGTH(`+value+`)<=16384 THEN `+value+` ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('teams','states','users','labels','projects') AND `+where, append([]any{workspace}, args...)...)
		if err != nil {
			return refs, err
		}
		for rows.Next() {
			var field string
			var raw []byte
			if err := rows.Scan(&field, &raw); err != nil {
				rows.Close()
				return refs, err
			}
			used += len(raw)*3 + 256
			if raw == nil || used > 1<<20 {
				rows.Close()
				return refs, ErrEventSourceBudget
			}
			switch field {
			case "teams":
				var item domain.Team
				err = json.Unmarshal(raw, &item)
				refs.teams[item.ID] = item
			case "states":
				var item domain.WorkflowState
				err = json.Unmarshal(raw, &item)
				refs.states[item.ID] = item
			case "users":
				var item domain.User
				err = json.Unmarshal(raw, &item)
				refs.users[item.ID] = item
			case "labels":
				var item domain.IssueLabel
				err = json.Unmarshal(raw, &item)
				refs.labels[item.ID] = item
			case "projects":
				var item domain.ProjectSummary
				err = json.Unmarshal(raw, &item)
				refs.projects[item.ID] = item
			}
			if err != nil {
				rows.Close()
				return refs, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return refs, err
		}
	}
	return refs, nil
}

func (s *SQLiteStore) resolveRealtimeIssueReferences(ctx context.Context, workspace string, issue domain.Issue) (domain.Issue, error) {
	ids := []string{issue.Team.ID, issue.State.ID, issue.Creator.ID}
	if issue.Assignee != nil {
		ids = append(ids, issue.Assignee.ID)
	}
	if issue.Delegate != nil {
		ids = append(ids, issue.Delegate.ID)
	}
	if issue.Project != nil {
		ids = append(ids, issue.Project.ID)
	}
	for _, label := range issue.Labels {
		ids = append(ids, label.ID)
	}
	refs, err := s.realtimeReferenceRows(ctx, workspace, ids)
	if err != nil {
		return issue, err
	}
	return refs.resolve(issue), nil
}

// ReadRealtimeDocumentComments transfers a complete bounded comment set.
// Overflow never returns a partial set that could replace a client's list.
func (s *SQLiteStore) ReadRealtimeDocumentComments(ctx context.Context, workspace, id string) ([]domain.Comment, int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT CASE WHEN SUM(LENGTH(data)+256) OVER (ORDER BY created_at,id)<=262144 THEN data ELSE NULL END FROM workspace_content_records WHERE workspace_key=? AND kind='comment' AND resource_id=? ORDER BY created_at,id LIMIT 4097`, workspace, id)
	if err != nil {
		return nil, 0, err
	}
	comments := []domain.Comment{}
	ids := []string{}
	used := 0
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if raw == nil || len(comments) >= 4096 {
			rows.Close()
			return nil, 0, ErrEventSourceBudget
		}
		var item domain.Comment
		if err := json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			return nil, 0, err
		}
		comments = append(comments, item)
		ids = append(ids, item.User.ID)
		used += len(raw)*3 + 256
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	refs, err := s.realtimeReferenceRows(ctx, workspace, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range comments {
		if user, ok := refs.users[comments[i].User.ID]; ok {
			comments[i].User = user
		}
	}
	return comments, used, nil
}

func (s *SQLiteStore) ReadRealtimeProjectTeams(ctx context.Context, workspace string, ids []string) (map[string][]string, int, error) {
	result := map[string][]string{}
	keys := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			keys = append(keys, id)
			seen[id] = true
		}
	}
	if len(keys) > eventSourceMaxReferences {
		return nil, 0, ErrEventSourceBudget
	}
	used := 0
	for start := 0; start < len(keys); start += 200 {
		where, args := bindList("record_key", keys[start:min(start+200, len(keys))])
		value := s.jsonText("data", "teamIds")
		rows, err := s.db.QueryContext(ctx, `SELECT record_key,LENGTH(`+value+`),CASE WHEN LENGTH(`+value+`)<=32768 THEN `+value+` ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND `+where, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var id string
			var raw []byte
			var size sql.NullInt64
			if err := rows.Scan(&id, &size, &raw); err != nil {
				rows.Close()
				return nil, 0, err
			}
			if size.Valid && size.Int64 > 32768 {
				rows.Close()
				return nil, 0, ErrEventSourceBudget
			}
			used += len(raw)*3 + len(id) + 128
			if used > 1<<20 {
				rows.Close()
				return nil, 0, ErrEventSourceBudget
			}
			var teams []string
			if raw != nil {
				if err := json.Unmarshal(raw, &teams); err != nil {
					rows.Close()
					return nil, 0, err
				}
			}
			result[id] = teams
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, 0, err
		}
	}
	return result, used, nil
}

func (s *SQLiteStore) RealtimeMetadataVersion(ctx context.Context, workspace string, key RecordKey) (string, error) {
	if recordJournalMode() == "on" && s.db.journalReady.Load() {
		version, deleted, err := s.RecordVersion(ctx, workspace, key)
		if deleted {
			version = ""
		}
		return version, err
	}
	var version string
	err := s.db.QueryRowContext(ctx, `SELECT version FROM metadata_catalog WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.Kind, key.ID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return version, err
}
