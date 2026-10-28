package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Connector (MCP) tools on worker runs. The in-process agent offers the acting user's MCP connector tools
// (withConnectorTools / withSelectedConnectorTools) and runs them through executeConnectorTool; a worker run reaches
// tools only through /mcp with its run token, so /mcp offers the same tools to a run token, and only to one:
//
//   - discovery: once per run (cached by run id), as the run's principal user (the chat user, the person who
//     delegated or mentioned the agent, the loop owner), with that user's credentials. A loop run contacts only
//     the connectors its principal selects (agentRunPrincipal.Connectors, the loop's ConnectorIDs: none when
//     empty); other runs discover every connector the user may use, narrowed by Connectors when set. A run whose
//     tool allowlist is set gets connector tools only when it lists agentRunConnectorAllowlist.
//   - tools/list: the discovered tools as external_<hash> with the connector's input schema; a tool whose
//     connector is set to always ask carries _meta["flow/approval"] = "always" (the worker holds those calls).
//   - tools/call: the call runs through executeAgentTool → executeConnectorTool like an in-process call — access is
//     re-checked (connectorAllowed: workspace MCP switch, HIPAA mode, approval status, shared or owned), arguments
//     are validated against the tool's inputSchema, and the per-call timeout and size limits apply. A call to an
//     always-ask tool runs only with _meta["flow/approved"] = true (the worker sends it once the person approved);
//     otherwise it is refused without contacting the connector. The thread gets the same action activities as an
//     in-process call (connector, arguments, ≤2 KB result), the workspace audit log records the call (no
//     arguments or results), and the outcome is kept for the run guard (a structured isError counts as failed).
//
//   - a selected connector that discovery skipped is recorded as a run notice (the payload's `notices`, and a loop
//     run record's notices), as an in-process loop run records it;
//   - a chat run's connector may ask its person for input mid-call (MCP elicitation, agent_runtime_elicitations.go);
//     other runs offer no elicitation, as in-process threads and loops do not.
//
// The connector's own result passes through (an isError result keeps its text and isError: true); the in-process
// agent gives its model the same text (agentToolFailureText).

// agentRunConnectorAllowlist in a run's tool allowlist (agentRunPrincipal.Tools) admits its connector tools.
const agentRunConnectorAllowlist = "external_*"

// MCP _meta keys of connector tools on worker runs.
const (
	agentRunApprovalMeta = "flow/approval"
	agentRunApprovedMeta = "flow/approved"
)

// agentRunConnectorTTL is how long a run's discovered connector tools are reused; a longer run discovers again
// (access is re-checked on every call regardless).
const agentRunConnectorTTL = 10 * time.Minute

type agentRunConnectorEntry struct {
	ready chan struct{}
	tools []connectorTool
	err   error
	at    time.Time
}

// agentRunConnectorCache holds each run's discovered connector tools (run id → *agentRunConnectorEntry).
var agentRunConnectorCache sync.Map

// allowsConnectors reports whether the run may be offered connector tools at all.
func (g *agentRunGrant) allowsConnectors() bool {
	return len(g.principal.Tools) == 0 || slices.Contains(g.principal.Tools, agentRunConnectorAllowlist)
}

