package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// POST /Bulk (RFC 7644 §3.7). Each operation runs through the same handler
// as the single-resource request (authentication, validation, ETags, team
// sync and audit included), so a bulk write behaves exactly like the
// separate requests. bulkId references ("bulkId:<id>") in a path or in data
// are replaced with the id of the resource that operation's POST created;
// an operation that refers to a bulkId defined later runs after it.

type scimBulkOperation struct {
	Method  string          `json:"method"`
	BulkID  string          `json:"bulkId"`
	Version string          `json:"version"`
	Path    string          `json:"path"`
	Data    json.RawMessage `json:"data"`
}

type scimBulkRequest struct {
	Schemas      []string            `json:"schemas"`
	FailOnErrors int                 `json:"failOnErrors"`
	Operations   []scimBulkOperation `json:"Operations"`
}

// scimBulkRecorder captures one operation's response.
type scimBulkRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *scimBulkRecorder) Header() http.Header { return r.header }
func (r *scimBulkRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(data)
}
func (r *scimBulkRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

const scimBulkIDPrefix = "bulkId:"

func (s *server) scimBulk(w http.ResponseWriter, r *http.Request) {
	workspaceKey, _, ok := s.authenticateSCIMRequest(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, scimBulkMaxPayload))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeSCIMError(w, http.StatusRequestEntityTooLarge, "", fmt.Sprintf("The size of the bulk operation exceeds the maxPayloadSize (%d).", scimBulkMaxPayload))
		return
	case err != nil:
		writeSCIMError(w, http.StatusBadRequest, scimInvalidSyntax, "could not read the request body")
		return
	}
	var request scimBulkRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		writeSCIMError(w, http.StatusBadRequest, scimInvalidSyntax, "request body is not valid JSON: "+err.Error())
		return
	}
	if len(request.Operations) > scimBulkMaxOperations {
		writeSCIMError(w, http.StatusRequestEntityTooLarge, "", fmt.Sprintf("The number of operations exceeds the maxOperations (%d).", scimBulkMaxOperations))
		return
	}
	defined := map[string]int{}
	for index, operation := range request.Operations {
		method := strings.ToUpper(strings.TrimSpace(operation.Method))
		if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch && method != http.MethodDelete {
			writeSCIMError(w, http.StatusBadRequest, scimInvalidSyntax, fmt.Sprintf("operation %d: method must be POST, PUT, PATCH or DELETE", index+1))
			return
		}
		if method == http.MethodPost {
			if strings.TrimSpace(operation.BulkID) == "" {
				writeSCIMError(w, http.StatusBadRequest, scimInvalidSyntax, fmt.Sprintf("operation %d: POST needs a bulkId", index+1))
				return
			}
			if _, duplicate := defined[operation.BulkID]; duplicate {
				writeSCIMError(w, http.StatusBadRequest, scimUniqueness, fmt.Sprintf("operation %d: bulkId %q is used twice", index+1, operation.BulkID))
				return
			}
			defined[operation.BulkID] = index
		}
	}
	base := scimBaseURL(r, workspaceKey)
	resolved := map[string]string{} // bulkId -> created resource id
	failed := map[string]bool{}     // bulkIds whose POST failed
	results := make([]map[string]any, len(request.Operations))
	done := make([]bool, len(request.Operations))
	errorCount := 0
	stop := false
	for remaining := len(request.Operations); remaining > 0 && !stop; {
		progressed := false
		for index, operation := range request.Operations {
			if done[index] || stop {
				continue
			}
			refs := scimBulkReferences(operation)
			waiting, unresolvable := false, ""
			for _, ref := range refs {
				switch _, ok := resolved[ref]; {
				case ok:
				case failed[ref]:
					unresolvable = ref
				default:
					if at, ok := defined[ref]; ok && !done[at] && at != index {
						waiting = true
					} else {
						unresolvable = ref
					}
				}
			}
			if waiting && unresolvable == "" {
				continue
			}
			done[index], progressed = true, true
			remaining--
			var result map[string]any
			if unresolvable != "" {
				result = scimBulkResult(operation, http.StatusConflict, nil, scimBulkErrorBody(http.StatusConflict, scimInvalidValue, fmt.Sprintf("bulkId %q does not refer to a resource created by this request", unresolvable)))
			} else {
				result = s.runSCIMBulkOperation(r, base, operation, resolved)
			}
			results[index] = result
			status, _ := strconv.Atoi(result["status"].(string))
			if status >= 400 {
				errorCount++
				if operation.BulkID != "" {
					failed[operation.BulkID] = true
				}
				if request.FailOnErrors > 0 && errorCount >= request.FailOnErrors {
					stop = true
				}
			} else if strings.EqualFold(operation.Method, http.MethodPost) {
				location, _ := result["location"].(string)
				resolved[operation.BulkID] = location[strings.LastIndex(location, "/")+1:]
			}
		}
		if !progressed {
			// Every remaining operation waits for another one: a cycle.
			for index, operation := range request.Operations {
				if !done[index] {
					done[index] = true
					remaining--
					results[index] = scimBulkResult(operation, http.StatusConflict, nil, scimBulkErrorBody(http.StatusConflict, scimInvalidValue, "circular bulkId references cannot be resolved"))
					errorCount++
				}
			}
		}
	}
	operations := []map[string]any{}
	for _, result := range results {
		if result != nil {
			operations = append(operations, result)
		}
	}
	writeSCIM(w, http.StatusOK, map[string]any{"schemas": []string{scimMessageBulkResponse}, "Operations": operations})
}

