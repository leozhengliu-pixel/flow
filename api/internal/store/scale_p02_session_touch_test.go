package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Count statements at the driver, including UPDATEs that match no rows.
type scaleP02SQL struct {
	selects, updates, affected               atomic.Int64
	failTouch, failSessionRead, failUserRead atomic.Bool
	beforeTouch                              func()
}

type scaleP02Connector struct {
	driver driver.Driver
	dsn    string
	stats  *scaleP02SQL
}

func (c scaleP02Connector) Driver() driver.Driver { return c.driver }

func (c scaleP02Connector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &scaleP02Conn{Conn: conn, stats: c.stats}, nil
}

type scaleP02Conn struct {
	driver.Conn
	stats *scaleP02SQL
}

func (c *scaleP02Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(query, "SELECT ") {
		c.stats.selects.Add(1)
	}
	if c.stats.failSessionRead.Load() && strings.Contains(query, "FROM auth_sessions") || c.stats.failUserRead.Load() && strings.Contains(query, "FROM auth_users") {
		return nil, errors.New("injected authentication read failure")
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *scaleP02Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	touch := strings.HasPrefix(query, "UPDATE auth_sessions SET last_seen_at=")
	if touch {
		c.stats.updates.Add(1)
		if c.stats.beforeTouch != nil {
			c.stats.beforeTouch()
		}
		if c.stats.failTouch.Load() {
			return nil, errors.New("injected session touch failure")
		}
	}
	result, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
	if touch && err == nil {
		rows, _ := result.RowsAffected()
		c.stats.affected.Add(rows)
	}
	return result, err
}

func scaleP02Store(t *testing.T) (*SQLiteStore, *scaleP02SQL) {
	t.Helper()
	return scaleP02StoreAt(t, scaleP02Config(t))
}

func scaleP02Config(t *testing.T) DatabaseConfig {
	t.Helper()
	config := DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "sessions.db"), MaxOpenConns: 1}
	databaseDriver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (databaseDriver == "") != (databaseURL == "") {
		t.Fatal("P02 database tests require both FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL")
	}
	if databaseDriver != "" {
		config.Driver, config.URL = databaseDriver, databaseURL
		if databaseDriver == "sqlite" {
			config.Path = databaseURL
		} else {
			// External audit-head initialization writes outside its transaction.
			config.MaxOpenConns = 4
		}
	}
	return config
}

func scaleP02StoreAt(t *testing.T, config DatabaseConfig) (*SQLiteStore, *scaleP02SQL) {
	t.Helper()
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	dsn := config.URL
	switch config.Driver {
	case "sqlite":
		dsn = sqliteDSN(config.Path)
	case "mysql":
		dsn, err = mysqlDSN(config.URL)
		if err != nil {
			t.Fatal(err)
		}
	}
	base := repo.db.DB
	stats := &scaleP02SQL{}
	repo.db.DB = sql.OpenDB(scaleP02Connector{driver: base.Driver(), dsn: dsn, stats: stats})
	repo.db.SetMaxOpenConns(config.MaxOpenConns)
	repo.db.SetMaxIdleConns(1)
	t.Cleanup(func() {
		_ = repo.Close()
		_ = base.Close()
	})
	if repo.db.journalWrites != nil {
		// Prepared journal statements belong to the pool that created them.
		for _, statement := range repo.db.journalWrites {
			_ = statement.Close()
		}
		repo.db.journalWrites = nil
		_ = repo.db.journalSQLFlush.Close()
		repo.db.journalSQLFlush = nil
		if err := repo.ensureSQLiteRecordJournalCommit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	return repo, stats
}

func scaleP02User(t *testing.T, repo *SQLiteStore) domain.User {
	t.Helper()
	user, verification, err := repo.Register(t.Context(), "P02 session user", idgen.New("scale-p02-store-")+"@example.test", "test-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := repo.EraseAccount(ctx, user.ID); err != nil {
			t.Errorf("clean up owned account: %v", err)
		}
	})
	if err := repo.VerifyEmail(t.Context(), verification); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	return user
}

