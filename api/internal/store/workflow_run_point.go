package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

// errWorkflowRunFullImage: the point attempt cannot reproduce this run
// exactly. It rolls back and the full-image attempt runs it under the same
// write lock.
var errWorkflowRunFullImage = errors.New("workflow run needs the full-image writer")

var workflowRunFullImageAttempts atomic.Int64

// WorkflowRunFullImageAttemptsForTesting returns how many workflow-run
// attempts the point writer handed to the full-image writer.
func WorkflowRunFullImageAttemptsForTesting() int64 {
	return workflowRunFullImageAttempts.Load()
}

// workflowRunPointAttempt runs the literal callback on the installed metadata
// image plus the one issue the run names, read from SQL, and writes point
// records for exactly what the callback changed: that issue, the issues it
// creates, its notifications, the IssueCount of the labels it adds, the
// definition, the run history, the account row and the event. Its Go work
// and lock hold follow the definition, the run history and the touched
// records, not the workspace's issues and content. Two SQL aggregates over
// the issue rows remain (the largest Number and the row count, for the full
// writer's sequence and total repair; no issue reaches Go), and an added label
// also projects the projects' and initiatives' label IDs for its count.
//
// It refuses (errWorkflowRunFullImage), before any write:
//   - an installed image that still carries inline issue or content records;
//   - a workflow ID that is also the ID of a stored issue or content record
//     (the event's entity is the first object with that ID in field order,
//     and issues and content come before the definitions);
//   - a createIssue run whose Number fold overflows;
//   - a resource issue whose stored row is larger than one point read admits
//     (errIssueMutationRowTooLarge): the full image streams it as framed
//     records, as the writer did before the point attempt;
//
// and, after the callback, a created issue whose ID is already stored.
//
// The caller holds the write lock.
func (s *SQLiteStore) workflowRunPointAttempt(ctx context.Context, w *workflowRunWrite) error {
	workspace, workflowID := w.workspace, w.workflowID
	unbind, err := s.bindMetadataTimestampInputs(ctx)
	if err != nil {
		return err
	}
	defer unbind()
	ctx = withIssueRecordWriteAdmission(ctx, s)
	attempt := &workflowRunAttempt{owner: &ResourceRead{store: s}}
	transferred := false
	defer func() {
		if !transferred {
			attempt.Close()
		}
	}()
	if err := attempt.owner.reserve(ctx, 256<<10); err != nil {
		return err
	}
	stored, err := s.retainMetadataLogicalSource(ctx, workspace)
	if err != nil {
		return err
	}
	defer stored.Release()
	cloned, err := cloneMetadataLogicalSnapshot(ctx, stored)
	if err != nil {
		return err
	}
	defer cloned.Release()
	for _, name := range []string{"issues", "comments", "activities", "notifications", "notificationDeliveries"} {
		index, _, _ := metadataLogicalField(name)
		missing, err := cloned.fields[index].isNil(ctx)
		if err != nil {
			return err
		}
		if !missing {
			return errWorkflowRunFullImage
		}
	}
	tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.metadataResidency.requireCoverage {
		if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
			return err
		}
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return err
	}
	if stamp.workspaceID != stored.Stamp().workspaceID {
		return ErrResourceAccessChanged
	}
	if collides, err := workflowRunRecordIDExists(ctx, tx, workspace, workflowID); err != nil || collides {
		if err == nil {
			err = errWorkflowRunFullImage
		}
		return err
	}
	// The resource issue, as the full image hydrates it, beside the
	// installed metadata. Content stays in SQL: no action reads it.
	rows := s.newIssueMutationRows(tx, workspace)
	rows.rawOnly = true
	defer rows.Close()
	var preimage []byte
	var resource *domain.Issue
	selected := domain.Bootstrap{
		Issues: []domain.Issue{}, Comments: map[string][]domain.Comment{}, Activities: map[string][]domain.ActivityEvent{},
		Notifications: []domain.Notification{}, NotificationDeliveries: []domain.NotificationDelivery{},
	}
	if w.resourceType == "issue" && w.resourceID != "" {
		records, err := rows.load(ctx, []string{w.resourceID}, true)
		if errors.Is(err, errIssueMutationRowTooLarge) {
			err = errWorkflowRunFullImage
		}
		if err != nil {
			return err
		}
		if record, found := records[w.resourceID]; found {
			preimage = record.raw
			selected.Issues, err = decodeIssueSuggestionPreimages(ctx, attempt.owner, []string{w.resourceID},
				map[string][]byte{w.resourceID: record.raw}, nil)
			if err != nil {
				return err
			}
			if len(selected.Issues) == 1 {
				value := selected.Issues[0]
				resource = &value
			}
		}
	}
	source, err := boundedDiscussionEventSource(ctx, cloned, selected)
	if err != nil {
		return err
	}
	defer source.Release()
	selected = domain.Bootstrap{}
	display, err := s.refreshLegacyDisplayImage(ctx, source)
	if err != nil {
		return err
	}
	defer display.Release()
	var actor *domain.User
	if value, found := actorFromContext(ctx); found {
		actor = &value
	}
	prepared, err := s.prepareLegacyActorImage(ctx, display, actor)
	if err != nil {
		return err
	}
	defer prepared.Close()
	traceMark(ctx, "image")
	// The stored issues' largest Number, folded once in SQL: the createIssue
	// fold and the full writer's sequence repair both use it.
	var fold struct {
		largest       int
		found, loaded bool
	}
	largestStored := func(ctx context.Context) (int, bool, error) {
		if !fold.loaded {
			largest, found, err := s.workflowRunLargestIssueNumber(ctx, tx, workspace)
			if err != nil {
				return 0, false, err
			}
			fold.largest, fold.found, fold.loaded = largest, found, true
		}
		return fold.largest, fold.found, nil
	}
	numbers := func(ctx context.Context) (int, bool, error) {
		return largestStored(ctx)
	}
	attempt.inputs, err = s.loadWorkflowRunInputs(ctx, prepared.Source(), workflowID, w.resourceType, w.resourceID, numbers)
	if err != nil {
		return err
	}
	data := &attempt.inputs.data
	// The lookups read through this transaction: a second connection could
	// wait on the pool this transaction holds.
	attempt.lookups, err = newIssueEditorLookups(ctx, attempt.inputs.issueHotInputs, tx, workspace, data.Workspace.ID, domain.Issue{})
	if err != nil {
		return err
	}
	attempt.lookups.install(data)
	traceMark(ctx, "inputs")
	if err := attempt.admitCallback(ctx); err != nil {
		return err
	}
	if !registerMetadataOutputClose(ctx, attempt.Close) {
		return ErrBoundedMetadataCoverage
	}
	transferred = true
	if err := attempt.runCallback(ctx, w); err != nil {
		return err
	}
	inputs := attempt.inputs
	created := len(data.Issues) - len(inputs.before.Issues)
	if created < 0 {
		return ErrBoundedMetadataCoverage
	}
	createdIssues := data.Issues[:created]
	if created > 0 {
		ids := make([]string, 0, created)
		for _, issue := range createdIssues {
			if slices.Contains(ids, issue.ID) {
				return errWorkflowRunFullImage
			}
			ids = append(ids, issue.ID)
		}
		existing, err := rows.load(ctx, ids, true)
		if errors.Is(err, errIssueMutationRowTooLarge) {
			err = errWorkflowRunFullImage
		}
		if err != nil {
			return err
		}
		if len(existing) != 0 {
			return errWorkflowRunFullImage
		}
	}
	patched := inputs.resourceOrdinal >= 0 && !reflect.DeepEqual(inputs.before.Issues[0], data.Issues[created])
	var after *domain.Issue
	if patched {
		after = &data.Issues[created]
	}
	labels, err := s.workflowRunLabelCounts(ctx, tx, workspace, inputs, resource, after)
	if err != nil {
		return err
	}
	projects, err := s.workflowRunProjectCounts(ctx, tx, workspace, inputs, createdIssues)
	if err != nil {
		return err
	}
	counted, err := inputs.image(ctx, labels, projects)
	if err != nil {
		return err
	}
	defer counted.Release()
	traceMark(ctx, "callback-image")
	payloadRaw, previous, err := s.workflowRunEvent(ctx, w, source, counted)
	if err != nil {
		return err
	}
	// Records, in the full writer's family order: notifications, then the
	// issues (created ones first, as they lead the collection).
	writeCtx := context.WithValue(ctx, legacyContentWriteBudgetKey{}, s)
	for _, notification := range data.Notifications {
		resource := notification.IssueID
		if resource == "" {
			resource = notification.SourceID
		}
		if err := writeContentRecord(writeCtx, tx, workspace, "notification", resource, notification); err != nil {
			return err
		}
	}
	largest, found, err := largestStored(ctx)
	if err != nil {
		return err
	}
	if !found || largest < 0 {
		largest = 0
	}
	if created > 0 {
		if err := s.writeWorkflowRunCreatedIssues(ctx, tx, workspace, createdIssues); err != nil {
			return err
		}
		for _, issue := range createdIssues {
			largest = max(largest, issue.Number)
		}
	}
	if patched {
		issue, err := workflowRunPatchedIssue(ctx, attempt.owner, counted, created)
		if err != nil {
			return err
		}
		if _, err := s.writeIssueRecordBatch(ctx, tx, workspace, []domain.Issue{issue}, *data, map[string][]byte{issue.ID: preimage}); err != nil {
			return err
		}
	}
	// The full writer's issue bookkeeping after every run: the count
	// revision, collection total and sequence (it repairs a sequence below
	// the largest stored Number).
	if err := writeIssueStats(ctx, tx, workspace, nil); err != nil {
		return err
	}
	if err := writeWorkflowRunIssueTotals(ctx, tx, workspace, largest); err != nil {
		return err
	}
	traceMark(ctx, "records")
	return s.workflowRunCommit(ctx, w, attempt, tx, stored, stamp, counted, payloadRaw, previous)
}

