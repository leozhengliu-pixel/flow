package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

// File storage API (the reference app's fileUpload + file storage authentication):
//
//	POST /api/files                    prepare an upload -> {uploadUrl, assetUrl, headers}
//	PUT  /api/files/uploads/{token}    send the bytes (the token is the credential)
//	GET  /uploads/{name}               authenticated download (session or Authorization)
//	POST /api/files/sign               short-lived signed download URLs
//	GET  /api/files/signed/{name}      download with ?expires=&signature=
//
// Any API response is rewritten to signed URLs when the request carries
// `X-Flow-File-Urls-Expire-In: <seconds>` (alias `Public-File-Urls-Expire-In`).

const (
	maxUploadBytes        = 20 << 20
	fileUploadTTL         = 15 * time.Minute
	maxSignedURLLifetime  = 7 * 24 * time.Hour
	fileSigningSecretName = "file-urls"
)

// fileUploadNow is the upload clock (tests move it forward).
var fileUploadNow = func() time.Time { return time.Now().UTC() }

var fileUploadQuota = store.FileUploadQuota{PerUser: 32, PerWorkspaceBytes: 256 << 20}

type fileUploadInput = publicapi.FileUploadInput

type fileUploadHeader = publicapi.FileUploadHeader

// fileUploadPayload is the prepared upload (the reference app's UploadFile).
type fileUploadPayload = publicapi.FileUpload

var unsafeFilenameCharacters = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// assetObjectKey gives every upload an unguessable storage name that is also
// safe inside Markdown and URLs.
func assetObjectKey(filename string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	extension := strings.ToLower(filepath.Ext(filepath.Base(filename)))
	extension = unsafeFilenameCharacters.ReplaceAllString(extension, "")
	if len(extension) > 16 {
		extension = ""
	}
	return "asset_" + hex.EncodeToString(random) + extension, nil
}

func cleanUploadFilename(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "." || name == "/" {
		name = ""
	}
	if len(name) > 255 {
		// Keep the end (the extension), starting on a character boundary.
		start := len(name) - 255
		for start < len(name) && !utf8.RuneStart(name[start]) {
			start++
		}
		name = name[start:]
	}
	return strings.ToValidUTF8(name, "�")
}

