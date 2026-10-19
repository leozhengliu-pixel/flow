package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

var (
	testPNG  = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 56)...)
	testWebM = append(append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81, 0x01, 0x42, 0x82, 0x84}, []byte("webm")...), bytes.Repeat([]byte{2}, 40)...)
)

type fakeSandboxAuth map[string]coding.SandboxGrant

func (f fakeSandboxAuth) AuthenticateSandboxToken(_ context.Context, token string) (coding.SandboxGrant, error) {
	if grant, ok := f[token]; ok {
		return grant, nil
	}
	return coding.SandboxGrant{}, coding.ErrTokenInvalid
}

type fakeSessionReader map[string]domain.CodingSession

func (f fakeSessionReader) CodingSession(_ context.Context, workspace, id string) (domain.CodingSession, error) {
	if session, ok := f[workspace+"/"+id]; ok {
		return session, nil
	}
	return domain.CodingSession{}, coding.ErrNotFound
}

type fakePullRequestReader []domain.SessionPullRequest

func (f fakePullRequestReader) SessionPullRequests(context.Context, string, string) ([]domain.SessionPullRequest, error) {
	return f, nil
}

// fakeCodeHost records PR body rewrites.
type fakeCodeHost struct {
	codehost.Stub
	mu     sync.Mutex
	bodies map[int]string
	writes []string
}

func (h *fakeCodeHost) GetChangeRequest(_ context.Context, _ string, repo codehost.RepoRef, number int) (codehost.ChangeRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return codehost.ChangeRequest{Repo: repo, Number: number, State: "open", Body: h.bodies[number]}, nil
}

func (h *fakeCodeHost) UpdateChangeRequestBody(_ context.Context, _ string, _ codehost.RepoRef, number int, body string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.bodies[number] = body
	h.writes = append(h.writes, body)
	return nil
}

type artifactTestEnv struct {
	server    *server
	workspace string
	host      *fakeCodeHost
	sessions  fakeSessionReader
	authorize error
}

func newArtifactTestEnv(t *testing.T) *artifactTestEnv {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "artifacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	metadata, ok := repository.WorkspaceMetadata("")
	if !ok {
		t.Fatal("no default workspace")
	}
	workspace := metadata.Workspace.URLKey
	host := &fakeCodeHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}, bodies: map[int]string{12: "Summary\n\nFixes FLOW-1"}}
	s := &server{store: repository, uploadPath: t.TempDir(), allowedOrigin: "https://flow.example"}
	s.coding = &codingServices{
		config: appconfig.CodingConfig{Enabled: true, BrowserEnabled: true, ArtifactMaxBytes: 4096},
		hosts:  map[codehost.Kind]codehost.Host{domain.CodeHostGitHub: host},
	}
	env := &artifactTestEnv{server: s, workspace: workspace, host: host, sessions: fakeSessionReader{
		workspace + "/s1":   {ID: "s1", AgentSessionID: "task-1", Phase: domain.CodingPhaseRunning},
		workspace + "/done": {ID: "done", Phase: domain.CodingPhaseDone},
	}}
	previous := codingArtifacts
	t.Cleanup(func() { codingArtifacts = previous })
	codingArtifacts = codingArtifactHooks{
		authenticator: func(*server) codingSandboxTokenAuth {
			return fakeSandboxAuth{
				"fcs_session":  {WorkspaceKey: workspace, SandboxID: "sb1", SessionID: "s1"},
				"fcs_done":     {WorkspaceKey: workspace, SandboxID: "sb2", SessionID: "done"},
				"fcs_snapshot": {WorkspaceKey: workspace, SandboxID: "sb3", SnapshotID: "snap"},
			}
		},
		sessions: func(*server) codingSessionReader { return env.sessions },
		pullRequests: func(*server) codingPullRequestReader {
			return fakePullRequestReader{
				{SessionID: "s1", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/web", Number: 12, State: "open"},
				{SessionID: "s1", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/web", Number: 9, State: "merged"},
			}
		},
		authorize:  func(*server, *http.Request, domain.CodingSession) error { return env.authorize },
		background: func(run func()) { run() },
	}
	return env
}

type uploadFields struct {
	token, kind, label, group, turnID, clientID string
	file                                        []byte
	noFile                                      bool
}

func (env *artifactTestEnv) upload(t *testing.T, fields uploadFields) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{"kind": fields.kind, "label": fields.label, "group": fields.group, "turnId": fields.turnID, "clientId": fields.clientID} {
		if value != "" {
			_ = writer.WriteField(name, value)
		}
	}
	if !fields.noFile {
		part, _ := writer.CreateFormFile("file", "shot.png")
		_, _ = part.Write(fields.file)
	}
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/internal/coding-runtime/artifacts", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if fields.token != "" {
		request.Header.Set("Authorization", "Bearer "+fields.token)
	}
	recorder := httptest.NewRecorder()
	env.server.codingRuntimeUploadArtifact(recorder, request)
	return recorder
}