// scimBulkReferences lists the bulkIds an operation's path and data refer to.
func scimBulkReferences(operation scimBulkOperation) []string {
	refs := []string{}
	if _, after, ok := strings.Cut(operation.Path, scimBulkIDPrefix); ok {
		refs = append(refs, strings.SplitN(after, "/", 2)[0])
	}
	var data any
	if len(operation.Data) > 0 && json.Unmarshal(operation.Data, &data) == nil {
		var walk func(any)
		walk = func(value any) {
			switch typed := value.(type) {
			case string:
				if strings.HasPrefix(typed, scimBulkIDPrefix) {
					refs = append(refs, strings.TrimPrefix(typed, scimBulkIDPrefix))
				}
			case []any:
				for _, item := range typed {
					walk(item)
				}
			case map[string]any:
				for _, item := range typed {
					walk(item)
				}
			}
		}
		walk(data)
	}
	return refs
}

// scimBulkResolve replaces resolved bulkId references in a value.
func scimBulkResolve(value any, resolved map[string]string) any {
	switch typed := value.(type) {
	case string:
		if id, ok := resolved[strings.TrimPrefix(typed, scimBulkIDPrefix)]; ok && strings.HasPrefix(typed, scimBulkIDPrefix) {
			return id
		}
	case []any:
		for index, item := range typed {
			typed[index] = scimBulkResolve(item, resolved)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = scimBulkResolve(item, resolved)
		}
	}
	return value
}

func scimBulkErrorBody(status int, scimType, detail string) map[string]any {
	body := map[string]any{"schemas": []string{scimMessageError}, "status": strconv.Itoa(status), "detail": detail}
	if scimType != "" {
		body["scimType"] = scimType
	}
	return body
}

func scimBulkResult(operation scimBulkOperation, status int, header http.Header, errorBody map[string]any) map[string]any {
	result := map[string]any{"method": strings.ToUpper(strings.TrimSpace(operation.Method)), "status": strconv.Itoa(status)}
	if operation.BulkID != "" {
		result["bulkId"] = operation.BulkID
	}
	if header != nil {
		if location := header.Get("Location"); location != "" {
			result["location"] = location
		}
		if version := header.Get("ETag"); version != "" {
			result["version"] = version
		}
	}
	if errorBody != nil {
		result["response"] = errorBody
	}
	return result
}

