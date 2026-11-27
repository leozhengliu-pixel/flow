package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A bounded start spools each workspace's typed frames to one logical file.
// The writer used to refuse any file past a fixed 1 GiB, whatever process
// spool disk budget was configured, so a workspace of 2,269 projects with
// 503 points in each of five progress history series fell back to legacy
// reads with a warning that gave no reason (#262). These tests open such a
// workspace scaled down to CI size, with the per-file capacity lowered in
// the same proportion through metadataLogicalFileAddressBytes:
//
//   - a per-file capacity below the file and a process budget above it (the
//     old fixed limit): admission falls back and the warning names the
//     collection, the capacities and the operator action;
//   - the default capacity under the same process budget: admission holds
//     and the file grows past the old per-file capacity;
//   - a process budget below the file: admission falls back and the warning
//     tells the operator to raise FLOW_METADATA_SOURCE_DISK_BYTES.
//
// FLOW_TEST_CAPACITY_PROJECTS, FLOW_TEST_CAPACITY_POINTS,
// FLOW_TEST_CAPACITY_FILE_BYTES (the old per-file capacity) and
// FLOW_TEST_CAPACITY_DISK_BYTES (the process budget) scale the fixture.
const (
	capacityHistoryProjects  = 8
	capacityHistoryPoints    = 503
	capacityHistoryFileBytes = 1 << 20
	capacityHistoryDiskBytes = 32 << 20
)

func capacityHistoryScale(t *testing.T) (projects, points int, fileBytes, diskBytes int64) {
	t.Helper()
	values := map[string]*int64{}
	p, q := int64(capacityHistoryProjects), int64(capacityHistoryPoints)
	fileBytes, diskBytes = capacityHistoryFileBytes, capacityHistoryDiskBytes
	values["FLOW_TEST_CAPACITY_PROJECTS"], values["FLOW_TEST_CAPACITY_POINTS"] = &p, &q
	values["FLOW_TEST_CAPACITY_FILE_BYTES"], values["FLOW_TEST_CAPACITY_DISK_BYTES"] = &fileBytes, &diskBytes
	for name, value := range values {
		if raw := os.Getenv(name); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed <= 0 {
				t.Fatalf("%s=%q", name, raw)
			}
			*value = parsed
		}
	}
	return int(p), int(q), fileBytes, diskBytes
}

type capacityStartup struct {
	bounded bool
	// largestFile is the largest storage of the test workspace's installed
	// source; spooled is every installed source's disk charge.
	largestFile, spooled int64
	peakHeap, peakLive   uint64
	elapsed              time.Duration
	warnings             []map[string]any
}

type capacityLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *capacityLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// openCapacityStartup opens a store with bounded metadata on, a per-file
// capacity of fileBytes and a process spool disk budget of diskBytes.
func openCapacityStartup(t *testing.T, open func() *SQLiteStore, fileBytes, diskBytes int64) capacityStartup {
	t.Helper()
	startupHistoryFlags(t, "on")
	t.Setenv("FLOW_METADATA_SOURCE_DISK_BYTES", strconv.FormatInt(diskBytes, 10))
	previousCapacity := metadataLogicalFileAddressBytes
	metadataLogicalFileAddressBytes = fileBytes
	defer func() { metadataLogicalFileAddressBytes = previousCapacity }()
	logs := &capacityLogBuffer{}
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(previousLogger)

	runtime.GC()
	var result capacityStartup
	done := make(chan struct{})
	sampled := make(chan [2]uint64)
	go func() {
		// HeapInuse includes garbage awaiting collection; the live heap is
		// what the last collection kept.
		var peak [2]uint64
		var stats runtime.MemStats
		live := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			runtime.ReadMemStats(&stats)
			metrics.Read(live)
			peak[0] = max(peak[0], stats.HeapInuse)
			if live[0].Value.Kind() == metrics.KindUint64 {
				peak[1] = max(peak[1], live[0].Value.Uint64())
			}
			select {
			case <-done:
				sampled <- peak
				return
			case <-ticker.C:
			}
		}
	}()
	diskBefore := metadataLogicalDiskUsed.Load()
	started := time.Now()
	repo := open()
	result.elapsed = time.Since(started)
	close(done)
	peaks := <-sampled
	result.peakHeap, result.peakLive = peaks[0], peaks[1]
	result.bounded = repo.BoundedMetadataEnabled()
	result.spooled = metadataLogicalDiskUsed.Load() - diskBefore
	if result.bounded {
		source, err := repo.retainMetadataLogicalSource(t.Context(), "test-workspace")
		if err != nil {
			repo.Close()
			t.Fatal(err)
		}
		for _, storage := range source.stores {
			result.largestFile = max(result.largestFile, storage.bytes)
		}
		source.Release()
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	logs.mu.Lock()
	scanner := bufio.NewScanner(bytes.NewReader(logs.buf.Bytes()))
	logs.mu.Unlock()
	for scanner.Scan() {
		var record map[string]any
		if json.Unmarshal(scanner.Bytes(), &record) == nil && strings.HasPrefix(record["msg"].(string), "startup metadata") {
			result.warnings = append(result.warnings, record)
		}
	}
	t.Logf("file_capacity=%d disk_budget=%d bounded=%t largest_file=%d spooled=%d peak_heap_inuse=%d peak_live_heap=%d open=%s warnings=%v",
		fileBytes, diskBytes, result.bounded, result.largestFile, result.spooled, result.peakHeap, result.peakLive, result.elapsed.Round(time.Millisecond), result.warnings)
	return result
}

func capacityWarning(t *testing.T, startup capacityStartup, reason string) map[string]any {
	t.Helper()
	for _, record := range startup.warnings {
		if record["msg"] == "startup metadata: bounded workspace admission failed; using legacy reads" && record["reason"] == reason {
			return record
		}
	}
	t.Fatalf("no admission fallback warning with reason %s: %v", reason, startup.warnings)
	return nil
}

