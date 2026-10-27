package main

import (
	"log/slog"
	"net/http"
)

// health answers the anonymous liveness probe used by load balancers, the
// container HEALTHCHECK and Kubernetes. The body only says whether this
// server can serve requests: build version, commit and the Redis deployment
// mode are left out because anyone can read it. Operators find those in the
// startup log line, and a failed coordinator ping is logged here (once per
// change of state, so frequent probes do not flood the log) with its error,
// which never reaches the response.
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if s.coordinator != nil {
		if err := s.coordinator.Ping(r.Context()); err != nil {
			if !s.healthDegraded.Swap(true) {
				slog.ErrorContext(r.Context(), "health check degraded: redis coordinator unreachable", "redis_mode", s.coordinator.Mode(), "error", err.Error())
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
			return
		}
		if s.healthDegraded.Swap(false) {
			slog.InfoContext(r.Context(), "health check recovered: redis coordinator reachable", "redis_mode", s.coordinator.Mode())
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
