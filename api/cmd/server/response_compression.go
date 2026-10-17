package main

import (
	"bufio"
	"compress/gzip"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

var responseGzipPool = sync.Pool{New: func() any { writer, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed); return writer }}

func acceptsGzip(value string) bool {
	wildcard := false
	for _, item := range strings.Split(value, ",") {
		parts := strings.Split(item, ";")
		encoding, quality := strings.TrimSpace(parts[0]), 1.0
		for _, parameter := range parts[1:] {
			if key, value, ok := strings.Cut(strings.TrimSpace(parameter), "="); ok && strings.EqualFold(key, "q") {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if strings.EqualFold(encoding, "gzip") {
			return quality > 0
		}
		if encoding == "*" {
			wildcard = quality > 0
		}
	}
	return wildcard
}

// Compress JSON incrementally without buffering a workspace snapshot. Streaming
// events, upgraded connections and already encoded assets retain their transport.
func compressAPIResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Upgrade") != "" || r.Header.Get("Range") != "" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		writer := &compressedResponse{ResponseWriter: w}
		defer func() {
			if writer.gzip != nil {
				_ = writer.gzip.Close()
				responseGzipPool.Put(writer.gzip)
			}
		}()
		next.ServeHTTP(writer, r)
	})
}

type compressedResponse struct {
	http.ResponseWriter
	gzip   *gzip.Writer
	status int
	// static selects text assets by content type instead of API JSON.
	static bool
}

// Text formats worth compressing; images, fonts, video and wasm are already dense or rarely help.
func compressibleStaticType(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "text/html", "text/css", "text/javascript", "application/javascript", "application/json", "application/manifest+json", "image/svg+xml", "text/plain", "text/xml", "application/xml":
		return true
	}
	return false
}

// compressStaticResponse gzips text assets served by the SPA file handler. Range requests are served as-is.
func compressStaticResponse(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, func()) {
	w.Header().Add("Vary", "Accept-Encoding")
	if r.Header.Get("Range") != "" || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		return w, func() {}
	}
	writer := &compressedResponse{ResponseWriter: w, static: true}
	return writer, func() {
		if writer.gzip != nil {
			_ = writer.gzip.Close()
			responseGzipPool.Put(writer.gzip)
		}
	}
}

func (w *compressedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *compressedResponse) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	compressible := strings.HasPrefix(w.Header().Get("Content-Type"), "application/json")
	if w.static {
		compressible = status == http.StatusOK && compressibleStaticType(w.Header().Get("Content-Type"))
	}
	if status != 204 && status != 304 && compressible && w.Header().Get("Content-Encoding") == "" && !strings.Contains(w.Header().Get("Cache-Control"), "no-transform") {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		w.gzip = responseGzipPool.Get().(*gzip.Writer)
		w.gzip.Reset(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *compressedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.gzip != nil {
		return w.gzip.Write(p)
	}
	return w.ResponseWriter.Write(p)
}
func (w *compressedResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.gzip != nil {
		_ = w.gzip.Flush()
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *compressedResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
