package opensandbox

// A fake OpenSandbox server for tests, implementing the subset of
// specs/sandbox-lifecycle.yml (0.1.0) and specs/execd-api.yaml (1.0.0) at
// release-1.1.0 that the provider uses, plus the execd PTY WebSocket
// (components/execd/PTY.md). Validation follows the spec's documented
// rules so request-shape regressions fail here first.

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type fakeCommand struct {
	ID         string
	Request    RunCommandRequest
	Running    bool
	ExitCode   *int
	Error      string
	Output     []string // log lines
	StartedAt  time.Time
	FinishedAt *time.Time
}

type fakeSandbox struct {
	Sandbox
	Request  CreateRequest
	Files    map[string][]byte
	Modes    map[string]int
	Commands map[string]*fakeCommand
	Order    []string // command ids in launch order
	PTYs     map[string]string
	// PingFailures makes the next n execd pings fail with 503.
	PingFailures int
}

// clone deep-copies what tests assert on (call with f.mu held).
func (sb *fakeSandbox) clone() *fakeSandbox {
	c := *sb
	c.Metadata = map[string]string{}
	for k, v := range sb.Metadata {
		c.Metadata[k] = v
	}
	if sb.ExpiresAt != nil {
		expires := *sb.ExpiresAt
		c.ExpiresAt = &expires
	}
	c.Files, c.Modes, c.Commands, c.PTYs = map[string][]byte{}, map[string]int{}, map[string]*fakeCommand{}, map[string]string{}
	for k, v := range sb.Files {
		c.Files[k] = append([]byte(nil), v...)
	}
	for k, v := range sb.Modes {
		c.Modes[k] = v
	}
	for k, v := range sb.Commands {
		cmd := *v
		c.Commands[k] = &cmd
	}
	for k, v := range sb.PTYs {
		c.PTYs[k] = v
	}
	c.Order = append([]string(nil), sb.Order...)
	return &c
}

func (f *fakeServer) setForeground(fn func(sb *fakeSandbox, req RunCommandRequest) ([]string, []string, int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.foreground = fn
}

func (f *fakeServer) renewalsCopy() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.renewals...)
}

type fakeError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter int
}

type fakeServer struct {
	mu        sync.Mutex
	srv       *httptest.Server
	apiKey    string
	now       func() time.Time
	sandboxes map[string]*fakeSandbox
	nextID    int
	nextCmd   int
	requests  []string // "METHOD path"
	headers   []http.Header
	// createHook may fail a create (return non-nil) or block it.
	createHook func(req CreateRequest) *fakeError
	// failNext fails the next n requests matching a path prefix.
	failNext map[string][]fakeError
	// foreground decides the outcome of foreground commands; nil = defaults.
	foreground func(sb *fakeSandbox, req RunCommandRequest) (stdout, stderr []string, exit int)
	// deleteToStopping keeps deleted sandboxes in Stopping until the next GET.
	deleteToStopping bool
	renewals         []time.Time
	resizes          [][2]int
	slow             time.Duration
}

func newFakeServer(apiKey string, now func() time.Time) *fakeServer {
	f := &fakeServer{apiKey: apiKey, now: now, sandboxes: map[string]*fakeSandbox{}, failNext: map[string][]fakeError{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

func (f *fakeServer) Close() { f.srv.Close() }

func (f *fakeServer) URL() string { return f.srv.URL }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, e fakeError) {
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfter))
	}
	w.Header().Set(HeaderRequestID, "req-test")
	writeJSON(w, e.Status, map[string]string{"code": e.Code, "message": e.Message})
}

// sandbox returns a copy of a stored sandbox (for assertions).
func (f *fakeServer) sandbox(id string) *fakeSandbox {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sandboxes[id]
}

