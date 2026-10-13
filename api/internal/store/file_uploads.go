package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// File uploads prepared through the REST API (POST /api/files) or the MCP
// prepare_attachment_upload tool. Rows are durable, so any API instance can
// accept the PUT and any instance can finalize it.

var (
	ErrFileUploadNotFound = errors.New("upload token is invalid or expired")
	ErrFileUploadBusy     = errors.New("upload is already in progress or completed")
	ErrFileUploadQuota    = errors.New("too many pending uploads; finish existing uploads first")
)

const (
	FileUploadPending   = "pending"
	FileUploadUploading = "uploading"
	FileUploadCompleted = "completed"
	FileUploadAttaching = "attaching"
)

type FileUpload struct {
	ID           string // sha256 of the upload token
	WorkspaceKey string
	UserID       string
	ObjectKey    string
	Filename     string
	ContentType  string
	ExpectedSize int64
	ActualSize   int64
	// Purpose is "asset" for REST uploads or "issue:<id>" for MCP uploads that
	// become issue attachments.
	Purpose   string
	Title     string
	Subtitle  string
	Status    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// FileUploadQuota bounds pending (not yet completed) uploads.
type FileUploadQuota struct {
	PerUser           int
	PerWorkspaceBytes int64
}

func (s *SQLiteStore) ensureFileUploads(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS file_uploads (id VARCHAR(64) PRIMARY KEY, workspace_key VARCHAR(191) NOT NULL, user_id VARCHAR(191) NOT NULL, object_key VARCHAR(191) NOT NULL, filename VARCHAR(512) NOT NULL, content_type VARCHAR(255) NOT NULL, expected_size BIGINT NOT NULL, actual_size BIGINT NOT NULL DEFAULT 0, purpose VARCHAR(255) NOT NULL, title VARCHAR(512) NOT NULL DEFAULT '', subtitle VARCHAR(512) NOT NULL DEFAULT '', status VARCHAR(16) NOT NULL, expires_at VARCHAR(40) NOT NULL, created_at VARCHAR(40) NOT NULL, updated_at VARCHAR(40) NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS api_signing_secrets (name VARCHAR(64) PRIMARY KEY, secret VARCHAR(255) NOT NULL, created_at VARCHAR(40) NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, index := range []string{
		"file_uploads_object_idx ON file_uploads(object_key)",
		"file_uploads_owner_idx ON file_uploads(workspace_key,user_id,status,expires_at)",
		"file_uploads_expiry_idx ON file_uploads(status,expires_at)",
	} {
		statement := "CREATE INDEX IF NOT EXISTS " + index
		if s.dialect == "mysql" {
			statement = "CREATE INDEX " + index
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}

func formatUploadTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func scanFileUpload(row interface{ Scan(...any) error }) (FileUpload, error) {
	var upload FileUpload
	var expires, created string
	err := row.Scan(&upload.ID, &upload.WorkspaceKey, &upload.UserID, &upload.ObjectKey, &upload.Filename, &upload.ContentType, &upload.ExpectedSize, &upload.ActualSize, &upload.Purpose, &upload.Title, &upload.Subtitle, &upload.Status, &expires, &created)
	if err != nil {
		return upload, err
	}
	upload.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expires)
	upload.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return upload, nil
}

const fileUploadColumns = "id,workspace_key,user_id,object_key,filename,content_type,expected_size,actual_size,purpose,title,subtitle,status,expires_at,created_at"

// CreateFileUpload records a prepared upload after checking the pending quota
// of the user and workspace.
func (s *SQLiteStore) CreateFileUpload(ctx context.Context, upload FileUpload, quota FileUploadQuota, now time.Time) error {
	if len(upload.ObjectKey) > 191 || len(upload.Filename) > 512 || len(upload.ContentType) > 255 || len(upload.Purpose) > 255 {
		return fmt.Errorf("upload metadata exceeds limit")
	}
	var userPending int
	var workspaceBytes sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_uploads WHERE workspace_key=? AND user_id=? AND status IN ('pending','uploading') AND expires_at>?`, upload.WorkspaceKey, upload.UserID, formatUploadTime(now)).Scan(&userPending); err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT SUM(expected_size) FROM file_uploads WHERE workspace_key=? AND status IN ('pending','uploading') AND expires_at>?`, upload.WorkspaceKey, formatUploadTime(now)).Scan(&workspaceBytes); err != nil {
		return err
	}
	if quota.PerUser > 0 && userPending >= quota.PerUser || quota.PerWorkspaceBytes > 0 && workspaceBytes.Int64+upload.ExpectedSize > quota.PerWorkspaceBytes {
		return ErrFileUploadQuota
	}
	upload.Status = FileUploadPending
	_, err := s.db.ExecContext(ctx, `INSERT INTO file_uploads(`+fileUploadColumns+`,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		upload.ID, upload.WorkspaceKey, upload.UserID, upload.ObjectKey, upload.Filename, upload.ContentType, upload.ExpectedSize, 0, upload.Purpose, upload.Title, upload.Subtitle, upload.Status, formatUploadTime(upload.ExpiresAt), formatUploadTime(now), formatUploadTime(now))
	return err
}

func (s *SQLiteStore) FileUpload(ctx context.Context, id string) (FileUpload, error) {
	upload, err := scanFileUpload(s.db.QueryRowContext(ctx, `SELECT `+fileUploadColumns+` FROM file_uploads WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return upload, ErrFileUploadNotFound
	}
	return upload, err
}

// FileUploadByObject finds the completed upload that produced a stored object.
func (s *SQLiteStore) FileUploadByObject(ctx context.Context, objectKey string) (FileUpload, error) {
	upload, err := scanFileUpload(s.db.QueryRowContext(ctx, `SELECT `+fileUploadColumns+` FROM file_uploads WHERE object_key=? AND status IN ('completed','attaching') LIMIT 1`, objectKey))
	if errors.Is(err, sql.ErrNoRows) {
		return upload, ErrFileUploadNotFound
	}
	return upload, err
}

// transitionFileUpload moves one upload between states with a compare-and-set
// so that concurrent requests on different instances cannot both win.
func (s *SQLiteStore) transitionFileUpload(ctx context.Context, id, from, to string, now time.Time, requireLive bool, set string, args ...any) (bool, error) {
	statement := `UPDATE file_uploads SET status=?,updated_at=?` + set + ` WHERE id=? AND status=?`
	values := append([]any{to, formatUploadTime(now)}, args...)
	values = append(values, id, from)
	if requireLive {
		statement += " AND expires_at>?"
		values = append(values, formatUploadTime(now))
	}
	result, err := s.db.ExecContext(ctx, statement, values...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// BeginFileUpload claims a pending upload for one PUT.
func (s *SQLiteStore) BeginFileUpload(ctx context.Context, id string, now time.Time) (FileUpload, error) {
	upload, err := s.FileUpload(ctx, id)
	if err != nil {
		return upload, err
	}
	if !upload.ExpiresAt.After(now) {
		return upload, ErrFileUploadNotFound
	}
	claimed, err := s.transitionFileUpload(ctx, id, FileUploadPending, FileUploadUploading, now, true, "")
	if err != nil {
		return upload, err
	}
	if !claimed {
		return upload, ErrFileUploadBusy
	}
	upload.Status = FileUploadUploading
	return upload, nil
}

// FinishFileUpload completes (size > 0 or an expected empty file) or releases
// a claimed upload.
func (s *SQLiteStore) FinishFileUpload(ctx context.Context, id string, completed bool, size int64, now time.Time) error {
	if !completed {
		_, err := s.transitionFileUpload(ctx, id, FileUploadUploading, FileUploadPending, now, false, "")
		return err
	}
	ok, err := s.transitionFileUpload(ctx, id, FileUploadUploading, FileUploadCompleted, now, false, ",actual_size=?", size)
	if err == nil && !ok {
		err = ErrFileUploadBusy
	}
	return err
}

// ClaimCompletedFileUpload reserves a completed upload while it is attached
// to an issue; ReleaseFileUpload returns it on failure.
func (s *SQLiteStore) ClaimCompletedFileUpload(ctx context.Context, id string, now time.Time) (bool, error) {
	return s.transitionFileUpload(ctx, id, FileUploadCompleted, FileUploadAttaching, now, true, "")
}

func (s *SQLiteStore) ReleaseFileUpload(ctx context.Context, id string, now time.Time) error {
	_, err := s.transitionFileUpload(ctx, id, FileUploadAttaching, FileUploadCompleted, now, false, "")
	return err
}

func (s *SQLiteStore) DeleteFileUpload(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM file_uploads WHERE id=?`, id)
	return err
}

// ExpiredFileUploads lists uploads whose token expired: pending ones (no
// object) and attachment uploads that were never attached (orphan objects).
// REST asset uploads stay once completed because Markdown may reference them.
func (s *SQLiteStore) ExpiredFileUploads(ctx context.Context, now time.Time, limit int) ([]FileUpload, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileUploadColumns+` FROM file_uploads WHERE expires_at<=? AND (status IN ('pending','uploading') OR status='completed' AND purpose<>'asset') ORDER BY expires_at LIMIT ?`, formatUploadTime(now.Add(-time.Minute)), min(max(limit, 1), 256))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []FileUpload{}
	for rows.Next() {
		upload, err := scanFileUpload(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, upload)
	}
	return result, rows.Err()
}

// SigningSecret returns a named random secret shared by every API instance,
// creating it on first use.
func (s *SQLiteStore) SigningSecret(ctx context.Context, name string) (string, error) {
	var secret string
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM api_signing_secrets WHERE name=?`, name).Scan(&secret)
	if err == nil {
		return secret, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO api_signing_secrets(name,secret,created_at) VALUES(?,?,?) ON CONFLICT DO NOTHING`, name, hex.EncodeToString(raw), formatUploadTime(time.Now())); err != nil {
		return "", err
	}
	err = s.db.QueryRowContext(ctx, `SELECT secret FROM api_signing_secrets WHERE name=?`, name).Scan(&secret)
	return secret, err
}
