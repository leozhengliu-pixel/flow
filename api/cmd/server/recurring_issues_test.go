package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func day(value string) time.Time {
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(err)
	}
	return date
}

func mustRule(t *testing.T, value string) recurrenceRule {
	t.Helper()
	rule, err := parseRecurrence(value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return rule
}

func TestRecurrenceParseAndCanonicalForm(t *testing.T) {
	for input, want := range map[string]string{
		"daily":                                          "FREQ=DAILY",
		"weekdays":                                       "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR",
		"FREQ=DAILY;INTERVAL=3":                          "FREQ=DAILY;INTERVAL=3",
		"freq=weekly;byday=fr,mo;interval=2":             "FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR",
		"RRULE:FREQ=MONTHLY;BYMONTHDAY=-1":               "FREQ=MONTHLY;BYMONTHDAY=-1",
		"FREQ=MONTHLY;BYDAY=-1FR":                        "FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=YEARLY;BYMONTH=11;BYDAY=4TH":               "FREQ=YEARLY;BYMONTH=11;BYDAY=4TH",
		"FREQ=YEARLY;INTERVAL=2;BYMONTH=2;BYMONTHDAY=29": "FREQ=YEARLY;INTERVAL=2;BYMONTH=2;BYMONTHDAY=29",
	} {
		if got := mustRule(t, input).String(); got != want {
			t.Errorf("%q => %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{"hourly", "FREQ=HOURLY", "FREQ=DAILY;BYDAY=MO", "FREQ=WEEKLY;BYDAY=2MO", "FREQ=MONTHLY;BYMONTHDAY=32", "FREQ=MONTHLY;BYDAY=5MO", "FREQ=DAILY;INTERVAL=0", "FREQ=DAILY;INTERVAL=100", "FREQ=MONTHLY;BYMONTHDAY=3;BYDAY=1MO", "FREQ=DAILY;COUNT=3", "FREQ=DAILY;FREQ=WEEKLY", ""} {
		if _, err := parseRecurrence(input); err == nil {
			t.Errorf("%q should be rejected", input)
		}
	}
	// Legacy stored presets stay valid and anchor on their occurrence date.
	if got := mustRule(t, "weekly").anchored(day("2026-09-29")).String(); got != "FREQ=WEEKLY;BYDAY=TU" {
		t.Fatalf("legacy weekly anchored = %s", got)
	}
	if got := mustRule(t, "monthly").anchored(day("2026-01-31")).String(); got != "FREQ=MONTHLY;BYMONTHDAY=31" {
		t.Fatalf("legacy monthly anchored = %s", got)
	}
}

func TestRecurrenceNextOccurrences(t *testing.T) {
	cases := []struct {
		rule  string
		from  string
		dates []string
	}{
		{"FREQ=DAILY", "2026-02-27", []string{"2026-02-28", "2026-03-01"}},
		{"FREQ=DAILY;INTERVAL=3", "2026-09-29", []string{"2026-10-02", "2026-10-05"}},
		{"weekdays", "2026-10-01", []string{"2026-10-02", "2026-10-05", "2026-10-06"}},
		{"FREQ=WEEKLY;BYDAY=MO,WE", "2026-09-28", []string{"2026-09-30", "2026-10-05", "2026-10-07"}},
		{"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE", "2026-09-28", []string{"2026-09-30", "2026-10-12", "2026-10-14", "2026-10-26"}},
		{"FREQ=WEEKLY;INTERVAL=2;BYDAY=SU", "2026-10-04", []string{"2026-10-18", "2026-11-01"}},
		// Month end: Jan 31 monthly -> Feb 28 -> Mar 31 (the rule keeps day 31).
		{"FREQ=MONTHLY;BYMONTHDAY=31", "2027-01-31", []string{"2027-02-28", "2027-03-31", "2027-04-30"}},
		{"FREQ=MONTHLY;BYMONTHDAY=-1", "2028-01-31", []string{"2028-02-29", "2028-03-31"}},
		{"FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=15", "2026-01-15", []string{"2026-04-15", "2026-07-15"}},
		{"FREQ=MONTHLY;BYDAY=2TU", "2026-09-08", []string{"2026-10-13", "2026-11-10"}},
		{"FREQ=MONTHLY;BYDAY=-1FR", "2026-09-25", []string{"2026-10-30", "2026-11-27"}},
		{"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", "2028-02-29", []string{"2029-02-28", "2030-02-28", "2031-02-28"}},
		{"FREQ=YEARLY;BYMONTH=11;BYDAY=4TH", "2026-11-26", []string{"2027-11-25"}},
	}
	for _, tc := range cases {
		rule := mustRule(t, tc.rule).anchored(day(tc.from))
		current := day(tc.from)
		got := []string{}
		for range tc.dates {
			current = rule.nextAfter(current)
			got = append(got, current.Format("2006-01-02"))
		}
		if !slices.Equal(got, tc.dates) {
			t.Errorf("%s from %s = %v, want %v", tc.rule, tc.from, got, tc.dates)
		}
	}
	if got := mustRule(t, "FREQ=MONTHLY;BYMONTHDAY=31").firstOnOrAfter(day("2026-02-10")); got != day("2026-02-28") {
		t.Fatalf("first monthly 31 in Feb = %s", got)
	}
	if got := mustRule(t, "FREQ=WEEKLY;BYDAY=FR").firstOnOrAfter(day("2026-10-03")); got != day("2026-10-09") {
		t.Fatalf("first Friday = %s", got)
	}
}

func TestRecurrenceUsesTeamTimezoneAcrossDST(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// DST starts 2026-03-08: local midnight moves from 05:00Z to 04:00Z.
	rule := mustRule(t, "daily")
	before := occurrenceInstant(day("2026-03-08"), newYork)
	after := occurrenceInstant(rule.nextAfter(day("2026-03-08")), newYork)
	if before != time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC) || after != time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC) {
		t.Fatalf("DST instants = %s, %s", before, after)
	}
	// 02:00Z on the 30th UTC is still the 29th in New York.
	if got := civilDate(time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC), newYork); got != day("2026-09-29") {
		t.Fatalf("civil date = %s", got)
	}
	if teamLocation("") != time.UTC || teamLocation("Not/AZone") != time.UTC {
		t.Fatal("unknown timezones fall back to UTC")
	}
	data := &domain.Bootstrap{TeamSettings: map[string]domain.TeamSettings{"team": {TeamID: "team", Timezone: "America/New_York"}}}
	issue := &domain.Issue{Team: domain.Team{ID: "team"}}
	weekly := "weekly"
	changes := map[string]string{}
	// Tuesday 2026-09-29 22:00 in New York (Wednesday in UTC).
	if err := applyRecurrenceUpdate(data, issue, &weekly, nil, time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC), changes); err != nil {
		t.Fatal(err)
	}
	if issue.Recurrence != "weekly" || !issue.NextOccurrenceAt.Equal(time.Date(2026, 10, 6, 4, 0, 0, 0, time.UTC)) {
		t.Fatalf("weekly in New York = %s next %s", issue.Recurrence, issue.NextOccurrenceAt)
	}
}

