package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP15BSpoolConfigurationAndAccounting(t *testing.T) {
	for _, value := range []string{"", "invalid", "-1", "9223372036854775808"} {
		t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", value)
		if got := recordSpoolMaxBytes(); got != 256<<20 {
			t.Fatalf("%q: limit=%d", value, got)
		}
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "17")
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	file, err := NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if filepath.Dir(file.file.Name()) != dir {
		t.Fatal("spool ignored TMPDIR")
	}
	if n, err := file.Write([]byte("12345")); n != 5 || err != nil || recordSpoolBytes.Load() != 5 {
		t.Fatalf("actual write: n=%d err=%v used=%d", n, err, recordSpoolBytes.Load())
	}
	if n, err := file.Write(make([]byte, 13)); n != 0 || !errors.Is(err, ErrRecordSpoolBudget) || recordSpoolBytes.Load() != 5 {
		t.Fatalf("denied write: n=%d err=%v used=%d", n, err, recordSpoolBytes.Load())
	}
	// A failed OS write must return its unused reservation immediately.
	if err := file.file.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := file.Write([]byte("123")); n != 0 || err == nil || recordSpoolBytes.Load() != 5 {
		t.Fatalf("failed write: n=%d err=%v used=%d", n, err, recordSpoolBytes.Load())
	}
	_ = file.Close()
	_ = file.Close()
	if recordSpoolBytes.Load() != 0 {
		t.Fatal("close did not release exactly the written bytes")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatalf("spool leaked: %v %v", files, err)
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "0")
	if _, err := NewRecordSpool(); !errors.Is(err, ErrRecordSpoolBudget) {
		t.Fatalf("zero budget: %v", err)
	}
}

func TestScaleP15BConcurrentSpoolsStayWithinProcessBudget(t *testing.T) {
	const limit = 256 << 10
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(limit))
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	var ready, finished sync.WaitGroup
	ready.Add(8)
	finished.Add(8)
	release := make(chan struct{})
	for range 8 {
		go func() {
			defer finished.Done()
			file, err := NewRecordSpool()
			if err != nil {
				if !errors.Is(err, ErrRecordSpoolBudget) {
					t.Error(err)
				}
				ready.Done()
				return
			}
			defer file.Close()
			chunk := make([]byte, 4096)
			for range 512 {
				if _, err := file.Write(chunk); err != nil {
					if !errors.Is(err, ErrRecordSpoolBudget) {
						t.Error(err)
					}
					break
				}
				if used := recordSpoolBytes.Load(); used > limit {
					t.Errorf("budget exceeded: %d", used)
				}
			}
			ready.Done()
			<-release
		}()
	}
	ready.Wait()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var actual int64
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			t.Fatal(err)
		}
		actual += info.Size()
	}
	if actual != limit || recordSpoolBytes.Load() != actual {
		t.Errorf("disk=%d charged=%d budget=%d", actual, recordSpoolBytes.Load(), limit)
	}
	close(release)
	finished.Wait()
	if recordSpoolBytes.Load() != 0 {
		t.Fatal("concurrent spools leaked their reservations")
	}
}

func TestScaleP15BSpoolChargesPartialOSWrites(t *testing.T) {
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(1<<20))
	t.Setenv("TMPDIR", t.TempDir())
	spool, err := NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	read := make(chan error, 1)
	go func() {
		var first [1]byte
		_, err := io.ReadFull(reader, first[:])
		_ = reader.Close()
		read <- err
	}()
	// A reader closing mid-write produces a real OS short write without
	// changing filesystem quotas. Restore the owned file before cleanup.
	owned := spool.file
	spool.file = writer
	n, writeErr := spool.Write(make([]byte, 1<<20))
	spool.file = owned
	if err := <-read; err != nil {
		t.Fatal(err)
	}
	if n <= 0 || n >= 1<<20 || writeErr == nil || recordSpoolBytes.Load() != int64(n) {
		t.Fatalf("partial write: n=%d err=%v charged=%d", n, writeErr, recordSpoolBytes.Load())
	}
	_ = spool.Close()
	if recordSpoolBytes.Load() != 0 {
		t.Fatal("partial write reservation leaked")
	}
}

func TestScaleP15BCanonicalStreamFallbackAndCleanup(t *testing.T) {
	repo, _ := scaleP04OwnedStore(t)
	workspace := repo.Bootstrap().Workspace.URLKey
	description := make([]byte, 256<<10)
	for i := range description {
		description[i] = byte('a' + i/(32<<10))
	}
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p15b.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "p15b-stream", Description: string(description)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	key := RecordKey{"projects", "p15b-stream"}
	var want []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.Kind, key.ID).Scan(&want); err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{0, 64 << 10, 1 << 20} {
		for _, mode := range []string{"success", "error", "cancel", "panic"} {
			t.Run(strconv.Itoa(budget)+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				t.Setenv("TMPDIR", dir)
				t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(budget))
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var got bytes.Buffer
				failure := errors.New("destination failed")
				dst := scaleP15BWriterFunc(func(p []byte) (int, error) {
					switch mode {
					case "error":
						return 0, failure
					case "cancel":
						cancel()
					case "panic":
						panic(failure)
					}
					return got.Write(p)
				})
				func() {
					defer func() {
						if recovered := recover(); recovered != nil && (mode != "panic" || recovered != failure) {
							t.Errorf("panic=%v", recovered)
						}
					}()
					err := repo.StreamCanonicalMetadataRecord(ctx, workspace, key, dst)
					switch mode {
					case "success":
						if err != nil || !bytes.Equal(got.Bytes(), want) {
							t.Errorf("canonical bytes changed: %v", err)
						}
					case "error":
						if !errors.Is(err, failure) {
							t.Errorf("destination error=%v", err)
						}
					case "cancel":
						if !errors.Is(err, context.Canceled) {
							t.Errorf("cancellation=%v", err)
						}
					case "panic":
						t.Error("missing destination panic")
					}
				}()
				files, err := os.ReadDir(dir)
				used := recordSpoolBytes.Load()
				// database/sql can finish its cancellation-triggered rollback
				// after Rollback has already returned ErrTxDone to our defer.
				deadline := time.Now().Add(2 * time.Second)
				for repo.db.Stats().InUse != 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if err != nil || len(files) != 0 || used != 0 || repo.db.Stats().InUse != 0 {
					t.Fatalf("leaked files=%v budget=%d SQL=%d error=%v", files, used, repo.db.Stats().InUse, err)
				}
			})
		}
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if err := repo.StreamCanonicalMetadataRecord(t.Context(), workspace, key, io.Discard); err != nil {
		t.Fatalf("unavailable TMPDIR failed request: %v", err)
	}
}

type scaleP15BWriterFunc func([]byte) (int, error)

func (f scaleP15BWriterFunc) Write(p []byte) (int, error) { return f(p) }
