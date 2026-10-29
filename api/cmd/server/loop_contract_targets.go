package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Output contract targets of issues, comments and status updates, and the
// title templates a run fills. A contract output says where its writes go:
// the triggering item, issues in a set the instructions describe, a named
// project or initiative, a team. A run's issue, comment and status update
// writes are checked against those targets before they run (a write to
// another target is refused with a tool error), recorded with the outputs
// they are on target for, and only those count as produced. A new document's
// or issue's title template has placeholders the runtime fills (the period,
// the team and project names) and placeholders the run's model fills
// through save_document's or save_issue's titleFields; the final title must
// fit the template.

// loopRuntimePlaceholders are the title placeholders the runtime fills.
var loopRuntimePlaceholders = []string{"date", "week", "month", "year", "day", "team", "project"}

// loopTitleFieldsArg is the structured field of save_document and save_issue
// (loop runs only) that carries the run's values for model placeholders.
const loopTitleFieldsArg = "titleFields"

// loopChooseTargetPrefix starts the refusal of a write to an existing
// document the instructions do not name.
const loopChooseTargetPrefix = "Choose the target document"

// loopChooseTargetNotice is the outcome of a run whose document output has
// no chosen target; the web app translates it.
const loopChooseTargetNotice = "Choose the target document: the instructions name an existing document without saying which one, so the run wrote to no existing document. Pick it under \"What this loop will produce\"."

