package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Output contracts: what a loop's runs produce, for each output kind — the
// operation, its target, how many, when, and the instruction text it was
// read from (domain.LoopOutputContract). The model reads it from the loop's
// name and instructions once per instructions hash
// (loop_output_contract_extraction.go); a person can edit it in the loop
// editor, which makes it authoritative. A run's prompt is written from it,
// its write tools are checked against it, and acceptance verifies that its
// required outputs exist.

// The operations of a contract output.
const (
	loopOpCreate = "create"
	loopOpUpdate = "update"
	loopOpAppend = "append"
	// loopOpWrite is a create or an update (a person's or a legacy
	// expectedOutputs output that does not say which).
	loopOpWrite     = "write"
	loopOpRead      = "read"
	loopOpForbidden = "forbidden"
)

// The sources of a contract.
const (
	loopContractModel    = "model"
	loopContractPerson   = "person"
	loopContractFallback = "fallback"
)

var (
	loopContractOperations    = []string{loopOpCreate, loopOpUpdate, loopOpAppend, loopOpWrite, loopOpRead, loopOpForbidden}
	loopContractTargetModes   = []string{"new", "existing", "triggering", "matching", "any"}
	loopContractCardinalities = []string{"onePerRun", "perItem", "any"}
)

// loopContractFallbackNotice is the fallback contract's error when the
// outputs could not be read; the web app translates it.
const loopContractFallbackNotice = "Couldn't determine outputs automatically — set them"

// loopOpWrites reports whether an operation makes or changes an output.
func loopOpWrites(op string) bool {
	return op == loopOpCreate || op == loopOpUpdate || op == loopOpAppend || op == loopOpWrite
}

// loopContractHash identifies the text a model contract is read from: the
// loop's name and instructions and the extraction prompt's version.
func loopContractHash(name, instructions string) string {
	sum := sha256.Sum256([]byte(loopContractPromptVersion + "\x00" + strings.TrimSpace(name) + "\x00" + strings.TrimSpace(instructions)))
	return hex.EncodeToString(sum[:12])
}

func loopContractHashOf(loop domain.Loop) string {
	return loopContractHash(loop.Name, loop.Instructions)
}