func (env *artifactTestEnv) get(t *testing.T, path string, handler http.HandlerFunc, values map[string]string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("X-Workspace-Key", env.workspace)
	for key, value := range header {
		request.Header[key] = value
	}
	for key, value := range values {
		request.SetPathValue(key, value)
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func errorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	return body["code"]
}

func TestCodingArtifactUploadRejections(t *testing.T) {
	env := newArtifactTestEnv(t)
	for _, test := range []struct {
		name   string
		fields uploadFields
		setup  func()
		want   int
		code   string
	}{
		{"no token", uploadFields{file: testPNG}, nil, http.StatusUnauthorized, apiErrorAuthentication},
		{"not a sandbox token", uploadFields{token: "flow_api_key", file: testPNG}, nil, http.StatusUnauthorized, apiErrorAuthentication},
		{"revoked token", uploadFields{token: "fcs_revoked", file: testPNG}, nil, http.StatusUnauthorized, apiErrorAuthentication},
		{"snapshot sandbox", uploadFields{token: "fcs_snapshot", file: testPNG}, nil, http.StatusForbidden, apiErrorForbidden},
		{"no file", uploadFields{token: "fcs_session", noFile: true}, nil, http.StatusBadRequest, apiErrorInvalidInput},
		{"empty file", uploadFields{token: "fcs_session", file: []byte{}}, nil, http.StatusBadRequest, apiErrorInvalidInput},
		{"too large", uploadFields{token: "fcs_session", file: append(append([]byte{}, testPNG...), make([]byte, 4096)...)}, nil, http.StatusRequestEntityTooLarge, apiErrorInvalidInput},
		{"svg is not accepted", uploadFields{token: "fcs_session", file: []byte("<svg onload=alert(1)>")}, nil, http.StatusUnsupportedMediaType, apiErrorInvalidInput},
		{"html is not accepted", uploadFields{token: "fcs_session", file: []byte("<!doctype html><script>")}, nil, http.StatusUnsupportedMediaType, apiErrorInvalidInput},
		{"kind mismatch", uploadFields{token: "fcs_session", kind: "video", file: testPNG}, nil, http.StatusUnsupportedMediaType, apiErrorInvalidInput},
		{"label too long", uploadFields{token: "fcs_session", label: strings.Repeat("x", 121), file: testPNG}, nil, http.StatusBadRequest, apiErrorInvalidInput},
		{"group too long", uploadFields{token: "fcs_session", group: strings.Repeat("x", 81), file: testPNG}, nil, http.StatusBadRequest, apiErrorInvalidInput},
		{"bad client id", uploadFields{token: "fcs_session", clientID: "a b/c", file: testPNG}, nil, http.StatusBadRequest, apiErrorInvalidInput},
		{"finished session", uploadFields{token: "fcs_done", file: testPNG}, nil, http.StatusConflict, apiErrorConflict},
		{"browser testing off", uploadFields{token: "fcs_session", file: testPNG}, func() { env.server.coding.config.BrowserEnabled = false }, http.StatusForbidden, apiErrorFeatureNotAccessible},
		{"coding disabled", uploadFields{token: "fcs_session", file: testPNG}, func() { env.server.coding.config.Enabled = false }, http.StatusNotFound, apiErrorNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			env.server.coding.config.Enabled, env.server.coding.config.BrowserEnabled = true, true
			if test.setup != nil {
				test.setup()
			}
			recorder := env.upload(t, test.fields)
			if recorder.Code != test.want || errorCode(t, recorder) != test.code {
				t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	env.server.coding.config.Enabled, env.server.coding.config.BrowserEnabled = true, true
	if artifacts, _ := env.server.store.CodingArtifacts(t.Context(), env.workspace, "s1"); len(artifacts) != 0 {
		t.Fatalf("rejected uploads must not be stored: %+v", artifacts)
	}
	// The runtime is not wired yet (track B not landed): a clear 503.
	codingArtifacts.authenticator = func(*server) codingSandboxTokenAuth { return nil }
	if recorder := env.upload(t, uploadFields{token: "fcs_session", file: testPNG}); recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("no authenticator: %d", recorder.Code)
	}
}

func TestCodingArtifactUploadNotMultipart(t *testing.T) {
	env := newArtifactTestEnv(t)
	request := httptest.NewRequest(http.MethodPost, "/internal/coding-runtime/artifacts", bytes.NewReader(testPNG))
	request.Header.Set("Content-Type", "image/png")
	request.Header.Set("Authorization", "Bearer fcs_session")
	recorder := httptest.NewRecorder()
	env.server.codingRuntimeUploadArtifact(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("got %d", recorder.Code)
	}
	// A declared length far above the limit is refused before reading.
	request = httptest.NewRequest(http.MethodPost, "/internal/coding-runtime/artifacts", strings.NewReader("x"))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	request.Header.Set("Authorization", "Bearer fcs_session")
	request.ContentLength = 10 << 20
	recorder = httptest.NewRecorder()
	env.server.codingRuntimeUploadArtifact(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("content length: %d", recorder.Code)
	}
}

func TestCodingArtifactUploadStoresLinksAndSigns(t *testing.T) {
	env := newArtifactTestEnv(t)
	ctx := t.Context()
	if _, err := env.server.store.CreateAgentInitiatedTask(ctx, domain.AgentTask{ID: "task-1", WorkspaceKey: env.workspace, AppUserID: "flow-agent", IssueID: "issue-1"}); err != nil {
		t.Fatal(err)
	}

	recorder := env.upload(t, uploadFields{token: "fcs_session", kind: "screenshot", label: "Before", group: "Settings  page", turnID: "turn-1", clientID: "c-1", file: testPNG})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", recorder.Code, recorder.Body.String())
	}
	var response coding.ArtifactResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.ID) != 26 || !strings.HasPrefix(response.URL, "https://flow.example/api/files/signed/coding-artifact_s1_"+response.ID+".png?expires=") {
		t.Fatalf("response: %+v %v", response, err)
	}

	// Stored with the normalized label and group, linked to the activity.
	artifacts, err := env.server.store.CodingArtifacts(ctx, env.workspace, "s1")
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("stored: %+v %v", artifacts, err)
	}
	artifact := artifacts[0]
	if artifact.Label != "before" || artifact.Group != "Settings page" || artifact.TurnID != "turn-1" || artifact.Kind != "screenshot" || artifact.ContentType != "image/png" || artifact.SizeBytes != int64(len(testPNG)) || artifact.ActivityID == "" {
		t.Fatalf("artifact: %+v", artifact)
	}
	activities, err := env.server.store.AgentActivities(ctx, env.workspace, "task-1", "")
	if err != nil || len(activities) != 1 {
		t.Fatalf("activities: %+v %v", activities, err)
	}
	activity := activities[0]
	contentPath := "/api/coding/sessions/s1/artifacts/" + artifact.ID + "/content"
	if activity.ID != artifact.ActivityID || activity.Type != "action" || activity.Metadata["artifactId"] != artifact.ID || activity.Metadata["action"] != "Captured screenshot" || activity.URL != contentPath || !strings.Contains(activity.Body, "![before]("+contentPath+")") {
		t.Fatalf("activity: %+v", activity)
	}

	// The open PR got the screenshots section with a 30-day signed URL; the
	// merged PR was left alone; the author's text is kept.
	if len(env.host.writes) != 1 {
		t.Fatalf("PR writes: %q", env.host.writes)
	}
	body := env.host.bodies[12]
	if !strings.HasPrefix(body, "Summary\n\nFixes FLOW-1\n\n"+coding.ArtifactSectionStart) || !strings.Contains(body, "https://flow.example/api/files/signed/coding-artifact_s1_"+artifact.ID+".png") {
		t.Fatalf("PR body: %s", body)
	}
	if expires := signedExpiry(t, body); expires.Before(time.Now().Add(29*24*time.Hour)) || expires.After(time.Now().Add(31*24*time.Hour)) {
		t.Fatalf("PR URL expiry %v", expires)
	}

	// A retry with the same client id returns the stored artifact, once.
	retry := env.upload(t, uploadFields{token: "fcs_session", label: "before", turnID: "turn-1", clientID: "c-1", file: testPNG})
	var retried coding.ArtifactResponse
	_ = json.Unmarshal(retry.Body.Bytes(), &retried)
	if retry.Code != http.StatusOK || retried.ID != response.ID {
		t.Fatalf("retry: %d %+v", retry.Code, retried)
	}
	if artifacts, _ := env.server.store.CodingArtifacts(ctx, env.workspace, "s1"); len(artifacts) != 1 {
		t.Fatalf("retry duplicated: %+v", artifacts)
	}
	if activities, _ := env.server.store.AgentActivities(ctx, env.workspace, "task-1", ""); len(activities) != 1 {
		t.Fatalf("retry duplicated the activity: %d", len(activities))
	}

	// A recording: kind derived from the bytes, linked as a video.
	video := env.upload(t, uploadFields{token: "fcs_session", label: "checkout flow", turnID: "turn-1", file: testWebM})
	if video.Code != http.StatusCreated {
		t.Fatalf("video: %d %s", video.Code, video.Body.String())
	}

	// Listing: signed for one hour plus the stable content URL.
	list := env.get(t, "/api/coding/sessions/s1/artifacts", env.server.listCodingSessionArtifacts, map[string]string{"id": "s1"}, nil)
	var listed []domain.CodingArtifact
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || list.Code != http.StatusOK || len(listed) != 2 {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	if listed[0].ContentURL != contentPath || listed[0].ExpiresAt == nil || listed[0].ExpiresAt.After(time.Now().Add(61*time.Minute)) || listed[1].Kind != "video" || listed[1].ContentType != "video/webm" {
		t.Fatalf("listed: %+v", listed)
	}

	// The signed URL works through the existing signed-file route; a tampered
	// or expired signature does not.
	signed, _ := url.Parse(listed[0].URL)
	name := strings.TrimPrefix(signed.Path, "/api/files/signed/")
	served := env.get(t, signed.RequestURI(), env.server.serveSignedFile, map[string]string{"name": name}, nil)
	if served.Code != http.StatusOK || !bytes.Equal(served.Body.Bytes(), testPNG) || served.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("signed download: %d %q", served.Code, served.Header())
	}
	tampered := strings.Replace(signed.RequestURI(), "signature=", "signature=0", 1)
	if recorder := env.get(t, tampered, env.server.serveSignedFile, map[string]string{"name": name}, nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("tampered: %d", recorder.Code)
	}
	key, _ := env.server.fileSigningKey(ctx)
	expired, _ := url.Parse(signedFileURL(key, "https://flow.example", name, time.Now().Add(-time.Minute)))
	if recorder := env.get(t, expired.RequestURI(), env.server.serveSignedFile, map[string]string{"name": name}, nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("expired: %d", recorder.Code)
	}

	// Stable content route, with ranges for scrubbing recordings.
	content := env.get(t, contentPath, env.server.getCodingSessionArtifactContent, map[string]string{"id": "s1", "artifactId": artifact.ID}, nil)
	if content.Code != http.StatusOK || !bytes.Equal(content.Body.Bytes(), testPNG) || content.Header().Get("Content-Type") != "image/png" || content.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("content: %d %v", content.Code, content.Header())
	}
	ranged := env.get(t, contentPath, env.server.getCodingSessionArtifactContent, map[string]string{"id": "s1", "artifactId": listed[1].ID}, http.Header{"Range": {"bytes=0-3"}})
	if ranged.Code != http.StatusPartialContent || !bytes.Equal(ranged.Body.Bytes(), testWebM[:4]) {
		t.Fatalf("range: %d %d", ranged.Code, ranged.Body.Len())
	}
	if recorder := env.get(t, "/x", env.server.getCodingSessionArtifactContent, map[string]string{"id": "s1", "artifactId": "nope"}, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing artifact: %d", recorder.Code)
	}

	// Readers who cannot see the session's thread get 404s.
	env.authorize = errNotFound
	if recorder := env.get(t, "/x", env.server.listCodingSessionArtifacts, map[string]string{"id": "s1"}, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("unauthorized list: %d", recorder.Code)
	}
	if recorder := env.get(t, "/x", env.server.getCodingSessionArtifactContent, map[string]string{"id": "s1", "artifactId": artifact.ID}, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("unauthorized content: %d", recorder.Code)
	}
	env.authorize = nil
	if recorder := env.get(t, "/x", env.server.listCodingSessionArtifacts, map[string]string{"id": "unknown"}, nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown session: %d", recorder.Code)
	}

	// The markdown helper for tracks D and J signs for 30 days.
	markdown, err := env.server.CodingArtifactMarkdown(ctx, env.workspace, "s1", "")
	if err != nil || !strings.Contains(markdown, "| ![Settings page before](https://flow.example/api/files/signed/") || !strings.Contains(markdown, "- [checkout flow](https://flow.example/api/files/signed/") {
		t.Fatalf("markdown: %v\n%s", err, markdown)
	}
	if markdown, err := env.server.CodingArtifactMarkdown(ctx, env.workspace, "empty", ""); err != nil || markdown != "" {
		t.Fatalf("empty markdown: %q %v", markdown, err)
	}
}

