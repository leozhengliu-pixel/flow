package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
)

// DocumentRecordSelector runs under the writer lock against immutable metadata.
// Returning false retains the canonical mutation, including large cascades.
type DocumentRecordSelector func(domain.Bootstrap, string, any) (MetadataRecordScope, bool)

type documentRecordSelectionKey struct{}
type documentRecordSelection struct {
	mode        string
	selectScope DocumentRecordSelector
}

func WithDocumentRecordSelection(ctx context.Context, mode string, selectScope DocumentRecordSelector) context.Context {
	return context.WithValue(ctx, documentRecordSelectionKey{}, documentRecordSelection{mode, selectScope})
}

var documentRecordComparisons atomic.Uint64
var documentRecordDifferences atomic.Uint64

func DocumentRecordVerificationCounts() (comparisons, differences uint64) {
	return documentRecordComparisons.Load(), documentRecordDifferences.Load()
}

func verifyDocumentRecords(mode string) bool {
	if mode == "shadow" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_DOCUMENT_RECORD_VERIFY"))) {
	case "off", "0", "false":
		return false
	case "on", "1", "true":
		return true
	}
	return testing.Testing()
}

func prepareDocumentRecordMutation(ctx context.Context, stored domain.Bootstrap, event string, payload any, mutate func(*domain.Bootstrap) (string, error)) (context.Context, func(*domain.Bootstrap) (string, error), bool) {
	requirements, ok := ctx.Value(documentRecordSelectionKey{}).(documentRecordSelection)
	if !ok || !RecordHydrationEnabled() || fullMutationsForced.Load() {
		return ctx, mutate, false
	}
	scope, ok := requirements.selectScope(stored, event, payload)
	if !ok || validateMetadataRecordScope(scope) != nil || validateMetadataRecordSelection(stored, scope) != nil || documentRecordReferenceFallback(stored, scope) {
		return ctx, mutate, false
	}
	ctx = WithMetadataRecords(ctx, scope)
	ctx = WithMetadataFields(ctx, metadataRecordFields(scope)...)
	apply := func(data *domain.Bootstrap) (string, error) {
		id, err := mutate(data)
		if err == nil {
			mergeDocumentRecordMutation(ctx, data, stored, scope)
		}
		return id, err
	}
	if !verifyDocumentRecords(requirements.mode) {
		return ctx, apply, true
	}
	return ctx, func(data *domain.Bootstrap) (string, error) {
		before := *data
		restoreUnlistedMetadata(&before, stored, nil)
		before.Viewer, before.ViewerRole, before.ViewerTeamIDs = data.Viewer, data.ViewerRole, data.ViewerTeamIDs
		before.Members, before.TeamMembers, before.Users = data.Members, data.TeamMembers, data.Users
		full := cloneCallbackInput(before)
		// The canonical path refreshes references before it installs the
		// request actor in Users; retain that ordering in the oracle too.
		users := full.Users
		full.Users = stored.Users
		refreshDisplayReferences(&full)
		refreshIssueReferences(&full)
		full.Users = users
		full.FormerMembers, full.CollaborationUpdates = data.FormerMembers, data.CollaborationUpdates
		protected, _ := json.Marshal(struct {
			Data    domain.Bootstrap
			Payload any
		}{before, payload})
		candidate, _, _ := runVerifiedCallback(data, payload, auditCollectorFrom(ctx), nil, apply)
		legacy, err, panicked := runVerifiedCallback(&full, payload, auditCollectorFrom(ctx), nil, mutate)
		comparison := documentRecordComparisons.Add(1)
		if path := compareMutationCallbacks(candidate, legacy, protected); path != "" {
			documentRecordDifferences.Add(1)
			slog.Error("document record verification mismatch; using canonical callback", "event", event, "field", path, "comparison", comparison)
			if testing.Testing() {
				panic("document record verification mismatch: " + event + " at " + path)
			}
		}
		// The oracle owns response captures, audit intents and persistence.
		*data = full
		if panicked != nil {
			panic(panicked)
		}
		return legacy.Aggregate, err
	}, true
}

