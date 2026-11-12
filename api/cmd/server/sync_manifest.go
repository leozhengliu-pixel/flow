package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type syncManifestCollection struct {
	Ready    bool   `json:"ready"`
	Revision string `json:"revision"`
}

type syncManifestResponse struct {
	Schema       int                               `json:"schema"`
	WorkspaceID  string                            `json:"workspaceId"`
	ViewerID     string                            `json:"viewerId"`
	AuthzEpoch   string                            `json:"authzEpoch"`
	Head         string                            `json:"head"`
	Capabilities []string                          `json:"capabilities"`
	Core         store.RecordReadCore              `json:"core"`
	Collections  map[string]syncManifestCollection `json:"collections"`
}

func syncFlagEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "on", "1", "true":
		return true
	default:
		return false
	}
}

func syncReadRequest(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.URL.Path == "/api/sync/manifest" || r.URL.Path == "/api/sync/records" || r.URL.Path == "/api/sync/changes") ||
		r.Method == http.MethodPost && r.URL.Path == "/api/sync/batch"
}

type notificationStateReader interface {
	notificationState(http.ResponseWriter, *http.Request)
}

func (s *server) notificationStateReadRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead || r.URL.Path != "/api/notifications/state" {
		return false
	}
	_, ok := any(s).(notificationStateReader)
	return ok
}

func (s *server) registerSyncRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sync/manifest", s.syncManifest)
	mux.HandleFunc("GET /api/sync/records", s.syncRecords)
	mux.HandleFunc("POST /api/sync/batch", s.syncBatch)
	// P14 supplies recipient state when its independently reviewed package lands.
	if handler, ok := any(s).(notificationStateReader); ok {
		mux.HandleFunc("GET /api/notifications/state", handler.notificationState)
	}
}

func (s *server) syncAccess(w http.ResponseWriter, r *http.Request) (store.AccessContext, bool) {
	var key *domain.APIKey
	if credential, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		key = &credential
	}
	access, err := s.store.ResolveAccess(r.Context(), workspaceKey(r), authUser(r).ID, key)
	if err != nil {
		writeSyncError(w, err)
		return store.AccessContext{}, false
	}
	return access, true
}

func (s *server) syncManifest(w http.ResponseWriter, r *http.Request) {
	if !syncFlagEnabled("FLOW_SYNC_MANIFEST") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// H0 must precede every snapshot/page read. Never replace it with a later
	// head: callers must replay all changes committed while initialization ran.
	head, err := s.store.WorkspaceChangeHead(r.Context(), workspaceKey(r))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	access, ok := s.syncAccess(w, r)
	if !ok {
		return
	}
	core, err := s.store.ReadRecordCore(r.Context(), access)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	result := syncManifestResponse{
		Schema: 2, WorkspaceID: access.WorkspaceID, ViewerID: access.PrincipalID,
		AuthzEpoch: access.AuthzEpoch, Head: strconv.FormatInt(head, 10),
		Capabilities: []string{}, Core: core, Collections: map[string]syncManifestCollection{},
	}
	for _, kind := range []string{"projects", "initiatives"} {
		status, err := s.store.ReadRecordStatus(r.Context(), access, kind)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		ready := status.Ready && syncFlagEnabled("FLOW_INDEXED_READS")
		result.Collections[kind] = syncManifestCollection{Ready: ready, Revision: status.Revision}
	}
	// P09/P12/P15 and the remaining core surfaces must be integrated before
	// advertising sync-v2. Its absence makes new clients use legacy bootstrap.
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return
	}
	writeSyncJSON(w, result, store.MetadataRecordPageMaxBytes)
}

func writeSyncJSON(w http.ResponseWriter, value any, budget int) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw)+1 > budget {
		writeError(w, http.StatusServiceUnavailable, "Sync response exceeds the page budget")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(raw, '\n'))
}

func writeSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrAuthForbidden):
		writeError(w, http.StatusForbidden, "Sync access changed; reload the manifest")
	case errors.Is(err, store.ErrMetadataRecordQuery), errors.Is(err, store.ErrMetadataPageBudget):
		writeError(w, http.StatusBadRequest, "invalid sync query")
	case errors.Is(err, store.ErrMetadataCursor):
		writeError(w, http.StatusConflict, "Sync cursor changed; reload the manifest")
	default:
		writeError(w, http.StatusServiceUnavailable, "Sync records are not ready; use the legacy read endpoint")
	}
}