// workflowRunRecordIDExists reports whether id is the ID of a stored issue or
// of a comment, activity, notification or delivery record.
func workflowRunRecordIDExists(ctx context.Context, tx *sqlTx, workspace, id string) (bool, error) {
	var issue, content int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id).Scan(&issue); err != nil {
		return false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_content_records WHERE workspace_key=? AND kind IN ('activity','comment','delivery','notification') AND id=?`, workspace, id).Scan(&content); err != nil {
		return false, err
	}
	return issue+content != 0, nil
}

// workflowRunLargestIssueNumber folds the stored issues' Number in SQL: the
// largest one, and whether any issue has one. One aggregate over the issue
// rows; no issue reaches Go.
func (s *SQLiteStore) workflowRunLargestIssueNumber(ctx context.Context, tx *sqlTx, workspace string) (int, bool, error) {
	expression := `CAST(json_extract(data,'$.number') AS INTEGER)`
	switch s.dialect {
	case "mysql":
		expression = `CAST(JSON_EXTRACT(CONVERT(data USING utf8mb4),'$.number') AS SIGNED)`
	case "postgres":
		expression = `CAST(flow_utf8(data)::jsonb->>'number' AS BIGINT)`
	}
	var largest sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(`+expression+`) FROM issue_records WHERE workspace_key=?`, workspace).Scan(&largest); err != nil {
		return 0, false, err
	}
	return int(largest.Int64), largest.Valid, nil
}