// agentRunConnectorTools are the connector tools of a run, discovered once per run. A failed discovery is not
// cached.
func (s *server) agentRunConnectorTools(ctx context.Context, grant *agentRunGrant) ([]connectorTool, error) {
	if !grant.allowsConnectors() || !s.agent.ToolsEnabled || !s.agent.WriteTools {
		return nil, nil
	}
	for {
		entry := &agentRunConnectorEntry{ready: make(chan struct{})}
		value, loaded := agentRunConnectorCache.LoadOrStore(grant.run.ID, entry)
		if loaded {
			existing := value.(*agentRunConnectorEntry)
			select {
			case <-existing.ready:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			if existing.err == nil && time.Since(existing.at) < agentRunConnectorTTL {
				return existing.tools, nil
			}
			agentRunConnectorCache.CompareAndDelete(grant.run.ID, existing)
			continue
		}
		sweepAgentRunConnectorCache()
		entry.tools, entry.err = s.discoverAgentRunConnectors(ctx, grant)
		entry.at = time.Now()
		close(entry.ready)
		if entry.err != nil {
			agentRunConnectorCache.CompareAndDelete(grant.run.ID, entry)
		}
		return entry.tools, entry.err
	}
}

func sweepAgentRunConnectorCache() {
	agentRunConnectorCache.Range(func(key, value any) bool {
		entry := value.(*agentRunConnectorEntry)
		select {
		case <-entry.ready:
			if time.Since(entry.at) >= agentRunConnectorTTL {
				agentRunConnectorCache.CompareAndDelete(key, entry)
			}
		default:
		}
		return true
	})
}

// discoverAgentRunConnectors discovers a run's connector tools as its principal user.
func (s *server) discoverAgentRunConnectors(ctx context.Context, grant *agentRunGrant) ([]connectorTool, error) {
	data, ok := s.store.WorkspaceMetadata(grant.run.WorkspaceKey)
	if !ok {
		return nil, errNotFound
	}
	request := connectorRequestContext{Workspace: grant.run.WorkspaceKey, UserID: grant.principal.UserID}
	if grant.run.Kind == store.AgentRunKindLoop || len(grant.principal.Connectors) > 0 {
		// Only the selected connectors are contacted, as for an in-process loop run.
		selection := connectorSelection{IDs: grant.principal.Connectors, Partial: loopConnectorPolicy() != "required", Budget: loopConnectorDiscoveryBudget}
		_, tools, problems, err := s.withSelectedConnectorTools(ctx, data, request, selection)
		if err == nil && len(problems) > 0 {
			// As an in-process loop run records it: a notice on the run per skipped connector.
			notices := make([]string, 0, len(problems))
			for _, problem := range problems {
				notices = append(notices, fmt.Sprintf("%s %s", loopConnectorSkippedNotice, problem.Err.Error()))
			}
			s.addAgentRunNotices(ctx, grant.run, notices)
		}
		return tools, err
	}
	_, tools, err := s.withConnectorTools(ctx, data, request)
	return tools, err
}

// agentRunNoticesKey keeps a run's notices (a selected connector that was skipped), returned with its payload.
const agentRunNoticesKey = "run:notices"

// agentRunNotices are the notices recorded on a run so far.
func (s *server) agentRunNotices(ctx context.Context, runID string) []string {
	raw, found, err := s.store.AgentRunKey(ctx, runID, agentRunNoticesKey)
	if err != nil || !found {
		return nil
	}
	var notices []string
	_ = json.Unmarshal(raw, &notices)
	return notices
}

// addAgentRunNotices records notices on a worker run as the in-process loop run records them (loopRunRecorder.addNotice):
// on the run (its payload lists them) and, for a loop run whose session is a running loop run record, on that
// record with a "notice" event, so the run page shows them.
func (s *server) addAgentRunNotices(ctx context.Context, run store.AgentRun, notices []string) {
	ctx = context.WithoutCancel(ctx)
	current := s.agentRunNotices(ctx, run.ID)
	added := []string{}
	for _, notice := range notices {
		if !slices.Contains(current, notice) && !slices.Contains(added, notice) {
			added = append(added, notice)
		}
	}
	if len(added) == 0 {
		return
	}
	raw, _ := json.Marshal(append(current, added...))
	if err := s.store.ReplaceAgentRunKey(ctx, run, agentRunNoticesKey, raw); err != nil {
		log.Printf("agent run %s: notices not recorded: %v", run.ID, err)
	}
	for _, notice := range added {
		log.Printf("agent run %s: %s", run.ID, notice)
	}
	if run.Kind != store.AgentRunKindLoop {
		return
	}
	events := make([]store.LoopRunEvent, 0, len(added))
	for _, notice := range added {
		events = append(events, loopRunEvent("notice", map[string]string{"notice": notice}))
	}
	changed := false
	updated, err := s.store.UpdateLoopRun(ctx, run.WorkspaceKey, run.SessionID, events, func(record *domain.LoopRun) error {
		if record.Status != "running" {
			return store.ErrNoMutation
		}
		for _, notice := range added {
			if !slices.Contains(record.Notices, notice) {
				record.Notices = append(record.Notices, notice)
				changed = true
			}
		}
		if !changed {
			return store.ErrNoMutation
		}
		return nil
	})
	if err == nil && changed {
		s.publishLoopRunEvent(run.WorkspaceKey, "loop_run.progress", updated)
	}
}

// agentRunListedConnectors is the tools/list entries of a run's connector tools; nil when the run has none or
// they cannot be discovered (the run then works without them, as an in-process run does).
func (s *server) agentRunListedConnectors(r *http.Request, grant *agentRunGrant, readonly bool) []map[string]any {
	if readonly || !grant.allowsConnectors() {
		return nil
	}
	if _, _, err := s.agentRunRequest(r, grant); err != nil {
		return nil
	}
	tools, err := s.agentRunConnectorTools(r.Context(), grant)
	if err != nil {
		log.Printf("agent run %s: connector discovery failed: %v", grant.run.ID, err)
		return nil
	}
	listed := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		meta := map[string]any{"flow/connector": tool.Policy.label()}
		if tool.needsApproval() {
			meta[agentRunApprovalMeta] = connectorToolApprovalAlways
		}
		listed = append(listed, map[string]any{"name": tool.Definition.Name, "description": tool.Definition.Description, "inputSchema": tool.Definition.Parameters, "annotations": map[string]any{"readOnlyHint": tool.ReadOnly}, "_meta": meta})
	}
	return listed
}

