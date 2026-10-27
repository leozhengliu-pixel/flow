package coding

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Turn write scope (write, noPublish, readOnly): what a follow-up turn may do
// to the repository and the draft pull request. The runner enforces it (a
// read-only turn never commits or pushes and fails when the repository
// changed anyway); the API decides it once, when the turn is queued, and
// sends it with the turn so the runner does not have to guess:
//
//  1. An explicit scope sent with the request (the optional `scope` field of
//     POST /api/coding/sessions/{id}/prompt) always wins.
//  2. Turns Flow starts to change the branch (fix a PR, rebase, lint, test,
//     environment fixes) are write turns.
//  3. Otherwise the request is parsed clause by clause (ParseTurnScope):
//     a negation only reaches the verb right after it and is dropped when it
//     is qualified by a target ("不要提交到 main", "never force-push"), a
//     specific object ("don't commit the debug logs", "不要修改测试") or a
//     condition that will be met in the turn ("don't push until the tests
//     pass"). Positive publish requests in other clauses ("改完推到分支")
//     count too.
//  4. When the parser is not sure (no signal at all, or signals that
//     contradict each other) and a model is configured, one short bounded
//     classification call decides; any failure keeps the parser's answer.
//     The turn is queued at once with the parser's answer, marked pending,
//     and the model's answer is settled in the background; a runner never
//     receives a turn whose scope is still pending (turn_scope_pending.go).
//
// The decision is stored on the queued turn, so redeliveries and restarts of
// the runner reuse it (one classification per turn).

// TurnScopeModel classifies a request the parser is unsure about. It returns
// TurnScopeWrite, TurnScopeNoPublish or TurnScopeReadOnly.
type TurnScopeModel interface {
	ClassifyTurnScope(ctx context.Context, request string) (string, error)
}

// TurnScopeModelFunc adapts a function to TurnScopeModel.
type TurnScopeModelFunc func(ctx context.Context, request string) (string, error)

// ClassifyTurnScope implements TurnScopeModel.
func (f TurnScopeModelFunc) ClassifyTurnScope(ctx context.Context, request string) (string, error) {
	return f(ctx, request)
}

// Sources of a turn's scope (Turn.ScopeSource).
const (
	TurnScopeSourceUser  = "user"  // chosen explicitly with the request
	TurnScopeSourceKind  = "kind"  // a turn Flow starts to change the branch
	TurnScopeSourceRules = "rules" // ParseTurnScope
	TurnScopeSourceModel = "model" // the model classification
)

// TurnScopeModelTimeout bounds the model classification of one request.
const TurnScopeModelTimeout = 6 * time.Second

// turnScopeModelInput bounds the request text sent to the model (runes).
const turnScopeModelInput = 2000

// ErrInvalidTurnScope is returned for a scope that is not one of the three.
var ErrInvalidTurnScope = errors.New("scope must be write, noPublish or readOnly")

// NormalizeTurnScope maps the accepted spellings of an explicit scope to
// TurnScopeWrite, TurnScopeNoPublish or TurnScopeReadOnly; "" and "auto"
// mean "infer it" and return "".
func NormalizeTurnScope(scope string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "auto":
		return "", nil
	case "write", "develop":
		return TurnScopeWrite, nil
	case "nopublish", "no-publish", "no_publish":
		return TurnScopeNoPublish, nil
	case "readonly", "read-only", "read_only", "verify":
		return TurnScopeReadOnly, nil
	}
	return "", ErrInvalidTurnScope
}

// writeTurnKinds are the turns Flow starts for a change of the branch.
var writeTurnKinds = map[string]bool{TurnKindFixPR: true, TurnKindRebase: true, TurnKindLint: true, TurnKindTest: true, "configureEnvironment": true, "fixEnvironment": true}

// TurnScopeDecision is the resolved scope of a turn.
type TurnScopeDecision struct {
	Scope  string
	Source string
	// Rule and Match explain a rules decision (logs and tests).
	Rule  string
	Match string
}

// ResolveTurnScope decides a turn's scope: explicit > kind > rules, with the
// model consulted only when the rules are unsure. A model error, timeout or
// unusable answer keeps the rules' answer.
func ResolveTurnScope(ctx context.Context, kind, explicit, request string, model TurnScopeModel) TurnScopeDecision {
	decision, ask := PlanTurnScope(kind, explicit, request, model)
	if !ask {
		return decision
	}
	return AskTurnScopeModel(ctx, model, request, decision)
}

