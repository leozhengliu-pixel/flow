package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func isDuplicateIndex(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "duplicate key name")
}

var workspaceRecordTables = []string{"issue_records", "issue_label_records", "issue_subscriber_records", "issue_actor_records", "issue_permission_records", "issue_attribute_records", "issue_attribute_migrations", "issue_collection_counts", "issue_records_migrations", "issue_scope_counts", "issue_stats_migrations", "issue_number_sequences", "workspace_content_records", "workspace_metadata_records", "issue_attachment_records", "issue_attachment_migrations", "issue_list_migrations", "issue_search_documents", "issue_search_migrations"}

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

func (s *SQLiteStore) replaceIssueRecords(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue, metadata domain.Bootstrap) error {
	refs := newIssueReferences(metadata)
	// Compatibility mutations supply the full collection; compare the existing
	// row payloads so unchanged entities never become database writes.
	rows, err := tx.QueryContext(ctx, `SELECT id,collection_order FROM issue_records WHERE workspace_key=?`, workspace)
	if err != nil {
		return err
	}
	previous := map[string]bool{}
	positions := map[string]int{}
	for rows.Next() {
		var id string
		var position int
		if err := rows.Scan(&id, &position); err != nil {
			rows.Close()
			return err
		}
		previous[id] = true
		positions[id] = position
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	order := issueCollectionOrder(issues, positions)
	deltas := map[issueStatsKey]int64{}
	for start := 0; start < len(issues); start += 64 {
		batch := issues[start:min(start+64, len(issues))]
		ids := make([]string, len(batch))
		for i, issue := range batch {
			ids[i] = issue.ID
		}
		clause, args := bindList("id", ids)
		rows, err := tx.QueryContext(ctx, `SELECT id,data FROM issue_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			return err
		}
		payloads := map[string][]byte{}
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				rows.Close()
				return err
			}
			payloads[id] = raw
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		changed := []domain.Issue{}
		for _, issue := range batch {
			position := order[issue.ID]
			values, err := issueRecordValues(workspace, issue)
			if err != nil {
				return err
			}
			raw := values[len(values)-1].([]byte)
			if !equalIssueRecordData(payloads[issue.ID], raw) && !refs.equalOwned(payloads[issue.ID], issue) {
				if old := payloads[issue.ID]; len(old) > 0 {
					var previousIssue domain.Issue
					if err := json.Unmarshal(old, &previousIssue); err != nil {
						return err
					}
					addIssueStats(deltas, issueStatsOf(previousIssue), -1)
				}
				addIssueStats(deltas, issueStatsOf(issue), 1)
				changed = append(changed, issue)
			} else if old, ok := positions[issue.ID]; !ok || old != position {
				if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET collection_order=? WHERE workspace_key=? AND id=?`, position, workspace, issue.ID); err != nil {
					return err
				}
			}
			delete(previous, issue.ID)
		}
		if err := s.importIssueRecordBatch(ctx, tx, workspace, changed, order); err != nil {
			return err
		}
	}
	for id := range previous {
		for _, table := range []string{"issue_subscriber_records", "issue_actor_records", "issue_attribute_records", "issue_attachment_records", "issue_search_documents"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE workspace_key=? AND issue_id=?", workspace, id); err != nil {
				return err
			}
		}
		old, err := readIssueStats(ctx, tx, workspace, id)
		if err != nil {
			return err
		}
		addIssueStats(deltas, old, -1)
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
	if err := writeIssueStats(ctx, tx, workspace, deltas); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO issue_records_migrations(workspace_key,migrated_at) VALUES(?,?) ON CONFLICT DO NOTHING`, workspace, time.Now().UTC().Format(time.RFC3339Nano))
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
		`CREATE TABLE IF NOT EXISTS issue_attribute_records (workspace_key VARCHAR(191) NOT NULL, issue_id VARCHAR(191) NOT NULL, field VARCHAR(32) NOT NULL, value VARCHAR(191) NOT NULL, PRIMARY KEY(workspace_key,issue_id,field))`,
		`CREATE TABLE IF NOT EXISTS issue_attribute_migrations (workspace_key VARCHAR(191) PRIMARY KEY, last_id VARCHAR(191) NOT NULL, complete INTEGER NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	indexes := []string{
		"issue_records_collection_idx ON issue_records(workspace_key,collection_order,id)",
		"issue_records_order_idx ON issue_records(workspace_key,archived,sort_order,id)",
		"issue_records_state_idx ON issue_records(workspace_key,archived,state_id,sort_order,id)",
		"issue_records_team_idx ON issue_records(workspace_key,team_id,archived,sort_order,id)",
		"issue_records_team_state_order_idx ON issue_records(workspace_key,team_id,state_id,sort_order,id)",
		"issue_records_project_idx ON issue_records(workspace_key,project_id,archived,sort_order,id)",
		"issue_records_assignee_idx ON issue_records(workspace_key,assignee_id,archived,sort_order,id)",
		"issue_records_parent_idx ON issue_records(workspace_key,parent_id,id)",
		"issue_records_updated_idx ON issue_records(workspace_key,updated_at,id)",
		"issue_records_created_idx ON issue_records(workspace_key,archived,created_at,id)",
		"issue_records_priority_idx ON issue_records(workspace_key,archived,priority,id)",
		"issue_records_assignee_counts_idx ON issue_records(workspace_key,assignee_id,archived,state_id,team_id)",
		"issue_records_creator_counts_idx ON issue_records(workspace_key,creator_id,archived,state_id,team_id)",
		"issue_attributes_value_idx ON issue_attribute_records(workspace_key,field,value,issue_id)",
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

const issueRecordInsert = `INSERT INTO issue_records(workspace_key,id,identifier,team_id,state_id,state_type,project_id,assignee_id,creator_id,cycle_id,parent_id,priority,sort_order,title,created_at,updated_at,archived,version,list_data,data) VALUES `
const issueRecordValuesSQL = `(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
const issueRecordTimestamp = "2006-01-02T15:04:05.000000000Z"
const issueRecordUpdate = ` ON CONFLICT(workspace_key,id) DO UPDATE SET identifier=excluded.identifier,team_id=excluded.team_id,state_id=excluded.state_id,state_type=excluded.state_type,project_id=excluded.project_id,assignee_id=excluded.assignee_id,creator_id=excluded.creator_id,cycle_id=excluded.cycle_id,parent_id=excluded.parent_id,priority=excluded.priority,sort_order=excluded.sort_order,title=excluded.title,created_at=excluded.created_at,updated_at=excluded.updated_at,archived=excluded.archived,version=excluded.version,list_data=excluded.list_data,data=excluded.data`

func equalIssueRecordData(previous, next []byte) bool {
	if bytes.Equal(previous, next) {
		return true
	}
	if len(previous) == 0 {
		return false
	}
	var issue domain.Issue
	if json.Unmarshal(previous, &issue) != nil {
		return false
	}
	values, err := issueRecordValues("", issue)
	return err == nil && bytes.Equal(values[len(values)-1].([]byte), next)
}

func issueRecordValues(workspace string, issue domain.Issue) ([]any, error) {
	normalizeIssueRecord(&issue)
	// Usage counts belong to the label read model. Copying them into every
	// issue rewrites the entire labeled collection whenever its count changes.
	issue.Labels = slices.Clone(issue.Labels)
	for i := range issue.Labels {
		issue.Labels[i].IssueCount = 0
	}
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
	list, err := json.Marshal(issueListProjection(issue))
	if err != nil {
		return nil, err
	}
	return []any{workspace, issue.ID, issue.Identifier, issue.Team.ID, issue.State.ID, issue.State.Type, project, assignee, issue.Creator.ID, cycle, parent, issue.Priority, issue.SortOrder, issue.Title, issue.CreatedAt.UTC().Format(issueRecordTimestamp), issue.UpdatedAt.UTC().Format(issueRecordTimestamp), archived, issue.Version, list, raw}, nil
}

func (s *SQLiteStore) writeIssueRecord(ctx context.Context, tx *sqlTx, workspace string, issue domain.Issue, metadata ...domain.Bootstrap) error {
	values, err := issueRecordValues(workspace, issue)
	if err != nil {
		return err
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, "SELECT data FROM issue_records WHERE workspace_key=? AND id=?", workspace, issue.ID).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if equalIssueRecordData(raw, values[len(values)-1].([]byte)) {
		return nil
	}
	if len(raw) > 0 && len(metadata) > 0 && newIssueReferences(metadata[0]).equalOwned(raw, issue) {
		return nil
	}
	var old *issueStatsDimensions
	if len(raw) > 0 {
		var previous domain.Issue
		if err := json.Unmarshal(raw, &previous); err != nil {
			return err
		}
		old = issueStatsOf(previous)
	}
	delta := map[issueStatsKey]int64{}
	addIssueStats(delta, old, -1)
	addIssueStats(delta, issueStatsOf(issue), 1)
	if err := writeIssueStats(ctx, tx, workspace, delta); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, issueRecordInsert+issueRecordValuesSQL+issueRecordUpdate, values...); err != nil {
		return err
	}
	return syncIssueIndexes(ctx, tx, workspace, []domain.Issue{issue})
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
		normalizeIssueRecord(&issue)
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

// Bootstrap keeps only list fields while it computes viewer visibility and
// counts. Full issue bodies are fetched in bounded batches during encoding.
func (s *SQLiteStore) BootstrapOutline(ctx context.Context, workspace, userID string) (domain.Bootstrap, error) {
	reader, data, ok := s.workspaceReadSource(ctx, workspace)
	if !ok {
		return data, ErrAuthForbidden
	}
	rows, err := reader.QueryContext(ctx, `SELECT COALESCE(list_data,data) FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, data.Workspace.URLKey)
	if err != nil {
		return data, err
	}
	data.Issues = []domain.Issue{}
	for rows.Next() {
		var raw []byte
		var issue domain.Issue
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return data, err
		}
		if err := json.Unmarshal(raw, &issue); err != nil {
			rows.Close()
			return data, err
		}
		normalizeIssueRecord(&issue)
		data.Issues = append(data.Issues, issueListProjection(issue))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	refreshIssueReferences(&data)
	if userID != "" {
		data, ok, err = s.projectBootstrapForUser(ctx, data, userID)
		if err != nil {
			return data, err
		}
		if !ok {
			return data, ErrAuthForbidden
		}
	} else {
		data.ViewerRole = "admin"
		refreshResourceCounts(&data)
	}
	return data, nil
}

