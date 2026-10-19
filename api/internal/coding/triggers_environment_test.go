package coding

import (
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestSuggestEnvironmentsRanking(t *testing.T) {
	archivedAt := time.Now()
	web := env("e-web", "Web", "acme/web")
	api := env("e-api", "Backend", "acme/payments-api")
	mobile := env("e-mobile", "Mobile", "acme/mobile")
	archived := env("e-old", "Old web", "acme/legacy")
	archived.ArchivedAt = &archivedAt
	gitlabWeb := domain.CodingEnvironment{ID: "e-glweb", Name: "GL web", CodeHost: domain.CodeHostGitLab, HostName: "gitlab.example.com", Repository: "acme/web"}
	all := []domain.CodingEnvironment{web, api, mobile, archived}

	cases := []struct {
		name      string
		input     EnvironmentSuggestionInput
		wantFirst string
		wantPick  string
		ambiguous bool
		errorCode domain.CodingErrorCode
	}{
		{
			name:      "linked pull request wins",
			input:     EnvironmentSuggestionInput{Environments: all, LinkedRepositories: []LinkedRepository{{CodeHost: "github", HostName: "github.com", Repository: "acme/payments-api"}}, RecentEnvironmentIDs: []string{"e-web"}},
			wantFirst: "e-api", wantPick: "e-api",
		},
		{
			name:      "full repository name in the description",
			input:     EnvironmentSuggestionInput{Environments: all, Text: "Crash in acme/mobile when opening settings"},
			wantFirst: "e-mobile", wantPick: "e-mobile",
		},
		{
			name:      "short repository name as a word",
			input:     EnvironmentSuggestionInput{Environments: all, Text: "The mobile app crashes"},
			wantFirst: "e-mobile", wantPick: "e-mobile",
		},
		{
			name:      "short name inside another word does not match",
			input:     EnvironmentSuggestionInput{Environments: all, Text: "automobile insurance form"},
			ambiguous: true,
		},
		{
			name:      "labels and team",
			input:     EnvironmentSuggestionInput{Environments: all, Labels: []string{"payments"}, TeamKey: "PAY", TeamName: "Payments"},
			wantFirst: "e-api", wantPick: "e-api",
		},
		{
			name:      "team recency alone breaks the tie",
			input:     EnvironmentSuggestionInput{Environments: all, RecentEnvironmentIDs: []string{"e-mobile", "e-web"}},
			wantFirst: "e-mobile", ambiguous: true, // 25 vs 15 is not a clear margin
		},
		{
			name:      "two linked repositories are ambiguous",
			input:     EnvironmentSuggestionInput{Environments: all, LinkedRepositories: []LinkedRepository{{Repository: "acme/web"}, {Repository: "acme/mobile"}}},
			ambiguous: true,
		},
		{
			name:      "host must match for a linked repository",
			input:     EnvironmentSuggestionInput{Environments: []domain.CodingEnvironment{web, gitlabWeb}, LinkedRepositories: []LinkedRepository{{CodeHost: "gitlab", HostName: "gitlab.example.com", Repository: "acme/web"}}},
			wantFirst: "e-glweb", wantPick: "e-glweb",
		},
		{
			name:     "explicit environment wins over ranking",
			input:    EnvironmentSuggestionInput{Environments: all, ExplicitEnvironmentID: "e-web", Text: "acme/mobile"},
			wantPick: "e-web", wantFirst: "e-mobile",
		},
		{
			name:      "explicit archived environment is refused",
			input:     EnvironmentSuggestionInput{Environments: all, ExplicitEnvironmentID: "e-old"},
			errorCode: domain.CodingErrRepoNotInPolicy, wantFirst: "e-api",
		},
		{
			name:     "single active environment",
			input:    EnvironmentSuggestionInput{Environments: []domain.CodingEnvironment{web, archived}},
			wantPick: "e-web", wantFirst: "e-web",
		},
		{
			name:      "no environments",
			input:     EnvironmentSuggestionInput{Environments: []domain.CodingEnvironment{archived}},
			errorCode: domain.CodingErrNoPolicyRepos,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pick := PickEnvironment(c.input)
			if pick.ErrorCode != c.errorCode || pick.Ambiguous != c.ambiguous || pick.EnvironmentID != c.wantPick {
				t.Fatalf("pick = %+v", pick)
			}
			for _, suggestion := range pick.Suggestions {
				if suggestion.EnvironmentID == "e-old" {
					t.Fatal("archived environments are never suggested")
				}
			}
			if c.wantFirst != "" && (len(pick.Suggestions) == 0 || pick.Suggestions[0].EnvironmentID != c.wantFirst) {
				t.Fatalf("first suggestion = %+v", pick.Suggestions)
			}
		})
	}
}

func TestSuggestEnvironmentsStableOrder(t *testing.T) {
	input := EnvironmentSuggestionInput{Environments: []domain.CodingEnvironment{env("b", "Beta", "x/beta"), env("a", "alpha", "x/alpha"), env("c", "Alpha", "x/gamma")}}
	got := SuggestEnvironments(input)
	if got[0].EnvironmentID != "a" || got[1].EnvironmentID != "c" || got[2].EnvironmentID != "b" {
		t.Fatalf("order = %+v", got)
	}
	if len(got[0].Reasons) != 0 || got[0].Score != 0 {
		t.Fatalf("no signal means no score: %+v", got[0])
	}
}
