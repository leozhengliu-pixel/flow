package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func isDuplicateIndex(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "duplicate key name")
}

var workspaceRecordTables = []string{"issue_records", "issue_label_records", "issue_subscriber_records", "issue_actor_records", "issue_permission_records", "issue_collection_counts", "issue_records_migrations", "issue_scope_counts", "issue_stats_migrations", "issue_number_sequences", "workspace_content_records"}

func (s *SQLiteStore) migrateIssueCollections(ctx context.Context) error {
	for key, data := range s.workspaces {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_records_migrations WHERE workspace_key=?`, key).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 || data.Activities != nil || data.Comments != nil || data.Notifications != nil || data.NotificationDeliveries != nil {
			if err := s.persistWorkspace(ctx, key, data, nil); err != nil {
				return err
			}
		}
		data.Issues = nil
		data.Activities = nil
		data.Comments = nil
		data.Notifications = nil
		data.NotificationDeliveries = nil
		s.workspaces[key] = data
	}
	return nil
}

func (s *SQLiteStore) replaceIssueRecords(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue) error {
	// Compatibility mutations supply the full collection; compare the existing
	// row payloads so unchanged entities never become database writes.
	rows, err := tx.QueryContext(ctx, `SELECT id,data,collection_order FROM issue_records WHERE workspace_key=?`, workspace)
	if err != nil {
		return err
	}
	previous := map[string]string{}
	positions := map[string]int{}
	for rows.Next() {
		var id string
		var raw []byte
		var position int
		if err := rows.Scan(&id, &raw, &position); err != nil {
			rows.Close()
			return err
		}
		previous[id] = string(raw)
		positions[id] = position
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for position, issue := range issues {
		raw, err := json.Marshal(issue)
		if err != nil {
			return err
		}
		if previous[issue.ID] != string(raw) {
			if err := s.writeIssueRecord(ctx, tx, workspace, issue); err != nil {
				return err
			}
		}
		if old, ok := positions[issue.ID]; !ok || old != position {
			if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET collection_order=? WHERE workspace_key=? AND id=?`, position, workspace, issue.ID); err != nil {
				return err
			}
		}
		delete(previous, issue.ID)
	}
	for id := range previous {
		for _, table := range []string{"issue_subscriber_records", "issue_actor_records"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE workspace_key=? AND issue_id=?", workspace, id); err != nil {
				return err
			}
		}
		old, err := readIssueStats(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		delta := map[issueStatsKey]int64{}
		addIssueStats(delta, old, -1)
		if err := writeIssueStats(ctx, tx, workspace, delta); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue_permission_records WHERE workspace_key=? AND issue_id=?`, workspace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue_label_records WHERE workspace_key=? AND issue_id=?`, workspace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM issue_records WHERE workspace_key=? AND id=?`, workspace, id); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO issue_records_migrations(workspace_key,migrated_at) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET migrated_at=excluded.migrated_at`, workspace, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO issue_collection_counts(workspace_key,total) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET total=excluded.total`, workspace, len(issues))
	if err != nil {
		return err
	}
	maxNumber := 0
	for _, issue := range issues {
		maxNumber = max(maxNumber, issue.Number)
	}
	return writeIssueNumber(ctx, tx, workspace, maxNumber)
}

// Issues have independent records and indexed query columns. JSON is retained
// only as the per-entity representation, never as the workspace issue array.
func (s *SQLiteStore) ensureIssueRecords(ctx context.Context) error {
	blob := "BLOB"
	if s.dialect == "mysql" {
		blob = "LONGBLOB"
	}
	if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	statements := []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS issue_records (
		 workspace_key VARCHAR(191) NOT NULL, id VARCHAR(191) NOT NULL,
		 identifier VARCHAR(191) NOT NULL, team_id VARCHAR(191) NOT NULL,
		 state_id VARCHAR(191) NOT NULL, state_type VARCHAR(32) NOT NULL,
		 project_id VARCHAR(191) NOT NULL, assignee_id VARCHAR(191) NOT NULL,
		 creator_id VARCHAR(191) NOT NULL, cycle_id VARCHAR(191) NOT NULL,
		 parent_id VARCHAR(191) NOT NULL, priority INTEGER NOT NULL,
		 sort_order DOUBLE PRECISION NOT NULL, title VARCHAR(2048) NOT NULL,
		 created_at VARCHAR(40) NOT NULL, updated_at VARCHAR(40) NOT NULL,
		 archived INTEGER NOT NULL, version BIGINT NOT NULL, collection_order BIGINT NOT NULL DEFAULT 0, data %s NOT NULL,
		 PRIMARY KEY(workspace_key,id), UNIQUE(workspace_key,identifier))`, blob),
		`CREATE TABLE IF NOT EXISTS issue_label_records (workspace_key VARCHAR(191) NOT NULL, issue_id VARCHAR(191) NOT NULL, label_id VARCHAR(191) NOT NULL, PRIMARY KEY(workspace_key,issue_id,label_id))`,
		`CREATE TABLE IF NOT EXISTS issue_subscriber_records (workspace_key VARCHAR(191) NOT NULL, issue_id VARCHAR(191) NOT NULL, user_id VARCHAR(191) NOT NULL, PRIMARY KEY(workspace_key,issue_id,user_id))`,
		`CREATE TABLE IF NOT EXISTS issue_actor_records (workspace_key VARCHAR(191) NOT NULL, issue_id VARCHAR(191) NOT NULL, user_id VARCHAR(191) NOT NULL, last_at VARCHAR(40) NOT NULL, PRIMARY KEY(workspace_key,issue_id,user_id))`,
		`CREATE TABLE IF NOT EXISTS issue_permission_records (workspace_key VARCHAR(191) NOT NULL, issue_id VARCHAR(191) NOT NULL, subject_type VARCHAR(32) NOT NULL, subject_id VARCHAR(191) NOT NULL, role VARCHAR(32) NOT NULL, PRIMARY KEY(workspace_key,issue_id,subject_type,subject_id))`,
		`CREATE TABLE IF NOT EXISTS issue_records_migrations (workspace_key VARCHAR(191) PRIMARY KEY, migrated_at VARCHAR(40) NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS issue_collection_counts (workspace_key VARCHAR(191) PRIMARY KEY, total BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS issue_number_sequences (workspace_key VARCHAR(191) PRIMARY KEY, last_number BIGINT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	indexes := []string{
		"issue_records_order_idx ON issue_records(workspace_key,archived,sort_order,id)",
		"issue_records_state_idx ON issue_records(workspace_key,archived,state_id,sort_order,id)",
		"issue_records_team_idx ON issue_records(workspace_key,team_id,archived,sort_order,id)",
		"issue_records_team_state_order_idx ON issue_records(workspace_key,team_id,state_id,sort_order,id)",
		"issue_records_project_idx ON issue_records(workspace_key,project_id,archived,sort_order,id)",
		"issue_records_assignee_idx ON issue_records(workspace_key,assignee_id,archived,sort_order,id)",
		"issue_records_parent_idx ON issue_records(workspace_key,parent_id,id)",
		"issue_records_updated_idx ON issue_records(workspace_key,updated_at,id)",
		"issue_labels_label_idx ON issue_label_records(workspace_key,label_id,issue_id)",
		"issue_subscribers_user_idx ON issue_subscriber_records(workspace_key,user_id,issue_id)",
		"issue_actors_user_idx ON issue_actor_records(workspace_key,user_id,issue_id)",
		"issue_permissions_subject_idx ON issue_permission_records(workspace_key,subject_type,subject_id,issue_id)",
	}
	for _, index := range indexes {
		statement := "CREATE INDEX IF NOT EXISTS " + index
		if s.dialect == "mysql" {
			statement = "CREATE INDEX " + index
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

const issueRecordInsert = `INSERT INTO issue_records(workspace_key,id,identifier,team_id,state_id,state_type,project_id,assignee_id,creator_id,cycle_id,parent_id,priority,sort_order,title,created_at,updated_at,archived,version,data) VALUES `
const issueRecordValuesSQL = `(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
const issueRecordTimestamp = "2006-01-02T15:04:05.000000000Z"
const issueRecordUpdate = ` ON CONFLICT(workspace_key,id) DO UPDATE SET identifier=excluded.identifier,team_id=excluded.team_id,state_id=excluded.state_id,state_type=excluded.state_type,project_id=excluded.project_id,assignee_id=excluded.assignee_id,creator_id=excluded.creator_id,cycle_id=excluded.cycle_id,parent_id=excluded.parent_id,priority=excluded.priority,sort_order=excluded.sort_order,title=excluded.title,created_at=excluded.created_at,updated_at=excluded.updated_at,archived=excluded.archived,version=excluded.version,data=excluded.data`

func issueRecordValues(workspace string, issue domain.Issue) ([]any, error) {
	raw, err := json.Marshal(issue)
	if err != nil {
		return nil, err
	}
	project, assignee, parent, cycle := "", "", "", ""
	if issue.Project != nil {
		project = issue.Project.ID
	}
	if issue.Assignee != nil {
		assignee = issue.Assignee.ID
	}
	if issue.ParentID != nil {
		parent = *issue.ParentID
	}
	if issue.CycleID != nil {
		cycle = *issue.CycleID
	}
	archived := 0
	if issue.ArchivedAt != nil {
		archived = 1
	}
	return []any{workspace, issue.ID, issue.Identifier, issue.Team.ID, issue.State.ID, issue.State.Type, project, assignee, issue.Creator.ID, cycle, parent, issue.Priority, issue.SortOrder, issue.Title, issue.CreatedAt.UTC().Format(issueRecordTimestamp), issue.UpdatedAt.UTC().Format(issueRecordTimestamp), archived, issue.Version, raw}, nil
}

func (s *SQLiteStore) writeIssueRecord(ctx context.Context, tx *sqlTx, workspace string, issue domain.Issue) error {
	old, err := readIssueStats(ctx, tx, workspace, issue.ID)
	if err != nil {
		return err
	}
	values, err := issueRecordValues(workspace, issue)
	if err != nil {
		return err
	}
	delta := map[issueStatsKey]int64{}
	addIssueStats(delta, old, -1)
	addIssueStats(delta, issueStatsOf(issue), 1)
	if err := writeIssueStats(ctx, tx, workspace, delta); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, issueRecordInsert+issueRecordValuesSQL+issueRecordUpdate, values...)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM issue_label_records WHERE workspace_key=? AND issue_id=?`, workspace, issue.ID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, label := range issue.Labels {
		if seen[label.ID] {
			continue
		}
		seen[label.ID] = true
		if _, err = tx.ExecContext(ctx, `INSERT INTO issue_label_records(workspace_key,issue_id,label_id) VALUES(?,?,?)`, workspace, issue.ID, label.ID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_permission_records WHERE workspace_key=? AND issue_id=?`, workspace, issue.ID); err != nil {
		return err
	}
	for _, permission := range issue.Permissions {
		if permission.Role == "" || strings.EqualFold(permission.Role, "none") {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO issue_permission_records(workspace_key,issue_id,subject_type,subject_id,role) VALUES(?,?,?,?,?) ON CONFLICT(workspace_key,issue_id,subject_type,subject_id) DO UPDATE SET role=excluded.role`, workspace, issue.ID, permission.SubjectType, permission.SubjectID, permission.Role); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_subscriber_records WHERE workspace_key=? AND issue_id=?`, workspace, issue.ID); err != nil {
		return err
	}
	seenSubscribers := map[string]bool{}
	for _, userID := range issue.SubscriberIDs {
		if seenSubscribers[userID] {
			continue
		}
		seenSubscribers[userID] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO issue_subscriber_records(workspace_key,issue_id,user_id) VALUES(?,?,?)`, workspace, issue.ID, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) readIssueRecords(ctx context.Context, workspace string) ([]domain.Issue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := []domain.Issue{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var issue domain.Issue
		if err := json.Unmarshal(raw, &issue); err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

// ImportIssues writes bounded batches without building a workspace-sized value.
// Existing IDs are updated atomically with their label index entries.
func (s *SQLiteStore) ImportIssues(ctx context.Context, workspace string, issues []domain.Issue) error {
	if len(issues) > 1000 {
		return fmt.Errorf("issue import batch exceeds 1000 records")
	}
	if _, ok := s.WorkspaceMetadata(workspace); !ok {
		return fmt.Errorf("workspace not found")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids := []string{}
	seen := map[string]bool{}
	for _, issue := range issues {
		if !seen[issue.ID] {
			ids = append(ids, issue.ID)
			seen[issue.ID] = true
		}
	}
	if len(ids) != len(issues) {
		return fmt.Errorf("duplicate IDs in issue import batch")
	}
	clause, args := bindList("id", ids)
	var existing int
	delta := map[issueStatsKey]int64{}
	rows, err := tx.QueryContext(ctx, `SELECT team_id,project_id,assignee_id,state_id,state_type,priority,archived FROM issue_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d issueStatsDimensions
		if err := rows.Scan(&d.Team, &d.Project, &d.Assignee, &d.State, &d.Type, &d.Priority, &d.Archived); err != nil {
			rows.Close()
			return err
		}
		existing++
		addIssueStats(delta, &d, -1)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, issue := range issues {
		addIssueStats(delta, issueStatsOf(issue), 1)
	}
	if err := s.importIssueRecordBatch(ctx, tx, workspace, issues); err != nil {
		return err
	}
	if err := writeIssueStats(ctx, tx, workspace, delta); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO issue_collection_counts(workspace_key,total) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET total=issue_collection_counts.total+excluded.total`, workspace, len(ids)-existing); err != nil {
		return err
	}
	maxNumber := 0
	for _, issue := range issues {
		maxNumber = max(maxNumber, issue.Number)
	}
	if err := writeIssueNumber(ctx, tx, workspace, maxNumber); err != nil {
		return err
	}
	return tx.Commit()
}

func writeIssueNumber(ctx context.Context, tx *sqlTx, workspace string, number int) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO issue_number_sequences(workspace_key,last_number) VALUES(?,?) ON CONFLICT(workspace_key) DO UPDATE SET last_number=CASE WHEN issue_number_sequences.last_number>excluded.last_number THEN issue_number_sequences.last_number ELSE excluded.last_number END`, workspace, number)
	return err
}

