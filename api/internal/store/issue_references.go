package store

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"flow/api/internal/domain"
)

// Display metadata has one owner. Refresh it on reads instead of rewriting
// every referencing issue when a member, team, status or label is renamed.
type issueReferences struct {
	teams    map[string]domain.Team
	states   map[string]domain.WorkflowState
	users    map[string]domain.User
	labels   map[string]domain.IssueLabel
	projects map[string]domain.ProjectSummary
}

func newIssueReferences(data domain.Bootstrap) issueReferences {
	r := issueReferences{map[string]domain.Team{}, map[string]domain.WorkflowState{}, map[string]domain.User{}, map[string]domain.IssueLabel{}, map[string]domain.ProjectSummary{}}
	for _, v := range data.Teams {
		r.teams[v.ID] = v
	}
	for _, v := range data.States {
		r.states[v.ID] = v
	}
	for _, v := range data.Users {
		r.users[v.ID] = v
	}
	for _, v := range data.Labels {
		r.labels[v.ID] = v
	}
	for _, v := range data.Projects {
		r.projects[v.ID] = domain.ProjectSummary{ID: v.ID, Name: v.Name, Icon: v.Icon, Color: v.Color}
	}
	return r
}

func (r issueReferences) resolve(issue domain.Issue) domain.Issue {
	if v, ok := r.teams[issue.Team.ID]; ok {
		issue.Team = v
	}
	if v, ok := r.states[issue.State.ID]; ok {
		issue.State = v
	}
	if v, ok := r.users[issue.Creator.ID]; ok {
		issue.Creator = v
	}
	if issue.Assignee != nil {
		if v, ok := r.users[issue.Assignee.ID]; ok {
			issue.Assignee = &v
		}
	}
	if issue.Delegate != nil {
		if v, ok := r.users[issue.Delegate.ID]; ok {
			issue.Delegate = &v
		}
	}
	if issue.Project != nil {
		if v, ok := r.projects[issue.Project.ID]; ok {
			issue.Project = &v
		}
	}
	issue.Labels = slices.Clone(issue.Labels)
	for i, v := range issue.Labels {
		if current, ok := r.labels[v.ID]; ok {
			issue.Labels[i] = current
		}
	}
	return issue
}

func (r issueReferences) equalOwned(previous []byte, next domain.Issue) bool {
	if len(previous) == 0 {
		return false
	}
	var old domain.Issue
	if json.Unmarshal(previous, &old) != nil {
		return false
	}
	// Status type is an indexed issue dimension and must still be updated.
	if old.State.Type != next.State.Type {
		return false
	}
	before, err := issueRecordValues("", r.resolve(old))
	if err != nil {
		return false
	}
	after, err := issueRecordValues("", r.resolve(next))
	if err != nil {
		return false
	}
	return bytes.Equal(before[len(before)-1].([]byte), after[len(after)-1].([]byte))
}

func (r issueReferences) equalDisplayData(previous, next []byte) bool {
	if bytes.Equal(previous, next) {
		return true
	}
	if len(previous) == 0 {
		return false
	}
	var canonical func([]byte) []byte
	canonical = func(raw []byte) []byte {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return raw
		}
		for _, field := range []string{"user", "actor", "lead", "creator", "owner"} {
			var user domain.User
			if json.Unmarshal(fields[field], &user) == nil {
				if current, ok := r.users[user.ID]; ok {
					fields[field], _ = json.Marshal(current)
				}
			}
		}
		if _, wrapped := fields["parent"]; wrapped && len(fields["item"]) > 0 {
			fields["item"] = canonical(fields["item"])
		}
		out, err := json.Marshal(fields)
		if err != nil {
			return raw
		}
		return out
	}
	return bytes.Equal(canonical(previous), canonical(next))
}

func refreshDisplayReferences(data *domain.Bootstrap) {
	r := newIssueReferences(*data)
	user := func(value domain.User) domain.User {
		if current, ok := r.users[value.ID]; ok {
			return current
		}
		return value
	}
	for i := range data.Members {
		data.Members[i].User = user(data.Members[i].User)
	}
	for i := range data.Projects {
		if data.Projects[i].Lead != nil {
			value := user(*data.Projects[i].Lead)
			data.Projects[i].Lead = &value
		}
	}
	for i := range data.Initiatives {
		data.Initiatives[i].Creator = user(data.Initiatives[i].Creator)
		if data.Initiatives[i].Owner != nil {
			value := user(*data.Initiatives[i].Owner)
			data.Initiatives[i].Owner = &value
		}
	}
	for key, items := range data.ProjectUpdates {
		for i := range items {
			items[i].User = user(items[i].User)
		}
		data.ProjectUpdates[key] = items
	}
	for key, items := range data.InitiativeUpdates {
		for i := range items {
			items[i].User = user(items[i].User)
		}
		data.InitiativeUpdates[key] = items
	}
	for key, items := range data.Comments {
		for i := range items {
			items[i].User = user(items[i].User)
		}
		data.Comments[key] = items
	}
	for key, items := range data.Activities {
		for i := range items {
			items[i].Actor = user(items[i].Actor)
		}
		data.Activities[key] = items
	}
	for i := range data.Notifications {
		data.Notifications[i].Actor = user(data.Notifications[i].Actor)
	}
}

func refreshIssueReferences(data *domain.Bootstrap) {
	refs := newIssueReferences(*data)
	for i, issue := range data.Issues {
		data.Issues[i] = refs.resolve(issue)
	}
}

func (s *SQLiteStore) resolveIssueReferences(ctx context.Context, workspace string, issues []domain.Issue) error {
	if len(issues) == 0 {
		return nil
	}
	ids := []string{}
	for _, issue := range issues {
		ids = append(ids, issue.Team.ID, issue.State.ID, issue.Creator.ID)
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
	}
	refs, err := s.readIssueReferences(ctx, workspace, ids)
	if err != nil {
		return err
	}
	for i, issue := range issues {
		issues[i] = refs.resolve(issue)
	}
	return nil
}

// Validators may call queries while holding the workspace mutation lock.
// Resolve references through bounded SQL reads rather than reacquiring that lock.
func (s *SQLiteStore) readIssueReferences(ctx context.Context, workspace string, ids []string) (issueReferences, error) {
	refs := newIssueReferences(domain.Bootstrap{})
	seen := map[string]bool{}
	unique := []string{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			unique = append(unique, id)
			seen[id] = true
		}
	}
	for start := 0; start < len(unique); start += 250 {
		where, args := bindList("record_key", unique[start:min(start+250, len(unique))])
		rows, err := s.db.QueryContext(ctx, "SELECT field,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('teams','states','users','labels','projects') AND "+where, append([]any{workspace}, args...)...)
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
			switch field {
			case "teams":
				var v domain.Team
				err = json.Unmarshal(raw, &v)
				refs.teams[v.ID] = v
			case "states":
				var v domain.WorkflowState
				err = json.Unmarshal(raw, &v)
				refs.states[v.ID] = v
			case "users":
				var v domain.User
				err = json.Unmarshal(raw, &v)
				refs.users[v.ID] = v
			case "labels":
				var v domain.IssueLabel
				err = json.Unmarshal(raw, &v)
				refs.labels[v.ID] = v
			case "projects":
				var v domain.ProjectSummary
				err = json.Unmarshal(raw, &v)
				refs.projects[v.ID] = v
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
