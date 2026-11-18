package main

import (
	"net/http"
	"net/url"
	"strings"
)

// Match fixed tokens and single-segment wildcards before unescaping slashes.
// Return the literal authorizer's decoded parts, not wildcard PathValues.
func scaleP21EscapedRouteParts(r *http.Request, pattern string) ([]string, bool) {
	if r == nil || r.URL == nil || !strings.HasPrefix(pattern, "/") || pattern == "/" {
		return nil, false
	}
	actual, wanted := strings.Split(r.URL.EscapedPath(), "/"), strings.Split(pattern, "/")
	if len(actual) != len(wanted) {
		return nil, false
	}
	for index, token := range wanted {
		if strings.HasPrefix(token, "{") && strings.HasSuffix(token, "}") {
			name := token[1 : len(token)-1]
			if name == "" || name == "$" || strings.Contains(name, "...") || strings.ContainsAny(name, "{}") || actual[index] == "" {
				return nil, false
			}
			continue
		}
		value, err := url.PathUnescape(actual[index])
		if err != nil {
			return nil, false
		}
		fixed, err := url.PathUnescape(token)
		if err != nil || value != fixed {
			return nil, false
		}
	}
	return strings.Split(strings.Trim(r.URL.Path, "/"), "/"), true
}
