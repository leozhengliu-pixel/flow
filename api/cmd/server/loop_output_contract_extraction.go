package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

// Output contract extraction: the configured model reads a loop's output
// contract from its name and instructions with one structured call (a
// forced tool call whose parameters are the contract schema, temperature 0).
// The result is validated (normalizeLoopContract); an invalid one is sent
// back once with its problems. It is cached on the loop and its matching
// versions by instructions hash, so runs do not read it again; when no model
// is configured or both attempts fail, the loop gets the conservative
// report-only fallback contract until a person sets the outputs.

// loopContractPromptVersion is part of the instructions hash: changing the
// extraction prompt reads every loop's contract again.
const loopContractPromptVersion = "contract-v2"

const loopContractToolName = "record_output_contract"

// loopContractSchema is the contract the model fills in (JSON Schema).
const loopContractSchema = `{"type":"object","additionalProperties":false,
"required":["analysis","readOnly","readOnlyEvidence","outputs","always","alwaysEvidence","skipConditions"],
"properties":{
"analysis":{"type":"string","description":"Before the fields: one short line per sentence or clause of the instructions that writes, reads, forbids, conditions or skips something."},
"readOnly":{"type":"boolean","description":"true only when the instructions ask for no change at all: the run's reply (its report) is its only output."},
"readOnlyEvidence":{"type":"string","description":"Exact quote that makes the loop read-only, else empty."},
"outputs":{"type":"array","items":{"type":"object","additionalProperties":false,
 "required":["kind","operation","required","target","cardinality","conditions","group","evidence","reasoning"],
 "properties":{
  "kind":{"type":"string","enum":["document","issue","comment","statusUpdate","change"]},
  "operation":{"type":"string","enum":["create","update","append","read","forbidden"]},
  "required":{"type":"boolean","description":"true when every run must produce it (create, update or append only)."},
  "target":{"type":"object","additionalProperties":false,"required":["mode","titleTemplate","placeholders","title","ambiguous","team","project","initiative","match","description"],
   "properties":{
    "mode":{"type":"string","enum":["new","existing","triggering","matching","any"],"description":"new: made each run; existing: a named document, project or initiative; triggering: the item that triggered the run; matching: issues in a set the instructions describe (match); any: anywhere."},
    "titleTemplate":{"type":"string","description":"A new document's or issue's title: {date}, {week}, {month}, {year} for the run's period, {team} and {project} for their names, and {name} for any other part the run fills (declare it in placeholders); else empty."},
    "placeholders":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["name","source"],"properties":{"name":{"type":"string","description":"The placeholder without braces, e.g. topic."},"source":{"type":"string","description":"What the run fills it from, in a few words."}}},"description":"Every {name} in titleTemplate other than date, week, month, year, team and project; else empty."},
    "title":{"type":"string","description":"An existing document's title as the instructions give it; else empty."},
    "ambiguous":{"type":"boolean","description":"true when an existing document is referred to only by description (the team doc, 团队文档), without its title."},
    "team":{"type":"string","description":"A specific team the instructions name for the output; empty when they name none or only 'the team'."},
    "project":{"type":"string","description":"A specific project the instructions name (the comment or status update is on it, the new issue goes in it); else empty."},
    "initiative":{"type":"string","description":"A specific initiative the instructions name for a status update or comment; else empty."},
    "match":{"type":"string","description":"For mode matching: the set of issues, as the instructions describe it; else empty."},
    "description":{"type":"string","description":"The target in a few words."}}},
  "cardinality":{"type":"string","enum":["onePerRun","perItem","any"]},
  "conditions":{"type":"array","items":{"type":"string"},"description":"Conditions quoted from the instructions under which it is made."},
  "group":{"type":"string","description":"Same non-empty value on outputs the instructions offer as alternatives (one is enough); else empty."},
  "evidence":{"type":"string","description":"Exact quote from the instructions."},
  "reasoning":{"type":"string","description":"One short sentence."}}}},
"always":{"type":"boolean","description":"true when the instructions require the outputs even if nothing changed."},
"alwaysEvidence":{"type":"string"},
"skipConditions":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["condition","evidence"],
 "properties":{"condition":{"type":"string"},"evidence":{"type":"string"}}},"description":"Conditions under which the whole run is skipped (holidays, weekends)."}}}`

var loopContractTool = agentProviderTool{
	Name:        loopContractToolName,
	Description: "Record the output contract of the automation loop: what its runs create, change, only read or must not touch. Call it exactly once with the complete contract.",
	Parameters:  json.RawMessage(loopContractSchema),
	Access:      "read",
}

