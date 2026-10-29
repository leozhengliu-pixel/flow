package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// A loop run's setup and its tool calls, shared by both runtimes: executeLoopRun runs the model loop in this process,
// and a loop run on the agent worker (agent_worker_loop.go) has /mcp run each of its tool calls here. Either way a
// call goes through the same gates in the same order — the loop's scope, the output contract, document writes and
// their claims, title fields — and is recorded on the run the same way.

// loopRunContext is what a loop run's turns and tool calls run with.
type loopRunContext struct {
	s         *server
	workspace string
	loop      domain.Loop
	trigger   loopTrigger
	rec       *loopRunRecorder
	limits    loopRunLimits
	data      domain.Bootstrap
	// request acts as the loop owner with the loop's team-restricted key, its connectors, filters and extra tools.
	request  *http.Request
	scope    *loopScope
	readOnly map[string]bool
	contract *domain.LoopOutputContract
	plan     loopRunPlan
}

// loopConnectorSetup gives a run's request its connector tools: it returns the request's context with them, or the
// run's failure.
type loopConnectorSetup func(request *http.Request, data domain.Bootstrap, owner domain.User) (context.Context, error)

// inProcessLoopConnectors discovers the loop's selected connectors as its owner, as an in-process run does: only
// they are contacted, a skipped one is noticed on the run (or fails it, FLOW_LOOP_CONNECTOR_POLICY=required).
func (s *server) inProcessLoopConnectors(ctx context.Context, workspace string, loop domain.Loop, rec *loopRunRecorder) loopConnectorSetup {
	return func(request *http.Request, data domain.Bootstrap, owner domain.User) (context.Context, error) {
		discoveryStarted := time.Now()
		selection := connectorSelection{IDs: loop.ConnectorIDs, Partial: loopConnectorPolicy() != "required", Budget: loopConnectorDiscoveryBudget}
		connectorCtx, _, problems, discoverErr := s.withSelectedConnectorTools(request.Context(), data, connectorRequestContext{Workspace: workspace, UserID: owner.ID}, selection)
		rec.timing.DiscoveryMs = time.Since(discoveryStarted).Milliseconds()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if discoverErr != nil {
			return nil, &loopRunError{reason: "unavailable", message: "A selected connector is unavailable: " + discoverErr.Error(), err: discoverErr}
		}
		for _, problem := range problems {
			rec.addNotice(fmt.Sprintf("%s %s", loopConnectorSkippedNotice, problem.Err.Error()))
		}
		return connectorCtx, nil
	}
}

