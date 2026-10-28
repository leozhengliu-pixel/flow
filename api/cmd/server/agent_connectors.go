package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type connectorTool struct {
	Definition agentProviderTool
	Policy     applicationPolicy
	RemoteName string
	// ReadOnly is whether Flow treats the tool as read-only: the workspace connector policy's override for it
	// (applicationPolicy.ToolAccess), else its MCP readOnlyHint, true only when the connector declares that the
	// tool does not modify its environment. The MCP default (no annotations, or readOnlyHint false) is a tool that
	// may write.
	ReadOnly bool
	// Annotated is true when the connector sent the tool's annotations at all.
	Annotated bool
	// AccessOverride is the policy's override that set ReadOnly ("read" or "write"), or "" when the annotation did.
	AccessOverride string
}
type connectorToolsKey struct{}

// needsApproval reports whether a call to the tool waits for the user. Connector tools run automatically,
// whatever their MCP annotations say, unless their connector is set to always ask.
func (t connectorTool) needsApproval() bool {
	return t.Policy.ToolApproval == connectorToolApprovalAlways
}

const (
	// maxConnectorTools bounds the connector tools one agent run is offered.
	maxConnectorTools = 128
	// connectorCallTimeout bounds one connector tool call.
	connectorCallTimeout = 6 * time.Minute
	// maxConnectorResultBytes bounds one connector response (a tools/list page or a tool result).
	maxConnectorResultBytes = 1 << 20
)

func connectorAllowed(settings domain.WorkspaceSettings, item applicationPolicy, userID string) bool {
	return settings.MCPConnectorsActive() && item.Kind == "mcp" && item.Status != "rejected" &&
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
	// unauthorized records that the MCP endpoint answered 401 Unauthorized.
	unauthorized *atomic.Bool
}

// errConnectorUnauthorized marks a connection the MCP server refused for
// missing or rejected credentials: the user needs to (re)authorize it.
var errConnectorUnauthorized = errors.New("MCP server requires authorization")

func (t connectorTransport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t connectorTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !integrationEndpointSafe(r.Context(), r.URL.String(), t.allowLocal, outboundTrustedPrivate) {
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
	if err == nil && response.StatusCode == http.StatusUnauthorized && t.unauthorized != nil && strings.TrimRight(r.URL.String(), "/") == strings.TrimRight(t.endpoint, "/") {
		t.unauthorized.Store(true)
	}
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
	httpClient := secureOutboundClient(0, outboundTrustedPrivate)
	if s.authDisabled && safeLocalDevelopmentURL(item.URL) {
		httpClient = &http.Client{Transport: http.DefaultTransport}
	}
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	unauthorized := &atomic.Bool{}
	httpClient.Transport = connectorTransport{base: base, endpoint: item.URL, token: token, headers: headers, allowLocal: s.authDisabled, unauthorized: unauthorized}
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
		if unauthorized.Load() {
			return nil, fmt.Errorf("%w: %w", errConnectorUnauthorized, err)
		}
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
	if len(encoded) > maxConnectorResultBytes {
		return nil, fmt.Errorf("MCP response exceeds limit")
	}
	return encoded, err
}

// withConnectorTools offers the MCP connector tools the acting user may use to the agent turns run with the
// returned context. Chat, loops and delegated threads share it: connectors are discovered once per run as
// request.UserID (the chat user, the loop owner, or the person who delegated or mentioned the agent), with that
// user's credentials, so a connector removed or revoked since the last run is no longer offered, and
// executeConnectorTool re-checks access on every call. On a discovery failure the context carries no tools.
func (s *server) withConnectorTools(ctx context.Context, data domain.Bootstrap, request connectorRequestContext) (context.Context, []connectorTool, error) {
	ctx = context.WithValue(ctx, connectorContextKey{}, request)
	tools, err := s.discoverConnectorTools(ctx, data, request.UserID)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, connectorToolsKey{}, tools), tools, nil
}