// loopContractSystemPrompt is the extraction prompt. Its examples cover the
// instructions earlier versions misread, in Chinese and English.
const loopContractSystemPrompt = `You read the instructions of an automation loop in a work tracker (Flow) and record its output contract: what each run of the loop must create or change, what it only reads, and what it must not touch. The runtime enforces your contract: a run may only make the writes you list, and a run that does not make a required output is flagged. Read the instructions literally and completely; do not invent outputs. Call record_output_contract once.

Output kinds:
- document: a Flow document (文档, Document, doc, page, 页面).
- issue: a Flow issue (事项, issue, ticket, task, 任务, 工单, bug, 问题 when it means an issue). Changing an issue's status, priority, assignee, labels, project or triage is kind issue, operation update.
- comment: a comment on an issue, project or document (评论, 留言, comment, reply in a thread).
- statusUpdate: a project or initiative status update (项目更新, 状态更新, 进展更新, project update, initiative update, weekly update when posted to a project).
- change: any other change in Flow (projects, labels, cycles, reminders, reactions).

Operations:
- create: make a new one (创建, 新建, 新增一个, 起草, 写一篇, 发布, 发表, 提交, 留言, post, publish, create, file, open, add a comment, leave a comment, draft a new doc). Writing content "into" a document the same run creates is still create.
- update: change an existing one (更新, 修改, 编辑, 改写, 覆盖, 写入已有/现有文档, 写入团队文档, update, edit, revise, rewrite, overwrite, write into the existing doc).
- append: add to the end of an existing document (追加, 附加, 添加到末尾, 在…后面加上, append, add to the end/bottom).
- read: the instructions only read, check, count, list or summarize it (查看, 检查, 阅读, 统计, 列出, 汇总, 总结, 整理, review, check, read, list, count, summarize, links to the documents used).
- forbidden: the instructions say not to make or change it (不要, 不用, 无需, 禁止, 请勿, 别, 不得, 不能, 避免, do not, don't, never, must not, without, no need to).

Rules:
1. A report, summary or digest (日报, 周报, 月报, 总结, 汇总, report, digest, summary) is the run's reply, not an output, unless the instructions say to put it into a document, a status update or a comment. "写周报" / "Write the weekly report" with no place to put it is read-only.
2. Nouns that name what is read, a topic or a place are not outputs: "今日新增事项：列出…" (the issues created today), "新建事项的数量", "列出创建者", "on any issue, project or other resource", "with links to the documents you used". A section heading of a report ("2. 今日新增事项：简要列出今天新创建的事项及创建者") asks to list, so the issues are read.
3. A refusal covers every object it names, including objects enumerated with 、 , 和, 或, or, and: "不要在任何事项、项目或其他资源上发布评论" forbids comments only (the issues and projects there are places, not refused kinds); "无需发布评论或创建事项" forbids comments and issues.
4. readOnly is true when the instructions ask for no change at all: they only read and report, or they say to only reply ("只回复…即可", "仅输出总结", "reply only", "just reply with the summary"), or they refuse writing anything ("不要…写入内容", "不做任何修改", "make no changes", "read-only", "write nothing"). A readOnly loop has no create, update or append outputs; list its refused kinds as forbidden.
5. An exception keeps its output: "除追加团队文档外，不要…" means append to the team document is an output and the refused kinds are forbidden. "只追加到团队文档" (only append to it) is an append output.
6. Do not add a forbidden entry for a kind that also has a create, update or append output; "不要复用已有 Document" / "do not reuse an existing document" is expressed by target.mode new on the create output.
7. required is true when every run must make the output. It is false when the output is only made if a condition holds ("如果发现阻塞，创建事项", "comment on bugs that lack repro steps", "for every new error"): put the condition in conditions. Outputs on the triggering issue of an event loop ("Comment on new bugs") are required.
8. target.mode: new for a document or issue the run makes; existing for a document that already exists, or for a project or initiative the instructions name (its name goes in target.project or target.initiative); triggering for the issue, project or initiative that triggered the run ("comment on it", "the new bug", "该事项", "触发的事项"); matching for issues in a set the instructions describe without naming them ("open bugs without repro steps", "issues labeled customer", "本周逾期的事项"), with the description in target.match; any otherwise ("each project", "any issue").
9. Existing documents: give the title in target.title when the instructions name it, as written, without quotes ("Release log"). A document named only by description ("已有团队文档", "团队文档", "the team doc", "our wiki page") has an empty title, ambiguous true and a description: the loop's owner then picks it. ambiguous is false whenever a title is given, and for every output that is not an existing document.
9a. Where comments, status updates and issues go: a comment on the triggering issue → mode triggering; comments on a described set of issues → mode matching with match; a comment or status update on a named project → mode existing with project; on a named initiative → mode existing with initiative; a status update on each project → mode any, cardinality perItem. Issue updates: the triggering issue → triggering; a described set → matching (with team or project when named). New issues: mode new, with team and project when named.
9b. target.titleTemplate: the new document's or issue's title with the run's period as {date} (YYYY-MM-DD), {week} (ISO week), {month} (YYYY-MM) or {year}, and {team} / {project} for those names: "标题格式为'每日总结-YYYY-MM-DD'" gives "每日总结-{date}"; "weekly notes titled Week N" gives "Week {week}". Keep the rest of the title exactly. Any other part the run fills in ("<error message>", "{topic}", "{客户名}", "[summary]") is a placeholder {name} in braces, declared in target.placeholders with what fills it: "titled Error: <message>" gives "Error: {message}" with placeholders [{"name":"message","source":"the error's message"}]. placeholders is empty when the title has no such part; titleTemplate is empty when no title is given.
10. target.team: only a specific team named for the output ("在设计团队下", "in the Mobile team"); "目标团队", "本团队", "the team", "our team" are the loop's own team: leave team empty and say so in description. target.project and target.initiative likewise hold only a specific project or initiative ("on the Mobile App project", "在「增长计划」项目上"); "each project" or "the project" names none.
11. cardinality: onePerRun when each run makes exactly one ("每次运行都要创建一个单独的", "one document per run", a daily document); perItem when one is made for each item ("为每个项目", "for each failing check"); any otherwise.
12. always is true when the outputs are due even if nothing changed ("即使没有变动也要写", "如果没有变动也要写日报", "even if nothing changed"). skipConditions are conditions that skip the whole run ("节假日跳过", "skip on weekends and public holidays", "If nothing changed, don't post"); skipping single items ("skip issues that already have a comment") is not one.
13. group: give outputs the same group when the instructions offer them as alternatives ("as a document or a project update").
14. evidence is an exact, short quote from the instructions (in their language); reasoning is one short English sentence. List each kind the instructions mention once per operation; do not list kinds they never mention.

Examples (instructions → contract, analysis omitted):

1. "在目标团队下创建一个新的 Document，标题格式为'每日总结-YYYY-MM-DD'，将完整的总结内容写入该 Document。每次运行都要创建一个单独的 Document，不要复用已有 Document。"
→ {"readOnly":false,"readOnlyEvidence":"","outputs":[{"kind":"document","operation":"create","required":true,"target":{"mode":"new","titleTemplate":"每日总结-{date}","placeholders":[],"title":"","ambiguous":false,"team":"","project":"","initiative":"","match":"","description":"a new document in the loop's team (目标团队) each run"},"cardinality":"onePerRun","conditions":[],"group":"","evidence":"在目标团队下创建一个新的 Document，标题格式为'每日总结-YYYY-MM-DD'","reasoning":"Each run creates its own new document; reusing an existing one is excluded by mode new."}],"always":false,"alwaysEvidence":"","skipConditions":[]}

2. "请总结团队今天的工作。\n1. 今日完成事项：列出已完成的事项。\n2. 今日新增事项：简要列出今天新创建的事项及创建者。\n将总结结果追加到已有团队文档中，不要在任何事项、项目或其他资源上发布评论。"
→ {"readOnly":false,"readOnlyEvidence":"","outputs":[{"kind":"document","operation":"append","required":true,"target":{"mode":"existing","titleTemplate":"","placeholders":[],"title":"","ambiguous":true,"team":"","project":"","initiative":"","match":"","description":"the team's existing document"},"cardinality":"onePerRun","conditions":[],"group":"","evidence":"将总结结果追加到已有团队文档中","reasoning":"The summary is appended to an existing team document."},{"kind":"issue","operation":"read","required":false,"target":{"mode":"any","titleTemplate":"","placeholders":[],"title":"","ambiguous":false,"team":"","project":"","initiative":"","match":"","description":"issues created or completed today"},"cardinality":"any","conditions":[],"group":"","evidence":"今日新增事项：简要列出今天新创建的事项及创建者","reasoning":"A report section that lists issues; nothing is created."},{"kind":"comment","operation":"forbidden","required":false,"target":{"mode":"any","titleTemplate":"","placeholders":[],"title":"","ambiguous":false,"team":"","project":"","initiative":"","match":"","description":"comments anywhere"},"cardinality":"any","conditions":[],"group":"","evidence":"不要在任何事项、项目或其他资源上发布评论","reasoning":"Comments are refused; the issues and projects are only places."}],"always":false,"alwaysEvidence":"","skipConditions":[]}

3. "请总结团队今天的工作。只回复总结文本即可，不要在任何事项、项目、文档或其他资源上发布评论或写入内容。"
→ {"readOnly":true,"readOnlyEvidence":"只回复总结文本即可，不要在任何事项、项目、文档或其他资源上发布评论或写入内容","outputs":[{"kind":"comment","operation":"forbidden","required":false,"target":{"mode":"any","titleTemplate":"","placeholders":[],"title":"","ambiguous":false,"team":"","project":"","initiative":"","match":"","description":"comments anywhere"},"cardinality":"any","conditions":[],"group":"","evidence":"不要在任何事项、项目、文档或其他资源上发布评论","reasoning":"Comments are refused."},{"kind":"document","operation":"forbidden","required":false,"target":{"mode":"any","titleTemplate":"","placeholders":[],"title":"","ambiguous":false,"team":"","project":"","initiative":"","match":"","description":"any document"},"cardinality":"any","conditions":[],"group":"","evidence":"或写入内容","reasoning":"Writing content anywhere is refused."}],"always":false,"alwaysEvidence":"","skipConditions":[]}

4. "每周五写周报，汇总本周完成的事项。" → {"readOnly":true,"readOnlyEvidence":"每周五写周报，汇总本周完成的事项","outputs":[{"kind":"issue","operation":"read","required":false,"target":{"mode":"any","titleTemplate":"","placeholders":[],"title":"","ambiguous":false,"team":"","project":"","initiative":"","match":"","description":"issues completed this week"},"cardinality":"any","conditions":[],"group":"","evidence":"汇总本周完成的事项","reasoning":"The weekly report is the reply; issues are only summarized."}],"always":false,"alwaysEvidence":"","skipConditions":[]}

5. "每周五把周报发布为项目更新。" → {"readOnly":false,"readOnlyEvidence":"","outputs":[{"kind":"statusUpdate","operation":"create","required":true,"target":{"mode":"any","titleTemplate":"","placeholders":[],"title":"","ambiguous":false,"team":"","project":"","initiative":"","match":"","description":"a project status update"},"cardinality":"any","conditions":[],"group":"","evidence":"把周报发布为项目更新","reasoning":"The report is posted as a project update."}],"always":false,"alwaysEvidence":"","skipConditions":[]}

6. "汇总今日工作，无需在事项、项目上发布评论或创建事项。" → {"readOnly":true,"readOnlyEvidence":"汇总今日工作","outputs":[{"kind":"comment","operation":"forbidden",...,"evidence":"无需在事项、项目上发布评论"},{"kind":"issue","operation":"forbidden",...,"evidence":"或创建事项"}],...}

7. "汇总今日工作并追加到团队文档。如果没有变动也要写日报。节假日跳过。" → {"readOnly":false,"outputs":[{"kind":"document","operation":"append","required":true,"target":{"mode":"existing","title":"","ambiguous":true,"description":"the team document",...},"cardinality":"onePerRun","evidence":"汇总今日工作并追加到团队文档",...}],"always":true,"alwaysEvidence":"如果没有变动也要写日报","skipConditions":[{"condition":"public holidays","evidence":"节假日跳过"}]}

8. "除追加团队文档外，不要发布评论或创建事项。" → outputs: document append required (existing, ambiguous true, "the team document"), comment forbidden, issue forbidden; readOnly false.

9. "Review the error log and open a new issue for every new error. If you find a blocker, assign it to the on-call engineer." → outputs: issue create, required false, target mode new, cardinality perItem, conditions ["for every new error"]; issue update, required false, conditions ["if you find a blocker"]; readOnly false.

10. "Summarize today's work. Don't post comments on any issue, project, or other resource." → readOnly true; outputs: comment forbidden (evidence "Don't post comments on any issue, project, or other resource").

11. "Append today's release notes to the "Release log" document, even if nothing shipped. Skip weekends." → outputs: document append required, target mode existing, title "Release log", cardinality onePerRun; always true ("even if nothing shipped"); skipConditions [{"condition":"weekends","evidence":"Skip weekends"}].

12. "Post the report as a document or project update titled "Feature requests"." → outputs: document create required, target new, titleTemplate "Feature requests", group "report"; statusUpdate create required, group "report".

13. "Triage new issues: set the priority and assign the right person. Do not comment." → outputs: issue update required, target triggering; comment forbidden; readOnly false.

14. "Comment on new bugs with a checklist of missing details." (an event loop) → outputs: comment create required, target triggering, cardinality onePerRun.

15. "Read the team doc and update it with this week's decisions; create a new doc only if a decision needs its own page." → outputs: document update required (existing, ambiguous true, description "the team doc"); document create required false, conditions ["only if a decision needs its own page"], target new.

16. "Post a weekly update on each project. If nothing changed, don't post." → outputs: statusUpdate create required, cardinality perItem; skipConditions [{"condition":"nothing changed","evidence":"If nothing changed, don't post"}].

17. "Every Friday post a status update on the Mobile App project." → outputs: statusUpdate create required, target mode existing, project "Mobile App", cardinality onePerRun.

18. "每周五在「增长计划」举措上发布进展更新。" → outputs: statusUpdate create required, target mode existing, initiative "增长计划".

19. "When a bug is reported, comment on it asking for repro steps." (an event loop) → outputs: comment create required, target mode triggering, cardinality onePerRun.

20. "Comment on every open bug that has no repro steps, asking for them." → outputs: comment create, required false, target mode matching, match "open bugs without repro steps", cardinality perItem, conditions ["open bugs without repro steps"].

21. "Set the priority of issues labeled customer in the Support team." → outputs: issue update required, target mode matching, match "issues labeled customer", team "Support".

22. "For each new error, open an issue in the Platform team's Reliability project titled "Error: <error message>"." → outputs: issue create, required false, target mode new, team "Platform", project "Reliability", titleTemplate "Error: {message}", placeholders [{"name":"message","source":"the error's message"}], cardinality perItem, conditions ["for each new error"].

23. "为每条新的客户反馈创建一篇文档，标题为'反馈-{客户名}-YYYY-MM-DD'。" → outputs: document create, required false, target mode new, titleTemplate "反馈-{客户名}-{date}", placeholders [{"name":"客户名","source":"反馈的客户名称"}], cardinality perItem.

24. "每周一在设计团队新建一个事项，标题为'设计评审-YYYY-MM-DD'。" → outputs: issue create required, target mode new, team "设计团队", titleTemplate "设计评审-{date}", placeholders [], cardinality onePerRun.

25. "把本周总结追加到团队文档。" → outputs: document append required, target mode existing, title "", ambiguous true, description "the team document".`

