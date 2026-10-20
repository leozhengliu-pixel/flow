//go:build integration

package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// TestExternalDatabaseCodingSandboxes runs the coding sandbox leases on MySQL
// or PostgreSQL (FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL): two store
// instances lease concurrently through FOR UPDATE SKIP LOCKED without sharing
// a row, and observation, billing, tokens and warm binding round-trip.
func TestExternalDatabaseCodingSandboxes(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	open := func() *SQLiteStore {
		repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repo.Close() })
		return repo
	}
	first, second := open(), open()
	ctx := t.Context()
	workspace := fmt.Sprintf("cs-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM coding_sandbox_tokens WHERE workspace_key=?`, workspace)
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM coding_sandboxes WHERE workspace_key=?`, workspace)
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM coding_sessions WHERE workspace_key=?`, workspace)
	})
	prefix := fmt.Sprintf("x%d", time.Now().UnixNano()%1_000_000_000)
	ids := map[string]bool{}
	for i := range 12 {
		id := fmt.Sprintf("%sn%d", prefix, i)
		ids[id] = true
		if err := first.insertCodingSandbox(ctx, first.db, CodingSandbox{ID: id, WorkspaceKey: workspace, Kind: "session", Size: "small"}, "fake"); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	owners := map[string]string{}
	var group sync.WaitGroup
	for index, repo := range []*SQLiteStore{first, second, first, second} {
		group.Add(1)
		go func(owner string, repo *SQLiteStore) {
			defer group.Done()
			rows, err := repo.LeaseSandboxes(context.Background(), owner, time.Minute, 50)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, row := range rows {
				if !ids[row.ID] {
					continue
				}
				if previous, ok := owners[row.ID]; ok {
					t.Errorf("%s leased by %s and %s", row.ID, previous, owner)
				}
				owners[row.ID] = owner
			}
		}(fmt.Sprintf("owner-%d", index), repo)
	}
	group.Wait()
	for id, owner := range owners {
		if err := first.SaveSandboxObservationAt(ctx, owner, id, CodingSandboxObservation{Phase: "running", ProviderRef: "flow-cs-" + id}, 1, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	token, err := second.MintSandboxToken(ctx, CodingSandbox{ID: prefix + "n0", WorkspaceKey: workspace}, time.Hour)
	if err != nil || token == "" {
		t.Fatal(err)
	}
	if err := first.CreateWarmSandbox(ctx, CodingSandbox{ID: prefix + "w", WorkspaceKey: workspace, Size: "small", Phase: "running"}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.ExecContext(ctx, `INSERT INTO coding_sessions(id,workspace_key,agent_session_id,creator_id,source,state,phase,phase_started_at,data,created_at,updated_at) VALUES(?,?,?,?,?,'active','running',0,?,0,0)`, prefix+"s", workspace, prefix+"a", "u", "issue", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if bound, err := second.BindWarmCodingSandbox(ctx, "small", "", prefix+"s", ""); err != nil || bound.SessionID != prefix+"s" {
		t.Fatalf("bind = %+v %v", bound, err)
	}
}
