package main

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// Chat work groups title each phase the way Linear's agent does ("Listing workspace users" above the tool rows it
// ran together). The model no longer narrates progress through report_progress (that cost a round trip of its
// own), so the server names a phase from the tool calls themselves: their names, arguments, and results.
//
// Titles are English templates with one {subject} (an identifier, a quoted search, or a resource noun); the web
// translates them by template (agent-phase-titles.json mirrors agentPhaseTemplates). Like Linear, a turn with a
// single tool call gets no title: its tool row ("Looked at issue") already says what happened.

const (
	phaseLookUp        = "Looking up {subject}"
	phaseLookAt        = "Looking at {subject}"
	phaseSearchIssues  = "Searching issues for {subject}"
	phaseSearchDocs    = "Searching documentation for {subject}"
	phaseComments      = "Reading comments on {subject}"
	phaseHistory       = "Reviewing history of {subject}"
	phaseUpdate        = "Updating {subject}"
	phaseUpdatePrio    = "Updating priority of {subject}"
	phaseUpdateStatus  = "Updating status of {subject}"
	phaseUpdateLabels  = "Updating labels of {subject}"
	phaseAssign        = "Assigning {subject}"
	phaseCreateIssue   = "Creating issue {subject}"
	phaseCreate        = "Creating {subject}"
	phaseComment       = "Commenting on {subject}"
	phaseDelete        = "Deleting {subject}"
	phaseUse           = "Using {subject}"
	agentPhaseMaxItems = 3
)

// agentPhaseTemplates lists every phase title template; the web translates titles by matching them.
var agentPhaseTemplates = []string{phaseLookUp, phaseLookAt, phaseSearchIssues, phaseSearchDocs, phaseComments, phaseHistory, phaseUpdate, phaseUpdatePrio, phaseUpdateStatus, phaseUpdateLabels, phaseAssign, phaseCreateIssue, phaseCreate, phaseComment, phaseDelete, phaseUse}

// agentInternalID matches stored ids (issue_16, project_1790…) that should never be shown as a subject.
var agentInternalID = regexp.MustCompile(`^[a-z]+(?:_[a-z]+)*_[0-9a-f]{1,}$`)

// agentPhaseCalls are the tool calls of a turn that show as tool rows.
func agentPhaseCalls(calls []domain.AgentToolCall) []domain.AgentToolCall {
	return slices.DeleteFunc(slices.Clone(calls), func(call domain.AgentToolCall) bool {
		name := strings.TrimPrefix(call.Name, "mcp__flow.")
		return name == agentProgressTool || name == agentLoadToolsTool || name == loopQuestionTool || name == "ask_question"
	})
}

// agentPhaseTitle names a turn's work from its tool calls, or "" when the turn has fewer than two.
func agentPhaseTitle(calls []domain.AgentToolCall) string {
	calls = agentPhaseCalls(calls)
	if len(calls) < 2 {
		return ""
	}
	type phrase struct {
		template string
		subjects []string
	}
	phrases := []*phrase{}
	for _, call := range calls {
		template, subject := agentCallPhrase(call)
		index := slices.IndexFunc(phrases, func(item *phrase) bool { return item.template == template })
		if index < 0 {
			phrases = append(phrases, &phrase{template: template})
			index = len(phrases) - 1
		}
		if subject != "" && !slices.Contains(phrases[index].subjects, subject) {
			phrases[index].subjects = append(phrases[index].subjects, subject)
		}
	}
	// The first action names the phase ("Searching issues for …" while it also opens a few of them); repeated
	// actions merge their subjects ("Looking up DEV-16, DEV-24").
	lead := phrases[0]
	subjects := lead.subjects
	if len(subjects) > agentPhaseMaxItems {
		subjects = append(subjects[:agentPhaseMaxItems:agentPhaseMaxItems], "…")
	}
	return strings.Replace(lead.template, "{subject}", strings.Join(subjects, ", "), 1)
}

// agentCallPhrase is one tool call as a phase template and its subject.
func agentCallPhrase(call domain.AgentToolCall) (string, string) {
	name := strings.TrimPrefix(call.Name, "mcp__flow.")
	var args map[string]any
	_ = json.Unmarshal(call.Arguments, &args)
	var result map[string]any
	_ = json.Unmarshal(call.Result, &result)
	verb, noun, _ := strings.Cut(name, "_")
	noun = strings.ReplaceAll(noun, "_", " ")
	target := agentCallTarget(args, result)
	switch {
	case name == "search_issues" || name == "search_documentation":
		if query := agentCallQueries(args); query != "" {
			if name == "search_issues" {
				return phaseSearchIssues, query
			}
			return phaseSearchDocs, query
		}
		return phaseLookAt, noun
	case name == "list_comments" && target != "":
		return phaseComments, target
	case (name == "list_issue_history" || name == "list_project_activity" || name == "list_document_history") && target != "":
		return phaseHistory, target
	case name == "save_comment":
		if target := agentCallTarget(map[string]any{"id": firstString(args, "issueId", "issue", "projectId", "documentId")}, nil); target != "" {
			return phaseComment, target
		}
		return phaseCreate, "comment"
	case name == "save_issue":
		if id := firstString(args, "id"); id != "" {
			subject := firstNonEmpty(agentCallTarget(map[string]any{"id": id}, result), "issue")
			switch {
			case args["priority"] != nil:
				return phaseUpdatePrio, subject
			case args["state"] != nil || args["status"] != nil:
				return phaseUpdateStatus, subject
			case args["assignee"] != nil || args["assigneeId"] != nil:
				return phaseAssign, subject
			case args["labels"] != nil || args["labelIds"] != nil:
				return phaseUpdateLabels, subject
			}
			return phaseUpdate, subject
		}
		if title := firstString(args, "title"); title != "" {
			return phaseCreateIssue, title
		}
		return phaseCreate, "issue"
	case verb == "get" && target != "":
		return phaseLookUp, target
	case verb == "get" || verb == "list":
		return phaseLookAt, noun
	case verb == "delete":
		if target != "" {
			return phaseDelete, target
		}
		return phaseDelete, noun
	case verb == "save" || verb == "update" || verb == "create" || verb == "restore" || verb == "triage" || verb == "resolve" || verb == "submit" || verb == "merge":
		if firstString(args, "id") != "" && target != "" {
			return phaseUpdate, target
		}
		if verb == "save" || verb == "create" {
			return phaseCreate, noun
		}
		return phaseUpdate, firstNonEmpty(target, noun)
	}
	return phaseUse, strings.ReplaceAll(name, "_", " ")
}

// agentCallTarget is the resource a call is about, as people know it: an identifier or name from the result when
// the model passed an internal id, otherwise the argument as written.
func agentCallTarget(args, result map[string]any) string {
	for _, key := range []string{"identifier", "name", "title"} {
		if value, ok := result[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"id", "issueId", "issue", "identifier", "projectId", "project", "documentId", "document", "initiativeId", "initiative", "name", "title", "query"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" && !agentInternalID.MatchString(value) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func agentCallQueries(args map[string]any) string {
	quoted := []string{}
	if queries, ok := args["queries"].([]any); ok {
		for _, item := range queries {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" && len(quoted) < agentPhaseMaxItems {
				quoted = append(quoted, `"`+strings.TrimSpace(text)+`"`)
			}
		}
	}
	if query := firstString(args, "query"); query != "" && len(quoted) == 0 {
		quoted = append(quoted, `"`+query+`"`)
	}
	return strings.Join(quoted, ", ")
}

func firstString(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
