package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// triageWriterBudgetSize is the triage scope of the triage writer budget fixtures; a measurement
// run may raise it with FLOW_SW440_SIZE.
func triageWriterBudgetSize(t *testing.T) int {
	if raw := os.Getenv("FLOW_SW440_SIZE"); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 100 {
			t.Fatalf("FLOW_SW440_SIZE=%q", raw)
		}
		return size
	}
	return 1000
}

// triageWriterBudgetSetup is the triage batch bulk scope with a realistic history on
// every issue (two comments, two update activities and their notifications)
// plus scope edges: a group of issues in the private triage team whose
// cross-team members a team auto-move pulls in, issues of a team without
// triage, archived issues still in triage, and a trashed issue ID.
func triageWriterBudgetSetup(t *testing.T, size int) (scaleIssueUpdateFixture, []string) {
	t.Helper()
	f := scaleIssueUpdateSetup(t)
	ids := triageBatchBulk(t, f, size)
	at := time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)
	member := f.principals[2].user
	trashed := "sw440-trashed"
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw440_history", "", nil, func(data *domain.Bootstrap) error {
		plain := domain.Team{ID: "sw440-plain", Name: "No triage", Key: "SWP"}
		data.Teams = append(data.Teams, plain)
		data.TeamSettings[plain.ID] = domain.TeamSettings{TeamID: plain.ID}
		var private domain.Team
		for _, team := range data.Teams {
			if team.ID == f.private {
				private = team
			}
		}
		for index := range data.Issues {
			issue := &data.Issues[index]
			if strings.HasPrefix(issue.ID, "sw400-") {
				// Distinct update times: legacy orders the scoped issues by
				// map iteration, so equal scores must not tie on them.
				issue.UpdatedAt = at.Add(time.Duration(index) * time.Second)
			}
			var number int
			if _, err := fmt.Sscanf(issue.ID, "sw400-bulk-%d", &number); err != nil {
				continue
			}
			group := number / 20
			switch {
			case group%6 == 2 && number%5 != 0:
				// The group's majority moves to the private triage team.
				issue.Team = private
			case group%6 == 4 && number%5 != 0:
				issue.Team = plain
			}
			if number%97 == 3 {
				archived := at
				issue.ArchivedAt = &archived
			}
		}
		recent := time.Now().UTC().Add(-2 * time.Minute)
		for index, id := range ids {
			for step := range 2 {
				created := at.Add(time.Duration(step) * time.Minute)
				if step == 1 && index%3 == 0 {
					// A recent edit by the viewer: a team move merges into it.
					created = recent
				}
				activity := domain.ActivityEvent{ID: fmt.Sprintf("sw440-a-%04d-%d", index, step), Type: "issue.updated", CreatedAt: created,
					Actor: f.data.Viewer, Metadata: map[string]string{"priority": fmt.Sprint(step + 1), "priorityBefore": fmt.Sprint(step)}}
				data.Activities[id] = append(data.Activities[id], activity)
				data.Notifications = append(data.Notifications, domain.Notification{ID: fmt.Sprintf("sw440-n-%04d-%d", index, step), RecipientID: member.ID, SourceType: "issue", SourceID: id, ActivityID: activity.ID, Actor: f.data.Viewer,
					IssueID: id, Type: "issue.updated", Category: "updates", GroupKey: "issue:" + id, OccurrenceCount: 1, CreatedAt: activity.CreatedAt})
				author := f.data.Viewer
				if step == 1 {
					author = member
				}
				data.Comments[id] = append(data.Comments[id], domain.Comment{ID: fmt.Sprintf("sw440-c-%04d-%d", index, step), Version: 1,
					Body: fmt.Sprintf("Seen again on build %d while saving a note", index*2+step), Reactions: map[string][]string{}, CreatedAt: at.Add(time.Duration(step) * time.Hour), User: author})
			}
		}
		// A document attached to a bulk issue that a team auto-move moves.
		data.Documents = append(data.Documents, domain.Document{ID: "sw440-doc", SlugID: "sw440-doc", Title: "Bulk notes", Content: "Notes",
			IssueID: "sw400-bulk-0000", IssueTeamID: triageBatchTeam, InheritedTeamIDs: []string{}, ProjectIDs: []string{}, TeamIDs: []string{},
			SubscriberIDs: []string{}, CreatedAt: at, UpdatedAt: at})
		data.Trash = append(data.Trash, domain.TrashEntry{ID: "sw440-trash", ResourceType: "issue", ResourceID: trashed, Title: "Trashed triage issue",
			Payload: []byte(`{"id":"` + trashed + `"}`), DeletedBy: f.data.Viewer, DeletedAt: at, ExpiresAt: at.AddDate(0, 1, 0)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	full, ok := f.repository.BootstrapFor(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("workspace missing")
	}
	var err error
	if f.baseline, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	// The requested scope keeps 1000 IDs: a trashed ID and a repeated ID
	// replace the last two bulk issues.
	scope := append(ids[:len(ids)-2:len(ids)-2], trashed, ids[5])
	return f, scope
}

// generate calls the triage generation entry the batch route uses after a
// committed batch, with the batch request's context.
func (f scaleIssueUpdateFixture) triageWriterBudgetGenerate(t *testing.T, ids []string) func(*server) *httptest.ResponseRecorder {
	return func(service *server) *httptest.ResponseRecorder {
		r := f.literalRequest(f.data.Viewer, nil, nil)
		r = r.WithContext(store.WithIssueRecordMutations(store.ContextWithActor(r.Context(), f.data.Viewer), ids...))
		r, closeOutput := beginRequestResourceReads(r)
		defer closeOutput()
		results, err := service.generateTriageIntelligenceForIssues(r.Context(), f.data.Workspace.URLKey, ids)
		w := httptest.NewRecorder()
		switch {
		case errors.Is(err, store.ErrNoMutation):
			writeJSON(w, http.StatusNoContent, results)
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusOK, results)
		}
		return w
	}
}

