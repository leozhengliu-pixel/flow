package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// A caller-owned SQL snapshot must not acquire the store mutex: workspace
// writers can hold that mutex while waiting for this connection.
func (s *SQLiteStore) resolveSnapshotApplicationAccess(ctx context.Context, a *domain.ApplicationInstallation) error {
	var workspaceID string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_id FROM workspace_states WHERE workspace_key=?`, a.WorkspaceKey).Scan(&workspaceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Missing imported workspaces use empty team policy; global OAuth
	// decoration still runs below, as in the installed metadata path.
	var policy domain.Bootstrap
	if err == nil {
		policy, err = s.resourcePolicyRecords(ctx, a.WorkspaceKey, workspaceID)
		if err != nil {
			return err
		}
	}
	a.ResolveTeamAccess(policy.Teams, policy.TeamSettings)
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE field='oauthApplications' AND `+s.jsonText("data", "clientId")+`=?`, a.ClientID)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var raw []byte
		var app domain.OAuthApplication
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &app); err != nil {
			return err
		}
		if app.ClientID != a.ClientID {
			continue
		}
		found = true
		a.Name, a.AvatarURL = app.Name, app.LogoURL
		a.Scopes = slices.DeleteFunc(slices.Clone(a.Scopes), func(scope string) bool { return !slices.Contains(app.Scopes, scope) })
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !found && strings.HasPrefix(a.ClientID, "flow_client_") {
		a.Active = false
	}
	return nil
}
