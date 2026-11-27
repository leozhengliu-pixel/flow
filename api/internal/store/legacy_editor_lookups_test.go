package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func legacyEditorLookupsRead(t *testing.T, ctx context.Context, view *SQLiteStore, workspace, workspaceID string, target domain.Issue, source *metadataLogicalSource) *issueEditorLookups {
	t.Helper()
	read, err := newLegacyIssueEditorLookups(ctx, &issueHotInputs{store: view, source: source}, workspace, workspaceID, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		read.close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Error("legacy editor lookup work leaked", view.MetadataWorkUsedForTesting())
		}
	})
	return read
}

func TestLegacyEditorFreshAnyDocument(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f, target := scaleIssuePersistenceFixture(t, nil, nil)
	workspace := f.data.Workspace.URLKey
	for _, reload := range []bool{false, true} {
		view := scaleIssuePersistenceView(t, f, true, reload)
		source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		queries := 0
		ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
		read := legacyEditorLookupsRead(t, ctx, view, workspace, f.data.Workspace.ID, target, source)
		source.Release()
		if queries != 0 {
			t.Fatal("lazy constructor queried SQL")
		}
		document := idgen.New("sw99-document-")
		put := func(body, at string) {
			t.Helper()
			if _, err := f.db.ExecContext(t.Context(), `INSERT INTO document_collaboration_updates(update_id,workspace_key,document_id,client_id,update_data,created_at) VALUES(?,?,?,?,?,?)`,
				idgen.New("sw99-update-"), workspace, document, "client", []byte(body), at); err != nil {
				t.Fatal(err)
			}
		}
		put("first", "2026-11-15T00:00:00Z")
		want, err := collaborationUpdatesLookup(t.Context(), f.db, workspace)(document)
		got, gotErr := read.collaborationUpdates(document)
		if err != nil || gotErr != nil || !reflect.DeepEqual(got, want) || queries != 2 {
			t.Fatal("arbitrary document read differs", got, want, err, gotErr, queries)
		}
		work := view.MetadataWorkUsedForTesting()
		got[0][0] = 'X'
		put("second", "2026-11-15T00:00:01Z")
		want, err = collaborationUpdatesLookup(t.Context(), f.db, workspace)(document)
		again, gotErr := read.collaborationUpdates(document)
		if err != nil || gotErr != nil || !reflect.DeepEqual(again, want) || len(again) != 2 || queries != 4 {
			t.Fatal("fresh repeated lookup was cached or aliased", again, want, err, gotErr, queries)
		}
		if view.MetadataWorkUsedForTesting() <= work || string(got[0]) != "Xirst" {
			t.Fatal("independent returned results lost ownership")
		}
		if empty, err := read.collaborationUpdates(document + "-missing"); err != nil || empty != nil {
			t.Fatal("literal nil empty-result shape changed", empty, err)
		}
		put(strings.Repeat("legal large update ", 120000), "2026-11-15T00:00:02Z")
		want, err = collaborationUpdatesLookup(t.Context(), f.db, workspace)(document)
		large, gotErr := read.collaborationUpdates(document)
		if err != nil || gotErr != nil || len(large) != 3 || !reflect.DeepEqual(large, want) {
			t.Fatal("legal large update differs from literal reader", err, gotErr, len(large))
		}
		read.close()
		read.close()
	}
}

func TestLegacyEditorFormerSQLAndNative(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f, target := scaleIssuePersistenceFixture(t, nil, nil)
	_ = scaleIssueEditorMembers(t, f, 9)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	view := scaleIssuePersistenceView(t, f, true, false)
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	read := legacyEditorLookupsRead(t, t.Context(), view, workspace, workspaceID, target, source)
	want, err := readFormerMembers(t.Context(), f.db, workspaceID, f.data.Members)
	if err != nil || !reflect.DeepEqual(read.formerMembers(), want) || read.check() != nil {
		t.Fatal("database-backed former membership differs", want, err, read.err)
	}
	before := view.MetadataWorkUsedForTesting()
	if !reflect.DeepEqual(read.formerMembers(), want) || view.MetadataWorkUsedForTesting() != before {
		t.Fatal("former result lost original lazy cache")
	}
	read.close()
	for _, copies := range []int{1, 10} {
		data := domain.Bootstrap{Members: []domain.WorkspaceMember{
			{User: domain.User{ID: "", AvatarURL: strings.Repeat("body ", 10000*copies)}, Status: "suspended"},
			{User: domain.User{ID: "former", AvatarURL: strings.Repeat("body ", 10000*copies)}, Status: "suspended"},
			{User: domain.User{ID: "removed"}, Status: "removed"},
		}}
		native, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
		if err != nil {
			t.Fatal(err)
		}
		// A fresh namespace has no memberships, like an auth-disabled workspace.
		read := legacyEditorLookupsRead(t, t.Context(), view, workspace, idgen.New("sw99-no-members-"), target, native)
		native.Release()
		if got := read.formerMembers(); read.check() != nil || !reflect.DeepEqual(got, map[string]bool{"": true, "former": true}) {
			t.Fatal("native ID-only fallback differs", got, read.err)
		}
		read.close()
	}
}

