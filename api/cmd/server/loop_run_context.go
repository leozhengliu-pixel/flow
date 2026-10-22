package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Follow-ups on a finished run: the saved run context the agent answers
// from, what kind of follow-up a reply is, the guard that keeps a
// continuation from repeating finished writes, the aggregate count tool loop
// runs may use, and the timing split of a run's work.

// loopRunTiming splits a run's (or reply's) wall time: loading the workspace
// and scope, connector discovery, waiting on the model and running tools.
type loopRunTiming struct {
	TotalMs     int64 `json:"totalMs"`
	LoadMs      int64 `json:"loadMs"`
	DiscoveryMs int64 `json:"discoveryMs"`
	ModelMs     int64 `json:"modelMs"`
	ModelTurns  int   `json:"modelTurns"`
	ToolMs      int64 `json:"toolMs"`
}

// timingEvent records the timing split in the run's event log and server log.
func (rec *loopRunRecorder) timingEvent() store.LoopRunEvent {
	kind := "run"
	if rec.reply != nil {
		kind = "reply"
	}
	timing := rec.timing
	log.Printf("Loop %s timing workspace=%s run=%s total_ms=%d load_ms=%d discovery_ms=%d model_ms=%d model_turns=%d tool_ms=%d", kind, rec.workspace, rec.run.ID, timing.TotalMs, timing.LoadMs, timing.DiscoveryMs, timing.ModelMs, timing.ModelTurns, timing.ToolMs)
	return loopRunEvent("timing", timing)
}

// loopConnectorSkippedNotice starts the run notice for a selected connector
// whose tools were not offered (followed by the connector and the reason).
const loopConnectorSkippedNotice = "A selected connector was skipped and its tools were not offered:"

// loopConnectorDiscoveryBudget bounds a run's connector discovery.
var loopConnectorDiscoveryBudget = 20 * time.Second

// loopConnectorPolicy is what a run does when a selected connector fails
// discovery (FLOW_LOOP_CONNECTOR_POLICY): "partial" (default) runs with the
// other connectors' tools and records a notice naming the failed one;
// "required" fails the run as unavailable before the model is called.
func loopConnectorPolicy() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_LOOP_CONNECTOR_POLICY")), "required") {
		return "required"
	}
	return "partial"
}

// addNotice records a notice on the run (a reply logs it).
func (rec *loopRunRecorder) addNotice(notice string) {
	if rec.reply == nil && !slices.Contains(rec.run.Notices, notice) {
		rec.run.Notices = append(rec.run.Notices, notice)
	}
	rec.logEvent("notice", map[string]string{"notice": notice})
}

// loopGrantedToolNames are MCP-only Flow tools loop runs are offered besides
// the in-process agent's tools: count_issues counts across every page with
// the same filters, access checks and team-restricted key as list_issues.
// Other agents keep their tool sets (agentMCPOnlyTools).
var loopGrantedToolNames = []string{"count_issues"}

var loopGrantedToolDefinitions = sync.OnceValue(func() []agentProviderTool {
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		return nil
	}
	tools := []agentProviderTool{}
	for _, item := range inventory {
		if name := strings.TrimPrefix(item.Name, "mcp__flow."); slices.Contains(loopGrantedToolNames, name) && item.Access == "read" {
			tools = append(tools, agentProviderTool{Name: name, Description: item.Description, Parameters: item.InputSchema, Access: item.Access})
		}
	}
	return tools
})

func loopGrantedTools() []agentProviderTool {
	return slices.Clone(loopGrantedToolDefinitions())
}

type agentGrantedToolsKey struct{}

// withAgentGrantedTools lets executeAgentTool run the named MCP-only tools
// for this request only.
func withAgentGrantedTools(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, agentGrantedToolsKey{}, slices.Clone(names))
}

func agentToolGranted(ctx context.Context, name string) bool {
	granted, _ := ctx.Value(agentGrantedToolsKey{}).([]string)
	return slices.Contains(granted, strings.TrimPrefix(name, "mcp__flow."))
}

// loopWriteFingerprint names what a create-style write made: the tool and
// the target its argument summary leads with (the issue commented on, the
// project updated, the new issue's title). Updates by id get none.
func loopWriteFingerprint(name string, args map[string]any) string {
	if loopStringArg(args, "id") != "" {
		return ""
	}
	return loopCallFingerprint(name, loopToolArgsSummary(args))
}

