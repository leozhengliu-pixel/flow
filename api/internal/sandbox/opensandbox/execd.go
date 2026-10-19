package opensandbox

// Client for execd, OpenSandbox's in-sandbox daemon (specs/execd-api.yaml,
// info.version 1.0.0 at release-1.1.0; PTY sessions per
// components/execd/PTY.md). execd is reached through the lifecycle server's
// proxy route or the sandbox's resolved endpoint.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// Execd is a client for one sandbox's execd.
type Execd struct {
	client  *Client
	base    string
	headers map[string]string
}

// RunCommandRequest is POST /command (exactly one of Command and Argv).
type RunCommandRequest struct {
	Command    string            `json:"command,omitempty"`
	Argv       []string          `json:"argv,omitempty"`
	Cwd        string            `json:"cwd,omitempty"`
	Background bool              `json:"background,omitempty"`
	TimeoutMS  int64             `json:"timeout,omitempty"`
	UID        *int              `json:"uid,omitempty"`
	GID        *int              `json:"gid,omitempty"`
	Envs       map[string]string `json:"envs,omitempty"`
}

// StreamEvent is one ServerStreamEvent.
type StreamEvent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	Timestamp int64  `json:"timestamp,omitempty"`
	Error     *struct {
		EName     string   `json:"ename"`
		EValue    string   `json:"evalue"`
		Traceback []string `json:"traceback,omitempty"`
	} `json:"error,omitempty"`
}

// CommandStatus is GET /command/status/{id}.
type CommandStatus struct {
	ID         string     `json:"id"`
	Running    bool       `json:"running"`
	ExitCode   *int       `json:"exit_code"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// CommandResult is the outcome of a foreground command.
type CommandResult struct {
	ID       string
	ExitCode int
	Error    string
}

func (e *Execd) url(p string) string { return e.base + p }

func (e *Execd) request(r request) request {
	r.url = e.url(r.url)
	if len(e.headers) > 0 {
		headers := make(map[string]string, len(e.headers)+len(r.headers))
		for key, value := range e.headers {
			headers[key] = value
		}
		for key, value := range r.headers {
			headers[key] = value
		}
		r.headers = headers
	}
	return r
}

// Ping checks that execd answers.
func (e *Execd) Ping(ctx context.Context) error {
	return e.client.do(ctx, e.request(request{op: "execd ping", method: http.MethodGet, url: "/ping"}))
}

// Upload writes content to path in the sandbox (mode is octal digits, e.g. 600).
func (e *Execd) Upload(ctx context.Context, target string, content []byte, mode int) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadata, _ := json.Marshal(map[string]any{"path": target, "mode": mode})
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="metadata"`)
	header.Set("Content-Type", "application/json")
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(metadata); err != nil {
		return err
	}
	header = textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, path.Base(target)))
	header.Set("Content-Type", "application/octet-stream")
	if part, err = writer.CreatePart(header); err != nil {
		return err
	}
	if _, err := part.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return e.client.do(ctx, e.request(request{op: "execd upload", method: http.MethodPost, url: "/files/upload", raw: body.Bytes(), contentType: writer.FormDataContentType(), retry: true}))
}

// Status returns a command's status.
func (e *Execd) Status(ctx context.Context, id string) (CommandStatus, error) {
	var status CommandStatus
	err := e.client.do(ctx, e.request(request{op: "execd command status", method: http.MethodGet, url: "/command/status/" + url.PathEscape(id), out: &status, retry: true}))
	return status, err
}

