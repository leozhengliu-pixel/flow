package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"flow/api/internal/publicapi"
	"flow/api/internal/store"
)

// Public API error categories. Every JSON error response under /api/ carries
// the human readable `error` message (kept for the web client), a stable
// machine `type` from this list and a `code` that is either a more specific
// legacy code (VERSION_CONFLICT, mfa_required, ...) or the category itself.
const (
	apiErrorInvalidInput         = "INVALID_INPUT"
	apiErrorAuthentication       = "AUTHENTICATION_ERROR"
	apiErrorForbidden            = "FORBIDDEN"
	apiErrorFeatureNotAccessible = "FEATURE_NOT_ACCESSIBLE"
	apiErrorNotFound             = "NOT_FOUND"
	apiErrorConflict             = "CONFLICT"
	apiErrorRateLimited          = "RATELIMITED"
	apiErrorInternal             = "INTERNAL_ERROR"
	apiErrorUnavailable          = "UNAVAILABLE"
)

// apiErrorTypes lists every documented category (docs/api.md, OpenAPI).
var apiErrorTypes = []string{apiErrorInvalidInput, apiErrorAuthentication, apiErrorForbidden, apiErrorFeatureNotAccessible, apiErrorNotFound, apiErrorConflict, apiErrorRateLimited, apiErrorInternal, apiErrorUnavailable}

// apiErrorBody is the documented error envelope. Every value is a string so
// clients that decode errors into string maps keep working.
type apiErrorBody = publicapi.Error

func apiErrorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return apiErrorAuthentication
	case status == http.StatusForbidden:
		return apiErrorForbidden
	case status == http.StatusNotFound || status == http.StatusGone:
		return apiErrorNotFound
	case status == http.StatusConflict || status == http.StatusPreconditionFailed:
		return apiErrorConflict
	case status == http.StatusTooManyRequests:
		return apiErrorRateLimited
	case status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout:
		return apiErrorUnavailable
	case status >= 500:
		return apiErrorInternal
	default:
		return apiErrorInvalidInput
	}
}

// apiErrorTypeFor refines the status category with well-known messages that
// the shared authorization middleware emits.
func apiErrorTypeFor(status int, message string) string {
	if status == http.StatusForbidden && message == "This workspace feature is disabled" {
		return apiErrorFeatureNotAccessible
	}
	return apiErrorType(status)
}

// internalErrorMessage is the only message a 500 response carries. The error
// behind it (often database or driver text naming tables, columns and SQL
// states) is logged with the request id instead.
const internalErrorMessage = "Internal server error"

// requestIDFrom returns the id requestLog gave this request, or "".
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// writeErrorFor writes err's message with status: the message of a typed
// request error (validation, conflict, permission) is written for the person
// who sent the request. A database or driver error never is: it becomes a
// logged 500 with the generic message and the request id.
func writeErrorFor(w http.ResponseWriter, r *http.Request, status int, err error) {
	if store.IsDatabaseError(err) {
		writeInternalError(w, r, err)
		return
	}
	writeError(w, status, err.Error())
}

// publicErrorMessage is err's text for a protocol that has no 500 of its own
// (a WebSocket error frame): a database or driver error is logged with the
// request id and becomes the generic message.
func publicErrorMessage(ctx context.Context, err error) string {
	if !store.IsDatabaseError(err) {
		return err.Error()
	}
	slog.ErrorContext(ctx, "internal error", "request_id", requestIDFrom(ctx), "error", err.Error())
	return internalErrorMessage
}

// writeInternalError logs err with the request id and writes the generic 500.
func writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	requestID := requestIDFrom(r.Context())
	slog.ErrorContext(r.Context(), "internal error response", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "error", err.Error())
	writeJSON(w, http.StatusInternalServerError, apiErrorBody{Error: internalErrorMessage, Code: apiErrorInternal, Type: apiErrorInternal, RequestID: requestID})
}

// writeAPIError writes an error with an explicit machine code; the envelope
// middleware adds `type` and `requestId`.
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	if code == "" {
		code = apiErrorTypeFor(status, message)
	}
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

// apiErrorEnvelopePath reports routes whose errors follow the Flow envelope.
// OAuth (RFC 6749 `error` codes), MCP (JSON-RPC) and SCIM keep their own
// protocol error formats.
func apiErrorEnvelopePath(path string) bool {
	if path == "/api/oauth/token" || strings.HasPrefix(path, "/api/mcp/") {
		return false
	}
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/uploads/")
}