// offeredConnectorTool is the connector tool behind an external_ tool name offered in ctx.
func offeredConnectorTool(ctx context.Context, name string) (connectorTool, bool) {
	items, _ := ctx.Value(connectorToolsKey{}).([]connectorTool)
	for _, tool := range items {
		if tool.Definition.Name == name {
			return tool, true
		}
	}
	return connectorTool{}, false
}

// connectorDiscoveryBudget bounds one discovery: connecting to and listing
// every connector it covers.
const connectorDiscoveryBudget = 30 * time.Second

// discoverConnectorTools lists the tools of the MCP connectors userID may use (at most maxConnectorTools).
func (s *server) discoverConnectorTools(ctx context.Context, data domain.Bootstrap, userID string) ([]connectorTool, error) {
	ctx, cancel := context.WithTimeout(ctx, connectorDiscoveryBudget)
	defer cancel()
	items := []connectorTool{}
	if !s.agent.ToolsEnabled || !s.agent.WriteTools {
		return items, nil
	}
	for _, policy := range applicationPolicies(&data) {
		if !connectorAllowed(data.WorkspaceSettings, policy, userID) {
			continue
		}
		tools, err := s.listConnectorTools(ctx, policy)
		if err != nil {
			return nil, err
		}
		items = append(items, tools...)
		if len(items) > maxConnectorTools {
			return nil, fmt.Errorf("too many MCP tools (maximum %d)", maxConnectorTools)
		}
	}
	return items, nil
}

// annotateConnectorCall names an external connector tool call after its connector and the tool's own name, so the
// chat can label a row whose Name is only an opaque id. Other calls, and connector tools no longer offered, are left as is.
func annotateConnectorCall(ctx context.Context, call *domain.AgentToolCall) {
	if call == nil || !strings.HasPrefix(call.Name, "external_") {
		return
	}
	if tool, ok := offeredConnectorTool(ctx, call.Name); ok {
		call.Connector, call.RemoteTool = tool.Policy.label(), tool.RemoteName
	}
}

// listConnectorTools connects to one connector and lists its tools.
func (s *server) listConnectorTools(ctx context.Context, policy applicationPolicy) ([]connectorTool, error) {
	client, err := s.openConnector(ctx, policy)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", policy.label(), err)
	}
	defer client.close()
	items := []connectorTool{}
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
				Annotations *struct {
					ReadOnlyHint *bool `json:"readOnlyHint"`
				} `json:"annotations"`
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
			item := connectorTool{Definition: agentProviderTool{Name: fmt.Sprintf("external_%x", hash[:16]), Description: connectorToolDescription(policy.label(), tool.Name, tool.Description, tool.InputSchema), Parameters: tool.InputSchema, Access: "write"}, Policy: policy, RemoteName: tool.Name}
			if tool.Annotations != nil {
				item.Annotated = true
				item.ReadOnly = tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint
			}
			// The workspace admin's decision wins over the connector's own annotation.
			switch item.AccessOverride = policy.ToolAccess[tool.Name]; item.AccessOverride {
			case connectorToolRead:
				item.ReadOnly = true
			case connectorToolWrite:
				item.ReadOnly = false
			default:
				item.AccessOverride = ""
			}
			items = append(items, item)
			if len(items) > maxConnectorTools {
				return nil, fmt.Errorf("too many MCP tools (maximum %d)", maxConnectorTools)
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
	return items, nil
}

// connectorProblem is a selected connector whose tools could not be offered.
type connectorProblem struct {
	Policy applicationPolicy
	Err    error
}

// connectorSelection narrows discovery to chosen connectors (a loop's
// ConnectorIDs). Partial keeps the tools of the connectors that answered when
// another selected one fails; otherwise any failure fails the discovery.
type connectorSelection struct {
	IDs     []string
	Partial bool
	Budget  time.Duration
}

