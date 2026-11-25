package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func BenchmarkScaleP09ActivateB1PairedTimezone(b *testing.B) {
	if os.Getenv("SCALE_ACTIVATE_B1_PAIRED") != "1" || os.Getenv("FLOW_TEST_DATABASE_DRIVER") != "" {
		b.Skip("opt-in private SQLite timing probe")
	}
	b.Setenv("FLOW_RECORD_JOURNAL", "off")
	off, offKey := benchmarkWorkspace(b, "b1-paired-off", 1000)
	b.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	main, mainKey := benchmarkWorkspace(b, "b1-paired-main", 1000)
	point, pointKey := benchmarkWorkspace(b, "b1-paired-point", 1000)
	statement, err := main.db.DB.PrepareContext(context.Background(), recordJournalSQLCommit)
	if err != nil {
		b.Fatal(err)
	}
	original := main.db.journalSQLFlush
	main.db.journalSQLFlush = statement
	b.Cleanup(func() { main.db.journalSQLFlush = original; _ = statement.Close() })
	if off.RecordJournalReady() || !main.RecordJournalReady() || !point.RecordJournalReady() {
		b.Fatal("timing modes did not retain their separate database readiness")
	}
	repos := []*SQLiteStore{off, main, point}
	keys := []string{offKey, mainKey, pointKey}
	labels := []string{"off", "main", "point"}
	counters := []int{0, 0, 0}
	ctx := context.Background()
	write := func(index int) {
		zone := []string{"Asia/Shanghai", "UTC"}[counters[index]%2]
		counters[index]++
		if err := repos[index].MutateWorkspace(ctx, keys[index], "team.settings_updated", "bulk-team-00000", map[string]string{"timezone": zone}, func(next *domain.Bootstrap) error {
			settings := next.TeamSettings["bulk-team-00000"]
			settings.Timezone = zone
			next.TeamSettings["bulk-team-00000"] = settings
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
	for index := range repos {
		for range 20 {
			write(index)
		}
	}
	type sample struct {
		Round, Order, Operations     int
		Mode                         string
		NanosecondsPerOp, BytesPerOp uint64
		AllocationsPerOp             uint64
	}
	samples := []sample{}
	const rounds = 30
	operations := 100
	if value, err := strconv.Atoi(os.Getenv("SCALE_ACTIVATE_B1_PAIRED_OPS")); err == nil && value > 0 && value <= 1000 {
		operations = value
	}
	b.ResetTimer()
	for round := range rounds {
		for order := range 3 {
			index := (round + order) % 3
			mode := "shadow"
			if index == 0 {
				mode = "off"
			}
			if err := os.Setenv("FLOW_RECORD_JOURNAL", mode); err != nil {
				b.Fatal(err)
			}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			started := time.Now()
			for range operations {
				write(index)
			}
			elapsed := time.Since(started)
			runtime.ReadMemStats(&after)
			samples = append(samples, sample{round + 1, order + 1, operations, labels[index], uint64(elapsed.Nanoseconds() / int64(operations)), (after.TotalAlloc - before.TotalAlloc) / uint64(operations), (after.Mallocs - before.Mallocs) / uint64(operations)})
		}
	}
	b.StopTimer()
	var offEntries int
	if err := off.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM record_journal`).Scan(&offEntries); err != nil || offEntries != 0 {
		b.Fatalf("off control captured journal entries: count=%d err=%v", offEntries, err)
	}
	medians := map[string]uint64{}
	means := map[string]float64{}
	for _, label := range labels {
		values := []uint64{}
		for _, sample := range samples {
			if sample.Mode == label {
				values = append(values, sample.NanosecondsPerOp)
				means[label] += float64(sample.NanosecondsPerOp) / rounds
			}
		}
		slices.Sort(values)
		medians[label] = (values[len(values)/2-1] + values[len(values)/2]) / 2
		b.ReportMetric(float64(medians[label]), fmt.Sprintf("%s-ns/op", label))
	}
	result := map[string]any{"samples": samples, "medians": medians, "means": means, "operationsPerBlock": operations, "environmentPerBlock": true, "pointMeanVersusOff": means["point"]/means["off"] - 1, "mainMeanVersusOff": means["main"]/means["off"] - 1, "pointVersusOff": float64(medians["point"])/float64(medians["off"]) - 1, "mainVersusOff": float64(medians["main"])/float64(medians["off"]) - 1}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(fmt.Sprintf("/tmp/codex-scale-activate-b1/paired-timezone-%d.json", operations), raw, 0o644); err != nil {
		b.Fatal(err)
	}
	b.Logf("paired timezone medians: %v", medians)
}
