package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"flow/api/internal/domain"
)

// Initiative group bindings connect one initiative directly to a chat group.
// They are kept apart from team bindings: a group may share a team and also
// follow initiatives, and an initiative binding never accepts group actions.

func (s *SQLiteStore) createChatInitiativeBindingSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS chat_initiative_bindings (id VARCHAR(191) PRIMARY KEY,workspace_key VARCHAR(191) NOT NULL,connection_id VARCHAR(191) NOT NULL,chat_id VARCHAR(191) NOT NULL,initiative_id VARCHAR(191) NOT NULL,payload `+chatPayloadColumn(s.dialect)+` NOT NULL,UNIQUE(connection_id,chat_id,initiative_id))`); err != nil {
		return err
	}
	return s.createChatIndexes(ctx, `CREATE INDEX IF NOT EXISTS chat_initiative_bindings_initiative ON chat_initiative_bindings(workspace_key,connection_id,initiative_id)`)
}

// ChatInitiativeBindings lists a connection's initiative bindings; an
// initiative ID limits the list to that initiative.
func (s *SQLiteStore) ChatInitiativeBindings(ctx context.Context, workspace, connection, initiativeID string) ([]domain.ChatInitiativeBinding, error) {
	query, args := `SELECT payload FROM chat_initiative_bindings WHERE workspace_key=? AND connection_id=?`, []any{workspace, connection}
	if initiativeID != "" {
		query += ` AND initiative_id=?`
		args = append(args, initiativeID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY id LIMIT 1000`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.ChatInitiativeBinding{}
	for rows.Next() {
		var raw []byte
		var item domain.ChatInitiativeBinding
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

func (s *SQLiteStore) ChatInitiativeBinding(ctx context.Context, workspace, connection, id string) (domain.ChatInitiativeBinding, error) {
	var raw []byte
	var item domain.ChatInitiativeBinding
	err := s.db.QueryRowContext(ctx, `SELECT payload FROM chat_initiative_bindings WHERE workspace_key=? AND connection_id=? AND id=?`, workspace, connection, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &item)
	}
	return item, err
}

// PutChatInitiativeBinding creates or updates a binding. A group follows an
// initiative once: another binding of the same pair is a conflict.
func (s *SQLiteStore) PutChatInitiativeBinding(ctx context.Context, workspace string, item domain.ChatInitiativeBinding) error {
	if !validChatKey(workspace, item.ID, item.ConnectionID, item.ChatID, item.InitiativeID) || len(item.ChatName) > 800 {
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
	err = tx.QueryRowContext(ctx, `SELECT workspace_key,connection_id FROM chat_initiative_bindings WHERE id=?`, item.ID).Scan(&owner, &connection)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_initiative_bindings(id,workspace_key,connection_id,chat_id,initiative_id,payload) VALUES(?,?,?,?,?,?)`, item.ID, workspace, item.ConnectionID, item.ChatID, item.InitiativeID, raw)
	} else if err == nil {
		if owner != workspace || connection != item.ConnectionID {
			return ErrAuthForbidden
		}
		_, err = tx.ExecContext(ctx, `UPDATE chat_initiative_bindings SET chat_id=?,initiative_id=?,payload=? WHERE id=? AND workspace_key=? AND connection_id=?`, item.ChatID, item.InitiativeID, raw, item.ID, workspace, item.ConnectionID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) DeleteChatInitiativeBinding(ctx context.Context, workspace, connection, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_initiative_bindings WHERE workspace_key=? AND connection_id=? AND id=?`, workspace, connection, id)
	return err
}
