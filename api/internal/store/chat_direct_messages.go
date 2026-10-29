package store

import (
	"context"
	"encoding/json"

	"flow/api/internal/domain"
)

// ChatLinkedIdentity is a member's linked chat account in one workspace, with
// the connection it was linked through.
type ChatLinkedIdentity struct {
	WorkspaceKey string
	ConnectionID string
	Provider     string
	Identity     domain.ChatIdentity
}

// ChatIdentitiesForUser lists the member's linked chat accounts in every
// workspace (at most 50), so a direct message with one bot can offer the
// other workspaces the member linked in the same tenant. It grants nothing:
// callers authorize each workspace with its own identity.
func (s *SQLiteStore) ChatIdentitiesForUser(ctx context.Context, userID string) ([]ChatLinkedIdentity, error) {
	if !validChatKey(userID) {
		return nil, ErrIssueQuery
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.workspace_key,i.connection_id,c.provider,i.payload FROM chat_identities i JOIN chat_integrations c ON c.connection_id=i.connection_id AND c.workspace_key=i.workspace_key WHERE i.user_id=? ORDER BY i.workspace_key,i.connection_id LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ChatLinkedIdentity{}
	for rows.Next() {
		var item ChatLinkedIdentity
		var raw []byte
		if err = rows.Scan(&item.WorkspaceKey, &item.ConnectionID, &item.Provider, &raw); err != nil {
			return nil, err
		}
		var saved persistedChatIdentity
		if err = json.Unmarshal(raw, &saved); err != nil {
			return nil, err
		}
		item.Identity = saved.ChatIdentity
		item.Identity.AuthProvider = saved.AuthProvider
		item.Identity.AuthIssuer = saved.AuthIssuer
		item.Identity.MFAVerifiedAt = saved.MFAVerifiedAt
		item.Identity.AuthenticatedAt = saved.AuthenticatedAt
		items = append(items, item)
	}
	return items, rows.Err()
}