// loopContractInput is the extraction's user message.
func loopContractInput(loop domain.Loop, teams []domain.Team) string {
	var input strings.Builder
	fmt.Fprintf(&input, "Loop name: %s\n", strings.TrimSpace(loop.Name))
	trigger := loop.TriggerType
	if trigger == "" {
		trigger = "schedule"
	}
	if trigger == "schedule" {
		fmt.Fprintf(&input, "Trigger: schedule\n")
	} else {
		fmt.Fprintf(&input, "Trigger: an event on a %s (the triggering %s is the run's subject)\n", trigger, trigger)
	}
	for _, team := range teams {
		if team.ID == loop.TeamID {
			fmt.Fprintf(&input, "The loop belongs to the team %s (key %s).\n", team.Name, team.Key)
		}
	}
	fmt.Fprintf(&input, "\nInstructions:\n%s\n", truncateSettingsText(loop.Instructions, 12000))
	return input.String()
}

// loopContractStub, set only by the package's tests, reads contracts from
// fixtures instead of the model, so tests whose fake providers script the
// run's turns are not sent an extraction request; a server with
// loopContractLive set still asks its model.
var loopContractStub func(loop domain.Loop) (*domain.LoopOutputContract, error)

// errLoopContractNotJSON: the model's arguments are not a JSON object.
var errLoopContractNotJSON = errors.New("the arguments are not a JSON object")

