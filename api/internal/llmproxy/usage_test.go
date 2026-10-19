package llmproxy

import (
	"strings"
	"testing"
	"time"

	"flow/api/internal/config"
)

func TestCostMicros(t *testing.T) {
	cases := []struct {
		name                                 string
		price                                config.ModelPrice
		input, output, cacheRead, cacheWrite int64
		want                                 int64
	}{
		// 1M input at $3 + 100k output at $15/M = $4.50.
		{"input and output", config.ModelPrice{Input: 3, Output: 15, CacheRead: 0.3}, 1_000_000, 100_000, 0, 0, 4_500_000},
		{"cache tokens", config.ModelPrice{CacheRead: 0.3, CacheWrite: 3.75}, 0, 0, 1_000_000, 1_000_000, 4_050_000},
		{"unpriced", config.ModelPrice{}, 1000, 1000, 1000, 1000, 0},
		{"rounding", config.ModelPrice{Input: 0.125}, 3, 0, 0, 0, 0},
		{"rounding up", config.ModelPrice{Input: 0.25}, 2, 0, 0, 0, 1},
	}
	for _, tc := range cases {
		if got := CostMicros(tc.price, tc.input, tc.output, tc.cacheRead, tc.cacheWrite); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestUsageMeterProtocols(t *testing.T) {
	cases := []struct {
		name, upstream, contentType, body string
		want                              Usage
	}{
		{
			name: "anthropic stream", upstream: UpstreamAnthropic, contentType: "text/event-stream; charset=utf-8",
			body: "event: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"m\",\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":5,\"cache_creation_input_tokens\":2,\"output_tokens\":1}}}\r\n\r\n" +
				": ping\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"\\\"usage\\\" in text\"}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":42}}\n\n",
			want: Usage{Model: "m", InputTokens: 10, OutputTokens: 42, CacheReadTokens: 5, CacheWriteTokens: 2},
		},
		{
			name: "anthropic delta with final input counts", upstream: UpstreamAnthropic, contentType: "text/event-stream",
			body: "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n" +
				"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":12,\"cache_read_input_tokens\":7,\"output_tokens\":3}}\n\n",
			want: Usage{InputTokens: 12, OutputTokens: 3, CacheReadTokens: 7},
		},
		{
			name: "anthropic json", upstream: UpstreamAnthropic, contentType: "application/json",
			body: `{"type":"message","model":"m","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":3,"output_tokens":4,"cache_read_input_tokens":1}}`,
			want: Usage{Model: "m", InputTokens: 3, OutputTokens: 4, CacheReadTokens: 1},
		},
		{
			name: "responses stream incomplete", upstream: UpstreamOpenAI, contentType: "text/event-stream",
			body: "event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"response\":{\"model\":\"g\",\"usage\":{\"input_tokens\":9,\"input_tokens_details\":{\"cached_tokens\":4},\"output_tokens\":2}}}\n\n",
			want: Usage{Model: "g", InputTokens: 5, CacheReadTokens: 4, OutputTokens: 2},
		},
		{
			name: "responses json", upstream: UpstreamOpenSource, contentType: "application/json",
			body: `{"object":"response","model":"g","usage":{"input_tokens":9,"output_tokens":2}}`,
			want: Usage{Model: "g", InputTokens: 9, OutputTokens: 2},
		},
		{
			name: "chat stream", upstream: UpstreamOpenSource, contentType: "text/event-stream",
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}],\"usage\":null}\n\ndata: {\"model\":\"q\",\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n",
			want: Usage{Model: "q", InputTokens: 7, OutputTokens: 3},
		},
		{
			name: "stream without trailing blank line", upstream: UpstreamOpenAI, contentType: "text/event-stream",
			body: "data: {\"model\":\"q\",\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}",
			want: Usage{Model: "q", InputTokens: 1, OutputTokens: 1},
		},
		{
			name: "invalid cached count ignored", upstream: UpstreamOpenAI, contentType: "application/json",
			body: `{"usage":{"prompt_tokens":5,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":50}}}`,
			want: Usage{InputTokens: 5, OutputTokens: 1},
		},
	}
	for _, tc := range cases {
		// Feed the body in every possible two-chunk split, and byte by byte, so
		// SSE framing across network reads is covered.
		splits := [][]string{{tc.body}}
		for i := 1; i < len(tc.body); i += 7 {
			splits = append(splits, []string{tc.body[:i], tc.body[i:]})
		}
		splits = append(splits, strings.Split(tc.body, ""))
		for _, chunks := range splits {
			meter := newUsageMeter(tc.upstream, tc.contentType, 1<<20)
			for _, chunk := range chunks {
				meter.Write([]byte(chunk))
			}
			if got := meter.Finish(); got != tc.want {
				t.Fatalf("%s (%d chunks): %+v, want %+v", tc.name, len(chunks), got, tc.want)
			}
		}
	}
}

func TestUsageMeterBounds(t *testing.T) {
	meter := newUsageMeter(UpstreamOpenAI, "application/json", 16)
	meter.Write([]byte(`{"usage":{"prompt_tokens":5,"completion_tokens":1}}`))
	if got := meter.Finish(); !got.Empty() {
		t.Fatalf("oversized bodies are not parsed: %+v", got)
	}
	stream := newUsageMeter(UpstreamOpenAI, "text/event-stream", 0)
	stream.Write([]byte("data: " + strings.Repeat("x", maxSSEEvent+10) + "\n\n"))
	stream.Write([]byte("data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n"))
	if got := stream.Finish(); got.InputTokens != 2 {
		t.Fatalf("an oversized event must not break later events: %+v", got)
	}
}

func TestNewIDIsLowercaseULID(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := newID(timeNow())
		if len(id) != 26 || strings.ToLower(id) != id || seen[id] {
			t.Fatalf("bad id %q", id)
		}
		seen[id] = true
	}
}

var timeNow = func() time.Time { return time.Unix(1_700_000_000, 0) }
