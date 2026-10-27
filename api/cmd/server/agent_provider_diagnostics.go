package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// What a provider response looked like, besides its content: token usage, how the stream was read and how the
// provider said it stopped. Loop runs use it to tell a truncated, reasoning-only or unreadable turn from an empty
// one, and record it per turn. None of it holds model or business text.

// agentProviderUsage is the provider's token count for one turn, when it sent one.
type agentProviderUsage struct {
	InputTokens     int `json:"inputTokens,omitempty"`
	OutputTokens    int `json:"outputTokens,omitempty"`
	ReasoningTokens int `json:"reasoningTokens,omitempty"`
	CachedTokens    int `json:"cachedTokens,omitempty"`
}

// agentStreamStats is how a response body was read.
type agentStreamStats struct {
	// Bytes is the size of the response body read.
	Bytes int
	// Events counts SSE data frames (1 for a JSON body).
	Events int
	// Unreadable counts frames that were not valid JSON; Unrecognized frames that were JSON in a shape Flow
	// does not read (no delta, an unknown event type).
	Unreadable   int
	Unrecognized int
	// Finished: the provider signalled the end of the response (a finish reason, response.completed or
	// response.incomplete, message_stop, [DONE]).
	Finished bool
}

// truncated reports whether the provider stopped the turn at its output limit (Chat Completions "length",
// Anthropic "max_tokens", Responses "incomplete" for max_output_tokens).
func (turn agentProviderTurn) truncated() bool {
	if detail := strings.ToLower(turn.StopDetail); detail != "" && detail != "max_output_tokens" && detail != "max_tokens" {
		return false
	}
	return agentTurnTruncated(turn.StopReason)
}

// filtered reports whether the provider withheld the output (a content filter or refusal).
func (turn agentProviderTurn) filtered() bool {
	switch strings.ToLower(firstNonEmpty(turn.StopDetail, turn.StopReason)) {
	case "content_filter", "refusal", "safety":
		return true
	}
	return false
}

// agentChatUsage reads a Chat Completions usage object (OpenAI and the compatible gateways' field names).
func agentChatUsage(raw json.RawMessage) *agentProviderUsage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptCacheHit      int `json:"prompt_cache_hit_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	}
	if json.Unmarshal(raw, &usage) != nil {
		return nil
	}
	return &agentProviderUsage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens, ReasoningTokens: usage.CompletionTokensDetails.ReasoningTokens, CachedTokens: max(usage.PromptTokensDetails.CachedTokens, usage.PromptCacheHit)}
}

// agentResponsesUsage reads a Responses usage object.
func agentResponsesUsage(raw json.RawMessage) *agentProviderUsage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var usage struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		InputTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokensDetails struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	}
	if json.Unmarshal(raw, &usage) != nil {
		return nil
	}
	return &agentProviderUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, ReasoningTokens: usage.OutputTokensDetails.ReasoningTokens, CachedTokens: usage.InputTokensDetails.CachedTokens}
}

// mergeAnthropicUsage adds an Anthropic usage object (message_start carries the input, message_delta the output).
func mergeAnthropicUsage(current *agentProviderUsage, raw json.RawMessage) *agentProviderUsage {
	if len(raw) == 0 || string(raw) == "null" {
		return current
	}
	var usage struct {
		InputTokens     int `json:"input_tokens"`
		OutputTokens    int `json:"output_tokens"`
		CacheReadTokens int `json:"cache_read_input_tokens"`
	}
	if json.Unmarshal(raw, &usage) != nil {
		return current
	}
	if current == nil {
		current = &agentProviderUsage{}
	}
	current.InputTokens = max(current.InputTokens, usage.InputTokens)
	current.OutputTokens = max(current.OutputTokens, usage.OutputTokens)
	current.CachedTokens = max(current.CachedTokens, usage.CacheReadTokens)
	return current
}

// agentSniffedBody is a response body read through a buffer, so its first bytes could be inspected.
type agentSniffedBody struct {
	*bufio.Reader
	io.Closer
	inner io.ReadCloser
}

// agentEventStream reports whether a response is a server-sent event stream: by its Content-Type, or, for
// gateways that stream without one, by a body that starts with an SSE field.
func agentEventStream(response *http.Response) bool {
	if isEventStream(response) {
		return true
	}
	if response.Body == nil {
		return false
	}
	reader := bufio.NewReaderSize(response.Body, 4096)
	response.Body = &agentSniffedBody{Reader: reader, Closer: response.Body, inner: response.Body}
	head, _ := reader.Peek(512)
	head = bytes.TrimPrefix(head, []byte{0xEF, 0xBB, 0xBF}) // a UTF-8 byte order mark
	head = bytes.TrimLeft(head, " \t\r\n")
	return bytes.HasPrefix(head, []byte("data:")) || bytes.HasPrefix(head, []byte("event:")) || bytes.HasPrefix(head, []byte(": "))
}

// agentResponseBytes is how much of a provider response body has been read.
func agentResponseBytes(body io.ReadCloser) int {
	if sniffed, ok := body.(*agentSniffedBody); ok {
		body = sniffed.inner
	}
	if idle, ok := body.(*agentIdleBody); ok {
		return idle.bytes
	}
	return 0
}

// agentUnreadableResponse is a provider response Flow could not parse.
func agentUnreadableResponse(message string) error {
	return &agentProviderError{message: message, unreadable: true}
}
