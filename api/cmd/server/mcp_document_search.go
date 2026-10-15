package main

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Document search for the agent and MCP clients: the full-text index picks the
// candidate documents, the caller's own document list decides access, and the
// ranking and snippets come from the candidates alone, so no tool call scans
// every document body.

// documentSearchCandidates caps the index hits read per query.
const documentSearchCandidates = 500

// documentSearchStopPhrases are question words that separate keywords in
// unspaced Chinese queries ("UAT的准出标准是什么" → "UAT", "准出标准").
var documentSearchStopPhrases = []string{"是什么", "什么", "有哪些", "哪些", "怎么样", "怎么", "如何", "是否", "为什么", "吗", "呢", "的"}

// documentSearchTerms splits a query into keywords: on spaces and punctuation,
// at Chinese/Japanese/Korean and Latin boundaries, and at question words. Single
// characters are dropped when longer keywords remain.
func documentSearchTerms(query string) []string {
	for _, phrase := range documentSearchStopPhrases {
		query = strings.ReplaceAll(query, phrase, " ")
	}
	terms := []string{}
	var current []rune
	currentCJK := false
	flush := func() {
		term := strings.Trim(string(current), "-_./#+")
		current = current[:0]
		if term != "" && !slices.ContainsFunc(terms, func(other string) bool { return strings.EqualFold(other, term) }) {
			terms = append(terms, term)
		}
	}
	for _, r := range query {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsMark(r) && !strings.ContainsRune("-_./#+", r) {
			flush()
			continue
		}
		cjk := documentSearchCJK(r)
		if len(current) > 0 && cjk != currentCJK {
			flush()
		}
		current, currentCJK = append(current, r), cjk
	}
	flush()
	if slices.ContainsFunc(terms, func(term string) bool { return utf8.RuneCountInString(term) > 1 }) {
		terms = slices.DeleteFunc(terms, func(term string) bool { return utf8.RuneCountInString(term) < 2 })
	}
	return terms[:min(len(terms), 8)]
}

func documentSearchCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func documentSearchFold(value string) string { return strings.Map(unicode.ToLower, value) }

type documentMatch struct {
	document domain.Document
	score    int
	terms    []string
	all      bool
	snippet  string
	section  string
}

// rankDocumentMatches returns the candidates whose title or body contain the
// query's keywords, best first: documents with every keyword (or, when
// anyFallback is set and none has them all, any keyword), then title hits,
// then how often the keywords occur. Only documents in candidates are returned,
// so the caller's access rules apply unchanged.
func (s *server) rankDocumentMatches(ctx context.Context, workspace string, candidates []domain.Document, query string, anyFallback bool) ([]documentMatch, error) {
	terms := documentSearchTerms(query)
	if len(terms) == 0 {
		return []documentMatch{}, nil
	}
	byID := make(map[string]domain.Document, len(candidates))
	for _, document := range candidates {
		byID[document.ID] = document
	}
	matches := []documentMatch{}
	for _, matchAll := range []bool{true, false} {
		if !matchAll && (!anyFallback || len(terms) == 1) {
			break
		}
		ids, err := s.store.SearchDocumentIDs(ctx, workspace, terms, matchAll, documentSearchCandidates)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			document, ok := byID[id]
			if !ok {
				continue
			}
			if match := scoreDocumentMatch(document, terms); len(match.terms) > 0 {
				matches = append(matches, match)
			}
		}
		if len(matches) > 0 {
			break
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].document.UpdatedAt.After(matches[j].document.UpdatedAt)
	})
	return matches, nil
}

// scoreDocumentMatch weighs matched keywords above title hits above
// occurrences, and finds the first body line with a keyword for the snippet.
func scoreDocumentMatch(document domain.Document, terms []string) documentMatch {
	match := documentMatch{document: document}
	title, content := documentSearchFold(document.Title), documentSearchFold(document.Content)
	titleHits, occurrences := 0, 0
	folded := make([]string, 0, len(terms))
	for _, term := range terms {
		key := documentSearchFold(term)
		inTitle, count := strings.Contains(title, key), strings.Count(content, key)
		if !inTitle && count == 0 {
			continue
		}
		match.terms, folded = append(match.terms, term), append(folded, key)
		if inTitle {
			titleHits++
		}
		occurrences += min(count, 20)
	}
	match.all = len(match.terms) == len(terms)
	match.score = len(match.terms)*1000 + titleHits*200 + occurrences
	if phrase := documentSearchFold(strings.Join(terms, " ")); len(terms) > 1 && (strings.Contains(title, phrase) || strings.Contains(content, phrase)) {
		match.score += 100
	}
	match.snippet, match.section = documentSnippet(document.Content, folded)
	return match
}

