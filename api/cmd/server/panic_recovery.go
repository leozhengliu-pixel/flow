package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
)

// panicValueLimit bounds the panic message in the log line.
const panicValueLimit = 512

// recoverPanics turns a handler panic into a logged error and, when nothing
// was sent yet, a JSON 500 in the standard error envelope. It sits right
// inside the security header middleware, so the 500 still carries the
// security headers.
//
// The log line has the request id, method, path (no query string), the panic
// value and the stack; never headers or bodies. A response that already
// started (an SSE stream, a download) is aborted with http.ErrAbortHandler so
// the client sees a broken response rather than a truncated success. A
// hijacked connection (WebSocket) belongs to its handler and is left alone.
// http.ErrAbortHandler itself is re-panicked untouched: it is how a handler
// asks net/http to abort quietly.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &panicRecoveryWriter{ResponseWriter: w}
		defer func() {
			value := recover()
			if value == nil {
				return
			}
			if err, ok := value.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(value)
			}
			// requestLog, inside this middleware, sets the id on the response.
			requestID := w.Header().Get("X-Request-ID")
			slog.ErrorContext(r.Context(), "http handler panic", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "panic", panicSummary(value), "stack", string(debug.Stack()))
			switch {
			case writer.hijacked:
				return
			case writer.wroteHeader:
				panic(http.ErrAbortHandler)
			}
			writePanicResponse(w, requestID)
		}()
		next.ServeHTTP(writer, r)
	})
}

func panicSummary(value any) string {
	text := fmt.Sprint(value)
	if len(text) > panicValueLimit {
		text = text[:panicValueLimit] + "…"
	}
	return text
}

// writePanicResponse drops what the handler put in the header (a stream's
// content type, a length, an encoding, cookies) and writes the envelope.
// Headers outer middleware set (request id, CORS, Vary) are kept.
func writePanicResponse(w http.ResponseWriter, requestID string) {
	header := w.Header()
	for name := range header {
		if name == "X-Request-Id" || name == "Vary" || strings.HasPrefix(name, "Access-Control-") {
			continue
		}
		delete(header, name)
	}
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(apiErrorBody{Error: "Internal server error", Code: apiErrorInternal, Type: apiErrorInternal, RequestID: requestID})
}

// panicRecoveryWriter records whether the response started or the
// connection was taken over.
type panicRecoveryWriter struct {
	http.ResponseWriter
	wroteHeader bool
	hijacked    bool
}

func (w *panicRecoveryWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *panicRecoveryWriter) WriteHeader(status int) {
	if status >= 200 {
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *panicRecoveryWriter) Write(p []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(p)
}

func (w *panicRecoveryWriter) ReadFrom(source io.Reader) (int64, error) {
	w.wroteHeader = true
	if from, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return from.ReadFrom(source)
	}
	return io.Copy(w.ResponseWriter, source)
}

func (w *panicRecoveryWriter) Flush() {
	w.wroteHeader = true
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *panicRecoveryWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffered, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, buffered, err
}
