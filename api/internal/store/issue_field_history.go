package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"flow/api/internal/domain"
)

const issueFieldHistoryKind = "issue_field_history"

// A compact change-history checkpoint: each PATCH field's last changed
// version, including writes to a parent or child as a side effect. It lives
// with the issue's content records, independently of webhook retention.
type issueFieldHistory struct {
	ID          string           `json:"id"`
	CreatedAt   time.Time        `json:"createdAt"`
	Version     int64            `json:"version"`
	BaseVersion int64            `json:"baseVersion"`
	Fields      map[string]int64 `json:"fields"`
}

func issueCASValues(issue domain.Issue) map[string]any {
	ids := func(values []string) []string {
		result := append([]string{}, values...)
		slices.Sort(result)
		return slices.Compact(result)
	}
	optional := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	labels := make([]string, 0, len(issue.Labels))
	for _, label := range issue.Labels {
		labels = append(labels, label.ID)
	}
	assignee, delegate, project := "", "", ""
	if issue.Assignee != nil {
		assignee = issue.Assignee.ID
	}
	if issue.Delegate != nil {
		delegate = issue.Delegate.ID
	}
	if issue.Project != nil {
		project = issue.Project.ID
	}
	return map[string]any{
		"title": issue.Title, "description": []any{issue.Description, issue.DescriptionState, issue.DocumentContent},
		"teamId": issue.Team.ID, "stateId": issue.State.ID, "priority": issue.Priority,
		"estimate": issue.Estimate, "assigneeId": assignee, "delegateId": delegate,
		"projectId": project, "projectMilestoneId": optional(issue.ProjectMilestoneID), "cycleId": optional(issue.CycleID),
		"dueDate": optional(issue.DueDate), "slaBreachesAt": issue.SLABreachesAt, "slaType": issue.SLAType,
		"labelIds": ids(labels), "subscriberIds": ids(issue.SubscriberIDs), "archived": issue.ArchivedAt != nil,
		"parentId": optional(issue.ParentID), "sortOrder": issue.SortOrder, "icon": issue.Icon,
		"recurrence": issue.Recurrence, "nextOccurrenceAt": issue.NextOccurrenceAt, "snoozedUntil": issue.SnoozedUntil,
	}
}

// Only PATCH's typed input opts into field CAS. Other versioned callbacks
// keep their whole-record precondition rather than guessing their intent.
func issueCASFields(payload any) ([]string, bool) {
	input, ok := payload.(domain.IssueUpdateInput)
	if !ok {
		return nil, false
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, false
	}
	names := map[string]bool{}
	if input.DescriptionData != nil || len(input.DocumentUpdateIDs) > 0 {
		names["description"] = true
	}
	for field := range fields {
		switch field {
		case "expectedVersion", "expectedDocumentVersion", "documentUpdateIds":
			continue
		case "descriptionState", "descriptionData", "contentState":
			field = "description"
		case "assigneeId":
			// An application assignee is interpreted as a delegate.
			names["delegateId"] = true
		}
		names[field] = true
	}
	return sortedKeys(names), true
}

func checkIssueFieldVersion(ctx context.Context, tx *sqlTx, workspace string, issue domain.Issue, expected int64, payload any) error {
	if issue.Version == expected {
		return nil
	}
	fields, ok := issueCASFields(payload)
	if !ok || expected < 1 || expected > issue.Version {
		return ErrIssueVersion
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=? AND id=?`, workspace, issueFieldHistoryKind, issue.ID, issue.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIssueVersion
	}
	if err != nil {
		return err
	}
	var history issueFieldHistory
	if err := json.Unmarshal(raw, &history); err != nil {
		return err
	}
	// Missing history (including a write by an older server during a
	// rolling upgrade) must fail closed, not assume the fields were untouched.
	if history.Version != issue.Version || expected < history.BaseVersion {
		return ErrIssueVersion
	}
	values := issueCASValues(issue)
	for _, field := range fields {
		if _, known := values[field]; !known {
			return ErrIssueVersion
		}
		if history.Fields[field] > expected {
			return ErrIssueVersion
		}
	}
	return nil
}

// All issue upsert paths call this before replacing their rows. The caller
// holds the issue locks; batch imports acquire them here too. Using previous
// values rather than request fields also captures automatic family writes.
func (s *SQLiteStore) recordIssueFieldHistory(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue, previous map[string][]byte) error {
	if len(issues) == 0 {
		return nil
	}
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	slices.Sort(ids)
	if previous == nil {
		records, err := s.loadIssueRecordsByID(ctx, tx, workspace, ids, true)
		if err != nil {
			return err
		}
		previous = make(map[string][]byte, len(records))
		for id, record := range records {
			previous[id] = record.raw
		}
	}
	histories, err := scopedContentChunked[issueFieldHistory](ctx, tx, workspace, issueFieldHistoryKind, ids)
	if err != nil {
		return err
	}
	rows := make([]contentRecordRow, 0, len(issues))
	for _, issue := range issues {
		normalizeIssueRecord(&issue)
		var before domain.Issue
		if raw := previous[issue.ID]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &before); err != nil {
				return err
			}
			normalizeIssueRecord(&before)
		}
		history := issueFieldHistory{ID: issue.ID, CreatedAt: issue.UpdatedAt, Version: issue.Version, BaseVersion: before.Version, Fields: map[string]int64{}}
		if before.ID == "" {
			history.BaseVersion = issue.Version
		}
		if stored := histories[issue.ID]; len(stored) == 1 && before.ID != "" && stored[0].Version == before.Version {
			history.BaseVersion, history.Fields = stored[0].BaseVersion, stored[0].Fields
			if history.Fields == nil {
				history.Fields = map[string]int64{}
			}
		}
		oldValues := issueCASValues(before)
		for field, value := range issueCASValues(issue) {
			if !reflect.DeepEqual(oldValues[field], value) {
				history.Fields[field] = issue.Version
				if issue.Version <= before.Version {
					// An unversioned rewrite leaves no historical boundary for
					// older clients; only the current version remains provable.
					history.BaseVersion = issue.Version
				}
			}
		}
		row, err := contentRecordRowFor(workspace, issueFieldHistoryKind, issue.ID, history)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	return writeContentRecordRows(ctx, tx, rows)
}
