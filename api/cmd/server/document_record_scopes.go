package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func documentRecordMode(name string) string {
	if !store.RecordHydrationEnabled() {
		return "off"
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "on", "1", "true":
		return "on"
	case "shadow":
		return "shadow"
	}
	return "off"
}

var documentRecordPolicyFields = []string{"users", "teams", "teamSettings", "teamParents", "members", "teamMembers", "states", "labels", "workspaceSettings", "userSettings", "favorites", "favoriteFolders", "subscriptions", "drafts", "threadSubscriptions", "notificationPreferences", "pushSubscriptions"}
var documentRecordReadFields = []string{"teams", "teamSettings", "teamParents", "workspaceSettings", "favorites"}

func documentRecordScope(ctx context.Context, id string) context.Context {
	mode := documentRecordMode("FLOW_DOCUMENT_RECORDS")
	if mode == "off" {
		return ctx
	}
	return store.WithDocumentRecordSelection(ctx, mode, func(data domain.Bootstrap, event string, payload any) (store.MetadataRecordScope, bool) {
		// Deletion creates a trash record; the trash family is independently
		// gated so either family can return to its canonical implementation.
		if event == "document.deleted" && documentRecordMode("FLOW_TRASH_RECORDS") == "off" {
			return store.MetadataRecordScope{}, false
		}
		if document, err := documentByID(&data, id); err == nil {
			scope := documentRecordRequirements(data, *document, payload)
			return scope, true
		}
		if event == "document.restored" && documentRecordMode("FLOW_TRASH_RECORDS") != "off" {
			return documentTrashRequirements(data, id, false)
		}
		return store.MetadataRecordScope{}, false
	})
}

func documentRecordRequirements(data domain.Bootstrap, document domain.Document, payload any) store.MetadataRecordScope {
	scope := store.MetadataRecordScope{Fields: slices.Clone(documentRecordPolicyFields), Write: []store.RecordKey{{Kind: "documents", ID: document.ID}}}
	projectIDs := slices.Clone(document.ProjectIDs)
	if input, ok := payload.(documentInput); ok && input.ProjectIDs != nil {
		projectIDs = append(projectIDs, normalizedStrings(*input.ProjectIDs)...)
	}
	for _, project := range data.Projects {
		if slices.Contains(projectIDs, project.ID) || slices.ContainsFunc(project.Resources, func(item domain.ProjectResource) bool { return item.ID == document.ID }) {
			scope.Write = append(scope.Write, store.RecordKey{Kind: "projects", ID: project.ID})
		}
	}
	return scope
}

func documentRecordRequest(r *http.Request) bool {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" || parts[1] != "documents" {
		return false
	}
	if len(parts) == 2 {
		return (r.Method == http.MethodGet || r.Method == http.MethodHead) && !connectionRequested(r)
	}
	if len(parts) == 3 {
		return true
	}
	switch parts[3] {
	case "permissions", "subscribers", "restore":
		return len(parts) <= 5
	case "revisions":
		return len(parts) == 5 && parts[4] == "import"
	case "comments":
		return len(parts) <= 5 || len(parts) == 6 && (parts[5] == "reactions" || parts[5] == "subscription")
	}
	return false
}

func documentReadRequirements(data domain.Bootstrap, r *http.Request) (store.MetadataRecordScope, bool) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	scope := store.MetadataRecordScope{Fields: slices.Clone(documentRecordReadFields)}
	if len(parts) == 2 {
		// List authorization only needs policy. The handler reads the directory
		// and emits complete legacy records one at a time.
		return scope, true
	}
	id := parts[2]
	if document, err := documentByID(&data, id); err == nil {
		scope = documentRecordRequirements(data, *document, nil)
		if r.Method == http.MethodPatch {
			var input documentInput
			if peekRequestJSON(r, &input) {
				scope = documentRecordRequirements(data, *document, input)
			}
		}
		scope.Fields = slices.Clone(documentRecordReadFields)
		return scope, true
	}
	// Exact trash ids avoid decoding unrelated restore payloads. Legacy trash
	// slugs and old rows without a resource id keep the canonical resolver.
	for _, entry := range data.Trash {
		if entry.ResourceType == "document" && entry.ResourceID == id {
			scope.Read = append(scope.Read, store.RecordKey{Kind: "trash", ID: entry.ID})
			return scope, true
		}
	}
	return scope, false
}

// Keep the established authorization checks and API-key redaction. Only their
// input changes; an unready canonical snapshot uses the old projection.
func (s *server) documentAuthorizationMetadata(r *http.Request, workspace, userID string) (domain.Bootstrap, error, bool) {
	mode := documentRecordMode("FLOW_DOCUMENT_RECORDS")
	if mode == "off" || !documentRecordRequest(r) {
		return domain.Bootstrap{}, nil, false
	}
	var data domain.Bootstrap
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		data, err = s.store.SelectedDocumentMetadata(r.Context(), workspace, userID, documentRecordCredential(r), func(data domain.Bootstrap) (store.MetadataRecordScope, bool) {
			return documentReadRequirements(data, r)
		})
		if !errors.Is(err, store.ErrResourceAccessChanged) || !errors.Is(err, store.ErrAuthForbidden) {
			break
		}
	}
	if data.Comments == nil {
		data.Comments = map[string][]domain.Comment{}
	}
	if errors.Is(err, store.ErrResourceAccessChanged) && !errors.Is(err, store.ErrAuthForbidden) {
		return domain.Bootstrap{}, nil, false
	}
	if mode == "shadow" {
		legacy, legacyErr := s.store.PagedWorkspaceMetadata(r.Context(), workspace, userID)
		if err != nil && legacyErr == nil {
			slog.Warn("document record read shadow difference", "stage", "read")
		} else if err == nil && legacyErr == nil && len(strings.Split(strings.Trim(r.URL.Path, "/"), "/")) > 2 {
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			want := []domain.Document{}
			if document, lookupErr := documentByID(&legacy, parts[2]); lookupErr == nil {
				want = append(want, *document)
			}
			a, _ := json.Marshal(want)
			b, _ := json.Marshal(data.Documents)
			if string(a) != string(b) {
				slog.Warn("document record read shadow difference", "stage", "document")
			}
		}
		return legacy, legacyErr, true
	}
	return data, err, true
}

func documentRecordCredential(r *http.Request) *domain.APIKey {
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		return &key
	}
	return nil
}

func (s *server) documentWorkspaceData(r *http.Request) domain.Bootstrap {
	if documentRecordMode("FLOW_DOCUMENT_RECORDS") != "on" {
		return s.workspaceData(r)
	}
	userID := authUser(r).ID
	if s.authDisabled {
		userID = ""
	}
	if data, ok := takePagedMetadata(r, workspaceKey(r), userID); ok {
		filterBootstrapForAPIKey(&data, r)
		return data
	}
	if data, err, handled := s.documentAuthorizationMetadata(r, workspaceKey(r), userID); handled {
		if err != nil {
			return domain.Bootstrap{}
		}
		filterBootstrapForAPIKey(&data, r)
		return data
	}
	return s.workspaceData(r)
}
