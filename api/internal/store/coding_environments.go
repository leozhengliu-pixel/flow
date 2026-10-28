package store

// Coding environments, environment secrets, snapshots and snapshot logs
// (docs/coding-sessions-design.md §6, §10, §18). Owner: track K.
//
// coding_environments keeps the domain.CodingEnvironment document in `data`;
// the indexed columns are copies. active_repo_key holds the repository key
// while the environment is active and NULL once archived, so the unique index
// (workspace_key, active_repo_key) allows one active environment per
// repository on every dialect (NULLs never collide). Writes use a version CAS.
//
// coding_environment_secrets holds AES-256-GCM ciphertext keyed by
// FLOW_CODING_SECRET_KEY (falling back to FLOW_CONNECTOR_SECRET_KEY) with
// AAD = environment id + NUL + name. There is no read API for values and no
// update: a secret is added once, then deleted and re-added. Values are only
// decrypted for a sandbox's setup phase (CodingEnvironmentSecretValues).
//
// coding_environment_snapshots rows carry the public snapshot plus the
// reconciler's build state in `data`; leases use (lease_owner, lease_until)
// CAS updates, as agent_runs does, and lease_until doubles as the next due time.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/codingenv"
	"flow/api/internal/domain"
)

// ErrCodingSecretUnreadable reports a secret that no longer decrypts (the key
// was rotated or is missing); the admin must delete and re-add it.
var ErrCodingSecretUnreadable = errors.New("coding: secret unreadable")

// Snapshot log limits.
const (
	codingSnapshotLogMaxBytes   = 8 << 20
	codingSnapshotLogChunkBytes = 256 << 10
)

var codingSecretKey struct {
	sync.RWMutex
	key []byte
}

// SetCodingSecretKey configures the AES-256 key (base64) for environment
// secrets. An empty key keeps the environment fallback
// (FLOW_CODING_SECRET_KEY, then FLOW_CONNECTOR_SECRET_KEY).
func SetCodingSecretKey(encoded string) error {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return errors.New("FLOW_CODING_SECRET_KEY must be a base64-encoded 32-byte key")
	}
	codingSecretKey.Lock()
	codingSecretKey.key = key
	codingSecretKey.Unlock()
	return nil
}

