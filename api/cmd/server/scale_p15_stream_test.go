package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP15JSONByteDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	values := []any{streamTestBootstrap(), domain.Bootstrap{}, struct {
		Strings []string                   `json:"strings"`
		Raw     json.RawMessage            `json:"raw"`
		Bytes   []byte                     `json:"bytes"`
		Map     map[string]json.RawMessage `json:"map"`
	}{[]string{"\x00\x01\b\f\n\r\t\\\"<>&\u2028\u2029", "\xff\xfe", "text"}, json.RawMessage(" { \"x\" : \"<>&\u2028\u2029\\\\\\\"\" , \"n\": 1.00e3 } "), []byte("binary\x00\xff"), map[string]json.RawMessage{"b": nil, "a": json.RawMessage("[ 1, 2 ]")}}}
	for _, value := range values {
		want, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		got := httptest.NewRecorder()
		writeStreamedJSON(t.Context(), got, http.StatusOK, value)
		if !bytes.Equal(got.Body.Bytes(), append(want, '\n')) {
			t.Fatalf("JSON differs for %T\n got %.800s\nwant %.800s", value, got.Body.Bytes(), want)
		}
	}
	for _, call := range []domain.AgentToolCall{{ID: "empty"}, {ID: "partial", Arguments: json.RawMessage(`{"partial":`), Result: json.RawMessage("text & <result>\u2028\xff")}, {ID: "valid", Arguments: json.RawMessage(`{"argument":true}`), Result: json.RawMessage(`{"result":"<>&"}`)}} {
		want, err := json.Marshal(call)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		buffer := bufio.NewWriter(&output)
		if err := encodeHydratedJSON(t.Context(), buffer, reflect.ValueOf(&call), 0); err != nil {
			t.Fatal(err)
		}
		_ = buffer.Flush()
		if !bytes.Equal(want, output.Bytes()) {
			t.Fatalf("tool call bytes differ: got %s want %s", output.Bytes(), want)
		}
	}
	for _, data := range []domain.Bootstrap{streamTestBootstrap(), {}, {Issues: []domain.Issue{}, Comments: map[string][]domain.Comment{"one": nil}, Activities: map[string][]domain.ActivityEvent{}, UserSettings: map[string]domain.UserSettings{}, NotificationPreferences: map[string]domain.NotificationPreferences{}, APIKeys: []domain.APIKey{}}} {
		for _, override := range []map[string]json.RawMessage{nil, {"workspace": json.RawMessage(`{"id":"override"}`), "issues": json.RawMessage(`[]`), "extra": json.RawMessage(`true`)}} {
			old, got := httptest.NewRecorder(), httptest.NewRecorder()
			var overrides []map[string]json.RawMessage
			if override != nil {
				overrides = append(overrides, override)
			}
			t.Setenv("FLOW_RECORD_HYDRATION", "off")
			writeBootstrapWithFields(old, data, nil, overrides...)
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			writeBootstrapWithFields(got, data, nil, overrides...)
			if !bytes.Equal(old.Body.Bytes(), got.Body.Bytes()) {
				t.Fatalf("legacy field bytes differ\n got %.800s\nwant %.800s", got.Body.Bytes(), old.Body.Bytes())
			}
		}
	}
}

type scaleP15CountingWriter struct {
	header  http.Header
	bytes   int
	max     int
	onWrite func() error
}

func (w *scaleP15CountingWriter) Header() http.Header { return w.header }
func (w *scaleP15CountingWriter) WriteHeader(int)     {}
func (w *scaleP15CountingWriter) Write(p []byte) (int, error) {
	if w.onWrite != nil {
		if err := w.onWrite(); err != nil {
			return 0, err
		}
	}
	w.bytes += len(p)
	w.max = max(w.max, len(p))
	return len(p), nil
}

func TestScaleP15HugeHistoryEncodingIsBounded(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	value := struct {
		History map[string][]string `json:"history"`
		Payload json.RawMessage     `json:"payload"`
	}{map[string][]string{"document": {strings.Repeat("history <>&\n", 1<<18)}}, json.RawMessage(`{"body":"` + strings.Repeat("body", 1<<18) + `"}`)}
	w := &scaleP15CountingWriter{header: make(http.Header)}
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			writeStreamedJSON(context.Background(), w, http.StatusOK, value)
		}
	})
	if result.AllocedBytesPerOp() > 2<<20 || w.max > 64<<10 || w.bytes < 4<<20 {
		t.Fatalf("encoding: %d bytes/op, max write %d, output %d", result.AllocedBytesPerOp(), w.max, w.bytes)
	}
	t.Logf("large history: %d B/op; largest output chunk %d", result.AllocedBytesPerOp(), w.max)
}

