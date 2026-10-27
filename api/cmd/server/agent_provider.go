package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

type agentProviderMessage struct {
	Role       string
	Content    string
	ToolCalls  []domain.AgentToolCall
	ToolResult *agentProviderToolResult
	// Images are sent as image inputs with a user message.
	Images []agentProviderImage
	// attachments are the stored files of a user message, read into Images and Content by addAgentAttachmentInputs.
	attachments []domain.AgentMessageAttachment
	// Reasoning is the provider's reasoning for an assistant turn (Chat Completions `reasoning_content`). Thinking
	// models served over Chat Completions (DeepSeek, Kimi) refuse a tool-call turn of the current request sent back
	// without it, so it is replayed there; Responses and Anthropic Messages never send it.
	Reasoning string
}

type agentProviderImage struct {
	MediaType string // image/png | image/jpeg | image/gif | image/webp
	Data      string // base64
	Name      string
}

func (image agentProviderImage) dataURL() string {
	return "data:" + image.MediaType + ";base64," + image.Data
}

type agentProviderToolResult struct {
	CallID  string
	Content string
	IsError bool
}

type agentProviderTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Access      string
}

type agentProviderEvent struct {
	Type     string
	Delta    string
	ToolCall *domain.AgentToolCall
}

type agentProviderTurn struct {
	Text       string
	Reasoning  string
	ToolCalls  []domain.AgentToolCall
	StopReason string
	ResponseID string
}

func (s *server) requestAgentTurn(ctx context.Context, messages []agentProviderMessage, emit func(agentProviderEvent) error) (agentProviderTurn, error) {
	if emit == nil {
		emit = func(agentProviderEvent) error { return nil }
	}
	if _, err := s.agentToolDefinitions(); err != nil {
		return agentProviderTurn{}, err
	}
	tools := s.agentTurnTools(ctx)
	// Stored chats can hold calls without outputs (a stopped reply, an open question); providers refuse those.
	messages = sanitizeAgentProviderMessages(messages)
	protocol := s.agent.Protocol
	if protocol == "" {
		protocol = "openai-chat-completions"
	}
	switch protocol {
	case "openai-responses":
		return s.requestOpenAIResponses(ctx, messages, tools, emit)
	case "anthropic-messages":
		return s.requestAnthropicMessages(ctx, messages, tools, emit)
	case "openai-chat-completions":
		return s.requestChatCompletions(ctx, messages, tools, emit)
	default:
		return agentProviderTurn{}, fmt.Errorf("unsupported Agent protocol %q", protocol)
	}
}

// agentTurnTools is the tool list a model turn is offered in ctx.
func (s *server) agentTurnTools(ctx context.Context) []agentProviderTool {
	tools, err := s.agentToolDefinitions()
	if err != nil {
		return nil
	}
	if toolset := agentChatToolsetFrom(ctx); toolset != nil && len(tools) > 0 {
		tools = toolset.tools(tools)
	}
	if connectors, ok := ctx.Value(connectorToolsKey{}).([]connectorTool); ok {
		for _, tool := range connectors {
			tools = append(tools, tool.Definition)
		}
	}
	if extra, ok := ctx.Value(agentExtraToolsKey{}).([]agentProviderTool); ok && len(tools) > 0 {
		tools = append(tools, extra...)
	}
	if len(tools) > 0 {
		tools = append(tools, agentFileToolList(ctx)...)
	}
	if len(tools) > 0 && agentWebToolsEnabled(ctx) {
		tools = append(tools, s.agentWebToolList()...)
	}
	// Loop runs narrow the tool list to what the loop is permitted to use.
	if allow, ok := ctx.Value(agentToolFilterKey{}).(func(agentProviderTool) bool); ok {
		tools = slices.DeleteFunc(tools, func(tool agentProviderTool) bool { return !allow(tool) })
	}
	return tools
}

// agentPromptCacheKey names the prompt cache a Responses request should use (see requestOpenAIResponses).
type agentPromptCacheKey struct{}

// agentToolsWithheldKey marks a turn that must answer without calling tools (tool_choice none): the reply's last
// word once its tool budget is spent. The tools stay in the request because the conversation already holds calls
// to them, which some providers refuse without their definitions.
type agentToolsWithheldKey struct{}

func withAgentToolsWithheld(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentToolsWithheldKey{}, true)
}

func agentToolsWithheld(ctx context.Context) bool {
	withheld, _ := ctx.Value(agentToolsWithheldKey{}).(bool)
	return withheld
}

type agentMaxOutputTokensKey struct{}

// withAgentMaxOutputTokens raises the output budget for one call. Reasoning
// models spend part of it thinking, so structured replies can otherwise be cut off.
func withAgentMaxOutputTokens(ctx context.Context, tokens int) context.Context {
	return context.WithValue(ctx, agentMaxOutputTokensKey{}, tokens)
}

func (s *server) agentMaxOutputTokens(ctx context.Context) int {
	if tokens, ok := ctx.Value(agentMaxOutputTokensKey{}).(int); ok && tokens > s.agent.MaxOutputTokens {
		return tokens
	}
	return s.agent.MaxOutputTokens
}

