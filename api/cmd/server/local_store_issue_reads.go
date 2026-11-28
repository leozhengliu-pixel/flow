package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const localStoreIssueKind = "issues"

func (s *server) localStoreIssueAccess(w http.ResponseWriter, r *http.Request) (store.EventAccess, bool) {
	if viewer := localStoreBrowserViewer(r); viewer == "" || viewer != r.Header.Get(localStoreHeader) {
		writeSyncError(w, store.ErrAuthForbidden)
		return store.EventAccess{}, false
	}
	if _, ready := s.syncV2Journal(); !ready || !s.localStoreAllowed(workspaceKey(r)) {
		http.NotFound(w, r)
		return store.EventAccess{}, false
	}
	access, err := s.resolveSyncAccess(r)
	if err != nil {
		writeSyncError(w, err)
		return store.EventAccess{}, false
	}
	values := r.URL.Query()
	if values.Get("workspaceId") != access.WorkspaceID || values.Has("authzEpoch") && values.Get("authzEpoch") != access.AuthzEpoch {
		writeSyncError(w, store.ErrAuthForbidden)
		return store.EventAccess{}, false
	}
	return access, true
}

func localStoreIssueRevision(access store.EventAccess) string {
	version := access.SourceVersion()
	return strconv.FormatInt(version.Metadata, 10) + ":" + strconv.FormatInt(version.Issues, 10)
}

func (s *server) validateLocalStoreIssueRead(r *http.Request, access store.EventAccess) error {
	err := s.syncChangesProjectionError(r, access, s.store.ValidateEventAccess(r.Context(), access))
	// A same-epoch write can invalidate the projection without revoking access.
	if err != nil && s.syncChangesRaced(r, access, err) {
		return store.ErrMetadataCursor
	}
	return err
}

func (s *server) readLocalStoreIssues(r *http.Request, access store.EventAccess, query store.IssueRecordQuery) (store.IssueRecordPage, error) {
	metadata, issueAccess, err := s.requestIssueQueryAccess(r)
	if err != nil {
		return store.IssueRecordPage{}, err
	}
	if metadata.Workspace.ID != access.WorkspaceID {
		return store.IssueRecordPage{}, store.ErrAuthForbidden
	}
	query.Workspace = metadata.Workspace.URLKey
	query.Access, query.AllowedTeamIDs = s.issueRecordAccess(r, metadata, issueAccess)
	query.Summary = true
	page, err := s.store.QueryIssueRecords(r.Context(), query)
	if err == nil {
		page.Items, err = s.projectIssueRecordReferences(r, metadata, query, page.Items)
	}
	return page, err
}

func (s *server) localStoreIssueRecords(ctx context.Context, workspace string, issues []domain.Issue) ([]store.MetadataRecord, error) {
	items := make([]store.MetadataRecord, 0, len(issues))
	for _, issue := range issues {
		key := store.RecordKey{Kind: localStoreIssueKind, ID: issue.ID}
		version, deleted, err := s.store.RecordVersion(ctx, workspace, key)
		if err != nil {
			return nil, err
		}
		if deleted {
			return nil, store.ErrMetadataCursor
		}
		if version == "" {
			// An issue created before the journal has no journal version yet.
			version = "0"
		}
		issue.Permissions = nil
		issue.ShareToken = ""
		issue.SuggestionsInputSketch = ""
		issue.Attachments = nil
		raw, err := json.Marshal(struct {
			domain.Issue
			Description string `json:"description,omitempty"`
		}{Issue: issue})
		if err != nil {
			return nil, err
		}
		items = append(items, store.MetadataRecord{Key: key, Version: version, Data: raw})
	}
	return items, nil
}

func (s *server) localStoreIssuePage(w http.ResponseWriter, r *http.Request) {
	access, ok := s.localStoreIssueAccess(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	q := r.URL.Query()
	if !q.Has("authzEpoch") {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	limit, err := syncPageNumber(q.Get("limit"), 50, 1, 100)
	if err != nil || q.Get("kind") != localStoreIssueKind || q.Get("filter") != "" || q.Get("order") != "" {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	budget, err := syncPageNumber(q.Get("maxBytes"), store.MetadataRecordPageMaxBytes, 8<<10, store.MetadataRecordPageMaxBytes)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	revision := localStoreIssueRevision(access)
	cursor := q.Get("cursor")
	if (cursor != "" || q.Has("revision")) && q.Get("revision") != revision {
		writeSyncError(w, store.ErrMetadataCursor)
		return
	}
	for {
		page, err := s.readLocalStoreIssues(r, access, store.IssueRecordQuery{Limit: limit, Cursor: cursor, Sort: "createdAt", Direction: "asc"})
		if err != nil {
			writeSyncError(w, err)
			return
		}
		items, err := s.localStoreIssueRecords(r.Context(), workspaceKey(r), page.Items)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		result := store.RecordPage{Items: items, Next: page.NextCursor, HasMore: page.HasMore, Revision: revision, AuthzEpoch: access.AuthzEpoch}
		raw, err := json.Marshal(result)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		if err = s.validateLocalStoreIssueRead(r, access); err != nil {
			writeSyncError(w, err)
			return
		}
		if len(raw)+1 <= budget || limit == 1 {
			s.writeSyncJSON(w, r, result, budget)
			return
		}
		limit = max(1, len(items)/2)
	}
}

func (s *server) localStoreIssueBatch(w http.ResponseWriter, r *http.Request, keys []store.RecordKey, projection string) {
	access, ok := s.localStoreIssueAccess(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if !r.URL.Query().Has("authzEpoch") || projection != "summary" || len(keys) == 0 {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	ids := make([]string, 0, len(keys))
	for _, key := range keys {
		if key.Kind != localStoreIssueKind || key.ID == "" || len(key.ID) > 191 {
			writeSyncError(w, store.ErrMetadataRecordQuery)
			return
		}
		ids = append(ids, key.ID)
	}
	page, err := s.readLocalStoreIssues(r, access, store.IssueRecordQuery{IssueIDs: ids, Limit: len(ids), RestrictToIssueIDs: true})
	if err != nil || page.HasMore {
		if err == nil {
			err = store.ErrMetadataPageBudget
		}
		writeSyncError(w, err)
		return
	}
	items, err := s.localStoreIssueRecords(r.Context(), workspaceKey(r), page.Items)
	if err == nil {
		err = s.validateLocalStoreIssueRead(r, access)
	}
	if err != nil {
		writeSyncError(w, err)
		return
	}
	byID := make(map[string]store.MetadataRecord, len(items))
	for _, item := range items {
		byID[item.Key.ID] = item
	}
	result := store.RecordBatch{Items: make([]store.MetadataRecord, 0, len(keys)), AuthzEpoch: access.AuthzEpoch}
	for _, key := range keys {
		if item, present := byID[key.ID]; present {
			result.Items = append(result.Items, item)
		} else {
			result.Items = append(result.Items, store.MetadataRecord{Key: key, Missing: true})
		}
	}
	s.writeSyncJSON(w, r, result, store.MetadataRecordPageMaxBytes)
}
