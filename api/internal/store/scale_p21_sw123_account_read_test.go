package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW123Script struct {
	rows       [][2]driver.Value
	queryError error
	endError   error
	closeError error
	orphans    int
	query      func()
	next       func()
	blocked    atomic.Int64
}

type scaleP21SW123Connector struct{ script *scaleP21SW123Script }

func (c scaleP21SW123Connector) Driver() driver.Driver { return c }
func (c scaleP21SW123Connector) Open(string) (driver.Conn, error) {
	return c.Connect(context.Background())
}
func (c scaleP21SW123Connector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &scaleP21SW123Connection{script: c.script}, nil
}

type scaleP21SW123Connection struct{ script *scaleP21SW123Script }

func (*scaleP21SW123Connection) Close() error { return nil }
func (*scaleP21SW123Connection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected account prepare")
}
func (*scaleP21SW123Connection) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected account transaction")
}
func (c *scaleP21SW123Connection) QueryContext(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "FROM issue_collection_counts") {
		return nil, errors.New("unexpected account query")
	}
	if c.script.query != nil {
		c.script.query()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.script.queryError != nil {
		return nil, c.script.queryError
	}
	return &scaleP21SW123Rows{script: c.script, guarded: strings.Contains(query, "CASE WHEN")}, nil
}

type scaleP21SW123Rows struct {
	script   *scaleP21SW123Script
	at       int
	guarded  bool
	finished bool
}

func (r *scaleP21SW123Rows) Columns() []string {
	if r.guarded {
		return []string{"bytes", "workspace_key", "total"}
	}
	return []string{"workspace_key", "total"}
}
func (r *scaleP21SW123Rows) Close() error { return r.script.closeError }
func scaleP21SW123Bytes(value driver.Value) int64 {
	switch value := value.(type) {
	case nil:
		return 0
	case []byte:
		return int64(len(value))
	default:
		return int64(len(fmt.Sprint(value)))
	}
}
func (r *scaleP21SW123Rows) Next(target []driver.Value) error {
	if r.script.next != nil {
		r.script.next()
	}
	if r.at >= len(r.script.rows)+r.script.orphans {
		if !r.finished {
			r.finished = true
			if r.script.endError != nil {
				return r.script.endError
			}
		}
		return io.EOF
	}
	var row [2]driver.Value
	if r.at < len(r.script.rows) {
		row = r.script.rows[r.at]
	} else {
		row = [2]driver.Value{strings.Repeat("orphan-", 40) + strconv.Itoa(r.at), int64(r.at)}
	}
	r.at++
	if r.guarded {
		size := scaleP21SW123Bytes(row[0]) + scaleP21SW123Bytes(row[1])
		target[0] = size
		if size > MetadataRecordPageMaxBytes {
			r.script.blocked.Add(1)
			row = [2]driver.Value{}
		}
		copy(target[1:], row[:])
	} else {
		copy(target, row[:])
	}
	return nil
}

func scaleP21SW123Store(t *testing.T, script *scaleP21SW123Script, bounded bool) *SQLiteStore {
	t.Helper()
	database := sql.OpenDB(scaleP21SW123Connector{script: script})
	t.Cleanup(func() { _ = database.Close() })
	requested := time.Date(2026, 11, 15, 1, 2, 3, 4, time.FixedZone("sw123", 3600))
	repo := &SQLiteStore{
		db: &sqlDatabase{DB: database, dialect: "sqlite"}, dialect: "sqlite",
		viewer: scaleP21SW118NativeViewer("sw123"), lastWorkspaceKey: "absent-default",
		workspaces: map[string]domain.Bootstrap{
			"physical-other": {Workspace: domain.Workspace{ID: "first", URLKey: "logical", Name: "zETA", Icon: "\x00icon", LogoURL: "logo", Color: "green", Region: "eu", CreatedAt: requested, DeletionRequestedAt: &requested}},
			"":               {Workspace: domain.Workspace{ID: "second", URLKey: "", Name: "Alpha", CreatedAt: requested}},
			"missing":        {Workspace: domain.Workspace{ID: "third", URLKey: "no-count", Name: "Beta", CreatedAt: requested}},
		},
	}
	if bounded {
		repo.metadataResidency = newMetadataResidency()
	}
	return repo
}

func scaleP21SW123Equal(t *testing.T, got, want domain.AccountBootstrap) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("account differs from literal: got=%#v want=%#v", got, want)
	}
	scaleP21SW118AssertViewer(t, got.Viewer, want.Viewer)
	for at := range got.Workspaces {
		if got.Workspaces[at].Workspace.DeletionRequestedAt != want.Workspaces[at].Workspace.DeletionRequestedAt {
			t.Fatal("account changed native workspace pointer ownership")
		}
	}
}