// PlanTurnScope is ResolveTurnScope without the model call: the decision of
// the explicit scope, the turn kind or the parser, and whether the model
// should still be asked (the parser is unsure and a model is configured).
// Prompt queues the turn with this decision right away and settles the
// model's answer in the background (turn_scope_pending.go).
func PlanTurnScope(kind, explicit, request string, model TurnScopeModel) (TurnScopeDecision, bool) {
	if scope, err := NormalizeTurnScope(explicit); err == nil && scope != "" {
		return TurnScopeDecision{Scope: scope, Source: TurnScopeSourceUser}, false
	}
	if writeTurnKinds[kind] {
		return TurnScopeDecision{Scope: TurnScopeWrite, Source: TurnScopeSourceKind}, false
	}
	parsed := ParseTurnScope(request)
	decision := TurnScopeDecision{Scope: parsed.Scope, Source: TurnScopeSourceRules, Rule: parsed.Rule, Match: parsed.Match}
	return decision, !parsed.Sure && model != nil
}

// AskTurnScopeModel asks the model about a request (bounded input, at most
// TurnScopeModelTimeout); an error, timeout or unusable answer returns
// fallback.
func AskTurnScopeModel(ctx context.Context, model TurnScopeModel, request string, fallback TurnScopeDecision) TurnScopeDecision {
	decision := fallback
	if model == nil {
		return decision
	}
	text := strings.TrimSpace(request)
	if runes := []rune(text); len(runes) > turnScopeModelInput {
		text = string(runes[:turnScopeModelInput])
	}
	callCtx, cancel := context.WithTimeout(ctx, TurnScopeModelTimeout)
	defer cancel()
	answer, err := model.ClassifyTurnScope(callCtx, text)
	if err != nil {
		return decision
	}
	if scope, err := NormalizeTurnScope(answer); err == nil && scope != "" {
		return TurnScopeDecision{Scope: scope, Source: TurnScopeSourceModel, Rule: "model"}
	}
	return decision
}

// ParsedTurnScope is the result of ParseTurnScope.
type ParsedTurnScope struct {
	Scope string
	// Sure is false when the request carries no signal at all or signals
	// that contradict each other; ResolveTurnScope then asks the model.
	Sure  bool
	Rule  string
	Match string
}

// TurnScopeModelPrompt is the system prompt of the model classification.
const TurnScopeModelPrompt = `You classify the latest message a person sent to a coding agent that works on a git branch with an open draft pull request. Decide what the agent may do to the repository in this turn.
- write: the agent may change files; its changes are committed, pushed and the pull request is updated. Use it for development requests, including ones that only restrict where or what to push (for example "don't push to main", "don't commit the .env file", "don't push until the tests pass").
- noPublish: the agent may change files but must not commit, push or update the pull request this turn ("try it locally, don't commit yet", "don't push until I review").
- readOnly: the repository and the pull request must stay exactly as they are; the person wants verification, inspection, an explanation or an answer only ("just verify", "keep the repo unchanged", "don't modify anything").
When unsure, answer write. Reply with exactly one word: write, noPublish or readOnly.`

// ── the clause parser ──────────────────────────────────────────────────────

const (
	negEN = `(?:do not|don't|dont|don’t|never|no need to|without|please don't)`
	negZH = `(?:不要|不得|不能|不准|不许|别|勿|无需|不用|不需要|切勿|禁止)`
	// Words between a negation and its verb that keep the negation on it.
	negFillEN = `(?:(?:yet|ever|actually|please|any|make|making|go|be|to|git)\s+)*`
	negFillZH = `(?:先|再|急着|着急|马上|立刻|立即|做|进行|任何|去|自己|这次|本次)*`
)

func reOf(pattern string) *regexp.Regexp { return regexp.MustCompile(`(?i)` + pattern) }

