package main

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Output intent: what a loop's name and instructions ask a run to do with
// each output kind (status updates, issues, comments, documents). A kind can
// be read, created, updated, appended to or forbidden; the required kinds are
// the ones the text asks to write. The run's prompt states them, its tools
// refuse the forbidden ones, and acceptance checks the required ones exist.

// The operations a loop's text asks for on an output kind.
const (
	loopOpCreate = "create"
	loopOpUpdate = "update"
	loopOpAppend = "append"
	// loopOpWrite asks for the output without saying whether it is new.
	loopOpWrite = "write"
	loopOpRead  = "read"
)

// loopOpRank orders operations from least to most specific; a kind asked
// for in several clauses keeps the most specific write.
var loopOpRank = map[string]int{loopOpRead: 0, loopOpWrite: 1, loopOpCreate: 2, loopOpUpdate: 3, loopOpAppend: 4}

// loopOutputExpectation is what a loop's runs are expected to produce.
type loopOutputExpectation struct {
	// Outputs must exist for the run to complete.
	Outputs []string
	// ReadOnly: the text names outputs only as things to read, check or
	// summarise (or forbids writing), so runs report rather than publish.
	ReadOnly bool
	// Operations is the write each required kind asks for (create, update,
	// append or write); empty for explicit outputs.
	Operations map[string]string
	// Prohibited kinds are forbidden by the text; a run's calls that would
	// make them are refused.
	Prohibited []string
	// Always: the outputs are required even when nothing changed.
	Always bool
	// Skips quote the conditions under which the text lets a run skip its
	// outputs ("节假日跳过", "skip on public holidays").
	Skips []string
	// Alternatives are required kinds the text offers as a choice ("as a
	// document or project update"): one of each group is enough.
	Alternatives [][]string
	// Explicit: Outputs come from the loop's expectedOutputs setting.
	Explicit bool
	// NoWrites: the text forbids every write ("只回复总结文本即可", "不要…
	// 写入内容", "read-only"); a run's write calls are all refused.
	NoWrites bool
}

// effectiveLoopOutputs are the outputs a run of the loop must produce.
func effectiveLoopOutputs(loop domain.Loop) []string {
	return loopOutputExpectations(loop).Outputs
}

// loopOutputExpectations are the loop's explicit expectedOutputs (["none"]:
// no output required), else what its name and instructions ask for. Explicit
// outputs replace the inference, prohibitions included: a loop set to expect
// a comment may comment whatever its text says.
func loopOutputExpectations(loop domain.Loop) loopOutputExpectation {
	explicit := []string{}
	for _, item := range loop.ExpectedOutputs {
		if item == "none" {
			return loopOutputExpectation{Explicit: true}
		}
		if slices.Contains(loopOutputKinds, item) && !slices.Contains(explicit, item) {
			explicit = append(explicit, item)
		}
	}
	inferred := inferLoopOutputIntentParts(loop.Name, loop.Instructions)
	if len(explicit) > 0 {
		// The text still says when a run may skip and whether the output is
		// needed when nothing changed.
		return loopOutputExpectation{Outputs: explicit, Explicit: true, Always: inferred.Always, Skips: inferred.Skips}
	}
	return inferred
}

// inferLoopOutputs reads the outputs a loop's text asks for.
func inferLoopOutputs(text string) []string {
	return inferLoopOutputIntent(text).Outputs
}

// loopOutputMarker names an output kind in loop text. Nouns name the kind
// ("status update", "文档"); actions are verb phrases asking for it ("comment
// on", "创建事项"). Comments and issues are often named as places ("on any
// issue"), so a write verb near their noun does not ask for them: only an
// action does (writeByNoun false).
type loopOutputMarker struct {
	kind        string
	nouns       *regexp.Regexp
	actions     *regexp.Regexp
	writeByNoun bool
}

