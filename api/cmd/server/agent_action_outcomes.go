package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"flow/api/internal/domain"
)

// What a chat reply says it did comes from the tool calls it made, never from the model's text alone. A model asked
// to move issues into a private team read the team list and then answered that every move failed for lack of
// permission without calling save_issue: no move was attempted, and there was no permission error to report.
//
// agentWriteLedger records each Flow write call of one reply with its outcome. Two checks use it:
//   - a final answer that reports change outcomes (failures, successes, permission errors) while the reply ran no
//     write at all is not passed on: the model is told once that nothing ran and to call the tool (nothing was
//     written yet, so the retry cannot repeat a write); a second such answer is replaced by Flow's own statement
//     that nothing was executed;
//   - a reply whose writes did not all succeed, or that claims failures its writes did not have, ends with Flow's
//     count of what was executed, what was not, what succeeded and what failed, with each failure's reason as the
//     tool returned it.

const (
	agentWriteSucceeded = "succeeded"
	agentWriteFailed    = "failed"
	agentWriteNotRun    = "not_run"
)

// agentWriteOutcome is one write call of a reply: the tool, what it was about, and how it ended.
type agentWriteOutcome struct {
	Tool   string
	Target string
	Status string
	Reason string
}

type agentWriteLedger struct {
	outcomes []agentWriteOutcome
}

// agentIsFlowWriteTool reports whether name is a Flow tool that changes workspace data.
func agentIsFlowWriteTool(name string) bool {
	return agentChangeToolAccess()[agentToolBaseName(name)] == "write"
}

// record notes a write call's outcome; calls of other tools are ignored.
func (l *agentWriteLedger) record(call domain.AgentToolCall, status, reason string) {
	if l == nil || !agentIsFlowWriteTool(call.Name) {
		return
	}
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
	l.outcomes = append(l.outcomes, agentWriteOutcome{Tool: agentToolBaseName(call.Name), Target: target, Status: status, Reason: strings.TrimSpace(reason)})
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
	if !s.agent.ToolsEnabled || !s.agent.WriteTools || len(turn.ToolCalls) > 0 || ledger.attempted() {
		return false
	}
	return agentChangeIntent.MatchString(request) && agentOutcomeClaim.MatchString(turn.Text)
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
		fmt.Fprintf(&text, "**Flow 执行结果：** 请求 %d 项更改，已执行 %d 项（成功 %d 项，失败 %d 项），未执行 %d 项。", len(l.outcomes), executed, succeeded, failed, notRun)
	} else {
		fmt.Fprintf(&text, "**Result from Flow:** %d change(s) requested: %d executed (%d succeeded, %d failed), %d not executed.", len(l.outcomes), executed, succeeded, failed, notRun)
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
		reason := outcome.Reason
		if len(reason) > 300 {
			reason = reason[:297] + "..."
		}
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
