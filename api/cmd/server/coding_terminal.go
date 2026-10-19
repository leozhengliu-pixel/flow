package main

// Debug terminal (docs/coding-sessions-design.md §18). Owner: track K.
//
// GET /api/coding/environments/{id}/terminal upgrades to a WebSocket, starts
// a temporary `debug` sandbox restored from the environment's latest ready
// snapshot (or a fresh clone), waits for it, and bridges the socket to
// Provider.Exec with a TTY running `bash -l`:
//
//   - binary frames carry TTY bytes both ways;
//   - text frames from the client are JSON: {"type":"resize","cols":N,"rows":N}
//     and {"type":"runPrepare"} (also accepted: "runPrepareScript", and
//     {"type":"input","data":"…"});
//   - text frames from the server are JSON:
//     {"type":"progress"|"status","message":"…"} while starting or preparing,
//     {"type":"ready","expiresAt":"…"} once the TTY is attached,
//     {"type":"error","message":"…"}, and {"type":"exit"} when the shell
//     exits or the TTL expires ("code" carries the shell exit code).
//
// The sandbox lives at most FLOW_CODING_DEBUG_TERMINAL_TTL (20 minutes, also
// its activeDeadlineSeconds) and is stopped when the socket closes. Setup
// secrets are never injected; "Run prepare script" runs the script with the
// secrets inside the same sandbox, after which the user can read them (the UI
// warns). Opening the terminal and running the prepare script are audited.
// Runtime is billed to the user like any sandbox (the sandbox reconciler for
// coding_sandboxes rows, the launcher otherwise).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
)

const (
	codingTerminalDefaultTTL  = 20 * time.Minute
	codingTerminalPoll        = time.Second
	codingTerminalFrameLimit  = 64 << 10
	codingTerminalStartWindow = 10 * time.Minute
)

type codingTerminalMessage struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Data string `json:"data,omitempty"`
}

