package llmproxy

// Responses ↔ Chat Completions translation (FLOW_CODING_LLM_OPENAI_WIRE_API=chat).
//
// Codex only speaks the OpenAI Responses API (its `wire_api = "chat"` provider
// option was removed), while many OpenAI-compatible providers (DeepSeek, vLLM,
// …) only offer Chat Completions. With the chat wire API configured, the proxy
// rewrites each POST /v1/responses into a POST /v1/chat/completions and turns
// the answer (streamed or not) back into Responses events / a response object.
//
// Covered: instructions and developer/system/user/assistant messages, text and
// image input, function tools and calls (custom/freeform tools become a
// function with one string argument "input"), tool outputs, tool_choice,
// parallel_tool_calls, max_output_tokens, temperature/top_p, reasoning effort,
// text.format → response_format, and usage (with cached and reasoning tokens).
// Hosted tools (web_search, file_search, …) and tool namespaces are dropped:
// a chat upstream cannot run them. previous_response_id is refused.
//
// Reasoning: chat providers that think (DeepSeek's reasoning_content) require
// the reasoning of a tool-calling assistant turn to be sent back. It is
// returned to the client as a reasoning item whose encrypted_content carries
// the text (Codex echoes encrypted_content because it asks for
// include=["reasoning.encrypted_content"]), and restored on the next request.

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// reasoningCarrierPrefix marks encrypted_content written by this translator.
const reasoningCarrierPrefix = "flowchat1:"

// errPreviousResponse is returned for previous_response_id, which needs
// server-side conversation state a chat upstream does not have.
var errPreviousResponse = errors.New("previous_response_id is not supported when the upstream speaks Chat Completions")

// chatTranslation is the per-request state the response side needs.
type chatTranslation struct {
	stream bool
	// custom holds the names of custom (freeform) tools, answered as
	// custom_tool_call items instead of function_call.
	custom map[string]bool
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          any            `json:"content"`
	ReasoningContent *string        `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

// responsesItem is the union of the Responses input items translated.
type responsesItem struct {
	Type             string          `json:"type"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Arguments        string          `json:"arguments"`
	Input            string          `json:"input"`
	Output           json.RawMessage `json:"output"`
	Summary          json.RawMessage `json:"summary"`
	EncryptedContent string          `json:"encrypted_content"`
}

type contentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
}

// parseParts reads a content value: a string or an array of parts.
func parseParts(raw json.RawMessage) []contentPart {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []contentPart{{Type: "text", Text: text}}
	}
	var parts []contentPart
	_ = json.Unmarshal(raw, &parts)
	return parts
}

// partsText joins the text of the parts (images are dropped).
func partsText(parts []contentPart) string {
	var texts []string
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text", "reasoning_text", "summary_text", "refusal":
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// userContent keeps images as image_url parts; text-only content stays a string.
func userContent(parts []contentPart) any {
	hasImage := false
	for _, part := range parts {
		if part.Type == "input_image" && len(part.ImageURL) > 0 {
			hasImage = true
		}
	}
	if !hasImage {
		return partsText(parts)
	}
	out := []map[string]any{}
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "text":
			out = append(out, map[string]any{"type": "text", "text": part.Text})
		case "input_image":
			var url string
			if json.Unmarshal(part.ImageURL, &url) == nil && url != "" {
				out = append(out, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			}
		}
	}
	return out
}

// reasoningText restores the reasoning carried by a reasoning item.
func reasoningText(item responsesItem) string {
	if encoded, ok := strings.CutPrefix(item.EncryptedContent, reasoningCarrierPrefix); ok {
		if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
			return string(decoded)
		}
	}
	if text := partsText(parseParts(item.Content)); text != "" {
		return text
	}
	return partsText(parseParts(item.Summary))
}

// messageBuilder folds Responses items into chat messages. Consecutive
// reasoning, assistant text and tool calls form one assistant message.
type messageBuilder struct {
	messages  []chatMessage
	pending   *chatMessage
	reasoning strings.Builder
	thinking  bool // the request enables reasoning
}