// documentSnippet returns about 240 characters from the first match on the
// first body line with the most keywords, and the heading of its section.
func documentSnippet(content string, folded []string) (string, string) {
	lines := strings.Split(content, "\n")
	best, bestCount := -1, 0
	for index, line := range lines {
		lower, count := documentSearchFold(line), 0
		for _, term := range folded {
			if strings.Contains(lower, term) {
				count++
			}
		}
		if count > bestCount {
			best, bestCount = index, count
		}
	}
	section := ""
	for index, line := range lines[:max(best+1, 0)] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			section = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
		if index < best {
			continue
		}
		lower := documentSearchFold(line)
		at := -1
		for _, term := range folded {
			if position := strings.Index(lower, term); position >= 0 && (at < 0 || position < at) {
				at = position
			}
		}
		// Folding maps rune to rune, so the match's rune offset holds in the original line.
		original := []rune(line)
		start := max(0, utf8.RuneCountInString(lower[:at])-60)
		runes := []rune(strings.Join(strings.Fields(string(original[start:])+" "+strings.Join(lines[index+1:min(len(lines), index+6)], " ")), " "))
		end := min(len(runes), 240)
		snippet := string(runes[:end])
		if start > 0 {
			snippet = "…" + snippet
		}
		if end < len(runes) {
			snippet += "…"
		}
		return snippet, section
	}
	runes := []rune(strings.Join(strings.Fields(content), " "))
	if len(runes) > 240 {
		return string(runes[:240]) + "…", ""
	}
	return string(runes), ""
}

func mcpDocumentURL(workspace, slug string, args map[string]any) string {
	path := "/" + url.PathEscape(workspace) + "/document/" + url.PathEscape(slug)
	if base := strings.TrimRight(stringArg(args, "__flowBaseURL"), "/"); base != "" {
		return base + path
	}
	return path
}

// searchMCPDocuments answers search_documents: several keyword sets, merged
// like search_issues, each hit with where it lives and a snippet to quote.
func (s *server) searchMCPDocuments(ctx context.Context, data domain.Bootstrap, args map[string]any) (any, error) {
	queries := stringsArg(args, "queries")
	if len(queries) == 0 || len(queries) > 5 {
		return nil, fmt.Errorf("provide 1 to 5 queries")
	}
	candidates := slices.Clone(data.Documents)
	var team *domain.Team
	if value := stringArg(args, "team"); value != "" {
		found, err := mcpFindTeam(data, value)
		if err != nil {
			return nil, err
		}
		team = &found
	}
	var project *domain.Project
	if value := stringArg(args, "project"); value != "" {
		found, err := mcpFindProject(data, value)
		if err != nil {
			return nil, err
		}
		project = &found
	}
	candidates = slices.DeleteFunc(candidates, func(item domain.Document) bool {
		return !boolArg(args, "includeArchived") && item.ArchivedAt != nil || team != nil && !slices.Contains(item.TeamIDs, team.ID) || project != nil && !slices.Contains(item.ProjectIDs, project.ID)
	})
	limit := min(max(intArg(args, "limit", 10), 1), 25)
	type hit struct {
		best    documentMatch
		score   int
		terms   []string
		queries []string
	}
	hits := map[string]*hit{}
	for _, query := range queries {
		matches, err := s.rankDocumentMatches(ctx, data.Workspace.URLKey, candidates, query, true)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			current := hits[match.document.ID]
			if current == nil {
				current = &hit{best: match}
				hits[match.document.ID] = current
			}
			if match.score > current.best.score {
				current.best = match
			}
			if !slices.Contains(current.queries, query) {
				current.queries = append(current.queries, query)
				current.score += match.score
			}
			for _, term := range match.terms {
				if !slices.Contains(current.terms, term) {
					current.terms = append(current.terms, term)
				}
			}
		}
	}
	ranked := make([]*hit, 0, len(hits))
	for _, item := range hits {
		ranked = append(ranked, item)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].best.document.UpdatedAt.After(ranked[j].best.document.UpdatedAt)
	})
	projects := map[string]domain.Project{}
	for _, item := range data.Projects {
		projects[item.ID] = item
	}
	teams := map[string]domain.Team{}
	for _, item := range data.Teams {
		teams[item.ID] = item
	}
	items := []map[string]any{}
	for _, item := range ranked[:min(limit, len(ranked))] {
		document := item.best.document
		result := map[string]any{"id": document.ID, "slugId": document.SlugID, "title": document.Title, "url": mcpDocumentURL(data.Workspace.URLKey, document.SlugID, args), "snippet": item.best.snippet, "matchedTerms": item.terms, "allTermsMatched": item.best.all, "matchedQueries": item.queries, "score": item.score, "updatedAt": document.UpdatedAt}
		if item.best.section != "" {
			result["section"] = item.best.section
		}
		for _, id := range document.ProjectIDs {
			if parent, ok := projects[id]; ok {
				result["project"] = map[string]any{"id": parent.ID, "name": parent.Name, "slugId": parent.SlugID}
				break
			}
		}
		for _, id := range document.TeamIDs {
			if parent, ok := teams[id]; ok {
				result["team"] = map[string]any{"id": parent.ID, "key": parent.Key, "name": parent.Name}
				break
			}
		}
		if document.ArchivedAt != nil {
			result["archivedAt"] = document.ArchivedAt
		}
		items = append(items, result)
	}
	return map[string]any{"queries": queries, "items": items, "total": len(ranked)}, nil
}
