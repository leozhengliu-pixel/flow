package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"flow/api/internal/domain"
)

// Data migrations rewrite stored records, so they run after the workspaces
// are loaded and every record table and index is in place (the schema
// migrations in migrate run before either exists). They share the
// schema_migrations table; versions from 1000 up keep them apart from the
// schema list.
const documentDefaultAccessMigrationVersion = 1001

// documentAccessMigrationBatch bounds the documents rewritten per
// transaction, so a large workspace never holds one long write.
var documentAccessMigrationBatch = 500

func (s *SQLiteStore) dataMigrations() []struct {
	version int
	name    string
	apply   func(context.Context) error
} {
	return []struct {
		version int
		name    string
		apply   func(context.Context) error
	}{
		{version: documentDefaultAccessMigrationVersion, name: "default document access grants", apply: s.migrateDocumentDefaultAccess},
	}
}

func (s *SQLiteStore) runDataMigrations(ctx context.Context) error {
	for _, migration := range s.dataMigrations() {
		applied, err := s.migrationApplied(ctx, migration.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := migration.apply(ctx); err != nil {
			return fmt.Errorf("data migration %d (%s): %w", migration.version, migration.name, err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, migration.version, migration.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			// Another instance starting at the same time may have recorded it
			// first; the migration itself is idempotent.
			if applied, checkErr := s.migrationApplied(ctx, migration.version); checkErr == nil && applied {
				continue
			}
			return fmt.Errorf("record data migration %d: %w", migration.version, err)
		}
	}
	return nil
}

func (s *SQLiteStore) migrationApplied(ctx context.Context, version int) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&count); err != nil {
		return false, fmt.Errorf("read schema migration %d: %w", version, err)
	}
	return count > 0, nil
}

// migrateDocumentDefaultAccess gives documents created before default grants
// existed the access a new document gets (see defaultDocumentGrants in the
// server): an editor grant for each of a team document's teams, or a
// workspace editor grant for a document without teams. Documents that already
// carry any team or workspace grant keep their access as it is, and the
// creator's owner grant is kept (or added, as the create path does). Deleted
// documents in "Recently deleted" are migrated too, so a restore brings the
// same access back.
//
// The workspaces are already in memory (the store holds all metadata there),
// so the documents are read from the snapshots; only the changed document and
// trash records are written, through the same delta writer a document edit
// uses, which keeps the search and customer-filter indexes in step.
func (s *SQLiteStore) migrateDocumentDefaultAccess(ctx context.Context) error {
	documents, deleted := 0, 0
	for _, key := range s.WorkspaceKeys() {
		live, trashed, err := s.migrateWorkspaceDocumentAccess(ctx, key)
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		documents += live
		deleted += trashed
	}
	if documents+deleted > 0 {
		slog.Info("granted default document access", "documents", documents, "deleted_documents", deleted)
	}
	return nil
}

type documentAccessTarget struct {
	trash bool
	index int
}

func (s *SQLiteStore) migrateWorkspaceDocumentAccess(ctx context.Context, key string) (int, int, error) {
	stored, ok := s.storedWorkspace(key)
	if !ok {
		return 0, 0, nil
	}
	targets := []documentAccessTarget{}
	for index, document := range stored.Documents {
		if _, changed := documentDefaultAccess(document.ID, document.TeamIDs, document.Creator.ID, document.CreatedAt, document.Permissions); changed {
			targets = append(targets, documentAccessTarget{index: index})
		}
	}
	for index, entry := range stored.Trash {
		if entry.ResourceType != "document" {
			continue
		}
		if _, changed, err := trashedDocumentDefaultAccess(entry.Payload); err != nil {
			return 0, 0, fmt.Errorf("trash entry %q: %w", entry.ID, err)
		} else if changed {
			targets = append(targets, documentAccessTarget{trash: true, index: index})
		}
	}
	live, trashed := 0, 0
	for start := 0; start < len(targets); start += documentAccessMigrationBatch {
		batch := targets[start:min(start+documentAccessMigrationBatch, len(targets))]
		l, t, err := s.migrateDocumentAccessBatch(ctx, key, batch)
		if err != nil {
			return live, trashed, err
		}
		live += l
		trashed += t
	}
	if live+trashed > 0 {
		s.dropMetadataCache(ctx, key)
		s.mu.Lock()
		if !slices.Contains(s.startupCacheDrops, key) {
			s.startupCacheDrops = append(s.startupCacheDrops, key)
		}
		s.mu.Unlock()
	}
	return live, trashed, nil
}

