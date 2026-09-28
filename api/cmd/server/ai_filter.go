package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// AI filter (Linear "Filter with AI"): the Flow Agent model turns a sentence
// into filter-bar filters. The client sends the vocabulary it can filter by
// (field -> options) so the model can only pick ids that exist; anything else
// in the reply is dropped. Without a configured Agent the endpoint answers 503
// and the client uses its rule-based parser.

type aiFilterOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type aiFilterInput struct {
	Query      string                      `json:"query"`
	ViewerName string                      `json:"viewerName,omitempty"`
	Today      string                      `json:"today,omitempty"`
	Fields     map[string][]aiFilterOption `json:"fields"`
}

type aiFilterResult struct {
	Field  string         `json:"field"`
	Option aiFilterOption `json:"option"`
}

const aiFilterSystemPrompt = `You convert a request about issues into filters for an issue tracker.
Reply with one JSON object and nothing else:
{"filters":[{"field":"<field>","ids":["<option id>"]}],"text":"<remaining keywords to search in titles, or empty>"}
Rules:
- Use only the fields and option ids listed in the vocabulary. Never invent ids.
- "me", "my", "mine" refer to the current user.
- Put words that name no listed option (topics, feature names) in "text"; leave it empty when everything was matched.
- Omit a field when the request does not mention it.`

func (s *server) aiIssueFilter(w http.ResponseWriter, r *http.Request) {
	var input aiFilterInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || len([]rune(input.Query)) > 500 {
		writeError(w, http.StatusBadRequest, "query must be 1-500 characters")
		return
	}
	if !s.agent.Enabled {
		writeError(w, http.StatusServiceUnavailable, "AI filter is not configured")
		return
	}
	fields := make([]string, 0, len(input.Fields))
	for field := range input.Fields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	if len(fields) > 24 {
		fields = fields[:24]
	}
	known := map[string]map[string]string{}
	var vocabulary strings.Builder
	for _, field := range fields {
		options := input.Fields[field]
		if len(options) > 300 {
			options = options[:300]
		}
		known[field] = map[string]string{}
		parts := []string{}
		for _, option := range options {
			label := option.Label
			if runes := []rune(label); len(runes) > 120 {
				label = string(runes[:120])
			}
			if option.ID == "" && field != "assignee" && field != "project" && field != "cycle" {
				continue
			}
			known[field][option.ID] = label
			parts = append(parts, fmt.Sprintf("%q=%s", option.ID, strings.ReplaceAll(label, "\n", " ")))
		}
		fmt.Fprintf(&vocabulary, "%s: %s\n", field, strings.Join(parts, "; "))
	}
	prompt := fmt.Sprintf("Current user: %s\nToday: %s\nVocabulary (field: \"id\"=label):\n%s\nRequest: %s", firstNonEmpty(input.ViewerName, "unknown"), firstNonEmpty(input.Today, time.Now().UTC().Format("2006-01-02")), vocabulary.String(), input.Query)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	turn, err := s.requestAgentTurnWithoutTools(ctx, []agentProviderMessage{{Role: "system", Content: aiFilterSystemPrompt}, {Role: "user", Content: prompt}})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	results, err := parseAIFilterReply(turn.Text, known)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"filters": results})
}

// parseAIFilterReply keeps only field/id pairs present in the vocabulary.
func parseAIFilterReply(text string, known map[string]map[string]string) ([]aiFilterResult, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("AI filter reply was not JSON")
	}
	var reply struct {
		Filters []struct {
			Field string   `json:"field"`
			IDs   []string `json:"ids"`
		} `json:"filters"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &reply); err != nil {
		return nil, fmt.Errorf("AI filter reply was not valid JSON")
	}
	results := []aiFilterResult{}
	seen := map[string]bool{}
	for _, filter := range reply.Filters {
		options, ok := known[filter.Field]
		if !ok {
			continue
		}
		for _, id := range filter.IDs {
			label, ok := options[id]
			if !ok || seen[filter.Field+"\x00"+id] {
				continue
			}
			seen[filter.Field+"\x00"+id] = true
			results = append(results, aiFilterResult{Field: filter.Field, Option: aiFilterOption{ID: id, Label: label}})
		}
	}
	if keywords := strings.TrimSpace(reply.Text); keywords != "" && len([]rune(keywords)) <= 200 {
		results = append(results, aiFilterResult{Field: "content", Option: aiFilterOption{ID: "query:" + keywords, Label: keywords}})
	}
	return results, nil
}
