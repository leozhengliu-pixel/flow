package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// `flow-api egress-acl` is the Squid helper behind the coding-session egress
// proxy (deploy/egress-proxy, docs/coding-sessions-security.md). It runs in
// the proxy container, reads Squid's helper protocol on stdin and asks Flow
// (POST /internal/coding-runtime/egress-check) for every decision:
//
//	flow-api egress-acl auth   [--concurrency]   auth_param basic program
//	    stdin:  "<sandboxId> <token>"              (URL-escaped by Squid)
//	flow-api egress-acl check  [--concurrency]   external_acl_type program
//	    stdin:  "<Proxy-Authorization> <host> <port> [method]"
//
// With --concurrency every line starts with Squid's channel id, and answers
// may be written out of order. Answers are OK, ERR (denied) or BH (Flow could
// not decide; Squid denies). Squid caches them (credentialsttl / ttl).
//
// Environment: FLOW_EGRESS_FLOW_URL (falls back to FLOW_CODING_RUNTIME_URL,
// then FLOW_APP_URL), FLOW_CODING_EGRESS_ACL_SECRET (or _FILE), and
// FLOW_EGRESS_ACL_TIMEOUT (default 5s).

type egressACLHelper struct {
	mode        string
	concurrency bool
	endpoint    string
	secret      string
	client      *http.Client
	retryDelay  time.Duration
}

func runEgressACLCommand(args []string) int {
	return runEgressACL(args, os.Stdin, os.Stdout, os.Stderr, os.Getenv)
}

func runEgressACL(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	helper, err := newEgressACLHelper(args, getenv)
	if err != nil {
		fmt.Fprintf(stderr, "flow-api egress-acl: %v\n", err)
		fmt.Fprintln(stderr, "usage: flow-api egress-acl auth|check [--concurrency]")
		return 2
	}
	if err := helper.serve(context.Background(), stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "flow-api egress-acl: %v\n", err)
		return 1
	}
	return 0
}

func newEgressACLHelper(args []string, getenv func(string) string) (*egressACLHelper, error) {
	helper := &egressACLHelper{retryDelay: 200 * time.Millisecond}
	for _, arg := range args {
		switch arg {
		case "auth", "check":
			if helper.mode != "" {
				return nil, fmt.Errorf("only one mode may be given")
			}
			helper.mode = arg
		case "--concurrency", "-c":
			helper.concurrency = true
		default:
			return nil, fmt.Errorf("unknown argument %q", arg)
		}
	}
	if helper.mode == "" {
		return nil, fmt.Errorf("a mode is required")
	}
	base := firstNonEmpty(strings.TrimSpace(getenv("FLOW_EGRESS_FLOW_URL")), strings.TrimSpace(getenv("FLOW_CODING_RUNTIME_URL")), strings.TrimSpace(getenv("FLOW_APP_URL")))
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(base), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("FLOW_EGRESS_FLOW_URL must be Flow's runtime URL, such as http://flow.flow.svc:8080")
	}
	helper.endpoint = parsed.String() + "/internal/coding-runtime/egress-check"
	helper.secret = strings.TrimSpace(getenv("FLOW_CODING_EGRESS_ACL_SECRET"))
	if path := strings.TrimSpace(getenv("FLOW_CODING_EGRESS_ACL_SECRET_FILE")); path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read FLOW_CODING_EGRESS_ACL_SECRET_FILE: %w", err)
		}
		helper.secret = strings.TrimSpace(string(content))
	}
	if helper.secret == "" {
		return nil, fmt.Errorf("FLOW_CODING_EGRESS_ACL_SECRET is required")
	}
	timeout := 5 * time.Second
	if raw := strings.TrimSpace(getenv("FLOW_EGRESS_ACL_TIMEOUT")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("FLOW_EGRESS_ACL_TIMEOUT must be a positive duration")
		}
		timeout = parsed
	}
	// The helper talks to Flow directly, never through a proxy.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	helper.client = &http.Client{Timeout: timeout, Transport: transport}
	return helper, nil
}

