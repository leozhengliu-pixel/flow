package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/llmproxy"
	"flow/api/internal/store"
)

// Coding-session MCP gateway (docs/coding-sessions-security.md "MCP
// servers"): the harness in a sandbox reaches the workspace's approved MCP
// connectors only through Flow, the same way model traffic goes through the
// LLM proxy and pushes through the git relay.
//
//	harness ──(streamable HTTP, Bearer sandbox token)──▶ /internal/coding-runtime/mcp/{server}
//	Flow ──(stored credential, outbound policy, pinned dial)──▶ the MCP server
//
// The sandbox never holds the connector's credential and needs no network
// route to it (an intranet MCP stays reachable from Flow only, under the
// operator's FLOW_TRUSTED_PRIVATE_ENDPOINTS). Every request re-checks the
// sandbox token (refused once the session or sandbox ends) and the policy
// (refused once an admin revokes it), and every tools/call is audited.
//
// The gateway is stateless: it answers initialize and ping itself and opens
// an upstream session per tools/list or tools/call, so nothing outlives a
// request. Server-to-client streams (GET) are not offered.
//
// Owner: coding sessions. Keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingMCPGatewayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /internal/coding-runtime/mcp/{server}", s.codingMCPGateway)
	mux.HandleFunc("GET /internal/coding-runtime/mcp/{server}", codingMCPGatewayNoStream)
	mux.HandleFunc("DELETE /internal/coding-runtime/mcp/{server}", codingMCPGatewayNoStream)
}

// codingMCPGatewayMaxBody caps one JSON-RPC request from the harness.
const codingMCPGatewayMaxBody = 1 << 20

// codingMCPGatewayTimeout bounds one forwarded request.
const codingMCPGatewayTimeout = 6 * time.Minute

// codingMCPServerAllowed reports whether a coding session started by
// creatorID may use policy through the gateway: an approved, shared MCP
// connector the workspace allows (connectorAllowed), which a workspace admin
// enabled for coding sessions or the operator listed in
// FLOW_CODING_MCP_SERVERS.
func codingMCPServerAllowed(settings domain.WorkspaceSettings, policy applicationPolicy, creatorID string, operatorIDs []string) bool {
	return policy.Kind == "mcp" && policy.Status == "approved" && policy.Shared && policy.URL != "" &&
		(policy.CodingSessions || slices.Contains(operatorIDs, policy.ID)) &&
		connectorAllowed(settings, policy, creatorID)
}

func (s *server) codingOperatorMCPServers() []string {
	if s.coding == nil {
		return nil
	}
	return s.coding.config.MCPServers
}

// codingMCPServers lists the session's gateway servers for the bootstrap.
func (s *server) codingMCPServers(_ context.Context, session domain.CodingSession, runtimeURL string) []coding.BootstrapMCPServer {
	data, ok := s.store.WorkspaceMetadata(session.WorkspaceKey)
	if !ok {
		return nil
	}
	operator := s.codingOperatorMCPServers()
	servers := []coding.BootstrapMCPServer{}
	used := map[string]bool{"flow": true}
	for _, policy := range applicationPolicies(&data) {
		if !codingMCPServerAllowed(data.WorkspaceSettings, policy, session.CreatorID, operator) {
			continue
		}
		name := codingMCPServerName(policy)
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s-%d", strings.TrimSuffix(codingMCPServerName(policy), "-"), i)
		}
		used[name] = true
		servers = append(servers, coding.BootstrapMCPServer{Name: name, Title: policy.Name, URL: strings.TrimRight(runtimeURL, "/") + "/internal/coding-runtime/mcp/" + url.PathEscape(policy.ID)})
		if len(servers) == 20 {
			break
		}
	}
	return servers
}

// codingMCPServerName is a harness config key for policy: its name in
// lower-case [a-z0-9-], never "flow" (the runner's own server).
func codingMCPServerName(policy applicationPolicy) string {
	var out strings.Builder
	dash := false
	for _, r := range strings.ToLower(policy.Name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			out.WriteRune(r)
			dash = false
		case !dash && out.Len() > 0:
			out.WriteByte('-')
			dash = true
		}
		if out.Len() >= 32 {
			break
		}
	}
	name := strings.Trim(out.String(), "-")
	if name == "" || name == "flow" {
		hash := sha256.Sum256([]byte(policy.ID))
		name = fmt.Sprintf("mcp-%x", hash[:4])
	}
	return name
}

func codingMCPGatewayNoStream(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", http.MethodPost)
	http.Error(w, "The Flow MCP gateway does not offer server-initiated streams", http.StatusMethodNotAllowed)
}

type codingMCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type codingMCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type codingMCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *codingMCPError `json:"error,omitempty"`
}

// codingMCPGatewayVersions are the protocol versions the gateway speaks to
// the harness (the upstream session negotiates its own).
var codingMCPGatewayVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

func (s *server) codingMCPGateway(w http.ResponseWriter, r *http.Request) {
	if !s.codingEnabled() || s.coding.service == nil {
		http.NotFound(w, r)
		return
	}
	caller, err := s.authenticateCodingSandbox(r.Context(), codingSandboxBearer(r))
	if errors.Is(err, llmproxy.ErrUnauthenticated) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "A live coding session's sandbox token is required", "code": "tokenInvalid"})
		return
	}
	if err != nil {
		log.Printf("coding MCP gateway: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Coding sessions: internal error"})
		return
	}
	policy, ok := s.codingMCPGatewayPolicy(caller, r.PathValue("server"))
	if !ok {
		// Unknown, unapproved or another workspace's server: the same answer.
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "This MCP server is not available to this coding session", "code": "mcpServerNotAllowed"})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, codingMCPGatewayMaxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "MCP request exceeds limit"})
		return
	}
	var request codingMCPRequest
	if json.Unmarshal(raw, &request) != nil || request.JSONRPC != "2.0" || request.Method == "" {
		writeJSON(w, http.StatusBadRequest, codingMCPResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &codingMCPError{Code: -32600, Message: "Invalid Request (batches are not supported)"}})
		return
	}
	if len(request.ID) == 0 {
		// Notifications (initialized, cancelled, …) need no upstream.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rpcErr := s.codingMCPGatewayCall(r.Context(), caller, policy, request)
	response := codingMCPResponse{JSONRPC: "2.0", ID: request.ID, Result: result, Error: rpcErr}
	if rpcErr == nil && result == nil {
		response.Result = map[string]any{}
	}
	writeJSON(w, http.StatusOK, response)
}