func loopCallFingerprint(name, summary string) string {
	first, _, _ := strings.Cut(summary, " · ")
	if strings.TrimSpace(first) == "" {
		return ""
	}
	return strings.TrimPrefix(name, "mcp__flow.") + "|" + first
}

// loopSucceededWrites are the write calls that completed on the run and its
// replies, oldest first.
func loopSucceededWrites(run domain.LoopRun) []domain.LoopRunToolCall {
	writes := []domain.LoopRunToolCall{}
	collect := func(calls []domain.LoopRunToolCall) {
		for _, call := range calls {
			if call.Status == "completed" && loopWriteToolName(call.Name) {
				writes = append(writes, call)
			}
		}
	}
	collect(run.ToolCalls)
	for _, reply := range run.Replies {
		collect(reply.ToolCalls)
	}
	return writes
}

// loopCompletedWrites indexes the writes that already succeeded by fingerprint.
func loopCompletedWrites(run domain.LoopRun) map[string]string {
	done := map[string]string{}
	for _, call := range loopSucceededWrites(run) {
		if fingerprint := loopCallFingerprint(call.Name, call.Args); fingerprint != "" {
			done[fingerprint] = loopCallText(call)
		}
	}
	return done
}

// Reply intents: a question about the run's result, a request to continue
// its work, or anything else.
const (
	loopReplyStatus   = "status"
	loopReplyContinue = "continue"
	loopReplyOther    = "other"
)

var (
	loopContinuePattern = regexp.MustCompile(`(?i)\b(continue|keep going|go on|resume|carry on|finish (it|up|the rest|the remaining)|do the rest|retry|try again|run (it )?again|redo|pick up where)\b|继续|接着|把剩下|剩下的|完成剩余|重试|再试|再跑|重新运行|补上|补完`)
	loopStatusPattern   = regexp.MustCompile(`(?i)^(is|was|did|has|have|are)\s+(it|this|that|the run|the loop|everything|you|they)\b.*\b(done|finish|finished|complete|completed|succeed|succeeded|work|worked|fail|failed|go|went|posted|published)\b|\b(what('s| is) the status|status\?|why did (it|the run|you) (fail|stop)|what happened|how did it go|did it (work|finish|fail))\b|完成了吗|做完了吗|好了吗|成功了吗|完成没|结束了吗|失败了吗|怎么样了|进展如何|结果(如何|怎样|怎么样)|是否完成|有没有完成|状态(如何|是什么|怎样)|为什么(失败|没有完成|没完成|停)|卡在哪`)
)

// loopReplyIntent classifies a follow-up. Only a short question about the
// result counts as a status question; when unsure the reply is "other" and
// keeps its tools.
func loopReplyIntent(body string) string {
	body = strings.TrimSpace(body)
	if loopContinuePattern.MatchString(body) {
		return loopReplyContinue
	}
	if len([]rune(body)) <= 80 && loopStatusPattern.MatchString(body) {
		return loopReplyStatus
	}
	return loopReplyOther
}

// Bounds of the saved context a reply's prompt carries.
const (
	loopContextToolCalls  = 20
	loopContextReplies    = 5
	loopContextTextLength = 1500
	loopContextErrLength  = 300
)

func loopClip(text string, limit int) string {
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}

func loopCallText(call domain.LoopRunToolCall) string {
	text := firstNonEmpty(call.Label, call.Name)
	if call.Args != "" {
		text += " (" + call.Args + ")"
	}
	return text
}

// writeLoopToolEvidence lists the most recent tool calls with their outcome.
func writeLoopToolEvidence(b *strings.Builder, indent string, calls []domain.LoopRunToolCall) {
	if len(calls) == 0 {
		fmt.Fprintf(b, "%sTool calls: none\n", indent)
		return
	}
	fmt.Fprintf(b, "%sTool calls (%d):\n", indent, len(calls))
	shown := calls
	if len(shown) > loopContextToolCalls {
		fmt.Fprintf(b, "%s  (%d earlier calls omitted)\n", indent, len(shown)-loopContextToolCalls)
		shown = shown[len(shown)-loopContextToolCalls:]
	}
	for _, call := range shown {
		fmt.Fprintf(b, "%s  - %s: %s", indent, loopCallText(call), call.Status)
		if call.Error != "" {
			fmt.Fprintf(b, " — %s", loopClip(call.Error, loopContextErrLength))
		}
		b.WriteString("\n")
	}
}

