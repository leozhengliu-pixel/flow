package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Run tokens let an out-of-process agent run (agent-worker) act in Flow exactly like today's in-process agent: a
// short-lived bearer token, minted for one run while a worker holds it, that /mcp and the internal runtime API
// accept. A token carries no claims of its own; it names its run, and the run's principal (stored with the run,
// never sent to the worker) says who the run acts as:
//
//   - mode "user" (in-app chat): the run acts as the user, and what it writes reads "Flow … on behalf of <user>"
//     (agentToolBotActor), like the chat agent's tool calls;
//   - mode "app" (comment threads, delegation, loops): the run acts as the built-in Flow application on behalf of the
//     person who asked; effective access is the application's intersected with that person's, exactly as
//     builtinApplicationContext gives the in-process thread agent.
//
// Every tool call re-checks the user, their workspace role, the workspace agent policy and (mode app) the
// installation, so revoking any of them stops a running run at its next call. Tool calls run through the same
// executeAgentToolRecordingChange as the in-process agent, so attribution, argument normalization and the change
// set are identical; the changes are stored per run (agent_run_changes) so they can be undone.

// agentRunTokenPrefix marks run tokens, so /mcp tells them from API keys and OAuth tokens without a lookup.
const agentRunTokenPrefix = "flowrun_"

// agentRunTokenTTL is how long a run token is valid; a worker mints a fresh one for a longer run.
const agentRunTokenTTL = 15 * time.Minute

// Run principal modes.
const (
	agentRunModeUser = "user"
	agentRunModeApp  = "app"
)

// agentRunPrincipal is who a run acts as, stored with the run (store.AgentRun.Principal).
type agentRunPrincipal struct {
	Mode string `json:"mode"`
	// UserID is the person the run acts for: the chat's user, or the person who mentioned or delegated to the agent.
	UserID string `json:"userId"`
	// AppUserID is the built-in application's user (mode app).
	AppUserID string `json:"appUserId,omitempty"`
	// TeamID is the session's team (mode app): the application must still have access to it.
	TeamID string `json:"teamId,omitempty"`
	// Scopes narrows a user-mode run's access (default read and write, like in-app chat).
	Scopes []string `json:"scopes,omitempty"`
	// TeamIDs, when set, restricts a user-mode run to these teams.
	TeamIDs []string `json:"teamIds,omitempty"`
	// Tools is the run's tool allowlist (Flow MCP tool names); empty allows every Flow tool.
	Tools []string `json:"tools,omitempty"`
}

func (p agentRunPrincipal) valid() bool {
	switch p.Mode {
	case agentRunModeUser:
		return p.UserID != ""
	case agentRunModeApp:
		return p.UserID != "" && p.AppUserID != ""
	}
	return false
}

// agentRunGrant is an authenticated run token: the run, who it acts as, and the request context its tool calls
// run under.
type agentRunGrant struct {
	run       store.AgentRun
	principal agentRunPrincipal
}

// allows reports whether the run may list and call a Flow tool.
func (g *agentRunGrant) allows(s *server, tool flowMCPTool, readonly bool) bool {
	if tool.Access == "write" && (readonly || !s.agent.WriteTools) {
		return false
	}
	return len(g.principal.Tools) == 0 || slices.Contains(g.principal.Tools, tool.Name)
}

type agentRunContextKey struct{}

func withAgentRun(ctx context.Context, run store.AgentRun) context.Context {
	return context.WithValue(ctx, agentRunContextKey{}, run)
}

// agentRunFrom returns the run a tool call is made for, if any.
func agentRunFrom(ctx context.Context) (store.AgentRun, bool) {
	run, ok := ctx.Value(agentRunContextKey{}).(store.AgentRun)
	return run, ok
}

func decodeAgentRunPrincipal(run store.AgentRun) (agentRunPrincipal, error) {
	var principal agentRunPrincipal
	if json.Unmarshal(run.Principal, &principal) != nil || !principal.valid() {
		return principal, store.ErrAuthForbidden
	}
	return principal, nil
}

