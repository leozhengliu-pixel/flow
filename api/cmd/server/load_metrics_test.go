//go:build loadtest

package main

import (
	"sync/atomic"
	"time"
)

type scaleLoadStreams struct {
	connections atomic.Int64
	disconnects atomic.Int64
	reconnects  atomic.Int64
}

type scaleLoadTransport struct {
	peakWaiters [2]int32
	waitSamples [2]int
}

func (m *scaleLoadTransport) sample(peers peerServers) {
	for i, server := range peers.servers {
		waiting := server.realtime.budget.waiters.Load()
		m.peakWaiters[i] = max(m.peakWaiters[i], waiting)
		if waiting > 0 {
			m.waitSamples[i]++
		}
	}
}

func (m *scaleLoadTransport) report(peers peerServers) any {
	values := make([]map[string]any, len(peers.servers))
	for i, server := range peers.servers {
		budget := server.realtime.budget
		values[i] = map[string]any{"limitBytes": budget.limit, "peakBytes": budget.peak.Load(), "peakWaiters": m.peakWaiters[i], "waitingSamples": m.waitSamples[i]}
	}
	return values
}

func scalePropagationSummary(changes int, values []time.Duration) map[string]any {
	var overOneSecond, overFiveSeconds int
	for _, value := range values {
		if value > time.Second {
			overOneSecond++
		}
		if value > 5*time.Second {
			overFiveSeconds++
		}
	}
	return map[string]any{
		"changes": changes, "deliveries": len(values), "over1s": overOneSecond, "over5s": overFiveSeconds,
		"mins": percentile(values, 0).Seconds(), "p05s": percentile(values, 0.05).Seconds(), "p25s": percentile(values, 0.25).Seconds(),
		"p50s": percentile(values, 0.5).Seconds(), "p95s": percentile(values, 0.95).Seconds(), "maxs": percentile(values, 1).Seconds(),
	}
}
