package coding

import (
	"regexp"
	"sort"
	"strings"

	"flow/api/internal/domain"
)

// Repository selection for triggers (docs/coding-sessions-design.md §15,
// track L). It is Flow's version of the reference issueRepositorySuggestions: an
// explicit environment wins; otherwise the active environments are ranked by
// the issue's linked PRs, the repositories and environments the text names,
// the issue labels, the team, and the team's recently used environments. A
// clear winner is picked; otherwise the caller asks the user with a select
// elicitation listing the suggestions.

// LinkedRepository is a repository the issue already has a PR/MR in.
type LinkedRepository struct {
	CodeHost   string `json:"codeHost,omitempty"`
	HostName   string `json:"hostName,omitempty"`
	Repository string `json:"repository"`
}

// EnvironmentSuggestionInput is what the ranking looks at.
type EnvironmentSuggestionInput struct {
	// Environments are the candidates; archived ones are ignored.
	Environments []domain.CodingEnvironment
	// ExplicitEnvironmentID is a user's or the agent's explicit choice.
	ExplicitEnvironmentID string
	// LinkedRepositories are the repos of the issue's linked PRs/MRs.
	LinkedRepositories []LinkedRepository
	// Text is the issue title, description and the request prompt.
	Text string
	// Labels are the issue's label names.
	Labels []string
	// TeamKey and TeamName identify the issue's team.
	TeamKey  string
	TeamName string
	// RecentEnvironmentIDs are the team's most recently used environments,
	// newest first.
	RecentEnvironmentIDs []string
}

// EnvironmentSuggestion is one ranked candidate.
type EnvironmentSuggestion struct {
	EnvironmentID string   `json:"environmentId"`
	Name          string   `json:"name"`
	CodeHost      string   `json:"codeHost"`
	HostName      string   `json:"hostName"`
	Repository    string   `json:"repository"`
	Score         int      `json:"score"`
	Reasons       []string `json:"reasons,omitempty"`
}

// EnvironmentPick is the outcome of PickEnvironment.
type EnvironmentPick struct {
	// EnvironmentID is set when one environment was chosen.
	EnvironmentID string `json:"environmentId,omitempty"`
	// Reason says why it was chosen: explicit | onlyEnvironment | ranked.
	Reason string `json:"reason,omitempty"`
	// Ambiguous is true when the user must choose among Suggestions.
	Ambiguous   bool                    `json:"ambiguous"`
	Suggestions []EnvironmentSuggestion `json:"suggestions"`
	// ErrorCode is noPolicyRepos when there is no usable environment and
	// repoNotInPolicy when the explicit environment is not usable.
	ErrorCode domain.CodingErrorCode `json:"errorCode,omitempty"`
}

// Ranking weights. A linked PR is the strongest signal; a full repository
// name in the text is nearly as strong; recency only breaks near-ties.
const (
	scoreLinkedRepository  = 100
	scoreFullNameMentioned = 60
	scoreRepoNameMentioned = 35
	scoreEnvNameMentioned  = 30
	scoreLabelMatch        = 20
	scoreTeamMatch         = 10
	scoreRecentFirst       = 25
	scoreRecentSecond      = 15
	scoreRecentOther       = 8

	// pickMinScore is the least score that can win on its own; pickMargin
	// is how far ahead of the runner-up the winner must be.
	pickMinScore = 20
	pickMargin   = 20
)

var nameTokenSplit = regexp.MustCompile(`[^a-z0-9]+`)

// ignoredNameTokens are repository-name fragments too generic to match on.
var ignoredNameTokens = map[string]bool{
	"app": true, "api": true, "web": true, "the": true, "and": true, "for": true, "com": true, "org": true, "www": true,
	"main": true, "core": true, "lib": true, "service": true, "server": true, "client": true, "repo": true, "src": true,
}