// prepareLoopRun loads what a loop run acts with: the owner and the workspace as they see it, the agent policy, the
// loop's scope and tools, and the output contract (resolved once and kept on the run).
func (s *server) prepareLoopRun(ctx context.Context, workspace string, loop domain.Loop, trigger loopTrigger, rec *loopRunRecorder, connectors loopConnectorSetup) (*loopRunContext, error) {
	limits := s.loopLimits()
	started := time.Now()
	directory, ok := s.store.WorkspaceMetadataFields(workspace, "users")
	if !ok {
		return nil, errNotFound
	}
	owner := userByID(&directory, loop.OwnerID)
	if owner == nil {
		return nil, &loopRunError{reason: "unavailable", message: "the loop owner is no longer in this workspace"}
	}
	// The run reads workspace metadata only; issues and discussions are read
	// through the paged tool queries, never loaded whole.
	var data domain.Bootstrap
	if s.authDisabled {
		data, ok = s.store.WorkspaceMetadata(workspace)
		if !ok {
			return nil, errNotFound
		}
		data.Viewer, data.ViewerRole = *owner, "admin"
	} else {
		var err error
		if data, err = s.store.PagedWorkspaceMetadata(ctx, workspace, owner.ID); err != nil {
			return nil, &loopRunError{reason: "unavailable", message: "the loop owner cannot access this workspace"}
		}
	}
	if err := agentWorkspacePolicy(data.WorkspaceSettings, data.ViewerRole); err != nil {
		return nil, &loopRunError{reason: "unavailable", message: err.Error(), err: err}
	}
	request, err := s.loopRunRequest(ctx, workspace, *owner)
	if err != nil {
		return nil, err
	}
	scope := s.newLoopScope(data, loop, trigger)
	rec.timing.LoadMs = time.Since(started).Milliseconds()
	connectorCtx, err := connectors(request, data, *owner)
	if err != nil {
		return nil, err
	}
	request = request.WithContext(connectorCtx)
	request = request.WithContext(context.WithValue(request.Context(), agentToolFilterKey{}, scope.offers))
	// "Team access" covers what the run can read, not only what it may change:
	// its tools run with a key restricted to the loop's teams, so lists,
	// searches and reads outside them come back empty or not found.
	request = request.WithContext(context.WithValue(request.Context(), apiKeyContextKey{}, scope.toolKey()))
	request = request.WithContext(context.WithValue(request.Context(), agentExtraToolsKey{}, append([]agentProviderTool{loopFinishToolDefinition}, loopGrantedTools()...)))
	request = request.WithContext(withAgentGrantedTools(request.Context(), loopGrantedToolNames))
	request = request.WithContext(withCodingLoopRun(request.Context(), loop, rec.run.ID, trigger, scope.codeAccess)) // coding sessions (track L)
	if loop.WebSearch && s.webSearchAvailable() {
		request = request.WithContext(withAgentWebTools(request.Context()))
	}
	toolNames, readOnly := s.loopRunTools(request.Context())
	// The run works to the loop's output contract, read once per text; a
	// reply on the run keeps the contract its run used.
	contract := rec.run.OutputContract
	if contract == nil {
		contractStarted := time.Now()
		documents := []domain.Document{}
		if stored, ok := s.store.WorkspaceMetadataFields(workspace, "documents"); ok {
			documents = stored.Documents
		}
		contract = resolveLoopContract(data, documents, loop, s.loopContractFor(ctx, workspace, loop), trigger, time.Now())
		rec.timing.LoadMs += time.Since(contractStarted).Milliseconds()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if rec.reply == nil {
			rec.setContract(contract)
		}
	}
	plan := loopRunPlan{Tools: toolNames, Limits: limits, Contract: contract}
	if loopModelFilledTitles(contract, "document") || loopModelFilledTitles(contract, "issue") {
		request = request.WithContext(context.WithValue(request.Context(), agentToolRewriteKey{}, loopTitleFieldsTools(contract)))
	}
	return &loopRunContext{s: s, workspace: workspace, loop: loop, trigger: trigger, rec: rec, limits: limits, data: data, request: request, scope: scope, readOnly: readOnly, contract: contract, plan: plan}, nil
}

// systemPrompt is the run's system prompt.
func (lc *loopRunContext) systemPrompt(ctx context.Context) string {
	entity := lc.s.loopEntityContext(ctx, lc.workspace, lc.data, lc.trigger)
	return loopSystemPrompt(lc.data, lc.loop, lc.trigger, entity, lc.scope, lc.s.webSearchAvailable(), lc.s.loopInstructionReferences(ctx, lc.workspace, lc.data, lc.loop), lc.plan) + loopReplySkillsPrompt(lc.rec.skills) + lc.rec.contextPrompt
}