func TestScaleP02SQLCounterPreservesPoolCapacity(t *testing.T) {
	config := scaleP02Config(t)
	if config.Driver == "sqlite" {
		config.MaxOpenConns = 4
	} else if config.MaxOpenConns < 2 {
		t.Fatal("external fixture needs a connection outside an open audit transaction")
	}
	repo, _ := scaleP02StoreAt(t, config)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var one int
	if err := repo.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("statement outside open transaction: value=%d error=%v", one, err)
	}
}

func TestScaleP02SessionTouchMeasurement(t *testing.T) {
	t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "")
	repo, stats := scaleP02Store(t)
	ctx := t.Context()
	user := scaleP02User(t, repo)
	_, token, err := repo.createSession(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Logout(context.Background(), token) })
	const requests = 2000
	stats.selects.Store(0)
	stats.updates.Store(0)
	stats.affected.Store(0)
	started := time.Now()
	for range requests {
		if got, err := repo.AuthenticateSession(ctx, token); err != nil || got.ID != user.ID {
			t.Fatalf("authentication: user=%s error=%v", got.ID, err)
		}
	}
	t.Logf("requests=%d SELECT=%d touch_UPDATE=%d affected=%d SELECT/request=%.3f UPDATE/request=%.3f elapsed=%s", requests, stats.selects.Load(), stats.updates.Load(), stats.affected.Load(), float64(stats.selects.Load())/requests, float64(stats.updates.Load())/requests, time.Since(started))
	if got := stats.selects.Load(); got != 2*requests {
		t.Fatalf("SELECT count=%d, want %d", got, 2*requests)
	}
	if got := stats.updates.Load(); got != 0 {
		t.Fatalf("fresh session issued %d UPDATEs, want 0", got)
	}
}

func TestScaleP02SessionTouchInterval(t *testing.T) {
	for _, item := range []struct {
		value string
		want  time.Duration
	}{{"", time.Minute}, {"0", 0}, {" 0 ", 0}, {"30s", 30 * time.Second}, {"60s", time.Minute}, {"61s", time.Minute}, {"-1s", time.Minute}, {"invalid", time.Minute}} {
		t.Run(item.value, func(t *testing.T) {
			t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", item.value)
			if got := sessionTouchInterval(); got != item.want {
				t.Fatalf("interval=%s, want %s", got, item.want)
			}
		})
	}
}

func TestScaleP02SessionTouchBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		name, raw string
		want      bool
	}{
		{"just-before", now.Add(-time.Minute + time.Nanosecond).Format(time.RFC3339Nano), false},
		{"exactly", now.Add(-time.Minute).Format(time.RFC3339Nano), true},
		{"just-after", now.Add(-time.Minute - time.Nanosecond).Format(time.RFC3339Nano), true},
		{"offset-equal-instant", "2026-10-01T13:59:30+02:00", false},
		{"offset-due", "2026-10-01T10:59:00-01:00", true},
		{"fractional", "2026-10-01T11:59:00.1Z", false},
		{"future", now.Add(time.Second).Format(time.RFC3339Nano), false},
		{"empty", "", true},
		{"malformed", "bad timestamp", true},
	} {
		t.Run(item.name, func(t *testing.T) {
			if got := sessionTouchDue(item.raw, now, time.Minute); got != item.want {
				t.Fatalf("due=%v, want %v", got, item.want)
			}
			if !sessionTouchDue(item.raw, now, 0) {
				t.Fatal("legacy mode skipped an UPDATE")
			}
		})
	}
}

func scaleP02Session(t *testing.T, repo *SQLiteStore) string {
	t.Helper()
	return scaleP02SessionFor(t, repo, scaleP02User(t, repo))
}

