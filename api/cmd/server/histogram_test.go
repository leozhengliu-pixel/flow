//go:build loadtest

package main

import (
	"math"
	"math/bits"
	"testing"
	"time"

	"flow/api/internal/store"
)

// Fixed memory, one microsecond resolution below 1ms, at most 0.8% bucket
// rounding above it. Reports use the bucket's upper bound, conservatively.
type scaleP01Histogram struct {
	bins [8192]int
	n    int
}

func (h *scaleP01Histogram) add(value time.Duration) {
	x := uint64(max(value.Microseconds(), 0))
	if value%time.Microsecond != 0 {
		x++
	}
	i := int(x)
	if x >= 1024 {
		exponent := bits.Len64(x) - 1
		i = 1024 + (exponent-10)*128 + int((x-(uint64(1)<<exponent))>>(exponent-7))
	}
	h.bins[min(i, len(h.bins)-1)]++
	h.n++
}
func (h *scaleP01Histogram) rank(rank int) time.Duration {
	if h.n == 0 {
		return 0
	}
	for i, count := range h.bins {
		rank -= count
		if rank <= 0 {
			x := uint64(i)
			if i >= 1024 {
				exponent := 10 + (i-1024)/128
				mantissa := (i - 1024) % 128
				x = (uint64(1) << exponent) + uint64(mantissa+1)*(uint64(1)<<(exponent-7))
			}
			return time.Duration(x) * time.Microsecond
		}
	}
	return 0
}
func (h *scaleP01Histogram) quantile(p float64) time.Duration {
	return h.rank(max(1, int(math.Ceil(float64(h.n)*p))))
}
func (h *scaleP01Histogram) merge(other *scaleP01Histogram) {
	for i, n := range other.bins {
		h.bins[i] += n
	}
	h.n += other.n
}

type scaleP01DeliverySummary struct {
	changes, expected, received, invalid, failed int
	success, all                                 scaleP01Histogram
}

func (s *scaleP01DeliverySummary) observe(delivery scaleP01Delivery, target, received map[int]time.Time, now time.Time, timeout time.Duration) {
	s.changes++
	for client, expected := range delivery.expected {
		if !expected {
			continue
		}
		s.expected++
		if at := received[client]; !at.IsZero() && !at.Before(delivery.commit.At) && !at.After(now) {
			s.received++
		}
		at, ok := target[client]
		if ok && !at.Before(delivery.commit.At) && !at.After(now) && at.Sub(delivery.commit.At) <= timeout {
			s.success.add(at.Sub(delivery.commit.At))
			s.all.add(at.Sub(delivery.commit.At))
		} else {
			if ok && (at.Before(delivery.commit.At) || at.After(now)) {
				s.invalid++
			}
			s.failed++
			s.all.add(max(timeout, now.Sub(delivery.commit.At)))
		}
	}
}
func (s *scaleP01DeliverySummary) merge(other *scaleP01DeliverySummary) {
	s.changes += other.changes
	s.expected += other.expected
	s.received += other.received
	s.invalid += other.invalid
	s.failed += other.failed
	s.success.merge(&other.success)
	s.all.merge(&other.all)
}
func (s *scaleP01DeliverySummary) report() map[string]any {
	slo := map[string]any{"denominator": s.expected, "completed": s.success.n, "failed": s.failed, "failureTreatment": "+infinity", "precision": "upper bucket bound; <=0.8% or 1us rounding"}
	for _, q := range []struct {
		name string
		p    float64
	}{{"p50", .5}, {"p95", .95}, {"p99", .99}, {"max", 1}} {
		var value any
		rank := max(1, int(math.Ceil(float64(s.expected)*q.p)))
		if s.expected > 0 && rank <= s.success.n {
			value = s.success.rank(rank).Seconds()
		}
		slo[q.name] = value
	}
	return map[string]any{"changes": s.changes, "expected": s.expected, "deliveries": s.success.n, "received": s.received, "missing": s.expected - s.received, "unappliedOrLate": s.failed, "invalidAcks": s.invalid, "p50s": s.all.quantile(.5).Seconds(), "p95s": s.all.quantile(.95).Seconds(), "maxs": s.all.quantile(1).Seconds(), "legacyPercentilesIncludeFailurePenalty": true, "sloLatencySeconds": slo}
}

func (l *scaleP01Ledger) target(phase string) map[string]map[int]time.Time {
	if phase == "received" {
		return l.received
	}
	if phase == "browser" {
		return l.browser
	}
	return l.applied
}

func (l *scaleP01Ledger) compact(now time.Time, timeout time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, delivery := range l.changes {
		if now.Sub(delivery.commit.At) <= timeout {
			continue
		}
		for _, phase := range []string{"received", "applied", "browser"} {
			if l.rollups[phase] == nil {
				l.rollups[phase] = map[string]*scaleP01DeliverySummary{}
			}
			if l.rollups[phase][delivery.kind] == nil {
				l.rollups[phase][delivery.kind] = &scaleP01DeliverySummary{}
			}
			l.rollups[phase][delivery.kind].observe(delivery, l.target(phase)[id], l.received[id], now, timeout)
		}
		delete(l.changes, id)
		delete(l.received, id)
		delete(l.applied, id)
		delete(l.browser, id)
		l.retired[id] = true
	}
	// Unrelated frames and uncorrelated early acknowledgements have a bounded
	// lifetime too; a later commit still contributes its full denominator.
	for _, target := range []map[string]map[int]time.Time{l.received, l.applied, l.browser} {
		for id, acks := range target {
			if _, ok := l.changes[id]; ok {
				continue
			}
			old := true
			for _, at := range acks {
				if now.Sub(at) <= timeout {
					old = false
					break
				}
			}
			if old {
				delete(target, id)
			}
		}
	}
}

func TestScaleP01HistogramAndCompaction(t *testing.T) {
	for _, value := range []time.Duration{0, time.Nanosecond, 900 * time.Microsecond, time.Millisecond, 50 * time.Millisecond, time.Second, time.Minute, time.Hour} {
		h := scaleP01Histogram{}
		h.add(value)
		got := h.quantile(.95)
		if got < value || got-value > max(time.Microsecond, value/125) {
			t.Fatalf("histogram %s -> %s", value, got)
		}
	}
	l := newScaleP01Ledger()
	now := time.Now()
	l.commit(store.ScaleP01Commit{ID: "a", At: now}, "document", []bool{true, true})
	l.ack(scaleP01Ack{"a", 0, now.Add(time.Second)}, "received")
	l.ack(scaleP01Ack{"a", 0, now.Add(2 * time.Second)}, "applied")
	before := l.report(now.Add(30*time.Second), 10*time.Second, "applied")
	l.compact(now.Add(30*time.Second), 10*time.Second)
	after := l.report(now.Add(30*time.Second), 10*time.Second, "applied")
	for _, field := range []string{"expected", "deliveries", "missing", "unappliedOrLate", "p95s"} {
		if before[field] != after[field] {
			t.Fatalf("compaction changed %s", field)
		}
	}
	if len(l.changes) != 0 || len(l.applied) != 0 {
		t.Fatal("completed fan-out retained per-client acknowledgements")
	}
}
