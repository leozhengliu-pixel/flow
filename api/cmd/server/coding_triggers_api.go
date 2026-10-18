package main

import "net/http"

// Session triggers from issues and pull/merge requests (docs/coding-sessions-design.md §15).
//
// Owner: track L (triggers & PR follow-ups). Handlers are 501 stubs until the track implements
// them; keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingTriggerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/issues/{id}/coding-session", s.startIssueCodingSession)
	mux.HandleFunc("POST /api/reviews/{id}/coding-session", s.startReviewCodingSession)
}

// startIssueCodingSession — TODO(track L).
//
//	POST /api/issues/{id}/coding-session: Starts (or continues) a session for an issue; used by delegate and comment triggers in the UI.
func (s *server) startIssueCodingSession(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// startReviewCodingSession — TODO(track L).
//
//	POST /api/reviews/{id}/coding-session: From a PR/MR: {kind: fix|followUp|rebase|lint|test, prompt}; links the PR to the session.
func (s *server) startReviewCodingSession(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}