func (s *server) requestAgentTurnWithoutTools(ctx context.Context, messages []agentProviderMessage) (agentProviderTurn, error) {
	emit := func(agentProviderEvent) error { return nil }
	messages = sanitizeAgentProviderMessages(messages)
	protocol := s.agent.Protocol
	if protocol == "" {
		protocol = "openai-chat-completions"
	}
	switch protocol {
	case "openai-responses":
		return s.requestOpenAIResponses(ctx, messages, nil, emit)
	case "anthropic-messages":
		return s.requestAnthropicMessages(ctx, messages, nil, emit)
	case "openai-chat-completions":
		return s.requestChatCompletions(ctx, messages, nil, emit)
	default:
		return agentProviderTurn{}, fmt.Errorf("unsupported Agent protocol %q", protocol)
	}
}

func (s *server) agentToolDefinitions() ([]agentProviderTool, error) {
	if !s.agent.ToolsEnabled {
		return nil, nil
	}
	var inventory []flowMCPTool
	if err := json.Unmarshal(flowMCPToolInventory, &inventory); err != nil {
		return nil, fmt.Errorf("could not load Flow Agent tools")
	}
	tools := make([]agentProviderTool, 0, len(inventory))
	for _, item := range inventory {
		if item.Access == "write" && !s.agent.WriteTools {
			continue
		}
		name := strings.TrimPrefix(item.Name, "mcp__flow.")
		if agentMCPOnlyTools[name] {
			continue
		}
		tools = append(tools, agentProviderTool{Name: name, Description: item.Description, Parameters: item.InputSchema, Access: item.Access})
	}
	return append(tools, agentProgressToolDefinition), nil
}

// agentMCPOnlyTools are Flow tools offered to MCP clients and worker runs (/mcp) but not to the in-process agent,
// whose tool set stays as it was until the worker runtime replaces it.
var agentMCPOnlyTools = map[string]bool{"count_issues": true, "list_agent_sessions": true, "get_agent_session": true, "archive_agent_session": true}

// agentProgressTool lets the model narrate its plan the way the reference app's agent does ("Reviewing project activity").
// It never touches data: chat sessions render it as a step title, other runners simply acknowledge it.
const agentProgressTool = "report_progress"

var agentProgressToolDefinition = agentProviderTool{
	Name:        agentProgressTool,
	Description: "Show the user what you are doing. Call it in the same turn as the lookups it describes, before each distinct phase of work. title: 2-5 words starting with a present participle, e.g. \"Reviewing project activity\" or \"Gathering project updates\". message: optional, only on the first call — one or two sentences on your plan, in the user's language.",
	Parameters:  json.RawMessage(`{"type":"object","required":["title"],"properties":{"title":{"type":"string","description":"2-5 word present-participle title"},"message":{"type":"string","description":"Optional one or two sentence plan"}},"additionalProperties":false}`),
	Access:      "read",
}

// agentReasoning is the Responses API reasoning block: summaries always, effort when configured.
func (s *server) agentReasoning() map[string]any {
	reasoning := map[string]any{"summary": "auto"}
	if s.agent.ReasoningEffort != "" {
		reasoning["effort"] = s.agent.ReasoningEffort
	}
	return reasoning
}

