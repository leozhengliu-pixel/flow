//go:build loadtest

package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"flow/api/internal/store"
)

// The bounded-metadata load rig's metrics listener (scripts/loadrig). Only a
// -tags loadtest build has it, and only when FLOW_LOADRIG_METRICS_ADDR names
// a loopback address: a separate listener, never a route of the API. It
// serves /metrics (JSON) and /debug/pprof/ (goroutine, heap, CPU profiles).

type loadRigProcessMetrics struct {
	PID                         int              `json:"pid"`
	At                          time.Time        `json:"at"`
	HeapAllocBytes              uint64           `json:"heapAllocBytes"`
	HeapInuseBytes              uint64           `json:"heapInuseBytes"`
	HeapSysBytes                uint64           `json:"heapSysBytes"`
	SysBytes                    uint64           `json:"sysBytes"`
	NumGC                       uint32           `json:"numGC"`
	PauseTotalNanos             uint64           `json:"pauseTotalNanos"`
	Goroutines                  int              `json:"goroutines"`
	MaxRSSBytes                 int64            `json:"maxRSSBytes"`
	CPUUserSeconds              float64          `json:"cpuUserSeconds"`
	CPUSystemSeconds            float64          `json:"cpuSystemSeconds"`
	BoundedMetadata             bool             `json:"boundedMetadata"`
	Counters                    map[string]int64 `json:"counters"`
	GOMAXPROCS                  int              `json:"gomaxprocs"`
	MemoryLimitConfiguredString string           `json:"gomemlimit,omitempty"`
}

func init() {
	addr := os.Getenv("FLOW_LOADRIG_METRICS_ADDR")
	if addr == "" {
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		log.Printf("load rig: FLOW_LOADRIG_METRICS_ADDR must be a loopback host:port; metrics listener off")
		return
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("load rig: metrics listener: %v", err)
		return
	}
	// One function handler, so the listener adds no API route binding.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodGet:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		case r.URL.Path == "/metrics":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(loadRigSnapshot())
		// Profiles of the replica under load (loopback, load-test builds only).
		case r.URL.Path == "/debug/pprof/profile":
			pprof.Profile(w, r)
		case r.URL.Path == "/debug/pprof/trace":
			pprof.Trace(w, r)
		case strings.HasPrefix(r.URL.Path, "/debug/pprof/"):
			pprof.Index(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	go func() { _ = http.Serve(listener, handler) }()
}

func loadRigSnapshot() loadRigProcessMetrics {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	maxRSS := usage.Maxrss
	if runtime.GOOS != "darwin" {
		maxRSS *= 1024
	}
	return loadRigProcessMetrics{
		PID: os.Getpid(), At: time.Now().UTC(),
		HeapAllocBytes: memory.HeapAlloc, HeapInuseBytes: memory.HeapInuse, HeapSysBytes: memory.HeapSys, SysBytes: memory.Sys,
		NumGC: memory.NumGC, PauseTotalNanos: memory.PauseTotalNs, Goroutines: runtime.NumGoroutine(), MaxRSSBytes: maxRSS,
		CPUUserSeconds:              float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6,
		CPUSystemSeconds:            float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6,
		BoundedMetadata:             os.Getenv("FLOW_BOUNDED_METADATA") == "on",
		Counters:                    store.LoadRigColdCounters(),
		GOMAXPROCS:                  runtime.GOMAXPROCS(0),
		MemoryLimitConfiguredString: os.Getenv("GOMEMLIMIT"),
	}
}