// seedCapacityHistory adds the history-heavy projects of seedStartupHistory
// in batches that fit one transaction's write budget.
func seedCapacityHistory(t *testing.T, repo *SQLiteStore, projects, points int) {
	t.Helper()
	const batch = 32
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	series := func(offset int) []domain.ProjectProgressHistoryPoint {
		history := make([]domain.ProjectProgressHistoryPoint, points)
		for point := range history {
			history[point] = domain.ProjectProgressHistoryPoint{Date: now.AddDate(0, 0, point-points), Value: float64(point + offset), ScopeCount: point + offset}
		}
		return history
	}
	for first := 0; first < projects; first += batch {
		if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), "test-workspace", "startup.history_heavy", "", nil, func(data *domain.Bootstrap) error {
			if first == 0 {
				data.Projects = nil
			}
			for i := first; i < min(first+batch, projects); i++ {
				data.Projects = append(data.Projects, domain.Project{
					ID: fmt.Sprintf("startup-project-%05d", i), Name: fmt.Sprintf("Startup project %05d", i),
					TeamIDs: []string{data.Teams[0].ID}, CreatedAt: now, UpdatedAt: now,
					IssueCountHistory: series(0), ScopeHistory: series(1), CompletedScopeHistory: series(2),
					InProgressScopeHistory: series(3), ProgressHistory: series(4),
				})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func testStartupLogicalFileCapacity(t *testing.T, open func(name string) func() *SQLiteStore) {
	projects, points, fileBytes, diskBytes := capacityHistoryScale(t)
	startupHistoryFlags(t, "off")
	repo := open("seed")()
	seedCapacityHistory(t, repo, projects, points)
	prepared, err := repo.PrepareBoundedMetadata(t.Context())
	if err != nil || !prepared.Ready {
		repo.Close()
		t.Fatalf("prepare bounded metadata: %+v %v", prepared, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	defaultCapacity := metadataLogicalFileAddressBytes

	// The old fixed per-file limit, scaled: the file does not fit it although
	// the process budget holds it.
	fixed := openCapacityStartup(t, open("fixed"), fileBytes, diskBytes)
	if fixed.bounded {
		t.Fatal("the scaled fixture fits the old per-file capacity; raise FLOW_TEST_CAPACITY_PROJECTS")
	}
	warning := capacityWarning(t, fixed, metadataLogicalCapacityFile)
	if warning["collection"] != "projects" || warning["file_capacity_bytes"] != float64(fileBytes) ||
		warning["spool_disk_budget_bytes"] != float64(diskBytes) || warning["file_offset_bytes"].(float64) > float64(fileBytes) ||
		!strings.Contains(warning["action"].(string), "per-file capacity") {
		t.Fatalf("file capacity fallback lost its diagnostic: %v", warning)
	}
	if _, found := warning["error"]; found {
		t.Fatal("the diagnostic logs the error text")
	}

	// The per-file capacity follows the process budget: the same file is
	// admitted and passes the old capacity.
	admitted := openCapacityStartup(t, open("admitted"), defaultCapacity, diskBytes)
	if !admitted.bounded || admitted.largestFile <= fileBytes || admitted.spooled > diskBytes {
		t.Fatalf("bounded admission within the process budget: bounded=%t largest_file=%d spooled=%d old_capacity=%d budget=%d",
			admitted.bounded, admitted.largestFile, admitted.spooled, fileBytes, diskBytes)
	}
	for _, record := range admitted.warnings {
		if strings.Contains(record["msg"].(string), "legacy reads") {
			t.Fatalf("admitted start logged a fallback: %v", record)
		}
	}

	// A process budget below the file refuses it and names the setting.
	starved := openCapacityStartup(t, open("starved"), defaultCapacity, admitted.largestFile/2)
	if starved.bounded {
		t.Fatal("a process budget below the file admitted it")
	}
	var refusal map[string]any
	for _, reason := range []string{metadataLogicalCapacityProcessDisk, "record_spool_budget"} {
		for _, record := range starved.warnings {
			if record["reason"] == reason {
				refusal = record
			}
		}
	}
	if refusal == nil || !strings.Contains(refusal["action"].(string), "FLOW_METADATA_SOURCE_DISK_BYTES") ||
		refusal["spool_disk_budget_bytes"] != float64(admitted.largestFile/2) {
		t.Fatalf("process budget fallback lost its diagnostic: %v", starved.warnings)
	}
}

func TestBoundedMetadataStartupLogicalFileCapacity(t *testing.T) {
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed.db")
	testStartupLogicalFileCapacity(t, func(name string) func() *SQLiteStore {
		if name == "seed" {
			return func() *SQLiteStore {
				repo, err := OpenSQLiteTestFixture(seedPath)
				if err != nil {
					t.Fatal(err)
				}
				return repo
			}
		}
		path := filepath.Join(dir, name+".db")
		scaleP21SW557CopyDatabase(t, seedPath, path)
		return func() *SQLiteStore {
			repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
			if err != nil {
				t.Fatal(err)
			}
			return repo
		}
	})
}

// The MySQL start uses application journal capture, as in #262.
func TestBoundedMetadataStartupLogicalFileCapacityMySQL(t *testing.T) {
	if strings.ToLower(os.Getenv("FLOW_TEST_DATABASE_DRIVER")) != "mysql" {
		t.Skip("requires FLOW_TEST_DATABASE_DRIVER=mysql and a throwaway MySQL database")
	}
	startupHistoryFlags(t, "off")
	open := scratchMySQLDatabaseWith(t, func() *SQLiteStore {
		repo, err := OpenDatabase(DatabaseConfig{Driver: "mysql", URL: os.Getenv("FLOW_TEST_DATABASE_URL"), MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}, func(config *DatabaseConfig) {
		config.RecordJournalCapture = RecordJournalCaptureApplication
	})
	testStartupLogicalFileCapacity(t, func(string) func() *SQLiteStore { return open })
}
