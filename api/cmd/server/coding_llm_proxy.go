package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

// LLM proxy (api/internal/llmproxy) and commit signing for sandboxes.
//
// Owner: track E (LLM proxy & signing). Keep the route list in sync with
// publicapi.InternalRoutes.
func (s *server) registerCodingLLMProxyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/llm-proxy/anthropic/v1/messages", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/anthropic/v1/messages/count_tokens", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/openai/v1/responses", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/openai/v1/chat/completions", s.codingLLMProxy)
	mux.HandleFunc("GET /internal/llm-proxy/openai/v1/models", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/opensource/v1/chat/completions", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/llm-proxy/opensource/v1/responses", s.codingLLMProxy)
	mux.HandleFunc("GET /internal/llm-proxy/opensource/v1/models", s.codingLLMProxy)
	mux.HandleFunc("POST /internal/coding-runtime/sign", s.codingRuntimeSign)
}

// codingSandboxAuthStore is the part of track B's coding.SessionStore that
// sandbox-token routes need. It is resolved at runtime so this file builds
// before B's store methods land; without them the routes answer 503.
type codingSandboxAuthStore interface {
	AuthenticateSandboxToken(ctx context.Context, token string) (coding.SandboxGrant, error)
	CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)
}

// codingSandboxListStore lets the token check confirm the sandbox is live.
type codingSandboxListStore interface {
	CodingSandboxes(ctx context.Context, workspaceKey, sessionID string) ([]sandbox.Record, error)
}

type codingSessionUpdateStore interface {
	UpdateCodingSession(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error)
}

var (
	errCodingStoreUnavailable = errors.New("coding session store is not available")
	errSandboxNotSession      = errors.New("sandbox token does not belong to a running coding session")
)

// codingSandboxCaller is an authenticated sandbox token with its session.
type codingSandboxCaller struct {
	grant   coding.SandboxGrant
	session domain.CodingSession
}

// authenticateCodingSandbox resolves a sandbox token to a live session sandbox
// (§7.2: valid only while its sandbox runs and the session is not terminal).
// It returns llmproxy.ErrUnauthenticated for any invalid token.
func (s *server) authenticateCodingSandbox(ctx context.Context, token string) (codingSandboxCaller, error) {
	return authenticateCodingSandboxToken(ctx, any(s.codingStore()), token, time.Now())
}

// authenticateCodingSandboxToken is authenticateCodingSandbox over any store
// that implements codingSandboxAuthStore (and optionally codingSandboxListStore).
func authenticateCodingSandboxToken(ctx context.Context, source any, token string, now time.Time) (codingSandboxCaller, error) {
	sessions, ok := source.(codingSandboxAuthStore)
	if !ok {
		return codingSandboxCaller{}, errCodingStoreUnavailable
	}
	if token == "" {
		return codingSandboxCaller{}, llmproxy.ErrUnauthenticated
	}
	grant, err := sessions.AuthenticateSandboxToken(ctx, token)
	if errors.Is(err, coding.ErrTokenInvalid) || errors.Is(err, coding.ErrNotFound) {
		return codingSandboxCaller{}, llmproxy.ErrUnauthenticated
	}
	if err != nil {
		return codingSandboxCaller{}, err
	}
	if grant.SessionID == "" || (grant.Kind != "" && grant.Kind != sandbox.KindSession) {
		return codingSandboxCaller{}, errors.Join(llmproxy.ErrUnauthenticated, errSandboxNotSession)
	}
	if !grant.ExpiresAt.IsZero() && now.After(grant.ExpiresAt) {
		return codingSandboxCaller{}, llmproxy.ErrUnauthenticated
	}
	session, err := sessions.CodingSession(ctx, grant.WorkspaceKey, grant.SessionID)
	if errors.Is(err, coding.ErrNotFound) {
		return codingSandboxCaller{}, llmproxy.ErrUnauthenticated
	}
	if err != nil {
		return codingSandboxCaller{}, err
	}
	if session.Phase == domain.CodingPhaseDone || session.EndedAt != nil {
		return codingSandboxCaller{}, errors.Join(llmproxy.ErrUnauthenticated, errSandboxNotSession)
	}
	if lister, ok := source.(codingSandboxListStore); ok {
		records, err := lister.CodingSandboxes(ctx, grant.WorkspaceKey, grant.SessionID)
		if err != nil {
			return codingSandboxCaller{}, err
		}
		if !codingSandboxLive(records, grant.SandboxID) {
			return codingSandboxCaller{}, errors.Join(llmproxy.ErrUnauthenticated, errSandboxNotSession)
		}
	}
	return codingSandboxCaller{grant: grant, session: session}, nil
}

