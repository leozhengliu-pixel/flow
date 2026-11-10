package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Permanent workspace deletion.
//
// DELETE /api/workspaces/{key} only schedules deletion (ScheduleWorkspaceDeletion).
// After the grace period the server's maintenance loop purges the workspace:
// it records a receipt (BeginWorkspacePurge), deletes the objects and
// credentials the workspace's rows point to, removes the workspace from the
// catalog (RemoveWorkspaceFromCatalog) and then deletes every row the
// workspace owns (PurgeWorkspaceRows). Each step is idempotent, and the
// receipt stays "purging" until the last one finished, so a pass interrupted
// by a crash or a restart finishes on the next run.

// workspacePurgeBatch is how many rows one purge statement deletes.
const workspacePurgeBatch = 1000

// Receipt status values.
const (
	WorkspacePurgePurging   = "purging"
	WorkspacePurgeCompleted = "completed"
)

// ErrWorkspacePurging is returned when a deletion can no longer be canceled
// because the permanent purge has started.
var ErrWorkspacePurging = errors.New("workspace deletion is already in progress")

// ErrWorkspaceKeyReused stops a purge whose URL key now belongs to another
// workspace: rows keyed by that URL key are no longer the purged workspace's.
var ErrWorkspaceKeyReused = errors.New("workspace URL key belongs to another workspace")

// workspacePurgeTable is a table whose rows belong to one workspace through a
// column holding the workspace's URL key (byID false) or its ID (byID true).
type workspacePurgeTable struct {
	table, column string
	byID          bool
}

// workspacePurgeTables lists every table that holds rows of one workspace.
// TestWorkspacePurgeCoversEverySchemaTable fails when a table with a
// workspace column is missing here (or from workspacePurgeIndirect /
// workspacePurgeExempt). Rows are deleted in this order after the indirect
// steps; there are no foreign keys between these tables, so the order only
// keeps children before the rows that name them.
var workspacePurgeTables = []workspacePurgeTable{
	// Agent runs and sessions.
	{"agent_run_activities", "workspace_key", false},
	{"agent_run_changes", "workspace_key", false},
	{"agent_run_keys", "workspace_key", false},
	{"agent_run_states", "workspace_key", false},
	{"agent_run_tokens", "workspace_key", false},
	{"agent_run_tool_calls", "workspace_key", false},
	{"agent_session_states", "workspace_key", false},
	{"agent_runs", "workspace_key", false},
	// Applications, API keys and integrations.
	{"api_key_lookup", "workspace_key", false},
	{"api_key_lookup_migrations", "workspace_key", false},
	{"application_agent_tasks", "workspace_key", false},
	{"application_installations", "workspace_key", false},
	{"audit_stream_secrets", "workspace_key", false},
	{"webhook_deliveries", "workspace_key", false},
	{"webhook_signing_secrets", "workspace_key", false},
	{"code_host_accounts", "workspace_key", false},
	{"code_host_installations", "workspace_key", false},
	{"code_host_webhook_deliveries", "workspace_key", false},
	// Chat (deleteChatWorkspace runs first and also removes these; listed
	// so a partially purged workspace is finished either way).
	{"chat_actions", "workspace_key", false},
	{"chat_bindings", "workspace_key", false},
	{"chat_domain_cursors", "workspace_key", false},
	{"chat_identities", "workspace_key", false},
	{"chat_initiative_bindings", "workspace_key", false},
	{"chat_intake_requests", "workspace_key", false},
	{"chat_jobs", "workspace_key", false},
	{"chat_messages", "workspace_key", false},
	{"chat_mutation_receipts", "workspace_key", false},
	{"chat_reply_leases", "workspace_key", false},
	{"chat_thread_items", "workspace_key", false},
	{"chat_threads", "workspace_key", false},
	{"chat_integrations", "workspace_key", false},
	// Coding sessions, sandboxes and environments.
	{"coding_environment_secrets", "workspace_key", false},
	{"coding_runner_events", "workspace_key", false},
	{"coding_sandbox_tokens", "workspace_key", false},
	{"coding_session_artifacts", "workspace_key", false},
	{"coding_session_commits", "workspace_key", false},
	{"coding_session_inbox", "workspace_key", false},
	{"coding_session_pull_requests", "workspace_key", false},
	{"coding_snapshot_logs", "workspace_key", false},
	{"coding_usage_records", "workspace_key", false},
	{"coding_sandboxes", "workspace_key", false},
	{"coding_environment_snapshots", "workspace_key", false},
	{"coding_sessions", "workspace_key", false},
	{"coding_environments", "workspace_key", false},
	// Issues and their projections.
	{"issue_activity_cycles", "workspace_key", false},
	{"issue_activity_cycles_migrations", "workspace_key", false},
	{"issue_actor_records", "workspace_key", false},
	{"issue_attachment_migrations", "workspace_key", false},
	{"issue_attachment_records", "workspace_key", false},
	{"issue_attribute_migrations", "workspace_key", false},
	{"issue_attribute_records", "workspace_key", false},
	{"issue_collection_counts", "workspace_key", false},
	{"issue_count_revisions", "workspace_key", false},
	{"issue_identifier_aliases", "workspace_key", false},
	{"issue_label_records", "workspace_key", false},
	{"issue_list_migrations", "workspace_key", false},
	{"issue_number_sequences", "workspace_key", false},
	{"issue_permission_records", "workspace_key", false},
	{"issue_records_migrations", "workspace_key", false},
	{"issue_scope_counts", "workspace_key", false},
	{"issue_search_documents", "workspace_key", false},
	{"issue_search_migrations", "workspace_key", false},
	{"issue_stats_migrations", "workspace_key", false},
	{"issue_subscriber_records", "workspace_key", false},
	{"issue_records", "workspace_key", false},
	{"customer_filter_migrations", "workspace_key", false},
	{"customer_filter_records", "workspace_key", false},
	{"customer_request_filter_records", "workspace_key", false},
	{"release_issue_filter_migrations", "workspace_key", false},
	{"release_issue_filter_records", "workspace_key", false},
	{"recurrence_instance_migrations", "workspace_key", false},
	{"team_default_favorite_overrides", "workspace_key", false},
	{"team_default_favorites", "workspace_key", false},
	// Loops, reviews and documents.
	{"loop_document_appends", "workspace_key", false},
	{"loop_run_events", "workspace_key", false},
	{"loop_run_migrations", "workspace_key", false},
	{"loop_run_records", "workspace_key", false},
	{"review_import_items", "workspace_key", false},
	{"review_import_state", "workspace_key", false},
	{"document_collaboration_updates", "workspace_key", false},
	// Uploads (their objects are deleted before the rows).
	{"file_uploads", "workspace_key", false},
	// Workspace content, metadata, search and caches.
	{"metadata_search_documents", "workspace_key", false},
	{"metadata_search_migration_checkpoints", "workspace_key", false},
	{"metadata_search_migrations", "workspace_key", false},
	{"metadata_blob_migrations", "workspace_key", false},
	{"metadata_blobs", "workspace_key", false},
	{"workspace_content_records", "workspace_key", false},
	{"workspace_metadata_records", "workspace_key", false},
	{"workspace_cache_revisions", "workspace_key", false},
	{"workspace_states", "workspace_key", false},
	// Rows keyed by the workspace ID.
	{"recently_viewed", "workspace_id", true},
	{"search_history", "workspace_id", true},
	{"scim_group_memberships", "workspace_id", true},
	{"scim_team_memberships", "workspace_id", true},
	{"scim_groups", "workspace_id", true},
	{"scim_tokens", "workspace_id", true},
	{"scim_user_roles", "workspace_id", true},
	{"team_memberships", "workspace_id", true},
	{"workspace_departures", "workspace_id", true},
	{"workspace_invitations", "workspace_id", true},
	{"workspace_memberships", "workspace_id", true},
	{"domain_event_sequencing", "workspace_id", true},
	{"domain_events", "workspace_id", true},
	{"workspace_event_seq", "workspace_id", true},
	{"workspace_key_renames", "workspace_id", true},
}

