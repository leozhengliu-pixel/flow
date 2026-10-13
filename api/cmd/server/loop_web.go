package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/websearch"
)

// Web tools: provider-agnostic search plus an SSRF-safe page fetch. Loops get
// them with webSearch on when a provider is configured. Flow Agent chat and
// comment threads have web access unless the workspace turns it off (like
// The reference app): fetch_url always, web_search through the configured provider or the
// model provider's hosted search.

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

// agentWebAccess reports whether Flow Agent may use the web in a workspace: on unless an admin turned it off.
func agentWebAccess(settings domain.WorkspaceSettings) bool {
	return !settings.AgentWebSearchDisabled && !settings.HIPAACompliance
}

// agentNativeWebSearch reports whether model turns can use the provider's hosted web search instead of a
// configured search provider (OpenAI Responses "web_search", Anthropic "web_search" server tool).
func (s *server) agentNativeWebSearch() bool {
	if !s.agent.NativeWebSearch || s.webSearchAvailable() || s.agentNativeWebSearchOff.Load() {
		return false
	}
	return s.agent.Protocol == "openai-responses" || s.agent.Protocol == "anthropic-messages"
}

// agentWebSearchUsable reports whether Flow Agent chat can search the web (a provider or hosted search).
func (s *server) agentWebSearchUsable() bool {
	return s.webSearchAvailable() || s.agentNativeWebSearch()
}

// agentWebToolList is the function tools a web-enabled turn gets: fetch_url always, web_search with a provider.
func (s *server) agentWebToolList() []agentProviderTool {
	if s.webSearchAvailable() {
		return webToolDefinitions
	}
	return []agentProviderTool{webToolDefinitions[1]}
}

// agentWebNote tells the model which web tools it has, and to say so honestly when search is unavailable.
func (s *server) agentWebNote() string {
	if s.agentWebSearchUsable() {
		return agentWebSearchNote
	}
	return agentWebFetchOnlyNote
}

// executeWebTool runs web_search or fetch_url.
func (s *server) executeWebTool(ctx context.Context, name string, arguments json.RawMessage) ([]byte, error) {
	if !agentWebToolsEnabled(ctx) || strings.TrimPrefix(name, "mcp__flow.") == webSearchToolName && !s.webSearchAvailable() {
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

const agentWebSearchNote = "\n\nWeb access: you can search the web and read pages with fetch_url for public or current information that is not in the workspace (versions, docs, best practices, news). Use it instead of answering such questions from memory. Cite every source you relied on as a markdown link whose text is the site's hostname, for example [nodejs.org](https://nodejs.org/en/download).\n"

const agentWebFetchOnlyNote = "\n\nWeb access: you can read a public web page with fetch_url when you know its URL (cite it as a markdown link whose text is the hostname, for example [nodejs.org](https://nodejs.org/en/download)), but web search is not configured on this server. When a question needs a search, say plainly that you cannot search the web here and answer from what you know, marked as possibly out of date.\n"
