package main

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type syncAccessRaceKey struct {
	Key    domain.APIKey `json:"key"`
	Secret string        `json:"secret"`
}

// setSyncAccessRaceHook installs syncAccessSourceSelectedHook for one test.
// The hook runs on the server's request goroutine, so failures are recorded
// and reported by the test goroutine.
func setSyncAccessRaceHook(t *testing.T, hook func(ctx context.Context, attempt int) error) (attempts func() int, failure func() error) {
	t.Helper()
	var mu sync.Mutex
	var last int
	var failed error
	syncAccessSourceSelectedHook = func(ctx context.Context, attempt int) {
		mu.Lock()
		defer mu.Unlock()
		last = max(last, attempt)
		if err := hook(ctx, attempt); err != nil && failed == nil {
			failed = err
		}
	}
	t.Cleanup(func() { syncAccessSourceSelectedHook = nil })
	return func() int {
			mu.Lock()
			defer mu.Unlock()
			return last
		}, func() error {
			mu.Lock()
			defer mu.Unlock()
			return failed
		}
}

// resetSyncAccessCache drops this server's cached event accesses. A cached
// access for an unchanged epoch is reused without resolving, so the race is
// reached by another replica or after eviction; this stands in for both.
func resetSyncAccessCache(s *server) {
	if s.realtime == nil {
		return
	}
	cache := &s.realtime.rules.accesses
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.entries, cache.order, cache.bytes, cache.sources = nil, nil, 0, nil
}

type syncAccessRaceError string

func (e syncAccessRaceError) Error() string { return string(e) }

// recordUnrelatedKeyUse commits the asynchronous API key first-use update and
// waits until it advanced the workspace metadata revision without moving the
// authorization epoch, as reported in the issue.
func recordUnrelatedKeyUse(ctx context.Context, repo *store.SQLiteStore, workspace string, key domain.APIKey) error {
	before, epoch, err := repo.RealtimeSourceVersionAndEpoch(ctx, workspace)
	if err != nil {
		return err
	}
	if err := repo.RecordAPIKeyUse(ctx, workspace, key.ID, ""); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		after, afterEpoch, err := repo.RealtimeSourceVersionAndEpoch(ctx, workspace)
		if err != nil {
			return err
		}
		if after.Metadata > before.Metadata {
			if afterEpoch != epoch {
				return syncAccessRaceError("API key usage moved the authorization epoch")
			}
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return syncAccessRaceError("API key usage did not advance the metadata revision")
}

func syncAccessRaceStores(t *testing.T, run func(t *testing.T, repo *store.SQLiteStore)) {
	for _, bounded := range []string{"off", "on"} {
		t.Run("bounded-"+bounded, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", bounded)
			t.Setenv("FLOW_RECORD_HYDRATION", "on")
			t.Setenv("FLOW_RECORD_JOURNAL", "on")
			t.Setenv("FLOW_SYNC_V2", "on")
			for driver, repo := range scaleP03ServerTestStores(t) {
				t.Run(driver, func(t *testing.T) {
					if driver == "mysql" {
						t.Logf("record journal capture: %s", repo.RecordJournalCapture())
					}
					if !repo.RecordJournalReady() || !repo.SyncV2Enabled() {
						t.Fatal("record journal and Sync V2 must be ready")
					}
					run(t, repo)
				})
			}
		})
	}
}