func (b *messageBuilder) assistant() *chatMessage {
	if b.pending == nil {
		b.pending = &chatMessage{Role: "assistant"}
	}
	return b.pending
}

func (b *messageBuilder) flush() {
	if b.pending == nil {
		return
	}
	message := b.pending
	b.pending = nil
	if b.reasoning.Len() > 0 {
		text := b.reasoning.String()
		message.ReasoningContent = &text
	} else if b.thinking && len(message.ToolCalls) > 0 {
		// Thinking providers refuse a tool-calling turn without its reasoning.
		empty := ""
		message.ReasoningContent = &empty
	}
	b.reasoning.Reset()
	if text, _ := message.Content.(string); text == "" && len(message.ToolCalls) == 0 && message.ReasoningContent == nil {
		return
	}
	if text, _ := message.Content.(string); text == "" {
		message.Content = nil
	}
	b.messages = append(b.messages, *message)
}

func (b *messageBuilder) add(item responsesItem) {
	switch item.Type {
	case "message", "":
		parts := parseParts(item.Content)
		switch item.Role {
		case "assistant":
			message := b.assistant()
			if len(message.ToolCalls) > 0 {
				b.flush()
				message = b.assistant()
			}
			text, _ := message.Content.(string)
			if add := partsText(parts); add != "" {
				if text != "" {
					text += "\n"
				}
				message.Content = text + add
			}
		case "system", "developer":
			b.flush()
			b.messages = append(b.messages, chatMessage{Role: "system", Content: partsText(parts)})
		default:
			b.flush()
			b.messages = append(b.messages, chatMessage{Role: "user", Content: userContent(parts)})
		}
	case "reasoning":
		if b.pending != nil && (b.pending.Content != nil || len(b.pending.ToolCalls) > 0) {
			b.flush()
		}
		b.assistant()
		b.reasoning.WriteString(reasoningText(item))
	case "function_call":
		message := b.assistant()
		message.ToolCalls = append(message.ToolCalls, chatToolCall{ID: item.CallID, Type: "function", Function: chatFunctionCall{Name: item.Name, Arguments: item.Arguments}})
	case "custom_tool_call":
		arguments, _ := json.Marshal(map[string]string{"input": item.Input})
		message := b.assistant()
		message.ToolCalls = append(message.ToolCalls, chatToolCall{ID: item.CallID, Type: "function", Function: chatFunctionCall{Name: item.Name, Arguments: string(arguments)}})
	case "function_call_output", "custom_tool_call_output":
		b.flush()
		b.messages = append(b.messages, chatMessage{Role: "tool", ToolCallID: item.CallID, Content: toolOutputText(item.Output)})
	}
	// Other items (hosted tool calls, compaction markers) have no chat form.
}

func toolOutputText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var wrapped struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && len(wrapped.Content) > 0 {
		return toolOutputText(wrapped.Content)
	}
	return partsText(parseParts(raw))
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Format      json.RawMessage `json:"format"`
}

// chatTools converts function and custom tools; hosted tools are dropped.
func chatTools(raw json.RawMessage, custom map[string]bool) []map[string]any {
	var tools []responsesTool
	_ = json.Unmarshal(raw, &tools)
	out := []map[string]any{}
	for _, tool := range tools {
		if tool.Name == "" {
			continue
		}
		function := map[string]any{"name": tool.Name}
		if tool.Description != "" {
			function["description"] = tool.Description
		}
		switch tool.Type {
		case "function":
			if len(tool.Parameters) > 0 && string(tool.Parameters) != "null" {
				function["parameters"] = tool.Parameters
			} else {
				function["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
			}
		case "custom":
			custom[tool.Name] = true
			description := "The raw input for this tool."
			var format struct {
				Type       string `json:"type"`
				Syntax     string `json:"syntax"`
				Definition string `json:"definition"`
			}
			if json.Unmarshal(tool.Format, &format) == nil && format.Definition != "" {
				description += " It must follow this " + format.Syntax + " grammar:\n" + format.Definition
			}
			function["parameters"] = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"input": map[string]any{"type": "string", "description": description}},
				"required":             []string{"input"},
				"additionalProperties": false,
			}
		default:
			continue
		}
		out = append(out, map[string]any{"type": "function", "function": function})
	}
	return out
}

