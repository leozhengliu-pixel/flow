package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

//go:embed flow_mcp_tools.json
var flowMCPToolInventory []byte

type flowMCPTool struct {
	Name        string          `json:"name"`
	Access      string          `json:"access"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type mcpRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpRPCResponse struct {
	JSONRPC string       `json:"jsonrpc"`
	ID      any          `json:"id,omitempty"`
	Result  any          `json:"result,omitempty"`
	Error   *mcpRPCError `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpActor struct {
	WorkspaceKey string
	User         domain.User
	APIKey       domain.APIKey
}

func (s *server) mcpHTTP(readonly bool) http.HandlerFunc {
	var tools []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &tools); err != nil {
		panic(fmt.Sprintf("parse Flow MCP tool inventory: %v", err))
	}
	for index := range tools {
		tools[index].Name = strings.TrimPrefix(tools[index].Name, "mcp__flow.")
	}
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
		if !decodeJSON(w, r, &request) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("MCP-Protocol-Version", "2025-11-25")
		if request.JSONRPC != "2.0" {
			s.writeMCPError(w, request.ID, -32600, "Invalid Request", nil)
			return
		}
		switch request.Method {
		case "initialize":
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]string{"name": "Flow", "version": version}, "instructions": "Manage Flow issues, projects, initiatives, documents, releases, reviews, teams, and comments."}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "ping":
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{}})
		case "tools/list":
			listed := make([]map[string]any, 0, len(tools))
			for _, tool := range tools {
				if readonly && tool.Access == "write" {
					continue
				}
				listed = append(listed, map[string]any{"name": tool.Name, "description": tool.Description, "inputSchema": json.RawMessage(tool.InputSchema), "annotations": map[string]any{"readOnlyHint": tool.Access == "read", "destructiveHint": strings.HasPrefix(tool.Name, "delete_") || tool.Name == "merge_diff"}})
			}
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": listed}})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(request.Params, &params); err != nil {
				s.writeMCPError(w, request.ID, -32602, "Invalid params", nil)
				return
			}
			if params.Arguments == nil {
				params.Arguments = map[string]any{}
			}
			index := slices.IndexFunc(tools, func(tool flowMCPTool) bool { return tool.Name == params.Name })
			if index < 0 {
				s.writeMCPError(w, request.ID, -32602, "Unknown tool", map[string]string{"name": params.Name})
				return
			}
			tool := tools[index]
			if !mcpToolAllowed(tool, actor.APIKey, readonly, params.Arguments) {
				s.writeMCPToolResult(w, request.ID, nil, fmt.Errorf("write scope is required"))
				return
			}
			ctx := context.WithValue(r.Context(), authUserContextKey{}, actor.User)
			ctx = context.WithValue(ctx, apiKeyContextKey{}, actor.APIKey)
			ctx = context.WithValue(ctx, workspaceKeyContextKey{}, actor.WorkspaceKey)
			ctx = store.ContextWithActor(ctx, actor.User)
			params.Arguments["__flowBaseURL"] = externalBaseURL(r)
			result, err := s.callFlowTool(ctx, actor, tool.Name, params.Arguments)
			s.writeMCPToolResult(w, request.ID, result, err)
		default:
			s.writeMCPError(w, request.ID, -32601, "Method not found", nil)
		}
	}
}

func mcpToolAllowed(tool flowMCPTool, key domain.APIKey, readonly bool, args map[string]any) bool {
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
	workspaceKey, key, ok := s.store.FindAPIKey(secretHash(strings.TrimSpace(header[len("Bearer "):])))
	if !ok || key.RevokedAt != nil || key.ExpiresAt != nil && !key.ExpiresAt.After(time.Now().UTC()) || key.Scopes != nil && !slices.ContainsFunc(key.Scopes, func(scope string) bool {
		return slices.Contains([]string{"read", "write", "admin", "create_issues", "create_comments"}, canonicalAPIKeyScope(scope))
	}) {
		s.mcpUnauthorized(w, r, "Token is invalid, expired, or lacks read access")
		return mcpActor{}, false
	}
	user, err := s.store.UserByID(r.Context(), key.CreatorID)
	if err != nil {
		s.mcpUnauthorized(w, r, "Token owner no longer exists")
		return mcpActor{}, false
	}
	data, ok, err := s.store.BootstrapForUser(r.Context(), workspaceKey, user.ID)
	if err != nil || !ok {
		s.mcpUnauthorized(w, r, "Token owner no longer has workspace access")
		return mcpActor{}, false
	}
	if key.AuthorizationID != "" && !slices.ContainsFunc(data.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool {
		return item.ID == key.AuthorizationID && item.RevokedAt == nil
	}) {
		s.mcpUnauthorized(w, r, "OAuth authorization has been revoked")
		return mcpActor{}, false
	}
	now := time.Now().UTC()
	_ = s.store.MutateWorkspace(r.Context(), workspaceKey, "api_key.used", key.ID, nil, func(next *domain.Bootstrap) error {
		if index := slices.IndexFunc(next.APIKeys, func(item domain.APIKey) bool { return item.ID == key.ID }); index >= 0 {
			next.APIKeys[index].LastUsedAt = &now
		}
		if index := slices.IndexFunc(next.OAuthAuthorizations, func(item domain.OAuthAuthorization) bool { return item.ID == key.AuthorizationID }); index >= 0 {
			next.OAuthAuthorizations[index].LastUsedAt = &now
		}
		return nil
	})
	return mcpActor{WorkspaceKey: workspaceKey, User: user, APIKey: key}, true
}

func (s *server) mcpUnauthorized(w http.ResponseWriter, r *http.Request, message string) {
	metadata := externalBaseURL(r) + "/.well-known/oauth-protected-resource/mcp"
	if strings.HasSuffix(r.URL.Path, "/readonly") {
		metadata += "/readonly"
	}
	w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+metadata+`"`)
	writeOAuthError(w, http.StatusUnauthorized, "invalid_token", message)
}

func (s *server) writeMCPError(w http.ResponseWriter, id any, code int, message string, data any) {
	writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Error: &mcpRPCError{Code: code, Message: message, Data: data}})
}

func (s *server) writeMCPToolResult(w http.ResponseWriter, id any, result any, err error) {
	if err != nil {
		writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true}})
		return
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		s.writeMCPError(w, id, -32603, "Internal error", nil)
		return
	}
	writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]string{{"type": "text", "text": string(raw)}}, "structuredContent": result, "isError": false}})
}