func TestScaleP21SW123AccountLiteralCountErrorsAndNativeValues(t *testing.T) {
	failure := errors.New("ordinary account SQL failure")
	for _, fixture := range []struct {
		name   string
		script *scaleP21SW123Script
	}{
		{"empty", &scaleP21SW123Script{}},
		{"native-duplicates", &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(7)}, {"", int64(-2)}, {[]byte("logical"), []byte("13")}, {"logical\x00", int64(99)}, {"Logical", int64(66)}, {"uninstalled", int64(999)}}}},
		{"query-error", &scaleP21SW123Script{queryError: failure}},
		{"key-scan-error-clears-prefix", &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(7)}, {nil, int64(4)}}}},
		{"count-scan-error-clears-prefix", &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(7)}, {"unused", "invalid-count"}}}},
		{"count-null-clears-prefix", &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(7)}, {"unused", nil}}}},
		{"count-overflow-clears-prefix", &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(7)}, {"unused", "18446744073709551616"}}}},
		{"terminal-error-keeps-prefix", &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(17)}}, endError: failure}},
		{"close-error-keeps-prefix", &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(19)}}, closeError: failure}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			for _, bounded := range []bool{false, true} {
				repo := scaleP21SW123Store(t, fixture.script, bounded)
				want := repo.Account()
				disk := recordSpoolBytes.Load()
				read, err := repo.BeginAccountRead(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				scaleP21SW123Equal(t, read.Account, want)
				if bounded && repo.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("account has no output owner")
				}
				read.Close()
				read.Close()
				if !reflect.DeepEqual(read.Account, domain.AccountBootstrap{}) || repo.MetadataWorkUsedForTesting() != 0 ||
					repo.WorkspaceMetadataDecodes() != 0 || recordSpoolBytes.Load() != disk {
					t.Fatal("account read leaked output, work, disk, or full hydration")
				}
			}
		})
	}
}

func TestScaleP21SW123AccountInstalledReloadAndEmptyCatalog(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, reload := range []bool{false, true} {
		if reload {
			if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		read, err := view.BeginAccountRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		scaleP21SW123Equal(t, read.Account, view.Account())
		read.Close()
	}
	view.mu.Lock()
	view.workspaces = map[string]domain.Bootstrap{}
	view.lastWorkspaceKey = "removed-default"
	view.mu.Unlock()
	read, err := view.BeginAccountRead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	scaleP21SW123Equal(t, read.Account, view.Account())
	if read.Account.Workspaces == nil {
		t.Fatal("empty account changed its nonnil workspace slice")
	}
}

func TestScaleP21SW123AccountOrphanAndBodyGrowth(t *testing.T) {
	script := &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(27)}}}
	repo := scaleP21SW123Store(t, script, true)
	disk := recordSpoolBytes.Load()
	owned := 0
	for _, count := range []int{0, 64, 4096} {
		script.orphans = count
		repo.mu.Lock()
		data := repo.workspaces["physical-other"]
		data.Projects = []domain.Project{{ID: "excluded", Description: strings.Repeat("unrelated body ", max(1, count))}}
		repo.workspaces["physical-other"] = data
		repo.mu.Unlock()
		want := repo.Account()
		peakDisk := disk
		script.next = func() { peakDisk = max(peakDisk, recordSpoolBytes.Load()) }
		read, err := repo.BeginAccountRead(t.Context())
		script.next = nil
		if err != nil {
			t.Fatal(err)
		}
		scaleP21SW123Equal(t, read.Account, want)
		used := repo.MetadataWorkUsedForTesting()
		if owned == 0 {
			owned = used
		}
		if used != owned || recordSpoolBytes.Load() != disk {
			t.Fatal("orphan/body growth changed retained account work or leaked stage disk", used, owned)
		}
		read.Close()
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("account output work was not released")
		}
		t.Logf("orphan-count=%d owned-work=%d peak-stage-disk=%d", count, used, peakDisk-disk)
	}
}

