package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"

	"github.com/go-sql-driver/mysql"
)

// captureMySQLFixture runs on MySQL with whichever capture the account gets
// (application without trigger authority), so both modes are compared.
func captureMySQLFixture(t *testing.T) scaleFixture {
	t.Helper()
	if os.Getenv("FLOW_TEST_DATABASE_DRIVER") != "mysql" {
		t.Skip("compares MySQL journal capture")
	}
	f := journalScaleDatabase(t)
	t.Logf("capture=%s", f.RecordJournalCapture())
	return f
}

// readAllChanges pages the journal from after to the head.
func readAllChanges(t *testing.T, f *SQLiteStore, workspaceID, after string) ([]CommittedChange, string) {
	t.Helper()
	var changes []CommittedChange
	for {
		page, err := f.ReadCommittedChanges(t.Context(), workspaceID, after, 50, 256<<10)
		if err != nil || page.ResyncRequired {
			t.Fatalf("read changes after %s: resync=%t %v", after, page.ResyncRequired, err)
		}
		changes = append(changes, page.Transactions...)
		after = page.Next
		if !page.HasMore {
			return changes, after
		}
	}
}

// Transactions committed concurrently are read back complete and in cursor
// order, by a reader that follows the cursor while they commit and by one
// that reads afterwards; none is skipped or repeated.
func TestRecordCaptureConcurrentWritersInCursorOrder(t *testing.T) {
	switch os.Getenv("FLOW_TEST_DATABASE_DRIVER") {
	case "mysql", "postgres", "postgresql":
	default:
		t.Skip("exercises concurrent external database writers")
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := journalScaleDatabase(t)
	t.Logf("dialect=%s capture=%s", f.dialect, f.RecordJournalCapture())
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	start := scaleHead(t, f)
	const writers, perWriter = 6, 20
	var committed sync.Map
	var deadlocks atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	began := time.Now()
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range perWriter {
				err := func() error {
					ctx := t.Context()
					tx, err := f.db.BeginTx(ctx, nil)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					own := fmt.Sprintf("capture-w%d-%d", w, n)
					shared := fmt.Sprintf("capture-shared-%d", n%3)
					for _, key := range []string{own, shared} {
						raw, _ := json.Marshal(domain.Project{ID: key, Name: fmt.Sprintf("writer %d write %d", w, n)})
						if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'labels',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, key, raw); err != nil {
							return err
						}
					}
					if n%4 == 3 {
						if _, err := tx.ExecContext(ctx, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='labels' AND record_key=?`, workspace, fmt.Sprintf("capture-w%d-%d", w, n-1)); err != nil {
							return err
						}
					}
					event := idgen.New("capture-event-")
					if _, err := insertDomainEvent(ctx, tx, domainEventRow{ID: event, Type: "project.updated", WorkspaceKey: workspace, WorkspaceID: workspaceID, AggregateID: own, Payload: []byte(`{}`), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
						return err
					}
					if err := tx.Commit(); err != nil {
						return err
					}
					committed.Store(event, own)
					return nil
				}()
				if databaseDeadlock(err) {
					deadlocks.Add(1)
					continue // the database rolled it back: it never committed
				}
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	writersDone := errsClosed(&wg)
	done := make(chan struct{})
	followed := map[string]bool{}
	var followErr error
	go func() {
		defer close(done)
		cursor, last := start, int64(0)
		finished := false
		for {
			page, err := f.ReadCommittedChanges(t.Context(), workspaceID, cursor, 20, 256<<10)
			if err != nil || page.ResyncRequired {
				followErr = fmt.Errorf("follow after %s: resync=%t %w", cursor, page.ResyncRequired, err)
				return
			}
			for _, change := range page.Transactions {
				seq, _ := strconv.ParseInt(change.Seq, 10, 64)
				if seq <= last || followed[change.TxID] {
					followErr = fmt.Errorf("cursor order: %d after %d (%s)", seq, last, change.TxID)
					return
				}
				last = seq
				followed[change.TxID] = true
			}
			cursor = page.Next
			if finished && !page.HasMore {
				return
			}
			select {
			case <-writersDone:
				finished = true
			case <-time.After(5 * time.Millisecond):
			}
		}
	}()
	wg.Wait()
	writeDuration := time.Since(began)
	close(errs)
	<-done
	for err := range errs {
		t.Fatal(err)
	}
	if followErr != nil {
		t.Fatal(followErr)
	}
	all, _ := readAllChanges(t, f.SQLiteStore, workspaceID, start)
	events := 0
	committed.Range(func(key, value any) bool {
		events++
		seq, err := f.RecordJournalEventSeq(t.Context(), key.(string))
		if err != nil || seq == "" {
			t.Errorf("committed event %s has no journal cursor: %v", key, err)
		}
		return true
	})
	if events == 0 || len(all) != events || len(followed) != events {
		t.Fatalf("committed %d transactions, read %d afterwards and %d while following", events, len(all), len(followed))
	}
	versions := map[RecordKey]int64{}
	last := int64(0)
	for _, change := range all {
		seq, _ := strconv.ParseInt(change.Seq, 10, 64)
		if !followed[change.TxID] || seq <= last {
			t.Fatalf("transaction %s out of order or missed while following", change.TxID)
		}
		last = seq
		for _, record := range change.Records {
			version, _ := strconv.ParseInt(record.Version, 10, 64)
			if version <= versions[record.Key] {
				t.Fatalf("version of %v went from %d to %d", record.Key, versions[record.Key], version)
			}
			versions[record.Key] = version
		}
	}
	for key, version := range versions {
		current, _, err := f.RecordVersion(t.Context(), workspace, key)
		if err != nil || current != strconv.FormatInt(version, 10) {
			t.Fatalf("%v: committed version %s, journal %d: %v", key, current, version, err)
		}
	}
	t.Logf("concurrent transactions=%d deadlocks=%d records=%d writer_elapsed=%s commits_per_second=%.1f",
		events, deadlocks.Load(), len(versions), writeDuration, float64(events)/writeDuration.Seconds())
	if deadlocks.Load() != 0 || events != writers*perWriter {
		t.Fatalf("committed %d of %d attempts with %d deadlocks", events, writers*perWriter, deadlocks.Load())
	}
}

// errsClosed reports when every writer returned.
func errsClosed(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() { wg.Wait(); close(ch) }()
	return ch
}

// A rolled-back transaction leaves no journal input: no version, dirty key,
// epoch, event marker or journal entry.
func TestRecordCaptureRollbackLeavesNoJournal(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := captureMySQLFixture(t)
	workspace, workspaceID, user := f.data.Workspace.URLKey, f.data.Workspace.ID, f.data.Viewer.ID
	head := scaleHead(t, f)
	epoch, err := f.AuthzEpoch(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	journalScalePut(t, tx, workspace, "projects", "capture-rollback", domain.Project{ID: "capture-rollback", Name: "Must vanish"})
	if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_memberships SET role='admin' WHERE workspace_id=? AND user_id=?`, workspaceID, user); err != nil {
		t.Fatal(err)
	}
	event := scaleEvent(t, f, tx, []byte(`{}`))
	token := tx.journalToken
	var dirty int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM record_journal_dirty WHERE tx_id=?`, token).Scan(&dirty); err != nil || dirty < 2 {
		t.Fatalf("capture wrote %d keys inside the transaction: %v", dirty, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var versions, keys, pending, journal int
	if err := f.db.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM record_versions WHERE workspace_key=? AND record_key='capture-rollback'),
		(SELECT COUNT(*) FROM record_journal_dirty WHERE tx_id=?),
		(SELECT COUNT(*) FROM record_journal_event_pending WHERE event_id=?),
		(SELECT COUNT(*) FROM record_journal WHERE workspace_id=?)`, workspace, token, event, workspaceID).Scan(&versions, &keys, &pending, &journal); err != nil {
		t.Fatal(err)
	}
	if versions != 0 || keys != 0 || pending != 0 {
		t.Fatalf("rolled-back capture survived: versions=%d keys=%d pending=%d", versions, keys, pending)
	}
	if got, err := f.AuthzEpoch(t.Context(), workspaceID); err != nil || got != epoch || scaleHead(t, f) != head {
		t.Fatalf("epoch or head moved: %s -> %s %v", epoch, got, err)
	}
	changes, _ := readAllChanges(t, f.SQLiteStore, workspaceID, head)
	if len(changes) != 0 {
		t.Fatalf("rolled-back transaction is visible: %+v", changes)
	}
}

// captureAdmin opens FLOW_TEST_MYSQL_ADMIN_URL (an account that can create
// schemas and users on a disposable MySQL 8.4 with binary logging on and
// trigger creator trust off).
func captureAdmin(t *testing.T) (*sql.DB, *url.URL) {
	t.Helper()
	raw := os.Getenv("FLOW_TEST_MYSQL_ADMIN_URL")
	if raw == "" {
		t.Skip("set FLOW_TEST_MYSQL_ADMIN_URL for a disposable MySQL 8.4 service")
	}
	adminURL, err := url.Parse(raw)
	if err != nil || adminURL.Scheme != "mysql" || adminURL.User == nil {
		t.Fatal("invalid MySQL admin URL")
	}
	config := mysql.NewConfig()
	config.User = adminURL.User.Username()
	config.Passwd, _ = adminURL.User.Password()
	config.Net, config.Addr = "tcp", adminURL.Host
	admin, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	var binlog, trust int
	if err := admin.QueryRowContext(t.Context(), `SELECT @@GLOBAL.log_bin,@@GLOBAL.log_bin_trust_function_creators`).Scan(&binlog, &trust); err != nil || binlog != 1 || trust != 0 {
		t.Skip("requires binary logging on and log_bin_trust_function_creators=0")
	}
	return admin, adminURL
}

type captureSchema struct {
	admin                  *sql.DB
	name, user             string
	privileged, restricted DatabaseConfig
}

// newCaptureSchema creates a scratch schema, and an account on it without
// TRIGGER (and without SUPER, so binary logging also refuses triggers).
func newCaptureSchema(t *testing.T, trigger bool) captureSchema {
	t.Helper()
	admin, adminURL := captureAdmin(t)
	name := fmt.Sprintf("flowcapture%x", time.Now().UnixNano())
	password := idgen.New("pw")
	exec := func(statement string) {
		t.Helper()
		if _, err := admin.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	exec("CREATE DATABASE " + name)
	account := "'" + name + "'@'%'"
	exec("CREATE USER " + account + " IDENTIFIED BY '" + password + "'")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(ctx, "DROP DATABASE "+name)
		_, _ = admin.ExecContext(ctx, "DROP USER "+account)
	})
	grants := "SELECT, INSERT, UPDATE, DELETE, CREATE, DROP, ALTER, INDEX, REFERENCES, CREATE TEMPORARY TABLES"
	if trigger {
		grants += ", TRIGGER"
	}
	exec("GRANT " + grants + " ON " + name + ".* TO " + account)
	privileged := *adminURL
	privileged.Path = "/" + name
	restricted := privileged
	restricted.User = url.UserPassword(name, password)
	return captureSchema{
		admin: admin, name: name, user: name,
		privileged: DatabaseConfig{Driver: "mysql", URL: privileged.String(), MaxOpenConns: 4},
		restricted: DatabaseConfig{Driver: "mysql", URL: restricted.String(), MaxOpenConns: 4},
	}
}

func (s captureSchema) triggers(t *testing.T) int {
	t.Helper()
	var count int
	if err := s.admin.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=? AND TRIGGER_NAME LIKE 'flow\_rj\_%'`, s.name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// triggerDDL counts the CREATE/DROP TRIGGER statements issued in the scratch
// schema, refused or not, from the server's statement digests.
func (s captureSchema) triggerDDL(t *testing.T) int64 {
	t.Helper()
	var count sql.NullInt64
	if err := s.admin.QueryRowContext(t.Context(), `SELECT SUM(COUNT_STAR) FROM performance_schema.events_statements_summary_by_digest WHERE SCHEMA_NAME=? AND (DIGEST_TEXT LIKE 'CREATE TRIGGER%' OR DIGEST_TEXT LIKE 'DROP TRIGGER%')`, s.name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count.Int64
}

func openCaptureStore(t *testing.T, config DatabaseConfig, capture string) *SQLiteStore {
	t.Helper()
	config.RecordJournalCapture = capture
	repo, err := OpenDatabase(config)
	if err != nil {
		t.Fatalf("open (%s capture): %v", capture, err)
	}
	return repo
}

type captureWorkspace struct{ key, id string }

func createCaptureWorkspace(t *testing.T, repo *SQLiteStore) captureWorkspace {
	t.Helper()
	user, token, err := repo.Register(t.Context(), "Capture owner", idgen.New("capture-")+"@example.test", "capture-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	data, err := repo.CreateWorkspace(ContextWithActor(t.Context(), user), "Capture", idgen.New("capture-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	return captureWorkspace{data.Workspace.URLKey, data.Workspace.ID}
}

func putCaptureRecord(t *testing.T, repo *SQLiteStore, workspace, id, name string) {
	t.Helper()
	raw, _ := json.Marshal(domain.Project{ID: id, Name: name})
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, workspace, id, raw); err != nil {
		t.Fatal(err)
	}
}

func repairCaptureWorkspace(t *testing.T, repo *SQLiteStore, workspaceID string) {
	t.Helper()
	for range 64 {
		remaining, err := repo.RepairRecordJournal(t.Context(), workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if !remaining {
			return
		}
	}
	t.Fatal("journal repair did not finish")
}

func recordsOf(changes []CommittedChange) []string {
	var keys []string
	for _, change := range changes {
		for _, record := range change.Records {
			keys = append(keys, record.Key.ID)
		}
	}
	return keys
}

// A fresh install and a restart on an account without trigger authority
// capture in the application, without attempting any trigger DDL; an
// account with authority configured for application capture installs none.
func TestRecordCaptureRestrictedStartup(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, trigger := range []bool{false, true} {
		t.Run(fmt.Sprintf("trigger-grant=%t", trigger), func(t *testing.T) {
			schema := newCaptureSchema(t, trigger)
			for start := range 2 {
				repo := openCaptureStore(t, schema.restricted, "")
				if !repo.RecordJournalReady() || repo.RecordJournalCapture() != RecordJournalCaptureApplication {
					t.Fatalf("start %d: ready=%t capture=%q", start, repo.RecordJournalReady(), repo.RecordJournalCapture())
				}
				ws := createCaptureWorkspace(t, repo)
				repairCaptureWorkspace(t, repo, ws.id)
				head := scaleHeadOf(t, repo, ws.id)
				putCaptureRecord(t, repo, ws.key, "restricted-project", "created")
				changes, _ := readAllChanges(t, repo, ws.id, head)
				if !slices.Equal(recordsOf(changes), []string{"restricted-project"}) {
					t.Fatalf("start %d: journal %+v", start, changes)
				}
				repo.Close()
			}
			if schema.triggers(t) != 0 || schema.triggerDDL(t) != 0 {
				t.Fatalf("restricted starts installed %d triggers or issued %d trigger statements", schema.triggers(t), schema.triggerDDL(t))
			}
		})
	}
	t.Run("authority-configured-application", func(t *testing.T) {
		schema := newCaptureSchema(t, false)
		repo := openCaptureStore(t, schema.privileged, RecordJournalCaptureApplication)
		defer repo.Close()
		var journalDDL int64
		if err := schema.admin.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM performance_schema.events_statements_summary_by_digest WHERE SCHEMA_NAME=? AND DIGEST_TEXT LIKE '%TRIGGER `+"`"+`flow\_rj\_%'`, schema.name).Scan(&journalDDL); err != nil {
			t.Fatal(err)
		}
		if repo.RecordJournalCapture() != RecordJournalCaptureApplication || schema.triggers(t) != 0 || journalDDL != 0 {
			t.Fatalf("capture=%q journal triggers=%d statements=%d", repo.RecordJournalCapture(), schema.triggers(t), journalDDL)
		}
	})
}

