package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
)

func (s *SQLiteStore) EnsureChatIntegrationsSchema(ctx context.Context) error {
	blob := "BLOB"
	if s.dialect == "mysql" {
		blob = "LONGBLOB"
	}
	if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS chat_integrations (connection_id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,provider VARCHAR(32) NOT NULL,app_id VARCHAR(191) NOT NULL,tenant_key VARCHAR(191) NOT NULL,payload ` + blob + ` NOT NULL,UNIQUE(workspace_key,provider),UNIQUE(provider,app_id,tenant_key))`,
		`CREATE TABLE IF NOT EXISTS chat_bindings (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,chat_id VARCHAR(191) NOT NULL,payload ` + blob + ` NOT NULL,UNIQUE(connection_id,chat_id))`,
		`CREATE TABLE IF NOT EXISTS chat_identities (workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,user_id VARCHAR(191) NOT NULL,open_id VARCHAR(191) NOT NULL,payload ` + blob + ` NOT NULL,PRIMARY KEY(workspace_key,connection_id,user_id),UNIQUE(connection_id,open_id))`,
		`CREATE TABLE IF NOT EXISTS chat_actions (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,user_id VARCHAR(191) NOT NULL,expires_at BIGINT NOT NULL,payload ` + blob + ` NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS chat_messages (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,message_id VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,created_at BIGINT NOT NULL,payload ` + blob + ` NOT NULL,UNIQUE(connection_id,message_id))`,
		`CREATE TABLE IF NOT EXISTS chat_request_limits (scope_key VARCHAR(191) PRIMARY KEY,next_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS chat_jobs (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,kind VARCHAR(64) NOT NULL,dedup_key VARCHAR(191) NOT NULL,payload ` + blob + ` NOT NULL,status VARCHAR(32) NOT NULL,attempts INTEGER NOT NULL,run_at BIGINT NOT NULL,lease_until BIGINT NOT NULL,last_error VARCHAR(1000) NOT NULL,UNIQUE(connection_id,kind,dedup_key))`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS chat_jobs_due ON chat_jobs(workspace_key,kind,status,run_at)`,
		`CREATE INDEX IF NOT EXISTS chat_jobs_retention ON chat_jobs(status,run_at)`,
		`CREATE INDEX IF NOT EXISTS chat_messages_issue ON chat_messages(connection_id,issue_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS chat_messages_retention ON chat_messages(created_at)`,
		`CREATE INDEX IF NOT EXISTS chat_actions_expiry ON chat_actions(expires_at)`,
	} {
		if s.dialect == "mysql" {
			statement = strings.Replace(statement, "CREATE INDEX IF NOT EXISTS", "CREATE INDEX", 1)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

func validChatKey(values ...string) bool {
	for _, value := range values {
		if value == "" || len(value) > 191 {
			return false
		}
	}
	return true
}

func (s *SQLiteStore) PutChatIntegration(ctx context.Context, item domain.ChatIntegrationRecord) error {
	if !validChatKey(item.WorkspaceKey, item.ConnectionID, item.AppID, item.Provider) || len(item.TenantKey) > 191 || (item.Provider != "feishu" && item.Provider != "lark") {
		return ErrIssueQuery
	}
	item.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var workspace, provider string
	err = tx.QueryRowContext(ctx, `SELECT workspace_key,provider FROM chat_integrations WHERE connection_id=?`, item.ConnectionID).Scan(&workspace, &provider)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_integrations(connection_id,workspace_key,provider,app_id,tenant_key,payload) VALUES(?,?,?,?,?,?)`, item.ConnectionID, item.WorkspaceKey, item.Provider, item.AppID, item.TenantKey, raw)
	} else if err == nil {
		if workspace != item.WorkspaceKey || provider != item.Provider {
			return ErrAuthForbidden
		}
		_, err = tx.ExecContext(ctx, `UPDATE chat_integrations SET app_id=?,tenant_key=?,payload=? WHERE connection_id=? AND workspace_key=? AND provider=?`, item.AppID, item.TenantKey, raw, item.ConnectionID, item.WorkspaceKey, item.Provider)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ChatIntegration(ctx context.Context, workspace, provider string) (domain.ChatIntegrationRecord, error) {
	var raw []byte
	var item domain.ChatIntegrationRecord
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_integrations WHERE workspace_key=? AND provider=?`, workspace, provider).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &item)
	}
	return item, err
}

func (s *SQLiteStore) ChatIntegrationByID(ctx context.Context, provider, id string) (domain.ChatIntegrationRecord, error) {
	var raw []byte
	var item domain.ChatIntegrationRecord
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_integrations WHERE provider=? AND connection_id=?`, provider, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &item)
	}
	return item, err
}

// chatSnapshotsNeeded reads the authoritative app records independently from
// legacy connection display metadata, which can lag after a partial setup.
func (s *SQLiteStore) chatSnapshotsNeeded(ctx context.Context, workspace string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM chat_integrations WHERE workspace_key=? LIMIT 2`, workspace)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var record domain.ChatIntegrationRecord
		if err = rows.Scan(&raw); err != nil {
			return false, err
		}
		if err = json.Unmarshal(raw, &record); err != nil {
			return false, err
		}
		if record.Connected {
			return true, nil
		}
	}
	return false, rows.Err()
}