// apiErrorEnvelope normalizes every error response under /api/ (handler
// writeError calls, direct JSON errors, plain-text mux 404/405 responses and
// http.Error calls) into the documented envelope. Only error bodies are
// buffered; successful and streaming responses pass straight through.
func apiErrorEnvelope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !apiErrorEnvelopePath(r.URL.Path) || r.Header.Get("Upgrade") != "" {
			next.ServeHTTP(w, r)
			return
		}
		writer := &apiErrorWriter{ResponseWriter: w}
		defer writer.finish(r)
		next.ServeHTTP(writer, r)
	})
}

const apiErrorBufferLimit = 256 << 10

type apiErrorWriter struct {
	http.ResponseWriter
	status    int
	buffering bool
	overflow  bool
	buffer    bytes.Buffer
}

func (w *apiErrorWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *apiErrorWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	if status >= 400 {
		w.buffering = true
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *apiErrorWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !w.buffering {
		return w.ResponseWriter.Write(p)
	}
	if !w.overflow && w.buffer.Len()+len(p) <= apiErrorBufferLimit {
		return w.buffer.Write(p)
	}
	if !w.overflow {
		// Too large to be an error message: emit the original response.
		w.overflow = true
		w.ResponseWriter.WriteHeader(w.status)
		if _, err := w.ResponseWriter.Write(w.buffer.Bytes()); err != nil {
			return 0, err
		}
		w.buffer.Reset()
	}
	return w.ResponseWriter.Write(p)
}

func (w *apiErrorWriter) Flush() {
	if w.buffering && !w.overflow {
		return
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *apiErrorWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}

func (w *apiErrorWriter) finish(r *http.Request) {
	if !w.buffering || w.overflow {
		return
	}
	body := normalizeAPIErrorBody(w.status, w.Header(), w.buffer.Bytes(), r)
	header := w.Header()
	header.Del("Content-Length")
	header.Del("Content-Disposition")
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	w.ResponseWriter.WriteHeader(w.status)
	if r.Method != http.MethodHead {
		_, _ = w.ResponseWriter.Write(body)
	}
}

// normalizeAPIErrorBody converts one error response into the envelope while
// preserving extra fields that handlers return (for example `current` on a
// version conflict or a specific legacy `code`).
func normalizeAPIErrorBody(status int, header http.Header, raw []byte, r *http.Request) []byte {
	contentType := header.Get("Content-Type")
	fields := map[string]any{}
	message := ""
	trimmed := bytes.TrimSpace(raw)
	if strings.Contains(strings.ToLower(contentType), "json") && len(trimmed) > 0 && trimmed[0] == '{' {
		if err := json.Unmarshal(trimmed, &fields); err != nil {
			fields = map[string]any{}
		}
	}
	switch value := fields["error"].(type) {
	case string:
		message = value
	case map[string]any:
		if text, ok := value["message"].(string); ok {
			message = text
		}
	}
	if message == "" {
		if text, ok := fields["message"].(string); ok {
			message = text
		}
	}
	if message == "" && len(fields) == 0 {
		message = strings.TrimSpace(string(trimmed))
	}
	if message == "" {
		message = http.StatusText(status)
	}
	requestID := requestIDFrom(r.Context())
	if status == http.StatusInternalServerError {
		// A 500 is never a message written for the client: whatever the
		// handler put in the body is logged and replaced, extra fields too.
		if message != internalErrorMessage {
			slog.ErrorContext(r.Context(), "internal error response", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "error", message)
		}
		code, _ := fields["code"].(string)
		fields = map[string]any{}
		if code = strings.TrimSpace(code); code != "" && code != apiErrorInternal && len(code) <= 64 && !strings.ContainsAny(code, " :") {
			fields["code"] = code
		}
		message = internalErrorMessage
	}
	category := apiErrorTypeFor(status, message)
	fields["error"] = message
	fields["type"] = category
	if code, ok := fields["code"].(string); !ok || strings.TrimSpace(code) == "" {
		fields["code"] = category
	}
	if requestID != "" {
		fields["requestId"] = requestID
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		encoded, _ = json.Marshal(apiErrorBody{Error: message, Code: category, Type: category})
	}
	return append(encoded, '\n')
}
