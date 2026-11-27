package main

import (
	"go/ast"
	"testing"
)

// Dynamic events and branches here are intentionally reviewed at the
// call site: the event name alone does not select a writer.
var hotMutationDispatches = map[string]string{
	`store/SQLiteStore.MutateWorkspace eventType`:                                            "Forwards the caller's context and event with the aggregate ID set.",
	`server/server.updateDocument favoriteMutationEvent("document.updated", input)`:          "WithDocumentUpdateWrite admits document.updated and both favorite events.",
	`server/server.maintainAdvancedSchedulesAt "schedules.maintained"`:                       "ContextWithAdvancedScheduleMaintenance selects the schedules writer.",
	`server/server.notifyAgentReply "agent_task.replied"`:                                    "ContextWithAgentReplyNotification selects the issue or other resource writer.",
	`server/server.createAttachmentFromURL "attachment.created"`:                             "Issue attachment context and record scope select the attachment writer.",
	`server/server.saveLinkAttachment eventType`:                                             "Issue attachment upsert context selects the attachment writer for create and update.",
	`server/server.updateLinkAttachment "attachment.updated"`:                                "Issue attachment context and record scope select the attachment writer.",
	`server/server.deleteIssue "issue.deleted"`:                                              "The bounded issue routes set the record alias and take DeleteIssueScoped instead.",
	`server/server.resolveCommentAction "issue.updated"`:                                     "ContextWithCommentActionResolution selects the comment-action resolution writer.",
	`server/server.createIssue "issue.created"`:                                              "The record route marks UsesIssueRecordMutations. It also receives the bounded legacy URL.",
	`server/server.updateIssue "issue.updated"`:                                              "The bounded legacy URL uses updateIssueRecord and the point issue writer.",
	`server/server.batchUpdate "issue.batch_updated"`:                                        "Record alias and ContextWithIssueBatchUpdate select the batch writer.",
	`server/server.createComment "comment.created"`:                                          "Record alias selects the issue discussion writer; the legacy branch uses the legacy discussion point writer.",
	`server/server.updateComment eventType`:                                                  "Issue discussion covers comment.updated, comment.resolved and comment.unresolved.",
	`server/server.deleteComment "comment.deleted"`:                                          "The bounded record and legacy discussion writers cover deletion.",
	`server/server.toggleCommentReaction "comment.reaction_toggled"`:                         "The bounded record and legacy discussion writers cover the reaction.",
	`server/server.toggleIssueReaction "issue.reaction_toggled"`:                             "The record alias selects the issue reaction writer; the legacy branch has a point writer.",
	`server/server.createRelation "issue.relation_added"`:                                    "ContextWithIssueRelationMutation selects the issue relation writer.",
	`server/server.createAttachment "attachment.created"`:                                    "Issue attachment context and record scope select the attachment writer.",
	`server/server.createIssueLink "issue.link_created"`:                                     "Issue attachment context and record scope select the attachment writer.",
	`server/server.acknowledgeDesktopNotifications "notification.desktop_acknowledged"`:      "ContextWithDesktopNotificationAcknowledgement selects the acknowledgment writer.",
	`server/server.maintainCycleSchedule "cycles.automatically_maintained"`:                  "ContextWithCycleScheduleMaintenance selects the cycle writer.",
	`server/server.createRecurringOccurrence "issue.created"`:                                "ContextWithRecurringOccurrenceWrite selects the occurrence writer.",
	`server/server.setIssueReleases "issue.releases_updated"`:                                "ContextWithIssueReleasesMutation selects the release-link writer.",
	`server/server.generateCommentActionsBounded "comment.actions_started"`:                  "ContextWithCommentActionProposal and the discussion scope select the proposal writer.",
	`server/server.generateCommentActionsBounded "comment.actions_updated"`:                  "ContextWithCommentActionProposal and the discussion scope select the proposal writer.",
	`server/server.normalizeRecurringIssueTimingBounded "issue.updated"`:                     "ContextWithIssueRecurrenceTimingMutation selects the recurrence timing writer.",
	`server/server.updateIssueSuggestionStateBounded eventType`:                              "ContextWithIssueSuggestionWrite selects the suggestion writer for accept and dismiss.",
	`server/server.generateTriageIntelligenceForIssuesBounded "issue.suggestions_generated"`: "ContextWithIssueSuggestionWrite selects the suggestion writer.",
	`server/server.maintainTeamSettings "settings.project_archive"`:                          "The delivery scheduler supplies WithColdWork for the archive callback.",
	`server/server.purgeTeam "team.deleted"`:                                                 "purgeTeam explicitly selects the COLD legacy writer.",
	`server/server.pruneIssueSuggestionTargets "issue.suggestions_pruned"`:                   "ContextWithIssueSuggestionWrite selects the suggestion writer.",
	`server/server.setThreadSubscription "comment.thread_subscription"`:                      "The bounded record and legacy discussion writers cover thread subscriptions.",
	`server/server.clearThreadSubscription "comment.thread_subscription"`:                    "The bounded record and legacy discussion writers cover thread subscriptions.",
	`server/server.runTriageIntelligenceWith options.eventType`:                              "The bounded triage callback supplies ContextWithIssueSuggestionWrite.",
	`store/SQLiteStore.cleanupTeamMemberData "team_member.cleaned_up" with ctx`:              "The forced full write runs only in tests.",
	`store/SQLiteStore.cleanupTeamMemberData "team_member.cleaned_up" with scope`:            "ChangeTeamMembership admits the COLD legacy writer before removing the member.",
}