// DeleteChatIntegration atomically revokes bindings, queued work and actions.
func (s *SQLiteStore) DeleteChatIntegration(ctx context.Context, workspace, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id IN (SELECT id FROM chat_actions WHERE workspace_key=? AND connection_id=? AND id LIKE 'chat_oauth_%')`, workspace, id); err != nil {
		return err
	}
	for _, table := range []string{"chat_bindings", "chat_identities", "chat_actions", "chat_messages", "chat_jobs", "chat_integrations"} {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_key=? AND connection_id=?`, workspace, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) ChatBindings(ctx context.Context, workspace, connection string) ([]domain.ChatBinding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM chat_bindings WHERE workspace_key=? AND connection_id=? ORDER BY id`, workspace, connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChatBinding{}
	for rows.Next() {
		var raw []byte
		var item domain.ChatBinding
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) PutChatBinding(ctx context.Context, workspace string, item domain.ChatBinding) error {
	if !validChatKey(workspace, item.ID, item.ConnectionID, item.ChatID, item.TeamID) {
		return ErrIssueQuery
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = chatRequireConnection(ctx, tx, workspace, item.ConnectionID, ""); err != nil {
		return err
	}
	var owner, connection string
	err = tx.QueryRowContext(ctx, `SELECT workspace_key,connection_id FROM chat_bindings WHERE id=?`, item.ID).Scan(&owner, &connection)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_bindings(id,workspace_key,connection_id,chat_id,payload) VALUES(?,?,?,?,?)`, item.ID, workspace, item.ConnectionID, item.ChatID, raw)
	} else if err == nil {
		if owner != workspace || connection != item.ConnectionID {
			return ErrAuthForbidden
		}
		_, err = tx.ExecContext(ctx, `UPDATE chat_bindings SET chat_id=?,payload=? WHERE id=? AND workspace_key=? AND connection_id=?`, item.ChatID, raw, item.ID, workspace, item.ConnectionID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) DeleteChatBinding(ctx context.Context, workspace, connection, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_bindings WHERE workspace_key=? AND connection_id=? AND id=?`, workspace, connection, id)
	return err
}

func (s *SQLiteStore) PutChatIdentity(ctx context.Context, workspace, connection string, item domain.ChatIdentity) error {
	if !validChatKey(workspace, connection, item.UserID, item.OpenID, item.TenantKey) {
		return ErrIssueQuery
	}
	raw, err := json.Marshal(persistedChatIdentity{item, item.AuthProvider, item.AuthIssuer, item.MFAVerifiedAt})
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = chatRequireConnection(ctx, tx, workspace, connection, item.TenantKey); err != nil {
		return err
	}
	var owner string
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM chat_identities WHERE connection_id=? AND open_id=?`, connection, item.OpenID).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && owner != item.UserID {
		return ErrAuthForbidden
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM chat_identities WHERE workspace_key=? AND connection_id=? AND user_id=?`, workspace, connection, item.UserID).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_identities(workspace_key,connection_id,user_id,open_id,payload) VALUES(?,?,?,?,?)`, workspace, connection, item.UserID, item.OpenID, raw)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE chat_identities SET open_id=?,payload=? WHERE workspace_key=? AND connection_id=? AND user_id=?`, item.OpenID, raw, workspace, connection, item.UserID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func chatRequireConnection(ctx context.Context, tx *sqlTx, workspace, connection, tenant string) error {
	query := `SELECT tenant_key FROM chat_integrations WHERE workspace_key=? AND connection_id=?`
	if tx.dialect != "sqlite" {
		query += ` FOR UPDATE`
	}
	var actual string
	err := tx.QueryRowContext(ctx, query, workspace, connection).Scan(&actual)
	if err != nil {
		return err
	}
	if tenant != "" && tenant != actual {
		return ErrAuthForbidden
	}
	return nil
}

func (s *SQLiteStore) ChatIdentityForUser(ctx context.Context, workspace, connection, userID string) (domain.ChatIdentity, error) {
	return s.chatIdentity(ctx, workspace, connection, "user_id", userID)
}
func (s *SQLiteStore) ChatIdentityForOpenID(ctx context.Context, workspace, connection, openID string) (domain.ChatIdentity, error) {
	return s.chatIdentity(ctx, workspace, connection, "open_id", openID)
}
func (s *SQLiteStore) chatIdentity(ctx context.Context, workspace, connection, field, value string) (domain.ChatIdentity, error) {
	var raw []byte
	var saved persistedChatIdentity
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_identities WHERE workspace_key=? AND connection_id=? AND `+field+`=?`, workspace, connection, value).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &saved)
	}
	item := saved.ChatIdentity
	item.AuthProvider = saved.AuthProvider
	item.AuthIssuer = saved.AuthIssuer
	item.MFAVerifiedAt = saved.MFAVerifiedAt
	return item, err
}

