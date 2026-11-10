package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/store"
)

// Workspace AI policy. Everything that sends workspace content to a model or
// another AI service (the Flow Agent provider, media transcription, speech,
// the coding LLM proxy, the agent worker) answers to one policy,
// agentWorkspacePolicy: HIPAA mode off, the workspace's AI switch on, and no
// guest when the workspace keeps agents from guests. Request paths check it
// before they read or send anything; background work (triage suggestions,
// loop descriptions and output contracts, comment actions, worker runs,
// coding sessions) checks it again when it runs, so turning AI off stops
// work that was queued before.
//
// ai_policy_test.go lists every function that reaches a model with the gate
// that covers it and fails when a new one appears without an entry.

// aiPolicyRefusalMessage is the 403 answer of every AI entry point the
// policy refuses (the /api/agent routes answer it too).
const aiPolicyRefusalMessage = "Agent access is disabled by workspace policy"

// aiPolicyPath reports whether a workspace route reaches the model, so the
// workspace middleware refuses it like the Agent routes. (/api/ai/conversations
// and /api/ai/prompt-progress only store records and stay open.)
func aiPolicyPath(path string) bool {
	return strings.HasPrefix(path, "/api/agent") || path == "/api/ai/issue-filter"
}

func writeAIPolicyRefusal(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, aiPolicyRefusalMessage)
}

// workspaceAIPolicy applies agentWorkspacePolicy to a workspace and the member
// the work runs for. An empty userID checks the workspace switches only, for
// work that no member asked for. A workspace that cannot be read, or a member
// who is no longer active in it, is refused.
func (s *server) workspaceAIPolicy(ctx context.Context, workspace, userID string) error {
	// An empty key reads the most recently written workspace, which may be
	// another tenant's; only AUTH_DISABLED development has a single one.
	if s.store == nil || workspace == "" && !s.authDisabled {
		return store.ErrAuthForbidden
	}
	metadata, ok := s.store.WorkspaceSettingsMetadata(workspace)
	if !ok {
		return store.ErrAuthForbidden
	}
	role := ""
	if userID != "" && !s.authDisabled {
		found, status, err := s.store.WorkspaceRole(ctx, metadata.Workspace.ID, userID)
		if err != nil || status != "active" {
			return store.ErrAuthForbidden
		}
		role = found
	}
	return agentWorkspacePolicy(metadata.WorkspaceSettings, role)
}

// requestAIPolicy is workspaceAIPolicy for the signed-in member of a request.
func (s *server) requestAIPolicy(r *http.Request) error {
	return s.workspaceAIPolicy(r.Context(), workspaceKey(r), authUser(r).ID)
}

// aiPolicyClaimedRuns keeps the claimed agent-worker runs the workspace AI
// policy still allows for the person each run acts for. The worker calls the
// model itself, so a run queued before the workspace turned AI off (or HIPAA
// on) ends here as failed, as if the worker had failed it, and is never
// handed out.
func (s *server) aiPolicyClaimedRuns(ctx context.Context, worker string, runs []store.AgentRun) []store.AgentRun {
	allowed := runs[:0]
	for _, run := range runs {
		userID := ""
		if principal, err := decodeAgentRunPrincipal(run); err == nil {
			userID = principal.UserID
		}
		if s.workspaceAIPolicy(ctx, run.WorkspaceKey, userID) == nil {
			allowed = append(allowed, run)
			continue
		}
		if chat, ok := workerChatInfo(run); ok {
			s.settleWorkerChatRun(ctx, run, chat, store.AgentRunFailed, aiPolicyRefusalMessage)
		}
		if info, ok := workerLoopInfo(run); ok {
			s.finishWorkerLoopRun(ctx, run, info, store.AgentRunFailed, aiPolicyRefusalMessage)
		}
		if _, err := s.store.FinishAgentRun(ctx, run.ID, worker, store.AgentRunResult{Status: store.AgentRunFailed, Error: aiPolicyRefusalMessage}, time.Now()); err != nil {
			log.Printf("agent run %s refused by workspace AI policy could not be finished: %v", run.ID, err)
		} else {
			agentRunElicitationLogs.Delete(run.ID)
		}
	}
	return allowed
}
