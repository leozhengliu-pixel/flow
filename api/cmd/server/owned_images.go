package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Owned images are the uploads that represent a workspace record rather than
// content: OAuth application icons (oauth_application_icon.go) and workspace
// logos. Each has an owner row (store.RecordStoredUpload), a reference (the
// application's or workspace's logo URL) and a read permission derived from
// who sees that record. They are served by serveOwnedImage, not by the
// attachment path, whose visibility rules know nothing about them.

// ownedImage is a resolved owned image and its read permission.
type ownedImage struct {
	name        string
	contentType string
	readable    func(ctx context.Context, userID string) bool
}

// resolveOwnedImage identifies an owned image stored as name.
func (s *server) resolveOwnedImage(ctx context.Context, name string) (ownedImage, bool) {
	var owner *store.FileUpload
	if upload, err := s.store.FileUploadByObject(ctx, name); err == nil {
		owner = &upload
	}
	if icon, ok := s.resolveOAuthAppIcon(name, owner); ok {
		return ownedImage{name: name, contentType: icon.contentType, readable: func(ctx context.Context, userID string) bool {
			return s.oauthAppIconReadable(ctx, icon, userID)
		}}, true
	}
	if logo, ok := s.resolveWorkspaceLogo(name, owner); ok {
		return ownedImage{name: name, contentType: logo.contentType, readable: func(ctx context.Context, userID string) bool {
			return s.activeWorkspaceMember(ctx, logo.workspaceKey, userID, false)
		}}, true
	}
	return ownedImage{}, false
}

// workspaceLogo is an uploaded workspace logo that is its workspace's logo now.
type workspaceLogo struct {
	contentType  string
	workspaceKey string
}

// resolveWorkspaceLogo identifies a workspace logo from its owner row. It is
// one only while it is the owning workspace's current logo: a replaced or
// removed logo is not served (its object is deleted then too).
//
// Its read permission is the workspace's active members (guests included),
// who see it in the workspace menu and settings. The invite-link page shows
// it to people who are not members yet, often before they sign in: the
// preview hands them a short-lived signed URL (signedWorkspaceLogo) instead
// of widening the logo's own permission, so a plain /uploads/ read stays
// members-only (another workspace's user gets 404, an anonymous request 401).
func (s *server) resolveWorkspaceLogo(name string, owner *store.FileUpload) (workspaceLogo, bool) {
	if owner == nil || owner.Purpose != store.FileUploadPurposeWorkspaceLogo || owner.WorkspaceKey == "" {
		return workspaceLogo{}, false
	}
	data, ok := s.store.WorkspaceMetadata(owner.WorkspaceKey)
	if !ok || data.Workspace.URLKey != owner.WorkspaceKey || data.Workspace.LogoURL != "/uploads/"+name {
		return workspaceLogo{}, false
	}
	contentType := owner.ContentType
	if contentType == "" {
		contentType = store.OwnedUploadTypes[strings.ToLower(filepath.Ext(name))]
	}
	return workspaceLogo{contentType: contentType, workspaceKey: owner.WorkspaceKey}, contentType != ""
}

// errWorkspaceLogoNotOwned refuses a workspace logo URL naming a file the
// workspace did not upload as its logo.
var errWorkspaceLogoNotOwned = errors.New("upload the logo with POST /api/workspaces/{workspaceKey}/logo")

// checkWorkspaceLogoInput allows a workspace's logoUrl to be set to an
// external URL, kept unchanged, or cleared; an /uploads/ file must be a logo
// this workspace uploaded (never an attachment or another workspace's file).
func (s *server) checkWorkspaceLogoInput(ctx context.Context, workspace domain.Workspace, logo string) error {
	logo = strings.TrimSpace(logo)
	if !strings.HasPrefix(logo, "/uploads/") || logo == workspace.LogoURL {
		return nil
	}
	owner, err := s.store.FileUploadByObject(ctx, strings.TrimPrefix(logo, "/uploads/"))
	if err != nil || owner.Purpose != store.FileUploadPurposeWorkspaceLogo || owner.WorkspaceKey != workspace.URLKey {
		return errWorkspaceLogoNotOwned
	}
	return nil
}

// workspaceLogoLinkLifetime bounds the signed logo URL an invitation preview
// returns.
const workspaceLogoLinkLifetime = 15 * time.Minute

// signedWorkspaceLogo returns the workspace for an invitation preview, whose
// reader may not be a member (or signed in): an uploaded logo becomes a
// short-lived signed URL for that one object. A logo that is not this
// workspace's own upload is left out.
func (s *server) signedWorkspaceLogo(ctx context.Context, workspace domain.Workspace) domain.Workspace {
	name, uploaded := strings.CutPrefix(workspace.LogoURL, "/uploads/")
	if !uploaded {
		return workspace
	}
	workspace.LogoURL = ""
	owner, err := s.store.FileUploadByObject(ctx, name)
	if err != nil {
		return workspace
	}
	if _, ok := s.resolveWorkspaceLogo(name, &owner); !ok || owner.WorkspaceKey != workspace.URLKey {
		return workspace
	}
	key, err := s.fileSigningKey(ctx)
	if err != nil {
		return workspace
	}
	workspace.LogoURL = signedFileURL(key, "", name, time.Now().Add(workspaceLogoLinkLifetime))
	return workspace
}

// serveOwnedImage writes an owned image. The media type comes from the
// upload (validated then), never from the stored object or the bytes. They
// are shown inline; an SVG passed the upload's sanitizer and is still
// sandboxed without scripts, and nothing is sniffed.
func (s *server) serveOwnedImage(w http.ResponseWriter, r *http.Request, image ownedImage) {
	storage, err := s.storage()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	reader, _, size, err := storage.Open(r.Context(), image.name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer reader.Close()
	header := w.Header()
	header.Set("Content-Type", image.contentType)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Disposition", `inline; filename="`+image.name+`"`)
	policy := "default-src 'none'; sandbox"
	if image.contentType == "image/svg+xml" {
		// Opened directly, an SVG may use its presentation styles only.
		policy = "default-src 'none'; style-src 'unsafe-inline'; sandbox"
	}
	header.Set("Content-Security-Policy", policy)
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Cache-Control", "private, max-age=3600")
	if size >= 0 {
		header.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, reader)
}
