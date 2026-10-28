package main

import (
	"encoding/json"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// Contract fixtures: the output contracts the extractor produces for the
// instructions the package's tests run, read instead of a model call (see
// loopContractStub). Instructions without a fixture get the permissive
// contract: every kind may be written, none is required.

func init() {
	loopContractStub = func(loop domain.Loop) (*domain.LoopOutputContract, error) {
		contract := loopFixtureContract(loop.Instructions)
		if err := normalizeLoopContract(contract); err != nil {
			panic("invalid contract fixture for " + loop.Instructions + ": " + err.Error())
		}
		return contract, nil
	}
}

// out builds a contract output.
func out(kind, op string, required bool, target domain.LoopContractTarget, evidence string) domain.LoopContractOutput {
	return domain.LoopContractOutput{Kind: kind, Operation: op, Required: required, Target: target, Evidence: evidence}
}

var (
	anyTarget      = domain.LoopContractTarget{Mode: "any"}
	newTarget      = domain.LoopContractTarget{Mode: "new"}
	teamDocTarget  = domain.LoopContractTarget{Mode: "existing", Description: "the team's existing document"}
	triggerTarget  = domain.LoopContractTarget{Mode: "triggering"}
	readOnlyReport = func(forbidden ...string) *domain.LoopOutputContract {
		contract := &domain.LoopOutputContract{ReadOnly: true, Outputs: []domain.LoopContractOutput{}}
		for _, kind := range forbidden {
			contract.Outputs = append(contract.Outputs, out(kind, loopOpForbidden, false, anyTarget, ""))
		}
		return contract
	}
	teamDigestContract = func() *domain.LoopOutputContract {
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{
			out("document", loopOpAppend, true, teamDocTarget, "追加到已有团队文档"),
			out("issue", loopOpRead, false, anyTarget, ""),
			out("comment", loopOpForbidden, false, anyTarget, "不要在任何事项、项目或其他资源上发布评论"),
		}}
	}
	perProjectUpdate = func() *domain.LoopOutputContract {
		output := out("statusUpdate", loopOpCreate, true, anyTarget, "on each active project")
		output.Cardinality = "perItem"
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{output}}
	}
	appendContract = func() *domain.LoopOutputContract {
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{out("document", loopOpAppend, true, teamDocTarget, "")}}
	}
)

// loopContractFixtures are keyed by the loop's instructions.
var loopContractFixtures = map[string]func() *domain.LoopOutputContract{
	loopTeamDigestZH: teamDigestContract,
	loopTeamDigestEN: teamDigestContract,
	"汇总今日工作并追加到团队文档。如果没有变动也要写日报。": func() *domain.LoopOutputContract {
		contract := appendContract()
		contract.Always, contract.AlwaysEvidence = true, "如果没有变动也要写日报"
		return contract
	},
	"每天把日报追加到团队文档，节假日跳过。": func() *domain.LoopOutputContract {
		contract := appendContract()
		contract.SkipConditions = []domain.LoopContractCondition{{Condition: "public holidays", Evidence: "节假日跳过"}}
		return contract
	},
	"每天把日报追加到团队文档。":                           appendContract,
	"将今日总结追加到已有团队文档中。":                        appendContract,
	"汇总开发团队本周工作，将周报追加到已有团队文档中。":               appendContract,
	"Append today's summary to the team doc.": appendContract,
	"Create a new document with today's summary.": func() *domain.LoopOutputContract {
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{out("document", loopOpCreate, true, newTarget, "")}}
	},
	"Update the team document with today's summary.": func() *domain.LoopOutputContract {
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{out("document", loopOpUpdate, true, domain.LoopContractTarget{Mode: "existing", Description: "the team document"}, "")}}
	},
	"Read the members and the open issues, then write the team digest document.": func() *domain.LoopOutputContract {
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{out("document", loopOpCreate, true, newTarget, ""), out("issue", loopOpRead, false, anyTarget, "")}}
	},
	"阅读今天事项下的评论并汇总需要跟进的问题。不要在任何事项、项目或其他资源上发布评论。": func() *domain.LoopOutputContract { return readOnlyReport("comment") },
	loopReplyOnlyDigestZH:  func() *domain.LoopOutputContract { return readOnlyReport("comment", "document") },
	replyOnlyInstructions:  func() *domain.LoopOutputContract { return readOnlyReport("comment", "document") },
	"Summarize open bugs.": func() *domain.LoopOutputContract { return readOnlyReport() },
	"检查项目是否有新的状态更新，汇总今天的事项；无变化时输出今日无变动":                                                                                       func() *domain.LoopOutputContract { return readOnlyReport() },
	"Check whether projects have new status updates and summarize today's items; if nothing changed, reply No changes today.": func() *domain.LoopOutputContract { return readOnlyReport() },
	"Read the existing project status updates and summarize the risks.":                                                       func() *domain.LoopOutputContract { return readOnlyReport() },
	"汇总本周的项目进展，不要发布状态更新。":                                                                                                     func() *domain.LoopOutputContract { return readOnlyReport("statusUpdate") },
	"检查项目是否有新的状态更新，汇总今天的事项":                                                                                                   func() *domain.LoopOutputContract { return readOnlyReport() },
	"每周五为每个项目发布状态更新": func() *domain.LoopOutputContract {
		output := out("statusUpdate", loopOpCreate, true, anyTarget, "为每个项目发布状态更新")
		output.Cardinality = "perItem"
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{output}}
	},
	"Post a status update on each active project.":         perProjectUpdate,
	"Post a weekly project update on each active project.": perProjectUpdate,
	"List the projects and their issues.": func() *domain.LoopOutputContract {
		// The loop is named "Flow 项目周更新" (a project weekly update).
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{out("statusUpdate", loopOpCreate, true, anyTarget, "Flow 项目周更新")}}
	},
	"Comment on new bugs.": func() *domain.LoopOutputContract {
		return &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{out("comment", loopOpCreate, true, triggerTarget, "Comment on new bugs.")}}
	},
}

// loopTestContracts are contracts a test sets for its own instructions.
var loopTestContracts = struct {
	mu        sync.Mutex
	contracts map[string]string
}{contracts: map[string]string{}}

// setLoopTestContract makes the extractor produce contract (JSON) for the
// instructions.
func setLoopTestContract(instructions, contract string) {
	loopTestContracts.mu.Lock()
	defer loopTestContracts.mu.Unlock()
	loopTestContracts.contracts[strings.TrimSpace(instructions)] = contract
}

func loopFixtureContract(instructions string) *domain.LoopOutputContract {
	instructions = strings.TrimSpace(instructions)
	loopTestContracts.mu.Lock()
	raw, ok := loopTestContracts.contracts[instructions]
	loopTestContracts.mu.Unlock()
	if ok {
		var contract domain.LoopOutputContract
		if err := json.Unmarshal([]byte(raw), &contract); err != nil {
			panic(err)
		}
		return &contract
	}
	if fixture, ok := loopContractFixtures[instructions]; ok {
		return fixture()
	}
	permissive := &domain.LoopOutputContract{Outputs: []domain.LoopContractOutput{}}
	for _, kind := range loopOutputKinds {
		permissive.Outputs = append(permissive.Outputs, out(kind, loopOpWrite, false, anyTarget, ""))
	}
	return permissive
}
