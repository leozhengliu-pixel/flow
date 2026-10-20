package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

// echoProvider is the fake provider with an Exec that echoes stdin (a fake TTY).
type echoProvider struct {
	*sandbox.Fake
	mu       sync.Mutex
	resizes  []sandbox.TerminalSize
	commands [][]string
	stdin    []string
	// restored holds sandboxes whose runner wrote sandbox.RunnerReadyPath
	// after its restore step.
	restored map[string]bool
	probes   int
	tokens   map[string]string
}

// StartSandbox remembers the runtime token (what the runner reads from its
// token file); sandbox.Fake itself never retains tokens.
func (p *echoProvider) StartSandbox(ctx context.Context, spec sandbox.SandboxSpec) (sandbox.SandboxStatus, error) {
	p.mu.Lock()
	if p.tokens == nil {
		p.tokens = map[string]string{}
	}
	p.tokens[spec.ID] = spec.Token
	p.mu.Unlock()
	return p.Fake.StartSandbox(ctx, spec)
}

func (p *echoProvider) token(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tokens[id]
}

// finishRestore plays the runner finishing its restore step: it writes the
// ready marker.
func (p *echoProvider) finishRestore(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.restored == nil {
		p.restored = map[string]bool{}
	}
	p.restored[id] = true
}

func (p *echoProvider) probeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.probes
}

func (p *echoProvider) Exec(ctx context.Context, id string, request sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if status, _ := p.Fake.Status(ctx, id); status.Phase != sandbox.PhaseRunning {
		return sandbox.ExecResult{}, sandbox.ErrNotFound
	}
	if len(request.Command) == 3 && request.Command[0] == "test" && request.Command[2] == sandbox.RunnerReadyPath {
		// A sandbox restoring an archive is ready once its restore step
		// finished; one without an archive has nothing to restore.
		spec, _ := p.Fake.Spec(id)
		p.mu.Lock()
		defer p.mu.Unlock()
		p.probes++
		if spec.Workspace.ArchiveURL != "" && !p.restored[id] {
			return sandbox.ExecResult{ExitCode: 1}, nil
		}
		return sandbox.ExecResult{ExitCode: 0}, nil
	}
	p.mu.Lock()
	p.commands = append(p.commands, request.Command)
	p.mu.Unlock()
	if !request.TTY {
		data, _ := io.ReadAll(request.Stdin)
		p.mu.Lock()
		p.stdin = append(p.stdin, string(data))
		p.mu.Unlock()
		_, _ = request.Stdout.Write([]byte("prepared\n"))
		return sandbox.ExecResult{ExitCode: 0}, nil
	}
	go func() {
		for size := range request.Resize {
			p.mu.Lock()
			p.resizes = append(p.resizes, size)
			p.mu.Unlock()
		}
	}()
	buffer := make([]byte, 1024)
	for {
		n, err := request.Stdin.Read(buffer)
		if n > 0 {
			if strings.Contains(string(buffer[:n]), "exit") {
				return sandbox.ExecResult{ExitCode: 3}, nil
			}
			if _, werr := request.Stdout.Write(append([]byte("echo:"), buffer[:n]...)); werr != nil {
				return sandbox.ExecResult{}, werr
			}
		}
		if err != nil {
			return sandbox.ExecResult{ExitCode: 0}, nil
		}
	}
}

// gitlabCheckHost is a GitLab host stub with track J's credential-mode check.
type gitlabCheckHost struct {
	codehost.Stub
	err error
}

func (h gitlabCheckHost) CheckCredentialMode(context.Context, string, codehost.RepoRef) error {
	return h.err
}

type codingEnvTestServer struct {
	t        *testing.T
	server   *server
	handler  http.Handler
	provider *echoProvider
	store    *store.SQLiteStore
	dbPath   string
	usage    []domain.CodingUsageRecord
	mu       sync.Mutex
}