// workflowRunLabelCounts returns every label's derived IssueCount by
// ordinal, as the full writer's recount computes it after the callback: the
// active issues carrying the label (SQL label index, plus the labels the
// callback added to an active resource issue), the active projects and the
// initiatives listing it. Issues the callback creates carry no labels. Like
// the recount, it also rewrites a stored label row whose count or shape the
// installed image has since derived. Project counts are not recounted: no
// action changes an issue's project.
func (s *SQLiteStore) workflowRunLabelCounts(ctx context.Context, tx *sqlTx, workspace string, inputs *workflowRunInputs, resource, after *domain.Issue) (map[int]int, error) {
	index, _, _ := metadataLogicalField("labels")
	empty, err := inputs.source.fields[index].isNil(ctx)
	if err != nil || empty {
		return nil, err
	}
	totals := map[string]int{}
	rows, err := tx.QueryContext(ctx, `SELECT l.label_id,COUNT(*) FROM issue_label_records l JOIN issue_records i ON i.workspace_key=l.workspace_key AND i.id=l.issue_id WHERE l.workspace_key=? AND i.archived=0 GROUP BY l.label_id`, workspace)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			rows.Close()
			return nil, err
		}
		if err := inputs.reserve(ctx, uint64(len(id))*2+64); err != nil {
			rows.Close()
			return nil, err
		}
		totals[id] = count
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if after != nil && resource != nil && resource.ArchivedAt == nil {
		known := map[string]bool{}
		for _, label := range inputs.before.Issues[0].Labels {
			known[label.ID] = true
		}
		for _, label := range after.Labels {
			if label.ID != "" && !known[label.ID] {
				known[label.ID] = true
				totals[label.ID]++
			}
		}
	}
	err = inputs.source.VisitFields(ctx, func(name string, collection metadataLogicalNode) error {
		if name != "projects" && name != "initiatives" {
			return nil
		}
		return collection.Children(ctx, func(_ reflect.Value, item metadataLogicalNode) error {
			work := &ResourceRead{store: s}
			defer work.Close()
			var ids []string
			if name == "projects" {
				value, err := work.projectNode(ctx, item, "ArchivedAt", "LabelIDs")
				if err != nil {
					return err
				}
				project := value.Interface().(domain.Project)
				if project.ArchivedAt != nil {
					return nil
				}
				ids = project.LabelIDs
			} else {
				value, err := work.projectNode(ctx, item, "LabelIDs")
				if err != nil {
					return err
				}
				ids = value.Interface().(domain.Initiative).LabelIDs
			}
			seen := map[string]bool{}
			for _, id := range ids {
				if id != "" && !seen[id] {
					seen[id] = true
					if _, counted := totals[id]; !counted {
						if err := inputs.reserve(ctx, uint64(len(id))*2+64); err != nil {
							return err
						}
					}
					totals[id]++
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	counts := map[int]int{}
	err = inputs.source.fields[index].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		work := &ResourceRead{store: s}
		defer work.Close()
		value, err := work.projectNode(ctx, node, "ID")
		if err != nil {
			return err
		}
		counts[int(ordinal.Int())] = totals[value.Interface().(domain.IssueLabel).ID]
		return nil
	})
	return counts, err
}

// workflowRunProjectCounts returns every project's derived IssueCount by
// ordinal, as the full writer's recount computes it: the active issues naming
// the project (one SQL GROUP BY over the stored issue rows, plus the active
// issues the callback creates). No action changes an existing issue's project
// or archive state. Like the recount, it covers every stored project, so a row
// whose stored count has drifted is rewritten and the others stay untouched.
func (s *SQLiteStore) workflowRunProjectCounts(ctx context.Context, tx *sqlTx, workspace string, inputs *workflowRunInputs, created []domain.Issue) (map[int]int, error) {
	index, _, _ := metadataLogicalField("projects")
	empty, err := inputs.source.fields[index].isNil(ctx)
	if err != nil || empty {
		return nil, err
	}
	totals := map[string]int{}
	rows, err := tx.QueryContext(ctx, `SELECT project_id,COUNT(*) FROM issue_records WHERE workspace_key=? AND archived=0 AND project_id<>'' GROUP BY project_id`, workspace)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			rows.Close()
			return nil, err
		}
		if err := inputs.reserve(ctx, uint64(len(id))*2+64); err != nil {
			rows.Close()
			return nil, err
		}
		totals[id] = count
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, issue := range created {
		if issue.ArchivedAt == nil && issue.Project != nil && issue.Project.ID != "" {
			totals[issue.Project.ID]++
		}
	}
	counts := map[int]int{}
	err = inputs.source.fields[index].Children(ctx, func(ordinal reflect.Value, node metadataLogicalNode) error {
		work := &ResourceRead{store: s}
		defer work.Close()
		value, err := work.projectNode(ctx, node, "ID")
		if err != nil {
			return err
		}
		counts[int(ordinal.Int())] = totals[value.Interface().(domain.Project).ID]
		return nil
	})
	return counts, err
}

// workflowRunPatchedIssue decodes the resource issue's postimage: the
// displayed issue with the callback's changes.
func workflowRunPatchedIssue(ctx context.Context, owner *ResourceRead, image *metadataLogicalSource, ordinal int) (domain.Issue, error) {
	index, _, _ := metadataLogicalField("issues")
	var issue domain.Issue
	found := false
	err := image.fields[index].Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if int(key.Int()) != ordinal {
			return nil
		}
		if node.cost > (metadataWorkBytes-(64<<10))/4 {
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, node.cost*4+64<<10); err != nil {
			return err
		}
		value, err := node.Value(ctx, metadataWorkBytes)
		if err != nil {
			return err
		}
		issue, found = value.Interface().(domain.Issue), true
		return ErrMetadataIterationDone
	})
	if err != nil && !errors.Is(err, ErrMetadataIterationDone) {
		return domain.Issue{}, err
	}
	if !found {
		return domain.Issue{}, fmt.Errorf("%w: workflow resource issue missing from its postimage", ErrBoundedMetadataCoverage)
	}
	return issue, nil
}