func loopProducedText(produced map[string]int) string {
	if len(produced) == 0 {
		return "nothing"
	}
	parts := []string{}
	for _, kind := range slices.Sorted(maps.Keys(produced)) {
		parts = append(parts, fmt.Sprintf("%s %d", kind, produced[kind]))
	}
	return strings.Join(parts, ", ")
}

// loopReplyContextPrompt is the compact, bounded record of the run and its
// earlier replies that a reply's answer works from, and how to answer this
// kind of follow-up.
func loopReplyContextPrompt(run domain.LoopRun, replyID, intent string) string {
	var b strings.Builder
	b.WriteString("\n\nYou are answering a follow-up message on a run of this loop that already ended. Saved record of the run (authoritative):\n")
	fmt.Fprintf(&b, "- Status: %s", run.Status)
	if run.FailureReason != "" {
		fmt.Fprintf(&b, " (reason: %s)", run.FailureReason)
	}
	b.WriteString("\n")
	if run.Error != "" {
		fmt.Fprintf(&b, "- Error: %s\n", loopClip(run.Error, loopContextErrLength*2))
	}
	if summary := run.Summary; summary != nil {
		fmt.Fprintf(&b, "- Final report: %s — %s\n", summary.Status, loopClip(summary.Summary, loopContextTextLength))
		if len(summary.Done) > 0 {
			fmt.Fprintf(&b, "  Done: %s\n", loopClip(strings.Join(summary.Done, "; "), loopContextTextLength))
		}
		if len(summary.NotDone) > 0 {
			fmt.Fprintf(&b, "  Not done: %s\n", loopClip(strings.Join(summary.NotDone, "; "), loopContextTextLength))
		}
	} else {
		b.WriteString("- Final report: none (the run did not call finish_run)\n")
	}
	expected := "none"
	if len(run.ExpectedOutputs) > 0 {
		expected = strings.Join(run.ExpectedOutputs, ", ")
	}
	fmt.Fprintf(&b, "- Required outputs: %s; successful changes made: %s\n", expected, loopProducedText(run.Produced))
	writeLoopToolEvidence(&b, "- ", run.ToolCalls)
	earlier := []domain.LoopRunReply{}
	for _, reply := range run.Replies {
		if reply.ID == replyID {
			break
		}
		earlier = append(earlier, reply)
	}
	if len(earlier) > loopContextReplies {
		fmt.Fprintf(&b, "- (%d earlier follow-ups omitted)\n", len(earlier)-loopContextReplies)
		earlier = earlier[len(earlier)-loopContextReplies:]
	}
	for _, reply := range earlier {
		fmt.Fprintf(&b, "- Earlier follow-up %q: %s", loopClip(reply.Body, 200), reply.Status)
		if reply.FailureReason != "" {
			fmt.Fprintf(&b, " (reason: %s)", reply.FailureReason)
		}
		if reply.Error != "" {
			fmt.Fprintf(&b, " — %s", loopClip(reply.Error, loopContextErrLength))
		}
		b.WriteString("\n")
		writeLoopToolEvidence(&b, "  ", reply.ToolCalls)
	}
	if writes := loopSucceededWrites(run); len(writes) > 0 {
		b.WriteString("- Changes that already succeeded (never repeat them):\n")
		if len(writes) > loopContextToolCalls {
			writes = writes[len(writes)-loopContextToolCalls:]
		}
		for _, call := range writes {
			fmt.Fprintf(&b, "  - %s\n", loopCallText(call))
		}
	}
	switch intent {
	case loopReplyStatus:
		b.WriteString("\nThis message asks about the run's result. Answer it from the saved record above: say whether the run finished, what it did and did not do, and why (cite the error or the report). Do not run the loop again and do not query or change anything: no tools are available for this answer. Reply in the language of the message.\n")
	case loopReplyContinue:
		b.WriteString("\nThis message asks you to continue the run. Do only the steps that are not done yet; the changes listed as already succeeded must not be repeated (repeats are refused). Read only what the remaining steps need. End with finish_run.\n")
	default:
		b.WriteString("\nAnswer the message. Use the saved record for anything it already covers instead of reading the workspace again; call tools only for information the record does not have or for changes the person asks for. Do not repeat changes that already succeeded.\n")
	}
	return b.String()
}
