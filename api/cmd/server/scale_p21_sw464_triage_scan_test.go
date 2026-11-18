package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// scaleP21SW464Text draws words from a small vocabulary so that issues share
// some tokens, most share none, and equal texts tie.
func scaleP21SW464Text(r *rand.Rand, words int) string {
	vocabulary := []string{"widget", "crash", "login", "export", "panel", "sync", "queue", "render", "token", "cache", "Δelta", "数据", "a", "x1", "2fa", "UPPER", "upper"}
	var parts []string
	for range words {
		if r.IntN(3) == 0 {
			parts = append(parts, vocabulary[r.IntN(len(vocabulary))])
		} else {
			parts = append(parts, fmt.Sprintf("u%d", r.IntN(400)))
		}
	}
	separators := []string{" ", ", ", "-", "/", "  "}
	return strings.Join(parts, separators[r.IntN(len(separators))])
}

func scaleP21SW464Issue(r *rand.Rand, id string, at time.Time) domain.Issue {
	teams := []string{"team-a", "team-b", "team-c"}
	issue := domain.Issue{
		ID: id, Identifier: fmt.Sprintf("SW-%d", r.IntN(50)), Title: scaleP21SW464Text(r, 1+r.IntN(8)),
		Description: scaleP21SW464Text(r, r.IntN(30)), Team: domain.Team{ID: teams[r.IntN(len(teams))]},
		State:     domain.WorkflowState{Type: []string{"backlog", "unstarted", "started", "completed", "canceled"}[r.IntN(5)]},
		UpdatedAt: at.Add(time.Duration(r.IntN(4)) * time.Minute),
	}
	if r.IntN(4) == 0 {
		issue.Description = strings.Repeat(scaleP21SW464Text(r, 3)+" ", 1+r.IntN(2000))
	}
	if r.IntN(8) == 0 {
		archived := at
		issue.ArchivedAt = &archived
	}
	if r.IntN(3) == 0 {
		issue.Project = &domain.ProjectSummary{ID: fmt.Sprintf("p%d", r.IntN(3))}
	}
	if r.IntN(3) == 0 {
		issue.Assignee = &domain.User{ID: fmt.Sprintf("user-%d", r.IntN(3))}
	}
	for range r.IntN(3) {
		issue.Labels = append(issue.Labels, domain.IssueLabel{ID: fmt.Sprintf("l%d", r.IntN(4))})
	}
	return issue
}

// The candidate index returns exactly what similarTriageIssues returns: the
// same candidates, scores and order (ties included), over randomized streams
// with repeated IDs and texts, archived and closed candidates, fields edited
// between queries, appended positions and more qualifying candidates than
// the 2000 cut-off.
func TestScaleP21SW464TriageCandidateIndexMatchesScan(t *testing.T) {
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for seed := range 60 {
		r := rand.New(rand.NewPCG(uint64(seed), 464))
		data := &domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{}}
		for index := range 3 {
			data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p%d", index), Name: scaleP21SW464Text(r, 3),
				Summary: scaleP21SW464Text(r, 4), Description: scaleP21SW464Text(r, 200)})
		}
		data.Projects[0].Lead = &domain.User{ID: "user-0"}
		for index := range 4 {
			data.Labels = append(data.Labels, domain.IssueLabel{ID: fmt.Sprintf("l%d", index), Name: scaleP21SW464Text(r, 2), Description: scaleP21SW464Text(r, 5)})
		}
		size := 20 + r.IntN(200)
		if seed%15 == 0 {
			// More than 2000 qualifying candidates: one shared text.
			size = 2600
		}
		for index := range size {
			issue := scaleP21SW464Issue(r, fmt.Sprintf("i%d", r.IntN(size/2+1)), at)
			if seed%15 == 0 {
				issue.Title, issue.ArchivedAt, issue.State.Type = "widget crash login", nil, "backlog"
			}
			if index > 0 && r.IntN(10) == 0 {
				// A repeated summary of an earlier position.
				issue = data.Issues[r.IntN(index)]
			}
			data.Issues = append(data.Issues, issue)
		}
		index := newTriageCandidateIndex(nil)
		for query := range 25 {
			if query%5 == 4 {
				// Positions appended between queries, and fields that
				// suggestion actions edit (never titles or descriptions).
				for range r.IntN(40) {
					data.Issues = append(data.Issues, scaleP21SW464Issue(r, fmt.Sprintf("i%d", r.IntN(size)), at))
				}
				for range 10 {
					edited := &data.Issues[r.IntN(len(data.Issues))]
					edited.Team.ID = []string{"team-a", "team-b", "team-c"}[r.IntN(3)]
					edited.State.Type = []string{"backlog", "completed"}[r.IntN(2)]
					edited.Labels = append(edited.Labels, domain.IssueLabel{ID: fmt.Sprintf("l%d", r.IntN(4))})
				}
			}
			issue := data.Issues[r.IntN(len(data.Issues))]
			if r.IntN(3) == 0 {
				issue = scaleP21SW464Issue(r, "probe", at)
			}
			if seed%15 == 0 {
				issue.Title = "widget crash login"
			}
			want := similarTriageIssues(data, &issue)
			got := index.similarTriageIssues(data, &issue)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("seed %d query %d: index returned %d candidates, scan %d", seed, query, len(got), len(want))
			}
			if seed%15 == 0 && len(want) != maxTriageSuggestionCandidates {
				t.Fatalf("seed %d: %d candidates, want the cut-off", seed, len(want))
			}
		}
	}
}

// scaleP21SW465Size is the batch of the SW465 low-score fixture; a
// measurement run sets FLOW_SW465_SIZE (350, 1000).
func scaleP21SW465Size(t *testing.T) int {
	if raw := os.Getenv("FLOW_SW465_SIZE"); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 100 {
			t.Fatalf("FLOW_SW465_SIZE=%q", raw)
		}
		return size
	}
	return 150
}