// sw440Check asserts what the legacy run of a case must have done for the
// differential to be meaningful.
func triageWriterBudgetCheck(t *testing.T, legacy scaleIssueUpdateOutcome, started time.Time, private string, auto, moved bool) {
	t.Helper()
	if legacy.Code != http.StatusOK {
		t.Fatalf("legacy status %d: %s", legacy.Code, legacy.Body)
	}
	var issues []domain.Issue
	if err := json.Unmarshal(legacy.Body, &issues); err != nil {
		t.Fatal(err)
	}
	generated, toPrivate, renumbered, archived := 0, 0, 0, 0
	for _, issue := range issues {
		if issue.SuggestionsGeneratedAt != nil && !issue.SuggestionsGeneratedAt.Before(started) {
			generated++
		}
		if len(issue.PreviousIdentifiers) > 0 {
			renumbered++
			if issue.Team.ID == private {
				toPrivate++
			}
		}
		if issue.ArchivedAt != nil {
			archived++
		}
	}
	accepted, history := 0, 0
	for _, row := range legacy.State.Suggestions {
		if strings.Contains(row[2].(string), `"state":"accepted"`) && strings.Contains(row[0].(string), "issue_suggestion_") {
			accepted++
		}
	}
	for _, row := range legacy.State.Content {
		if strings.HasPrefix(row[3].(string), `{"id":"sw440-`) {
			history++
		}
	}
	t.Logf("legacy: %d results (%d archived), %d generated, %d accepted, %d renumbered (%d into the private team), %d history rows, %d events, %d webhooks, %d realtime",
		len(issues), archived, generated, accepted, renumbered, toPrivate, history, len(legacy.State.Events), len(legacy.Webhooks), len(legacy.Realtime))
	if generated < 700 || archived == 0 {
		t.Fatalf("legacy generated suggestions for %d issues (%d archived)", generated, archived)
	}
	if history < 5000 {
		t.Fatalf("legacy kept %d seeded history rows", history)
	}
	if len(legacy.State.Events) != 1 || len(legacy.Webhooks) != 1 || len(legacy.Realtime) != 1 {
		t.Fatalf("legacy events/webhooks/realtime %d/%d/%d, want one suggestion write", len(legacy.State.Events), len(legacy.Webhooks), len(legacy.Realtime))
	}
	if auto && accepted == 0 {
		t.Fatal("legacy auto actions accepted nothing")
	}
	if moved && (renumbered < 50 || toPrivate == 0) {
		t.Fatalf("legacy triage moved %d issues, %d into the private team", renumbered, toPrivate)
	}
}

// Triage generation over a 1000-issue scope with a realistic history, called
// as the batch route calls it after its write, from an empty work budget:
// legacy, warm and SQL-reloaded runs agree byte for byte on the result, issue,
// history, suggestion, document, sequence and alias rows, events, webhooks,
// realtime and the installed image. Team auto-moves append to and merge into
// the moved issues' history.
func TestThousandIssueTriageGenerationDifferential(t *testing.T) {
	triageWriterBudgetRun(t, "suggest", nil, false)
}

