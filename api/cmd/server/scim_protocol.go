package main

import (
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	scimSchemaUser           = "urn:ietf:params:scim:schemas:core:2.0:User"
	scimSchemaEnterpriseUser = "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User"
	scimSchemaGroup          = "urn:ietf:params:scim:schemas:core:2.0:Group"
	scimSchemaSPC            = "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"
	scimSchemaResourceType   = "urn:ietf:params:scim:schemas:core:2.0:ResourceType"
	scimSchemaSchema         = "urn:ietf:params:scim:schemas:core:2.0:Schema"
	scimMessageListResponse  = "urn:ietf:params:scim:api:messages:2.0:ListResponse"
	scimMessageError         = "urn:ietf:params:scim:api:messages:2.0:Error"
	scimMessagePatchOp       = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
	scimMessageBulkRequest   = "urn:ietf:params:scim:api:messages:2.0:BulkRequest"
	scimMessageBulkResponse  = "urn:ietf:params:scim:api:messages:2.0:BulkResponse"
	scimBulkMaxOperations    = 1000
	scimBulkMaxPayload       = 1 << 20
	scimDefaultCount         = 100
	scimMaxCount             = 1000
	scimFilterScanLimit      = 100000
	scimInvalidFilter        = "invalidFilter"
	scimInvalidValue         = "invalidValue"
	scimInvalidSyntax        = "invalidSyntax"
	scimInvalidPath          = "invalidPath"
	scimNoTarget             = "noTarget"
	scimUniqueness           = "uniqueness"
	scimTooMany              = "tooMany"
	scimMutability           = "mutability"
	scimContentType          = "application/scim+json; charset=utf-8"
)

// writeSCIM writes a SCIM response body with the SCIM media type.
func writeSCIM(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", scimContentType)
	w.WriteHeader(status)
	if value == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("encode SCIM response: %v", err)
	}
}

// writeSCIMError writes an RFC 7644 §3.12 error: status is a string and
// scimType is set for 400 and 409 responses that have one.
func writeSCIMError(w http.ResponseWriter, status int, scimType, detail string) {
	body := map[string]any{"schemas": []string{scimMessageError}, "status": strconv.Itoa(status), "detail": detail}
	if scimType != "" {
		body["scimType"] = scimType
	}
	writeSCIM(w, status, body)
}

// scimRequestError carries an HTTP status and scimType from request
// handling down to the response.
type scimRequestError struct {
	status   int
	scimType string
	detail   string
}

func (e *scimRequestError) Error() string { return e.detail }

func scimBadRequest(scimType, detail string) error {
	return &scimRequestError{status: http.StatusBadRequest, scimType: scimType, detail: detail}
}

func writeSCIMRequestError(w http.ResponseWriter, err error) {
	var requestErr *scimRequestError
	if errors.As(err, &requestErr) {
		writeSCIMError(w, requestErr.status, requestErr.scimType, requestErr.detail)
		return
	}
	var filterErr scimFilterError
	if errors.As(err, &filterErr) {
		writeSCIMError(w, http.StatusBadRequest, scimInvalidFilter, filterErr.detail)
		return
	}
	// requestLog put the id on the response before the handler ran.
	requestID := w.Header().Get("X-Request-ID")
	slog.Error("SCIM request failed", "request_id", requestID, "error", err.Error())
	detail := "internal error"
	if requestID != "" {
		detail += " (request id " + requestID + ")"
	}
	writeSCIMError(w, http.StatusInternalServerError, "", detail)
}

// decodeSCIM reads a JSON request body; malformed JSON is invalidSyntax.
func decodeSCIM(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	stripRequestJSONNUL(r)
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeSCIMError(w, http.StatusBadRequest, scimInvalidSyntax, "request body is not valid JSON: "+err.Error())
		return false
	}
	return true
}

func scimBaseURL(r *http.Request, workspaceKey string) string {
	return externalBaseURL(r) + "/scim/v2/" + url.PathEscape(workspaceKey)
}

// scimListParams reads startIndex (1-based, values below 1 mean 1) and
// count (absent means the default page, negative means 0, capped at the
// maximum the service provider config declares).
type scimListParams struct {
	startIndex int
	count      int
	sortBy     string // as sent; "" when unsorted
	sortPath   []string
	descending bool
}