var (
	// Clause boundaries: sentence ends, commas and contrast/sequence words.
	// A "." only ends a sentence before whitespace or the end (".env", "v1.2").
	clauseSplit = reOf(`[;!?\n。；！？，,]+|\.(?:\s+|$)|\s+(?:but|then|and then|however|instead)\s+|但是|但|然后|不过|并且|同时`)

	// Explicit statements that the repository must stay as it is.
	keepObject     = `(?:repo(?:sitory)?|code(?:base)?|branch(?:es)?|working ?tree|worktree|work ?tree|files?|everything|state|git state|workspace|pr|pull request|merge request|mr|head)`
	keepDeterminer = `(?:(?:the|this|your|my|our|existing|current|open|whole|entire|both|draft)\s+)*`
	readOnlyStrong = []*regexp.Regexp{
		reOf(`\b(?:keep|leave)\s+` + keepDeterminer + keepObject + `(?:\s*(?:,|and|&)\s*` + keepDeterminer + keepObject + `)*\s+(?:(?:exactly|completely|entirely|fully|all|totally)\s+)?(?:unchanged|untouched|as[- ]is|as it is|as they are|intact|unmodified|alone|clean)\b`),
		reOf(`\bread[- ]only\b`),
		reOf(`\bno (?:code |file )?(?:changes|edits|modifications)\b`),
		reOf(`\bwithout (?:making |doing )?(?:any )?(?:code |file )?(?:changes|edits|modifications)(?:\s+to\s+(?:the\s+)?(?:repo(?:sitory)?|code(?:base)?|branch|files))?\s*$`),
		reOf(`\bdry[- ]run\b`),
		reOf(`保持\s*(?:现有的?|当前的?|已有的?)?\s*(?:仓库|代码库|代码|分支|工作区|一切|所有文件|文件|现有\s*PR|PR|MR|现状|HEAD)(?:\s*(?:和|与|及|以及|、)\s*(?:现有的?|当前的?|已有的?)?\s*(?:仓库|代码库|代码|分支|工作区|PR|MR|HEAD))*\s*(?:都|完全)?(?:不变|原样|不动|不改|不被修改)`),
		reOf(`(?:仓库|代码库|分支|工作区)(?:和[^，。]{0,10})?(?:保持|维持)(?:不变|原样|现状)`),
		reOf(`只读`),
	}
	// "only verify"-style requests: read-only unless the request also asks
	// for changes.
	readOnlyWeak = []*regexp.Regexp{
		reOf(`\b(?:verif(?:y|ication)|check|review|inspection|investigation|analysis|report)[- ]only\b`),
		reOf(`\b(?:only|just)\s+(?:verify|check|confirm|review|inspect|look|investigate|analy[sz]e|answer|report|tell me|explain|diagnose|read)\b`),
		reOf(`(?:只|仅|仅仅|就|只是)(?:需要)?(?:做|进行|帮我)?(?:验证|核实|核对|检查|确认|看看|看一下|回答|分析|排查|解释|说明|调查|审查|评审|汇报)`),
	}

	// A negated change of the repository: the verb and what follows it.
	negModifyEN = []*regexp.Regexp{
		reOf(`\b` + negEN + `\s+` + negFillEN + `(?:any\s+)?(?:code\s+|file\s+)?(changes|modifications|edits)\b(.*)$`),
		reOf(`\b` + negEN + `\s+` + negFillEN + `((?:chang|modif|edit|touch|alter|writ|delet|mess)\w*)\b(.*)$`),
	}
	negModifyZH = reOf(negZH + negFillZH + `(修改|改动|更改|变更|改|动|碰)(.*)$`)
	// Objects that make a negated change repository-wide.
	repoWideEN = reOf(`^(?:\s*(?:with|to|in|on|of)?\s*(?:anything|any(?:thing)?|a thing|a single (?:file|line)|the|this|that|it|any|existing|current|open|draft|whole|entire|of|code(?:base)?|repo(?:sitory)?|branch|working tree|worktree|files?|pr|pull request|merge request|mr|state|at all|yet|please|here|there|now|for now|in this turn|this turn|anything else|either)\b)*\s*$`)
	repoWideZH = reOf(`^(?:\s|任何|的|了|一下|东西|内容|地方|文件|代码库|代码|仓库|分支|现有|当前|已有|PR|MR|合并请求|工作区|呢|吧|啊|哦|这次|本次|暂时|也)*$`)

	// A negated publish: the verb and what follows it.
	negPublishEN = reOf(`\b` + negEN + `\s+` + negFillEN + `(?:(?:directly|force)[- ]?\s*)?(commit(?:ting)?|push(?:ing)?|publish(?:ing)?|(?:open|create|raise|submit)(?:ing)?\s+(?:a\s+|the\s+|any\s+)?(?:new\s+|draft\s+)?(?:pr|pull request|merge request|mr))\b(.*)$`)
	negPublishZH = reOf(negZH + negFillZH + `(直接)?(提交|推送|推到|推上去?|commit|push|发布|(?:开|提|创建)\s*(?:一个|个)?\s*(?:pr|mr|合并请求))(.*)$`)
	// Targets that turn "don't push" into "don't push there".
	publishTargetEN = reOf(`\b(?:to|into|onto|on|in|against)\s+(?:the\s+)?(?:main|master|trunk|develop|default branch|base branch|upstream|origin/\w+|prod(?:uction)?|release(?: branch)?|other branches|another branch)\b|\bdirectly\b|\bforce\b|--force|\bforce[- ]push`)
	publishTargetZH = reOf(`(?:到|进|至|上|入)\s*(?:main|master|主干|主分支|默认分支|基础分支|基线|生产|线上|其他分支|别的分支)|直接|强推|强制`)
	// "until the tests pass": the condition is met within the turn (write).
	untilChecksEN = reOf(`\b(?:until|before|unless)\b.*\b(?:tests?|ci|checks?|lint(?:er)?|build|it|everything)\b.*\b(?:pass(?:es|ing)?|green|succeeds?|works?|builds?|compiles?|done|finished)\b`)
	untilChecksZH = reOf(`(?:测试|单测|CI|ci|检查|lint|构建|编译|全部)[^，,]{0,8}(?:通过|跑通|变绿|成功|过了|没问题)(?:之前|前|以前)?`)
	// "until I review": the person decides later (noPublish).
	untilPersonEN = reOf(`\b(?:until|before|unless)\b.*\b(?:i|we|you ask|someone|a reviewer|they)\b`)
	untilPersonZH = reOf(`(?:我|我们|有人|审核人)[^，,]{0,8}(?:确认|审核|审查|review|看过|看完|同意|批准|说)(?:之前|前|以前|再说)?|等我`)
	// Fillers stripped from what follows a negated publish.
	publishRestEN = reOf(`^(?:\s*(?:or|and|nor|/|it|them|this|that|anything|any|the|my|your|our|changes?|code|work|fix|branch|yet|now|for now|right now|this time|in this turn|this turn|at all|either|please|until later|locally|commit(?:ting)?|push(?:ing)?|publish(?:ing)?|open|create|update|a|pr|pull request|merge request|mr)\b)*\s*$`)
	publishRestZH = reOf(`^(?:\s|或|或者|和|、|也不要?|也别|任何|东西|内容|代码|改动|修改|更改|变更|了|呢|吧|啊|哦|这次|本次|暂时|先|提交|推送|推|commit|push|PR|pr|MR|mr|合并请求|上去|远端|远程)*$`)

	// Development and publish verbs (counted when not negated).
	devEN     = reOf(`\b(?:fix(?:es|ing)?|implement\w*|add(?:s|ing)?|creat(?:e|ing)|writ(?:e|ing)|updat(?:e|ing)|chang(?:e|ing)|modif(?:y|ying)|refactor\w*|remov(?:e|ing)|delet(?:e|ing)|renam(?:e|ing)|migrat(?:e|ing)|bump|upgrad(?:e|ing)|support|optimi[sz]e|clean up|move|extract|replac(?:e|ing)|rewrit(?:e|ing)|port|address|resolve|continue|apply|make (?:it|the|this)|build (?:a|the|an))\b`)
	devZH     = reOf(`修复|修一下|修好|修掉|实现|添加|增加|新增|加上|加个|加一个|改成|改为|改一下|改下|改完|改好|修改|重构|删除|删掉|移除|更新|补上|补充|编写|写一个|写个|支持|迁移|升级|优化|调整|开发|替换|继续`)
	publishEN = reOf(`\b(?:commit(?:\s+(?:it|this|that|them|the changes|your changes|everything))?|push(?:\s+(?:it|this|that|them|the branch|the changes|your changes|everything))?(?:\s+to\s+(?:the\s+|this\s+|our\s+|your\s+|a\s+)?(?:feature\s+|session\s+|same\s+|current\s+|remote\s+)?branch)?|open\s+(?:a\s+|the\s+)?(?:draft\s+)?(?:pr|pull request)|update\s+(?:the\s+|this\s+)?(?:pr|pull request)|publish)\b`)
	publishZH = reOf(`推到(?:分支|远端|远程|origin)?|推送|推上去|提交|提\s*(?:一个|个)?\s*(?:pr|mr)|更新\s*(?:pr|mr)|开\s*(?:一个|个)?\s*(?:pr|mr)`)
	// A verb is negated when a negation directly precedes it (or a negated
	// verb joined by "or": "don't commit or push").
	negatedBeforeEN = reOf(`(?:` + negEN + `|\bnor|\bnot|\bno)\s+` + negFillEN + `(?:\w+\s+(?:or|nor|and)\s+)?$`)
	negatedBeforeZH = reOf(`(?:` + negZH + `|不)` + negFillZH + `(?:[\p{Han}]{1,3}(?:或|或者|和|、|也不要?|也别))?(?:直接)?$`)
	// Topics rather than requests ("how you would fix it", "the fix").
	questionVerbEN = reOf(`\bhow\s+(?:to|you(?:'d| would| could| should| can)?|we(?:'d| would| could| should| can)?|i(?:'d| would| could| should| can)?|would you|should we|do we|can we|could we|it would)\s+\w+|\b(?:the|a|an|possible|potential|proposed|best|right|likely|your)\s+(?:fix|fixes|patch|change|changes|implementation|update)\b`)
	questionVerbZH = reOf(`(?:怎么|如何|怎样|该怎么|应该怎么)[^，,]{0,6}`)

	// A plain go-ahead continues the work (write).
	goAhead = reOf(`^(?:(?:ok(?:ay)?|yes|yep|yeah|sure|go ahead|go for it|continue|proceed|do it|please do|please|lgtm|sounds good|ship it|好的?|好吧|可以|行|是的?|对|继续|开始吧?|没问题|去做吧?|就这样|照做|同意|吧)[\s.!。！~,，]*)+$`)
)