// serve answers one line per request until stdin closes (Squid shutdown).
func (h *egressACLHelper) serve(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	writer := bufio.NewWriter(stdout)
	var mu sync.Mutex
	var wg sync.WaitGroup
	write := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		_, _ = writer.WriteString(line + "\n")
		_ = writer.Flush()
	}
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if !h.concurrency {
			write(h.answer(ctx, line))
			continue
		}
		channel, rest, _ := strings.Cut(line, " ")
		wg.Add(1)
		go func() {
			defer wg.Done()
			write(channel + " " + h.answer(ctx, rest))
		}()
	}
	wg.Wait()
	return scanner.Err()
}

// answer turns one request line into OK, ERR or BH.
func (h *egressACLHelper) answer(ctx context.Context, line string) string {
	fields := strings.Fields(line)
	request := EgressCheckRequest{}
	switch h.mode {
	case "auth":
		if len(fields) < 2 {
			return egressHelperReply("ERR", "malformed request")
		}
		request.SandboxID, request.SandboxToken = squidUnescape(fields[0]), squidUnescape(fields[1])
	case "check":
		if len(fields) < 3 {
			return egressHelperReply("ERR", "malformed request")
		}
		user, token, ok := parseProxyBasicAuth(squidUnescape(fields[0]))
		if !ok {
			return egressHelperReply("ERR", "missing proxy credentials")
		}
		port, err := strconv.Atoi(squidUnescape(fields[2]))
		if err != nil || port < 0 || port > 65535 {
			return egressHelperReply("ERR", "invalid port")
		}
		request.SandboxID, request.SandboxToken = user, token
		request.Host, request.Port = squidUnescape(fields[1]), port
		if len(fields) > 3 {
			request.Method = squidUnescape(fields[3])
		}
		if request.Host == "" {
			return egressHelperReply("ERR", "missing destination")
		}
	}
	decision, err := h.check(ctx, request)
	if err != nil {
		return egressHelperReply("BH", err.Error())
	}
	if decision.Allow {
		return "OK"
	}
	return egressHelperReply("ERR", firstNonEmpty(decision.Reason, "denied"))
}

// check posts the request to Flow, retrying once on a transport error or 5xx.
func (h *egressACLHelper) check(ctx context.Context, request EgressCheckRequest) (EgressCheckResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return EgressCheckResponse{}, err
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return EgressCheckResponse{}, ctx.Err()
			case <-time.After(h.retryDelay):
			}
		}
		decision, retry, err := h.post(ctx, body)
		if err == nil {
			return decision, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return EgressCheckResponse{}, lastErr
}

func (h *egressACLHelper) post(ctx context.Context, body []byte) (EgressCheckResponse, bool, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint, bytes.NewReader(body))
	if err != nil {
		return EgressCheckResponse{}, false, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+h.secret)
	response, err := h.client.Do(httpRequest)
	if err != nil {
		return EgressCheckResponse{}, true, fmt.Errorf("flow unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode >= 500 {
		return EgressCheckResponse{}, true, fmt.Errorf("flow answered %d", response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		return EgressCheckResponse{}, false, fmt.Errorf("flow answered %d", response.StatusCode)
	}
	var decision EgressCheckResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&decision); err != nil {
		return EgressCheckResponse{}, false, fmt.Errorf("invalid flow response")
	}
	return decision, false, nil
}

// squidUnescape decodes Squid's URL-escaped helper fields; "-" means empty.
func squidUnescape(value string) string {
	if value == "-" {
		return ""
	}
	if decoded, err := url.PathUnescape(value); err == nil {
		return decoded
	}
	return value
}

// parseProxyBasicAuth reads "Basic base64(user:password)".
func parseProxyBasicAuth(header string) (string, string, bool) {
	scheme, encoded, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "basic") {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", "", false
	}
	user, password, ok := strings.Cut(string(decoded), ":")
	if !ok || user == "" || password == "" {
		return "", "", false
	}
	return user, password, true
}

// egressHelperReply formats a Squid helper answer with a quoted message.
func egressHelperReply(result, message string) string {
	message = strings.NewReplacer(`"`, "'", "\n", " ", "\r", " ", `\`, "/").Replace(message)
	return result + ` message="` + message + `"`
}