type codingTerminalStatus struct {
	Type      string     `json:"type"` // progress | status | ready | error | exit
	Message   string     `json:"message,omitempty"`
	SandboxID string     `json:"sandboxId,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	ExitCode  *int       `json:"code,omitempty"`
	Restored  *bool      `json:"restored,omitempty"`
}

// codingTerminalSocket serializes writes to the WebSocket.
type codingTerminalSocket struct {
	mu   sync.Mutex
	conn *websocket.Conn
	ctx  context.Context
}

func (socket *codingTerminalSocket) status(status codingTerminalStatus) {
	encoded, _ := json.Marshal(status)
	socket.mu.Lock()
	defer socket.mu.Unlock()
	_ = socket.conn.Write(socket.ctx, websocket.MessageText, encoded)
}

// Write sends TTY output as a binary frame (io.Writer for Exec's Stdout).
func (socket *codingTerminalSocket) Write(data []byte) (int, error) {
	socket.mu.Lock()
	defer socket.mu.Unlock()
	if err := socket.conn.Write(socket.ctx, websocket.MessageBinary, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (s *server) codingEnvironmentTerminal(w http.ResponseWriter, r *http.Request) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	if !s.codingEnabled() || !s.coding.config.DebugTerminal {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The debug terminal is turned off on this server", "code": string(domain.CodingErrSandboxDisabled)})
		return
	}
	if capabilities := s.coding.sandbox.Capabilities(); !capabilities.Exec || !capabilities.TTY {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "The sandbox provider does not support terminals", "code": string(domain.CodingErrSandboxDisabled)})
		return
	}
	if environment.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "Unarchive the environment first")
		return
	}
	launcher := s.codingEnvironmentLauncher()
	if launcher == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Sandboxes are not available", "code": string(domain.CodingErrSandboxDisabled)})
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(codingTerminalFrameLimit)
	ttl := s.coding.config.Timeouts.DebugTerminal
	if ttl <= 0 {
		ttl = codingTerminalDefaultTTL
	}
	expiresAt := time.Now().Add(ttl).UTC()
	ctx, cancel := context.WithDeadline(context.Background(), expiresAt)
	defer cancel()
	// Socket I/O must outlive the TTL context: coder/websocket closes the
	// connection when a Read's context ends, and the "exit" frame is sent
	// after the deadline.
	connCtx, connCancel := context.WithCancel(context.Background())
	defer connCancel()
	socket := &codingTerminalSocket{conn: conn, ctx: connCtx}
	closeReason := "terminal closed"
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, closeReason) }()

	sandboxID := coding.NewResourceID(time.Now())
	workspaceSource, restored := s.codingTerminalWorkspace(ctx, request.workspace, environment, sandboxID)
	launch := coding.LaunchRequest{
		ID: sandboxID, WorkspaceKey: request.workspace, Kind: sandbox.KindDebug, EnvironmentID: environment.ID, UserID: request.user.ID,
		Size: environment.SandboxSize, Workspace: workspaceSource, Deadline: ttl, Env: map[string]string{sandbox.EnvMode: coding.ModeDebug},
	}
	socket.status(codingTerminalStatus{Type: "progress", Message: "Starting the terminal sandbox", SandboxID: sandboxID, Restored: &restored})
	if err := launcher.Launch(ctx, launch); err != nil {
		log.Printf("coding terminal %s: launch: %v", environment.ID, err)
		socket.status(codingTerminalStatus{Type: "error", Message: "The terminal sandbox could not be started"})
		closeReason = "start failed"
		return
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), time.Minute)
		defer releaseCancel()
		if err := launcher.Release(releaseCtx, request.workspace, sandboxID); err != nil {
			log.Printf("coding terminal %s: release sandbox %s: %v", environment.ID, sandboxID, err)
		}
		s.auditCodingEnvironment(r, request.workspace, "coding_environment.terminal_closed", environment.ID, map[string]any{"sandboxId": sandboxID})
	}()
	s.auditCodingEnvironment(r, request.workspace, "coding_environment.terminal_opened", environment.ID, map[string]any{"sandboxId": sandboxID, "restored": restored})

	// The reader runs from the start so a client close stops the wait.
	stdinReader, stdinWriter := io.Pipe()
	resize := make(chan sandbox.TerminalSize, 4)
	prepare := make(chan struct{}, 1)
	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		defer stdinWriter.Close()
		for {
			kind, data, err := conn.Read(connCtx)
			if err != nil {
				return
			}
			if kind == websocket.MessageBinary {
				if _, err := stdinWriter.Write(data); err != nil {
					return
				}
				continue
			}
			var message codingTerminalMessage
			if json.Unmarshal(data, &message) != nil {
				continue
			}
			switch message.Type {
			case "resize":
				if message.Cols > 0 && message.Rows > 0 {
					select {
					case resize <- sandbox.TerminalSize{Width: message.Cols, Height: message.Rows}:
					default: // drop a burst; the next resize wins
					}
				}
			case "input":
				if _, err := stdinWriter.Write([]byte(message.Data)); err != nil {
					return
				}
			case "runPrepare", "runPrepareScript":
				select {
				case prepare <- struct{}{}:
				default:
				}
			}
		}
	}()

	if err := s.waitForCodingTerminal(ctx, launcher, request.workspace, sandboxID, clientGone); err != nil {
		if ctx.Err() == nil {
			socket.status(codingTerminalStatus{Type: "error", Message: err.Error()})
		}
		closeReason = "start failed"
		return
	}
	socket.status(codingTerminalStatus{Type: "ready", SandboxID: sandboxID, ExpiresAt: &expiresAt})

	execDone := make(chan error, 1)
	var exitCode int
	go func() {
		result, err := s.coding.sandbox.Exec(ctx, sandboxID, sandbox.ExecRequest{Command: []string{"bash", "-l"}, TTY: true, Stdin: stdinReader, Stdout: socket, Resize: resize})
		exitCode = result.ExitCode
		execDone <- err
	}()
	for {
		select {
		case <-prepare:
			go s.runCodingTerminalPrepareScript(ctx, r, request, environment, sandboxID, socket)
		case err := <-execDone:
			if err != nil && ctx.Err() == nil {
				log.Printf("coding terminal %s: exec: %v", environment.ID, err)
				socket.status(codingTerminalStatus{Type: "error", Message: "The shell stopped unexpectedly"})
			} else if ctx.Err() == nil {
				code := exitCode
				socket.status(codingTerminalStatus{Type: "exit", ExitCode: &code})
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.codingTerminalExpired(socket, &closeReason)
			}
			return
		case <-clientGone:
			cancel()
			<-execDone
			return
		case <-ctx.Done():
			s.codingTerminalExpired(socket, &closeReason)
			_ = stdinReader.Close()
			return
		}
	}
}

func (s *server) codingTerminalExpired(socket *codingTerminalSocket, reason *string) {
	socket.status(codingTerminalStatus{Type: "exit", Message: "The terminal reached its time limit"})
	*reason = "terminal expired"
}

// codingTerminalWorkspace restores the newest ready snapshot when there is one.
func (s *server) codingTerminalWorkspace(ctx context.Context, workspace string, environment domain.CodingEnvironment, sandboxID string) (sandbox.VolumeSource, bool) {
	strategy := s.codingSnapshotStrategy()
	if strategy == nil {
		return sandbox.VolumeSource{}, false
	}
	snapshot, err := s.store.LatestUsableCodingSnapshot(ctx, workspace, environment.ID)
	if err != nil {
		return sandbox.VolumeSource{}, false
	}
	source, err := strategy.Restore(ctx, snapshot.StorageRef, sandboxID, workspace)
	if err != nil {
		log.Printf("coding terminal %s: restore snapshot %s: %v (using a fresh clone)", environment.ID, snapshot.ID, err)
		return sandbox.VolumeSource{}, false
	}
	return source, true
}

func (s *server) waitForCodingTerminal(ctx context.Context, launcher coding.SandboxLauncher, workspace, sandboxID string, clientGone <-chan struct{}) error {
	window := s.coding.config.Timeouts.Start
	if window <= 0 {
		window = codingTerminalStartWindow
	}
	deadline := time.Now().Add(window)
	ticker := time.NewTicker(codingTerminalPoll)
	defer ticker.Stop()
	for {
		status, err := launcher.Observe(ctx, workspace, sandboxID)
		if err == nil {
			switch status.Phase {
			case sandbox.PhaseRunning:
				return nil
			case sandbox.PhaseFailed, sandbox.PhaseSucceeded:
				return fmt.Errorf("the terminal sandbox stopped (%s)", strings.TrimSpace(string(status.Reason)+" "+status.Message))
			}
		}
		if time.Now().After(deadline) {
			return errors.New("the terminal sandbox did not start in time")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-clientGone:
			return errors.New("the client disconnected")
		case <-ticker.C:
		}
	}
}

// runCodingTerminalPrepareScript runs the environment's prepare script with
// its secrets in the terminal sandbox. The script and the exports go through
// stdin, never the command line (which the API server may log).
func (s *server) runCodingTerminalPrepareScript(ctx context.Context, r *http.Request, request codingEnvironmentRequest, environment domain.CodingEnvironment, sandboxID string, socket *codingTerminalSocket) {
	s.configureCodingSecretKey()
	secrets, err := s.store.CodingEnvironmentSecretValues(ctx, request.workspace, environment.ID)
	if err != nil {
		socket.status(codingTerminalStatus{Type: "error", Message: "The environment's secrets could not be read; delete and add them again"})
		return
	}
	s.auditCodingEnvironment(r, request.workspace, "coding_environment.terminal_prepare_script", environment.ID, map[string]any{"sandboxId": sandboxID, "secrets": len(secrets)})
	script := codingPrepareScript(environment, secrets)
	socket.status(codingTerminalStatus{Type: "status", Message: "Running the prepare script with the environment's secrets"})
	result, err := s.coding.sandbox.Exec(ctx, sandboxID, sandbox.ExecRequest{Command: []string{"bash", "-s"}, Stdin: strings.NewReader(script), Stdout: socket, Stderr: socket})
	if err != nil {
		socket.status(codingTerminalStatus{Type: "error", Message: "The prepare script could not run"})
		return
	}
	code := result.ExitCode
	socket.status(codingTerminalStatus{Type: "status", Message: fmt.Sprintf("The prepare script finished with exit code %d", code), ExitCode: &code})
}

// codingPrepareScript is the shell program fed to `bash -s`: exports of the
// plain variables and secrets, then the prepare script in the repository.
func codingPrepareScript(environment domain.CodingEnvironment, secrets []domain.CodingEnvVar) string {
	var builder strings.Builder
	builder.WriteString("set -e\n")
	for _, variable := range append(append([]domain.CodingEnvVar{}, environment.Mise.EnvironmentVariables...), secrets...) {
		fmt.Fprintf(&builder, "export %s=%s\n", variable.Name, shellQuote(variable.Value))
	}
	builder.WriteString("cd " + sandbox.WorkspacePath + "/repo 2>/dev/null || cd " + sandbox.WorkspacePath + "\n")
	builder.WriteString(environment.PrepareScript)
	builder.WriteString("\n")
	return builder.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
