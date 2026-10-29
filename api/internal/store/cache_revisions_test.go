package store

import "testing"

func TestUnscopedEventsDoNotQueueCacheRevisions(t *testing.T) {
	tx := &sqlTx{}
	if err := tx.queueEventCacheRevisions(t.Context(), domainEventRow{Type: "issue.updated"}); err != nil {
		t.Fatal(err)
	}
	if len(tx.cacheRevisions) != 0 {
		t.Fatal("unscoped event advanced a workspace revision")
	}
}

// Statements that write the workspace document or its metadata records name
// the workspace they write; every other statement names none.
func TestMetadataWriteWorkspace(t *testing.T) {
	for _, test := range []struct {
		query string
		args  []any
		want  string
	}{
		{`INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'users',?,0,?) ON CONFLICT(workspace_key,field,record_key) DO UPDATE SET data=excluded.data`, []any{"acme", "u1", []byte("{}")}, "acme"},
		{`INSERT INTO workspace_states(workspace_key,workspace_id,data,updated_at) VALUES(?,?,?,?)`, []any{"acme", "ws1", []byte("{}"), "now"}, "acme"},
		{`UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, []any{[]byte("{}"), "acme", "k1"}, "acme"},
		{`UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key = ? AND field=? AND record_key=?`, []any{3, "acme", "teams", "t1"}, "acme"},
		{`UPDATE workspace_states SET data=?,updated_at=? WHERE workspace_key=?`, []any{[]byte("{}"), "now", "acme"}, "acme"},
		{`DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='favorites' AND record_key=?`, []any{"acme", "f1"}, "acme"},
		{`  delete from workspace_states where workspace_key=?`, []any{"acme"}, "acme"},
		{`SELECT data FROM workspace_metadata_records WHERE workspace_key=?`, []any{"acme"}, ""},
		{`INSERT INTO chat_domain_cursors(workspace_key,seq) SELECT w.workspace_key,0 FROM workspace_states w WHERE w.workspace_key=?`, []any{"acme"}, ""},
		{`UPDATE workspace_metadata_records_migrations SET done=1 WHERE workspace_key=?`, []any{"acme"}, ""},
		{`UPDATE issue_records SET data=? WHERE workspace_key=?`, []any{[]byte("{}"), "acme"}, ""},
	} {
		got, ok := metadataWriteWorkspace(test.query, test.args)
		if got != test.want || ok != (test.want != "") {
			t.Errorf("%s: got %q (%v), want %q", test.query, got, ok, test.want)
		}
	}
}