// runSCIMBulkOperation runs one operation through its single-resource
// handler with the bulk request's credentials and returns its result.
func (s *server) runSCIMBulkOperation(r *http.Request, base string, operation scimBulkOperation, resolved map[string]string) map[string]any {
	method := strings.ToUpper(strings.TrimSpace(operation.Method))
	path := strings.TrimSpace(operation.Path)
	for bulkID, id := range resolved {
		path = strings.ReplaceAll(path, scimBulkIDPrefix+bulkID, id)
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	var handler http.HandlerFunc
	id := ""
	switch {
	case len(segments) == 1 && segments[0] == "Users" && method == http.MethodPost:
		handler = s.scimUsers
	case len(segments) == 1 && segments[0] == "Groups" && method == http.MethodPost:
		handler = s.scimGroups
	case len(segments) == 2 && segments[0] == "Users" && method != http.MethodPost && segments[1] != "":
		handler, id = s.scimUser, segments[1]
	case len(segments) == 2 && segments[0] == "Groups" && method != http.MethodPost && segments[1] != "":
		handler, id = s.scimGroup, segments[1]
	default:
		return scimBulkResult(operation, http.StatusBadRequest, nil, scimBulkErrorBody(http.StatusBadRequest, scimInvalidPath, fmt.Sprintf("%s %s is not a bulk operation Flow supports (POST /Users or /Groups; PUT, PATCH or DELETE /Users/{id} or /Groups/{id})", method, operation.Path)))
	}
	var body []byte
	if method != http.MethodDelete {
		var data any
		if len(operation.Data) == 0 || json.Unmarshal(operation.Data, &data) != nil {
			return scimBulkResult(operation, http.StatusBadRequest, nil, scimBulkErrorBody(http.StatusBadRequest, scimInvalidSyntax, "data must be a JSON object"))
		}
		body, _ = json.Marshal(scimBulkResolve(data, resolved))
	}
	inner, err := http.NewRequestWithContext(r.Context(), method, strings.TrimSuffix(r.URL.Path, "/Bulk")+"/"+strings.Join(segments, "/"), bytes.NewReader(body))
	if err != nil {
		return scimBulkResult(operation, http.StatusBadRequest, nil, scimBulkErrorBody(http.StatusBadRequest, scimInvalidPath, "invalid path"))
	}
	inner.Host, inner.TLS, inner.RemoteAddr = r.Host, r.TLS, r.RemoteAddr
	inner.Header = r.Header.Clone()
	inner.Header.Del("If-Match")
	inner.Header.Del("If-None-Match")
	inner.Header.Del("Content-Length")
	inner.Header.Set("Content-Type", "application/scim+json")
	if version := strings.TrimSpace(operation.Version); version != "" {
		inner.Header.Set("If-Match", version)
	}
	inner.SetPathValue("workspace", r.PathValue("workspace"))
	if id != "" {
		inner.SetPathValue("id", id)
	}
	recorder := &scimBulkRecorder{header: http.Header{}}
	handler(recorder, inner)
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	if status >= 400 {
		errorBody := map[string]any{}
		if json.Unmarshal(recorder.body.Bytes(), &errorBody) != nil {
			errorBody = scimBulkErrorBody(status, "", strings.TrimSpace(recorder.body.String()))
		}
		return scimBulkResult(operation, status, nil, errorBody)
	}
	if recorder.header.Get("Location") == "" && id != "" {
		recorder.header.Set("Location", base+"/"+segments[0]+"/"+id)
	}
	if recorder.header.Get("Location") == "" && method == http.MethodPost {
		var created struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(recorder.body.Bytes(), &created) == nil && created.ID != "" {
			recorder.header.Set("Location", base+"/"+segments[0]+"/"+created.ID)
		}
	}
	return scimBulkResult(operation, status, recorder.header, nil)
}