// codingSandboxLive reports whether the sandbox is still meant to run. The
// observed phase may lag the runner by one observation, so only finished or
// stop-requested sandboxes are refused.
func codingSandboxLive(records []sandbox.Record, sandboxID string) bool {
	for _, record := range records {
		if record.ID != sandboxID {
			continue
		}
		switch {
		case record.EndedAt != nil, record.Desired == sandbox.DesiredStopped:
			return false
		case record.Phase == sandbox.PhaseSucceeded, record.Phase == sandbox.PhaseFailed, record.Phase == sandbox.PhaseMissing:
			return false
		}
		return true
	}
	return false
}

// codingSandboxBearer reads the runtime token (Authorization: Bearer fcs_…).
func codingSandboxBearer(r *http.Request) string {
	if value, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// codingLLMAuthenticator adapts the sandbox token check to llmproxy.
type codingLLMAuthenticator struct{ s *server }

func (a codingLLMAuthenticator) AuthenticateLLMCaller(ctx context.Context, token string) (llmproxy.Caller, error) {
	caller, err := a.s.authenticateCodingSandbox(ctx, token)
	if err != nil {
		return llmproxy.Caller{}, err
	}
	return llmCallerFor(caller), nil
}

// llmCallerFor maps an authenticated sandbox to the proxy's caller: usage is
// attributed to the session creator (the loop owner for loops) and the loop.
func llmCallerFor(caller codingSandboxCaller) llmproxy.Caller {
	session := caller.session
	loopID := ""
	if session.Source == domain.CodingSourceLoop {
		loopID = session.SourceID
	}
	selection := strings.TrimSpace(session.ModelSelection)
	return llmproxy.Caller{
		WorkspaceKey: caller.grant.WorkspaceKey, SessionID: session.ID, SandboxID: caller.grant.SandboxID,
		UserID: session.CreatorID, LoopID: loopID, Harness: session.Harness, Model: session.Model,
		AutoModel: selection == "" || selection == "auto",
	}
}

// codingUsageRecorder writes LLM usage through track B's UsageStore.
type codingUsageRecorder struct{ s *server }

func (u codingUsageRecorder) RecordCodingUsage(ctx context.Context, record domain.CodingUsageRecord) error {
	// Through the session Meter (cost fill-in, metrics, post-record spend
	// check that ends sessions over their limit).
	if u.s.coding != nil && u.s.coding.meter != nil {
		return u.s.coding.meter.Record(ctx, record)
	}
	return u.s.codingStore().RecordCodingUsage(ctx, record)
}

// codingModelSwitcher persists an Auto fallback on the session and posts a
// thought activity ("Switched to X") on the AgentTask anchor (§12).
type codingModelSwitcher struct{ s *server }

func (sw codingModelSwitcher) RecordModelSwitch(ctx context.Context, caller llmproxy.Caller, from, to, reason string) error {
	sessions, ok := any(sw.s.codingStore()).(codingSandboxAuthStore)
	if !ok {
		return errCodingStoreUnavailable
	}
	session, err := sessions.CodingSession(ctx, caller.WorkspaceKey, caller.SessionID)
	if err != nil {
		return err
	}
	if updater, ok := any(sw.s.codingStore()).(codingSessionUpdateStore); ok && reason == llmproxy.ReasonModelNotFound {
		if _, err := updater.UpdateCodingSession(ctx, caller.WorkspaceKey, caller.SessionID, func(current *domain.CodingSession) error {
			if current.Model == "" || current.Model == from {
				current.Model = to
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if session.AgentSessionID == "" {
		return nil
	}
	body := "Switched to " + to
	if reason == llmproxy.ReasonOverloaded {
		body += " (" + from + " is overloaded)"
	} else {
		body += " (" + from + " is unavailable)"
	}
	for attempt := 0; attempt < 3; attempt++ {
		task, err := sw.s.store.AgentTask(ctx, caller.WorkspaceKey, session.AgentSessionID)
		if err != nil {
			return err
		}
		_, err = sw.s.store.AppendAgentActivity(ctx, caller.WorkspaceKey, task.ID, task.Version, domain.AgentActivity{
			ActorID: task.AppUserID, Type: "thought", Body: body, Metadata: map[string]any{"modelSwitch": map[string]any{"from": from, "to": to, "reason": reason}},
		})
		if !errors.Is(err, store.ErrIssueVersion) {
			return err
		}
	}
	return nil
}

// codingLLMProxies holds one proxy per coding runtime, so per-session rate
// limits and Auto fallbacks persist across requests.
var codingLLMProxies sync.Map // *codingServices -> *llmproxy.Proxy

func (s *server) codingLLMProxyHandler() *llmproxy.Proxy {
	if existing, ok := codingLLMProxies.Load(s.coding); ok {
		return existing.(*llmproxy.Proxy)
	}
	guard := s.coding.llmSpendChecker()
	if guard == nil {
		slog.Warn("coding sessions: the LLM proxy has no spend guard; usage is metered but spend limits are not enforced per request")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 64
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = 10 * time.Second
	proxy := &llmproxy.Proxy{
		Config:   s.coding.config.LLM,
		Auth:     codingLLMAuthenticator{s: s},
		Meter:    &llmproxy.GuardedMeter{Usage: codingUsageRecorder{s: s}, Guard: guard},
		Client:   &http.Client{Transport: transport},
		Switcher: codingModelSwitcher{s: s},
	}
	actual, _ := codingLLMProxies.LoadOrStore(s.coding, proxy)
	return actual.(*llmproxy.Proxy)
}

// codingLLMProxy serves the LLM proxy routes (§7.2, §12):
//
//	POST /internal/llm-proxy/anthropic/v1/messages: Anthropic Messages (Claude Code).
//	POST /internal/llm-proxy/anthropic/v1/messages/count_tokens: Anthropic token counting.
//	POST /internal/llm-proxy/openai/v1/responses: OpenAI Responses (Codex).
//	POST /internal/llm-proxy/openai/v1/chat/completions: OpenAI chat completions.
//	GET /internal/llm-proxy/openai/v1/models: Model list for the session.
//	POST /internal/llm-proxy/opensource/v1/chat/completions: Open-source (OpenAI-compatible) models.
//	POST /internal/llm-proxy/opensource/v1/responses: Open-source models over the Responses API.
//	GET /internal/llm-proxy/opensource/v1/models: Open-source model list.
func (s *server) codingLLMProxy(w http.ResponseWriter, r *http.Request) {
	if !s.codingEnabled() {
		writeCodingDisabled(w)
		return
	}
	s.codingLLMProxyHandler().ServeHTTP(w, r)
}

// writeCodingDisabled answers sandbox routes while the coding runtime is off
// (501, like the other runtime routes of a disabled runtime).
func writeCodingDisabled(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "Coding sessions are not enabled on this server")
}

// codingSigningError is the /sign error body; code is a coding error code
// (missingCommitSigningKey, signingFailed) the runner reports on the session.
type codingSigningError struct {
	Error string                 `json:"error"`
	Code  domain.CodingErrorCode `json:"code,omitempty"`
}

// codingRuntimeSign signs a commit or tag payload with the session creator's
// key (ssh or openpgp); the private key never leaves the API.
//
//	POST /internal/coding-runtime/sign: Signs a commit payload with the requesting user's key (ssh or openpgp).
func (s *server) codingRuntimeSign(w http.ResponseWriter, r *http.Request) {
	if !s.codingEnabled() {
		writeCodingDisabled(w)
		return
	}
	caller, err := s.authenticateCodingSandbox(r.Context(), codingSandboxBearer(r))
	if err != nil {
		if errors.Is(err, llmproxy.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "The sandbox token is invalid, expired or revoked")
		} else {
			writeError(w, http.StatusServiceUnavailable, "Flow could not verify the sandbox token; retry shortly")
		}
		return
	}
	var input coding.SignRequest
	// Payloads travel base64-encoded in JSON: allow for the 4/3 expansion.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, coding.MaxSignPayloadBytes*4/3+4096)).Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, codingSigningError{Error: "The payload to sign is too large", Code: domain.CodingErrSigningFailed})
		} else {
			writeError(w, http.StatusBadRequest, "Invalid sign request")
		}
		return
	}
	session := caller.session
	started := time.Now()
	response, err := coding.SignForUser(r.Context(), s.store, session.CreatorID, input, s.userHasLegacySigningKey(caller.grant.WorkspaceKey, session.CreatorID))
	logAttrs := []any{"session_id", session.ID, "sandbox_id", caller.grant.SandboxID, "format", input.Format, "payload_bytes", len(input.Payload), "duration_ms", time.Since(started).Milliseconds()}
	var signingErr *coding.CommitSigningError
	switch {
	case errors.As(err, &signingErr):
		status := http.StatusUnprocessableEntity
		if signingErr.Code == domain.CodingErrMissingCommitSigningKey {
			status = http.StatusPreconditionFailed
		}
		slog.WarnContext(r.Context(), "coding commit signing refused", append(logAttrs, "code", string(signingErr.Code))...)
		writeJSON(w, status, codingSigningError{Error: signingErr.Message, Code: signingErr.Code})
	case err != nil:
		slog.ErrorContext(r.Context(), "coding commit signing failed", append(logAttrs, "error", err.Error())...)
		writeError(w, http.StatusServiceUnavailable, "Flow could not load the signing key; retry shortly")
	default:
		slog.InfoContext(r.Context(), "coding commit signed", logAttrs...)
		writeJSON(w, http.StatusOK, response)
	}
}

// userHasLegacySigningKey reports whether the user's account shows a signing
// key in this workspace (CommitSigningKey metadata). With no stored material
// it is a key uploaded before Flow kept private keys, and the user is asked to
// upload it again rather than to add one.
func (s *server) userHasLegacySigningKey(workspace, userID string) bool {
	_, settings, ok := s.store.AccountCollections(workspace, userID)
	return ok && settings.CommitSigningKey != nil
}

// resolveCodingCommitSigning applies the workspace's "Require signed commits"
// policy (CodingAgentSettings.CommitSigningEnabled) for the session creator at
// provisioning. Track B calls it and copies the result into
// BootstrapGit.SigningFormat / SigningPublicKey; a *coding.CommitSigningError
// (missingCommitSigningKey) fails the session without retry.
func (s *server) resolveCodingCommitSigning(ctx context.Context, workspace, userID string) (coding.SigningResolution, error) {
	required := false
	if metadata, ok := s.store.WorkspaceSettingsMetadata(workspace); ok {
		required = metadata.WorkspaceSettings.CodingAgentSettings.CommitSigningEnabled
	}
	return coding.ResolveCommitSigning(ctx, s.store, userID, required, s.userHasLegacySigningKey(workspace, userID))
}

var _ coding.SigningKeyStore = (*store.SQLiteStore)(nil)