type persistedChatIdentity struct {
	domain.ChatIdentity
	AuthProvider  string     `json:"authProvider"`
	AuthIssuer    string     `json:"authIssuer"`
	MFAVerifiedAt *time.Time `json:"mfaVerifiedAt,omitempty"`
}

func (s *SQLiteStore) DeleteChatIdentity(ctx context.Context, workspace, connection, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id IN (SELECT id FROM chat_actions WHERE workspace_key=? AND connection_id=? AND user_id=? AND id LIKE 'chat_oauth_%')`, workspace, connection, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM chat_actions WHERE workspace_key=? AND connection_id=? AND user_id=?`, workspace, connection, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM chat_identities WHERE workspace_key=? AND connection_id=? AND user_id=?`, workspace, connection, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) PutChatAction(ctx context.Context, item domain.ChatAction) error {
	if !validChatKey(item.ID, item.WorkspaceKey, item.ConnectionID) || len(item.Payload) > 65536 || !item.ExpiresAt.After(time.Now()) || item.ExpiresAt.After(time.Now().Add(31*24*time.Hour)) {
		return ErrIssueQuery
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO chat_actions(id,workspace_key,connection_id,user_id,expires_at,payload) SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM chat_integrations WHERE workspace_key=? AND connection_id=?) ON CONFLICT(id) DO NOTHING`, item.ID, item.WorkspaceKey, item.ConnectionID, item.UserID, item.ExpiresAt.UnixMilli(), raw, item.WorkspaceKey, item.ConnectionID)
	return err
}
func (s *SQLiteStore) GetChatAction(ctx context.Context, workspace, connection, id string) (domain.ChatAction, error) {
	var raw []byte
	var item domain.ChatAction
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_actions WHERE workspace_key=? AND connection_id=? AND id=? AND expires_at>?`, workspace, connection, id, time.Now().UnixMilli()).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &item)
	}
	return item, err
}
func (s *SQLiteStore) ConsumeChatAction(ctx context.Context, workspace, connection, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM chat_actions WHERE workspace_key=? AND connection_id=? AND id=? AND expires_at>?`, workspace, connection, id, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *SQLiteStore) UpdateChatAction(ctx context.Context, item domain.ChatAction) error {
	if len(item.Payload) > 65536 || !json.Valid(item.Payload) {
		return ErrIssueQuery
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE chat_actions SET payload=? WHERE workspace_key=? AND connection_id=? AND id=? AND expires_at>?`, raw, item.WorkspaceKey, item.ConnectionID, item.ID, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) PutChatMessage(ctx context.Context, item domain.ChatMessage) error {
	if !validChatKey(item.ID, item.WorkspaceKey, item.ConnectionID, item.ChatID, item.MessageID, item.IssueID) {
		return ErrIssueQuery
	}
	item.UpdatedAt = time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = item.UpdatedAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = chatRequireConnection(ctx, tx, item.WorkspaceKey, item.ConnectionID, ""); err != nil {
		return err
	}
	var existing []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM chat_messages WHERE workspace_key=? AND connection_id=? AND message_id=?`, item.WorkspaceKey, item.ConnectionID, item.MessageID).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		raw, e := json.Marshal(item)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_messages(id,workspace_key,connection_id,message_id,issue_id,created_at,payload) VALUES(?,?,?,?,?,?,?)`, item.ID, item.WorkspaceKey, item.ConnectionID, item.MessageID, item.IssueID, item.CreatedAt.UnixMilli(), raw)
	} else if err == nil {
		var previous domain.ChatMessage
		if e := json.Unmarshal(existing, &previous); e != nil {
			return e
		}
		item.CreatedAt = previous.CreatedAt
		item.ID = previous.ID
		raw, e := json.Marshal(item)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `UPDATE chat_messages SET issue_id=?,payload=? WHERE workspace_key=? AND connection_id=? AND message_id=?`, item.IssueID, raw, item.WorkspaceKey, item.ConnectionID, item.MessageID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) ChatMessagesForIssue(ctx context.Context, workspace, connection, issueID string) ([]domain.ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM chat_messages WHERE workspace_key=? AND connection_id=? AND issue_id=? AND created_at>=? ORDER BY created_at DESC,id LIMIT 100`, workspace, connection, issueID, time.Now().Add(-14*24*time.Hour).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChatMessage{}
	for rows.Next() {
		var raw []byte
		var item domain.ChatMessage
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
func (s *SQLiteStore) ChatMessage(ctx context.Context, workspace, connection, messageID string) (domain.ChatMessage, error) {
	var raw []byte
	var item domain.ChatMessage
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_messages WHERE workspace_key=? AND connection_id=? AND message_id=?`, workspace, connection, messageID).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &item)
	}
	return item, err
}

func (s *SQLiteStore) EnqueueChatJob(ctx context.Context, item domain.ChatJob) (bool, error) {
	if !validChatKey(item.ID, item.WorkspaceKey, item.ConnectionID, item.Kind, item.DedupKey) || len(item.Payload) > 1<<20 || !json.Valid(item.Payload) {
		return false, ErrIssueQuery
	}
	if item.RunAt.IsZero() {
		item.RunAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO chat_jobs(id,workspace_key,connection_id,kind,dedup_key,payload,status,attempts,run_at,lease_until,last_error) SELECT ?,?,?,?,?,?,'pending',0,?,0,'' WHERE EXISTS(SELECT 1 FROM chat_integrations WHERE workspace_key=? AND connection_id=?) ON CONFLICT DO NOTHING`, item.ID, item.WorkspaceKey, item.ConnectionID, item.Kind, item.DedupKey, []byte(item.Payload), item.RunAt.UnixMilli(), item.WorkspaceKey, item.ConnectionID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *SQLiteStore) ClaimChatJobs(ctx context.Context, workspace, kind string, limit int, now time.Time) ([]domain.ChatJob, error) {
	limit = max(1, min(limit, 100))
	if _, err := s.db.ExecContext(ctx, `UPDATE chat_jobs SET status='failed',last_error='Attempt limit reached after expired worker lease',lease_until=0,run_at=? WHERE workspace_key=? AND kind=? AND status='processing' AND attempts>=8 AND lease_until<=?`, now.UnixMilli(), workspace, kind, now.UnixMilli()); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,connection_id,dedup_key,payload,status,attempts,run_at,lease_until,last_error FROM chat_jobs WHERE workspace_key=? AND kind=? AND attempts<8 AND run_at<=? AND (status='pending' OR (status='processing' AND lease_until<=?)) ORDER BY run_at,id LIMIT ?`, workspace, kind, now.UnixMilli(), now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	var candidates []domain.ChatJob
	for rows.Next() {
		item := domain.ChatJob{WorkspaceKey: workspace, Kind: kind}
		var run, lease int64
		if err = rows.Scan(&item.ID, &item.ConnectionID, &item.DedupKey, &item.Payload, &item.Status, &item.Attempts, &run, &lease, &item.LastError); err != nil {
			rows.Close()
			return nil, err
		}
		item.RunAt = time.UnixMilli(run)
		item.LeaseUntil = time.UnixMilli(lease)
		candidates = append(candidates, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	claimed := []domain.ChatJob{}
	for _, item := range candidates {
		lease := now.Add(time.Minute).UnixMilli()
		result, err := s.db.ExecContext(ctx, `UPDATE chat_jobs SET status='processing',attempts=attempts+1,lease_until=? WHERE id=? AND workspace_key=? AND attempts=? AND (status='pending' OR (status='processing' AND lease_until<=?))`, lease, item.ID, workspace, item.Attempts, now.UnixMilli())
		if err != nil {
			return nil, err
		}
		n, _ := result.RowsAffected()
		if n == 1 {
			item.Status = "processing"
			item.Attempts++
			item.LeaseUntil = time.UnixMilli(lease)
			claimed = append(claimed, item)
		}
	}
	return claimed, nil
}

func (s *SQLiteStore) FinishChatJob(ctx context.Context, item domain.ChatJob, status string, runAt time.Time, lastError string) error {
	if status != "pending" && status != "completed" && status != "failed" {
		return ErrIssueQuery
	}
	if item.Attempts >= 8 && status == "pending" {
		status = "failed"
	}
	if status != "pending" {
		runAt = time.Now().UTC()
	}
	if len(lastError) > 1000 {
		lastError = lastError[:1000]
	}
	result, err := s.db.ExecContext(ctx, `UPDATE chat_jobs SET status=?,run_at=?,lease_until=0,last_error=? WHERE id=? AND workspace_key=? AND connection_id=? AND status='processing' AND lease_until=? AND attempts=?`, status, runAt.UnixMilli(), lastError, item.ID, item.WorkspaceKey, item.ConnectionID, item.LeaseUntil.UnixMilli(), item.Attempts)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("chat job claim no longer owned")
	}
	return nil
}

// PurgeChatHistory removes a bounded page from each retention class. Completed
// inbox IDs survive at least 30 days so delayed platform retries stay deduped.
func (s *SQLiteStore) PurgeChatHistory(ctx context.Context, now time.Time) error {
	if err := s.purgeChatMutationReceipts(ctx, now); err != nil {
		return err
	}
	classes := []struct {
		table, key, predicate string
		before                int64
	}{
		{"chat_actions", "id", "expires_at<=?", now.UnixMilli()},
		{"chat_jobs", "id", "status IN ('completed','failed') AND run_at<?", now.Add(-30 * 24 * time.Hour).UnixMilli()},
		{"chat_messages", "id", "created_at<?", now.Add(-30 * 24 * time.Hour).UnixMilli()},
		{"chat_request_limits", "scope_key", "next_at<?", now.Add(-24 * time.Hour).UnixMilli()},
	}
	for _, item := range classes {
		rows, err := s.db.QueryContext(ctx, `SELECT `+item.key+` FROM `+item.table+` WHERE `+item.predicate+` LIMIT 500`, item.before)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			continue
		}
		if item.table == "chat_actions" {
			for _, id := range ids {
				if strings.HasPrefix(id, "chat_oauth_") {
					if err = s.DeleteConnectorSecret(ctx, id); err != nil {
						return err
					}
				}
			}
		}
		clause, args := bindList(item.key, ids)
		if _, err = s.db.ExecContext(ctx, `DELETE FROM `+item.table+` WHERE `+clause+` AND `+item.predicate, append(args, item.before)...); err != nil {
			return err
		}
	}
	return nil
}

func ChatRecordID(parts ...string) string {
	return fmt.Sprintf("chat_%x", sha256.Sum256([]byte(strings.Join(parts, "\x00"))))
}

// ReserveChatRequest serializes an application's outbound message requests
// across replicas. The conservative application-wide limit also protects the
// platform's lower per-group quota without storing group membership data.
func (s *SQLiteStore) ReserveChatRequest(ctx context.Context, scope string, interval time.Duration, now time.Time) (time.Time, error) {
	if !validChatKey(scope) || interval < time.Millisecond {
		return time.Time{}, ErrIssueQuery
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO chat_request_limits(scope_key,next_at) VALUES(?,?) ON CONFLICT(scope_key) DO NOTHING`, scope, now.Add(interval).UnixMilli())
	if err != nil {
		return time.Time{}, err
	}
	n, _ := result.RowsAffected()
	if n == 1 {
		return now, nil
	}
	for attempt := 0; attempt < 16; attempt++ {
		var previous int64
		if err = s.db.QueryRowContext(ctx, `SELECT next_at FROM chat_request_limits WHERE scope_key=?`, scope).Scan(&previous); err != nil {
			return time.Time{}, err
		}
		next := max(now.UnixMilli(), previous)
		if next > now.Add(15*time.Second).UnixMilli() {
			return time.Time{}, errors.New("Chat message rate queue is full")
		}
		result, err = s.db.ExecContext(ctx, `UPDATE chat_request_limits SET next_at=? WHERE scope_key=? AND next_at=?`, next+interval.Milliseconds(), scope, previous)
		if err != nil {
			return time.Time{}, err
		}
		n, _ = result.RowsAffected()
		if n == 1 {
			return time.UnixMilli(next), nil
		}
	}
	return time.Time{}, errors.New("Chat message rate reservation busy")
}