// agentRunConnectorNote is the system-prompt note of a run whose connector tools could not be discovered (err, as
// agentRunConnectorTools returned it; the in-process thread agent adds the same note).
func agentRunConnectorNote(grant *agentRunGrant, err error) string {
	if err == nil || !grant.allowsConnectors() {
		return ""
	}
	return "\nThe workspace's connected MCP tools are unavailable in this session (" + truncateSettingsText(err.Error(), 300) + "). If the request needs them, say so.\n"
}

// callMCPRunConnector answers tools/call of a connector tool for a run token.
func (s *server) callMCPRunConnector(w http.ResponseWriter, r *http.Request, id any, grant *agentRunGrant, name string, args map[string]any, meta map[string]any, readonly bool) {
	fail := func(err error) { s.writeMCPToolResult(w, id, nil, err) }
	if readonly || !grant.allowsConnectors() || !s.agent.WriteTools {
		fail(fmt.Errorf("%s is not available to this agent run", name))
		return
	}
	request, data, err := s.agentRunRequest(r, grant)
	if err != nil {
		fail(fmt.Errorf("the run's user or application no longer has access"))
		return
	}
	tools, err := s.agentRunConnectorTools(request.Context(), grant)
	if err != nil {
		fail(fmt.Errorf("The workspace's connected MCP tools are unavailable in this session: %s", truncateSettingsText(err.Error(), 300)))
		return
	}
	index := slices.IndexFunc(tools, func(tool connectorTool) bool { return tool.Definition.Name == name })
	if index < 0 {
		fail(fmt.Errorf("MCP tool is no longer available; its connection was removed or access was revoked"))
		return
	}
	tool := tools[index]
	if tool.needsApproval() && meta[agentRunApprovedMeta] != true {
		fail(fmt.Errorf("%s: %s waits for the user's approval (its connection is set to always ask); it was not run", tool.Policy.label(), tool.RemoteName))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 160 {
		s.writeMCPError(w, id, -32602, "Idempotency-Key is too long", nil)
		return
	}
	if key != "" {
		if cached, found, err := s.store.AgentRunKey(r.Context(), grant.run.ID, "mcp:"+key); err == nil && found {
			writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Result: json.RawMessage(cached)})
			return
		}
	}
	arguments, err := json.Marshal(args)
	if err != nil {
		fail(fmt.Errorf("invalid MCP tool arguments"))
		return
	}
	call := domain.AgentToolCall{ID: "run_" + grant.run.ID, Name: name, Arguments: arguments}
	appendActivity := s.agentRunActivityAppender(request.Context(), grant.run)
	metadata := connectorActionMetadata(tool, call)
	if err := appendActivity(domain.AgentActivity{Type: "action", Body: tool.RemoteName, Ephemeral: true, Metadata: maps.Clone(metadata)}); err != nil {
		fail(fmt.Errorf("the session no longer accepts this run's activity (it was stopped or is waiting for the person)"))
		return
	}
	// A connector call may change data outside Flow: the run is never retried automatically after it.
	if err := s.store.MarkAgentRunWrote(request.Context(), grant.run.ID); err != nil {
		fail(err)
		return
	}
	// A chat run's connector may ask its person mid-call (agent_runtime_elicitations.go); other runs' may not.
	connectorContext := connectorRequestContext{Workspace: grant.run.WorkspaceKey, UserID: grant.principal.UserID, Elicit: s.agentRunElicit(grant)}
	ctx := context.WithValue(request.Context(), connectorContextKey{}, connectorContext)
	ctx = context.WithValue(ctx, connectorToolsKey{}, tools)
	started := time.Now()
	result, callErr := s.executeAgentTool(request.WithContext(ctx), data, call)
	s.recordAgentRunConnectorOutcome(r.Context(), grant.run, name, args, callErr != nil)
	s.auditAgentRunConnectorCall(grant, tool, result, callErr, time.Since(started))
	metadata["result"] = connectorActionResult(result, callErr)
	_ = appendActivity(domain.AgentActivity{Type: "action", Body: tool.RemoteName, Metadata: metadata})

	var body any
	switch {
	case callErr == nil && json.Valid(result):
		body = json.RawMessage(result)
	case callErr != nil && len(result) > 0 && json.Valid(result):
		// The connector reported a failure (isError): its own result, flagged.
		var parsed map[string]any
		if json.Unmarshal(result, &parsed) == nil && parsed != nil {
			parsed["isError"] = true
			body = parsed
		}
	}
	if body == nil {
		encoded, ok := mcpToolCallResult(nil, callErr, nil)
		if callErr == nil {
			encoded, ok = mcpToolCallResult(string(result), nil, nil)
		}
		if !ok {
			s.writeMCPError(w, id, -32603, "Internal error", nil)
			return
		}
		body = encoded
	}
	if key != "" && callErr == nil {
		if raw, err := json.Marshal(body); err == nil {
			_ = s.store.SaveAgentRunKey(r.Context(), grant.run, "mcp:"+key, raw)
		}
	}
	writeJSON(w, http.StatusOK, mcpRPCResponse{JSONRPC: "2.0", ID: id, Result: body})
}