// mintAgentRunToken creates a token for a run a worker holds. Only its hash is stored.
func (s *server) mintAgentRunToken(ctx context.Context, run store.AgentRun) (string, time.Time, error) {
	if !store.AgentRunHeld(run.Status) {
		return "", time.Time{}, store.ErrAgentRunLeaseLost
	}
	if _, err := decodeAgentRunPrincipal(run); err != nil {
		return "", time.Time{}, err
	}
	secret, err := randomOpaqueToken(agentRunTokenPrefix)
	if err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(agentRunTokenTTL).UTC()
	if err := s.store.CreateAgentRunToken(ctx, run, secretHash(secret), expires); err != nil {
		return "", time.Time{}, err
	}
	return secret, expires, nil
}

// randomOpaqueToken is prefix plus 32 random bytes, URL-safe.
func randomOpaqueToken(prefix string) (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

// authenticateAgentRunToken resolves a run token to its grant; it fails once the token expired, was revoked, or
// the run ended.
func (s *server) authenticateAgentRunToken(ctx context.Context, token string) (*agentRunGrant, error) {
	if !strings.HasPrefix(token, agentRunTokenPrefix) || s.store == nil {
		return nil, store.ErrAgentRunTokenInvalid
	}
	run, err := s.store.AgentRunForToken(ctx, secretHash(token), time.Now())
	if err != nil {
		return nil, err
	}
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		return nil, err
	}
	return &agentRunGrant{run: run, principal: principal}, nil
}

// agentRunRequest is the request a run's tool call executes under, with the workspace data executeAgentTool needs
// (workspace, settings, viewer and role). It re-checks every grant the run depends on.
func (s *server) agentRunRequest(r *http.Request, grant *agentRunGrant) (*http.Request, domain.Bootstrap, error) {
	ctx := r.Context()
	principal := grant.principal
	metadata, ok := s.store.WorkspaceSettingsMetadata(grant.run.WorkspaceKey)
	if !ok {
		return r, domain.Bootstrap{}, store.ErrAuthForbidden
	}
	user, err := s.store.UserByID(ctx, principal.UserID)
	if err != nil || !user.Active || user.App {
		return r, domain.Bootstrap{}, store.ErrAuthForbidden
	}
	role, status, err := s.store.WorkspaceRole(ctx, metadata.Workspace.ID, user.ID)
	if err != nil || status != "active" || agentWorkspacePolicy(metadata.WorkspaceSettings, role) != nil {
		return r, domain.Bootstrap{}, store.ErrAuthForbidden
	}
	data := domain.Bootstrap{Workspace: metadata.Workspace, WorkspaceSettings: metadata.WorkspaceSettings}
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, grant.run.WorkspaceKey)
	ctx = withAgentRun(ctx, grant.run)
	// Links in tool results open in the app, not on the worker's loopback host.
	if base := s.loopAppURL(); base != "" {
		ctx = context.WithValue(ctx, agentBaseURLKey{}, base)
	}
	switch principal.Mode {
	case agentRunModeUser:
		scopes := principal.Scopes
		if len(scopes) == 0 {
			scopes = []string{"read", "write"}
		}
		key := domain.APIKey{Scopes: scopes}
		if principal.TeamIDs != nil {
			key.TeamRestriction, key.TeamIDs = "selected", slices.Clone(principal.TeamIDs)
		}
		data.Viewer, data.ViewerRole = user, role
		ctx = context.WithValue(ctx, authUserContextKey{}, user)
		ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
		ctx = store.ContextWithActor(ctx, user)
	case agentRunModeApp:
		app, err := s.store.ApplicationByUser(ctx, principal.AppUserID)
		if err != nil || !app.Active || !app.Builtin || principal.TeamID != "" && !slices.Contains(app.AccessTeamIDs(), principal.TeamID) {
			return r, domain.Bootstrap{}, store.ErrAuthForbidden
		}
		appUser := app.User()
		key := domain.APIKey{CreatorID: app.UserID, SubjectUserID: app.UserID, Actor: "app", Scopes: app.Scopes, TeamRestriction: "selected", TeamIDs: app.AccessTeamIDs(), OAuthClientID: app.ClientID}
		data.Viewer, data.ViewerRole = appUser, "app"
		ctx = context.WithValue(ctx, authUserContextKey{}, appUser)
		ctx = context.WithValue(ctx, apiKeyContextKey{}, key)
		ctx = context.WithValue(ctx, builtinInvokerContextKey{}, user)
		ctx = store.ContextWithActor(ctx, appUser)
	default:
		return r, domain.Bootstrap{}, store.ErrAuthForbidden
	}
	return r.WithContext(ctx), data, nil
}