func newCodingEnvTestServer(t *testing.T) *codingEnvTestServer {
	t.Helper()
	t.Setenv("FLOW_CODING_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)))
	dbPath := filepath.Join(t.TempDir(), "flow.db")
	repository, err := store.OpenSQLiteTestFixture(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	objects, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	provider := &echoProvider{Fake: sandbox.NewFake(nil)}
	config := appconfig.CodingConfig{
		Enabled: true, Provider: appconfig.CodingProviderFake, RuntimeURL: "http://flow.test", InstanceID: "test", Image: "img:test", DebugTerminal: true,
		Sizes:            map[string]appconfig.SandboxSize{"medium": {CPU: "2", Memory: "16Gi"}},
		Snapshot:         appconfig.CodingSnapshotConfig{Strategy: "archive", ArchivePrefix: "coding-snapshots", ArchiveMaxBytes: 1 << 20, Timeout: 30 * time.Minute, Retain: 2},
		Timeouts:         appconfig.CodingTimeouts{DebugTerminal: 20 * time.Minute, Start: time.Minute, StopGrace: time.Second},
		RuntimePricesUSD: map[string]float64{"medium": 0.36},
	}
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true, objectStore: objects}
	s.coding = &codingServices{config: config, sandbox: provider, hosts: map[codehost.Kind]codehost.Host{domain.CodeHostGitHub: codehost.Stub{HostKind: domain.CodeHostGitHub}, domain.CodeHostGitLab: gitlabCheckHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitLab}}}}
	harness := &codingEnvTestServer{t: t, server: s, handler: newHandler(s), provider: provider, store: repository, dbPath: dbPath}
	codingEnvironmentLauncherOverride = func(s *server) coding.SandboxLauncher {
		return coding.ProviderLauncher{Provider: provider, Config: s.coding.config, Usage: func(_ context.Context, record domain.CodingUsageRecord) error {
			harness.mu.Lock()
			harness.usage = append(harness.usage, record)
			harness.mu.Unlock()
			return nil
		}}
	}
	t.Cleanup(func() {
		codingEnvironmentLauncherOverride, codingSandboxAuthenticator, codingSessionLookup, codingEnvironmentSessionStarter = nil, nil, nil, nil
	})
	return harness
}