func (f *fakeServer) list() []*fakeSandbox {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*fakeSandbox{}
	for _, sb := range f.sandboxes {
		out = append(out, sb.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (f *fakeServer) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeServer) exact(request string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r == request {
			n++
		}
	}
	return n
}

// seed stores a sandbox directly.
func (f *fakeServer) seed(sb Sandbox) *fakeSandbox {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sb.ID == "" {
		f.nextID++
		sb.ID = fmt.Sprintf("sbx-%03d", f.nextID)
	}
	stored := &fakeSandbox{Sandbox: sb, Files: map[string][]byte{}, Modes: map[string]int{}, Commands: map[string]*fakeCommand{}, PTYs: map[string]string{}}
	f.sandboxes[sb.ID] = stored
	return stored
}

// runner returns the launched background command of a sandbox.
func (f *fakeServer) runner(id string) *fakeCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	sb := f.sandboxes[id]
	if sb == nil {
		return nil
	}
	for _, cid := range sb.Order {
		if cmd := sb.Commands[cid]; cmd.Request.Background {
			copy := *cmd
			return &copy
		}
	}
	return nil
}

// finishRunner ends a sandbox's runner with an exit code.
func (f *fakeServer) finishRunner(id string, code int, lines ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, cid := range f.sandboxes[id].Order {
		cmd := f.sandboxes[id].Commands[cid]
		if cmd.Request.Background {
			cmd.Output = append(cmd.Output, lines...)
			cmd.Running = false
			cmd.ExitCode = &code
			now := f.now()
			cmd.FinishedAt = &now
		}
	}
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.headers = append(f.headers, r.Header.Clone())
	slow := f.slow
	for prefix, failures := range f.failNext {
		if strings.HasPrefix(r.Method+" "+r.URL.Path, prefix) && len(failures) > 0 {
			f.failNext[prefix] = failures[1:]
			f.mu.Unlock()
			writeError(w, failures[0])
			return
		}
	}
	f.mu.Unlock()
	if slow > 0 {
		select {
		case <-time.After(slow):
		case <-r.Context().Done():
			return
		}
	}
	p := r.URL.Path
	if p == "/health" {
		writeJSON(w, 200, map[string]string{"status": "healthy"})
		return
	}
	if strings.HasPrefix(p, "/execd/") {
		// Direct endpoint access: /execd/{id}/...
		rest := strings.TrimPrefix(p, "/execd/")
		id, sub, _ := strings.Cut(rest, "/")
		f.execd(w, r, id, "/"+sub)
		return
	}
	if !strings.HasPrefix(p, "/v1/") {
		writeError(w, fakeError{Status: 404, Code: "NOT_FOUND", Message: p})
		return
	}
	p = strings.TrimPrefix(p, "/v1")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	// Proxy routes are unauthenticated in single-tenant mode (server/opensandbox_server/middleware/auth.py).
	if len(parts) >= 4 && parts[0] == "sandboxes" && parts[2] == "proxy" {
		if parts[3] != strconv.Itoa(DefaultExecdPort) {
			writeError(w, fakeError{Status: 502, Code: "PROXY", Message: "no backend"})
			return
		}
		f.execd(w, r, parts[1], "/"+strings.Join(parts[4:], "/"))
		return
	}
	if f.apiKey != "" && r.Header.Get(HeaderAPIKey) != f.apiKey {
		writeError(w, fakeError{Status: 401, Code: "INVALID_API_KEY", Message: "Authentication credentials are missing"})
		return
	}
	switch {
	case p == "/sandboxes" && r.Method == http.MethodPost:
		f.create(w, r)
	case p == "/sandboxes" && r.Method == http.MethodGet:
		f.listHandler(w, r)
	case len(parts) == 2 && parts[0] == "sandboxes" && r.Method == http.MethodGet:
		f.get(w, parts[1])
	case len(parts) == 2 && parts[0] == "sandboxes" && r.Method == http.MethodDelete:
		f.delete(w, parts[1])
	case len(parts) == 3 && parts[2] == "renew-expiration" && r.Method == http.MethodPost:
		f.renew(w, r, parts[1])
	case len(parts) == 3 && parts[2] == "metadata" && r.Method == http.MethodPatch:
		f.patch(w, r, parts[1])
	case len(parts) == 4 && parts[2] == "endpoints" && r.Method == http.MethodGet:
		f.mu.Lock()
		_, ok := f.sandboxes[parts[1]]
		f.mu.Unlock()
		if !ok {
			writeError(w, fakeError{Status: 404, Code: "KUBERNETES::SANDBOX_NOT_FOUND", Message: "not found"})
			return
		}
		host := strings.TrimPrefix(f.srv.URL, "http://")
		writeJSON(w, 200, Endpoint{Endpoint: host + "/execd/" + parts[1], Headers: map[string]string{"X-Endpoint-Route": parts[1]}})
	default:
		writeError(w, fakeError{Status: 404, Code: "NOT_FOUND", Message: r.Method + " " + p})
	}
}

