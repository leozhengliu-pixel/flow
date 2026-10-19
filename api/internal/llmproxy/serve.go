package llmproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

type endpoint string

const (
	endpointMessages    endpoint = "messages"
	endpointCountTokens endpoint = "count_tokens"
	endpointResponses   endpoint = "responses"
	endpointChat        endpoint = "chat_completions"
	endpointModels      endpoint = "models"
)

// metered endpoints produce usage; count_tokens answers with an
// input_tokens count that is not consumption and must not be billed.
func (e endpoint) metered() bool {
	return e == endpointMessages || e == endpointResponses || e == endpointChat
}

// parseRoute maps /internal/llm-proxy/{upstream}/v1/... to an endpoint.
func parseRoute(method, path string) (upstream string, ep endpoint, rest string, ok bool) {
	tail, found := strings.CutPrefix(path, PathPrefix)
	if !found {
		return "", "", "", false
	}
	upstream, rest, _ = strings.Cut(tail, "/")
	rest = strings.TrimSuffix(rest, "/")
	switch upstream {
	case UpstreamAnthropic:
		switch {
		case method == http.MethodPost && rest == "v1/messages":
			return upstream, endpointMessages, rest, true
		case method == http.MethodPost && rest == "v1/messages/count_tokens":
			return upstream, endpointCountTokens, rest, true
		}
	case UpstreamOpenAI, UpstreamOpenSource:
		switch {
		case method == http.MethodPost && rest == "v1/responses":
			return upstream, endpointResponses, rest, true
		case method == http.MethodPost && rest == "v1/chat/completions":
			return upstream, endpointChat, rest, true
		case method == http.MethodGet && rest == "v1/models":
			return upstream, endpointModels, rest, true
		}
	}
	return upstream, "", rest, false
}

// upstreamSpec is where and how a request is forwarded.
type upstreamSpec struct {
	name    string
	baseURL string
	apiKey  string
}

func (p *Proxy) upstream(name string) (upstreamSpec, bool) {
	switch name {
	case UpstreamAnthropic:
		return upstreamSpec{name, p.Config.AnthropicBaseURL, p.Config.AnthropicAPIKey}, p.Config.AnthropicBaseURL != "" && p.Config.AnthropicAPIKey != ""
	case UpstreamOpenAI:
		return upstreamSpec{name, p.Config.OpenAIBaseURL, p.Config.OpenAIAPIKey}, p.Config.OpenAIBaseURL != "" && p.Config.OpenAIAPIKey != ""
	case UpstreamOpenSource:
		// Self-hosted OpenAI-compatible servers (vLLM, Ollama) may need no key.
		return upstreamSpec{name, p.Config.OpenSourceBaseURL, p.Config.OpenSourceAPIKey}, p.Config.OpenSourceBaseURL != ""
	}
	return upstreamSpec{}, false
}

// url joins the upstream base with the API path. The Anthropic base is the
// API host (https://api.anthropic.com); the OpenAI-style bases already end in
// /v1 (https://api.openai.com/v1), so the v1/ of the proxy path is dropped.
func (u upstreamSpec) url(rest, rawQuery string) string {
	base := strings.TrimRight(u.baseURL, "/")
	path := rest
	if u.name != UpstreamAnthropic {
		path = strings.TrimPrefix(rest, "v1/")
	}
	target := base + "/" + path
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return target
}

// authorize injects the real provider key. The sandbox's own credentials were
// never copied onto the outgoing request.
func (u upstreamSpec) authorize(header http.Header) {
	if u.apiKey == "" {
		return
	}
	if u.name == UpstreamAnthropic {
		header.Set("x-api-key", u.apiKey)
		return
	}
	header.Set("Authorization", "Bearer "+u.apiKey)
}

// forwardedRequestHeaders are the only request headers passed upstream.
// Credentials, cookies, proxy and hop-by-hop headers are dropped, and
// Accept-Encoding is left to the transport so bodies can be metered.
var forwardedRequestHeaders = []string{"Accept", "Anthropic-Version", "Anthropic-Beta", "OpenAI-Beta", "User-Agent", "X-Stainless-Helper-Method", "X-Stainless-Retry-Count", "X-Stainless-Timeout"}

// droppedResponseHeaders are not copied back to the sandbox.
var droppedResponseHeaders = map[string]bool{"Connection": true, "Keep-Alive": true, "Proxy-Authenticate": true, "Proxy-Authorization": true, "Te": true, "Trailer": true, "Transfer-Encoding": true, "Upgrade": true, "Set-Cookie": true, "Alt-Svc": true}

// Error codes surfaced to the runner through X-Flow-Error-Code.
const headerErrorCode = "X-Flow-Error-Code"

// sandboxToken reads Authorization: Bearer (OpenAI clients and Claude Code
// with ANTHROPIC_AUTH_TOKEN) or x-api-key (Anthropic clients).
func sandboxToken(r *http.Request) string {
	if value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(r.Header.Get("x-api-key"))
}