// runToolCall runs one tool call of the run's model (not report_progress or finish_run, which the run handles itself)
// through the loop's gates, records it on the run and returns what the model reads.
func (lc *loopRunContext) runToolCall(ctx context.Context, call domain.AgentToolCall) (string, bool) {
	s, rec, request, data, scope, contract, readOnly, limits, workspace := lc.s, lc.rec, lc.request, lc.data, lc.scope, lc.contract, lc.readOnly, lc.limits, lc.workspace
	index := rec.startTool(request.Context(), call)
	status := "completed"
	var result []byte
	toolStarted := time.Now()
	callErr := scope.check(ctx, call)
	if callErr == nil && !readOnly[strings.TrimPrefix(call.Name, "mcp__flow.")] {
		if label, done := rec.completedWrites[loopWriteFingerprint(call.Name, loopToolArgs(call))]; done {
			callErr = fmt.Errorf("This change already succeeded earlier in this run (%s); it was not repeated. Continue with the steps that are not done yet", label)
		}
	}
	// The loop's own work follows its output contract; a person
	// replying on the run page may still ask for anything.
	name := strings.TrimPrefix(call.Name, "mcp__flow.")
	external := strings.HasPrefix(name, "external_")
	var output *loopOutputCall
	switch {
	case callErr != nil || rec.answering || contract == nil || readOnly[name]:
	case contract.ReadOnly && external:
		// A read-only loop's refusal covers connector tools, which can change data outside Flow.
		callErr = loopConnectorWriteRefusal(request.Context(), call)
	case contract.ReadOnly && loopWriteToolName(name):
		// "只回复总结文本即可，不要…写入内容": no change of any kind.
		callErr = fmt.Errorf("%s change anything in Flow; reply with the summary only. The call was not made", loopProhibitedCallPrefix)
	case !external:
		output, callErr = s.loopContractCallRefusal(ctx, rec, data, contract, call)
		if callErr == nil && output != nil && output.arguments != nil {
			// A new issue's title, team and project from the contract;
			// the team must still be one the loop can use.
			call.Arguments = output.arguments
			callErr = scope.check(ctx, call)
		}
	}
	var document *loopDocumentWrite
	if callErr == nil && name == "save_document" {
		document, callErr = s.prepareLoopDocumentWrite(rec, data, contract, call)
		if callErr == nil && document.arguments != nil {
			// The contract's title for this run and its team; the
			// team must still be one the loop can use.
			call.Arguments = document.arguments
			callErr = scope.check(ctx, call)
		}
	}
	if callErr == nil {
		// titleFields is the loop's own argument: the tools do not take it.
		call.Arguments = loopWithoutTitleFields(call.Arguments)
	}
	if callErr != nil {
		status = "blocked"
	} else if document != nil && document.applied != "" {
		result = document.appliedResult()
	} else {
		execute := func() error {
			result, callErr = s.executeAgentTool(request, data, call)
			return callErr
		}
		if readOnly[strings.TrimPrefix(call.Name, "mcp__flow.")] {
			// Reads are safe to repeat; changes are never retried.
			retries, _ := loopRetry(ctx, limits, execute)
			rec.addRetries(retries)
		} else {
			execute()
		}
		if callErr != nil {
			status = "error"
		} else if document != nil {
			s.verifyLoopDocumentWrite(workspace, document, result)
		} else if output != nil && output.record.ID == "" {
			output.record.ID = loopOutputResultID(result)
		}
		if document != nil {
			s.settleLoopDocumentClaim(workspace, document, callErr)
		}
	}
	rec.timing.ToolMs += time.Since(toolStarted).Milliseconds()
	write := !readOnly[strings.TrimPrefix(call.Name, "mcp__flow.")] && !strings.HasPrefix(call.Name, "external_")
	var record *domain.LoopRunDocumentWrite
	if document != nil {
		record = document.record
		// A write that was not repeated changes nothing now.
		write = write && (document.applied == "" || record.Changed)
	}
	callErr = publicToolError(ctx, call.Name, callErr)
	var written *domain.LoopRunOutputWrite
	if output != nil {
		written = output.record
	}
	rec.finishTool(index, status, callErr, write, loopToolArgs(call), record, written)
	if fingerprint := loopWriteFingerprint(call.Name, loopToolArgs(call)); write && status == "completed" && rec.completedWrites != nil && fingerprint != "" {
		rec.completedWrites[fingerprint] = loopToolLabel(call.Name, loopToolArgs(call))
	}
	content := string(result)
	if callErr != nil {
		content = agentToolFailureText(result, callErr)
	}
	return truncateLoopToolOutput(content, limits.MaxToolOutputBytes), callErr != nil
}

// progressStep records a report_progress call as a step of the run.
func (lc *loopRunContext) progressStep(call domain.AgentToolCall) {
	var progress struct {
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(call.Arguments, &progress)
	lc.rec.addStep(progress.Title, progress.Message)
}
