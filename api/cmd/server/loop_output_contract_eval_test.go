package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
)

// An evaluation of the output contract extractor against a real model: the
// instructions earlier inference misread and new ones, in Chinese and
// English, each with the contract fields that matter for enforcement. It
// calls the model once per instruction, one at a time, and only when
// FLOW_LOOP_CONTRACT_EVAL=1 (CI never calls a model). It defaults to the local
// OpenAI-compatible proxy:
//
//	FLOW_LOOP_CONTRACT_EVAL=1 go test ./cmd/server -run TestLoopContractEvaluation -v
//
// FLOW_LOOP_CONTRACT_EVAL_URL, _MODEL, _PROTOCOL and _KEY point it elsewhere.

type loopContractEvalCase struct {
	text     string
	trigger  string // empty: schedule
	readOnly bool
	// writes are the kinds the contract creates, updates or appends to, by
	// operation ("update|append" accepts either); kinds left out must not be
	// written.
	writes   map[string]string
	required []string
	// title is a new document's title template; docTitle an existing
	// document's title; team the team a new document goes to.
	title, docTitle, team string
	always, skips, group  bool
}

var loopContractEvalCases = []loopContractEvalCase{
	// The reproduction.
	{text: loopDailyDocumentZH, writes: map[string]string{"document": "create"}, required: []string{"document"}, title: "每日总结-{date}"},
	// "今日新增事项" is a report section, not an issue to create.
	{text: "请总结团队今天的工作。\n1. 今日完成事项：列出已完成的事项。\n2. 今日新增事项：简要列出今天新创建的事项及创建者。\n将总结结果追加到已有团队文档中，不要在任何事项、项目或其他资源上发布评论。", writes: map[string]string{"document": "append"}, required: []string{"document"}},
	{text: "汇总今日新增事项和新建事项的数量。", readOnly: true},
	// Reply only, write nothing.
	{text: loopReplyOnlyDigestZH, readOnly: true},
	{text: "汇总今日工作并追加到团队文档。只回复总结文本即可，不要写入任何内容。", readOnly: true},
	{text: "Summarize today's work. Reply only; don't post comments or write anything to any issue, project, document or other resource.", readOnly: true},
	{text: "List today's new issues and just reply with the summary.", readOnly: true},
	// A report named by its period is the reply unless it is put somewhere.
	{text: "每周五写周报，汇总本周完成的事项。", readOnly: true},
	{text: "生成本周周报。", readOnly: true},
	{text: "Write the weekly report.", readOnly: true},
	{text: "每周五把周报发布为项目更新。", writes: map[string]string{"statusUpdate": "create"}, required: []string{"statusUpdate"}},
	{text: "Post the weekly digest as a project update.", writes: map[string]string{"statusUpdate": "create"}, required: []string{"statusUpdate"}},
	{text: "把周报追加到团队文档。", writes: map[string]string{"document": "append"}, required: []string{"document"}},
	// Appending to an existing team document.
	{text: loopTeamDigestZH, writes: map[string]string{"document": "append"}, required: []string{"document"}},
	{text: loopTeamDigestEN, writes: map[string]string{"document": "append"}, required: []string{"document"}},
	{text: "汇总今日进展并写入团队文档，除此之外不要做任何修改。", writes: map[string]string{"document": "update|append"}, required: []string{"document"}},
	{text: "Update the team document with today's summary.", writes: map[string]string{"document": "update"}, required: []string{"document"}},
	{text: "Append today's release notes to the \"Release log\" document.", writes: map[string]string{"document": "append"}, required: []string{"document"}, docTitle: "Release log"},
	// Refusals enumerated with 、 and exceptions.
	{text: "汇总今日工作，无需在事项、项目上发布评论或创建事项。", readOnly: true},
	{text: "汇总今日工作。禁止在事项、项目或其他资源上发布评论。", readOnly: true},
	{text: "除追加团队文档外，不要发布评论或创建事项。", writes: map[string]string{"document": "append"}, required: []string{"document"}},
	{text: "Append today's notes to the team doc without commenting on any issue.", writes: map[string]string{"document": "append"}, required: []string{"document"}},
	// Conditions: even if nothing changed, skips.
	{text: "汇总今日工作并追加到团队文档。如果没有变动也要写日报。", writes: map[string]string{"document": "append"}, required: []string{"document"}, always: true},
	{text: "每天把日报追加到团队文档，节假日跳过。", writes: map[string]string{"document": "append"}, required: []string{"document"}, skips: true},
	{text: "Append the daily report to the team doc; skip on public holidays and weekends.", writes: map[string]string{"document": "append"}, required: []string{"document"}, skips: true},
	{text: "Post a weekly update on each project. If nothing changed, don't post.", writes: map[string]string{"statusUpdate": "create"}, required: []string{"statusUpdate"}, skips: true},
	// New documents.
	{text: "Create a new document with today's summary.", writes: map[string]string{"document": "create"}, required: []string{"document"}},
	{text: "新建一篇文档记录今日总结。", writes: map[string]string{"document": "create"}, required: []string{"document"}},
	{text: "Every Monday create a doc titled \"Week {week} plan\" in the Mobile team with the week's priorities.", writes: map[string]string{"document": "create"}, required: []string{"document"}, title: "Week {week} plan", team: "Mobile"},
	{text: "Post the report as a document or project update titled \"Feature requests\".", writes: map[string]string{"document": "create", "statusUpdate": "create"}, required: []string{"statusUpdate", "document"}, group: true},
	// Issues: real creates, conditional creates, updates.
	{text: "新建一个事项跟踪登录失败的问题。", writes: map[string]string{"issue": "create"}, required: []string{"issue"}},
	{text: "如果发现阻塞，创建事项并指派给负责人。", writes: map[string]string{"issue": "create|create,update"}},
	{text: "Review the error log and open a new issue for every new error.", writes: map[string]string{"issue": "create"}},
	{text: "Triage new issues and set their priority; do not comment.", trigger: "issue", writes: map[string]string{"issue": "update"}, required: []string{"issue"}},
	// Comments.
	{text: "Comment on new bugs.", trigger: "issue", writes: map[string]string{"comment": "create"}, required: []string{"comment"}},
	{text: "Read the doc and comment on the issue.", trigger: "issue", writes: map[string]string{"comment": "create"}, required: []string{"comment"}},
	// Plain reports.
	{text: "检查项目是否有新的状态更新，汇总今天的事项；无变化时输出今日无变动", readOnly: true},
	{text: "Count open issues per team and summarize.", readOnly: true},
	{text: "统计每个团队的未完成事项数量并汇总", readOnly: true},
	{text: "Read the team document and summarize it.", readOnly: true},
}

