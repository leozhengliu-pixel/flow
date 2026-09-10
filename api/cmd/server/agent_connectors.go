package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"flow/api/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type connectorTool struct {
	Definition agentProviderTool
	Policy     applicationPolicy
	RemoteName string
}
type connectorToolsKey struct{}

func connectorAllowed(settings domain.WorkspaceSettings, item applicationPolicy, userID string) bool {
	return settings.MCPConnectorsEnabled && !settings.HIPAACompliance && item.Kind == "mcp" && item.Status != "rejected" &&
		(item.Shared || item.OwnerID == userID) && (settings.AllowedMCPConnectors != "approved" || item.Status == "approved")
}

// Credentials are deployment-owned and bound to the registered URL. Workspace
// metadata contains no token or arbitrary environment-variable references.
func connectorToken(item applicationPolicy) string {
	var credentials map[string]struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	_ = json.Unmarshal([]byte(os.Getenv("FLOW_MCP_CREDENTIALS")), &credentials)
	value := credentials[item.ID]
	if strings.TrimRight(value.URL, "/") != strings.TrimRight(item.URL, "/") {
		return ""
	}
	return value.Token
}

type remoteMCPClient struct {
	session *mcp.ClientSession
	client  *http.Client
}
type connectorRequestContext struct {
	Workspace, UserID string
	Elicit            func(context.Context, applicationPolicy, *mcp.ElicitParams) (*mcp.ElicitResult, error)
}
type connectorContextKey struct{}
type connectorTransport struct {
	base            http.RoundTripper
	endpoint, token string
	headers         map[string]string
	allowLocal      bool
}

func (t connectorTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t connectorTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !integrationEndpointSafe(r.Context(), r.URL.String(), t.allowLocal) {
		return nil, fmt.Errorf("unsafe MCP destination")
	}
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	if strings.TrimRight(r.URL.String(), "/") == strings.TrimRight(t.endpoint, "/") {
		if t.token != "" {
			clone.Header.Set("Authorization", "Bearer "+t.token)
		}
		for k, v := range t.headers {
			clone.Header.Set(k, v)
		}
	}
	response, err := t.base.RoundTrip(clone)
	if err == nil && response.StatusCode != http.StatusSwitchingProtocols {
		body := response.Body
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(body, 2<<20), body}
	}
	return response, err
}
func (s *server) openConnector(ctx context.Context, item applicationPolicy) (*remoteMCPClient, error) {
	requestContext, _ := ctx.Value(connectorContextKey{}).(connectorRequestContext)
	token := connectorToken(item)
	headers := map[string]string{}
	if requestContext.Workspace != "" {
		credential, err := s.connectorCredential(ctx, requestContext.Workspace, requestContext.UserID, item)
		if err != nil {
			return nil, err
		}
		if credential != nil {
			if credential.Token != nil {
				token = credential.Token.AccessToken
			}
			headers = credential.Headers
		}
	}
	httpClient := secureOutboundClient(0)
	if s.authDisabled && safeLocalDevelopmentURL(item.URL) {
		httpClient = &http.Client{Transport: http.DefaultTransport}
	}
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClient.Transport = connectorTransport{base: base, endpoint: item.URL, token: token, headers: headers, allowLocal: s.authDisabled}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	options := &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}}
	if requestContext.Elicit != nil {
		options.Capabilities.Elicitation = &mcp.ElicitationCapabilities{Form: &mcp.FormElicitationCapabilities{}, URL: &mcp.URLElicitationCapabilities{}}
		options.ElicitationHandler = func(ctx context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return requestContext.Elicit(ctx, item, request.Params)
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "Flow", Version: "1"}, options)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: item.URL, HTTPClient: httpClient, MaxRetries: 1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		httpClient.CloseIdleConnections()
		return nil, fmt.Errorf("MCP connection failed; check authorization and server availability: %w", err)
	}
	return &remoteMCPClient{session: session, client: httpClient}, nil
}
func (c *remoteMCPClient) close() { _ = c.session.Close(); c.client.CloseIdleConnections() }
func (c *remoteMCPClient) rpc(ctx context.Context, method string, params any, _ bool) (json.RawMessage, error) {
	raw, _ := json.Marshal(params)
	var result any
	var err error
	switch method {
	case "tools/list":
		var input mcp.ListToolsParams
		if json.Unmarshal(raw, &input) != nil {
			return nil, errInvalid
		}
		result, err = c.session.ListTools(ctx, &input)
	case "tools/call":
		var input mcp.CallToolParams
		if json.Unmarshal(raw, &input) != nil {
			return nil, errInvalid
		}
		result, err = c.session.CallTool(ctx, &input)
	default:
		return nil, fmt.Errorf("unsupported MCP operation")
	}
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if len(encoded) > 1<<20 {
		return nil, fmt.Errorf("MCP response exceeds limit")
	}
	return encoded, err
}

