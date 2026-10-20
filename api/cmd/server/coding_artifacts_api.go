package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Session artifacts: browser screenshots and recordings (docs/coding-sessions-design.md §17).
//
// Owner: track I (browser testing & artifacts).
//
//	POST /internal/coding-runtime/artifacts                          runner upload (sandbox token, multipart)
//	GET  /api/coding/sessions/{id}/artifacts                         list with signed URLs (1 h)
//	GET  /api/coding/sessions/{id}/artifacts/{artifactId}/content    stable authenticated download (web app, activities)
//
// Objects go through Flow's storage driver (FLOW_STORAGE_DRIVER) under a flat
// key `coding-artifact_<session>_<artifact>.<ext>`: the local driver and the
// existing signed-file route (/api/files/signed/{name}) only serve base names,
// so the design's `coding-artifacts/<ws>/<session>/<id>` path is flattened.
// PR/MR bodies get signed URLs valid for 30 days (renewed on every rewrite).
func (s *server) registerCodingArtifactRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/coding-runtime/artifacts", s.codingRuntimeUploadArtifact)
	mux.HandleFunc("GET /api/coding/sessions/{id}/artifacts", s.listCodingSessionArtifacts)
	mux.HandleFunc("GET /api/coding/sessions/{id}/artifacts/{artifactId}/content", s.getCodingSessionArtifactContent)
}

const (
	codingArtifactObjectPrefix = "coding-artifact_"
	// codingArtifactFieldLimit bounds each non-file multipart field.
	codingArtifactFieldLimit = 1 << 10
	// codingArtifactMultipartOverhead is allowed on top of the file limit for
	// boundaries and the small fields.
	codingArtifactMultipartOverhead = 64 << 10
	codingArtifactSyncTimeout       = 30 * time.Second
)

var codingArtifactClientID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// codingSandboxTokenAuth is the sandbox token check (track B's SessionStore).
type codingSandboxTokenAuth interface {
	AuthenticateSandboxToken(ctx context.Context, token string) (coding.SandboxGrant, error)
}

// codingSessionReader reads a session (track B's SessionStore).
type codingSessionReader interface {
	CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)
}

// codingPullRequestReader lists PR/MR links (track D's ChangeRequestStore).
type codingPullRequestReader interface {
	SessionPullRequests(ctx context.Context, workspaceKey, sessionID string) ([]domain.SessionPullRequest, error)
}

// codingArtifactHooks are the collaborators this file needs from other
// tracks; tests replace them.
type codingArtifactHooks struct {
	authenticator func(*server) codingSandboxTokenAuth
	sessions      func(*server) codingSessionReader
	pullRequests  func(*server) codingPullRequestReader
	// authorize checks that the web caller may read the session.
	authorize func(*server, *http.Request, domain.CodingSession) error
	// background runs follow-up work (PR body sync) after the response.
	background func(func())
}

var codingArtifacts = codingArtifactHooks{
	// The defaults are the codingstore adapter, statically typed, so a
	// missing store method is a compile error rather than a 503.
	authenticator: func(s *server) codingSandboxTokenAuth { return s.codingStore() },
	sessions:      func(s *server) codingSessionReader { return s.codingStore() },
	pullRequests:  func(s *server) codingPullRequestReader { return s.codingStore() },
	authorize: func(s *server, r *http.Request, session domain.CodingSession) error {
		// The same rule as the session and its thread (its AgentTask anchor:
		// issue, document, agent chat, pull request, loop, environment).
		return s.codingSessionAnchorAccess(r, session, false)
	},
	background: func(run func()) { go run() },
}

