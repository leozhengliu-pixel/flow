package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Coding sessions (track D): PR/MR links to sessions (coding.ChangeRequestStore),
// GitHub App installations (codehost.Installations) and webhook delivery
// dedupe. See docs/coding-sessions-design.md §6 and §13.

// normalizeRepository is the indexed form of a repository path: hosts treat
// owner/name case-insensitively, so webhook routing must too.
func normalizeRepository(repository string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(repository), "/"))
}

// UpsertSessionPullRequest links a PR/MR to a session, idempotently. Updates
// older than the stored row are ignored (webhooks can arrive out of order),
// empty fields keep their stored values, and a merged PR never goes back to
// open or closed.
func (s *SQLiteStore) UpsertSessionPullRequest(ctx context.Context, workspaceKey string, pullRequest domain.SessionPullRequest) error {
	if pullRequest.SessionID == "" || pullRequest.CodeHost == "" || pullRequest.Number <= 0 || pullRequest.Repository == "" {
		return errors.New("session pull request needs a session, code host, repository and number")
	}
	now := time.Now().UTC()
	if pullRequest.UpdatedAt.IsZero() {
		pullRequest.UpdatedAt = now
	}
	hostName := strings.ToLower(pullRequest.HostName)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM coding_session_pull_requests WHERE session_id=? AND code_host=? AND host_name=? AND number=?`, pullRequest.SessionID, pullRequest.CodeHost, hostName, pullRequest.Number).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if pullRequest.CreatedAt.IsZero() {
			pullRequest.CreatedAt = pullRequest.UpdatedAt
		}
	case err != nil:
		return err
	default:
		var existing domain.SessionPullRequest
		if err := json.Unmarshal(raw, &existing); err != nil {
			return err
		}
		if pullRequest.UpdatedAt.Before(existing.UpdatedAt) {
			return tx.Commit()
		}
		pullRequest = mergeSessionPullRequest(existing, pullRequest)
	}
	if pullRequest.State == "" {
		pullRequest.State = "open"
	}
	data, err := json.Marshal(pullRequest)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO coding_session_pull_requests(session_id,code_host,host_name,repository,number,workspace_key,state,data,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(session_id,code_host,host_name,number) DO UPDATE SET repository=excluded.repository,state=excluded.state,data=excluded.data,updated_at=excluded.updated_at`,
		pullRequest.SessionID, pullRequest.CodeHost, hostName, normalizeRepository(pullRequest.Repository), pullRequest.Number, workspaceKey, pullRequest.State, data, pullRequest.CreatedAt.UnixMilli(), pullRequest.UpdatedAt.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func mergeSessionPullRequest(existing, next domain.SessionPullRequest) domain.SessionPullRequest {
	keep := func(value *string, previous string) {
		if *value == "" {
			*value = previous
		}
	}
	keep(&next.ExternalID, existing.ExternalID)
	keep(&next.URL, existing.URL)
	keep(&next.Title, existing.Title)
	keep(&next.State, existing.State)
	keep(&next.HeadBranch, existing.HeadBranch)
	keep(&next.BaseBranch, existing.BaseBranch)
	keep(&next.HeadSHA, existing.HeadSHA)
	keep(&next.ReviewID, existing.ReviewID)
	keep(&next.HostName, existing.HostName)
	if existing.State == "merged" {
		next.State = "merged"
	}
	if !existing.CreatedAt.IsZero() {
		next.CreatedAt = existing.CreatedAt
	}
	return next
}

// SessionPullRequests lists a session's PRs/MRs, oldest first.
func (s *SQLiteStore) SessionPullRequests(ctx context.Context, workspaceKey, sessionID string) ([]domain.SessionPullRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM coding_session_pull_requests WHERE workspace_key=? AND session_id=? ORDER BY created_at,number`, workspaceKey, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.SessionPullRequest{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item domain.SessionPullRequest
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// SessionsForChangeRequest finds every session linked to a PR/MR, across
// workspaces (webhook routing); callers filter by workspace.
func (s *SQLiteStore) SessionsForChangeRequest(ctx context.Context, codeHost, hostName, repository string, number int) ([]domain.CodingSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.workspace_key,s.state,s.phase,s.error_code,s.version,s.data FROM coding_session_pull_requests p JOIN coding_sessions s ON s.id=p.session_id AND s.workspace_key=p.workspace_key WHERE p.code_host=? AND p.host_name=? AND p.repository=? AND p.number=? ORDER BY s.created_at`,
		codeHost, strings.ToLower(hostName), normalizeRepository(repository), number)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CodingSession{}
	for rows.Next() {
		var id, workspace, state, phase, errorCode string
		var version int64
		var raw []byte
		if err := rows.Scan(&id, &workspace, &state, &phase, &errorCode, &version, &raw); err != nil {
			return nil, err
		}
		var session domain.CodingSession
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &session); err != nil {
				return nil, err
			}
		}
		// The indexed columns are authoritative over the JSON copy.
		session.ID, session.WorkspaceKey, session.State, session.Phase, session.Version = id, workspace, domain.CodingSessionState(state), domain.CodingSessionPhase(phase), version
		session.ErrorCode = domain.CodingErrorCode(errorCode)
		result = append(result, session)
	}
	return result, rows.Err()
}