func (s *server) createFileUpload(w http.ResponseWriter, r *http.Request) {
	var input fileUploadInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Filename = cleanUploadFilename(input.Filename)
	input.ContentType = strings.TrimSpace(input.ContentType)
	if input.Filename == "" {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "filename is required")
		return
	}
	if input.ContentType == "" {
		input.ContentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(input.Filename)))
	}
	if input.ContentType == "" {
		input.ContentType = "application/octet-stream"
	}
	if _, _, err := mime.ParseMediaType(input.ContentType); err != nil || uploadContentTypeTooLong(input.ContentType) {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "contentType must be a valid media type")
		return
	}
	if input.Size < 0 || input.Size > maxUploadBytes {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "size must be between 0 and 20 MB")
		return
	}
	workspace := workspaceKey(r)
	metadata, ok := s.store.WorkspaceMetadata(workspace)
	if !ok || workspace == "" && !s.authDisabled {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Select a workspace with the X-Workspace-Key header")
		return
	}
	workspace = metadata.Workspace.URLKey
	payload, err := s.prepareFileUpload(r.Context(), store.FileUpload{WorkspaceKey: workspace, UserID: requestActor(s, r).ID, Filename: input.Filename, ContentType: input.ContentType, ExpectedSize: input.Size, Purpose: "asset"}, externalBaseURL(r), "/api/files/uploads/")
	if errors.Is(err, store.ErrFileUploadQuota) {
		writeAPIError(w, http.StatusTooManyRequests, apiErrorRateLimited, err.Error())
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not prepare the upload")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// prepareFileUpload records a durable pending upload and returns the client
// instructions. uploadPath is the PUT route prefix (REST or MCP).
func (s *server) prepareFileUpload(ctx context.Context, upload store.FileUpload, baseURL, uploadPath string) (fileUploadPayload, error) {
	s.cleanupFileUploads(ctx)
	token, err := randomSecret("upload_")
	if err != nil {
		return fileUploadPayload{}, err
	}
	if upload.ObjectKey, err = assetObjectKey(upload.Filename); err != nil {
		return fileUploadPayload{}, err
	}
	now := fileUploadNow()
	upload.ID = secretHash(token)
	upload.ExpiresAt = now.Add(fileUploadTTL)
	if err := s.store.CreateFileUpload(ctx, upload, fileUploadQuota, now); err != nil {
		return fileUploadPayload{}, err
	}
	baseURL = strings.TrimRight(baseURL, "/")
	payload := fileUploadPayload{
		ID:          upload.ID[:24],
		UploadURL:   baseURL + uploadPath + token,
		AssetURL:    baseURL + "/uploads/" + upload.ObjectKey,
		Headers:     []fileUploadHeader{{Key: "Content-Type", Value: upload.ContentType}},
		ContentType: upload.ContentType,
		Filename:    upload.Filename,
		Size:        upload.ExpectedSize,
		ExpiresAt:   upload.ExpiresAt,
	}
	return payload, nil
}

func (s *server) putFileUpload(w http.ResponseWriter, r *http.Request) {
	s.receiveFileUpload(w, r, r.PathValue("token"), "asset")
}

// receiveFileUpload stores the PUT body for one prepared upload whose purpose
// is one of kinds. Concurrent PUTs for the same token (on any instance) get
// 409 except the first.
func (s *server) receiveFileUpload(w http.ResponseWriter, r *http.Request, token string, kinds ...string) {
	id := secretHash(token)
	now := fileUploadNow()
	upload, err := s.store.BeginFileUpload(r.Context(), id, now)
	if errors.Is(err, store.ErrFileUploadNotFound) || err == nil && !slices.Contains(kinds, purposeKind(upload.Purpose)) {
		if err == nil {
			_ = s.store.FinishFileUpload(context.WithoutCancel(r.Context()), id, false, 0, now)
		}
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, store.ErrFileUploadNotFound.Error())
		return
	}
	if errors.Is(err, store.ErrFileUploadBusy) {
		writeAPIError(w, http.StatusConflict, apiErrorConflict, err.Error())
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "upload unavailable")
		return
	}
	completed := false
	defer func() {
		if !completed {
			_ = s.store.FinishFileUpload(context.WithoutCancel(r.Context()), id, false, 0, fileUploadNow())
		}
	}()
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1)
	storage, err := s.storage()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "storage unavailable")
		return
	}
	body, policyErr := s.checkUploadPolicy(upload.WorkspaceKey, upload.Filename, r.Body)
	if policyErr != nil {
		writeAPIError(w, http.StatusForbidden, apiErrorForbidden, policyErr.Error())
		return
	}
	size, err := storage.Put(r.Context(), upload.ObjectKey, body, upload.ContentType)
	if err != nil || size > maxUploadBytes || size != upload.ExpectedSize {
		_ = storage.Delete(context.WithoutCancel(r.Context()), upload.ObjectKey)
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "uploaded size does not match the prepared upload")
		return
	}
	if err := s.store.FinishFileUpload(context.WithoutCancel(r.Context()), id, true, size, fileUploadNow()); err != nil {
		_ = storage.Delete(context.WithoutCancel(r.Context()), upload.ObjectKey)
		writeAPIError(w, http.StatusConflict, apiErrorConflict, store.ErrFileUploadBusy.Error())
		return
	}
	completed = true
	w.WriteHeader(http.StatusNoContent)
}

func purposeKind(purpose string) string {
	kind, _, _ := strings.Cut(purpose, ":")
	return kind
}

// cleanupFileUploads removes a bounded batch of expired uploads and the
// objects of attachment uploads that were never attached.
func (s *server) cleanupFileUploads(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	expired, err := s.store.ExpiredFileUploads(ctx, fileUploadNow(), 16)
	if err != nil || len(expired) == 0 {
		return
	}
	storage, err := s.storage()
	if err != nil {
		return
	}
	for _, upload := range expired {
		if upload.Status == store.FileUploadCompleted || upload.Status == store.FileUploadUploading {
			if err := storage.Delete(ctx, upload.ObjectKey); err != nil && !errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
		_ = s.store.DeleteFileUpload(ctx, upload.ID)
	}
}

// serveUploadOrAsset serves REST-uploaded assets to the uploader and to the
// active members of the upload's workspace, then falls back to the existing
// attachment visibility rules.
func (s *server) serveUploadOrAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.HasPrefix(name, "asset_") && filepath.Base(name) == name {
		if upload, err := s.store.FileUploadByObject(r.Context(), name); err == nil && upload.Purpose == "asset" && (s.authDisabled || s.assetReadable(r, upload)) {
			s.writeStoredObject(w, r, name)
			return
		}
	}
	s.serveUpload(w, r)
}

