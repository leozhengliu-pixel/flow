package main

import (
	"net/http"

	"flow/api/internal/store"
)

func (s *server) scaleP19Metadata(w http.ResponseWriter, r *http.Request) (store.ScaleP19Snapshot, bool) {
	if !acquireProjection(r) {
		if retry, refused := projectionRefused(r); refused {
			writeProjectionBusy(w, retry)
		}
		return store.ScaleP19Snapshot{}, false
	}
	snapshot, err := s.store.ReadScaleP19Snapshot(r.Context(), workspaceKey(r), authUser(r).ID, s.authDisabled)
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
