package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
)

// A built-in agent thread run (a delegated issue, a mention) can read code through connector (MCP) tools before it
// acts. Reading is preparation, not the work: without a bound a model asked to implement something kept searching
// the same files until the run hit maxAgentToolTurns and never started Coding. These budgets bound that preparation;
// once one is spent (or only builtinDecisionTurns turns are left) connector tools are no longer offered and the model
// is told to decide: start the coding session, or reply with what it found and the blocker.
const (
	// builtinPrepMaxCalls is how many connector calls a run may make before it must decide.
	builtinPrepMaxCalls = 8
	// builtinCodingPrepMaxCalls is the smaller budget when the issue asks for code changes and Coding can start:
	// the coding agent reads the repository itself, so at most a few lookups (say, which repository) come first.
	builtinCodingPrepMaxCalls = 3
	// builtinPrepMaxBytes bounds the connector output a run reads before it must decide.
	builtinPrepMaxBytes = 64 << 10
	// builtinMaxNoProgress is how many calls without new information (an identical repeat, arguments the tool's
	// schema rejects, a failed call, a call over budget) a run may make before it must decide.
	builtinMaxNoProgress = 2
	// builtinDecisionTurns are the last turns of a run, which are offered no connector tools.
	builtinDecisionTurns = 2
)

// builtinCodingContext is what a built-in thread run knows about coding: whether it can start a coding session,
// whether the issue or the instructions ask for code changes, and a coding environment they name.
type builtinCodingContext struct {
	CanCode bool
	// Development is set when the request clearly asks for code changes: development wording, no investigation /
	// explanation / planning / review wording in the request, and no "do not change the code" anywhere.
	Development   bool
	EnvironmentID string
	// EnvironmentKnown is set when EnvironmentID is one of the workspace's coding environments: the one the
	// instructions name, the only one matching the issue's repository, or the workspace's only environment.
	EnvironmentKnown bool
	// Environments are the workspace's coding environments, listed in the blocker when none is known.
	Environments []domain.CodingEnvironment
	Issue        string // the delegated or mentioned issue's identifier
	Chinese      bool
}

// builtinDevelopmentIntent matches a request to change code: implement, fix, refactor, write tests, open a PR/MR
// (Chinese and English). Investigating or planning alone does not match.
var builtinDevelopmentIntent = regexp.MustCompile(`(?i)实现|开发|修复|修改代码|改代码|写代码|编写代码|提交代码|单元测试|跑测试|运行测试|执行测试|补充测试|重构|草稿\s*(MR|PR)|合并请求|拉取请求|(创建|提交?|开|发起)\s*(一个|个)?\s*(草稿)?\s*(MR|PR)\b|` +
	`\b(implement|implementing|fix|develop|refactor|patch|write (the )?code|code changes?|pull request|merge request|draft (pr|mr)|open (a |an )?(draft )?(pr|mr)|run (the )?tests|add (unit )?tests|unit tests?)\b`)

// builtinDevelopmentNoun is a development word used as a noun ("why the fix failed"), removed before matching.
var builtinDevelopmentNoun = regexp.MustCompile(`(?i)\b(the|a|this|that|its|their|our)\s+(fix|patch|implementation|refactor(ing)?)\b`)

// builtinAnalysisIntent matches a request to investigate, explain, plan, estimate or review — work that reads and
// answers, never one that changes code.
var builtinAnalysisIntent = regexp.MustCompile(`(?i)分析|原因|为什么|为何|解释|讲解|讲一下|说明一下|调研|排查|定位|评估|估算|工作量|方案|计划|规划|评审|审查|审阅|总结|梳理|看一下|看看|` +
	`\b(analy[sz]e|analy[sz]ing|analysis|investigate|investigating|investigation|explain|explaining|explanation|why|root cause|plan|planning|proposal|estimate|estimating|estimation|review|reviewing|assess|assessment|evaluate|evaluation|summari[sz]e|research|look into|take a look)\b`)