// withSelectedConnectorTools offers the tools of the selected connectors the
// caller may use. The set comes from the selection and request.UserID's
// access before any network request, so an empty selection contacts no
// server and unselected connectors (slow, unreachable or with expired
// credentials) never delay the run. The selected connectors are discovered
// concurrently within one budget and stop with ctx. A selected connector the
// caller may not use, or one that fails, is returned as a problem (Partial)
// or as the error. The returned context always carries exactly the tools
// returned, so executeConnectorTool only runs offered tools.
func (s *server) withSelectedConnectorTools(ctx context.Context, data domain.Bootstrap, request connectorRequestContext, selection connectorSelection) (context.Context, []connectorTool, []connectorProblem, error) {
	ctx = context.WithValue(ctx, connectorContextKey{}, request)
	tools := []connectorTool{}
	problems := []connectorProblem{}
	chosen := []applicationPolicy{}
	if s.agent.ToolsEnabled && s.agent.WriteTools && len(selection.IDs) > 0 {
		policies := applicationPolicies(&data)
		for _, id := range uniqueAgentIDs(selection.IDs) {
			index := slices.IndexFunc(policies, func(policy applicationPolicy) bool { return policy.ID == id })
			switch {
			case index < 0:
				problems = append(problems, connectorProblem{Policy: applicationPolicy{ID: id, Name: id}, Err: fmt.Errorf("%s: the connection no longer exists", id)})
			case !connectorAllowed(data.WorkspaceSettings, policies[index], request.UserID):
				problems = append(problems, connectorProblem{Policy: policies[index], Err: fmt.Errorf("%s: the connection is not available to this user", policies[index].label())})
			default:
				chosen = append(chosen, policies[index])
			}
		}
	}
	if len(chosen) > 0 {
		budget := selection.Budget
		if budget <= 0 {
			budget = connectorDiscoveryBudget
		}
		discoverCtx, cancel := context.WithTimeout(ctx, budget)
		type outcome struct {
			index int
			tools []connectorTool
			err   error
		}
		// Buffered: a connection that ignores the deadline finishes (and
		// closes its client) in the background without blocking the run.
		outcomes := make(chan outcome, len(chosen))
		for index, policy := range chosen {
			go func() {
				tools, err := s.listConnectorTools(discoverCtx, policy)
				outcomes <- outcome{index: index, tools: tools, err: err}
			}()
		}
		results := make([][]connectorTool, len(chosen))
		failures := make([]error, len(chosen))
		finished := make([]bool, len(chosen))
	collect:
		for pending := len(chosen); pending > 0; pending-- {
			select {
			case result := <-outcomes:
				results[result.index], failures[result.index], finished[result.index] = result.tools, result.err, true
			case <-discoverCtx.Done():
				break collect
			}
		}
		stopped := discoverCtx.Err()
		cancel()
		for index := range chosen {
			if !finished[index] {
				failures[index] = fmt.Errorf("discovery did not finish within %s: %w", budget, stopped)
			}
		}
		for index, policy := range chosen {
			err := failures[index]
			if err == nil && len(tools)+len(results[index]) > maxConnectorTools {
				err = fmt.Errorf("too many MCP tools (maximum %d)", maxConnectorTools)
			}
			if err != nil {
				if !strings.HasPrefix(err.Error(), policy.label()+":") {
					err = fmt.Errorf("%s: %w", policy.label(), err)
				}
				problems = append(problems, connectorProblem{Policy: policy, Err: err})
				continue
			}
			tools = append(tools, results[index]...)
		}
	}
	if err := ctx.Err(); err != nil {
		return ctx, nil, problems, err
	}
	if len(problems) > 0 && !selection.Partial {
		return ctx, nil, problems, problems[0].Err
	}
	return context.WithValue(ctx, connectorToolsKey{}, tools), tools, problems, nil
}