func (f *fakeServer) create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, fakeError{Status: 400, Code: "SANDBOX::INVALID_PARAMETER", Message: err.Error()})
		return
	}
	pool := req.Extensions["poolRef"]
	switch {
	case pool == "" && (req.Image == nil || req.Image.URI == ""):
		writeError(w, fakeError{Status: 400, Code: "SANDBOX::INVALID_PARAMETER", Message: "image is required"})
		return
	case req.Image != nil && len(req.Entrypoint) == 0:
		writeError(w, fakeError{Status: 400, Code: "SANDBOX::INVALID_PARAMETER", Message: "entrypoint is required with image"})
		return
	case pool == "" && len(req.ResourceLimits) == 0:
		writeError(w, fakeError{Status: 400, Code: "SANDBOX::INVALID_PARAMETER", Message: "resourceLimits is required"})
		return
	case pool != "" && req.NetworkPolicy != nil:
		writeError(w, fakeError{Status: 400, Code: "SANDBOX::INVALID_PARAMETER", Message: "networkPolicy with poolRef"})
		return
	case req.Timeout != nil && *req.Timeout < 60:
		writeError(w, fakeError{Status: 400, Code: "SANDBOX::INVALID_PARAMETER", Message: "timeout must be >= 60"})
		return
	}
	for key, value := range req.Metadata {
		if strings.HasPrefix(key, "opensandbox.io/") || len(value) > 63 {
			writeError(w, fakeError{Status: 400, Code: "SANDBOX::INVALID_METADATA_LABEL", Message: key})
			return
		}
	}
	f.mu.Lock()
	hook := f.createHook
	f.mu.Unlock()
	if hook != nil {
		if e := hook(req); e != nil {
			writeError(w, *e)
			return
		}
	}
	f.mu.Lock()
	f.nextID++
	now := f.now()
	sb := Sandbox{ID: fmt.Sprintf("sbx-%03d", f.nextID), Status: Status{State: StateRunning, Reason: "POD_READY_WITH_IP"}, Metadata: req.Metadata, Entrypoint: req.Entrypoint, CreatedAt: now, Image: req.Image}
	if req.Timeout != nil {
		expires := now.Add(time.Duration(*req.Timeout) * time.Second)
		sb.ExpiresAt = &expires
	}
	stored := &fakeSandbox{Sandbox: sb, Request: req, Files: map[string][]byte{}, Modes: map[string]int{}, Commands: map[string]*fakeCommand{}, PTYs: map[string]string{}}
	f.sandboxes[sb.ID] = stored
	f.mu.Unlock()
	w.Header().Set("Location", "/v1/sandboxes/"+sb.ID)
	writeJSON(w, http.StatusAccepted, sb)
}

