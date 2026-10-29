package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Releases live in workspace metadata, so issue filters over them ("Released
// date after 1 week ago", "Release pipeline is …", "No releases") would
// otherwise have to ship every member issue id to the query. Each release's
// membership is indexed here instead, one row per (release, issue), and kept
// in the same transaction as the release record.
func (s *SQLiteStore) ensureReleaseIssueIndex(ctx context.Context) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS release_issue_filter_records(workspace_key VARCHAR(191) NOT NULL,release_id VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,pipeline_id VARCHAR(191) NOT NULL DEFAULT '',stage VARCHAR(191) NOT NULL DEFAULT '',status VARCHAR(64) NOT NULL DEFAULT '',released_at VARCHAR(64) NOT NULL DEFAULT '',PRIMARY KEY(workspace_key,release_id,issue_id))`,
		`CREATE TABLE IF NOT EXISTS release_issue_filter_migrations(workspace_key VARCHAR(191) PRIMARY KEY,last_id VARCHAR(191) NOT NULL,complete INTEGER NOT NULL)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// Per-issue lookups back the correlated EXISTS filters; the date-first index
	// lets a planner start from a narrow released-at range instead.
	for name, columns := range map[string]string{"release_issue_issue_idx": "workspace_key,issue_id,released_at", "release_issue_released_idx": "workspace_key,released_at,issue_id"} {
		statement := "CREATE INDEX IF NOT EXISTS " + name + " ON release_issue_filter_records(" + columns + ")"
		if s.dialect == "mysql" {
			statement = strings.Replace(statement, "IF NOT EXISTS ", "", 1)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

// releaseIssueRow is the filterable part of one release, shared by its issues.
type releaseIssueRow struct {
	pipeline, stage, status, releasedAt string
}

func releaseIssueRowFor(release domain.Release) releaseIssueRow {
	row := releaseIssueRow{pipeline: release.PipelineID, stage: release.Stage, status: release.Status}
	if release.ReleasedAt != nil {
		row.releasedAt = release.ReleasedAt.UTC().Format(issueRecordTimestamp)
	}
	return row
}

// writeReleaseIssueRecords keeps one release's rows equal to its record (nil
// raw deletes them). Only changed rows are written: a release update that
// keeps its issues costs one guarded UPDATE.
func writeReleaseIssueRecords(ctx context.Context, tx *sqlTx, workspace, field, id string, raw []byte) error {
	if field != "releases" {
		return nil
	}
	queueIssueCountRevision(ctx, tx, workspace)
	if raw == nil {
		_, err := tx.ExecContext(ctx, `DELETE FROM release_issue_filter_records WHERE workspace_key=? AND release_id=?`, workspace, id)
		return err
	}
	var release domain.Release
	if err := json.Unmarshal(raw, &release); err != nil {
		return err
	}
	row := releaseIssueRowFor(release)
	wanted := map[string]bool{}
	for _, issueID := range release.IssueIDs {
		if issueID != "" {
			wanted[issueID] = true
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT issue_id FROM release_issue_filter_records WHERE workspace_key=? AND release_id=?`, workspace, id)
	if err != nil {
		return err
	}
	stored := map[string]bool{}
	for rows.Next() {
		var issueID string
		if err := rows.Scan(&issueID); err != nil {
			rows.Close()
			return err
		}
		stored[issueID] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(stored) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE release_issue_filter_records SET pipeline_id=?,stage=?,status=?,released_at=? WHERE workspace_key=? AND release_id=? AND (pipeline_id<>? OR stage<>? OR status<>? OR released_at<>?)`, row.pipeline, row.stage, row.status, row.releasedAt, workspace, id, row.pipeline, row.stage, row.status, row.releasedAt); err != nil {
			return err
		}
	}
	removed, added := []string{}, []string{}
	for issueID := range stored {
		if !wanted[issueID] {
			removed = append(removed, issueID)
		}
	}
	for issueID := range wanted {
		if !stored[issueID] {
			added = append(added, issueID)
		}
	}
	slices.Sort(removed)
	slices.Sort(added)
	for chunk := range slices.Chunk(removed, 200) {
		clause, args := bindList("issue_id", chunk)
		if _, err := tx.ExecContext(ctx, `DELETE FROM release_issue_filter_records WHERE workspace_key=? AND release_id=? AND `+clause, append([]any{workspace, id}, args...)...); err != nil {
			return err
		}
	}
	for chunk := range slices.Chunk(added, 200) {
		args := make([]any, 0, len(chunk)*7)
		for _, issueID := range chunk {
			args = append(args, workspace, id, issueID, row.pipeline, row.stage, row.status, row.releasedAt)
		}
		values := strings.TrimSuffix(strings.Repeat("(?,?,?,?,?,?,?),", len(chunk)), ",")
		if _, err := tx.ExecContext(ctx, `INSERT INTO release_issue_filter_records(workspace_key,release_id,issue_id,pipeline_id,stage,status,released_at) VALUES`+values+` ON CONFLICT DO NOTHING`, args...); err != nil {
			return err
		}
	}
	return nil
}

// migrateReleaseIssueIndex indexes releases written before the index existed,
// in bounded checkpointed transactions over the release records only.
func (s *SQLiteStore) migrateReleaseIssueIndex(ctx context.Context) error {
	for _, workspace := range s.WorkspaceKeys() {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO release_issue_filter_migrations(workspace_key,last_id,complete) VALUES(?,'',0) ON CONFLICT DO NOTHING`, workspace); err != nil {
			return err
		}
		for {
			var last string
			var done int
			if err := s.db.QueryRowContext(ctx, `SELECT last_id,complete FROM release_issue_filter_migrations WHERE workspace_key=?`, workspace).Scan(&last, &done); err != nil {
				return err
			}
			if done != 0 {
				break
			}
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			err = func() error {
				lock := ""
				if s.dialect != "sqlite" {
					lock = " FOR UPDATE"
				}
				rows, err := tx.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='releases' AND record_key>? ORDER BY record_key LIMIT 64`+lock, workspace, last)
				if err != nil {
					return err
				}
				type record struct {
					id  string
					raw []byte
				}
				batch := []record{}
				for rows.Next() {
					var item record
					if err := rows.Scan(&item.id, &item.raw); err != nil {
						rows.Close()
						return err
					}
					batch = append(batch, item)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return err
				}
				for _, item := range batch {
					if err := writeReleaseIssueRecords(ctx, tx, workspace, "releases", item.id, item.raw); err != nil {
						return err
					}
					last = item.id
				}
				if len(batch) < 64 {
					done = 1
				}
				_, err = tx.ExecContext(ctx, `UPDATE release_issue_filter_migrations SET last_id=?,complete=? WHERE workspace_key=?`, last, done, workspace)
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

// releaseFilterColumns maps the release query fields to their indexed column.
var releaseFilterColumns = map[string]string{
	"release": "release_id", "releaseId": "release_id", "releasePipeline": "pipeline_id", "releasePipelineId": "pipeline_id",
	"releaseStage": "stage", "releaseStatus": "status", "releasedAt": "released_at",
}

func isReleaseFilterField(field string) bool {
	_, ok := releaseFilterColumns[field]
	return ok
}

// compileReleaseFilter matches an issue through any of its releases, like
// The reference app: "Released date after X" holds when some release of the issue was
// released after X, "No released date" when none was released, "is empty" on
// the membership fields when the issue is in no release at all.
func compileReleaseFilter(node IssueFilter) (string, []any, error) {
	column := releaseFilterColumns[node.Field]
	op := strings.ToLower(node.Operator)
	if op == "" {
		op = "is"
	}
	member := `SELECT 1 FROM release_issue_filter_records r WHERE r.workspace_key=i.workspace_key AND r.issue_id=i.id`
	if column == "released_at" {
		member += ` AND r.released_at<>''`
	}
	switch op {
	case "isempty":
		return "NOT EXISTS (" + member + ")", nil, nil
	case "isnotempty":
		return "EXISTS (" + member + ")", nil, nil
	case "is", "in", "isnot", "notin":
		if column == "released_at" || len(node.Values) == 0 {
			return "", nil, ErrIssueQuery
		}
		set := slices.DeleteFunc(slices.Clone(node.Values), func(value string) bool { return value == "" })
		parts := []string{}
		var args []any
		if len(set) > 0 {
			clause, values := bindList("r."+column, set)
			parts = append(parts, "EXISTS ("+member+" AND "+clause+")")
			args = values
		}
		if len(set) < len(node.Values) {
			parts = append(parts, "NOT EXISTS ("+member+")")
		}
		clause := "(" + strings.Join(parts, " OR ") + ")"
		if op == "isnot" || op == "notin" {
			clause = "NOT " + clause
		}
		return clause, args, nil
	case "before", "after", "gt", "gte", "lt", "lte":
		if column != "released_at" || len(node.Values) != 1 {
			return "", nil, ErrIssueQuery
		}
		date, err := time.Parse(time.RFC3339Nano, node.Values[0])
		if err != nil {
			date, err = time.Parse("2006-01-02", node.Values[0])
		}
		if err != nil {
			return "", nil, ErrIssueQuery
		}
		operator := map[string]string{"before": "<", "after": ">", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[op]
		return "EXISTS (" + member + " AND r.released_at" + operator + "?)", []any{date.UTC().Format(issueRecordTimestamp)}, nil
	}
	return "", nil, fmt.Errorf("%w: unsupported operator %s", ErrIssueQuery, node.Operator)
}
