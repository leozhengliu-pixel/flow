package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// IntegrationWebhookCredentials is the complete input to webhook verification,
// not a connection row that may be written back to storage.
type IntegrationWebhookCredentials struct {
	ID               string
	WebhookSecret    string
	WebhookSecretEnv string
	SigningSecret    string
	SigningSecretEnv string
	Username         string
}

// BeginIntegrationWebhookCredentials selects the first configured connection in
// canonical order. The caller owns release through verification and dispatch.
// Its SQL snapshot is closed before this method returns.
func (s *SQLiteStore) BeginIntegrationWebhookCredentials(ctx context.Context, workspace, provider string) (IntegrationWebhookCredentials, bool, func(), error) {
	emptyRelease := func() {}
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	// textArgs strips PostgreSQL NULs; a missing lookup must not alias a row.
	if s.dialect == "postgres" && (strings.ContainsRune(workspace, '\x00') || strings.ContainsRune(provider, '\x00')) {
		return IntegrationWebhookCredentials{}, false, emptyRelease, nil
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return IntegrationWebhookCredentials{}, false, emptyRelease, err
	}
	defer tx.Rollback()
	var workspaceID string
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id,`+s.scaleP21SW18CollectionState("integrationConnections")+` FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&workspaceID, &active); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationWebhookCredentials{}, false, emptyRelease, nil
		}
		return IntegrationWebhookCredentials{}, false, emptyRelease, err
	}
	if active < 0 {
		return IntegrationWebhookCredentials{}, false, emptyRelease, ErrBoundedMetadataCoverage
	}
	if active == 0 {
		return IntegrationWebhookCredentials{}, false, emptyRelease, nil
	}
	expressions := []string{"record_key"}
	for _, key := range []string{"webhookSecret", "webhookSecretEnv", "signingSecret", "signingSecretEnv", "username"} {
		value := s.jsonText("data", "config."+key)
		if s.dialect == "mysql" {
			// Preserve the literal string "null", which is a valid secret.
			extract := "JSON_EXTRACT(CONVERT(data USING utf8mb4), '$.config." + key + "')"
			value = "CASE WHEN JSON_TYPE(" + extract + ")='STRING' THEN JSON_UNQUOTE(" + extract + ") ELSE NULL END"
		}
		expressions = append(expressions, "COALESCE("+value+",'')")
	}
	where := `workspace_key=? AND field='integrationConnections' AND ` +
		s.metadataRecordKeyEquals(s.jsonText("data", "provider"), "?") +
		` AND (` + s.metadataRecordKeyEquals(s.jsonText("data", "status"), "'configured'") +
		` OR ` + s.metadataRecordKeyEquals(s.jsonText("data", "status"), "'connected'") + `)`
	lengths := make([]string, len(expressions))
	for i, expression := range expressions {
		if s.dialect == "sqlite" {
			lengths[i] = "LENGTH(CAST(" + expression + " AS BLOB))"
		} else {
			lengths[i] = "OCTET_LENGTH(" + expression + ")"
		}
	}
	var size int64
	if err := tx.QueryRowContext(ctx, `SELECT `+strings.Join(lengths, "+")+` FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT 1`, workspace, provider).Scan(&size); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IntegrationWebhookCredentials{}, false, emptyRelease, nil
		}
		return IntegrationWebhookCredentials{}, false, emptyRelease, err
	}
	const fixedBytes, multiplier = 64 << 10, 8
	if size < 0 || size > int64((int(^uint(0)>>1)-fixedBytes)/multiplier) {
		return IntegrationWebhookCredentials{}, false, emptyRelease, ErrMetadataWorkBudget
	}
	ctx, release, err := s.admitMetadataWork(ctx, int(size)*multiplier+fixedBytes)
	if err != nil {
		return IntegrationWebhookCredentials{}, false, emptyRelease, err
	}
	var result IntegrationWebhookCredentials
	err = tx.QueryRowContext(ctx, `SELECT `+strings.Join(expressions, ",")+` FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT 1`, workspace, provider).Scan(
		&result.ID, &result.WebhookSecret, &result.WebhookSecretEnv, &result.SigningSecret, &result.SigningSecretEnv, &result.Username,
	)
	if err != nil {
		release()
		return IntegrationWebhookCredentials{}, false, emptyRelease, err
	}
	if got := len(result.ID) + len(result.WebhookSecret) + len(result.WebhookSecretEnv) + len(result.SigningSecret) + len(result.SigningSecretEnv) + len(result.Username); int64(got) != size {
		release()
		return IntegrationWebhookCredentials{}, false, emptyRelease, fmt.Errorf("%w: webhook scalar snapshot changed", ErrResourceAccessChanged)
	}
	return result, true, release, nil
}
