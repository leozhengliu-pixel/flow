package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) registerScaleP20Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/resource-windows/members", s.scaleP20Members)
	mux.HandleFunc("GET /api/resource-windows/trash", s.scaleP20Trash)
	mux.HandleFunc("GET /api/documents/{id}/history/{revisionId}", s.scaleP20Revision)
}

func scaleP20SyncRequest(r *http.Request) bool {
	return r.URL.Query().Get("surface") == "resource-window" && syncReadRequest(r)
}

func scaleP20ReadRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.URL.Path == "/api/resource-windows/members" || r.URL.Path == "/api/resource-windows/trash" {
		return true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) >= 3 && parts[0] == "api" && parts[1] == "documents" &&
		(len(parts) == 3 && r.URL.Query().Get("history") == "summary" || len(parts) == 4 && parts[3] == "history" && r.URL.Query().Get("projection") == "summary" || len(parts) == 5 && parts[3] == "history")
}

func (s *server) scaleP20Metadata(w http.ResponseWriter, r *http.Request, kind string) (domain.Bootstrap, string, store.AccessContext, bool) {
	access, ok := s.syncAccess(w, r)
	if !ok {
		return domain.Bootstrap{}, "", access, false
	}
	userID := authUser(r).ID
	if s.authDisabled {
		userID = ""
	}
	data, revision, err := s.store.ResourceWindowMetadata(r.Context(), workspaceKey(r), userID, documentRecordCredential(r), kind)
	if err != nil {
		scaleP20ReadError(w, err)
		return domain.Bootstrap{}, "", access, false
	}
	filterBootstrapForAPIKey(&data, r)
	if s.authDisabled {
		materializeDevelopmentMembers(&data)
	}
	sanitizeBootstrap(&data)
	s.applyCalendarAway(r.Context(), &data)
	if kind == "trash" {
		for index := range data.Trash {
			var payload map[string]json.RawMessage
			if json.Unmarshal(data.Trash[index].Payload, &payload) == nil && payload != nil {
				payload["deletedAt"], _ = json.Marshal(data.Trash[index].DeletedAt)
				data.Trash[index].Payload, _ = json.Marshal(payload)
			}
		}
	}
	return data, revision, access, true
}

func scaleP20ReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrMetadataPageBudget) {
		writeError(w, http.StatusNotImplemented, "Resource window exceeds the bounded adapter; use the legacy read endpoint")
		return
	}
	writeSyncError(w, err)
}

func (s *server) scaleP20Manifest(w http.ResponseWriter, r *http.Request) bool {
	if !scaleP20SyncRequest(r) {
		return false
	}
	if !syncFlagEnabled("FLOW_INDEXED_READS") || !store.RecordHydrationEnabled() {
		writeError(w, http.StatusNotFound, "not found")
		return true
	}
	// This conservative adapter fixes H0 before reading any directory. Until
	// every collection has journal deltas, a changed head requests a new set
	// of bounded windows instead of claiming an incomplete transaction applied.
	head, err := s.store.WorkspaceChangeHead(r.Context(), workspaceKey(r))
	if err != nil {
		writeSyncError(w, err)
		return true
	}
	data, revision, access, ok := s.scaleP20Metadata(w, r, "core")
	if !ok {
		return true
	}
	raw, _ := json.Marshal(data)
	var core map[string]json.RawMessage
	_ = json.Unmarshal(raw, &core)
	for field, value := range core {
		if string(value) == "null" {
			delete(core, field)
		}
	}
	collections := map[string]any{}
	for _, kind := range []string{"documents", "projects", "initiatives", "cycles", "users", "members", "teamMembers", "departedMembers", "invitations", "trash"} {
		delete(core, kind)
		collections[kind] = map[string]any{"revision": revision, "complete": false}
	}
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return true
	}
	result := map[string]any{"schema": 2, "workspaceId": data.Workspace.ID, "viewerId": data.Viewer.ID, "authzEpoch": access.AuthzEpoch, "head": strconv.FormatInt(head, 10), "capabilities": []string{"sync-v2", "resource-windows"}, "core": core, "collections": collections}
	encoded, _ := json.Marshal(result)
	if len(encoded)+1 > store.MetadataRecordPageMaxBytes {
		scaleP20ReadError(w, store.ErrMetadataPageBudget)
		return true
	}
	writeSyncJSON(w, result, store.MetadataRecordPageMaxBytes)
	return true
}