// codingRuntimeUploadArtifact stores a screenshot or recording from the runner.
//
//	POST /internal/coding-runtime/artifacts
//	Authorization: Bearer fcs_…
//	multipart/form-data: file, kind (screenshot|video), label, group, turnId, clientId
//
// Content types are sniffed (PNG, JPEG, WebM, MP4); the size limit is
// FLOW_CODING_ARTIFACT_MAX_BYTES; at most coding.MaxArtifactsPerTurn per turn.
// A repeated clientId returns the artifact stored by the first request.
func (s *server) codingRuntimeUploadArtifact(w http.ResponseWriter, r *http.Request) {
	if !s.codingEnabled() {
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "Coding sessions are not enabled")
		return
	}
	grant, ok := s.codingArtifactSandboxGrant(w, r)
	if !ok {
		return
	}
	if !s.coding.config.BrowserEnabled {
		writeAPIError(w, http.StatusForbidden, apiErrorFeatureNotAccessible, "Browser testing is disabled (FLOW_CODING_BROWSER=off)")
		return
	}
	if grant.SessionID == "" {
		writeAPIError(w, http.StatusForbidden, apiErrorForbidden, "Only session sandboxes can attach artifacts")
		return
	}
	maxBytes := s.coding.config.ArtifactMaxBytes
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	upload, uploadErr := readCodingArtifactUpload(w, r, maxBytes)
	if uploadErr != nil {
		writeAPIError(w, uploadErr.Status, uploadErr.Code, uploadErr.Message)
		return
	}
	workspace := grant.WorkspaceKey
	if sessions := codingArtifacts.sessions(s); sessions != nil {
		session, err := sessions.CodingSession(r.Context(), workspace, grant.SessionID)
		if err != nil {
			writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "Coding session not found")
			return
		}
		if session.Phase == domain.CodingPhaseDone {
			writeAPIError(w, http.StatusConflict, apiErrorConflict, coding.ErrArtifactSessionClosed.Error())
			return
		}
	}
	existing, err := s.store.CodingArtifacts(r.Context(), workspace, grant.SessionID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not read session artifacts")
		return
	}
	artifactID := codingArtifactID(grant.SessionID, upload.clientID)
	turnCount := 0
	for _, artifact := range existing {
		if artifact.ID == artifactID {
			// A retry of an upload that already succeeded.
			s.writeCodingArtifactResponse(w, r, http.StatusOK, workspace, artifact)
			return
		}
		if upload.turnID != "" && artifact.TurnID == upload.turnID {
			turnCount++
		}
	}
	if turnCount >= coding.MaxArtifactsPerTurn {
		writeAPIError(w, http.StatusTooManyRequests, apiErrorRateLimited, "This turn already has the maximum of 20 artifacts")
		return
	}
	if len(existing) >= coding.MaxArtifactsPerSession {
		writeAPIError(w, http.StatusTooManyRequests, apiErrorRateLimited, "This session already has the maximum number of artifacts")
		return
	}
	storage, err := s.storage()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, apiErrorUnavailable, "Storage is unavailable")
		return
	}
	objectKey := codingArtifactObjectPrefix + grant.SessionID + "_" + artifactID + upload.extension
	size, err := storage.Put(r.Context(), objectKey, bytes.NewReader(upload.content), upload.contentType)
	if err != nil || size != int64(len(upload.content)) {
		_ = storage.Delete(context.WithoutCancel(r.Context()), objectKey)
		writeAPIError(w, http.StatusServiceUnavailable, apiErrorUnavailable, "Could not store the artifact")
		return
	}
	artifact := domain.CodingArtifact{
		ID:          artifactID,
		SessionID:   grant.SessionID,
		Kind:        upload.kind,
		Label:       upload.label,
		Group:       upload.group,
		TurnID:      upload.turnID,
		ContentType: upload.contentType,
		SizeBytes:   size,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.AddCodingArtifact(context.WithoutCancel(r.Context()), workspace, artifact, objectKey); err != nil {
		_ = storage.Delete(context.WithoutCancel(r.Context()), objectKey)
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not record the artifact")
		return
	}
	ctx := context.WithoutCancel(r.Context())
	if activityID := s.appendCodingArtifactActivity(ctx, workspace, artifact); activityID != "" {
		artifact.ActivityID = activityID
		if err := s.store.SetCodingArtifactActivity(ctx, workspace, artifact.SessionID, artifact.ID, activityID); err != nil {
			slog.WarnContext(ctx, "coding artifact: link activity", "session", artifact.SessionID, "artifact", artifact.ID, "error", err)
		}
	}
	baseURL := s.codingArtifactBaseURL(r)
	codingArtifacts.background(func() {
		syncCtx, cancel := context.WithTimeout(ctx, codingArtifactSyncTimeout)
		defer cancel()
		s.syncCodingArtifactsToChangeRequests(syncCtx, workspace, artifact.SessionID, baseURL)
	})
	s.writeCodingArtifactResponse(w, r, http.StatusCreated, workspace, artifact)
}