// loopModelPlaceholders are a title template's placeholders the runtime does
// not fill ("{topic}" gives "topic"), in order, once each.
func loopModelPlaceholders(template string) []string {
	names := []string{}
	for _, match := range loopUnknownPlaceholder.FindAllString(template, -1) {
		name := strings.TrimSpace(match[1 : len(match)-1])
		if name != "" && !slices.Contains(loopRuntimePlaceholders, strings.ToLower(name)) && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// normalizeLoopContractTarget fits a target to its kind and operation: the
// fields that kind uses, its modes, the ambiguity of an unnamed existing
// document, and the placeholders of a title template (every one the runtime
// does not fill must be declared with what the run fills it from). It
// returns the problem, if any.
func normalizeLoopContractTarget(kind, op string, target *domain.LoopContractTarget, clip func(string, int) string) string {
	placeholders := []domain.LoopContractPlaceholder{}
	for _, item := range target.Placeholders {
		item.Name, item.Source = strings.Trim(strings.TrimSpace(item.Name), "{} "), clip(item.Source, 200)
		if item.Name == "" || utf8.RuneCountInString(item.Name) > 40 || slices.Contains(loopRuntimePlaceholders, strings.ToLower(item.Name)) || len(placeholders) >= 8 || slices.ContainsFunc(placeholders, func(other domain.LoopContractPlaceholder) bool { return strings.EqualFold(other.Name, item.Name) }) {
			continue
		}
		placeholders = append(placeholders, item)
	}
	target.Placeholders = placeholders
	titled := (kind == "document" || kind == "issue") && (op == loopOpCreate || op == loopOpWrite)
	switch kind {
	case "document":
		target.Project, target.ProjectID, target.Initiative, target.InitiativeID, target.Match = "", "", "", "", ""
		if target.Mode == "matching" {
			target.Mode = "existing"
		}
	case "issue":
		target.Title, target.DocumentID, target.Initiative, target.InitiativeID = "", "", "", ""
		if op == loopOpCreate {
			// A new issue is new; where it goes is its team and project.
			target.Mode = "new"
		}
		if op == loopOpCreate {
			target.Match = ""
		}
	case "comment":
		target.Title, target.DocumentID = "", ""
		if target.Mode == "new" {
			target.Mode = "any"
		}
	case "statusUpdate":
		target.Title, target.DocumentID, target.Match = "", "", ""
		if target.Mode == "new" || target.Mode == "matching" {
			target.Mode = "any"
		}
		if target.ProjectID != "" || target.Project != "" {
			// A status update is on one project or one initiative.
			target.Initiative, target.InitiativeID = "", ""
		}
	default:
		target.Title, target.DocumentID, target.Project, target.ProjectID, target.Initiative, target.InitiativeID, target.Match = "", "", "", "", "", "", ""
	}
	if target.Mode != "matching" {
		target.Match = ""
	}
	if !titled {
		target.TitleTemplate, target.Placeholders, target.RenderedTitle = "", nil, ""
	}
	target.Ambiguous = kind == "document" && target.Ambiguous && target.Title == "" && target.DocumentID == "" && (op == loopOpUpdate || op == loopOpAppend || op == loopOpWrite)
	if target.Ambiguous {
		target.Mode = "existing"
	}
	if titled && target.TitleTemplate != "" {
		names := loopModelPlaceholders(target.TitleTemplate)
		target.Placeholders = slices.DeleteFunc(target.Placeholders, func(item domain.LoopContractPlaceholder) bool {
			return !slices.ContainsFunc(names, func(name string) bool { return strings.EqualFold(name, item.Name) })
		})
		missing := []string{}
		for _, name := range names {
			if !slices.ContainsFunc(target.Placeholders, func(item domain.LoopContractPlaceholder) bool { return strings.EqualFold(item.Name, name) }) {
				missing = append(missing, "{"+name+"}")
			}
		}
		if len(missing) > 0 {
			return fmt.Sprintf("target.titleTemplate uses %s, which the runtime cannot fill: list each in target.placeholders with what the run fills it from, or write the title without it", strings.Join(missing, ", "))
		}
	}
	if len(target.Placeholders) == 0 {
		target.Placeholders = nil
	}
	return ""
}

// loopPlaceholderSource is what fills a model placeholder, for prompts and
// refusals.
func loopPlaceholderSource(target domain.LoopContractTarget, name string) string {
	for _, item := range target.Placeholders {
		if strings.EqualFold(item.Name, name) && item.Source != "" {
			return item.Source
		}
	}
	return "what the instructions say"
}

// loopTitleFieldsHint names the titleFields a template needs: {"topic": "<the error's subject>"}.
func loopTitleFieldsHint(target domain.LoopContractTarget, names []string) string {
	parts := []string{}
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%q: \"<%s>\"", name, loopPlaceholderSource(target, name)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// loopFillTitle is a new document's or issue's final title: the run's
// rendered template with the model's placeholders filled from titleFields.
// A title the model wrote that fits the template is accepted too. The final
// title is never empty and keeps no braces; otherwise the call is refused.
func loopFillTitle(rendered string, target domain.LoopContractTarget, args map[string]any, noun string) (string, error) {
	names := loopModelPlaceholders(rendered)
	title := rendered
	fields, _ := args[loopTitleFieldsArg].(map[string]any)
	missing := []string{}
	for _, name := range names {
		value := ""
		for key, raw := range fields {
			if text, ok := raw.(string); ok && strings.EqualFold(strings.Trim(strings.TrimSpace(key), "{}"), name) {
				value = strings.Join(strings.Fields(text), " ")
			}
		}
		if value == "" || strings.ContainsAny(value, "{}") {
			missing = append(missing, name)
			continue
		}
		title = strings.ReplaceAll(title, "{"+name+"}", value)
	}
	if len(missing) > 0 {
		given := strings.TrimSpace(loopStringArg(args, "title"))
		if given != "" && !strings.ContainsAny(given, "{}") && loopTitleMatches(rendered, given) {
			return given, nil
		}
		return "", fmt.Errorf("This loop's new %s is titled %q: pass %s %s with the call (the rest of the title is filled by Flow), so the call was not made", noun, rendered, loopTitleFieldsArg, loopTitleFieldsHint(target, missing))
	}
	title = strings.TrimSpace(title)
	if title == "" || strings.ContainsAny(title, "{}") || !loopTitleMatches(rendered, title) {
		return "", fmt.Errorf("This loop's new %s is titled %q, and %q does not fit it (every {…} part filled, no braces left), so the call was not made", noun, rendered, title)
	}
	return title, nil
}

// renderLoopTitleNames fills a rendered title's {team} and {project}.
func renderLoopTitleNames(title, team, project string) string {
	for name, value := range map[string]string{"team": team, "project": project} {
		if value != "" {
			title = strings.NewReplacer("{"+name+"}", value, "{"+strings.ToUpper(name[:1])+name[1:]+"}", value).Replace(title)
		}
	}
	return title
}

// loopModelFilledTitles reports whether a contract's new documents or issues
// of a kind have placeholders the run fills (save_document or save_issue
// then offers titleFields).
func loopModelFilledTitles(contract *domain.LoopOutputContract, kind string) bool {
	return contract != nil && slices.ContainsFunc(contract.Outputs, func(output domain.LoopContractOutput) bool {
		return output.Kind == kind && (output.Operation == loopOpCreate || output.Operation == loopOpWrite) && len(loopModelPlaceholders(firstNonEmpty(output.Target.RenderedTitle, output.Target.TitleTemplate))) > 0
	})
}

// loopTitleFieldsTools adds titleFields to save_document and save_issue in a
// run whose contract has model-filled title placeholders for them.
func loopTitleFieldsTools(contract *domain.LoopOutputContract) func(agentProviderTool) agentProviderTool {
	kinds := map[string]string{"save_document": "document", "save_issue": "issue"}
	return func(tool agentProviderTool) agentProviderTool {
		kind, ok := kinds[strings.TrimPrefix(tool.Name, "mcp__flow.")]
		if !ok || !loopModelFilledTitles(contract, kind) {
			return tool
		}
		var schema map[string]any
		if json.Unmarshal(tool.Parameters, &schema) != nil {
			return tool
		}
		properties, _ := schema["properties"].(map[string]any)
		if properties == nil {
			properties = map[string]any{}
			schema["properties"] = properties
		}
		properties[loopTitleFieldsArg] = map[string]any{
			"type":                 "object",
			"additionalProperties": map[string]any{"type": "string"},
			"description":          "This loop's title placeholders, by name without braces (for example {\"topic\": \"Login failures\"}). Creating, Flow builds the title from the loop's title template with them.",
		}
		raw, err := json.Marshal(schema)
		if err != nil {
			return tool
		}
		tool.Parameters = raw
		return tool
	}
}

// loopWriteSubject is what an issue, comment or status update write acts
// on, as far as it can be found.
type loopWriteSubject struct {
	op string
	// itemID is the issue, comment or status update changed (empty for a
	// create).
	itemID string
	// parentType and parentID: what a comment or status update is on
	// (issue, project, initiative, document, milestone, statusUpdate), or
	// the issue a save_issue changes.
	parentType, parentID string
	// teamID and projectID: the issue's (changed, created, or the one a
	// comment is on).
	teamID, projectID string
	// found: the parent (or the item) exists.
	found bool
}

// loopFindIssue finds an issue by id or identifier in the store.
func (s *server) loopFindIssue(ctx context.Context, workspace, ref string) (domain.Issue, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return domain.Issue{}, false
	}
	issue, err := s.store.IssueRecord(ctx, workspace, ref)
	if err != nil {
		issue, err = s.issueByIdentifier(ctx, workspace, ref)
	}
	if err != nil {
		if key := agentIssueReferenceKey(ref); key != "" && key != ref {
			return s.loopFindIssue(ctx, workspace, key)
		}
	}
	return issue, err == nil
}

// loopSubjectParent classifies what a comment is on.
func (s *server) loopSubjectParent(ctx context.Context, workspace string, data domain.Bootstrap, ref string, subject *loopWriteSubject) {
	if ref == "" {
		return
	}
	if issue, ok := s.loopFindIssue(ctx, workspace, ref); ok {
		subject.parentType, subject.parentID, subject.teamID, subject.found = "issue", issue.ID, issue.Team.ID, true
		if issue.Project != nil {
			subject.projectID = issue.Project.ID
		}
		return
	}
	if project, err := mcpFindProject(data, ref); err == nil {
		subject.parentType, subject.parentID, subject.found = "project", project.ID, true
		return
	}
	if initiative, err := mcpFindInitiative(data, ref); err == nil {
		subject.parentType, subject.parentID, subject.found = "initiative", initiative.ID, true
		return
	}
	if document, err := mcpFindDocument(data, ref); err == nil {
		subject.parentType, subject.parentID, subject.found = "document", document.ID, true
		return
	}
	subject.parentType, subject.parentID, subject.found = "", ref, mcpCommentParentVisible(data, ref)
}

// loopWriteSubjectOf reads what a save_issue, triage_issue, save_comment or
// save_status_update call acts on.
func (s *server) loopWriteSubjectOf(ctx context.Context, workspace string, data domain.Bootstrap, name string, args map[string]any) loopWriteSubject {
	subject := loopWriteSubject{op: loopOpCreate}
	switch name {
	case "save_issue", "triage_issue":
		ref := loopStringArg(args, "id", "issueId", "issue")
		if ref == "" && name == "save_issue" {
			if team, err := mcpFindTeam(data, loopStringArg(args, "team", "teamId")); err == nil {
				subject.teamID = team.ID
			}
			if project, err := mcpFindProject(data, loopStringArg(args, "project")); err == nil {
				subject.projectID = project.ID
			}
			subject.found = true
			return subject
		}
		subject.op = loopOpUpdate
		if issue, ok := s.loopFindIssue(ctx, workspace, ref); ok {
			subject.itemID, subject.parentType, subject.parentID, subject.teamID, subject.found = issue.ID, "issue", issue.ID, issue.Team.ID, true
			if issue.Project != nil {
				subject.projectID = issue.Project.ID
			}
		}
	case "save_comment":
		id, reply := loopStringArg(args, "id"), loopStringArg(args, "parentId")
		switch {
		case id != "":
			if resource, _, err := s.store.CommentResource(ctx, workspace, id); err == nil {
				subject.op, subject.itemID = loopOpUpdate, id
				s.loopSubjectParent(ctx, workspace, data, resource, &subject)
			} else if mcpCommentVisible(data, id) {
				subject.op, subject.itemID, subject.found = loopOpUpdate, id, true
				for resource, comments := range data.Comments {
					if slices.ContainsFunc(comments, func(comment domain.Comment) bool { return comment.ID == id }) {
						s.loopSubjectParent(ctx, workspace, data, resource, &subject)
					}
				}
			} else {
				// An issue passed as the comment id posts a new comment on it.
				s.loopSubjectParent(ctx, workspace, data, id, &subject)
			}
		case reply != "":
			if resource, _, err := s.store.CommentResource(ctx, workspace, reply); err == nil {
				s.loopSubjectParent(ctx, workspace, data, resource, &subject)
			}
		default:
			s.loopSubjectParent(ctx, workspace, data, loopStringArg(args, "issueId", "projectId", "initiativeId", "documentId", "milestoneId", "statusUpdateId"), &subject)
		}
	case "save_status_update":
		if loopStringArg(args, "id") != "" {
			subject.op, subject.itemID = loopOpUpdate, loopStringArg(args, "id")
		}
		switch loopStringArg(args, "type") {
		case "project":
			if project, err := mcpFindProject(data, loopStringArg(args, "project")); err == nil {
				subject.parentType, subject.parentID, subject.found = "project", project.ID, true
			}
		case "initiative":
			if initiative, err := mcpFindInitiative(data, loopStringArg(args, "initiative")); err == nil {
				subject.parentType, subject.parentID, subject.found = "initiative", initiative.ID, true
			}
		}
	}
	return subject
}

// loopTargetHolds reports whether a write is on an output's target. The
// triggering item is the run's entity; a matching target is issues (in its
// team and project, when named); a named project or initiative is the one a
// comment or status update is on, or a new or changed issue is in; a team is
// a new or changed issue's.
func loopTargetHolds(kind string, target domain.LoopContractTarget, subject loopWriteSubject, entityType, entityID string) bool {
	createdIssue := kind == "issue" && subject.op == loopOpCreate
	switch target.Mode {
	case "triggering":
		if !createdIssue && (entityID == "" || subject.parentID != entityID || subject.parentType != "" && entityType != "" && subject.parentType != entityType) {
			return false
		}
	case "matching":
		if !createdIssue && subject.parentType != "issue" {
			return false
		}
	}
	onIssue := kind == "issue" || subject.parentType == "issue"
	if target.ProjectID != "" {
		if onIssue && (kind == "issue" || target.Mode == "matching") {
			if subject.projectID != target.ProjectID {
				return false
			}
		} else if subject.parentType != "project" || subject.parentID != target.ProjectID {
			return false
		}
	}
	if target.InitiativeID != "" && kind != "issue" && (subject.parentType != "initiative" || subject.parentID != target.InitiativeID) {
		return false
	}
	if target.TeamID != "" && onIssue && (kind == "issue" || target.Mode == "matching") && subject.teamID != target.TeamID {
		return false
	}
	return true
}

// loopTargetName says an output's target for prompts and refusals.
func loopTargetName(data domain.Bootstrap, kind string, target domain.LoopContractTarget, entityType, entityLabel string) string {
	parts := []string{}
	switch target.Mode {
	case "triggering":
		// (A new issue's target is always new: triggering is an update's or a comment's.)
		name := "the " + firstNonEmpty(entityType, "item") + " that triggered the run"
		if entityLabel != "" {
			name += " (" + entityLabel + ")"
		}
		parts = append(parts, name)
	case "matching":
		parts = append(parts, fmt.Sprintf("issues matching %q", firstNonEmpty(target.Match, target.Description)))
	}
	project := target.Project
	if target.ProjectID != "" {
		if found, err := mcpFindProject(data, target.ProjectID); err == nil {
			project = found.Name
		}
	}
	initiative := target.Initiative
	if target.InitiativeID != "" {
		if found, err := mcpFindInitiative(data, target.InitiativeID); err == nil {
			initiative = found.Name
		}
	}
	team := target.Team
	if target.TeamID != "" {
		if found, ok := loopFindTeam(data, target.TeamID); ok {
			team = found.Name
		}
	}
	scoped := kind == "issue" || target.Mode == "matching"
	switch {
	case project != "" && scoped:
		parts = append(parts, fmt.Sprintf("in the project %q", project))
	case project != "":
		parts = append(parts, fmt.Sprintf("the project %q", project))
	case initiative != "":
		parts = append(parts, fmt.Sprintf("the initiative %q", initiative))
	}
	if team != "" && scoped {
		parts = append(parts, fmt.Sprintf("in the team %q", team))
	}
	return strings.Join(parts, " ")
}

// loopTargetConstrained reports whether an output's target limits where its
// writes go.
func loopTargetConstrained(kind string, target domain.LoopContractTarget) bool {
	return target.Mode == "triggering" || target.Mode == "matching" || target.ProjectID != "" || target.InitiativeID != "" || target.TeamID != "" && kind == "issue"
}

// loopSubjectLabel names a write's subject in a refusal.
func loopSubjectLabel(subject loopWriteSubject, args map[string]any) string {
	switch {
	case subject.parentType != "" && subject.parentID != "":
		return fmt.Sprintf("the %s %s", subject.parentType, subject.parentID)
	case subject.teamID != "" || subject.projectID != "":
		return "an issue in team " + firstNonEmpty(subject.teamID, "?") + " and project " + firstNonEmpty(subject.projectID, "none")
	}
	return firstNonEmpty(loopStringArg(args, "issueId", "projectId", "initiativeId", "documentId", "id", "project", "initiative"), "that item")
}

// loopOutputCall is an issue, comment or status update call checked against
// the run's contract: its record, and the arguments it runs with when the
// contract sets them (a new issue's title, team and project).
type loopOutputCall struct {
	record    *domain.LoopRunOutputWrite
	arguments json.RawMessage
}

// loopContractIndexes are the indexes of a contract's outputs of a kind
// whose operation is one of ops.
func loopContractIndexes(contract *domain.LoopOutputContract, kind string, ops ...string) []int {
	indexes := []int{}
	for index, output := range contract.Outputs {
		if output.Kind == kind && slices.Contains(ops, output.Operation) {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// loopPriorOutputWrites are the issue, comment and status update writes the
// run recorded before this call.
func loopPriorOutputWrites(rec *loopRunRecorder) []domain.LoopRunToolCall {
	calls := slices.Clone(rec.run.ToolCalls)
	if rec.reply != nil {
		calls = append(calls, rec.reply.ToolCalls...)
	}
	return slices.DeleteFunc(calls, func(call domain.LoopRunToolCall) bool { return call.Output == nil })
}

// checkLoopOutputCall checks an issue, comment or status update write
// against the run's contract. A create needs an output of its kind that
// creates and whose target it is on (a new issue gets the output's team,
// project and title); an update must be on the target of one of the kind's
// update outputs, when it has any, or change an item this run made. Other
// writes of the kind are refused with the targets the loop writes to.
func (s *server) checkLoopOutputCall(ctx context.Context, rec *loopRunRecorder, data domain.Bootstrap, contract *domain.LoopOutputContract, call domain.AgentToolCall, kind string) (*loopOutputCall, error) {
	name := strings.TrimPrefix(call.Name, "mcp__flow.")
	args := loopToolArgs(call)
	entityType, entityID := rec.run.EntityType, rec.run.EntityID
	entityLabel := firstNonEmpty(rec.run.EntityIdentifier, entityID)
	subject := s.loopWriteSubjectOf(ctx, rec.workspace, data, name, args)
	record := &domain.LoopRunOutputWrite{Kind: kind, Operation: subject.op, ID: subject.itemID, ParentType: subject.parentType, ParentID: subject.parentID}
	prepared := &loopOutputCall{record: record}
	refuse := func(candidates []int) error {
		targets := []string{}
		for _, index := range candidates {
			if label := loopTargetName(data, kind, contract.Outputs[index].Target, entityType, entityLabel); label != "" && !slices.Contains(targets, label) {
				targets = append(targets, label)
			}
		}
		if entityID == "" && slices.ContainsFunc(candidates, func(index int) bool { return contract.Outputs[index].Target.Mode == "triggering" }) && len(targets) <= 1 {
			return fmt.Errorf("%s: this loop's %s go on the item that triggered the run, and this run has none, so the call was not made", loopContractRefusalPrefix, loopKindNouns[kind])
		}
		return fmt.Errorf("%s: this loop's %s go on %s, not on %s, so the call was not made. Make only the outputs the loop lists", loopContractRefusalPrefix, loopKindNouns[kind], strings.Join(targets, " or "), loopSubjectLabel(subject, args))
	}
	if subject.op == loopOpCreate {
		creates := loopContractIndexes(contract, kind, loopOpCreate, loopOpWrite)
		if len(creates) == 0 {
			if loopContractForbids(contract, kind) {
				return nil, fmt.Errorf("%s %s; the call was not made", loopProhibitedCallPrefix, loopProhibitedLabels[kind])
			}
			return nil, fmt.Errorf("%s: this loop's outputs do not include creating %s, so the call was not made. Make only the outputs the loop lists", loopContractRefusalPrefix, loopKindNouns[kind])
		}
		next := map[string]any{}
		for key, value := range args {
			next[key] = value
		}
		if kind == "issue" {
			// A new issue goes to the output's team and project unless the call names them.
			for _, index := range creates {
				target := contract.Outputs[index].Target
				if target.TeamID != "" && loopStringArg(next, "team", "teamId") == "" {
					next["team"], subject.teamID = target.TeamID, target.TeamID
				}
				if target.ProjectID != "" && loopStringArg(next, "project") == "" {
					next["project"], subject.projectID = target.ProjectID, target.ProjectID
				}
				if target.TeamID != "" || target.ProjectID != "" {
					break
				}
			}
		}
		matched := []int{}
		for _, index := range creates {
			if !subject.found || loopTargetHolds(kind, contract.Outputs[index].Target, subject, entityType, entityID) {
				matched = append(matched, index)
			}
		}
		if len(matched) == 0 {
			return nil, refuse(creates)
		}
		// One per run: a second create of an output that makes one is refused
		// when no other output takes it.
		made := map[int]int{}
		for _, prior := range loopPriorOutputWrites(rec) {
			if prior.Status == "completed" && prior.Output.Operation == loopOpCreate && prior.Output.Kind == kind {
				for _, index := range prior.Output.Outputs {
					made[index]++
				}
			}
		}
		open := slices.DeleteFunc(slices.Clone(matched), func(index int) bool {
			return contract.Outputs[index].Cardinality == "onePerRun" && made[index] > 0
		})
		if len(open) == 0 {
			return nil, fmt.Errorf("%s: this loop creates one %s per run and this run already created it, so the call was not made", loopContractRefusalPrefix, strings.TrimSuffix(loopKindNouns[kind], "s"))
		}
		record.Outputs = open
		if kind == "issue" {
			output := contract.Outputs[open[0]]
			if rendered := output.Target.RenderedTitle; rendered != "" {
				title, err := loopFillTitle(rendered, output.Target, next, "issue")
				if err != nil {
					return nil, err
				}
				next["title"] = title
			}
			record.Title = loopStringArg(next, "title")
			record.ParentType, record.ParentID = "", ""
		}
		delete(next, loopTitleFieldsArg)
		if !sameJSON(next, args) {
			prepared.arguments, _ = json.Marshal(next)
		}
		return prepared, nil
	}
	if loopContractForbids(contract, "change") {
		return nil, fmt.Errorf("%s make other changes in Flow; the call was not made", loopProhibitedCallPrefix)
	}
	// An item this run made may be changed: it counts for the output that made it.
	if subject.itemID != "" {
		for _, prior := range loopPriorOutputWrites(rec) {
			if prior.Status == "completed" && prior.Output.Operation == loopOpCreate && prior.Output.ID == subject.itemID {
				record.Outputs = slices.Clone(prior.Output.Outputs)
				return prepared, nil
			}
		}
	}
	updates := loopContractIndexes(contract, kind, loopOpUpdate, loopOpWrite)
	if len(updates) == 0 {
		// The loop's outputs do not change items of this kind: the change is
		// allowed, as other changes are, but is not one of its outputs.
		return prepared, nil
	}
	for _, index := range updates {
		if !subject.found && !loopTargetConstrained(kind, contract.Outputs[index].Target) || subject.found && loopTargetHolds(kind, contract.Outputs[index].Target, subject, entityType, entityID) {
			record.Outputs = append(record.Outputs, index)
		}
	}
	if len(record.Outputs) == 0 && subject.found {
		return nil, refuse(updates)
	}
	return prepared, nil
}

// loopWithoutTitleFields removes the loop's titleFields from a call's
// arguments.
func loopWithoutTitleFields(arguments json.RawMessage) json.RawMessage {
	if !strings.Contains(string(arguments), loopTitleFieldsArg) {
		return arguments
	}
	var args map[string]any
	if json.Unmarshal(arguments, &args) != nil {
		return arguments
	}
	if _, ok := args[loopTitleFieldsArg]; !ok {
		return arguments
	}
	delete(args, loopTitleFieldsArg)
	raw, err := json.Marshal(args)
	if err != nil {
		return arguments
	}
	return raw
}

// loopOutputResultID reads the id of the item a write made from its result.
func loopOutputResultID(result []byte) string {
	var receipt struct {
		ID    string `json:"id"`
		Issue struct {
			ID string `json:"id"`
		} `json:"issue"`
	}
	if json.Unmarshal(result, &receipt) != nil {
		return ""
	}
	return firstNonEmpty(receipt.ID, receipt.Issue.ID)
}

// loopContractUnchosen is the run contract's first output that writes to an
// existing document the instructions do not name and nobody picked.
func loopContractUnchosen(contract *domain.LoopOutputContract) *domain.LoopContractOutput {
	if contract == nil || contract.ReadOnly {
		return nil
	}
	for index, output := range contract.Outputs {
		if output.Kind == "document" && output.Target.Ambiguous && output.Target.DocumentID == "" && (output.Operation == loopOpUpdate || output.Operation == loopOpAppend || output.Operation == loopOpWrite) {
			return &contract.Outputs[index]
		}
	}
	return nil
}