func syncAccessRaceCursor(t *testing.T, f scaleP03PlatformFixture) string {
	t.Helper()
	cursor, err := f.repository.RecordJournalHead(t.Context(), f.data.Workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}

func syncAccessRaceKeys(t *testing.T, f scaleP03PlatformFixture, count int) []syncAccessRaceKey {
	t.Helper()
	keys := make([]syncAccessRaceKey, 0, count)
	for index := range count {
		keys = append(keys, authRequest[syncAccessRaceKey](t, f.admin, http.MethodPost, f.host.URL+"/api/api-keys",
			map[string]any{"name": "Sync race key " + strconv.Itoa(index) + " " + strconv.FormatInt(time.Now().UnixNano(), 10), "scopes": []string{"read"}}, f.data.Workspace.URLKey, http.StatusCreated))
	}
	return keys
}

// Issue #261: an unrelated metadata write (API key usage telemetry) that
// commits after the policy source was selected and before access is resolved
// from it is a revision race, not a denial. The request re-resolves against
// current authority and answers 200 with the pending changes.
func TestSyncChangesAccessRaceWithAPIKeyUse(t *testing.T) {
	syncAccessRaceStores(t, func(t *testing.T, repo *store.SQLiteStore) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		key := syncAccessRaceKeys(t, f, 1)[0]
		// A first successful read, as a client polling Sync V2 would have.
		cursor := syncAccessRaceCursor(t, f)
		authRequest[syncV2ChangesResponse](t, f.admin, http.MethodGet, f.host.URL+"/api/sync/changes?after="+cursor, nil, workspace, http.StatusOK)
		created := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Pending sync change", "teamId": f.data.Teams[0].ID}, workspace, http.StatusCreated)

		resetSyncAccessCache(f.service)
		var fired atomic.Bool
		attempts, failure := setSyncAccessRaceHook(t, func(ctx context.Context, attempt int) error {
			if attempt != 1 || !fired.CompareAndSwap(false, true) {
				return nil
			}
			return recordUnrelatedKeyUse(ctx, repo, workspace, key.Key)
		})
		raced := authRequest[syncV2ChangesResponse](t, f.admin, http.MethodGet, f.host.URL+"/api/sync/changes?after="+cursor, nil, workspace, http.StatusOK)
		if err := failure(); err != nil {
			t.Fatal(err)
		}
		if !fired.Load() || attempts() < 2 {
			t.Fatalf("the unrelated write did not race access resolution: fired=%v attempts=%d", fired.Load(), attempts())
		}
		syncAccessSourceSelectedHook = nil
		if raced.ResyncRequired || raced.Next == cursor || len(raced.Transactions) == 0 {
			t.Fatalf("raced read did not return the pending changes: %+v", raced)
		}
		// The issue creation arrives as an upsert of the issue or, for a
		// summary-only issue transaction, an invalidation of issues.
		found := slices.ContainsFunc(raced.Transactions, func(tx syncV2Transaction) bool {
			return slices.Contains(tx.Invalidations, "issues") ||
				slices.ContainsFunc(tx.Upserts, func(upsert syncV2Upsert) bool { return upsert.Key.ID == created.ID })
		})
		if !found {
			t.Fatalf("raced read did not project the created issue: %+v", raced)
		}
		repeat := authRequest[syncV2ChangesResponse](t, f.admin, http.MethodGet, f.host.URL+"/api/sync/changes?after="+cursor, nil, workspace, http.StatusOK)
		if raced.Next != repeat.Next || raced.AuthzEpoch != repeat.AuthzEpoch || raced.HasMore != repeat.HasMore || !reflect.DeepEqual(syncAccessRaceShape(raced), syncAccessRaceShape(repeat)) {
			t.Fatalf("raced page differs from a settled read:\nraced=%+v\nrepeat=%+v", raced, repeat)
		}
	})
}

// syncAccessRaceShape compares pages without upsert versions, which a later
// read may legitimately observe as newer for a record touched in between.
func syncAccessRaceShape(page syncV2ChangesResponse) []string {
	shape := []string{}
	for _, tx := range page.Transactions {
		shape = append(shape, "tx:"+tx.TxID+":"+tx.Seq)
		for _, upsert := range tx.Upserts {
			shape = append(shape, "upsert:"+upsert.Key.Kind+":"+upsert.Key.ID)
		}
		for _, removal := range tx.Removals {
			shape = append(shape, "removal:"+removal.Key.Kind+":"+removal.Key.ID)
		}
		shape = append(shape, tx.Invalidations...)
	}
	return shape
}

