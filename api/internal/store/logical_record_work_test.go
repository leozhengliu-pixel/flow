package store

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A bounded start normalizes a workspace's logical source one record at a
// time. The normalizer used to refuse any record whose logical decode cost
// passed a fixed eighth of metadataWorkBytes() (32 MiB), before it asked the
// process work budget, so a workspace with a 24 MB document (decode cost
// about 48 MB) fell back to legacy reads whatever FLOW_METADATA_WORK_BYTES
// allowed, and the warning named the process budget (#264). These tests
// open a workspace with a document of T = 24 MB of text (decode cost about
// 2T, work about 16T = 384 MB) and a project whose description is three
// quarters of it (work about 288 MB):
//
//   - under a 336 MB work budget the project is normalized but the document
//     needs more than the per-record limit: admission falls back and the
//     warning names the collection, the record's cost and work, the limits
//     and the setting;
//   - with FLOW_METADATA_WORK_BYTES raised above the document's work both are
//     admitted, read back whole, and the work reserved stays within the
//     budget.
//
// CI writes T = 3 MB and raises the work per byte of decode cost
// (metadataRecordWorkFactor) eightfold, so the records need the same work
// as the full-size ones under the same budgets. FLOW_TEST_RECORD_WORK_BYTES
// sets T (24000000 writes the document of #264 with the production factor)
// and FLOW_TEST_RECORD_WORK_BUDGET the raised work budget.
const (
	recordWorkTextBytes = 3_000_000
	recordWorkFullBytes = 24_000_000
	recordWorkBudget    = 1 << 30
	// recordWorkWriteBytes lets one transaction write the large records.
	recordWorkWriteBytes = 256 << 20
)

func recordWorkScale(t *testing.T) (textBytes, budget int64) {
	t.Helper()
	textBytes, budget = recordWorkTextBytes, recordWorkBudget
	for name, value := range map[string]*int64{"FLOW_TEST_RECORD_WORK_BYTES": &textBytes, "FLOW_TEST_RECORD_WORK_BUDGET": &budget} {
		if raw := os.Getenv(name); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed <= 0 {
				t.Fatalf("%s=%q", name, raw)
			}
			*value = parsed
		}
	}
	factor := metadataRecordWorkFactor()
	t.Cleanup(SetMetadataRecordWorkFactorForTesting(max(factor, int(int64(factor)*recordWorkFullBytes/textBytes))))
	return textBytes, budget
}

