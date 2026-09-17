package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIResponseCompressionNegotiationAndTransport(t *testing.T) {
	for _, test := range []struct {
		name, accept, contentType, encoding string
		status                              int
		compressed                          bool
	}{
		{"json", "gzip", "application/json; charset=utf-8", "", 200, true},
		{"explicit refusal", "gzip;q=0, *;q=1", "application/json", "", 200, false},
		{"wildcard", "br, *;q=0.5", "application/json", "", 200, true},
		{"identity", "identity", "application/json", "", 200, false},
		{"SSE", "gzip", "text/event-stream", "", 200, false},
		{"already encoded", "gzip", "application/json", "br", 200, false},
		{"not modified", "gzip", "application/json", "", 304, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := strings.Repeat(`{"description":"Repeated application content"}`, 2000)
			handler := compressAPIResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				if test.encoding != "" {
					w.Header().Set("Content-Encoding", test.encoding)
				}
				w.WriteHeader(test.status)
				if test.status != 304 {
					_, _ = io.WriteString(w, body)
				}
				w.(http.Flusher).Flush()
			}))
			r := httptest.NewRequest("GET", "/api/issue-records/bootstrap", nil)
			r.Header.Set("Accept-Encoding", test.accept)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if (w.Header().Get("Content-Encoding") == "gzip") != test.compressed {
				t.Fatal(w.Header())
			}
			if test.compressed {
				reader, err := gzip.NewReader(w.Body)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				if string(decoded) != body {
					t.Fatal("compressed response corrupt")
				}
			} else if test.status != 304 && w.Body.String() != body {
				t.Fatal("uncompressed stream changed")
			}
		})
	}
}

func TestAPICompressionReducesLargeBootstrap(t *testing.T) {
	body := strings.Repeat(`{"name":"Project","description":"large repeated text","team":"test"}`, 200000)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/issue-records/bootstrap", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	compressAPIResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})).ServeHTTP(w, r)
	if w.Body.Len() > len(body)/5 {
		t.Fatalf("compression too small: %d -> %d", len(body), w.Body.Len())
	}
	t.Logf("JSON transport bytes %d -> %d", len(body), w.Body.Len())
}