// A race that persists through every bounded attempt answers a retryable 503
// with Retry-After, never 403.
func TestSyncChangesAccessRaceExhaustedIsRetryable(t *testing.T) {
	syncAccessRaceStores(t, func(t *testing.T, repo *store.SQLiteStore) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		keys := syncAccessRaceKeys(t, f, syncAccessAttempts)
		cursor := syncAccessRaceCursor(t, f)
		attempts, failure := setSyncAccessRaceHook(t, func(ctx context.Context, attempt int) error {
			if attempt > len(keys) {
				return syncAccessRaceError("resolution exceeded its attempt bound")
			}
			return recordUnrelatedKeyUse(ctx, repo, workspace, keys[attempt-1].Key)
		})
		w := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/sync/changes?after="+cursor, workspace)
		if err := failure(); err != nil {
			t.Fatal(err)
		}
		if attempts() != syncAccessAttempts {
			t.Fatalf("attempts = %d, want %d", attempts(), syncAccessAttempts)
		}
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
			t.Fatalf("exhausted race: %d Retry-After=%q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
		}
	})
}

// Genuine denials in the same window still answer 403: a member removed from
// the workspace, and an API key revoked, between source selection and access
// resolution.
func TestSyncChangesAccessRaceKeepsGenuineDenials(t *testing.T) {
	syncAccessRaceStores(t, func(t *testing.T, repo *store.SQLiteStore) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		cursor := syncAccessRaceCursor(t, f)

		t.Run("removed-member", func(t *testing.T) {
			client, member := scaleP03Join(t, f, "Sync race member", "sync-race-"+strconv.FormatInt(time.Now().UnixNano(), 10)+"@example.test", "member", f.data.Teams[0].ID)
			authRequest[syncV2ChangesResponse](t, client, http.MethodGet, f.host.URL+"/api/sync/changes?after="+cursor, nil, workspace, http.StatusOK)
			resetSyncAccessCache(f.service)
			var fired atomic.Bool
			_, failure := setSyncAccessRaceHook(t, func(ctx context.Context, attempt int) error {
				if !fired.CompareAndSwap(false, true) {
					return nil
				}
				return repo.RemoveMember(context.WithoutCancel(ctx), f.data.Workspace.ID, member.ID)
			})
			w := scaleP03Read(t, f, client, "", http.MethodGet, "/api/sync/changes?after="+cursor, workspace)
			if err := failure(); err != nil {
				t.Fatal(err)
			}
			if !fired.Load() || w.Code != http.StatusForbidden {
				t.Fatalf("removed member: fired=%v %d %s", fired.Load(), w.Code, w.Body)
			}
		})

		t.Run("revoked-key", func(t *testing.T) {
			key := syncAccessRaceKeys(t, f, 1)[0]
			if w := scaleP03Read(t, f, nil, key.Secret, http.MethodGet, "/api/sync/changes?after="+cursor, workspace); w.Code != http.StatusOK {
				t.Fatalf("key read before revocation: %d %s", w.Code, w.Body)
			}
			resetSyncAccessCache(f.service)
			var fired atomic.Bool
			_, failure := setSyncAccessRaceHook(t, func(ctx context.Context, attempt int) error {
				if !fired.CompareAndSwap(false, true) {
					return nil
				}
				if status := authRequestStatus(t, f.admin, http.MethodDelete, f.host.URL+"/api/api-keys/"+key.Key.ID, nil, workspace); status != http.StatusNoContent {
					return syncAccessRaceError("revoke answered " + strconv.Itoa(status))
				}
				return nil
			})
			w := scaleP03Read(t, f, nil, key.Secret, http.MethodGet, "/api/sync/changes?after="+cursor, workspace)
			if err := failure(); err != nil {
				t.Fatal(err)
			}
			if !fired.Load() || w.Code != http.StatusForbidden {
				t.Fatalf("revoked key: fired=%v %d %s", fired.Load(), w.Code, w.Body)
			}
		})
	})
}