var loopOutputMarkers = []loopOutputMarker{
	{"statusUpdate", regexp.MustCompile(`status updates?|project updates?|initiative updates?|weekly updates?|周更新|项目更新|状态更新|进展更新`), regexp.MustCompile(`\bpost (an |the )?updates?\b|发布更新`), true},
	{"issue", nil, regexp.MustCompile(`\b(create|creates|creating|file|files|filing|raise|raises|log|logs)\s+(an?\s+|one\s+|the\s+)?(new\s+)?([\w-]+\s+)?issues?\b|\bopen(s|ing)?\s+(an?|one|new)\s+(new\s+)?([\w-]+\s+)?issues?\b|(创建|新建|新增|提交|开)\s*(一个|一条|一些|新的|新)?\s*(issue|事项|问题|任务|工单)`), false},
	{"comment", regexp.MustCompile(`\bcomment(s|ing|ed)?\b|评论|留言`), regexp.MustCompile(`\bcomment(s|ing)? on\b|\b(leave|leaves|leaving|add|adds|adding|post|posts|posting|write|writes|writing|send|sends|reply with|drop)\s+(a |an |one |the |short |brief |\w+ ){0,2}comments?\b|\breply to\b|(上|中|里|下|并|进行)\s*(评论|留言)\s*$|(为|给|对|向)[^，,。；;]*?(评论|留言)\s*$|(发布|发表|添加|写|发|留下|留|回复)\s*(一条|一个|条|个)?\s*(评论|留言)`), false},
	{"document", regexp.MustCompile(`\bdocs?\b|\bdocuments?\b|文档`), regexp.MustCompile(`\b(create|creates|creating|write|writes|writing|draft|drafts|drafting|start)\s+(a |an |one )(new )?([\w'-]+ )?(doc|document)\b|(创建|撰写|新建|起草)\s*(一篇|一份|一个)?\s*(新的)?[^，,。；;]{0,8}文档`), true},
}

// loopReportNouns name a report by its period ("周报", "日报", "weekly digest"). A report is not an output kind of
// its own: written or appended to a document it is a document ("把周报追加到团队文档"), posted as a project or
// initiative update it is a status update ("将周报发布为项目更新"); otherwise it is the run's reply ("每周五写周报").
var loopReportNouns = regexp.MustCompile(`周报|日报|月报|周总结|月总结|日总结|\b(?:weekly|daily|monthly)\s+(?:reports?|digests?|summar(?:y|ies))\b|\bdigests?\b`)

