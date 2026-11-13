package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP15BHTTPSpoolCleanup(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "p15b-signing-fixture")
	for _, path := range []string{"legacy", "paged", "signed"} {
		for _, budget := range []int{0, 64 << 10, 2 << 20} {
			for _, mode := range []string{"success", "error", "cancel", "panic"} {
				t.Run(fmt.Sprintf("%s/%d/%s", path, budget, mode), func(t *testing.T) {
					dir := t.TempDir()
					t.Setenv("TMPDIR", dir)
					t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(budget))
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					failure := errors.New("writer failed")
					w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
						switch mode {
						case "error":
							return failure
						case "cancel":
							cancel()
						case "panic":
							panic(failure)
						}
						return nil
					}}
					s := &server{}
					r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/issue-records/bootstrap", nil)
					if path == "signed" {
						r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
					}
					producer := http.HandlerFunc(func(out http.ResponseWriter, request *http.Request) {
						if path == "legacy" {
							writeBootstrapJSON(out, domain.Bootstrap{Projects: []domain.Project{{Description: strings.Repeat("x", 256<<10)}}})
						} else {
							writeStreamedJSON(request.Context(), out, http.StatusOK, struct {
								Body string `json:"body"`
							}{strings.Repeat("x", 256<<10)})
						}
					})
					var handler http.Handler = s.signedFileURLs(producer)
					if path == "legacy" {
						handler = serializeLegacyBootstrap(producer)
					}
					panicked := false
					func() {
						defer func() {
							if recovered := recover(); recovered != nil {
								panicked = true
								if mode == "success" || (mode == "panic" && recovered != failure) || (mode != "panic" && recovered != http.ErrAbortHandler) {
									t.Errorf("unexpected panic: %v", recovered)
								}
							}
						}()
						handler.ServeHTTP(w, r)
					}()
					if mode != "success" && !panicked {
						t.Fatal("missing abort/panic")
					}
					if mode == "success" && w.bytes < 256<<10 {
						t.Fatal("response was truncated")
					}
					files, err := os.ReadDir(dir)
					if err != nil || len(files) != 0 {
						t.Fatalf("leaked files: %v %v", files, err)
					}
					// A leak in the shared byte budget would deny this exact-sized write.
					t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "1")
					file, err := store.NewRecordSpool()
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					if _, err := file.Write([]byte("x")); err != nil {
						t.Fatalf("spool reservation leaked: %v", err)
					}
				})
			}
		}
	}
}

func TestScaleP15BConcurrentLargeResponsesShareStoreBudget(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	const limit = 256 << 10
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(limit))
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	// A canonical-record spool consumes half the same allowance as HTTP.
	held, err := store.NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err := held.Write(make([]byte, limit/2)); err != nil {
		t.Fatal(err)
	}
	check := func() {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Error(err)
			return
		}
		var bytes int64
		for _, file := range files {
			if info, err := file.Info(); err == nil {
				bytes += info.Size()
			}
		}
		if bytes > limit {
			t.Errorf("process spool exceeded limit: %d > %d", bytes, limit)
		}
	}
	var finished, started sync.WaitGroup
	finished.Add(6)
	started.Add(6)
	for i := range 6 {
		go func() {
			defer finished.Done()
			started.Done()
			started.Wait()
			w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
				check()
				return nil
			}}
			r := httptest.NewRequest(http.MethodGet, "/api/issue-records/bootstrap", nil)
			producer := http.HandlerFunc(func(out http.ResponseWriter, request *http.Request) {
				writeStreamedJSON(request.Context(), out, http.StatusOK, struct {
					Body string `json:"body"`
				}{strings.Repeat("x", 4<<20)})
			})
			if i%2 == 0 {
				serializeLegacyBootstrap(producer).ServeHTTP(w, r)
			} else {
				(&server{}).signedFileURLs(producer).ServeHTTP(w, r)
			}
			if w.bytes != (4<<20)+12 || w.max > 64<<10 {
				t.Errorf("incomplete/unbounded direct output: bytes=%d chunk=%d", w.bytes, w.max)
			}
		}()
	}
	finished.Wait()
	check()
	_ = held.Close()
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("concurrent downloads leaked temporary files")
	}
}

