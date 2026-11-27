package store

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A bounded start rebuilds the logical source of every resident workspace.
// Each frame header patch used to walk every memo entry of the storage
// (logical_frame_memo.go), so a workspace of history-heavy projects took
// quadratic time to open: 2,300 projects with 68 points in each of the five
// progress history series did not open within 145 s, against 6.7 s with
// bounded metadata off. These tests open such a workspace (scaled down to
// CI time) both ways and bound the bounded start by the flags-off one.
//
// FLOW_TEST_STARTUP_HISTORY_PROJECTS and FLOW_TEST_STARTUP_HISTORY_POINTS
// scale the fixture; FLOW_TEST_STARTUP_HISTORY_CPUPROFILE names a file for a
// CPU profile of the bounded open.
const (
	startupHistoryProjects = 400
	startupHistoryPoints   = 40
)

func startupHistoryScale(t *testing.T) (projects, points int) {
	t.Helper()
	projects, points = startupHistoryProjects, startupHistoryPoints
	for name, value := range map[string]*int{"FLOW_TEST_STARTUP_HISTORY_PROJECTS": &projects, "FLOW_TEST_STARTUP_HISTORY_POINTS": &points} {
		if raw := os.Getenv(name); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				t.Fatalf("%s=%q", name, raw)
			}
			*value = parsed
		}
	}
	return projects, points
}

func startupHistoryFlags(t *testing.T, bounded string) {
	t.Setenv("FLOW_BOUNDED_METADATA", bounded)
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_METADATA_CATALOG", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
}

// timeStartupHistoryOpen opens a store and closes it again, returning how
// long the open took.
func timeStartupHistoryOpen(t *testing.T, bounded bool, open func() *SQLiteStore) time.Duration {
	t.Helper()
	value := "off"
	if bounded {
		value = "on"
	}
	startupHistoryFlags(t, value)
	if path := os.Getenv("FLOW_TEST_STARTUP_HISTORY_CPUPROFILE"); path != "" && bounded {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(file); err != nil {
			t.Fatal(err)
		}
		defer func() {
			pprof.StopCPUProfile()
			_ = file.Close()
		}()
	}
	started := time.Now()
	repo := open()
	elapsed := time.Since(started)
	enabled, capture := repo.BoundedMetadataEnabled(), repo.RecordJournalCapture()
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if enabled != bounded {
		t.Fatalf("bounded=%t start opened with bounded metadata %t", bounded, enabled)
	}
	t.Logf("bounded=%t open=%s capture=%q", bounded, elapsed.Round(time.Millisecond), capture)
	return elapsed
}

func checkStartupHistoryBound(t *testing.T, projects, points int, legacy, bounded time.Duration) {
	t.Helper()
	t.Logf("history-heavy startup projects=%d points=%d flags_off=%s flags_on=%s", projects, points, legacy.Round(time.Millisecond), bounded.Round(time.Millisecond))
	limit := legacy*10 + 10*time.Second
	if raceDetector {
		// The race detector instruments every spool write (~45x slower here than a
		// normal build: 1m21s on CI for 400 x 40). The quadratic frame-memo drop
		// this guards against took 35 s in a normal build, far past this bound
		// under race too.
		limit = legacy*100 + 3*time.Minute
	}
	if bounded > limit {
		t.Fatalf("bounded startup took %s against flags-off %s", bounded.Round(time.Millisecond), legacy.Round(time.Millisecond))
	}
}

func TestBoundedMetadataStartupHistoryHeavy(t *testing.T) {
	projects, points := startupHistoryScale(t)
	startupHistoryFlags(t, "off")
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed.db")
	repo, err := OpenSQLiteTestFixture(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	seedStartupHistory(t, repo, projects, points)
	prepared, err := repo.PrepareBoundedMetadata(t.Context())
	if err != nil || !prepared.Ready {
		repo.Close()
		t.Fatalf("prepare bounded metadata: %+v %v", prepared, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	opener := func(name string) func() *SQLiteStore {
		path := filepath.Join(dir, name)
		copyDatabase(t, seedPath, path)
		return func() *SQLiteStore {
			repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
			if err != nil {
				t.Fatal(err)
			}
			return repo
		}
	}
	legacy := timeStartupHistoryOpen(t, false, opener("legacy.db"))
	bounded := timeStartupHistoryOpen(t, true, opener("bounded.db"))
	checkStartupHistoryBound(t, projects, points, legacy, bounded)
}

// The MySQL start uses application journal capture, as a database account
// without the TRIGGER privilege does.
func TestBoundedMetadataStartupHistoryHeavyMySQL(t *testing.T) {
	if strings.ToLower(os.Getenv("FLOW_TEST_DATABASE_DRIVER")) != "mysql" {
		t.Skip("requires FLOW_TEST_DATABASE_DRIVER=mysql and a throwaway MySQL database")
	}
	projects, points := startupHistoryScale(t)
	startupHistoryFlags(t, "off")
	openBase := func() *SQLiteStore {
		repo, err := OpenDatabase(DatabaseConfig{Driver: "mysql", URL: os.Getenv("FLOW_TEST_DATABASE_URL"), MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	open := scratchMySQLDatabaseWith(t, openBase, func(config *DatabaseConfig) {
		config.RecordJournalCapture = RecordJournalCaptureApplication
	})
	repo := open()
	seedStartupHistory(t, repo, projects, points)
	prepared, err := repo.PrepareBoundedMetadata(t.Context())
	if err != nil || !prepared.Ready {
		repo.Close()
		t.Fatalf("prepare bounded metadata: %+v %v", prepared, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	legacy := timeStartupHistoryOpen(t, false, open)
	bounded := timeStartupHistoryOpen(t, true, open)
	checkStartupHistoryBound(t, projects, points, legacy, bounded)
}

// seedStartupHistory gives the test workspace projects whose five progress
// history series hold points dated points each.
func seedStartupHistory(t *testing.T, repo *SQLiteStore, projects, points int) {
	t.Helper()
	workspace := "test-workspace"
	data, ok := repo.storedWorkspace(workspace)
	if !ok || len(data.Teams) == 0 {
		t.Fatal("test fixture workspace is missing its team")
	}
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	series := func(offset int) []domain.ProjectProgressHistoryPoint {
		history := make([]domain.ProjectProgressHistoryPoint, points)
		for point := range history {
			history[point] = domain.ProjectProgressHistoryPoint{
				Date:       now.AddDate(0, 0, point-points),
				Value:      float64(point + offset),
				ScopeCount: point + offset,
			}
		}
		return history
	}
	if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "startup.history_heavy", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = make([]domain.Project, projects)
		for i := range data.Projects {
			data.Projects[i] = domain.Project{
				ID:                     fmt.Sprintf("startup-project-%05d", i),
				Name:                   fmt.Sprintf("Startup project %05d", i),
				TeamIDs:                []string{data.Teams[0].ID},
				CreatedAt:              now,
				UpdatedAt:              now,
				IssueCountHistory:      series(0),
				ScopeHistory:           series(1),
				CompletedScopeHistory:  series(2),
				InProgressScopeHistory: series(3),
				ProgressHistory:        series(4),
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