var (
	// Operation verbs, matched with the output nouns taken out of the clause
	// (so the "update" in "status update" is not a verb).
	loopAppendVerbs = regexp.MustCompile(`\bappend(s|ed|ing)?\b|\badd(s|ed|ing)?\b[^,.;]*\bto the (end|bottom)\b|追加|附加|续写|添加到[^，,。；;]*(末尾|最后|后面|底部)|在[^，,。；;]*(末尾|最后|后面|底部)\s*(添加|加上|补充|写入|写上|加入|写)`)
	loopUpdateVerbs = regexp.MustCompile(`\b(update|updates|updating|edit|edits|editing|revise|revises|revising|modify|modifies|modifying|amend|amends|amending|overwrite|overwrites|overwriting|rewrite|rewrites|rewriting|refresh|refreshes)\b|\bchange (it|its|the|this|that)\b|更新|修改|编辑|修订|改写|覆盖|替换|补充|完善`)
	loopCreateVerbs = regexp.MustCompile(`\b(create|creates|creating|draft|drafts|drafting)\b|\b(a|one) new\b|\bstart a\b|新建|创建|新增|起草|写一(篇|份|个)|生成一(篇|份|个)`)
	// loopWriteVerbs ask for something to be made or published.
	loopWriteVerbs = regexp.MustCompile(`\b(post|posts|posting|publish|publishes|publishing|create|creates|creating|write|writes|writing|add|adds|adding|leave|leaves|leaving|submit|submits|send|sends|share|shares|save|saves|record|records|put|puts|file an|open an)\b|发布|发表|发送|发一|创建|新建|撰写|编写|写|提交|添加|留下|回复|记录|保存`)
	// loopIntoTarget: a generic write into a document ("写入团队文档",
	// "write it to the doc") changes an existing one.
	loopIntoTarget = regexp.MustCompile(`\b(into|to|in|onto)\b|写入|写进|到|进|入|已有|现有|现存|既有|原有|\b(existing|current|same|shared)\b`)
	// loopReadVerbs ask for something to be read, checked or summarised.
	loopReadVerbs = regexp.MustCompile(`\b(check|checks|checking|whether|review|reviews|reviewing|read|reads|reading|look|looks|find|finds|see|summarize|summarise|summarizes|summarises|summary|summaries|list|lists|count|counts|monitor|monitors|track|tracks|watch|watches|collect|collects|gather|gathers|refer|based on|search|searches|searching|use|uses|using|consult|consults|look up|relied on|rely on)\b|搜索|查找|使用|检查|查看|看看|是否|有没有|有无|汇总|总结|统计|读取|阅读|列出|查询|监控|关注|回顾|获取|整理|参考|根据|基于`)
	// loopChangeVerbs are writes that make no output kind ("set the
	// priority", "分配负责人"): such a loop is not read-only.
	loopChangeVerbs = regexp.MustCompile(`\b(set|sets|assign|assigns|move|moves|label|labels|close|closes|triage|triages|archive|archives|merge|merges|tag|tags|prioriti[sz]e|prioriti[sz]es|mark|marks)\b|设置|分配|指派|移动|关闭|归档|分类|打标签|标记|分诊|合并`)
	// loopNegationCue refuses what follows it. Bare 别 is checked apart
	// (see loopNegationIndex) so 特别 and 分别 do not count.
	loopNegationCue = regexp.MustCompile(`\b(do not|don't|dont|never|must not|mustn't|should not|shouldn't|no need to|without|avoid|refrain from|not to|cannot|can't|may not|nor)\b|不要|不用|无需|无须|不需要|不必|请勿|切勿|勿|禁止|严禁|不得|不能|不可|不准|不许|避免|不(发布|发表|发送|发|创建|新建|撰写|编写|写|提交|添加|修改|更改|留言|评论|回复|追加|更新)`)
	// loopBlanket forbids every write ("read-only", "make no changes").
	loopBlanket = regexp.MustCompile(`\bread[- ]only\b|\bmake no (changes|edits|writes)\b|只读|仅读取|只读取|不(做|作)任何(修改|更改|改动|变更)|不写入任何`)
	// loopBlanketObject is a refusal's catch-all object ("do not change
	// anything", "不要做任何修改").
	loopBlanketObject = regexp.MustCompile(`\b(anything|any changes|any edits|any writes|any other changes)\b|任何`)
	// loopExclusive limits the writes to the clause's kind ("only update the
	// doc", "只追加到团队文档"); "only if" is a condition, not a limit.
	loopExclusive = regexp.MustCompile(`(^|[\s,，])(only|just|solely)\s+(append|update|edit|write|add|post|create|comment|modify|change)\b|只(需要?|要|能|可以)?\s*(追加|更新|修改|写入|写|发布|创建|评论|留言|编辑)|仅(仅|需|限)?\s*(追加|更新|修改|写入|写|发布|创建|评论|留言|编辑)`)
	// loopException carves objects out of a refusal ("除追加团队文档外",
	// "except the team doc").
	loopExceptionZH = regexp.MustCompile(`除了?([^，,。；;]*?)(之外|以外|外)`)
	loopExceptionEN = regexp.MustCompile(`\b(except|other than|apart from|aside from|besides)\b(.*)$`)
	// loopLocative strips the places a refused write names ("在任何事项、项目
	// 上", "on any issue, project …") so they are not read as its objects.
	loopLocativeZH = regexp.MustCompile(`在[^，,。；;]*?(之上|之中|上|下|中|里|内)`)
	loopLocativeEN = regexp.MustCompile(`\b(on|in|under|to|into|onto|for|from|about|across|within|at)\b.*$`)
	// loopCondition makes a clause conditional ("如果没有变动", "if nothing
	// changed"); loopConditionLead is a clause that only states one.
	loopCondition     = regexp.MustCompile(`\b(if|when|whenever|unless|in case|otherwise)\b|如果|若|假如|要是|如遇|一旦|倘若|否则|的话|的时候|(没有|无|未)[^，,。；;]{0,10}时`)
	loopConditionLead = regexp.MustCompile(`^\s*(\b(if|when|whenever|unless|in case|otherwise)\b|如果|若|假如|要是|如遇|一旦|倘若|否则|当)`)
	// loopAlways keeps the output required when nothing changed.
	loopAlways = regexp.MustCompile(`(即使|即便|就算|哪怕|无论|不管|不论)[^。；;!?！？]*(没有|无|没|变动|变化|更新|进展|改动|新内容)[^。；;!?！？]*(也|都|仍|照常|照样)|(没有|无)[^。；;!?！？]*(也|仍然|仍|照常|照样)\s*(要|需要|需|得|必须|写|发|追加|更新|记录)|\beven (if|when|though)\b[^.;!?]*\b(nothing|no|none|unchanged|quiet)\b|\bregardless of (whether|changes|activity)\b|\bwhether or not (anything|something) changed\b|照常(写|发布|追加|更新|记录)`)
	// A run skip: a skip verb with a day condition, or a skip of the run or
	// report itself. Skipping items ("skip issues that …") is not one.
	loopSkipVerb = regexp.MustCompile(`跳过|略过|暂停|停止(执行|运行)|不(用|必|需要?)?\s*(执行|运行|写|发布|发|生成|更新|追加)|无需(执行|运行|写|发布|生成|更新|追加)|\bskip(s|ped|ping)?\b|\bdo(es)? not run\b|\bdon'?t run\b|\bpause\b|\bdo nothing\b`)
	loopSkipWhen = regexp.MustCompile(`节假日|假日|假期|法定假|公众假期|周末|休息日|非工作日|调休|放假|\bholidays?\b|\bweekends?\b|\bdays? off\b|\bnon-working\b|\bvacation\b`)
	loopSkipRun  = regexp.MustCompile(`(跳过|略过)\s*(本次|今天|当天|当日|运行|执行|日报|周报|报告|本轮)|\bskip (the |this |today's )?(run|report|day|entry)\b|\bskip it\b`)
	// loopAlternative offers a choice between outputs ("a document or a
	// project update").
	loopAlternative = regexp.MustCompile(`\bor\b|或者|或|还是`)
	loopPieceCut    = regexp.MustCompile(`[，,：:—]`)
	loopConjCut     = regexp.MustCompile(`\b(and then|and|then)\b|并且|然后|接着|随后|之后|并`)
	loopPronoun     = regexp.MustCompile(`\b(it|them|there|this one|that one)\b|其中|里面|进去|上面|该`)
	loopConjVerbs   = []string{"在", "将", "把", "为", "给", "对", "向", "追加", "更新", "发布", "创建", "写", "评论", "留言", "回复", "添加", "记录", "汇总", "检查", "读取", "阅读", "查看", "统计", "总结", "附加", "新建", "修改", "编辑"}

	loopSentenceBreak = regexp.MustCompile(`[。；;！!？?\n]|\.\s|\.$`)

	// loopReportHeading is a report section ("2. 今日新增事项：简要列出…",
	// "New issues today: list them…"): the heading names what the report
	// covers, and what follows the colon says to read, list or summarise it,
	// so the heading asks for nothing to be made.
	loopReportHeading = regexp.MustCompile(`^\s*(?:(?:\d+|[一二三四五六七八九十]+)\s*[.、)）]\s*|[-*•·]\s*|[（(]\s*\d+\s*[)）]\s*)?([^：:，,。；;]{1,32}[：:])\s*(?:请|简要|简单|简短|大致|逐条|分别|按条)*\s*(?:列出|列举|罗列|汇总|总结|统计|说明|描述|概述|简述|介绍|整理|梳理|盘点|概括|\b(?:list|lists|summari[sz]e|describe|count|outline|mention|include|show|give|report|name)\b)`)
	// loopReportHeadingTarget: a heading that names a destination or refuses
	// something is an instruction, not a section name.
	loopReportHeadingTarget = regexp.MustCompile(`将|把|到|\b(to|into|onto)\b`)
	// loopIssueNounLead: "今日新增事项", "列出新建事项", "新增事项和新建事项" —
	// a create verb after a time or scope word, a read verb or a conjunction
	// qualifies the issues being talked about (the issues created today),
	// it does not ask to create one. "新建一个事项" (with a quantifier) still does.
	loopIssueNounLead   = regexp.MustCompile(`(今日|今天|本日|当天|当日|昨日|昨天|本周|上周|这周|本月|上月|近期|最近|近日|期间|所有|全部|哪些|多少|的|和|及|与|、|列出|列举|罗列|汇总|总结|统计|查看|检查|整理|梳理|回顾|读取|阅读|获取|查询|关注|监控|盘点|概括)\s*$`)
	loopIssueQuantifier = regexp.MustCompile(`一个|一条|一些`)
	// loopVerbNouns are nouns built on a write verb ("创建者", "提交时间"):
	// they name a property, not a write.
	loopVerbNouns = regexp.MustCompile(`(创建|新建|提交|发布|添加|回复|更新|修改|编辑)(者|人|时间|日期)`)
	// loopReplyOnly limits the run to its text reply ("只回复总结文本即可",
	// "reply only", "just reply with the summary"): it writes nothing.
	loopReplyOnly = regexp.MustCompile(`(只|仅)(需|要|需要)?\s*(回复|回答|输出|返回|给出)[^，,。；;]{0,10}?(文本|文字|即可|就行|就可以|就好)|\b(reply|respond|answer)\s+only\b|\b(only|just)\s+(reply|respond|answer)(\s+with\s+(the|a|your)?\s*(summary|report|text|answer|digest|results?)\b|\s+in\s+(the\s+)?(chat|run)\b|\s*$|\s*[,;.])|\btext\s+(reply|answer|response)\s+only\b`)
	// loopReplyOnlyComment: "只回复评论即可" replies to comments; it is a write.
	loopReplyOnlyComment = regexp.MustCompile(`评论|留言|事项|\b(comments?|threads?|issues?)\b`)
	// loopReplyTarget: "reply only to comments that …" names whom to reply
	// to; it is a comment, not a reply-only run.
	loopReplyTarget = regexp.MustCompile(`^\s*(to|on|in the thread|under)\b`)
	// loopGenericWrite is a refusal of writing anything ("写入内容",
	// "write anything"), whatever resources it names as places.
	loopGenericWrite = regexp.MustCompile(`(写入|写|添加|修改|更改|改动|变更|创建|新建|发布|保存)\s*(任何)?\s*(内容|东西|数据|信息)|\b(write|add|change|modify|create|edit|post|save)\s+(anything|any content|content|any data|any changes|any output)\b`)
)

