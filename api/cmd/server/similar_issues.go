package main

import (
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"flow/api/internal/domain"
)

// Similar issues ("Possible duplicates"): a TF-IDF vector-space ranking over
// title and description. Tokens are stemmed and folded onto canonical
// synonyms, and CJK text is split into character bigrams, so "Login page
// broken" matches "Sign-in defect on the login screen". Candidates come from
// the access-checked search index, so the whole workspace is compared rather
// than only issues the client has loaded.

var similarStopWords = map[string]bool{"the": true, "a": true, "an": true, "and": true, "or": true, "to": true, "of": true, "in": true, "on": true, "for": true, "with": true, "is": true, "are": true, "be": true, "when": true, "from": true, "at": true, "by": true, "it": true, "not": true, "can": true, "should": true, "this": true, "that": true, "we": true, "i": true, "as": true, "if": true, "do": true, "does": true, "was": true, "into": true, "after": true, "before": true}

var similarCanonical = func() map[string]string {
	result := map[string]string{}
	for canonical, values := range semanticSynonyms {
		for _, value := range values {
			result[value] = canonical
		}
	}
	for _, group := range [][]string{{"login", "signin", "sign", "logon"}, {"crash", "crashes", "freeze", "hang"}, {"slow", "latency", "lag", "performance"}, {"delete", "remove"}, {"create", "add", "new"}, {"screen", "page", "view"}} {
		for _, value := range group[1:] {
			result[value] = group[0]
		}
	}
	return result
}()

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

func similarStem(token string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(token) > len(suffix)+2 && strings.HasSuffix(token, suffix) {
			return strings.TrimSuffix(token, suffix)
		}
	}
	return token
}

func similarTokens(text string) []string {
	result := []string{}
	for _, word := range strings.Fields(normalizeSearch(text)) {
		runes := []rune(word)
		if len(runes) > 0 && isCJK(runes[0]) {
			if len(runes) == 1 {
				result = append(result, word)
			}
			for index := 0; index+1 < len(runes); index++ {
				result = append(result, string(runes[index:index+2]))
			}
			continue
		}
		if len(runes) < 2 || similarStopWords[word] {
			continue
		}
		if canonical, ok := similarCanonical[word]; ok {
			word = canonical
		}
		stem := similarStem(word)
		if canonical, ok := similarCanonical[stem]; ok {
			stem = canonical
		}
		result = append(result, stem)
	}
	return result
}

// similarVector weights title terms twice as heavily as description terms.
func similarVector(issue domain.Issue) map[string]float64 {
	vector := map[string]float64{}
	for _, token := range similarTokens(issue.Title) {
		vector[token] += 2
	}
	description := issue.Description
	if len(description) > 4000 {
		description = description[:4000]
	}
	for _, token := range similarTokens(description) {
		vector[token]++
	}
	return vector
}

type similarIssueResult struct {
	Issue             domain.Issue `json:"issue"`
	Score             float64      `json:"score"`
	PossibleDuplicate bool         `json:"possibleDuplicate"`
	MatchedTerms      []string     `json:"matchedTerms"`
}

// rankSimilarIssues scores candidates against target with TF-IDF cosine similarity.
func rankSimilarIssues(target domain.Issue, candidates []domain.Issue, limit int, threshold float64) []similarIssueResult {
	excluded := map[string]bool{target.ID: true}
	if target.ParentID != nil {
		excluded[*target.ParentID] = true
	}
	for _, id := range target.SubIssueIDs {
		excluded[id] = true
	}
	for _, relation := range target.Relations {
		excluded[relation.RelatedIssueID] = true
	}
	pool := []domain.Issue{}
	for _, candidate := range candidates {
		if !excluded[candidate.ID] && candidate.ArchivedAt == nil && candidate.State.Type != "canceled" {
			excluded[candidate.ID] = true
			pool = append(pool, candidate)
		}
	}
	vectors := make([]map[string]float64, len(pool))
	frequency := map[string]int{}
	targetVector := similarVector(target)
	for term := range targetVector {
		frequency[term]++
	}
	for index, candidate := range pool {
		vectors[index] = similarVector(candidate)
		for term := range vectors[index] {
			frequency[term]++
		}
	}
	documents := float64(len(pool) + 1)
	weigh := func(vector map[string]float64) (map[string]float64, float64) {
		weighted, norm := map[string]float64{}, 0.0
		for term, count := range vector {
			value := (1 + math.Log(count)) * math.Log(1+documents/float64(frequency[term]))
			weighted[term] = value
			norm += value * value
		}
		return weighted, math.Sqrt(norm)
	}
	targetWeights, targetNorm := weigh(targetVector)
	if targetNorm == 0 {
		return []similarIssueResult{}
	}
	results := []similarIssueResult{}
	for index, candidate := range pool {
		weights, norm := weigh(vectors[index])
		if norm == 0 {
			continue
		}
		dot, matched := 0.0, []string{}
		for term, value := range weights {
			if other, ok := targetWeights[term]; ok {
				dot += value * other
				matched = append(matched, term)
			}
		}
		score := dot / (norm * targetNorm)
		if score < threshold {
			continue
		}
		sort.Strings(matched)
		candidate.Description, candidate.DescriptionState, candidate.DocumentContent, candidate.Attachments = "", "", nil, nil
		results = append(results, similarIssueResult{Issue: candidate, Score: math.Round(score*1000) / 1000, PossibleDuplicate: score >= 0.6, MatchedTerms: matched})
	}
	sort.SliceStable(results, func(left, right int) bool { return results[left].Score > results[right].Score })
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func (s *server) getIssueRecordSimilar(w http.ResponseWriter, r *http.Request) {
	_, query, err := s.issueRecordsQuery(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	target, err := s.store.AuthorizedIssueRecord(r.Context(), query, r.PathValue("id"))
	if err != nil {
		issueDetailReadError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 20 {
		limit = 5
	}
	// Query the index with the target's most distinctive raw words (title first).
	terms := []string{}
	for _, word := range append(strings.Fields(normalizeSearch(target.Title)), strings.Fields(normalizeSearch(target.Description))...) {
		if len([]rune(word)) < 2 || similarStopWords[word] || slices.Contains(terms, word) {
			continue
		}
		terms = append(terms, word)
		for canonical, values := range semanticSynonyms {
			if word == canonical || slices.Contains(values, word) {
				for _, value := range append([]string{canonical}, values...) {
					if !slices.Contains(terms, value) {
						terms = append(terms, value)
					}
				}
			}
		}
		if len(terms) >= 24 {
			break
		}
	}
	if len(terms) > 32 {
		terms = terms[:32]
	}
	query.Archived = "false"
	candidates := []domain.Issue{}
	if len(terms) > 0 {
		if err := s.store.SearchIssueCandidateTerms(r.Context(), query, terms, nil, 300, func(issue domain.Issue) error {
			candidates = append(candidates, issue)
			return nil
		}); err != nil {
			issueRecordsError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": rankSimilarIssues(target, candidates, limit, 0.2)})
}