func TestScaleP15BUnavailableTemporaryDirectory(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	for _, handler := range []http.Handler{
		serializeLegacyBootstrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeBootstrapJSON(w, domain.Bootstrap{}) })),
		(&server{}).signedFileURLs(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeStreamedJSON(r.Context(), w, 200, domain.Bootstrap{})
		})),
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/issue-records/bootstrap", nil))
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("TMPDIR failure became HTTP failure: %d %.100s", w.Code, w.Body.Bytes())
		}
	}
}

func TestScaleP15BSigningKeepsNonJSONHeaders(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		body := `"/uploads/plain.png"`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/plain", nil)
		r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
		(&server{}).signedFileURLs(http.HandlerFunc(func(out http.ResponseWriter, _ *http.Request) {
			out.Header().Set("Content-Type", "text/plain")
			out.Header().Set("Content-Length", strconv.Itoa(len(body)))
			out.WriteHeader(status)
			_, _ = io.WriteString(out, body)
		})).ServeHTTP(w, r)
		if w.Code != status || w.Body.String() != body || w.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
			t.Fatal("non-JSON body or headers changed")
		}
	}
}

func TestScaleP15BSigningSurvivesSpoolFallback(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "p15b-signing-fixture")
	t.Setenv("TMPDIR", t.TempDir())
	for _, budget := range []int{0, 64 << 10, 4 << 20} {
		t.Run(strconv.Itoa(budget), func(t *testing.T) {
			t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(budget))
			for _, kind := range []string{"bootstrap", "paged", "json", "raw-json"} {
				t.Run(kind, func(t *testing.T) {
					data := domain.Bootstrap{Projects: []domain.Project{{Name: "/uploads/name.png", Description: "![before](/uploads/a%20b.png) " + strings.Repeat("history x ", 1<<15) + " /uploads/after.png"}}}
					var original bytes.Buffer
					out := httptest.NewRecorder()
					encode := func(w http.ResponseWriter, ctx context.Context) {
						switch kind {
						case "bootstrap":
							writeBootstrapJSON(w, data)
						case "paged":
							writeStreamedJSON(ctx, w, 200, data)
						case "json":
							writeJSON(w, 200, data)
						case "raw-json":
							w.Header().Set("Content-Type", "application/json")
							_ = json.NewEncoder(w).Encode(data)
						}
					}
					encode(out, t.Context())
					original.Write(out.Body.Bytes())
					var signing *hydratedURLSigning
					r := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
					r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
					got := httptest.NewRecorder()
					(&server{}).signedFileURLs(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
						signing, _ = request.Context().Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
						encode(w, request.Context())
					})).ServeHTTP(got, r)
					if signing == nil {
						t.Fatal("signing was unavailable")
					}
					want := rewriteUploadURLs(original.Bytes(), signing.key, signing.base, signing.expires)
					if !bytes.Equal(got.Body.Bytes(), want) {
						t.Fatalf("signing changed with budget=%d kind=%s: got %.300s want %.300s", budget, kind, got.Body.Bytes(), want)
					}
				})
			}
		})
	}
}

func TestScaleP15BProductionMiddlewareSignsWithoutSpool(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	const signingKey = "p15b-production-middleware-fixture"
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", signingKey)
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			prefix := strings.Repeat("body x ", 32<<10) + " ![image]("
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p15b.seed", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{ID: "p15b-signed", Name: "Signed", TeamIDs: []string{f.data.Teams[0].ID}, Description: prefix + "/uploads/p15b.png)"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			t.Setenv("TMPDIR", dir)
			for _, budget := range []int{0, 64 << 10, 2 << 20} {
				t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(budget))
				for _, path := range []string{"/api/bootstrap", "/api/issue-records/bootstrap", "/api/projects/p15b-signed"} {
					r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.host.URL+path, nil)
					if err != nil {
						t.Fatal(err)
					}
					r.Header.Set("X-Workspace-Key", workspace)
					r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
					response, err := f.admin.Do(r)
					if err != nil {
						t.Fatal(err)
					}
					raw, err := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if err != nil || response.StatusCode != http.StatusOK {
						t.Fatalf("%s budget=%d: status=%d read=%v", path, budget, response.StatusCode, err)
					}
					var project domain.Project
					if path == "/api/projects/p15b-signed" {
						err = json.Unmarshal(raw, &project)
					} else {
						var data domain.Bootstrap
						err = json.Unmarshal(raw, &data)
						for _, item := range data.Projects {
							if item.ID == "p15b-signed" {
								project = item
							}
						}
					}
					if err != nil || !strings.HasPrefix(project.Description, prefix) || !strings.HasSuffix(project.Description, ")") {
						t.Fatalf("%s lost the signed project body: %v", path, err)
					}
					link := strings.TrimSuffix(strings.TrimPrefix(project.Description, prefix), ")")
					parsed, err := url.Parse(link)
					if err != nil {
						t.Fatal(err)
					}
					expires, err := strconv.ParseInt(parsed.Query().Get("expires"), 10, 64)
					want := signedFileURL([]byte(signingKey), f.host.URL, "p15b.png", time.Unix(expires, 0))
					if err != nil || link != want {
						t.Fatalf("%s budget=%d: URL signature changed", path, budget)
					}
				}
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("production middleware leaked spools: %v %v", files, err)
			}
		})
	}
}