// codingMCPGatewayPolicy finds the server in the session's own workspace,
// re-reading the policies so a revoked approval applies to the next call.
func (s *server) codingMCPGatewayPolicy(caller codingSandboxCaller, id string) (applicationPolicy, bool) {
	if id == "" {
		return applicationPolicy{}, false
	}
	data, ok := s.store.WorkspaceMetadata(caller.grant.WorkspaceKey)
	if !ok {
		return applicationPolicy{}, false
	}
	for _, policy := range applicationPolicies(&data) {
		if policy.ID == id {
			return policy, codingMCPServerAllowed(data.WorkspaceSettings, policy, caller.session.CreatorID, s.codingOperatorMCPServers())
		}
	}
	return applicationPolicy{}, false
}

func (s *server) codingMCPGatewayCall(ctx context.Context, caller codingSandboxCaller, policy applicationPolicy, request codingMCPRequest) (any, *codingMCPError) {
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		version := codingMCPGatewayVersions[0]
		if slices.Contains(codingMCPGatewayVersions, params.ProtocolVersion) {
			version = params.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": codingMCPServerName(policy), "title": policy.Name, "version": "1"},
			"instructions":    policy.Name + " (relayed by Flow; calls are audited).",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list", "tools/call":
	default:
		return nil, &codingMCPError{Code: -32601, Message: "Method not found: " + request.Method}
	}
	var params map[string]any
	if len(request.Params) > 0 && json.Unmarshal(request.Params, &params) != nil {
		return nil, &codingMCPError{Code: -32602, Message: "Invalid params"}
	}
	if params == nil {
		params = map[string]any{}
	}
	tool, _ := params["name"].(string)
	if request.Method == "tools/call" && tool == "" {
		return nil, &codingMCPError{Code: -32602, Message: "Invalid params: name is required"}
	}
	ctx, cancel := context.WithTimeout(ctx, codingMCPGatewayTimeout)
	defer cancel()
	// The creator's per-user connector credential (or the deployment's
	// FLOW_MCP_CREDENTIALS entry); elicitation is not offered in sandboxes.
	ctx = context.WithValue(ctx, connectorContextKey{}, connectorRequestContext{Workspace: caller.grant.WorkspaceKey, UserID: caller.session.CreatorID})
	started := time.Now()
	result, err := s.codingMCPGatewayForward(ctx, policy, request.Method, params)
	if request.Method == "tools/call" {
		s.auditCodingMCPToolCall(caller, policy, tool, result, err, time.Since(started))
	}
	if err != nil {
		log.Printf("coding MCP gateway session=%s server=%s %s: %v", caller.session.ID, policy.ID, request.Method, err)
		message := "The MCP server could not be reached from Flow"
		if errors.Is(err, errInvalid) {
			message = "Invalid MCP request"
		}
		return nil, &codingMCPError{Code: -32603, Message: message}
	}
	return result, nil
}

// codingMCPGatewayForward opens the upstream session (agent_connectors.go:
// the outbound policy's pinned dialer, the stored credential bound to the
// registered URL) and forwards one request.
func (s *server) codingMCPGatewayForward(ctx context.Context, policy applicationPolicy, method string, params map[string]any) (json.RawMessage, error) {
	client, err := s.openConnector(ctx, policy)
	if err != nil {
		return nil, err
	}
	defer client.close()
	return client.rpc(ctx, method, params, false)
}

// auditCodingMCPToolCall records one relayed tool call in the workspace
// audit log (arguments and results are not stored: they can hold code and
// data the audit log must not keep).
func (s *server) auditCodingMCPToolCall(caller codingSandboxCaller, policy applicationPolicy, tool string, result json.RawMessage, callErr error, elapsed time.Duration) {
	outcome := "ok"
	var reported struct {
		IsError bool `json:"isError"`
	}
	switch {
	case callErr != nil:
		outcome = "failed"
	case json.Unmarshal(result, &reported) == nil && reported.IsError:
		outcome = "toolError"
	}
	metadata := map[string]any{
		"mcpServerId": policy.ID, "mcpServer": policy.Name, "tool": tool, "outcome": outcome,
		"sandboxId": caller.grant.SandboxID, "userId": caller.session.CreatorID, "durationMs": elapsed.Milliseconds(),
	}
	ctx := store.WithMetadataFields(context.Background(), "auditLog")
	if err := s.store.MutateWorkspace(ctx, caller.grant.WorkspaceKey, "audit.coding_mcp", caller.session.ID, map[string]any{"action": "coding_session.mcp_tool_call"}, func(data *domain.Bootstrap) error {
		appendAudit(data, "coding_session.mcp_tool_call", "coding_session", caller.session.ID, metadata)
		return nil
	}); err != nil {
		log.Printf("coding MCP gateway audit workspace=%s session=%s: %v", caller.grant.WorkspaceKey, caller.session.ID, err)
	}
}
