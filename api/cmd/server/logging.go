package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

type requestIDKey struct{}
type serverRequestIDKey struct{}
type requestLoggerKey struct{}

func serverRequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(serverRequestIDKey{}).(string)
	return id
}

func requestLoggerFrom(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(requestLoggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

func initLogging() {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_LOG_FORMAT")), "json") {
		handler = slog.NewJSONHandler(os.Stdout, options)
	} else {
		handler = slog.NewTextHandler(os.Stdout, options)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	// Keep legacy log.Printf calls container-visible while they are migrated.
	log.SetOutput(os.Stdout)
	log.SetFlags(0)
	slog.Info("logging initialized", "format", logFormat(), "level", level.String())
}

func logFormat() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_LOG_FORMAT")), "json") {
		return "json"
	}
	return "text"
}

func requestLog(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverRequestID := newRequestID()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = serverRequestID
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = context.WithValue(ctx, serverRequestIDKey{}, serverRequestID)
		logger := slog.With("request_id", serverRequestID)
		ctx = context.WithValue(ctx, requestLoggerKey{}, logger)
		started := time.Now()
		next.ServeHTTP(w, r.WithContext(ctx))
		// Match a method-qualified registration, since authentication may
		// have cloned r and mux redirects can supply a path as the pattern.
		var route string
		if mux != nil {
			_, pattern := mux.Handler(r)
			if method, path, ok := strings.Cut(pattern, " "); ok && !strings.HasPrefix(method, "/") && strings.HasPrefix(path, "/") {
				route = pattern
			}
		}
		logger.InfoContext(ctx, "http request", "route", route, "status", "completed", "duration_ms", time.Since(started).Seconds()*1000)
	})
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(raw[:])
}
