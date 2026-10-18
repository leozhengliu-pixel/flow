package main

import "net/http"

// Environment, secret, snapshot and debug-terminal routes (docs/coding-sessions-design.md §7.1, §10).
//
// Owner: track K (environments & snapshots). Handlers are 501 stubs until the track implements
// them; keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingEnvironmentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/coding/repositories", s.listCodingRepositories)
	mux.HandleFunc("GET /api/coding/environments", s.listCodingEnvironments)
	mux.HandleFunc("POST /api/coding/environments", s.createCodingEnvironment)
	mux.HandleFunc("GET /api/coding/environments/{id}", s.getCodingEnvironment)
	mux.HandleFunc("PATCH /api/coding/environments/{id}", s.updateCodingEnvironment)
	mux.HandleFunc("POST /api/coding/environments/{id}/archive", s.archiveCodingEnvironment)
	mux.HandleFunc("POST /api/coding/environments/{id}/unarchive", s.unarchiveCodingEnvironment)
	mux.HandleFunc("POST /api/coding/environments/{id}/secrets", s.addCodingEnvironmentSecrets)
	mux.HandleFunc("DELETE /api/coding/environments/{id}/secrets/{name}", s.deleteCodingEnvironmentSecret)
	mux.HandleFunc("POST /api/coding/environments/{id}/env-import", s.importCodingEnvironmentFile)
	mux.HandleFunc("GET /api/coding/environments/{id}/snapshots", s.listCodingSnapshots)
	mux.HandleFunc("POST /api/coding/environments/{id}/snapshots", s.refreshCodingSnapshot)
	mux.HandleFunc("POST /api/coding/snapshots/{id}/cancel", s.cancelCodingSnapshot)
	mux.HandleFunc("GET /api/coding/snapshots/{id}/logs", s.getCodingSnapshotLogs)
	mux.HandleFunc("POST /api/coding/environments/{id}/configure-with-agent", s.configureCodingEnvironmentWithAgent)
	mux.HandleFunc("POST /api/coding/environments/{id}/fix-with-agent", s.fixCodingEnvironmentWithAgent)
	mux.HandleFunc("GET /api/coding/environments/{id}/terminal", s.codingEnvironmentTerminal)
	mux.HandleFunc("PUT /internal/coding-runtime/snapshots/{id}/archive", s.codingRuntimePutSnapshotArchive)
	mux.HandleFunc("GET /internal/coding-runtime/snapshots/{id}/archive", s.codingRuntimeGetSnapshotArchive)
	mux.HandleFunc("POST /internal/coding-runtime/snapshots/{id}/logs", s.codingRuntimeAppendSnapshotLogs)
	mux.HandleFunc("POST /internal/coding-runtime/snapshots/{id}/complete", s.codingRuntimeCompleteSnapshot)
	mux.HandleFunc("POST /internal/coding-runtime/environment-proposal", s.codingRuntimeEnvironmentProposal)
	mux.HandleFunc("POST /api/coding/environments/{id}/proposal/apply", s.applyCodingEnvironmentProposal)
}