// ParseTurnScope classifies a request clause by clause (see the package
// comment above). It never fails: requests without any signal are write
// turns, marked unsure.
func ParseTurnScope(request string) ParsedTurnScope {
	text := strings.TrimSpace(strings.ReplaceAll(request, "’", "'"))
	if text == "" {
		return ParsedTurnScope{Scope: TurnScopeWrite, Rule: "empty"}
	}
	if goAhead.MatchString(text) {
		return ParsedTurnScope{Scope: TurnScopeWrite, Sure: true, Rule: "goAhead", Match: text}
	}
	var strong, weak, noPublish, dev, publish, hedge string
	for _, clause := range clauseSplit.Split(text, -1) {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		if strong == "" {
			strong = firstScopeMatch(readOnlyStrong, clause)
		}
		if weak == "" {
			weak = firstScopeMatch(readOnlyWeak, clause)
		}
		if strong == "" {
			if match, ok := negatedModify(clause); ok {
				strong = match
			}
		}
		switch match, kind := negatedPublish(clause); kind {
		case "cue":
			if noPublish == "" {
				noPublish = match
			}
		case "hedge":
			if hedge == "" {
				hedge = match
			}
		}
		topic := questionVerbZH.ReplaceAllString(questionVerbEN.ReplaceAllString(clause, " "), " ")
		if dev == "" {
			dev = positiveMatch(devEN, devZH, topic)
		}
		if publish == "" {
			publish = positiveMatch(publishEN, publishZH, topic)
		}
	}
	asks := dev != "" || publish != ""
	switch {
	case strong != "":
		return ParsedTurnScope{Scope: TurnScopeReadOnly, Sure: !asks, Rule: "keepUnchanged", Match: strong}
	case noPublish != "":
		return ParsedTurnScope{Scope: TurnScopeNoPublish, Sure: publish == "", Rule: "noPublish", Match: noPublish}
	case weak != "" && asks:
		// "just check it and fix what's broken": changes may be wanted, the
		// publish is not obvious; the model decides, never-fail fallback.
		return ParsedTurnScope{Scope: TurnScopeNoPublish, Rule: "verifyAndChange", Match: weak}
	case weak != "":
		return ParsedTurnScope{Scope: TurnScopeReadOnly, Sure: true, Rule: "verifyOnly", Match: weak}
	case asks:
		return ParsedTurnScope{Scope: TurnScopeWrite, Sure: true, Rule: "develop", Match: firstNonEmpty(publish, dev)}
	}
	return ParsedTurnScope{Scope: TurnScopeWrite, Rule: "default", Match: hedge}
}

