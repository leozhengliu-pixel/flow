package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/google/jsonschema-go/jsonschema"
)

//go:embed flow_mcp_tools.json
var flowMCPToolInventory []byte

type flowMCPTool struct {
	Name        string          `json:"name"`
	Access      string          `json:"access"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	validator   *jsonschema.Resolved
	// scalars are the top-level arguments typed only integer, number or boolean (normalizeFlowToolArgs).
	scalars map[string]string
}

type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpRPCResponse struct {
	JSONRPC string       `json:"jsonrpc"`
	ID      any          `json:"id"`
	Result  any          `json:"result,omitempty"`
	Error   *mcpRPCError `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpActor struct {
	WorkspaceKey        string
	User                domain.User
	APIKey              domain.APIKey
	AuthorizationUserID string
	// Run is set for an agent run token (agent_run_tokens.go): tool calls then act as the run's principal.
	Run *agentRunGrant
}

func (s *server) mcpHTTP(readonly bool) http.HandlerFunc {
	tools := flowToolContract()
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := s.authenticateMCP(w, r)
		if !ok {
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeOAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "MCP uses Streamable HTTP POST requests")
			return
		}
		var request mcpRPCRequest
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		stripRequestJSONNUL(r)
		decoder := json.NewDecoder(r.Body)
		var envelope json.RawMessage
		if err := decoder.Decode(&envelope); err != nil {
			s.writeMCPError(w, nil, -32700, "Parse error", nil)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			s.writeMCPError(w, nil, -32700, "Parse error", nil)
			return
		}
		decoder = json.NewDecoder(bytes.NewReader(envelope))
		decoder.UseNumber()
		if err := decoder.Decode(&request); err != nil {
			s.writeMCPError(w, nil, -32600, "Invalid Request", nil)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("MCP-Protocol-Version", "2025-11-25")
		if request.JSONRPC != "2.0" || request.Method == "" || request.ID != nil && !validMCPRequestID(request.ID) {
			s.writeMCPError(w, nil, -32600, "Invalid Request", nil)
			return
		}
		if request.ID == nil {
			if strings.HasPrefix(request.Method, "notifications/") {
				w.WriteHeader(http.StatusAccepted)
			} else {
				s.writeMCPError(w, nil, -32600, "Request ID is required", nil)
			}
			return
		}
		switch request.Method {
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if len(request.Params) > 0 && json.Unmarshal(request.Params, &params) != nil {
				s.writeMCPError(w, request.ID, -32602, "Invalid initialize parameters", nil)
				return
			}
			protocol := "2025-11-25"
			if slices.Contains([]string{"2025-03-26", "2025-06-18", "2025-11-25"}, params.ProtocolVersion) {
				protocol = params.ProtocolVersion
			}
			w.Header().Set("MCP-Protocol-Version", protocol)
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"protocolVersion": protocol, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]string{"name": "Flow", "version": version}, "instructions": "Manage Flow issues, projects, initiatives, cycles, documents, customers, releases, reviews, teams, views, loops, and comments."}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "ping":
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{}})
		case "tools/list":
			listed := make([]map[string]any, 0, len(tools))
			for _, tool := range tools {
				if readonly && tool.Access == "write" || actor.Run != nil && !actor.Run.allows(s, tool, readonly) {
					continue
				}
				listed = append(listed, map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": json.RawMessage(tool.InputSchema), "annotations": map[string]any{"readOnlyHint": tool.Access == "read", "destructiveHint": strings.HasPrefix(tool.Name, "delete_") || tool.Name == "merge_diff"}})
			}
			if actor.Run != nil {
				// A run token is also offered its acting user's connector tools (agent_runtime_connectors.go).
				listed = append(listed, s.agentRunListedConnectors(r, actor.Run, readonly)...)
				// And the tools its session gives it (agent_runtime_session_tools.go).
				for _, tool := range s.agentRunSessionTools(r.Context(), actor.Run.run) {
					if actor.Run.allows(s, tool, readonly) {
						listed = append(listed, map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": json.RawMessage(tool.InputSchema), "annotations": map[string]any{"readOnlyHint": tool.Access == "read", "destructiveHint": false}})
					}
				}
			}
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": listed}})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
				Meta      map[string]any `json:"_meta"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				s.writeMCPError(w, request.ID, -32602, "Invalid params", nil)
				return
			}
			if params.Arguments == nil {
				params.Arguments = map[string]any{}
			}
			if actor.Run != nil && strings.HasPrefix(params.Name, "external_") {
				s.callMCPRunConnector(w, r, request.ID, actor.Run, params.Name, params.Arguments, params.Meta, readonly)
				return
			}
			if actor.Run != nil {
				if tool, ok := s.agentRunSessionTool(r.Context(), actor.Run.run, params.Name); ok {
					if tool.Name == loopQuestionTool {
						// A question to the person, not a tool call: outside the reply's tool budget, as in-process.
						if err := checkFlowToolArgs(tool, params.Arguments); err != nil {
							s.writeMCPToolResult(w, r, request.ID, nil, err)
							return
						}
						s.callMCPRunLoopQuestion(w, r, request.ID, actor.Run, params.Arguments)
						return
					}
					s.callMCPRunTool(w, r, request.ID, actor.Run, tool, params.Arguments, readonly)
					return
				}
			}
			index := slices.IndexFunc(tools, func(tool flowMCPTool) bool { return tool.Name == params.Name })
			if index < 0 {
				s.writeMCPError(w, request.ID, -32602, "Unknown tool", map[string]string{"name": params.Name})
				return
			}
			tool := tools[index]
			if actor.Run != nil {
				s.callMCPRunTool(w, r, request.ID, actor.Run, tool, params.Arguments, readonly)
				return
			}
			if !mcpToolAllowed(tool, actor.APIKey, readonly, params.Arguments) {
				s.writeMCPToolResult(w, r, request.ID, nil, fmt.Errorf("write scope is required"))
				return
			}
			if err := checkFlowToolArgs(tool, params.Arguments); err != nil {
				s.writeMCPToolResult(w, r, request.ID, nil, err)
				return
			}
			ctx := context.WithValue(r.Context(), authUserContextKey{}, actor.User)
			ctx = context.WithValue(ctx, apiKeyContextKey{}, actor.APIKey)
			ctx = context.WithValue(ctx, workspaceKeyContextKey{}, actor.WorkspaceKey)
			ctx = store.ContextWithActor(ctx, actor.User)
			params.Arguments["__flowBaseURL"] = externalBaseURL(r)
			result, err := s.callFlowTool(ctx, actor, tool.Name, params.Arguments)
			s.writeMCPToolResult(w, r, request.ID, result, publicToolError(ctx, tool.Name, err))
		default:
			s.writeMCPError(w, request.ID, -32601, "Method not found", nil)
		}
	}
}

func validMCPRequestID(id any) bool {
	switch value := id.(type) {
	case string:
		return true
	case json.Number:
		number, err := strconv.ParseFloat(string(value), 64)
		return err == nil && !math.IsInf(number, 0) && math.Trunc(number) == number
	default:
		return false
	}
}

func mcpToolAllowed(tool flowMCPTool, key domain.APIKey, readonly bool, args map[string]any) bool {
	if key.Actor == "app" {
		if strings.Contains(tool.Name, "release") {
			if tool.Access == "read" {
				return apiKeyHasScope(key, "release:read")
			}
			if readonly || tool.Name != "save_release" {
				return false
			}
			if stringArg(args, "id") == "" {
				return apiKeyHasScope(key, "release:create")
			}
			if !apiKeyHasScope(key, "releaseStage:update") {
				return false
			}
			for field := range args {
				if field != "id" && field != "stage" {
					return false
				}
			}
			return stringArg(args, "stage") != ""
		}
		for _, group := range []string{"customer", "initiative", "release"} {
			if strings.Contains(tool.Name, group) {
				permission := group + ":read"
				writePermission := group + ":write"
				if group == "release" {
					writePermission = "release:create"
				}
				if tool.Access != "read" {
					return !readonly && apiKeyHasScope(key, writePermission)
				}
				return apiKeyHasScope(key, permission) || apiKeyHasScope(key, writePermission)
			}
		}
	}
	if tool.Access == "read" {
		return mcpAPIKeyHasScope(key, "read")
	}
	if readonly {
		return false
	}
	name := strings.ToLower(tool.Name)
	if strings.Contains(name, "save_issue") {
		if !mcpAPIKeyHasScope(key, "create_issues") {
			return false
		}
		// Creating/updating an issue is a narrower capability than mutating
		// links, relations, estimates, or release associations. Those
		// secondary resources require the broad write scope.
		return !mcpIssueNeedsWriteScope(args) || mcpAPIKeyHasScope(key, "write")
	}
	if strings.Contains(name, "save_comment") {
		if mcpCommentNeedsWriteScope(args) {
			return mcpAPIKeyHasScope(key, "write")
		}
		return mcpAPIKeyHasScope(key, "create_comments")
	}
	return mcpAPIKeyHasScope(key, "write")
}

func mcpIssueNeedsWriteScope(args map[string]any) bool {
	for _, name := range []string{"links", "blockedBy", "blocks", "relatedTo", "removeBlockedBy", "removeBlocks", "removeRelatedTo", "duplicateOf", "estimate", "addReleases", "removeReleases", "setReleases"} {
		if mcpArgumentHasValue(args, name) {
			return true
		}
	}
	return false
}

func mcpCommentNeedsWriteScope(args map[string]any) bool {
	// Updating/replying to an existing comment can target any resource, so
	// keep those operations behind the broad write permission.
	if mcpArgumentHasValue(args, "id") || mcpArgumentHasValue(args, "parentId") {
		return true
	}
	for _, name := range []string{"projectId", "initiativeId", "documentId", "milestoneId", "statusUpdateId", "statusUpdateType"} {
		if mcpArgumentHasValue(args, name) {
			return true
		}
	}
	return false
}

func mcpArgumentHasValue(args map[string]any, name string) bool {
	value, ok := args[name]
	if !ok || value == nil {
		return false
	}
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []any:
		return len(value) > 0
	case []string:
		return len(value) > 0
	default:
		return true
	}
}

func mcpAPIKeyHasScope(key domain.APIKey, required string) bool {
	// A nil scope is the full-access representation used by personal keys.
	if key.Scopes == nil {
		return true
	}
	if apiKeyHasScope(key, required) {
		return true
	}
	if required == "read" {
		return apiKeyHasScope(key, "write")
	}
	if required == "create_issues" || required == "create_comments" {
		return apiKeyHasScope(key, "write")
	}
	return false
}

func (s *server) authenticateMCP(w http.ResponseWriter, r *http.Request) (mcpActor, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		s.mcpUnauthorized(w, r, "Bearer token is required")
		return mcpActor{}, false
	}
	if token := strings.TrimSpace(header[len("Bearer "):]); strings.HasPrefix(token, agentRunTokenPrefix) {
		grant, err := s.authenticateAgentRunToken(r.Context(), token)
		if err != nil {
			if s.limitInvalidRunToken(w, r) {
				s.mcpUnauthorized(w, r, "Run token is invalid, expired, or its run has ended")
			}
			return mcpActor{}, false
		}
		if !s.limitAgentRun(w, r, grant.run) {
			return mcpActor{}, false
		}
		return mcpActor{WorkspaceKey: grant.run.WorkspaceKey, Run: grant}, true
	}
	authentication, err := s.store.AuthenticateAPIKeyRecord(r.Context(), "", secretHash(strings.TrimSpace(header[len("Bearer "):])), applicationApproved)
	key := authentication.Key
	if err != nil || key.Scopes != nil && !slices.ContainsFunc(key.Scopes, func(scope string) bool {
		return slices.Contains([]string{"read", "write", "admin", "create_issues", "create_comments"}, canonicalAPIKeyScope(scope))
	}) {
		s.mcpUnauthorized(w, r, "Token is invalid, expired, or lacks read access")
		return mcpActor{}, false
	}
	_ = s.store.RecordAPIKeyUse(r.Context(), authentication.Workspace.URLKey, key.ID, key.AuthorizationID)
	user := authentication.User
	if key.OAuthClientID != "" && key.Actor != "app" {
		// MCP clients authorized as the user: "<User> via <client>".
		name, icon := s.oauthClientDisplay(r, key.OAuthClientID)
		user.Via = &domain.ActorBot{ID: key.OAuthClientID, Type: domain.ActorBotTypeOAuthApplication, Name: name, AvatarURL: icon}
	}
	return mcpActor{WorkspaceKey: authentication.Workspace.URLKey, User: user, APIKey: key}, true
}

func (s *server) mcpUnauthorized(w http.ResponseWriter, r *http.Request, message string) {
	metadata := externalBaseURL(r) + "/.well-known/oauth-protected-resource/mcp"
	if strings.HasSuffix(r.URL.Path, "/readonly") {
		metadata += "/readonly"
	}
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+metadata+`"`)
	writeOAuthError(w, http.StatusUnauthorized, "invalid_token", message)
}

// mcpInvalidArguments is the tool error for arguments that fail the tool's input schema. MCP treats input
// validation as a tool execution error (isError), not a JSON-RPC protocol error: the reason reaches the model as the
// result text, so it can correct the call. Malformed JSON-RPC requests and unknown tools stay protocol errors.
func mcpInvalidArguments(tool flowMCPTool, args map[string]any, err error) error {
	hint := ""
	if mcpPatchNotAList(args) {
		hint = " " + mcpPatchShapeHint + "."
	}
	return fmt.Errorf("Invalid arguments for %s: %s.%s Check the tool's input schema and call it again with corrected arguments.", tool.Name, strings.TrimSuffix(err.Error(), "."), hint)
}

// mcpPatchShapeHint names the shape of the patch argument of the save tools
// (save_issue, save_project, save_initiative, save_document): a list of edits.
// The usual mistake is one edit passed as an object.
const mcpPatchShapeHint = `patch is a list of edits, for example [{"op":"append","text":"..."}]`

// mcpPatchNotAList reports a patch argument that is not a list of edits.
func mcpPatchNotAList(args map[string]any) bool {
	patch, present := args["patch"]
	if !present || patch == nil {
		return false
	}
	_, list := patch.([]any)
	return !list
}

func (s *server) writeMCPError(w http.ResponseWriter, id any, code int, message string, data any) {
	writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Error: &mcpRPCError{Code: code, Message: message, Data: data}})
}