// listCodingRepositories — TODO(track K).
//
//	GET /api/coding/repositories: Repositories for the environment editor (?codeHost=&hostName=&q=&cursor=).
func (s *server) listCodingRepositories(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// listCodingEnvironments — TODO(track K).
//
//	GET /api/coding/environments: Environments (?archived=true includes archived).
func (s *server) listCodingEnvironments(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// createCodingEnvironment — TODO(track K).
//
//	POST /api/coding/environments: Creates an environment (one active environment per repository).
func (s *server) createCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// getCodingEnvironment — TODO(track K).
//
//	GET /api/coding/environments/{id}: One environment with masked secrets and the latest snapshot.
func (s *server) getCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// updateCodingEnvironment — TODO(track K).
//
//	PATCH /api/coding/environments/{id}: Updates fields (version CAS); config changes mark the snapshot stale.
func (s *server) updateCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// archiveCodingEnvironment — TODO(track K).
//
//	POST /api/coding/environments/{id}/archive: Archives the environment.
func (s *server) archiveCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// unarchiveCodingEnvironment — TODO(track K).
//
//	POST /api/coding/environments/{id}/unarchive: Restores an archived environment (fails if the repo has another active one).
func (s *server) unarchiveCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// addCodingEnvironmentSecrets — TODO(track K).
//
//	POST /api/coding/environments/{id}/secrets: Adds secrets {secrets:[{name,value}]}; existing names are rejected (immutable).
func (s *server) addCodingEnvironmentSecrets(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// deleteCodingEnvironmentSecret — TODO(track K).
//
//	DELETE /api/coding/environments/{id}/secrets/{name}: Deletes a secret.
func (s *server) deleteCodingEnvironmentSecret(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// importCodingEnvironmentFile — TODO(track K).
//
//	POST /api/coding/environments/{id}/env-import: Parses a .env body into {variables, secrets} suggestions (nothing saved).
func (s *server) importCodingEnvironmentFile(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// listCodingSnapshots — TODO(track K).
//
//	GET /api/coding/environments/{id}/snapshots: Snapshot history with statuses.
func (s *server) listCodingSnapshots(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// refreshCodingSnapshot — TODO(track K).
//
//	POST /api/coding/environments/{id}/snapshots: Builds a new snapshot ("Refresh").
func (s *server) refreshCodingSnapshot(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// cancelCodingSnapshot — TODO(track K).
//
//	POST /api/coding/snapshots/{id}/cancel: Cancels a queued or building snapshot.
func (s *server) cancelCodingSnapshot(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// getCodingSnapshotLogs — TODO(track K).
//
//	GET /api/coding/snapshots/{id}/logs: Snapshot build logs (?follow=true streams).
func (s *server) getCodingSnapshotLogs(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// configureCodingEnvironmentWithAgent — TODO(track K).
//
//	POST /api/coding/environments/{id}/configure-with-agent: Starts an environmentSetup session that proposes mise/prepare config.
func (s *server) configureCodingEnvironmentWithAgent(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// fixCodingEnvironmentWithAgent — TODO(track K).
//
//	POST /api/coding/environments/{id}/fix-with-agent: Starts an environmentSetup session from the failed snapshot's logs.
func (s *server) fixCodingEnvironmentWithAgent(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingEnvironmentTerminal — TODO(track K).
//
//	GET /api/coding/environments/{id}/terminal: Debug terminal: WebSocket to a temporary sandbox's shell (destroyed after FLOW_CODING_DEBUG_TERMINAL_TTL).
func (s *server) codingEnvironmentTerminal(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimePutSnapshotArchive — TODO(track K).
//
//	PUT /internal/coding-runtime/snapshots/{id}/archive: Runner uploads a snapshot archive (archive strategy).
func (s *server) codingRuntimePutSnapshotArchive(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeGetSnapshotArchive — TODO(track K).
//
//	GET /internal/coding-runtime/snapshots/{id}/archive: Restore init container downloads a snapshot archive.
func (s *server) codingRuntimeGetSnapshotArchive(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeAppendSnapshotLogs — TODO(track K).
//
//	POST /internal/coding-runtime/snapshots/{id}/logs: Runner appends snapshot build log lines.
func (s *server) codingRuntimeAppendSnapshotLogs(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeCompleteSnapshot — TODO(track K).
//
//	POST /internal/coding-runtime/snapshots/{id}/complete: Runner reports the snapshot build result.
func (s *server) codingRuntimeCompleteSnapshot(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeEnvironmentProposal — TODO(track K).
//
//	POST /internal/coding-runtime/environment-proposal: Configure/Fix with agent: the agent's proposed environment changes (stored as the environment's pending proposal).
func (s *server) codingRuntimeEnvironmentProposal(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// applyCodingEnvironmentProposal — TODO(track K).
//
//	POST /api/coding/environments/{id}/proposal/apply: Applies (or {discard:true}) the pending agent proposal.
func (s *server) applyCodingEnvironmentProposal(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}