func codingCipher() (cipher.AEAD, error) {
	codingSecretKey.RLock()
	key := codingSecretKey.key
	codingSecretKey.RUnlock()
	if key == nil {
		encoded := strings.TrimSpace(os.Getenv("FLOW_CODING_SECRET_KEY"))
		if encoded == "" {
			encoded = strings.TrimSpace(os.Getenv("FLOW_CONNECTOR_SECRET_KEY"))
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(decoded) != 32 {
			return nil, errors.New("FLOW_CODING_SECRET_KEY (or FLOW_CONNECTOR_SECRET_KEY) must be a base64-encoded 32-byte key")
		}
		key = decoded
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// CodingSecretsConfigured reports whether environment secrets can be stored.
func CodingSecretsConfigured() bool { _, err := codingCipher(); return err == nil }

func codingSecretAAD(environmentID, name string) []byte {
	return []byte(environmentID + "\x00" + name)
}

// ensureCodingEnvironmentExtras creates track K's additions to the coding
// schema (called from ensureCodingSessions).
func (s *SQLiteStore) ensureCodingEnvironmentExtras(ctx context.Context) error {
	blob := "BLOB"
	switch s.dialect {
	case "mysql":
		blob = "LONGBLOB"
	case "postgres":
		blob = "BYTEA"
	}
	id := "VARCHAR(191)"
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS coding_snapshot_logs(snapshot_id `+id+` NOT NULL,seq BIGINT NOT NULL,workspace_key `+id+` NOT NULL,chunk `+blob+` NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(snapshot_id,seq))`); err != nil {
		return err
	}
	return nil
}

func isCodingUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "duplicate key") || strings.Contains(message, "duplicate entry") || strings.Contains(message, "unique violation") || strings.Contains(message, "constraint failed: unique") || strings.Contains(message, "23505") || strings.Contains(message, "error 1062")
}

func codingMillisPtr(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}

// ---------------------------------------------------------------------------
// Environments

const codingEnvironmentColumns = `id,workspace_key,version,archived_at,data,created_at,updated_at`

type codingRowScanner interface{ Scan(dest ...any) error }

func scanCodingEnvironment(row codingRowScanner) (domain.CodingEnvironment, error) {
	var (
		environment                    domain.CodingEnvironment
		id, workspace                  string
		version, archived, created, up int64
		data                           []byte
	)
	if err := row.Scan(&id, &workspace, &version, &archived, &data, &created, &up); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return environment, domain.ErrCodingNotFound
		}
		return environment, err
	}
	if err := json.Unmarshal(data, &environment); err != nil {
		return environment, fmt.Errorf("decode coding environment %s: %w", id, err)
	}
	environment.ID, environment.WorkspaceKey, environment.Version = id, workspace, version
	environment.CreatedAt, environment.UpdatedAt = codingTime(created), codingTime(up)
	environment.ArchivedAt = nil
	if archived > 0 {
		at := codingTime(archived)
		environment.ArchivedAt = &at
	}
	environment.Secrets, environment.LatestSnapshot = nil, nil
	return environment, nil
}

func encodeCodingEnvironment(environment domain.CodingEnvironment) ([]byte, error) {
	environment.Secrets, environment.LatestSnapshot, environment.ArchivedAt = nil, nil, nil
	return json.Marshal(environment)
}

func activeRepoKeyValue(environment domain.CodingEnvironment) any {
	if environment.ArchivedAt != nil {
		return nil
	}
	return codingenv.ActiveRepoKey(environment.CodeHost, environment.HostName, environment.Repository)
}

// CreateCodingEnvironment validates and inserts an environment. It returns
// domain.ErrCodingRepoInUse when the repository already has an active environment.
func (s *SQLiteStore) CreateCodingEnvironment(ctx context.Context, environment domain.CodingEnvironment) (domain.CodingEnvironment, error) {
	if strings.TrimSpace(environment.WorkspaceKey) == "" {
		return domain.CodingEnvironment{}, fmt.Errorf("%w: workspace is required", codingenv.ErrInvalidEnvironment)
	}
	if err := codingenv.NormalizeEnvironment(&environment); err != nil {
		return domain.CodingEnvironment{}, err
	}
	now := time.Now().UTC()
	if environment.ID == "" {
		environment.ID = codingenv.NewResourceID(now)
	}
	if len(environment.ID) > 191 {
		return domain.CodingEnvironment{}, fmt.Errorf("%w: id is too long", codingenv.ErrInvalidEnvironment)
	}
	if environment.CreatedAt.IsZero() {
		environment.CreatedAt = now
	}
	environment.UpdatedAt, environment.Version, environment.PendingProposal = now, 1, nil
	if err := s.insertCodingEnvironment(ctx, s.db, environment); err != nil {
		return domain.CodingEnvironment{}, err
	}
	return s.CodingEnvironment(ctx, environment.WorkspaceKey, environment.ID)
}

type codingExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *SQLiteStore) insertCodingEnvironment(ctx context.Context, db codingExecer, environment domain.CodingEnvironment) error {
	data, err := encodeCodingEnvironment(environment)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `INSERT INTO coding_environments(id,workspace_key,name,code_host,host_name,repository,active_repo_key,archived_at,version,data,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		environment.ID, environment.WorkspaceKey, environment.Name, environment.CodeHost, environment.HostName, environment.Repository, activeRepoKeyValue(environment), codingMillisPtr(environment.ArchivedAt), environment.Version, data, environment.CreatedAt.UnixMilli(), environment.UpdatedAt.UnixMilli())
	if isCodingUniqueViolation(err) {
		var exists int
		if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_environments WHERE id=?`, environment.ID).Scan(&exists) == nil && exists > 0 {
			return fmt.Errorf("%w: an environment with this id already exists", domain.ErrCodingVersionConflict)
		}
		return domain.ErrCodingRepoInUse
	}
	return err
}

// CodingEnvironment returns one environment with its secret names (never
// values) and its newest snapshot.
func (s *SQLiteStore) CodingEnvironment(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironment, error) {
	environment, err := scanCodingEnvironment(s.db.QueryRowContext(ctx, `SELECT `+codingEnvironmentColumns+` FROM coding_environments WHERE workspace_key=? AND id=?`, workspaceKey, id))
	if err != nil {
		return environment, err
	}
	return s.decorateCodingEnvironment(ctx, environment)
}

// CodingEnvironmentForRepository returns the active environment of a repository.
func (s *SQLiteStore) CodingEnvironmentForRepository(ctx context.Context, workspaceKey, codeHost, hostName, repository string) (domain.CodingEnvironment, error) {
	_, repository = codingenv.ParseRepositoryInput(repository)
	if hostName == "" {
		hostName = codingenv.DefaultHostName(codeHost)
	}
	key := codingenv.ActiveRepoKey(strings.ToLower(codeHost), strings.ToLower(hostName), repository)
	environment, err := scanCodingEnvironment(s.db.QueryRowContext(ctx, `SELECT `+codingEnvironmentColumns+` FROM coding_environments WHERE workspace_key=? AND active_repo_key=?`, workspaceKey, key))
	if err != nil {
		return environment, err
	}
	return s.decorateCodingEnvironment(ctx, environment)
}

// ListCodingEnvironments lists a workspace's environments by name.
func (s *SQLiteStore) ListCodingEnvironments(ctx context.Context, workspaceKey string, includeArchived bool) ([]domain.CodingEnvironment, error) {
	query := `SELECT ` + codingEnvironmentColumns + ` FROM coding_environments WHERE workspace_key=?`
	if !includeArchived {
		query += ` AND archived_at=0`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY name,id`, workspaceKey)
	if err != nil {
		return nil, err
	}
	environments := []domain.CodingEnvironment{}
	for rows.Next() {
		environment, err := scanCodingEnvironment(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		environments = append(environments, environment)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	secrets, err := s.codingSecretRefs(ctx, workspaceKey, "")
	if err != nil {
		return nil, err
	}
	for index := range environments {
		environments[index].Secrets = secrets[environments[index].ID]
		if latest, err := s.latestCodingSnapshot(ctx, workspaceKey, environments[index].ID); err == nil {
			environments[index].LatestSnapshot = &latest
		} else if !errors.Is(err, domain.ErrCodingNotFound) {
			return nil, err
		}
	}
	return environments, nil
}

func (s *SQLiteStore) decorateCodingEnvironment(ctx context.Context, environment domain.CodingEnvironment) (domain.CodingEnvironment, error) {
	secrets, err := s.codingSecretRefs(ctx, environment.WorkspaceKey, environment.ID)
	if err != nil {
		return environment, err
	}
	environment.Secrets = secrets[environment.ID]
	latest, err := s.latestCodingSnapshot(ctx, environment.WorkspaceKey, environment.ID)
	if err == nil {
		environment.LatestSnapshot = &latest
	} else if !errors.Is(err, domain.ErrCodingNotFound) {
		return environment, err
	}
	return environment, nil
}

// UpdateCodingEnvironment applies mutate under a version CAS. version 0 skips
// the caller's version check (internal writers); the row version still
// guards against concurrent writes. Identity fields, secrets, archive state
// and timestamps cannot be changed through mutate.
func (s *SQLiteStore) UpdateCodingEnvironment(ctx context.Context, workspaceKey, id string, version int64, mutate func(*domain.CodingEnvironment) error) (domain.CodingEnvironment, error) {
	for attempt := 0; attempt < 5; attempt++ {
		current, err := scanCodingEnvironment(s.db.QueryRowContext(ctx, `SELECT `+codingEnvironmentColumns+` FROM coding_environments WHERE workspace_key=? AND id=?`, workspaceKey, id))
		if err != nil {
			return domain.CodingEnvironment{}, err
		}
		if version > 0 && current.Version != version {
			return domain.CodingEnvironment{}, domain.ErrCodingVersionConflict
		}
		next := current
		next.OwnerIDs = slices.Clone(current.OwnerIDs)
		next.AllowedDomains = slices.Clone(current.AllowedDomains)
		if err := mutate(&next); err != nil {
			return domain.CodingEnvironment{}, err
		}
		next.ID, next.WorkspaceKey, next.CreatedAt, next.CreatedBy, next.ArchivedAt = current.ID, current.WorkspaceKey, current.CreatedAt, current.CreatedBy, current.ArchivedAt
		if err := codingenv.NormalizeEnvironment(&next); err != nil {
			return domain.CodingEnvironment{}, err
		}
		next.Version = current.Version + 1
		next.UpdatedAt = time.Now().UTC()
		if !next.UpdatedAt.After(current.UpdatedAt) {
			next.UpdatedAt = current.UpdatedAt.Add(time.Millisecond)
		}
		data, err := encodeCodingEnvironment(next)
		if err != nil {
			return domain.CodingEnvironment{}, err
		}
		result, err := s.db.ExecContext(ctx, `UPDATE coding_environments SET name=?,code_host=?,host_name=?,repository=?,active_repo_key=?,version=?,data=?,updated_at=? WHERE workspace_key=? AND id=? AND version=?`,
			next.Name, next.CodeHost, next.HostName, next.Repository, activeRepoKeyValue(next), next.Version, data, next.UpdatedAt.UnixMilli(), workspaceKey, id, current.Version)
		if isCodingUniqueViolation(err) {
			return domain.CodingEnvironment{}, domain.ErrCodingRepoInUse
		}
		if err != nil {
			return domain.CodingEnvironment{}, err
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			return s.CodingEnvironment(ctx, workspaceKey, id)
		}
		if version > 0 {
			return domain.CodingEnvironment{}, domain.ErrCodingVersionConflict
		}
	}
	return domain.CodingEnvironment{}, domain.ErrCodingVersionConflict
}

// SetCodingEnvironmentArchived archives (clearing active_repo_key) or
// unarchives an environment. Unarchiving fails with domain.ErrCodingRepoInUse when
// another active environment uses the repository. Repeating either is a no-op.
func (s *SQLiteStore) SetCodingEnvironmentArchived(ctx context.Context, workspaceKey, id string, archived bool) (domain.CodingEnvironment, error) {
	current, err := scanCodingEnvironment(s.db.QueryRowContext(ctx, `SELECT `+codingEnvironmentColumns+` FROM coding_environments WHERE workspace_key=? AND id=?`, workspaceKey, id))
	if err != nil {
		return domain.CodingEnvironment{}, err
	}
	if (current.ArchivedAt != nil) == archived {
		return s.decorateCodingEnvironment(ctx, current)
	}
	now := time.Now().UTC()
	var archivedAt int64
	var repoKey any
	if archived {
		archivedAt = now.UnixMilli()
	} else {
		repoKey = codingenv.ActiveRepoKey(current.CodeHost, current.HostName, current.Repository)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE coding_environments SET archived_at=?,active_repo_key=?,version=version+1,updated_at=? WHERE workspace_key=? AND id=? AND version=?`, archivedAt, repoKey, now.UnixMilli(), workspaceKey, id, current.Version)
	if isCodingUniqueViolation(err) {
		return domain.CodingEnvironment{}, domain.ErrCodingRepoInUse
	}
	if err != nil {
		return domain.CodingEnvironment{}, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return domain.CodingEnvironment{}, domain.ErrCodingVersionConflict
	}
	return s.CodingEnvironment(ctx, workspaceKey, id)
}

// SetCodingEnvironmentProposal stores (or, with nil, clears) the pending
// agent proposal without changing the editor's version.
func (s *SQLiteStore) SetCodingEnvironmentProposal(ctx context.Context, workspaceKey, id string, proposal *domain.CodingEnvironmentProposal) (domain.CodingEnvironment, error) {
	for attempt := 0; attempt < 5; attempt++ {
		var data []byte
		var updated int64
		if err := s.db.QueryRowContext(ctx, `SELECT data,updated_at FROM coding_environments WHERE workspace_key=? AND id=?`, workspaceKey, id).Scan(&data, &updated); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.CodingEnvironment{}, domain.ErrCodingNotFound
			}
			return domain.CodingEnvironment{}, err
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(data, &document); err != nil {
			return domain.CodingEnvironment{}, err
		}
		if proposal == nil {
			delete(document, "pendingProposal")
		} else {
			encoded, err := json.Marshal(proposal)
			if err != nil {
				return domain.CodingEnvironment{}, err
			}
			document["pendingProposal"] = encoded
		}
		next, err := json.Marshal(document)
		if err != nil {
			return domain.CodingEnvironment{}, err
		}
		now := max(time.Now().UnixMilli(), updated+1)
		result, err := s.db.ExecContext(ctx, `UPDATE coding_environments SET data=?,updated_at=? WHERE workspace_key=? AND id=? AND updated_at=?`, next, now, workspaceKey, id, updated)
		if err != nil {
			return domain.CodingEnvironment{}, err
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			return s.CodingEnvironment(ctx, workspaceKey, id)
		}
	}
	return domain.CodingEnvironment{}, domain.ErrCodingVersionConflict
}

// ---------------------------------------------------------------------------
// Secrets

func (s *SQLiteStore) codingSecretRefs(ctx context.Context, workspaceKey, environmentID string) (map[string][]domain.CodingSecretRef, error) {
	query, args := `SELECT environment_id,name,created_by,created_at FROM coding_environment_secrets WHERE workspace_key=?`, []any{workspaceKey}
	if environmentID != "" {
		query += ` AND environment_id=?`
		args = append(args, environmentID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY environment_id,name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]domain.CodingSecretRef{}
	for rows.Next() {
		var environment, name, by string
		var created int64
		if err := rows.Scan(&environment, &name, &by, &created); err != nil {
			return nil, err
		}
		result[environment] = append(result[environment], domain.CodingSecretRef{Name: name, CreatedBy: by, CreatedAt: codingTime(created)})
	}
	return result, rows.Err()
}

// CodingEnvironmentSecretNames lists the names of an environment's secrets.
func (s *SQLiteStore) CodingEnvironmentSecretNames(ctx context.Context, workspaceKey, environmentID string) ([]string, error) {
	refs, err := s.codingSecretRefs(ctx, workspaceKey, environmentID)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(refs[environmentID]))
	for _, ref := range refs[environmentID] {
		names = append(names, ref.Name)
	}
	return names, nil
}

// PutCodingEnvironmentSecret encrypts and stores a new secret. Secrets are
// immutable: an existing name fails with domain.ErrCodingSecretExists.
func (s *SQLiteStore) PutCodingEnvironmentSecret(ctx context.Context, workspaceKey, environmentID, name, value, createdBy string) error {
	if err := codingenv.ValidateSecret(name, value); err != nil {
		return err
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_environments WHERE workspace_key=? AND id=?`, workspaceKey, environmentID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return domain.ErrCodingNotFound
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_environment_secrets WHERE environment_id=?`, environmentID).Scan(&count); err != nil {
		return err
	}
	if count >= codingenv.EnvironmentSecretsMax {
		return fmt.Errorf("%w: at most %d secrets per environment", codingenv.ErrInvalidEnvironment, codingenv.EnvironmentSecretsMax)
	}
	aead, err := codingCipher()
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nonce, nonce, []byte(value), codingSecretAAD(environmentID, name))
	_, err = s.db.ExecContext(ctx, `INSERT INTO coding_environment_secrets(environment_id,name,workspace_key,ciphertext,created_by,created_at) VALUES(?,?,?,?,?,?)`, environmentID, name, workspaceKey, sealed, createdBy, time.Now().UnixMilli())
	if isCodingUniqueViolation(err) {
		return domain.ErrCodingSecretExists
	}
	return err
}

// DeleteCodingEnvironmentSecret removes a secret (domain.ErrCodingNotFound when absent).
func (s *SQLiteStore) DeleteCodingEnvironmentSecret(ctx context.Context, workspaceKey, environmentID, name string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM coding_environment_secrets WHERE workspace_key=? AND environment_id=? AND name=?`, workspaceKey, environmentID, name)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return domain.ErrCodingNotFound
	}
	return nil
}

// CodingEnvironmentSecretValues decrypts an environment's secrets. Only the
// setup-secrets runtime route calls it (once per sandbox, during setup).
func (s *SQLiteStore) CodingEnvironmentSecretValues(ctx context.Context, workspaceKey, environmentID string) ([]domain.CodingEnvVar, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name,ciphertext FROM coding_environment_secrets WHERE workspace_key=? AND environment_id=? ORDER BY name`, workspaceKey, environmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type sealed struct {
		name   string
		cipher []byte
	}
	var items []sealed
	for rows.Next() {
		var item sealed
		if err := rows.Scan(&item.name, &item.cipher); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]domain.CodingEnvVar, 0, len(items))
	if len(items) == 0 {
		return values, nil
	}
	aead, err := codingCipher()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCodingSecretUnreadable, err)
	}
	for _, item := range items {
		if len(item.cipher) < aead.NonceSize() {
			return nil, fmt.Errorf("%w: %s", ErrCodingSecretUnreadable, item.name)
		}
		plain, err := aead.Open(nil, item.cipher[:aead.NonceSize()], item.cipher[aead.NonceSize():], codingSecretAAD(environmentID, item.name))
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrCodingSecretUnreadable, item.name)
		}
		values = append(values, domain.CodingEnvVar{Name: item.name, Value: string(plain)})
	}
	return values, nil
}

// ---------------------------------------------------------------------------
// Snapshots

type codingSnapshotData struct {
	Snapshot        domain.CodingEnvironmentSnapshot `json:"snapshot"`
	StartFailures   int                              `json:"startFailures,omitempty"`
	FinishFailures  int                              `json:"finishFailures,omitempty"`
	Plan            json.RawMessage                  `json:"plan,omitempty"`
	Result          *codingenv.SnapshotResult        `json:"result,omitempty"`
	ResultSandboxID string                           `json:"resultSandboxId,omitempty"`
	ResultSeq       int64                            `json:"resultSeq,omitempty"`
	SucceededAt     *time.Time                       `json:"succeededAt,omitempty"`
}

const codingSnapshotColumns = `id,workspace_key,status,storage_ref,attempts,cancel_requested,data,created_at,updated_at`

type codingSnapshotRow struct {
	job       codingenv.SnapshotJob
	data      codingSnapshotData
	updatedAt int64
}

func scanCodingSnapshot(row codingRowScanner) (codingSnapshotRow, error) {
	var (
		result                    codingSnapshotRow
		id, workspace, status, sr string
		attempts, cancel          int
		data                      []byte
		created, updated          int64
	)
	if err := row.Scan(&id, &workspace, &status, &sr, &attempts, &cancel, &data, &created, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, domain.ErrCodingNotFound
		}
		return result, err
	}
	if err := json.Unmarshal(data, &result.data); err != nil {
		return result, fmt.Errorf("decode coding snapshot %s: %w", id, err)
	}
	snapshot := result.data.Snapshot
	snapshot.ID, snapshot.Status, snapshot.StorageRef, snapshot.CreatedAt = id, status, sr, codingTime(created)
	result.data.Snapshot = snapshot
	result.updatedAt = updated
	result.job = codingenv.SnapshotJob{
		WorkspaceKey: workspace, Snapshot: snapshot, Attempts: attempts, StartFailures: result.data.StartFailures, FinishFailures: result.data.FinishFailures,
		CancelRequested: cancel != 0, Plan: result.data.Plan, SucceededAt: result.data.SucceededAt, ResultSeq: result.data.ResultSeq,
	}
	if result.data.Result != nil && result.data.ResultSandboxID == snapshot.SandboxID {
		copied := *result.data.Result
		result.job.Result = &copied
	}
	return result, nil
}

func snapshotTerminal(status string) bool {
	return status != domain.CodingSnapshotQueued && status != domain.CodingSnapshotBuilding
}

// CreateCodingSnapshot inserts a snapshot row (status queued unless set).
func (s *SQLiteStore) CreateCodingSnapshot(ctx context.Context, workspaceKey string, snapshot domain.CodingEnvironmentSnapshot) error {
	if snapshot.ID == "" || snapshot.EnvironmentID == "" {
		return errors.New("coding: snapshot id and environment are required")
	}
	if snapshot.Status == "" {
		snapshot.Status = domain.CodingSnapshotQueued
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(codingSnapshotData{Snapshot: snapshot})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO coding_environment_snapshots(id,workspace_key,environment_id,status,strategy,config_hash,storage_ref,data,created_at,updated_at,finished_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		snapshot.ID, workspaceKey, snapshot.EnvironmentID, snapshot.Status, snapshot.Strategy, snapshot.ConfigHash, snapshot.StorageRef, data, snapshot.CreatedAt.UnixMilli(), snapshot.CreatedAt.UnixMilli(), codingMillisPtr(snapshot.FinishedAt), codingMillisPtr(snapshot.ExpiresAt))
	if isCodingUniqueViolation(err) {
		return domain.ErrCodingVersionConflict
	}
	return err
}

func (s *SQLiteStore) codingSnapshotRow(ctx context.Context, workspaceKey, id string) (codingSnapshotRow, error) {
	return scanCodingSnapshot(s.db.QueryRowContext(ctx, `SELECT `+codingSnapshotColumns+` FROM coding_environment_snapshots WHERE workspace_key=? AND id=?`, workspaceKey, id))
}

// CodingSnapshot returns one snapshot.
func (s *SQLiteStore) CodingSnapshot(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironmentSnapshot, error) {
	row, err := s.codingSnapshotRow(ctx, workspaceKey, id)
	return row.job.Snapshot, err
}

// CodingSnapshotJob returns one snapshot with its build state.
func (s *SQLiteStore) CodingSnapshotJob(ctx context.Context, workspaceKey, id string) (codingenv.SnapshotJob, error) {
	row, err := s.codingSnapshotRow(ctx, workspaceKey, id)
	return row.job, err
}

func (s *SQLiteStore) querySnapshots(ctx context.Context, query string, args ...any) ([]codingSnapshotRow, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []codingSnapshotRow
	for rows.Next() {
		row, err := scanCodingSnapshot(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// ListCodingSnapshots lists an environment's snapshots, newest first.
func (s *SQLiteStore) ListCodingSnapshots(ctx context.Context, workspaceKey, environmentID string, limit int) ([]domain.CodingEnvironmentSnapshot, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.querySnapshots(ctx, `SELECT `+codingSnapshotColumns+` FROM coding_environment_snapshots WHERE workspace_key=? AND environment_id=? ORDER BY created_at DESC,id DESC LIMIT ?`, workspaceKey, environmentID, limit)
	if err != nil {
		return nil, err
	}
	snapshots := make([]domain.CodingEnvironmentSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshots = append(snapshots, row.job.Snapshot)
	}
	return snapshots, nil
}

func (s *SQLiteStore) latestCodingSnapshot(ctx context.Context, workspaceKey, environmentID string) (domain.CodingEnvironmentSnapshot, error) {
	snapshots, err := s.ListCodingSnapshots(ctx, workspaceKey, environmentID, 1)
	if err != nil {
		return domain.CodingEnvironmentSnapshot{}, err
	}
	if len(snapshots) == 0 {
		return domain.CodingEnvironmentSnapshot{}, domain.ErrCodingNotFound
	}
	return snapshots[0], nil
}

// ActiveCodingSnapshots lists queued and building snapshots of an environment.
func (s *SQLiteStore) ActiveCodingSnapshots(ctx context.Context, workspaceKey, environmentID string) ([]domain.CodingEnvironmentSnapshot, error) {
	rows, err := s.querySnapshots(ctx, `SELECT `+codingSnapshotColumns+` FROM coding_environment_snapshots WHERE workspace_key=? AND environment_id=? AND status IN ('queued','building') ORDER BY created_at DESC,id DESC`, workspaceKey, environmentID)
	if err != nil {
		return nil, err
	}
	snapshots := make([]domain.CodingEnvironmentSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshots = append(snapshots, row.job.Snapshot)
	}
	return snapshots, nil
}

// LatestReadyCodingSnapshot returns the newest unexpired ready snapshot with configHash.
func (s *SQLiteStore) LatestReadyCodingSnapshot(ctx context.Context, workspaceKey, environmentID, configHash string) (domain.CodingEnvironmentSnapshot, error) {
	rows, err := s.querySnapshots(ctx, `SELECT `+codingSnapshotColumns+` FROM coding_environment_snapshots WHERE workspace_key=? AND environment_id=? AND config_hash=? AND status='ready' AND (expires_at=0 OR expires_at>?) ORDER BY created_at DESC,id DESC LIMIT 1`, workspaceKey, environmentID, configHash, time.Now().UnixMilli())
	if err != nil {
		return domain.CodingEnvironmentSnapshot{}, err
	}
	if len(rows) == 0 {
		return domain.CodingEnvironmentSnapshot{}, domain.ErrCodingNotFound
	}
	return rows[0].job.Snapshot, nil
}

// LatestUsableCodingSnapshot returns the newest unexpired ready snapshot of
// an environment regardless of its config hash (the debug terminal).
func (s *SQLiteStore) LatestUsableCodingSnapshot(ctx context.Context, workspaceKey, environmentID string) (domain.CodingEnvironmentSnapshot, error) {
	rows, err := s.querySnapshots(ctx, `SELECT `+codingSnapshotColumns+` FROM coding_environment_snapshots WHERE workspace_key=? AND environment_id=? AND status='ready' AND storage_ref<>'' AND (expires_at=0 OR expires_at>?) ORDER BY created_at DESC,id DESC LIMIT 1`, workspaceKey, environmentID, time.Now().UnixMilli())
	if err != nil {
		return domain.CodingEnvironmentSnapshot{}, err
	}
	if len(rows) == 0 {
		return domain.CodingEnvironmentSnapshot{}, domain.ErrCodingNotFound
	}
	return rows[0].job.Snapshot, nil
}

// writeSnapshotRow saves row.data under an updated_at CAS (extra conditions
// in where). It reports whether the row was written.
func (s *SQLiteStore) writeSnapshotRow(ctx context.Context, row codingSnapshotRow, set string, setArgs []any, where string, whereArgs ...any) (bool, error) {
	snapshot := row.data.Snapshot
	data, err := json.Marshal(row.data)
	if err != nil {
		return false, err
	}
	now := max(time.Now().UnixMilli(), row.updatedAt+1)
	args := []any{snapshot.Status, snapshot.StorageRef, data, now, codingMillisPtr(snapshot.FinishedAt), codingMillisPtr(snapshot.ExpiresAt)}
	args = append(args, setArgs...)
	args = append(args, row.job.WorkspaceKey, snapshot.ID, row.updatedAt)
	args = append(args, whereArgs...)
	query := `UPDATE coding_environment_snapshots SET status=?,storage_ref=?,data=?,updated_at=?,finished_at=?,expires_at=?` + set + ` WHERE workspace_key=? AND id=? AND updated_at=?` + where
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected == 1, nil
}

// UpdateCodingSnapshot applies mutate to a snapshot (CAS on updated_at).
func (s *SQLiteStore) UpdateCodingSnapshot(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingEnvironmentSnapshot) error) (domain.CodingEnvironmentSnapshot, error) {
	for attempt := 0; attempt < 8; attempt++ {
		row, err := s.codingSnapshotRow(ctx, workspaceKey, id)
		if err != nil {
			return domain.CodingEnvironmentSnapshot{}, err
		}
		next := row.data.Snapshot
		if err := mutate(&next); err != nil {
			return domain.CodingEnvironmentSnapshot{}, err
		}
		next.ID, next.EnvironmentID, next.CreatedAt = row.data.Snapshot.ID, row.data.Snapshot.EnvironmentID, row.data.Snapshot.CreatedAt
		row.data.Snapshot = next
		ok, err := s.writeSnapshotRow(ctx, row, "", nil, "")
		if err != nil {
			return domain.CodingEnvironmentSnapshot{}, err
		}
		if ok {
			return next, nil
		}
	}
	return domain.CodingEnvironmentSnapshot{}, domain.ErrCodingVersionConflict
}

// RequestCodingSnapshotCancel marks a queued or building snapshot for cancel
// (the reconciler stops its sandbox). A terminal snapshot is returned as is.
func (s *SQLiteStore) RequestCodingSnapshotCancel(ctx context.Context, workspaceKey, id string) (domain.CodingEnvironmentSnapshot, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE coding_environment_snapshots SET cancel_requested=1 WHERE workspace_key=? AND id=? AND status IN ('queued','building')`, workspaceKey, id); err != nil {
		return domain.CodingEnvironmentSnapshot{}, err
	}
	return s.CodingSnapshot(ctx, workspaceKey, id)
}

// RecordCodingSnapshotResult stores the runner's complete call for the
// current build sandbox. A repeated identical call is accepted; a result from
// an older attempt's sandbox, or for a finished snapshot, fails with
// domain.ErrCodingVersionConflict.
func (s *SQLiteStore) RecordCodingSnapshotResult(ctx context.Context, workspaceKey, id, sandboxID string, result codingenv.SnapshotResult) error {
	for attempt := 0; attempt < 8; attempt++ {
		row, err := s.codingSnapshotRow(ctx, workspaceKey, id)
		if err != nil {
			return err
		}
		snapshot := row.data.Snapshot
		if snapshot.Status != domain.CodingSnapshotBuilding || snapshot.SandboxID != sandboxID {
			return domain.ErrCodingVersionConflict
		}
		if row.data.Result != nil && row.data.ResultSandboxID == sandboxID {
			return nil
		}
		copied := result
		row.data.Result, row.data.ResultSandboxID, row.data.ResultSeq = &copied, sandboxID, row.data.ResultSeq+1
		ok, err := s.writeSnapshotRow(ctx, row, "", nil, "")
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return domain.ErrCodingVersionConflict
}

// SupersedeQueuedCodingSnapshots marks an environment's queued snapshots,
// except exceptID, superseded.
func (s *SQLiteStore) SupersedeQueuedCodingSnapshots(ctx context.Context, workspaceKey, environmentID, exceptID string, now time.Time) (int, error) {
	rows, err := s.querySnapshots(ctx, `SELECT `+codingSnapshotColumns+` FROM coding_environment_snapshots WHERE workspace_key=? AND environment_id=? AND status='queued' AND id<>?`, workspaceKey, environmentID, exceptID)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, row := range rows {
		finished := now.UTC()
		row.data.Snapshot.Status, row.data.Snapshot.FinishedAt = domain.CodingSnapshotSuperseded, &finished
		// Only while still queued and unleased: a build that just started keeps going.
		ok, err := s.writeSnapshotRow(ctx, row, "", nil, ` AND status='queued' AND lease_until<?`, now.UnixMilli())
		if err != nil {
			return count, err
		}
		if ok {
			count++
		}
	}
	return count, nil
}

// LeaseCodingSnapshotJobs leases due queued and building snapshots to owner.
func (s *SQLiteStore) LeaseCodingSnapshotJobs(ctx context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]codingenv.SnapshotJob, error) {
	if owner == "" || len(owner) > 191 {
		return nil, errors.New("coding: lease owner is required")
	}
	if limit <= 0 {
		limit = 20
	}
	at := now.UnixMilli()
	candidates, err := s.querySnapshots(ctx, `SELECT `+codingSnapshotColumns+` FROM coding_environment_snapshots WHERE status IN ('queued','building') AND lease_until<=? ORDER BY lease_until,created_at LIMIT ?`, at, limit)
	if err != nil {
		return nil, err
	}
	jobs := make([]codingenv.SnapshotJob, 0, len(candidates))
	for _, candidate := range candidates {
		result, err := s.db.ExecContext(ctx, `UPDATE coding_environment_snapshots SET lease_owner=?,lease_until=? WHERE id=? AND status IN ('queued','building') AND lease_until<=? AND updated_at=?`, owner, now.Add(lease).UnixMilli(), candidate.job.Snapshot.ID, at, candidate.updatedAt)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			jobs = append(jobs, candidate.job)
		}
	}
	return jobs, nil
}

// SaveCodingSnapshotJob writes the reconciler's step under owner's lease and
// makes the row due at next. Terminal rows release the lease for good.
func (s *SQLiteStore) SaveCodingSnapshotJob(ctx context.Context, owner string, job codingenv.SnapshotJob, next time.Time) error {
	for attempt := 0; attempt < 8; attempt++ {
		row, err := s.codingSnapshotRow(ctx, job.WorkspaceKey, job.Snapshot.ID)
		if err != nil {
			return err
		}
		stored := row.data
		data := codingSnapshotData{
			Snapshot: job.Snapshot, StartFailures: job.StartFailures, FinishFailures: job.FinishFailures, Plan: job.Plan, SucceededAt: job.SucceededAt, ResultSeq: stored.ResultSeq,
		}
		data.Snapshot.ID, data.Snapshot.EnvironmentID, data.Snapshot.CreatedAt = stored.Snapshot.ID, stored.Snapshot.EnvironmentID, stored.Snapshot.CreatedAt
		// Keep a runner result for the current sandbox unless this step
		// consumed it: one that arrived during the step (newer sequence) or one
		// the step left for later (job.Result still set) survives.
		if stored.Result != nil && stored.ResultSandboxID == job.Snapshot.SandboxID && job.Snapshot.Status == domain.CodingSnapshotBuilding {
			if stored.ResultSeq > job.ResultSeq || job.Result != nil {
				data.Result, data.ResultSandboxID = stored.Result, stored.ResultSandboxID
			}
		}
		row.data = data
		leaseUntil := next.UnixMilli()
		leaseOwner := owner
		if snapshotTerminal(job.Snapshot.Status) {
			leaseUntil, leaseOwner = 0, ""
		}
		ok, err := s.writeSnapshotRow(ctx, row, `,attempts=?,lease_owner=?,lease_until=?`, []any{job.Attempts, leaseOwner, leaseUntil}, ` AND lease_owner=?`, owner)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		var currentOwner string
		if err := s.db.QueryRowContext(ctx, `SELECT lease_owner FROM coding_environment_snapshots WHERE id=?`, job.Snapshot.ID).Scan(&currentOwner); err != nil {
			return err
		}
		if currentOwner != owner {
			return domain.ErrCodingLeaseLost
		}
	}
	return domain.ErrCodingVersionConflict
}

// PrunableCodingSnapshots returns ready snapshots that expired or fall beyond
// the newest retain per environment.
func (s *SQLiteStore) PrunableCodingSnapshots(ctx context.Context, now time.Time, retain, limit int) ([]codingenv.SnapshotJob, error) {
	if retain < 1 {
		retain = 1
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.querySnapshots(ctx, `SELECT `+codingSnapshotColumns+` FROM (SELECT `+codingSnapshotColumns+`,expires_at,ROW_NUMBER() OVER (PARTITION BY workspace_key,environment_id ORDER BY created_at DESC,id DESC) AS rn FROM coding_environment_snapshots WHERE status='ready') ranked WHERE rn>? OR (expires_at>0 AND expires_at<=?) LIMIT ?`, retain, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	jobs := make([]codingenv.SnapshotJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, row.job)
	}
	return jobs, nil
}

// MarkCodingSnapshotPruned records that a ready snapshot's storage was deleted.
func (s *SQLiteStore) MarkCodingSnapshotPruned(ctx context.Context, workspaceKey, id, status string, now time.Time) error {
	_, err := s.UpdateCodingSnapshot(ctx, workspaceKey, id, func(snapshot *domain.CodingEnvironmentSnapshot) error {
		snapshot.Status, snapshot.StorageRef = status, ""
		return nil
	})
	return err
}

// TrimCodingSnapshotHistory deletes finished snapshot rows (and their logs)
// beyond the newest keep per environment. Rows that still hold storage are kept.
func (s *SQLiteStore) TrimCodingSnapshotHistory(ctx context.Context, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM (SELECT id,status,storage_ref,ROW_NUMBER() OVER (PARTITION BY workspace_key,environment_id ORDER BY created_at DESC,id DESC) AS rn FROM coding_environment_snapshots) ranked WHERE rn>? AND storage_ref='' AND status NOT IN ('queued','building') LIMIT 200`, keep)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	deleted := 0
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM coding_snapshot_logs WHERE snapshot_id=?`, id); err != nil {
			return deleted, err
		}
		result, err := s.db.ExecContext(ctx, `DELETE FROM coding_environment_snapshots WHERE id=? AND storage_ref='' AND status NOT IN ('queued','building')`, id)
		if err != nil {
			return deleted, err
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			deleted++
		}
	}
	return deleted, nil
}

// StaleCodingEnvironments returns active environments whose newest snapshot
// is ready and was created before readyBefore (auto-refresh candidates).
func (s *SQLiteStore) StaleCodingEnvironments(ctx context.Context, readyBefore time.Time, limit int) ([]codingenv.StaleEnvironment, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.querySnapshots(ctx, `SELECT `+codingSnapshotColumns+` FROM (SELECT `+codingSnapshotColumns+`,ROW_NUMBER() OVER (PARTITION BY workspace_key,environment_id ORDER BY created_at DESC,id DESC) AS rn FROM coding_environment_snapshots WHERE status IN ('queued','building','ready','failed')) ranked WHERE rn=1 AND status='ready' AND created_at<? LIMIT ?`, readyBefore.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	result := []codingenv.StaleEnvironment{}
	for _, row := range rows {
		environment, err := s.CodingEnvironment(ctx, row.job.WorkspaceKey, row.job.Snapshot.EnvironmentID)
		if errors.Is(err, domain.ErrCodingNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if environment.ArchivedAt != nil {
			continue
		}
		result = append(result, codingenv.StaleEnvironment{Environment: environment, Latest: row.job.Snapshot})
	}
	return result, nil
}

// LeaseCodingSnapshots implements codingenv.EnvironmentStore on top of LeaseCodingSnapshotJobs.
func (s *SQLiteStore) LeaseCodingSnapshots(ctx context.Context, owner string, lease time.Duration, limit int) ([]domain.CodingEnvironmentSnapshot, error) {
	jobs, err := s.LeaseCodingSnapshotJobs(ctx, owner, time.Now(), lease, limit)
	if err != nil {
		return nil, err
	}
	snapshots := make([]domain.CodingEnvironmentSnapshot, 0, len(jobs))
	for _, job := range jobs {
		snapshots = append(snapshots, job.Snapshot)
	}
	return snapshots, nil
}

// ---------------------------------------------------------------------------
// Snapshot logs

// AppendCodingSnapshotLog appends runner output to a snapshot's log. Logs are
// capped at 8 MiB per snapshot; later output is dropped after a marker line.
func (s *SQLiteStore) AppendCodingSnapshotLog(ctx context.Context, workspaceKey, snapshotID string, chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}
	if len(chunk) > codingSnapshotLogChunkBytes {
		chunk = chunk[:codingSnapshotLogChunkBytes]
	}
	for attempt := 0; attempt < 8; attempt++ {
		var seq, size int64
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0),COALESCE(SUM(LENGTH(chunk)),0) FROM coding_snapshot_logs WHERE snapshot_id=?`, snapshotID).Scan(&seq, &size); err != nil {
			return err
		}
		if size >= codingSnapshotLogMaxBytes {
			return nil
		}
		if size+int64(len(chunk)) > codingSnapshotLogMaxBytes {
			chunk = append(chunk[:codingSnapshotLogMaxBytes-size-int64(len("\n[log truncated]\n"))], []byte("\n[log truncated]\n")...)
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO coding_snapshot_logs(snapshot_id,seq,workspace_key,chunk,created_at) VALUES(?,?,?,?,?)`, snapshotID, seq+1, workspaceKey, chunk, time.Now().UnixMilli())
		if isCodingUniqueViolation(err) {
			continue
		}
		return err
	}
	return domain.ErrCodingVersionConflict
}