// codingArtifactSandboxGrant resolves the bearer sandbox token (artifact routes).
func (s *server) codingArtifactSandboxGrant(w http.ResponseWriter, r *http.Request) (coding.SandboxGrant, bool) {
	token, found := strings.CutPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
	token = strings.TrimSpace(token)
	if !found || !strings.HasPrefix(token, coding.SandboxTokenPrefix) {
		writeAPIError(w, http.StatusUnauthorized, apiErrorAuthentication, "A sandbox token is required")
		return coding.SandboxGrant{}, false
	}
	authenticator := codingArtifacts.authenticator(s)
	if authenticator == nil {
		writeAPIError(w, http.StatusServiceUnavailable, apiErrorUnavailable, "The coding runtime is not ready")
		return coding.SandboxGrant{}, false
	}
	grant, err := authenticator.AuthenticateSandboxToken(r.Context(), token)
	if err != nil {
		if !errors.Is(err, coding.ErrTokenInvalid) {
			slog.WarnContext(r.Context(), "coding artifact: authenticate sandbox token", "error", err)
		}
		writeAPIError(w, http.StatusUnauthorized, apiErrorAuthentication, "The sandbox token is invalid, expired or revoked")
		return coding.SandboxGrant{}, false
	}
	return grant, true
}

type codingArtifactUpload struct {
	content                        []byte
	contentType, kind, extension   string
	label, group, turnID, clientID string
}

// readCodingArtifactUpload parses and validates the multipart request.
func readCodingArtifactUpload(w http.ResponseWriter, r *http.Request, maxBytes int64) (codingArtifactUpload, *coding.ArtifactUploadError) {
	invalid := func(message string) *coding.ArtifactUploadError {
		return &coding.ArtifactUploadError{Status: http.StatusBadRequest, Code: apiErrorInvalidInput, Message: message}
	}
	tooLarge := &coding.ArtifactUploadError{Status: http.StatusRequestEntityTooLarge, Code: apiErrorInvalidInput, Message: "The artifact is larger than the " + humanBytes(maxBytes) + " limit (FLOW_CODING_ARTIFACT_MAX_BYTES)"}
	if r.ContentLength > maxBytes+codingArtifactMultipartOverhead {
		return codingArtifactUpload{}, tooLarge
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return codingArtifactUpload{}, invalid("Send multipart/form-data with a file field")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+codingArtifactMultipartOverhead)
	reader, err := r.MultipartReader()
	if err != nil {
		return codingArtifactUpload{}, invalid("Send multipart/form-data with a file field")
	}
	var upload codingArtifactUpload
	fields := map[string]string{}
	haveFile := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return codingArtifactUpload{}, tooLarge
		}
		if err != nil {
			return codingArtifactUpload{}, invalid("Malformed multipart body")
		}
		name := part.FormName()
		if name == "file" {
			if haveFile {
				part.Close()
				return codingArtifactUpload{}, invalid("Send exactly one file")
			}
			haveFile = true
			content, err := io.ReadAll(io.LimitReader(part, maxBytes+1))
			part.Close()
			if errors.As(err, &maxErr) {
				return codingArtifactUpload{}, tooLarge
			}
			if err != nil {
				return codingArtifactUpload{}, invalid("Could not read the file")
			}
			if int64(len(content)) > maxBytes {
				return codingArtifactUpload{}, tooLarge
			}
			upload.content = content
			continue
		}
		value, err := readMultipartField(part)
		part.Close()
		if err != nil {
			return codingArtifactUpload{}, invalid("Field " + name + " is too long")
		}
		fields[name] = value
	}
	if !haveFile || len(upload.content) == 0 {
		return codingArtifactUpload{}, invalid("The file field is required and must not be empty")
	}
	contentType, kind, extension, ok := coding.DetectArtifactType(upload.content)
	if !ok {
		return codingArtifactUpload{}, &coding.ArtifactUploadError{Status: http.StatusUnsupportedMediaType, Code: apiErrorInvalidInput, Message: "Only PNG and JPEG screenshots and WebM or MP4 recordings are accepted"}
	}
	upload.contentType, upload.kind, upload.extension = contentType, kind, extension
	if declared := strings.TrimSpace(fields["kind"]); declared != "" && declared != kind {
		return codingArtifactUpload{}, &coding.ArtifactUploadError{Status: http.StatusUnsupportedMediaType, Code: apiErrorInvalidInput, Message: "kind " + declared + " does not match the uploaded " + contentType + " file"}
	}
	if upload.label, ok = coding.CleanArtifactText(fields["label"], coding.MaxArtifactLabelLength); !ok {
		return codingArtifactUpload{}, invalid("label must be at most 120 characters")
	}
	if upload.label == "" {
		upload.label = kind
	}
	if coding.IsBeforeAfter(upload.label) {
		upload.label = strings.ToLower(upload.label)
	}
	if upload.group, ok = coding.CleanArtifactText(fields["group"], coding.MaxArtifactGroupLength); !ok {
		return codingArtifactUpload{}, invalid("group must be at most 80 characters")
	}
	upload.turnID = strings.TrimSpace(fields["turnId"])
	if len(upload.turnID) > 191 || strings.ContainsAny(upload.turnID, " \t\r\n") {
		return codingArtifactUpload{}, invalid("turnId is invalid")
	}
	upload.clientID = strings.TrimSpace(fields["clientId"])
	if upload.clientID != "" && !codingArtifactClientID.MatchString(upload.clientID) {
		return codingArtifactUpload{}, invalid("clientId must be 1-128 letters, digits or ._:-")
	}
	return upload, nil
}