func TestScaleP21SW123AccountGuardedLargeKeysAndRows(t *testing.T) {
	key := strings.Repeat("native\x00", 16000)
	for _, total := range []driver.Value{int64(31), "oversized"} {
		script := &scaleP21SW123Script{rows: [][2]driver.Value{{key, total}}}
		repo := scaleP21SW123Store(t, script, true)
		repo.workspaces = map[string]domain.Bootstrap{"physical": {Workspace: domain.Workspace{URLKey: key, Name: "Large key", CreatedAt: time.Now()}}}
		if total == "oversized" {
			script.rows[0][1] = strings.Repeat("9", MetadataRecordPageMaxBytes+1)
			read, err := repo.BeginAccountRead(t.Context())
			if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || script.blocked.Load() != 1 {
				t.Fatal("oversized SQL total was transferred or swallowed", read, err, script.blocked.Load())
			}
			continue
		}
		want := repo.Account()
		read, err := repo.BeginAccountRead(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		scaleP21SW123Equal(t, read.Account, want)
		read.Close()
		script.rows[0][0] = strings.Repeat("x", MetadataRecordPageMaxBytes+1)
		read, err = repo.BeginAccountRead(t.Context())
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || script.blocked.Load() != 1 {
			t.Fatal("oversized SQL key was transferred or swallowed", read, err, script.blocked.Load())
		}
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("oversized SQL row leaked work")
		}
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	if f.dialect == "sqlite" {
		key := "sw123-oversized-" + f.data.Workspace.URLKey
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO issue_collection_counts(workspace_key,total) VALUES(CAST(? || zeroblob(?) AS BLOB),7)`, key, MetadataRecordPageMaxBytes+1); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = f.db.ExecContext(context.Background(), `DELETE FROM issue_collection_counts WHERE workspace_key=CAST(? || zeroblob(?) AS BLOB)`, key, MetadataRecordPageMaxBytes+1)
		})
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginAccountRead(t.Context())
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("SQLite oversized orphan key bypassed guarded transfer", read, err)
		}
	}
}

func TestScaleP21SW123AccountJoinedDefaultsAndSort(t *testing.T) {
	repo := scaleP21SW123Store(t, &scaleP21SW123Script{}, true)
	repo.workspaces = map[string]domain.Bootstrap{
		"first":  {Workspace: domain.Workspace{Name: "Zulu"}},
		"second": {Workspace: domain.Workspace{Name: "alpha"}},
		"third":  {Workspace: domain.Workspace{Name: "Beta"}},
	}
	before := time.Now().UTC()
	read, err := repo.BeginAccountRead(t.Context())
	after := time.Now().UTC()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	for at, name := range []string{"alpha", "Beta", "Zulu"} {
		membership := read.Account.Workspaces[at]
		if membership.Workspace.Name != name || membership.Role != "Admin" ||
			membership.JoinedAt.Before(before) || membership.JoinedAt.After(after) ||
			membership.JoinedAt.Location() != time.UTC || !membership.Workspace.CreatedAt.IsZero() {
			t.Fatal("account changed literal joined default or case-insensitive ordering", membership)
		}
	}
}

func TestScaleP21SW123AccountCancellationAdmissionAndStageFailure(t *testing.T) {
	script := &scaleP21SW123Script{rows: [][2]driver.Value{{"logical", int64(7)}}}
	repo := scaleP21SW123Store(t, script, true)
	disk := recordSpoolBytes.Load()
	for _, point := range []string{"before", "query", "rows"} {
		ctx, cancel := context.WithCancel(t.Context())
		switch point {
		case "before":
			cancel()
		case "query":
			script.query = cancel
		case "rows":
			script.next = cancel
		}
		read, err := repo.BeginAccountRead(ctx)
		cancel()
		script.query, script.next = nil, nil
		if read != nil || !errors.Is(err, context.Canceled) || repo.MetadataWorkUsedForTesting() != 0 || recordSpoolBytes.Load() != disk {
			t.Fatal("account swallowed cancellation or leaked resources", point, read, err)
		}
	}
	limit := repo.metadataResidency.work.limit
	repo.metadataResidency.work.limit = 1
	read, err := repo.BeginAccountRead(t.Context())
	repo.metadataResidency.work.limit = limit
	if read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("account bypassed scratch admission", read, err)
	}
	ctx := context.WithValue(t.Context(), metadataLogicalScratchKey{}, metadataLogicalScratch{directory: t.TempDir() + "/absent"})
	if read, err := repo.BeginAccountRead(ctx); read != nil || err == nil {
		t.Fatal("new stage failure was ignored as an old SQL failure", read, err)
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.FormatInt(disk, 10))
	if read, err := repo.BeginAccountRead(t.Context()); read != nil || !errors.Is(err, ErrRecordSpoolBudget) {
		t.Fatal("account bypassed disk-stage allowance", read, err)
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.FormatInt(defaultRecordSpoolMaxBytes, 10))
	repo.mu.Lock()
	data := repo.workspaces["physical-other"]
	data.Workspace.LogoURL = strings.Repeat("large retained header ", 220000)
	repo.workspaces["physical-other"] = data
	repo.mu.Unlock()
	if read, err := repo.BeginAccountRead(t.Context()); read != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("account bypassed result/encoding admission", read, err)
	}
	if repo.MetadataWorkUsedForTesting() != 0 || recordSpoolBytes.Load() != disk {
		t.Fatal("account failure leaked admission or disk")
	}
}

func TestScaleP21SW123AccountOutputTransferAndBorrowedValues(t *testing.T) {
	repo := scaleP21SW123Store(t, &scaleP21SW123Script{}, true)
	read, err := repo.BeginAccountRead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var closes []func()
	ctx = WithMetadataOutputOwner(ctx, func(close func()) bool { closes = append(closes, close); return true })
	if err := read.TransferOutput(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if repo.MetadataWorkUsedForTesting() == 0 || read.Account.Viewer.ID == "" {
		t.Fatal("cancellation released transferred account values")
	}
	read.Account.Viewer.Name = "caller scalar"
	read.Account.Viewer.AppScopes[0] = "shared-scope"
	read.Account.Viewer.Via.Name = "shared-bot"
	if repo.DefaultViewer().Name == "caller scalar" || repo.DefaultViewer().AppScopes[0] != "shared-scope" || repo.DefaultViewer().Via.Name != "shared-bot" {
		t.Fatal("owned reader changed literal shallow viewer semantics")
	}
	closes[0]()
	closes[0]()
	if repo.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Account, domain.AccountBootstrap{}) {
		t.Fatal("transferred account output survived owner close")
	}
	read, err = repo.BeginAccountRead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := read.TransferOutput(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) || repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unowned transfer did not reject and close", err)
	}
}

func TestScaleP21SW123AccountCountsPrecedeInstalledLock(t *testing.T) {
	script := &scaleP21SW123Script{rows: [][2]driver.Value{{"replacement", int64(41)}}}
	repo := scaleP21SW123Store(t, script, true)
	next := domain.Bootstrap{Workspace: domain.Workspace{Name: "Replacement", URLKey: "replacement", CreatedAt: time.Now()}}
	script.query = func() {
		repo.mu.Lock()
		repo.viewer = scaleP21SW118NativeViewer("replacement")
		repo.workspaces = map[string]domain.Bootstrap{"physical": next}
		repo.lastWorkspaceKey = "replacement"
		repo.mu.Unlock()
	}
	read, err := repo.BeginAccountRead(t.Context())
	script.query = nil
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	scaleP21SW123Equal(t, read.Account, repo.Account())
	if read.Account.Workspaces[0].IssueCount != 41 {
		t.Fatal("account did not join staged counts to the later installed catalog")
	}
	// A closed caller snapshot cannot replace Account's independent SQL read.
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: repo.db, reader: tx})
	again, err := repo.BeginAccountRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	scaleP21SW123Equal(t, again.Account, repo.Account())
}

func TestScaleP21SW123AccountConcurrentInstalledReplacement(t *testing.T) {
	repo := scaleP21SW123Store(t, &scaleP21SW123Script{}, true)
	first := repo.Account()
	repo.viewer = scaleP21SW118NativeViewer("second")
	repo.lastWorkspaceKey = "second"
	repo.workspaces = map[string]domain.Bootstrap{"second": {Workspace: domain.Workspace{Name: "Second", CreatedAt: time.Now()}}}
	second := repo.Account()
	secondCatalog := repo.workspaces
	repo.viewer = first.Viewer
	repo.lastWorkspaceKey = first.LastWorkspaceKey
	firstCatalog := map[string]domain.Bootstrap{}
	for at, membership := range first.Workspaces {
		firstCatalog[strconv.Itoa(at)] = domain.Bootstrap{Workspace: membership.Workspace}
	}
	repo.workspaces = firstCatalog
	start := make(chan struct{})
	failures := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(3)
	go func() {
		defer workers.Done()
		<-start
		for at := range 1000 {
			repo.mu.Lock()
			if at%2 == 0 {
				repo.viewer, repo.lastWorkspaceKey, repo.workspaces = first.Viewer, first.LastWorkspaceKey, firstCatalog
			} else {
				repo.viewer, repo.lastWorkspaceKey, repo.workspaces = second.Viewer, second.LastWorkspaceKey, secondCatalog
			}
			repo.mu.Unlock()
		}
	}()
	for range 2 {
		go func() {
			defer workers.Done()
			<-start
			for range 4 {
				read, err := repo.BeginAccountRead(t.Context())
				if err == nil && !reflect.DeepEqual(read.Account, first) && !reflect.DeepEqual(read.Account, second) {
					err = errors.New("torn installed account")
				}
				if read != nil {
					read.Close()
				}
				if err != nil {
					select {
					case failures <- err:
					default:
					}
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("concurrent account reads leaked work")
	}
}