func TestThousandIssueTriageAutoActionsDifferential(t *testing.T) {
	triageWriterBudgetRun(t, "auto-actions", func(settings *domain.TriageIntelligenceSettings) {
		settings.AssigneeAction, settings.ProjectAction, settings.LabelAction = "auto", "auto", "auto"
		settings.DuplicateAction, settings.RelatedAction = "auto", "auto"
	}, false)
}

func TestThousandIssueTriageTeamAutoMoveDifferential(t *testing.T) {
	triageWriterBudgetRun(t, "team-auto-move", func(settings *domain.TriageIntelligenceSettings) {
		settings.TeamAction, settings.LabelAction = "auto", "auto"
	}, true)
}

func triageWriterBudgetRun(t *testing.T, name string, actions func(*domain.TriageIntelligenceSettings), moved bool) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := triageWriterBudgetSetup(t, triageWriterBudgetSize(t))
	t.Run(name, func(t *testing.T) {
		var setup func(*testing.T)
		if actions != nil {
			setup = triageBatchActions(f, actions)
		}
		f.prepareTriageReset(t, f.prepare(scaleIssueUpdateCase{setup: setup, issue: f.triage.ID}))
		started := time.Now().UTC()
		outcomes := f.differential(t, name, func(service *server) *httptest.ResponseRecorder {
			bounded := service.store.BoundedMetadataEnabled()
			if bounded && service.store.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("the bounded run does not start from an empty work budget")
			}
			service.store.MetadataWorkPeakForTesting()
			began := time.Now()
			w := f.triageWriterBudgetGenerate(t, ids)(service)
			t.Logf("bounded=%t generation took %s", bounded, time.Since(began))
			if bounded {
				t.Logf("bounded peak work %.1f MB", float64(service.store.MetadataWorkPeakForTesting())/(1<<20))
			}
			return w
		})
		triageWriterBudgetCheck(t, outcomes[0], started, f.private, actions != nil, moved)
	})
}

// moved is a bulk issue the team auto-move moves: its group's
// majority is in the private triage team (triageWriterBudgetSetup).
const triageWriterBudgetMoved = "sw400-bulk-0040"

// attach attaches the triage writer budget document to the moved issue before
// the installed image is captured.
func attach(f scaleIssueUpdateFixture, base func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) {
		base(t)
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw441_attach", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Documents {
				if document := &data.Documents[index]; document.ID == "sw440-doc" {
					document.IssueID, document.IssueTeamID = triageWriterBudgetMoved, triageBatchTeam
					return nil
				}
			}
			return errors.New("sw441 document missing")
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// A write committed by another server between this server's installed image
// and its generation reassigns the team of a document attached to an issue
// that the generation moves: both change the document's issueTeamId. The
// outcome equals the legacy order (that write, then generation) byte for
// byte, and the installed image equals the SQL reload. The bounded writer's
// first attempt loses the race on the document record, refreshes it into the
// installed image and runs the generation again.
//
// (Until 2026-11-18 this test reassigned the document of an issue the
// generation does not move: no writer touches that record, so the warm image
// kept it as before the other server's write until its peer event. Legacy
// does the same on a second server; triage leftovers proves that equality.)
func TestThousandIssueTriageConflictingWrite(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := triageWriterBudgetSetup(t, triageWriterBudgetSize(t))
	f.prepareTriageReset(t, attach(f, f.prepare(scaleIssueUpdateCase{issue: f.triage.ID, setup: triageBatchActions(f, func(settings *domain.TriageIntelligenceSettings) {
		settings.TeamAction, settings.LabelAction = "auto", "auto"
	})})))
	started := time.Now().UTC()
	// The other server is the canonical store: the legacy source is that
	// store, which sees the write before its generation (the legacy order).
	peer := func(t *testing.T) { f.reassign(t, f.repository, "sw440-plain") }
	outcomes := f.triageLeftoversDifferential(t, []string{"legacy", "warm", "reloaded"}, peer, func(service *server) *httptest.ResponseRecorder {
		attempts := store.IssueSuggestionWriteAttemptsForTesting()
		w := f.triageWriterBudgetGenerate(t, ids)(service)
		if service.store.BoundedMetadataEnabled() {
			if rerun := store.IssueSuggestionWriteAttemptsForTesting() - attempts; rerun != 2 {
				t.Fatalf("bounded suggestion write ran %d attempts, want 2 (a lost race on the document, then the rerun)", rerun)
			}
		}
		return w
	}, true)
	triageWriterBudgetCheck(t, outcomes[0], started, f.private, true, true)
	moved := false
	for _, row := range outcomes[0].State.Documents {
		if row[0] == "sw440-doc" {
			var document domain.Document
			if err := json.Unmarshal([]byte(row[2].(string)), &document); err != nil {
				t.Fatal(err)
			}
			moved = document.IssueID == triageWriterBudgetMoved && document.IssueTeamID != "sw440-plain" && document.IssueTeamID != triageBatchTeam &&
				document.UpdatedAt.Equal(time.Date(2026, 1, 4, 5, 6, 7, 0, time.UTC))
		}
	}
	if !moved {
		t.Fatal("legacy did not move the document the other server reassigned")
	}
}

