//go:build unix

package store

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// withFreshMetadataSpool gives the test its own TMPDIR and process spool
// state, and restores both.
func withFreshMetadataSpool(t *testing.T) string {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)
	metadataSpool.mu.Lock()
	saved := struct {
		dir  string
		lock *os.File
	}{metadataSpool.dir, metadataSpool.lock}
	metadataSpool.dir, metadataSpool.lock = "", nil
	metadataSpool.mu.Unlock()
	t.Cleanup(func() {
		_ = RemoveMetadataSpoolDirectory()
		metadataSpool.mu.Lock()
		metadataSpool.dir, metadataSpool.lock = saved.dir, saved.lock
		metadataSpool.mu.Unlock()
	})
	return tmp
}

func TestMetadataSpoolDirectoryLayout(t *testing.T) {
	tmp := withFreshMetadataSpool(t)
	dir := metadataSpoolDirectory("")
	if filepath.Dir(dir) != tmp || !isMetadataSpoolDirectoryName(filepath.Base(dir)) || !strings.HasPrefix(filepath.Base(dir), "flow-p21-"+strconv.Itoa(os.Getpid())+"-") {
		t.Fatal("unexpected spool directory", dir)
	}
	if again := metadataSpoolDirectory(""); again != dir {
		t.Fatal("spool directory changed", dir, again)
	}
	// A later TMPDIR change cannot move installed spools.
	t.Setenv("TMPDIR", filepath.Join(tmp, "missing"))
	if again := metadataSpoolDirectory(""); again != dir {
		t.Fatal("TMPDIR change moved the spool directory", dir, again)
	}
	if custom := metadataSpoolDirectory("/x"); custom != "/x" {
		t.Fatal("configured directory ignored")
	}
}

func TestMetadataSpoolShutdownRemovesInstalledFiles(t *testing.T) {
	tmp := withFreshMetadataSpool(t)
	f, view, _ := scaleP21SW27KnownWarmReloadFixture(t)
	workspace := f.data.Workspace.URLKey
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	var names []string
	for _, storage := range source.stores {
		if name := storage.Name(); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		t.Fatal("installed source has no spool file")
	}
	for _, name := range names {
		if filepath.Dir(filepath.Dir(name)) != tmp {
			t.Fatal("spool file outside the per-process directory", name)
		}
	}
	// What SQLiteStore.Close does first, with the reader still holding its
	// reference: the file stays until the last reader releases it ...
	view.releaseMetadataLogicalSources()
	f.releaseMetadataLogicalSources()
	source.Release()
	source = nil
	for _, name := range names {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatal("spool file survived the release of every source", name, err)
		}
	}
	dir := metadataSpoolDirectory("")
	if err := RemoveMetadataSpoolDirectory(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("process spool directory survived shutdown", err)
	}
}