func readSCIMListParams(r *http.Request) scimListParams {
	query := r.URL.Query()
	params := scimListParams{startIndex: 1, count: scimDefaultCount}
	if value, err := strconv.Atoi(strings.TrimSpace(query.Get("startIndex"))); err == nil && value > 1 {
		params.startIndex = value
	}
	if raw := strings.TrimSpace(query.Get("count")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			params.count = min(max(value, 0), scimMaxCount)
		}
	}
	if sortBy := strings.TrimSpace(query.Get("sortBy")); sortBy != "" {
		if path, err := scimAttributePath(sortBy); err == nil {
			params.sortBy, params.sortPath = sortBy, path
		}
	}
	// sortOrder defaults to ascending (RFC 7644 §3.4.2.3); it only applies
	// with sortBy.
	params.descending = params.sortBy != "" && strings.EqualFold(strings.TrimSpace(query.Get("sortOrder")), "descending")
	return params
}

func scimListResponse(total int, params scimListParams, resources []map[string]any) map[string]any {
	if resources == nil {
		resources = []map[string]any{}
	}
	return map[string]any{"schemas": []string{scimMessageListResponse}, "totalResults": total, "startIndex": params.startIndex, "itemsPerPage": len(resources), "Resources": resources}
}

// scimProjection applies the attributes / excludedAttributes query
// parameters (RFC 7644 §3.4.2.5) to a resource. id and schemas are always
// returned. Paths may name a sub-attribute (name.givenName).
type scimProjection struct {
	include map[string]map[string]bool
	exclude map[string]map[string]bool
}

func readSCIMProjection(r *http.Request) scimProjection {
	parse := func(raw string) map[string]map[string]bool {
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		result := map[string]map[string]bool{}
		for _, item := range strings.Split(raw, ",") {
			path, err := scimAttributePath(item)
			if err != nil || len(path) == 0 {
				continue
			}
			if result[path[0]] == nil {
				result[path[0]] = map[string]bool{}
			}
			if len(path) > 1 {
				result[path[0]][path[1]] = true
			}
		}
		return result
	}
	query := r.URL.Query()
	return scimProjection{include: parse(query.Get("attributes")), exclude: parse(query.Get("excludedAttributes"))}
}

// wants reports whether the projection keeps a top-level attribute, so
// handlers can skip loading expensive ones (Group members).
func (p scimProjection) wants(attribute string) bool {
	attribute = strings.ToLower(attribute)
	if p.include != nil {
		_, ok := p.include[attribute]
		return ok
	}
	if subs, ok := p.exclude[attribute]; ok && len(subs) == 0 {
		return false
	}
	return true
}

func (p scimProjection) apply(resource map[string]any) map[string]any {
	if p.include == nil && p.exclude == nil {
		return resource
	}
	result := map[string]any{}
	for key, value := range resource {
		lower := strings.ToLower(key)
		if lower == "id" || lower == "schemas" {
			result[key] = value
			continue
		}
		if p.include != nil {
			subs, ok := p.include[lower]
			if !ok {
				continue
			}
			result[key] = scimFilterSubAttributes(value, subs, true)
			continue
		}
		if subs, ok := p.exclude[lower]; ok {
			if len(subs) == 0 {
				continue
			}
			result[key] = scimFilterSubAttributes(value, subs, false)
			continue
		}
		result[key] = value
	}
	return result
}

func scimFilterSubAttributes(value any, subs map[string]bool, keep bool) any {
	if len(subs) == 0 {
		return value
	}
	switch typed := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, sub := range typed {
			if subs[strings.ToLower(key)] == keep {
				result[key] = sub
			}
		}
		return result
	case []map[string]any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, scimFilterSubAttributes(item, subs, keep).(map[string]any))
		}
		return result
	}
	return value
}

// Discovery: ServiceProviderConfig, ResourceTypes and Schemas (RFC 7643 §5-7).