// workspacePurgeIndirect lists tables without a workspace column that still
// hold workspace rows, and how PurgeWorkspaceRows finds them.
var workspacePurgeIndirect = map[string]string{
	"application_agent_activities": "session_id of the workspace's application_agent_tasks",
	"connector_secrets":            "chat credentials (deleteChatWorkspace), code host account tokens; per-connector credentials and integration OAuth tokens (IntegrationOAuthSecretID) are deleted by the server from the workspace's connections",
	"chat_request_limits":          "scope keys of the workspace's chat integrations (deleteChatWorkspace)",
	"oauth_authorization_codes":    "data.workspaceKey",
	"oauth_refresh_tokens":         "data.workspaceKey",
	"workspace_state":              "legacy single-workspace row whose data.workspace.id is the workspace",
	"account_state":                "last_workspace_key moves to another workspace",
	"auth_account_state":           "last_workspace_key is cleared",
}

// workspacePurgeKept lists tables keyed by workspace whose rows outlive the
// purge on purpose.
var workspacePurgeKept = map[string]string{
	"workspace_deletion_receipts": "the receipt records the purge (identifiers and counts, no personal data)",
	// The audit log is evidence of what happened in the workspace, its
	// deletion included, so it is not deleted with the workspace: audit
	// retention (FLOW_AUDIT_LOG_RETENTION_DAYS, PruneAuditEvents) removes a
	// purged workspace's entries like any other workspace's once they age out.
	"audit_events":      "audit entries stay until audit retention prunes them",
	"audit_chain_heads": "the audit chain head (seq and hash bookkeeping) stays with the entries it verifies",
}

// workspacePurgeExempt lists tables that hold no workspace rows.
var workspacePurgeExempt = map[string]string{
	"api_signing_secrets":         "instance signing keys",
	"auth_identities":             "user accounts (account deletion)",
	"auth_sessions":               "user accounts (account deletion)",
	"auth_session_security":       "user accounts (account deletion)",
	"auth_tokens":                 "user accounts (account deletion)",
	"auth_users":                  "user accounts (account deletion)",
	"auth_email_events":           "user accounts (account deletion)",
	"coding_signing_keys":         "per-user commit signing keys",
	"personal_connections":        "per-user connections",
	"personal_data_exports":       "per-account Download your data jobs (user_id only); a workspace purge expires its members' archives (server expireMemberDataExports), account erasure deletes the jobs and archives",
	"oauth_clients":               "instance OAuth client registrations",
	"schema_migrations":           "schema bookkeeping",
	"search_index_settings":       "instance search settings",
	"sqlite_sequence":             "SQLite bookkeeping",
	"issue_search_fts":            "SQLite full-text index kept in step with issue_search_documents by triggers",
	"issue_search_fts_config":     "SQLite full-text index internals",
	"issue_search_fts_data":       "SQLite full-text index internals",
	"issue_search_fts_docsize":    "SQLite full-text index internals",
	"issue_search_fts_idx":        "SQLite full-text index internals",
	"metadata_search_fts":         "SQLite full-text index kept in step with metadata_search_documents by triggers",
	"metadata_search_fts_config":  "SQLite full-text index internals",
	"metadata_search_fts_data":    "SQLite full-text index internals",
	"metadata_search_fts_docsize": "SQLite full-text index internals",
	"metadata_search_fts_idx":     "SQLite full-text index internals",
}

