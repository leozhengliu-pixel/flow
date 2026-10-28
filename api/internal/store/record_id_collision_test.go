package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Servers sharing one database each write their own workspace at full speed:
// nothing serialises them (each workspace has its own event sequence), but
// every write adds a domain_events row and that table's id is global. Ids
// generated in the same clock tick on two servers must still differ, or the
// later write fails on a duplicate key (MySQL 1062, Postgres 23505).
//
// "clock" runs on the real clock; "same tick" makes every id look as if a
// different server generated it in one tick, so only the random part of the
// id keeps them apart. On MySQL / Postgres (FLOW_TEST_DATABASE_DRIVER /
// FLOW_TEST_DATABASE_URL) every writer has its own store, as servers do;
// SQLite runs one writer at a time, so its writers share one store.
// FLOW_RECORD_ID_SERVERS sets the writer count (16).
func TestDomainEventIDsDistinctAcrossServers(t *testing.T) {
	servers := 16
	if n, _ := strconv.Atoi(os.Getenv("FLOW_RECORD_ID_SERVERS")); n > 0 {
		servers = n
	}
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	ctx := context.Background()
	stores := make([]*SQLiteStore, servers)
	if driver != "" && databaseURL != "" {
		for index := range stores {
			repository, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 2, MaxIdleConns: 2})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { repository.Close() })
			stores[index] = repository
		}
	} else {
		repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "ids.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repository.Close() })
		for index := range stores {
			stores[index] = repository
		}
	}
	workspaces := make([]string, servers)
	stamp := time.Now().UnixNano() % 1_000_000
	for index, repository := range stores {
		workspace := fmt.Sprintf("record-ids-%d-%d", stamp, index)
		data, err := repository.CreateWorkspace(ctx, "Record ids", workspace, "us")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repository.DeleteWorkspace(ctx, workspace) })
		now := time.Now().UTC()
		issue := domain.Issue{ID: fmt.Sprintf("rid-%d", index), Identifier: "RID-1", Number: 1, Title: "ids", Team: data.Teams[0], State: data.States[0], Creator: data.Viewer, Version: 1, SubIssueIDs: []string{}, CreatedAt: now, UpdatedAt: now}
		if err := repository.ImportIssues(ctx, workspace, []domain.Issue{issue}); err != nil {
			t.Fatal(err)
		}
		workspaces[index] = workspace
	}
	run := func(t *testing.T, updates int) {
		var failures, duplicates atomic.Int64
		var failure, duplicate atomic.Value
		var wg sync.WaitGroup
		start := make(chan struct{})
		for index := range stores {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for k := range updates {
					_, err := stores[index].UpdateIssueRecord(ctx, workspaces[index], fmt.Sprintf("rid-%d", index), nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
						issue.Title = fmt.Sprintf("ids %d %d", k, time.Now().UnixNano())
						return nil
					})
					if err != nil {
						failures.Add(1)
						failure.Store(err.Error())
						if isCodingUniqueViolation(err) {
							duplicates.Add(1)
							duplicate.Store(err.Error())
						}
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		if duplicates.Load() > 0 {
			t.Fatalf("%d of %d writes failed on a duplicate key: %v", duplicates.Load(), servers*updates, duplicate.Load())
		}
		if failures.Load() > 0 {
			t.Fatalf("%d writes failed: %v", failures.Load(), failure.Load())
		}
	}
	t.Run("clock", func(t *testing.T) { run(t, 150) })
	t.Run("same tick", func(t *testing.T) {
		at := time.Now().UTC()
		defer idgen.SimulateServersForTest(at)()
		run(t, 40)
		// Every event of the run did share the time part.
		var shared int
		if err := stores[0].db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE id LIKE ?`, fmt.Sprintf("evt_%d_%%", at.UnixNano())).Scan(&shared); err != nil {
			t.Fatal(err)
		}
		if shared < servers*40 {
			t.Fatalf("%d events share the frozen time part, want at least %d", shared, servers*40)
		}
	})
}
