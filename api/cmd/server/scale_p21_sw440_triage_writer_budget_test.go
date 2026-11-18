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

// scaleP21SW440Size is the triage scope of the SW440 fixtures; a measurement
// run may raise it with FLOW_SW440_SIZE.
func scaleP21SW440Size(t *testing.T) int {
	if raw := os.Getenv("FLOW_SW440_SIZE"); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 100 {
			t.Fatalf("FLOW_SW440_SIZE=%q", raw)
		}
		return size
	}
	return 1000
}

// scaleP21SW440Setup is the SW400 bulk scope with a realistic history on
// every issue (two comments, two update activities and their notifications)
// plus scope edges: a group of issues in the private triage team whose
// cross-team members a team auto-move pulls in, issues of a team without
// triage, archived issues still in triage, and a trashed issue ID.
func scaleP21SW440Setup(t *testing.T, size int) (scaleP21IssueUpdateFixture, []string) {
	t.Helper()
	f := scaleP21IssueUpdateSetup(t)
	ids := scaleP21SW400Bulk(t, f, size)
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
			IssueID: "sw400-bulk-0000", IssueTeamID: scaleP21SW400Team, InheritedTeamIDs: []string{}, ProjectIDs: []string{}, TeamIDs: []string{},
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
func (f scaleP21IssueUpdateFixture) sw440Generate(t *testing.T, ids []string) func(*server) *httptest.ResponseRecorder {
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
func scaleP21SW440Check(t *testing.T, legacy scaleP21IssueUpdateOutcome, started time.Time, private string, auto, moved bool) {
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
func TestScaleP21SW440ThousandIssueTriageGenerationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := scaleP21SW440Setup(t, scaleP21SW440Size(t))
	for _, test := range []struct {
		name        string
		setup       func(*testing.T)
		auto, moved bool
	}{
		{name: "suggest"},
		{name: "auto-actions", setup: scaleP21SW400Actions(f, func(settings *domain.TriageIntelligenceSettings) {
			settings.AssigneeAction, settings.ProjectAction, settings.LabelAction = "auto", "auto", "auto"
			settings.DuplicateAction, settings.RelatedAction = "auto", "auto"
		}), auto: true},
		{name: "team-auto-move", setup: scaleP21SW400Actions(f, func(settings *domain.TriageIntelligenceSettings) {
			settings.TeamAction, settings.LabelAction = "auto", "auto"
		}), auto: true, moved: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{setup: test.setup, issue: f.triage.ID})
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			started := time.Now().UTC()
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				if service.store.BoundedMetadataEnabled() && service.store.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("the bounded run does not start from an empty work budget")
				}
				service.store.MetadataWorkPeakForTesting()
				w := f.sw440Generate(t, ids)(service)
				if service.store.BoundedMetadataEnabled() {
					t.Logf("bounded peak work %.1f MB", float64(service.store.MetadataWorkPeakForTesting())/(1<<20))
				}
				return w
			})
			scaleP21SW440Check(t, outcomes[0], started, f.private, test.auto, test.moved)
		})
	}
}

// scaleP21SW441Moved is a bulk issue the team auto-move moves into the private
// triage team: its group's majority is there (scaleP21SW440Setup).
const scaleP21SW441Moved = "sw400-bulk-0040"