// normalizeLoopContract checks a contract against the schema and cleans it:
// enums, kinds and operations that fit together, targets for their kinds and
// bounded text. It reports every problem it finds (the extractor sends them
// back to the model); a person's contract is rejected with them.
func normalizeLoopContract(contract *domain.LoopOutputContract) error {
	problems := []string{}
	clip := func(text string, limit int) string {
		text = strings.TrimSpace(text)
		if utf8.RuneCountInString(text) > limit {
			text = string([]rune(text)[:limit])
		}
		return text
	}
	if len(contract.Outputs) > 20 {
		problems = append(problems, "at most 20 outputs")
		contract.Outputs = contract.Outputs[:20]
	}
	outputs := []domain.LoopContractOutput{}
	for index, output := range contract.Outputs {
		where := fmt.Sprintf("outputs[%d]", index)
		output.Kind, output.Operation = strings.TrimSpace(output.Kind), strings.TrimSpace(output.Operation)
		if !slices.Contains(loopOutputKinds, output.Kind) {
			problems = append(problems, fmt.Sprintf("%s.kind must be one of %s", where, strings.Join(loopOutputKinds, ", ")))
			continue
		}
		if !slices.Contains(loopContractOperations, output.Operation) {
			problems = append(problems, fmt.Sprintf("%s.operation must be one of %s", where, strings.Join(loopContractOperations, ", ")))
			continue
		}
		if output.Operation == loopOpAppend && output.Kind != "document" {
			problems = append(problems, fmt.Sprintf("%s: append is only for documents; use create or update for %s", where, output.Kind))
			continue
		}
		if !loopOpWrites(output.Operation) {
			output.Required = false
		}
		target := &output.Target
		target.Mode = strings.TrimSpace(target.Mode)
		if target.Mode == "" {
			target.Mode = "any"
		}
		if !slices.Contains(loopContractTargetModes, target.Mode) {
			problems = append(problems, fmt.Sprintf("%s.target.mode must be one of %s", where, strings.Join(loopContractTargetModes, ", ")))
			continue
		}
		target.TitleTemplate, target.Title, target.DocumentID = clip(target.TitleTemplate, 200), clip(target.Title, 200), clip(target.DocumentID, 120)
		target.Team, target.TeamID, target.Description = clip(target.Team, 120), clip(target.TeamID, 120), clip(target.Description, 300)
		target.Project, target.ProjectID, target.Initiative, target.InitiativeID = clip(target.Project, 120), clip(target.ProjectID, 120), clip(target.Initiative, 120), clip(target.InitiativeID, 120)
		target.Match, target.RenderedTitle = clip(target.Match, 300), clip(target.RenderedTitle, 200)
		if output.Kind == "document" && output.Operation == loopOpCreate && target.Mode == "existing" {
			problems = append(problems, fmt.Sprintf("%s: a created document is new; use target.mode new (or operation update/append for an existing document)", where))
			continue
		}
		if output.Kind == "document" && (output.Operation == loopOpUpdate || output.Operation == loopOpAppend) && target.Mode == "new" && target.TitleTemplate != "" {
			problems = append(problems, fmt.Sprintf("%s: update and append change an existing document; give its title in target.title, or use operation create for a new document", where))
			continue
		}
		if problem := normalizeLoopContractTarget(output.Kind, output.Operation, target, clip); problem != "" {
			problems = append(problems, where+": "+problem)
			continue
		}
		output.Cardinality = strings.TrimSpace(output.Cardinality)
		if output.Cardinality == "" {
			output.Cardinality = "any"
		}
		if !slices.Contains(loopContractCardinalities, output.Cardinality) {
			problems = append(problems, fmt.Sprintf("%s.cardinality must be one of %s", where, strings.Join(loopContractCardinalities, ", ")))
			continue
		}
		conditions := []string{}
		for _, condition := range output.Conditions {
			if condition = clip(condition, 300); condition != "" && len(conditions) < 10 {
				conditions = append(conditions, condition)
			}
		}
		output.Conditions = conditions
		output.Group = clip(output.Group, 40)
		output.Evidence, output.Reasoning = clip(output.Evidence, 500), clip(output.Reasoning, 500)
		outputs = append(outputs, output)
	}
	contract.Outputs = outputs
	if contract.ReadOnly && slices.ContainsFunc(outputs, func(output domain.LoopContractOutput) bool { return loopOpWrites(output.Operation) }) {
		problems = append(problems, "readOnly is true but some outputs create, update or append: a read-only loop changes nothing (mark those outputs read or forbidden, or set readOnly false)")
	}
	contract.ReadOnlyEvidence, contract.AlwaysEvidence = clip(contract.ReadOnlyEvidence, 500), clip(contract.AlwaysEvidence, 500)
	skips := []domain.LoopContractCondition{}
	for _, skip := range contract.SkipConditions {
		skip.Condition, skip.Evidence = clip(skip.Condition, 300), clip(skip.Evidence, 500)
		if skip.Condition != "" && len(skips) < 10 {
			skips = append(skips, skip)
		}
	}
	contract.SkipConditions = skips
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// loopLegacyContract is the contract of a loop's legacy expectedOutputs
// (["none"]: nothing required): the kinds named are required in any form,
// and, as before, every other write is allowed.
func loopLegacyContract(expected []string) *domain.LoopOutputContract {
	contract := &domain.LoopOutputContract{Source: loopContractPerson, Outputs: []domain.LoopContractOutput{}}
	for _, kind := range loopOutputKinds {
		required := slices.Contains(expected, kind)
		contract.Outputs = append(contract.Outputs, domain.LoopContractOutput{Kind: kind, Operation: loopOpWrite, Required: required, Target: domain.LoopContractTarget{Mode: "any"}, Cardinality: "any"})
	}
	return contract
}

// loopFallbackContract is the conservative contract used when the outputs
// could not be read: the run reports in its reply and changes nothing, until
// a person sets the outputs.
func loopFallbackContract(hash, reason string) *domain.LoopOutputContract {
	now := time.Now().UTC()
	return &domain.LoopOutputContract{Source: loopContractFallback, InstructionsHash: hash, ReadOnly: true, Outputs: []domain.LoopContractOutput{}, Error: firstNonEmpty(reason, loopContractFallbackNotice), ExtractedAt: &now}
}

// loopStoredContract is the contract a loop has now without asking the
// model: a person's (authoritative), the legacy expectedOutputs, or a model
// contract read from the current text. ok is false when the model must read
// it (none yet, a stale one, or a fallback to retry).
func loopStoredContract(loop domain.Loop) (*domain.LoopOutputContract, bool) {
	contract := loop.OutputContract
	if contract != nil && contract.Source == loopContractPerson {
		return contract, true
	}
	if len(loop.ExpectedOutputs) > 0 {
		return loopLegacyContract(loop.ExpectedOutputs), true
	}
	if contract != nil && contract.Source == loopContractModel && contract.InstructionsHash == loopContractHashOf(loop) {
		return contract, true
	}
	return contract, false
}

// loopContractStale reports whether a presented contract no longer matches
// the loop's text: a model contract to read again, or a person's contract
// set for earlier instructions.
func loopContractStale(loop domain.Loop) bool {
	contract := loop.OutputContract
	return contract != nil && contract.InstructionsHash != "" && contract.InstructionsHash != loopContractHashOf(loop)
}

// loopRunPeriod is the time a run's period placeholders are rendered for:
// the scheduled time, else now, in the loop's timezone.
func loopRunPeriod(loop domain.Loop, trigger loopTrigger, now time.Time) time.Time {
	at := now
	if !trigger.ScheduledAt.IsZero() {
		at = trigger.ScheduledAt
	}
	if loop.TriggerType == "schedule" {
		return at.In(loopScheduleLocation(normalizeLoopTriggerConfig("schedule", loop.TriggerConfig)))
	}
	return at.UTC()
}

// loopTitlePlaceholders are the period placeholders a title template may use,
// with the spelled-out date formats people and models also write.
var loopTitlePlaceholders = regexp.MustCompile(`\{(date|week|month|year|day)\}|YYYY-MM-DD|yyyy-mm-dd|YYYY/MM/DD|YYYYMMDD|YYYY-Www|YYYY-WW|YYYY-MM|yyyy-mm|YYYY`)

// loopUnknownPlaceholder is a placeholder the runtime cannot fill ("{topic}").
var loopUnknownPlaceholder = regexp.MustCompile(`\{[^{}]+\}`)

// renderLoopTitle fills a title template's period placeholders for a run.
// The week is the occurrence's ISO week when the loop runs weekly.
func renderLoopTitle(template string, at time.Time, occurrence string) string {
	year, week := at.ISOWeek()
	weekLabel := fmt.Sprintf("%04d-W%02d", year, week)
	if len(occurrence) == len("2026-W44") && strings.Contains(occurrence, "-W") {
		weekLabel = occurrence
	}
	return loopTitlePlaceholders.ReplaceAllStringFunc(template, func(token string) string {
		switch token {
		case "{date}", "{day}", "YYYY-MM-DD", "yyyy-mm-dd":
			return at.Format("2006-01-02")
		case "YYYY/MM/DD":
			return at.Format("2006/01/02")
		case "YYYYMMDD":
			return at.Format("20060102")
		case "{week}", "YYYY-Www", "YYYY-WW":
			return weekLabel
		case "{month}", "YYYY-MM", "yyyy-mm":
			return at.Format("2006-01")
		}
		return at.Format("2006")
	})
}

// loopTitleMatches reports whether a title fits a rendered template whose
// unknown placeholders ("{topic}") stand for any text.
func loopTitleMatches(rendered, title string) bool {
	if !loopUnknownPlaceholder.MatchString(rendered) {
		return strings.EqualFold(strings.TrimSpace(rendered), strings.TrimSpace(title))
	}
	parts := loopUnknownPlaceholder.Split(rendered, -1)
	for index := range parts {
		parts[index] = regexp.QuoteMeta(parts[index])
	}
	pattern, err := regexp.Compile(`(?i)^\s*` + strings.Join(parts, `.+`) + `\s*$`)
	return err == nil && pattern.MatchString(title)
}

// loopFindTeam finds a team by id, key or name.
func loopFindTeam(data domain.Bootstrap, ref string) (domain.Team, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return domain.Team{}, false
	}
	for _, team := range data.Teams {
		if team.ID == ref || strings.EqualFold(team.Key, ref) || strings.EqualFold(team.Name, ref) {
			return team, true
		}
	}
	return domain.Team{}, false
}