// CodingSnapshotLogs returns log output after seq (0 = from the start), at
// most maxBytes, and the last sequence number read.
func (s *SQLiteStore) CodingSnapshotLogs(ctx context.Context, workspaceKey, snapshotID string, after int64, maxBytes int) ([]byte, int64, error) {
	if maxBytes <= 0 {
		maxBytes = codingSnapshotLogMaxBytes
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq,chunk FROM coding_snapshot_logs WHERE workspace_key=? AND snapshot_id=? AND seq>? ORDER BY seq`, workspaceKey, snapshotID, after)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []byte
	last := after
	for rows.Next() {
		var seq int64
		var chunk []byte
		if err := rows.Scan(&seq, &chunk); err != nil {
			return nil, after, err
		}
		if len(out)+len(chunk) > maxBytes && len(out) > 0 {
			break
		}
		out = append(out, chunk...)
		last = seq
	}
	return out, last, rows.Err()
}

// ---------------------------------------------------------------------------
// Legacy migration

// codingEnvironmentsMigrationVersion records the move of
// WorkspaceSettings.CodingAgentSettings.Environments into coding_environments.
const codingEnvironmentsMigrationVersion = 1100

// migrateLegacyCodingEnvironments copies every workspace's legacy environment
// list (id, name, repository, setupCommand → prepareScript, archived) into
// coding_environments, inferring the code host from the repository URL
// (default github / github.com), then clears the list. It is idempotent: rows
// keep the legacy id, existing ids are skipped, and a second active
// environment for the same repository is stored archived.
func (s *SQLiteStore) migrateLegacyCodingEnvironments(ctx context.Context) error {
	migrated := 0
	for _, key := range s.WorkspaceKeys() {
		var count int
		err := s.rerunBackgroundWrite(ctx, "coding environment migration", key, func() (err error) {
			count, err = s.migrateWorkspaceCodingEnvironments(ctx, key)
			return err
		})
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		migrated += count
	}
	if migrated > 0 {
		slog.Info("moved legacy coding environments into coding_environments", "environments", migrated)
	}
	return nil
}

func (s *SQLiteStore) migrateWorkspaceCodingEnvironments(ctx context.Context, key string) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before, ok := s.storedWorkspace(key)
	if !ok || len(before.WorkspaceSettings.CodingAgentSettings.Environments) == 0 {
		return 0, nil
	}
	migrated := 0
	createdAt := before.WorkspaceSettings.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	for index, legacy := range before.WorkspaceSettings.CodingAgentSettings.Environments {
		environment, ok := legacyCodingEnvironment(key, index, legacy, createdAt)
		if !ok {
			slog.Warn("skipped a legacy coding environment with an unusable repository", "workspace", key, "environment", legacy.ID, "name", legacy.Name, "repository", legacy.Repository)
			continue
		}
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM coding_environments WHERE id=?`, environment.ID).Scan(&exists); err != nil {
			return migrated, err
		}
		if exists > 0 {
			continue
		}
		err := s.insertCodingEnvironment(ctx, s.db, environment)
		if errors.Is(err, domain.ErrCodingRepoInUse) && environment.ArchivedAt == nil {
			archived := createdAt
			environment.ArchivedAt = &archived
			err = s.insertCodingEnvironment(ctx, s.db, environment)
		}
		if err != nil {
			return migrated, err
		}
		migrated++
	}
	after := before
	after.WorkspaceSettings.CodingAgentSettings.Environments = nil
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return migrated, err
	}
	defer tx.Rollback()
	if err := s.persistMetadataTx(ctx, tx, key, before, &after); err != nil {
		return migrated, err
	}
	if err := tx.Commit(); err != nil {
		return migrated, err
	}
	s.installWorkspace(key, after, false)
	s.dropMetadataCache(ctx, key)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, key) {
		s.startupCacheDrops = append(s.startupCacheDrops, key)
	}
	s.mu.Unlock()
	return migrated, nil
}