func TestApplyRecurrenceUpdateStartDatesAndStop(t *testing.T) {
	data := &domain.Bootstrap{}
	issue := &domain.Issue{Team: domain.Team{ID: "team"}}
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) // Tuesday
	set := func(schedule, start *string) {
		t.Helper()
		if err := applyRecurrenceUpdate(data, issue, schedule, start, now, map[string]string{}); err != nil {
			t.Fatal(err)
		}
	}
	ptr := func(value string) *string { return &value }
	set(ptr("FREQ=WEEKLY;BYDAY=MO,FR"), nil)
	if got := issue.NextOccurrenceAt.Format("2006-01-02"); got != "2026-10-02" {
		t.Fatalf("first Mon/Fri = %s", got)
	}
	set(ptr("monthly"), ptr("2026-10-31"))
	if issue.Recurrence != "FREQ=MONTHLY;BYMONTHDAY=31" || issue.NextOccurrenceAt.Format("2006-01-02") != "2026-10-31" {
		t.Fatalf("monthly from Oct 31 = %s %s", issue.Recurrence, issue.NextOccurrenceAt)
	}
	// Re-sending the stored schedule keeps the next occurrence.
	previous := *issue.NextOccurrenceAt
	set(ptr(issue.Recurrence), nil)
	if !issue.NextOccurrenceAt.Equal(previous) {
		t.Fatal("unchanged schedule must keep its next occurrence")
	}
	// Legacy clients send an RFC3339 instant; past starts clamp to today.
	set(ptr("daily"), ptr("2026-01-01T09:00:00Z"))
	if got := issue.NextOccurrenceAt.Format("2006-01-02"); got != "2026-09-29" {
		t.Fatalf("past start = %s", got)
	}
	set(ptr(""), nil)
	if issue.Recurrence != "" || issue.NextOccurrenceAt != nil {
		t.Fatal("stop recurring must clear the schedule")
	}
	if err := applyRecurrenceUpdate(data, issue, nil, ptr("2026-10-01"), now, map[string]string{}); err == nil {
		t.Fatal("next occurrence without a schedule must be rejected")
	}
	if err := applyRecurrenceUpdate(data, issue, ptr("hourly"), nil, now, map[string]string{}); err == nil {
		t.Fatal("unknown schedule must be rejected")
	}
}

