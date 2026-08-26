package store

import (
	"context"
	"time"
)

type DocumentCollaborationUpdate struct {
	ID         string    `json:"id"`
	DocumentID string    `json:"documentId"`
	ClientID   string    `json:"clientId"`
	Data       []byte    `json:"-"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (s *SQLiteStore) AppendDocumentCollaborationUpdate(ctx context.Context, workspaceKey string, update DocumentCollaborationUpdate) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO document_collaboration_updates(update_id,workspace_key,document_id,client_id,update_data,created_at) VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING`, update.ID, workspaceKey, update.DocumentID, update.ClientID, update.Data, update.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

func (s *SQLiteStore) DocumentCollaborationUpdates(ctx context.Context, workspaceKey, documentID string) ([]DocumentCollaborationUpdate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT update_id,client_id,update_data,created_at FROM document_collaboration_updates WHERE workspace_key=? AND document_id=? ORDER BY created_at,update_id`, workspaceKey, documentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	updates := []DocumentCollaborationUpdate{}
	for rows.Next() {
		var update DocumentCollaborationUpdate
		var createdAt string
		if err := rows.Scan(&update.ID, &update.ClientID, &update.Data, &createdAt); err != nil {
			return nil, err
		}
		update.DocumentID = documentID
		update.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		updates = append(updates, update)
	}
	return updates, rows.Err()
}

func (s *SQLiteStore) DeleteDocumentCollaborationUpdates(ctx context.Context, workspaceKey, documentID string, updateIDs []string) error {
	if len(updateIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, updateID := range updateIDs {
		if _, err := tx.ExecContext(ctx, `DELETE FROM document_collaboration_updates WHERE workspace_key=? AND document_id=? AND update_id=?`, workspaceKey, documentID, updateID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) DeleteDocumentCollaborationDocument(ctx context.Context, workspaceKey, documentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM document_collaboration_updates WHERE workspace_key=? AND document_id=?`, workspaceKey, documentID)
	return err
}