func legacyCodingEnvironment(workspaceKey string, index int, legacy domain.CodingAgentEnvironment, createdAt time.Time) (domain.CodingEnvironment, bool) {
	hostName, repository := codingenv.ParseRepositoryInput(legacy.Repository)
	environment := domain.CodingEnvironment{
		ID: strings.TrimSpace(legacy.ID), WorkspaceKey: workspaceKey, Name: legacy.Name, CodeHost: codingenv.InferCodeHost(hostName), HostName: hostName,
		Repository: repository, PrepareScript: legacy.SetupCommand, Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if environment.ID == "" || len(environment.ID) > 191 {
		environment.ID = fmt.Sprintf("legacy-%s-%d", strings.ToLower(strings.NewReplacer("/", "-", " ", "-").Replace(repository)), index)
		if len(environment.ID) > 191 {
			environment.ID = environment.ID[:191]
		}
	}
	if err := codingenv.NormalizeEnvironment(&environment); err != nil {
		return environment, false
	}
	if legacy.Archived {
		archived := createdAt
		environment.ArchivedAt = &archived
	}
	return environment, true
}

// CodingSnapshotWorkspace returns the workspace of a snapshot (archive
// strategies only get the snapshot id).
func (s *SQLiteStore) CodingSnapshotWorkspace(ctx context.Context, snapshotID string) (string, error) {
	var workspace string
	err := s.db.QueryRowContext(ctx, `SELECT workspace_key FROM coding_environment_snapshots WHERE id=?`, snapshotID).Scan(&workspace)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrCodingNotFound
	}
	return workspace, err
}
