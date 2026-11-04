package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

var uploadedOAuthAppIconPattern = regexp.MustCompile(`^/uploads/oauth_app_icon_[A-Za-z0-9_-]+\.(?:png|jpe?g|webp|svg)$`)

// uploadedOAuthAppIcon reports an application icon stored by
// POST /api/oauth-applications/icon.
func uploadedOAuthAppIcon(value string) bool {
	return uploadedOAuthAppIconPattern.MatchString(strings.TrimSpace(value))
}

// maxApplicationIconURL bounds an application icon URL (it is shown as the
// application member's avatar in every workspace that installs it).
const maxApplicationIconURL = 2048

// validApplicationIconURL reports whether an application icon (logo) URL is
// safe to show as an avatar: an absolute HTTPS URL without credentials,
// fragment, spaces or control characters, or, when uploads is true, an
// application icon Flow stored itself (/uploads/oauth_app_icon_…). Other
// schemes (javascript:, data:, http:), relative paths and other uploaded
// files (attachments, workspace logos) are rejected.
func validApplicationIconURL(raw string, uploads bool) bool {
	if len(raw) > maxApplicationIconURL || strings.TrimSpace(raw) != raw || strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return false
	}
	if uploads && uploadedOAuthAppIcon(raw) {
		return true
	}
	return validOAuthMetadataURI(raw)
}

// POST /api/oauth-applications/icon stores an application icon picked on the
// create/edit page (the reference app's "Application icon · At least 256x256px") and
// returns its URL for the manifest's display.iconUrl. PNG, JPEG, WebP or a
// safe SVG, at most 5 MB; workspace admins only (as every
// /api/oauth-applications route). The upload is recorded as owned by the
// workspace and the admin, which is what lets an application of that
// workspace use it as its logo and the admin preview it before then.
func (s *server) uploadOAuthApplicationIcon(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 6<<20)
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid icon")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "icon file is required")
		return
	}
	defer file.Close()
	extension := strings.ToLower(filepath.Ext(header.Filename))
	payload, err := io.ReadAll(io.LimitReader(file, (5<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid icon")
		return
	}
	if len(payload) > 5<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "icon exceeds 5 MB")
		return
	}
	contentType, err := validateWorkspaceLogo(extension, payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "icon must be PNG, JPEG, WebP, or a safe SVG")
		return
	}
	workspace, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	objectKey := fmt.Sprintf("oauth_app_icon_%s%s", idgen.Stamp(), extension)
	size, err := storage.Put(r.Context(), objectKey, bytes.NewReader(payload), contentType)
	if err != nil || size != int64(len(payload)) {
		_ = storage.Delete(r.Context(), objectKey)
		writeError(w, http.StatusInternalServerError, "icon upload failed")
		return
	}
	owner := store.FileUpload{WorkspaceKey: workspace.Workspace.URLKey, UserID: requestActor(s, r).ID, ObjectKey: objectKey, Filename: "icon" + extension, ContentType: contentType, ActualSize: size, Purpose: store.FileUploadPurposeOAuthAppIcon}
	if err := s.store.RecordStoredUpload(r.Context(), owner, time.Now().UTC()); err != nil {
		_ = storage.Delete(r.Context(), objectKey)
		writeError(w, http.StatusInternalServerError, "icon upload failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": "/uploads/" + objectKey})
}

// errOAuthAppIconNotOwned refuses an application logo naming an icon that
// another workspace uploaded (or that is not known to be an uploaded icon).
var errOAuthAppIconNotOwned = errors.New("upload the application icon in this workspace")

// checkOAuthAppIconOwner allows an application of workspace to use logo: an
// external HTTPS logo, the application's current logo (current), or an icon
// this workspace uploaded. It keeps an application from adopting another
// workspace's icon, which would make that icon readable wherever the
// application is shown.
func (s *server) checkOAuthAppIconOwner(ctx context.Context, workspace, logo, current string) error {
	logo = strings.TrimSpace(logo)
	if !strings.HasPrefix(logo, "/uploads/") || logo == current {
		return nil
	}
	owner, err := s.store.FileUploadByObject(ctx, strings.TrimPrefix(logo, "/uploads/"))
	if err != nil || owner.Purpose != store.FileUploadPurposeOAuthAppIcon || owner.WorkspaceKey != workspace {
		return errOAuthAppIconNotOwned
	}
	return nil
}