type legacyEditorLookupsReader struct {
	reader metadataReader
	err    error
}

func (r legacyEditorLookupsReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.reader.QueryContext(ctx, query, args...)
}

func TestLegacyEditorIgnoredErrorsAndCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f, target := scaleIssuePersistenceFixture(t, nil, nil)
	view := scaleIssuePersistenceView(t, f, true, false)
	workspace := f.data.Workspace.URLKey
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	ordinary := errors.New("ordinary lookup driver error")
	for _, mode := range []string{"former-error", "updates-error", "cancel", "quota", "closed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			read := legacyEditorLookupsRead(t, ctx, view, workspace, f.data.Workspace.ID, target, source)
			limit := view.metadataResidency.work.limit
			switch mode {
			case "former-error":
				read.reader = legacyEditorLookupsReader{err: ordinary}
				if got := read.formerMembers(); got == nil || len(got) != 0 || read.check() != nil {
					t.Fatal("best-effort former error newly refused callback", got, read.err)
				}
				read.reader = f.db
				if got := read.formerMembers(); len(got) != 0 {
					t.Fatal("failed former map lost original cache", got)
				}
			case "updates-error":
				read.reader = legacyEditorLookupsReader{err: ordinary}
				if got, err := read.collaborationUpdates("any"); got != nil || !errors.Is(err, ordinary) || read.check() != nil {
					t.Fatal("caller-ignored ordinary collaboration error poisoned callback", got, err, read.err)
				}
				read.reader = f.db
				if _, err := read.collaborationUpdates("any"); err != nil || read.check() != nil {
					t.Fatal("ordinary collaboration error was cached", err, read.err)
				}
			case "cancel":
				cancel()
				if _, err := read.collaborationUpdates("any"); !errors.Is(err, context.Canceled) || !errors.Is(read.check(), context.Canceled) {
					t.Fatal("cancellation was not fatal", err, read.err)
				}
			case "quota":
				view.metadataResidency.work.limit = view.MetadataWorkUsedForTesting()
				_, err := read.collaborationUpdates("any")
				view.metadataResidency.work.limit = limit
				if !errors.Is(err, ErrMetadataWorkBudget) || !errors.Is(read.check(), ErrMetadataWorkBudget) {
					t.Fatal("quota refusal was ignored", err, read.err)
				}
			case "closed":
				read.close()
				if _, err := read.collaborationUpdates("any"); !errors.Is(err, ErrMetadataRecordQuery) {
					t.Fatal("closed lookup still read", err)
				}
			}
			read.close()
		})
	}
}

type legacyEditorLookupsConnector struct{ growth bool }
type legacyEditorLookupsDriver struct{}
type legacyEditorLookupsConn struct{ growth bool }
type legacyEditorLookupsRows struct {
	columns []string
	values  []driver.Value
	at      int
	tail    error
}