func mergeDocumentRecordMutation(ctx context.Context, data *domain.Bootstrap, stored domain.Bootstrap, scope MetadataRecordScope) {
	// Deletion creates its trash id inside the callback. Only new trash rows
	// may extend the declared write set; existing unrelated payloads stay shared.
	newTrash := []domain.TrashEntry{}
	for _, entry := range data.Trash {
		if !slices.ContainsFunc(stored.Trash, func(item domain.TrashEntry) bool { return item.ID == entry.ID }) {
			scope.Write = append(scope.Write, RecordKey{Kind: "trash", ID: entry.ID})
			newTrash = append(newTrash, entry)
		}
	}
	newDocuments := []domain.Document{}
	for _, document := range data.Documents {
		if metadataRecordSelected(scope, "documents", document.ID, true) && !slices.ContainsFunc(stored.Documents, func(item domain.Document) bool { return item.ID == document.ID }) {
			newDocuments = append(newDocuments, document)
		}
	}
	viewer, role, teams := data.Viewer, data.ViewerRole, data.ViewerTeamIDs
	restoreMetadataRecordFields(WithMetadataRecords(ctx, scope), data, stored, metadataRecordFields(scope))
	data.Viewer, data.ViewerRole, data.ViewerTeamIDs = viewer, role, teams
	// These callbacks prepend new/restored records. Preserve that order when
	// merging the selected records into the untouched collection.
	if len(newTrash) > 0 {
		data.Trash = append(newTrash, slices.DeleteFunc(data.Trash, func(entry domain.TrashEntry) bool {
			return slices.ContainsFunc(newTrash, func(item domain.TrashEntry) bool { return item.ID == entry.ID })
		})...)
	}
	if len(newDocuments) > 0 {
		data.Documents = append(newDocuments, slices.DeleteFunc(data.Documents, func(document domain.Document) bool {
			return slices.ContainsFunc(newDocuments, func(item domain.Document) bool { return item.ID == document.ID })
		})...)
	}
}

// SelectedDocumentMetadata is a trusted storage read. It projects the owned
// selection through the existing policy, including favorites and trash access.
// The selector must only inspect the immutable input; it may not retain it.
func (s *SQLiteStore) SelectedDocumentMetadata(ctx context.Context, workspace, userID string, key *domain.APIKey, selectScope func(domain.Bootstrap) (MetadataRecordScope, bool)) (domain.Bootstrap, error) {
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	s.mu.RLock()
	source, exists := s.workspaces[workspace]
	known, exact := s.copyRevisionLocked(workspace)
	var data domain.Bootstrap
	selected := false
	var scope MetadataRecordScope
	if exists {
		var ok bool
		scope, ok = selectScope(source)
		if ok && validateMetadataRecordScope(scope) == nil {
			selected = true
		} else {
			scope = MetadataRecordScope{}
		}
		if selected && exact && known.workspaceID == stamp.workspaceID && known.revision == stamp.revision {
			data = metadataRecordSnapshot(source, scope, metadataRecordFields(scope))
		}
	}
	s.mu.RUnlock()
	if !selected {
		return domain.Bootstrap{}, ErrResourceAccessChanged
	}
	if data.Workspace.ID == "" {
		hydrated, hydrateErr := s.HydrateWorkspaceMetadata(ctx, workspace, scope)
		if hydrateErr != nil {
			return domain.Bootstrap{}, ErrResourceAccessChanged
		}
		data = hydrated.Data
	}
	if userID != "" {
		data, err = s.projectDocumentRecordMetadata(ctx, workspace, userID, key, data)
		if err != nil {
			return domain.Bootstrap{}, err
		}
	} else {
		stripTrashLinks(&data)
		data.ViewerRole = "admin"
	}
	current, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if current != stamp {
		return domain.Bootstrap{}, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
	}
	return data, nil
}

func (s *SQLiteStore) projectDocumentRecordMetadata(ctx context.Context, workspace, userID string, key *domain.APIKey, data domain.Bootstrap) (domain.Bootstrap, error) {
	before, err := s.ResolveAccess(ctx, workspace, userID, key)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	projected, ok, err := s.projectBootstrapForUser(ctx, data, userID)
	if err != nil || !ok {
		return domain.Bootstrap{}, ErrAuthForbidden
	}
	after, err := s.ResolveAccess(ctx, workspace, userID, key)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if before.AuthzEpoch != after.AuthzEpoch || before.CredentialScope != after.CredentialScope {
		return domain.Bootstrap{}, errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
	}
	return projected, nil
}
