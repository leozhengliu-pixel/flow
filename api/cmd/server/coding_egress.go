package main

import "net/http"

// Egress ACL check for the Squid external ACL helper (authenticated with FLOW_CODING_EGRESS_ACL_SECRET).
//
// Owner: track H (deploy, egress proxy, docs). Handlers are 501 stubs until the track implements
// them; keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingEgressRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/coding-runtime/egress-check", s.codingEgressCheck)
}

// codingEgressCheck — TODO(track H).
//
//	POST /internal/coding-runtime/egress-check: {sandboxToken, host, port} → {allow} by the environment's network mode.
func (s *server) codingEgressCheck(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}
