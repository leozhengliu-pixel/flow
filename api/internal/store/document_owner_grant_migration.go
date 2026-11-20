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

const documentOwnerGrantMigrationVersion = 1041

// ownerGrantID is the identifier normalize gives a document's missing creator
// grant. Grants written by the create path carry random identifiers.
func ownerGrantID(creatorID string) string { return "document_permission_" + creatorID }

// synthesizedOwnerGrant reports whether the permission is the creator's owner
// grant as normalize derives it.
func synthesizedOwnerGrant(permission domain.DocumentPermission, creatorID string) bool {
	return creatorID != "" && permission.ID == ownerGrantID(creatorID) && permission.SubjectType == "user" && permission.SubjectID == creatorID && permission.Role == "owner"
}

// migrateDocumentOwnerGrants writes the creator's owner grant into documents
// stored without one (rows from before documents carried grants). Loading a
// workspace adds the grant to the in-memory image (normalize), so the literal
// reads see it, but a bounded read decodes the stored records and did not: the
// creator of a document in a private team they are not on could not read it,
// and the document's permissions list was empty. Persisting what normalize
// derives makes both read the same record. Deleted documents' payloads are
// migrated too, so a restore brings the same grant back.
func (s *SQLiteStore) migrateDocumentOwnerGrants(ctx context.Context) error {
	documents, deleted := 0, 0
	for _, key := range s.WorkspaceKeys() {
		var live, trashed int
		err := s.rerunBackgroundWrite(ctx, "document owner grant migration", key, func() (err error) {
			live, trashed, err = s.migrateWorkspaceDocumentOwnerGrants(ctx, key)
			return err
		})
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		documents += live
		deleted += trashed
	}
	if documents+deleted > 0 {
		slog.Info("wrote document owner grants", "documents", documents, "deleted_documents", deleted)
	}
	return nil
}

// MigrateDocumentOwnerGrantsForTesting runs the migration on the loaded workspaces.
func (s *SQLiteStore) MigrateDocumentOwnerGrantsForTesting(ctx context.Context) error {
	return s.migrateDocumentOwnerGrants(ctx)
}

func (s *SQLiteStore) migrateWorkspaceDocumentOwnerGrants(ctx context.Context, key string) (int, int, error) {
	current, ok := s.storedWorkspace(key)
	if !ok {
		return 0, 0, nil
	}
	// The candidates are fixed up front: a document whose grant was already
	// stored is rewritten identically, so it must not be picked up again.
	var documentIDs, entryIDs []string
	for _, document := range current.Documents {
		if slices.ContainsFunc(document.Permissions, func(permission domain.DocumentPermission) bool {
			return synthesizedOwnerGrant(permission, document.Creator.ID)
		}) {
			documentIDs = append(documentIDs, document.ID)
		}
	}
	for _, entry := range current.Trash {
		if entry.ResourceType != "document" {
			continue
		}
		if _, changed, err := trashedDocumentOwnerGrant(entry.Payload); err != nil {
			return 0, 0, fmt.Errorf("trash entry %q: %w", entry.ID, err)
		} else if changed {
			entryIDs = append(entryIDs, entry.ID)
		}
	}
	live, trashed := 0, 0
	for start := 0; start < len(documentIDs) || start < len(entryIDs); start += documentAccessMigrationBatch {
		l, t, err := s.migrateDocumentOwnerGrantBatch(ctx, key, window(documentIDs, start), window(entryIDs, start))
		if err != nil {
			return live, trashed, err
		}
		live += l
		trashed += t
	}
	return live, trashed, nil
}

func window(ids []string, start int) []string {
	if start >= len(ids) {
		return nil
	}
	return ids[start:min(start+documentAccessMigrationBatch, len(ids))]
}

// migrateDocumentOwnerGrantBatch rewrites one batch. The delta writer compares
// a "before" without the derived grants to the image, so exactly those
// records are written.
func (s *SQLiteStore) migrateDocumentOwnerGrantBatch(ctx context.Context, key string, documentIDs, entryIDs []string) (int, int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	current, ok := s.storedWorkspace(key)
	if !ok {
		return 0, 0, nil
	}
	before, after := current, current
	documentsCloned, trashCloned := false, false
	live, trashed := 0, 0
	for index, document := range current.Documents {
		if !slices.Contains(documentIDs, document.ID) {
			continue
		}
		position := slices.IndexFunc(document.Permissions, func(permission domain.DocumentPermission) bool {
			return synthesizedOwnerGrant(permission, document.Creator.ID)
		})
		if position < 0 {
			continue
		}
		if !documentsCloned {
			before.Documents, documentsCloned = slices.Clone(current.Documents), true
		}
		before.Documents[index].Permissions = slices.Delete(slices.Clone(document.Permissions), position, position+1)
		live++
	}
	for index, entry := range current.Trash {
		if !slices.Contains(entryIDs, entry.ID) {
			continue
		}
		payload, changed, err := trashedDocumentOwnerGrant(entry.Payload)
		if err != nil {
			return 0, 0, err
		}
		if !changed {
			continue
		}
		if !trashCloned {
			after.Trash, trashCloned = slices.Clone(current.Trash), true
		}
		after.Trash[index].Payload = payload
		trashed++
	}
	if live+trashed == 0 {
		return 0, 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := s.persistMetadataTx(ctx, tx, key, before, &after); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	s.installWorkspace(key, after, false)
	s.dropMetadataCache(ctx, key)
	s.mu.Lock()
	if !slices.Contains(s.startupCacheDrops, key) {
		s.startupCacheDrops = append(s.startupCacheDrops, key)
	}
	s.mu.Unlock()
	return live, trashed, nil
}

// trashedDocumentOwnerGrant adds the creator's owner grant to a deleted
// document's payload that has no grant for its creator.
func trashedDocumentOwnerGrant(payload json.RawMessage) (json.RawMessage, bool, error) {
	if len(payload) == 0 || payload[0] != '{' {
		return payload, false, nil
	}
	var document struct {
		ID      string `json:"id"`
		Creator struct {
			ID string `json:"id"`
		} `json:"creator"`
		CreatedAt   time.Time                   `json:"createdAt"`
		Permissions []domain.DocumentPermission `json:"permissions"`
	}
	if json.Unmarshal(payload, &document) != nil || document.ID == "" || document.Creator.ID == "" {
		return payload, false, nil
	}
	if slices.ContainsFunc(document.Permissions, func(permission domain.DocumentPermission) bool {
		return permission.SubjectType == "user" && permission.SubjectID == document.Creator.ID
	}) {
		return payload, false, nil
	}
	root := map[string]json.RawMessage{}
	if json.Unmarshal(payload, &root) != nil {
		return payload, false, nil
	}
	at := document.CreatedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	grants := append(slices.Clone(document.Permissions), domain.DocumentPermission{ID: ownerGrantID(document.Creator.ID), DocumentID: document.ID, SubjectType: "user", SubjectID: document.Creator.ID, Role: "owner", CreatedAt: at, UpdatedAt: at})
	encoded, err := json.Marshal(grants)
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