func (s *server) assetReadable(r *http.Request, upload store.FileUpload) bool {
	user := authUser(r).ID
	if user == "" {
		return false
	}
	if user == upload.UserID {
		return true
	}
	workspaceID, err := s.store.WorkspaceIDForKey(r.Context(), upload.WorkspaceKey)
	if err != nil {
		return false
	}
	role, status, err := s.store.WorkspaceRole(r.Context(), workspaceID, user)
	return err == nil && status == "active" && role != "guest"
}

func (s *server) writeStoredObject(w http.ResponseWriter, r *http.Request, name string) {
	storage, err := s.storage()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	reader, contentType, size, err := storage.Open(r.Context(), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer reader.Close()
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if !safeInlineUploadType(contentType, name) {
		w.Header().Set("Content-Disposition", `attachment; filename="download"`)
	}
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, reader)
}

// File URL signing.

func (s *server) fileSigningKey(ctx context.Context) ([]byte, error) {
	if configured := strings.TrimSpace(os.Getenv("FLOW_FILE_URL_SIGNING_KEY")); configured != "" {
		return []byte(configured), nil
	}
	secret, err := s.store.SigningSecret(ctx, fileSigningSecretName)
	if err != nil {
		return nil, err
	}
	return []byte(secret), nil
}

func fileURLSignature(key []byte, name string, expires int64) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s\n%d", name, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func signedFileURL(key []byte, baseURL, name string, expires time.Time) string {
	unix := expires.Unix()
	return strings.TrimRight(baseURL, "/") + "/api/files/signed/" + url.PathEscape(name) + "?expires=" + strconv.FormatInt(unix, 10) + "&signature=" + fileURLSignature(key, name, unix)
}

// serveSignedFile serves an object to anyone holding an unexpired signature.
func (s *server) serveSignedFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	expires, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	signature := r.URL.Query().Get("signature")
	if name == "" || filepath.Base(name) != name || err != nil || signature == "" {
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "file not found")
		return
	}
	if time.Unix(expires, 0).Before(time.Now()) {
		writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "This signed file URL has expired")
		return
	}
	key, err := s.fileSigningKey(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "file signing unavailable")
		return
	}
	if !hmac.Equal([]byte(fileURLSignature(key, name, expires)), []byte(strings.ToLower(signature))) {
		writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "Invalid file URL signature")
		return
	}
	s.writeStoredObject(w, r, name)
}

type signedFile = publicapi.SignedFile

// signFileURLs issues signed URLs for files the caller can download now:
// GET /api/files/sign?url=<file url>&url=…&expiresIn=<seconds, default 3600>.
func (s *server) signFileURLs(w http.ResponseWriter, r *http.Request) {
	urls := r.URL.Query()["url"]
	if len(urls) == 0 || len(urls) > 100 {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "Pass 1 to 100 url parameters")
		return
	}
	lifetime, ok := time.Hour, true
	if raw := r.URL.Query().Get("expiresIn"); raw != "" {
		lifetime, ok = signedURLLifetime(raw)
	}
	if !ok {
		writeAPIError(w, http.StatusBadRequest, apiErrorInvalidInput, "expiresIn must be between 1 and 604800 seconds")
		return
	}
	key, err := s.fileSigningKey(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "file signing unavailable")
		return
	}
	expires := time.Now().Add(lifetime).Truncate(time.Second)
	result := []signedFile{}
	for _, raw := range urls {
		name := uploadNameFromURL(raw)
		if name == "" || !s.fileReadable(r, name) {
			writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "file not found: "+raw)
			return
		}
		result = append(result, signedFile{URL: raw, SignedURL: signedFileURL(key, externalBaseURL(r), name, expires), ExpiresAt: expires})
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": result})
}

func uploadNameFromURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	path := parsed.Path
	if !strings.HasPrefix(path, "/uploads/") {
		return ""
	}
	name := strings.TrimPrefix(path, "/uploads/")
	if name == "" || filepath.Base(name) != name {
		return ""
	}
	return name
}

