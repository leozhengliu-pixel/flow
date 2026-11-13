package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Run in a fresh process per mode. The fixture remains resident; blocked
// socket writes expose producer retention without retaining response bytes.
func TestScaleP15HeapConcurrent10x(t *testing.T) {
	if os.Getenv("FLOW_P15_HEAP_MEASURE") != "on" {
		t.Skip("set FLOW_P15_HEAP_MEASURE=on for the 10x retained-heap probe")
	}
	records := 23210
	if value := os.Getenv("FLOW_P15B_MEASURE_RECORDS"); value != "" {
		var err error
		records, err = strconv.Atoi(value)
		if err != nil || records < 1 || records > 232100 {
			t.Fatal("invalid record count")
		}
	}
	copies := 2
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	data := domain.Bootstrap{Projects: make([]domain.Project, records)}
	bodyBytes := 490 * (1 << 20) / records
	if records != 23210 {
		empty, err := json.Marshal(domain.Project{ID: fmt.Sprint(records - 1), Name: "Project"})
		if err != nil {
			t.Fatal(err)
		}
		bodyBytes -= len(empty) - 2
	}
	for i := range data.Projects {
		data.Projects[i] = domain.Project{ID: fmt.Sprint(i), Name: "Project", Description: strings.Repeat("x", bodyBytes)}
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peakHeap, peakRetained, peakSpool atomic.Uint64
	peakHeap.Store(before.HeapAlloc)
	peakRetained.Store(before.HeapAlloc)
	maximum := func(target *atomic.Uint64, value uint64) {
		for old := target.Load(); old < value && !target.CompareAndSwap(old, value); old = target.Load() {
		}
	}
	sample := func() {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		maximum(&peakHeap, mem.HeapAlloc)
		var bytes uint64
		files, _ := os.ReadDir(dir)
		for _, file := range files {
			if info, err := os.Stat(filepath.Join(dir, file.Name())); err == nil {
				bytes += uint64(info.Size())
			}
		}
		maximum(&peakSpool, bytes)
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	var readers sync.WaitGroup
	readers.Add(4)
	start := make(chan struct{})
	latencies := make([]time.Duration, 4)
	outputs := make([]int, 4)
	digests := make([]string, 4)
	service := &server{}
	var profileMu sync.Mutex
	for i := range 4 {
		go func() {
			defer readers.Done()
			<-start
			began := time.Now()
			first := true
			digest := sha256.New()
			w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
				if first {
					first = false
					// Let the other encoders reach their first socket write.
					time.Sleep(200 * time.Millisecond)
					profileMu.Lock()
					runtime.GC()
					var mem runtime.MemStats
					runtime.ReadMemStats(&mem)
					if mem.HeapAlloc > peakRetained.Load() {
						peakRetained.Store(mem.HeapAlloc)
						if path := os.Getenv("FLOW_P15_HEAP_PROFILE"); path != "" {
							file, err := os.Create(path)
							if err != nil {
								t.Error(err)
							} else {
								if err := pprof.WriteHeapProfile(file); err != nil {
									t.Error(err)
								}
								_ = file.Close()
							}
						}
					}
					profileMu.Unlock()
				}
				return nil
			}}
			r := httptest.NewRequest(http.MethodGet, "/api/issue-records/bootstrap", nil)
			service.signedFileURLs(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
				owned := data
				var snapshots [][]domain.Project
				for range copies {
					owned.Projects = slices.Clone(owned.Projects)
					snapshots = append(snapshots, owned.Projects)
				}
				writeStreamedJSON(r.Context(), out, http.StatusOK, owned)
				runtime.KeepAlive(snapshots)
			})).ServeHTTP(scaleP15HeapHashWriter{w, digest}, r)
			latencies[i], outputs[i] = time.Since(began), w.bytes
			digests[i] = fmt.Sprintf("%x", digest.Sum(nil))
		}()
	}
	close(start)
	readers.Wait()
	close(stop)
	<-stopped
	runtime.KeepAlive(data)
	for _, output := range outputs {
		if output < 490<<20 || output != outputs[0] {
			t.Fatalf("incomplete output: %v", outputs)
		}
	}
	for _, digest := range digests {
		if digest != digests[0] {
			t.Fatalf("response digests differ: %v", digests)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("spools after completion: %v %v", files, err)
	}
	slices.Sort(latencies)
	t.Logf("P15_HEAP hydration=%s records=%d copies_per_reader=%d readers=4 response_bytes=%d resident_heap_bytes=%d retained_heap_bytes=%d peak_heap_bytes=%d peak_spool_bytes=%d max_latency_ms=%d sha256=%s", os.Getenv("FLOW_RECORD_HYDRATION"), records, copies, outputs[0], before.HeapAlloc, peakRetained.Load(), peakHeap.Load(), peakSpool.Load(), latencies[3].Milliseconds(), digests[0])
	if os.Getenv("FLOW_RECORD_HYDRATION") == "on" && peakRetained.Load() > 1<<30 {
		t.Fatalf("retained heap exceeds the 1 GiB live-heap target: %d", peakRetained.Load())
	}
}

type scaleP15HeapHashWriter struct {
	http.ResponseWriter
	digest interface{ Write([]byte) (int, error) }
}

func (w scaleP15HeapHashWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	_, _ = w.digest.Write(p[:n])
	return n, err
}

