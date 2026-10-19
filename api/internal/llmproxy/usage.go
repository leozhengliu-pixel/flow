package llmproxy

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Usage is what one proxied request consumed. InputTokens excludes cache
// reads and writes, so each count is priced exactly once.
type Usage struct {
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Empty reports whether nothing was metered.
func (u Usage) Empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.CacheReadTokens == 0 && u.CacheWriteTokens == 0
}

// usageFields covers the usage objects of all three wire protocols:
// Anthropic (input_tokens excludes cache), OpenAI Responses (input_tokens
// includes input_tokens_details.cached_tokens) and chat completions
// (prompt_tokens includes prompt_tokens_details.cached_tokens).
type usageFields struct {
	InputTokens              *int64 `json:"input_tokens"`
	OutputTokens             *int64 `json:"output_tokens"`
	CacheCreationInputTokens *int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     *int64 `json:"cache_read_input_tokens"`
	InputTokensDetails       *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	PromptTokens        *int64 `json:"prompt_tokens"`
	CompletionTokens    *int64 `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// usageEnvelope is any JSON document (a full response or one SSE event) that
// may carry usage.
type usageEnvelope struct {
	Type    string       `json:"type"`
	Model   string       `json:"model"`
	Usage   *usageFields `json:"usage"`
	Message *struct {
		Model string       `json:"model"`
		Usage *usageFields `json:"usage"`
	} `json:"message"`
	Response *struct {
		Model string       `json:"model"`
		Usage *usageFields `json:"usage"`
	} `json:"response"`
}

func value(p *int64) int64 {
	if p == nil || *p < 0 {
		return 0
	}
	return *p
}

// applyAnthropic merges an Anthropic usage object. message_start carries the
// input and cache counts; message_delta carries the cumulative output count
// (and, on newer API versions, the final input/cache counts), so present
// fields overwrite earlier ones.
func (u *Usage) applyAnthropic(f *usageFields) {
	if f == nil {
		return
	}
	if f.InputTokens != nil {
		u.InputTokens = value(f.InputTokens)
	}
	if f.OutputTokens != nil {
		u.OutputTokens = value(f.OutputTokens)
	}
	if f.CacheReadInputTokens != nil {
		u.CacheReadTokens = value(f.CacheReadInputTokens)
	}
	if f.CacheCreationInputTokens != nil {
		u.CacheWriteTokens = value(f.CacheCreationInputTokens)
	}
}

// applyOpenAI sets the final OpenAI usage (Responses or chat completions).
func (u *Usage) applyOpenAI(f *usageFields) {
	if f == nil {
		return
	}
	input, output, cached := value(f.InputTokens), value(f.OutputTokens), int64(0)
	if f.InputTokensDetails != nil {
		cached = f.InputTokensDetails.CachedTokens
	}
	if f.PromptTokens != nil || f.CompletionTokens != nil {
		input, output = value(f.PromptTokens), value(f.CompletionTokens)
		if f.PromptTokensDetails != nil {
			cached = f.PromptTokensDetails.CachedTokens
		}
	}
	if cached < 0 || cached > input {
		cached = 0
	}
	u.InputTokens, u.OutputTokens, u.CacheReadTokens = input-cached, output, cached
}

// usageMeter extracts usage from a response body as it streams by.
type usageMeter struct {
	protocol string // UpstreamAnthropic or "openai" (OpenAI and OpenAI-compatible)
	sse      bool
	limit    int64
	usage    Usage

	// SSE state.
	line      bytes.Buffer
	data      bytes.Buffer
	event     string
	overflow  bool
	discarded bool

	// Plain JSON body (bounded).
	body bytes.Buffer
}

// maxSSEEvent bounds one buffered SSE event; usage events are small, while
// content deltas larger than this are skipped (they never carry usage).
const maxSSEEvent = 4 << 20

func newUsageMeter(upstream string, contentType string, limit int64) *usageMeter {
	protocol := "openai"
	if upstream == UpstreamAnthropic {
		protocol = UpstreamAnthropic
	}
	return &usageMeter{protocol: protocol, sse: strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream"), limit: limit}
}

// Write consumes a chunk of the response body. It never fails, so it can be
// fed from the streaming loop without affecting the pass-through.
func (m *usageMeter) Write(chunk []byte) {
	if !m.sse {
		if m.overflow {
			return
		}
		if int64(m.body.Len()+len(chunk)) > m.limit {
			m.overflow = true
			m.body.Reset()
			return
		}
		m.body.Write(chunk)
		return
	}
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			m.appendLine(chunk)
			return
		}
		m.appendLine(chunk[:i])
		m.endLine()
		chunk = chunk[i+1:]
	}
}

func (m *usageMeter) appendLine(part []byte) {
	if m.discarded {
		return
	}
	if m.line.Len()+len(part) > maxSSEEvent {
		m.discarded = true
		m.line.Reset()
		return
	}
	m.line.Write(part)
}

func (m *usageMeter) endLine() {
	line := bytes.TrimSuffix(m.line.Bytes(), []byte("\r"))
	discarded := m.discarded
	m.discarded = false
	defer m.line.Reset()
	if discarded {
		// The line was too large; drop the whole event.
		m.data.Reset()
		m.data.WriteString("\x00")
		return
	}
	if len(line) == 0 {
		m.dispatch()
		return
	}
	if line[0] == ':' {
		return
	}
	field, rest, _ := bytes.Cut(line, []byte(":"))
	rest = bytes.TrimPrefix(rest, []byte(" "))
	switch string(field) {
	case "event":
		m.event = string(rest)
	case "data":
		if m.data.Len()+len(rest) > maxSSEEvent {
			m.data.Reset()
			m.data.WriteString("\x00")
			return
		}
		if m.data.Len() > 0 {
			m.data.WriteByte('\n')
		}
		m.data.Write(rest)
	}
}

func (m *usageMeter) dispatch() {
	data := m.data.Bytes()
	event := m.event
	defer func() { m.data.Reset(); m.event = "" }()
	if len(data) == 0 || data[0] != '{' {
		return // "[DONE]", pings, or a dropped oversized event
	}
	// Cheap pre-filter: only events that may carry usage are decoded.
	if !bytes.Contains(data, []byte(`"usage"`)) {
		return
	}
	m.apply(event, data)
}

func (m *usageMeter) apply(event string, data []byte) {
	var envelope usageEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return
	}
	if envelope.Type == "" {
		envelope.Type = event
	}
	if m.protocol == UpstreamAnthropic {
		switch {
		case envelope.Message != nil:
			if envelope.Message.Model != "" {
				m.usage.Model = envelope.Message.Model
			}
			m.usage.applyAnthropic(envelope.Message.Usage)
		case envelope.Usage != nil:
			if envelope.Model != "" {
				m.usage.Model = envelope.Model
			}
			m.usage.applyAnthropic(envelope.Usage)
		}
		return
	}
	switch {
	case envelope.Response != nil && envelope.Response.Usage != nil:
		// Responses API: response.completed / response.incomplete / response.failed.
		if envelope.Response.Model != "" {
			m.usage.Model = envelope.Response.Model
		}
		m.usage.applyOpenAI(envelope.Response.Usage)
	case envelope.Usage != nil:
		// Chat completions final chunk, or a non-streamed body.
		if envelope.Model != "" {
			m.usage.Model = envelope.Model
		}
		m.usage.applyOpenAI(envelope.Usage)
	}
}

// Finish returns the metered usage once the body has ended.
func (m *usageMeter) Finish() Usage {
	if m.sse {
		if m.line.Len() > 0 {
			m.endLine()
		}
		if m.data.Len() > 0 {
			m.dispatch()
		}
		return m.usage
	}
	if !m.overflow && m.body.Len() > 0 {
		m.apply("", m.body.Bytes())
	}
	return m.usage
}