// loopContractWrites lists a contract's write operations per kind.
func loopContractEvalWrites(contract *domain.LoopOutputContract) map[string]string {
	ops := map[string][]string{}
	for _, output := range contract.Outputs {
		if loopOpWrites(output.Operation) && !slices.Contains(ops[output.Kind], output.Operation) {
			ops[output.Kind] = append(ops[output.Kind], output.Operation)
		}
	}
	result := map[string]string{}
	for kind, list := range ops {
		slices.Sort(list)
		result[kind] = strings.Join(list, ",")
	}
	return result
}

func TestLoopContractEvaluation(t *testing.T) {
	if os.Getenv("FLOW_LOOP_CONTRACT_EVAL") != "1" {
		t.Skip("set FLOW_LOOP_CONTRACT_EVAL=1 to evaluate the extractor against a real model")
	}
	env := func(key, fallback string) string {
		if value := os.Getenv(key); value != "" {
			return value
		}
		return fallback
	}
	srv := &server{loopContractLive: true, agent: appconfig.AgentConfig{
		Enabled: true, Protocol: env("FLOW_LOOP_CONTRACT_EVAL_PROTOCOL", "openai-chat-completions"), BaseURL: env("FLOW_LOOP_CONTRACT_EVAL_URL", "http://127.0.0.1:8787/v1"),
		APIKey: env("FLOW_LOOP_CONTRACT_EVAL_KEY", "local-go-proxy"), Model: env("FLOW_LOOP_CONTRACT_EVAL_MODEL", "deepseek-v4.1-flash"), MaxOutputTokens: 4096, Timeout: 3 * time.Minute,
	}}
	teams := []domain.Team{{ID: "team_mobile", Key: "MOB", Name: "Mobile"}, {ID: "team_dev", Key: "DEV", Name: "开发团队"}}
	only := os.Getenv("FLOW_LOOP_CONTRACT_EVAL_ONLY")
	fields := []string{"readOnly", "writes", "required", "target", "always", "skips", "group"}
	passed, total := map[string]int{}, map[string]int{}
	failures := []string{}
	for index, item := range loopContractEvalCases {
		if only != "" && !slices.Contains(strings.Split(only, ","), fmt.Sprint(index)) {
			continue
		}
		trigger := firstNonEmpty(item.trigger, "schedule")
		loop := domain.Loop{ID: fmt.Sprintf("loop_eval_%d", index), Name: "Loop", Instructions: item.text, TriggerType: trigger}
		ctx, cancel := context.WithTimeout(context.Background(), loopContractReadTimeout)
		started := time.Now()
		contract, err := srv.requestLoopContract(ctx, loop, teams)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Sprintf("#%d %q: extraction failed: %v", index, item.text, err))
			for _, field := range fields {
				total[field]++
			}
			continue
		}
		check := func(field string, ok bool, got, want any) {
			total[field]++
			if ok {
				passed[field]++
				return
			}
			failures = append(failures, fmt.Sprintf("#%d %q: %s = %v, want %v", index, item.text, field, got, want))
		}
		check("readOnly", contract.ReadOnly == item.readOnly, contract.ReadOnly, item.readOnly)
		writes := loopContractEvalWrites(contract)
		writesOK := !item.readOnly || len(writes) == 0
		if !item.readOnly {
			writesOK = len(writes) == len(item.writes)
			for kind, want := range item.writes {
				if !slices.Contains(strings.Split(want, "|"), writes[kind]) {
					writesOK = false
				}
			}
		}
		check("writes", writesOK, writes, item.writes)
		required := loopContractRequiredKinds(contract)
		wantRequired := slices.Clone(item.required)
		slices.Sort(wantRequired)
		gotRequired := slices.Clone(required)
		slices.Sort(gotRequired)
		check("required", slices.Equal(gotRequired, wantRequired), gotRequired, wantRequired)
		targetOK, target := true, ""
		for _, output := range loopContractOutputs(contract, "document") {
			if !loopOpWrites(output.Operation) {
				continue
			}
			target += fmt.Sprintf("%s mode=%s template=%q title=%q team=%q; ", output.Operation, output.Target.Mode, output.Target.TitleTemplate, output.Target.Title, output.Target.Team)
			if output.Operation == loopOpCreate && (output.Target.Mode != "new" || item.title != "" && output.Target.TitleTemplate != item.title || item.team != "" && !strings.EqualFold(output.Target.Team, item.team)) {
				targetOK = false
			}
			if (output.Operation == loopOpAppend || output.Operation == loopOpUpdate) && (output.Target.Mode != "existing" || !strings.EqualFold(output.Target.Title, item.docTitle)) {
				targetOK = false
			}
		}
		check("target", targetOK, target, fmt.Sprintf("template=%q title=%q team=%q", item.title, item.docTitle, item.team))
		check("always", contract.Always == item.always, contract.Always, item.always)
		check("skips", (len(contract.SkipConditions) > 0) == item.skips, contract.SkipConditions, item.skips)
		groups := 0
		for _, output := range contract.Outputs {
			if output.Group != "" && loopOpWrites(output.Operation) {
				groups++
			}
		}
		check("group", (groups > 1) == item.group, groups, item.group)
		t.Logf("#%d %.1fs readOnly=%v writes=%v required=%v", index, time.Since(started).Seconds(), contract.ReadOnly, writes, required)
	}
	report := []string{}
	for _, field := range fields {
		report = append(report, fmt.Sprintf("%s %d/%d", field, passed[field], total[field]))
	}
	t.Logf("accuracy: %s", strings.Join(report, ", "))
	for _, failure := range failures {
		t.Error(failure)
	}
}