// writeWorkflowRunCreatedIssues inserts the issues a run created ahead of the
// collection, where the full writer orders them: just before the first stored
// issue, or from zero in an empty collection.
func (s *SQLiteStore) writeWorkflowRunCreatedIssues(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue) error {
	var first sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT collection_order FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id LIMIT 1`, workspace).Scan(&first)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	orders := make(map[string]int, len(issues))
	deltas := map[issueStatsKey]int64{}
	for at, issue := range issues {
		orders[issue.ID] = at
		if first.Valid {
			orders[issue.ID] = int(first.Int64) - len(issues) + at
		}
		addIssueStats(deltas, issueStatsOf(issue), 1)
	}
	if err := writeIssueStats(ctx, tx, workspace, deltas); err != nil {
		return err
	}
	return s.importIssueRecordBatch(ctx, tx, workspace, issues, orders)
}

// writeWorkflowRunIssueTotals repeats the full writer's collection total and
// sequence after an issue write.
func writeWorkflowRunIssueTotals(ctx context.Context, tx *sqlTx, workspace string, largest int) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO issue_records_migrations(workspace_key,migrated_at) VALUES(?,?) ON CONFLICT DO NOTHING`, workspace, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_records WHERE workspace_key=?`, workspace).Scan(&total); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO issue_collection_counts(workspace_key,total) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET total=excluded.total`, workspace, total); err != nil {
		return err
	}
	return writeIssueNumber(ctx, tx, workspace, largest)
}