// WorkspacePurgeTarget names the workspace a purge removes.
type WorkspacePurgeTarget struct {
	Key string
	ID  string
}

// WorkspaceDeletionReceipt records that a workspace was (or is being)
// purged. It holds identifiers and counts only, no personal data.
type WorkspaceDeletionReceipt struct {
	WorkspaceID  string           `json:"workspaceId"`
	WorkspaceKey string           `json:"workspaceKey"`
	RequestRef   string           `json:"requestRef,omitempty"`
	RequestedAt  time.Time        `json:"requestedAt"`
	StartedAt    time.Time        `json:"startedAt"`
	CompletedAt  *time.Time       `json:"completedAt,omitempty"`
	Status       string           `json:"status"`
	Counts       map[string]int64 `json:"counts"`
	LastError    string           `json:"lastError,omitempty"`
}

// Target is the workspace the receipt purges.
func (r WorkspaceDeletionReceipt) Target() WorkspacePurgeTarget {
	return WorkspacePurgeTarget{Key: r.WorkspaceKey, ID: r.WorkspaceID}
}

// AddCount adds n to the receipt's count for name.
func (r *WorkspaceDeletionReceipt) AddCount(name string, n int64) {
	if n == 0 {
		return
	}
	if r.Counts == nil {
		r.Counts = map[string]int64{}
	}
	r.Counts[name] += n
}

func (s *SQLiteStore) ensureWorkspaceDeletionReceipts(ctx context.Context) error {
	text := "TEXT"
	if s.dialect == "mysql" {
		text = "LONGTEXT"
	}
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_deletion_receipts(workspace_id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,request_ref VARCHAR(191) NOT NULL,requested_at VARCHAR(40) NOT NULL,started_at VARCHAR(40) NOT NULL,completed_at VARCHAR(40) NOT NULL,status VARCHAR(16) NOT NULL,counts `+text+` NOT NULL,last_error `+text+` NULL)`)
	return err
}

const workspaceReceiptColumns = `workspace_id,workspace_key,request_ref,requested_at,started_at,completed_at,status,counts,last_error`

func scanWorkspaceReceipt(scan func(...any) error) (WorkspaceDeletionReceipt, error) {
	var receipt WorkspaceDeletionReceipt
	var requested, started, completed string
	var counts []byte
	var lastError sql.NullString
	if err := scan(&receipt.WorkspaceID, &receipt.WorkspaceKey, &receipt.RequestRef, &requested, &started, &completed, &receipt.Status, &counts, &lastError); err != nil {
		return receipt, err
	}
	receipt.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested)
	receipt.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	if completed != "" {
		if at, err := time.Parse(time.RFC3339Nano, completed); err == nil {
			receipt.CompletedAt = &at
		}
	}
	receipt.Counts = map[string]int64{}
	_ = json.Unmarshal(counts, &receipt.Counts)
	receipt.LastError = lastError.String
	return receipt, nil
}

// WorkspaceDeletionReceipt returns the receipt of the workspace with this ID.
func (s *SQLiteStore) WorkspaceDeletionReceipt(ctx context.Context, workspaceID string) (WorkspaceDeletionReceipt, bool, error) {
	receipt, err := scanWorkspaceReceipt(s.db.QueryRowContext(ctx, `SELECT `+workspaceReceiptColumns+` FROM workspace_deletion_receipts WHERE workspace_id=?`, workspaceID).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceDeletionReceipt{}, false, nil
	}
	return receipt, err == nil, err
}

// WorkspaceDeletionReceipts lists receipts, newest first; status "" lists all.
func (s *SQLiteStore) WorkspaceDeletionReceipts(ctx context.Context, status string, limit int) ([]WorkspaceDeletionReceipt, error) {
	if limit <= 0 {
		limit = 100
	}
	query, args := `SELECT `+workspaceReceiptColumns+` FROM workspace_deletion_receipts`, []any{}
	if status != "" {
		query += ` WHERE status=?`
		args = append(args, status)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY started_at DESC,workspace_id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := []WorkspaceDeletionReceipt{}
	for rows.Next() {
		receipt, err := scanWorkspaceReceipt(rows.Scan)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

// SaveWorkspaceDeletionReceipt stores the receipt's counts, status and error.
func (s *SQLiteStore) SaveWorkspaceDeletionReceipt(ctx context.Context, receipt WorkspaceDeletionReceipt) error {
	counts, _ := json.Marshal(receipt.Counts)
	completed := ""
	if receipt.CompletedAt != nil {
		completed = receipt.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	var lastError any
	if receipt.LastError != "" {
		lastError = receipt.LastError
	}
	_, err := s.db.ExecContext(ctx, `UPDATE workspace_deletion_receipts SET counts=?,status=?,completed_at=?,last_error=? WHERE workspace_id=?`, string(counts), receipt.Status, completed, lastError, receipt.WorkspaceID)
	return err
}

// WorkspacesDueForDeletion lists workspaces whose deletion was requested at
// or before cutoff.
func (s *SQLiteStore) WorkspacesDueForDeletion(cutoff time.Time) []domain.Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	due := []domain.Workspace{}
	for _, data := range s.workspaces {
		if requested := data.Workspace.DeletionRequestedAt; requested != nil && !requested.After(cutoff) {
			due = append(due, data.Workspace)
		}
	}
	slices.SortFunc(due, func(a, b domain.Workspace) int { return strings.Compare(a.URLKey, b.URLKey) })
	return due
}

// withDeletionLock serializes starting a purge with canceling the deletion,
// in this process and (with Redis coordination) across replicas.
func (s *SQLiteStore) withDeletionLock(ctx context.Context, workspaceKey string, action func() error) error {
	s.deletionMu.Lock()
	defer s.deletionMu.Unlock()
	if s.coordinator == nil {
		return action()
	}
	return s.coordinator.WithWorkspaceLock(ctx, "__workspace_deletion__:"+workspaceKey, action)
}

// BeginWorkspacePurge records that the purge of a workspace whose deletion
// was requested at or before cutoff starts now. It returns false when the
// deletion was canceled (or requested later) meanwhile. A receipt that
// already exists is returned as it is.
func (s *SQLiteStore) BeginWorkspacePurge(ctx context.Context, workspaceKey string, cutoff time.Time) (WorkspaceDeletionReceipt, bool, error) {
	var receipt WorkspaceDeletionReceipt
	started := false
	err := s.withDeletionLock(ctx, workspaceKey, func() error {
		// Another replica may have canceled the deletion: read the stored
		// workspace, not this replica's copy.
		if s.coordinator != nil {
			if err := s.ReloadWorkspace(ctx, workspaceKey); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil
				}
				return err
			}
		}
		data, ok := s.BootstrapFor(workspaceKey)
		if !ok {
			return nil
		}
		workspace := data.Workspace
		if existing, found, err := s.WorkspaceDeletionReceipt(ctx, workspace.ID); err != nil || found {
			receipt, started = existing, found
			return err
		}
		requested := workspace.DeletionRequestedAt
		if requested == nil || requested.After(cutoff) {
			return nil
		}
		now := time.Now().UTC()
		receipt = WorkspaceDeletionReceipt{WorkspaceID: workspace.ID, WorkspaceKey: workspace.URLKey, RequestRef: s.deletionRequestRef(ctx, workspace.ID), RequestedAt: requested.UTC(), StartedAt: now, Status: WorkspacePurgePurging, Counts: map[string]int64{}}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO workspace_deletion_receipts(`+workspaceReceiptColumns+`) VALUES(?,?,?,?,?,'',?,'{}',NULL) ON CONFLICT DO NOTHING`, receipt.WorkspaceID, receipt.WorkspaceKey, receipt.RequestRef, receipt.RequestedAt.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), receipt.Status); err != nil {
			return err
		}
		started = true
		return nil
	})
	return receipt, started, err
}

