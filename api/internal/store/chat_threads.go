package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Chat threads map a Flow resource (an issue) to the message thread carrying
// it in one group, and the thread's items (comments) to their messages. The
// mapping is generic so other resources can be carried in threads too.

// chatThreadRetention bounds how long a thread keeps receiving replies: past
// it the next message starts a new thread.
const chatThreadRetention = 180 * 24 * time.Hour

func (s *SQLiteStore) createChatThreadSchema(ctx context.Context) error {
	blob := chatPayloadColumn(s.dialect)
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS chat_threads (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,chat_id VARCHAR(191) NOT NULL,resource_type VARCHAR(32) NOT NULL,resource_id VARCHAR(191) NOT NULL,root_message_id VARCHAR(191) NOT NULL,created_at BIGINT NOT NULL,payload ` + blob + ` NOT NULL,UNIQUE(connection_id,chat_id,resource_type,resource_id))`,
		`CREATE TABLE IF NOT EXISTS chat_thread_items (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,thread_id VARCHAR(191) NOT NULL,item_type VARCHAR(32) NOT NULL,item_id VARCHAR(191) NOT NULL,message_id VARCHAR(191) NOT NULL,created_at BIGINT NOT NULL,payload ` + blob + ` NOT NULL,UNIQUE(thread_id,item_type,item_id))`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return s.createChatIndexes(ctx,
		`CREATE INDEX IF NOT EXISTS chat_threads_root ON chat_threads(connection_id,root_message_id)`,
		`CREATE INDEX IF NOT EXISTS chat_threads_retention ON chat_threads(created_at)`,
		`CREATE INDEX IF NOT EXISTS chat_thread_items_message ON chat_thread_items(connection_id,message_id)`,
		`CREATE INDEX IF NOT EXISTS chat_thread_items_retention ON chat_thread_items(created_at)`,
	)
}

func chatPayloadColumn(dialect string) string {
	switch dialect {
	case "mysql":
		return "LONGBLOB"
	case "postgres":
		return "BYTEA"
	}
	return "BLOB"
}

func (s *SQLiteStore) createChatIndexes(ctx context.Context, statements ...string) error {
	for _, statement := range statements {
		if s.dialect == "mysql" {
			statement = strings.Replace(statement, "CREATE INDEX IF NOT EXISTS", "CREATE INDEX", 1)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

// ChatThreadID is the stable identity of the thread started by a root message.
func ChatThreadID(connectionID, rootMessageID string) string {
	return "chat_thread_" + ChatRecordID("thread", connectionID, rootMessageID)[5:]
}

// ChatThreadItemID is the stable identity of an item in a thread.
func ChatThreadItemID(threadID, itemType, itemID string) string {
	return "chat_thread_item_" + ChatRecordID("thread-item", threadID, itemType, itemID)[5:]
}

// ChatThread returns the current thread of a resource in a group.
func (s *SQLiteStore) ChatThread(ctx context.Context, workspace, connection, chatID, resourceType, resourceID string) (domain.ChatThread, error) {
	return s.scanChatThread(s.db.QueryRowContext(ctx, `SELECT payload FROM chat_threads WHERE workspace_key=? AND connection_id=? AND chat_id=? AND resource_type=? AND resource_id=? AND created_at>=?`, workspace, connection, chatID, resourceType, resourceID, time.Now().Add(-chatThreadRetention).UnixMilli()))
}

// ChatThreadForRoot returns the thread whose root is the message, for a reply
// received in it.
func (s *SQLiteStore) ChatThreadForRoot(ctx context.Context, workspace, connection, rootMessageID string) (domain.ChatThread, error) {
	return s.scanChatThread(s.db.QueryRowContext(ctx, `SELECT payload FROM chat_threads WHERE workspace_key=? AND connection_id=? AND root_message_id=? AND created_at>=? ORDER BY created_at DESC LIMIT 1`, workspace, connection, rootMessageID, time.Now().Add(-chatThreadRetention).UnixMilli()))
}

func (s *SQLiteStore) scanChatThread(row *sql.Row) (domain.ChatThread, error) {
	var raw []byte
	var item domain.ChatThread
	err := row.Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &item)
	}
	return item, err
}

// PutChatThread records the thread of a resource in a group. A thread with
// another root replaces the resource's previous thread, whose items go with
// it: their messages belong to the old thread.
func (s *SQLiteStore) PutChatThread(ctx context.Context, workspace string, item domain.ChatThread) (domain.ChatThread, error) {
	if !validChatKey(workspace, item.ConnectionID, item.ChatID, item.ResourceType, item.ResourceID, item.RootMessageID) || len(item.ResourceType) > 32 || len(item.PlatformThreadID) > 191 {
		return item, ErrIssueQuery
	}
	now := time.Now().UTC()
	item.ID = ChatThreadID(item.ConnectionID, item.RootMessageID)
	item.UpdatedAt = now
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.SyncFrom.IsZero() {
		item.SyncFrom = item.CreatedAt
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return item, err
	}
	defer tx.Rollback()
	if err = chatRequireConnection(ctx, tx, workspace, item.ConnectionID, ""); err != nil {
		return item, err
	}
	var existingID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM chat_threads WHERE workspace_key=? AND connection_id=? AND chat_id=? AND resource_type=? AND resource_id=?`, workspace, item.ConnectionID, item.ChatID, item.ResourceType, item.ResourceID).Scan(&existingID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return item, err
	}
	if err == nil && existingID != item.ID {
		if _, err = tx.ExecContext(ctx, `DELETE FROM chat_thread_items WHERE workspace_key=? AND connection_id=? AND thread_id=?`, workspace, item.ConnectionID, existingID); err != nil {
			return item, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM chat_threads WHERE workspace_key=? AND id=?`, workspace, existingID); err != nil {
			return item, err
		}
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return item, err
	}
	if existingID == item.ID {
		_, err = tx.ExecContext(ctx, `UPDATE chat_threads SET payload=? WHERE workspace_key=? AND id=?`, raw, workspace, item.ID)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_threads(id,workspace_key,connection_id,chat_id,resource_type,resource_id,root_message_id,created_at,payload) VALUES(?,?,?,?,?,?,?,?,?)`, item.ID, workspace, item.ConnectionID, item.ChatID, item.ResourceType, item.ResourceID, item.RootMessageID, item.CreatedAt.UnixMilli(), raw)
	}
	if err != nil {
		return item, err
	}
	return item, tx.Commit()
}