func (h *codingEnvTestServer) do(method, path string, body any, token string) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader io.Reader
	contentType := "application/json"
	switch value := body.(type) {
	case nil:
	case []byte:
		reader, contentType = bytes.NewReader(value), "application/octet-stream"
	default:
		raw, _ := json.Marshal(value)
		reader = bytes.NewReader(raw)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	h.handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeRecorder[T any](t *testing.T, recorder *httptest.ResponseRecorder, want int) T {
	t.Helper()
	if recorder.Code != want {
		t.Fatalf("status %d, want %d: %s", recorder.Code, want, recorder.Body.String())
	}
	var value T
	if recorder.Body.Len() > 0 {
		if err := json.Unmarshal(recorder.Body.Bytes(), &value); err != nil {
			t.Fatalf("decode %s: %v", recorder.Body.String(), err)
		}
	}
	return value
}

func TestCodingEnvironmentRoutes(t *testing.T) {
	h := newCodingEnvTestServer(t)
	created := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, "/api/coding/environments", map[string]any{
		"repository": "acme/web", "prepareScript": "npm ci", "mise": map[string]any{"tools": map[string]string{"node": "22"}},
		"secrets": []map[string]string{{"name": "NPM_TOKEN", "value": "npm_secret_value"}},
	}, ""), http.StatusCreated)
	if created.Repository != "acme/web" || len(created.Secrets) != 1 || created.Secrets[0].Name != "NPM_TOKEN" || created.LatestSnapshot == nil || created.LatestSnapshot.Status != domain.CodingSnapshotQueued {
		t.Fatalf("created: %+v", created)
	}
	path := "/api/coding/environments/" + created.ID
	if body := h.do(http.MethodGet, path, nil, "").Body.String(); strings.Contains(body, "npm_secret_value") {
		t.Fatal("secret values are never returned")
	}
	conflict := h.do(http.MethodPost, "/api/coding/environments", map[string]any{"repository": "ACME/web"}, "")
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "REPO_IN_USE") {
		t.Fatalf("repo in use: %d %s", conflict.Code, conflict.Body.String())
	}
	if invalid := h.do(http.MethodPost, "/api/coding/environments", map[string]any{"repository": "acme/x", "sandboxSize": "huge"}, ""); invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "sandboxSize") {
		t.Fatalf("invalid: %d %s", invalid.Code, invalid.Body.String())
	}
	if missing := h.do(http.MethodPatch, path, map[string]any{"prepareScript": "x"}, ""); missing.Code != http.StatusBadRequest {
		t.Fatalf("version required: %d", missing.Code)
	}
	updated := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPatch, path, map[string]any{"version": created.Version, "prepareScript": "pnpm i", "id": "ignored"}, ""), http.StatusOK)
	if updated.PrepareScript != "pnpm i" || updated.Version != created.Version+1 || updated.ID != created.ID || updated.Mise.Tools["node"] != "22" {
		t.Fatalf("updated: %+v", updated)
	}
	if stale := h.do(http.MethodPatch, path, map[string]any{"version": created.Version, "prepareScript": "y"}, ""); stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "VERSION_CONFLICT") {
		t.Fatalf("stale: %d %s", stale.Code, stale.Body.String())
	}
	// The config change queued a new snapshot and superseded the queued one.
	snapshots := decodeRecorder[[]domain.CodingEnvironmentSnapshot](t, h.do(http.MethodGet, path+"/snapshots", nil, ""), http.StatusOK)
	if len(snapshots) != 2 || snapshots[0].Status != domain.CodingSnapshotQueued || snapshots[1].Status != domain.CodingSnapshotSuperseded {
		t.Fatalf("snapshots: %+v", snapshots)
	}
	if again := h.do(http.MethodPost, path+"/snapshots", nil, ""); again.Code != http.StatusOK {
		t.Fatalf("refresh with the same config reuses the queued build: %d", again.Code)
	}
	canceled := decodeRecorder[domain.CodingEnvironmentSnapshot](t, h.do(http.MethodPost, "/api/coding/snapshots/"+snapshots[0].ID+"/cancel", nil, ""), http.StatusOK)
	if canceled.ID != snapshots[0].ID {
		t.Fatalf("cancel: %+v", canceled)
	}

	// Secrets: add-only, immutable, deletable.
	exists := h.do(http.MethodPost, path+"/secrets", map[string]any{"secrets": []map[string]string{{"name": "NPM_TOKEN", "value": "other"}}}, "")
	if exists.Code != http.StatusConflict || !strings.Contains(exists.Body.String(), "SECRET_EXISTS") {
		t.Fatalf("immutable: %d %s", exists.Code, exists.Body.String())
	}
	withSecret := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, path+"/secrets", map[string]any{"secrets": []map[string]string{{"name": "DB_PASSWORD", "value": "pw"}}}, ""), http.StatusOK)
	if len(withSecret.Secrets) != 2 {
		t.Fatalf("added: %+v", withSecret.Secrets)
	}
	if reserved := h.do(http.MethodPost, path+"/secrets", map[string]any{"secrets": []map[string]string{{"name": "FLOW_X", "value": "pw"}}}, ""); reserved.Code != http.StatusBadRequest {
		t.Fatalf("reserved: %d", reserved.Code)
	}
	afterDelete := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodDelete, path+"/secrets/DB_PASSWORD", nil, ""), http.StatusOK)
	if len(afterDelete.Secrets) != 1 {
		t.Fatalf("deleted: %+v", afterDelete.Secrets)
	}
	if missing := h.do(http.MethodDelete, path+"/secrets/DB_PASSWORD", nil, ""); missing.Code != http.StatusNotFound {
		t.Fatalf("delete missing: %d", missing.Code)
	}

	// .env import preview stores nothing.
	preview := decodeRecorder[coding.EnvImportResult](t, h.do(http.MethodPost, path+"/env-import", map[string]string{"content": "export NODE_ENV=dev\nAPI_KEY='abc'\nFLOW_TOKEN=x\n"}, ""), http.StatusOK)
	if len(preview.Variables) != 1 || len(preview.Secrets) != 1 || preview.Secrets[0].Value != "abc" || len(preview.Warnings) != 1 {
		t.Fatalf("preview: %+v", preview)
	}
	if after := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodGet, path, nil, ""), http.StatusOK); len(after.Secrets) != 1 || len(after.Mise.EnvironmentVariables) != 0 {
		t.Fatalf("import must not store: %+v", after)
	}

	// Archive and unarchive.
	archived := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, path+"/archive", nil, ""), http.StatusOK)
	if archived.ArchivedAt == nil {
		t.Fatal("archived")
	}
	if list := decodeRecorder[[]domain.CodingEnvironment](t, h.do(http.MethodGet, "/api/coding/environments", nil, ""), http.StatusOK); len(list) != 0 {
		t.Fatalf("archived are hidden: %d", len(list))
	}
	if list := decodeRecorder[[]domain.CodingEnvironment](t, h.do(http.MethodGet, "/api/coding/environments?archived=true", nil, ""), http.StatusOK); len(list) != 1 {
		t.Fatalf("archived listed on request: %d", len(list))
	}
	if refresh := h.do(http.MethodPost, path+"/snapshots", nil, ""); refresh.Code != http.StatusBadRequest {
		t.Fatalf("archived cannot build: %d", refresh.Code)
	}
	decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, path+"/unarchive", nil, ""), http.StatusOK)
	if notFound := h.do(http.MethodGet, "/api/coding/environments/nope", nil, ""); notFound.Code != http.StatusNotFound {
		t.Fatalf("not found: %d", notFound.Code)
	}
}