// requestLog accumulates the fields of the single structured log line each
// request writes. It never holds bodies, tokens or keys.
type requestLog struct {
	upstream, endpoint, session, sandbox, workspace string
	requested, model                                string
	status                                          int
	stream, fallback                                bool
	usage                                           Usage
	cost                                            int64
	reason                                          string
}

func (l *requestLog) attrs(started time.Time, now time.Time) []any {
	return []any{
		"upstream", l.upstream, "endpoint", l.endpoint, "session_id", l.session, "sandbox_id", l.sandbox, "workspace", l.workspace,
		"requested_model", l.requested, "model", l.model, "status", l.status, "stream", l.stream, "fallback", l.fallback,
		"input_tokens", l.usage.InputTokens, "output_tokens", l.usage.OutputTokens, "cache_read_tokens", l.usage.CacheReadTokens,
		"cache_write_tokens", l.usage.CacheWriteTokens, "cost_micros", l.cost, "duration_ms", now.Sub(started).Milliseconds(), "reason", l.reason,
	}
}

// ServeHTTP proxies one model API request for a sandbox.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.init()
	started := p.now()
	upstreamName, ep, rest, ok := parseRoute(r.Method, r.URL.Path)
	entry := &requestLog{upstream: upstreamName, endpoint: string(ep)}
	defer func() {
		countRequest(r.Context(), entry.upstream, entry.status)
		level := slog.LevelInfo
		if entry.status >= 500 || entry.status == 0 {
			level = slog.LevelWarn
		}
		p.logger().Log(r.Context(), level, "llm proxy request", entry.attrs(started, p.now())...)
	}()
	fail := func(status int, errType, message, code string) {
		entry.status, entry.reason = status, errType
		writeProxyError(w, status, errType, message, code)
	}
	if !ok {
		fail(http.StatusNotFound, "not_found_error", "Unknown LLM proxy route", "")
		return
	}
	token := sandboxToken(r)
	if token == "" || p.Auth == nil {
		fail(http.StatusUnauthorized, "authentication_error", "A sandbox token is required", "")
		return
	}
	caller, err := p.Auth.AuthenticateLLMCaller(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrUnauthenticated) {
			fail(http.StatusUnauthorized, "authentication_error", "The sandbox token is invalid, expired or revoked", "")
		} else {
			fail(http.StatusServiceUnavailable, "api_error", "Flow could not verify the sandbox token; retry shortly", "")
		}
		return
	}
	entry.session, entry.sandbox, entry.workspace = caller.SessionID, caller.SandboxID, caller.WorkspaceKey
	if HarnessUpstream(caller.Harness) != upstreamName {
		fail(http.StatusForbidden, "permission_error", "This session's agent cannot use the "+upstreamName+" API", "")
		return
	}
	spec, configured := p.upstream(upstreamName)
	if !configured {
		fail(http.StatusServiceUnavailable, "api_error", "The "+upstreamName+" upstream is not configured on this Flow server", "")
		return
	}
	release, retryAfter, allowed := p.limiter.acquire(caller.SessionID)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retryAfter.Round(time.Second)/time.Second))))
		fail(http.StatusTooManyRequests, "rate_limit_error", "Too many model requests from this session; slow down", "")
		return
	}
	defer release()

	if ep == endpointModels {
		entry.status = http.StatusOK
		writeModelList(w, p.listedModels(caller))
		return
	}

	if p.Meter != nil {
		if err := p.Meter.CheckSpend(r.Context(), caller); err != nil {
			if IsSpendLimit(err) {
				w.Header().Set(headerErrorCode, string(domain.CodingErrQuotaExceeded))
				fail(http.StatusPaymentRequired, "quota_exceeded", "The coding spend limit has been reached", string(domain.CodingErrQuotaExceeded))
			} else {
				fail(http.StatusServiceUnavailable, "api_error", "Flow could not check the spend limit; retry shortly", "")
			}
			return
		}
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, p.limits.MaxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			fail(http.StatusRequestEntityTooLarge, "request_too_large", "The request body exceeds "+strconv.FormatInt(p.limits.MaxBodyBytes, 10)+" bytes", "")
		} else {
			fail(http.StatusBadRequest, "invalid_request_error", "The request body could not be read", "")
		}
		return
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil || document == nil {
		fail(http.StatusBadRequest, "invalid_request_error", "The request body must be a JSON object", "")
		return
	}
	var requested string
	if model, present := document["model"]; present {
		_ = json.Unmarshal(model, &requested)
	}
	entry.requested = requested
	decision, err := p.enforceModel(caller, upstreamName, requested)
	if err != nil {
		fail(http.StatusForbidden, "permission_error", "No model is available for this session", string(domain.CodingErrModelUnavailable))
		return
	}
	var translation *chatTranslation
	if ep == endpointResponses && upstreamName == UpstreamOpenAI && p.Config.OpenAIWireAPI == config.OpenAIWireAPIChat {
		// The upstream only speaks Chat Completions (chatwire.go).
		chat, translated, err := responsesToChat(document)
		if err != nil {
			fail(http.StatusBadRequest, "invalid_request_error", err.Error(), "")
			return
		}
		document, translation, ep, rest = chat, translated, endpointChat, "v1/chat/completions"
	}
	var stream bool
	if value, present := document["stream"]; present {
		_ = json.Unmarshal(value, &stream)
	}
	entry.stream = stream
	if ep == endpointChat && stream {
		// Usage is only sent on streamed chat completions when asked for.
		options := map[string]json.RawMessage{}
		if existing, present := document["stream_options"]; present {
			_ = json.Unmarshal(existing, &options)
		}
		options["include_usage"] = json.RawMessage("true")
		document["stream_options"], _ = json.Marshal(options)
	}

	ctx, cancel := context.WithTimeout(r.Context(), p.timeout())
	defer cancel()
	response, decision, fellBack, err := p.forward(ctx, r, spec, ep, rest, caller, document, decision)
	entry.model, entry.fallback = decision.Model, fellBack
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			fail(http.StatusGatewayTimeout, "timeout_error", "The model provider did not answer in time", "")
		} else if r.Context().Err() != nil {
			entry.status, entry.reason = 499, "client_closed"
		} else {
			fail(http.StatusBadGateway, "api_error", "The model provider could not be reached", "")
		}
		return
	}
	defer response.Body.Close()

	header := w.Header()
	for key, values := range response.Header {
		if !droppedResponseHeaders[http.CanonicalHeaderKey(key)] {
			header[key] = values
		}
	}
	header.Set("X-Flow-Model", decision.Model)
	if !caller.AutoModel && !decision.Small && response.StatusCode != http.StatusOK && modelMissingStatus(response.StatusCode) && ep != endpointCountTokens {
		if missing, _ := peekModelUnavailable(response); missing == ReasonModelNotFound {
			header.Set(headerErrorCode, string(domain.CodingErrModelUnavailable))
		}
	}
	entry.status = response.StatusCode
	success := response.StatusCode >= 200 && response.StatusCode < 300

	var meter *usageMeter
	if ep.metered() && success {
		meter = newUsageMeter(upstreamName, response.Header.Get("Content-Type"), p.limits.MaxMeteredBytes)
	}
	var copyErr error
	if translation != nil && success {
		copyErr = relayChatAsResponses(w, response, translation, meter, p.now())
	} else {
		w.WriteHeader(response.StatusCode)
		copyErr = streamBody(w, response.Body, meter)
	}
	if copyErr != nil {
		entry.reason = "stream_interrupted"
	}
	if meter != nil {
		entry.usage = meter.Finish()
		entry.cost = p.record(r.Context(), caller, decision.Model, entry.usage)
	}
}

