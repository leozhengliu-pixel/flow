package main

import "net/http"

// Session artifacts (browser screenshots).
//
// Owner: track I (browser testing & artifacts). Handlers are 501 stubs until the track implements
// them; keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingArtifactRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/coding-runtime/artifacts", s.codingRuntimeUploadArtifact)
	mux.HandleFunc("GET /api/coding/sessions/{id}/artifacts", s.listCodingSessionArtifacts)
}

// codingRuntimeUploadArtifact — TODO(track I).
//
//	POST /internal/coding-runtime/artifacts: Runner uploads a screenshot (multipart: file, kind, label, turnId).
func (s *server) codingRuntimeUploadArtifact(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// listCodingSessionArtifacts — TODO(track I).
//
//	GET /api/coding/sessions/{id}/artifacts: Artifacts of a session with signed URLs.
func (s *server) listCodingSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}