func chatToolChoice(raw json.RawMessage) any {
	var choice string
	if json.Unmarshal(raw, &choice) == nil {
		switch choice {
		case "auto", "none", "required":
			return choice
		}
		return "auto"
	}
	var named struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &named) == nil && (named.Type == "function" || named.Type == "custom") && named.Name != "" {
		return map[string]any{"type": "function", "function": map[string]any{"name": named.Name}}
	}
	return "auto"
}

func responseFormat(raw json.RawMessage) any {
	var text struct {
		Format *struct {
			Type   string          `json:"type"`
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict *bool           `json:"strict"`
		} `json:"format"`
	}
	if json.Unmarshal(raw, &text) != nil || text.Format == nil {
		return nil
	}
	switch text.Format.Type {
	case "json_object":
		return map[string]any{"type": "json_object"}
	case "json_schema":
		schema := map[string]any{"name": text.Format.Name, "schema": text.Format.Schema}
		if schema["name"] == "" {
			schema["name"] = "output"
		}
		if text.Format.Strict != nil {
			schema["strict"] = *text.Format.Strict
		}
		return map[string]any{"type": "json_schema", "json_schema": schema}
	}
	return nil
}

// responsesToChat rewrites a Responses request document as a chat completions
// request document.
func responsesToChat(document map[string]json.RawMessage) (map[string]json.RawMessage, *chatTranslation, error) {
	var previous string
	_ = json.Unmarshal(document["previous_response_id"], &previous)
	if previous != "" {
		return nil, nil, errPreviousResponse
	}
	translation := &chatTranslation{custom: map[string]bool{}}
	_ = json.Unmarshal(document["stream"], &translation.stream)

	var reasoning struct {
		Effort string `json:"effort"`
	}
	_ = json.Unmarshal(document["reasoning"], &reasoning)
	builder := &messageBuilder{thinking: reasoning.Effort != "" && reasoning.Effort != "none"}

	var instructions string
	if json.Unmarshal(document["instructions"], &instructions) == nil && instructions != "" {
		builder.messages = append(builder.messages, chatMessage{Role: "system", Content: instructions})
	}
	var inputText string
	if json.Unmarshal(document["input"], &inputText) == nil {
		builder.messages = append(builder.messages, chatMessage{Role: "user", Content: inputText})
	} else {
		var items []responsesItem
		if raw, present := document["input"]; present && json.Unmarshal(raw, &items) != nil {
			return nil, nil, errors.New("input must be a string or an array of items")
		}
		for _, item := range items {
			builder.add(item)
		}
		builder.flush()
	}

	out := map[string]any{"messages": builder.messages, "stream": translation.stream}
	if raw, present := document["model"]; present {
		out["model"] = raw
	}
	if tools := chatTools(document["tools"], translation.custom); len(tools) > 0 {
		out["tools"] = tools
		if raw, present := document["tool_choice"]; present {
			out["tool_choice"] = chatToolChoice(raw)
		}
		if raw, present := document["parallel_tool_calls"]; present {
			out["parallel_tool_calls"] = raw
		}
	}
	for from, to := range map[string]string{"max_output_tokens": "max_tokens", "temperature": "temperature", "top_p": "top_p", "user": "user"} {
		if raw, present := document[from]; present && string(raw) != "null" {
			out[to] = raw
		}
	}
	if reasoning.Effort != "" {
		out["reasoning_effort"] = reasoning.Effort
	}
	if format := responseFormat(document["text"]); format != nil {
		out["response_format"] = format
	}
	chat := make(map[string]json.RawMessage, len(out))
	for key, value := range out {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, nil, err
		}
		chat[key] = raw
	}
	return chat, translation, nil
}

