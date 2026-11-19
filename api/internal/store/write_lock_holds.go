package store

import (
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Write-lock holds by writer (load measurement). A live store's write lock
// (coldWriteMutex.local) records, when it is taken, the calling frames; when
// it is returned, the hold is added to the writer those frames name. HOT
// writers wait for whichever writer holds the lock, so the holders with the
// largest total explain the HOT lock waits. The cost is one runtime.Callers
// per acquisition.

// writeLockHoldFrames is how many caller frames identify a holder.
const writeLockHoldFrames = 12

type writeLockHolder [writeLockHoldFrames]uintptr

type writeLockHoldStats struct{ holds, nanos, longest atomic.Int64 }

var writeLockHolds sync.Map // writeLockHolder -> *writeLockHoldStats

func noteWriteLockHold(holder *writeLockHolder, started time.Time) {
	held := int64(time.Since(started))
	value, ok := writeLockHolds.Load(*holder)
	if !ok {
		value, _ = writeLockHolds.LoadOrStore(*holder, new(writeLockHoldStats))
	}
	stats := value.(*writeLockHoldStats)
	stats.holds.Add(1)
	stats.nanos.Add(held)
	storeMaxInt64(&stats.longest, held)
}

// writeLockHoldSkipped are frames of the lock and of the shared mutation
// plumbing, which name no writer.
var writeLockHoldSkipped = []string{
	"store.(*coldWriteMutex).", "sync.(*Mutex).", "store.mutationAttempts", "store.(*SQLiteStore).mutationAttempt",
	"store.(*SQLiteStore).withWorkspaceLock", "runtime.",
}

// writeLockHolderName names a holder by its first three frames outside the
// lock plumbing (innermost first, package prefixes dropped).
func writeLockHolderName(holder writeLockHolder) string {
	pcs := holder[:]
	if i := slices.Index(pcs, 0); i >= 0 {
		pcs = pcs[:i]
	}
	frames := runtime.CallersFrames(pcs)
	names := []string{}
	prefix := ""
	if first, _ := runtime.CallersFrames(pcs).Next(); strings.Contains(first.Function, "(*coldWriteMutex).") {
		prefix = "COLD " // a COLD facade took the shared lock
	}
	for len(names) < 3 {
		frame, more := frames.Next()
		name := frame.Function
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if i := strings.Index(name, ".func"); i >= 0 {
			name = name[:i] // a closure is named by its function
		}
		skip := name == ""
		for _, prefix := range writeLockHoldSkipped {
			if strings.HasPrefix(name, prefix) {
				skip = true
			}
		}
		if !skip {
			name = strings.TrimPrefix(strings.TrimPrefix(name, "store."), "(*SQLiteStore).")
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		if !more {
			break
		}
	}
	if len(names) == 0 {
		return prefix + "unknown"
	}
	return prefix + strings.Join(names, "<")
}

// WriteLockHoldsSnapshot reports the write-lock holds by writer name: count,
// total nanoseconds and the longest hold.
func WriteLockHoldsSnapshot() map[string][3]int64 {
	result := map[string][3]int64{}
	writeLockHolds.Range(func(key, value any) bool {
		stats := value.(*writeLockHoldStats)
		name := writeLockHolderName(key.(writeLockHolder))
		previous := result[name]
		result[name] = [3]int64{previous[0] + stats.holds.Load(), previous[1] + stats.nanos.Load(), max(previous[2], stats.longest.Load())}
		return true
	})
	return result
}