func TestScaleP15HeapDownloadsShareAdmission(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "p15-heap-test-key")
	for _, spoolBytes := range []string{"0", "32768", "268435456"} {
		t.Run(spoolBytes, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", spoolBytes)
			t.Setenv("TMPDIR", t.TempDir())
			var calls atomic.Int32
			producer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				writeStreamedJSON(r.Context(), w, http.StatusOK, domain.Bootstrap{Projects: []domain.Project{{Description: strings.Repeat("body ", 16<<10) + " /uploads/asset.png"}}})
			})
			gate := make(chan struct{}, 1)
			mux := http.NewServeMux()
			mux.Handle("GET /api/bootstrap", serializeLegacyBootstrap(producer, gate))
			mux.Handle("GET /api/exports/test/download", serializeLegacyBootstrap(producer, gate))
			mux.Handle("GET /api/issue-records/bootstrap", producer)
			handler := (&server{}).signedFileURLs(mux)
			blocked, drained, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(drained) }) }
			t.Cleanup(release)
			first := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/bootstrap", nil)
			first.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
			go func() {
				defer close(done)
				held := false
				w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
					if !held {
						held = true
						close(blocked)
						select {
						case <-drained:
						case <-t.Context().Done():
							return t.Context().Err()
						}
					}
					return nil
				}}
				handler.ServeHTTP(w, first)
			}()
			select {
			case <-blocked:
			case <-time.After(5 * time.Second):
				t.Fatal("first download did not reach the socket")
			}
			for _, path := range []string{"/api/bootstrap", "/api/issue-records/bootstrap", "/api/exports/test/download"} {
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
				r := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				cancel()
				if w.Code != http.StatusServiceUnavailable || calls.Load() != 1 {
					t.Fatalf("%s reached the producer before admission: status=%d calls=%d", path, w.Code, calls.Load())
				}
			}
			// Small resource requests remain available during a slow download.
			w := httptest.NewRecorder()
			boundHydratedLegacyDownloads(producer).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/projects/test", nil))
			if w.Code != http.StatusOK || calls.Load() != 2 {
				t.Fatal("download admission blocked an ordinary resource read")
			}
			release()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("nested signing/bootstrap admission deadlocked")
			}
			for _, path := range []string{"/api/issue-records/bootstrap", "/api/bootstrap", "/api/exports/test/download"} {
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
				cancel()
				if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) {
					t.Fatalf("%s did not recover after the slow download: status=%d", path, w.Code)
				}
			}
		})
	}
}

func TestScaleP15HeapAdmissionOffKeepsConcurrentProducers(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(2)
	handler := boundHydratedLegacyDownloads(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer readers.Wait()
	defer close(release)
	for range 2 {
		go func() {
			defer readers.Done()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/issue-records/bootstrap", nil))
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("flag-off producers no longer run concurrently")
		}
	}
}

func TestScaleP15HeapAdmissionReleasesAfterPanic(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	r := httptest.NewRequest(http.MethodGet, "/api/issue-records/bootstrap", nil)
	scaleP15ExpectAbort(t, func() {
		boundHydratedLegacyDownloads(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic(http.ErrAbortHandler)
		})).ServeHTTP(httptest.NewRecorder(), r)
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	boundHydratedLegacyDownloads(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(w, r.WithContext(ctx))
	if w.Code != http.StatusNoContent {
		t.Fatal("producer panic leaked download admission")
	}
}

type scaleP15HeapCustomInt int64

func (scaleP15HeapCustomInt) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

type scaleP15HeapPointerInt int64

func (*scaleP15HeapPointerInt) MarshalJSON() ([]byte, error) { return []byte(`"pointer"`), nil }

func TestScaleP15HeapScalarJSONDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	values := []any{
		struct {
			Signed   []int64                  `json:"signed"`
			Unsigned []uint64                 `json:"unsigned"`
			Bool     []bool                   `json:"bool"`
			Time     []time.Time              `json:"time"`
			Custom   []scaleP15HeapCustomInt  `json:"custom"`
			Pointer  []scaleP15HeapPointerInt `json:"pointer"`
		}{
			Signed: []int64{0, -1, 1<<63 - 1, -1 << 63}, Unsigned: []uint64{0, 1, 1<<64 - 1}, Bool: []bool{false, true},
			Time:   []time.Time{{}, time.Date(1, 1, 1, 0, 0, 0, 1, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 123456789, time.FixedZone("test", -7*3600))},
			Custom: []scaleP15HeapCustomInt{42}, Pointer: []scaleP15HeapPointerInt{42},
		},
		struct {
			Numbers map[string]any `json:"numbers"`
		}{map[string]any{"int8": int8(-128), "int16": int16(-32768), "int32": int32(-2147483648), "int": int(-1), "uint8": uint8(255), "uint16": uint16(65535), "uint32": uint32(4294967295), "uint": uint(1), "uintptr": uintptr(2)}},
	}
	for _, value := range values {
		want, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		writeStreamedJSON(t.Context(), w, http.StatusOK, value)
		if !bytes.Equal(w.Body.Bytes(), append(want, '\n')) {
			t.Fatalf("scalar JSON differs: got %s want %s", w.Body.Bytes(), want)
		}
	}
	for _, value := range []time.Time{time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.FixedZone("invalid", 24*3600))} {
		var got bytes.Buffer
		_, wantErr := json.Marshal(value)
		gotErr := encodeHydratedJSON(t.Context(), bufio.NewWriter(&got), reflect.ValueOf(value), 0)
		if wantErr == nil || gotErr == nil || wantErr.Error() != gotErr.Error() {
			t.Fatalf("time error differs: got %v want %v", gotErr, wantErr)
		}
	}
}
