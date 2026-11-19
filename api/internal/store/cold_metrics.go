package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Operator visibility of the bounded-metadata COLD path (FLOW_BOUNDED_METADATA
// on): a periodic summary of the process-wide COLD counters and a startup
// report of each workspace's metadata catalog. No endpoint is involved; both
// are structured slog lines.

// ColdMetricsSnapshot reports the process-wide bounded-metadata counters:
// COLD slot use and waits, legacy-view lane refusals, COLD facade and
// legacy-view decodes, COLD legacy write-lock holds and writer lock waits.
// Counts and nanosecond totals only grow; coldSlotsHeld and coldSlotWaiting
// are gauges and the *Max values process-wide maxima.
func ColdMetricsSnapshot() map[string]int64 {
	waiting := int64(0)
	queues := int64(0)
	coldSlotQueues.Range(func(_, value any) bool {
		waiting += value.(*coldSlotQueue).waiting.Load()
		queues++
		return true
	})
	facades, facadeTime, viewTime := ColdHydrationForTesting()
	holds, holdTotal, holdMax := ColdWriteLockHoldForTesting()
	fallbacks := int64(0)
	reasons := ColdPublishFallbacksForTesting()
	for _, count := range reasons {
		fallbacks += count
	}
	snapshot := map[string]int64{
		"coldWriteSyncNanos":          coldWriteSyncNanos.Load(),
		"coldWriteRefreshes":          coldWriteRefreshes.Load(),
		"coldWriteRefreshNanos":       coldWriteRefreshNanos.Load(),
		"coldWritePublishNanos":       coldWritePublishNanos.Load(),
		"coldPublishBaseNanos":        coldPublishBaseNanos.Load(),
		"coldPublishIncrementals":     coldPublishIncrementals.Load(),
		"coldPublishIncrementalNanos": coldPublishIncrementalNanos.Load(),
		"coldPublishFulls":            coldPublishFulls.Load(),
		"coldPublishFullNanos":        coldPublishFullNanos.Load(),
		"coldPublishUnchanged":        coldPublishUnchanged.Load(),
		"coldPublishBaseKnown":        coldPublishBaseKnown.Load(),
		"coldPublishFallbacks":        fallbacks,
		"coldSlotAcquisitions":        coldSlotAcquisitions.Load(),
		"coldSlotRefusals":            coldSlotRefusals.Load(),
		"coldSlotsHeld":               coldSlotHolders.Load(),
		"coldSlotWaiting":             waiting,
		"coldSlotQueues":              queues,
		"coldSlotWaits":               coldSlotWaits.Load(),
		"coldSlotWaitNanos":           coldSlotWaitNanos.Load(),
		"coldSlotWaitMaxNanos":        coldSlotWaitMax.Load(),
		"legacyViewRefusals":          legacyViewRefusals.Load(),
		"legacyViewWaits":             legacyViewWaits.Load(),
		"legacyViewWaitNanos":         legacyViewWaitNanos.Load(),
		"legacyViewWaitMax":           legacyViewWaitMax.Load(),
		"coldFacadeDecodes":           facades,
		"coldFacadeDecodeNanos":       int64(facadeTime),
		"coldFacadeDecodeMaxNanos":    coldFacadeDecodeMax.Load(),
		"coldViewReads":               coldAccessorReads.Load(),
		"coldViewDecodeNanos":         int64(viewTime),
		"coldViewDecodeMaxNanos":      coldAccessorDecodeMax.Load(),
		"coldViewFailures":            coldAccessorFailures.Load(),
		"coldWriteLockHolds":          holds,
		"coldWriteLockNanos":          int64(holdTotal),
		"coldWriteLockMaxNanos":       int64(holdMax),
		"writerLockWaits":             writerLockWaits.waits.Load(),
		"writerLockWaitNanos":         writerLockWaits.nanos.Load(),
		"writerLockWaitMax":           writerLockWaits.longest.Load(),
		"coldWriterLockWaits":         coldWriterLockWaits.waits.Load(),
		"coldWriterWaitNanos":         coldWriterLockWaits.nanos.Load(),
		"coldWriterWaitMax":           coldWriterLockWaits.longest.Load(),
	}
	for reason, count := range reasons {
		snapshot["coldPublishFallback "+reason] = count
	}
	return snapshot
}

// DefaultBoundedMetadataStatsInterval is the period of the COLD summary line.
const DefaultBoundedMetadataStatsInterval = 60 * time.Second

// BoundedMetadataStatsInterval reads FLOW_BOUNDED_METADATA_STATS_INTERVAL: a
// Go duration ("30s", "2m") or whole seconds ("45"); 0 disables the summary.
// Unset or invalid values give the default (an invalid one is logged).
func BoundedMetadataStatsInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("FLOW_BOUNDED_METADATA_STATS_INTERVAL"))
	if raw == "" {
		return DefaultBoundedMetadataStatsInterval
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
		return d
	}
	slog.Warn("bounded metadata: invalid FLOW_BOUNDED_METADATA_STATS_INTERVAL, using the default", "value", raw, "default", DefaultBoundedMetadataStatsInterval)
	return DefaultBoundedMetadataStatsInterval
}