func (f *fakeServer) listHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter, err := url.ParseQuery(query.Get("metadata"))
	if err != nil {
		writeError(w, fakeError{Status: 400, Code: "INVALID", Message: err.Error()})
		return
	}
	page, _ := strconv.Atoi(query.Get("page"))
	size, _ := strconv.Atoi(query.Get("pageSize"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	states := query["state"]
	f.mu.Lock()
	matches := []Sandbox{}
	for _, sb := range f.sandboxes {
		ok := true
		for key := range filter {
			if sb.Metadata[key] != filter.Get(key) {
				ok = false
			}
		}
		if len(states) > 0 && !contains(states, sb.Status.State) {
			ok = false
		}
		if ok {
			matches = append(matches, sb.Sandbox)
		}
	}
	f.mu.Unlock()
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
	start := min((page-1)*size, len(matches))
	end := min(start+size, len(matches))
	pages := (len(matches) + size - 1) / size
	writeJSON(w, 200, listResponse{Items: matches[start:end], Pagination: pagination{Page: page, PageSize: size, TotalItems: len(matches), TotalPages: pages, HasNextPage: page < pages}})
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (f *fakeServer) get(w http.ResponseWriter, id string) {
	f.mu.Lock()
	sb, ok := f.sandboxes[id]
	var copy Sandbox
	if ok {
		copy = sb.Sandbox
	}
	f.mu.Unlock()
	if !ok {
		writeError(w, fakeError{Status: 404, Code: "KUBERNETES::SANDBOX_NOT_FOUND", Message: "sandbox " + id + " not found"})
		return
	}
	writeJSON(w, 200, copy)
}

func (f *fakeServer) delete(w http.ResponseWriter, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sb, ok := f.sandboxes[id]
	if !ok {
		writeError(w, fakeError{Status: 404, Code: "KUBERNETES::SANDBOX_NOT_FOUND", Message: "not found"})
		return
	}
	if f.deleteToStopping && sb.Status.State != StateStopping {
		sb.Status = Status{State: StateStopping, Reason: "user_delete"}
	} else {
		delete(f.sandboxes, id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeServer) renew(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, fakeError{Status: 400, Code: "INVALID", Message: err.Error()})
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sb, ok := f.sandboxes[id]
	if !ok {
		writeError(w, fakeError{Status: 404, Code: "KUBERNETES::SANDBOX_NOT_FOUND", Message: "not found"})
		return
	}
	if !body.ExpiresAt.After(f.now()) || (sb.ExpiresAt != nil && !body.ExpiresAt.After(*sb.ExpiresAt)) {
		writeError(w, fakeError{Status: 400, Code: "KUBERNETES::INVALID_EXPIRATION", Message: "must be in the future and after the current expiresAt"})
		return
	}
	expires := body.ExpiresAt
	sb.ExpiresAt = &expires
	f.renewals = append(f.renewals, expires)
	writeJSON(w, 200, map[string]time.Time{"expiresAt": expires})
}

func (f *fakeServer) patch(w http.ResponseWriter, r *http.Request, id string) {
	var patch map[string]*string
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, fakeError{Status: 400, Code: "INVALID", Message: err.Error()})
		return
	}
	f.mu.Lock()
	sb, ok := f.sandboxes[id]
	if !ok {
		f.mu.Unlock()
		writeError(w, fakeError{Status: 404, Code: "KUBERNETES::SANDBOX_NOT_FOUND", Message: "not found"})
		return
	}
	metadata := map[string]string{}
	for key, value := range sb.Metadata {
		metadata[key] = value
	}
	for key, value := range patch {
		if strings.HasPrefix(key, "opensandbox.io/") {
			f.mu.Unlock()
			writeError(w, fakeError{Status: 400, Code: "SANDBOX::INVALID_METADATA_LABEL", Message: key})
			return
		}
		if value == nil {
			delete(metadata, key)
		} else {
			metadata[key] = *value
		}
	}
	sb.Metadata = metadata
	copy := sb.Sandbox
	f.mu.Unlock()
	writeJSON(w, 200, copy)
}

// execd emulates one sandbox's execd.
func (f *fakeServer) execd(w http.ResponseWriter, r *http.Request, id, path string) {
	f.mu.Lock()
	sb, ok := f.sandboxes[id]
	if ok && path == "/ping" && sb.PingFailures > 0 {
		sb.PingFailures--
		f.mu.Unlock()
		writeError(w, fakeError{Status: 503, Code: "NOT_READY", Message: "execd starting"})
		return
	}
	f.mu.Unlock()
	if !ok {
		writeError(w, fakeError{Status: 404, Code: "SANDBOX_NOT_FOUND", Message: id})
		return
	}
	switch {
	case path == "/ping":
		w.WriteHeader(200)
	case path == "/files/upload" && r.Method == http.MethodPost:
		f.upload(w, r, sb)
	case path == "/command" && r.Method == http.MethodPost:
		f.command(w, r, sb)
	case strings.HasPrefix(path, "/command/status/"):
		f.mu.Lock()
		cmd := sb.Commands[strings.TrimPrefix(path, "/command/status/")]
		var status CommandStatus
		if cmd != nil {
			started := cmd.StartedAt
			status = CommandStatus{ID: cmd.ID, Running: cmd.Running, ExitCode: cmd.ExitCode, Error: cmd.Error, StartedAt: &started, FinishedAt: cmd.FinishedAt}
		}
		f.mu.Unlock()
		if cmd == nil {
			writeError(w, fakeError{Status: 404, Code: "NOT_FOUND", Message: "command not found"})
			return
		}
		writeJSON(w, 200, status)
	case strings.HasPrefix(path, "/command/") && strings.HasSuffix(path, "/logs"):
		cid := strings.TrimSuffix(strings.TrimPrefix(path, "/command/"), "/logs")
		f.mu.Lock()
		cmd := sb.Commands[cid]
		var lines []string
		if cmd != nil {
			lines = append(lines, cmd.Output...)
		}
		f.mu.Unlock()
		if cmd == nil {
			writeError(w, fakeError{Status: 404, Code: "NOT_FOUND", Message: "command not found"})
			return
		}
		start := 0
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			cursor, _ := strconv.Atoi(raw)
			start = min(cursor+1, len(lines))
		}
		w.Header().Set("EXECD-COMMANDS-TAIL-CURSOR", strconv.Itoa(len(lines)-1))
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		for _, line := range lines[start:] {
			_, _ = io.WriteString(w, line+"\n")
		}
	case path == "/pty" && r.Method == http.MethodPost:
		var body struct {
			Cwd     string `json:"cwd"`
			Command string `json:"command"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.nextCmd++
		pid := fmt.Sprintf("pty-%d", f.nextCmd)
		sb.PTYs[pid] = body.Command
		f.mu.Unlock()
		writeJSON(w, 200, map[string]string{"session_id": pid})
	case strings.HasPrefix(path, "/pty/") && strings.HasSuffix(path, "/ws"):
		f.ptyWS(w, r, sb, strings.TrimSuffix(strings.TrimPrefix(path, "/pty/"), "/ws"))
	case strings.HasPrefix(path, "/pty/") && r.Method == http.MethodDelete:
		f.mu.Lock()
		delete(sb.PTYs, strings.TrimPrefix(path, "/pty/"))
		f.mu.Unlock()
		w.WriteHeader(200)
	default:
		writeError(w, fakeError{Status: 404, Code: "NOT_FOUND", Message: path})
	}
}

func (f *fakeServer) upload(w http.ResponseWriter, r *http.Request, sb *fakeSandbox) {
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, fakeError{Status: 400, Code: "INVALID", Message: err.Error()})
		return
	}
	var meta struct {
		Path string `json:"path"`
		Mode int    `json:"mode"`
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, fakeError{Status: 400, Code: "INVALID", Message: err.Error()})
			return
		}
		switch part.FormName() {
		case "metadata":
			if err := json.NewDecoder(part).Decode(&meta); err != nil {
				writeError(w, fakeError{Status: 400, Code: "INVALID", Message: "metadata: " + err.Error()})
				return
			}
		case "file":
			if meta.Path == "" {
				writeError(w, fakeError{Status: 400, Code: "INVALID", Message: "metadata part must precede the file"})
				return
			}
			content, _ := io.ReadAll(part)
			f.mu.Lock()
			sb.Files[meta.Path] = content
			sb.Modes[meta.Path] = meta.Mode
			f.mu.Unlock()
		}
	}
	w.WriteHeader(200)
}

var _ = multipart.ErrMessageTooLarge

func writeEvent(w http.ResponseWriter, event StreamEvent) {
	raw, _ := json.Marshal(event)
	_, _ = w.Write(append(raw, '\n', '\n'))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (f *fakeServer) command(w http.ResponseWriter, r *http.Request, sb *fakeSandbox) {
	var req RunCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Command == "") == (len(req.Argv) == 0) {
		writeError(w, fakeError{Status: 400, Code: "INVALID_REQUEST_BODY", Message: "exactly one of command and argv"})
		return
	}
	if req.GID != nil && req.UID == nil {
		writeError(w, fakeError{Status: 400, Code: "INVALID_REQUEST_BODY", Message: "gid requires uid"})
		return
	}
	f.mu.Lock()
	f.nextCmd++
	cmd := &fakeCommand{ID: fmt.Sprintf("cmd-%d", f.nextCmd), Request: req, Running: req.Background, StartedAt: f.now()}
	sb.Commands[cmd.ID] = cmd
	sb.Order = append(sb.Order, cmd.ID)
	foreground := f.foreground
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(200)
	writeEvent(w, StreamEvent{Type: "init", Text: cmd.ID})
	if req.Background {
		return
	}
	stdout, stderr, exit := f.defaultForeground(sb, req)
	if foreground != nil {
		stdout, stderr, exit = foreground(sb, req)
	}
	for _, line := range stdout {
		writeEvent(w, StreamEvent{Type: "stdout", Text: line})
	}
	for _, line := range stderr {
		writeEvent(w, StreamEvent{Type: "stderr", Text: line})
	}
	f.mu.Lock()
	cmd.ExitCode = &exit
	f.mu.Unlock()
	if exit != 0 {
		event := StreamEvent{Type: "error"}
		event.Error = &struct {
			EName     string   `json:"ename"`
			EValue    string   `json:"evalue"`
			Traceback []string `json:"traceback,omitempty"`
		}{EName: "CommandExecError", EValue: strconv.Itoa(exit)}
		writeEvent(w, event)
		return
	}
	writeEvent(w, StreamEvent{Type: "execution_complete"})
}

// defaultForeground emulates the provider's own helper commands.
func (f *fakeServer) defaultForeground(sb *fakeSandbox, req RunCommandRequest) ([]string, []string, int) {
	if len(req.Argv) >= 3 && strings.Contains(req.Argv[2], "kill -TERM") {
		f.mu.Lock()
		for _, cid := range sb.Order {
			if cmd := sb.Commands[cid]; cmd.Request.Background && cmd.Running {
				cmd.Running = false
				code := 143
				cmd.ExitCode = &code
				now := f.now()
				cmd.FinishedAt = &now
				cmd.Output = append(cmd.Output, "runner: SIGTERM, committing work in progress")
			}
		}
		f.mu.Unlock()
	}
	return nil, nil, 0
}

func (f *fakeServer) ptyWS(w http.ResponseWriter, r *http.Request, sb *fakeSandbox, id string) {
	f.mu.Lock()
	_, ok := sb.PTYs[id]
	f.mu.Unlock()
	if !ok {
		writeError(w, fakeError{Status: 404, Code: "SESSION_GONE", Message: id})
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := r.Context()
	connected, _ := json.Marshal(map[string]string{"type": "connected", "session_id": id, "mode": "pty", "role": "holder"})
	_ = conn.Write(ctx, websocket.MessageText, connected)
	// A replay frame first (offset 0), as on a reconnect.
	_ = conn.Write(ctx, websocket.MessageBinary, append([]byte{ptyReplay, 0, 0, 0, 0, 0, 0, 0, 0}, []byte("$ ")...))
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageText {
			var frame ptyFrame
			if json.Unmarshal(data, &frame) == nil && frame.Type == "resize" {
				f.mu.Lock()
				f.resizes = append(f.resizes, [2]int{frame.Cols, frame.Rows})
				f.mu.Unlock()
			}
			continue
		}
		if len(data) == 0 || data[0] != ptyStdin {
			continue
		}
		input := string(data[1:])
		if strings.Contains(input, "\x04") {
			code := 3
			exit, _ := json.Marshal(ptyFrame{Type: "exit", ExitCode: &code})
			_ = conn.Write(ctx, websocket.MessageText, exit)
			_ = conn.Close(websocket.StatusNormalClosure, "")
			return
		}
		_ = conn.Write(ctx, websocket.MessageBinary, append([]byte{ptyStdout}, []byte("echo:"+input)...))
	}
}
