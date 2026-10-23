package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStartupLock is an in-memory lock shared by several "processes".
type fakeStartupLock struct {
	mu      sync.Mutex
	owner   *fakeStartupClient
	tries   atomic.Int64
	unlocks atomic.Int64
}

type fakeStartupClient struct {
	lock    *fakeStartupLock
	name    string
	failTry error
	closed  atomic.Bool
}

func (c *fakeStartupClient) tryLock(context.Context) (bool, error) {
	c.lock.tries.Add(1)
	if c.failTry != nil {
		return false, c.failTry
	}
	c.lock.mu.Lock()
	defer c.lock.mu.Unlock()
	if c.lock.owner == nil {
		c.lock.owner = c
		return true, nil
	}
	return false, nil
}

func (c *fakeStartupClient) holder(context.Context) string {
	c.lock.mu.Lock()
	defer c.lock.mu.Unlock()
	if c.lock.owner == nil {
		return "none"
	}
	return c.lock.owner.name
}

func (c *fakeStartupClient) unlock(context.Context) (bool, error) {
	c.lock.unlocks.Add(1)
	c.lock.mu.Lock()
	defer c.lock.mu.Unlock()
	if c.lock.owner != c {
		return false, nil
	}
	c.lock.owner = nil
	return true, nil
}

func (c *fakeStartupClient) ping(context.Context) error { return nil }
func (c *fakeStartupClient) close() error {
	c.closed.Store(true)
	return nil
}

func fastStartupLockOptions(timeout time.Duration) startupLockOptions {
	return startupLockOptions{name: "test", timeout: timeout, poll: time.Millisecond, maxPoll: 5 * time.Millisecond, logEvery: 10 * time.Millisecond, keepAlive: time.Millisecond}
}