// agentRunChangeRef is what a run's tool result says about the change it recorded (structuredContent.change), so
// the worker can show it on the action activity and the change can be undone by its id.
type agentRunChangeRef struct {
	ID         string `json:"id"`
	RunID      string `json:"runId"`
	Seq        int64  `json:"seq"`
	Action     string `json:"action"`
	EntityType string `json:"entityType"`
	EntityID   string `json:"entityId,omitempty"`
	Label      string `json:"label,omitempty"`
	URL        string `json:"url,omitempty"`
	Undoable   bool   `json:"undoable"`
}

func newAgentRunChangeRef(runID string, seq int64, change domain.AgentChange) agentRunChangeRef {
	return agentRunChangeRef{ID: runID + ":" + strconv.FormatInt(seq, 10), RunID: runID, Seq: seq, Action: change.Action, EntityType: change.EntityType, EntityID: change.EntityID, Label: change.Label, URL: change.URL, Undoable: change.Undoable}
}

// callAgentRunTool runs a Flow tool for a run token. A batch delete runs as one call per target, so each deleted
// item is its own undoable change.
func (s *server) callAgentRunTool(r *http.Request, grant *agentRunGrant, name string, args map[string]any) (any, []agentRunChangeRef, error) {
	if _, batch := args["ids"]; !batch || !mcpBatchDeleteTools()[name] {
		result, ref, err := s.callAgentRunToolOnce(r, grant, name, args)
		if ref == nil {
			return result, nil, err
		}
		return result, []agentRunChangeRef{*ref}, err
	}
	if stringArg(args, "id") != "" {
		return nil, nil, fmt.Errorf("pass either id or ids, not both")
	}
	ids := []string{}
	for _, id := range stringsArg(args, "ids") {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > mcpBatchDeleteLimit {
		return nil, nil, fmt.Errorf("ids must name between 1 and %d items", mcpBatchDeleteLimit)
	}
	items := []any{}
	refs := []agentRunChangeRef{}
	failures := []map[string]string{}
	for _, id := range ids {
		single := maps.Clone(args)
		delete(single, "ids")
		single["id"] = id
		result, ref, err := s.callAgentRunToolOnce(r, grant, name, single)
		if err != nil {
			failures = append(failures, map[string]string{"id": id, "error": err.Error()})
			continue
		}
		items = append(items, result)
		if ref != nil {
			refs = append(refs, *ref)
		}
	}
	if len(items) == 0 {
		messages := []string{}
		for _, failure := range failures {
			messages = append(messages, failure["id"]+": "+failure["error"])
		}
		return nil, nil, fmt.Errorf("nothing was deleted: %s", strings.Join(messages, "; "))
	}
	receipt := map[string]any{"deleted": len(items), "items": items}
	if len(failures) > 0 {
		receipt["errors"] = failures
	}
	return receipt, refs, nil
}

func (s *server) callAgentRunToolOnce(r *http.Request, grant *agentRunGrant, name string, args map[string]any) (any, *agentRunChangeRef, error) {
	request, data, err := s.agentRunRequest(r, grant)
	if err != nil {
		return nil, nil, fmt.Errorf("the run's user or application no longer has access")
	}
	arguments, err := json.Marshal(args)
	if err != nil {
		return nil, nil, err
	}
	if agentChangeToolAccess()[name] == "write" {
		// Recorded before the call: a write can land even when the call then reports an error, and a run that may
		// have written is never retried automatically.
		if err := s.store.MarkAgentRunWrote(request.Context(), grant.run.ID); err != nil {
			return nil, nil, err
		}
	}
	call := domain.AgentToolCall{ID: "run_" + grant.run.ID, Name: name, Arguments: arguments}
	raw, err := s.executeAgentToolRecordingChange(request, data, &call)
	if err != nil {
		return nil, nil, err
	}
	var ref *agentRunChangeRef
	if call.Change != nil {
		seq, err := s.store.AppendAgentRunChange(request.Context(), grant.run, name, arguments, *call.Change)
		if err != nil {
			return nil, nil, err
		}
		item := newAgentRunChangeRef(grant.run.ID, seq, *call.Change)
		ref = &item
	}
	var result any
	if json.Unmarshal(raw, &result) != nil {
		result = string(raw)
	}
	return result, ref, nil
}

// agentRunUndoActor is who undoes a run's changes: the person the run acted for, directly (an undo is the
// person's own action, not the agent's), with their own access.
func (s *server) agentRunUndoActor(r *http.Request, run store.AgentRun) (context.Context, mcpActor, error) {
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		return nil, mcpActor{}, err
	}
	metadata, ok := s.store.WorkspaceSettingsMetadata(run.WorkspaceKey)
	if !ok {
		return nil, mcpActor{}, store.ErrAuthForbidden
	}
	user, err := s.store.UserByID(r.Context(), principal.UserID)
	if err != nil || !user.Active || user.App {
		return nil, mcpActor{}, store.ErrAuthForbidden
	}
	if _, status, err := s.store.WorkspaceRole(r.Context(), metadata.Workspace.ID, user.ID); err != nil || status != "active" {
		return nil, mcpActor{}, store.ErrAuthForbidden
	}
	ctx := context.WithValue(r.Context(), workspaceKeyContextKey{}, run.WorkspaceKey)
	ctx, actor := s.agentChangeActor(r.WithContext(ctx), user)
	// The reverted records change on the server only: every tab gets their realtime events.
	return store.ContextWithRealtimeClient(ctx, ""), actor, nil
}

