package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// longCJKTitle is length CJK characters starting at seed, so each run of the
// test on a kept external database has titles of its own.
func longCJKTitle(length, seed int) string {
	var builder strings.Builder
	for index := 0; index < length; index++ {
		builder.WriteRune(rune(0x4e00 + (seed+index*7919)%5000))
	}
	return builder.String()
}

func longEnglishTitle(maxLength, seed int) string {
	words := []string{"billing", "export", "migration", "dashboard", "staging", "invoice", "retry", "webhook", "latency", "report"}
	var builder strings.Builder
	for index := 0; ; index++ {
		word := fmt.Sprintf("%s%d", words[(seed+index)%len(words)], seed+index)
		if builder.Len()+len(word)+1 > maxLength {
			return builder.String()
		}
		if index > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(word)
	}
}

// Issue titles at the 2,048-character limit, CJK or English, find their
// duplicate as a triage candidate and get a similar-issue suggestion. On
// MySQL the full title used to be one full-text phrase of over 128 ngram
// tokens, refused with Error 191, so the issue got no suggestions. Runs on
// SQLite and, with FLOW_TEST_DATABASE_DRIVER/URL, on MySQL or Postgres.
func TestTriageCandidatesForLongTitles(t *testing.T) {
	for name, repo := range favoriteTabTestStores(t) {
		t.Run(name, func(t *testing.T) { testTriageCandidatesForLongTitles(t, name, repo) })
	}
}

func testTriageCandidatesForLongTitles(t *testing.T, driver string, repo *store.SQLiteStore) {
	ctx := t.Context()
	data := repo.Bootstrap()
	team, state := data.Teams[0], data.States[0]
	stamp := time.Now().UnixNano()
	seed := int(stamp/1000) % 100000
	titles := map[string]string{
		"cjk":     longCJKTitle(domain.IssueTitleMaxLength, seed),
		"english": longEnglishTitle(domain.IssueTitleMaxLength, seed),
	}
	service := &server{store: repo}
	query := store.IssueRecordQuery{Workspace: data.Workspace.URLKey, Archived: "false"}
	for kind, title := range titles {
		t.Run(kind, func(t *testing.T) {
			if got := len([]rune(title)); got > domain.IssueTitleMaxLength || got < domain.IssueTitleMaxLength-20 {
				t.Fatalf("title has %d characters", got)
			}
			candidate := domain.Issue{ID: fmt.Sprintf("long-title-%s-%d", kind, stamp), Identifier: fmt.Sprintf("LONG-%d%d", len(kind), seed), Title: title, Description: "Existing report with the same long title.", Team: team, State: state, Labels: []domain.IssueLabel{}}
			if err := repo.ImportIssues(ctx, data.Workspace.URLKey, []domain.Issue{candidate}); err != nil {
				t.Fatal(err)
			}
			probe := candidate
			probe.ID, probe.Identifier = candidate.ID+"-probe", candidate.Identifier+"9"
			var items []domain.Issue
			found := false
			// InnoDB can leave a just-committed row out of a BOOLEAN MODE
			// match of several phrases for a moment (under 200 ms here);
			// an issue filed earlier is always there.
			for attempt := 0; attempt < 20 && !found; attempt++ {
				if attempt > 0 {
					if driver != "mysql" {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				var err error
				items, err = service.triageCandidateIssues(ctx, &probe, query)
				if err != nil {
					t.Fatalf("candidate search: %v", err)
				}
				for _, item := range items {
					found = found || item.ID == candidate.ID
				}
			}
			if !found {
				t.Fatalf("the issue with the same %d-character title is not a candidate (%d candidates)", len([]rune(title)), len(items))
			}
			snapshot := data
			snapshot.Issues = append(append([]domain.Issue{}, items...), probe)
			suggestions := generateIssueSuggestions(&snapshot, &snapshot.Issues[len(snapshot.Issues)-1], time.Now().UTC())
			similar := false
			for _, suggestion := range suggestions {
				similar = similar || suggestion.Type == "similarIssue" && suggestion.SuggestedIssueID == candidate.ID
			}
			if !similar {
				t.Fatalf("no similar-issue suggestion for the duplicate: %#v", suggestions)
			}
		})
	}
}
