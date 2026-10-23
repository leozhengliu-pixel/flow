package main

import (
	"context"
	"slices"
	"strings"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// The start_coding_session tool (docs/coding-sessions-design.md §15, track
// L). The Flow agent calls it from chat, from a delegated or mentioned issue,
// and from a Loop run ("Autofix bugs" → "Start a coding session"). It follows
// the reference rule: a request to fix, implement or open a PR starts a session
// right away; a request to investigate, plan or create tasks does not (the
// tool re-checks the user's words and refuses with guidance).

const startCodingSessionTool = "start_coding_session"

// codingLoopRunKey carries the running Loop into tool calls.
type codingLoopRunKey struct{}

type codingLoopRun struct {
	loop       domain.Loop
	runID      string
	trigger    loopTrigger
	codeAccess string
}

// withCodingLoopRun marks a Loop run's tool context (loop_runtime.go).
func withCodingLoopRun(ctx context.Context, loop domain.Loop, runID string, trigger loopTrigger, codeAccess string) context.Context {
	return context.WithValue(ctx, codingLoopRunKey{}, codingLoopRun{loop: loop, runID: runID, trigger: trigger, codeAccess: codeAccess})
}

func codingLoopRunFrom(ctx context.Context) (codingLoopRun, bool) {
	run, ok := ctx.Value(codingLoopRunKey{}).(codingLoopRun)
	return run, ok
}

// codingToolError is a tool result the agent can act on.
type codingToolError struct {
	Started bool                   `json:"started"`
	Code    domain.CodingErrorCode `json:"code"`
	Error   string                 `json:"error"`
}

func (s *server) startCodingSessionFromTool(ctx context.Context, actor mcpActor, args map[string]any) (any, error) {
	workspace := actor.WorkspaceKey
	metadata, ok := s.store.WorkspaceMetadataFields(workspace, "workspaceSettings", "asks", "emailIntakeMessages", "reviews", "members")
	if !ok {
		return nil, errNotFound
	}
	if !s.codingEnabled() || !workspaceFeatureEnabled(metadata.WorkspaceSettings, "coding-sessions") {
		return codingToolError{Code: domain.CodingErrSandboxDisabled, Error: "Coding sessions are not enabled in this workspace. Tell the user an admin can turn them on in Settings › Coding sessions."}, nil
	}
	triggers, ok := s.codingTriggers(workspace)
	if !ok {
		return codingToolError{Code: domain.CodingErrSandboxDisabled, Error: "Coding sessions are not available on this server yet."}, nil
	}
	prompt := strings.TrimSpace(stringArg(args, "prompt"))
	request := strings.TrimSpace(firstNonEmpty(stringArg(args, "userRequest"), prompt))
	if prompt == "" {
		return nil, errInvalid
	}
	loopRun, inLoop := codingLoopRunFrom(ctx)
	trigger := coding.IssueTrigger{
		WorkspaceKey: workspace, Settings: metadata.WorkspaceSettings.CodingAgentSettings, SpendLimits: metadata.WorkspaceSettings.SpendLimits,
		Source: domain.CodingSourceAgentChat, CreatorID: firstNonEmpty(actor.AuthorizationUserID, actor.User.ID),
		AnchorAppUserID: s.builtinAgentUserID(ctx, workspace), EnvironmentID: strings.TrimSpace(stringArg(args, "environmentId")),
		CheckIntent: true, Request: request, ContextText: stringArg(args, "repository"),
	}
	if issueID := strings.TrimSpace(stringArg(args, "issueId")); issueID != "" {
		issue, err := s.store.IssueRecord(ctx, workspace, issueID)
		if err != nil {
			if issue, err = s.issueByIdentifier(ctx, workspace, issueID); err != nil {
				return nil, errNotFound
			}
		}
		if key := actor.APIKey; key.TeamRestriction == "selected" && !slices.Contains(key.TeamIDs, issue.Team.ID) {
			return nil, errNotFound
		}
		withIssue := s.issueCodingTrigger(ctx, workspace, metadata, issue, actor.User)
		trigger.Issue, trigger.SourceID, trigger.RecentEnvironmentIDs = withIssue.Issue, issue.ID, withIssue.RecentEnvironmentIDs
		trigger.Prompt = coding.IssuePrompt(*withIssue.Issue, prompt)
		trigger.Source = domain.CodingSourceComment
		if actor.User.App && actor.User.BuiltinAgent && withIssue.AnchorTaskID != "" {
			// The Flow agent working on its delegation: the delegation task is
			// the anchor and the decision to code is the agent's own.
			trigger.Source, trigger.AnchorTaskID, trigger.IdempotencyKey = domain.CodingSourceIssue, withIssue.AnchorTaskID, withIssue.IdempotencyKey
			trigger.CheckIntent = false
			if task, err := s.store.AgentTask(ctx, workspace, withIssue.AnchorTaskID); err == nil && task.CreatorID != "" {
				trigger.CreatorID = task.CreatorID
			}
		} else if !actor.User.App {
			trigger.Source = domain.CodingSourceAgentChat
		}
		if withIssue.AnchorTaskID != "" {
			// A delegated issue has one session per delegation, whoever starts
			// it (see issueCodingIdempotencyKey).
			trigger.AnchorTaskID, trigger.IdempotencyKey = withIssue.AnchorTaskID, withIssue.IdempotencyKey
		}
		if !trigger.Issue.Origin.External && trigger.Source == domain.CodingSourceComment {
			// A comment by someone who is not a member is an external source.
			for _, member := range metadata.Members {
				if member.User.ID == trigger.CreatorID && !codingMemberRole(member.Role) {
					trigger.Issue.Origin = coding.ExternalOrigin{External: true, Source: "guest:" + member.User.ID, Label: firstNonEmpty(member.User.DisplayName, member.User.Name)}
				}
			}
		}
	}
	if inLoop {
		if loopRun.codeAccess != "readWrite" {
			return codingToolError{Code: domain.CodingErrPermissionDenied, Error: "This loop cannot change code: set its code access to read and write (and allow automation access to repositories) to start coding sessions."}, nil
		}
		if trigger.Issue == nil && loopRun.trigger.EntityType == "issue" && loopRun.trigger.EntityID != "" {
			if issue, err := s.store.IssueRecord(ctx, workspace, loopRun.trigger.EntityID); err == nil {
				withIssue := s.issueCodingTrigger(ctx, workspace, metadata, issue, actor.User)
				trigger.Issue, trigger.RecentEnvironmentIDs = withIssue.Issue, withIssue.RecentEnvironmentIDs
				trigger.Prompt = coding.IssuePrompt(*withIssue.Issue, prompt)
			}
		}
		trigger.Source, trigger.LoopID, trigger.SourceID = domain.CodingSourceLoop, loopRun.loop.ID, loopRun.runID
		trigger.CreatorID = firstNonEmpty(loopRun.loop.OwnerID, trigger.CreatorID)
		trigger.CheckIntent = false // the loop's instructions decided
		trigger.AnchorTaskID = ""
		trigger.IdempotencyKey = "loop:" + loopRun.loop.ID + ":" + loopRun.runID + ":" + trigger.SourceID
		if trigger.Issue != nil {
			trigger.IdempotencyKey += ":" + trigger.Issue.ID
		}
	}
	if trigger.Prompt == "" {
		trigger.Prompt = prompt
	}
	result, err := triggers.StartForIssue(ctx, trigger)
	if err != nil && !codingNotificationOnly(err) {
		if code := coding.TriggerErrorCode(err); code != "" {
			return codingToolError{Code: code, Error: strings.TrimPrefix(err.Error(), "coding trigger: "+string(code)+": ")}, nil
		}
		return nil, err
	}
	return codingToolResult(result), nil
}

// codingToolResult is the compact result the agent sees.
func codingToolResult(result coding.TriggerResult) map[string]any {
	out := map[string]any{"outcome": result.Outcome, "started": result.Outcome == coding.OutcomeStarted || result.Outcome == coding.OutcomeContinued || result.Outcome == coding.OutcomeEnvironmentChosen}
	if result.Session != nil {
		out["sessionId"] = result.Session.ID
		out["agentSessionId"] = result.Session.AgentSessionID
		out["state"] = result.Session.State
		out["phase"] = result.Session.Phase
		out["environmentId"] = result.Session.EnvironmentID
	}
	if result.Approval {
		out["approvalRequired"] = true
		out["message"] = "The request came from an external source, so a team member must approve the session before it runs. Tell the user it is waiting for approval."
	}
	if result.Pick != nil && result.Pick.Ambiguous {
		suggestions := make([]map[string]any, 0, len(result.Pick.Suggestions))
		for _, suggestion := range result.Pick.Suggestions {
			suggestions = append(suggestions, map[string]any{"environmentId": suggestion.EnvironmentID, "name": suggestion.Name, "repository": suggestion.Repository})
		}
		out["environmentSuggestions"] = suggestions
		out["message"] = "The repository is ambiguous; the session asks the user to pick one. You can also call the tool again with environmentId."
	}
	if result.Message != "" && out["message"] == nil {
		out["message"] = result.Message
	}
	if result.Intent != nil {
		out["intent"] = result.Intent.Intent
	}
	return out
}
