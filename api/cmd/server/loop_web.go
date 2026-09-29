package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/websearch"
)

// Web tools: provider-agnostic search plus an SSRF-safe page fetch. Loops get
// them with webSearch on; Flow Agent chat gets them when the workspace enables
// agent web search. They are offered only when a provider is configured.

const (
	webSearchToolName = "web_search"
	fetchURLToolName  = "fetch_url"
)

var webToolDefinitions = []agentProviderTool{
	{
		Name:        webSearchToolName,
		Description: "Search the public web. Returns results with title, url and snippet. Use it for information that is not in the Flow workspace; cite the URLs you rely on.",
		Parameters:  json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string","description":"Search query"},"maxResults":{"type":"integer","minimum":1,"maximum":10,"description":"Number of results (default 5)"}},"additionalProperties":false}`),
		Access:      "read",
	},
	{
		Name:        fetchURLToolName,
		Description: "Fetch a public web page (http or https) and return its readable text. Use it to read a search result or a link in the workspace; cite the URL you read.",
		Parameters:  json.RawMessage(`{"type":"object","required":["url"],"properties":{"url":{"type":"string","description":"The http(s) URL to read"}},"additionalProperties":false}`),
		Access:      "read",
	},
}

// agentWebToolsKey marks a request whose model turns may use the web tools.
type agentWebToolsKey struct{}

func withAgentWebTools(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentWebToolsKey{}, true)
}

func agentWebToolsEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(agentWebToolsKey{}).(bool)
	return enabled
}

func (s *server) webSearchAvailable() bool {
	return s.webSearch != nil
}

func (s *server) webSearchProviderName() string {
	if s.webSearch == nil {
		return ""
	}
	return s.webSearch.Name()
}

func isWebTool(name string) bool {
	name = strings.TrimPrefix(name, "mcp__flow.")
	return name == webSearchToolName || name == fetchURLToolName
}

const loopWebSearchUnavailable = "Web search is enabled for this loop, but no web search provider is configured on this server."

// executeWebTool runs web_search or fetch_url.
func (s *server) executeWebTool(ctx context.Context, name string, arguments json.RawMessage) ([]byte, error) {
	if !s.webSearchAvailable() || !agentWebToolsEnabled(ctx) {
		return nil, fmt.Errorf("Web search is not available")
	}
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"maxResults"`
		URL        string `json:"url"`
	}
	if len(arguments) > 0 && json.Unmarshal(arguments, &args) != nil {
		return nil, fmt.Errorf("invalid arguments for %s", name)
	}
	switch strings.TrimPrefix(name, "mcp__flow.") {
	case webSearchToolName:
		query := strings.TrimSpace(args.Query)
		if query == "" {
			return nil, fmt.Errorf("query is required")
		}
		if len(query) > 400 {
			query = query[:400]
		}
		searchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		results, err := s.webSearch.Search(searchCtx, query, websearch.ClampResults(args.MaxResults))
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"query": query, "results": results})
	case fetchURLToolName:
		if strings.TrimSpace(args.URL) == "" {
			return nil, fmt.Errorf("url is required")
		}
		fetcher := s.webFetcher
		if fetcher == nil {
			fetcher = &websearch.Fetcher{}
		}
		page, err := fetcher.Fetch(ctx, args.URL)
		if err != nil {
			return nil, err
		}
		return json.Marshal(page)
	}
	return nil, fmt.Errorf("unknown web tool %s", name)
}

const agentWebSearchNote = "\n\nWeb search: you can use web_search and fetch_url for public information that is not in the workspace. When you use web results, cite the URLs you relied on as markdown links.\n"