// errLoopContractNoModel: no model is configured to read the contract.
var errLoopContractNoModel = errors.New("no model is configured to read the loop's outputs")

// parseLoopContractArguments reads and validates the model's contract.
func parseLoopContractArguments(raw string) (*domain.LoopOutputContract, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(strings.TrimSuffix(raw, "```"), "```json")
	raw = strings.TrimPrefix(raw, "```")
	if start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}"); start >= 0 && end > start {
		raw = raw[start : end+1]
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("%w: %v", errLoopContractNotJSON, err)
	}
	missing := []string{}
	for _, key := range []string{"readOnly", "outputs", "always", "skipConditions"} {
		if _, ok := fields[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required fields: %s", strings.Join(missing, ", "))
	}
	var contract domain.LoopOutputContract
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		return nil, fmt.Errorf("the contract does not match the schema: %v", err)
	}
	if contract.Outputs == nil {
		contract.Outputs = []domain.LoopContractOutput{}
	}
	// Only the model's fields: the runtime sets these.
	contract.Source, contract.InstructionsHash, contract.Model, contract.ExtractedAt, contract.Error, contract.UpdatedBy = "", "", "", nil, "", ""
	for index := range contract.Outputs {
		output := &contract.Outputs[index]
		target := &output.Target
		target.DocumentID, target.TeamID, target.ProjectID, target.InitiativeID, target.RenderedTitle = "", "", "", "", ""
		if output.Kind == "document" && (output.Operation == loopOpUpdate || output.Operation == loopOpAppend) && strings.TrimSpace(target.Title) == "" {
			// An existing document the instructions do not name is the
			// owner's to pick, never any document.
			target.Ambiguous, target.Mode = true, "existing"
		}
	}
	if err := normalizeLoopContract(&contract); err != nil {
		return nil, err
	}
	return &contract, nil
}