// streamBody copies the upstream body to the client chunk by chunk, flushing
// after every write so SSE events reach the harness as they arrive.
func streamBody(w http.ResponseWriter, body io.Reader, meter *usageMeter) error {
	controller := http.NewResponseController(w)
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := body.Read(buffer)
		if n > 0 {
			if meter != nil {
				meter.Write(buffer[:n])
			}
			if _, err := w.Write(buffer[:n]); err != nil {
				return err
			}
			if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

// forward sends the request upstream. On an Auto session it retries once with
// the next catalog model when the model is missing or overloaded (§12).
func (p *Proxy) forward(ctx context.Context, r *http.Request, spec upstreamSpec, ep endpoint, rest string, caller Caller, document map[string]json.RawMessage, decision modelDecision) (*http.Response, modelDecision, bool, error) {
	send := func(model string) (*http.Response, error) {
		document["model"], _ = json.Marshal(model)
		var body bytes.Buffer
		encoder := json.NewEncoder(&body)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(document); err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(ctx, r.Method, spec.url(rest, r.URL.RawQuery), bytes.NewReader(bytes.TrimSuffix(body.Bytes(), []byte("\n"))))
		if err != nil {
			return nil, err
		}
		for _, name := range forwardedRequestHeaders {
			if value := r.Header.Values(name); len(value) > 0 {
				request.Header[http.CanonicalHeaderKey(name)] = value
			}
		}
		request.Header.Set("Content-Type", "application/json")
		spec.authorize(request.Header)
		return p.client().Do(request)
	}
	response, err := send(decision.Model)
	if err != nil || !caller.AutoModel || decision.Small || ep == endpointCountTokens || !modelMissingStatus(response.StatusCode) {
		return response, decision, false, err
	}
	reason, response := peekModelUnavailable(response)
	if reason == "" {
		return response, decision, false, nil
	}
	next := p.nextModel(caller, decision.Model)
	if next == "" {
		return response, decision, false, nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	response.Body.Close()
	retried, err := send(next)
	if err != nil {
		return nil, decision, true, err
	}
	from := decision.Model
	decision.Model, decision.Rewritten = next, next != decision.Requested
	if reason == ReasonModelNotFound {
		// A missing model stays missing: later requests go straight to next.
		p.overrides.Store(caller.SessionID, next)
	}
	p.recordSwitch(ctx, caller, from, next, reason)
	return retried, decision, true, nil
}

func (p *Proxy) recordSwitch(ctx context.Context, caller Caller, from, to, reason string) {
	if p.Switcher == nil {
		return
	}
	if _, seen := p.switched.LoadOrStore(caller.SessionID+"\x00"+from+"\x00"+to, struct{}{}); seen {
		return
	}
	switchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := p.Switcher.RecordModelSwitch(switchCtx, caller, from, to, reason); err != nil {
		p.logger().Warn("llm proxy model switch not recorded", "session_id", caller.SessionID, "from", from, "to", to, "error", err.Error())
	}
}

// Reasons an upstream answer counts as "model unavailable" (passed to
// ModelSwitcher.RecordModelSwitch).
const (
	ReasonModelNotFound = "model_not_found"
	ReasonOverloaded    = "overloaded"
)

// modelMissingStatus is a status that may mean the model is unavailable.
func modelMissingStatus(status int) bool {
	switch status {
	case http.StatusNotFound, http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusServiceUnavailable, 529:
		return true
	}
	return false
}

// peekModelUnavailable classifies an error answer. For 400/404/422 it reads
// up to 64 KiB of the body to find a model-not-found error, then restores the
// body so it can still be passed through.
func peekModelUnavailable(response *http.Response) (string, *http.Response) {
	switch response.StatusCode {
	case http.StatusServiceUnavailable, 529:
		return ReasonOverloaded, response
	case http.StatusNotFound, http.StatusBadRequest, http.StatusUnprocessableEntity:
	default:
		return "", response
	}
	peek, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(peek), response.Body), response.Body}
	text := strings.ToLower(string(peek))
	if !strings.Contains(text, "model") {
		return "", response
	}
	for _, marker := range []string{"not_found", "not found", "does not exist", "unknown model", "invalid model", "model_not_found", "no such model", "unsupported model"} {
		if strings.Contains(text, marker) {
			return ReasonModelNotFound, response
		}
	}
	return "", response
}

// record prices and stores the metered usage; it returns the cost.
func (p *Proxy) record(ctx context.Context, caller Caller, model string, usage Usage) int64 {
	if usage.Empty() {
		return 0
	}
	price, priced := p.Config.Prices[model]
	if !priced && usage.Model != "" {
		price, priced = p.Config.Prices[usage.Model]
	}
	cost := int64(0)
	if priced {
		cost = CostMicros(price, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens)
	} else {
		p.logger().Warn("llm proxy model has no configured price; usage recorded at zero cost", "model", model, "session_id", caller.SessionID)
	}
	countUsage(ctx, model, usage, cost, priced)
	if p.Meter == nil {
		return cost
	}
	now := p.now().UTC()
	record := domain.CodingUsageRecord{
		ID: newID(now), WorkspaceKey: caller.WorkspaceKey, SessionID: caller.SessionID, SandboxID: caller.SandboxID,
		UserID: caller.UserID, LoopID: caller.LoopID, Kind: "llm", Model: model,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
		CostMicros: cost, CreatedAt: now,
	}
	// The harness may have hung up already; the usage still happened.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := p.Meter.RecordUsage(recordCtx, record); err != nil {
		p.logger().Error("llm proxy usage not recorded", "session_id", caller.SessionID, "model", model, "cost_micros", cost, "error", err.Error())
	}
	return cost
}

// writeProxyError answers in a shape both SDK families parse: Anthropic's
// {"type":"error","error":{...}} and OpenAI's {"error":{...}}.
func writeProxyError(w http.ResponseWriter, status int, errType, message, code string) {
	body := map[string]any{"type": errType, "message": message}
	if code != "" {
		body["code"] = code
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": body})
}

func writeModelList(w http.ResponseWriter, models []string) {
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, map[string]any{"id": model, "object": "model", "created": 0, "owned_by": "flow"})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

// newID returns a 26-character lowercase ULID (48-bit ms time + 80 random bits).
func newID(now time.Time) string {
	const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(now.UnixMilli())<<16)
	_, _ = rand.Read(raw[6:])
	// 128 bits -> 26 base32 characters, most significant first.
	var hi, lo = binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:])
	out := make([]byte, 26)
	for i := 25; i >= 0; i-- {
		out[i] = alphabet[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out)
}
