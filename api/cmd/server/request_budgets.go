package main

import (
	"errors"
	"net/http"
	"strings"
)

const maxJSONRequestBytes = 16 << 20

func requestBodyBudget(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			if r.ContentLength > maxJSONRequestBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "JSON request exceeds 16 MiB")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
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