// resolveLoopContract is the contract a run works to: a copy with title
// templates rendered for the run's period, teams found by name (a team the
// text names only generically, "the team", is the loop's, and so are a team
// loop's new documents), and existing documents named by title found by id.
func resolveLoopContract(data domain.Bootstrap, documents []domain.Document, loop domain.Loop, contract *domain.LoopOutputContract, trigger loopTrigger, now time.Time) *domain.LoopOutputContract {
	if contract == nil {
		return nil
	}
	resolved := *contract
	resolved.Outputs = slices.Clone(contract.Outputs)
	resolved.SkipConditions = slices.Clone(contract.SkipConditions)
	at := loopRunPeriod(loop, trigger, now)
	for index := range resolved.Outputs {
		output := &resolved.Outputs[index]
		output.Conditions = slices.Clone(output.Conditions)
		target := &output.Target
		if target.TeamID != "" {
			if _, ok := loopFindTeam(data, target.TeamID); !ok {
				target.TeamID = ""
			}
		}
		if target.TeamID == "" && target.Team != "" {
			if team, ok := loopFindTeam(data, target.Team); ok {
				target.TeamID = team.ID
			} else if loop.TeamID != "" {
				target.TeamID = loop.TeamID
			}
		}
		// A named project or initiative is found by id, name or slug; one
		// that is not there limits nothing.
		if target.ProjectID != "" || target.Project != "" {
			target.ProjectID = ""
			if project, err := mcpFindProject(data, firstNonEmpty(contract.Outputs[index].Target.ProjectID, target.Project)); err == nil {
				target.ProjectID, target.Project = project.ID, project.Name
			}
		}
		if target.InitiativeID != "" || target.Initiative != "" {
			target.InitiativeID = ""
			if initiative, err := mcpFindInitiative(data, firstNonEmpty(contract.Outputs[index].Target.InitiativeID, target.Initiative)); err == nil {
				target.InitiativeID, target.Initiative = initiative.ID, initiative.Name
			}
		}
		target.Placeholders = slices.Clone(target.Placeholders)
		if output.Kind != "document" && output.Kind != "issue" {
			continue
		}
		if output.Kind == "document" && target.TeamID == "" && target.Team == "" && output.Operation == loopOpCreate && loop.TeamID != "" {
			// A team loop's new documents are its team's unless the text names another.
			target.TeamID = loop.TeamID
		}
		if target.TitleTemplate != "" {
			team, project := "", target.Project
			if found, ok := loopFindTeam(data, firstNonEmpty(target.TeamID, loop.TeamID)); ok {
				team = found.Name
			}
			if project == "" && trigger.EntityType == "project" {
				if found, err := mcpFindProject(data, trigger.EntityID); err == nil {
					project = found.Name
				}
			}
			target.RenderedTitle = renderLoopTitleNames(renderLoopTitle(target.TitleTemplate, at, trigger.Occurrence), team, project)
		}
		if output.Kind != "document" {
			continue
		}
		if target.DocumentID == "" && target.Title != "" && target.Mode != "new" {
			matches := []string{}
			for _, document := range documents {
				if document.ArchivedAt == nil && (strings.EqualFold(strings.TrimSpace(document.Title), target.Title) || target.TitleTemplate == "" && loopTitlePlaceholders.MatchString(target.Title) && strings.EqualFold(document.Title, renderLoopTitle(target.Title, at, trigger.Occurrence))) {
					matches = append(matches, document.ID)
				}
			}
			if len(matches) == 1 {
				target.DocumentID = matches[0]
			}
		}
	}
	return &resolved
}