// loopIssueAction finds the first match of an issue or comment action that
// asks for one: an issue create verb that qualifies the issues talked about
// ("今日新增事项") is not one (see loopIssueNounLead).
func loopIssueAction(marker loopOutputMarker, clause string) []int {
	for _, match := range marker.actions.FindAllStringIndex(clause, -1) {
		if !loopIssueNounMatch(marker, clause, match) {
			return match
		}
	}
	return nil
}

// loopIssueNoun finds an issue named as a noun phrase ("今日新增事项"), or nil.
func loopIssueNoun(marker loopOutputMarker, clause string) []int {
	if marker.kind != "issue" {
		return nil
	}
	for _, match := range marker.actions.FindAllStringIndex(clause, -1) {
		if loopIssueNounMatch(marker, clause, match) {
			return match
		}
	}
	return nil
}

// loopIssueNounMatch reports whether an issue action match only qualifies
// the issues talked about.
func loopIssueNounMatch(marker loopOutputMarker, clause string, match []int) bool {
	text := clause[match[0]:match[1]]
	return marker.kind == "issue" && text != "" && text[0] >= utf8.RuneSelf && !loopIssueQuantifier.MatchString(text) && loopIssueNounLead.MatchString(clause[:match[0]])
}

// loopStripReportHeading drops a report section's heading from a sentence.
func loopStripReportHeading(sentence string) string {
	match := loopReportHeading.FindStringSubmatchIndex(sentence)
	if match == nil {
		return sentence
	}
	heading := sentence[match[2]:match[3]]
	if loopNegationIndex(heading) >= 0 || loopReportHeadingTarget.MatchString(heading) {
		return sentence
	}
	return sentence[match[3]:]
}