// coldMetricsDelta compares two snapshots and returns the log attributes of
// what changed since prev, or nil when nothing did. Waits and refusals are
// deltas; slotWaitMax and the decode maxima are process-wide maxima so far.
func coldMetricsDelta(prev, cur map[string]int64, interval time.Duration) []any {
	d := func(key string) int64 { return cur[key] - prev[key] }
	avg := func(nanos, count int64) time.Duration {
		if count <= 0 {
			return 0
		}
		return time.Duration(nanos / count)
	}
	changed := d("coldSlotWaits") != 0 || d("coldSlotRefusals") != 0 || d("legacyViewRefusals") != 0 ||
		d("coldFacadeDecodes") != 0 || d("coldViewReads") != 0 || d("coldViewFailures") != 0 ||
		d("writerLockWaits") != 0 || d("coldWriterLockWaits") != 0 || d("coldWriteLockHolds") != 0
	if !changed {
		return nil
	}
	return []any{
		"interval", interval,
		"slotWaits", d("coldSlotWaits"),
		"slotWaitAvg", avg(d("coldSlotWaitNanos"), d("coldSlotWaits")),
		"slotWaitMax", time.Duration(cur["coldSlotWaitMaxNanos"]),
		"slotRefusals", d("coldSlotRefusals"),
		"slotsHeld", cur["coldSlotsHeld"],
		"slotWaiting", cur["coldSlotWaiting"],
		"viewRefusals", d("legacyViewRefusals"),
		"facadeDecodes", d("coldFacadeDecodes"),
		"facadeDecodeAvg", avg(d("coldFacadeDecodeNanos"), d("coldFacadeDecodes")),
		"facadeDecodeMax", time.Duration(cur["coldFacadeDecodeMaxNanos"]),
		"viewDecodes", d("coldViewReads"),
		"viewDecodeAvg", avg(d("coldViewDecodeNanos"), d("coldViewReads")),
		"viewDecodeMax", time.Duration(cur["coldViewDecodeMaxNanos"]),
		"viewFailures", d("coldViewFailures"),
		"lockWaits", d("writerLockWaits"),
		"lockWaitAvg", avg(d("writerLockWaitNanos"), d("writerLockWaits")),
		"lockWaitMax", time.Duration(cur["writerLockWaitMax"]),
		"coldLockWaits", d("coldWriterLockWaits"),
		"coldLockWaitAvg", avg(d("coldWriterWaitNanos"), d("coldWriterLockWaits")),
		"coldLockWaitMax", time.Duration(cur["coldWriterWaitMax"]),
		"coldWriteLockHolds", d("coldWriteLockHolds"),
		"coldPublishIncremental", d("coldPublishIncrementals"),
		"coldPublishIncrementalAvg", avg(d("coldPublishIncrementalNanos"), d("coldPublishIncrementals")),
		"coldPublishFull", d("coldPublishFulls"),
		"coldPublishFullAvg", avg(d("coldPublishFullNanos"), d("coldPublishFulls")),
		"coldPublishFallbacks", d("coldPublishFallbacks"),
	}
}

// RunColdMetricsLog logs one "bounded metadata: cold path" line per interval
// with the counter deltas since the previous line, only when something
// changed. It blocks until ctx ends and starts nothing (returns at once) when
// interval is not positive.
func RunColdMetricsLog(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	prev := ColdMetricsSnapshot()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := ColdMetricsSnapshot()
			if attrs := coldMetricsDelta(prev, cur, interval); attrs != nil {
				slog.Info("bounded metadata: cold path", attrs...)
			}
			prev = cur
		}
	}
}

// MetadataCatalogCompleteness is one workspace's catalog state: collections
// counts the collections with canonical records or a catalog row, incomplete
// those whose catalog backfill has not finished.
type MetadataCatalogCompleteness struct {
	Workspace   string
	Collections int
	Incomplete  int
}

// MetadataCatalogCompleteness counts, per workspace, the metadata collections
// and those whose catalog is not complete (phase 2). Read-only SQL counts; no
// record body is read.
func (s *SQLiteStore) MetadataCatalogCompleteness(ctx context.Context) ([]MetadataCatalogCompleteness, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.workspace_key, COUNT(*),
		COALESCE(SUM(CASE WHEN p.field IS NULL OR p.phase<>2 THEN 1 ELSE 0 END),0)
		FROM (SELECT workspace_key,field FROM workspace_metadata_records
			UNION SELECT workspace_key,field FROM metadata_catalog_collections) f
		LEFT JOIN metadata_catalog_collections p ON p.workspace_key=f.workspace_key AND p.field=f.field
		GROUP BY f.workspace_key ORDER BY f.workspace_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MetadataCatalogCompleteness
	for rows.Next() {
		var c MetadataCatalogCompleteness
		if err := rows.Scan(&c.Workspace, &c.Collections, &c.Incomplete); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// LogMetadataCatalogCompleteness logs, with bounded metadata on, one line per
// workspace: whether its catalog is complete and how many collections are
// not. Flag off it logs nothing. A failed count is logged and skipped.
func (s *SQLiteStore) LogMetadataCatalogCompleteness(ctx context.Context) {
	if !s.BoundedMetadataEnabled() {
		return
	}
	catalogs, err := s.MetadataCatalogCompleteness(ctx)
	if err != nil && err != sql.ErrNoRows {
		slog.Warn("bounded metadata: catalog completeness unavailable", "error", err)
		return
	}
	for _, c := range catalogs {
		if c.Incomplete == 0 {
			slog.Info("bounded metadata: catalog", "workspace", c.Workspace, "complete", true, "collections", c.Collections, "incompleteCollections", 0)
			continue
		}
		slog.Warn("bounded metadata: catalog", "workspace", c.Workspace, "complete", false, "collections", c.Collections, "incompleteCollections", c.Incomplete,
			"hint", fmt.Sprintf("%d collections still need the catalog backfill", c.Incomplete))
	}
}