// checkOAuthAppLogoInput applies checkOAuthAppIconOwner to a logo set on the
// request's workspace application appID ("" while creating one).
func (s *server) checkOAuthAppLogoInput(r *http.Request, logo *string, appID string) error {
	if logo == nil {
		return nil
	}
	data, _ := s.store.WorkspaceMetadata(workspaceKey(r))
	current := ""
	for _, app := range data.OAuthApplications {
		if appID != "" && app.ID == appID {
			current = app.LogoURL
		}
	}
	return s.checkOAuthAppIconOwner(r.Context(), data.Workspace.URLKey, *logo, current)
}

// oauthAppIcon is an uploaded application icon and what refers to it: the
// upload's owner (absent for icons stored before owners were recorded) and
// the applications showing it as their logo.
type oauthAppIcon struct {
	name        string
	contentType string
	owner       *store.FileUpload
	apps        []store.OAuthApplicationLogoRef
}

// resolveOAuthAppIcon identifies an uploaded application icon from its owner
// row (nil when the object has none). An object is one when its upload was
// recorded as an application icon or, for an icon stored before owners were
// recorded (and not backfilled), when it has the icon upload's name and an
// application uses it. Only an owning workspace's applications count as
// references, so a logo copied into another workspace grants nothing.
func (s *server) resolveOAuthAppIcon(name string, owner *store.FileUpload) (oauthAppIcon, bool) {
	icon := oauthAppIcon{name: name, contentType: store.OwnedUploadTypes[strings.ToLower(filepath.Ext(name))]}
	if owner != nil {
		if owner.Purpose != store.FileUploadPurposeOAuthAppIcon {
			return icon, false
		}
		icon.owner = owner
		if owner.ContentType != "" {
			icon.contentType = owner.ContentType
		}
	} else if !uploadedOAuthAppIcon("/uploads/" + name) {
		return icon, false
	}
	for _, ref := range s.store.OAuthApplicationsWithLogo("/uploads/" + name) {
		if icon.owner == nil || ref.WorkspaceKey == icon.owner.WorkspaceKey {
			icon.apps = append(icon.apps, ref)
		}
	}
	if icon.owner == nil && len(icon.apps) == 0 || icon.contentType == "" {
		return icon, false
	}
	return icon, true
}

// oauthAppIconReadable is the icon's read permission. The icon is shown
// wherever its application is: on the application's settings pages, as the
// installed agent's avatar, in "via <app>" attributions and on the OAuth
// consent screen. So it is readable by
//   - the admin who uploaded it and the owning workspace's admins (the
//     create/edit page previews an icon before the application is saved);
//   - active members of the application's workspace and of every workspace
//     that installed it (they see the application and its agent);
//   - any signed-in account when the application is public: its consent
//     screen is shown to users of any workspace, before they install it.
//
// Anonymous requests never reach it: the consent screen requires signing in
// first, and the session check answers 401.
func (s *server) oauthAppIconReadable(ctx context.Context, icon oauthAppIcon, userID string) bool {
	if userID == "" {
		return false
	}
	if icon.owner != nil && (icon.owner.UserID == userID || s.activeWorkspaceMember(ctx, icon.owner.WorkspaceKey, userID, true)) {
		return true
	}
	for _, ref := range icon.apps {
		if ref.Application.PublicEnabled || s.activeWorkspaceMember(ctx, ref.WorkspaceKey, userID, false) {
			return true
		}
		installations, err := s.store.ApplicationInstallationsByClient(ctx, ref.Application.ClientID)
		if err != nil {
			continue
		}
		for _, installation := range installations {
			if installation.WorkspaceKey != ref.WorkspaceKey && s.activeWorkspaceMember(ctx, installation.WorkspaceKey, userID, false) {
				return true
			}
		}
	}
	return false
}

// activeWorkspaceMember reports an active membership of the workspace (an
// admin's when admin is set).
func (s *server) activeWorkspaceMember(ctx context.Context, workspace, userID string, admin bool) bool {
	if workspace == "" {
		return false
	}
	workspaceID, err := s.store.WorkspaceIDForKey(ctx, workspace)
	if err != nil {
		return false
	}
	role, status, err := s.store.WorkspaceRole(ctx, workspaceID, userID)
	return err == nil && status == "active" && (!admin || workspaceAdminRole(role))
}