// scaleP21SW441Attach attaches the SW440 document to the moved issue before
// the installed image is captured.
func scaleP21SW441Attach(f scaleP21IssueUpdateFixture, base func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) {
		base(t)
		if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw441_attach", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Documents {
				if document := &data.Documents[index]; document.ID == "sw440-doc" {
					document.IssueID, document.IssueTeamID = scaleP21SW441Moved, scaleP21SW400Team
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
// does the same on a second server; SW460 proves that equality.)
func TestScaleP21SW441ThousandIssueTriageConflictingWrite(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := scaleP21SW440Setup(t, scaleP21SW440Size(t))
	f.scaleP21TriageWriterFixture.prepare = scaleP21SW441Attach(f, f.prepare(scaleP21IssueUpdateCase{issue: f.triage.ID, setup: scaleP21SW400Actions(f, func(settings *domain.TriageIntelligenceSettings) {
		settings.TeamAction, settings.LabelAction = "auto", "auto"
	})}))
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	started := time.Now().UTC()
	// The other server is the canonical store: the legacy source is that
	// store, which sees the write before its generation (the legacy order).
	peer := func(t *testing.T) { f.sw460Reassign(t, f.repository, "sw440-plain") }
	outcomes := f.sw460Differential(t, []string{"legacy", "warm", "reloaded"}, peer, func(service *server) *httptest.ResponseRecorder {
		attempts := store.IssueSuggestionWriteAttemptsForTesting()
		w := f.sw440Generate(t, ids)(service)
		if service.store.BoundedMetadataEnabled() {
			if rerun := store.IssueSuggestionWriteAttemptsForTesting() - attempts; rerun != 2 {
				t.Fatalf("bounded suggestion write ran %d attempts, want 2 (a lost race on the document, then the rerun)", rerun)
			}
		}
		return w
	}, true)
	scaleP21SW440Check(t, outcomes[0], started, f.private, true, true)
	moved := false
	for _, row := range outcomes[0].State.Documents {
		if row[0] == "sw440-doc" {
			moved = strings.Contains(row[2].(string), `"issueId":"`+scaleP21SW441Moved+`","issueTeamId":"`+f.private+`"`) &&
				strings.Contains(row[2].(string), `"updatedAt":"2026-01-04T05:06:07Z"`)
		}
	}
	if !moved {
		t.Fatal("legacy did not move the document the other server reassigned into the private team")
	}
}

// From an empty budget lowered below the bounded writer's measured peak, the
// generation fails with the clean work-budget error and publishes nothing:
// every row, the installed image and the sinks equal a run that did nothing.
// Just above the peak it succeeds and equals legacy.
func TestScaleP21SW442ThousandIssueTriageBudgetCeiling(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f, ids := scaleP21SW440Setup(t, scaleP21SW440Size(t))
	f.scaleP21TriageWriterFixture.prepare = f.prepare(scaleP21IssueUpdateCase{issue: f.triage.ID, setup: scaleP21SW400Actions(f, func(settings *domain.TriageIntelligenceSettings) {
		settings.TeamAction, settings.LabelAction = "auto", "auto"
	})})
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	generate := f.sw440Generate(t, ids)
	legacy, started := f.run(t, "legacy", generate)
	expected := scaleP21IssueUpdateNormalize(t, legacy, started)
	for _, source := range []string{"warm", "reloaded"} {
		peak := 0
		got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
			service.store.MetadataWorkPeakForTesting()
			w := generate(service)
			peak = service.store.MetadataWorkPeakForTesting()
			return w
		})
		if normalized := scaleP21IssueUpdateNormalize(t, got, started); normalized != expected {
			t.Fatalf("%s changed: %s", source, scaleP21M2Diff(expected, normalized))
		}
		t.Logf("%s: bounded peak work %.1f MB of %d MB", source, float64(peak)/(1<<20), 256)
		idle, idleStarted := f.run(t, source, func(*server) *httptest.ResponseRecorder { return nil })
		for _, limit := range []int{peak - 64<<10, peak + 64<<10} {
			got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
				restore := service.store.SetMetadataWorkLimitForTesting(limit)
				defer restore()
				return generate(service)
			})
			if limit > peak {
				if normalized := scaleP21IssueUpdateNormalize(t, got, started); normalized != expected {
					t.Fatalf("%s at %d bytes changed: %s", source, limit, scaleP21M2Diff(expected, normalized))
				}
				continue
			}
			if got.Code != http.StatusInternalServerError || !strings.Contains(string(got.Body), store.ErrMetadataWorkBudget.Error()) {
				t.Fatalf("%s below the peak: %d %s", source, got.Code, got.Body)
			}
			got.Code, got.Body = 0, nil
			if want, normalized := scaleP21IssueUpdateNormalize(t, idle, idleStarted), scaleP21IssueUpdateNormalize(t, got, started); normalized != want {
				t.Fatalf("%s below the peak published or wrote: %s", source, scaleP21M2Diff(want, normalized))
			}
		}
	}
}
