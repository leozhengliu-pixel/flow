package store

import (
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The memory ceiling of bounded metadata (FLOW_METADATA_MEMORY_BYTES).
//
// Bounded metadata keeps a replica's memory under a ceiling, and spends what
// lies below it on speed: decoded images of installed generations, admitted
// request work, resident policy, row caches and concurrent COLD readers. One
// setting states the ceiling; every tier's budget is derived from it, and the
// tier's own variable, when set, overrides the derived value:
//
//	tier                          share of the ceiling   override
//	decoded images                35%                   FLOW_COLD_DECODE_CACHE_BYTES
//	admitted request work         25%                   FLOW_METADATA_WORK_BYTES
//	resident policy               1/16                  FLOW_METADATA_POLICY_BYTES
//	resident row cache            1/32                  -
//	installed source descriptors  1/32                  FLOW_METADATA_SOURCE_BYTES
//	bootstrap encodings           1/16                  -
//	COLD slots, legacy-view lanes one per GiB, 1 to 4     FLOW_COLD_SLOTS, FLOW_COLD_VIEW_LANES
//	COLD projections              one per 512 MiB, 2 to 8 FLOW_COLD_PROJECTION_CONCURRENCY
//	spool disk                    the ceiling (at least 1 GiB, disk not memory)
//	                                                      FLOW_METADATA_SOURCE_DISK_BYTES
//
// The remainder leaves headroom for request scratch, encoding and other
// subsystems. The automatic plan uses 70% of available memory and honors a
// lower GOMEMLIMIT. The Go runtime's soft memory limit also honors the plan.
//
// Tests use a 1 GiB automatic plan for reproducible admission limits.

const (
	metadataMemoryFloor        = 1 << 30
	metadataMemoryLimitPercent = 70
)

var metadataMemoryBase = sync.OnceValues(func() (int64, string) {
	if testing.Testing() {
		return metadataMemoryFloor, "floor"
	}
	return defaultMetadataMemory(cgroupMemoryLimit(), physicalMemoryBytes(), debug.SetMemoryLimit(-1))
})

func defaultMetadataMemory(cgroup, physical, runtimeLimit int64) (int64, string) {
	hard, source := physical, "physical memory"
	if cgroup > 0 && (hard == 0 || cgroup <= hard) {
		hard, source = cgroup, "cgroup"
	}
	ceiling := int64(metadataMemoryFloor)
	if hard > 0 {
		// Keep headroom even in a container smaller than the usual floor.
		ceiling = min(max(hard/100*metadataMemoryLimitPercent, ceiling), hard-hard/5)
	} else {
		source = "floor"
	}
	if runtimeLimit > 0 && runtimeLimit < ceiling {
		return runtimeLimit, "GOMEMLIMIT"
	}
	return ceiling, source
}

// metadataMemoryCeiling is the replica's bounded-metadata memory ceiling and
// where it came from. An explicit FLOW_METADATA_MEMORY_BYTES (a positive
// whole number of bytes) is used as given.
func metadataMemoryCeiling() (int64, string) {
	if raw := strings.TrimSpace(os.Getenv("FLOW_METADATA_MEMORY_BYTES")); raw != "" {
		if value, err := strconv.ParseInt(raw, 10, strconv.IntSize); err == nil && value > 0 {
			return value, "FLOW_METADATA_MEMORY_BYTES"
		}
	}
	return metadataMemoryBase()
}

func metadataMemoryShare(numerator, denominator int64) int64 {
	ceiling, _ := metadataMemoryCeiling()
	return max(ceiling/denominator*numerator, 1)
}

// metadataMemoryCount is one per perBytes of the ceiling, between 1 and most.
func metadataMemoryCount(perBytes int64, most int) int {
	ceiling, _ := metadataMemoryCeiling()
	return int(min(max(ceiling/perBytes, 1), int64(most)))
}

// metadataWorkBudgetBytes is the process work budget: how much admitted
// work concurrent operations may hold together. Individual operations
// retain their existing metadataWorkBytes() checks.
func metadataWorkBudgetBytes() int {
	return int(min(metadataBudgetBytes("FLOW_METADATA_WORK_BYTES", metadataMemoryShare(1, 4)), 1<<46))
}

func metadataPolicyBudgetBytes() int {
	return int(min(metadataBudgetBytes("FLOW_METADATA_POLICY_BYTES", metadataMemoryShare(1, 16)), 1<<46))
}

func metadataBodyCacheBudgetBytes() int {
	return int(min(metadataMemoryShare(1, 32), 1<<46))
}

// metadataColdSlots is the replica's concurrent COLD facades.
func metadataColdSlots() int {
	return envPositiveInt("FLOW_COLD_SLOTS", metadataMemoryCount(1<<30, 4))
}

// metadataColdViewLanes is the replica's concurrent legacy-view decodes.
func metadataColdViewLanes() int {
	return envPositiveInt("FLOW_COLD_VIEW_LANES", metadataMemoryCount(1<<30, 4))
}

// BoundedColdProjectionConcurrency is the default number of COLD requests
// allowed to hold a legacy projection at once, separate from HOT reads.
func BoundedColdProjectionConcurrency() int {
	return max(metadataMemoryCount(512<<20, 8), 2)
}

// BoundedMetadataReadConcurrency is the default number of workspace metadata
// projections (the issue bootstrap) a bounded replica serves at once: one
// per 512 MiB of the ceiling, between 4 and 16. Readers of one generation
// share its decoded image, so each projection costs its own view only.
func BoundedMetadataReadConcurrency() int {
	return max(metadataMemoryCount(512<<20, 16), 4)
}

var applyMetadataMemoryOnce sync.Once

// applyMetadataMemoryPlan sets the soft Go memory limit and logs the budgets
// once, when a bounded store is opened.
func applyMetadataMemoryPlan() {
	applyMetadataMemoryOnce.Do(func() {
		if testing.Testing() {
			return
		}
		ceiling, source := metadataMemoryCeiling()
		runtimeLimit := min(debug.SetMemoryLimit(-1), ceiling)
		debug.SetMemoryLimit(runtimeLimit)
		mib := func(bytes int64) int64 { return bytes >> 20 }
		slog.Info("bounded metadata memory", "ceiling_mib", mib(ceiling), "ceiling_from", source,
			"images_mib", mib(coldDecodeCacheBudget()), "work_mib", mib(int64(metadataWorkBudgetBytes())),
			"policy_mib", mib(int64(metadataPolicyBudgetBytes())), "row_cache_mib", mib(int64(metadataBodyCacheBudgetBytes())),
			"bootstrap_encoding_mib", mib(BoundedBootstrapEncodingBytes()),
			"sources_mib", mib(metadataLogicalDescriptorCap()), "spool_disk_mib", mib(metadataLogicalDiskCap()),
			"cold_slots", metadataColdSlots(), "view_lanes", metadataColdViewLanes(), "default_read_slots", BoundedMetadataReadConcurrency(),
			"default_cold_projection_slots", BoundedColdProjectionConcurrency(),
			"runtime_limit_mib", mib(runtimeLimit))
	})
}
