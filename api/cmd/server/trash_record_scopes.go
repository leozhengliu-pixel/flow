package main

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func documentTrashRequirements(data domain.Bootstrap, ref string, entryID bool) (store.MetadataRecordScope, bool) {
	for _, entry := range data.Trash {
		if entry.ResourceType != "document" || entryID && entry.ID != ref || !entryID && entry.ResourceID != ref {
			continue
		}
		var document domain.Document
		if json.Unmarshal(entry.Payload, &document) != nil || document.ID == "" {
			return store.MetadataRecordScope{}, false
		}
		if entryID && slices.ContainsFunc(data.Documents, func(item domain.Document) bool { return item.ID == document.ID }) {
			return store.MetadataRecordScope{}, false
		}
		scope := documentRecordRequirements(data, document, nil)
		scope.Write = append(scope.Write, store.RecordKey{Kind: "trash", ID: entry.ID})
		if entry.Links != nil {
			for _, resource := range entry.Links.ProjectResources {
				key := store.RecordKey{Kind: "projects", ID: resource.ProjectID}
				if !slices.Contains(scope.Write, key) {
					scope.Write = append(scope.Write, key)
				}
			}
		}
		return scope, true
	}
	return store.MetadataRecordScope{}, false
}

func (s *server) documentTrashAuthorizationHeaders(r *http.Request, workspace string) ([]domain.TrashEntry, bool) {
	if documentRecordMode("FLOW_TRASH_RECORDS") != "on" {
		return nil, false
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[1] != "trash" || parts[3] != "restore" {
		return nil, false
	}
	entry, ok := s.store.TrashRecordHeader(workspace, parts[2])
	if !ok || entry.ResourceType != "document" {
		return nil, false
	}
	return []domain.TrashEntry{entry}, true
}

func trashRecordRestoreScope(ctx context.Context, id string) context.Context {
	ctx = trashRestoreScope(ctx, id)
	mode := documentRecordMode("FLOW_TRASH_RECORDS")
	if mode == "off" {
		return ctx
	}
	return store.WithDocumentRecordSelection(ctx, mode, func(data domain.Bootstrap, _ string, _ any) (store.MetadataRecordScope, bool) {
		return documentTrashRequirements(data, id, true)
	})
}