func seedRecordWork(t *testing.T, repo *SQLiteStore, textBytes int) {
	t.Helper()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	text := strings.Repeat("large document text ", textBytes/20+1)[:textBytes]
	if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "documents"), "test-workspace", "startup.large_document", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{
			ID: "record-work-document", SlugID: "record-work-document", Title: "Large document", Content: text,
			Creator: data.Viewer, ProjectIDs: []string{}, TeamIDs: []string{data.Teams[0].ID}, SubscriberIDs: []string{},
			CreatedAt: now, UpdatedAt: now,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), "test-workspace", "startup.large_project", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{
			ID: "record-work-project", Name: "Large project", Description: text[:textBytes*3/4],
			TeamIDs: []string{data.Teams[0].ID}, CreatedAt: now, UpdatedAt: now,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func testStartupRecordWork(t *testing.T, open func(name string) func() *SQLiteStore) {
	textBytes, budget := recordWorkScale(t)
	startupHistoryFlags(t, "off")
	repo := open("seed")()
	seedRecordWork(t, repo, int(textBytes))
	prepared, err := repo.PrepareBoundedMetadata(t.Context())
	if err != nil || !prepared.Ready {
		repo.Close()
		t.Fatalf("prepare bounded metadata: %+v %v", prepared, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	disk := int64(8 << 30)

	// The work budget holds the project but not the document: the refusal
	// names the per-record guard, not the process budget.
	factor := int64(metadataRecordWorkFactor())
	refusedBudget := 14 * textBytes * factor / 8
	t.Setenv("FLOW_METADATA_WORK_BYTES", strconv.FormatInt(refusedBudget, 10))
	refused := openCapacityStartup(t, open("refused"), metadataLogicalFileAddressBytes, disk, "", nil)
	if refused.bounded {
		t.Fatal("records over the per-record work limit were admitted; raise FLOW_TEST_RECORD_WORK_BYTES")
	}
	warning := capacityWarning(t, refused, metadataRecordWorkLimitReason)
	cost, costLimit := warning["record_cost_bytes"].(float64), warning["record_cost_limit_bytes"].(float64)
	work, workLimit := warning["record_work_bytes"].(float64), warning["record_work_limit_bytes"].(float64)
	if collection := warning["collection"]; collection != "documents" {
		t.Fatalf("record work fallback names collection %v: %v", collection, warning)
	}
	// The limit follows the budget, less the base work, and is below the
	// document's cost. The work passes the old fixed operation limit.
	if cost < 2*float64(textBytes) || cost <= costLimit || work <= workLimit || workLimit != float64(refusedBudget) ||
		costLimit > float64(refusedBudget/factor) || costLimit < float64((refusedBudget-(2<<20))/factor) ||
		work <= float64(metadataWorkBytes()) || work < cost*float64(factor) ||
		!strings.Contains(warning["action"].(string), "FLOW_METADATA_WORK_BYTES") {
		t.Fatalf("record work fallback lost its diagnostic: %v", warning)
	}
	for _, attr := range []string{"error", "work_budget_bytes", "file_capacity_bytes"} {
		if _, found := warning[attr]; found {
			t.Fatalf("record work fallback logs %s: %v", attr, warning)
		}
	}

	// A budget above the records' work admits them. Their work is reserved
	// through the process budget.
	t.Setenv("FLOW_METADATA_WORK_BYTES", strconv.FormatInt(budget, 10))
	if float64(budget) < work {
		t.Fatalf("FLOW_TEST_RECORD_WORK_BUDGET=%d is below the record's work %v", budget, work)
	}
	admitted := openCapacityStartup(t, open("admitted"), metadataLogicalFileAddressBytes, disk, "", nil)
	if !admitted.bounded {
		t.Fatalf("records within the work budget fell back: %v", admitted.warnings)
	}
	for _, record := range admitted.warnings {
		if strings.Contains(record["msg"].(string), "legacy reads") {
			t.Fatalf("admitted start logged a fallback: %v", record)
		}
	}
	t.Logf("record_text_bytes=%d work_factor=%d record_cost_bytes=%v record_work_bytes=%v work_budget=%d peak_live_heap=%d peak_heap_inuse=%d",
		textBytes, factor, cost, work, budget, admitted.peakLive, admitted.peakHeap)

	reopened := open("admitted")()
	defer reopened.Close()
	if !reopened.BoundedMetadataEnabled() {
		t.Fatal("reopened store is not bounded")
	}
	source, err := reopened.retainMetadataLogicalSource(t.Context(), "test-workspace")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	data, err := source.Select(t.Context(), MetadataRecordScope{Fields: []string{"documents", "projects"}}, metadataPolicyBytes)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, document := range data.Documents {
		if document.ID == "record-work-document" && len(document.Content) == int(textBytes) {
			found++
		}
	}
	for _, project := range data.Projects {
		if project.ID == "record-work-project" && len(project.Description) == int(textBytes*3/4) {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("admitted source lost a large record: %d of 2", found)
	}
	if peak := reopened.metadataResidency.work.peak; peak > int(budget) {
		t.Fatalf("reserved work %d passed the budget %d", peak, budget)
	}
}

func TestBoundedMetadataStartupLargeRecordWork(t *testing.T) {
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seed.db")
	testStartupRecordWork(t, func(name string) func() *SQLiteStore {
		if name == "seed" {
			return func() *SQLiteStore {
				repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: seedPath, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1,
					MaxTransactionBytes: recordWorkWriteBytes, MaxStateBytes: recordWorkWriteBytes})
				if err != nil {
					t.Fatal(err)
				}
				return repo
			}
		}
		path := filepath.Join(dir, name+".db")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			copyDatabase(t, seedPath, path)
		}
		return func() *SQLiteStore {
			repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
			if err != nil {
				t.Fatal(err)
			}
			return repo
		}
	})
}

// The MySQL start uses application journal capture, as in #264.
func TestBoundedMetadataStartupLargeRecordWorkMySQL(t *testing.T) {
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
		config.MaxTransactionBytes, config.MaxStateBytes = recordWorkWriteBytes, recordWorkWriteBytes
	})
	testStartupRecordWork(t, func(string) func() *SQLiteStore { return open })
}

// The per-record guard compares the record's work with the limit before it
// reserves anything, and reports both.
func TestMetadataRecordWorkLimitDiagnostic(t *testing.T) {
	if err := checkMetadataRecordWork("documents", 32<<20, 8, 64<<10, 1<<20, 512<<20); err != nil {
		t.Fatal("a record within the limit was refused", err)
	}
	err := checkMetadataRecordWork("documents", 48_497_478, 8, 64<<10, 1<<20, metadataWorkBytes())
	var record *metadataRecordWorkError
	if !errors.As(err, &record) || !errors.Is(err, ErrMetadataWorkBudget) || err.Error() != ErrMetadataWorkBudget.Error() {
		t.Fatal("record refusal is not a typed work budget error", err)
	}
	if record.collection != "documents" || record.cost != 48_497_478 || record.limit != uint64(metadataWorkBytes()) ||
		record.costLimit != (uint64(metadataWorkBytes())-(1<<20)-(64<<10))/8 || record.work != 48_497_478*8+(1<<20)+(64<<10) {
		t.Fatalf("record refusal sizes: %+v", record)
	}
	if boundedAdmissionReason(&metadataLogicalFieldError{field: "documents", err: err}) != metadataRecordWorkLimitReason {
		t.Fatal("record refusal reason")
	}
	if action := boundedAdmissionAction(err); !strings.Contains(action, "FLOW_METADATA_WORK_BYTES") {
		t.Fatal("record refusal action", action)
	}
	if err := checkMetadataRecordWork("documents", 1<<63, 8, 64<<10, 0, metadataWorkBytes()); !errors.As(err, &record) || record.work != 1<<64-1 {
		t.Fatal("an overflowing record's work is not saturated", err)
	}
}