func scaleHeadOf(t *testing.T, repo *SQLiteStore, workspaceID string) string {
	t.Helper()
	head, err := repo.RecordJournalHead(t.Context(), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// An installation that captures with triggers moves to application capture
// without losing a transaction in flight, drops its triggers only through
// the explicit command, and can return to triggers.
func TestRecordCaptureTriggerMigrationAndRollback(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	schema := newCaptureSchema(t, false)
	ctx := t.Context()

	// 1. A trigger installation (privileged account, the previous default).
	triggerStore := openCaptureStore(t, schema.privileged, RecordJournalCaptureTriggers)
	defer func() { triggerStore.Close() }()
	if triggerStore.RecordJournalCapture() != RecordJournalCaptureTriggers || schema.triggers(t) == 0 {
		t.Fatalf("trigger installation: capture=%q triggers=%d", triggerStore.RecordJournalCapture(), schema.triggers(t))
	}
	ws := createCaptureWorkspace(t, triggerStore)
	repairCaptureWorkspace(t, triggerStore, ws.id)
	head := scaleHeadOf(t, triggerStore, ws.id)

	// 2. A write in flight on a trigger replica while the first application
	// replica starts (restricted account: application capture, triggers
	// recorded, so it keeps identifying itself to them).
	inFlight, err := triggerStore.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	journalScalePut(t, inFlight, ws.key, "projects", "in-flight", domain.Project{ID: "in-flight", Name: "trigger replica"})
	type opened struct {
		repo *SQLiteStore
		err  error
	}
	starting := make(chan opened, 1)
	go func() {
		config := schema.restricted
		repo, err := OpenDatabase(config)
		starting <- opened{repo, err}
	}()
	// The switch-over runs under the startup lock and waits for rows the
	// in-flight transaction holds; that transaction commits meanwhile.
	time.Sleep(300 * time.Millisecond)
	if err := inFlight.Commit(); err != nil {
		t.Fatal(err)
	}
	start := <-starting
	if start.err != nil {
		t.Fatal(start.err)
	}
	appStore := start.repo
	defer func() { appStore.Close() }()
	if appStore.RecordJournalCapture() != RecordJournalCaptureApplication || !appStore.db.captureTriggers.Load() || schema.triggers(t) == 0 {
		t.Fatalf("switch: capture=%q tracked triggers=%t triggers=%d", appStore.RecordJournalCapture(), appStore.db.captureTriggers.Load(), schema.triggers(t))
	}
	putCaptureRecord(t, appStore, ws.key, "dual", "application replica beside triggers")
	changes, head2 := readAllChanges(t, appStore, ws.id, head)
	if !slices.Equal(recordsOf(changes), []string{"in-flight", "dual"}) {
		t.Fatalf("switch-over journal %+v", changes)
	}

	// 3. Every replica captures in the application: drop the triggers
	// (an account with TRIGGER), idempotently.
	dropper := openCaptureStore(t, schema.privileged, RecordJournalCaptureApplication)
	for range 2 {
		if err := dropper.DropRecordJournalTriggers(ctx); err != nil {
			t.Fatal(err)
		}
	}
	dropper.Close()
	if schema.triggers(t) != 0 {
		t.Fatalf("%d triggers remain", schema.triggers(t))
	}
	triggerStore.Close()
	appStore.Close()
	appStore = openCaptureStore(t, schema.restricted, "")
	status, err := appStore.RecordJournalCaptureStatus(ctx)
	if err != nil || appStore.db.captureTriggers.Load() || status.Persisted != RecordJournalCaptureApplication || status.TriggersRecorded || status.UncoveredWorkspaces != 0 {
		t.Fatalf("after drop: %+v %v", status, err)
	}
	putCaptureRecord(t, appStore, ws.key, "dual", "application only")
	changes, head3 := readAllChanges(t, appStore, ws.id, head2)
	if !slices.Equal(recordsOf(changes), []string{"dual"}) {
		t.Fatalf("application-only journal %+v", changes)
	}

	// 4. Roll back to triggers (an account that may install them). A
	// replica still capturing without the session token is noticed as an
	// untracked writer and repaired, never lost.
	triggerStore = openCaptureStore(t, schema.privileged, RecordJournalCaptureTriggers)
	if triggerStore.RecordJournalCapture() != RecordJournalCaptureTriggers || schema.triggers(t) == 0 {
		t.Fatalf("rollback: capture=%q triggers=%d", triggerStore.RecordJournalCapture(), schema.triggers(t))
	}
	putCaptureRecord(t, triggerStore, ws.key, "rolled-back", "trigger replica")
	putCaptureRecord(t, appStore, ws.key, "stale-replica", "application replica, triggers back")
	page, err := triggerStore.ReadCommittedChanges(ctx, ws.id, head3, 50, 256<<10)
	if !errors.Is(err, ErrRecordJournalCoverage) || !page.ResyncRequired {
		t.Fatalf("untracked write during rollback not detected: %+v %v", page, err)
	}
	repairCaptureWorkspace(t, triggerStore, ws.id)
	changes, _ = readAllChanges(t, triggerStore, ws.id, head3)
	if len(changes) == 0 || !slices.Contains(changes[len(changes)-1].Invalidations, "workspace") {
		t.Fatalf("rollback repair did not resync the workspace: %+v", changes)
	}
	for _, key := range []string{"rolled-back", "stale-replica"} {
		if version, deleted, err := triggerStore.RecordVersion(ctx, ws.key, RecordKey{"projects", key}); err != nil || version == "" || deleted {
			t.Fatalf("%s lost: %q %t %v", key, version, deleted, err)
		}
	}
	appStore.Close()
	triggerStore.Close()
	// A restricted start after the rollback captures in the application
	// beside the reinstalled triggers.
	appStore = openCaptureStore(t, schema.restricted, "")
	if appStore.RecordJournalCapture() != RecordJournalCaptureApplication || !appStore.db.captureTriggers.Load() {
		t.Fatalf("restart after rollback: capture=%q", appStore.RecordJournalCapture())
	}
}
