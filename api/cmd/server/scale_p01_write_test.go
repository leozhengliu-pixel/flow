//go:build loadtest

package main

import (
	"runtime"
	"testing"
	"time"

	"flow/api/internal/store"
)

type scaleP01WriteCost struct {
	writeCost
	statements   int64
	cpu, elapsed float64
}

func scaleP01MeasureWrite(t *testing.T, peers peerServers, step func()) scaleP01WriteCost {
	t.Helper()
	sqlCount := func() int64 {
		var n int64
		for _, repo := range peers.stores {
			if value, ok := scaleP01Probes.Load(repo); ok {
				sql := value.(*store.ScaleP01Probe).SQLSnapshot()
				n += sql.Statements
			}
		}
		return n
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	decodes, statements, cpu, began := peers.metadataDecodes(), sqlCount(), scaleP01CPUTime(), time.Now()
	step()
	elapsed, cpuAfter := time.Since(began), scaleP01CPUTime()
	runtime.ReadMemStats(&after)
	return scaleP01WriteCost{writeCost: writeCost{peers.metadataDecodes() - decodes, after.TotalAlloc - before.TotalAlloc}, statements: sqlCount() - statements, cpu: cpuAfter.UserSeconds + cpuAfter.SystemSeconds - cpu.UserSeconds - cpu.SystemSeconds, elapsed: elapsed.Seconds()}
}

func (cost *scaleP01WriteCost) add(next scaleP01WriteCost) {
	cost.decodes += next.decodes
	cost.bytes += next.bytes
	cost.statements += next.statements
	cost.cpu += next.cpu
	cost.elapsed += next.elapsed
}

func scaleP01WriteMetrics(cost scaleP01WriteCost, writes int) map[string]any {
	n := float64(writes)
	return map[string]any{"statementsPerWrite": float64(cost.statements) / n, "cpuSecondsPerWrite": cost.cpu / n, "meanElapsedMs": cost.elapsed / n * 1000, "scope": "whole process, both replicas; forced GC excluded"}
}