// codeHostInstallationID is the deterministic row id of an installation in a
// workspace, so concurrent setup callbacks and webhooks converge on one row.
func codeHostInstallationID(workspaceKey, codeHost, hostName, installationID string) string {
	sum := sha256.Sum256([]byte(workspaceKey + "\x00" + codeHost + "\x00" + strings.ToLower(hostName) + "\x00" + installationID))
	return "chi_" + hex.EncodeToString(sum[:13])
}

// UpsertCodeHostInstallation links an installation to installation.WorkspaceKey
// (or refreshes it). The row id is deterministic.
func (s *SQLiteStore) UpsertCodeHostInstallation(ctx context.Context, installation domain.CodeHostInstallation) error {
	if installation.WorkspaceKey == "" || installation.CodeHost == "" || installation.InstallationID == "" {
		return errors.New("code host installation needs a workspace, code host and installation id")
	}
	installation.HostName = strings.ToLower(installation.HostName)
	installation.ID = codeHostInstallationID(installation.WorkspaceKey, installation.CodeHost, installation.HostName, installation.InstallationID)
	now := time.Now().UTC()
	installation.UpdatedAt = now
	var createdAt int64
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM code_host_installations WHERE id=?`, installation.ID).Scan(&createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		installation.CreatedAt = now
	case err != nil:
		return err
	default:
		installation.CreatedAt = time.UnixMilli(createdAt).UTC()
	}
	data, err := json.Marshal(installation)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO code_host_installations(id,workspace_key,code_host,host_name,installation_id,suspended_at,data,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET suspended_at=excluded.suspended_at,data=excluded.data,updated_at=excluded.updated_at`,
		installation.ID, installation.WorkspaceKey, installation.CodeHost, installation.HostName, installation.InstallationID, suspendedMillis(installation.SuspendedAt), data, installation.CreatedAt.UnixMilli(), now.UnixMilli())
	return err
}

func suspendedMillis(at *time.Time) int64 {
	if at == nil {
		return 0
	}
	return at.UnixMilli()
}

// RefreshCodeHostInstallation updates the account, repository selection and
// suspension of an installation in every workspace it is linked to
// (installation webhooks). It never links new workspaces.
func (s *SQLiteStore) RefreshCodeHostInstallation(ctx context.Context, installation domain.CodeHostInstallation) error {
	workspaces, err := s.WorkspacesForInstallation(ctx, installation.CodeHost, installation.HostName, installation.InstallationID)
	if err != nil {
		return err
	}
	for _, workspace := range workspaces {
		next := installation
		next.WorkspaceKey = workspace
		if err := s.UpsertCodeHostInstallation(ctx, next); err != nil {
			return err
		}
	}
	return nil
}