// requestLoopContract asks the model for a loop's contract: up to two
// attempts, the second told what was wrong with the first.
func (s *server) requestLoopContract(ctx context.Context, loop domain.Loop, teams []domain.Team) (*domain.LoopOutputContract, error) {
	if !s.agent.Enabled || strings.TrimSpace(s.agent.BaseURL) == "" && strings.TrimSpace(s.agent.Model) == "" {
		return nil, errLoopContractNoModel
	}
	if loopContractStub != nil && !s.loopContractLive {
		contract, err := loopContractStub(loop)
		if contract != nil {
			now := time.Now().UTC()
			contract.Source, contract.InstructionsHash, contract.Model, contract.ExtractedAt = loopContractModel, loopContractHashOf(loop), s.agent.Model, &now
		}
		return contract, err
	}
	messages := []agentProviderMessage{{Role: "system", Content: loopContractSystemPrompt}, {Role: "user", Content: loopContractInput(loop, teams)}}
	ctx = withAgentMaxOutputTokens(ctx, 8192)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		// A stalled attempt leaves time for the second one.
		attemptCtx, cancel := context.WithTimeout(ctx, loopContractAttemptTimeout)
		turn, err := s.requestAgentStructuredTurn(attemptCtx, messages, loopContractTool)
		cancel()
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				break
			}
			continue
		}
		raw := strings.TrimSpace(turn.Text)
		for _, call := range turn.ToolCalls {
			if call.Name == loopContractToolName {
				raw = string(call.Arguments)
				break
			}
		}
		contract, err := parseLoopContractArguments(raw)
		if err == nil {
			now := time.Now().UTC()
			contract.Source, contract.InstructionsHash, contract.Model, contract.ExtractedAt = loopContractModel, loopContractHashOf(loop), s.agent.Model, &now
			return contract, nil
		}
		lastErr = fmt.Errorf("invalid contract: %w", err)
		if errors.Is(err, errLoopContractNotJSON) {
			// Unreadable arguments (tool-call markup leaked into them): ask afresh rather than echo them back.
			messages = append(messages[:2:2], agentProviderMessage{Role: "user", Content: fmt.Sprintf("Answer only by calling %s once; its arguments are one JSON object that follows the schema, with no other text or markup.", loopContractToolName)})
			continue
		}
		messages = append(messages, agentProviderMessage{Role: "user", Content: fmt.Sprintf("Your contract was not valid: %s.\nYour answer was:\n%s\nCall %s again with the complete, corrected contract: its arguments are one JSON object that follows the schema, with no other text or markup.", err.Error(), truncateSettingsText(raw, 6000), loopContractToolName)})
	}
	return nil, lastErr
}

