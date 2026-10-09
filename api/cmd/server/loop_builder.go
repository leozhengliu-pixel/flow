package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The loop builder is the agent chat Linear opens beside a loop draft: it edits
// the draft with save_loop and, for templates, asks a few chip questions
// (elicitations) before publishing.

const loopQuestionTool = "ask_question"

var loopQuestionToolDefinition = agentProviderTool{
	Name:        loopQuestionTool,
	Description: "Ask the user one multiple-choice question and wait for the answer. The app shows the options as chips. Use it only while setting up a loop draft, one question per call.",
	Parameters:  json.RawMessage(`{"type":"object","required":["question","options"],"properties":{"question":{"type":"string","description":"One short question"},"options":{"type":"array","minItems":2,"maxItems":3,"items":{"type":"string"},"description":"2-3 short answers (2-5 words each)"}},"additionalProperties":false}`),
	Access:      "read",
}

// agentExtraToolsKey adds session-specific tools (the loop builder's question tool).
type agentExtraToolsKey struct{}

// sessionLoops returns the loops a conversation is building, in session order.
func sessionLoops(data domain.Bootstrap, session domain.AgentSession) []domain.Loop {
	loops := []domain.Loop{}
	for _, id := range session.LoopIDs {
		if loop := loopByID(&data, id); loop != nil {
			loops = append(loops, presentLoop(nil, *loop))
		}
	}
	return loops
}

func sessionHasDraftLoop(data domain.Bootstrap, session domain.AgentSession) bool {
	return slices.ContainsFunc(sessionLoops(data, session), func(loop domain.Loop) bool { return loop.Status == "draft" })
}

// loopBuilderPrompt is the system prompt section for loops in the conversation.
func loopBuilderPrompt(data domain.Bootstrap, session domain.AgentSession) string {
	loops := sessionLoops(data, session)
	if len(loops) == 0 {
		return ""
	}
	var prompt strings.Builder
	prompt.WriteString("\n\nLoop builder:\nThe loop editor is open beside this chat. Edit the loop below only with save_loop and its id; the editor updates live. Loops are automations Flow Agent runs on a schedule or when issues, projects, initiatives, releases or teams change.\n")
	for _, loop := range loops {
		team := ""
		for _, item := range data.Teams {
			if item.ID == loop.TeamID {
				team = item.Name + " (" + item.Key + ")"
			}
		}
		summary := map[string]any{"id": loop.ID, "name": loop.Name, "status": loop.Status, "level": loop.Level, "team": team, "trigger": loop.TriggerType, "triggerConfig": loop.TriggerConfig, "instructions": truncateSettingsText(loop.Instructions, 8000), "codeAccess": loop.CodeAccess, "allowChangesOutsideTrigger": loop.AllowChangesOutsideTrigger, "allowExternalSync": loop.AllowExternalSync, "webSearch": loop.WebSearch}
		raw, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Fprintf(&prompt, "\nLoop %s:\n%s\n", loop.ID, raw)
		if loop.Status != "draft" {
			prompt.WriteString("This loop is already created. Change it only as the user asks, then reply in one sentence.\n")
			continue
		}
		if template := loopTemplateByID(loop.TemplateID); template != nil {
			fmt.Fprintf(&prompt, `This draft comes from the %q template; its name, trigger and instructions are already written.
- Start your first reply with: "I've opened a draft and written the instructions for you. A few details depend on how your workspace is set up, so I'll ask about those, and then you can publish it."
- Then ask 1 to 3 questions about autonomy and scope, one at a time with %s, each with 2-3 short options (2-5 words). Good questions for this template:
`, template.Name, loopQuestionTool)
			for _, question := range template.Questions {
				fmt.Fprintf(&prompt, "  - %s (%s)\n", question.Question, strings.Join(question.Options, " / "))
			}
			prompt.WriteString(`- After each answer, call save_loop with the loop id to apply it: rewrite the affected instruction steps (keep the rest of the instructions intact) and, where it fits, the trigger filters (for example "Skip already assigned" → filters [{"field":"assignee","operator":"is","value":null}]). If the user skips a question, keep the template's default.
- After the last answer, call save_loop with the loop id and publish: true to create and enable the loop.
- Finish with one sentence saying the loop is live and what it will do. Do not repeat the instructions.
`)
			continue
		}
		prompt.WriteString(`This draft was started from scratch or from the user's request.
- Fill it in from the request with one save_loop call using the loop id: a short name (2-5 words), the trigger (schedule: interval/unit/time/startDate/weekdays; events: event, value, teams, filters) and detailed instructions written as a short intro sentence plus numbered steps that say what to look up, what to change and what to report, like a well-written runbook.
- Resolve relative dates against Today. Use the list tools to look up teams, people, labels and statuses you need to name.
- Do not ask questions unless the request is impossible to act on. Do not publish, enable or run the loop.
- Reply with one or two sentences on what you set up and end with: "It remains a draft for your review; I didn't publish, enable, or run it."
`)
	}
	return prompt.String()
}