func (s *server) executeConnectorTool(r *http.Request, data domain.Bootstrap, call domain.AgentToolCall) ([]byte, error) {
	ctx, cancel := context.WithTimeout(r.Context(), connectorCallTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if !s.agent.WriteTools {
		return nil, fmt.Errorf("External MCP tools require write-tool access and approval")
	}
	tool, ok := offeredConnectorTool(r.Context(), call.Name)
	if !ok {
		// Tools are discovered per run: a held call resumed after its connector was removed or revoked is not offered.
		return nil, fmt.Errorf("MCP tool is no longer available; its connection was removed or access was revoked")
	}
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
	var args map[string]any
	if json.Unmarshal(call.Arguments, &args) != nil || args == nil {
		return nil, fmt.Errorf("invalid MCP tool arguments")
	}
	// Arguments the tool's schema rejects (a property it does not have, a missing required one) are refused here
	// with what the tool accepts, instead of being sent upstream.
	if err := validateConnectorArguments(tool.RemoteName, tool.Definition.Parameters, args); err != nil {
		return nil, err
	}
	client, err := s.openConnector(r.Context(), tool.Policy)
	if err != nil {
		return nil, err
	}
	defer client.close()
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
		return result, errConnectorReportedFailure
	}
	return result, nil
}

// errConnectorReportedFailure is the error of a connector call whose result is flagged isError: the call failed,
// and the result returned with it is the connector's own account of why.
var errConnectorReportedFailure = errors.New("MCP tool reported a failure")

// connectorFailureText is what the model reads for a call the connector reported as failed (isError): the
// connector's own result text, as a worker run's model reads it through /mcp. ok is false for other errors and
// for a flagged result without text (the model then reads the error).
func connectorFailureText(result []byte, err error) (string, bool) {
	var parsed struct {
		Content []json.RawMessage `json:"content"`
	}
	if !errors.Is(err, errConnectorReportedFailure) || json.Unmarshal(result, &parsed) != nil || len(parsed.Content) == 0 {
		return "", false
	}
	text := connectorResultText(result, len(result))
	return text, strings.TrimSpace(text) != ""
}

// agentToolFailureText is a failed tool call's result for the model: the connector's own text when a connector
// reported the failure, the error otherwise.
func agentToolFailureText(result []byte, err error) string {
	if text, ok := connectorFailureText(result, err); ok {
		return text
	}
	return publicToolError(context.Background(), "", err).Error()
}

// connectorActionMetadata is the action activity of a connector call (connector, tool and arguments), shared by the
// in-process agent and worker runs; the result is added once the call ran (connectorActionResult).
func connectorActionMetadata(tool connectorTool, call domain.AgentToolCall) map[string]any {
	parameter := tool.Policy.Name
	if args := strings.TrimSpace(string(call.Arguments)); args != "" && args != "{}" {
		parameter += " " + truncateSettingsText(args, 500)
	}
	return map[string]any{"action": tool.RemoteName, "parameter": parameter, "connector": tool.Policy.Name}
}

// connectorActionResult is a connector call's result for its action activity: the text content (at most 2 KB),
// or the error with the connector's own text.
func connectorActionResult(result []byte, err error) string {
	if err == nil {
		return connectorResultText(result, 2000)
	}
	logged := "Error: " + err.Error()
	if detail := connectorResultText(result, 2000); detail != "" {
		logged += "\n" + detail
	}
	return logged
}

// connectorNotRunResult is the action activity result of a connector call the run guard answered without running
// it (an identical repeat or a call over budget).
func connectorNotRunResult(content string) string {
	return "Not run: " + truncateSettingsText(strings.SplitN(content, "\n", 2)[0], 300)
}

// connectorResultText is a connector tool result's text content for the activity log, at most limit bytes.
func connectorResultText(result []byte, limit int) string {
	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	text := string(result)
	if json.Unmarshal(result, &parsed) == nil && len(parsed.Content) > 0 {
		parts := []string{}
		for _, item := range parsed.Content {
			if item.Type == "text" && strings.TrimSpace(item.Text) != "" {
				parts = append(parts, strings.TrimSpace(item.Text))
			}
		}
		text = strings.Join(parts, "\n")
	}
	if len(text) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return text
}