// loopContractAttemptTimeout bounds one model call of an extraction; two
// attempts fit in loopContractReadTimeout.
const (
	loopContractAttemptTimeout = 110 * time.Second
	loopContractReadTimeout    = 4 * time.Minute
)

// loopContractFlights runs one extraction per loop text at a time.
var loopContractFlights = struct {
	mu      sync.Mutex
	running map[string]*loopContractFlight
}{running: map[string]*loopContractFlight{}}

type loopContractFlight struct {
	done     chan struct{}
	contract *domain.LoopOutputContract
}

// loopFallbackRetry is how long a fallback contract stands before the model
// is asked again.
const loopFallbackRetry = 10 * time.Minute

// loopContractFor is the contract a loop's runs use now: a stored one when
// it fits the text, else read by the model and stored, else the fallback
// (stored too, so the editor shows the warning).
func (s *server) loopContractFor(ctx context.Context, workspace string, loop domain.Loop) *domain.LoopOutputContract {
	if contract, ok := loopStoredContract(loop); ok {
		return contract
	}
	hash := loopContractHashOf(loop)
	if current := loop.OutputContract; current != nil && current.Source == loopContractFallback && current.InstructionsHash == hash && current.ExtractedAt != nil && time.Since(*current.ExtractedAt) < loopFallbackRetry {
		return current
	}
	key := workspace + "|" + loop.ID + "|" + hash
	loopContractFlights.mu.Lock()
	flight, ok := loopContractFlights.running[key]
	if !ok {
		flight = &loopContractFlight{done: make(chan struct{})}
		loopContractFlights.running[key] = flight
		// The read outlives the request or run that asked for it (an editor
		// that navigates away, a cancelled run): others may be waiting on it,
		// and a cancelled read must not leave a fallback behind.
		go s.readLoopContract(context.WithoutCancel(ctx), workspace, loop, hash, key, flight)
	}
	loopContractFlights.mu.Unlock()
	select {
	case <-flight.done:
		return flight.contract
	case <-ctx.Done():
		// This caller stops waiting; the contract it gets is never stored.
		return loopFallbackContract(hash, ctx.Err().Error())
	}
}

// readLoopContract is one flight's model read: it stores the result (the
// fallback when the read fails) and hands it to every waiter.
func (s *server) readLoopContract(ctx context.Context, workspace string, loop domain.Loop, hash, key string, flight *loopContractFlight) {
	defer func() {
		loopContractFlights.mu.Lock()
		delete(loopContractFlights.running, key)
		loopContractFlights.mu.Unlock()
		close(flight.done)
	}()
	teams := []domain.Team{}
	if data, ok := s.store.WorkspaceMetadataFields(workspace, "teams"); ok {
		teams = data.Teams
	}
	callCtx, cancel := context.WithTimeout(ctx, loopContractReadTimeout)
	defer cancel()
	contract, err := s.requestLoopContract(callCtx, loop, teams)
	if err != nil {
		log.Printf("Loop output contract workspace=%s loop=%s: %v", workspace, loop.ID, err)
		reason := loopContractFallbackNotice
		if errors.Is(err, errLoopContractNoModel) {
			reason += " (no model is configured)"
		}
		contract = loopFallbackContract(hash, reason)
	}
	s.storeLoopContract(workspace, loop.ID, hash, contract)
	flight.contract = contract
}