// scaleP21SW465Titles makes the bulk issues search candidates of each other
// (every title has "widget") that almost all score below the 0.14 cut-off:
// one shared token among eight. Every 25th issue also shares "alpha beta",
// with the tied candidate-only issues too, every 7th issue's description
// shares "notice", and every 11th issue has a long description.
func scaleP21SW465Titles(t *testing.T, f scaleP21IssueUpdateFixture) {
	t.Helper()
	at := time.Date(2026, 1, 6, 7, 8, 9, 0, time.UTC)
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw465_titles", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Issues {
			issue := &data.Issues[index]
			if strings.HasPrefix(issue.ID, "sw400-open-") {
				// Candidate-only issues outside the batch with equal
				// texts and update times: their equal scores tie, and
				// the scan order breaks the tie.
				issue.Title, issue.Description, issue.UpdatedAt = "widget alpha beta tie rare", "notice tie", at.Add(-time.Hour)
				continue
			}
			var number int
			if _, err := fmt.Sscanf(issue.ID, "sw400-bulk-%d", &number); err != nil {
				continue
			}
			// Distinct update times: legacy orders the scoped issues by map
			// iteration, so equal scores must not tie on them.
			issue.UpdatedAt = at.Add(time.Duration(number) * time.Second)
			words := []string{"widget"}
			for k := range 7 {
				words = append(words, fmt.Sprintf("q%04dw%d", number, k))
			}
			if number%25 == 0 {
				words[1], words[2] = "alpha", "beta"
			}
			issue.Title = strings.Join(words, " ")
			description := fmt.Sprintf("q%04dd1 q%04dd2", number, number)
			if number%7 == 0 {
				description += " notice"
			}
			if number%11 == 0 {
				description += strings.Repeat(fmt.Sprintf(" q%04dlong", number), 900)
			}
			issue.Description = description
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Triage generation over a batch whose candidates mostly score below the
// cut-off (each issue's search returns the other batch issues): the legacy,
// warm and SQL-reloaded generations with the candidate index equal each other
// and the literal scans (index off) byte for byte, response, every row,
// event, publication and installed image. Each run logs its generation time.
// FLOW_SW465_SIZE sets the batch; FLOW_SW465_SCAN=0 skips the literal scans.
func TestScaleP21SW465LowScoreTriageGenerationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21IssueUpdateSetup(t)
	ids := scaleP21SW400Bulk(t, f, scaleP21SW465Size(t))
	scaleP21SW465Titles(t, f)
	full, ok := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{issue: f.triage.ID, setup: scaleP21SW400Actions(f, func(settings *domain.TriageIntelligenceSettings) {
		settings.RelatedAction, settings.LabelAction = "auto", "auto"
	})})
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	defer func() { triageCandidateIndexOff = false }()
	generate := f.sw440Generate(t, ids)
	type run struct {
		source string
		scan   bool
	}
	// Each index run is compared with the literal scan of its source, then
	// every source with legacy.
	runs := []run{{"legacy", false}, {"warm", false}, {"reloaded", false}}
	if os.Getenv("FLOW_SW465_SCAN") != "0" {
		runs = []run{{"legacy", true}, {"legacy", false}, {"warm", true}, {"warm", false}, {"reloaded", false}}
	}
	outcomes := map[run]string{}
	var legacy scaleP21IssueUpdateOutcome
	for _, item := range runs {
		var took time.Duration
		triageCandidateIndexOff = item.scan
		got, started := f.sw460Run(t, item.source, func(*testing.T) {}, func(service *server) *httptest.ResponseRecorder {
			begin := time.Now()
			w := generate(service)
			took = time.Since(begin)
			return w
		}, true)
		triageCandidateIndexOff = false
		mode := "index"
		if item.scan {
			mode = "scan"
		}
		t.Logf("%d issues, %s %s: generation %.2fs", len(ids), item.source, mode, took.Seconds())
		normalized := scaleP21IssueUpdateNormalize(t, got, started)
		if dir := os.Getenv("FLOW_SW465_DUMP"); dir != "" {
			_ = os.WriteFile(dir+"/"+item.source+"-"+mode+".json", []byte(normalized), 0o600)
		}
		if _, seen := outcomes[item]; !seen {
			outcomes[item] = normalized
		}
		if item == (run{"legacy", false}) {
			legacy = got
		}
		if want, found := outcomes[run{item.source, true}]; found && normalized != want {
			t.Fatalf("%s: the index changed the literal scan: %s", item.source, scaleP21M2Diff(want, normalized))
		}
		if want, found := outcomes[run{"legacy", false}]; found && !item.scan && normalized != want {
			t.Fatalf("%s changed: %s", item.source, scaleP21M2Diff(want, normalized))
		}
	}
	if legacy.Code != http.StatusOK || len(legacy.State.Events) != 1 {
		t.Fatalf("legacy status %d with %d events: %.300s", legacy.Code, len(legacy.State.Events), legacy.Body)
	}
	related, accepted := 0, 0
	for _, row := range legacy.State.Suggestions {
		data := row[2].(string)
		if strings.Contains(data, `"type":"relatedIssue"`) {
			related++
			if strings.Contains(data, `"state":"accepted"`) {
				accepted++
			}
		}
	}
	t.Logf("legacy: %d related-issue suggestions (%d accepted)", related, accepted)
	if related == 0 || related > len(ids) {
		t.Fatalf("legacy made %d related-issue suggestions for %d issues; the fixture must keep most candidates below the cut-off", related, len(ids))
	}
}
