package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// POST /Users/.search and /Groups/.search (RFC 7644 §3.4.3) run the list
// query of a SearchRequest body. Searching several resource types at once
// (POST /.search) is not implemented (501).
func (s *server) scimSearch(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.authenticateSCIMRequest(w, r); !ok {
		return
	}
	var request struct {
		Attributes         []string `json:"attributes"`
		ExcludedAttributes []string `json:"excludedAttributes"`
		Filter             string   `json:"filter"`
		SortBy             string   `json:"sortBy"`
		SortOrder          string   `json:"sortOrder"`
		StartIndex         *int     `json:"startIndex"`
		Count              *int     `json:"count"`
	}
	if !decodeSCIM(w, r, &request) {
		return
	}
	resource := strings.TrimSuffix(r.URL.Path, "/.search")
	resource = resource[strings.LastIndex(resource, "/")+1:]
	if resource != "Users" && resource != "Groups" {
		writeSCIMError(w, http.StatusNotImplemented, "", "searching several resource types at once is not supported; use /Users/.search or /Groups/.search")
		return
	}
	query := url.Values{}
	for key, value := range map[string]string{"attributes": strings.Join(request.Attributes, ","), "excludedAttributes": strings.Join(request.ExcludedAttributes, ","), "filter": request.Filter, "sortBy": request.SortBy, "sortOrder": request.SortOrder} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if request.StartIndex != nil {
		query.Set("startIndex", strconv.Itoa(*request.StartIndex))
	}
	if request.Count != nil {
		query.Set("count", strconv.Itoa(*request.Count))
	}
	list := r.Clone(r.Context())
	list.Method, list.Body = http.MethodGet, http.NoBody
	list.URL.RawQuery = query.Encode()
	if resource == "Users" {
		s.scimUsers(w, list)
	} else {
		s.scimGroups(w, list)
	}
}

// scimUnknown answers the paths and methods the SCIM service does not have
// with SCIM error bodies: 405 on a known endpoint, 404 otherwise.
func (s *server) scimUnknown(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.authenticateSCIMRequest(w, r); !ok {
		return
	}
	rest := r.PathValue("rest")
	first, _, _ := strings.Cut(rest, "/")
	switch first {
	case "ServiceProviderConfig", "ResourceTypes", "Schemas", "Users", "Groups", "Bulk":
		writeSCIMError(w, http.StatusMethodNotAllowed, "", r.Method+" is not supported on /"+rest)
	default:
		writeSCIMError(w, http.StatusNotFound, "", "/"+rest+" is not a SCIM endpoint")
	}
}
