//go:build loadtest

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"

	"flow/api/internal/coordination"
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
	diagnoseWorkspaceRefusal = loadRigWorkspaceRefusal
	diagnoseWorkspaceReload = func(start time.Time, versionWait time.Duration, trace store.WorkspaceReloadTrace, err error) {
		log.Printf("freshness reload=%d elapsed_ms=%d version_ms=%d delta_attempts=%d delta_races=%d delta_ms=%d full_attempts=%d full_ms=%d install_wait_ms=%d locked_reload_ms=%d ok=%t",
			start.UnixNano(), time.Since(start).Milliseconds(), versionWait.Milliseconds(), trace.DeltaAttempts, trace.DeltaRaces, trace.DeltaBuild.Milliseconds(), trace.FullAttempts, trace.FullBuild.Milliseconds(), trace.InstallWait.Milliseconds(), trace.LockedReload.Milliseconds(), err == nil)
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

func loadRigWorkspaceRefusal(s *server, r *http.Request, workspace string, err error, waited time.Duration) {
	pattern := "unmatched"
	if mux, ok := r.Context().Value(coldAuthorizationMuxKey{}).(*http.ServeMux); ok {
		if _, matched := mux.Handler(r); matched != "" {
			pattern = matched
		}
	}
	s.workspaceVersions.mu.Lock()
	load := s.workspaceVersions.loading[workspace]
	applied := s.workspaceVersions.applied[workspace]
	s.workspaceVersions.mu.Unlock()
	var reload, elapsed int64
	if load != nil {
		reload, elapsed = load.started.UnixNano(), time.Since(load.started).Milliseconds()
	}
	ctx, cancel := context.WithTimeout(s.store.WorkerContext(), workspaceFreshnessProbeTimeout)
	defer cancel()
	state, stateErr := s.store.WorkspacePeerState(ctx, workspace)
	shared, sharedErr := s.sharedWorkspaceVersion(ctx, workspace)
	log.Printf("freshness refusal route=%q deadline=%t wait_ms=%d reload=%d reload_elapsed_ms=%d sql=%t journal_head=%d policy_head=%d revision=%d installed_revision=%d redis=%t redis_version=%d applied_version=%d",
		pattern, errors.Is(err, context.DeadlineExceeded), waited.Milliseconds(), reload, elapsed, stateErr == nil, state.Head, state.InstalledPolicyHead, state.MetadataRevision, state.InstalledRevision, sharedErr == nil, shared, applied)
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
	counters := store.LoadRigColdCounters()
	if counters == nil {
		counters = map[string]int64{}
	}
	counters["bootstrapEncodingHits"] = bootstrapEncodingHits.Load()
	counters["bootstrapEncodingStores"] = bootstrapEncodingStores.Load()
	// The shared Redis cache tier (docs/redis-cache.md).
	for name, value := range coordination.SharedCacheCounters() {
		counters[name] = value
	}
	return loadRigProcessMetrics{
		PID: os.Getpid(), At: time.Now().UTC(),
		HeapAllocBytes: memory.HeapAlloc, HeapInuseBytes: memory.HeapInuse, HeapSysBytes: memory.HeapSys, SysBytes: memory.Sys,
		NumGC: memory.NumGC, PauseTotalNanos: memory.PauseTotalNs, Goroutines: runtime.NumGoroutine(), MaxRSSBytes: maxRSS,
		CPUUserSeconds:              float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6,
		CPUSystemSeconds:            float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6,
		BoundedMetadata:             os.Getenv("FLOW_BOUNDED_METADATA") == "on",
		Counters:                    counters,
		GOMAXPROCS:                  runtime.GOMAXPROCS(0),
		MemoryLimitConfiguredString: os.Getenv("GOMEMLIMIT"),
	}
}
