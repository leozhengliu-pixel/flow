package coding

import (
	"context"
	"regexp"
	"strings"
)

// Coding intent (docs/coding-sessions-design.md §15, track L). Linear's
// agent starts a coding session immediately when it is asked to change code
// ("fix this bug", "open a PR") and does not when it is asked to investigate,
// plan or create tasks. Flow applies the same rule in two places: the
// start_coding_session tool description steers the chat model, and the tool
// itself re-checks the user's request with ClassifyCodingIntent so a model
// that over-eagerly calls the tool for an investigation is refused with
// guidance instead of spending a sandbox.

// CodingIntent is the classified intent of a request.
type CodingIntent string

const (
	// IntentCode asks for code changes: start a coding session now.
	IntentCode CodingIntent = "code"
	// IntentNoCode asks to investigate, plan, answer or create tasks.
	IntentNoCode CodingIntent = "noCode"
	// IntentUnclear could not be classified; the caller decides.
	IntentUnclear CodingIntent = "unclear"
)

// IntentModel is the optional model fallback for requests the rules cannot
// classify. Implementations return IntentCode, IntentNoCode or IntentUnclear.
type IntentModel interface {
	ClassifyCodingIntent(ctx context.Context, request string) (CodingIntent, error)
}

// IntentModelFunc adapts a function to IntentModel.
type IntentModelFunc func(ctx context.Context, request string) (CodingIntent, error)

// ClassifyCodingIntent implements IntentModel.
func (f IntentModelFunc) ClassifyCodingIntent(ctx context.Context, request string) (CodingIntent, error) {
	return f(ctx, request)
}

// IntentDecision is the result of ClassifyCodingIntent.
type IntentDecision struct {
	Intent CodingIntent `json:"intent"`
	// Rule names the rule that decided ("explicitNoCode", "codeVerb",
	// "investigateVerb", "model", "default").
	Rule string `json:"rule"`
	// Match is the phrase that triggered the rule, for logs and guidance.
	Match string `json:"match,omitempty"`
}

// StartsCoding reports whether a session may start for this decision. Only
// an explicit no-code request blocks: when the request is unclear, the
// agent's own decision to call the tool stands.
func (d IntentDecision) StartsCoding() bool { return d.Intent != IntentNoCode }

var (
	// Phrases that rule out coding even when a code verb appears
	// ("create tasks to fix the login bug", "don't open a PR yet").
	explicitNoCodePatterns = compileIntentPatterns(
		`\b(?:create|make|file|open|write|add|draft|split(?: this)? into)\s+(?:a\s+|some\s+|the\s+)?(?:follow[- ]up\s+)?(?:sub[- ]?)?(?:tasks?|issues?|tickets?|sub[- ]?issues?|todos?)\b`,
		`\bbreak\s+(?:it|this|that|them|the\s+\w+)?\s*down\b`,
		`\b(?:write|make|draft|come up with|put together|create)\s+(?:a\s+|an\s+)?(?:\w+\s+)?(?:plan|proposal|spec|rfc|design doc)\b`,
		`\b(?:do not|don't|dont|never|no need to|without)\s+(?:\w+\s+){0,2}(?:change|changing|touch|touching|write|writing|modify|modifying|edit|editing|commit|committing|push|pushing)\s+(?:any\s+)?(?:the\s+)?(?:code|files?|repo|repository)\b`,
		`\b(?:do not|don't|dont|no need to)\s+(?:\w+\s+){0,2}(?:open|create|make|submit)\s+(?:a\s+)?(?:pr|pull request|merge request|mr)\b`,
		`\bno\s+code\s+changes?\b`,
		`\bjust\s+(?:investigate|look|research|analy[sz]e|plan|explain|summari[sz]e)\b`,
		`创建(?:子)?(?:任务|工单|issue)`, `拆分(?:成|为)?(?:任务|子任务)`, `(?:写|做|制定)(?:一个|一份)?(?:计划|方案)`,
		`(?:不要|别|无需|不用)(?:修改|改|动|提交)(?:任何)?代码`, `(?:不要|别)(?:开|提|创建)\s*(?:一个|个)?\s*(?:pr|mr|合并请求)`,
	)
	// Explicit requests for code changes.
	codeVerbPatterns = compileIntentPatterns(
		`\b(?:open|create|make|raise|submit|send|put up|draft)\s+(?:a\s+|the\s+)?(?:draft\s+)?(?:pr|pull request|merge request|mr|patch)\b`,
		`\brefactor(?:s|ing)?\b`, `\b(?:add|write|update)\s+(?:a\s+|some\s+|the\s+|unit\s+|integration\s+|e2e\s+)?(?:tests?|specs?|test coverage)\b`,
		`\b(?:make|apply|ship|code|write)\s+(?:the|this|that|a)\s+(?:change|fix|code)\b`, `\bwrite\s+(?:the\s+)?code\b`,
		`\b(?:bump|upgrade|downgrade)\s+\S+`, `\brename\s+\S+`, `\bresolve\s+(?:the\s+|this\s+)?(?:bug|issue|conflicts?|lint)\b`,
		`\brebase\b`, `\baddress\s+(?:the\s+|all\s+)?(?:review\s+)?(?:comments?|feedback|threads?)\b`,
		`\bmake\s+(?:the\s+)?(?:tests?|ci|checks?|build|lint)\s+(?:pass|green)\b`, `\bchange\s+the\s+code\b`, `\bmigrate\s+\S+\s+to\b`, `\bremove\s+(?:the\s+)?(?:dead|unused)\s+code\b`, `\bcode\s+(?:it|this)\s+up\b`,
		`修复`, `实现`, `重构`, `(?:提|开|创建|提交)\s*(?:一个|个)?\s*(?:pr|mr|合并请求)`, `(?:改|修改|写)代码`, `(?:补|加|写)(?:单元)?测试`,
	)
	// Bare code verbs that are also nouns ("the fix"); they are matched after
	// fixNounPattern removes the noun uses.
	bareCodeVerbPatterns = compileIntentPatterns(`\bfix(?:es|ing|ed)?\b`, `\bhot\s?fix\b`, `\bpatch\b`, `\bimplement(?:s|ing)?\b`)
	// Investigation, planning and question verbs (no code unless a code verb
	// also appears, as in "investigate and fix").
	investigateVerbPatterns = compileIntentPatterns(
		`\binvestigat(?:e|es|ing|ion)\b`, `\blook\s+into\b`, `\bresearch\b`, `\banaly[sz](?:e|is|ing)\b`, `\btriage\b`,
		`\bplan(?:ning|s)?\b`, `\bscope\b`, `\bestimat(?:e|ion)\b`, `\bsummari[sz]e\b`, `\bexplain\b`, `\bdiagnos(?:e|is)\b`,
		`\bwhy\b`, `\bwhat\s+(?:is|are|causes|caused)\b`, `\bhow\s+(?:does|do|would|should)\b`, `\broot\s+cause\b`, `\breview\b`,
		`\bfind\s+out\b`, `\bdig\s+into\b`,
		`调查`, `排查`, `研究`, `分析`, `规划`, `计划`, `总结`, `解释`, `为什么`, `评估`,
	)
)

