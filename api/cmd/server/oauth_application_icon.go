package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var uploadedOAuthAppIconPattern = regexp.MustCompile(`^/uploads/oauth_app_icon_[A-Za-z0-9_-]+\.(?:png|jpe?g|webp|svg)$`)

// uploadedOAuthAppIcon reports an application icon stored by
// POST /api/oauth-applications/icon.
func uploadedOAuthAppIcon(value string) bool {
	return uploadedOAuthAppIconPattern.MatchString(strings.TrimSpace(value))
}

// POST /api/oauth-applications/icon stores an application icon picked on the
// create/edit page (the reference app's "Application icon · At least 256x256px") and
// returns its URL for the manifest's display.iconUrl. PNG, JPEG, WebP or a
// safe SVG, at most 5 MB; workspace admins only (as every
// /api/oauth-applications route).
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
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	objectKey := fmt.Sprintf("oauth_app_icon_%d%s", time.Now().UnixNano(), extension)
	size, err := storage.Put(r.Context(), objectKey, bytes.NewReader(payload), contentType)
	if err != nil || size != int64(len(payload)) {
		_ = storage.Delete(r.Context(), objectKey)
		writeError(w, http.StatusInternalServerError, "icon upload failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": "/uploads/" + objectKey})
}
