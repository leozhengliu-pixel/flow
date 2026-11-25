package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type p06SQLConnector struct {
	base  driver.Driver
	path  string
	count *atomic.Int64
}

func (c p06SQLConnector) Driver() driver.Driver { return c.base }
func (c p06SQLConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.base.Open(c.path)
	if err != nil {
		return nil, err
	}
	return p06SQLConn{conn, c.count}, nil
}

type p06SQLConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c p06SQLConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.count.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c p06SQLConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.count.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c p06SQLConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	c.count.Add(1)
	var tx driver.Tx
	var err error
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = b.BeginTx(ctx, o)
	} else {
		tx, err = c.Conn.Begin()
	}
	if err != nil {
		return nil, err
	}
	return p06SQLTx{tx, c.count}, nil
}

type p06SQLTx struct {
	driver.Tx
	count *atomic.Int64
}

func (tx p06SQLTx) Commit() error   { tx.count.Add(1); return tx.Tx.Commit() }
func (tx p06SQLTx) Rollback() error { tx.count.Add(1); return tx.Tx.Rollback() }

func TestScaleP06Replay500SQLBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.db")
	fixture, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	base := fixture.db.DB.Driver()
	if err := fixture.Close(); err != nil {
		t.Fatal(err)
	}
	var statements atomic.Int64
	db := sql.OpenDB(p06SQLConnector{base, path, &statements})
	defer db.Close()
	db.SetMaxOpenConns(1)
	repo := &SQLiteStore{db: &sqlDatabase{DB: db, dialect: "sqlite"}, dialect: "sqlite"}
	ctx := t.Context()
	for i := 1; i <= 500; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO domain_events(id,workspace_id,event_type,aggregate_id,payload,created_at,seq) VALUES(?,?,?,?,?,?,?)`, fmt.Sprintf("p06-%d", i), "p06-window", "workspace.updated", "workspace", []byte(`{"name":"small"}`), time.Now().UTC().Format(time.RFC3339Nano), i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO workspace_event_seq(workspace_id,head,pruned) VALUES(?,?,?)`, "p06-window", 500, 0); err != nil {
		t.Fatal(err)
	}
	statements.Store(0)
	old, err := repo.WorkspaceChanges(ctx, "p06-window", 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	before := statements.Swap(0)
	window, err := repo.OpenRealtimeReplay(ctx, "p06-window", 0)
	if err != nil {
		t.Fatal(err)
	}
	var records []ChangeRecord
	var cursor int64
	for {
		page, err := repo.ReadRealtimeReplayPage(ctx, window, cursor, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if page.ResyncRequired {
			t.Fatal("unexpected resync")
		}
		records = append(records, page.Records...)
		for _, r := range page.Records {
			cursor = r.Seq
		}
		if !page.HasMore {
			break
		}
	}
	after := statements.Load()
	want, _ := json.Marshal(old.Records)
	got, _ := json.Marshal(records)
	if string(want) != string(got) || len(records) != 500 {
		t.Fatal("paged replay lost or changed records")
	}
	if after > before+8 {
		t.Fatalf("replay SQL statements before=%d after=%d", before, after)
	}
	t.Logf("500-event replay SQL statements: before=%d after=%d", before, after)
}

func TestScaleP06CollaborationPagesRestartOnCompaction(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	data := repo.Bootstrap()
	issue := data.Issues[0]
	doc := "document_content_" + issue.ID
	if issue.DocumentContent != nil && issue.DocumentContent.ID != "" {
		doc = issue.DocumentContent.ID
	}
	ctx := t.Context()
	now := time.Now().UTC()
	for i := range 70 {
		_, err := repo.AppendDocumentCollaborationUpdate(ctx, data.Workspace.URLKey, DocumentCollaborationUpdate{ID: fmt.Sprintf("collab_p06_%03d", i), DocumentID: doc, Data: []byte{byte(i)}, CreatedAt: now.Add(time.Duration(i) * time.Millisecond)})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := repo.ReadCollaborationRecoveryPage(ctx, data.Workspace.URLKey, doc, issue.ID, "", CollaborationRecoveryCursor{}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !first.More || len(first.Updates) == 0 {
		t.Fatal("fixture must span pages")
	}
	_, err = repo.AppendDocumentCollaborationUpdate(ctx, data.Workspace.URLKey, DocumentCollaborationUpdate{ID: "collab_p06_live", DocumentID: doc, Data: []byte{99}, CreatedAt: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	cursor := first.Cursor
	count := len(first.Updates)
	for {
		p, err := repo.ReadCollaborationRecoveryPage(ctx, data.Workspace.URLKey, doc, issue.ID, "", cursor, 4096)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range p.Updates {
			if u.ID == "collab_p06_live" {
				t.Fatal("post-boundary append entered the snapshot")
			}
		}
		count += len(p.Updates)
		cursor = p.Cursor
		if !p.More {
			break
		}
	}
	if count != 70 {
		t.Fatalf("snapshot rows=%d", count)
	}
	ids := []string{}
	for i := range 70 {
		ids = append(ids, fmt.Sprintf("collab_p06_%03d", i))
	}
	err = repo.MutateWorkspace(ctx, data.Workspace.URLKey, "issue.updated", issue.ID, nil, func(d *domain.Bootstrap) error {
		for i := range d.Issues {
			if d.Issues[i].ID == issue.ID {
				if d.Issues[i].DocumentContent == nil {
					d.Issues[i].DocumentContent = &domain.DocumentContent{ID: doc}
				}
				d.Issues[i].DocumentContent.ContentState = "AQID"
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p, err := repo.ReadCollaborationRecoveryPage(ctx, data.Workspace.URLKey, doc, issue.ID, "", first.Cursor, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Restart || p.ContentState != "AQID" {
		t.Fatal("compaction did not restart from the current base")
	}
	if err := repo.DeleteDocumentCollaborationUpdates(ctx, data.Workspace.URLKey, doc, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReadCollaborationRecoveryPage(ctx, data.Workspace.URLKey, doc, issue.ID, "", p.Cursor, 4096); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(ctx, data.Workspace.URLKey, "issue.updated", issue.ID, nil, func(d *domain.Bootstrap) error {
		for i := range d.Issues {
			if d.Issues[i].ID == issue.ID {
				d.Issues[i].DocumentContent.ID = "new-generation"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p, err = repo.ReadCollaborationRecoveryPage(ctx, data.Workspace.URLKey, doc, issue.ID, "", p.Cursor, 4096)
	if err != nil || !p.Conflict {
		t.Fatalf("generation conflict=%v err=%v", p.Conflict, err)
	}
}