func TestCodingEnvironmentGitLabCredentialCheck(t *testing.T) {
	h := newCodingEnvTestServer(t)
	h.server.coding.hosts[domain.CodeHostGitLab] = gitlabCheckHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitLab}, err: errors.New("the GitLab connection needs Maintainer access")}
	refused := h.do(http.MethodPost, "/api/coding/environments", map[string]any{"codeHost": "gitlab", "repository": "group/app"}, "")
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "Maintainer") {
		t.Fatalf("credential mode check: %d %s", refused.Code, refused.Body.String())
	}
	h.server.coding.hosts[domain.CodeHostGitLab] = gitlabCheckHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitLab}, err: codehost.ErrTransient}
	decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, "/api/coding/environments", map[string]any{"codeHost": "gitlab", "repository": "group/app"}, ""), http.StatusCreated)
}

func TestCodingEnvironmentRuntimeRoutes(t *testing.T) {
	h := newCodingEnvTestServer(t)
	created := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, "/api/coding/environments", map[string]any{"repository": "acme/run"}, ""), http.StatusCreated)
	workspace := created.WorkspaceKey
	if workspace == "" {
		workspace = h.store.WorkspaceKeys()[0]
	}
	snapshotID := created.LatestSnapshot.ID
	// Start the build so the snapshot has a sandbox.
	reconciler := h.server.codingSnapshotReconciler("test")
	if err := reconciler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := h.store.CodingSnapshotJob(context.Background(), workspace, snapshotID)
	if err != nil || job.Snapshot.Status != domain.CodingSnapshotBuilding {
		t.Fatalf("building: %+v %v", job, err)
	}
	grants := map[string]coding.SandboxGrant{
		"fcs_build":   {WorkspaceKey: workspace, SandboxID: job.Snapshot.SandboxID, SnapshotID: snapshotID, Kind: sandbox.KindSnapshot},
		"fcs_other":   {WorkspaceKey: workspace, SandboxID: "x", SnapshotID: "another", Kind: sandbox.KindSnapshot},
		"fcs_restore": {WorkspaceKey: workspace, SandboxID: "r", SessionID: "sess", SnapshotID: snapshotID, Kind: sandbox.KindSession},
		"fcs_setup":   {WorkspaceKey: workspace, SandboxID: "s", SessionID: "setup-session", Kind: sandbox.KindSession},
	}
	codingSandboxAuthenticator = func(_ context.Context, token string) (coding.SandboxGrant, error) {
		if grant, ok := grants[token]; ok {
			return grant, nil
		}
		return coding.SandboxGrant{}, coding.ErrTokenInvalid
	}
	base := "/internal/coding-runtime/snapshots/" + snapshotID
	if unauth := h.do(http.MethodPost, base+"/logs", []byte("x"), ""); unauth.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", unauth.Code)
	}
	if bad := h.do(http.MethodPost, base+"/logs", []byte("x"), "fcs_wrong"); bad.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", bad.Code)
	}
	if other := h.do(http.MethodPost, base+"/logs", []byte("x"), "fcs_other"); other.Code != http.StatusForbidden {
		t.Fatalf("other snapshot: %d", other.Code)
	}
	if logs := h.do(http.MethodPost, base+"/logs", []byte("installing node\n"), "fcs_build"); logs.Code != http.StatusNoContent {
		t.Fatalf("logs: %d %s", logs.Code, logs.Body.String())
	}
	if logs := h.do(http.MethodPost, base+"/logs", map[string]any{"lines": []string{"npm ci", "done"}}, "fcs_build"); logs.Code != http.StatusNoContent {
		t.Fatalf("json logs: %d", logs.Code)
	}
	read := decodeRecorder[map[string]any](t, h.do(http.MethodGet, "/api/coding/snapshots/"+snapshotID+"/logs", nil, ""), http.StatusOK)
	if read["logs"] != "installing node\nnpm ci\ndone\n" || read["complete"] != false {
		t.Fatalf("read logs: %+v", read)
	}
	archive := append([]byte{0x28, 0xb5, 0x2f, 0xfd}, bytes.Repeat([]byte("zstd"), 100)...)
	if invalid := h.do(http.MethodPut, base+"/archive", []byte("not an archive"), "fcs_build"); invalid.Code != http.StatusBadRequest {
		t.Fatalf("zstd magic required: %d", invalid.Code)
	}
	put := decodeRecorder[map[string]any](t, h.do(http.MethodPut, base+"/archive", archive, "fcs_build"), http.StatusOK)
	if put["sizeBytes"] != float64(len(archive)) || put["sha256"] == "" {
		t.Fatalf("upload: %+v", put)
	}
	if tooBig := h.do(http.MethodPut, base+"/archive", append([]byte{0x28, 0xb5, 0x2f, 0xfd}, bytes.Repeat([]byte("x"), 2<<20)...), "fcs_build"); tooBig.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("archive limit: %d", tooBig.Code)
	}
	// The oversized upload removed the object; upload again.
	decodeRecorder[map[string]any](t, h.do(http.MethodPut, base+"/archive", archive, "fcs_build"), http.StatusOK)
	if bad := h.do(http.MethodPost, base+"/complete", map[string]any{"status": "weird"}, "fcs_build"); bad.Code != http.StatusBadRequest {
		t.Fatalf("bad status: %d", bad.Code)
	}
	if done := h.do(http.MethodPost, base+"/complete", map[string]any{"status": "ready", "baseSha": "abc"}, "fcs_build"); done.Code != http.StatusNoContent {
		t.Fatalf("complete: %d %s", done.Code, done.Body.String())
	}
	if err := reconciler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Due again only after the poll interval; run the step directly.
	job, _ = h.store.CodingSnapshotJob(context.Background(), workspace, snapshotID)
	if job.Snapshot.Status == domain.CodingSnapshotBuilding {
		reconciler.Now = func() time.Time { return time.Now().Add(time.Minute) }
		if err := reconciler.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		job, _ = h.store.CodingSnapshotJob(context.Background(), workspace, snapshotID)
	}
	if job.Snapshot.Status != domain.CodingSnapshotReady || job.Snapshot.SizeBytes != int64(len(archive)) || job.Snapshot.BaseSHA != "abc" {
		t.Fatalf("ready from the uploaded archive: %+v", job.Snapshot)
	}
	restore := h.do(http.MethodGet, base+"/archive", nil, "fcs_restore")
	if restore.Code != http.StatusOK || !bytes.Equal(restore.Body.Bytes(), archive) {
		t.Fatalf("restore download: %d %d bytes", restore.Code, restore.Body.Len())
	}
	if denied := h.do(http.MethodGet, base+"/archive", nil, "fcs_setup"); denied.Code != http.StatusForbidden {
		t.Fatalf("restore without the snapshot grant: %d", denied.Code)
	}

	// Configure with agent → proposal → apply.
	// The real starter goes through the session core, which refuses while the
	// workspace has not enabled coding sessions.
	if unavailable := h.do(http.MethodPost, "/api/coding/environments/"+created.ID+"/configure-with-agent", map[string]any{}, ""); unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("coding sessions off for the workspace: %d", unavailable.Code)
	}
	var started codingEnvironmentSetupInput
	codingEnvironmentSessionStarter = func(_ context.Context, _ *server, input codingEnvironmentSetupInput) (domain.CodingSession, error) {
		started = input
		return domain.CodingSession{ID: "setup-session", Source: domain.CodingSourceEnvironmentSetup, EnvironmentID: input.EnvironmentID}, nil
	}
	session := decodeRecorder[domain.CodingSession](t, h.do(http.MethodPost, "/api/coding/environments/"+created.ID+"/configure-with-agent", map[string]any{"prompt": "use pnpm"}, ""), http.StatusCreated)
	if session.ID != "setup-session" || started.TurnKind != coding.TurnConfigureEnvironment || !strings.Contains(started.Prompt, "use pnpm") || started.EnvironmentID != created.ID {
		t.Fatalf("configure: %+v %+v", session, started)
	}
	codingSessionLookup = func(_ context.Context, _, id string) (domain.CodingSession, error) {
		if id == "setup-session" {
			return domain.CodingSession{ID: id, Source: domain.CodingSourceEnvironmentSetup, EnvironmentID: created.ID}, nil
		}
		return domain.CodingSession{}, coding.ErrNotFound
	}
	script := "pnpm install"
	accepted := decodeRecorder[map[string]any](t, h.do(http.MethodPost, "/internal/coding-runtime/environment-proposal", map[string]any{
		"prepareScript": script, "mise": map[string]any{"tools": map[string]string{"node": "22"}, "environmentVariables": []map[string]string{{"name": "SECRET_TOKEN", "value": "leak"}}},
		"explanation": "pnpm workspace", "verified": true,
	}, "fcs_setup"), http.StatusOK)
	if missing, _ := accepted["missingSecrets"].([]any); len(missing) != 1 || missing[0] != "SECRET_TOKEN" {
		t.Fatalf("secret values are stripped: %+v", accepted)
	}
	if denied := h.do(http.MethodPost, "/internal/coding-runtime/environment-proposal", map[string]any{"prepareScript": "x"}, "fcs_build"); denied.Code != http.StatusForbidden {
		t.Fatalf("snapshot sandboxes cannot propose: %d", denied.Code)
	}
	withProposal := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodGet, "/api/coding/environments/"+created.ID, nil, ""), http.StatusOK)
	if withProposal.PendingProposal == nil || *withProposal.PendingProposal.PrepareScript != script || strings.Contains(h.do(http.MethodGet, "/api/coding/environments/"+created.ID, nil, "").Body.String(), "leak") {
		t.Fatalf("pending proposal: %+v", withProposal.PendingProposal)
	}
	applied := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, "/api/coding/environments/"+created.ID+"/proposal/apply", map[string]any{}, ""), http.StatusOK)
	if applied.PendingProposal != nil || applied.PrepareScript != script || applied.Mise.Tools["node"] != "22" {
		t.Fatalf("applied: %+v", applied)
	}
	if none := h.do(http.MethodPost, "/api/coding/environments/"+created.ID+"/proposal/apply", map[string]any{"discard": true}, ""); none.Code != http.StatusConflict {
		t.Fatalf("no proposal: %d", none.Code)
	}
	// Fix with agent picks the latest failed snapshot's logs.
	fix := h.do(http.MethodPost, "/api/coding/environments/"+created.ID+"/fix-with-agent", map[string]any{}, "")
	if fix.Code != http.StatusCreated || started.TurnKind != coding.TurnFixEnvironment {
		t.Fatalf("fix: %d %+v", fix.Code, started)
	}
}