// toolInternalError replaces a database or driver error in a tool result: the
// MCP client or agent model gets a generic sentence with the request id, the
// log has the error.
type toolInternalError struct {
	requestID string
	read      bool
}

func (e *toolInternalError) Error() string {
	if e.read {
		return fmt.Sprintf("Flow could not read this data because of an internal server error (request id %s). Try again later.", e.requestID)
	}
	return fmt.Sprintf("The change could not be saved because of an internal server error (request id %s). Try again later.", e.requestID)
}

// publicToolError is the error a tool call reports to an MCP client or an
// agent model. Tool errors are written for the caller ("issue not found",
// "title is required"), except database, driver and object-storage errors
// (isInternalError): their text names tables, columns, SQL states, buckets
// and paths, so they are logged with the request id and
// replaced, as is a 500 from a REST handler the tool ran in process. A call
// outside an HTTP request gets a fresh id for the log line.
func publicToolError(ctx context.Context, tool string, err error) error {
	var handlerErr *internalHandlerError
	if err == nil || !isInternalError(err) && !errors.As(err, &handlerErr) {
		return err
	}
	requestID := requestIDFrom(ctx)
	if requestID == "" {
		requestID = newRequestID()
	}
	tool = strings.TrimPrefix(tool, "mcp__flow.")
	slog.ErrorContext(ctx, "flow tool internal error", "request_id", requestID, "tool", tool, "error", err.Error())
	return &toolInternalError{requestID: requestID, read: agentChangeToolAccess()[tool] == "read"}
}