// Direct HOT use of the generic callback API must select a reviewed writer
// or carry a COLD context. The route and background guards check where those
// contexts originate. Inspect both HOT handlers and their helpers, including
// callers that do not use a bootstrap accessor of their own.
func checkHotMutationDispatches(t *testing.T, graph *writeChainBoundaryGraph, inventory map[string]writeChainBoundaryReview) {
	t.Helper()
	hot := map[string]bool{}
	var queue []string
	add := func(key string) {
		if !hot[key] {
			hot[key] = true
			queue = append(queue, key)
		}
	}
	for key := range graph.funcs {
		if inventory[graph.inventoryKey(key)].Temperature == "hot" {
			add(key)
		}
	}
	out := map[string][]string{}
	for _, ref := range graph.refs {
		if ref.offOnly {
			continue
		}
		if ref.kind == "route" {
			if scaleRouteTemperature[ref.pattern] == "hot" {
				add(ref.to)
			}
			continue
		}
		if _, cut := storeWrapperBoundaryBoundaries[ref.from+"->"+ref.to]; !ref.byName && !cut {
			out[ref.from] = append(out[ref.from], ref.to)
		}
	}
	for len(queue) != 0 {
		from := queue[0]
		queue = queue[1:]
		for _, to := range out[from] {
			add(to)
		}
	}
	seen := map[string]bool{}
	for _, ref := range graph.refs {
		if ref.offOnly || !hot[ref.from] {
			continue
		}
		var eventArg int
		switch ref.to {
		case "store/SQLiteStore.MutateWorkspace", "store/SQLiteStore.MutateWorkspaceWithAggregate":
			eventArg = 2
		case "store/SQLiteStore.Mutate", "store/SQLiteStore.MutateWithAggregate":
			eventArg = 1
		default:
			continue
		}
		if ref.kind != "call" {
			t.Errorf("HOT generic mutation method reference requires review: %s at %s:%d", ref.from, ref.file, ref.line)
			continue
		}
		for _, path := range graph.refAncestors(ref) {
			call, ok := path[len(path)-2].(*ast.CallExpr)
			if !ok || call.Fun != path[len(path)-1] || len(call.Args) <= eventArg || graph.offOnly(path) {
				continue
			}
			event := writeChainBoundarySource(graph.fset, call.Args[eventArg])
			site := ref.from + " " + event
			// Context distinguishes branches that dispatch the same event.
			if hotMutationDispatches[site] == "" {
				site += " with " + writeChainBoundarySource(graph.fset, call.Args[0])
			}
			if hotMutationDispatches[site] == "" || seen[site] {
				t.Errorf("HOT generic mutation requires review: %s at %s:%d", site, ref.file, ref.line)
			}
			seen[site] = true
		}
	}
	for site := range hotMutationDispatches {
		if !seen[site] {
			t.Errorf("stale HOT generic mutation review: %s", site)
		}
	}
}