// builtinCodingVeto matches an explicit "do not change the code" ("先不要改代码", "don't change code"): it vetoes
// Coding wherever it appears.
var builtinCodingVeto = regexp.MustCompile(`(?i)(不要|别|不用|无需|不需要|请勿|勿|不必|暂不|先不|不)\s*(去)?\s*(修改|改动|改|动|写|编写|提交)\s*(任何)?\s*(的)?\s*代码|只(做)?(分析|调研|评估|评审|讨论|出方案|给方案|给出方案)|` +
	`\b(don'?t|do not|without|no need to|not to|never)\s+(change|changing|modify|modifying|touch|touching|write|writing|edit|editing|commit|committing)\s+(any\s+|the\s+)?code\b|\bno code changes?\b|\b(don'?t|do not)\s+(implement|fix|start coding|open (a |an )?(pr|mr))\b`)

// builtinRequestsDevelopment reports whether request (the delegator's instructions, the mention's comment or the
// person's latest reply) clearly asks for code changes. The issue's title and description count for a delegation:
// without instructions the issue is the request, and instructions that neither ask for development nor for
// analysis ("please handle this") defer to it; a mention or a reply (no issue text) decides on its own words.
// An investigation, explanation, plan, estimate or review never counts, and an explicit "do not change the code"
// anywhere vetoes it.
func builtinRequestsDevelopment(request, issueTitle, issueDescription string) bool {
	if builtinCodingVeto.MatchString(request + "\n" + issueTitle + "\n" + issueDescription) {
		return false
	}
	development := func(text string) bool {
		return builtinDevelopmentIntent.MatchString(builtinDevelopmentNoun.ReplaceAllString(text, ""))
	}
	if request = strings.TrimSpace(request); request != "" {
		if builtinAnalysisIntent.MatchString(request) {
			return false
		}
		if development(request) || issueTitle+issueDescription == "" {
			return development(request)
		}
	}
	// The issue is the request: development only when nothing in it asks for analysis instead (a mixed request, such
	// as "analyse the cause and propose a fix", is left to the agent and never starts Coding by itself).
	issue := issueTitle + "\n" + issueDescription
	return development(issue) && !builtinAnalysisIntent.MatchString(issue)
}

// builtinEnvironmentMention captures an environment id the instructions spell out ("environmentId: env_123",
// "环境 ID：env_123").
var builtinEnvironmentMention = regexp.MustCompile("(?i)(?:environment[ _-]?id|coding environment|环境\\s*(?:id|ID)?)\\s*[:：=]?\\s*[`\"'“]?([A-Za-z0-9][A-Za-z0-9_.:-]{2,63})")

// builtinCodingContextFor reads the coding context of a run from the task and its issue. The request is the
// person's latest message when it is a reply in the thread, otherwise the instructions (a delegation) or the
// comment (a mention); the issue's own text counts only for a delegation.
func (s *server) builtinCodingContextFor(ctx context.Context, task domain.AgentTask, data domain.Bootstrap) builtinCodingContext {
	result := builtinCodingContext{CanCode: s.agent.ToolsEnabled && s.agent.WriteTools && s.codingEnabled() && workspaceFeatureEnabled(data.WorkspaceSettings, coding.FeatureFlag)}
	initial := firstNonEmpty(task.InitialPrompt, task.Prompt)
	text := strings.TrimSpace(initial + "\n" + task.Prompt)
	var issue *domain.Issue
	if len(data.Issues) == 1 && (task.ResourceType == "" || task.ResourceType == "issue") {
		issue = &data.Issues[0]
		result.Issue = issue.Identifier
		text += "\n" + issue.Title + "\n" + issue.Description
	}
	request, title, description := initial, "", ""
	if issue != nil && task.Trigger == "delegation" {
		request = strings.TrimPrefix(strings.TrimSpace(initial), issue.Title)
		title, description = issue.Title, issue.Description
	}
	if reply := strings.TrimSpace(task.Prompt); reply != "" && reply != strings.TrimSpace(initial) && task.PendingTool == nil {
		// A reply in the thread is the request now (an approval answer continues the earlier one).
		request, title, description = reply, "", ""
	}
	result.Development = builtinRequestsDevelopment(request, title, description)
	result.Chinese = containsHan(text)
	if match := builtinEnvironmentMention.FindStringSubmatch(text); match != nil {
		result.EnvironmentID = strings.TrimRight(match[1], ".:")
	}
	if result.CanCode && s.store != nil {
		if environments, err := s.store.ListCodingEnvironments(ctx, task.WorkspaceKey, false); err == nil {
			result.Environments = environments
			result.resolveEnvironment(text, issue, data)
		}
	}
	return result
}