func (s *server) scimServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	workspaceKey, _, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	writeSCIM(w, http.StatusOK, map[string]any{
		"schemas":        []string{scimSchemaSPC},
		"patch":          map[string]bool{"supported": true},
		"bulk":           map[string]any{"supported": true, "maxOperations": scimBulkMaxOperations, "maxPayloadSize": scimBulkMaxPayload},
		"filter":         map[string]any{"supported": true, "maxResults": scimMaxCount},
		"changePassword": map[string]bool{"supported": false},
		"sort":           map[string]bool{"supported": true},
		"etag":           map[string]bool{"supported": true},
		"authenticationSchemes": []map[string]any{{
			"type": "oauthbearertoken", "name": "OAuth Bearer Token", "primary": true,
			"description": "Authentication with a workspace SCIM token, sent as Authorization: Bearer <token>.",
		}},
		"meta": map[string]any{"resourceType": "ServiceProviderConfig", "location": scimBaseURL(r, workspaceKey) + "/ServiceProviderConfig"},
	})
}

func scimResourceTypes(base string) []map[string]any {
	return []map[string]any{
		{"schemas": []string{scimSchemaResourceType}, "id": "User", "name": "User", "endpoint": "/Users", "description": "Workspace member", "schema": scimSchemaUser, "schemaExtensions": []map[string]any{{"schema": scimSchemaEnterpriseUser, "required": false}}, "meta": map[string]any{"resourceType": "ResourceType", "location": base + "/ResourceTypes/User"}},
		{"schemas": []string{scimSchemaResourceType}, "id": "Group", "name": "Group", "endpoint": "/Groups", "description": "Directory group: a role group or a group linked to a team", "schema": scimSchemaGroup, "meta": map[string]any{"resourceType": "ResourceType", "location": base + "/ResourceTypes/Group"}},
	}
}

func (s *server) scimResourceTypes(w http.ResponseWriter, r *http.Request) {
	workspaceKey, _, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	items := scimResourceTypes(scimBaseURL(r, workspaceKey))
	if id := r.PathValue("id"); id != "" {
		for _, item := range items {
			if strings.EqualFold(item["id"].(string), id) {
				writeSCIM(w, http.StatusOK, item)
				return
			}
		}
		writeSCIMError(w, http.StatusNotFound, "", "resource type "+id+" not found")
		return
	}
	writeSCIM(w, http.StatusOK, scimListResponse(len(items), scimListParams{startIndex: 1}, items))
}

func scimAttribute(name, kind string, options map[string]any) map[string]any {
	attribute := map[string]any{"name": name, "type": kind, "multiValued": false, "required": false, "mutability": "readWrite", "returned": "default", "uniqueness": "none"}
	if kind == "string" || kind == "reference" {
		attribute["caseExact"] = false
	}
	for key, value := range options {
		attribute[key] = value
	}
	return attribute
}