type recurringFixture struct {
	t          *testing.T
	path       string
	repository *store.SQLiteStore
	server     *server
	key        string
}

func newRecurringFixture(t *testing.T) *recurringFixture {
	t.Helper()
	fixture := &recurringFixture{t: t, path: filepath.Join(t.TempDir(), "flow.db")}
	fixture.open()
	t.Cleanup(func() { fixture.repository.Close() })
	return fixture
}

func (f *recurringFixture) open() {
	f.t.Helper()
	repository, err := store.OpenSQLiteTestFixture(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.repository = repository
	f.server = &server{store: repository, realtime: newRealtimeHub()}
	f.key = repository.Bootstrap().Workspace.URLKey
}

func (f *recurringFixture) issues() []domain.Issue {
	f.t.Helper()
	data, ok := f.repository.BootstrapFor(f.key)
	if !ok {
		f.t.Fatal("workspace missing")
	}
	return data.Issues
}

func (f *recurringFixture) issue(id string) domain.Issue {
	f.t.Helper()
	issue, err := f.repository.IssueRecord(context.Background(), f.key, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return issue
}

func (f *recurringFixture) generate(now time.Time) []domain.Issue {
	f.t.Helper()
	created, err := f.server.generateWorkspaceRecurringIssues(context.Background(), f.key, now)
	if err != nil {
		f.t.Fatal(err)
	}
	return created
}

func TestRecurringIssueGenerationCopiesFieldsCatchesUpAndIsIdempotent(t *testing.T) {
	f := newRecurringFixture(t)
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC) // Tuesday
	var sourceID, teamID string
	var labelIDs []string
	err := f.repository.MutateWorkspace(t.Context(), f.key, "test.recurring", "recurring", nil, func(data *domain.Bootstrap) error {
		source := &data.Issues[0]
		sourceID, teamID = source.ID, source.Team.ID
		settings := teamSettings(data, teamID)
		settings.Timezone, settings.TriageEnabled = "UTC", true
		for _, state := range statesForTeam(data, teamID) {
			if state.Type == "backlog" {
				settings.DefaultStateID = state.ID
			}
		}
		data.TeamSettings[teamID] = settings
		for _, label := range data.Labels {
			if labelAvailableForResource(data, label, "issue") && len(labelIDs) < 2 {
				labelIDs = append(labelIDs, label.ID)
			}
		}
		estimate := 3.0
		occurrence := occurrenceInstant(day("2026-09-07"), time.UTC) // Monday, three periods ago
		cycle := "cycle_that_should_not_copy"
		source.Title, source.Description, source.Priority, source.PriorityLabel = "Weekly report", "Send the numbers", 2, priorityLabel(2)
		source.Estimate, source.Labels, source.Assignee, source.CycleID = &estimate, labelsByID(data, labelIDs), &data.Users[len(data.Users)-1], &cycle
		source.Creator = data.Users[0]
		if len(data.Projects) > 0 {
			source.Project = projectByID(data, data.Projects[0].ID)
		}
		source.Recurrence, source.NextOccurrenceAt = "FREQ=WEEKLY;BYDAY=MO", &occurrence
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	source := f.issue(sourceID)
	created := f.generate(now)
	if len(created) != 1 {
		t.Fatalf("created %d issues, want 1 for all missed periods", len(created))
	}
	next := f.issue(created[0].ID)
	if next.Title != source.Title || next.Description != source.Description || next.Priority != 2 || next.Estimate == nil || *next.Estimate != 3 || next.Team.ID != teamID {
		t.Fatalf("copied fields = %#v", next)
	}
	if next.Assignee == nil || next.Assignee.ID != source.Assignee.ID || next.Creator.ID != source.Creator.ID {
		t.Fatalf("assignee/creator = %#v / %#v", next.Assignee, next.Creator)
	}
	if (source.Project == nil) != (next.Project == nil) || source.Project != nil && next.Project.ID != source.Project.ID {
		t.Fatalf("project = %#v", next.Project)
	}
	if got := issueLabelIDs(next.Labels); !slices.Equal(got, issueLabelIDs(source.Labels)) {
		t.Fatalf("labels = %v want %v", got, issueLabelIDs(source.Labels))
	}
	if next.CycleID != nil && *next.CycleID == "cycle_that_should_not_copy" {
		t.Fatal("cycle must not be copied")
	}
	if next.DueDate == nil || *next.DueDate != "2026-09-28" || next.RecurrenceOccurrence != "2026-09-28" || next.RecurrenceSeriesID != sourceID {
		t.Fatalf("occurrence = due %v occurrence %q series %q", next.DueDate, next.RecurrenceOccurrence, next.RecurrenceSeriesID)
	}
	if next.Recurrence != "FREQ=WEEKLY;BYDAY=MO" || next.NextOccurrenceAt == nil || !next.NextOccurrenceAt.Equal(occurrenceInstant(day("2026-10-05"), time.UTC)) {
		t.Fatalf("schedule should move to the new issue: %q %v", next.Recurrence, next.NextOccurrenceAt)
	}
	if next.State.ID == "" || next.State.Type == "backlog" && next.TriagedAt == nil {
		t.Fatalf("recurring copies must skip triage: state %#v triaged %v", next.State, next.TriagedAt)
	}
	updatedSource := f.issue(sourceID)
	if updatedSource.Recurrence != "" || updatedSource.NextOccurrenceAt != nil {
		t.Fatalf("source should stop recurring: %q %v", updatedSource.Recurrence, updatedSource.NextOccurrenceAt)
	}
	_, activities, err := f.repository.IssueContent(t.Context(), f.key, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(activities, func(event domain.ActivityEvent) bool {
		return event.Type == "issue.created" && event.Metadata["recurringFrom"] == source.Identifier
	}) {
		t.Fatalf("missing recurring creation activity: %#v", activities)
	}

	// Ticking again, or after a restart, creates nothing until the next date.
	if again := f.generate(now.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("duplicate generation: %d", len(again))
	}
	f.repository.Close()
	f.open()
	if again := f.generate(now.Add(time.Hour)); len(again) != 0 {
		t.Fatalf("duplicate generation after restart: %d", len(again))
	}
	following := f.generate(time.Date(2026, 10, 5, 0, 1, 0, 0, time.UTC))
	if len(following) != 1 || following[0].DueDate == nil || *following[0].DueDate != "2026-10-05" || following[0].RecurrenceSeriesID != sourceID {
		t.Fatalf("following occurrence = %#v", following)
	}

	// Re-enabling the schedule on an issue whose occurrence already exists only
	// advances the schedule (idempotent per series + date).
	due := occurrenceInstant(day("2026-10-05"), time.UTC)
	err = f.repository.MutateWorkspace(t.Context(), f.key, "test.recurring", "recurring", nil, func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, next.ID)
		if err != nil {
			return err
		}
		issue.Recurrence, issue.NextOccurrenceAt = "FREQ=WEEKLY;BYDAY=MO", &due
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.issues())
	if again := f.generate(time.Date(2026, 10, 5, 0, 2, 0, 0, time.UTC)); len(again) != 0 || len(f.issues()) != before {
		t.Fatalf("existing occurrence must not be recreated")
	}
	if advanced := f.issue(next.ID); advanced.NextOccurrenceAt == nil || !advanced.NextOccurrenceAt.Equal(occurrenceInstant(day("2026-10-12"), time.UTC)) {
		t.Fatalf("schedule not advanced: %v", advanced.NextOccurrenceAt)
	}
}

func TestRecurringIssueSchedulerStopsWithContext(t *testing.T) {
	f := newRecurringFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		f.server.runRecurringIssueScheduler(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}