func nameTokens(value string) []string {
	var tokens []string
	for _, token := range nameTokenSplit.Split(strings.ToLower(value), -1) {
		if len(token) >= 3 && !ignoredNameTokens[token] {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// containsWord reports whether text contains phrase delimited by
// non-identifier characters (so "flow" does not match "workflow").
func containsWord(text, phrase string) bool {
	if phrase == "" {
		return false
	}
	for start := 0; ; {
		index := strings.Index(text[start:], phrase)
		if index < 0 {
			return false
		}
		index += start
		end := index + len(phrase)
		before := index == 0 || !isIdentByte(text[index-1])
		after := end == len(text) || !isIdentByte(text[end])
		if before && after {
			return true
		}
		start = index + 1
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '-' || b == '/' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z')
}

func repoShortName(repository string) string {
	repository = strings.TrimSuffix(strings.TrimSpace(repository), ".git")
	if index := strings.LastIndex(repository, "/"); index >= 0 {
		return repository[index+1:]
	}
	return repository
}

func sameRepository(environment domain.CodingEnvironment, linked LinkedRepository) bool {
	if !strings.EqualFold(strings.TrimSuffix(environment.Repository, ".git"), strings.TrimSuffix(linked.Repository, ".git")) {
		return false
	}
	if linked.CodeHost != "" && environment.CodeHost != "" && !strings.EqualFold(linked.CodeHost, environment.CodeHost) {
		return false
	}
	if linked.HostName != "" && environment.HostName != "" && !strings.EqualFold(linked.HostName, environment.HostName) {
		return false
	}
	return true
}

// activeEnvironments drops archived environments.
func activeEnvironments(environments []domain.CodingEnvironment) []domain.CodingEnvironment {
	active := make([]domain.CodingEnvironment, 0, len(environments))
	for _, environment := range environments {
		if environment.ArchivedAt == nil {
			active = append(active, environment)
		}
	}
	return active
}

// SuggestEnvironments ranks the active environments, best first. Ties keep
// a stable order by name, then id.
func SuggestEnvironments(input EnvironmentSuggestionInput) []EnvironmentSuggestion {
	text := strings.ToLower(input.Text)
	labels := make([]string, 0, len(input.Labels))
	for _, label := range input.Labels {
		labels = append(labels, nameTokens(label)...)
	}
	team := append(nameTokens(input.TeamKey), nameTokens(input.TeamName)...)
	recent := map[string]int{}
	for index, id := range input.RecentEnvironmentIDs {
		if _, seen := recent[id]; !seen {
			recent[id] = index
		}
	}
	var suggestions []EnvironmentSuggestion
	for _, environment := range activeEnvironments(input.Environments) {
		suggestion := EnvironmentSuggestion{
			EnvironmentID: environment.ID, Name: environment.Name, CodeHost: environment.CodeHost,
			HostName: environment.HostName, Repository: environment.Repository,
		}
		add := func(score int, reason string) {
			suggestion.Score += score
			suggestion.Reasons = append(suggestion.Reasons, reason)
		}
		for _, linked := range input.LinkedRepositories {
			if sameRepository(environment, linked) {
				add(scoreLinkedRepository, "linkedPullRequest")
				break
			}
		}
		fullName := strings.ToLower(strings.TrimSuffix(environment.Repository, ".git"))
		short := strings.ToLower(repoShortName(environment.Repository))
		switch {
		case fullName != "" && strings.Contains(fullName, "/") && containsWord(text, fullName):
			add(scoreFullNameMentioned, "repositoryMentioned")
		case len(short) >= 3 && !ignoredNameTokens[short] && containsWord(text, short):
			add(scoreRepoNameMentioned, "repositoryNameMentioned")
		}
		if name := strings.ToLower(strings.TrimSpace(environment.Name)); len(name) >= 3 && name != short && name != fullName && containsWord(text, name) {
			add(scoreEnvNameMentioned, "environmentMentioned")
		}
		repoTokens := append(nameTokens(short), nameTokens(environment.Name)...)
		if overlaps(labels, repoTokens) {
			add(scoreLabelMatch, "labelMatch")
		}
		if overlaps(team, repoTokens) {
			add(scoreTeamMatch, "teamMatch")
		}
		if index, ok := recent[environment.ID]; ok {
			switch index {
			case 0:
				add(scoreRecentFirst, "recentlyUsedByTeam")
			case 1:
				add(scoreRecentSecond, "recentlyUsedByTeam")
			default:
				add(scoreRecentOther, "recentlyUsedByTeam")
			}
		}
		suggestions = append(suggestions, suggestion)
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		if suggestions[i].Score != suggestions[j].Score {
			return suggestions[i].Score > suggestions[j].Score
		}
		if !strings.EqualFold(suggestions[i].Name, suggestions[j].Name) {
			return strings.ToLower(suggestions[i].Name) < strings.ToLower(suggestions[j].Name)
		}
		return suggestions[i].EnvironmentID < suggestions[j].EnvironmentID
	})
	return suggestions
}

func overlaps(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if a == b {
				return true
			}
		}
	}
	return false
}

// PickEnvironment chooses the environment for a trigger: the explicit one,
// the only active one, or a clear ranking winner. Otherwise it reports
// Ambiguous with the ranked suggestions for a select elicitation.
func PickEnvironment(input EnvironmentSuggestionInput) EnvironmentPick {
	active := activeEnvironments(input.Environments)
	suggestions := SuggestEnvironments(input)
	if input.ExplicitEnvironmentID != "" {
		for _, environment := range active {
			if environment.ID == input.ExplicitEnvironmentID {
				return EnvironmentPick{EnvironmentID: environment.ID, Reason: "explicit", Suggestions: suggestions}
			}
		}
		return EnvironmentPick{ErrorCode: domain.CodingErrRepoNotInPolicy, Suggestions: suggestions}
	}
	switch len(active) {
	case 0:
		return EnvironmentPick{ErrorCode: domain.CodingErrNoPolicyRepos, Suggestions: suggestions}
	case 1:
		return EnvironmentPick{EnvironmentID: active[0].ID, Reason: "onlyEnvironment", Suggestions: suggestions}
	}
	top := suggestions[0]
	second := suggestions[1]
	if top.Score >= pickMinScore && top.Score-second.Score >= pickMargin {
		return EnvironmentPick{EnvironmentID: top.EnvironmentID, Reason: "ranked", Suggestions: suggestions}
	}
	return EnvironmentPick{Ambiguous: true, Suggestions: suggestions}
}

// FilterEnvironments keeps the environments whose id is in allowed.
func FilterEnvironments(environments []domain.CodingEnvironment, allowed func(domain.CodingEnvironment) bool) []domain.CodingEnvironment {
	kept := make([]domain.CodingEnvironment, 0, len(environments))
	for _, environment := range environments {
		if allowed(environment) {
			kept = append(kept, environment)
		}
	}
	return kept
}
