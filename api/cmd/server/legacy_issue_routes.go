package main

import (
	"net/http"
	"strings"
)

// Old browser builds use /issues. Route their writes through the bounded
// records implementation before authorization as well as before dispatch;
// they share its contract, expectedVersion included.
func boundedLegacyIssueWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.URL.Path == "/api/issues" || strings.HasPrefix(r.URL.Path, "/api/issues/")) &&
			(r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodPut || r.Method == http.MethodDelete) {
			copy := r.Clone(r.Context())
			copy.URL.Path = "/api/issue-records" + strings.TrimPrefix(r.URL.Path, "/api/issues")
			copy.URL.RawPath = ""
			r = copy
		}
		next.ServeHTTP(w, r)
	})
}
