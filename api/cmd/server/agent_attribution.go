package main

import (
	"net/http"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// builtinAgentAvatarURL is the built-in "Flow" agent's avatar (store.EnsureBuiltinApplication).
const builtinAgentAvatarURL = "/favicon.svg"

// agentToolBotActor attributes what an agent tool call writes to Flow's agent
// acting on behalf of a person "the reference app set priority to Urgent on
// behalf of Skyler Anderson":
//   - in-app chat: the viewer is the person; records keep them as the actor.
//   - the built-in application (comment threads, delegation): the viewer is the
//     agent's app user and the invoking person is in builtinInvokerContextKey.
func agentToolBotActor(r *http.Request, viewer domain.User) *domain.ActorBot {
	behalf := viewer
	if invoker, ok := r.Context().Value(builtinInvokerContextKey{}).(domain.User); ok && invoker.ID != "" {
		behalf = invoker
	}
	if behalf.ID == "" || behalf.App {
		return nil
	}
	bot := &domain.ActorBot{ID: store.BuiltinApplicationClientID, Type: domain.ActorBotTypeAgent, Name: "Flow", AvatarURL: builtinAgentAvatarURL, OnBehalfOfID: behalf.ID, OnBehalfOfName: agentBehalfName(behalf)}
	if viewer.App {
		if name := agentBehalfName(viewer); name != "" {
			bot.Name = name
		}
		if viewer.AvatarURL != "" {
			bot.AvatarURL = viewer.AvatarURL
		}
	}
	return bot
}

// withAgentAttribution marks the request's viewer as acting through Flow's
// agent, so every record and history entry the tool call writes as the viewer
// (viewerBotActor) carries the agent attribution. OAuth attribution that is
// already present (an application calling the agent) is kept.
func withAgentAttribution(r *http.Request, data *domain.Bootstrap) {
	if data == nil || data.Viewer.Via != nil {
		return
	}
	data.Viewer.Via = agentToolBotActor(r, data.Viewer)
}

func agentBehalfName(user domain.User) string {
	return firstNonEmpty(strings.TrimSpace(user.DisplayName), strings.TrimSpace(user.Name))
}

// appendEstimateActivity records an estimate change made outside the issue
// update handler (the MCP save_issue estimate write) in the issue history,
// with the same metadata the handler writes.
func appendEstimateActivity(data *domain.Bootstrap, issue *domain.Issue, next *float64) {
	format := func(value *float64) string {
		if value == nil {
			return "0"
		}
		return strconv.FormatFloat(*value, 'f', -1, 64)
	}
	if format(issue.Estimate) == format(next) {
		return
	}
	appendActivity(data, issue.ID, "issue.updated", data.Viewer, map[string]string{"estimate": format(next), "estimateBefore": format(issue.Estimate)})
}