// loopContractOutputs are a contract's outputs of a kind.
func loopContractOutputs(contract *domain.LoopOutputContract, kind string) []domain.LoopContractOutput {
	if contract == nil {
		return nil
	}
	outputs := []domain.LoopContractOutput{}
	for _, output := range contract.Outputs {
		if output.Kind == kind {
			outputs = append(outputs, output)
		}
	}
	return outputs
}

// loopContractRequiredKinds are the kinds a run must produce (the run's
// expectedOutputs).
func loopContractRequiredKinds(contract *domain.LoopOutputContract) []string {
	kinds := []string{}
	if contract == nil || contract.ReadOnly {
		return kinds
	}
	for _, kind := range loopOutputKinds {
		if slices.ContainsFunc(contract.Outputs, func(output domain.LoopContractOutput) bool {
			return output.Kind == kind && output.Required && loopOpWrites(output.Operation)
		}) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// loopContractWrites reports whether a contract lets the run make any change.
func loopContractWrites(contract *domain.LoopOutputContract) bool {
	return contract != nil && !contract.ReadOnly && slices.ContainsFunc(contract.Outputs, func(output domain.LoopContractOutput) bool { return loopOpWrites(output.Operation) })
}

// loopContractForbids reports whether the contract forbids a kind and no
// other output of it writes.
func loopContractForbids(contract *domain.LoopOutputContract, kind string) bool {
	outputs := loopContractOutputs(contract, kind)
	return slices.ContainsFunc(outputs, func(output domain.LoopContractOutput) bool { return output.Operation == loopOpForbidden }) &&
		!slices.ContainsFunc(outputs, func(output domain.LoopContractOutput) bool { return loopOpWrites(output.Operation) })
}

// loopCallOutput is the kind and operation of a write tool call: a save
// without an id creates, with one it updates; document tools that take an
// id update that document; any other write is a change.
func loopCallOutput(name string, args map[string]any) (string, string) {
	name = strings.TrimPrefix(name, "mcp__flow.")
	op := loopOpCreate
	if loopStringArg(args, "id") != "" {
		op = loopOpUpdate
	}
	switch name {
	case "save_status_update":
		return "statusUpdate", op
	case "save_issue":
		return "issue", op
	case "triage_issue":
		return "issue", loopOpUpdate
	case "save_comment":
		return "comment", op
	case "save_document":
		return "document", op
	}
	if loopDocumentTool(name) {
		return "document", loopOpUpdate
	}
	return "change", loopOpUpdate
}

// loopKindNouns name the kinds in refusals and prompts.
var loopKindNouns = map[string]string{
	"statusUpdate": "status updates",
	"issue":        "issues",
	"comment":      "comments",
	"document":     "documents",
	"change":       "other changes in Flow",
}

// loopContractCallRefusal checks a write call that is not a save_document
// against the run's contract (save_document is checked by
// prepareLoopDocumentWrite, which reads the document). Issue, comment and
// status update writes are checked against their outputs' targets
// (checkLoopOutputCall), which also gives their record; a forbidden kind is
// refused; other changes are allowed unless the contract forbids them.
func (s *server) loopContractCallRefusal(ctx context.Context, rec *loopRunRecorder, data domain.Bootstrap, contract *domain.LoopOutputContract, call domain.AgentToolCall) (*loopOutputCall, error) {
	if contract == nil || rec.answering {
		return nil, nil
	}
	args := loopToolArgs(call)
	kind, _ := loopCallOutput(call.Name, args)
	switch kind {
	case "document":
		if strings.TrimPrefix(call.Name, "mcp__flow.") == "save_document" {
			return nil, nil
		}
		document, found := s.loopFindDocument(rec.workspace, loopStringArg(args, "id"))
		if !found {
			return nil, nil
		}
		_, err := loopContractDocumentTarget(rec, contract, document)
		return nil, err
	case "issue", "comment", "statusUpdate":
		return s.checkLoopOutputCall(ctx, rec, data, contract, call, kind)
	}
	if loopContractForbids(contract, "change") {
		return nil, fmt.Errorf("%s make other changes in Flow; the call was not made", loopProhibitedCallPrefix)
	}
	return nil, nil
}

// loopContractRefusalPrefix starts the error of a call outside the contract.
const loopContractRefusalPrefix = "Not in this loop's outputs"

// loopDocumentCreatedInRun reports whether this run (or its replies) created
// the document.
func loopDocumentCreatedInRun(rec *loopRunRecorder, id string) bool {
	return slices.ContainsFunc(loopPriorDocumentWrites(rec), func(call domain.LoopRunToolCall) bool {
		return call.Status == "completed" && call.Document.Mode == loopOpCreate && call.Document.ID == id
	})
}

// loopContractTargetMatches reports whether a document is an existing-
// document output's target: by id, else by title; a target the instructions
// name only in words ("the team doc") is none until a person picks it, and
// one a person left as "any existing document" is any document in its team.
func loopContractTargetMatches(output domain.LoopContractOutput, document domain.Document) bool {
	target := output.Target
	switch {
	case target.DocumentID != "":
		return target.DocumentID == document.ID
	case target.Title != "":
		return strings.EqualFold(strings.TrimSpace(document.Title), target.Title) || target.RenderedTitle != "" && strings.EqualFold(document.Title, target.RenderedTitle)
	case target.Mode == "new" || target.Ambiguous:
		return false
	case target.TeamID != "":
		return len(document.TeamIDs) == 0 || slices.Contains(document.TeamIDs, target.TeamID)
	}
	return true
}

// loopContractDocumentTarget finds the output a change to an existing
// document is for, or the refusal: a document this run created may be
// changed; otherwise an update, append or write output must target it. A
// loop that creates a new document each run changes no other document.
func loopContractDocumentTarget(rec *loopRunRecorder, contract *domain.LoopOutputContract, document domain.Document) (*domain.LoopContractOutput, error) {
	outputs := loopContractOutputs(contract, "document")
	if loopDocumentCreatedInRun(rec, document.ID) {
		for index := range outputs {
			if outputs[index].Operation == loopOpCreate || outputs[index].Operation == loopOpWrite {
				return &outputs[index], nil
			}
		}
		return nil, nil
	}
	for index, output := range outputs {
		if (output.Operation == loopOpUpdate || output.Operation == loopOpAppend || output.Operation == loopOpWrite) && loopContractTargetMatches(output, document) {
			return &outputs[index], nil
		}
	}
	if unchosen := loopContractUnchosen(contract); unchosen != nil {
		return nil, loopChooseTargetError(*unchosen)
	}
	changes := slices.ContainsFunc(outputs, func(output domain.LoopContractOutput) bool {
		return output.Operation == loopOpUpdate || output.Operation == loopOpAppend || output.Operation == loopOpWrite
	})
	for _, output := range outputs {
		if output.Operation == loopOpCreate && output.Target.Mode == "new" && !changes {
			title := firstNonEmpty(output.Target.RenderedTitle, output.Target.TitleTemplate)
			named := ""
			if title != "" {
				named = fmt.Sprintf(" titled %q", title)
			}
			return nil, fmt.Errorf("%s: this loop creates a new document%s for each run and does not change existing documents such as %q, so the call was not made. Create the new document with save_document (title and content, no id) and put the complete content in it", loopContractRefusalPrefix, named, document.Title)
		}
	}
	if loopContractForbids(contract, "document") {
		return nil, fmt.Errorf("%s %s; the call was not made", loopProhibitedCallPrefix, loopProhibitedLabels["document"])
	}
	targets := []string{}
	for _, output := range outputs {
		if output.Operation == loopOpUpdate || output.Operation == loopOpAppend || output.Operation == loopOpWrite {
			targets = append(targets, loopContractDocumentName(output))
		}
	}
	if len(targets) > 0 {
		return nil, fmt.Errorf("%s: this loop writes to %s, not to %q, so the call was not made", loopContractRefusalPrefix, strings.Join(targets, " or "), document.Title)
	}
	return nil, fmt.Errorf("%s: this loop's outputs do not include changing documents, so the call was not made", loopContractRefusalPrefix)
}

// loopChooseTargetError refuses a write to an existing document while the
// output's document is not chosen.
func loopChooseTargetError(output domain.LoopContractOutput) error {
	described := ""
	if text := firstNonEmpty(output.Target.Description, output.Evidence); text != "" {
		described = fmt.Sprintf(" (%q)", text)
	}
	return fmt.Errorf("%s: this loop's instructions refer to an existing document%s without saying which one, and nobody has picked it in the loop's outputs yet, so the run writes to no existing document and the call was not made. Read what you need, put the content in finish_run's summary, and say that the target document must be chosen", loopChooseTargetPrefix, described)
}

// loopContractDocumentName names an output's document for messages.
func loopContractDocumentName(output domain.LoopContractOutput) string {
	target := output.Target
	switch {
	case target.Title != "" && target.DocumentID != "":
		return fmt.Sprintf("the document %q (id %s)", target.Title, target.DocumentID)
	case target.Title != "":
		return fmt.Sprintf("the document %q", target.Title)
	case target.DocumentID != "":
		return "the document with id " + target.DocumentID
	case target.Description != "":
		return target.Description
	}
	return "an existing document"
}

// loopContractCreate checks a new document against the contract and returns
// the output it is for with the arguments to create it with: the title the
// template gives this run and the output's team. A create the contract does
// not allow, a second document of a one-per-run output, or a document in
// another team is refused.
func loopContractCreate(rec *loopRunRecorder, data domain.Bootstrap, contract *domain.LoopOutputContract, args map[string]any) (*domain.LoopContractOutput, map[string]any, error) {
	outputs := loopContractOutputs(contract, "document")
	creates := []domain.LoopContractOutput{}
	for _, output := range outputs {
		if output.Operation == loopOpCreate || output.Operation == loopOpWrite {
			creates = append(creates, output)
		}
	}
	if len(creates) == 0 {
		switch {
		case loopContractUnchosen(contract) != nil:
			return nil, nil, loopChooseTargetError(*loopContractUnchosen(contract))
		case slices.ContainsFunc(outputs, func(output domain.LoopContractOutput) bool {
			return output.Operation == loopOpUpdate || output.Operation == loopOpAppend
		}):
			return nil, nil, fmt.Errorf("This loop writes to an existing document (%s): find it with search_documents or list_documents and call save_document with its id instead of creating a new document", loopContractDocumentName(outputs[slices.IndexFunc(outputs, func(output domain.LoopContractOutput) bool {
				return output.Operation == loopOpUpdate || output.Operation == loopOpAppend
			})]))
		case loopContractForbids(contract, "document"):
			return nil, nil, fmt.Errorf("%s %s; the call was not made", loopProhibitedCallPrefix, loopProhibitedLabels["document"])
		}
		return nil, nil, fmt.Errorf("%s: this loop's outputs do not include creating documents, so the call was not made", loopContractRefusalPrefix)
	}
	created := 0
	for _, call := range loopPriorDocumentWrites(rec) {
		if call.Status == "completed" && call.Document.Mode == loopOpCreate && call.Document.Changed {
			created++
		}
	}
	output := creates[min(created, len(creates)-1)]
	if created >= len(creates) && !slices.ContainsFunc(creates, func(item domain.LoopContractOutput) bool { return item.Cardinality != "onePerRun" }) {
		return nil, nil, fmt.Errorf("%s: this loop creates one document per run and this run already created it; change that document with save_document and its id instead of creating another", loopContractRefusalPrefix)
	}
	if index := slices.IndexFunc(creates, func(item domain.LoopContractOutput) bool { return item.Cardinality != "onePerRun" }); created >= len(creates) && index >= 0 {
		output = creates[index]
	}
	next := map[string]any{}
	for key, value := range args {
		next[key] = value
	}
	if rendered := output.Target.RenderedTitle; rendered != "" {
		// The contract names the title: the run's period goes in, whatever the
		// model wrote, and the run fills only the placeholders it owns.
		title, err := loopFillTitle(rendered, output.Target, args, "document")
		if err != nil {
			return nil, nil, err
		}
		next["title"] = title
	}
	delete(next, loopTitleFieldsArg)
	if teamID := output.Target.TeamID; teamID != "" {
		team, _ := loopFindTeam(data, teamID)
		given := loopStringArg(args, "team", "teamId")
		if given == "" {
			next["team"] = teamID
		} else if found, ok := loopFindTeam(data, given); !ok || found.ID != teamID {
			return nil, nil, fmt.Errorf("%s: this loop creates its document in the %s team (team %q), not in %q; the call was not made", loopContractRefusalPrefix, team.Name, team.Key, given)
		}
	}
	return &output, next, nil
}

// loopContractOutputDone reports whether a run produced a required output
// (the contract's index-th), as its tool records and the stored documents
// show it: an issue, comment or status update write on the output's target,
// a document write that changed the document. A refused or failed call
// never counts.
func loopContractOutputDone(run *domain.LoopRun, index int, output domain.LoopContractOutput) bool {
	if output.Kind == "change" {
		return run.Produced["change"] > 0
	}
	if output.Kind != "document" {
		return slices.ContainsFunc(run.ToolCalls, func(call domain.LoopRunToolCall) bool {
			// A legacy "issue" output is a new issue.
			return call.Status == "completed" && call.Output != nil && slices.Contains(call.Output.Outputs, index) && !(output.Kind == "issue" && output.Operation == loopOpWrite && call.Output.Operation != loopOpCreate)
		})
	}
	for _, call := range run.ToolCalls {
		record := call.Document
		if record == nil {
			continue
		}
		if record.ExistingRunID != "" && output.Operation != loopOpCreate {
			// An earlier run already appended this occurrence's section.
			return true
		}
		if call.Status != "completed" || !record.Changed {
			continue
		}
		switch output.Operation {
		case loopOpCreate:
			if record.Mode == loopOpCreate && (output.Target.RenderedTitle == "" || loopTitleMatches(output.Target.RenderedTitle, record.Title)) && (output.Target.TeamID == "" || slices.Contains(record.TeamIDs, output.Target.TeamID)) {
				return true
			}
		case loopOpUpdate, loopOpAppend:
			if record.Mode != loopOpCreate && (output.Target.DocumentID == "" || output.Target.DocumentID == record.ID) {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// loopContractOutputLabel names a missing output.
func loopContractOutputLabel(output domain.LoopContractOutput) string {
	if output.Kind != "document" {
		label := loopOutputLabels[output.Kind]
		if output.Operation == loopOpUpdate && output.Kind != "change" {
			label = "an update to " + strings.TrimSuffix(loopKindNouns[output.Kind], "s")
		}
		if output.Kind != "change" && loopTargetConstrained(output.Kind, output.Target) {
			if where := loopTargetName(domain.Bootstrap{}, output.Kind, output.Target, "", ""); where != "" {
				joiner := " on "
				if strings.HasPrefix(where, "in ") {
					joiner = " "
				}
				label += joiner + where
			}
		}
		return label
	}
	switch output.Operation {
	case loopOpCreate:
		label := "a new document"
		if title := firstNonEmpty(output.Target.RenderedTitle, output.Target.TitleTemplate); title != "" {
			label += fmt.Sprintf(" titled %q", title)
		}
		return label
	case loopOpAppend:
		if output.Target.Title != "" {
			return fmt.Sprintf("an append to the document %q", output.Target.Title)
		}
		return "an append to an existing document"
	case loopOpUpdate:
		if output.Target.Title != "" {
			return fmt.Sprintf("an update to the document %q", output.Target.Title)
		}
		return "an update to an existing document"
	}
	return loopOutputLabels["document"]
}

// loopContractMissing lists the required outputs a run did not produce; a
// group of alternatives needs one of them.
func loopContractMissing(run *domain.LoopRun, contract *domain.LoopOutputContract) []domain.LoopContractOutput {
	missing := []domain.LoopContractOutput{}
	if contract == nil || contract.ReadOnly {
		return missing
	}
	for index, output := range contract.Outputs {
		if !output.Required || !loopOpWrites(output.Operation) || loopContractOutputDone(run, index, output) {
			continue
		}
		alternative := false
		for other, item := range contract.Outputs {
			alternative = alternative || output.Group != "" && item.Group == output.Group && loopOpWrites(item.Operation) && loopContractOutputDone(run, other, item)
		}
		if alternative {
			continue
		}
		missing = append(missing, output)
	}
	return missing
}

// loopDocumentPrompts ask for each document operation.
var loopDocumentPrompts = map[string]string{
	loopOpAppend: `append to the existing document: find it (search_documents, list_documents or get_document), then call save_document once with its id and patch [{"op":"append","text":"..."}]. Never pass content (it replaces the whole body) and do not create a new document; the existing content must stay as it is`,
	loopOpUpdate: "update the existing document: find it, then call save_document with its id (patch to change part of it); do not create a new document instead",
	loopOpCreate: "create a new document with save_document (title and content, no id)",
	loopOpWrite:  "create or update the document(s) the instructions ask for with save_document",
}

var loopOutputPrompts = map[string]string{
	"statusUpdate": "post the status update(s) the instructions ask for with save_status_update",
	"issue":        "create the issue(s) the instructions ask for with save_issue",
	"comment":      "post the comment(s) the instructions ask for with save_comment",
	"document":     "create or update the document(s) the instructions ask for with save_document",
	"change":       "make the change(s) in Flow the instructions ask for",
}

// loopUpdatePrompts ask for changes to existing items.
var loopUpdatePrompts = map[string]string{
	"statusUpdate": "edit the status update(s) the instructions name with save_status_update and their id",
	"issue":        "change the issue(s) the instructions name (status, priority, assignee, labels, triage) with save_issue and their id",
	"comment":      "edit the comment(s) the instructions name with save_comment and their id",
}

// loopContractOutputPrompt is one output's line in the run prompt.
func loopContractOutputPrompt(output domain.LoopContractOutput) string {
	var line strings.Builder
	switch {
	case output.Kind == "document":
		text := loopDocumentPrompts[output.Operation]
		if output.Operation == loopOpAppend || output.Operation == loopOpUpdate {
			text = strings.Replace(text, "the existing document", loopContractDocumentName(output), 1)
		}
		line.WriteString(text)
		target := output.Target
		if output.Operation == loopOpCreate || output.Operation == loopOpWrite {
			loopTitlePrompt(&line, target, "save_document")
		}
		if target.TeamID != "" || target.Team != "" {
			fmt.Fprintf(&line, ", in the team %s", firstNonEmpty(target.Team, target.TeamID))
		}
		if target.DocumentID != "" && (output.Operation == loopOpAppend || output.Operation == loopOpUpdate) {
			fmt.Fprintf(&line, " (its id is %s)", target.DocumentID)
		}
		if output.Operation == loopOpCreate && target.Mode == "new" {
			line.WriteString("; make a new one for this run and do not write to any other existing document")
		}
	case output.Operation == loopOpUpdate && loopUpdatePrompts[output.Kind] != "":
		line.WriteString(loopUpdatePrompts[output.Kind])
		loopTargetPrompt(&line, output)
	default:
		line.WriteString(loopOutputPrompts[output.Kind])
		if output.Kind == "issue" && (output.Operation == loopOpCreate || output.Operation == loopOpWrite) {
			loopTitlePrompt(&line, output.Target, "save_issue")
		}
		loopTargetPrompt(&line, output)
	}
	switch output.Cardinality {
	case "onePerRun":
		line.WriteString("; exactly one per run")
	case "perItem":
		line.WriteString("; one for each item the instructions name")
	}
	if len(output.Conditions) > 0 {
		fmt.Fprintf(&line, " (when: %s)", strings.Join(output.Conditions, "; "))
	}
	return line.String()
}

// loopTitlePrompt asks for a new document's or issue's title: exactly the
// rendered template, with titleFields for the placeholders the run fills.
func loopTitlePrompt(line *strings.Builder, target domain.LoopContractTarget, tool string) {
	title := firstNonEmpty(target.RenderedTitle, target.TitleTemplate)
	if title == "" {
		return
	}
	fmt.Fprintf(line, ", titled exactly %q", title)
	if names := loopModelPlaceholders(title); len(names) > 0 {
		fmt.Fprintf(line, " (pass %s %s to %s for the {…} parts; Flow builds the title)", loopTitleFieldsArg, loopTitleFieldsHint(target, names), tool)
	}
}

// loopTargetPrompt says where an issue, comment or status update output
// goes; writes elsewhere are refused.
func loopTargetPrompt(line *strings.Builder, output domain.LoopContractOutput) {
	if output.Kind == "change" || !loopTargetConstrained(output.Kind, output.Target) {
		return
	}
	if where := loopTargetName(domain.Bootstrap{}, output.Kind, output.Target, "", ""); where != "" {
		target := output.Target
		ids := []string{}
		for _, id := range []string{target.ProjectID, target.InitiativeID} {
			if id != "" {
				ids = append(ids, id)
			}
		}
		if output.Kind == "issue" && target.TeamID != "" {
			ids = append(ids, "team "+target.TeamID)
		}
		suffix := ""
		if len(ids) > 0 {
			suffix = " (" + strings.Join(ids, ", ") + ")"
		}
		joiner := " on "
		if strings.HasPrefix(where, "in ") {
			joiner = " "
		}
		fmt.Fprintf(line, ",%s%s%s only; calls on anything else are refused", joiner, where, suffix)
	}
}

// loopContractPrompt tells the run's model what its contract asks for and
// allows: required and optional outputs, refusals, when nothing changed
// counts and when the run may skip.
func loopContractPrompt(prompt *strings.Builder, contract *domain.LoopOutputContract) {
	if contract == nil {
		return
	}
	if contract.ReadOnly {
		prompt.WriteString("\nThis loop reads and reports: its reply is its only output. Do not post status updates or create issues, comments or documents, and do not look for a place to publish. Put the complete report in finish_run's summary (status done, or nothing_to_do when there is nothing new, worded the way the instructions ask).\n")
		prompt.WriteString("The instructions forbid every change: do not create, change or delete anything in Flow or through a connector tool (such calls are refused; only connector tools their connector marks read-only run). The text reply in finish_run's summary is the run's only output.\n")
	} else {
		required, optional := []string{}, []string{}
		unchosen := loopContractUnchosen(contract)
		if unchosen != nil {
			fmt.Fprintf(prompt, "\nThe instructions ask to write to an existing document (%s) without saying which one, and it has not been chosen in the loop's outputs yet: do not write to any existing document (such calls are refused). Read what you need, put the content in finish_run's summary, and say that the target document must be chosen.\n", firstNonEmpty(unchosen.Target.Description, unchosen.Evidence, "the document"))
		}
		for _, output := range contract.Outputs {
			if !loopOpWrites(output.Operation) || output.Kind == "document" && output.Target.Ambiguous && output.Target.DocumentID == "" && output.Operation != loopOpCreate {
				continue
			}
			if output.Required {
				required = append(required, loopContractOutputPrompt(output))
			} else {
				optional = append(optional, loopContractOutputPrompt(output))
			}
		}
		if len(required) > 0 {
			fmt.Fprintf(prompt, "\nThis loop is expected to produce output: %s. A run that ends without it is flagged for review, so only report nothing_to_do when the instructions truly do not apply.\n", strings.Join(required, "; "))
		} else if len(optional) == 0 {
			prompt.WriteString("\nThis loop reads and reports: its instructions mention status updates, issues, comments or documents only as things to check, read or summarize. Put the complete report in finish_run's summary.\n")
		}
		if len(optional) > 0 {
			fmt.Fprintf(prompt, "It may also, only when the instructions' conditions apply: %s.\n", strings.Join(optional, "; "))
		}
		groups := map[string][]string{}
		order := []string{}
		for _, output := range contract.Outputs {
			if output.Group != "" && loopOpWrites(output.Operation) {
				if _, ok := groups[output.Group]; !ok {
					order = append(order, output.Group)
				}
				groups[output.Group] = append(groups[output.Group], loopOutputLabels[output.Kind])
			}
		}
		for _, group := range order {
			if len(groups[group]) > 1 {
				fmt.Fprintf(prompt, "The instructions offer a choice: one of %s is enough.\n", strings.Join(groups[group], " or "))
			}
		}
		refused := []string{}
		for _, kind := range loopOutputKinds {
			if kind != "change" && loopContractForbids(contract, kind) {
				refused = append(refused, loopProhibitedLabels[kind])
			}
		}
		if len(refused) > 0 {
			fmt.Fprintf(prompt, "Do not %s: the instructions forbid it, and such calls are refused.\n", strings.Join(refused, " or "))
		}
		uncreated := []string{}
		for _, kind := range []string{"statusUpdate", "issue", "comment", "document"} {
			if !loopContractForbids(contract, kind) && !slices.ContainsFunc(loopContractOutputs(contract, kind), func(output domain.LoopContractOutput) bool {
				return output.Operation == loopOpCreate || output.Operation == loopOpWrite
			}) {
				uncreated = append(uncreated, loopKindNouns[kind])
			}
		}
		if len(uncreated) > 0 {
			fmt.Fprintf(prompt, "Do not create %s: they are not among this loop's outputs, and such calls are refused.\n", strings.Join(uncreated, ", "))
		}
		prompt.WriteString("If a required output could not be made, finish_run with status incomplete and say why in notDone.\n")
		if contract.Always {
			prompt.WriteString("The output is required even when nothing changed: write it anyway (saying there were no changes) instead of reporting nothing_to_do.\n")
		}
	}
	if len(contract.SkipConditions) > 0 {
		conditions := []string{}
		for _, skip := range contract.SkipConditions {
			conditions = append(conditions, firstNonEmpty(skip.Evidence, skip.Condition))
		}
		fmt.Fprintf(prompt, "The instructions say when a run is skipped: %s. When such a condition applies today, change nothing and call finish_run with status skipped, skipReason naming the condition, and a summary saying why. Do not skip for any other reason.\n", strings.Join(conditions, " / "))
	}
}