func (s *server) requestOpenAIResponses(ctx context.Context, messages []agentProviderMessage, tools []agentProviderTool, emit func(agentProviderEvent) error) (agentProviderTurn, error) {
	instructions, input := responsesInput(messages)
	payload := map[string]any{
		"model": s.agent.Model, "instructions": instructions, "input": input, "stream": true,
		"max_output_tokens": s.agentMaxOutputTokens(ctx), "store": false,
		"reasoning": s.agentReasoning(),
	}
	if len(tools) > 0 {
		payload["tools"] = mapTools(tools, func(tool agentProviderTool, parameters any) any {
			return map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": parameters}
		})
	}
	// Without a search provider, a web-enabled turn searches with the provider's hosted tool.
	native := len(tools) > 0 && agentWebToolsEnabled(ctx) && s.agentNativeWebSearch()
	if native {
		payload["tools"] = append(payload["tools"].([]any), map[string]any{"type": "web_search"})
	}
	// Requests sharing a cache key are routed to where their common prefix (tools and instructions) is already
	// processed. Without it this provider cached nothing, and every turn re-read ~15k tokens of tools.
	cacheKey, _ := ctx.Value(agentPromptCacheKey{}).(string)
	if cacheKey != "" && !s.agentPromptCacheOff.Load() {
		payload["prompt_cache_key"] = cacheKey
	}
	if len(tools) > 0 && agentToolsWithheld(ctx) {
		payload["tool_choice"] = "none"
	}
	response, err := s.agentProviderRequest(ctx, "/responses", payload, false)
	var providerErr *agentProviderError
	if payload["prompt_cache_key"] != nil && errors.As(err, &providerErr) && providerErr.status == http.StatusBadRequest && strings.Contains(providerErr.message, "prompt_cache_key") {
		// An OpenAI-compatible gateway that doesn't know the field: drop it from now on.
		s.agentPromptCacheOff.Store(true)
		delete(payload, "prompt_cache_key")
		response, err = s.agentProviderRequest(ctx, "/responses", payload, false)
	}
	if native && errors.As(err, &providerErr) && providerErr.status == http.StatusBadRequest && strings.Contains(strings.ToLower(providerErr.message), "web_search") {
		// A gateway without hosted web search: fetch_url still works; stop offering search from now on.
		s.agentNativeWebSearchOff.Store(true)
		payload["tools"] = slices.DeleteFunc(payload["tools"].([]any), func(tool any) bool {
			item, ok := tool.(map[string]any)
			return ok && item["type"] == "web_search"
		})
		response, err = s.agentProviderRequest(ctx, "/responses", payload, false)
	}
	if err != nil {
		return agentProviderTurn{}, err
	}
	defer response.Body.Close()
	if !isEventStream(response) {
		turn, decodeErr := decodeOpenAIResponse(response)
		return turn, emitDecodedAgentTurn(turn, decodeErr, emit)
	}
	turn := agentProviderTurn{}
	calls := map[string]*domain.AgentToolCall{}
	order := []string{}
	err = readSSE(response.Body, func(_ string, data string) error {
		if data == "[DONE]" {
			return nil
		}
		var event struct {
			Type      string `json:"type"`
			Delta     string `json:"delta"`
			ItemID    string `json:"item_id"`
			Arguments string `json:"arguments"`
			Item      struct {
				ID        string `json:"id"`
				CallID    string `json:"call_id"`
				Type      string `json:"type"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Action    struct {
					Query   string   `json:"query"`
					Queries []string `json:"queries"`
				} `json:"action"`
			} `json:"item"`
			Response struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Usage  *struct {
					InputTokens        int `json:"input_tokens"`
					OutputTokens       int `json:"output_tokens"`
					InputTokensDetails struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"input_tokens_details"`
					OutputTokensDetails struct {
						ReasoningTokens int `json:"reasoning_tokens"`
					} `json:"output_tokens_details"`
				} `json:"usage"`
				Error *struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			// Some gateways put the error fields at the top level of an "error" event.
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil
		}
		switch event.Type {
		case "response.created", "response.in_progress":
			if event.Response.ID != "" {
				turn.ResponseID = event.Response.ID
			}
		case "response.output_text.delta":
			turn.Text += event.Delta
			return emit(agentProviderEvent{Type: "text.delta", Delta: event.Delta})
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			turn.Reasoning += event.Delta
			return emit(agentProviderEvent{Type: "reasoning.delta", Delta: event.Delta})
		case "response.output_item.added", "response.output_item.done":
			if event.Item.Type == "web_search_call" {
				// Hosted search runs on the provider's side; it shows as a step, never as a call to execute.
				status := "running"
				if event.Type == "response.output_item.done" {
					status = "completed"
				}
				arguments, _ := json.Marshal(map[string]any{"query": firstNonEmpty(event.Item.Action.Query, strings.Join(event.Item.Action.Queries, ", "))})
				return emit(agentProviderEvent{Type: "tool.hosted", ToolCall: &domain.AgentToolCall{ID: agentHostedToolPrefix + event.Item.ID, Name: webSearchToolName, Arguments: arguments, Status: status}})
			}
			if event.Type == "response.output_item.done" {
				return nil
			}
			if event.Item.Type == "function_call" {
				id := firstNonEmpty(event.Item.CallID, event.Item.ID)
				call := &domain.AgentToolCall{ID: id, Name: event.Item.Name, Arguments: json.RawMessage(event.Item.Arguments), Status: "running"}
				calls[event.Item.ID] = call
				calls[id] = call
				order = append(order, id)
				return emit(agentProviderEvent{Type: "tool.started", ToolCall: cloneToolCall(call)})
			}
		case "response.function_call_arguments.delta":
			if call := calls[event.ItemID]; call != nil {
				call.Arguments = append(call.Arguments, event.Delta...)
				return emit(agentProviderEvent{Type: "tool.delta", Delta: event.Delta, ToolCall: cloneToolCall(call)})
			}
		case "response.function_call_arguments.done":
			if call := calls[event.ItemID]; call != nil && event.Arguments != "" {
				call.Arguments = json.RawMessage(event.Arguments)
			}
		case "response.completed":
			turn.StopReason = event.Response.Status
			if usage := event.Response.Usage; usage != nil {
				slog.InfoContext(ctx, "agent provider usage", "model", s.agent.Model, "input_tokens", usage.InputTokens, "cached_tokens", usage.InputTokensDetails.CachedTokens, "output_tokens", usage.OutputTokens, "reasoning_tokens", usage.OutputTokensDetails.ReasoningTokens, "tools", len(tools))
			}
		case "response.failed", "error":
			message, code := "OpenAI Responses stream failed", event.Code
			if event.Error != nil && event.Error.Message != "" {
				message, code = event.Error.Message, event.Error.Code
			} else if event.Response.Error != nil && event.Response.Error.Message != "" {
				message, code = event.Response.Error.Message, event.Response.Error.Code
			} else if event.Message != "" {
				message = event.Message
			}
			// Server-side failures and rate limits may succeed when the turn is asked again.
			transient := code == "server_error" || code == "rate_limit_exceeded" || code == "server_is_overloaded" || code == "slow_down"
			return &agentProviderError{message: message, transient: transient}
		}
		return nil
	})
	turn.ToolCalls = orderedToolCalls(calls, order)
	return turn, err
}