func scaleP20Nodes(data domain.Bootstrap, kind string) ([]any, error) {
	value := reflect.ValueOf(data)
	typ := value.Type()
	for i := range value.NumField() {
		if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] != kind || value.Field(i).Kind() != reflect.Slice {
			continue
		}
		items := value.Field(i)
		result := make([]any, items.Len())
		for j := range items.Len() {
			result[j] = items.Index(j).Interface()
		}
		return result, nil
	}
	return nil, store.ErrMetadataRecordQuery
}

func (s *server) scaleP20Page(w http.ResponseWriter, r *http.Request, kind string) {
	data, revision, access, ok := s.scaleP20Metadata(w, r, kind)
	if !ok {
		return
	}
	q := store.PageRequest{Kind: kind, Cursor: r.URL.Query().Get("cursor"), Filter: r.URL.Query().Get("q"), Limit: 100, MaxBytes: store.MetadataRecordPageMaxBytes}
	var err error
	q.Limit, err = syncPageNumber(r.URL.Query().Get("limit"), 100, 1, 100)
	if err != nil || len(q.Filter) > 4096 {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	q.MaxBytes, err = syncPageNumber(r.URL.Query().Get("maxBytes"), store.MetadataRecordPageMaxBytes, 8<<10, store.MetadataRecordPageMaxBytes)
	if err != nil || r.URL.Query().Get("filter") != "" || r.URL.Query().Get("order") != "" {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	start, err := store.ResourceWindowCursor(access, q, revision)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	nodes, err := scaleP20Nodes(data, kind)
	if err != nil || start > len(nodes) {
		writeSyncError(w, store.ErrMetadataCursor)
		return
	}
	page := store.RecordPage{Items: []store.MetadataRecord{}, Revision: revision, AuthzEpoch: access.AuthzEpoch}
	remaining, index := q.MaxBytes-4096, start
	for index < len(nodes) {
		node := nodes[index]
		raw, _ := json.Marshal(node)
		var identity struct {
			ID     string      `json:"id"`
			UserID string      `json:"userId"`
			TeamID string      `json:"teamId"`
			User   domain.User `json:"user"`
		}
		_ = json.Unmarshal(raw, &identity)
		id := identity.ID
		if kind == "members" || kind == "departedMembers" {
			id = identity.User.ID
		}
		if kind == "teamMembers" {
			id = identity.TeamID + ":" + identity.UserID
		}
		if q.Filter != "" && (kind == "users" || kind == "members") {
			user := identity.User
			if kind == "users" {
				_ = json.Unmarshal(raw, &user)
			}
			if !strings.Contains(strings.ToLower(user.Name+" "+user.DisplayName+" "+user.Username+" "+user.Email), strings.ToLower(strings.TrimSpace(q.Filter))) {
				index++
				continue
			}
		}
		item := store.MetadataRecord{Key: store.RecordKey{Kind: kind, ID: id}, Version: revision, Data: raw}
		encoded, _ := json.Marshal(item)
		if len(page.Items) == q.Limit || len(encoded)+1 > remaining && len(page.Items) > 0 {
			break
		}
		if len(encoded)+1 > remaining {
			scaleP20ReadError(w, store.ErrMetadataPageBudget)
			return
		}
		page.Items = append(page.Items, item)
		remaining -= len(encoded) + 1
		index++
	}
	page.HasMore = index < len(nodes)
	if page.HasMore {
		page.Next, err = store.SealResourceWindowCursor(access, q, revision, index)
		if err != nil {
			writeSyncError(w, err)
			return
		}
	}
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return
	}
	writeSyncJSON(w, page, q.MaxBytes)
}

func (s *server) scaleP20Records(w http.ResponseWriter, r *http.Request) bool {
	if !scaleP20SyncRequest(r) {
		return false
	}
	if !syncFlagEnabled("FLOW_INDEXED_READS") {
		writeError(w, http.StatusNotFound, "not found")
		return true
	}
	s.scaleP20Page(w, r, r.URL.Query().Get("kind"))
	return true
}

func (s *server) scaleP20Members(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("collection")
	if kind == "" {
		kind = "members"
	}
	if !slices.Contains([]string{"users", "members", "teamMembers", "departedMembers", "invitations"}, kind) {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	s.scaleP20Page(w, r, kind)
}

func (s *server) scaleP20DocumentMetadata(w http.ResponseWriter, r *http.Request, ref string, trashOnly bool) (domain.Bootstrap, domain.Document, *domain.TrashEntry, bool) {
	userID := authUser(r).ID
	if s.authDisabled {
		userID = ""
	}
	data, err := s.store.SelectedDocumentMetadata(r.Context(), workspaceKey(r), userID, documentRecordCredential(r), func(source domain.Bootstrap) (store.MetadataRecordScope, bool) {
		scope := store.MetadataRecordScope{Fields: documentRecordReadFields}
		if !trashOnly {
			if doc, err := documentByID(&source, ref); err == nil {
				scope = documentRecordRequirements(source, *doc, nil)
				scope.Fields = documentRecordReadFields
			}
		}
		exact := slices.ContainsFunc(source.Trash, func(entry domain.TrashEntry) bool {
			return entry.ResourceType == "document" && (entry.ResourceID == ref || entry.ID == ref)
		})
		for _, entry := range source.Trash {
			if entry.ResourceType != "document" {
				continue
			}
			match := entry.ResourceID == ref || entry.ID == ref
			if !match && !exact {
				var header struct {
					ID, SlugID      string
					PreviousSlugIDs []string
				}
				if json.Unmarshal(entry.Payload, &header) == nil {
					match = header.ID == ref || header.SlugID == ref || slices.Contains(header.PreviousSlugIDs, ref)
				}
			}
			if match {
				scope.Read = append(scope.Read, store.RecordKey{Kind: "trash", ID: entry.ID})
			}
		}
		return scope, true
	})
	if err != nil {
		writeSyncError(w, err)
		return data, domain.Document{}, nil, false
	}
	filterBootstrapForAPIKey(&data, r)
	if doc, err := documentByID(&data, ref); !trashOnly && err == nil && documentRole(s, data, *doc) != "none" {
		return data, *doc, nil, true
	}
	for _, entry := range data.Trash {
		var doc domain.Document
		if json.Unmarshal(entry.Payload, &doc) == nil && documentRole(s, data, doc) != "none" {
			at := entry.DeletedAt
			doc.DeletedAt = &at
			return data, doc, &entry, true
		}
	}
	writeError(w, http.StatusNotFound, "document not found")
	return data, domain.Document{}, nil, false
}

func scaleP20RevisionHeaders(doc domain.Document) []map[string]any {
	result := make([]map[string]any, len(doc.Revisions))
	for index, revision := range doc.Revisions {
		current := index == 0 && revision.Title == doc.Title && revision.Content == doc.Content && len(revision.Offloaded) == 0
		revision.Content, revision.ContentState, revision.ContentData, revision.Offloaded = "", "", nil, nil
		raw, _ := json.Marshal(revision)
		var header map[string]any
		_ = json.Unmarshal(raw, &header)
		delete(header, "content")
		delete(header, "contentState")
		delete(header, "contentData")
		header["_bodyOmitted"], header["_current"] = true, current
		result[index] = header
	}
	return result
}

func scaleP20DocumentJSON(doc domain.Document) map[string]any {
	headers := scaleP20RevisionHeaders(doc)
	doc.Revisions = nil
	raw, _ := json.Marshal(doc)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	result["revisions"] = headers
	return result
}

func (s *server) scaleP20DocumentResult(r *http.Request, doc domain.Document) any {
	if r.URL.Query().Get("history") == "summary" {
		return scaleP20DocumentJSON(doc)
	}
	return s.withDocumentVersions(r, doc)
}

func (s *server) scaleP20DocumentRead(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("history") != "summary" {
		return false
	}
	access, allowed := s.syncAccess(w, r)
	if !allowed {
		return true
	}
	_, doc, _, ok := s.scaleP20DocumentMetadata(w, r, r.PathValue("id"), false)
	if ok {
		if err := s.store.ValidateAccess(r.Context(), access); err != nil {
			writeSyncError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, scaleP20DocumentJSON(doc))
	}
	return true
}

func (s *server) scaleP20Trash(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("resourceId")
	if ref == "" {
		s.scaleP20Page(w, r, "trash")
		return
	}
	access, allowed := s.syncAccess(w, r)
	if !allowed {
		return
	}
	_, doc, entry, ok := s.scaleP20DocumentMetadata(w, r, ref, true)
	if !ok {
		return
	}
	raw, _ := json.Marshal(scaleP20DocumentJSON(doc))
	entry.Payload, entry.Links = raw, nil
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *server) scaleP20History(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Query().Get("projection") != "summary" {
		return false
	}
	access, allowed := s.syncAccess(w, r)
	if !allowed {
		return true
	}
	_, doc, _, ok := s.scaleP20DocumentMetadata(w, r, r.PathValue("id"), false)
	if ok {
		if err := s.store.ValidateAccess(r.Context(), access); err != nil {
			writeSyncError(w, err)
			return true
		}
		writePage(w, r, scaleP20RevisionHeaders(doc))
	}
	return true
}

func (s *server) scaleP20Revision(w http.ResponseWriter, r *http.Request) {
	access, allowed := s.syncAccess(w, r)
	if !allowed {
		return
	}
	data, doc, _, ok := s.scaleP20DocumentMetadata(w, r, r.PathValue("id"), false)
	if !ok {
		return
	}
	index := slices.IndexFunc(doc.Revisions, func(revision domain.DocumentRevision) bool { return revision.ID == r.PathValue("revisionId") })
	if index < 0 {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	revisions := []domain.DocumentRevision{doc.Revisions[index]}
	if err := s.store.LoadDocumentRevisionBodies(r.Context(), data.Workspace.URLKey, revisions); err != nil {
		writeError(w, http.StatusServiceUnavailable, "document history is temporarily unavailable")
		return
	}
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, revisions[0])
}

func (s *server) scaleP20Batch(w http.ResponseWriter, r *http.Request, keys []store.RecordKey, projection string) bool {
	if !scaleP20SyncRequest(r) {
		return false
	}
	if len(keys) > 50 || projection != "detail" {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return true
	}
	access, ok := s.syncAccess(w, r)
	if !ok {
		return true
	}
	batch := store.RecordBatch{Items: []store.MetadataRecord{}, AuthzEpoch: access.AuthzEpoch}
	revision, err := s.store.ResourceWindowRevision(r.Context(), workspaceKey(r))
	if err != nil {
		writeSyncError(w, err)
		return true
	}
	remaining := store.MetadataRecordPageMaxBytes - 4096
	for _, key := range keys {
		if key.Kind != "documents" {
			writeSyncError(w, store.ErrMetadataRecordQuery)
			return true
		}
		// Reuse the selected P17 authorization, but keep missing keys anonymous.
		probe := *r
		probe.SetPathValue("id", key.ID)
		recorder := newScaleP20ReadRecorder()
		_, doc, _, found := s.scaleP20DocumentMetadata(recorder, &probe, key.ID, false)
		item := store.MetadataRecord{Key: key, Missing: !found}
		if !found && recorder.code != http.StatusNotFound {
			writeSyncError(w, errors.New("record read unavailable"))
			return true
		}
		if found {
			if doc.DeletedAt != nil {
				item.Missing = true
			} else {
				item.Version = revision
				item.Data, _ = json.Marshal(scaleP20DocumentJSON(doc))
				encoded, _ := json.Marshal(item)
				if len(encoded)+1 > remaining {
					item.Data, item.Omitted, item.Detail = nil, true, &item.Key
				}
			}
		}
		encoded, _ := json.Marshal(item)
		remaining -= len(encoded) + 1
		batch.Items = append(batch.Items, item)
	}
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return true
	}
	if current, err := s.store.ResourceWindowRevision(r.Context(), workspaceKey(r)); err != nil || current != revision {
		writeSyncError(w, store.ErrMetadataCursor)
		return true
	}
	writeSyncJSON(w, batch, store.MetadataRecordPageMaxBytes)
	return true
}

type scaleP20ReadRecorder struct {
	header http.Header
	code   int
}

func newScaleP20ReadRecorder() *scaleP20ReadRecorder {
	return &scaleP20ReadRecorder{header: http.Header{}}
}
func (w *scaleP20ReadRecorder) Header() http.Header            { return w.header }
func (w *scaleP20ReadRecorder) WriteHeader(code int)           { w.code = code }
func (w *scaleP20ReadRecorder) Write(data []byte) (int, error) { return len(data), nil }

func (s *server) scaleP20Changes(w http.ResponseWriter, r *http.Request) bool {
	if !scaleP20SyncRequest(r) {
		return false
	}
	if !syncFlagEnabled("FLOW_INDEXED_READS") {
		writeError(w, http.StatusNotFound, "not found")
		return true
	}
	access, ok := s.syncAccess(w, r)
	if !ok {
		return true
	}
	head, err := s.store.WorkspaceChangeHead(r.Context(), workspaceKey(r))
	if err != nil {
		writeSyncError(w, err)
		return true
	}
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil || after < 0 {
		writeSyncError(w, store.ErrMetadataCursor)
		return true
	}
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return true
	}
	writeSyncJSON(w, syncV2ChangesResponse{Next: strconv.FormatInt(head, 10), Head: strconv.FormatInt(head, 10), AuthzEpoch: access.AuthzEpoch, Transactions: []syncV2Transaction{}, ResyncRequired: after != head}, store.MetadataRecordPageMaxBytes)
	return true
}