// resolveEnvironment decides the coding environment of the run: the one the instructions name (by id); else the
// only environment whose repository the issue links or the text names; else the workspace's only environment. A
// named id that is not an environment, or several candidates, leave it unknown.
func (c *builtinCodingContext) resolveEnvironment(text string, issue *domain.Issue, data domain.Bootstrap) {
	byID := func(id string) bool {
		return id != "" && slices.ContainsFunc(c.Environments, func(item domain.CodingEnvironment) bool { return item.ID == id })
	}
	if byID(c.EnvironmentID) {
		c.EnvironmentKnown = true
		return
	}
	for _, environment := range c.Environments {
		if environment.ID != "" && strings.Contains(text, environment.ID) {
			c.EnvironmentID, c.EnvironmentKnown = environment.ID, true
			return
		}
	}
	if c.EnvironmentID != "" {
		return // named, but not an environment of this workspace
	}
	repositories := []string{}
	if issue != nil {
		for _, linked := range linkedRepositoriesForIssue(data, *issue) {
			repositories = append(repositories, strings.ToLower(linked.Repository))
		}
	}
	lower := strings.ToLower(text)
	matches := []string{}
	for _, environment := range c.Environments {
		repository := strings.ToLower(strings.Trim(environment.Repository, "/"))
		if repository != "" && (slices.Contains(repositories, repository) || strings.Contains(lower, repository)) {
			matches = append(matches, environment.ID)
		}
	}
	switch {
	case len(matches) == 1:
		c.EnvironmentID, c.EnvironmentKnown = matches[0], true
	case len(matches) == 0 && len(c.Environments) == 1:
		c.EnvironmentID, c.EnvironmentKnown = c.Environments[0].ID, true
	}
}

// environmentBlocker says why Coding cannot start without a person choosing the environment.
func (c builtinCodingContext) environmentBlocker() string {
	names := make([]string, 0, len(c.Environments))
	for index, environment := range c.Environments {
		if index == 8 {
			names = append(names, fmt.Sprintf("… %d more", len(c.Environments)-index))
			break
		}
		names = append(names, fmt.Sprintf("%s (%s, %s)", firstNonEmpty(environment.Name, environment.ID), environment.ID, environment.Repository))
	}
	switch {
	case len(c.Environments) == 0 && c.Chinese:
		return "工作区还没有 Coding 环境，需要管理员在 设置 › Coding 环境 中添加"
	case len(c.Environments) == 0:
		return "the workspace has no coding environment yet; an admin adds one in Settings › Coding environments"
	case c.EnvironmentID != "" && c.Chinese:
		return "指定的 environmentId " + c.EnvironmentID + " 不是本工作区的 Coding 环境；可用的环境：" + strings.Join(names, "，")
	case c.EnvironmentID != "":
		return "environmentId " + c.EnvironmentID + " is not a coding environment of this workspace; available: " + strings.Join(names, ", ")
	case c.Chinese:
		return "说明中没有指定 Coding 环境，且有多个环境可选：" + strings.Join(names, "，") + "。请在说明中写明 environmentId"
	}
	return "the instructions name no coding environment and several could apply: " + strings.Join(names, ", ") + ". Name one with environmentId"
}

