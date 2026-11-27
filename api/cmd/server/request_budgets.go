package main

import (
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
)

const maxJSONRequestBytes = 16 << 20

// jsonRequestLimitOverride replaces maxJSONRequestBytes in tests, so they
// exercise bodies over the general limit at CI sizes.
var jsonRequestLimitOverride atomic.Int64

// jsonRequestLimit is the general JSON request body limit (16 MiB).
func jsonRequestLimit() int64 {
	if limit := jsonRequestLimitOverride.Load(); limit > 0 {
		return limit
	}
	return maxJSONRequestBytes
}

// requestBodyBudget limits JSON request bodies to 16 MiB. The document
// create and update routes have their own limit (documentRequestBodies).
func requestBodyBudget(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") && !documentBodyRoute(r) {
			limit := jsonRequestLimit()
			if r.ContentLength > limit {
				writeError(w, http.StatusRequestEntityTooLarge, "JSON request exceeds 16 MiB")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}

func requestDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "Request body is too large")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid JSON")
}