func TestScaleP15BSignedStringSourceDifferential(t *testing.T) {
	signing := &hydratedURLSigning{key: []byte("key"), base: "https://flow.test", expires: time.Unix(1900000000, 0)}
	ctx := context.WithValue(t.Context(), hydratedURLSigningKey{}, signing)
	binary, err := base64.StdEncoding.DecodeString("/uploads/abc")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{
		"/uploads/" + strings.Repeat("long", 1<<18),
		strings.Repeat("x", 32764) + " /uploads/first.png then /uploads/second.png",
		"https://assets.test/uploads/file%2Fname.png",
		"/uploads/bad%ZZ.png <>&\n\u2028\xff /api/files/uploads/untouched",
		json.RawMessage(`{"body":"/uploads/one.png","raw":"<>&","escaped":"quote\"/uploads/two.png","value":1.00e3}`),
		binary,
		domain.AgentToolCall{ID: "partial", Result: json.RawMessage("raw /uploads/result.png\n"), Arguments: json.RawMessage(`{`)},
	} {
		var original, got bytes.Buffer
		plain := bufio.NewWriter(&original)
		if err := encodeHydratedJSON(t.Context(), plain, reflect.ValueOf(value), 0); err != nil {
			t.Fatal(err)
		}
		_ = plain.Flush()
		buffer := bufio.NewWriter(&got)
		if err := encodeHydratedJSON(ctx, buffer, reflect.ValueOf(value), 0); err != nil {
			t.Fatal(err)
		}
		_ = buffer.Flush()
		want := rewriteUploadURLs(original.Bytes(), signing.key, signing.base, signing.expires)
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("signed source differs for %T: got %.200s want %.200s", value, got.Bytes(), want)
		}
	}
}

func TestScaleP15BPermissionDifferentialWithNoSpooling(t *testing.T) {
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "0")
	TestScaleP15PermissionDifferential(t)
}

func TestScaleP15BSignedLargeHistoryEncodingIsBounded(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	text := strings.Repeat("history x ", 1<<18) + " /uploads/picture.png"
	signing := &hydratedURLSigning{key: []byte("key"), base: "https://flow.test", expires: time.Unix(1900000000, 0)}
	ctx := context.WithValue(t.Context(), hydratedURLSigningKey{}, signing)
	for _, value := range []any{text, json.RawMessage(`{"history":"` + text + `"}`), domain.AgentToolCall{ID: "partial", Result: json.RawMessage(text)}} {
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			w := &scaleP15CountingWriter{header: make(http.Header)}
			result := testing.Benchmark(func(b *testing.B) {
				for range b.N {
					buffer := bufio.NewWriterSize(w, 64<<10)
					if err := encodeHydratedJSON(ctx, buffer, reflect.ValueOf(value), 0); err != nil {
						b.Fatal(err)
					}
					if err := buffer.Flush(); err != nil {
						b.Fatal(err)
					}
				}
			})
			if result.AllocedBytesPerOp() > 2<<20 || w.max > 64<<10 || w.bytes < len(text) {
				t.Fatalf("signed encoding: %d B/op, max chunk %d, output %d", result.AllocedBytesPerOp(), w.max, w.bytes)
			}
			t.Logf("signed large history: %d B/op; max chunk %d", result.AllocedBytesPerOp(), w.max)
		})
	}
}

var _ io.Writer = (*store.RecordSpool)(nil)