func readMultipartField(part *multipart.Part) (string, error) {
	value, err := io.ReadAll(io.LimitReader(part, codingArtifactFieldLimit+1))
	if err != nil {
		return "", err
	}
	if len(value) > codingArtifactFieldLimit {
		return "", errors.New("field too long")
	}
	return string(value), nil
}

// codingArtifactID is deterministic for a client id (idempotent retries) and
// random otherwise: 26 lowercase base32 characters like the other coding ids.
func codingArtifactID(sessionID, clientID string) string {
	var raw []byte
	if clientID != "" {
		sum := sha256.Sum256([]byte(sessionID + "\x00" + clientID))
		raw = sum[:]
	} else {
		raw = make([]byte, 20)
		if _, err := rand.Read(raw); err != nil {
			panic(err)
		}
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))[:26]
}

func humanBytes(value int64) string {
	switch {
	case value >= 1<<20 && value%(1<<20) == 0:
		return strconv.FormatInt(value>>20, 10) + " MiB"
	case value >= 1<<10 && value%(1<<10) == 0:
		return strconv.FormatInt(value>>10, 10) + " KiB"
	default:
		return strconv.FormatInt(value, 10) + " bytes"
	}
}

func (s *server) writeCodingArtifactResponse(w http.ResponseWriter, r *http.Request, status int, workspace string, artifact domain.CodingArtifact) {
	signed := s.signCodingArtifacts(r.Context(), workspace, artifact.SessionID, []domain.CodingArtifact{artifact}, s.codingArtifactBaseURL(r), coding.ArtifactListURLLifetime)
	writeJSON(w, status, coding.ArtifactResponse{ID: artifact.ID, URL: signed[0].URL})
}

// codingArtifactBaseURL is the public Flow URL used in signed links: the
// runner reaches Flow through the internal runtime URL, which browsers and
// code hosts cannot.
func (s *server) codingArtifactBaseURL(r *http.Request) string {
	return strings.TrimRight(firstNonEmpty(strings.TrimSpace(os.Getenv("FLOW_APP_URL")), s.allowedOrigin, externalBaseURL(r)), "/")
}

// codingArtifactContentPath is the stable, cookie-authenticated URL.
func codingArtifactContentPath(sessionID, artifactID string) string {
	return "/api/coding/sessions/" + url.PathEscape(sessionID) + "/artifacts/" + url.PathEscape(artifactID) + "/content"
}

// signCodingArtifacts fills URL (signed, expiring after lifetime), ExpiresAt
// and ContentURL. Artifacts whose object is unknown or whose signing key is
// unavailable keep only ContentURL.
func (s *server) signCodingArtifacts(ctx context.Context, workspace, sessionID string, artifacts []domain.CodingArtifact, baseURL string, lifetime time.Duration) []domain.CodingArtifact {
	result := make([]domain.CodingArtifact, len(artifacts))
	copy(result, artifacts)
	keys, err := s.store.CodingArtifactObjectKeys(ctx, workspace, sessionID)
	if err != nil {
		slog.WarnContext(ctx, "coding artifact: object keys", "session", sessionID, "error", err)
	}
	signingKey, signErr := s.fileSigningKey(ctx)
	if signErr != nil {
		slog.WarnContext(ctx, "coding artifact: file url signing unavailable", "error", signErr)
	}
	expires := time.Now().Add(lifetime).Truncate(time.Second).UTC()
	for index := range result {
		result[index].ContentURL = codingArtifactContentPath(sessionID, result[index].ID)
		result[index].URL = ""
		result[index].ExpiresAt = nil
		if objectKey := keys[result[index].ID]; objectKey != "" && signErr == nil {
			result[index].URL = signedFileURL(signingKey, baseURL, objectKey, expires)
			at := expires
			result[index].ExpiresAt = &at
		}
	}
	return result
}

