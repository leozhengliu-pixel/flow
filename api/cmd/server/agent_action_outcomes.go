package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// What an agent says it did comes from the tool calls it made, never from the model's text alone. A model asked
// to move issues into a private team read the team list and then answered that every move failed for lack of
// permission without calling save_issue: no move was attempted, and there was no permission error to report.
//
// agentWriteLedger records each write call of one reply with its outcome: Flow write tools, and connector (MCP)
// tools that may write (agentLedgerTool). Three checks use it:
//   - a final answer that reports change outcomes (failures, successes, permission errors) while the reply ran no
//     write at all is not passed on: the model is told once that nothing ran and to call the tool (nothing was
//     written yet, so the retry cannot repeat a write); a second such answer is replaced by Flow's own statement
//     that nothing was executed;
//   - a sentence that says every change failed (or every change succeeded) while the ledger has successes (or
//     failures) is rewritten into Flow's count (correct);
//   - a reply whose writes did not all succeed, or that claims failures its writes did not have, ends with Flow's
//     count of what was executed, what was not, what succeeded and what failed, with each failure's reason as the
//     tool returned it (summary).
//
// The same ledger and checks run wherever a final answer is made from tool results: chat replies
// (runAgentSession), application-agent threads (executeBuiltinApplication), and worker runs, whose ledger Flow
// keeps from the run's /mcp calls (agent_run_outcomes.go) and checks when the worker asks for the run's last word
// and when it posts the response.

const (
	agentWriteSucceeded = "succeeded"
	agentWriteFailed    = "failed"
	agentWriteNotRun    = "not_run"
)