// Logs returns a background command's combined output after cursor (nil =
// everything) and the cursor for the next call.
func (e *Execd) Logs(ctx context.Context, id string, cursor *int64) (string, int64, error) {
	query := url.Values{}
	if cursor != nil {
		query.Set("cursor", strconv.FormatInt(*cursor, 10))
	}
	ctx, cancel := context.WithTimeout(ctx, e.client.requestTimeout)
	defer cancel()
	target := e.url("/command/" + url.PathEscape(id) + "/logs")
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", 0, err
	}
	e.header(req.Header)
	resp, err := e.client.http.Do(req)
	if err != nil {
		return "", 0, &APIError{Op: "execd command logs", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, responseError("execd command logs", resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return "", 0, &APIError{Op: "execd command logs", Err: err}
	}
	next := int64(0)
	if cursor != nil {
		next = *cursor
	}
	if value, err := strconv.ParseInt(resp.Header.Get("EXECD-COMMANDS-TAIL-CURSOR"), 10, 64); err == nil {
		next = value
	}
	return string(raw), next, nil
}

func (e *Execd) header(header http.Header) {
	header.Set("User-Agent", userAgent)
	e.client.authorize(header)
	for key, value := range e.headers {
		header.Set(key, value)
	}
}

// Run starts a command and calls onEvent for every stream event. For a
// background command the stream ends after the init event; the result's ID
// is the command id either way.
func (e *Execd) Run(ctx context.Context, run RunCommandRequest, onEvent func(StreamEvent) error) (CommandResult, error) {
	payload, err := json.Marshal(run)
	if err != nil {
		return CommandResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url("/command"), bytes.NewReader(payload))
	if err != nil {
		return CommandResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	e.header(req.Header)
	resp, err := e.client.http.Do(req)
	if err != nil {
		return CommandResult{}, &APIError{Op: "execd run command", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return CommandResult{}, responseError("execd run command", resp)
	}
	result := CommandResult{ExitCode: -1}
	completed, failed := false, false
	err = ReadEvents(resp.Body, func(event StreamEvent) error {
		switch event.Type {
		case "init":
			result.ID = event.Text
			if run.Background {
				// Detached: the id is all Flow needs; do not wait for the
				// stream to end (execd may keep it open).
				return errStopStream
			}
		case "error":
			failed = true
			if event.Error != nil {
				result.Error = strings.TrimSpace(event.Error.EName + ": " + event.Error.EValue)
				if code, err := strconv.Atoi(strings.TrimSpace(event.Error.EValue)); err == nil {
					result.ExitCode = code
				}
			}
		case "execution_complete":
			completed = true
		}
		if onEvent != nil {
			return onEvent(event)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopStream) {
		return result, &APIError{Op: "execd run command", Err: err}
	}
	switch {
	case run.Background:
		if result.ID == "" {
			return result, &APIError{Op: "execd run command", Err: errors.New("background command started without an id")}
		}
	case failed && result.ExitCode < 0:
		result.ExitCode = 1
	case completed && !failed:
		result.ExitCode = 0
	case !completed && !failed:
		return result, &APIError{Op: "execd run command", Err: io.ErrUnexpectedEOF}
	}
	return result, nil
}

// errStopStream ends ReadEvents early without an error.
var errStopStream = errors.New("stop stream")

// ReadEvents parses execd's event stream: either SSE "data:" lines or raw
// JSON objects separated by blank lines (what execd writes).
func ReadEvents(body io.Reader, handle func(StreamEvent) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), math.MaxInt32)
	var data []string
	flush := func() error {
		if len(data) == 0 {
			return nil
		}
		joined := strings.Join(data, "\n")
		data = nil
		var event StreamEvent
		if err := json.Unmarshal([]byte(joined), &event); err != nil {
			// Not JSON: raw stdout, as the official SDKs treat it.
			event = StreamEvent{Type: "stdout", Text: joined}
		}
		return handle(event)
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return err
			}
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "{"):
			if len(data) > 0 {
				if err := flush(); err != nil {
					return err
				}
			}
			data = append(data, line)
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return flush()
}

// PTY frame bytes (components/execd/pkg/web/model/pty_ws.go).
const (
	ptyStdin  byte = 0x00
	ptyStdout byte = 0x01
	ptyStderr byte = 0x02
	ptyReplay byte = 0x03
)

type ptyFrame struct {
	Type     string `json:"type"`
	Cols     int    `json:"cols,omitempty"`
	Rows     int    `json:"rows,omitempty"`
	Signal   string `json:"signal,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Error    string `json:"error,omitempty"`
	Code     string `json:"code,omitempty"`
}

// CreatePTY creates a PTY session that runs `bash -c command` (or a login
// shell when command is empty) once the WebSocket attaches.
func (e *Execd) CreatePTY(ctx context.Context, cwd, command string) (string, error) {
	var resp struct {
		SessionID string `json:"session_id"`
	}
	err := e.client.do(ctx, e.request(request{op: "execd create pty", method: http.MethodPost, url: "/pty", body: map[string]string{"cwd": cwd, "command": command}, out: &resp}))
	if err == nil && resp.SessionID == "" {
		err = &APIError{Op: "execd create pty", Err: errors.New("no session id")}
	}
	return resp.SessionID, err
}

// DeletePTY tears a PTY session down (best effort, missing is fine).
func (e *Execd) DeletePTY(ctx context.Context, id string) error {
	err := e.client.do(ctx, e.request(request{op: "execd delete pty", method: http.MethodDelete, url: "/pty/" + url.PathEscape(id)}))
	if IsNotFound(err) {
		return nil
	}
	return err
}

// TerminalIO is what AttachPTY pumps.
type TerminalIO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Resize <-chan [2]int // cols, rows
}

// AttachPTY attaches to a PTY session over WebSocket and pumps it until the
// shell exits, returning its exit code.
func (e *Execd) AttachPTY(ctx context.Context, id string, terminal TerminalIO) (int, error) {
	target, err := url.Parse(e.url("/pty/" + url.PathEscape(id) + "/ws"))
	if err != nil {
		return -1, err
	}
	switch target.Scheme {
	case "https":
		target.Scheme = "wss"
	default:
		target.Scheme = "ws"
	}
	header := http.Header{}
	e.header(header)
	dialCtx, cancelDial := context.WithTimeout(ctx, e.client.requestTimeout)
	conn, resp, err := websocket.Dial(dialCtx, target.String(), &websocket.DialOptions{HTTPClient: e.client.http, HTTPHeader: header})
	cancelDial()
	if err != nil {
		if resp != nil {
			return -1, &APIError{Op: "execd attach pty", Status: resp.StatusCode, Err: err}
		}
		return -1, &APIError{Op: "execd attach pty", Err: err}
	}
	conn.SetReadLimit(64 << 20)
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if terminal.Stdin != nil {
		go func() {
			buffer := make([]byte, 32<<10)
			for {
				n, err := terminal.Stdin.Read(buffer)
				if n > 0 {
					frame := append([]byte{ptyStdin}, buffer[:n]...)
					if conn.Write(ctx, websocket.MessageBinary, frame) != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
	}
	if terminal.Resize != nil {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case size, ok := <-terminal.Resize:
					if !ok {
						return
					}
					frame, _ := json.Marshal(ptyFrame{Type: "resize", Cols: size[0], Rows: size[1]})
					if conn.Write(ctx, websocket.MessageText, frame) != nil {
						return
					}
				}
			}
		}()
	}
	stdout := terminal.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return -1, ctx.Err()
			}
			return -1, fmt.Errorf("opensandbox pty: %w", err)
		}
		if kind == websocket.MessageBinary {
			if len(data) == 0 {
				continue
			}
			switch data[0] {
			case ptyStdout, ptyStderr:
				if _, err := stdout.Write(data[1:]); err != nil {
					return -1, err
				}
			case ptyReplay:
				if len(data) >= 9 {
					_ = binary.BigEndian.Uint64(data[1:9])
					if _, err := stdout.Write(data[9:]); err != nil {
						return -1, err
					}
				}
			}
			continue
		}
		var frame ptyFrame
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch frame.Type {
		case "exit":
			code := 0
			if frame.ExitCode != nil {
				code = *frame.ExitCode
			}
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return code, nil
		case "error":
			return -1, fmt.Errorf("opensandbox pty: %s %s", frame.Code, frame.Error)
		}
	}
}