func (s *server) writeMCPToolResult(w http.ResponseWriter, r *http.Request, id any, result any, err error) {
	body, ok := mcpToolCallResult(result, publicToolError(r.Context(), "", err), nil)
	if !ok {
		s.writeMCPError(w, id, -32603, "Internal error", nil)
		return
	}
	writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Result: body})
}

// mcpToolCallResult is a tools/call result: the tool's JSON as text content and as structuredContent. The MCP spec
// requires structuredContent to be an object, and strict clients (Claude Code, the OpenAI Agents SDK) reject
// anything else, so a list result such as list_cycles' becomes {items: […]} and another non-object value
// {value: …}; the text content keeps the tool's own JSON. extra adds fields to structuredContent. ok is false when
// the result cannot be encoded.
func mcpToolCallResult(result any, err error, extra map[string]any) (map[string]any, bool) {
	if err != nil {
		return map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}, true
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return nil, false
	}
	body := map[string]any{"content": []map[string]string{{"type": "text", "text": string(raw)}}, "isError": false}
	if structured := mcpStructuredContent(raw, extra); structured != nil {
		body["structuredContent"] = structured
	}
	return body, true
}

func mcpStructuredContent(raw json.RawMessage, extra map[string]any) any {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		if len(extra) == 0 {
			return nil
		}
		return extra
	}
	if trimmed[0] != '{' {
		key := "value"
		if trimmed[0] == '[' {
			key = "items"
		}
		wrapped := map[string]any{key: raw}
		maps.Copy(wrapped, extra)
		return wrapped
	}
	if len(extra) == 0 {
		return raw
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return raw
	}
	maps.Copy(object, extra)
	return object
}