// storeLoopContract caches a read contract on the loop, when its text is
// still the one read and no person set the outputs, and on the loop's
// versions with the same text.
func (s *server) storeLoopContract(workspace, loopID, hash string, contract *domain.LoopOutputContract) {
	err := s.store.MutateWorkspace(context.Background(), workspace, "loop.output_contract", loopID, nil, func(data *domain.Bootstrap) error {
		loop := loopByID(data, loopID)
		if loop == nil {
			return errNotFound
		}
		if loopContractHashOf(*loop) == hash {
			if loop.OutputContract != nil && loop.OutputContract.Source == loopContractPerson {
				if loopContractStale(*loop) {
					loop.SuggestedOutputContract = mergeLoopPersonChoices(loop.OutputContract, contract)
				}
			} else {
				loop.OutputContract = contract
				loop.SuggestedOutputContract = nil
			}
		}
		if contract.Source == loopContractModel {
			for index := range data.LoopVersions {
				version := &data.LoopVersions[index]
				if version.LoopID == loopID && loopContractHash(version.Definition.Name, version.Definition.Instructions) == hash && (version.OutputContract == nil || version.OutputContract.Source != loopContractPerson) {
					version.OutputContract = contract
				}
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNotFound) {
		log.Printf("Loop output contract store workspace=%s loop=%s: %v", workspace, loopID, err)
	}
}

// mergeLoopPersonChoices keeps choices made in the editor when a fresh model
// read describes the same output. The model still owns fields that the new
// instructions clearly changed; only picked entities and edited title
// templates are carried across the review boundary.
func mergeLoopPersonChoices(person, suggested *domain.LoopOutputContract) *domain.LoopOutputContract {
	if person == nil || suggested == nil {
		return suggested
	}
	merged := *suggested
	merged.Outputs = make([]domain.LoopContractOutput, len(suggested.Outputs))
	copy(merged.Outputs, suggested.Outputs)
	used := make(map[int]bool, len(person.Outputs))
	for index := range merged.Outputs {
		merged.Outputs[index].Target.Placeholders = append([]domain.LoopContractPlaceholder(nil), suggested.Outputs[index].Target.Placeholders...)
	}
	for _, previous := range person.Outputs {
		match := -1
		for index, candidate := range merged.Outputs {
			if used[index] || (candidate.Kind != previous.Kind || candidate.Operation != previous.Operation) {
				continue
			}
			match = index
			break
		}
		if match < 0 && strings.TrimSpace(previous.Evidence) != "" {
			for index, candidate := range merged.Outputs {
				if !used[index] && strings.TrimSpace(candidate.Evidence) == strings.TrimSpace(previous.Evidence) {
					match = index
					break
				}
			}
		}
		if match < 0 {
			continue
		}
		used[match] = true
		if !loopTargetClearlyChanged(previous.Target, merged.Outputs[match].Target) {
			carryLoopPersonTarget(&merged.Outputs[match].Target, previous.Target)
		}
	}
	return &merged
}

func loopTargetClearlyChanged(previous, next domain.LoopContractTarget) bool {
	if previous.Mode != "" && next.Mode != "" && previous.Mode != next.Mode {
		return true
	}
	if previous.DocumentID != "" && next.DocumentID != "" && previous.DocumentID != next.DocumentID {
		return true
	}
	if previous.DocumentID != "" && next.Title != "" && previous.Title != "" && !strings.EqualFold(previous.Title, next.Title) {
		return true
	}
	if previous.ProjectID != "" && next.ProjectID != "" && previous.ProjectID != next.ProjectID {
		return true
	}
	if previous.ProjectID != "" && next.Project != "" && previous.Project != "" && !strings.EqualFold(previous.Project, next.Project) {
		return true
	}
	if previous.InitiativeID != "" && next.InitiativeID != "" && previous.InitiativeID != next.InitiativeID {
		return true
	}
	if previous.InitiativeID != "" && next.Initiative != "" && previous.Initiative != "" && !strings.EqualFold(previous.Initiative, next.Initiative) {
		return true
	}
	if previous.TeamID != "" && next.TeamID != "" && previous.TeamID != next.TeamID {
		return true
	}
	if previous.TeamID != "" && next.Team != "" && previous.Team != "" && !strings.EqualFold(previous.Team, next.Team) {
		return true
	}
	if previous.Match != "" && next.Match != "" && !strings.EqualFold(previous.Match, next.Match) {
		return true
	}
	if previous.TitleTemplate != "" && next.TitleTemplate != "" && previous.TitleTemplate != next.TitleTemplate {
		return true
	}
	return false
}

func carryLoopPersonTarget(next *domain.LoopContractTarget, previous domain.LoopContractTarget) {
	if previous.DocumentID != "" {
		next.DocumentID, next.Title, next.Ambiguous, next.Mode = previous.DocumentID, previous.Title, false, previous.Mode
	}
	if previous.ProjectID != "" {
		next.ProjectID, next.Project = previous.ProjectID, previous.Project
	}
	if previous.InitiativeID != "" {
		next.InitiativeID, next.Initiative = previous.InitiativeID, previous.Initiative
	}
	if previous.TeamID != "" {
		next.TeamID, next.Team = previous.TeamID, previous.Team
	}
	if previous.TitleTemplate != "" {
		next.TitleTemplate = previous.TitleTemplate
		next.Placeholders = append([]domain.LoopContractPlaceholder(nil), previous.Placeholders...)
	}
}

// extractLoopContractAsync reads a saved loop's contract in the background
// (after a short pause, so a burst of saves reads it once).
func (s *server) extractLoopContractAsync(workspace string, loop domain.Loop) {
	if !s.agent.Enabled || strings.TrimSpace(loop.Instructions) == "" {
		return
	}
	if _, ok := loopStoredContract(loopContractReviewCopy(loop)); ok {
		return
	}
	go func() {
		if s.loopContractDebounce > 0 {
			time.Sleep(s.loopContractDebounce)
		}
		data, ok := s.store.WorkspaceMetadataFields(workspace, "loops")
		if !ok {
			return
		}
		current := loopByID(&data, loop.ID)
		if current == nil || loopContractHashOf(*current) != loopContractHashOf(loop) {
			return
		}
		s.loopContractFor(s.store.WorkerContext(), data.Workspace.URLKey, loopContractReviewCopy(*current))
	}()
}

// A review reads the new text without replacing the contract runs still use.
func loopContractReviewCopy(loop domain.Loop) domain.Loop {
	if loop.OutputContract != nil && loop.OutputContract.Source == loopContractPerson && loopContractStale(loop) {
		loop.OutputContract, loop.ExpectedOutputs = loop.SuggestedOutputContract, nil
	}
	return loop
}

// analyzeLoopOutputs reads a loop's output contract now (the editor's "What
// this loop will produce"): the stored one when it fits the text, else the
// model's; force reads it again even then (a person's outputs stay). It
// answers with the loop.
func (s *server) analyzeLoopOutputs(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Force bool `json:"force"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	loop := loopByID(&data, r.PathValue("id"))
	if loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	if err := s.checkLoopEditor(&data, *loop); err != nil {
		respondLoopMutation(w, err, http.StatusOK, nil)
		return
	}
	current := loopContractReviewCopy(*loop)
	if input.Force && (current.OutputContract == nil || current.OutputContract.Source != loopContractPerson) {
		hash := loopContractHashOf(current)
		contract, err := s.requestLoopContract(r.Context(), current, data.Teams)
		if err != nil {
			log.Printf("Loop output contract workspace=%s loop=%s: %v", data.Workspace.URLKey, current.ID, err)
			contract = loopFallbackContract(hash, loopContractFallbackNotice)
		}
		s.storeLoopContract(data.Workspace.URLKey, current.ID, hash, contract)
	} else if strings.TrimSpace(current.Instructions) != "" {
		s.loopContractFor(r.Context(), data.Workspace.URLKey, current)
	}
	stored, ok := s.store.WorkspaceMetadataFields(workspaceKey(r), "loops")
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if loop = loopByID(&stored, current.ID); loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	writeJSON(w, http.StatusOK, presentLoop(s.loopRunCounts(r.Context(), stored.Workspace.URLKey), *loop))
}

// startLoopContractMigration reads the contracts of existing loops in the
// background: one loop at a time (one model call per loop text), each
// failure retried with backoff, against the deployment's configured model.
// Loops a person set outputs for, or whose contract fits their text, are
// skipped; until a loop's contract is read, its runs read it when they start.
func (s *server) startLoopContractMigration() {
	if !s.loopContractMigration || !s.agent.Enabled {
		return
	}
	go func() {
		ctx := s.store.WorkerContext()
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		for _, workspace := range s.store.WorkspaceKeys() {
			data, ok := s.store.WorkspaceMetadataFields(workspace, "loops")
			if !ok {
				continue
			}
			for _, loop := range data.Loops {
				if strings.TrimSpace(loop.Instructions) == "" {
					continue
				}
				if _, ok := loopStoredContract(loop); ok {
					continue
				}
				for attempt, backoff := 0, 10*time.Second; attempt < 3; attempt, backoff = attempt+1, backoff*3 {
					contract := s.loopContractFor(ctx, data.Workspace.URLKey, loop)
					if ctx.Err() != nil {
						return
					}
					if contract.Source != loopContractFallback || strings.Contains(contract.Error, "no model") {
						break
					}
					// A fallback stands for loopFallbackRetry; ask again after a pause.
					loop.OutputContract = nil
					select {
					case <-ctx.Done():
						return
					case <-time.After(backoff):
					}
				}
			}
		}
	}()
}
