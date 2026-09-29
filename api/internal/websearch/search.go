// Package websearch gives Flow Agent and loops a provider-agnostic web search
// tool and an SSRF-safe page fetcher.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Result is one web search hit.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Provider searches the web.
type Provider interface {
	Name() string
	Search(ctx context.Context, query string, maxResults int) ([]Result, error)
}

// Config selects a provider. An empty Provider disables web search.
type Config struct {
	Provider string // tavily | brave | searxng
	APIKey   string
	URL      string // searxng base URL, or API base override for tavily/brave
}

const (
	DefaultResults = 5
	MaxResults     = 10
	searchTimeout  = 10 * time.Second
	maxSearchBody  = 2 << 20
)

// New returns the configured provider, or nil when web search is disabled.
// The client defaults to one with a 10 s timeout.
func New(config Config, client *http.Client) Provider {
	if client == nil {
		client = &http.Client{Timeout: searchTimeout}
	}
	base := strings.TrimRight(config.URL, "/")
	switch strings.ToLower(config.Provider) {
	case "tavily":
		return &tavily{client: client, key: config.APIKey, base: firstNonEmpty(base, "https://api.tavily.com")}
	case "brave":
		return &brave{client: client, key: config.APIKey, base: firstNonEmpty(base, "https://api.search.brave.com")}
	case "searxng":
		if base == "" {
			return nil
		}
		return &searxng{client: client, base: base}
	}
	return nil
}

// ClampResults bounds the requested number of results.
func ClampResults(value int) int {
	if value < 1 {
		return DefaultResults
	}
	return min(value, MaxResults)
}

type tavily struct {
	client *http.Client
	key    string
	base   string
}

func (p *tavily) Name() string { return "tavily" }

func (p *tavily) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "max_results": ClampResults(maxResults), "search_depth": "basic"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+p.key)
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := doJSON(p.client, request, &payload); err != nil {
		return nil, err
	}
	results := []Result{}
	for _, item := range payload.Results {
		results = appendResult(results, item.Title, item.URL, item.Content, maxResults)
	}
	return results, nil
}

type brave struct {
	client *http.Client
	key    string
	base   string
}

func (p *brave) Name() string { return "brave" }

func (p *brave) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	values := url.Values{"q": {query}, "count": {fmt.Sprint(ClampResults(maxResults))}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/res/v1/web/search?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Subscription-Token", p.key)
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := doJSON(p.client, request, &payload); err != nil {
		return nil, err
	}
	results := []Result{}
	for _, item := range payload.Web.Results {
		results = appendResult(results, item.Title, item.URL, item.Description, maxResults)
	}
	return results, nil
}

type searxng struct {
	client *http.Client
	base   string
}

func (p *searxng) Name() string { return "searxng" }

func (p *searxng) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	values := url.Values{"q": {query}, "format": {"json"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/search?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := doJSON(p.client, request, &payload); err != nil {
		return nil, err
	}
	results := []Result{}
	for _, item := range payload.Results {
		results = appendResult(results, item.Title, item.URL, item.Content, maxResults)
	}
	return results, nil
}

func doJSON(client *http.Client, request *http.Request, target any) error {
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("web search request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSearchBody))
	if err != nil {
		return fmt.Errorf("web search response could not be read: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("web search provider returned HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("web search provider returned invalid JSON")
	}
	return nil
}

// appendResult adds one cleaned hit, skipping non-http(s) and duplicate URLs.
func appendResult(results []Result, title, link, snippet string, maxResults int) []Result {
	if len(results) >= ClampResults(maxResults) {
		return results
	}
	link = strings.TrimSpace(link)
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return results
	}
	for _, existing := range results {
		if existing.URL == link {
			return results
		}
	}
	snippet = stripTags(snippet)
	if runes := []rune(snippet); len(runes) > 500 {
		snippet = string(runes[:497]) + "…"
	}
	return append(results, Result{Title: stripTags(title), URL: link, Snippet: snippet})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