func (s *server) discoverConnectorTools(ctx context.Context, data domain.Bootstrap) ([]connectorTool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	items := []connectorTool{}
	if !s.agent.ToolsEnabled || !s.agent.WriteTools {
		return items, nil
	}
	for _, policy := range applicationPolicies(&data) {
		if !connectorAllowed(data.WorkspaceSettings, policy, data.Viewer.ID) {
			continue
		}
		client, err := s.openConnector(ctx, policy)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", policy.Name, err)
		}
		defer client.close()
		cursor := ""
		for page := 0; page < 10; page++ {
			params := map[string]string{}
			if cursor != "" {
				params["cursor"] = cursor
			}
			raw, err := client.rpc(ctx, "tools/list", params, false)
			if err != nil {
				return nil, err
			}
			var result struct {
				Tools []struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					InputSchema json.RawMessage `json:"inputSchema"`
				} `json:"tools"`
				NextCursor string `json:"nextCursor"`
			}
			if json.Unmarshal(raw, &result) != nil {
				return nil, fmt.Errorf("invalid MCP tool inventory")
			}
			for _, tool := range result.Tools {
				var schema map[string]json.RawMessage
				if tool.Name == "" || len(tool.Name) > 256 || len(tool.InputSchema) > 64<<10 || json.Unmarshal(tool.InputSchema, &schema) != nil || schema == nil {
					return nil, fmt.Errorf("invalid MCP tool definition")
				}
				hash := sha256.Sum256([]byte(policy.ID + "\x00" + tool.Name))
				items = append(items, connectorTool{Definition: agentProviderTool{Name: fmt.Sprintf("external_%x", hash[:16]), Description: truncateSettingsText(policy.Name+": "+tool.Description, 2000), Parameters: tool.InputSchema, Access: "write"}, Policy: policy, RemoteName: tool.Name})
				if len(items) > 128 {
					return nil, fmt.Errorf("too many MCP tools (maximum 128)")
				}
			}
			if result.NextCursor == "" {
				break
			}
			if result.NextCursor == cursor || page == 9 {
				return nil, fmt.Errorf("MCP inventory pagination exceeded limit")
			}
			cursor = result.NextCursor
		}
	}
	return items, nil
}

func (s *server) executeConnectorTool(r *http.Request, data domain.Bootstrap, call domain.AgentToolCall) ([]byte, error) {
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	if !s.agent.WriteTools {
		return nil, fmt.Errorf("External MCP tools require write-tool access and approval")
	}
	items, _ := r.Context().Value(connectorToolsKey{}).([]connectorTool)
	for _, tool := range items {
		if tool.Definition.Name == call.Name {
			fresh, ok := s.store.WorkspaceMetadata(workspaceKey(r))
			if !ok {
				return nil, errNotFound
			}
			allowed := false
			for _, policy := range applicationPolicies(&fresh) {
				if policy.ID == tool.Policy.ID && policy.URL == tool.Policy.URL && connectorAllowed(fresh.WorkspaceSettings, policy, data.Viewer.ID) {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, fmt.Errorf("MCP connection access was revoked")
			}
			client, err := s.openConnector(r.Context(), tool.Policy)
			if err != nil {
				return nil, err
			}
			defer client.close()
			var args map[string]any
			if json.Unmarshal(call.Arguments, &args) != nil || args == nil {
				return nil, fmt.Errorf("invalid MCP tool arguments")
			}
			result, err := client.rpc(r.Context(), "tools/call", map[string]any{"name": tool.RemoteName, "arguments": args}, false)
			if err != nil {
				return nil, err
			}
			var outcome struct {
				IsError bool `json:"isError"`
			}
			if json.Unmarshal(result, &outcome) != nil {
				return nil, fmt.Errorf("invalid MCP tool result")
			}
			if outcome.IsError {
				return result, fmt.Errorf("MCP tool reported a failure")
			}
			return result, nil
		}
	}
	return nil, fmt.Errorf("unknown MCP tool")
}