// loopNegationIndex is where the clause's first refusal starts, or -1.
func loopNegationIndex(clause string) int {
	index := -1
	if match := loopNegationCue.FindStringIndex(clause); match != nil {
		index = match[0]
	}
	for offset := 0; ; {
		position := strings.Index(clause[offset:], "别")
		if position < 0 {
			break
		}
		position += offset
		previous, _ := utf8.DecodeLastRuneInString(clause[:position])
		if !strings.ContainsRune("特区个分识级类告辨性差告派", previous) && (index < 0 || position < index) {
			index = position
			break
		}
		offset = position + len("别")
	}
	return index
}

// loopHasVerb reports whether a piece of a clause says to do anything; a
// piece that does not continues the previous one (an enumeration: "on any
// issue, project, or other resource").
func loopHasVerb(piece string) bool {
	if loopNegationIndex(piece) >= 0 || loopAppendVerbs.MatchString(piece) || loopUpdateVerbs.MatchString(piece) || loopCreateVerbs.MatchString(piece) || loopWriteVerbs.MatchString(piece) || loopReadVerbs.MatchString(piece) || loopChangeVerbs.MatchString(piece) || loopSkipVerb.MatchString(piece) {
		return true
	}
	for _, marker := range loopOutputMarkers {
		if marker.actions.MatchString(piece) {
			return true
		}
	}
	return false
}

// loopIntentClauses splits a sentence into clauses that each say one thing:
// at commas and colons (not at 、, which enumerates), keeping verbless pieces
// with the clause before them, then at conjunctions that start a new verb
// ("read the doc and comment on the issue", "汇总今日工作并追加到文档").
func loopIntentClauses(sentence string) []string {
	merged := []string{}
	for _, piece := range loopPieceCut.Split(sentence, -1) {
		if strings.TrimSpace(piece) == "" {
			continue
		}
		if len(merged) > 0 && !loopHasVerb(piece) && !loopConditionLead.MatchString(piece) {
			merged[len(merged)-1] += "、" + piece
			continue
		}
		merged = append(merged, piece)
	}
	clauses := []string{}
	for _, piece := range merged {
		start := 0
		for _, match := range loopConjCut.FindAllStringIndex(piece, -1) {
			if piece[match[0]:match[1]] == "并" && !slices.ContainsFunc(loopConjVerbs, func(verb string) bool { return strings.HasPrefix(strings.TrimSpace(piece[match[1]:]), verb) }) {
				continue
			}
			head, rest := piece[start:match[0]], piece[match[1]:]
			if strings.TrimSpace(head) == "" || !loopHasVerb(rest) || !loopHasVerb(head) {
				continue
			}
			clauses = append(clauses, head)
			start = match[1]
		}
		clauses = append(clauses, piece[start:])
	}
	return clauses
}