func firstScopeMatch(patterns []*regexp.Regexp, text string) string {
	for _, pattern := range patterns {
		if match := pattern.FindString(text); match != "" {
			return match
		}
	}
	return ""
}

// negatedModify finds "don't modify <repo-wide object>" in a clause; a
// specific object ("don't modify the tests") restricts the work instead.
func negatedModify(clause string) (string, bool) {
	for _, pattern := range negModifyEN {
		if m := pattern.FindStringSubmatch(clause); m != nil && repoWideEN.MatchString(m[2]) {
			return strings.TrimSpace(strings.TrimSuffix(m[0], m[2])), true
		}
	}
	if m := negModifyZH.FindStringSubmatch(clause); m != nil && repoWideZH.MatchString(m[2]) {
		return strings.TrimSpace(strings.TrimSuffix(m[0], m[2])), true
	}
	return "", false
}

// negatedPublish finds "don't commit / push" in a clause. It is a cue when
// it applies to the turn's work as a whole, a hedge (no signal, but worth a
// model look) when qualified by a target, and nothing when qualified by a
// specific object or by a condition the turn itself can meet.
func negatedPublish(clause string) (string, string) {
	if m := negPublishEN.FindStringSubmatchIndex(clause); m != nil {
		whole, rest := clause[m[0]:m[1]], clause[m[4]:m[5]]
		prefix := clause[:m[0]]
		match := strings.TrimSpace(clause[m[0]:m[4]])
		switch {
		case publishTargetEN.MatchString(whole):
			return match, "hedge"
		case untilPersonEN.MatchString(rest):
			return match, "cue"
		case untilChecksEN.MatchString(rest) || untilChecksEN.MatchString(prefix):
			return match, ""
		case publishRestEN.MatchString(rest):
			return match, "cue"
		}
		return match, ""
	}
	if m := negPublishZH.FindStringSubmatchIndex(clause); m != nil {
		whole, rest := clause[m[0]:m[1]], clause[m[6]:m[7]]
		prefix := clause[:m[0]]
		match := strings.TrimSpace(clause[m[0]:m[6]])
		switch {
		case m[2] >= 0 || publishTargetZH.MatchString(whole):
			return match, "hedge"
		case untilPersonZH.MatchString(prefix) || untilPersonZH.MatchString(rest):
			return match, "cue"
		case untilChecksZH.MatchString(prefix):
			return match, ""
		case publishRestZH.MatchString(rest):
			return match, "cue"
		}
		return match, ""
	}
	return "", ""
}