// fixNounPattern finds "fix" used as a noun ("plan the fix", "propose a
// fix"), which is not a request to change code by itself.
var fixNounPattern = regexp.MustCompile(`(?i)\b(?:the|a|an|possible|potential|proposed|best|right|likely)\s+(?:fix|fixes|patch|implementation)\b`)

func compileIntentPatterns(patterns ...string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		compiled = append(compiled, regexp.MustCompile(`(?i)`+pattern))
	}
	return compiled
}

func firstIntentMatch(patterns []*regexp.Regexp, text string) string {
	for _, pattern := range patterns {
		if match := pattern.FindString(text); match != "" {
			return match
		}
	}
	return ""
}

// ClassifyCodingIntent applies Linear's rule to a user request: explicit
// no-code phrases win, then explicit code verbs, then investigation verbs;
// anything else goes to the model (when given) and is otherwise unclear. A
// model error never blocks: the result is unclear.
func ClassifyCodingIntent(ctx context.Context, request string, model IntentModel) IntentDecision {
	text := strings.Join(strings.Fields(request), " ")
	if text == "" {
		return IntentDecision{Intent: IntentUnclear, Rule: "default"}
	}
	if match := firstIntentMatch(explicitNoCodePatterns, text); match != "" {
		return IntentDecision{Intent: IntentNoCode, Rule: "explicitNoCode", Match: match}
	}
	if match := firstIntentMatch(codeVerbPatterns, text); match != "" {
		return IntentDecision{Intent: IntentCode, Rule: "codeVerb", Match: match}
	}
	if match := firstIntentMatch(bareCodeVerbPatterns, fixNounPattern.ReplaceAllString(text, " ")); match != "" {
		return IntentDecision{Intent: IntentCode, Rule: "codeVerb", Match: match}
	}
	if match := firstIntentMatch(investigateVerbPatterns, text); match != "" {
		return IntentDecision{Intent: IntentNoCode, Rule: "investigateVerb", Match: match}
	}
	if model != nil {
		intent, err := model.ClassifyCodingIntent(ctx, text)
		if err == nil && (intent == IntentCode || intent == IntentNoCode || intent == IntentUnclear) {
			return IntentDecision{Intent: intent, Rule: "model"}
		}
	}
	return IntentDecision{Intent: IntentUnclear, Rule: "default"}
}

// NoCodeGuidance is the tool result the agent gets when it tried to start a
// session for a request that asked for no code changes.
func NoCodeGuidance(decision IntentDecision) string {
	return "Not started: the request asks to investigate, plan or create tasks (\"" + decision.Match +
		"\"), not to change code. Do that work directly and reply with the result; offer to start a coding session " +
		"if a code change looks useful. Start one only when the user asks for code changes, a fix or a pull request."
}