// agentRunActivityAppender appends activities to a run's agent session (comment thread or delegation) as its
// application, like the in-process agent; a run without a session (chat) records none.
func (s *server) agentRunActivityAppender(ctx context.Context, run store.AgentRun) func(domain.AgentActivity) error {
	task, err := s.store.AgentTask(ctx, run.WorkspaceKey, run.SessionID)
	if err != nil {
		return func(domain.AgentActivity) error { return nil }
	}
	var mu sync.Mutex
	return func(activity domain.AgentActivity) error {
		mu.Lock()
		defer mu.Unlock()
		current, err := s.store.ApplicationByUser(ctx, task.AppUserID)
		if err != nil || !current.Active || !slices.Contains(current.AccessTeamIDs(), task.TeamID) {
			return store.ErrAuthForbidden
		}
		if fresh, err := s.store.AgentTask(ctx, task.WorkspaceKey, task.ID); err == nil {
			task = fresh
		}
		activity.ActorID = task.AppUserID
		next, err := appendBuiltinActivity(ctx, s.store, task, activity)
		if err == nil {
			task = next
		}
		return err
	}
}

// agentRunConnectorOutcomeKey is where the outcome of a run's connector call is kept for the run guard.
func agentRunConnectorOutcomeKey(name string, args map[string]any) string {
	canonical, _ := json.Marshal(args)
	hash := sha256.Sum256([]byte(name + "\x00" + string(canonical)))
	return "connector:outcome:" + hex.EncodeToString(hash[:16])
}

