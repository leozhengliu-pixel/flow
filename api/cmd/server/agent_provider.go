package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"flow/api/internal/domain"
)

type agentProviderMessage struct {
	Role       string
	Content    string
	ToolCalls  []domain.AgentToolCall
	ToolResult *agentProviderToolResult
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
	tools, err := s.agentToolDefinitions()
	if err != nil {
		return agentProviderTurn{}, err
	}
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

func (s *server) requestAgentTurnWithoutTools(ctx context.Context, messages []agentProviderMessage) (agentProviderTurn, error) {
	emit := func(agentProviderEvent) error { return nil }
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
		tools = append(tools, agentProviderTool{Name: name, Description: item.Description, Parameters: item.InputSchema, Access: item.Access})
	}
	return tools, nil
}

func (s *server) requestOpenAIResponses(ctx context.Context, messages []agentProviderMessage, tools []agentProviderTool, emit func(agentProviderEvent) error) (agentProviderTurn, error) {
	instructions, input := responsesInput(messages)
	payload := map[string]any{
		"model": s.agent.Model, "instructions": instructions, "input": input, "stream": true,
		"max_output_tokens": s.agent.MaxOutputTokens, "store": false,
		"reasoning": map[string]any{"summary": "auto"},
	}
	if len(tools) > 0 {
		payload["tools"] = mapTools(tools, func(tool agentProviderTool, parameters any) any {
			return map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": parameters}
		})
	}
	response, err := s.agentProviderRequest(ctx, "/responses", payload, false)
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
			} `json:"item"`
			Response struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
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
		case "response.output_item.added":
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
		case "response.failed", "error":
			message := "OpenAI Responses stream failed"
			if event.Error != nil && event.Error.Message != "" {
				message = event.Error.Message
			} else if event.Response.Error != nil && event.Response.Error.Message != "" {
				message = event.Response.Error.Message
			}
			return errors.New(message)
		}
		return nil
	})
	turn.ToolCalls = orderedToolCalls(calls, order)
	return turn, err
}

func (s *server) requestChatCompletions(ctx context.Context, messages []agentProviderMessage, tools []agentProviderTool, emit func(agentProviderEvent) error) (agentProviderTurn, error) {
	payload := map[string]any{"model": s.agent.Model, "messages": chatMessages(messages), "stream": true, "max_tokens": s.agent.MaxOutputTokens}
	if len(tools) > 0 {
		payload["tools"] = mapTools(tools, func(tool agentProviderTool, parameters any) any {
			return map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": parameters}}
		})
	}
	response, err := s.agentProviderRequest(ctx, "/chat/completions", payload, false)
	if err != nil {
		return agentProviderTurn{}, err
	}
	defer response.Body.Close()
	if !isEventStream(response) {
		turn, decodeErr := decodeChatResponse(response)
		return turn, emitDecodedAgentTurn(turn, decodeErr, emit)
	}
	turn := agentProviderTurn{}
	calls := map[int]*domain.AgentToolCall{}
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
			turn.Reasoning += choice.Delta.ReasoningContent
			if err := emit(agentProviderEvent{Type: "reasoning.delta", Delta: choice.Delta.ReasoningContent}); err != nil {
				return err
			}
		}
		for _, delta := range choice.Delta.ToolCalls {
			call := calls[delta.Index]
			if call == nil {
				call = &domain.AgentToolCall{ID: delta.ID, Status: "running"}
				calls[delta.Index] = call
				if err := emit(agentProviderEvent{Type: "tool.started", ToolCall: cloneToolCall(call)}); err != nil {
					return err
				}
			}
			if delta.ID != "" {
				call.ID = delta.ID
			}
			call.Name += delta.Function.Name
			call.Arguments = append(call.Arguments, delta.Function.Arguments...)
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
	return turn, err
}

func (s *server) requestAnthropicMessages(ctx context.Context, messages []agentProviderMessage, tools []agentProviderTool, emit func(agentProviderEvent) error) (agentProviderTurn, error) {
	system, input := anthropicMessages(messages)
	payload := map[string]any{"model": s.agent.Model, "system": system, "messages": input, "stream": true, "max_tokens": s.agent.MaxOutputTokens}
	if len(tools) > 0 {
		payload["tools"] = mapTools(tools, func(tool agentProviderTool, parameters any) any {
			return map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": parameters}
		})
	}
	response, err := s.agentProviderRequest(ctx, "/messages", payload, true)
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
	err = readSSE(response.Body, func(_ string, data string) error {
		var event struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
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
	client := s.agentClient
	if client == nil {
		client = &http.Client{Timeout: s.agent.Timeout}
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("Flow Agent provider is unavailable")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("Flow Agent provider returned status %d: %s", response.StatusCode, providerError(body))
	}
	return response, nil
}

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
		if message.Content != "" {
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

func chatMessages(messages []agentProviderMessage) []any {
	result := []any{}
	for _, message := range messages {
		if message.ToolResult != nil {
			result = append(result, map[string]any{"role": "tool", "tool_call_id": message.ToolResult.CallID, "content": message.ToolResult.Content})
			continue
		}
		item := map[string]any{"role": message.Role, "content": message.Content}
		if len(message.ToolCalls) > 0 {
			calls := []any{}
			for _, call := range message.ToolCalls {
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
			}
			item["tool_calls"] = calls
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
			result = append(result, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": message.ToolResult.CallID, "content": message.ToolResult.Content, "is_error": message.ToolResult.IsError}}})
			continue
		}
		content := []any{}
		if message.Content != "" {
			content = append(content, map[string]any{"type": "text", "text": message.Content})
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
