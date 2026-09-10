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

// IssueDocumentID checks the active CRDT generation without allocating an issue,
// its description, attachments or activity for every incoming keystroke.
func (s *SQLiteStore) IssueDocumentID(ctx context.Context, workspace, issueID string) (string, error) {
	expression := s.jsonText("data", "documentContent.id")
	if s.dialect == "postgres" {
		expression = "(convert_from(data, 'UTF8')::jsonb #>> '{documentContent,id}')"
	}
	var documentID string
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(NULLIF("+expression+", ''), ?) FROM issue_records WHERE workspace_key=? AND id=?", "document_content_"+issueID, workspace, issueID).Scan(&documentID)
	return documentID, err
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

func (s *SQLiteStore) WalkDocumentUpdates(ctx context.Context, workspace, document string, visit func(DocumentCollaborationUpdate) error) error {
	rows, err := s.db.QueryContext(ctx, `SELECT update_id,client_id,update_data,created_at FROM document_collaboration_updates WHERE workspace_key=? AND document_id=? ORDER BY created_at,update_id`, workspace, document)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var update DocumentCollaborationUpdate
		var created string
		if err := rows.Scan(&update.ID, &update.ClientID, &update.Data, &created); err != nil {
			return err
		}
		update.DocumentID = document
		update.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if err := visit(update); err != nil {
			return err
		}
	}
	return rows.Err()
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