// loopStripNouns removes the output nouns from a clause, so verbs are read
// without the nouns that contain them ("status update", "项目更新").
func loopStripNouns(clause string) string {
	for _, marker := range loopOutputMarkers {
		if marker.nouns != nil {
			clause = marker.nouns.ReplaceAllString(clause, " ")
		}
	}
	return clause
}

// loopKindMention finds a kind in a clause: where it is named (-1 when not)
// and whether an action asks for it.
func loopKindMention(marker loopOutputMarker, clause string) (int, bool) {
	position, action := -1, false
	if match := loopIssueAction(marker, clause); match != nil {
		position, action = match[0], true
	} else if match := loopIssueNoun(marker, clause); match != nil {
		position = match[0]
	}
	if marker.nouns != nil {
		if match := marker.nouns.FindStringIndex(clause); match != nil && (position < 0 || match[0] < position) {
			position = match[0]
		}
	}
	return position, action
}

// loopClauseOp is the operation a clause asks for on a kind it names, or ""
// when it has no verb at all.
func loopClauseOp(marker loopOutputMarker, clause string, action bool) string {
	verbs := loopStripNouns(clause)
	if !marker.writeByNoun {
		switch {
		case action && marker.kind == "issue":
			return loopOpCreate
		case action:
			return loopOpWrite
		case loopHasVerb(verbs):
			return loopOpRead
		}
		return ""
	}
	switch {
	case loopAppendVerbs.MatchString(verbs):
		return loopOpAppend
	case loopUpdateVerbs.MatchString(verbs):
		return loopOpUpdate
	case loopCreateVerbs.MatchString(verbs), action && marker.kind == "document":
		return loopOpCreate
	case loopWriteVerbs.MatchString(verbs), action:
		if marker.kind == "document" && loopIntoTarget.MatchString(verbs) {
			return loopOpUpdate
		}
		return loopOpWrite
	case loopReadVerbs.MatchString(verbs):
		return loopOpRead
	}
	return ""
}

// loopIntentState collects what a loop's clauses say about each kind.
type loopIntentState struct {
	ops        map[string]string
	read       map[string]bool
	prohibited map[string]bool
	optional   map[string]bool
	blanket    bool
	// excepted: a refusal carves out what the loop does write ("除追加团队
	// 文档外", "除此之外").
	excepted  bool
	exclusive bool
	changes   bool
	always    bool
	skips     []string
	// reads and writes: the instructions use any read or write verb.
	reads, writes bool
	// reply: a report named by its period is written without a document or update to put it in ("写周报"):
	// the run's reply is the report.
	reply bool
	// alternatives are kinds a clause offers as a choice.
	alternatives [][]string
}

func (state *loopIntentState) want(kind, op string) {
	if op == loopOpRead {
		state.read[kind] = true
		return
	}
	if current, ok := state.ops[kind]; !ok || loopOpRank[op] > loopOpRank[current] {
		state.ops[kind] = op
	}
}

func (state *loopIntentState) addSkip(sentence string) {
	sentence = strings.TrimSpace(sentence)
	if sentence != "" && !slices.Contains(state.skips, sentence) {
		state.skips = append(state.skips, sentence)
	}
}

// inferLoopOutputIntent reads side effects off a loop's text.
func inferLoopOutputIntent(text string) loopOutputExpectation {
	return inferLoopOutputIntentParts("", text)
}

