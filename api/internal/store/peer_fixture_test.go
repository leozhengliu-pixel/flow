package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScaleP21SW311PeerFixtureSQLitePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persistent-peers.sqlite")
	t.Setenv("FLOW_TEST_DATABASE_DRIVER", "sqlite")
	t.Setenv("FLOW_TEST_DATABASE_URL", path)
	pair := peerStoreSets(t)["sqlite"]
	for _, repo := range pair {
		var sequence int
		var name, opened string
		if err := repo.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &opened); err != nil {
			t.Fatal(err)
		}
		requested, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := os.Stat(opened)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(actual, requested) {
			t.Fatalf("peer database opened %q instead of requested scratch path %q", opened, path)
		}
	}
}