func TestScaleP15SignedURLFileDifferential(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	key, expires := []byte("p15-test-key"), time.Unix(1900000000, 0)
	bodies := []string{
		`{"url":"/uploads/image.png","text":"![x](/uploads/test%20file.png) /uploads/a.png?x=y","api":"/api/files/uploads/token /api/mcp/uploads/token"}`,
		`{"a":"https://assets.test/uploads/file%2Fname.png","b":"/uploads/bad%ZZ.png","c":"/uploads/x&<.png","d":"https://assets.test/uploads/a%FF"}`,
		`{"raw":"` + strings.Repeat("x", 32765) + `/uploads/chunk.png","long":"/uploads/` + strings.Repeat("long", 1<<18) + `"}`,
		`{"a":"/uploads/empty%","b":"/uploads/percent%0","c":"/uploads/a%20b","d":"/uploads/a%00b","e":"/uploads/` + strings.Repeat("x", 192) + `?query"}`,
	}
	for _, base := range []string{"https://flow.test", "https://flow.test/\"<>&/"} {
		for i, body := range bodies {
			t.Run(fmt.Sprintf("%s/%d", base, i), func(t *testing.T) {
				source, err := newLegacySpool()
				if err != nil {
					t.Fatal(err)
				}
				defer removeLegacySpool(source)
				if _, err := io.WriteString(source, body); err != nil {
					t.Fatal(err)
				}
				var got bytes.Buffer
				if err := rewriteUploadURLFile(t.Context(), source, &got, key, base, expires); err != nil {
					t.Fatal(err)
				}
				want := rewriteUploadURLs([]byte(body), key, base, expires)
				if !bytes.Equal(got.Bytes(), want) {
					t.Fatalf("signed bytes differ at fixture %d\n got %.500s\nwant %.500s", i, got.Bytes(), want)
				}
			})
		}
	}
}

func scaleP15ExpectAbort(t *testing.T, run func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("abort = %v", got)
		}
	}()
	run()
	t.Fatal("response was not aborted")
}

func TestScaleP15DetachedDownloadReleasesProducer(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	budget := &realtimeBudget{limit: 2 << 20}
	done := false
	w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
		if !done {
			t.Fatal("socket write pinned producer state")
		}
		if budget.used.Load() == 0 {
			t.Fatal("in-flight chunk has no transport lease")
		}
		return nil
	}}
	ctx := context.WithValue(t.Context(), legacyTransportBudgetKey{}, budget)
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/bootstrap", nil)
	serializeLegacyBootstrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { done = true }()
		writeBootstrapJSON(w, domain.Bootstrap{Projects: []domain.Project{{ID: "large", Description: strings.Repeat("body", 1<<20)}}})
	})).ServeHTTP(w, r)
	if w.bytes < 4<<20 || w.max > 64<<10 || budget.used.Load() != 0 {
		t.Fatalf("download chunks=%d bytes=%d budget=%d", w.max, w.bytes, budget.used.Load())
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatalf("spool files leaked: %v", files)
	}
}

func TestScaleP15DetachedPagedBootstrapReleasesProducer(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("TMPDIR", t.TempDir())
	done := false
	s := &server{realtime: &realtimeHub{budget: &realtimeBudget{limit: 2 << 20}}}
	w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
		if !done {
			t.Fatal("paged bootstrap retained the producer while sending")
		}
		return nil
	}}
	s.signedFileURLs(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		defer func() { done = true }()
		writeStreamedJSON(r.Context(), out, http.StatusOK, streamTestBootstrap())
	})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/issue-records/bootstrap", nil))
	if w.bytes == 0 || s.realtime.budget.used.Load() != 0 {
		t.Fatal("paged bootstrap did not release its transport lease")
	}
}