func (s *SQLiteStore) migrateDocumentAccessBatch(ctx context.Context, key string, batch []documentAccessTarget) (int, int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before, ok := s.storedWorkspace(key)
	if !ok {
		return 0, 0, nil
	}
	after := before
	documentsCloned, trashCloned := false, false
	live, trashed := 0, 0
	for _, target := range batch {
		if !target.trash {
			if target.index >= len(before.Documents) {
				continue
			}
			document := before.Documents[target.index]
			permissions, changed := documentDefaultAccess(document.ID, document.TeamIDs, document.Creator.ID, document.CreatedAt, document.Permissions)
			if !changed {
				continue
			}
			if !documentsCloned {
				after.Documents, documentsCloned = slices.Clone(before.Documents), true
			}
			after.Documents[target.index].Permissions = permissions
			live++
			continue
		}
		if target.index >= len(before.Trash) {
			continue
		}
		payload, changed, err := trashedDocumentDefaultAccess(before.Trash[target.index].Payload)
		if err != nil {
			return 0, 0, err
		}
		if !changed {
			continue
		}
		if !trashCloned {
			after.Trash, trashCloned = slices.Clone(before.Trash), true
		}
		after.Trash[target.index].Payload = payload
		trashed++
	}
	if live+trashed == 0 {
		return 0, 0, nil
	}
	// Only the metadata records are written: persistWorkspaceFrom would also
	// rewrite the account state's last workspace for every migrated workspace.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := s.persistMetadataTx(ctx, tx, key, before, after); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	s.installWorkspace(key, after, false)
	return live, trashed, nil
}

// documentDefaultAccess returns the document's permissions with the default
// grants added, and whether anything was added. A document that already has a
// team or workspace grant is left unchanged.
func documentDefaultAccess(documentID string, teamIDs []string, creatorID string, createdAt time.Time, permissions []domain.DocumentPermission) ([]domain.DocumentPermission, bool) {
	// Only team documents still on the old default (no grants, or just the
	// creator's owner grant) change: a document shared with anyone else, or
	// with no team, keeps the access its owner chose.
	if documentID == "" || !slices.ContainsFunc(teamIDs, func(teamID string) bool { return teamID != "" }) || slices.ContainsFunc(permissions, func(permission domain.DocumentPermission) bool {
		return permission.SubjectType != "user" || permission.SubjectID != creatorID
	}) {
		return permissions, false
	}
	// Grants date from the document's creation (as the stored owner grant
	// does), so the result does not depend on when the migration runs.
	at := createdAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result := slices.Clone(permissions)
	if creatorID != "" && !slices.ContainsFunc(permissions, func(permission domain.DocumentPermission) bool {
		return permission.SubjectType == "user" && permission.SubjectID == creatorID
	}) {
		result = append(result, domain.DocumentPermission{ID: "document_permission_" + creatorID, DocumentID: documentID, SubjectType: "user", SubjectID: creatorID, Role: "owner", CreatedAt: at, UpdatedAt: at})
	}
	teams := []string{}
	for _, teamID := range teamIDs {
		if teamID != "" && !slices.Contains(teams, teamID) {
			teams = append(teams, teamID)
		}
	}
	if len(teams) == 0 {
		result = append(result, domain.DocumentPermission{ID: "document_permission_workspace_", DocumentID: documentID, SubjectType: "workspace", SubjectID: "", Role: "editor", CreatedAt: at, UpdatedAt: at})
	}
	for _, teamID := range teams {
		result = append(result, domain.DocumentPermission{ID: "document_permission_team_" + teamID, DocumentID: documentID, SubjectType: "team", SubjectID: teamID, Role: "editor", CreatedAt: at, UpdatedAt: at})
	}
	return result, true
}

// trashedDocumentDefaultAccess applies documentDefaultAccess to a deleted
// document's trash payload. Only the payload's permissions member is
// replaced; every other member keeps its stored encoding.
func trashedDocumentDefaultAccess(payload json.RawMessage) (json.RawMessage, bool, error) {
	if len(payload) == 0 || payload[0] != '{' {
		return payload, false, nil
	}
	var document struct {
		ID      string   `json:"id"`
		TeamIDs []string `json:"teamIds"`
		Creator struct {
			ID string `json:"id"`
		} `json:"creator"`
		CreatedAt   time.Time                   `json:"createdAt"`
		Permissions []domain.DocumentPermission `json:"permissions"`
	}
	if json.Unmarshal(payload, &document) != nil {
		// Restore skips payloads it cannot decode; so does the migration.
		return payload, false, nil
	}
	permissions, changed := documentDefaultAccess(document.ID, document.TeamIDs, document.Creator.ID, document.CreatedAt, document.Permissions)
	if !changed {
		return payload, false, nil
	}
	root := map[string]json.RawMessage{}
	if json.Unmarshal(payload, &root) != nil {
		return payload, false, nil
	}
	encoded, err := json.Marshal(permissions)
	if err != nil {
		return payload, false, err
	}
	root["permissions"] = encoded
	next, err := json.Marshal(root)
	if err != nil {
		return payload, false, err
	}
	return next, true, nil
}
