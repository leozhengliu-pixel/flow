package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Owned uploads are images the server stores itself for a workspace record
// (no upload token): an OAuth application icon or a workspace logo. Their
// file_uploads row records the owning workspace (and the uploading admin), so
// reads can be authorized against the record that shows the image.

const (
	// FileUploadPurposeOAuthAppIcon marks an icon stored by
	// POST /api/oauth-applications/icon.
	FileUploadPurposeOAuthAppIcon = "oauth_app_icon"
	// FileUploadPurposeWorkspaceLogo marks a logo stored by
	// POST /api/workspaces/{workspaceKey}/logo.
	FileUploadPurposeWorkspaceLogo = "workspace_logo"
)

// OwnedUploadTypes are the image types owned uploads are stored as, by file
// extension (the upload validates the bytes against them).
var OwnedUploadTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".svg": "image/svg+xml"}

var (
	oauthAppIconObject = regexp.MustCompile(`^oauth_app_icon_[A-Za-z0-9_-]+\.(?:png|jpe?g|webp|svg)$`)
	ownedUploadObject  = regexp.MustCompile(`^[A-Za-z0-9_-]+\.(?:png|jpe?g|webp|svg)$`)
)

// StoredUploadID is the file_uploads id of an owned upload's object.
func StoredUploadID(objectKey string) string {
	sum := sha256.Sum256([]byte(objectKey))
	return hex.EncodeToString(sum[:])
}

// RecordStoredUpload records the owner of an object the server stored
// itself. The row is completed (and never expires for owned purposes), so it
// keeps the object's owner for as long as the object exists.
func (s *SQLiteStore) RecordStoredUpload(ctx context.Context, upload FileUpload, now time.Time) error {
	if upload.ID == "" {
		upload.ID = StoredUploadID(upload.ObjectKey)
	}
	if len(upload.ObjectKey) > 191 || utf8.RuneCountInString(upload.Filename) > 512 || utf8.RuneCountInString(upload.ContentType) > 255 || len(upload.Purpose) > 255 {
		return fmt.Errorf("upload metadata exceeds limit")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO file_uploads(`+fileUploadColumns+`,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		upload.ID, upload.WorkspaceKey, upload.UserID, upload.ObjectKey, upload.Filename, upload.ContentType, upload.ActualSize, upload.ActualSize, upload.Purpose, "", "", FileUploadCompleted, formatUploadTime(now), formatUploadTime(now), formatUploadTime(now))
	return err
}

// DeleteStoredUpload forgets an owned upload whose object was deleted.
func (s *SQLiteStore) DeleteStoredUpload(ctx context.Context, objectKey string) error {
	return s.DeleteFileUpload(ctx, StoredUploadID(objectKey))
}

// BackfillUploadOwners records the owner of application icons and workspace
// logos stored before uploads recorded owners, from the record showing them
// now: an icon belongs to the workspace (and creator) of the OAuth
// application whose logo it is, a logo to the workspace whose logo it is (its
// object name carries the workspace id). An icon no application uses, or one
// used by applications of different workspaces, stays without an owner. It
// runs at startup and returns how many owners it recorded.
func (s *SQLiteStore) BackfillUploadOwners(ctx context.Context) (int, error) {
	icons := map[string][]FileUpload{}
	logos := []FileUpload{}
	s.mu.RLock()
	for key, data := range s.workspaces {
		if name, ok := strings.CutPrefix(data.Workspace.LogoURL, "/uploads/"); ok && data.Workspace.ID != "" && strings.HasPrefix(name, "workspace_logo_"+data.Workspace.ID+"_") && ownedUploadObject.MatchString(name) {
			logos = append(logos, FileUpload{WorkspaceKey: key, ObjectKey: name, Purpose: FileUploadPurposeWorkspaceLogo})
		}
		for _, app := range data.OAuthApplications {
			if name, ok := strings.CutPrefix(app.LogoURL, "/uploads/"); ok && oauthAppIconObject.MatchString(name) {
				icons[name] = append(icons[name], FileUpload{WorkspaceKey: key, UserID: app.CreatorID, ObjectKey: name, Purpose: FileUploadPurposeOAuthAppIcon})
			}
		}
	}
	s.mu.RUnlock()
	candidates := logos
	for _, owners := range icons {
		if slices.ContainsFunc(owners, func(owner FileUpload) bool { return owner.WorkspaceKey != owners[0].WorkspaceKey }) {
			continue
		}
		candidates = append(candidates, owners[0])
	}
	recorded := 0
	var errs []error
	now := time.Now().UTC()
	for _, upload := range candidates {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_uploads WHERE object_key=?`, upload.ObjectKey).Scan(&count); err != nil {
			errs = append(errs, err)
			continue
		}
		if count > 0 {
			continue
		}
		extension := strings.ToLower(filepath.Ext(upload.ObjectKey))
		upload.Filename, upload.ContentType = "icon"+extension, OwnedUploadTypes[extension]
		if upload.Purpose == FileUploadPurposeWorkspaceLogo {
			upload.Filename = "logo" + extension
		}
		if err := s.RecordStoredUpload(ctx, upload, now); err != nil {
			errs = append(errs, err)
			continue
		}
		recorded++
	}
	return recorded, errors.Join(errs...)
}