func (s *server) recordAgentRunConnectorOutcome(ctx context.Context, run store.AgentRun, name string, args map[string]any, failed bool) {
	raw, _ := json.Marshal(map[string]bool{"isError": failed})
	if err := s.store.ReplaceAgentRunKey(context.WithoutCancel(ctx), run, agentRunConnectorOutcomeKey(name, args), raw); err != nil {
		log.Printf("agent run %s: connector outcome not recorded: %v", run.ID, err)
	}
}

// agentRunConnectorFailed reports whether the run's last call of tool with these arguments (as the model sent
// them) failed when it ran through /mcp — an error or an isError result, whatever its text says.
func (s *server) agentRunConnectorFailed(ctx context.Context, run store.AgentRun, name, arguments string) bool {
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return false
	}
	if args == nil {
		args = map[string]any{}
	}
	raw, found, err := s.store.AgentRunKey(ctx, run.ID, agentRunConnectorOutcomeKey(name, args))
	if err != nil || !found {
		return false
	}
	var outcome struct {
		IsError bool `json:"isError"`
	}
	return json.Unmarshal(raw, &outcome) == nil && outcome.IsError
}

// auditAgentRunConnectorCall records one connector call of a worker run in the workspace audit log (arguments
// and results are not stored).
func (s *server) auditAgentRunConnectorCall(grant *agentRunGrant, tool connectorTool, result []byte, callErr error, elapsed time.Duration) {
	outcome := "ok"
	var reported struct {
		IsError bool `json:"isError"`
	}
	switch {
	case len(result) > 0 && json.Unmarshal(result, &reported) == nil && reported.IsError:
		outcome = "toolError"
	case callErr != nil:
		outcome = "failed"
	}
	metadata := map[string]any{
		"mcpServerId": tool.Policy.ID, "mcpServer": tool.Policy.Name, "tool": tool.RemoteName, "outcome": outcome,
		"runId": grant.run.ID, "sessionId": grant.run.SessionID, "userId": grant.principal.UserID, "durationMs": elapsed.Milliseconds(),
	}
	ctx := store.WithMetadataFields(context.Background(), "auditLog")
	if err := s.store.MutateWorkspace(ctx, grant.run.WorkspaceKey, "audit.agent_run_mcp", grant.run.ID, map[string]any{"action": "agent_run.mcp_tool_call"}, func(data *domain.Bootstrap) error {
		appendAudit(data, "agent_run.mcp_tool_call", "agent_run", grant.run.ID, metadata)
		return nil
	}); err != nil {
		log.Printf("agent run %s: connector call audit: %v", grant.run.ID, err)
	}
}

// agentRunConnectorTool is the connector tool behind name for a run (the guard's "Not run" activity names it).
func (s *server) agentRunConnectorTool(ctx context.Context, run store.AgentRun, name string) (connectorTool, bool) {
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		return connectorTool{}, false
	}
	tools, err := s.agentRunConnectorTools(ctx, &agentRunGrant{run: run, principal: principal})
	if err != nil {
		return connectorTool{}, false
	}
	index := slices.IndexFunc(tools, func(tool connectorTool) bool { return tool.Definition.Name == name })
	if index < 0 {
		return connectorTool{}, false
	}
	return tools[index], true
}