// DeleteCodeHostInstallation removes an installation from every workspace
// (the App was uninstalled). Missing rows are not an error.
func (s *SQLiteStore) DeleteCodeHostInstallation(ctx context.Context, codeHost, hostName, installationID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM code_host_installations WHERE code_host=? AND host_name=? AND installation_id=?`, codeHost, strings.ToLower(hostName), installationID)
	return err
}

// UnlinkCodeHostInstallation removes one workspace's link to an
// installation, by row id or installation id. It reports whether a row went.
func (s *SQLiteStore) UnlinkCodeHostInstallation(ctx context.Context, workspaceKey, codeHost, id string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM code_host_installations WHERE workspace_key=? AND code_host=? AND (id=? OR installation_id=?)`, workspaceKey, codeHost, id, id)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count > 0, nil
}

// CodeHostInstallations lists a workspace's installations of a code host.
func (s *SQLiteStore) CodeHostInstallations(ctx context.Context, workspaceKey, codeHost string) ([]domain.CodeHostInstallation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key,data FROM code_host_installations WHERE workspace_key=? AND code_host=? ORDER BY created_at,id`, workspaceKey, codeHost)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CodeHostInstallation{}
	for rows.Next() {
		var workspace string
		var raw []byte
		if err := rows.Scan(&workspace, &raw); err != nil {
			return nil, err
		}
		var item domain.CodeHostInstallation
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		item.WorkspaceKey = workspace
		result = append(result, item)
	}
	return result, rows.Err()
}

// WorkspacesForInstallation lists the workspaces an installation serves.
func (s *SQLiteStore) WorkspacesForInstallation(ctx context.Context, codeHost, hostName, installationID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace_key FROM code_host_installations WHERE code_host=? AND host_name=? AND installation_id=? ORDER BY workspace_key`, codeHost, strings.ToLower(hostName), installationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var workspace string
		if err := rows.Scan(&workspace); err != nil {
			return nil, err
		}
		result = append(result, workspace)
	}
	return result, rows.Err()
}

// IntegrationConnections lists a workspace's integration connections of a
// provider (GitLab connections used as a code host; classic GitHub
// connections that already sync PRs).
func (s *SQLiteStore) IntegrationConnections(_ context.Context, workspaceKey, provider string) ([]domain.IntegrationConnection, error) {
	result := []domain.IntegrationConnection{}
	if workspaceKey == "" {
		return result, nil
	}
	data, ok := s.WorkspaceMetadata(workspaceKey)
	if !ok {
		return result, nil
	}
	for _, connection := range data.IntegrationConnections {
		if strings.EqualFold(connection.Provider, provider) {
			result = append(result, connection)
		}
	}
	return result, nil
}

// codeHostWebhookRetention bounds the delivery dedupe table; GitHub and
// GitLab redeliver within days, not weeks.
const codeHostWebhookRetention = 14 * 24 * time.Hour

// ClaimCodeHostWebhookDelivery records that a delivery was dispatched to a
// workspace. It returns false when the delivery was already claimed, so a
// redelivered or duplicated webhook is applied once.
func (s *SQLiteStore) ClaimCodeHostWebhookDelivery(ctx context.Context, codeHost, deliveryID, workspaceKey string) (bool, error) {
	if deliveryID == "" {
		return true, nil
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO code_host_webhook_deliveries(code_host,delivery_id,workspace_key,created_at) VALUES(?,?,?,?) ON CONFLICT(code_host,delivery_id,workspace_key) DO NOTHING`, codeHost, deliveryID, workspaceKey, now.UnixMilli())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count > 0 && now.UnixNano()%64 == 0 {
		// Occasional sweep instead of a background loop.
		_, _ = s.db.ExecContext(ctx, `DELETE FROM code_host_webhook_deliveries WHERE created_at<?`, now.Add(-codeHostWebhookRetention).UnixMilli())
	}
	return count > 0, nil
}

// ReleaseCodeHostWebhookDelivery forgets a claim whose processing failed,
// so GitHub's redelivery is applied.
func (s *SQLiteStore) ReleaseCodeHostWebhookDelivery(ctx context.Context, codeHost, deliveryID, workspaceKey string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM code_host_webhook_deliveries WHERE code_host=? AND delivery_id=? AND workspace_key=?`, codeHost, deliveryID, workspaceKey)
	return err
}