func containsHan(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// builtinRunGuard bounds a run's read-only preparation and makes repeated calls converge.
type builtinRunGuard struct {
	coding     builtinCodingContext
	calls      int
	bytes      int
	noProgress int
	// results are the connector calls made in this run, by tool and canonical arguments.
	results map[string]string
	// decided is set once the decision nudge was sent.
	decided bool
	// started is what start_coding_session returned in this run.
	started *codingStartOutcome
	// hadSession is set when the session already had a coding session when the run began (an earlier run
	// started Coding): the run never starts another one by itself.
	hadSession bool
}

// codingDue reports whether this run must still enter Coding: the request clearly asks for code changes, Coding is
// available, and neither this run nor an earlier one started it.
func (g *builtinRunGuard) codingDue() bool {
	return g.coding.CanCode && g.coding.Development && g.started == nil && !g.hadSession
}

// transferCall is the start_coding_session call Flow makes for the agent when the model did not (codingDue with a
// known environment).
func (g *builtinRunGuard) transferCall(issueTitle string) domain.AgentToolCall {
	prompt := "Make the code changes " + firstNonEmpty(g.coding.Issue, "the issue") + " asks for"
	if issueTitle != "" {
		prompt += " (" + issueTitle + ")"
	}
	prompt += ", following the issue's description and the delegator's instructions. Add or update the tests, run them, and open a draft pull/merge request."
	args, _ := json.Marshal(map[string]any{"issueId": g.coding.Issue, "environmentId": g.coding.EnvironmentID, "prompt": prompt})
	return domain.AgentToolCall{ID: "flow_coding_transfer", Name: startCodingSessionTool, Arguments: args}
}

func newBuiltinRunGuard(coding builtinCodingContext) *builtinRunGuard {
	return &builtinRunGuard{coding: coding, results: map[string]string{}}
}

func (g *builtinRunGuard) maxCalls() int {
	if g.coding.CanCode && g.coding.Development && g.started == nil {
		return builtinCodingPrepMaxCalls
	}
	return builtinPrepMaxCalls
}

// exhausted reports whether the preparation budget is spent.
func (g *builtinRunGuard) exhausted() bool {
	return g.calls >= g.maxCalls() || g.bytes >= builtinPrepMaxBytes || g.noProgress >= builtinMaxNoProgress
}

// mustDecide reports whether turn round (0-based) runs without connector tools.
func (g *builtinRunGuard) mustDecide(round int) bool {
	return g.exhausted() || round >= maxAgentToolTurns-builtinDecisionTurns
}

// canonicalToolCallKey is a call's tool and arguments with object keys sorted, so the same search spelled with
// another key order or spacing is the same call.
func canonicalToolCallKey(call domain.AgentToolCall) string {
	var args any
	if json.Unmarshal(call.Arguments, &args) != nil {
		return call.Name + "\x00" + strings.TrimSpace(string(call.Arguments))
	}
	raw, _ := json.Marshal(args) // maps marshal with sorted keys
	return call.Name + "\x00" + string(raw)
}

// before answers a connector call without running it when it repeats an earlier identical call (the earlier result
// and a nudge) or the budget is spent (a refusal). handled false means the call runs.
func (g *builtinRunGuard) before(call domain.AgentToolCall) (content string, failed, handled bool) {
	if cached, ok := g.results[canonicalToolCallKey(call)]; ok {
		g.noProgress++
		return "You already made this exact call in this session; it was not run again and its result is unchanged (below). " +
			"Do not repeat it. " + g.decisionText() + "\n\nEarlier result:\n" + cached, false, true
	}
	if g.exhausted() {
		g.noProgress++
		return agentToolErrorContent(fmt.Errorf("the read-only budget of this session is used up (%d connector calls, %d KB read); the call was not run. %s", g.calls, g.bytes>>10, g.decisionText())), true, true
	}
	return "", false, false
}

// after records a connector call that ran: a failure (arguments the tool's schema rejects, an upstream error) or
// an empty result is a call without progress; every result is kept for repeats.
func (g *builtinRunGuard) after(call domain.AgentToolCall, content string, noProgress bool) {
	g.calls++
	g.bytes += len(content)
	if noProgress {
		g.noProgress++
	}
	g.results[canonicalToolCallKey(call)] = content
}

// decisionText tells the model what to do instead of searching further.
func (g *builtinRunGuard) decisionText() string {
	if g.started != nil {
		return "The coding session is already requested; do not search further. Reply now with its status (" + g.started.delivery.label() + "), not with a claim that the work is done."
	}
	if g.coding.CanCode && g.coding.Development {
		args := "issueId " + firstNonEmpty(g.coding.Issue, "of the issue")
		if g.coding.EnvironmentKnown {
			args += ", environmentId " + g.coding.EnvironmentID
		} else if g.coding.CanCode {
			args += "; no coding environment is known: " + g.coding.environmentBlocker()
		}
		return "Stop searching and decide now: call start_coding_session (" + args + ") with a prompt that states the change, the tests to run and that a draft MR is wanted — the coding agent reads the repository itself. If something blocks it, reply with the blocker instead."
	}
	return "Stop searching and reply now with what you found, what is left, and any blocker."
}

// decisionNudge is appended to the last tool result of the turn before the first turn without connector tools.
func (g *builtinRunGuard) decisionNudge() string {
	return "\n\n[Flow] Connector tools are no longer offered in this session. " + g.decisionText()
}

// codingDelivery is how far a coding session got: requested is not delivered.
type codingDelivery string

const (
	codingNotStarted       codingDelivery = "notStarted"
	codingAwaitingApproval codingDelivery = "awaitingApproval"
	codingAwaitingInput    codingDelivery = "awaitingInput"
	codingRunning          codingDelivery = "running"
	codingDelivered        codingDelivery = "delivered"
	codingFailed           codingDelivery = "failed"
)

func (d codingDelivery) label() string {
	switch d {
	case codingAwaitingApproval:
		return "waiting for a team member's approval"
	case codingAwaitingInput:
		return "waiting for the user to choose the repository or answer a question"
	case codingRunning:
		return "running; nothing is delivered yet"
	case codingDelivered:
		return "delivered with code changes and a pull/merge request"
	case codingFailed:
		return "failed"
	}
	return "not started"
}

// codingSessionDelivery reads a session's delivery state. Delivered needs evidence: changed files and a pull/merge
// request, and the session done with its work.
func codingSessionDelivery(session domain.CodingSession) codingDelivery {
	switch {
	case session.ApprovalState == "required" || session.Phase == domain.CodingPhaseAwaitingApproval:
		return codingAwaitingApproval
	case session.State == domain.CodingSessionError || session.State == domain.CodingSessionStale:
		return codingFailed
	case session.State == domain.CodingSessionAwaitingInput:
		return codingAwaitingInput
	case session.State == domain.CodingSessionComplete && len(session.Diffs) > 0 && (session.LatestPullRequest != nil || len(session.PullRequests) > 0):
		return codingDelivered
	case session.State == domain.CodingSessionComplete:
		return codingFailed // ended without changes or a pull/merge request
	}
	return codingRunning
}

// codingStartOutcome is what one start_coding_session call returned.
type codingStartOutcome struct {
	delivery  codingDelivery
	sessionID string
	blocker   string
}

// recordCodingStart reads a start_coding_session result into the run's state and returns the result the model
// reads, with the delivery state and how to report it.
func (s *server) recordCodingStart(ctx context.Context, workspace string, g *builtinRunGuard, content string, failed bool) string {
	var result map[string]any
	if json.Unmarshal([]byte(content), &result) != nil || result == nil {
		if failed {
			g.started = &codingStartOutcome{delivery: codingNotStarted, blocker: strings.TrimSpace(content)}
		}
		return content
	}
	outcome := &codingStartOutcome{delivery: codingNotStarted}
	outcome.sessionID, _ = result["sessionId"].(string)
	started, _ := result["started"].(bool)
	switch {
	case failed || result["code"] != nil || result["error"] != nil:
		blocker, _ := result["error"].(string)
		outcome.blocker = firstNonEmpty(blocker, "the coding session could not start")
	case result["approvalRequired"] == true:
		outcome.delivery = codingAwaitingApproval
	case result["environmentSuggestions"] != nil:
		outcome.delivery = codingAwaitingInput
	case started || outcome.sessionID != "":
		outcome.delivery = codingRunning
	}
	if outcome.sessionID != "" && outcome.blocker == "" {
		if session, err := s.store.CodingSession(ctx, workspace, outcome.sessionID); err == nil {
			outcome.delivery = codingSessionDelivery(session)
		}
	}
	if outcome.delivery == codingNotStarted && outcome.blocker == "" {
		message, _ := result["message"].(string)
		outcome.blocker = firstNonEmpty(message, "the coding session did not start")
	}
	g.started = outcome
	result["deliveryStatus"] = string(outcome.delivery)
	if outcome.delivery == codingNotStarted {
		result["reportAs"] = "Coding did not start: report this blocker plainly; do not say the work is done."
	} else {
		result["reportAs"] = "The coding session is " + outcome.delivery.label() + ". Say that Coding was started and its state; the code changes, test results and the draft MR appear in the session when it finishes. Do not say the development is done."
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return content
	}
	return string(raw)
}

// builtinCompletionClaim matches a reply saying the development is done or a PR/MR is open (a future clause such
// as "完成后" is removed before matching, see claimsCodingCompletion).
var builtinCompletionClaim = regexp.MustCompile(`(?i)(开发|实现|修改|修复|代码|编码|功能)(已经?|均已|全部)?完成|已经?完成(了)?(开发|实现|修改|修复|代码|编码)|已经?实现|已经?修复|代码已提交|已提交(代码|修改)|已(创建|开启|提交|发起).{0,8}(MR|PR|合并请求)|(MR|PR|合并请求)已(创建|开启|提交|发起)|已合并|` +
	`\b(i|we)('ve| have)? (implemented|fixed)\b|\b(has|have) been (implemented|fixed|merged)\b|\b(development|implementation|the fix|the change|the code changes?|the work) (is|are) (now )?(done|complete|completed|finished)\b|\b(opened|created) (a|the) (draft )?(pr|mr|pull request|merge request)\b`)

var builtinFutureCompletion = strings.NewReplacer("完成后", "", "修复后", "", "实现后", "", "提交后", "")

func claimsCodingCompletion(text string) bool {
	return builtinCompletionClaim.MatchString(builtinFutureCompletion.Replace(text))
}

// builtinFinalReply is the reply a run posts. A run that requested Coding (or one asked for code changes) may say
// the development is done only when its coding session shows the evidence (changes and a pull/merge request);
// otherwise the claim is replaced with the session's real state, or with why Coding did not start.
func (s *server) builtinFinalReply(ctx context.Context, task domain.AgentTask, g *builtinRunGuard, text string) string {
	delivery, blocker := codingNotStarted, ""
	if g.started != nil {
		delivery, blocker = g.started.delivery, g.started.blocker
	}
	sessionID := task.CodingSessionID
	if g.started != nil && g.started.sessionID != "" {
		sessionID = g.started.sessionID
	}
	if sessionID != "" && s.store != nil {
		if session, err := s.store.CodingSession(ctx, task.WorkspaceKey, sessionID); err == nil {
			delivery, blocker = codingSessionDelivery(session), session.Error
		}
	}
	tracked := g.started != nil || sessionID != "" || g.coding.Development
	if !tracked || delivery == codingDelivered || !claimsCodingCompletion(text) {
		return text
	}
	return codingDeliveryReply(delivery, blocker, g.coding)
}

// codingDeliveryReply states a coding session's delivery state in the language of the issue.
func codingDeliveryReply(delivery codingDelivery, blocker string, coding builtinCodingContext) string {
	issue := firstNonEmpty(coding.Issue, "this issue")
	if coding.Chinese {
		issue = firstNonEmpty(coding.Issue, "该 Issue")
		switch delivery {
		case codingAwaitingApproval:
			return "已为 " + issue + " 发起 Coding 会话，正在等待团队成员批准；批准前不会修改代码。"
		case codingAwaitingInput:
			return "已为 " + issue + " 发起 Coding 会话，正在等待选择代码仓库或回答问题；尚未修改代码。"
		case codingRunning:
			return "已为 " + issue + " 启动 Coding 会话，正在运行。代码修改、测试结果和草稿 MR 完成后会显示在会话中；目前尚未交付。"
		case codingFailed:
			return "为 " + issue + " 启动的 Coding 会话未能完成交付" + chineseBlocker(blocker) + "，没有可用的代码修改或 MR。"
		}
		return "尚未为 " + issue + " 启动 Coding 会话，因此没有修改代码" + chineseBlocker(blocker) + "。"
	}
	switch delivery {
	case codingAwaitingApproval:
		return "Started a coding session for " + issue + "; it is waiting for a team member's approval, and no code has changed yet."
	case codingAwaitingInput:
		return "Started a coding session for " + issue + "; it is waiting for a repository choice or an answer, and no code has changed yet."
	case codingRunning:
		return "Started a coding session for " + issue + "; it is running. The code changes, test results and the draft MR will appear in the session when it finishes — nothing is delivered yet."
	case codingFailed:
		return "The coding session for " + issue + " did not deliver" + englishBlocker(blocker) + "; there are no usable code changes or MR."
	}
	return "Coding was not started for " + issue + ", so no code was changed" + englishBlocker(blocker) + "."
}

func chineseBlocker(blocker string) string {
	if blocker = strings.TrimSpace(blocker); blocker == "" {
		return ""
	}
	return "（原因：" + truncateSettingsText(blocker, 300) + "）"
}

func englishBlocker(blocker string) string {
	if blocker = strings.TrimSpace(blocker); blocker == "" {
		return ""
	}
	return " (" + truncateSettingsText(blocker, 300) + ")"
}

// validateConnectorArguments checks a connector call's arguments against the tool's inputSchema before the call
// is sent: required properties are present, top-level values have the declared type, and a tool that declares its
// properties gets no others (unless additionalProperties or patternProperties allow them). The error tells the
// model what the tool accepts.
func validateConnectorArguments(tool string, schema json.RawMessage, args map[string]any) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(schema, &root) != nil || root == nil {
		return nil
	}
	properties := map[string]map[string]any{}
	_, declared := root["properties"]
	if declared {
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(root["properties"], &raw)
		for name, value := range raw {
			var property map[string]any
			_ = json.Unmarshal(value, &property)
			properties[name] = property
		}
	}
	var required []string
	_ = json.Unmarshal(root["required"], &required)
	extraAllowed := !declared
	if additional, ok := root["additionalProperties"]; ok && strings.TrimSpace(string(additional)) != "false" {
		extraAllowed = true
	}
	if _, ok := root["patternProperties"]; ok {
		extraAllowed = true
	}
	accepted := make([]string, 0, len(properties))
	for name := range properties {
		accepted = append(accepted, name)
	}
	sort.Strings(accepted)
	problems := []string{}
	unknown := []string{}
	for name := range args {
		if _, ok := properties[name]; !ok && !extraAllowed {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		problems = append(problems, fmt.Sprintf("unknown property %q", name))
	}
	sort.Strings(required)
	for _, name := range required {
		if value, ok := args[name]; !ok || value == nil {
			problems = append(problems, fmt.Sprintf("missing required property %q", name))
		}
	}
	names := make([]string, 0, len(args))
	for name := range args {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		property, ok := properties[name]
		if !ok || args[name] == nil {
			continue
		}
		types := schemaTypes(property["type"])
		if len(types) > 0 && !slices.ContainsFunc(types, func(kind string) bool { return jsonValueHasType(args[name], kind) }) {
			problems = append(problems, fmt.Sprintf("property %q must be %s", name, strings.Join(types, " or ")))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	list := "none"
	if len(accepted) > 0 {
		list = strings.Join(accepted, ", ")
	}
	return fmt.Errorf("invalid arguments for %s: %s (accepted properties: %s; required: %s). The call was not sent; fix the arguments or use another tool", tool, strings.Join(problems, "; "), list, firstNonEmpty(strings.Join(required, ", "), "none"))
}

func schemaTypes(value any) []string {
	switch kind := value.(type) {
	case string:
		return []string{kind}
	case []any:
		types := []string{}
		for _, item := range kind {
			if name, ok := item.(string); ok {
				types = append(types, name)
			}
		}
		return types
	}
	return nil
}

func jsonValueHasType(value any, kind string) bool {
	switch kind {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "null":
		return value == nil
	}
	return true
}

// connectorToolDescription is a connector tool's description for the model. The tool is offered under a hashed
// name, so the description leads with the connector, the tool's own name and its arguments — kept ahead of the
// server's text, which is cut to fit.
func connectorToolDescription(label, name, description string, schema json.RawMessage) string {
	var root struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(schema, &root)
	args := make([]string, 0, len(root.Properties))
	for property := range root.Properties {
		if slices.Contains(root.Required, property) {
			args = append(args, property+" (required)")
		} else {
			args = append(args, property)
		}
	}
	sort.Strings(args)
	summary := "none"
	if len(args) > 0 {
		summary = strings.Join(args, ", ")
	}
	head := fmt.Sprintf("%s tool %s. Arguments: %s. ", label, name, truncateSettingsText(summary, 600))
	return truncateSettingsText(head+strings.TrimSpace(description), 2000)
}