// CodingArtifactMarkdown renders a session's "Screenshots" section with
// signed URLs valid for 30 days, for the PR/MR body that tracks D and J write
// when they create or update a change request. It returns "" when the session
// has no artifacts.
func (s *server) CodingArtifactMarkdown(ctx context.Context, workspace, sessionID, baseURL string) (string, error) {
	artifacts, err := s.store.CodingArtifacts(ctx, workspace, sessionID)
	if err != nil {
		return "", err
	}
	if len(artifacts) == 0 {
		return "", nil
	}
	if baseURL == "" {
		baseURL = strings.TrimRight(firstNonEmpty(strings.TrimSpace(os.Getenv("FLOW_APP_URL")), s.allowedOrigin), "/")
	}
	return coding.ArtifactMarkdown(s.signCodingArtifacts(ctx, workspace, sessionID, artifacts, baseURL, coding.ArtifactPullRequestURLLifetime)), nil
}

// syncCodingArtifactsToChangeRequests rewrites the screenshots section of
// every open PR/MR linked to the session. Failures are logged; the next
// upload or PR update retries with fresh URLs.
func (s *server) syncCodingArtifactsToChangeRequests(ctx context.Context, workspace, sessionID, baseURL string) {
	reader := codingArtifacts.pullRequests(s)
	if reader == nil || s.coding == nil {
		return
	}
	pullRequests, err := reader.SessionPullRequests(ctx, workspace, sessionID)
	if err != nil || len(pullRequests) == 0 {
		if err != nil {
			slog.WarnContext(ctx, "coding artifact: list pull requests", "session", sessionID, "error", err)
		}
		return
	}
	artifacts, err := s.store.CodingArtifacts(ctx, workspace, sessionID)
	if err != nil {
		slog.WarnContext(ctx, "coding artifact: list artifacts", "session", sessionID, "error", err)
		return
	}
	signed := s.signCodingArtifacts(ctx, workspace, sessionID, artifacts, baseURL, coding.ArtifactPullRequestURLLifetime)
	for _, pullRequest := range pullRequests {
		if pullRequest.State != "" && pullRequest.State != "open" {
			continue
		}
		host := s.coding.hosts[codehost.Kind(pullRequest.CodeHost)]
		if host == nil {
			continue
		}
		repo := codehost.RepoRef{CodeHost: codehost.Kind(pullRequest.CodeHost), HostName: pullRequest.HostName, FullName: pullRequest.Repository}
		if _, err := coding.SyncChangeRequestArtifacts(ctx, host, workspace, repo, pullRequest.Number, signed); err != nil {
			slog.WarnContext(ctx, "coding artifact: update change request body", "session", sessionID, "repository", pullRequest.Repository, "number", pullRequest.Number, "error", err)
		}
	}
}

// appendCodingArtifactActivity adds an action activity with the inline image
// (or a link to the recording) to the session's AgentTask anchor. It retries
// version conflicts a few times and gives up quietly when the anchor no
// longer accepts agent activities (finished sessions keep the artifact).
func (s *server) appendCodingArtifactActivity(ctx context.Context, workspace string, artifact domain.CodingArtifact) string {
	sessions := codingArtifacts.sessions(s)
	if sessions == nil {
		return ""
	}
	session, err := sessions.CodingSession(ctx, workspace, artifact.SessionID)
	if err != nil || session.AgentSessionID == "" {
		return ""
	}
	contentURL := codingArtifactContentPath(artifact.SessionID, artifact.ID)
	action, result := "Captured screenshot", "!["+markdownAlt(artifact.Label)+"]("+contentURL+")"
	if artifact.Kind == coding.ArtifactKindVideo {
		action, result = "Recorded browser session", "["+markdownAlt(artifact.Label)+"]("+contentURL+")"
	}
	parameter := artifact.Label
	if artifact.Group != "" {
		parameter = artifact.Group + " · " + artifact.Label
	}
	for attempt := 0; attempt < 3; attempt++ {
		task, err := s.store.AgentTask(ctx, workspace, session.AgentSessionID)
		if err != nil {
			return ""
		}
		activity := domain.AgentActivity{
			ActorID: task.AppUserID,
			Type:    "action",
			Body:    action + ": " + parameter + "\n\n" + result,
			URL:     contentURL,
			Metadata: map[string]any{
				"action": action, "parameter": parameter, "result": result,
				"artifactId": artifact.ID, "artifactKind": artifact.Kind, "artifactLabel": artifact.Label, "artifactGroup": artifact.Group,
				"codingSessionId": artifact.SessionID, "turnId": artifact.TurnID,
			},
		}
		updated, err := s.store.AppendAgentActivity(ctx, workspace, task.ID, task.Version, activity)
		if err == nil {
			return latestActivityID(ctx, s, workspace, updated.ID, artifact.ID)
		}
		if !errors.Is(err, store.ErrIssueVersion) {
			return ""
		}
		// Either a concurrent append (retry) or a state that refuses actions.
		if task.Status == "complete" || task.Status == "error" || task.Status == "canceled" || task.Status == "awaitingInput" {
			return ""
		}
	}
	return ""
}

