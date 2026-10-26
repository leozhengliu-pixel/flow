package main

import (
	"fmt"
	"strings"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

// A chat reply and a built-in thread run share one tool budget: at most agentMaxToolTurns model turns with tool
// calls (FLOW_AGENT_MAX_TOOL_TURNS) and agentMaxToolCalls tool calls across them (FLOW_AGENT_MAX_TOOL_CALLS). The
// budget is generous on purpose — bulk work (a team of 40, a batch of issues) legitimately takes many steps — so
// runaway protection does not depend on it being low: an identical call that already failed is answered from that
// failure instead of running again, and a reply that keeps repeating failed calls, or whose every call fails turn
// after turn, stops early. A reply that stops on the budget ends with a summary of what it did and what is left
// (agentToolBudgetNudge), not a bare error, and "Continue" picks up from there.

const (
	// agentRepeatedFailureLimit is how many identical repeats of failed calls a reply may make before it stops.
	agentRepeatedFailureLimit = 3
	// agentFailedTurnLimit is how many turns in a row may have every tool call fail before the reply stops.
	agentFailedTurnLimit = 4
)

// agentMaxToolTurns is the turn budget of one chat reply or built-in thread run.
func (s *server) agentMaxToolTurns() int {
	if s.agent.MaxToolTurns > 0 {
		return s.agent.MaxToolTurns
	}
	return appconfig.DefaultAgentMaxToolTurns
}

// agentMaxToolCalls is the tool-call budget of one chat reply or built-in thread run.
func (s *server) agentMaxToolCalls() int {
	if s.agent.MaxToolCalls > 0 {
		return s.agent.MaxToolCalls
	}
	return appconfig.DefaultAgentMaxToolCalls
}

// Why a reply stopped before its model finished (agentToolBudget.stop).
const (
	agentBudgetTurns    = "turns"
	agentBudgetCalls    = "calls"
	agentBudgetRepeats  = "repeats"
	agentBudgetFailures = "failures"
)

// agentToolBudget counts one reply's tool calls and remembers the ones that failed.
type agentToolBudget struct {
	maxCalls int
	calls    int
	// failed maps a call (tool and canonical arguments) that failed to its error.
	failed      map[string]string
	repeats     int
	failedTurns int
	turnCalls   int
	turnFailed  int
	// stop is set once the reply must stop calling tools (one of the agentBudget* reasons).
	stop string
}

func newAgentToolBudget(maxCalls int) *agentToolBudget {
	return &agentToolBudget{maxCalls: maxCalls, failed: map[string]string{}}
}

// before reports whether call may run. A call over the budget, or an identical repeat of a failed call, does not
// run: content is the result the model gets instead (as an error).
func (b *agentToolBudget) before(call domain.AgentToolCall) (content string, run bool) {
	if b.calls >= b.maxCalls {
		return b.spent(), false
	}
	if message, ok := b.failed[canonicalToolCallKey(call)]; ok {
		b.repeats++
		b.turnCalls++
		b.turnFailed++
		if b.repeats >= agentRepeatedFailureLimit {
			b.stop = agentBudgetRepeats
		}
		return "Not run: you already made this exact call and it failed: " + message + " Change the arguments or use another tool; if nothing can fix it, stop and tell the user what blocked it.", false
	}
	b.calls++
	return "", true
}

// count is before for a call whose repeats another guard answers (a thread run's connector calls, see
// builtinRunGuard): only the call budget applies.
func (b *agentToolBudget) count() (content string, run bool) {
	if b.calls >= b.maxCalls {
		return b.spent(), false
	}
	b.calls++
	return "", true
}

func (b *agentToolBudget) spent() string {
	b.stop = agentBudgetCalls
	return fmt.Sprintf("Not run: this reply has used its tool-call budget (%d calls). Do not call more tools; tell the user what was done and what is left.", b.maxCalls)
}

// after records a call's outcome. A successful Flow write can change what an earlier call found (the issue now
// exists, the person joined), so it lets failed calls run again.
func (b *agentToolBudget) after(call domain.AgentToolCall, content string, failed bool) {
	b.turnCalls++
	if failed {
		b.turnFailed++
		if runes := []rune(strings.TrimSpace(content)); len(runes) > 300 {
			content = string(runes[:300]) + "…"
		}
		b.failed[canonicalToolCallKey(call)] = strings.TrimSpace(content)
		return
	}
	if agentChangeToolAccess()[strings.TrimPrefix(call.Name, "mcp__flow.")] == "write" {
		clear(b.failed)
	}
}

// endTurn closes a turn: a run of turns in which every call failed stops the reply.
func (b *agentToolBudget) endTurn() {
	if b.turnCalls > 0 {
		if b.turnFailed == b.turnCalls {
			b.failedTurns++
			if b.failedTurns >= agentFailedTurnLimit && b.stop == "" {
				b.stop = agentBudgetFailures
			}
		} else {
			b.failedTurns = 0
		}
	}
	b.turnCalls, b.turnFailed = 0, 0
}

// agentToolBudgetNudge asks the model for the reply's last word once its budget stopped it: no more tools, what
// was done, what is left, and how to continue.
func agentToolBudgetNudge(reason string, maxTurns, maxCalls int) string {
	why := fmt.Sprintf("it has used its step limit (%d steps)", maxTurns)
	switch reason {
	case agentBudgetCalls:
		why = fmt.Sprintf("it has used its tool-call limit (%d calls)", maxCalls)
	case agentBudgetRepeats, agentBudgetFailures:
		why = "its tool calls keep failing"
	}
	return "[Flow] This reply must stop now: " + why + ". Do not call any more tools. Answer the user in their language: " +
		"what you did (with links to what you created or changed), what is still left of the request and why, and that they can say \"Continue\" to pick up where you stopped. Do not claim anything is done that the tool results do not show."
}

// agentToolBudgetFallback is the reply's last word when the model could not give one. changes is how many changes
// the reply's result cards show (-1: not known, as in a thread run); chat replies also offer "Continue" as a
// suggestion.
func agentToolBudgetFallback(reason string, maxTurns, maxCalls int, chinese bool, changes int, suggest bool) string {
	if chinese {
		why := fmt.Sprintf("本次回复已用完步骤上限（%d 步）", maxTurns)
		switch reason {
		case agentBudgetCalls:
			why = fmt.Sprintf("本次回复已用完工具调用上限（%d 次）", maxCalls)
		case agentBudgetRepeats, agentBudgetFailures:
			why = "工具调用反复失败"
		}
		done := ""
		switch {
		case changes == 0:
			done = "目前还没有做任何修改。"
		case changes > 0:
			done = fmt.Sprintf("已完成的 %d 项修改见下方。", changes)
		}
		text := why + "，请求还没有全部完成。" + done + "回复“继续”，我会从停下的地方接着做。"
		if suggest {
			text += "\n\n```suggestions\n继续\n```"
		}
		return text
	}
	why := fmt.Sprintf("This reply used its step limit (%d steps)", maxTurns)
	switch reason {
	case agentBudgetCalls:
		why = fmt.Sprintf("This reply used its tool-call limit (%d calls)", maxCalls)
	case agentBudgetRepeats, agentBudgetFailures:
		why = "My tool calls kept failing"
	}
	done := ""
	switch {
	case changes == 0:
		done = " Nothing was changed yet."
	case changes == 1:
		done = " The change made so far is listed below."
	case changes > 1:
		done = fmt.Sprintf(" The %d changes made so far are listed below.", changes)
	}
	text := why + " before the request was finished." + done + " Reply \"Continue\" and I'll pick up where I stopped."
	if suggest {
		text += "\n\n```suggestions\nContinue\n```"
	}
	return text
}

// agentToolBudgetNotice is the line shown under a reply the budget stopped.
func agentToolBudgetNotice(reason string, maxTurns, maxCalls int, chinese bool) string {
	switch {
	case chinese && reason == agentBudgetCalls:
		return fmt.Sprintf("已达到单次回复的工具调用上限（%d 次）", maxCalls)
	case chinese && (reason == agentBudgetRepeats || reason == agentBudgetFailures):
		return "工具调用反复失败，已停止"
	case chinese:
		return fmt.Sprintf("已达到单次回复的步骤上限（%d 步）", maxTurns)
	case reason == agentBudgetCalls:
		return fmt.Sprintf("Stopped at the tool-call limit for one reply (%d calls)", maxCalls)
	case reason == agentBudgetRepeats || reason == agentBudgetFailures:
		return "Stopped because tool calls kept failing"
	}
	return fmt.Sprintf("Stopped at the step limit for one reply (%d steps)", maxTurns)
}
