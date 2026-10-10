package store

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

const accessWorkspace = "test-workspace"

type accessFixture struct {
	creator domain.User
	team    string
	created time.Time
}

// seedLegacyDocuments writes documents (and a deleted document) in the shapes
// stored before default grants existed, then marks the access migration as
// not yet applied so the next open runs it.
func seedLegacyDocuments(t *testing.T, repo *SQLiteStore) accessFixture {
	t.Helper()
	ctx := context.Background()
	data, ok := repo.BootstrapFor(accessWorkspace)
	if !ok || len(data.Teams) == 0 || len(data.Users) == 0 {
		t.Fatalf("fixture workspace has no teams or users")
	}
	fixture := accessFixture{creator: data.Users[0], team: data.Teams[0].ID, created: time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)}
	owner := func(id string) domain.DocumentPermission {
		return domain.DocumentPermission{ID: "document_permission_owner", DocumentID: id, SubjectType: "user", SubjectID: fixture.creator.ID, Role: "owner", CreatedAt: fixture.created, UpdatedAt: fixture.created}
	}
	document := func(id string, teams []string, permissions []domain.DocumentPermission) domain.Document {
		return domain.Document{ID: id, SlugID: id, Title: id, Creator: fixture.creator, TeamIDs: teams, ProjectIDs: []string{}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: fixture.created, UpdatedAt: fixture.created, Version: 3, Permissions: permissions}
	}
	teamViewer := domain.DocumentPermission{ID: "document_permission_team_" + fixture.team, DocumentID: "doc_team_viewer", SubjectType: "team", SubjectID: fixture.team, Role: "viewer", CreatedAt: fixture.created, UpdatedAt: fixture.created}
	workspaceViewer := domain.DocumentPermission{ID: "document_permission_workspace_", DocumentID: "doc_workspace_viewer", SubjectType: "workspace", Role: "viewer", CreatedAt: fixture.created, UpdatedAt: fixture.created}
	sharedUser := domain.DocumentPermission{ID: "document_permission_shared", DocumentID: "doc_team_shared", SubjectType: "user", SubjectID: "usr_shared", Role: "editor", CreatedAt: fixture.created, UpdatedAt: fixture.created}
	trashed, err := json.Marshal(document("doc_trashed", []string{fixture.team}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(ctx, accessWorkspace, "document.created", "doc_team_owner", nil, func(next *domain.Bootstrap) error {
		next.Documents = append(next.Documents,
			document("doc_team_owner", []string{fixture.team}, []domain.DocumentPermission{owner("doc_team_owner")}),
			document("doc_team_viewer", []string{fixture.team}, []domain.DocumentPermission{owner("doc_team_viewer"), teamViewer}),
			document("doc_team_shared", []string{fixture.team}, []domain.DocumentPermission{owner("doc_team_shared"), sharedUser}),
			document("doc_no_team", []string{}, []domain.DocumentPermission{owner("doc_no_team")}),
			document("doc_workspace_viewer", []string{}, []domain.DocumentPermission{owner("doc_workspace_viewer"), workspaceViewer}),
			document("doc_legacy", []string{fixture.team}, nil),
		)
		next.Trash = append(next.Trash, domain.TrashEntry{ID: "trash_doc", ResourceType: "document", ResourceID: "doc_trashed", Title: "Trashed", Payload: trashed, DeletedBy: fixture.creator, DeletedAt: fixture.created, ExpiresAt: fixture.created.AddDate(0, 0, 30)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The legacy document is stored without permissions.
	if raw := storedDocumentRecord(t, repo, "documents", "doc_legacy"); bytes.Contains(raw, []byte(`"permissions"`)) {
		t.Fatalf("legacy document was stored with permissions: %s", raw)
	}
	if _, err := repo.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=?`, documentDefaultAccessMigrationVersion); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func storedDocumentRecord(t *testing.T, repo *SQLiteStore, field, key string) []byte {
	t.Helper()
	var raw []byte
	if err := repo.db.QueryRowContext(context.Background(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, accessWorkspace, field, key).Scan(&raw); err != nil {
		t.Fatalf("read %s record %s: %v", field, key, err)
	}
	return raw
}

func storedDocument(t *testing.T, repo *SQLiteStore, id string) domain.Document {
	t.Helper()
	var document domain.Document
	if err := json.Unmarshal(storedDocumentRecord(t, repo, "documents", id), &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func grantsOf(permissions []domain.DocumentPermission) []string {
	result := []string{}
	for _, permission := range permissions {
		result = append(result, permission.ID+"|"+permission.SubjectType+":"+permission.SubjectID+"="+permission.Role)
	}
	return result
}

func memoryDocument(t *testing.T, repo *SQLiteStore, id string) domain.Document {
	t.Helper()
	data, _ := repo.BootstrapFor(accessWorkspace)
	index := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == id })
	if index < 0 {
		t.Fatalf("document %s missing", id)
	}
	return data.Documents[index]
}

func documentAccessRecords(t *testing.T, repo *SQLiteStore) map[string][]byte {
	t.Helper()
	rows, err := repo.db.QueryContext(context.Background(), `SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('documents','trash')`, accessWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string][]byte{}
	for rows.Next() {
		var field, key string
		var raw []byte
		if err := rows.Scan(&field, &key, &raw); err != nil {
			t.Fatal(err)
		}
		result[field+"/"+key] = raw
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDocumentDefaultAccessMigration(t *testing.T) {
	previous := documentAccessMigrationBatch
	documentAccessMigrationBatch = 2 // several batches over the seeded documents
	t.Cleanup(func() { documentAccessMigrationBatch = previous })
	path := filepath.Join(t.TempDir(), "flow.db")
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedLegacyDocuments(t, repo)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { repo.Close() }()

	creatorGrant := func(id string) string { return id + "|user:" + fixture.creator.ID + "=owner" }
	teamEditor := "document_permission_team_" + fixture.team + "|team:" + fixture.team + "=editor"
	want := map[string][]string{
		// A team document with only its creator's grant gains its team.
		"doc_team_owner": {creatorGrant("document_permission_owner"), teamEditor},
		// A chosen team role is kept.
		"doc_team_viewer": {creatorGrant("document_permission_owner"), "document_permission_team_" + fixture.team + "|team:" + fixture.team + "=viewer"},
		// A team document its owner shared with someone keeps that choice.
		"doc_team_shared": {creatorGrant("document_permission_owner"), "document_permission_shared|user:usr_shared=editor"},
		// A document without teams keeps its access.
		"doc_no_team": {creatorGrant("document_permission_owner")},
		// A chosen workspace role is kept.
		"doc_workspace_viewer": {creatorGrant("document_permission_owner"), "document_permission_workspace_|workspace:=viewer"},
		// A legacy document gains its creator's owner grant and its team.
		"doc_legacy": {creatorGrant("document_permission_" + fixture.creator.ID), teamEditor},
	}
	for id, grants := range want {
		stored := storedDocument(t, repo, id)
		if got := grantsOf(stored.Permissions); !slices.Equal(got, grants) {
			t.Errorf("%s stored grants = %v, want %v", id, got, grants)
		}
		if got := grantsOf(memoryDocument(t, repo, id).Permissions); !slices.Equal(got, grants) {
			t.Errorf("%s loaded grants = %v, want %v", id, got, grants)
		}
		// Access changes are not edits: version and update time stay.
		if stored.Version != 3 || !stored.UpdatedAt.Equal(fixture.created) {
			t.Errorf("%s version/updatedAt changed: %d %v", id, stored.Version, stored.UpdatedAt)
		}
	}
	added := storedDocument(t, repo, "doc_legacy").Permissions[1]
	if added.DocumentID != "doc_legacy" || !added.CreatedAt.Equal(fixture.created) || !added.UpdatedAt.Equal(fixture.created) {
		t.Fatalf("added grant = %+v", added)
	}

	// The deleted document's payload gets the same access, so restoring it
	// brings it back.
	var entry domain.TrashEntry
	if err := json.Unmarshal(storedDocumentRecord(t, repo, "trash", "trash_doc"), &entry); err != nil {
		t.Fatal(err)
	}
	var trashed domain.Document
	if err := json.Unmarshal(entry.Payload, &trashed); err != nil {
		t.Fatal(err)
	}
	if got, grants := grantsOf(trashed.Permissions), []string{creatorGrant("document_permission_" + fixture.creator.ID), teamEditor}; !slices.Equal(got, grants) {
		t.Fatalf("trashed document grants = %v, want %v", got, grants)
	}
	if trashed.Title != "doc_trashed" || trashed.Version != 3 {
		t.Fatalf("trashed payload lost fields: %+v", trashed)
	}

	applied, err := repo.migrationApplied(context.Background(), documentDefaultAccessMigrationVersion)
	if err != nil || !applied {
		t.Fatalf("migration not recorded: %v %v", applied, err)
	}

	// Running the migration again (or reopening) changes nothing.
	records := documentAccessRecords(t, repo)
	if err := repo.migrateDocumentDefaultAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if again := documentAccessRecords(t, repo); !recordsEqual(records, again) {
		t.Fatal("second migration run rewrote records")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if again := documentAccessRecords(t, repo); !recordsEqual(records, again) {
		t.Fatal("reopening rewrote records")
	}
	for id, grants := range want {
		if got := grantsOf(memoryDocument(t, repo, id).Permissions); !slices.Equal(got, grants) {
			t.Errorf("%s grants after reopen = %v, want %v", id, got, grants)
		}
	}
}

func recordsEqual(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if !bytes.Equal(value, b[key]) {
			return false
		}
	}
	return true
}

func TestDocumentDefaultAccessMigrationDropsSharedMetadataCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flow.db")
	server := miniredis.RunT(t)
	connect := func() *coordination.Redis {
		coordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{server.Addr()}, Prefix: "access-test", ConnectTimeout: time.Second, LockTTL: time.Second, LockWait: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { coordinator.Close() })
		return coordinator
	}
	repo, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	repo.SetWorkspaceCoordinator(connect())
	seedLegacyDocuments(t, repo)
	// Fill the shared metadata cache with the pre-migration records.
	if err := repo.ReloadWorkspace(t.Context(), accessWorkspace); err != nil {
		t.Fatal(err)
	}
	if ready, _ := repo.hotCache().CacheGet(t.Context(), repo.metadataReadyKey(accessWorkspace)); string(ready) != "1" {
		t.Fatal("metadata cache was not filled")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}

	// The store opens (and migrates) before the coordinator is attached.
	repo, err = OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	repo.SetWorkspaceCoordinator(connect())
	if ready, _ := repo.hotCache().CacheGet(t.Context(), repo.metadataReadyKey(accessWorkspace)); len(ready) != 0 {
		t.Fatal("stale metadata cache survived the migration")
	}
	if err := repo.ReloadWorkspace(t.Context(), accessWorkspace); err != nil {
		t.Fatal(err)
	}
	if grants := memoryDocument(t, repo, "doc_team_owner").Permissions; !slices.ContainsFunc(grants, func(permission domain.DocumentPermission) bool {
		return permission.SubjectType == "team" && permission.Role == "editor"
	}) {
		t.Fatalf("reload served pre-migration grants: %v", grantsOf(grants))
	}
}