// deletionRequestRef is the ID of the domain event that scheduled the
// deletion, so the receipt names the request without naming its author.
func (s *SQLiteStore) deletionRequestRef(ctx context.Context, workspaceID string) string {
	var id string
	_ = s.db.QueryRowContext(ctx, `SELECT id FROM domain_events WHERE workspace_id=? AND event_type='workspace.deletion_scheduled' ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&id)
	return id
}

// workspacePurgeStarted reports whether the workspace's purge has begun.
func (s *SQLiteStore) workspacePurgeStarted(ctx context.Context, workspaceID string) (bool, error) {
	receipt, found, err := s.WorkspaceDeletionReceipt(ctx, workspaceID)
	return found && receipt.Status == WorkspacePurgePurging, err
}

// ---------------------------------------------------------------- objects

// WorkspaceObjects are what a workspace's rows point to outside the
// database, collected before the rows are deleted.
type WorkspaceObjects struct {
	// Uploads are object keys in the uploads store: attachments, assets,
	// logos and icons the workspace's records reference or own.
	Uploads []string
	// CodingObjects are object keys of coding artifacts, archived sandbox
	// logs and snapshot archives.
	CodingObjects []string
	// CodingSessionIDs name the sessions whose harness state is stored.
	CodingSessionIDs []string
	// PulseSummaryTexts are the texts of generated pulse summaries (their
	// spoken audio is cached under a hash of the text).
	PulseSummaryTexts []string
}

// uploadReference matches a relative /uploads/ link inside text (Markdown,
// HTML): the name runs to the next space, quote, bracket or query.
var uploadReference = regexp.MustCompile(`(?:^|[\s("'=\[<>])/uploads/([^\s"'()<>\[\]?#\\]+)`)

func validUploadName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 512 && !strings.ContainsAny(name, "/\\\x00")
}

// objectCollector gathers the references of decoded JSON values.
type objectCollector struct {
	uploads    map[string]bool
	pulseTexts map[string]bool
	// linksOnly: only values that are an upload URL (attachments), not
	// links inside text.
	linksOnly bool
}

func newObjectCollector() *objectCollector {
	return &objectCollector{uploads: map[string]bool{}, pulseTexts: map[string]bool{}}
}

func (c *objectCollector) addText(text string) {
	if name, ok := strings.CutPrefix(text, "/uploads/"); ok {
		if index := strings.IndexAny(name, "?#"); index >= 0 {
			name = name[:index]
		}
		if validUploadName(name) {
			c.uploads[name] = true
		}
	}
	if c.linksOnly || !strings.Contains(text, "/uploads/") {
		return
	}
	for _, match := range uploadReference.FindAllStringSubmatch(text, -1) {
		name := match[1]
		if decoded, err := url.PathUnescape(name); err == nil {
			name = decoded
		}
		if validUploadName(name) {
			c.uploads[name] = true
		}
	}
}

func (c *objectCollector) walk(value any) {
	switch item := value.(type) {
	case string:
		c.addText(item)
	case []any:
		for _, element := range item {
			c.walk(element)
		}
	case map[string]any:
		for key, element := range item {
			if key == "pulseSummary" {
				if summary, ok := element.(map[string]any); ok {
					if text, ok := summary["text"].(string); ok && text != "" {
						c.pulseTexts[text] = true
					}
				}
			}
			c.walk(element)
		}
	}
}

// add collects the references in a stored value: JSON (possibly gzipped),
// or plain text.
func (c *objectCollector) add(raw []byte) {
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		body, err := gunzipBody(raw)
		if err != nil {
			return
		}
		raw = body
	}
	if !bytes.Contains(raw, []byte("/uploads/")) && !bytes.Contains(raw, []byte(`"pulseSummary"`)) {
		return
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		if !c.linksOnly {
			c.addText(string(raw))
		}
		return
	}
	c.walk(value)
}

// scanWorkspaceRows calls visit with the data column of every row of table
// owned by the workspace, reading pages ordered by keys (the rest of the
// table's primary key) so no statement holds a long cursor.
func (s *SQLiteStore) scanWorkspaceRows(ctx context.Context, table, workspaceColumn, workspace string, keys []string, dataColumn string, visit func([]byte)) error {
	last := make([]string, len(keys))
	first := true
	for {
		query := `SELECT ` + strings.Join(keys, ",") + `,` + dataColumn + ` FROM ` + table + ` WHERE ` + workspaceColumn + `=?`
		args := []any{workspace}
		if !first {
			clauses := []string{}
			for index := range keys {
				parts := []string{}
				for prior := 0; prior < index; prior++ {
					parts = append(parts, keys[prior]+`=?`)
					args = append(args, last[prior])
				}
				parts = append(parts, keys[index]+`>?`)
				args = append(args, last[index])
				clauses = append(clauses, `(`+strings.Join(parts, ` AND `)+`)`)
			}
			query += ` AND (` + strings.Join(clauses, ` OR `) + `)`
		}
		query += ` ORDER BY ` + strings.Join(keys, ",") + ` LIMIT 200`
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		count := 0
		var page [][]byte
		for rows.Next() {
			values := make([]any, len(keys)+1)
			columns := make([]sql.NullString, len(keys))
			for index := range keys {
				values[index] = &columns[index]
			}
			var data []byte
			values[len(keys)] = &data
			if err := rows.Scan(values...); err != nil {
				rows.Close()
				return err
			}
			for index := range keys {
				last[index] = columns[index].String
			}
			page = append(page, data)
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, data := range page {
			visit(data)
		}
		if count < 200 {
			return nil
		}
		first = false
	}
}

// WorkspaceObjectReferences collects what the workspace's rows point to
// outside the database. Uploads also owned or referenced by another workspace
// are left out.
func (s *SQLiteStore) WorkspaceObjectReferences(ctx context.Context, target WorkspacePurgeTarget) (WorkspaceObjects, error) {
	var objects WorkspaceObjects
	collector := newObjectCollector()
	scans := []struct {
		table string
		keys  []string
		data  string
	}{
		{"workspace_states", []string{"workspace_id"}, "data"},
		{"workspace_metadata_records", []string{"field", "record_key"}, "data"},
		{"workspace_content_records", []string{"kind", "resource_id", "id"}, "data"},
		{"issue_records", []string{"id"}, "data"},
		{"loop_run_records", []string{"id"}, "data"},
		{"agent_runs", []string{"id"}, "payload"},
		{"application_agent_tasks", []string{"id"}, "data"},
		{"metadata_blobs", []string{"blob_key"}, "data"},
	}
	for _, scan := range scans {
		if err := s.scanWorkspaceRows(ctx, scan.table, "workspace_key", target.Key, scan.keys, scan.data, collector.add); err != nil {
			return objects, fmt.Errorf("scan %s: %w", scan.table, err)
		}
	}
	// Bodies moved to the content store (document versions, long agent
	// replies) can link uploads too.
	if content := s.db.content; content != nil && content.objects != nil {
		keys, err := s.workspaceStrings(ctx, `SELECT blob_key FROM metadata_blobs WHERE workspace_key=? AND data IS NULL`, target.Key)
		if err != nil {
			return objects, err
		}
		for _, key := range keys {
			if body, err := content.readObject(ctx, target.Key, key); err == nil {
				collector.add(body)
			}
		}
	}
	owned, err := s.workspaceStrings(ctx, `SELECT object_key FROM file_uploads WHERE workspace_key=? AND object_key<>''`, target.Key)
	if err != nil {
		return objects, err
	}
	for _, key := range owned {
		if validUploadName(key) {
			collector.uploads[key] = true
		}
	}
	foreign, err := s.foreignUploads(ctx, target.Key, collector.uploads)
	if err != nil {
		return objects, err
	}
	for name := range collector.uploads {
		if !foreign[name] {
			objects.Uploads = append(objects.Uploads, name)
		}
	}
	for text := range collector.pulseTexts {
		objects.PulseSummaryTexts = append(objects.PulseSummaryTexts, text)
	}
	coding := map[string]bool{}
	for _, query := range []string{
		`SELECT object_key FROM coding_session_artifacts WHERE workspace_key=? AND object_key<>''`,
		`SELECT logs_key FROM coding_sandboxes WHERE workspace_key=? AND logs_key<>''`,
		`SELECT logs_key FROM coding_environment_snapshots WHERE workspace_key=? AND logs_key<>''`,
		// Volume snapshots name a cluster resource, not an object.
		`SELECT storage_ref FROM coding_environment_snapshots WHERE workspace_key=? AND storage_ref<>'' AND strategy IN ('','archive')`,
	} {
		keys, err := s.workspaceStrings(ctx, query, target.Key)
		if err != nil {
			return objects, err
		}
		for _, key := range keys {
			coding[key] = true
		}
	}
	for key := range coding {
		objects.CodingObjects = append(objects.CodingObjects, key)
	}
	if objects.CodingSessionIDs, err = s.workspaceStrings(ctx, `SELECT id FROM coding_sessions WHERE workspace_key=?`, target.Key); err != nil {
		return objects, err
	}
	slices.Sort(objects.Uploads)
	slices.Sort(objects.CodingObjects)
	slices.Sort(objects.PulseSummaryTexts)
	return objects, nil
}

// WorkspaceUserIDs lists the workspace's current and former members.
func (s *SQLiteStore) WorkspaceUserIDs(ctx context.Context, workspaceID string) ([]string, error) {
	users := map[string]bool{}
	for _, query := range []string{
		`SELECT user_id FROM workspace_memberships WHERE workspace_id=?`,
		`SELECT user_id FROM workspace_departures WHERE workspace_id=?`,
		`SELECT user_id FROM scim_user_roles WHERE workspace_id=?`,
	} {
		ids, err := s.workspaceStrings(ctx, query, workspaceID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			users[id] = true
		}
	}
	list := make([]string, 0, len(users))
	for id := range users {
		list = append(list, id)
	}
	slices.Sort(list)
	return list, nil
}

// DeleteConnectorSecrets deletes the sealed secrets with these IDs and
// reports how many existed.
func (s *SQLiteStore) DeleteConnectorSecrets(ctx context.Context, ids []string) (int64, error) {
	var deleted int64
	for start := 0; start < len(ids); start += 250 {
		clause, args := bindList("id", ids[start:min(start+250, len(ids))])
		result, err := s.db.ExecContext(ctx, `DELETE FROM connector_secrets WHERE `+clause, args...)
		if err != nil {
			return deleted, err
		}
		deleted += rowsAffected(result)
	}
	return deleted, nil
}

// foreignUploads reports which of names another workspace owns (a
// file_uploads row) or attaches to an issue, so purging this workspace does
// not delete a file another workspace still shows.
func (s *SQLiteStore) foreignUploads(ctx context.Context, workspace string, names map[string]bool) (map[string]bool, error) {
	foreign := map[string]bool{}
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	slices.Sort(list)
	for start := 0; start < len(list); start += 250 {
		batch := list[start:min(start+250, len(list))]
		clause, args := bindList("object_key", batch)
		owners, err := s.db.QueryContext(ctx, `SELECT object_key FROM file_uploads WHERE workspace_key<>? AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for owners.Next() {
			var key string
			if err := owners.Scan(&key); err != nil {
				owners.Close()
				return nil, err
			}
			foreign[key] = true
		}
		err = owners.Err()
		owners.Close()
		if err != nil {
			return nil, err
		}
		hashes := make([]string, len(batch))
		byHash := map[string]string{}
		for index, name := range batch {
			hashes[index] = attachmentKey("/uploads/" + name)
			byHash[hashes[index]] = name
		}
		clause, args = bindList("url_hash", hashes)
		attached, err := s.db.QueryContext(ctx, `SELECT DISTINCT url_hash FROM issue_attachment_records WHERE workspace_key<>? AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for attached.Next() {
			var hash string
			if err := attached.Scan(&hash); err != nil {
				attached.Close()
				return nil, err
			}
			foreign[byHash[hash]] = true
		}
		err = attached.Err()
		attached.Close()
		if err != nil {
			return nil, err
		}
	}
	return foreign, nil
}

// uploadsAttachedInWorkspace reports which upload names an issue of the
// workspace still attaches.
func (s *SQLiteStore) uploadsAttachedInWorkspace(ctx context.Context, workspace string, names []string) (map[string]bool, error) {
	attached := map[string]bool{}
	for start := 0; start < len(names); start += 250 {
		batch := names[start:min(start+250, len(names))]
		byHash := map[string]string{}
		hashes := make([]string, len(batch))
		for index, name := range batch {
			hashes[index] = attachmentKey("/uploads/" + name)
			byHash[hashes[index]] = name
		}
		clause, args := bindList("url_hash", hashes)
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT url_hash FROM issue_attachment_records WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var hash string
			if err := rows.Scan(&hash); err != nil {
				rows.Close()
				return nil, err
			}
			attached[byHash[hash]] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return attached, nil
}

// AttachmentUploads returns the upload names a JSON value attaches: string
// values that are an /uploads/ URL. Links inside text (an image pasted into
// a description) are left out, because text is copied between records.
func AttachmentUploads(raw []byte) []string {
	collector := newObjectCollector()
	collector.linksOnly = true
	collector.add(raw)
	names := make([]string, 0, len(collector.uploads))
	for name := range collector.uploads {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// ReleasableUploads returns the names among names that the workspace's
// issues no longer attach and no other workspace owns or attaches, so their
// files can be deleted once the records that attached them are purged.
func (s *SQLiteStore) ReleasableUploads(ctx context.Context, workspace string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	attached, err := s.uploadsAttachedInWorkspace(ctx, workspace, names)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, name := range names {
		set[name] = true
	}
	foreign, err := s.foreignUploads(ctx, workspace, set)
	if err != nil {
		return nil, err
	}
	releasable := []string{}
	for _, name := range names {
		if !attached[name] && !foreign[name] && validUploadName(name) {
			releasable = append(releasable, name)
		}
	}
	return releasable, nil
}

// DeleteWorkspaceFileUploads deletes the workspace's upload records of these
// objects (their files are deleted by the caller).
func (s *SQLiteStore) DeleteWorkspaceFileUploads(ctx context.Context, workspace string, objectKeys []string) error {
	for start := 0; start < len(objectKeys); start += 250 {
		clause, args := bindList("object_key", objectKeys[start:min(start+250, len(objectKeys))])
		if _, err := s.db.ExecContext(ctx, `DELETE FROM file_uploads WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) workspaceStrings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if value.String != "" {
			values = append(values, value.String)
		}
	}
	return values, rows.Err()
}

// ---------------------------------------------------------------- catalog

// RemoveWorkspaceFromCatalog makes the workspace disappear: its state row,
// memberships and invitations are deleted, accounts that last opened it move
// on, and every replica drops its copy. Its other rows stay until
// PurgeWorkspaceRows deletes them. Deleted rows are added to counts (which
// may be nil).
func (s *SQLiteStore) RemoveWorkspaceFromCatalog(ctx context.Context, workspaceKey string, counts map[string]int64) error {
	if counts == nil {
		counts = map[string]int64{}
	}
	if s.coordinator == nil {
		return s.removeWorkspaceFromCatalog(ctx, workspaceKey, counts)
	}
	var workspaceID string
	err := s.coordinator.WithWorkspaceLock(ctx, "__workspace_catalog__", func() error {
		if err := s.ReloadAllWorkspaces(ctx); err != nil {
			return err
		}
		if data, ok := s.BootstrapFor(workspaceKey); ok {
			workspaceID = data.Workspace.ID
		}
		return s.removeWorkspaceFromCatalog(ctx, workspaceKey, counts)
	})
	if err == nil {
		s.publishWorkspaceEvent(ctx, workspaceKey, "workspace.deleted", workspaceID, map[string]string{"urlKey": workspaceKey})
	}
	return err
}

func (s *SQLiteStore) removeWorkspaceFromCatalog(ctx context.Context, workspaceKey string, counts map[string]int64) error {
	s.lockWorkspaceWrites()
	defer s.unlockWorkspaceWrites()
	data, ok := s.workspaces[workspaceKey]
	if !ok {
		return fmt.Errorf("workspace not found")
	}
	remaining := map[string]domain.Bootstrap{}
	for key, value := range s.workspaces {
		if key != workspaceKey {
			remaining[key] = value
		}
	}
	nextWorkspaceKey := firstWorkspaceKey(remaining)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	deleted := map[string]int64{}
	result, err := tx.ExecContext(ctx, `DELETE FROM workspace_states WHERE workspace_key = ?`, workspaceKey)
	if err != nil {
		return err
	}
	deleted["workspace_states"] = rowsAffected(result)
	for _, table := range []string{"team_memberships", "workspace_memberships", "workspace_departures", "workspace_invitations"} {
		result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_id = ?`, data.Workspace.ID)
		if err != nil {
			return err
		}
		deleted[table] = rowsAffected(result)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_account_state SET last_workspace_key='',updated_at=? WHERE last_workspace_key=?`, time.Now().UTC().Format(time.RFC3339Nano), workspaceKey); err != nil {
		return err
	}
	now := time.Now().UTC()
	viewerRaw, _ := json.Marshal(s.viewer)
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_state(id,last_workspace_key,viewer,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET last_workspace_key=excluded.last_workspace_key,viewer=excluded.viewer,updated_at=excluded.updated_at`, nextWorkspaceKey, viewerRaw, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for table, n := range deleted {
		if n != 0 {
			counts[table] += n
		}
	}
	s.forgetChatSnapshotFlag(workspaceKey)
	s.issueGroupCounts.drop(workspaceKey)
	s.workspaces = remaining
	s.lastWorkspaceKey = nextWorkspaceKey
	return nil
}

// ---------------------------------------------------------------- rows

// PurgeWorkspaceRows deletes every row the workspace owns, the content-store
// bodies its metadata moved out of rows and its cached copies, adding what it
// deleted to counts. It runs after RemoveWorkspaceFromCatalog, deletes in
// small batches and can be repeated: an interrupted purge finishes on the
// next call.
func (s *SQLiteStore) PurgeWorkspaceRows(ctx context.Context, target WorkspacePurgeTarget, counts map[string]int64) error {
	if target.Key == "" || target.ID == "" {
		return fmt.Errorf("purge needs the workspace URL key and ID")
	}
	if data, ok := s.BootstrapFor(target.Key); ok {
		if data.Workspace.ID != target.ID {
			return ErrWorkspaceKeyReused
		}
		return fmt.Errorf("workspace %s is still in the catalog", target.ID)
	}
	add := func(name string, n int64) {
		if n != 0 {
			counts[name] += n
		}
	}
	// Chat integrations, their credentials, rate limits and pending OAuth.
	if validChatKey(target.Key) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		err = deleteChatWorkspace(ctx, tx, target.Key)
		if err == nil {
			err = tx.Commit()
		}
		_ = tx.Rollback()
		if err != nil {
			return fmt.Errorf("chat: %w", err)
		}
	}
	// Code host account tokens are sealed under the account's ID.
	accounts, err := s.workspaceStrings(ctx, `SELECT id FROM code_host_accounts WHERE workspace_key=?`, target.Key)
	if err != nil {
		return err
	}
	for _, id := range accounts {
		result, err := s.db.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, CodeHostAccountSecretID(id))
		if err != nil {
			return err
		}
		add("connector_secrets", rowsAffected(result))
	}
	// Application agent activity logs are keyed by their task.
	for {
		tasks, err := s.workspaceStrings(ctx, `SELECT id FROM application_agent_tasks WHERE workspace_key=? ORDER BY id LIMIT 200`, target.Key)
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			break
		}
		clause, args := bindList("session_id", tasks)
		result, err := s.db.ExecContext(ctx, `DELETE FROM application_agent_activities WHERE `+clause, args...)
		if err != nil {
			return err
		}
		add("application_agent_activities", rowsAffected(result))
		clause, args = bindList("id", tasks)
		result, err = s.db.ExecContext(ctx, `DELETE FROM application_agent_tasks WHERE workspace_key=? AND `+clause, append([]any{target.Key}, args...)...)
		if err != nil {
			return err
		}
		add("application_agent_tasks", rowsAffected(result))
	}
	// OAuth grants issued for the workspace.
	for _, table := range []string{"oauth_authorization_codes", "oauth_refresh_tokens"} {
		result, err := s.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+s.jsonText("data", "workspaceKey")+`=?`, target.Key)
		if err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
		add(table, rowsAffected(result))
	}
	// A legacy single-workspace row would bring the workspace back when no
	// other workspace is left.
	var legacy []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_state WHERE id=1`).Scan(&legacy); err == nil {
		var stored struct {
			Workspace domain.Workspace `json:"workspace"`
		}
		if json.Unmarshal(legacy, &stored) == nil && (stored.Workspace.ID == target.ID || stored.Workspace.ID == "" && stored.Workspace.URLKey == target.Key) {
			result, err := s.db.ExecContext(ctx, `DELETE FROM workspace_state WHERE id=1`)
			if err != nil {
				return err
			}
			add("workspace_state", rowsAffected(result))
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Offloaded bodies in the content store, then their rows.
	if deleted, err := s.purgeWorkspaceContentObjects(ctx, target.Key); err != nil {
		return err
	} else {
		add("objects.content", deleted)
	}
	for _, table := range workspacePurgeTables {
		value := target.Key
		if table.byID {
			value = target.ID
		}
		deleted, err := s.deleteWorkspaceRows(ctx, table.table, table.column, value)
		add(table.table, deleted)
		if err != nil {
			return fmt.Errorf("%s: %w", table.table, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE auth_account_state SET last_workspace_key='',updated_at=? WHERE last_workspace_key=?`, time.Now().UTC().Format(time.RFC3339Nano), target.Key); err != nil {
		return err
	}
	s.dropMetadataCache(ctx, target.Key)
	s.forgetChatSnapshotFlag(target.Key)
	s.issueGroupCounts.drop(target.Key)
	return nil
}

// purgeWorkspaceContentObjects deletes the content-store bodies of the
// workspace's offloaded metadata, a page of rows at a time; the rows go with
// the other tables.
func (s *SQLiteStore) purgeWorkspaceContentObjects(ctx context.Context, workspace string) (int64, error) {
	content := s.db.content
	if content == nil {
		return 0, nil
	}
	var deleted int64
	last := ""
	for {
		keys, err := s.workspaceStrings(ctx, `SELECT blob_key FROM metadata_blobs WHERE workspace_key=? AND blob_key>? ORDER BY blob_key LIMIT 500`, workspace, last)
		if err != nil {
			return deleted, err
		}
		for _, key := range keys {
			content.cache.remove(workspace + "/" + key)
			if content.objects == nil {
				continue
			}
			if err := content.objects.Delete(ctx, offloadedObjectKey(workspace, key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return deleted, fmt.Errorf("delete offloaded content: %w", err)
			}
			deleted++
		}
		if len(keys) < 500 {
			return deleted, nil
		}
		last = keys[len(keys)-1]
	}
}

// deleteWorkspaceRows deletes a table's rows whose column equals value, one
// batch per statement, and returns how many it deleted.
func (s *SQLiteStore) deleteWorkspaceRows(ctx context.Context, table, column, value string) (int64, error) {
	var statement string
	switch s.dialect {
	case "mysql":
		statement = `DELETE FROM ` + table + ` WHERE ` + column + `=? LIMIT ` + fmt.Sprint(workspacePurgeBatch)
	case "postgres":
		statement = `DELETE FROM ` + table + ` WHERE ctid IN (SELECT ctid FROM ` + table + ` WHERE ` + column + `=? LIMIT ` + fmt.Sprint(workspacePurgeBatch) + `)`
	default:
		statement = `DELETE FROM ` + table + ` WHERE rowid IN (SELECT rowid FROM ` + table + ` WHERE ` + column + `=? LIMIT ` + fmt.Sprint(workspacePurgeBatch) + `)`
	}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		result, err := s.db.ExecContext(ctx, statement, value)
		if err != nil {
			return total, err
		}
		deleted := rowsAffected(result)
		total += deleted
		if deleted < workspacePurgeBatch {
			return total, nil
		}
	}
}

func rowsAffected(result sql.Result) int64 {
	if result == nil {
		return 0
	}
	n, err := result.RowsAffected()
	if err != nil || n < 0 {
		return 0
	}
	return n
}