func TestMetadataSpoolShutdownRemovesLeakedFiles(t *testing.T) {
	withFreshMetadataSpool(t)
	dir := metadataSpoolDirectory("")
	leaked := filepath.Join(dir, "flow-p21-logical-leak")
	if err := os.WriteFile(leaked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMetadataSpoolDirectory(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Fatal("leaked spool file survived", err)
	}
}

func TestMetadataSpoolStaleDirectorySweep(t *testing.T) {
	tmp := withFreshMetadataSpool(t)
	// A dead process: directory and lock file, nobody holds the lock.
	stale := filepath.Join(tmp, "flow-p21-999999-dead")
	mustMkdirFile(t, filepath.Join(stale, metadataSpoolLockName))
	mustMkdirFile(t, filepath.Join(stale, "flow-p21-logical-1"))
	// A process between mkdir and its lock file: young, kept.
	young := filepath.Join(tmp, "flow-p21-999998-young")
	mustMkdirFile(t, filepath.Join(young, "flow-p21-logical-2"))
	// An old directory with no lock file: removed.
	old := filepath.Join(tmp, "flow-p21-999997-old")
	mustMkdirFile(t, filepath.Join(old, "flow-p21-logical-3"))
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	// Names that are not spool directories are never touched.
	other := filepath.Join(tmp, "flow-p21-notes")
	mustMkdirFile(t, filepath.Join(other, ".lock"))
	legacy := filepath.Join(tmp, "flow-p21-logical-123")
	mustMkdirFile(t, legacy)
	// A live holder in this process (a second file description): kept.
	live := filepath.Join(tmp, "flow-p21-999996-live")
	mustMkdirFile(t, filepath.Join(live, "flow-p21-logical-4"))
	lock, err := os.OpenFile(filepath.Join(live, metadataSpoolLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if ok, err := lockMetadataSpoolFile(lock); err != nil || !ok {
		t.Fatal("test lock", ok, err)
	}
	own := metadataSpoolDirectory("") // runs the sweep
	for path, kept := range map[string]bool{stale: false, old: false, young: true, live: true, other: true, legacy: true, own: true} {
		_, err := os.Stat(path)
		if kept != (err == nil) {
			t.Errorf("%s kept=%v, want %v (%v)", filepath.Base(path), err == nil, kept, err)
		}
	}
}

func mustMkdirFile(t *testing.T, path string) {
	t.Helper()
	if filepath.Base(path) == "flow-p21-logical-123" {
		// A plain file named like a legacy spool file.
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMetadataSpoolProcessHelper is the child of the concurrent-process test:
// it creates a spool directory with a file, prints the directory, and lives
// until its stdin closes.
func TestMetadataSpoolProcessHelper(t *testing.T) {
	if os.Getenv("FLOW_SPOOL_HELPER") != "1" {
		t.Skip("helper process")
	}
	dir := metadataSpoolDirectory("")
	if err := os.WriteFile(filepath.Join(dir, "flow-p21-logical-child"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("DIR " + dir + "\n")
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	if os.Getenv("FLOW_SPOOL_HELPER_CLEAN") == "1" {
		_ = RemoveMetadataSpoolDirectory()
	}
}

type spoolHelper struct {
	cmd   *exec.Cmd
	stdin interface{ Close() error }
	dir   string
}

func startSpoolHelper(t *testing.T, tmp string) *spoolHelper {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMetadataSpoolProcessHelper$")
	cmd.Env = append(os.Environ(), "FLOW_SPOOL_HELPER=1", "TMPDIR="+tmp)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &spoolHelper{cmd: cmd, stdin: stdin}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if dir, ok := strings.CutPrefix(scanner.Text(), "DIR "); ok {
			h.dir = dir
			return h
		}
	}
	t.Fatal("helper process printed no directory")
	return nil
}

func TestMetadataSpoolConcurrentProcessesKeepEachOthersFiles(t *testing.T) {
	tmp := withFreshMetadataSpool(t)
	first := startSpoolHelper(t, tmp)
	second := startSpoolHelper(t, tmp) // sweeps at its own startup
	third := startSpoolHelper(t, tmp)
	_ = metadataSpoolDirectory("") // and so does this process
	for _, h := range []*spoolHelper{first, second, third} {
		if _, err := os.Stat(filepath.Join(h.dir, "flow-p21-logical-child")); err != nil {
			t.Fatal("a live process lost its spool file", err)
		}
	}
	// Killed without cleanup: the next sweep removes exactly that directory.
	if err := second.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = second.cmd.Wait()
	removeStaleMetadataSpoolDirectories(tmp)
	if _, err := os.Stat(second.dir); !os.IsNotExist(err) {
		t.Fatal("the dead process's directory was not removed", err)
	}
	for _, h := range []*spoolHelper{first, third} {
		if _, err := os.Stat(filepath.Join(h.dir, "flow-p21-logical-child")); err != nil {
			t.Fatal("sweep removed a live process's file", err)
		}
	}
	// A clean exit of one process leaves the others alone.
	_ = first.stdin.Close()
	_ = first.cmd.Wait()
	if _, err := os.Stat(filepath.Join(third.dir, "flow-p21-logical-child")); err != nil {
		t.Fatal("a peer's exit removed this process's file", err)
	}
}
