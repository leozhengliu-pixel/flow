package main

import (
	"bufio"
	"bytes"
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
	if strings.TrimRight(value.URL, "/") != item.URL {
		return ""
	}
	return value.Token
}

type remoteMCPClient struct {
	server            *server
	policy            applicationPolicy
	session, protocol string
	client            *http.Client
}

func (s *server) openConnector(ctx context.Context, item applicationPolicy) (*remoteMCPClient, error) {
	client := secureOutboundClient(20 * time.Second)
	if s.authDisabled && safeLocalDevelopmentURL(item.URL) {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c := &remoteMCPClient{server: s, policy: item, client: client}
	result, err := c.rpc(ctx, "initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "Flow", "version": "1"}}, false)
	if err != nil {
		return nil, err
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(result, &initialized) != nil {
		return nil, fmt.Errorf("invalid MCP initialization")
	}
	switch initialized.ProtocolVersion {
	case "2025-11-25", "2025-06-18", "2025-03-26":
		c.protocol = initialized.ProtocolVersion
	default:
		return nil, fmt.Errorf("unsupported MCP protocol version")
	}
	_, err = c.rpc(ctx, "notifications/initialized", nil, true)
	return c, err
}

func (c *remoteMCPClient) rpc(ctx context.Context, method string, params any, notification bool) (json.RawMessage, error) {
	if !integrationEndpointSafe(ctx, c.policy.URL, c.server.authDisabled) {
		return nil, fmt.Errorf("unsafe MCP endpoint")
	}
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if !notification {
		payload["id"] = 1
	}
	if params != nil {
		payload["params"] = params
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.policy.URL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	if c.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", c.protocol)
	}
	if token := connectorToken(c.policy); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("MCP server unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("MCP server returned HTTP %d", response.StatusCode)
	}
	if method == "initialize" {
		c.session = response.Header.Get("Mcp-Session-Id")
	}
	if notification {
		return nil, nil
	}
	decode := func(raw []byte) (json.RawMessage, error) {
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return nil, fmt.Errorf("invalid MCP response")
		}
		if string(envelope.ID) != "1" {
			return nil, nil
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("MCP request failed (%d)", envelope.Error.Code)
		}
		return envelope.Result, nil
	}
	if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(io.LimitReader(response.Body, 2<<20))
		scanner.Buffer(make([]byte, 4096), 1<<20)
		var event strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if event.Len() > 0 {
					result, e := decode([]byte(event.String()))
					event.Reset()
					if e != nil || result != nil {
						return result, e
					}
				}
			} else if strings.HasPrefix(line, "data:") {
				event.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
				event.WriteByte('\n')
			}
		}
		return nil, fmt.Errorf("MCP stream ended without a result")
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, fmt.Errorf("MCP response exceeds limit")
	}
	result, err := decode(raw)
	if err == nil && result == nil {
		err = fmt.Errorf("missing MCP result")
	}
	return result, err
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
		defer client.client.CloseIdleConnections()
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
			defer client.client.CloseIdleConnections()
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
