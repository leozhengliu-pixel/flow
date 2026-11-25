package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// An opt-in output-path measurement: four readers of one resident 10x
// workspace. Include the fixture in the heap peak, but exclude its creation
// from request latency. No HTTP recorder retains the response bytes.
func TestScaleP15BMeasureConcurrent10xBootstrap(t *testing.T) {
	if os.Getenv("FLOW_P15B_MEASURE") != "on" {
		t.Skip("set FLOW_P15B_MEASURE=on for the four-reader 10x measurement")
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	signed := os.Getenv("FLOW_P15B_MEASURE_SIGNED") == "on"
	if signed {
		t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "p15b-measurement-signing-key")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	records, copies := 23210, 0
	if value := os.Getenv("FLOW_P15B_MEASURE_RECORDS"); value != "" {
		var err error
		records, err = strconv.Atoi(value)
		if err != nil || records < 1 || records > 232100 {
			t.Fatal("FLOW_P15B_MEASURE_RECORDS must be between 1 and 232100")
		}
	}
	if value := os.Getenv("FLOW_P15B_MEASURE_COPIES"); value != "" {
		var err error
		copies, err = strconv.Atoi(value)
		if err != nil || copies < 0 || copies > 2 {
			t.Fatal("FLOW_P15B_MEASURE_COPIES must be between 0 and 2")
		}
	}
	bodyBytes := 490 * (1 << 20) / records
	if records != 23210 {
		// Keep the count-growth response near 490 MiB including JSON fields.
		empty, err := json.Marshal(domain.Project{ID: fmt.Sprint(records - 1), Name: "Project"})
		if err != nil {
			t.Fatal(err)
		}
		bodyBytes -= len(empty) - 2
	}
	data := domain.Bootstrap{Projects: make([]domain.Project, records)}
	for i := range data.Projects {
		data.Projects[i] = domain.Project{ID: fmt.Sprint(i), Name: "Project", Description: strings.Repeat("x", bodyBytes)}
		if signed && i%128 == 0 {
			data.Projects[i].Description += " /uploads/asset.png"
		}
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peakHeap, peakSpool atomic.Uint64
	peakHeap.Store(before.HeapAlloc)
	stop, stopped := make(chan struct{}), make(chan struct{})
	sample := func() {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		for old := peakHeap.Load(); old < mem.HeapAlloc && !peakHeap.CompareAndSwap(old, mem.HeapAlloc); old = peakHeap.Load() {
		}
		var size uint64
		files, _ := os.ReadDir(dir)
		for _, file := range files {
			if info, err := os.Stat(filepath.Join(dir, file.Name())); err == nil {
				size += uint64(info.Size())
			}
		}
		for old := peakSpool.Load(); old < size && !peakSpool.CompareAndSwap(old, size); old = peakSpool.Load() {
		}
	}
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
	var readers, firstWrites sync.WaitGroup
	readers.Add(4)
	firstWrites.Add(4)
	start := make(chan struct{})
	latencies := make([]time.Duration, 4)
	outputs := make([]int, 4)
	service := &server{}
	for i := range 4 {
		go func() {
			defer readers.Done()
			<-start
			firstWrites.Done()
			firstWrites.Wait()
			began := time.Now()
			first := true
			w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
				if first {
					first = false
					sample()
				}
				return nil
			}}
			r := httptest.NewRequest(http.MethodGet, "/api/issue-records/bootstrap", nil)
			if signed {
				r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
			}
			service.signedFileURLs(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
				owned := data
				var snapshots [][]domain.Project
				for range copies {
					owned.Projects = slices.Clone(owned.Projects)
					snapshots = append(snapshots, owned.Projects)
				}
				writeStreamedJSON(r.Context(), out, http.StatusOK, owned)
				runtime.KeepAlive(snapshots)
			})).ServeHTTP(w, r)
			latencies[i], outputs[i] = time.Since(began), w.bytes
		}()
	}
	close(start)
	readers.Wait()
	close(stop)
	<-stopped
	runtime.KeepAlive(data)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	for _, output := range outputs {
		if output < 490<<20 || output != outputs[0] {
			t.Fatalf("incomplete output: %v", outputs)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("spools after completion: %v, %v", files, err)
	}
	t.Logf("P15B_MEASURE signed=%t records=%d copies_per_reader=%d body_bytes=%d readers=4 response_bytes=%d peak_spool_bytes=%d resident_heap_bytes=%d peak_heap_bytes=%d max_latency_ms=%d", signed, records, copies, bodyBytes, outputs[0], peakSpool.Load(), before.HeapAlloc, peakHeap.Load(), latencies[3].Milliseconds())
}