func TestStartupLockSerializesHoldersAndWaitersSkipDoneWork(t *testing.T) {
	shared := &fakeStartupLock{}
	var migrated atomic.Int64 // the "schema version" each process re-checks
	var inside, maxInside atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for index := range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &fakeStartupClient{lock: shared, name: fmt.Sprintf("process-%d", index)}
			errs <- withStartupLock(context.Background(), client, fastStartupLockOptions(5*time.Second), func() error {
				now := inside.Add(1)
				for {
					seen := maxInside.Load()
					if now <= seen || maxInside.CompareAndSwap(seen, now) {
						break
					}
				}
				defer inside.Add(-1)
				// Re-check after acquiring: only the first holder migrates.
				if migrated.Load() == 0 {
					time.Sleep(20 * time.Millisecond)
					migrated.Add(1)
				}
				return nil
			})
			if !client.closed.Load() {
				errs <- fmt.Errorf("%s did not close its lock connection", client.name)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if maxInside.Load() != 1 {
		t.Fatalf("%d processes ran startup work at once", maxInside.Load())
	}
	if migrated.Load() != 1 {
		t.Fatalf("migration ran %d times", migrated.Load())
	}
	if shared.unlocks.Load() != 5 || shared.owner != nil {
		t.Fatalf("unlocks=%d owner=%v", shared.unlocks.Load(), shared.owner)
	}
}

func TestStartupLockTimesOutNamingTheHolder(t *testing.T) {
	shared := &fakeStartupLock{}
	holder := &fakeStartupClient{lock: shared, name: "flow host-a pid 42"}
	if ok, _ := holder.tryLock(context.Background()); !ok {
		t.Fatal("holder did not take the lock")
	}
	waiter := &fakeStartupClient{lock: shared, name: "waiter"}
	started := time.Now()
	ran := false
	err := withStartupLock(context.Background(), waiter, fastStartupLockOptions(50*time.Millisecond), func() error { ran = true; return nil })
	if !errors.Is(err, ErrStartupLockTimeout) {
		t.Fatalf("err = %v, want ErrStartupLockTimeout", err)
	}
	if ran {
		t.Fatal("action ran without the lock")
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("timed out after %s", elapsed)
	}
	for _, want := range []string{"flow host-a pid 42", "FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	if !waiter.closed.Load() {
		t.Fatal("waiter kept its lock connection open after timing out")
	}
	if shared.tries.Load() < 3 {
		t.Fatalf("waiter tried only %d times", shared.tries.Load())
	}
}

func TestStartupLockErrorsAndCancellation(t *testing.T) {
	shared := &fakeStartupLock{}
	broken := &fakeStartupClient{lock: shared, name: "broken", failTry: errors.New("connection refused")}
	if err := withStartupLock(context.Background(), broken, fastStartupLockOptions(time.Second), func() error { return nil }); err == nil || !strings.Contains(err.Error(), "connection refused") || !broken.closed.Load() {
		t.Fatalf("err=%v closed=%v", err, broken.closed.Load())
	}
	holder := &fakeStartupClient{lock: shared, name: "holder"}
	holder.tryLock(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	waiter := &fakeStartupClient{lock: shared, name: "waiter"}
	if err := withStartupLock(ctx, waiter, fastStartupLockOptions(time.Minute), func() error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// The action's error is returned and the lock is still released.
	holder.unlock(context.Background())
	actionErr := errors.New("migration failed")
	if err := withStartupLock(context.Background(), waiter, fastStartupLockOptions(time.Second), func() error { return actionErr }); !errors.Is(err, actionErr) {
		t.Fatalf("err = %v", err)
	}
	if shared.owner != nil {
		t.Fatal("lock still held after a failed action")
	}
	// No backend (in-memory SQLite): the action just runs.
	ran := false
	if err := withStartupLock(context.Background(), nil, startupLockOptions{}, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("err=%v ran=%v", err, ran)
	}
}

func TestStartupLockNames(t *testing.T) {
	if got := mysqlStartupLockName("flow"); got != "flow_migrate:flow" {
		t.Fatalf("mysql lock name %q", got)
	}
	long := mysqlStartupLockName(strings.Repeat("d", 64))
	if len(long) > 64 || long == mysqlStartupLockName(strings.Repeat("e", 64)) {
		t.Fatalf("long mysql lock name %q", long)
	}
	for path, want := range map[string]string{
		"data/flow.db":                 "data/flow.db.startup-lock",
		"/srv/flow.db?_pragma=x(1)":    "/srv/flow.db.startup-lock",
		":memory:":                     "",
		"file:test?mode=memory":        "",
		"file::memory:?cache=shared":   "",
		"   ":                          "",
		"/var/lib/flow/data/flow.db  ": "/var/lib/flow/data/flow.db.startup-lock",
	} {
		if got := sqliteLockPath(path); got != want {
			t.Fatalf("sqliteLockPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSQLiteStartupLockFileWaitsForHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db.startup-lock")
	holder := newFileStartupLock(path)
	if holder == nil {
		t.Skip("no file locks on this platform")
	}
	lock, err := acquireStartupLock(context.Background(), holder, fastStartupLockOptions(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	err = withStartupLock(context.Background(), newFileStartupLock(path), fastStartupLockOptions(30*time.Millisecond), func() error { return nil })
	if !errors.Is(err, ErrStartupLockTimeout) || !strings.Contains(err.Error(), startupProcessName()) {
		t.Fatalf("second opener: %v", err)
	}
	released := make(chan struct{})
	time.AfterFunc(30*time.Millisecond, func() { lock.release(); close(released) })
	ran := false
	if err := withStartupLock(context.Background(), newFileStartupLock(path), fastStartupLockOptions(5*time.Second), func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("waiter after release: err=%v ran=%v", err, ran)
	}
	<-released
}

// Several stores opening one fresh SQLite file at the same time take turns:
// every one opens and each migration is recorded once.
func TestConcurrentSQLiteOpensShareOneMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	const openers = 4
	stores := make([]*SQLiteStore, openers)
	errs := make([]error, openers)
	var wg sync.WaitGroup
	for index := range openers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[index], errs[index] = OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path, MaxOpenConns: 1, MigrationLockTimeout: time.Minute})
		}()
	}
	wg.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("opener %d: %v", index, err)
		}
		defer stores[index].Close()
	}
	var total, distinct int
	if err := stores[0].db.QueryRowContext(context.Background(), `SELECT COUNT(*), COUNT(DISTINCT version) FROM schema_migrations`).Scan(&total, &distinct); err != nil {
		t.Fatal(err)
	}
	if total == 0 || total != distinct {
		t.Fatalf("schema_migrations total=%d distinct=%d", total, distinct)
	}
}
