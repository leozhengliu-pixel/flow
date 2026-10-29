package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"flow/api/internal/domain"
)

// Chat intake requests: the request made from a chat message, one per
// message of a connection (UNIQUE(connection_id,message_id)), so the same
// message never creates two requests whichever way and however often members
// ask. The record also routes the request's issue back to the message's
// thread (by issue).

func (s *SQLiteStore) createChatIntakeRequestSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS chat_intake_requests (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,message_id VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,created_at BIGINT NOT NULL,payload `+chatPayloadColumn(s.dialect)+` NOT NULL,UNIQUE(connection_id,message_id))`); err != nil {
		return err
	}
	return s.createChatIndexes(ctx, `CREATE INDEX IF NOT EXISTS chat_intake_requests_issue ON chat_intake_requests(connection_id,issue_id)`)
}

// ChatIntakeRequestID is the stable identity of the request made from a
// message.
func ChatIntakeRequestID(connectionID, messageID string) string {
	return "chat_request_" + ChatRecordID("intake-request", connectionID, messageID)[5:]
}

func validChatIntakeRequest(workspace string, item domain.ChatIntakeRequest) bool {
	return validChatKey(workspace, item.ConnectionID, item.ChatID, item.MessageID, item.BindingID, item.IssueID, item.RequesterID, item.RequesterOpenID) && len(item.AskID) <= 191 &&
		(item.Source == domain.ChatRequestSourceMention || item.Source == domain.ChatRequestSourceReaction || item.Source == domain.ChatRequestSourceShortcut) &&
		(item.Status == domain.ChatRequestPending || item.Status == domain.ChatRequestCreated)
}

// ReserveChatIntakeRequest records the request of a message once. When the
// message already carries one, that record is returned with reserved false.
func (s *SQLiteStore) ReserveChatIntakeRequest(ctx context.Context, workspace string, item domain.ChatIntakeRequest) (domain.ChatIntakeRequest, bool, error) {
	if !validChatIntakeRequest(workspace, item) {
		return item, false, ErrIssueQuery
	}
	now := time.Now().UTC()
	item.ID = ChatIntakeRequestID(item.ConnectionID, item.MessageID)
	item.CreatedAt, item.UpdatedAt = now, now
	raw, err := json.Marshal(item)
	if err != nil {
		return item, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, false, err
	}
	defer tx.Rollback()
	if err = chatRequireConnection(ctx, tx, workspace, item.ConnectionID, ""); err != nil {
		return item, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO chat_intake_requests(id,workspace_key,connection_id,message_id,issue_id,created_at,payload) VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, item.ID, workspace, item.ConnectionID, item.MessageID, item.IssueID, now.UnixMilli(), raw)
	if err != nil {
		return item, false, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 1 {
		if err != nil {
			return item, false, err
		}
		return item, true, tx.Commit()
	}
	existing, err := scanChatIntakeRequest(tx.QueryRowContext(ctx, `SELECT payload FROM chat_intake_requests WHERE connection_id=? AND message_id=?`, item.ConnectionID, item.MessageID))
	if err != nil {
		return item, false, err
	}
	return existing, false, nil
}

func scanChatIntakeRequest(row *sql.Row) (domain.ChatIntakeRequest, error) {
	var raw []byte
	var item domain.ChatIntakeRequest
	err := row.Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &item)
	}
	return item, err
}

// ChatIntakeRequestForMessage returns the request made from a message.
func (s *SQLiteStore) ChatIntakeRequestForMessage(ctx context.Context, workspace, connection, messageID string) (domain.ChatIntakeRequest, error) {
	return scanChatIntakeRequest(s.db.QueryRowContext(ctx, `SELECT payload FROM chat_intake_requests WHERE workspace_key=? AND connection_id=? AND message_id=?`, workspace, connection, messageID))
}

// ChatIntakeRequestsForIssue lists the requests an issue was created for
// (one, unless the issue was moved between workspaces' connections).
func (s *SQLiteStore) ChatIntakeRequestsForIssue(ctx context.Context, workspace, connection, issueID string) ([]domain.ChatIntakeRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM chat_intake_requests WHERE workspace_key=? AND connection_id=? AND issue_id=? ORDER BY created_at,id LIMIT 20`, workspace, connection, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChatIntakeRequest{}
	for rows.Next() {
		var raw []byte
		var item domain.ChatIntakeRequest
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

// UpdateChatIntakeRequest saves a request's progress (its issue, request
// record, status and last reported outcome).
func (s *SQLiteStore) UpdateChatIntakeRequest(ctx context.Context, workspace string, item domain.ChatIntakeRequest) error {
	if !validChatIntakeRequest(workspace, item) || item.ID != ChatIntakeRequestID(item.ConnectionID, item.MessageID) {
		return ErrIssueQuery
	}
	item.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE chat_intake_requests SET issue_id=?,payload=? WHERE workspace_key=? AND connection_id=? AND id=?`, item.IssueID, raw, workspace, item.ConnectionID, item.ID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteChatIntakeRequest releases a message whose request was refused, so
// another member may make it.
func (s *SQLiteStore) DeleteChatIntakeRequest(ctx context.Context, workspace, connection, id string) error {
	if !validChatKey(workspace, connection, id) {
		return ErrIssueQuery
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_intake_requests WHERE workspace_key=? AND connection_id=? AND id=?`, workspace, connection, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}