// ── earlier uncommitted changes ────────────────────────────────────────────
//
// Paths that already had uncommitted changes before a turn (left by an
// earlier turn, a restore or the person) are not committed by the turn
// unless the turn itself changes them. A request that explicitly asks to
// commit them too ("把之前未提交的改动也提交", "include the earlier
// uncommitted changes", "commit everything") sets Turn.IncludeLeftovers, so
// the runner commits the whole working tree. Only write turns ever commit.

var (
	// Earlier changes as an object: "the earlier uncommitted changes",
	// "之前未提交的改动", "遗留的修改".
	leftoverQualifierEN = `(?:earlier|previous|prior|existing|old|older|other|remaining|pending|leftover|left-over|local|outstanding)`
	leftoverNounEN      = reOf(`\b(?:(?:all|both)\s+)?(?:of\s+)?(?:the\s+|those\s+|these\s+|my\s+|your\s+)?(?:(?:` + leftoverQualifierEN + `)\s+)*(?:uncommitted|unstaged|leftover|left-over|earlier|previous|prior|pending|outstanding)\s+(?:(?:local|code|file)\s+)?(?:changes|edits|files|work|modifications)\b`)
	leftoverNounZH      = reOf(`(?:之前|此前|先前|以前|早先|上次|上一轮|原来|原先|原有|已有|现有|遗留|残留|剩下|剩余|其他|其它|工作区里?|本地)?(?:的)?(?:未|没|没有|还没|还没有|尚未)(?:被)?提交(?:的)?(?:改动|修改|更改|变更|文件|代码|内容|东西)|(?:之前|此前|先前|以前|早先|上次|上一轮|遗留|残留|剩下|剩余|原有)(?:的)?(?:改动|修改|更改|变更)`)
	// Verbs that take the earlier changes into the commit.
	leftoverVerbEN = reOf(`\b(?:commit(?:ting)?|includ(?:e|ing))\b`)
	leftoverVerbZH = reOf(`提交|commit|带上|包括|包含`)
	// "commit everything", "commit all (the) changes", "全部提交", "提交所有改动".
	commitEverything = []*regexp.Regexp{
		reOf(`\bcommit\s+(?:everything|all(?:\s+(?:of\s+)?(?:the\s+|my\s+|your\s+)?(?:changes|files|edits|work))?)\b`),
		reOf(`\binclude\s+everything\s+in\s+(?:the\s+|this\s+)?commit\b`),
		reOf(`(?:全部|所有|一切)(?:的)?(?:改动|修改|更改|变更|文件|东西|内容)?(?:都|也|一起|一并|全部)?(?:提交|commit)`),
		reOf(`(?:提交|commit)\s*(?:全部|所有|一切)(?:的)?(?:改动|修改|更改|变更|文件|东西|内容)?(?:$|[^\p{Han}]|吧|了|呢|啊|上去)`),
	}
	// Any negation in a clause keeps the earlier changes out ("不要提交之前的改动",
	// "don't commit the earlier changes"); a reminder is not a negation.
	leftoverReminder = reOf(`\b(?:don't|dont|do not|never)\s+forget\s+(?:to\s+)?|别忘了|别忘记|不要忘了|不要忘记|记得`)
	leftoverNegation = reOf(`\b(?:don't|dont|do not|never|not|no need|without|shouldn't|should not|mustn't|must not|avoid|except|excluding|exclude)\b|\b(?:leave|keep)\b.*\b(?:out|uncommitted|aside|alone|as is|separate)\b|不要|不用|不需要|无需|别|勿|不得|不能|不准|不许|禁止|切勿|没必要|不必|除了|排除|先留着|留着`)
)