func TestCodingEnvironmentTerminal(t *testing.T) {
	h := newCodingEnvTestServer(t)
	created := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, "/api/coding/environments", map[string]any{"repository": "acme/term", "prepareScript": "make deps", "secrets": []map[string]string{{"name": "API_KEY", "value": "it's-secret"}}}, ""), http.StatusCreated)
	server := httptest.NewServer(h.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/coding/environments/" + created.ID + "/terminal"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	readStatus := codingTerminalReader(t, ctx, conn)
	starting := readStatus("progress", "")
	sandboxID, _ := starting["sandboxId"].(string)
	if ready := readStatus("ready", ""); ready["expiresAt"] == nil {
		t.Fatalf("ready carries expiresAt: %v", ready)
	}
	spec, ok := h.provider.Spec(sandboxID)
	if !ok || spec.Kind != sandbox.KindDebug || spec.Env[sandbox.EnvMode] != coding.ModeDebug || spec.Deadline != 20*time.Minute {
		t.Fatalf("debug sandbox: %+v", spec)
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("ls\n")); err != nil {
		t.Fatal(err)
	}
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.MessageBinary {
			if string(data) != "echo:ls\n" {
				t.Fatalf("tty output: %q", data)
			}
			break
		}
	}
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"runPrepare"}`)); err != nil {
		t.Fatal(err)
	}
	readStatus("status", "finished with exit code 0")
	h.provider.mu.Lock()
	resizes, stdin := append([]sandbox.TerminalSize(nil), h.provider.resizes...), append([]string(nil), h.provider.stdin...)
	h.provider.mu.Unlock()
	if len(resizes) != 1 || resizes[0].Width != 120 || resizes[0].Height != 40 {
		t.Fatalf("resize: %+v", resizes)
	}
	if len(stdin) != 1 || !strings.Contains(stdin[0], `export API_KEY='it'"'"'s-secret'`) || !strings.Contains(stdin[0], "make deps") {
		t.Fatalf("prepare script through stdin: %q", stdin)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	exited := readStatus("exit", "")
	if exited["code"] != float64(3) {
		t.Fatalf("exit code: %v", exited)
	}
	// The sandbox is released and its runtime billed once the socket closes.
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, _ := h.provider.Status(context.Background(), sandboxID)
		h.mu.Lock()
		billed := len(h.usage)
		h.mu.Unlock()
		if status.Phase == sandbox.PhaseMissing && billed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sandbox not released/billed: %+v billed=%d", status, billed)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if h.usage[0].Kind != "runtime" || h.usage[0].SandboxID != sandboxID {
		t.Fatalf("usage: %+v", h.usage[0])
	}
}

func TestCodingEnvironmentTerminalDisabled(t *testing.T) {
	h := newCodingEnvTestServer(t)
	created := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, "/api/coding/environments", map[string]any{"repository": "acme/off"}, ""), http.StatusCreated)
	h.server.coding.config.DebugTerminal = false
	if off := h.do(http.MethodGet, "/api/coding/environments/"+created.ID+"/terminal", nil, ""); off.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled terminal: %d", off.Code)
	}
}

// codingTerminalReader returns a reader that waits for a server text frame
// of type want (whose message contains contains, when set).
func codingTerminalReader(t *testing.T, ctx context.Context, conn *websocket.Conn) func(want, contains string) map[string]any {
	return func(want, contains string) map[string]any {
		t.Helper()
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("waiting for %s: %v", want, err)
			}
			if kind != websocket.MessageText {
				continue
			}
			var frame map[string]any
			_ = json.Unmarshal(data, &frame)
			message, _ := frame["message"].(string)
			if frame["type"] == want && strings.Contains(message, contains) {
				return frame
			}
			if frame["type"] == "error" && want != "error" {
				t.Fatalf("terminal error: %v", frame)
			}
		}
	}
}

func TestCodingEnvironmentTerminalExpires(t *testing.T) {
	h := newCodingEnvTestServer(t)
	h.server.coding.config.Timeouts.DebugTerminal = 1500 * time.Millisecond
	created := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, "/api/coding/environments", map[string]any{"repository": "acme/ttl"}, ""), http.StatusCreated)
	server := httptest.NewServer(h.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/coding/environments/"+created.ID+"/terminal", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	read := codingTerminalReader(t, ctx, conn)
	read("ready", "")
	if exit := read("exit", "time limit"); exit["type"] != "exit" {
		t.Fatalf("exit on TTL: %v", exit)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("the socket closes after the TTL")
	}
}