func (s *server) requestChatCompletions(ctx context.Context, messages []agentProviderMessage, tools []agentProviderTool, emit func(agentProviderEvent) error) (agentProviderTurn, error) {
	reasoningKey := s.agent.BaseURL + "\x00" + s.agent.Model
	_, replay := agentChatReasoningProviders.Load(reasoningKey)
	payload := map[string]any{"model": s.agent.Model, "messages": chatMessagesReplaying(messages, replay), "stream": true, "max_tokens": s.agentMaxOutputTokens(ctx)}
	if s.agent.ReasoningEffort != "" {
		payload["reasoning_effort"] = s.agent.ReasoningEffort
	}
	if len(tools) > 0 {
		payload["tools"] = mapTools(tools, func(tool agentProviderTool, parameters any) any {
			return map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": parameters}}
		})
		if agentToolsWithheld(ctx) {
			payload["tool_choice"] = "none"
		}
	}
	response, err := s.agentProviderRequest(ctx, "/chat/completions", payload, false)
	if !replay && agentReasoningReplayRequired(err) {
		// A thinking model wants every tool-call turn of the current request back with its reasoning, including
		// turns Flow rebuilt (an approval's held calls, a stored chat). Remember the provider and send them again
		// with reasoning_content ("" where it was not kept), which it accepts.
		agentChatReasoningProviders.Store(reasoningKey, true)
		payload["messages"] = chatMessagesReplaying(messages, true)
		response, err = s.agentProviderRequest(ctx, "/chat/completions", payload, false)
	}
	if err != nil {
		return agentProviderTurn{}, err
	}
	defer response.Body.Close()
	if !isEventStream(response) {
		turn, decodeErr := decodeChatResponse(response)
		if turn.Reasoning != "" {
			agentChatReasoningProviders.Store(reasoningKey, true)
		}
		return turn, emitDecodedAgentTurn(turn, decodeErr, emit)
	}
	turn := agentProviderTurn{}
	calls := map[int]*domain.AgentToolCall{}
	// slots maps a delta index to the call it currently continues (see the parallel calls without an index below).
	slots := map[int]int{}
	err = readSSE(response.Body, func(_ string, data string) error {
		if data == "[DONE]" {
			return nil
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int                              `json:"index"`
						ID       string                           `json:"id"`
						Function struct{ Name, Arguments string } `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil
		}
		if event.Error != nil {
			return errors.New(event.Error.Message)
		}
		if len(event.Choices) == 0 {
			return nil
		}
		choice := event.Choices[0]
		if choice.Delta.Content != "" {
			turn.Text += choice.Delta.Content
			if err := emit(agentProviderEvent{Type: "text.delta", Delta: choice.Delta.Content}); err != nil {
				return err
			}
		}
		if choice.Delta.ReasoningContent != "" {
			if turn.Reasoning == "" {
				agentChatReasoningProviders.Store(reasoningKey, true)
			}
			turn.Reasoning += choice.Delta.ReasoningContent
			if err := emit(agentProviderEvent{Type: "reasoning.delta", Delta: choice.Delta.ReasoningContent}); err != nil {
				return err
			}
		}
		for _, delta := range choice.Delta.ToolCalls {
			// Some gateways stream parallel calls without an index (all 0): a chunk with a different call id starts
			// a new call instead of being appended to the previous one's name and arguments.
			index, ok := slots[delta.Index]
			if !ok {
				index = delta.Index
			}
			call := calls[index]
			if call != nil && delta.ID != "" && call.ID != "" && delta.ID != call.ID {
				index = len(calls)
				for calls[index] != nil {
					index++
				}
				slots[delta.Index], call = index, nil
			}
			started := call == nil
			if started {
				call = &domain.AgentToolCall{ID: delta.ID, Status: "running"}
				calls[index] = call
			}
			if delta.ID != "" {
				call.ID = delta.ID
			}
			call.Name += delta.Function.Name
			call.Arguments = append(call.Arguments, delta.Function.Arguments...)
			// The first chunk carries the call's name: announce the call with it, so its row reads "Creating issue…"
			// rather than a nameless "Running tool…" (and load_tools is recognized and kept out of view).
			if started {
				if err := emit(agentProviderEvent{Type: "tool.started", ToolCall: cloneToolCall(call)}); err != nil {
					return err
				}
			}
			if delta.Function.Name != "" || delta.Function.Arguments != "" {
				if err := emit(agentProviderEvent{Type: "tool.delta", Delta: delta.Function.Arguments, ToolCall: cloneToolCall(call)}); err != nil {
					return err
				}
			}
		}
		if choice.FinishReason != "" {
			turn.StopReason = choice.FinishReason
		}
		return nil
	})
	turn.ToolCalls = indexedToolCalls(calls)
	if err == nil && turn.StopReason == "tool_calls" && len(turn.ToolCalls) == 0 {
		// The provider says the model called tools but none arrived in a shape Flow reads.
		slog.WarnContext(ctx, "agent provider finished with tool_calls but sent no tool calls", "model", s.agent.Model, "text_bytes", len(turn.Text), "reasoning_bytes", len(turn.Reasoning))
	}
	return turn, err
}

func (s *server) requestAnthropicMessages(ctx context.Context, messages []agentProviderMessage, tools []agentProviderTool, emit func(agentProviderEvent) error) (agentProviderTurn, error) {
	system, input := anthropicMessages(messages)
	payload := map[string]any{"model": s.agent.Model, "system": system, "messages": input, "stream": true, "max_tokens": s.agentMaxOutputTokens(ctx)}
	if len(tools) > 0 {
		payload["tools"] = mapTools(tools, func(tool agentProviderTool, parameters any) any {
			return map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": parameters}
		})
	}
	// Without a search provider, a web-enabled turn searches with Claude's web search server tool.
	native := len(tools) > 0 && agentWebToolsEnabled(ctx) && s.agentNativeWebSearch()
	if native {
		payload["tools"] = append(payload["tools"].([]any), map[string]any{"type": "web_search_20250305", "name": webSearchToolName, "max_uses": 5})
	}
	if len(tools) > 0 && agentToolsWithheld(ctx) {
		payload["tool_choice"] = map[string]any{"type": "none"}
	}
	response, err := s.agentProviderRequest(ctx, "/messages", payload, true)
	var providerErr *agentProviderError
	if native && errors.As(err, &providerErr) && providerErr.status == http.StatusBadRequest && strings.Contains(strings.ToLower(providerErr.message), "web_search") {
		s.agentNativeWebSearchOff.Store(true)
		payload["tools"] = slices.DeleteFunc(payload["tools"].([]any), func(tool any) bool {
			item, ok := tool.(map[string]any)
			return ok && item["type"] == "web_search_20250305"
		})
		response, err = s.agentProviderRequest(ctx, "/messages", payload, true)
	}
	if err != nil {
		return agentProviderTurn{}, err
	}
	defer response.Body.Close()
	if !isEventStream(response) {
		turn, decodeErr := decodeAnthropicResponse(response)
		return turn, emitDecodedAgentTurn(turn, decodeErr, emit)
	}
	turn := agentProviderTurn{}
	calls := map[int]*domain.AgentToolCall{}
	// Hosted web search blocks by stream index, and their streamed query.
	hosted := map[int]string{}
	hostedArgs := map[string]json.RawMessage{}
	err = readSSE(response.Body, func(_ string, data string) error {
		var event struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
				// ToolUseID links a server tool's result to its call (web_search_tool_result).
				ToolUseID string `json:"tool_use_id"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				ID string `json:"id"`
			} `json:"message"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return nil
		}
		switch event.Type {
		case "message_start":
			turn.ResponseID = event.Message.ID
		case "content_block_start":
			switch event.ContentBlock.Type {
			case "server_tool_use":
				hosted[event.Index] = agentHostedToolPrefix + event.ContentBlock.ID
				return emit(agentProviderEvent{Type: "tool.hosted", ToolCall: &domain.AgentToolCall{ID: hosted[event.Index], Name: webSearchToolName, Status: "running"}})
			case "web_search_tool_result":
				id := agentHostedToolPrefix + event.ContentBlock.ToolUseID
				return emit(agentProviderEvent{Type: "tool.hosted", ToolCall: &domain.AgentToolCall{ID: id, Name: webSearchToolName, Arguments: hostedArgs[id], Status: "completed"}})
			}
			if event.ContentBlock.Type == "tool_use" {
				arguments := event.ContentBlock.Input
				if strings.TrimSpace(string(arguments)) == "{}" {
					arguments = nil
				}
				call := &domain.AgentToolCall{ID: event.ContentBlock.ID, Name: event.ContentBlock.Name, Arguments: arguments, Status: "running"}
				calls[event.Index] = call
				return emit(agentProviderEvent{Type: "tool.started", ToolCall: cloneToolCall(call)})
			}
		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				turn.Text += event.Delta.Text
				return emit(agentProviderEvent{Type: "text.delta", Delta: event.Delta.Text})
			case "thinking_delta":
				turn.Reasoning += event.Delta.Thinking
				return emit(agentProviderEvent{Type: "reasoning.delta", Delta: event.Delta.Thinking})
			case "input_json_delta":
				if id := hosted[event.Index]; id != "" {
					hostedArgs[id] = append(hostedArgs[id], event.Delta.PartialJSON...)
					return nil
				}
				if call := calls[event.Index]; call != nil {
					call.Arguments = append(call.Arguments, event.Delta.PartialJSON...)
					return emit(agentProviderEvent{Type: "tool.delta", Delta: event.Delta.PartialJSON, ToolCall: cloneToolCall(call)})
				}
			}
		case "message_delta":
			turn.StopReason = event.Delta.StopReason
		case "error":
			if event.Error != nil {
				return errors.New(event.Error.Message)
			}
			return errors.New("Anthropic Messages stream failed")
		}
		return nil
	})
	turn.ToolCalls = indexedToolCalls(calls)
	return turn, err
}

func (s *server) agentProviderRequest(ctx context.Context, suffix string, payload any, anthropic bool) (*http.Response, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("could not encode Agent request")
	}
	endpoint := agentEndpoint(s.agent.BaseURL, suffix)
	client := s.agentClient
	if client == nil {
		client = newAgentHTTPClient()
	}
	// FLOW_AGENT_TIMEOUT is an idle timeout: a streamed answer may run as long as the provider keeps sending
	// (reasoning models with tools routinely take more than a minute per turn), but not stall for that long.
	// Only a client that sets its own whole-request Timeout opts out.
	requestCtx, cancel := context.WithCancelCause(ctx)
	var idle *time.Timer
	if client.Timeout == 0 && s.agent.Timeout > 0 {
		idle = time.AfterFunc(s.agent.Timeout, func() { cancel(errAgentProviderIdle) })
	}
	stop := func() {
		if idle != nil {
			idle.Stop()
		}
		cancel(nil)
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		stop()
		return nil, fmt.Errorf("could not create Agent request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if anthropic {
		if s.agent.APIKey != "" {
			request.Header.Set("x-api-key", s.agent.APIKey)
		}
		request.Header.Set("anthropic-version", s.agent.AnthropicVersion)
	} else if s.agent.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+s.agent.APIKey)
	}
	started := time.Now()
	logAttrs := []any{"endpoint", suffix, "model", s.agent.Model, "request_bytes", len(raw)}
	response, err := client.Do(request)
	if err != nil {
		idled := errors.Is(context.Cause(requestCtx), errAgentProviderIdle)
		stop()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var netErr net.Error
		providerErr := &agentProviderError{message: "Flow Agent provider is unavailable", transient: true, timeout: idled || errors.As(err, &netErr) && netErr.Timeout()}
		slog.WarnContext(ctx, "agent provider request failed", append(logAttrs, "duration_ms", time.Since(started).Milliseconds(), "timeout", providerErr.timeout, "error", err.Error())...)
		return nil, providerErr
	}
	headers := time.Since(started)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		defer stop()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		status := response.StatusCode
		slog.WarnContext(ctx, "agent provider request failed", append(logAttrs, "status", status, "duration_ms", headers.Milliseconds())...)
		return nil, &agentProviderError{message: fmt.Sprintf("Flow Agent provider returned status %d: %s", status, providerError(body)), status: status, transient: status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout, timeout: status == http.StatusGatewayTimeout || status == http.StatusRequestTimeout}
	}
	response.Body = &agentIdleBody{ReadCloser: response.Body, ctx: requestCtx, idle: idle, timeout: s.agent.Timeout, stop: stop, logCtx: ctx, logAttrs: append(logAttrs, "status", response.StatusCode, "headers_ms", headers.Milliseconds()), started: started}
	return response, nil
}

var errAgentProviderIdle = errors.New("Flow Agent provider stopped responding")

// newAgentHTTPClient is the provider client. It must not set http.Client.Timeout: that caps the whole streamed
// response, so a long but healthy turn would be cut off mid-answer ("context deadline exceeded … while reading
// body"). agentProviderRequest enforces FLOW_AGENT_TIMEOUT as an idle timeout instead.
func newAgentHTTPClient() *http.Client {
	return &http.Client{}
}

// agentIdleBody is a provider response body that restarts the idle timer whenever data arrives and releases the
// request when closed.
type agentIdleBody struct {
	io.ReadCloser
	ctx     context.Context
	idle    *time.Timer
	timeout time.Duration
	stop    func()
	// Timing for the "agent provider response" log line, so slow or stalled providers are visible in the API logs.
	logCtx    context.Context
	logAttrs  []any
	started   time.Time
	firstByte time.Duration
	bytes     int
	idled     bool
	closed    bool
}

func (b *agentIdleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if b.bytes == 0 {
			b.firstByte = time.Since(b.started)
		}
		b.bytes += n
		if b.idle != nil {
			b.idle.Reset(b.timeout)
		}
	}
	if err != nil && err != io.EOF && errors.Is(context.Cause(b.ctx), errAgentProviderIdle) {
		b.idled = true
		return n, &agentProviderError{message: errAgentProviderIdle.Error(), transient: true, timeout: true}
	}
	return n, err
}

func (b *agentIdleBody) Close() error {
	err := b.ReadCloser.Close()
	b.stop()
	if !b.closed && b.logCtx != nil {
		b.closed = true
		attrs := append(b.logAttrs, "first_byte_ms", b.firstByte.Milliseconds(), "duration_ms", time.Since(b.started).Milliseconds(), "response_bytes", b.bytes)
		if b.idled {
			slog.WarnContext(b.logCtx, "agent provider response stalled", append(attrs, "idle_timeout_ms", b.timeout.Milliseconds())...)
		} else {
			slog.InfoContext(b.logCtx, "agent provider response", attrs...)
		}
	}
	return err
}

// agentProviderError is a failed provider request. Transient failures (no
// connection, timeouts, 429 and 5xx) may succeed when the request is retried.
type agentProviderError struct {
	message   string
	status    int
	transient bool
	timeout   bool
}

func (e *agentProviderError) Error() string { return e.message }

func responsesInput(messages []agentProviderMessage) (string, []any) {
	instructions := ""
	input := []any{}
	for _, message := range messages {
		if message.Role == "system" {
			if instructions == "" {
				instructions = message.Content
			} else {
				instructions += "\n\n" + message.Content
			}
			continue
		}
		if len(message.Images) > 0 && message.Role == "user" {
			content := []any{map[string]any{"type": "input_text", "text": message.Content}}
			for _, image := range message.Images {
				content = append(content, map[string]any{"type": "input_image", "image_url": image.dataURL()})
			}
			input = append(input, map[string]any{"role": message.Role, "content": content})
		} else if message.Content != "" {
			input = append(input, map[string]any{"role": message.Role, "content": message.Content})
		}
		for _, call := range message.ToolCalls {
			input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
		}
		if message.ToolResult != nil {
			input = append(input, map[string]any{"type": "function_call_output", "call_id": message.ToolResult.CallID, "output": message.ToolResult.Content})
		}
	}
	return instructions, input
}

// agentChatReasoningProviders holds the Chat Completions providers (base URL and model) seen returning
// reasoning_content or asking for it back; their assistant tool-call turns always carry the field.
var agentChatReasoningProviders sync.Map

// agentReasoningReplayRequired reports whether a provider refused a request because an assistant tool-call turn
// came without its reasoning_content (DeepSeek: "The `reasoning_content` in the thinking mode must be passed back").
func agentReasoningReplayRequired(err error) bool {
	var providerErr *agentProviderError
	return errors.As(err, &providerErr) && providerErr.status == http.StatusBadRequest && strings.Contains(providerErr.message, "reasoning_content")
}

func chatMessages(messages []agentProviderMessage) []any {
	return chatMessagesReplaying(messages, false)
}

// chatMessagesReplaying encodes messages for Chat Completions. An assistant turn's kept reasoning is always sent
// back as reasoning_content; with replayReasoning every assistant tool-call turn carries the field, empty when its
// reasoning was not kept (thinking models accept "" but refuse a missing field).
func chatMessagesReplaying(messages []agentProviderMessage, replayReasoning bool) []any {
	result := []any{}
	for _, message := range messages {
		if message.ToolResult != nil {
			result = append(result, map[string]any{"role": "tool", "tool_call_id": message.ToolResult.CallID, "content": message.ToolResult.Content})
			continue
		}
		item := map[string]any{"role": message.Role, "content": message.Content}
		if len(message.Images) > 0 && message.Role == "user" {
			content := []any{map[string]any{"type": "text", "text": message.Content}}
			for _, image := range message.Images {
				content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image.dataURL()}})
			}
			item["content"] = content
		}
		if len(message.ToolCalls) > 0 {
			calls := []any{}
			for _, call := range message.ToolCalls {
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
			}
			item["tool_calls"] = calls
		}
		if message.Role == "assistant" && (message.Reasoning != "" || replayReasoning && len(message.ToolCalls) > 0) {
			item["reasoning_content"] = message.Reasoning
		}
		result = append(result, item)
	}
	return result
}

func anthropicMessages(messages []agentProviderMessage) (string, []any) {
	system := ""
	result := []any{}
	for _, message := range messages {
		if message.Role == "system" {
			system += message.Content
			continue
		}
		if message.ToolResult != nil {
			block := map[string]any{"type": "tool_result", "tool_use_id": message.ToolResult.CallID, "content": message.ToolResult.Content, "is_error": message.ToolResult.IsError}
			// All results of one assistant turn go back in a single user message, as the Messages API expects.
			if last := len(result) - 1; last >= 0 && anthropicToolResultMessage(result[last]) {
				previous := result[last].(map[string]any)
				previous["content"] = append(previous["content"].([]any), block)
				continue
			}
			result = append(result, map[string]any{"role": "user", "content": []any{block}})
			continue
		}
		content := []any{}
		if message.Content != "" {
			content = append(content, map[string]any{"type": "text", "text": message.Content})
		}
		if message.Role == "user" {
			for _, image := range message.Images {
				content = append(content, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": image.MediaType, "data": image.Data}})
			}
		}
		for _, call := range message.ToolCalls {
			var input any = map[string]any{}
			_ = json.Unmarshal(call.Arguments, &input)
			content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": input})
		}
		result = append(result, map[string]any{"role": message.Role, "content": content})
	}
	return system, result
}

// anthropicToolResultMessage reports whether an encoded message is a user message holding only tool results.
func anthropicToolResultMessage(value any) bool {
	message, ok := value.(map[string]any)
	if !ok || message["role"] != "user" {
		return false
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) == 0 {
		return false
	}
	for _, block := range content {
		if item, ok := block.(map[string]any); !ok || item["type"] != "tool_result" {
			return false
		}
	}
	return true
}

func emitDecodedAgentTurn(turn agentProviderTurn, decodeErr error, emit func(agentProviderEvent) error) error {
	if decodeErr != nil {
		return decodeErr
	}
	if turn.Reasoning != "" {
		if err := emit(agentProviderEvent{Type: "reasoning.delta", Delta: turn.Reasoning}); err != nil {
			return err
		}
	}
	if turn.Text != "" {
		if err := emit(agentProviderEvent{Type: "text.delta", Delta: turn.Text}); err != nil {
			return err
		}
	}
	for index := range turn.ToolCalls {
		call := turn.ToolCalls[index]
		if err := emit(agentProviderEvent{Type: "tool.started", ToolCall: &call}); err != nil {
			return err
		}
	}
	return nil
}

func mapTools(tools []agentProviderTool, mapper func(agentProviderTool, any) any) []any {
	result := make([]any, 0, len(tools))
	for _, tool := range tools {
		var parameters any = map[string]any{"type": "object", "properties": map[string]any{}}
		if len(tool.Parameters) > 0 {
			_ = json.Unmarshal(tool.Parameters, &parameters)
		}
		result = append(result, mapper(tool, parameters))
	}
	return result
}

func readSSE(reader io.Reader, visit func(event, data string) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	event := ""
	data := []string{}
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := visit(event, strings.Join(data, "\n"))
		event, data = "", data[:0]
		return err
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		var providerErr *agentProviderError
		if errors.As(err, &providerErr) {
			return providerErr
		}
		return fmt.Errorf("could not read Agent stream: %w", err)
	}
	return flush()
}

func decodeOpenAIResponse(response *http.Response) (agentProviderTurn, error) {
	var decoded struct {
		ID         string `json:"id"`
		OutputText string `json:"output_text"`
		Output     []struct {
			Type      string                        `json:"type"`
			ID        string                        `json:"id"`
			CallID    string                        `json:"call_id"`
			Name      string                        `json:"name"`
			Arguments string                        `json:"arguments"`
			Content   []struct{ Type, Text string } `json:"content"`
			Summary   []struct{ Type, Text string } `json:"summary"`
		} `json:"output"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&decoded); err != nil {
		return agentProviderTurn{}, fmt.Errorf("Flow Agent provider returned an invalid Responses payload")
	}
	turn := agentProviderTurn{ResponseID: decoded.ID, Text: decoded.OutputText, StopReason: "completed"}
	for _, item := range decoded.Output {
		for _, summary := range item.Summary {
			if summary.Type == "summary_text" {
				turn.Reasoning += summary.Text
			}
		}
		if item.Type == "function_call" {
			turn.ToolCalls = append(turn.ToolCalls, domain.AgentToolCall{ID: firstNonEmpty(item.CallID, item.ID), Name: item.Name, Arguments: json.RawMessage(item.Arguments), Status: "running"})
		}
		for _, content := range item.Content {
			if content.Type == "output_text" {
				turn.Text += content.Text
			}
		}
	}
	return turn, nil
}

func decodeChatResponse(response *http.Response) (agentProviderTurn, error) {
	var decoded struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string                           `json:"id"`
					Function struct{ Name, Arguments string } `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&decoded); err != nil || len(decoded.Choices) == 0 {
		return agentProviderTurn{}, fmt.Errorf("Flow Agent provider returned an invalid Chat Completions payload")
	}
	choice := decoded.Choices[0]
	turn := agentProviderTurn{Text: choice.Message.Content, Reasoning: choice.Message.ReasoningContent, StopReason: choice.FinishReason}
	for _, item := range choice.Message.ToolCalls {
		turn.ToolCalls = append(turn.ToolCalls, domain.AgentToolCall{ID: item.ID, Name: item.Function.Name, Arguments: json.RawMessage(item.Function.Arguments), Status: "running"})
	}
	return turn, nil
}

func decodeAnthropicResponse(response *http.Response) (agentProviderTurn, error) {
	var decoded struct {
		ID         string `json:"id"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type     string          `json:"type"`
			Text     string          `json:"text"`
			Thinking string          `json:"thinking"`
			ID       string          `json:"id"`
			Name     string          `json:"name"`
			Input    json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&decoded); err != nil {
		return agentProviderTurn{}, fmt.Errorf("Flow Agent provider returned an invalid Anthropic Messages payload")
	}
	turn := agentProviderTurn{ResponseID: decoded.ID, StopReason: decoded.StopReason}
	for _, item := range decoded.Content {
		switch item.Type {
		case "text":
			turn.Text += item.Text
		case "thinking":
			turn.Reasoning += item.Thinking
		case "tool_use":
			turn.ToolCalls = append(turn.ToolCalls, domain.AgentToolCall{ID: item.ID, Name: item.Name, Arguments: item.Input, Status: "running"})
		}
	}
	return turn, nil
}

