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

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID = newRequestID()
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		started := time.Now()
		next.ServeHTTP(w, r.WithContext(ctx))
		slog.InfoContext(ctx, "http request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", "completed", "duration_ms", time.Since(started).Seconds()*1000)
	})
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(raw[:])
}