// ParseIncludeLeftovers reports whether a request explicitly asks to commit
// the uncommitted changes that were in the working tree before the turn.
// A clause names the earlier changes and a verb that takes them in ("提交",
// "commit", "include"), or asks to commit everything; a negation in that
// clause cancels it. The earlier changes may be named in one clause and the
// verb follow in the next ("之前未提交的改动，也一起提交").
func ParseIncludeLeftovers(request string) bool {
	text := strings.TrimSpace(strings.ReplaceAll(request, "’", "'"))
	if text == "" {
		return false
	}
	pendingNoun := false
	for _, clause := range clauseSplit.Split(text, -1) {
		clause = strings.TrimSpace(leftoverReminder.ReplaceAllString(clause, " "))
		if clause == "" {
			continue
		}
		if leftoverNegation.MatchString(clause) {
			pendingNoun = false
			continue
		}
		if firstScopeMatch(commitEverything, clause) != "" {
			return true
		}
		noun := leftoverNounEN.MatchString(clause) || leftoverNounZH.MatchString(clause)
		// The verb outside the object ("未提交的改动" itself says "提交").
		rest := leftoverNounZH.ReplaceAllString(leftoverNounEN.ReplaceAllString(clause, " "), " ")
		verb := leftoverVerbEN.MatchString(rest) || leftoverVerbZH.MatchString(rest)
		switch {
		case noun && verb, pendingNoun && verb:
			return true
		case noun:
			pendingNoun = true
		default:
			pendingNoun = false
		}
	}
	return false
}

// positiveMatch is the first development or publish verb in a clause that
// no negation directly precedes.
func positiveMatch(en, zh *regexp.Regexp, clause string) string {
	for _, loc := range en.FindAllStringIndex(clause, -1) {
		if !negatedBeforeEN.MatchString(clause[:loc[0]]) {
			return clause[loc[0]:loc[1]]
		}
	}
	for _, loc := range zh.FindAllStringIndex(clause, -1) {
		if !negatedBeforeZH.MatchString(clause[:loc[0]]) {
			return clause[loc[0]:loc[1]]
		}
	}
	return ""
}