func signedExpiry(t *testing.T, body string) time.Time {
	t.Helper()
	index := strings.Index(body, "expires=")
	if index < 0 {
		t.Fatalf("no signed URL in %s", body)
	}
	digits := body[index+len("expires="):]
	end := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' })
	unix, err := strconv.ParseInt(digits[:end], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return time.Unix(unix, 0)
}

func TestCodingArtifactTurnLimit(t *testing.T) {
	env := newArtifactTestEnv(t)
	for index := 0; index < coding.MaxArtifactsPerTurn; index++ {
		artifact := domain.CodingArtifact{ID: "pre" + strconv.Itoa(index), SessionID: "s1", Kind: "screenshot", Label: "x", TurnID: "turn-9", ContentType: "image/png", CreatedAt: time.Now()}
		if err := env.server.store.AddCodingArtifact(t.Context(), env.workspace, artifact, "coding-artifact_s1_"+artifact.ID+".png"); err != nil {
			t.Fatal(err)
		}
	}
	if recorder := env.upload(t, uploadFields{token: "fcs_session", turnID: "turn-9", file: testPNG}); recorder.Code != http.StatusTooManyRequests || errorCode(t, recorder) != apiErrorRateLimited {
		t.Fatalf("21st artifact of a turn: %d %s", recorder.Code, recorder.Body.String())
	}
	// Another turn still has room.
	if recorder := env.upload(t, uploadFields{token: "fcs_session", turnID: "turn-10", file: testPNG}); recorder.Code != http.StatusCreated {
		t.Fatalf("next turn: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestCodingArtifactActivityIsSkippedForFinishedThreads(t *testing.T) {
	env := newArtifactTestEnv(t)
	ctx := t.Context()
	task, err := env.server.store.CreateAgentInitiatedTask(ctx, domain.AgentTask{ID: "task-1", WorkspaceKey: env.workspace, AppUserID: "flow-agent", IssueID: "issue-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.server.store.AppendAgentActivity(ctx, env.workspace, task.ID, task.Version, domain.AgentActivity{ActorID: "flow-agent", Type: "response", Body: "Done"}); err != nil {
		t.Fatal(err)
	}
	recorder := env.upload(t, uploadFields{token: "fcs_session", label: "after", file: testPNG})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", recorder.Code, recorder.Body.String())
	}
	artifacts, _ := env.server.store.CodingArtifacts(ctx, env.workspace, "s1")
	if len(artifacts) != 1 || artifacts[0].ActivityID != "" {
		t.Fatalf("artifact is kept without an activity: %+v", artifacts)
	}
}

func TestCodingArtifactID(t *testing.T) {
	first, again, other := codingArtifactID("s1", "c"), codingArtifactID("s1", "c"), codingArtifactID("s2", "c")
	random1, random2 := codingArtifactID("s1", ""), codingArtifactID("s1", "")
	if first != again || first == other || random1 == random2 || len(first) != 26 || strings.ToLower(first) != first {
		t.Fatalf("ids: %s %s %s %s %s", first, again, other, random1, random2)
	}
	if humanBytes(10<<20) != "10 MiB" || humanBytes(1536) != "1536 bytes" || humanBytes(2048) != "2 KiB" {
		t.Fatal("humanBytes")
	}
}

// The upload body is streamed through MaxBytesReader: a chunked body larger
// than the limit is refused even without a Content-Length.
func TestCodingArtifactUploadChunkedTooLarge(t *testing.T) {
	env := newArtifactTestEnv(t)
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		part, _ := form.CreateFormFile("file", "big.png")
		_, _ = part.Write(testPNG)
		_, _ = part.Write(make([]byte, 200<<10))
		_ = form.Close()
		_ = writer.Close()
	}()
	request := httptest.NewRequest(http.MethodPost, "/internal/coding-runtime/artifacts", reader)
	request.ContentLength = -1
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer fcs_session")
	recorder := httptest.NewRecorder()
	env.server.codingRuntimeUploadArtifact(recorder, request)
	_, _ = io.Copy(io.Discard, reader)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d %s", recorder.Code, recorder.Body.String())
	}
}

var _ = errors.New
