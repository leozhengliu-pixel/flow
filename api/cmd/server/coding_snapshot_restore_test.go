package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

// Snapshot restores over the real chain: the sandbox record written by the
// launcher or the session executor carries the snapshot id, the sandbox
// reconciler mints the runtime token for that record, the token's grant
// carries the record's snapshot id, and GET
// /internal/coding-runtime/snapshots/{id}/archive serves the real archive
// from the object store only for that id.

// codingTestArchive is a real snapshot archive (tar.zst of workspace/repo, a
// git repository with one commit) and the commit's sha.
func codingTestArchive(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	for _, tool := range []string{"git", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	src := t.TempDir()
	repo := filepath.Join(src, "workspace", "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Flow", "-c", "user.email=flow@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "README.md")
	git("commit", "-q", "-m", "snapshot "+name)
	head := git("rev-parse", "HEAD")
	archive := filepath.Join(t.TempDir(), "archive.tar.zst")
	if out, err := exec.Command("tar", "--zstd", "-cf", archive, "-C", src, "workspace").CombinedOutput(); err != nil {
		t.Skipf("tar cannot write zstd archives here: %v %s", err, out)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	return data, head
}

// codingTestRestoredHead unpacks an archive the way the restore step does
// and returns the HEAD of the restored repository.
func codingTestRestoredHead(t *testing.T, archive []byte) string {
	t.Helper()
	root := t.TempDir()
	command := exec.Command("tar", "--zstd", "-xf", "-", "-C", root)
	command.Stdin = bytes.NewReader(archive)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unpack: %v %s", err, out)
	}
	out, err := exec.Command("git", "-C", filepath.Join(root, "workspace", "repo"), "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("restored repository: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// codingTestReadySnapshot stores archive through the server's archive
// strategy (the test object store) and records a ready snapshot for it.
func codingTestReadySnapshot(t *testing.T, s *server, workspace string, environment domain.CodingEnvironment, archive []byte, configHash string, created time.Time) domain.CodingEnvironmentSnapshot {
	t.Helper()
	ctx := context.Background()
	archives, ok := s.codingSnapshotStrategy().(coding.ArchiveStore)
	if !ok {
		t.Fatal("the archive strategy is configured")
	}
	id := coding.NewResourceID(created)
	ref, size, _, err := archives.Store(ctx, id, workspace, bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	finished := created.Add(time.Minute)
	snapshot := domain.CodingEnvironmentSnapshot{
		ID: id, EnvironmentID: environment.ID, Status: domain.CodingSnapshotReady, Strategy: sandbox.StrategyArchive, ConfigHash: configHash,
		StorageRef: ref, SizeBytes: size, CreatedAt: created, FinishedAt: &finished,
	}
	if err := s.store.CreateCodingSnapshot(ctx, workspace, snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// codingTestGetArchive is the runner's archive download over real HTTP.
func codingTestGetArchive(t *testing.T, base, archiveURL, token string) (int, []byte) {
	t.Helper()
	path := archiveURL
	if index := strings.Index(path, "/internal/"); index >= 0 {
		path = path[index:]
	}
	request, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, body
}

// runSandboxReconciler runs the real sandbox reconciler until the test ends,
// making rows due again between passes instead of waiting for them.
func runSandboxReconciler(t *testing.T, s *server, dbPath string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &sandbox.Reconciler{Store: s.codingStore(), Provider: s.coding.sandbox, Config: s.coding.config, Owner: "restore-test"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			_, _ = raw.ExecContext(ctx, `UPDATE coding_sandboxes SET lease_until=0 WHERE lease_owner=''`)
			_, _ = reconciler.RunOnce(ctx)
			select {
			case <-ctx.Done():
			case <-time.After(50 * time.Millisecond):
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		raw.Close()
	})
}

func newRestoreTerminalTest(t *testing.T) (*codingEnvTestServer, domain.CodingEnvironment, string) {
	t.Helper()
	h := newCodingEnvTestServer(t)
	created := decodeRecorder[domain.CodingEnvironment](t, h.do(http.MethodPost, "/api/coding/environments", map[string]any{"repository": "acme/restore"}, ""), http.StatusCreated)
	workspace := ""
	for _, key := range h.store.WorkspaceKeys() {
		if _, err := h.store.CodingEnvironment(context.Background(), key, created.ID); err == nil {
			workspace = key
		}
	}
	if workspace == "" {
		t.Fatal("the environment's workspace")
	}
	return h, created, workspace
}

func dialCodingTerminal(t *testing.T, ctx context.Context, server *httptest.Server, environmentID string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/coding/environments/"+environmentID+"/terminal", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

// TestCodingTerminalRestoresGrantedSnapshot drives the debug terminal over
// the production launcher (coding_sandboxes rows) and the real sandbox
// reconciler: the record and the token grant carry the snapshot, the archive
// route serves exactly that snapshot (HEAD matches) and refuses another one,
// and the terminal is ready only after the restore step finished.
func TestCodingTerminalRestoresGrantedSnapshot(t *testing.T) {
	h, environment, workspace := newRestoreTerminalTest(t)
	codingEnvironmentLauncherOverride = nil // the production RecordLauncher
	archive, head := codingTestArchive(t, "granted")
	otherArchive, _ := codingTestArchive(t, "other")
	other := codingTestReadySnapshot(t, h.server, workspace, environment, otherArchive, "", time.Now().Add(-2*time.Hour))
	granted := codingTestReadySnapshot(t, h.server, workspace, environment, archive, "", time.Now().Add(-time.Hour))
	server := httptest.NewServer(h.handler)
	defer server.Close()
	runSandboxReconciler(t, h.server, h.dbPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := dialCodingTerminal(t, ctx, server, environment.ID)
	frames := codingTerminalFrames(ctx, conn)
	next := func() map[string]any {
		t.Helper()
		select {
		case frame, ok := <-frames:
			if !ok {
				t.Fatal("the terminal closed")
			}
			if frame["type"] == "error" {
				t.Fatalf("terminal error: %v", frame)
			}
			return frame
		case <-ctx.Done():
			t.Fatal("timed out waiting for a terminal frame")
		}
		return nil
	}
	starting := next()
	sandboxID, _ := starting["sandboxId"].(string)
	if starting["type"] != "progress" || sandboxID == "" || starting["restored"] != nil {
		t.Fatalf("the first frame announces the start, not a finished restore: %v", starting)
	}
	if restoring := next(); restoring["type"] != "progress" || restoring["message"] != "Restoring the snapshot" {
		t.Fatalf("restoring: %v", restoring)
	}

	// The record (and so the grant) is bound to the snapshot the archive URL names.
	record, err := h.server.codingStore().CodingSandbox(ctx, sandboxID)
	if err != nil {
		t.Fatal(err)
	}
	if record.SnapshotID != granted.ID || record.Kind != sandbox.KindDebug || !strings.HasSuffix(record.Workspace.ArchiveURL, sandbox.ArchivePath(granted.ID)) {
		t.Fatalf("debug sandbox record: snapshot=%q kind=%s archive=%q", record.SnapshotID, record.Kind, record.Workspace.ArchiveURL)
	}
	spec, ok := h.provider.Spec(sandboxID)
	token := h.provider.token(sandboxID)
	if !ok || spec.SnapshotID != granted.ID || spec.Workspace.ArchiveURL != record.Workspace.ArchiveURL || !strings.HasPrefix(token, coding.SandboxTokenPrefix) {
		t.Fatalf("started sandbox: ok=%v snapshot=%q archive=%q token=%v", ok, spec.SnapshotID, spec.Workspace.ArchiveURL, token != "")
	}
	grant, err := h.server.codingStore().AuthenticateSandboxToken(ctx, token)
	if err != nil || grant.SnapshotID != granted.ID || grant.Kind != sandbox.KindDebug {
		t.Fatalf("grant: %+v %v", grant, err)
	}
	// The runner's restore step: the granted archive downloads and restores
	// the snapshot's HEAD; another snapshot of the environment is refused.
	status, body := codingTestGetArchive(t, server.URL, spec.Workspace.ArchiveURL, token)
	if status != http.StatusOK || !bytes.Equal(body, archive) {
		t.Fatalf("granted archive: %d (%d bytes, want %d)", status, len(body), len(archive))
	}
	if restored := codingTestRestoredHead(t, body); restored != head {
		t.Fatalf("restored HEAD %s, want %s", restored, head)
	}
	if status, _ := codingTestGetArchive(t, server.URL, sandbox.ArchivePath(other.ID), token); status != http.StatusForbidden {
		t.Fatalf("an ungranted snapshot: %d, want 403", status)
	}

	// Not ready while the restore step runs.
	for h.provider.probeCount() < 2 {
		select {
		case frame := <-frames:
			t.Fatalf("a frame before the restore finished: %v", frame)
		case <-ctx.Done():
			t.Fatal("the terminal never checked the restore")
		case <-time.After(50 * time.Millisecond):
		}
	}
	h.provider.finishRestore(sandboxID)
	restored := next()
	if restored["type"] != "progress" || restored["restored"] != true {
		t.Fatalf("restored progress: %v", restored)
	}
	if ready := next(); ready["type"] != "ready" || ready["expiresAt"] == nil {
		t.Fatalf("ready: %v", ready)
	}
}

// TestCodingTerminalReportsRestoreFailure: when the restore step fails the
// client gets the runner's reason, never "ready" and an exit code.
func TestCodingTerminalReportsRestoreFailure(t *testing.T) {
	h, environment, workspace := newRestoreTerminalTest(t)
	archive, _ := codingTestArchive(t, "failing")
	snapshot := codingTestReadySnapshot(t, h.server, workspace, environment, archive, "", time.Now().Add(-time.Hour))
	server := httptest.NewServer(h.handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn := dialCodingTerminal(t, ctx, server, environment.ID)
	read := codingTerminalReader(t, ctx, conn)
	starting := read("progress", "")
	sandboxID, _ := starting["sandboxId"].(string)
	read("progress", "Restoring the snapshot")
	// The restore step fails (as a 403 from the archive route would make it)
	// and the container stops with the restore exit code.
	reason := "restoring snapshot " + snapshot.ID + " failed: runtime API rejected the sandbox token (403)"
	h.provider.AppendLog(sandboxID, `{"time":"2026-10-19T00:00:00Z","level":"info","msg":"flow-runner starting","mode":"restore"}`+"\n")
	h.provider.AppendLog(sandboxID, `{"time":"2026-10-19T00:00:01Z","level":"error","msg":"restore failed","error":"`+reason+`"}`+"\n")
	if err := h.provider.SetPhase(sandboxID, sandbox.PhaseFailed, sandbox.ReasonInitFailed); err != nil {
		t.Fatal(err)
	}
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for the restore error: %v", err)
		}
		if kind != websocket.MessageText {
			continue
		}
		text := string(data)
		if strings.Contains(text, `"ready"`) || strings.Contains(text, `"exit"`) {
			t.Fatalf("a failed restore must not attach a shell: %s", text)
		}
		if strings.Contains(text, `"error"`) {
			if !strings.Contains(text, "Restoring the snapshot failed: "+reason) {
				t.Fatalf("restore error: %s", text)
			}
			break
		}
	}
}

func TestRunnerRestoreError(t *testing.T) {
	log := "\x01\x00\x00\x00\x00\x00\x00\x40" + `{"level":"info","msg":"restoring snapshot","snapshotId":"a"}` + "\n" +
		"\x02\x00\x00\x00\x00\x00\x00\x40" + `{"level":"error","msg":"restore failed","error":"boom (403)"}` + "\n"
	if got := runnerRestoreError(strings.NewReader(log)); got != "boom (403)" {
		t.Fatalf("restore error = %q", got)
	}
	if got := runnerRestoreError(strings.NewReader(`{"msg":"snapshot restored"}`)); got != "" {
		t.Fatalf("no failure = %q", got)
	}
}

// codingTerminalFrames streams the server's text frames.
func codingTerminalFrames(ctx context.Context, conn *websocket.Conn) <-chan map[string]any {
	frames := make(chan map[string]any)
	go func() {
		defer close(frames)
		for {
			kind, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if kind != websocket.MessageText {
				continue
			}
			var frame map[string]any
			if decodeErr := json.Unmarshal(data, &frame); decodeErr == nil {
				select {
				case frames <- frame:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return frames
}

// startCodingIssueSession delegates a new issue to the Flow agent and starts
// its coding session.
func startCodingIssueSession(e *codingE2E, title string) string {
	e.t.Helper()
	issue := e.createIssue(title)
	agentUser := e.s.builtinAgentUserID(context.Background(), "test-workspace")
	requestJSON[domain.Issue](e.t, e.handler, http.MethodPatch, "/api/issue-records/"+issue.ID, map[string]any{"delegateId": agentUser}, http.StatusOK)
	var started coding.TriggerResult
	e.decode(e.do(http.MethodPost, "/api/issues/"+issue.ID+"/coding-session", map[string]any{}, "", nil), http.StatusCreated, &started)
	if started.Session == nil {
		e.t.Fatalf("started: %+v", started)
	}
	return started.Session.ID
}

// TestCodingSessionRestoresGrantedSnapshot covers sessions that start from an
// environment snapshot, over the session executor, the real sandbox
// reconciler, token issuance and the archive route: a fresh session sandbox
// (restore step with FLOW_ARCHIVE_URL) and a warm sandbox bound to the
// session (its runner restores after bootstrap).
func TestCodingSessionRestoresGrantedSnapshot(t *testing.T) {
	e := newCodingE2E(t)
	ctx := context.Background()
	e.installGitHubApp()
	environment, err := e.s.codingStore().CreateCodingEnvironment(ctx, domain.CodingEnvironment{
		WorkspaceKey: "test-workspace", Name: "Web", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/web", DefaultBranch: "main", SandboxSize: domain.CodingSizeMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	names, err := e.repo.CodingEnvironmentSecretNames(ctx, "test-workspace", environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	hash := coding.SnapshotConfigHash(environment, e.s.coding.config.Image, coding.SnapshotBaseRef(environment), names)
	archive, head := codingTestArchive(t, "session")
	otherArchive, _ := codingTestArchive(t, "stale")
	other := codingTestReadySnapshot(t, e.s, "test-workspace", environment, otherArchive, "stale-config", time.Now().Add(-2*time.Hour))
	snapshot := codingTestReadySnapshot(t, e.s, "test-workspace", environment, archive, hash, time.Now().Add(-time.Hour))
	server := httptest.NewServer(e.handler)
	defer server.Close()

	checkGrant := func(sandboxID, token string) {
		t.Helper()
		record, err := e.s.codingStore().CodingSandbox(ctx, sandboxID)
		if err != nil || record.SnapshotID != snapshot.ID {
			t.Fatalf("sandbox record snapshot = %q (%v), want %s", record.SnapshotID, err, snapshot.ID)
		}
		grant, err := e.s.codingStore().AuthenticateSandboxToken(ctx, token)
		if err != nil || grant.SnapshotID != snapshot.ID || grant.SessionID == "" {
			t.Fatalf("grant: %+v %v", grant, err)
		}
		status, body := codingTestGetArchive(t, server.URL, sandbox.ArchivePath(snapshot.ID), token)
		if status != http.StatusOK || !bytes.Equal(body, archive) {
			t.Fatalf("granted archive: %d (%d bytes)", status, len(body))
		}
		if restored := codingTestRestoredHead(t, body); restored != head {
			t.Fatalf("restored HEAD %s, want %s", restored, head)
		}
		if status, _ := codingTestGetArchive(t, server.URL, sandbox.ArchivePath(other.ID), token); status != http.StatusForbidden {
			t.Fatalf("an ungranted snapshot: %d, want 403", status)
		}
	}

	// A fresh session sandbox restores through FLOW_ARCHIVE_URL before the runner starts.
	first := e.drive(startCodingIssueSession(e, "Restore from the snapshot"), domain.CodingPhaseStarting)
	if first.SnapshotID != snapshot.ID {
		t.Fatalf("session snapshot = %q, want %s", first.SnapshotID, snapshot.ID)
	}
	spec, ok := e.provider.Spec(first.SandboxID)
	if !ok || spec.SnapshotID != snapshot.ID || !strings.HasSuffix(spec.Workspace.ArchiveURL, sandbox.ArchivePath(snapshot.ID)) {
		t.Fatalf("session sandbox spec: snapshot=%q archive=%q", spec.SnapshotID, spec.Workspace.ArchiveURL)
	}
	firstToken := e.provider.token(first.SandboxID)
	status, body := codingTestGetArchive(t, server.URL, spec.Workspace.ArchiveURL, firstToken)
	if status != http.StatusOK || !bytes.Equal(body, archive) {
		t.Fatalf("FLOW_ARCHIVE_URL download: %d", status)
	}
	checkGrant(first.SandboxID, firstToken)
	var boot coding.BootstrapResponse
	e.decode(e.do(http.MethodGet, "/internal/coding-runtime/bootstrap", nil, firstToken, nil), http.StatusOK, &boot)
	if boot.SnapshotID != snapshot.ID || !boot.Restored {
		t.Fatalf("a restored sandbox skips setup: snapshot=%q restored=%v", boot.SnapshotID, boot.Restored)
	}

	// A warm sandbox bound to the next session: the bind records the
	// snapshot, and bootstrap tells its runner to restore it.
	e.s.coding.config.WarmPool = map[string]int{domain.CodingSizeMedium: 1}
	e.s.coding.config.WarmPoolMaxAge = time.Hour
	pool := &sandbox.WarmPool{Store: e.s.codingStore(), Config: e.s.coding.config}
	if err := pool.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sandboxes := &sandbox.Reconciler{Store: e.s.codingStore(), Provider: e.s.coding.sandbox, Config: e.s.coding.config, Owner: "warm"}
	if _, err := sandboxes.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if counts, _ := e.repo.WarmSandboxes(ctx); counts[sandbox.WarmKey(domain.CodingSizeMedium, "")] != 1 {
		t.Fatalf("warm sandboxes: %v", counts)
	}
	second := e.drive(startCodingIssueSession(e, "Restore in a warm sandbox"), domain.CodingPhaseStarting)
	warmSpec, ok := e.provider.Spec(second.SandboxID)
	if !ok || warmSpec.Kind != sandbox.KindWarm || warmSpec.Workspace.ArchiveURL != "" || second.SnapshotID != snapshot.ID {
		t.Fatalf("the session is bound to the warm sandbox: ok=%v kind=%s snapshot=%q", ok, warmSpec.Kind, second.SnapshotID)
	}
	warmToken := e.provider.token(second.SandboxID)
	checkGrant(second.SandboxID, warmToken)
	var warmBoot coding.BootstrapResponse
	e.decode(e.do(http.MethodGet, "/internal/coding-runtime/bootstrap", nil, warmToken, nil), http.StatusOK, &warmBoot)
	if warmBoot.SnapshotID != snapshot.ID || warmBoot.Restored {
		t.Fatalf("the warm runner must restore the snapshot itself: snapshot=%q restored=%v", warmBoot.SnapshotID, warmBoot.Restored)
	}
}