// callMCPRunTool answers tools/call for an agent run token. An Idempotency-Key header (the worker sends
// "<runId>:<toolUseId>") makes a retried write return the first result instead of writing twice.
func (s *server) callMCPRunTool(w http.ResponseWriter, r *http.Request, id any, grant *agentRunGrant, tool flowMCPTool, args map[string]any, readonly bool) {
	if !grant.allows(s, tool, readonly) {
		err := fmt.Errorf("%s is not available to this agent run", tool.Name)
		s.recordAgentRunFlowWrite(r.Context(), grant.run, tool.Name, nil, nil, agentWriteNotRun, err.Error())
		s.writeMCPToolResult(w, r, id, nil, err)
		return
	}
	if err := checkFlowToolArgs(tool, args); err != nil {
		s.writeMCPToolResult(w, r, id, nil, err)
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 160 {
		s.writeMCPError(w, id, -32602, "Idempotency-Key is too long", nil)
		return
	}
	if key != "" && tool.Access == "write" {
		if cached, found, err := s.store.AgentRunKey(r.Context(), grant.run.ID, "mcp:"+key); err == nil && found {
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Result: json.RawMessage(cached)})
			return
		}
		// The call's ledger entries are kept under its key: a retry replaces them (agent_run_outcomes.go).
		r = r.WithContext(withAgentRunCallKey(r.Context(), key))
	}
	// A chat reply's tool budget (agent_worker_chat.go): a call over it, or a repeat of a failed one, is not run.
	if content, run := s.workerChatToolBefore(r.Context(), grant.run, tool.Name, args); !run {
		s.recordAgentRunFlowWrite(r.Context(), grant.run, tool.Name, nil, nil, agentWriteNotRun, content)
		s.writeMCPToolResult(w, r, id, nil, errors.New(content))
		return
	}
	result, changes, err := s.callAgentRunTool(r, grant, tool.Name, args)
	if err != nil {
		s.workerChatToolAfter(r.Context(), grant.run, tool.Name, args, publicToolError(r.Context(), tool.Name, err).Error(), true)
	} else {
		s.workerChatToolAfter(r.Context(), grant.run, tool.Name, args, "", false)
	}
	var extra map[string]any
	switch {
	case len(changes) == 1:
		extra = map[string]any{"change": changes[0]}
	case len(changes) > 1:
		extra = map[string]any{"changes": changes}
	}
	err = publicToolError(r.Context(), tool.Name, err)
	body, ok := mcpToolCallResult(result, err, extra)
	if !ok {
		s.writeMCPError(w, id, -32603, "Internal error", nil)
		return
	}
	if key != "" && tool.Access == "write" && err == nil {
		if raw, marshalErr := json.Marshal(body); marshalErr == nil {
			_ = s.store.SaveAgentRunKey(r.Context(), grant.run, "mcp:"+key, raw)
		}
	}
	writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Result: body})
}