// agentWriteOutcome is one write call of a reply: the tool, what it was about, and how it ended.
type agentWriteOutcome struct {
	Tool   string `json:"tool"`
	Target string `json:"target,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type agentWriteLedger struct {
	outcomes []agentWriteOutcome
}

// agentIsFlowWriteTool reports whether name is a Flow tool that changes workspace data.
func agentIsFlowWriteTool(name string) bool {
	return agentChangeToolAccess()[agentToolBaseName(name)] == "write"
}

// agentLedgerTool is the name a call is listed under in a ledger, and whether it belongs there: a Flow write tool,
// or a connector (MCP) tool offered in ctx that may write (its connector does not mark it read-only, or the
// workspace's connector policy treats it as a write). Read tools are not changes.
func agentLedgerTool(ctx context.Context, name string) (string, bool) {
	if agentIsFlowWriteTool(name) {
		return agentToolBaseName(name), true
	}
	if tool, ok := offeredConnectorTool(ctx, name); ok && !tool.ReadOnly {
		return agentConnectorLedgerName(tool), true
	}
	return "", false
}

// agentConnectorLedgerName names a connector tool in a ledger: the tool and its connector.
func agentConnectorLedgerName(tool connectorTool) string {
	return tool.RemoteName + " · " + tool.Policy.label()
}

// agentWriteOutcomeFor is the ledger entry of a write call (tool is its ledger name).
func agentWriteOutcomeFor(tool string, call domain.AgentToolCall, status, reason string) agentWriteOutcome {
	var args, result map[string]any
	_ = json.Unmarshal(call.Arguments, &args)
	if status == agentWriteSucceeded {
		_ = json.Unmarshal(call.Result, &result)
	}
	// The issue as the request named it (DEV-4), not the identifier a team move gave it.
	target := firstString(args, "id", "issueId", "issue", "identifier")
	if target == "" || agentInternalID.MatchString(target) {
		if named := agentCallTarget(args, result); named != "" {
			target = named
		}
	}
	return agentWriteOutcome{Tool: tool, Target: agentClip(target, 200), Status: status, Reason: agentClip(strings.TrimSpace(reason), 2000)}
}

// agentClip cuts text to at most limit bytes on a character boundary.
func agentClip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit-3], "") + "..."
}

// record notes a write call's outcome; calls of other tools are ignored.
func (l *agentWriteLedger) record(ctx context.Context, call domain.AgentToolCall, status, reason string) {
	if l == nil {
		return
	}
	if tool, ok := agentLedgerTool(ctx, call.Name); ok {
		l.outcomes = append(l.outcomes, agentWriteOutcomeFor(tool, call, status, reason))
	}
}

func (l *agentWriteLedger) attempted() bool { return l != nil && len(l.outcomes) > 0 }

func (l *agentWriteLedger) counts() (succeeded, failed, notRun int) {
	for _, outcome := range l.outcomes {
		switch outcome.Status {
		case agentWriteSucceeded:
			succeeded++
		case agentWriteFailed:
			failed++
		default:
			notRun++
		}
	}
	return
}

// agentOutcomeClaim matches text that reports how changes went: failures, successes, refusals, permission errors
// (English and Chinese). Future or conditional wording ("I'll move them") does not match.
var agentOutcomeClaim = regexp.MustCompile(`(?i)` +
	`\b(?:failed|failures?|succeeded|successful(?:ly)?|permissions?|not permitted|not allowed|forbidden|denied|unauthori[sz]ed|no access|access rights)\b` +
	`|\b(?:could ?n[o']t|cannot|can't|unable to|(?:was|were) not able to|did ?n[o']t)\s+(?:be\s+)?(?:move|migrate|transfer|update|change|assign|edit|modify|delete|create|apply)` +
	`|\b(?:have|has|were|was|been|are now|is now)\s+(?:all\s+|now\s+|successfully\s+|been\s+)*(?:moved|migrated|transferred|updated|changed|reassigned|deleted|created)\b` +
	`|\bI(?:'ve| have)?\s+(?:moved|migrated|transferred|updated|changed|reassigned|deleted|created)\b` +
	`|失败|成功|权限|无权|没有访问|拒绝|禁止` +
	`|(?:无法|不能|没法|未能|没能|没有)(?:将|把)?[^。！？\n]{0,24}?(?:移动|迁移|转移|移到|移入|转入|更新|修改|调整|删除|创建)` +
	`|已(?:经)?(?:全部|成功)?(?:将|把)?[^。！？\n]{0,24}?(?:移动|迁移|转移|移到|移入|转入|更新|修改|调整|删除|创建)`)

// agentFailureClaim matches text that says changes failed or were refused.
var agentFailureClaim = regexp.MustCompile(`(?i)` +
	`\b(?:failed|failures?|permissions?|not permitted|not allowed|forbidden|denied|unauthori[sz]ed|no access)\b` +
	`|\b(?:could ?n[o']t|cannot|can't|unable to|(?:was|were) not able to)\s+(?:be\s+)?(?:move|migrate|transfer|update|change|assign|edit|modify|delete|create|apply)` +
	`|失败|权限|无权|没有访问|拒绝|禁止|(?:无法|不能|没法|未能|没能)(?:将|把)?[^。！？\n]{0,24}?(?:移动|迁移|转移|移到|移入|转入|更新|修改|调整|删除|创建)`)

// agentUngroundedOutcomeNudge tells the model its answer reported results no tool produced.
const agentUngroundedOutcomeNudge = "Your last reply reported the outcome of changes (what failed, what succeeded, or a permission problem), but no write tool ran in this reply: nothing was changed and no permission was checked, so there is no outcome to report. Do not report results you did not get from a tool. If the user asked for a change, call the write tool now (for example save_issue with `id` and `team` to move an issue); Flow returns the real error when the user may not make it. Otherwise say plainly that nothing was changed."

// agentUngroundedOutcome reports whether a final answer (a turn without tool calls) reports change outcomes in a
// reply that ran no write, for a request to change something while write tools are on.
func (s *server) agentUngroundedOutcome(request string, turn agentProviderTurn, ledger *agentWriteLedger) bool {
	return len(turn.ToolCalls) == 0 && s.agentUngroundedText(request, turn.Text, ledger)
}

// agentUngroundedText reports whether answer, a reply's final text, reports change outcomes while the reply ran no
// write, for a request to change something while write tools are on.
func (s *server) agentUngroundedText(request, answer string, ledger *agentWriteLedger) bool {
	if !s.agent.ToolsEnabled || !s.agent.WriteTools || ledger.attempted() {
		return false
	}
	return agentChangeIntent.MatchString(request) && agentOutcomeClaim.MatchString(answer)
}

// agentNothingExecutedText replaces an answer that kept reporting outcomes no tool produced.
func agentNothingExecutedText(chinese bool) string {
	if chinese {
		return "未执行任何更改：本次回复没有调用任何写入工具，因此没有任何操作被执行（成功 0 项，失败 0 项），也没有进行权限检查，不存在权限错误。如需执行，请再发送一次请求。"
	}
	return "Nothing was changed: this reply did not call any tool to carry out the request, so no change was executed (0 succeeded, 0 failed) and no permission was checked. Ask again to retry."
}

// agentWriteSummaryMaxItems caps the failures and skipped calls a summary lists one by one.
const agentWriteSummaryMaxItems = 20

// The headings a summary starts with: an answer that has one was already reported (a worker run's reply checked at
// its last word is checked again when the worker posts it).
const (
	agentSummaryHeading   = "**Result from Flow:**"
	agentSummaryHeadingZH = "**Flow 执行结果：**"
)

// summary is Flow's account of a reply's writes, or "" when there is nothing the answer could have got wrong: no
// write, or every write succeeded and the answer claims no failure.
func (l *agentWriteLedger) summary(answer string, chinese bool) string {
	if !l.attempted() {
		return ""
	}
	succeeded, failed, notRun := l.counts()
	if failed == 0 && notRun == 0 && !agentFailureClaim.MatchString(answer) {
		return ""
	}
	executed := succeeded + failed
	var text strings.Builder
	if chinese {
		fmt.Fprintf(&text, agentSummaryHeadingZH+" 请求 %d 项更改，已执行 %d 项（成功 %d 项，失败 %d 项），未执行 %d 项。", len(l.outcomes), executed, succeeded, failed, notRun)
	} else {
		fmt.Fprintf(&text, agentSummaryHeading+" %d change(s) requested: %d executed (%d succeeded, %d failed), %d not executed.", len(l.outcomes), executed, succeeded, failed, notRun)
	}
	listed := 0
	for _, outcome := range l.outcomes {
		if outcome.Status == agentWriteSucceeded {
			continue
		}
		if listed == agentWriteSummaryMaxItems {
			if chinese {
				text.WriteString("\n- ……")
			} else {
				text.WriteString("\n- …")
			}
			break
		}
		listed++
		target := outcome.Target
		if target == "" {
			target = outcome.Tool
		}
		reason := agentClip(outcome.Reason, 300)
		switch {
		case chinese && outcome.Status == agentWriteFailed:
			fmt.Fprintf(&text, "\n- 失败：%s（%s）：%s", target, outcome.Tool, reason)
		case chinese:
			fmt.Fprintf(&text, "\n- 未执行：%s（%s）：%s", target, outcome.Tool, reason)
		case outcome.Status == agentWriteFailed:
			fmt.Fprintf(&text, "\n- Failed: %s (%s): %s", target, outcome.Tool, reason)
		default:
			fmt.Fprintf(&text, "\n- Not executed: %s (%s): %s", target, outcome.Tool, reason)
		}
	}
	return text.String()
}

// report is a final answer as Flow passes it on: sentences contrary to the ledger rewritten (correct), and Flow's
// summary at the end when there is one. An answer that already carries the summary is left as it is.
func (l *agentWriteLedger) report(answer string, chinese bool) string {
	if strings.Contains(answer, agentSummaryHeading) || strings.Contains(answer, agentSummaryHeadingZH) {
		return answer
	}
	answer = l.correct(answer, chinese)
	if summary := l.summary(answer, chinese); summary != "" {
		if strings.TrimSpace(answer) != "" {
			answer = strings.TrimRight(answer, " \t\n") + "\n\n"
		}
		answer += summary
	}
	return answer
}

// A sentence that says every change failed, or that none went through.
var agentAllFailedClaim = regexp.MustCompile(`(?i)` +
	`\b(?:all|every|each|both)\b[^.!?。！？\n]{0,80}?(?:\b(?:failed|denied|refused|rejected)\b|\b(?:could ?n[o']t|cannot|can't|unable to|(?:was|were) not able to|(?:was|were)n't|(?:was|were) not)\s+(?:be\s+)?(?:moved|migrated|transferred|updated|changed|assigned|edited|modified|deleted|created|applied|move|migrate|transfer|update|change|assign|edit|modify|delete|create|apply)\b)` +
	`|\b(?:none|neither)\b[^.!?。！？\n]{0,60}?\b(?:succeeded|worked|went through|(?:was|were|has been|have been)\s+(?:moved|migrated|transferred|updated|changed|reassigned|deleted|created))\b` +
	`|(?:全部|全都|都|均|一律|统统|所有|每个|每一个)[^。！？!?\n]{0,30}?(?:失败|没有成功|未成功|无法|不能|未能|没能|没有权限|无权)` +
	`|(?:没有|无)(?:一个|一项|任何)[^。！？!?\n]{0,20}?成功`)

// A sentence that says every change succeeded.
var agentAllSucceededClaim = regexp.MustCompile(`(?i)` +
	`\b(?:all|every|each|both|everything)\b[^.!?。！？\n]{0,80}?\b(?:succeeded|successful(?:ly)?|moved|migrated|transferred|updated|changed|reassigned|deleted|created|applied|done|completed)\b` +
	`|(?:全部|全都|都|均|所有|每个|每一个|统统)[^。！？!?\n]{0,30}?(?:成功|已(?:经)?(?:被)?(?:移动|迁移|转移|移到|移入|转入|更新|修改|调整|删除|创建|完成))`)

// A sentence that qualifies its claim ("all but one", "其余", "not all") is not a claim about every change.
var agentPartialWording = regexp.MustCompile(`(?i)\b(?:except|but|other than|apart from|besides|only|some|partially|partly|remaining|rest|not all|not every|while|whereas|although|however|if|when|once|will|would|should)\b|除了|除此|除去|之外|以外|其余|其他|其它|部分|只有|仅|但|不过|其中|并非|不是所有|有的|个别|如果|若|将会|将要`)

// agentSentenceLead is the indentation and list marker a rewritten sentence keeps.
var agentSentenceLead = regexp.MustCompile(`^\s*(?:[-*•]\s+|\d+[.)]\s+|>\s*)?`)

// correct rewrites each sentence of answer that says all changes failed while some succeeded, or that all succeeded
// while some failed or did not run, into Flow's count of them (the first such sentence; later ones are dropped).
// Only a sentence that is about every change is rewritten; a qualified one ("all but DEV-2 failed") is kept, and
// Flow's summary still follows the answer.
func (l *agentWriteLedger) correct(answer string, chinese bool) string {
	if !l.attempted() {
		return answer
	}
	succeeded, failed, notRun := l.counts()
	var out strings.Builder
	corrected := false
	last := 0
	for _, span := range agentSentenceSpans(answer) {
		sentence := answer[span[0]:span[1]]
		if len(sentence) > 400 || agentPartialWording.MatchString(sentence) {
			continue
		}
		contrary := false
		switch {
		case agentAllFailedClaim.MatchString(sentence):
			contrary = succeeded > 0
		case agentAllSucceededClaim.MatchString(sentence) && !agentFailureClaim.MatchString(sentence):
			contrary = failed+notRun > 0
		}
		if !contrary {
			continue
		}
		out.WriteString(answer[last:span[0]])
		lead := agentSentenceLead.FindString(sentence)
		if !corrected {
			out.WriteString(lead)
			if chinese {
				fmt.Fprintf(&out, "Flow 更正：%d 项更改中，成功 %d 项，失败 %d 项，未执行 %d 项。", len(l.outcomes), succeeded, failed, notRun)
			} else {
				fmt.Fprintf(&out, "Correction from Flow: of %d change(s), %d succeeded, %d failed and %d were not executed.", len(l.outcomes), succeeded, failed, notRun)
			}
		}
		corrected = true
		last = span[1]
	}
	if !corrected {
		return answer
	}
	out.WriteString(answer[last:])
	return out.String()
}

// agentSentenceSpans splits text into sentences: at line breaks, at Chinese and English sentence marks, and at a
// period followed by a space or the end. Each span keeps its closing mark; line breaks stay between spans.
func agentSentenceSpans(text string) [][2]int {
	spans := [][2]int{}
	start := 0
	add := func(end int) {
		if strings.TrimSpace(text[start:end]) != "" {
			spans = append(spans, [2]int{start, end})
		}
	}
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		next := index + size
		switch {
		case r == '\n':
			add(index)
			start = next
		case r == '。' || r == '！' || r == '？' || r == '!' || r == '?':
			add(next)
			start = next
		case r == '.' && (next == len(text) || text[next] == ' ' || text[next] == '\n' || text[next] == '\t'):
			add(next)
			start = next
		}
		index = next
	}
	add(len(text))
	return spans
}