// DeleteChatThread forgets a thread whose root message is gone, with its items.
func (s *SQLiteStore) DeleteChatThread(ctx context.Context, workspace, connection, threadID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM chat_thread_items WHERE workspace_key=? AND connection_id=? AND thread_id=?`, workspace, connection, threadID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM chat_threads WHERE workspace_key=? AND connection_id=? AND id=?`, workspace, connection, threadID); err != nil {
		return err
	}
	return tx.Commit()
}

// ChatThreadItems lists a thread's items, oldest first.
func (s *SQLiteStore) ChatThreadItems(ctx context.Context, workspace, connection, threadID string) ([]domain.ChatThreadItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM chat_thread_items WHERE workspace_key=? AND connection_id=? AND thread_id=? ORDER BY created_at,id LIMIT 1000`, workspace, connection, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChatThreadItem{}
	for rows.Next() {
		var raw []byte
		var item domain.ChatThreadItem
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

// ChatThreadItemForMessage returns the item a thread message carries.
func (s *SQLiteStore) ChatThreadItemForMessage(ctx context.Context, workspace, connection, messageID string) (domain.ChatThreadItem, error) {
	var raw []byte
	var item domain.ChatThreadItem
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_thread_items WHERE workspace_key=? AND connection_id=? AND message_id=? ORDER BY created_at DESC LIMIT 1`, workspace, connection, messageID).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &item)
	}
	return item, err
}

// InsertChatThreadItem records an item once: false when the thread already
// carries it (another worker, a retry, or the item's own origin).
func (s *SQLiteStore) InsertChatThreadItem(ctx context.Context, workspace, connection string, item domain.ChatThreadItem) (bool, error) {
	if !validChatKey(workspace, connection, item.ThreadID, item.ItemType, item.ItemID, item.MessageID) || len(item.ItemType) > 32 || item.Origin != domain.ChatThreadOriginFlow && item.Origin != domain.ChatThreadOriginChat {
		return false, ErrIssueQuery
	}
	now := time.Now().UTC()
	item.ID = ChatThreadItemID(item.ThreadID, item.ItemType, item.ItemID)
	item.UpdatedAt = now
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = chatRequireConnection(ctx, tx, workspace, connection, ""); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO chat_thread_items(id,workspace_key,connection_id,thread_id,item_type,item_id,message_id,created_at,payload) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, item.ID, workspace, connection, item.ThreadID, item.ItemType, item.ItemID, item.MessageID, item.CreatedAt.UnixMilli(), raw)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, tx.Commit()
}

// UpdateChatThreadItem saves an item's new message, version or deletion.
func (s *SQLiteStore) UpdateChatThreadItem(ctx context.Context, workspace, connection string, item domain.ChatThreadItem) error {
	if !validChatKey(workspace, connection, item.ID, item.MessageID) {
		return ErrIssueQuery
	}
	item.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE chat_thread_items SET message_id=?,payload=? WHERE workspace_key=? AND connection_id=? AND id=?`, item.MessageID, raw, workspace, connection, item.ID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteChatThreadItem removes an item record, such as a reservation whose
// message could not be sent.
func (s *SQLiteStore) DeleteChatThreadItem(ctx context.Context, workspace, connection, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_thread_items WHERE workspace_key=? AND connection_id=? AND id=?`, workspace, connection, id)
	return err
}

// purgeChatThreads removes a bounded page of threads and items past retention.
func (s *SQLiteStore) purgeChatThreads(ctx context.Context, now time.Time) error {
	before := now.Add(-chatThreadRetention).UnixMilli()
	for _, table := range []string{"chat_thread_items", "chat_threads"} {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM `+table+` WHERE created_at<? LIMIT 500`, before)
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
		clause, args := bindList("id", ids)
		if _, err = s.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+clause+` AND created_at<?`, append(args, before)...); err != nil {
			return err
		}
	}
	return nil
}