func (c legacyEditorLookupsConnector) Connect(context.Context) (driver.Conn, error) {
	return legacyEditorLookupsConn{growth: c.growth}, nil
}
func (legacyEditorLookupsConnector) Driver() driver.Driver { return legacyEditorLookupsDriver{} }
func (legacyEditorLookupsDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connector required")
}
func (legacyEditorLookupsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare unsupported")
}
func (legacyEditorLookupsConn) Close() error { return nil }
func (legacyEditorLookupsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction unsupported")
}
func (c legacyEditorLookupsConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "COUNT(*)") {
		size := int64(10)
		if c.growth {
			size = 1
		}
		return &legacyEditorLookupsRows{columns: []string{"count", "size"}, values: []driver.Value{int64(2), size}, tail: io.EOF}, nil
	}
	if strings.HasPrefix(query, "SELECT CASE WHEN") {
		body := driver.Value([]byte("first"))
		if c.growth {
			body = nil
		}
		return &legacyEditorLookupsRows{columns: []string{"data", "size"}, values: []driver.Value{body, int64(5)}, tail: io.ErrUnexpectedEOF}, nil
	}
	return &legacyEditorLookupsRows{columns: []string{"data"}, values: []driver.Value{[]byte("first")}, tail: io.ErrUnexpectedEOF}, nil
}
func (r *legacyEditorLookupsRows) Columns() []string { return r.columns }
func (r *legacyEditorLookupsRows) Close() error      { return nil }
func (r *legacyEditorLookupsRows) Next(destination []driver.Value) error {
	if r.at > 0 {
		return r.tail
	}
	r.at++
	copy(destination, r.values)
	return nil
}

func TestLegacyEditorPartialEOFAndTransferGuard(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f, target := scaleIssuePersistenceFixture(t, nil, nil)
	view := scaleIssuePersistenceView(t, f, true, false)
	source, err := view.retainMetadataLogicalSource(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, growth := range []bool{false, true} {
		db := sql.OpenDB(legacyEditorLookupsConnector{growth: growth})
		reader := &sqlDatabase{DB: db, dialect: "sqlite"}
		read := legacyEditorLookupsRead(t, t.Context(), view, f.data.Workspace.URLKey, f.data.Workspace.ID, target, source)
		read.reader = reader
		want, wantErr := collaborationUpdatesLookup(t.Context(), reader, f.data.Workspace.URLKey)("any")
		got, gotErr := read.collaborationUpdates("any")
		if growth {
			if got != nil || !errors.Is(gotErr, ErrMetadataRecordQuery) || !errors.Is(read.check(), ErrMetadataRecordQuery) {
				t.Fatal("post-preflight body growth escaped transfer guard", got, gotErr, read.err)
			}
		} else {
			if !reflect.DeepEqual(got, want) || !errors.Is(gotErr, wantErr) || !errors.Is(gotErr, io.ErrUnexpectedEOF) || read.check() != nil {
				t.Fatal("partial EOF result/error changed", got, want, gotErr, wantErr, read.err)
			}
			before := view.MetadataWorkUsedForTesting()
			got[0][0] = 'X'
			again, err := read.collaborationUpdates("other-document")
			if !reflect.DeepEqual(again, want) || !errors.Is(err, io.ErrUnexpectedEOF) || view.MetadataWorkUsedForTesting() <= before {
				t.Fatal("partial EOF result ownership/freshness changed", again, want, err)
			}
		}
		read.close()
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyFormerPartialEOFAndTransferGuard(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f, target := scaleIssuePersistenceFixture(t, nil, nil)
	view := scaleIssuePersistenceView(t, f, true, false)
	source, err := view.retainMetadataLogicalSource(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, growth := range []bool{false, true} {
		db := sql.OpenDB(legacyEditorLookupsConnector{growth: growth})
		reader := &sqlDatabase{DB: db, dialect: "sqlite"}
		read := legacyEditorLookupsRead(t, t.Context(), view, f.data.Workspace.URLKey, f.data.Workspace.ID, target, source)
		read.reader = reader
		want, wantErr := readFormerMembers(t.Context(), reader, f.data.Workspace.ID, nil)
		got := read.formerMembers()
		if growth {
			if len(got) != 0 || !errors.Is(read.check(), ErrMetadataRecordQuery) {
				t.Fatal("post-preflight former ID growth escaped transfer guard", got, read.err)
			}
		} else {
			if !errors.Is(wantErr, io.ErrUnexpectedEOF) || !reflect.DeepEqual(got, want) || read.check() != nil {
				t.Fatal("best-effort partial former map changed", got, want, wantErr, read.err)
			}
			before := view.MetadataWorkUsedForTesting()
			if !reflect.DeepEqual(read.formerMembers(), want) || view.MetadataWorkUsedForTesting() != before {
				t.Fatal("ordinary EOF former map was not cached")
			}
		}
		read.close()
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