// latestActivityID finds the activity just appended for the artifact.
func latestActivityID(ctx context.Context, s *server, workspace, taskID, artifactID string) string {
	activities, err := s.store.AgentActivities(ctx, workspace, taskID, "")
	if err != nil {
		return ""
	}
	for index := len(activities) - 1; index >= 0; index-- {
		if id, _ := activities[index].Metadata["artifactId"].(string); id == artifactID {
			return activities[index].ID
		}
	}
	return ""
}

func markdownAlt(value string) string {
	return strings.NewReplacer("[", "(", "]", ")", "\n", " ", "\r", " ").Replace(value)
}

// codingArtifactSession resolves and authorizes the session of a web request.
func (s *server) codingArtifactSession(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	if !s.codingEnabled() {
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "Coding sessions are not enabled")
		return "", "", false
	}
	workspace := workspaceKey(r)
	metadata, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "Workspace not found")
		return "", "", false
	}
	workspace = metadata.Workspace.URLKey
	sessionID := r.PathValue("id")
	sessions := codingArtifacts.sessions(s)
	if sessions == nil {
		writeAPIError(w, http.StatusServiceUnavailable, apiErrorUnavailable, "The coding runtime is not ready")
		return "", "", false
	}
	session, err := sessions.CodingSession(r.Context(), workspace, sessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "Coding session not found")
		return "", "", false
	}
	if err := codingArtifacts.authorize(s, r, session); err != nil {
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "Coding session not found")
		return "", "", false
	}
	return workspace, session.ID, true
}

// listCodingSessionArtifacts answers the artifacts of a session, oldest first,
// with signed URLs valid for one hour and stable contentUrl links.
//
//	GET /api/coding/sessions/{id}/artifacts
func (s *server) listCodingSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	workspace, sessionID, ok := s.codingArtifactSession(w, r)
	if !ok {
		return
	}
	artifacts, err := s.store.CodingArtifacts(r.Context(), workspace, sessionID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, apiErrorInternal, "Could not read session artifacts")
		return
	}
	writeJSON(w, http.StatusOK, s.signCodingArtifacts(r.Context(), workspace, sessionID, artifacts, s.codingArtifactBaseURL(r), coding.ArtifactListURLLifetime))
}

// getCodingSessionArtifactContent serves an artifact to a reader of the
// session. Range requests are supported when the storage driver can seek, so
// recordings can be scrubbed.
//
//	GET /api/coding/sessions/{id}/artifacts/{artifactId}/content
func (s *server) getCodingSessionArtifactContent(w http.ResponseWriter, r *http.Request) {
	workspace, sessionID, ok := s.codingArtifactSession(w, r)
	if !ok {
		return
	}
	artifact, objectKey, err := s.store.CodingArtifact(r.Context(), workspace, sessionID, r.PathValue("artifactId"))
	if err != nil {
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "Artifact not found")
		return
	}
	storage, err := s.storage()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, apiErrorUnavailable, "Storage is unavailable")
		return
	}
	reader, _, size, err := storage.Open(r.Context(), objectKey)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, apiErrorNotFound, "Artifact not found")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", artifact.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if seeker, ok := reader.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "", artifact.CreatedAt, seeker)
		return
	}
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, reader)
}
