package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"flow/api/internal/domain"
)

// askUserTool lets the agent ask the user to choose (the reference app's select elicitation): the reply ends with the question
// and its options, the run stops, and the user's next message — a picked option or free text — answers it.
const askUserTool = "ask_user"

var askUserToolDefinition = agentProviderTool{
	Name: askUserTool,
	Description: "Ask the user to choose between options and stop until they answer. The app shows the question with the options as buttons; " +
		"the user picks one or types something else, and their answer arrives as the next user message. Use it for an approval " +
		"(options Approve and Reject) and when a required value is genuinely ambiguous — not for choices you can reasonably make yourself. " +
		"Ask one question per call and call nothing else in the same turn.",
	Parameters: json.RawMessage(`{"type":"object","required":["question","options"],"properties":{` +
		`"question":{"type":"string","description":"The question, as you would say it to the user (markdown allowed)"},` +
		`"options":{"type":"array","minItems":2,"maxItems":6,"items":{"type":"object","required":["label"],"properties":{"label":{"type":"string","description":"Button text, 1-4 words"},"value":{"type":"string","description":"What the answer means, if different from the label"}},"additionalProperties":false}},` +
		`"allowFreeText":{"type":"boolean","description":"Whether the user may answer with something else instead (default true)"}` +
		`},"additionalProperties":false}`),
	Access: "read",
}

type askUserArguments struct {
	Question string `json:"question"`
	Options  []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"options"`
	AllowFreeText *bool `json:"allowFreeText"`
}

// parseAskUser validates an ask_user call into its question text and options.
func parseAskUser(call domain.AgentToolCall) (string, domain.AgentQuestion, error) {
	var args askUserArguments
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return "", domain.AgentQuestion{}, fmt.Errorf("ask_user arguments must be a JSON object: %w", err)
	}
	question := strings.TrimSpace(args.Question)
	if question == "" || len(question) > 8000 {
		return "", domain.AgentQuestion{}, fmt.Errorf("ask_user needs a question of at most 8000 characters")
	}
	result := domain.AgentQuestion{AllowFreeText: args.AllowFreeText == nil || *args.AllowFreeText}
	seen := map[string]bool{}
	for _, option := range args.Options {
		label := strings.TrimSpace(option.Label)
		value := strings.TrimSpace(option.Value)
		if value == "" {
			value = label
		}
		if label == "" || len(label) > 80 || len(value) > 500 || seen[strings.ToLower(value)] {
			continue
		}
		seen[strings.ToLower(value)] = true
		result.Options = append(result.Options, domain.AgentQuestionOption{Label: label, Value: value})
	}
	if len(result.Options) < 2 || len(result.Options) > 6 {
		return "", domain.AgentQuestion{}, fmt.Errorf("ask_user needs 2 to 6 distinct options")
	}
	return question, result, nil
}

// agentQuestionAnswer is the option a follow-up message picks for the reply's pending question.
type agentQuestionAnswer struct {
	PartID string `json:"partId"`
	Value  string `json:"value"`
}

// answerAgentQuestion records the user's next message as the answer to the latest reply's pending question, if it
// has one: a picked option (answer.PartID names the question) or free text. It returns the question part id when an
// option was picked, for the user message's AnswerTo.
func answerAgentQuestion(session *domain.AgentSession, message string, answer *agentQuestionAnswer) (string, error) {
	for index := len(session.Messages) - 1; index >= 0; index-- {
		reply := &session.Messages[index]
		if reply.Role != "assistant" {
			continue
		}
		for partIndex := range reply.Parts {
			part := &reply.Parts[partIndex]
			if part.Type != "question" || part.Question == nil || part.Status != "pending" {
				continue
			}
			answerTo := ""
			result := map[string]any{}
			if answer != nil && answer.PartID == part.ID {
				option := -1
				for candidate, choice := range part.Question.Options {
					if choice.Value == answer.Value {
						option = candidate
					}
				}
				if option < 0 {
					return "", fmt.Errorf("%w: that option is not one of the question's choices", errInvalid)
				}
				choice := part.Question.Options[option]
				part.Question.Selected = choice.Value
				result["selected"] = choice.Value
				result["label"] = choice.Label
				answerTo = part.ID
			} else {
				if answer != nil {
					return "", fmt.Errorf("%w: the question was already answered", errInvalid)
				}
				part.Question.Answer = message
				result["answer"] = message
			}
			part.Status = "completed"
			if part.ToolCall != nil {
				raw, _ := json.Marshal(result)
				call := *part.ToolCall
				call.Status, call.Result = "completed", raw
				part.ToolCall = &call
			}
			return answerTo, nil
		}
		break
	}
	if answer != nil {
		return "", fmt.Errorf("%w: there is no question waiting for an answer", errInvalid)
	}
	return "", nil
}
