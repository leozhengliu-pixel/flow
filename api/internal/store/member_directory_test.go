package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Members who joined before joining added them to the workspace directory
// (the users writes resolve people against) are added by the startup
// migration, once, and stay after a restart. Suspended members and
// application users are not added.
func TestMemberDirectoryMigrationAddsJoinedMembers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "directory.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := repo.WorkspaceKeys()[0]
	data, _ := repo.storedWorkspace(key)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, row := range []struct{ id, email, status, role string }{
		{"usr_directory_joined", "directory-joined@example.com", "active", "member"},
		{"usr_directory_suspended", "directory-suspended@example.com", "suspended", "member"},
		{"usr_directory_guest", "directory-guest@example.com", "active", "guest"},
	} {
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO auth_users(id,email,name,display_name,avatar_url,password_hash,email_verified_at,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, row.id, row.email, row.id, row.id, "", "", now, 1, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.db.ExecContext(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,?,?,?)`, data.Workspace.ID, row.id, row.role, row.status, now, now); err != nil {
			t.Fatal(err)
		}
	}
	inDirectory := func(repo *SQLiteStore) []string {
		current, _ := repo.storedWorkspace(key)
		ids := []string{}
		for _, user := range current.Users {
			if slices.Contains([]string{"usr_directory_joined", "usr_directory_suspended", "usr_directory_guest"}, user.ID) {
				ids = append(ids, user.ID)
			}
		}
		slices.Sort(ids)
		return ids
	}
	if ids := inDirectory(repo); len(ids) != 0 {
		t.Fatalf("members written straight to the database are already in the directory: %v", ids)
	}
	if err := repo.migrateMemberDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"usr_directory_guest", "usr_directory_joined"}
	if ids := inDirectory(repo); !slices.Equal(ids, want) {
		t.Fatalf("directory after the migration: %v, want %v", ids, want)
	}
	if added, err := repo.syncMemberDirectory(ctx, key); err != nil || added != 0 {
		t.Fatalf("a second pass added %d (%v)", added, err)
	}
	current, _ := repo.storedWorkspace(key)
	joined := current.Users[slices.IndexFunc(current.Users, func(user domain.User) bool { return user.ID == "usr_directory_joined" })]
	if joined.Email != "directory-joined@example.com" || !joined.Active {
		t.Fatalf("directory entry: %+v", joined)
	}
	repo.Close()

	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if ids := inDirectory(reopened); !slices.Equal(ids, want) {
		t.Fatalf("directory after a restart: %v, want %v", ids, want)
	}
}
