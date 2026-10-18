package main

import "net/http"

// Session, settings, models, usage and status routes for the web app (docs/coding-sessions-design.md §7.1).
//
// Owner: track B (session core). Handlers are 501 stubs until the track implements
// them; keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/coding/status", s.getCodingStatus)
	mux.HandleFunc("GET /api/coding/settings", s.getCodingSettings)
	mux.HandleFunc("PATCH /api/coding/settings", s.updateCodingSettings)
	mux.HandleFunc("GET /api/coding/models", s.listCodingModels)
	mux.HandleFunc("GET /api/coding/usage", s.getCodingUsage)
	mux.HandleFunc("GET /api/coding/sessions", s.listCodingSessions)
	mux.HandleFunc("POST /api/coding/sessions", s.createCodingSession)
	mux.HandleFunc("GET /api/coding/sessions/{id}", s.getCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/prompt", s.promptCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/stop", s.stopCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/restart", s.restartCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/approve", s.approveCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/pull-request", s.createCodingSessionPullRequest)
	mux.HandleFunc("GET /api/coding/sessions/{id}/sandboxes/{sandboxId}/logs", s.getCodingSandboxLogs)
}

// getCodingStatus — TODO(track B).
//
//	GET /api/coding/status: Runtime status: enabled, provider, preflight checks, connected code hosts, regions, sizes.
func (s *server) getCodingStatus(w http.ResponseWriter, r *http.Request) { codingNotImplemented(w, r) }

// getCodingSettings — TODO(track B).
//
//	GET /api/coding/settings: Workspace coding policy (WorkspaceSettings.CodingAgentSettings, normalized).
func (s *server) getCodingSettings(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// updateCodingSettings — TODO(track B).
//
//	PATCH /api/coding/settings: Updates the policy; admins only.
func (s *server) updateCodingSettings(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// listCodingModels — TODO(track B).
//
//	GET /api/coding/models: Model catalog per harness with the Auto pick and reasoning efforts.
func (s *server) listCodingModels(w http.ResponseWriter, r *http.Request) { codingNotImplemented(w, r) }

// getCodingUsage — TODO(track B).
//
//	GET /api/coding/usage: Metered spend for Settings › Usage & limits (?since=).
func (s *server) getCodingUsage(w http.ResponseWriter, r *http.Request) { codingNotImplemented(w, r) }

// listCodingSessions — TODO(track B).
//
//	GET /api/coding/sessions: Lists sessions (?issueId=&state=&creatorId=).
func (s *server) listCodingSessions(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// createCodingSession — TODO(track B).
//
//	POST /api/coding/sessions: Starts a session (issue, agent chat, comment, loop); Idempotency-Key honoured.
func (s *server) createCodingSession(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// getCodingSession — TODO(track B).
//
//	GET /api/coding/sessions/{id}: One session with sandboxes, PRs/MRs, commits, artifacts.
func (s *server) getCodingSession(w http.ResponseWriter, r *http.Request) { codingNotImplemented(w, r) }

// promptCodingSession — TODO(track B).
//
//	POST /api/coding/sessions/{id}/prompt: Follow-up prompt (queues a turn in the inbox).
func (s *server) promptCodingSession(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// stopCodingSession — TODO(track B).
//
//	POST /api/coding/sessions/{id}/stop: Stops the session (state stopping → stale).
func (s *server) stopCodingSession(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// restartCodingSession — TODO(track B).
//
//	POST /api/coding/sessions/{id}/restart: Restarts after error/stop; {useDefaultModel:true} is "Restart with default model".
func (s *server) restartCodingSession(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// approveCodingSession — TODO(track B).
//
//	POST /api/coding/sessions/{id}/approve: Approves (or {reject:true}) a session from an untrusted external source.
func (s *server) approveCodingSession(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// createCodingSessionPullRequest — TODO(track B).
//
//	POST /api/coding/sessions/{id}/pull-request: Opens the draft PR/MR now (when the agent pushed but none exists).
func (s *server) createCodingSessionPullRequest(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// getCodingSandboxLogs — TODO(track B).
//
//	GET /api/coding/sessions/{id}/sandboxes/{sandboxId}/logs: Sandbox logs (live from the provider, or archived after it ended).
func (s *server) getCodingSandboxLogs(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}