// sessionLoopTool reports whether a tool call edits a loop this conversation is
// building; those edits are applied without an approval card.
func sessionLoopTool(session domain.AgentSession, call domain.AgentToolCall) bool {
	if strings.TrimPrefix(call.Name, "mcp__flow.") != "save_loop" || len(session.LoopIDs) == 0 {
		return false
	}
	id := loopStringArg(loopToolArgs(call), "id")
	return id != "" && slices.Contains(session.LoopIDs, id)
}

// saveLoopToolTitle labels a save_loop tool row the way Linear does.
func saveLoopToolTitle(result []byte) string {
	var card struct {
		Status    string `json:"status"`
		Published bool   `json:"published"`
	}
	if json.Unmarshal(result, &card) != nil {
		return ""
	}
	switch {
	case card.Published:
		return "Created automation"
	case card.Status == "draft":
		return "Updated workflow definition draft"
	case card.Status != "":
		return "Updated automation"
	}
	return ""
}

// loopQuestionSchema is the elicitation form for one chip question.
func loopQuestionSchema(question string, options []string) map[string]any {
	enum := []any{}
	for _, option := range options {
		enum = append(enum, option)
	}
	return map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string", "title": question, "enum": enum}}, "required": []any{"answer"}}
}

func parseLoopQuestion(call domain.AgentToolCall) (string, []string, error) {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if json.Unmarshal(call.Arguments, &args) != nil || strings.TrimSpace(args.Question) == "" {
		return "", nil, fmt.Errorf("question is required")
	}
	options := []string{}
	for _, option := range args.Options {
		if option = strings.TrimSpace(option); option != "" && len(options) < 3 && !slices.Contains(options, option) {
			options = append(options, option)
		}
	}
	if len(options) < 2 {
		return "", nil, fmt.Errorf("give 2-3 options")
	}
	return strings.TrimSpace(args.Question), options, nil
}

// askLoopQuestion shows a chip question as an elicitation and returns the
// tool result for the model. The answered part keeps the tool call so later
// turns still see the question and answer.
func (s *server) askLoopQuestion(r *http.Request, session *domain.AgentSession, call domain.AgentToolCall, emit func(domain.AgentMessagePart, string) error) (string, bool) {
	question, options, err := parseLoopQuestion(call)
	if err != nil {
		return err.Error(), true
	}
	if _, ok := r.Context().Value(agentExtraToolsKey{}).([]agentProviderTool); !ok {
		return "Questions are not available in this conversation; choose the first option and continue.", true
	}
	var answered domain.AgentMessagePart
	result, err := s.requestAgentElicitation(r.Context(), workspaceKey(r), session.ID, session.UserID, applicationPolicy{Name: "Flow Agent"}, &mcp.ElicitParams{Mode: "form", Message: question, RequestedSchema: loopQuestionSchema(question, options)}, func(part domain.AgentMessagePart, kind string) error {
		answered = part
		return emit(part, kind)
	})
	if err != nil {
		return "The question could not be shown: " + err.Error(), true
	}
	content := `{"answer":null,"note":"The user skipped this question; keep the default and continue."}`
	if result.Action == "accept" {
		if answer, ok := result.Content["answer"].(string); ok && strings.TrimSpace(answer) != "" {
			raw, _ := json.Marshal(map[string]string{"answer": answer})
			content = string(raw)
		}
	}
	if answered.ID != "" {
		call.Status, call.Result = "completed", json.RawMessage(content)
		answered.ToolCall = cloneToolCall(&call)
		if answered.Elicitation != nil && result.Action == "accept" {
			answered.Text, _ = result.Content["answer"].(string)
		}
		_ = emit(answered, "elicitation.resolved")
	}
	return content, false
}
