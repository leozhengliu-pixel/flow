package store

import (
	"path/filepath"
	"testing"
)

func TestCodingSchemaIsIdempotentAndEnforcesOneActiveEnvironmentPerRepo(t *testing.T) {
	repo, err := OpenSQLite(filepath.Join(t.TempDir(), "coding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	if err := repo.ensureCodingSessions(t.Context()); err != nil {
		t.Fatalf("second ensure must be a no-op: %v", err)
	}
	insert := `INSERT INTO coding_environments(id,workspace_key,name,code_host,host_name,repository,active_repo_key,archived_at,data,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,0,0)`
	if _, err := repo.db.ExecContext(t.Context(), insert, "e1", "w", "one", "github", "github.com", "acme/app", "github:github.com/acme/app", 0, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), insert, "e2", "w", "two", "github", "github.com", "acme/app", "github:github.com/acme/app", 0, []byte(`{}`)); err == nil {
		t.Fatal("a second active environment for the same repository must be rejected")
	}
	// Archived environments keep active_repo_key NULL and do not conflict.
	for _, id := range []string{"e3", "e4"} {
		if _, err := repo.db.ExecContext(t.Context(), insert, id, "w", id, "github", "github.com", "acme/app", nil, 1, []byte(`{}`)); err != nil {
			t.Fatalf("archived environment %s: %v", id, err)
		}
	}
}