func scaleP02SessionFor(t *testing.T, repo *SQLiteStore, user domain.User) string {
	t.Helper()
	_, token, err := repo.createSession(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Logout(context.Background(), token) })
	return token
}

func scaleP02Seen(t *testing.T, repo *SQLiteStore, token string) string {
	t.Helper()
	var raw string
	if err := repo.db.QueryRowContext(t.Context(), `SELECT last_seen_at FROM auth_sessions WHERE token_hash=?`, tokenHash(token)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP02SetSeen(t *testing.T, repo *SQLiteStore, token, raw string) {
	t.Helper()
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE auth_sessions SET last_seen_at=? WHERE token_hash=?`, raw, tokenHash(token)); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP02SessionTouchWritesAndFailures(t *testing.T) {
	t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "")
	repo, stats := scaleP02Store(t)
	token := scaleP02Session(t, repo)
	user, err := repo.AuthenticateSession(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	for _, raw := range []string{old, "invalid", ""} {
		scaleP02SetSeen(t, repo, token, raw)
		before := stats.updates.Load()
		stats.failTouch.Store(true)
		if got, err := repo.AuthenticateSession(t.Context(), token); err != nil || !reflect.DeepEqual(got, user) {
			t.Fatalf("touch failure changed authentication: user=%s error=%v", got.ID, err)
		}
		if seen := scaleP02Seen(t, repo, token); seen != raw {
			t.Fatal("failed write changed the timestamp")
		}
		stats.failTouch.Store(false)
		if _, err := repo.AuthenticateSession(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		seen := scaleP02Seen(t, repo, token)
		parsed, err := time.Parse(time.RFC3339Nano, seen)
		if err != nil || time.Since(parsed) > time.Second {
			t.Fatalf("retry did not refresh activity: %q, %v", seen, err)
		}
		if _, err := repo.AuthenticateSession(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		if updates := stats.updates.Load() - before; updates != 2 || scaleP02Seen(t, repo, token) != seen {
			t.Fatalf("write failure/retry/fresh request attempted %d UPDATEs, want 2", updates)
		}
	}
	t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "0")
	before := stats.updates.Load()
	for range 4 {
		if _, err := repo.AuthenticateSession(t.Context(), token); err != nil {
			t.Fatal(err)
		}
	}
	if got := stats.updates.Load() - before; got != 4 {
		t.Fatalf("legacy UPDATE/request=%d/4", got)
	}
}

func TestScaleP02SessionAuthenticationStaysLive(t *testing.T) {
	for _, mode := range []string{"0", ""} {
		t.Run("interval="+mode, func(t *testing.T) {
			t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", mode)
			repo, stats := scaleP02Store(t)
			ctx := t.Context()
			user := scaleP02User(t, repo)
			for _, action := range []string{"password-change", "logout", "revoke", "revoke-others", "reset-password", "expired", "malformed-expiry", "duration", "inactive", "profile-change", "missing", "session-read-error", "user-read-error"} {
				t.Run(action, func(t *testing.T) {
					token := scaleP02SessionFor(t, repo, user)
					if _, err := repo.AuthenticateSession(ctx, token); err != nil {
						t.Fatal(err)
					}
					want := ErrAuthInvalid
					switch action {
					case "password-change":
						if err := repo.ChangePassword(ctx, user.ID, "test-password", "updated-password"); err != nil {
							t.Fatal(err)
						}
						if _, err := repo.AuthenticateSession(ctx, token); err != nil {
							t.Fatal("password change invalidated an existing session")
						}
						return
					case "logout":
						if err := repo.Logout(ctx, token); err != nil {
							t.Fatal(err)
						}
					case "revoke":
						current := scaleP02SessionFor(t, repo, user)
						if err := repo.RevokeSession(ctx, user.ID, tokenHash(token)[:12], current); err != nil {
							t.Fatal(err)
						}
					case "revoke-others":
						current := scaleP02SessionFor(t, repo, user)
						if err := repo.RevokeOtherSessions(ctx, user.ID, current); err != nil {
							t.Fatal(err)
						}
						if _, err := repo.AuthenticateSession(ctx, current); err != nil {
							t.Fatal("current session revoked")
						}
					case "reset-password":
						reset, err := repo.RequestPasswordReset(ctx, user.Email)
						if err != nil {
							t.Fatal(err)
						}
						if err := repo.ResetPassword(ctx, reset, "recovered-password"); err != nil {
							t.Fatal(err)
						}
					case "expired", "malformed-expiry":
						expires := time.Now().Add(-time.Nanosecond).UTC().Format(time.RFC3339Nano)
						if action == "malformed-expiry" {
							expires = "invalid"
						}
						if _, err := repo.db.ExecContext(ctx, `UPDATE auth_sessions SET expires_at=? WHERE token_hash=?`, expires, tokenHash(token)); err != nil {
							t.Fatal(err)
						}
						want = ErrAuthExpired
					case "duration":
						if _, err := repo.db.ExecContext(ctx, `UPDATE auth_sessions SET created_at=? WHERE token_hash=?`, time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339Nano), tokenHash(token)); err != nil {
							t.Fatal(err)
						}
						if repo.EnforceSessionDuration(ctx, token, 1) {
							t.Fatal("expired workspace duration accepted")
						}
					case "inactive":
						if _, err := repo.db.ExecContext(ctx, `UPDATE auth_users SET active=0 WHERE id=?`, user.ID); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() {
							_, _ = repo.db.ExecContext(context.Background(), `UPDATE auth_users SET active=1 WHERE id=?`, user.ID)
						})
					case "missing":
						token = "missing-session"
					case "profile-change":
						updated, err := repo.UpdateProfile(ctx, user.ID, "Changed name", "changed-name", "")
						if err != nil {
							t.Fatal(err)
						}
						got, err := repo.AuthenticateSession(ctx, token)
						if err != nil || !reflect.DeepEqual(got, updated) {
							t.Fatal("authentication reused a stale user")
						}
						return
					case "session-read-error":
						stats.failSessionRead.Store(true)
						t.Cleanup(func() { stats.failSessionRead.Store(false) })
					case "user-read-error":
						stats.failUserRead.Store(true)
						t.Cleanup(func() { stats.failUserRead.Store(false) })
					}
					before := stats.updates.Load()
					if got, err := repo.AuthenticateSession(ctx, token); !errors.Is(err, want) || got.ID != "" {
						t.Fatalf("authentication user=%s error=%v, want %v", got.ID, err, want)
					}
					if stats.updates.Load() != before {
						t.Fatal("rejected authentication touched the session")
					}
				})
			}
		})
	}
}

func TestScaleP02SessionTouchReplicaCAS(t *testing.T) {
	t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "")
	config := scaleP02Config(t)
	first, firstSQL := scaleP02StoreAt(t, config)
	second, secondSQL := scaleP02StoreAt(t, config)
	token := scaleP02Session(t, first)
	scaleP02SetSeen(t, first, token, time.Now().Add(-2*time.Minute).UTC().Format(time.RFC3339Nano))
	ready, release := make(chan struct{}, 2), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	for _, stats := range []*scaleP02SQL{firstSQL, secondSQL} {
		stats.updates.Store(0)
		stats.affected.Store(0)
		stats.beforeTouch = func() { ready <- struct{}{}; <-release }
	}
	errs := make(chan error, 2)
	for _, repo := range []*SQLiteStore{first, second} {
		go func() { _, err := repo.AuthenticateSession(t.Context(), token); errs <- err }()
	}
	for range 2 {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("replicas did not both reach the touch with the old timestamp")
		}
	}
	unblock()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if updates, affected := firstSQL.updates.Load()+secondSQL.updates.Load(), firstSQL.affected.Load()+secondSQL.affected.Load(); updates != 2 || affected != 1 {
		t.Fatalf("competing replicas: UPDATEs=%d affected=%d, want 2 and 1", updates, affected)
	}
}

func TestScaleP02SessionTouchLegacyReplicaAndDeletion(t *testing.T) {
	t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "")
	repo, stats := scaleP02Store(t)
	token := scaleP02Session(t, repo)
	old := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	scaleP02SetSeen(t, repo, token, old)
	t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "0")
	if _, err := repo.AuthenticateSession(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	fresh := scaleP02Seen(t, repo, token)
	t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "")
	before := stats.affected.Load()
	repo.touchSession(t.Context(), tokenHash(token), old, time.Now().UTC())
	if stats.affected.Load() != before || scaleP02Seen(t, repo, token) != fresh {
		t.Fatal("CAS overwrote a legacy replica's newer touch")
	}
	if err := repo.Logout(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	repo.touchSession(t.Context(), tokenHash(token), old, time.Now().UTC())
	if stats.affected.Load() != before {
		t.Fatal("touch resurrected a deleted session")
	}
	if _, err := repo.AuthenticateSession(t.Context(), token); !errors.Is(err, ErrAuthInvalid) {
		t.Fatalf("deleted session: %v", err)
	}
}

func TestScaleP02SessionTouchSustainedWriteRate(t *testing.T) {
	t.Setenv("FLOW_SESSION_TOUCH_INTERVAL", "")
	repo, stats := scaleP02Store(t)
	ctx := t.Context()
	user := scaleP02User(t, repo)
	base := time.Now().UTC()
	const sessions, seconds = 2000, 120
	hashes := make([]string, sessions)
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := range hashes {
		hashes[i] = tokenHash(fmt.Sprintf("p02-load-%d-%d", base.UnixNano(), i))
		seen := base.Add(-time.Duration(i%60) * time.Second).Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, hashes[i], user.ID, base.Add(time.Hour).Format(time.RFC3339Nano), base.Format(time.RFC3339Nano), seen); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, hash := range hashes {
			_, _ = repo.db.ExecContext(context.Background(), `DELETE FROM auth_sessions WHERE token_hash=?`, hash)
		}
	})
	stats.updates.Store(0)
	stats.affected.Store(0)
	var maxWrites int64
	// Advance request time deterministically; retain real SQL reads and CAS writes.
	for second := 1; second <= seconds; second++ {
		now := base.Add(time.Duration(second) * time.Second)
		before := stats.updates.Load()
		for _, hash := range hashes {
			var seen string
			if err := repo.db.QueryRowContext(ctx, `SELECT last_seen_at FROM auth_sessions WHERE token_hash=?`, hash).Scan(&seen); err != nil {
				t.Fatal(err)
			}
			repo.touchSession(ctx, hash, seen, now)
		}
		writes := stats.updates.Load() - before
		maxWrites = max(maxWrites, writes)
		if writes > 34 {
			t.Fatalf("second %d: %d touch UPDATEs, want <=34", second, writes)
		}
	}
	if got := stats.affected.Load(); got != 2*sessions {
		t.Fatalf("touches=%d, want %d", got, 2*sessions)
	}
	for _, hash := range hashes {
		var raw string
		if err := repo.db.QueryRowContext(ctx, `SELECT last_seen_at FROM auth_sessions WHERE token_hash=?`, hash).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		seen, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || base.Add(seconds*time.Second).Sub(seen) >= time.Minute {
			t.Fatalf("active session timestamp is over a minute behind: %q, %v", raw, err)
		}
	}
	t.Logf("sessions=%d simulated_seconds=%d requests=%d touch_UPDATE=%d affected=%d writes/second=%.3f peak_writes/second=%d", sessions, seconds, sessions*seconds, stats.updates.Load(), stats.affected.Load(), float64(stats.updates.Load())/seconds, maxWrites)
}