// inferLoopOutputIntentParts reads a loop's name and instructions. The name
// only names outputs (a title such as "Weekly update"); its verbs ("Update
// digest") do not make the loop change anything.
func inferLoopOutputIntentParts(name, text string) loopOutputExpectation {
	state := &loopIntentState{ops: map[string]string{}, read: map[string]bool{}, prohibited: map[string]bool{}, optional: map[string]bool{}}
	for part, body := range []string{name, text} {
		body = loopVerbNouns.ReplaceAllString(strings.ToLower(body), "$2")
		if loopBlanket.MatchString(body) {
			state.blanket = true
		}
		if match := loopReplyOnly.FindStringIndex(body); match != nil && part == 1 && !loopReplyOnlyComment.MatchString(body[match[0]:match[1]]) && !loopReplyTarget.MatchString(body[match[1]:]) {
			// "只回复总结文本即可": the reply is the run's only output.
			state.blanket = true
		}
		if part == 1 {
			state.reads = loopReadVerbs.MatchString(body)
			state.writes = loopWriteVerbs.MatchString(body)
		}
		for _, sentence := range loopSentenceBreak.Split(body, -1) {
			if strings.TrimSpace(sentence) == "" {
				continue
			}
			state.sentence(sentence, part == 0)
		}
	}
	return state.expectation()
}

// sentence reads one sentence's clauses.
func (state *loopIntentState) sentence(sentence string, title bool) {
	sentence = loopStripReportHeading(sentence)
	if loopAlways.MatchString(sentence) {
		state.always = true
	}
	last := ""
	previous := ""
	for _, clause := range loopIntentClauses(sentence) {
		conditional := loopCondition.MatchString(clause) || loopConditionLead.MatchString(previous)
		previous = clause
		if loopSkipVerb.MatchString(clause) && (loopSkipWhen.MatchString(clause) || loopSkipRun.MatchString(clause)) {
			state.addSkip(sentence)
			continue
		}
		// Objects excepted from a refusal are what the loop does write.
		exception := ""
		if match := loopExceptionZH.FindStringSubmatchIndex(clause); match != nil {
			exception = clause[match[2]:match[3]]
			clause = clause[:match[0]] + clause[match[1]:]
		} else if match := loopExceptionEN.FindStringSubmatchIndex(clause); match != nil && loopNegationIndex(clause[:match[0]]) >= 0 {
			exception = clause[match[4]:match[5]]
			clause = clause[:match[0]]
		}
		if exception != "" {
			state.excepted = true
			for _, marker := range loopOutputMarkers {
				if position, action := loopKindMention(marker, exception); position >= 0 {
					op := loopClauseOp(marker, exception, action)
					if op == "" || op == loopOpRead {
						op = firstNonEmpty(loopClauseOp(marker, loopStripNouns(clause)+" "+exception, true), loopOpWrite)
					}
					state.want(marker.kind, op)
					last = marker.kind
				}
			}
		}
		cue := loopNegationIndex(clause)
		positive, negated := clause, ""
		if cue >= 0 {
			positive, negated = clause[:cue], clause[cue:]
		}
		named := false
		// A comment or issue asked for by its action is the clause's object;
		// a document or update named after it is what the comment refers to
		// ("post a comment with links to the documents").
		actionAt := -1
		for _, marker := range loopOutputMarkers {
			if match := loopIssueAction(marker, positive); !marker.writeByNoun && match != nil && (actionAt < 0 || match[0] < actionAt) {
				actionAt = match[0]
			}
		}
		wanted := map[string]int{}
		// What the clause asks for before any refusal.
		for _, marker := range loopOutputMarkers {
			position, action := loopKindMention(marker, positive)
			if position < 0 {
				continue
			}
			named = true
			last = marker.kind
			op := loopClauseOp(marker, positive, action)
			if verbs := loopStripNouns(positive); marker.writeByNoun && !action && actionAt >= 0 && position > actionAt && op != loopOpRead && !loopAppendVerbs.MatchString(verbs) && !loopUpdateVerbs.MatchString(verbs) {
				op = loopOpRead
			}
			if op == "" {
				// A noun with no verb: the sentence decides; a short line with
				// no verb at all is a title and names the output.
				switch rest := loopStripNouns(sentence); {
				case !marker.writeByNoun:
					op = loopOpRead
				case loopNegationIndex(rest) < 0 && loopWriteVerbs.MatchString(rest):
					op = loopOpWrite
				case loopReadVerbs.MatchString(rest):
					op = loopOpRead
				case utf8.RuneCountInString(strings.TrimSpace(sentence)) <= 24:
					op = loopOpWrite
				default:
					op = loopOpRead
				}
			}
			state.want(marker.kind, op)
			if op != loopOpRead {
				wanted[marker.kind] = position
			}
			if op != loopOpRead && loopExclusive.MatchString(positive) {
				state.exclusive = true
			}
		}
		if len(wanted) > 1 && loopAlternative.MatchString(positive) {
			// "Post the report as a document or project update": either one.
			group := []string{}
			for _, kind := range loopOutputKinds {
				if _, ok := wanted[kind]; ok {
					group = append(group, kind)
				}
			}
			state.alternatives = append(state.alternatives, group)
		}
		if !named && last != "" {
			// "Read the team doc, then append to it": a change with no object
			// of its own continues the kind named before it.
			marker := loopOutputMarkers[slices.IndexFunc(loopOutputMarkers, func(item loopOutputMarker) bool { return item.kind == last })]
			verbs := loopStripNouns(positive)
			inherit := loopAppendVerbs.MatchString(verbs) || loopUpdateVerbs.MatchString(verbs) || (loopWriteVerbs.MatchString(verbs) || loopCreateVerbs.MatchString(verbs)) && loopPronoun.MatchString(verbs)
			if inherit && strings.TrimSpace(verbs) != "" {
				op := loopClauseOp(loopOutputMarker{kind: marker.kind, actions: marker.actions, writeByNoun: true}, positive, false)
				if op != "" && op != loopOpRead {
					named = true
					state.want(marker.kind, op)
					if loopExclusive.MatchString(positive) {
						state.exclusive = true
					}
				}
			}
		}
		if !named && loopReportNouns.MatchString(positive) && !loopChangeVerbs.MatchString(loopReportNouns.ReplaceAllString(positive, " ")) {
			// "每周五写周报", "Send the weekly digest": a report with no document or update to put it in is the
			// run's reply, not a change.
			named, state.reply = true, true
		}
		if !named && !title && !conditional && cue < 0 && (loopWriteVerbs.MatchString(positive) || loopUpdateVerbs.MatchString(positive) || loopChangeVerbs.MatchString(positive)) {
			state.changes = true
		}
		if negated == "" {
			continue
		}
		// What the refusal covers: the kinds it names, without the places it
		// names them on.
		objects := loopLocativeEN.ReplaceAllString(loopLocativeZH.ReplaceAllString(negated, " "), " ")
		refused := false
		for _, marker := range loopOutputMarkers {
			_, action := loopKindMention(marker, negated)
			position, _ := loopKindMention(marker, objects)
			if !action && position < 0 {
				continue
			}
			refused = true
			last = marker.kind
			if conditional {
				state.optional[marker.kind] = true
			} else {
				state.prohibited[marker.kind] = true
			}
		}
		switch {
		case refused:
			if conditional {
				state.addSkip(sentence)
			} else if loopGenericWrite.MatchString(objects) {
				// "不要在任何事项、文档上发布评论或写入内容": writing anything
				// is refused too, not only the kinds it names.
				state.blanket = true
			}
		case loopBlanketObject.MatchString(negated) && !conditional:
			state.blanket = true
		case conditional && (loopWriteVerbs.MatchString(negated) || loopUpdateVerbs.MatchString(negated) || loopAppendVerbs.MatchString(negated)):
			// "If nothing changed, don't post": the run may skip.
			state.addSkip(sentence)
		}
	}
}

