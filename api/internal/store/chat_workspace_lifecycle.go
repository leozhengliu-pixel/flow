package store

import (
	"context"
	"encoding/json"
	"strings"

	"flow/api/internal/domain"
)

// Connection IDs are stable across workspace URL changes. Secret references
// must use that immutable identity so encrypted credentials remain readable.
func ChatCredentialID(connectionID string) string {
	return ChatRecordID("credentials", connectionID)
}

var chatWorkspaceTables = []string{
	"chat_integrations", "chat_bindings", "chat_identities", "chat_actions",
	"chat_messages", "chat_jobs", "chat_domain_cursors", "chat_mutation_receipts",
}

func revokeChatWorkspaceOAuth(ctx context.Context, tx *sqlTx, workspace string) error {
	for {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM chat_actions WHERE workspace_key=? AND SUBSTR(id,1,11)='chat_oauth_' ORDER BY id LIMIT 500`, workspace)
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
			if strings.HasPrefix(id, "chat_oauth_") {
				ids = append(ids, id)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			if _, err = tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM chat_actions WHERE workspace_key=? AND id=?`, workspace, id); err != nil {
				return err
			}
		}
	}
}

// renameChatPayloadWorkspace rewrites only the top-level routing field with a
// JSON parser. User text and nested card payloads may contain the old URL key.
func renameChatPayloadWorkspace(ctx context.Context, tx *sqlTx, table, key, oldKey, newKey string) error {
	for {
		rows, err := tx.QueryContext(ctx, `SELECT `+key+`,payload FROM `+table+` WHERE workspace_key=? ORDER BY `+key+` LIMIT 500`, oldKey)
		if err != nil {
			return err
		}
		type record struct {
			id  string
			raw []byte
		}
		var records []record
		for rows.Next() {
			var item record
			if err = rows.Scan(&item.id, &item.raw); err != nil {
				rows.Close()
				return err
			}
			records = append(records, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		for _, item := range records {
			var payload map[string]json.RawMessage
			if err = json.Unmarshal(item.raw, &payload); err != nil {
				return err
			}
			if payload == nil {
				return ErrIssueQuery
			}
			payload["workspaceKey"], err = json.Marshal(newKey)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET workspace_key=?,payload=? WHERE workspace_key=? AND `+key+`=?`, newKey, raw, oldKey, item.id); err != nil {
				return err
			}
		}
	}
}

func renameChatWorkspace(ctx context.Context, tx *sqlTx, oldKey, newKey string) error {
	if !validChatKey(oldKey, newKey) {
		return ErrIssueQuery
	}
	if oldKey == newKey {
		return nil
	}
	// OAuth state carries the old workspace in encrypted storage. Revoke it
	// instead of guessing whether a browser callback still targets that space.
	if err := revokeChatWorkspaceOAuth(ctx, tx, oldKey); err != nil {
		return err
	}
	for _, table := range []struct{ name, key string }{{"chat_integrations", "connection_id"}, {"chat_actions", "id"}, {"chat_messages", "id"}} {
		if err := renameChatPayloadWorkspace(ctx, tx, table.name, table.key, oldKey, newKey); err != nil {
			return err
		}
	}
	for _, table := range []string{"chat_bindings", "chat_identities", "chat_jobs", "chat_domain_cursors", "chat_mutation_receipts"} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET workspace_key=? WHERE workspace_key=?`, newKey, oldKey); err != nil {
			return err
		}
	}
	return nil
}

func deleteChatWorkspace(ctx context.Context, tx *sqlTx, workspace string) error {
	if !validChatKey(workspace) {
		return ErrIssueQuery
	}
	rows, err := tx.QueryContext(ctx, `SELECT connection_id,provider,app_id,tenant_key FROM chat_integrations WHERE workspace_key=?`, workspace)
	if err != nil {
		return err
	}
	type connection struct{ id, provider, app, tenant string }
	var connections []connection
	for rows.Next() {
		var item connection
		if err = rows.Scan(&item.id, &item.provider, &item.app, &item.tenant); err != nil {
			rows.Close()
			return err
		}
		connections = append(connections, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range connections {
		if _, err = tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, ChatCredentialID(item.id)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM chat_request_limits WHERE scope_key=?`, ChatRecordID("rate", item.provider, item.app, item.tenant)); err != nil {
			return err
		}
	}
	// A previous interrupted disconnect can remove the routing row before the
	// legacy display metadata. Clean its stable secret reference as well.
	rows, err = tx.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='integrationConnections'`, workspace)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var raw []byte
		var item domain.IntegrationConnection
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &item); err != nil {
			rows.Close()
			return err
		}
		if item.Provider == "feishu" || item.Provider == "lark" {
			stale = append(stale, item.ID)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range stale {
		if _, err = tx.ExecContext(ctx, `DELETE FROM connector_secrets WHERE id=?`, ChatCredentialID(id)); err != nil {
			return err
		}
	}
	if err = revokeChatWorkspaceOAuth(ctx, tx, workspace); err != nil {
		return err
	}
	for _, table := range chatWorkspaceTables {
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_key=?`, workspace); err != nil {
			return err
		}
	}
	return nil
}