// From an empty budget lowered below the bounded writer's measured peak, the
// generation fails with the clean work-budget error and publishes nothing:
// every row, the installed image and the sinks equal a run that did nothing.
// Just above the peak it succeeds and equals legacy.
func TestThousandIssueTriageBudgetCeiling(t *testing.T) {
	budgetCeiling(t, "warm", false)
}

func TestThousandIssueTriageReloadedBudgetCeiling(t *testing.T) {
	budgetCeiling(t, "reloaded", false)
}

func TestThousandIssueTriageBudgetRefusal(t *testing.T) {
	budgetCeiling(t, "warm", true)
}

func TestThousandIssueTriageReloadedBudgetRefusal(t *testing.T) {
	budgetCeiling(t, "reloaded", true)
}

func budgetCeiling(t *testing.T, source string, below bool) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := triageWriterBudgetSetup(t, triageWriterBudgetSize(t))
	f.prepareTriageReset(t, f.prepare(scaleIssueUpdateCase{issue: f.triage.ID, setup: triageBatchActions(f, func(settings *domain.TriageIntelligenceSettings) {
		settings.TeamAction, settings.LabelAction = "auto", "auto"
	})}))
	generate := f.triageWriterBudgetGenerate(t, ids)
	var expected string
	if !below {
		legacy, started := f.run(t, "legacy", generate)
		expected = scaleIssueUpdateNormalize(t, legacy, started)
	}
	peak := 0
	got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
		service.store.MetadataWorkPeakForTesting()
		w := generate(service)
		peak = service.store.MetadataWorkPeakForTesting()
		return w
	})
	t.Logf("%s: bounded peak work %.1f MB of %d MB", source, float64(peak)/(1<<20), 256)
	runAtLimit := func(limit int) (scaleIssueUpdateOutcome, time.Time) {
		return f.run(t, source, func(service *server) *httptest.ResponseRecorder {
			restore := service.store.SetMetadataWorkLimitForTesting(limit)
			defer restore()
			return generate(service)
		})
	}
	if !below {
		if normalized := scaleIssueUpdateNormalize(t, got, started); normalized != expected {
			t.Fatalf("%s changed: %s", source, policyFirstDifference(expected, normalized))
		}
		limit := peak + 64<<10
		got, started := runAtLimit(limit)
		if normalized := scaleIssueUpdateNormalize(t, got, started); normalized != expected {
			t.Fatalf("%s at %d bytes changed: %s", source, limit, policyFirstDifference(expected, normalized))
		}
		return
	}
	if got.Code != http.StatusOK {
		t.Fatalf("%s calibration status %d: %.300s", source, got.Code, got.Body)
	}
	idle, idleStarted := f.run(t, source, func(*server) *httptest.ResponseRecorder { return nil })
	got, started = runAtLimit(peak - 64<<10)
	if got.Code != http.StatusInternalServerError || !strings.Contains(string(got.Body), store.ErrMetadataWorkBudget.Error()) {
		t.Fatalf("%s below the peak: %d %s", source, got.Code, got.Body)
	}
	got.Code, got.Body = 0, nil
	if want, normalized := scaleIssueUpdateNormalize(t, idle, idleStarted), scaleIssueUpdateNormalize(t, got, started); normalized != want {
		t.Fatalf("%s below the peak published or wrote: %s", source, policyFirstDifference(want, normalized))
	}
}
