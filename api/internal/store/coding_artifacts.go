package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Session artifacts: browser screenshots and recordings (docs/coding-sessions-design.md
// §17). Owner: track I. Rows live in coding_session_artifacts (coding_schema.go);
// `data` holds the domain.CodingArtifact document without URLs, which are
// derived per request (signed or authenticated) from object_key.

// AddCodingArtifact inserts an artifact. It is idempotent on the artifact id:
// inserting an id that already exists for the same session succeeds without
// changing the stored row (runner retries reuse their client id, which the API
// turns into the artifact id).
func (s *SQLiteStore) AddCodingArtifact(ctx context.Context, workspaceKey string, artifact domain.CodingArtifact, objectKey string) error {
	if artifact.ID == "" || artifact.SessionID == "" || workspaceKey == "" || objectKey == "" {
		return ErrIssueQuery
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	artifact.URL, artifact.ContentURL, artifact.ExpiresAt = "", "", nil
	raw, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO coding_session_artifacts(id,session_id,workspace_key,object_key,data,created_at) VALUES(?,?,?,?,?,?)`, artifact.ID, artifact.SessionID, workspaceKey, objectKey, raw, artifact.CreatedAt.UnixMilli())
	if err == nil {
		return nil
	}
	existing, _, lookupErr := s.CodingArtifact(ctx, workspaceKey, artifact.SessionID, artifact.ID)
	if lookupErr == nil && existing.ID == artifact.ID {
		return nil
	}
	return err
}

// CodingArtifacts lists a session's artifacts, oldest first.
func (s *SQLiteStore) CodingArtifacts(ctx context.Context, workspaceKey, sessionID string) ([]domain.CodingArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM coding_session_artifacts WHERE workspace_key=? AND session_id=? ORDER BY created_at,id`, workspaceKey, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.CodingArtifact{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var artifact domain.CodingArtifact
		if err := json.Unmarshal(raw, &artifact); err != nil {
			return nil, err
		}
		result = append(result, artifact)
	}
	// The indexed column is milliseconds; documents retain the full timestamp.
	slices.SortFunc(result, func(a, b domain.CodingArtifact) int {
		if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	return result, rows.Err()
}

// ErrCodingArtifactNotFound is returned for an unknown artifact. (The store
// cannot import internal/coding: coding → sandbox → config → store.)
var ErrCodingArtifactNotFound = errors.New("coding artifact not found")

// CodingArtifact returns one artifact of a session and its object key
// (ErrCodingArtifactNotFound when absent or in another workspace or session).
func (s *SQLiteStore) CodingArtifact(ctx context.Context, workspaceKey, sessionID, id string) (domain.CodingArtifact, string, error) {
	var raw []byte
	var objectKey string
	err := s.db.QueryRowContext(ctx, `SELECT data,object_key FROM coding_session_artifacts WHERE workspace_key=? AND session_id=? AND id=?`, workspaceKey, sessionID, id).Scan(&raw, &objectKey)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.CodingArtifact{}, "", ErrCodingArtifactNotFound
	}
	if err != nil {
		return domain.CodingArtifact{}, "", err
	}
	var artifact domain.CodingArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return domain.CodingArtifact{}, "", err
	}
	return artifact, objectKey, nil
}

// CodingArtifactObjectKeys maps artifact ids to object keys for a session.
func (s *SQLiteStore) CodingArtifactObjectKeys(ctx context.Context, workspaceKey, sessionID string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,object_key FROM coding_session_artifacts WHERE workspace_key=? AND session_id=?`, workspaceKey, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		result[id] = key
	}
	return result, rows.Err()
}

// SetCodingArtifactActivity links an artifact to the action activity that
// shows it in the session thread.
func (s *SQLiteStore) SetCodingArtifactActivity(ctx context.Context, workspaceKey, sessionID, id, activityID string) error {
	artifact, _, err := s.CodingArtifact(ctx, workspaceKey, sessionID, id)
	if err != nil {
		return err
	}
	if artifact.ActivityID == strings.TrimSpace(activityID) {
		return nil
	}
	artifact.ActivityID = strings.TrimSpace(activityID)
	raw, err := json.Marshal(artifact)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE coding_session_artifacts SET data=? WHERE workspace_key=? AND session_id=? AND id=?`, raw, workspaceKey, sessionID, id)
	return err
}

// DeleteCodingArtifacts removes a session's artifact rows and returns their
// object keys so the caller can delete the objects (session deletion, GC).
func (s *SQLiteStore) DeleteCodingArtifacts(ctx context.Context, workspaceKey, sessionID string) ([]string, error) {
	keys, err := s.CodingArtifactObjectKeys(ctx, workspaceKey, sessionID)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM coding_session_artifacts WHERE workspace_key=? AND session_id=?`, workspaceKey, sessionID); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key)
	}
	return result, nil
}

// CodingArtifactOwner identifies a session that owns artifacts.
type CodingArtifactOwner struct {
	WorkspaceKey string
	SessionID    string
}

// OrphanCodingArtifactSessions lists sessions that still have artifact rows
// but no coding_sessions row (deleted sessions), at most limit of them.
func (s *SQLiteStore) OrphanCodingArtifactSessions(ctx context.Context, limit int) ([]CodingArtifactOwner, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT a.workspace_key,a.session_id FROM coding_session_artifacts a WHERE NOT EXISTS (SELECT 1 FROM coding_sessions cs WHERE cs.id=a.session_id) ORDER BY a.workspace_key,a.session_id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	owners := []CodingArtifactOwner{}
	for rows.Next() {
		var owner CodingArtifactOwner
		if err := rows.Scan(&owner.WorkspaceKey, &owner.SessionID); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}
	return owners, rows.Err()
}