func TestScaleP15DownloadCancellationAndPanicRelease(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	for _, mode := range []string{"producer-panic", "cancelled-client", "failed-client"} {
		t.Run(mode, func(t *testing.T) {
			budget := &realtimeBudget{limit: 2 << 20}
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), legacyTransportBudgetKey{}, budget))
			defer cancel()
			w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
				if mode == "cancelled-client" {
					cancel()
					return nil
				}
				return errors.New("client disconnected")
			}}
			handler := serializeLegacyBootstrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if mode == "producer-panic" {
					panic(http.ErrAbortHandler)
				}
				writeBootstrapJSON(w, domain.Bootstrap{Projects: []domain.Project{{Description: strings.Repeat("x", 1<<20)}}})
			}))
			scaleP15ExpectAbort(t, func() { handler.ServeHTTP(w, httptest.NewRequestWithContext(ctx, "GET", "/api/bootstrap", nil)) })
			files, _ := os.ReadDir(dir)
			if len(files) != 0 || budget.used.Load() != 0 {
				t.Fatalf("leaked files=%d budget=%d", len(files), budget.used.Load())
			}
		})
	}
}

func TestScaleP15SigningKeepsNonJSONStreaming(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("TMPDIR", t.TempDir()+"/does-not-exist")
	s := &server{}
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusNoContent} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/events", nil)
		r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
		s.signedFileURLs(http.HandlerFunc(func(out http.ResponseWriter, _ *http.Request) {
			out.Header().Set("Content-Type", "text/event-stream")
			out.WriteHeader(status)
			if status == http.StatusNoContent {
				return
			}
			_, _ = io.WriteString(out, "data: /uploads/untouched.png\n\n")
			out.(http.Flusher).Flush()
			if w.Body.Len() == 0 || !w.Flushed {
				t.Fatal("non-JSON response was buffered")
			}
		})).ServeHTTP(w, r)
		if w.Code != status || strings.Contains(w.Body.String(), "/api/files/signed/") {
			t.Fatal("non-JSON response changed")
		}
	}
}

func TestScaleP15SigningBeyondLegacyBufferLimit(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "p15-fixture-signing-key")
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	s := &server{realtime: &realtimeHub{budget: &realtimeBudget{limit: 2 << 20}}}
	var prefix bytes.Buffer
	w := &scaleP15CountingWriter{header: make(http.Header)}
	w.onWrite = func() error { return nil }
	output := &scaleP15PrefixWriter{scaleP15CountingWriter: w, prefix: &prefix}
	r := httptest.NewRequest("GET", "/api/bootstrap", nil)
	r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
	s.signedFileURLs(http.HandlerFunc(func(out http.ResponseWriter, _ *http.Request) {
		out.Header().Set("Content-Type", "application/json")
		out.Header().Set("Content-Length", "123")
		_, _ = io.WriteString(out, `{"url":"/uploads/image.png","body":"`)
		chunk := strings.Repeat("x", 32<<10)
		for range 1050 {
			_, _ = io.WriteString(out, chunk)
		}
		_, _ = io.WriteString(out, `"}`)
	})).ServeHTTP(output, r)
	if !strings.Contains(prefix.String(), "/api/files/signed/image.png?") || w.bytes <= 32<<20 || w.max > 64<<10 || w.Header().Get("Content-Length") != "" || s.realtime.budget.used.Load() != 0 {
		t.Fatalf("large signed response: signed=%v bytes=%d chunk=%d length=%s lease=%d", strings.Contains(prefix.String(), "/api/files/signed/"), w.bytes, w.max, w.Header().Get("Content-Length"), s.realtime.budget.used.Load())
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("signed response leaked temporary files")
	}
}

type scaleP15PrefixWriter struct {
	*scaleP15CountingWriter
	prefix *bytes.Buffer
}

func (w *scaleP15PrefixWriter) Write(p []byte) (int, error) {
	if w.prefix.Len() < 1024 {
		_, _ = w.prefix.Write(p[:min(len(p), 1024-w.prefix.Len())])
	}
	return w.scaleP15CountingWriter.Write(p)
}

func TestScaleP15CancelledJSONStopsWithinChunk(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	ctx, cancel := context.WithCancel(t.Context())
	w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error { cancel(); return nil }}
	scaleP15ExpectAbort(t, func() {
		writeStreamedJSON(ctx, w, http.StatusOK, struct {
			Body string `json:"body"`
		}{strings.Repeat("x", 4<<20)})
	})
	if w.bytes > 64<<10 {
		t.Fatalf("cancelled encoder wrote %d bytes", w.bytes)
	}
	var dst bytes.Buffer
	buffer := bufio.NewWriter(&dst)
	if err := encodeHydratedJSON(ctx, buffer, reflect.ValueOf("stopped"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled string = %v", err)
	}
}