func scimSchemas(base string) []map[string]any {
	user := map[string]any{
		"schemas": []string{scimSchemaSchema}, "id": scimSchemaUser, "name": "User", "description": "Workspace member",
		"attributes": []map[string]any{
			scimAttribute("userName", "string", map[string]any{"required": true, "uniqueness": "server", "description": "Unique sign-in name, usually the work email. Compared case-insensitively."}),
			scimAttribute("name", "complex", map[string]any{"description": "The member's name.", "subAttributes": []map[string]any{
				scimAttribute("formatted", "string", map[string]any{"description": "Full name; used when givenName and familyName are not sent."}),
				scimAttribute("givenName", "string", nil),
				scimAttribute("familyName", "string", nil),
				scimAttribute("middleName", "string", nil),
				scimAttribute("honorificPrefix", "string", nil),
				scimAttribute("honorificSuffix", "string", nil),
			}}),
			scimAttribute("displayName", "string", map[string]any{"description": "Name shown in the workspace."}),
			scimAttribute("active", "boolean", map[string]any{"description": "false suspends the member; true restores them."}),
			scimAttribute("emails", "complex", map[string]any{"multiValued": true, "description": "The primary (or first work) email becomes the member's email.", "subAttributes": []map[string]any{
				scimAttribute("value", "string", nil),
				scimAttribute("type", "string", map[string]any{"canonicalValues": []string{"work", "home", "other"}}),
				scimAttribute("primary", "boolean", nil),
			}}),
			scimAttribute("roles", "complex", map[string]any{"multiValued": true, "mutability": "writeOnly", "returned": "never", "description": "Workspace role (owner, admin, member, guest or a mapped role value).", "subAttributes": []map[string]any{
				scimAttribute("value", "string", nil),
			}}),
			scimAttribute("title", "string", map[string]any{"description": "Job title; shown as the member's job title in Flow."}),
			scimAttribute("nickName", "string", nil),
			scimAttribute("userType", "string", nil),
			scimAttribute("preferredLanguage", "string", nil),
			scimAttribute("locale", "string", nil),
			scimAttribute("timezone", "string", map[string]any{"description": "IANA time zone; used for the member's Pulse delivery when they have not set one."}),
			scimAttribute("phoneNumbers", "complex", map[string]any{"multiValued": true, "subAttributes": []map[string]any{
				scimAttribute("value", "string", nil),
				scimAttribute("display", "string", nil),
				scimAttribute("type", "string", map[string]any{"canonicalValues": []string{"work", "home", "mobile", "fax", "pager", "other"}}),
				scimAttribute("primary", "boolean", nil),
			}}),
			scimAttribute("addresses", "complex", map[string]any{"multiValued": true, "subAttributes": []map[string]any{
				scimAttribute("formatted", "string", nil),
				scimAttribute("streetAddress", "string", nil),
				scimAttribute("locality", "string", nil),
				scimAttribute("region", "string", nil),
				scimAttribute("postalCode", "string", nil),
				scimAttribute("country", "string", nil),
				scimAttribute("type", "string", map[string]any{"canonicalValues": []string{"work", "home", "other"}}),
				scimAttribute("primary", "boolean", nil),
			}}),
		},
		"meta": map[string]any{"resourceType": "Schema", "location": base + "/Schemas/" + scimSchemaUser},
	}
	enterprise := map[string]any{
		"schemas": []string{scimSchemaSchema}, "id": scimSchemaEnterpriseUser, "name": "EnterpriseUser", "description": "Enterprise User",
		"attributes": []map[string]any{
			scimAttribute("employeeNumber", "string", nil),
			scimAttribute("costCenter", "string", nil),
			scimAttribute("organization", "string", nil),
			scimAttribute("division", "string", nil),
			scimAttribute("department", "string", nil),
			scimAttribute("manager", "complex", map[string]any{"description": "The member's manager, as the directory identifies them.", "subAttributes": []map[string]any{
				scimAttribute("value", "string", nil),
				scimAttribute("$ref", "reference", map[string]any{"referenceTypes": []string{"User"}}),
				scimAttribute("displayName", "string", nil),
			}}),
		},
		"meta": map[string]any{"resourceType": "Schema", "location": base + "/Schemas/" + scimSchemaEnterpriseUser},
	}
	group := map[string]any{
		"schemas": []string{scimSchemaSchema}, "id": scimSchemaGroup, "name": "Group", "description": "Directory group",
		"attributes": []map[string]any{
			scimAttribute("displayName", "string", map[string]any{"required": true, "description": "Matched against role groups and team group mappings."}),
			scimAttribute("members", "complex", map[string]any{"multiValued": true, "description": "Group members (User ids).", "subAttributes": []map[string]any{
				scimAttribute("value", "string", map[string]any{"mutability": "immutable", "caseExact": true}),
				scimAttribute("$ref", "reference", map[string]any{"mutability": "immutable", "referenceTypes": []string{"User"}}),
				scimAttribute("display", "string", map[string]any{"mutability": "readOnly"}),
			}}),
		},
		"meta": map[string]any{"resourceType": "Schema", "location": base + "/Schemas/" + scimSchemaGroup},
	}
	return []map[string]any{user, enterprise, group}
}

func (s *server) scimSchemas(w http.ResponseWriter, r *http.Request) {
	workspaceKey, _, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	items := scimSchemas(scimBaseURL(r, workspaceKey))
	if id := r.PathValue("id"); id != "" {
		for _, item := range items {
			if strings.EqualFold(item["id"].(string), id) {
				writeSCIM(w, http.StatusOK, item)
				return
			}
		}
		writeSCIMError(w, http.StatusNotFound, "", "schema "+id+" not found")
		return
	}
	writeSCIM(w, http.StatusOK, scimListResponse(len(items), scimListParams{startIndex: 1}, items))
}