// --- response side ---

type chatUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	PromptCacheHitTokens *int64 `json:"prompt_cache_hit_tokens"`
}

type chatDelta struct {
	Content          *string `json:"content"`
	ReasoningContent *string `json:"reasoning_content"`
	Reasoning        *string `json:"reasoning"`
	ToolCalls        []struct {
		Index    *int   `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// chatChunk is a streamed chunk or (with Message) a full completion.
type chatChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Created int64  `json:"created"`
	Choices []struct {
		Index        int        `json:"index"`
		Delta        *chatDelta `json:"delta"`
		Message      *chatDelta `json:"message"`
		FinishReason *string    `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

type openToolCall struct {
	id, callID, name string
	arguments        strings.Builder
	outputIndex      int
}

// responsesBuilder accumulates chat output as Responses output items and,
// when emit is set, writes the matching Responses SSE events.
type responsesBuilder struct {
	translation *chatTranslation
	emit        func(event string, payload map[string]any) error

	id, model string
	created   int64
	sequence  int
	started   bool
	output    []map[string]any
	nextIndex int

	reasoning      *strings.Builder
	reasoningID    string
	reasoningIndex int
	message        *strings.Builder
	messageID      string
	messageIndex   int
	tools          []*openToolCall
	toolsByIndex   map[int]*openToolCall

	usage  *chatUsage
	finish string
	failed string
}

func newResponsesBuilder(translation *chatTranslation, now time.Time) *responsesBuilder {
	return &responsesBuilder{translation: translation, id: "resp_" + newID(now), created: now.Unix(), toolsByIndex: map[int]*openToolCall{}}
}

func (b *responsesBuilder) send(event string, payload map[string]any) error {
	if b.emit == nil {
		return nil
	}
	payload["type"] = event
	payload["sequence_number"] = b.sequence
	b.sequence++
	return b.emit(event, payload)
}

func (b *responsesBuilder) itemID(prefix string) string {
	return fmt.Sprintf("%s_%s_%d", prefix, strings.TrimPrefix(b.id, "resp_"), b.nextIndex)
}

func (b *responsesBuilder) envelope(status string) map[string]any {
	response := map[string]any{
		"id": b.id, "object": "response", "created_at": b.created, "model": b.model, "status": status,
		"output": b.output, "parallel_tool_calls": true, "tool_choice": "auto", "tools": []any{},
	}
	if b.usage != nil {
		cached := int64(0)
		if b.usage.PromptTokensDetails != nil {
			cached = b.usage.PromptTokensDetails.CachedTokens
		} else if b.usage.PromptCacheHitTokens != nil {
			cached = *b.usage.PromptCacheHitTokens
		}
		reasoning := int64(0)
		if b.usage.CompletionTokensDetails != nil {
			reasoning = b.usage.CompletionTokensDetails.ReasoningTokens
		}
		total := b.usage.TotalTokens
		if total == 0 {
			total = b.usage.PromptTokens + b.usage.CompletionTokens
		}
		response["usage"] = map[string]any{
			"input_tokens": b.usage.PromptTokens, "input_tokens_details": map[string]any{"cached_tokens": cached},
			"output_tokens": b.usage.CompletionTokens, "output_tokens_details": map[string]any{"reasoning_tokens": reasoning},
			"total_tokens": total,
		}
	}
	return response
}

func (b *responsesBuilder) start(chunk *chatChunk) error {
	if chunk.Model != "" {
		b.model = chunk.Model
	}
	if b.started {
		return nil
	}
	b.started = true
	if chunk.Created > 0 {
		b.created = chunk.Created
	}
	if err := b.send("response.created", map[string]any{"response": b.envelope("in_progress")}); err != nil {
		return err
	}
	return b.send("response.in_progress", map[string]any{"response": b.envelope("in_progress")})
}

func (b *responsesBuilder) closeReasoning() error {
	if b.reasoning == nil {
		return nil
	}
	text := b.reasoning.String()
	b.reasoning = nil
	item := map[string]any{
		"type": "reasoning", "id": b.reasoningID,
		"summary":           []any{map[string]any{"type": "summary_text", "text": text}},
		"content":           []any{map[string]any{"type": "reasoning_text", "text": text}},
		"encrypted_content": reasoningCarrierPrefix + base64.StdEncoding.EncodeToString([]byte(text)),
	}
	b.output = append(b.output, item)
	return b.send("response.output_item.done", map[string]any{"output_index": b.reasoningIndex, "item": item})
}

func (b *responsesBuilder) closeMessage() error {
	if b.message == nil {
		return nil
	}
	text := b.message.String()
	b.message = nil
	part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
	item := map[string]any{"type": "message", "id": b.messageID, "role": "assistant", "status": "completed", "content": []any{part}}
	b.output = append(b.output, item)
	for _, step := range []struct {
		event   string
		payload map[string]any
	}{
		{"response.output_text.done", map[string]any{"item_id": b.messageID, "output_index": b.messageIndex, "content_index": 0, "text": text}},
		{"response.content_part.done", map[string]any{"item_id": b.messageID, "output_index": b.messageIndex, "content_index": 0, "part": part}},
		{"response.output_item.done", map[string]any{"output_index": b.messageIndex, "item": item}},
	} {
		if err := b.send(step.event, step.payload); err != nil {
			return err
		}
	}
	return nil
}

func (b *responsesBuilder) toolItem(call *openToolCall, done bool) map[string]any {
	status := "in_progress"
	if done {
		status = "completed"
	}
	if b.translation.custom[call.name] {
		input := ""
		if done {
			var arguments struct {
				Input *string `json:"input"`
			}
			if json.Unmarshal([]byte(call.arguments.String()), &arguments) == nil && arguments.Input != nil {
				input = *arguments.Input
			} else {
				input = call.arguments.String()
			}
		}
		return map[string]any{"type": "custom_tool_call", "id": call.id, "call_id": call.callID, "name": call.name, "input": input, "status": status}
	}
	arguments := ""
	if done {
		arguments = call.arguments.String()
	}
	return map[string]any{"type": "function_call", "id": call.id, "call_id": call.callID, "name": call.name, "arguments": arguments, "status": status}
}

// apply folds one chunk (or a full completion) into the response.
func (b *responsesBuilder) apply(chunk *chatChunk) error {
	if chunk.Error != nil {
		b.failed = chunk.Error.Message
		if b.failed == "" {
			b.failed = "The model provider reported an error"
		}
		return nil
	}
	if err := b.start(chunk); err != nil {
		return err
	}
	if chunk.Usage != nil {
		b.usage = chunk.Usage
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			continue
		}
		delta := choice.Delta
		if delta == nil {
			delta = choice.Message
		}
		if delta != nil {
			if err := b.applyDelta(delta); err != nil {
				return err
			}
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			b.finish = *choice.FinishReason
		}
	}
	return nil
}

func (b *responsesBuilder) applyDelta(delta *chatDelta) error {
	reasoning := delta.ReasoningContent
	if reasoning == nil {
		reasoning = delta.Reasoning
	}
	if reasoning != nil && *reasoning != "" {
		if b.reasoning == nil {
			b.reasoning, b.reasoningID, b.reasoningIndex = &strings.Builder{}, b.itemID("rs"), b.nextIndex
			b.nextIndex++
			if err := b.send("response.output_item.added", map[string]any{"output_index": b.reasoningIndex, "item": map[string]any{"type": "reasoning", "id": b.reasoningID, "summary": []any{}}}); err != nil {
				return err
			}
		}
		b.reasoning.WriteString(*reasoning)
		if err := b.send("response.reasoning_text.delta", map[string]any{"item_id": b.reasoningID, "output_index": b.reasoningIndex, "content_index": 0, "delta": *reasoning}); err != nil {
			return err
		}
	}
	if delta.Content != nil && *delta.Content != "" {
		if err := b.closeReasoning(); err != nil {
			return err
		}
		if b.message == nil {
			b.message, b.messageID, b.messageIndex = &strings.Builder{}, b.itemID("msg"), b.nextIndex
			b.nextIndex++
			if err := b.send("response.output_item.added", map[string]any{"output_index": b.messageIndex, "item": map[string]any{"type": "message", "id": b.messageID, "role": "assistant", "status": "in_progress", "content": []any{}}}); err != nil {
				return err
			}
			if err := b.send("response.content_part.added", map[string]any{"item_id": b.messageID, "output_index": b.messageIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}); err != nil {
				return err
			}
		}
		b.message.WriteString(*delta.Content)
		if err := b.send("response.output_text.delta", map[string]any{"item_id": b.messageID, "output_index": b.messageIndex, "content_index": 0, "delta": *delta.Content}); err != nil {
			return err
		}
	}
	for position, toolDelta := range delta.ToolCalls {
		index := position
		if toolDelta.Index != nil {
			index = *toolDelta.Index
		}
		call := b.toolsByIndex[index]
		if call == nil {
			if err := b.closeReasoning(); err != nil {
				return err
			}
			if err := b.closeMessage(); err != nil {
				return err
			}
			call = &openToolCall{id: b.itemID("fc"), callID: toolDelta.ID, name: toolDelta.Function.Name, outputIndex: b.nextIndex}
			if call.callID == "" {
				call.callID = "call_" + strings.TrimPrefix(call.id, "fc_")
			}
			b.nextIndex++
			b.toolsByIndex[index] = call
			b.tools = append(b.tools, call)
			if err := b.send("response.output_item.added", map[string]any{"output_index": call.outputIndex, "item": b.toolItem(call, false)}); err != nil {
				return err
			}
		} else if call.name == "" && toolDelta.Function.Name != "" {
			call.name = toolDelta.Function.Name
		}
		if toolDelta.Function.Arguments != "" {
			call.arguments.WriteString(toolDelta.Function.Arguments)
			if !b.translation.custom[call.name] {
				if err := b.send("response.function_call_arguments.delta", map[string]any{"item_id": call.id, "output_index": call.outputIndex, "delta": toolDelta.Function.Arguments}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// finishResponse closes the open items and returns the final event name and
// response object: response.completed, response.incomplete (length or content
// filter) or response.failed (an error chunk, or a stream that ended early).
func (b *responsesBuilder) finishResponse(ended bool) (string, map[string]any, error) {
	if !b.started {
		if err := b.start(&chatChunk{}); err != nil {
			return "", nil, err
		}
	}
	if err := b.closeReasoning(); err != nil {
		return "", nil, err
	}
	if err := b.closeMessage(); err != nil {
		return "", nil, err
	}
	for _, call := range b.tools {
		item := b.toolItem(call, true)
		b.output = append(b.output, item)
		if !b.translation.custom[call.name] {
			if err := b.send("response.function_call_arguments.done", map[string]any{"item_id": call.id, "output_index": call.outputIndex, "arguments": call.arguments.String()}); err != nil {
				return "", nil, err
			}
		}
		if err := b.send("response.output_item.done", map[string]any{"output_index": call.outputIndex, "item": item}); err != nil {
			return "", nil, err
		}
	}
	b.tools, b.toolsByIndex = nil, map[int]*openToolCall{}
	switch {
	case b.failed != "" || (!ended && b.finish == ""):
		message := b.failed
		if message == "" {
			message = "The model provider's stream ended before the response was complete"
		}
		response := b.envelope("failed")
		response["error"] = map[string]any{"code": "server_error", "message": message}
		return "response.failed", response, nil
	case b.finish == "length":
		response := b.envelope("incomplete")
		response["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		return "response.incomplete", response, nil
	case b.finish == "content_filter":
		response := b.envelope("incomplete")
		response["incomplete_details"] = map[string]any{"reason": "content_filter"}
		return "response.incomplete", response, nil
	}
	return "response.completed", b.envelope("completed"), nil
}

// maxChatLine bounds one upstream SSE line.
const maxChatLine = 8 << 20

// relayChatAsResponses reads a successful chat completions answer, feeds the
// raw bytes to the usage meter and writes the Responses equivalent: SSE events
// when the client streamed, else one response object.
func relayChatAsResponses(w http.ResponseWriter, upstream *http.Response, translation *chatTranslation, meter *usageMeter, now time.Time) error {
	header := w.Header()
	header.Del("Content-Length")
	header.Del("Content-Encoding")
	builder := newResponsesBuilder(translation, now)
	controller := http.NewResponseController(w)
	upstreamSSE := strings.HasPrefix(strings.ToLower(strings.TrimSpace(upstream.Header.Get("Content-Type"))), "text/event-stream")

	if translation.stream {
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		builder.emit = func(event string, payload map[string]any) error {
			raw, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
				return err
			}
			if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
				return err
			}
			return nil
		}
	} else {
		header.Set("Content-Type", "application/json")
	}
	w.WriteHeader(upstream.StatusCode)

	ended, readErr := false, error(nil)
	if upstreamSSE {
		reader := bufio.NewReaderSize(upstream.Body, 64<<10)
		var data bytes.Buffer
		for {
			line, err := readLine(reader)
			if len(line) > 0 || err == nil {
				if meter != nil {
					meter.Write(line)
					meter.Write([]byte("\n"))
				}
				trimmed := bytes.TrimRight(line, "\r")
				if len(trimmed) == 0 {
					if done, applyErr := applySSEData(builder, data.Bytes()); applyErr != nil {
						return applyErr
					} else if done {
						ended = true
					}
					data.Reset()
				} else if payload, ok := bytes.CutPrefix(trimmed, []byte("data:")); ok {
					if data.Len() > 0 {
						data.WriteByte('\n')
					}
					data.Write(bytes.TrimPrefix(payload, []byte(" ")))
				}
			}
			if err != nil {
				if done, applyErr := applySSEData(builder, data.Bytes()); applyErr != nil {
					return applyErr
				} else if done {
					ended = true
				}
				if !errors.Is(err, io.EOF) {
					readErr = err
				}
				break
			}
		}
		// Some providers end the stream after the finish chunk without [DONE].
		if !ended && readErr == nil && builder.finish != "" {
			ended = true
		}
	} else {
		raw, err := io.ReadAll(io.LimitReader(upstream.Body, maxChatLine*4))
		if meter != nil {
			meter.Write(raw)
		}
		var chunk chatChunk
		if err != nil {
			readErr = err
		} else if json.Unmarshal(raw, &chunk) != nil {
			builder.failed = "The model provider returned an unreadable response"
		} else if err := builder.apply(&chunk); err != nil {
			return err
		}
		ended = readErr == nil
	}

	event, response, err := builder.finishResponse(ended)
	if err != nil {
		return err
	}
	if translation.stream {
		if err := builder.send(event, map[string]any{"response": response}); err != nil {
			return err
		}
		return readErr
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		return err
	}
	return readErr
}

// applySSEData applies one SSE data payload; it reports [DONE].
func applySSEData(builder *responsesBuilder, data []byte) (bool, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return false, nil
	}
	if string(data) == "[DONE]" {
		return true, nil
	}
	var chunk chatChunk
	if json.Unmarshal(data, &chunk) != nil {
		return false, nil
	}
	return false, builder.apply(&chunk)
}

// readLine reads one line without its newline, bounded by maxChatLine (the
// rest of an oversized line is skipped).
func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) <= maxChatLine {
			line = append(line, part...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimSuffix(line, []byte("\n")), err
	}
}