func (state *loopIntentState) expectation() loopOutputExpectation {
	result := loopOutputExpectation{Operations: map[string]string{}, Always: state.always, Skips: state.skips, Alternatives: state.alternatives}
	if state.blanket && !state.exclusive && !state.excepted {
		// A refusal of every write ("只回复…即可", "不要…写入内容", "read
		// only") wins over outputs read off other clauses; only a refusal
		// that names its exceptions, or the loop's expectedOutputs, keeps them.
		state.ops, state.alternatives = map[string]string{}, nil
		result.Alternatives, result.NoWrites = nil, true
	}
	for _, kind := range loopOutputKinds {
		if op, ok := state.ops[kind]; ok {
			result.Outputs = append(result.Outputs, kind)
			result.Operations[kind] = op
		}
	}
	for _, kind := range loopOutputKinds {
		if kind == "change" || state.ops[kind] != "" {
			continue
		}
		if state.prohibited[kind] || state.blanket || state.exclusive {
			result.Prohibited = append(result.Prohibited, kind)
		}
	}
	optional := false
	for kind := range state.optional {
		optional = optional || state.ops[kind] == "" && !slices.Contains(result.Prohibited, kind)
	}
	if len(result.Outputs) == 0 && !optional && !state.changes {
		// Outputs named only to read, refused, or no output named and only
		// reading verbs ("count open issues per team and summarise"): the
		// report is the output.
		result.ReadOnly = len(state.read) > 0 || len(result.Prohibited) > 0 || state.blanket || state.reads && !state.writes || state.reply
	}
	// Every write refused: the reply is the output, whatever the text's
	// other verbs ("回复", "写") say.
	result.ReadOnly = result.ReadOnly || result.NoWrites
	if len(result.Outputs) == 0 && !result.ReadOnly {
		result.Operations = nil
	}
	return result
}
