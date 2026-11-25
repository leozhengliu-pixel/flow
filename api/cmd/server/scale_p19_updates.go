package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP19UpdateRecord struct {
	id    string
	value any
}
type scaleP19UpdatePage struct {
	Schema     int               `json:"schema"`
	Items      []json.RawMessage `json:"items"`
	Details    map[string]bool   `json:"details,omitempty"`
	Total      int               `json:"total"`
	Offset     int               `json:"offset"`
	NextCursor string            `json:"nextCursor,omitempty"`
	HasMore    bool              `json:"hasMore"`
	Revision   string            `json:"revision"`
	AuthzEpoch string            `json:"authzEpoch"`
}

// Negotiation is explicit: without window=1 the existing handler returns its
// original array, including its exact field redaction and ordering. Oversized
// values use authenticated 32 KiB fragments, rather than truncated JSON fields.
func (s *server) scaleP19UpdatePage(w http.ResponseWriter, r *http.Request, kind string, values any) {
	access, ok := s.syncAccess(w, r)
	if !ok {
		return
	}
	head, err := s.store.WorkspaceChangeHead(r.Context(), workspaceKey(r))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	revision := strconv.FormatInt(head, 10)
	records := []scaleP19UpdateRecord{}
	switch values := values.(type) {
	case []domain.ProjectUpdate:
		for _, v := range values {
			records = append(records, scaleP19UpdateRecord{v.ID, v})
		}
	case []domain.InitiativeUpdate:
		for _, v := range values {
			records = append(records, scaleP19UpdateRecord{v.ID, v})
		}
	}
	binding, err := scaleP19UpdateBinding(kind, r.PathValue("id"), revision, records)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	if id := r.URL.Query().Get("update"); id != "" {
		var raw []byte
		for _, record := range records {
			if record.id == id {
				raw, err = json.Marshal(record.value)
				break
			}
		}
		if len(raw) == 0 || err != nil {
			writeError(w, http.StatusNotFound, "update not found")
			return
		}
		binding += fmt.Sprintf(":%s:%x", id, sha256.Sum256(raw))
		position, err := s.store.OpenScaleP19Cursor(r.Context(), access, kind, binding, r.URL.Query().Get("cursor"))
		if err != nil {
			writeSyncError(w, err)
			return
		}
		offset := 0
		if position != "" {
			offset, err = strconv.Atoi(position)
			if err != nil || offset < 0 || offset >= len(raw) {
				writeSyncError(w, store.ErrMetadataCursor)
				return
			}
		}
		end := min(offset+32<<10, len(raw))
		next := ""
		if end < len(raw) {
			next, err = s.store.SealScaleP19Cursor(r.Context(), access, kind, binding, strconv.Itoa(end))
			if err != nil {
				writeSyncError(w, err)
				return
			}
		}
		if err := s.store.ValidateAccess(r.Context(), access); err != nil {
			writeSyncError(w, err)
			return
		}
		writeSyncJSON(w, map[string]any{"schema": 1, "revision": revision, "authzEpoch": access.AuthzEpoch, "encoding": "base64-json", "data": base64.StdEncoding.EncodeToString(raw[offset:end]), "offset": offset, "hasMore": end < len(raw), "nextCursor": next}, store.MetadataRecordPageMaxBytes)
		return
	}
	limit, err := syncPageNumber(r.URL.Query().Get("limit"), 20, 1, 100)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	position, err := s.store.OpenScaleP19Cursor(r.Context(), access, kind, binding, r.URL.Query().Get("cursor"))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	offset := 0
	if position != "" {
		offset, err = strconv.Atoi(position)
		if err != nil || offset < 0 {
			writeSyncError(w, store.ErrMetadataCursor)
			return
		}
	}
	page := scaleP19UpdatePage{Schema: 1, Items: []json.RawMessage{}, Details: map[string]bool{}, Total: len(records), Offset: offset, Revision: revision, AuthzEpoch: access.AuthzEpoch}
	bytes := 2048
	for _, record := range records[min(offset, len(records)):min(offset+limit, len(records))] {
		raw, err := json.Marshal(record.value)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		if len(raw) > store.MetadataRecordPageMaxBytes-4096 {
			page.Details[record.id] = true
			raw, _ = json.Marshal(map[string]string{"id": record.id})
		}
		if bytes+len(raw) > store.MetadataRecordPageMaxBytes-2048 {
			break
		}
		page.Items = append(page.Items, raw)
		bytes += len(raw) + 1
	}
	page.HasMore = offset+len(page.Items) < len(records)
	if page.HasMore {
		page.NextCursor, err = s.store.SealScaleP19Cursor(r.Context(), access, kind, binding, strconv.Itoa(offset+len(page.Items)))
		if err != nil {
			writeSyncError(w, err)
			return
		}
	}
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return
	}
	writeSyncJSON(w, page, store.MetadataRecordPageMaxBytes)
}

func scaleP19UpdateBinding(kind, id, revision string, records []scaleP19UpdateRecord) (string, error) {
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	// The event head can advance between PulseFeed and this handler, or lag an
	// unsequenced write. Bind the actual projected values as well as the head.
	// Encode individual records so hashing does not buffer the entire history.
	for _, record := range records {
		if err := encoder.Encode(record.value); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%s:%s:%s:%x", kind, id, revision, digest.Sum(nil)), nil
}