// applyAgentRunChanges undoes (undo) or re-applies entries of a run's change set: the given sequence numbers, or
// every undoable entry when seqs is empty. Undo goes newest first. It returns the entries it changed.
func (s *server) applyAgentRunChanges(r *http.Request, run store.AgentRun, seqs []int64, undo bool) ([]store.AgentRunChange, error) {
	ctx, actor, err := s.agentRunUndoActor(r, run)
	if err != nil {
		return nil, err
	}
	changes, err := s.store.AgentRunChanges(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	targets := slices.DeleteFunc(changes, func(item store.AgentRunChange) bool {
		return !item.Change.Undoable || len(seqs) > 0 && !slices.Contains(seqs, item.Seq) || (item.Change.State == "undone") == undo
	})
	if len(targets) == 0 {
		return nil, agentChangeConflict{"There is nothing to change in this run"}
	}
	if undo {
		slices.Reverse(targets)
	}
	applied := []store.AgentRunChange{}
	var failure error
	for _, item := range targets {
		change := item.Change
		next, err := s.applyAgentChange(ctx, actor, domain.AgentToolCall{Name: item.Tool, Arguments: item.Arguments, Change: &change}, undo)
		if err != nil {
			failure = errors.Join(failure, err)
			continue
		}
		if err := s.store.UpdateAgentRunChange(ctx, run.ID, item.Seq, next); err != nil {
			return applied, err
		}
		item.Change = next
		applied = append(applied, item)
	}
	if len(applied) == 0 {
		return nil, failure
	}
	return applied, nil
}
