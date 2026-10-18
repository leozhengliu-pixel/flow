package main

import "net/http"

// Runner-facing API (/internal/coding-runtime/*), authenticated with the sandbox token (coding.SandboxTokenPrefix). Wire types: api/internal/coding/protocol.go.
//
// Owner: track B (session core). Handlers are 501 stubs until the track implements
// them; keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingRuntimeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/coding-runtime/bootstrap", s.codingRuntimeBootstrap)
	mux.HandleFunc("GET /internal/coding-runtime/setup-secrets", s.codingRuntimeSetupSecrets)
	mux.HandleFunc("POST /internal/coding-runtime/heartbeat", s.codingRuntimeHeartbeat)
	mux.HandleFunc("POST /internal/coding-runtime/events", s.codingRuntimeEvents)
	mux.HandleFunc("GET /internal/coding-runtime/inbox", s.codingRuntimeInbox)
	mux.HandleFunc("POST /internal/coding-runtime/turns/{turnId}/complete", s.codingRuntimeCompleteTurn)
	mux.HandleFunc("POST /internal/coding-runtime/credentials", s.codingRuntimeCredentials)
	mux.HandleFunc("GET /internal/coding-runtime/harness-state", s.codingRuntimeGetHarnessState)
	mux.HandleFunc("PUT /internal/coding-runtime/harness-state", s.codingRuntimePutHarnessState)
}

// codingRuntimeBootstrap — TODO(track B).
//
//	GET /internal/coding-runtime/bootstrap: Session spec for the runner; ?wait= long-polls for warm-pool binding (204 while unbound).
func (s *server) codingRuntimeBootstrap(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeSetupSecrets — TODO(track B).
//
//	GET /internal/coding-runtime/setup-secrets: Environment secrets, answered once and only during the setup phase.
func (s *server) codingRuntimeSetupSecrets(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeHeartbeat — TODO(track B).
//
//	POST /internal/coding-runtime/heartbeat: Keeps the sandbox alive; answers {stop:true} when stopping.
func (s *server) codingRuntimeHeartbeat(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeEvents — TODO(track B).
//
//	POST /internal/coding-runtime/events: Batched runner events → AgentActivity, plan, diffs.
func (s *server) codingRuntimeEvents(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeInbox — TODO(track B).
//
//	GET /internal/coding-runtime/inbox: Long-poll for follow-up turns and signals (?after=&wait=).
func (s *server) codingRuntimeInbox(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeCompleteTurn — TODO(track B).
//
//	POST /internal/coding-runtime/turns/{turnId}/complete: Ends a turn: summary, commits, push result, harness resume id.
func (s *server) codingRuntimeCompleteTurn(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeCredentials — TODO(track B).
//
//	POST /internal/coding-runtime/credentials: git credential helper / gh / glab: a short-lived repo-scoped token from the code host.
func (s *server) codingRuntimeCredentials(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimeGetHarnessState — TODO(track B).
//
//	GET /internal/coding-runtime/harness-state: Harness session files (~/.claude, ~/.codex) saved after the last turn, to resume in a new sandbox.
func (s *server) codingRuntimeGetHarnessState(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// codingRuntimePutHarnessState — TODO(track B).
//
//	PUT /internal/coding-runtime/harness-state: Saves the harness session files at the end of a turn (object store, size-capped).
func (s *server) codingRuntimePutHarnessState(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}