// fileReadable applies the same rules as an authenticated GET /uploads/{name}.
func (s *server) fileReadable(r *http.Request, name string) bool {
	if s.authDisabled {
		return true
	}
	if upload, err := s.store.FileUploadByObject(r.Context(), name); err == nil && upload.Purpose == "asset" && s.assetReadable(r, upload) {
		return true
	}
	user := authUser(r)
	account, err := s.store.AccountForUser(r.Context(), user.ID)
	return err == nil && s.attachmentVisible(r.Context(), account, user.ID, "/uploads/"+name)
}

func signedURLLifetime(raw string) (time.Duration, bool) {
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || seconds < 1 || time.Duration(seconds)*time.Second > maxSignedURLLifetime {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func requestedFileURLLifetime(r *http.Request) (time.Duration, bool) {
	for _, header := range []string{"X-Flow-File-Urls-Expire-In", "Public-File-Urls-Expire-In"} {
		if value := r.Header.Get(header); value != "" {
			return signedURLLifetime(value)
		}
	}
	return 0, false
}

var (
	uploadURLValue = regexp.MustCompile(`"((?:https?://[^"/\\\s]+)?/uploads/([^"\\/?#]+))"`)
	uploadURLText  = regexp.MustCompile(`(?:https?://[^"/\\\s()<>\]]+)?/uploads/([A-Za-z0-9][A-Za-z0-9._%\-]{0,190})`)
)

// signedFileURLs rewrites /uploads/ URLs inside successful JSON responses to
// signed URLs when the client asks for them with the expire-in header.
func (s *server) signedFileURLs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lifetime, requested := requestedFileURLLifetime(r)
		if !requested || !strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/api/files") || r.Header.Get("Upgrade") != "" {
			next.ServeHTTP(w, r)
			return
		}
		writer := &bufferedJSONWriter{ResponseWriter: w, limit: 32 << 20}
		next.ServeHTTP(writer, r)
		if !writer.buffering {
			return
		}
		body := writer.buffer.Bytes()
		if key, err := s.fileSigningKey(r.Context()); err == nil {
			body = rewriteUploadURLs(body, key, externalBaseURL(r), time.Now().Add(lifetime).Truncate(time.Second))
		} else {
			slog.WarnContext(r.Context(), "file url signing unavailable", "error", err)
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(writer.status)
		_, _ = w.Write(body)
	})
}

func rewriteUploadURLs(body, key []byte, baseURL string, expires time.Time) []byte {
	sign := func(name string) string {
		if decoded, err := url.PathUnescape(name); err == nil {
			name = decoded
		}
		return signedFileURL(key, baseURL, name, expires)
	}
	body = uploadURLValue.ReplaceAllFunc(body, func(match []byte) []byte {
		parts := uploadURLValue.FindSubmatch(match)
		encoded, _ := json.Marshal(sign(string(parts[2])))
		return encoded
	})
	// Markdown and text occurrences. Upload *routes* (/api/files/uploads/,
	// /api/mcp/uploads/) are not downloads and stay unchanged.
	var output bytes.Buffer
	last := 0
	for _, match := range uploadURLText.FindAllSubmatchIndex(body, -1) {
		start, end := match[0], match[1]
		prefix := body[:start]
		if bytes.HasSuffix(prefix, []byte("/api/files")) || bytes.HasSuffix(prefix, []byte("/api/mcp")) {
			continue
		}
		output.Write(body[last:start])
		encoded, _ := json.Marshal(sign(string(body[match[2]:match[3]])))
		output.Write(encoded[1 : len(encoded)-1])
		last = end
	}
	output.Write(body[last:])
	return output.Bytes()
}

// bufferedJSONWriter holds a successful JSON response so it can be rewritten;
// anything else streams through untouched.
type bufferedJSONWriter struct {
	http.ResponseWriter
	status    int
	buffering bool
	limit     int
	buffer    bytes.Buffer
}

func (w *bufferedJSONWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *bufferedJSONWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	if status == http.StatusOK || status == http.StatusCreated {
		if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			w.buffering = true
			return
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *bufferedJSONWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !w.buffering {
		return w.ResponseWriter.Write(p)
	}
	if w.buffer.Len()+len(p) > w.limit {
		// Too large to rewrite: send what we have unchanged.
		w.buffering = false
		w.ResponseWriter.WriteHeader(w.status)
		if _, err := w.ResponseWriter.Write(w.buffer.Bytes()); err != nil {
			return 0, err
		}
		w.buffer.Reset()
		return w.ResponseWriter.Write(p)
	}
	return w.buffer.Write(p)
}

func (w *bufferedJSONWriter) Flush() {
	if w.buffering {
		return
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
