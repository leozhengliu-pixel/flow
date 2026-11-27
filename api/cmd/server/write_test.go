//go:build loadtest

package main

import (
	"runtime"
	"testing"
	"time"

	"flow/api/internal/store"
)

type scaleWriteCost struct {
	writeCost
	statements   int64
	cpu, elapsed float64
}

func scaleMeasureWrite(t *testing.T, peers peerServers, step func()) scaleWriteCost {
	t.Helper()
	sqlCount := func() int64 {
		var n int64
		for _, repo := range peers.stores {
			if value, ok := scaleProbes.Load(repo); ok {
				sql := value.(*store.ScaleProbe).SQLSnapshot()
				n += sql.Statements
			}
		}
		return n
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	decodes, statements, cpu, began := peers.metadataDecodes(), sqlCount(), scaleCPUTime(), time.Now()
	step()
	elapsed, cpuAfter := time.Since(began), scaleCPUTime()
	runtime.ReadMemStats(&after)
	return scaleWriteCost{writeCost: writeCost{peers.metadataDecodes() - decodes, after.TotalAlloc - before.TotalAlloc}, statements: sqlCount() - statements, cpu: cpuAfter.UserSeconds + cpuAfter.SystemSeconds - cpu.UserSeconds - cpu.SystemSeconds, elapsed: elapsed.Seconds()}
}

func (cost *scaleWriteCost) add(next scaleWriteCost) {
	cost.decodes += next.decodes
	cost.bytes += next.bytes
	cost.statements += next.statements
	cost.cpu += next.cpu
	cost.elapsed += next.elapsed
}

func scaleWriteMetrics(cost scaleWriteCost, writes int) map[string]any {
	n := float64(writes)
	return map[string]any{"statementsPerWrite": float64(cost.statements) / n, "cpuSecondsPerWrite": cost.cpu / n, "meanElapsedMs": cost.elapsed / n * 1000, "scope": "whole process, both replicas; forced GC excluded"}
}
