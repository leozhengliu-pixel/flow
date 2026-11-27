package main

import (
	"net/http"

	"flow/api/internal/store"
)

func (s *server) queryWindowMetadataScaleMetadata(w http.ResponseWriter, r *http.Request) (store.ScaleSnapshot, bool) {
	if !acquireProjection(r) {
		if retry, refused := projectionRefused(r); refused {
			writeProjectionBusy(w, retry)
		}
		return store.ScaleSnapshot{}, false
	}
	snapshot, err := s.store.ReadScaleSnapshot(r.Context(), workspaceKey(r), authUser(r).ID, s.authDisabled)
	if err != nil {
		writeSyncError(w, err)
		return snapshot, false
	}
	if !s.authDisabled {
		filterBootstrapForAPIKey(&snapshot.Data, r)
		if err := s.filterPreferenceIssueTeams(r, &snapshot.Data); err != nil {
			writeSyncError(w, err)
			return snapshot, false
		}
	}
	return snapshot, true
}