func normalizeIssueRecord(issue *domain.Issue) {
	if issue.Version == 0 {
		issue.Version = 1
	}
	if issue.Reactions == nil {
		issue.Reactions = map[string][]string{}
	}
	if issue.Labels == nil {
		issue.Labels = []domain.IssueLabel{}
	}
	if issue.SubscriberIDs == nil {
		issue.SubscriberIDs = []string{}
	}
	if issue.SubIssueIDs == nil {
		issue.SubIssueIDs = []string{}
	}
	if issue.Relations == nil {
		issue.Relations = []domain.IssueRelation{}
	}
	if issue.Attachments == nil {
		issue.Attachments = []domain.Attachment{}
	}
}

// ImportIssues writes bounded batches without building a workspace-sized value.
// Existing IDs are updated atomically with their label index entries.
func (s *SQLiteStore) ImportIssues(ctx context.Context, workspace string, issues []domain.Issue) error {
	if len(issues) > 1000 {
		return fmt.Errorf("issue import batch exceeds 1000 records")
	}
	if len(issues) == 0 {
		return nil
	}
	encoded := make(map[string][]byte, len(issues))
	totalBytes := 0
	for _, issue := range issues {
		if issue.ID == "" {
			return fmt.Errorf("issue import requires non-empty IDs")
		}
		values, err := issueRecordValues(workspace, issue)
		if err != nil {
			return err
		}
		raw := values[len(values)-1].([]byte)
		totalBytes += len(raw)
		if len(raw) > 1<<20 || totalBytes > 16<<20 {
			return fmt.Errorf("issue import exceeds 1 MiB per issue or 16 MiB per transaction")
		}
		encoded[issue.ID] = raw
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
	unchanged := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT id,data FROM issue_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		existing++
		if equalIssueRecordData(raw, encoded[id]) {
			unchanged[id] = true
			continue
		}
		var previous domain.Issue
		if err := json.Unmarshal(raw, &previous); err != nil {
			rows.Close()
			return err
		}
		addIssueStats(delta, issueStatsOf(previous), -1)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	changed := make([]domain.Issue, 0, len(issues))
	for _, issue := range issues {
		if unchanged[issue.ID] {
			continue
		}
		changed = append(changed, issue)
		addIssueStats(delta, issueStatsOf(issue), 1)
	}
	if len(changed) == 0 {
		return nil
	}
	if err := s.importIssueRecordBatch(ctx, tx, workspace, changed); err != nil {
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
			expression = `CAST(JSON_UNQUOTE(JSON_EXTRACT(CONVERT(data USING utf8mb4),'$.number')) AS UNSIGNED)`
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

func (s *SQLiteStore) importIssueRecordBatch(ctx context.Context, tx *sqlTx, workspace string, issues []domain.Issue, orders ...map[string]int) error {
	insert, tuple, update := issueRecordInsert, issueRecordValuesSQL, issueRecordUpdate
	if len(orders) > 0 {
		insert = strings.TrimSuffix(insert, ") VALUES ") + ",collection_order) VALUES "
		tuple = strings.TrimSuffix(tuple, ")") + ",?)"
		update += ",collection_order=excluded.collection_order"
	}
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
			if len(orders) > 0 {
				values = append(values, orders[0][issues[end].ID])
			}
			args = append(args, values...)
			tuples = append(tuples, tuple)
			end++
		}
		if _, err := tx.ExecContext(ctx, insert+strings.Join(tuples, ",")+update, args...); err != nil {
			return err
		}
		start = end
	}
	return syncIssueIndexes(ctx, tx, workspace, issues)
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
	clone := cloneBootstrap(data)
	clone.Issues = []domain.Issue{}
	clone.Activities = map[string][]domain.ActivityEvent{}
	clone.Comments = map[string][]domain.Comment{}
	clone.Notifications = []domain.Notification{}
	clone.NotificationDeliveries = []domain.NotificationDelivery{}
	return clone, true
}