func (s *SQLiteStore) migrateIssueSequences(ctx context.Context) error {
	for workspace := range s.workspaces {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_number_sequences WHERE workspace_key=?`, workspace).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		expression := `CAST(json_extract(data,'$.number') AS INTEGER)`
		if s.dialect == "mysql" {
			expression = `CAST(JSON_UNQUOTE(JSON_EXTRACT(data,'$.number')) AS UNSIGNED)`
		}
		if s.dialect == "postgres" {
			expression = `CAST(convert_from(data,'UTF8')::jsonb->>'number' AS BIGINT)`
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_number_sequences(workspace_key,last_number) SELECT ?,COALESCE(MAX(`+expression+`),0) FROM issue_records WHERE workspace_key=? ON CONFLICT(workspace_key) DO UPDATE SET last_number=excluded.last_number`, workspace, workspace); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) importIssueRecordBatch(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue) error {
	for start := 0; start < len(issues); {
		end := start
		size := 0
		var args []any
		var tuples []string
		for end < len(issues) && end-start < 100 {
			values, err := issueRecordValues(workspace, issues[end])
			if err != nil {
				return err
			}
			raw := values[len(values)-1].([]byte)
			if end > start && size+len(raw) > 4<<20 {
				break
			}
			size += len(raw)
			args = append(args, values...)
			tuples = append(tuples, issueRecordValuesSQL)
			end++
		}
		if _, err := tx.ExecContext(ctx, issueRecordInsert+strings.Join(tuples, ",")+issueRecordUpdate, args...); err != nil {
			return err
		}
		ids := make([]string, 0, end-start)
		for _, issue := range issues[start:end] {
			ids = append(ids, issue.ID)
		}
		where, values := bindList("issue_id", ids)
		for _, table := range []string{"issue_label_records", "issue_permission_records", "issue_subscriber_records"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE workspace_key=? AND "+where, append([]any{workspace}, values...)...); err != nil {
				return err
			}
		}
		var labelArgs, permissionArgs []any
		var labelTuples, permissionTuples []string
		var subscriberArgs []any
		var subscriberTuples []string
		for _, issue := range issues[start:end] {
			seenSubscribers := map[string]bool{}
			for _, userID := range issue.SubscriberIDs {
				if seenSubscribers[userID] {
					continue
				}
				seenSubscribers[userID] = true
				subscriberArgs = append(subscriberArgs, workspace, issue.ID, userID)
				subscriberTuples = append(subscriberTuples, "(?,?,?)")
			}
			seen := map[string]bool{}
			for _, label := range issue.Labels {
				if seen[label.ID] {
					continue
				}
				seen[label.ID] = true
				labelArgs = append(labelArgs, workspace, issue.ID, label.ID)
				labelTuples = append(labelTuples, "(?,?,?)")
			}
			for _, permission := range issue.Permissions {
				if permission.Role == "" || strings.EqualFold(permission.Role, "none") {
					continue
				}
				permissionArgs = append(permissionArgs, workspace, issue.ID, permission.SubjectType, permission.SubjectID, permission.Role)
				permissionTuples = append(permissionTuples, "(?,?,?,?,?)")
			}
		}
		if len(labelTuples) > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO issue_label_records(workspace_key,issue_id,label_id) VALUES `+strings.Join(labelTuples, ","), labelArgs...); err != nil {
				return err
			}
		}
		if len(permissionTuples) > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO issue_permission_records(workspace_key,issue_id,subject_type,subject_id,role) VALUES `+strings.Join(permissionTuples, ",")+` ON CONFLICT(workspace_key,issue_id,subject_type,subject_id) DO UPDATE SET role=excluded.role`, permissionArgs...); err != nil {
				return err
			}
		}
		if len(subscriberTuples) > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO issue_subscriber_records(workspace_key,issue_id,user_id) VALUES `+strings.Join(subscriberTuples, ","), subscriberArgs...); err != nil {
				return err
			}
		}
		start = end
	}
	return nil
}

func (s *SQLiteStore) issueCollectionCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,total FROM issue_collection_counts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]int{}
	for rows.Next() {
		var workspace string
		var count int
		if err := rows.Scan(&workspace, &count); err != nil {
			return nil, err
		}
		values[workspace] = count
	}
	return values, rows.Err()
}

// WorkspaceMetadata does not load the issue collection. Query endpoints must use
// this projection instead of the compatibility full-bootstrap API.
func (s *SQLiteStore) WorkspaceMetadata(workspace string) (domain.Bootstrap, bool) {
	s.mu.RLock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	s.mu.RUnlock()
	if !ok {
		return domain.Bootstrap{}, false
	}
	data.Issues = nil
	data.Activities = nil
	data.Comments = nil
	data.Notifications = nil
	data.NotificationDeliveries = nil
	raw, err := json.Marshal(data)
	if err != nil {
		return domain.Bootstrap{}, false
	}
	var clone domain.Bootstrap
	if json.Unmarshal(raw, &clone) != nil {
		return domain.Bootstrap{}, false
	}
	clone.Issues = []domain.Issue{}
	clone.Activities = map[string][]domain.ActivityEvent{}
	clone.Comments = map[string][]domain.Comment{}
	clone.Notifications = []domain.Notification{}
	clone.NotificationDeliveries = []domain.NotificationDelivery{}
	return clone, true
}
