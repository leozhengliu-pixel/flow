package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type chatMutationContextKey struct{}

// WithChatMutationKey ties a verified chat command to the issue transaction.
// The command key is scoped to its workspace and actual Flow actor in storage.
func WithChatMutationKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, chatMutationContextKey{}, key)
}

func ChatMutationKey(ctx context.Context) string {
	key, _ := ctx.Value(chatMutationContextKey{}).(string)
	return key
}

func (s *SQLiteStore) createChatMutationReceiptsSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS chat_mutation_receipts (workspace_key VARCHAR(191) NOT NULL,user_id VARCHAR(191) NOT NULL,mutation_key VARCHAR(191) NOT NULL,issue_id VARCHAR(191) NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(workspace_key,user_id,mutation_key))`)
	if err != nil {
		return err
	}
	statement := `CREATE INDEX IF NOT EXISTS chat_mutation_receipts_created ON chat_mutation_receipts(created_at)`
	if s.dialect == "mysql" {
		statement = strings.Replace(statement, "CREATE INDEX IF NOT EXISTS", "CREATE INDEX", 1)
	}
	_, err = s.db.ExecContext(ctx, statement)
	if err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
		return err
	}
	return nil
}

type chatReceiptReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readChatMutationReceipt(ctx context.Context, reader chatReceiptReader, workspace, userID, key string) (string, bool, error) {
	if !validChatKey(workspace, userID, key) {
		return "", false, ErrIssueQuery
	}
	var issueID string
	err := reader.QueryRowContext(ctx, `SELECT issue_id FROM chat_mutation_receipts WHERE workspace_key=? AND user_id=? AND mutation_key=?`, workspace, userID, key).Scan(&issueID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return issueID, err == nil, err
}

// ChatMutationReceipt is consulted only after the caller reauthorizes the
// current issue. It deliberately does not confer resource or actor access.
func (s *SQLiteStore) ChatMutationReceipt(ctx context.Context, workspace, userID, key string) (string, bool, error) {
	return readChatMutationReceipt(ctx, s.db, workspace, userID, key)
}

func writeChatMutationReceipt(ctx context.Context, tx *sqlTx, workspace, userID, key, issueID string) error {
	if !validChatKey(workspace, userID, key, issueID) {
		return ErrIssueQuery
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO chat_mutation_receipts(workspace_key,user_id,mutation_key,issue_id,created_at) VALUES(?,?,?,?,?)`, workspace, userID, key, issueID, time.Now().UTC().UnixMilli())
	return err
}

func (s *SQLiteStore) purgeChatMutationReceipts(ctx context.Context, now time.Time) error {
	before := now.Add(-30 * 24 * time.Hour).UnixMilli()
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,user_id,mutation_key FROM chat_mutation_receipts WHERE created_at<? ORDER BY created_at LIMIT 500`, before)
	if err != nil {
		return err
	}
	type receiptKey struct{ workspace, userID, key string }
	var keys []receiptKey
	for rows.Next() {
		var key receiptKey
		if err = rows.Scan(&key.workspace, &key.userID, &key.key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if _, err = s.db.ExecContext(ctx, `DELETE FROM chat_mutation_receipts WHERE workspace_key=? AND user_id=? AND mutation_key=? AND created_at<?`, key.workspace, key.userID, key.key, before); err != nil {
			return err
		}
	}
	return nil
}
