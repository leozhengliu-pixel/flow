package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// POST /Users/.search and /Groups/.search (RFC 7644 §3.4.3) run the list
// query of a SearchRequest body. POST /.search searches Users and Groups
// together: the result lists Users first, then Groups, unless sortBy
// orders them across both types (ties keep Users first), so paging is
// stable.
func (s *server) scimSearch(w http.ResponseWriter, r *http.Request) {
	workspaceKey, workspaceID, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
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
	root := resource != "Users" && resource != "Groups"
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
	switch {
	case root:
		s.scimRootSearch(w, list, workspaceKey, workspaceID)
	case resource == "Users":
		s.scimUsers(w, list)
	default:
		s.scimGroups(w, list)
	}
}

// scimRootSearch answers POST /.search (RFC 7644 §3.4.3): the filter,
// sorting, projection and paging of the list endpoints over Users and Groups
// together. Each type contributes its first startIndex+count-1 matches in
// the requested order; the merged, ordered list is then paged.
func (s *server) scimRootSearch(w http.ResponseWriter, r *http.Request, workspaceKey, workspaceID string) {
	base := scimBaseURL(r, workspaceKey)
	rawFilter := r.URL.Query().Get("filter")
	filter, err := parseSCIMFilter(rawFilter)
	if err != nil {
		writeSCIMRequestError(w, err)
		return
	}
	params, projection := readSCIMListParams(r), readSCIMProjection(r)
	window := params.startIndex - 1 + params.count
	if window > scimFilterScanLimit {
		writeSCIMRequestError(w, &scimRequestError{status: http.StatusBadRequest, scimType: scimTooMany, detail: "startIndex is too large for a search across resource types"})
		return
	}
	// totalResults is the number of Users plus Groups that match, so the
	// user query always runs (a count of 0 only returns the total).
	userParams := params
	userParams.startIndex, userParams.count = 1, window
	users, userTotal, err := s.listSCIMUserResources(r.Context(), workspaceID, base, filter, userParams)
	if err != nil {
		writeSCIMRequestError(w, err)
		return
	}
	groups, err := s.listSCIMGroupResources(r.Context(), workspaceKey, workspaceID, base, filter, rawFilter, projection)
	if err != nil {
		writeSCIMRequestError(w, err)
		return
	}
	if len(params.sortPath) == 0 {
		// Users come ordered by userName; order Groups by displayName.
		sortSCIMResources(groups, scimListParams{sortPath: []string{"displayname"}})
	}
	merged := append(users, groups...)
	sortSCIMResources(merged, params)
	resources := scimPage(merged, params)
	for index := range resources {
		resources[index] = projection.apply(resources[index])
	}
	writeSCIM(w, http.StatusOK, scimListResponse(userTotal+len(groups), params, resources))
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