func isEventStream(response *http.Response) bool {
	return strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
}

func agentEndpoint(baseURL, suffix string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, suffix) {
		return base
	}
	return base + suffix
}

func providerError(body []byte) string {
	var decoded struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &decoded) == nil && decoded.Error.Message != "" {
		return decoded.Error.Message
	}
	message := strings.TrimSpace(string(body))
	if len(message) > 300 {
		message = message[:300]
	}
	return message
}

func orderedToolCalls(calls map[string]*domain.AgentToolCall, order []string) []domain.AgentToolCall {
	result := make([]domain.AgentToolCall, 0, len(order))
	seen := map[*domain.AgentToolCall]bool{}
	for _, id := range order {
		if call := calls[id]; call != nil && !seen[call] {
			seen[call] = true
			result = append(result, *cloneToolCall(call))
		}
	}
	return result
}

func indexedToolCalls(calls map[int]*domain.AgentToolCall) []domain.AgentToolCall {
	result := make([]domain.AgentToolCall, 0, len(calls))
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		if call := calls[index]; call != nil {
			result = append(result, *cloneToolCall(call))
		}
	}
	return result
}

func cloneToolCall(call *domain.AgentToolCall) *domain.AgentToolCall {
	if call == nil {
		return nil
	}
	clone := *call
	clone.Arguments = append(json.RawMessage(nil), call.Arguments...)
	clone.Result = append(json.RawMessage(nil), call.Result...)
	return &clone
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
