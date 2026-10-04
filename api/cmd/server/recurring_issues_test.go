package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
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
	// The next instance is created at 00:01 local time on the day after the
	// due date. DST starts 2026-03-08 and ends 2026-11-01 in New York.
	for due, want := range map[string]time.Time{
		"2026-03-07": time.Date(2026, 3, 8, 5, 1, 0, 0, time.UTC),
		"2026-03-08": time.Date(2026, 3, 9, 4, 1, 0, 0, time.UTC),
		"2026-10-31": time.Date(2026, 11, 1, 4, 1, 0, 0, time.UTC),
		"2026-11-01": time.Date(2026, 11, 2, 5, 1, 0, 0, time.UTC),
	} {
		if got := recurrenceCreationInstant(day(due), newYork); !got.Equal(want) {
			t.Errorf("creation after %s = %s, want %s", due, got, want)
		}
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
	// Tuesday 2026-09-29 22:00 in New York (Wednesday in UTC): without a due
	// date the first instance is due a week from (local) today.
	if err := applyRecurrenceUpdate(data, issue, &weekly, nil, "", false, time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC), map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if issue.Recurrence != "weekly" || optionalID(issue.DueDate) != "2026-10-06" || !issue.NextOccurrenceAt.Equal(time.Date(2026, 10, 7, 4, 1, 0, 0, time.UTC)) {
		t.Fatalf("weekly in New York = %s due %v next %s", issue.Recurrence, optionalID(issue.DueDate), issue.NextOccurrenceAt)
	}
}

func TestApplyRecurrenceUpdateFirstDueDatesAndStop(t *testing.T) {
	data := &domain.Bootstrap{}
	issue := &domain.Issue{Team: domain.Team{ID: "team"}}
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) // Tuesday
	ptr := func(value string) *string { return &value }
	set := func(schedule, firstDue *string) map[string]string {
		t.Helper()
		changes := map[string]string{}
		if err := applyRecurrenceUpdate(data, issue, schedule, firstDue, optionalID(issue.DueDate), false, now, changes); err != nil {
			t.Fatal(err)
		}
		return changes
	}
	// The due date of an issue update is applied before the recurrence.
	moveDue := func(value string) error {
		previous := optionalID(issue.DueDate)
		if value == "" {
			issue.DueDate = nil
		} else {
			issue.DueDate = ptr(value)
		}
		return applyRecurrenceUpdate(data, issue, nil, nil, previous, true, now, map[string]string{})
	}
	check := func(recurrence, due, next string) {
		t.Helper()
		if issue.Recurrence != recurrence || optionalID(issue.DueDate) != due || issue.NextOccurrenceAt == nil || issue.NextOccurrenceAt.Format(time.RFC3339) != next {
			t.Fatalf("schedule = %q due %q next %v, want %q due %q next %s", issue.Recurrence, optionalID(issue.DueDate), issue.NextOccurrenceAt, recurrence, due, next)
		}
	}
	// No due date: the first matching date after today.
	changes := set(ptr("FREQ=WEEKLY;BYDAY=MO,FR"), nil)
	check("FREQ=WEEKLY;BYDAY=MO,FR", "2026-10-02", "2026-10-03T00:01:00Z")
	if changes["dueDate"] != "2026-10-02" || changes["recurrence"] == "" || changes["nextOccurrenceAt"] == "" {
		t.Fatalf("changes = %v", changes)
	}
	// The legacy nextOccurrenceAt input is the first due date; schedules
	// without a day are anchored on it.
	set(ptr("monthly"), ptr("2026-10-31"))
	check("FREQ=MONTHLY;BYMONTHDAY=31", "2026-10-31", "2026-11-01T00:01:00Z")
	// Re-sending the stored schedule keeps it.
	if changes := set(ptr(issue.Recurrence), nil); len(changes) != 0 {
		t.Fatalf("unchanged schedule changed %v", changes)
	}
	check("FREQ=MONTHLY;BYMONTHDAY=31", "2026-10-31", "2026-11-01T00:01:00Z")
	// RFC3339 alias values are calendar dates in the team time zone; a past
	// first due date is kept (the next instance follows right away).
	set(ptr("daily"), ptr("2026-01-01T09:00:00Z"))
	check("daily", "2026-01-01", "2026-01-02T00:01:00Z")
	// Linear's "every 2 weeks" from a first due date repeats on its weekday,
	// and follows the due date when it is edited.
	issue.DueDate = ptr("2026-10-07") // Wednesday
	set(ptr("FREQ=WEEKLY;INTERVAL=2"), nil)
	check("FREQ=WEEKLY;INTERVAL=2;BYDAY=WE", "2026-10-07", "2026-10-08T00:01:00Z")
	if err := moveDue("2026-10-09"); err != nil {
		t.Fatal(err)
	}
	check("FREQ=WEEKLY;INTERVAL=2;BYDAY=FR", "2026-10-09", "2026-10-10T00:01:00Z")
	// Multi-day schedules keep their days when the due date moves.
	set(ptr("FREQ=WEEKLY;BYDAY=MO,FR"), nil)
	if err := moveDue("2026-10-12"); err != nil {
		t.Fatal(err)
	}
	check("FREQ=WEEKLY;BYDAY=MO,FR", "2026-10-12", "2026-10-13T00:01:00Z")
	if err := moveDue(""); err == nil {
		t.Fatal("clearing the due date of a recurring issue must be rejected")
	}
	issue.DueDate = ptr("2026-10-12")
	// Stopping keeps the due date.
	set(ptr(""), nil)
	if issue.Recurrence != "" || issue.NextOccurrenceAt != nil || optionalID(issue.DueDate) != "2026-10-12" {
		t.Fatalf("stop recurring = %q %v due %v", issue.Recurrence, issue.NextOccurrenceAt, issue.DueDate)
	}
	// A due date change on a non-recurring issue is not a schedule change.
	if err := moveDue("2026-10-14"); err != nil || issue.NextOccurrenceAt != nil {
		t.Fatalf("plain due date change = %v %v", err, issue.NextOccurrenceAt)
	}
	if err := applyRecurrenceUpdate(data, issue, nil, ptr("2026-10-01"), "", false, now, map[string]string{}); err == nil {
		t.Fatal("first due date without a schedule must be rejected")
	}
	if err := applyRecurrenceUpdate(data, issue, ptr("hourly"), nil, "", false, now, map[string]string{}); err == nil {
		t.Fatal("unknown schedule must be rejected")
	}
	// A schedule on an issue that already has a due date starts from it.
	issue.DueDate = ptr("2026-10-14")
	set(ptr("weekly"), nil)
	check("weekly", "2026-10-14", "2026-10-15T00:01:00Z")
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

// mutate edits the fixture's workspace on the full path (test setup only).
func (f *recurringFixture) mutate(edit func(data *domain.Bootstrap) error) {
	f.t.Helper()
	if err := f.repository.MutateWorkspace(f.t.Context(), f.key, "test.recurring", "recurring", nil, edit); err != nil {
		f.t.Fatal(err)
	}
}

// series lists the issues of a recurring series by due date.
func (f *recurringFixture) series(id string) []domain.Issue {
	f.t.Helper()
	result := []domain.Issue{}
	for _, issue := range f.issues() {
		if issue.ID == id || issue.RecurrenceSeriesID == id {
			result = append(result, issue)
		}
	}
	slices.SortFunc(result, func(a, b domain.Issue) int { return strings.Compare(optionalID(a.DueDate), optionalID(b.DueDate)) })
	return result
}

func (f *recurringFixture) children(id string) []domain.Issue {
	f.t.Helper()
	result := []domain.Issue{}
	for _, issue := range f.issues() {
		if issue.ParentID != nil && *issue.ParentID == id {
			result = append(result, issue)
		}
	}
	return result
}

// assertOneInstancePerDueDate fails when a series has two issues for one due date.
func (f *recurringFixture) assertOneInstancePerDueDate() {
	f.t.Helper()
	seen := map[string]string{}
	for _, issue := range f.issues() {
		if issue.RecurrenceSeriesID == "" || issue.RecurrenceOccurrence == "" {
			continue
		}
		key := issue.RecurrenceSeriesID + "/" + issue.RecurrenceOccurrence
		if other, ok := seen[key]; ok {
			f.t.Fatalf("duplicate instance %s: %s and %s", key, other, issue.ID)
		}
		seen[key] = issue.ID
	}
}

func TestRecurringIssueGenerationCopiesFieldsCatchesUpAndIsIdempotent(t *testing.T) {
	f := newRecurringFixture(t)
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC) // Tuesday
	var sourceID, teamID, childID, archivedChildID string
	var labelIDs []string
	f.mutate(func(data *domain.Bootstrap) error {
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
		due := "2026-09-07" // Monday, three periods ago
		next := recurrenceCreationInstant(day(due), time.UTC)
		cycle := "cycle_that_should_not_copy"
		source.Title, source.Description, source.Priority, source.PriorityLabel, source.Icon = "Weekly report", "Send the numbers", 2, priorityLabel(2), "Repeat"
		source.Estimate, source.Labels, source.Assignee, source.CycleID = &estimate, labelsByID(data, labelIDs), &data.Users[len(data.Users)-1], &cycle
		source.Creator = data.Users[0]
		source.ParentID = nil
		if len(data.Projects) > 0 {
			source.Project = projectByID(data, data.Projects[0].ID)
		}
		source.DueDate, source.Recurrence, source.NextOccurrenceAt = &due, "FREQ=WEEKLY;BYDAY=MO", &next
		// Two sub-issues; the archived one is not recreated.
		child, archived := &data.Issues[1], &data.Issues[2]
		childID, archivedChildID = child.ID, archived.ID
		child.ParentID, archived.ParentID = &source.ID, &source.ID
		child.Title, child.Priority, child.PriorityLabel, child.Labels, child.Assignee = "Collect numbers", 3, priorityLabel(3), labelsByID(data, labelIDs[:1]), &data.Users[0]
		archivedAt := now.Add(-time.Hour)
		archived.ArchivedAt = &archivedAt
		source.SubIssueIDs = []string{archived.ID, child.ID}
		for index := range data.Issues[3:] {
			if other := &data.Issues[3+index]; other.ParentID != nil && *other.ParentID == source.ID {
				other.ParentID = nil
			}
		}
		return nil
	})
	source := f.issue(sourceID)
	created := f.generate(now)
	if len(created) != 1 {
		t.Fatalf("created %d issues, want 1 for all missed periods", len(created))
	}
	next := f.issue(created[0].ID)
	if next.Title != source.Title || next.Description != source.Description || next.Priority != 2 || next.Estimate == nil || *next.Estimate != 3 || next.Team.ID != teamID || next.Icon != "Repeat" {
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
	// Missed periods collapse into one instance due on the first cadence date
	// that is not already past (Monday Sep 28 is; Oct 5 is not).
	if next.DueDate == nil || *next.DueDate != "2026-10-05" || next.RecurrenceOccurrence != "2026-10-05" || next.RecurrenceSeriesID != sourceID {
		t.Fatalf("instance = due %v occurrence %q series %q", next.DueDate, next.RecurrenceOccurrence, next.RecurrenceSeriesID)
	}
	if next.Recurrence != "FREQ=WEEKLY;BYDAY=MO" || next.NextOccurrenceAt == nil || !next.NextOccurrenceAt.Equal(time.Date(2026, 10, 6, 0, 1, 0, 0, time.UTC)) {
		t.Fatalf("schedule should move to the new issue: %q %v", next.Recurrence, next.NextOccurrenceAt)
	}
	if next.State.ID == "" || next.State.Type == "backlog" && next.TriagedAt == nil {
		t.Fatalf("recurring copies must skip triage: state %#v triaged %v", next.State, next.TriagedAt)
	}
	// Sub-issues are recreated (once, live ones only) under the new instance.
	children := f.children(next.ID)
	if len(children) != 1 || !slices.Equal(next.SubIssueIDs, []string{children[0].ID}) {
		t.Fatalf("recreated sub-issues = %#v (subIssueIds %v)", children, next.SubIssueIDs)
	}
	original := f.issue(childID)
	if child := children[0]; child.Title != original.Title || child.Priority != 3 || child.Assignee == nil || child.Assignee.ID != original.Assignee.ID || !slices.Equal(issueLabelIDs(child.Labels), issueLabelIDs(original.Labels)) || child.Recurrence != "" || child.ID == childID || child.ID == archivedChildID {
		t.Fatalf("recreated sub-issue = %#v", child)
	}
	if child := f.issue(childID); child.ParentID == nil || *child.ParentID != sourceID {
		t.Fatal("the previous instance keeps its sub-issues")
	}
	updatedSource := f.issue(sourceID)
	if updatedSource.Recurrence != "" || updatedSource.NextOccurrenceAt != nil || optionalID(updatedSource.DueDate) != "2026-09-07" {
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

	// Ticking again, or after a restart, creates nothing until the due date
	// has passed: 00:01 on the day after Oct 5.
	if again := f.generate(now.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("duplicate generation: %d", len(again))
	}
	f.repository.Close()
	f.open()
	if again := f.generate(now.Add(time.Hour)); len(again) != 0 {
		t.Fatalf("duplicate generation after restart: %d", len(again))
	}
	if early := f.generate(time.Date(2026, 10, 6, 0, 0, 59, 0, time.UTC)); len(early) != 0 {
		t.Fatalf("created before the due date passed: %#v", early)
	}
	following := f.generate(time.Date(2026, 10, 6, 0, 1, 0, 0, time.UTC))
	if len(following) != 1 || following[0].DueDate == nil || *following[0].DueDate != "2026-10-12" || following[0].RecurrenceSeriesID != sourceID {
		t.Fatalf("following instance = %#v", following)
	}
	if grandchildren := f.children(following[0].ID); len(grandchildren) != 1 || grandchildren[0].Title != "Collect numbers" {
		t.Fatalf("sub-issues must be recreated once per instance: %#v", grandchildren)
	}
	if again := f.generate(time.Date(2026, 10, 6, 0, 2, 0, 0, time.UTC)); len(again) != 0 {
		t.Fatalf("duplicate following instance: %d", len(again))
	}

	// Re-enabling the schedule on an issue whose next instance already exists
	// creates nothing (one instance per series and due date) and retries once
	// that instance's due date has passed.
	f.mutate(func(data *domain.Bootstrap) error {
		issue, err := issueByID(data, next.ID)
		if err != nil {
			return err
		}
		due := recurrenceCreationInstant(day("2026-10-05"), time.UTC)
		issue.Recurrence, issue.NextOccurrenceAt = "FREQ=WEEKLY;BYDAY=MO", &due
		return nil
	})
	before := len(f.issues())
	if again := f.generate(time.Date(2026, 10, 6, 0, 3, 0, 0, time.UTC)); len(again) != 0 || len(f.issues()) != before {
		t.Fatalf("existing instance must not be recreated")
	}
	if advanced := f.issue(next.ID); advanced.Recurrence == "" || advanced.NextOccurrenceAt == nil || !advanced.NextOccurrenceAt.Equal(time.Date(2026, 10, 13, 0, 1, 0, 0, time.UTC)) {
		t.Fatalf("schedule not advanced: %q %v", advanced.Recurrence, advanced.NextOccurrenceAt)
	}
	f.assertOneInstancePerDueDate()
}

func TestRecurringIssueTimingFollowsTeamTimezoneAcrossDST(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	f := newRecurringFixture(t)
	var sourceID string
	f.mutate(func(data *domain.Bootstrap) error {
		source := &data.Issues[0]
		sourceID = source.ID
		settings := teamSettings(data, source.Team.ID)
		settings.Timezone = "America/New_York"
		data.TeamSettings[source.Team.ID] = settings
		due := "2026-03-07"
		next := recurrenceCreationInstant(day(due), newYork)
		source.DueDate, source.Recurrence, source.NextOccurrenceAt = &due, "daily", &next
		return nil
	})
	// 00:01 EST on Mar 8 is 05:01Z; after the switch to EDT 00:01 is 04:01Z.
	if early := f.generate(time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)); len(early) != 0 {
		t.Fatalf("created at local midnight: %#v", early)
	}
	first := f.generate(time.Date(2026, 3, 8, 5, 1, 0, 0, time.UTC))
	if len(first) != 1 || optionalID(first[0].DueDate) != "2026-03-08" || !first[0].NextOccurrenceAt.Equal(time.Date(2026, 3, 9, 4, 1, 0, 0, time.UTC)) {
		t.Fatalf("first instance = %#v", first)
	}
	if early := f.generate(time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)); len(early) != 0 {
		t.Fatalf("created before 00:01 EDT: %#v", early)
	}
	second := f.generate(time.Date(2026, 3, 9, 4, 1, 0, 0, time.UTC))
	if len(second) != 1 || optionalID(second[0].DueDate) != "2026-03-09" || second[0].RecurrenceSeriesID != sourceID {
		t.Fatalf("second instance = %#v", second)
	}
}

// Recurring issues written by the previous model (an instance created at
// local midnight of its occurrence date, due that date; the original issue
// without a due date) keep their schedules without duplicates.
func TestRecurringIssueLegacySchedulesAreNormalized(t *testing.T) {
	f := newRecurringFixture(t)
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC) // Thursday
	var futureID, instanceID, overdueID, series string
	f.mutate(func(data *domain.Bootstrap) error {
		for _, issue := range data.Issues[:3] {
			settings := teamSettings(data, issue.Team.ID)
			settings.Timezone = "UTC"
			data.TeamSettings[issue.Team.ID] = settings
		}
		midnight := func(date string) *time.Time {
			value := occurrenceInstant(day(date), time.UTC)
			return &value
		}
		// The original issue of a series, next occurrence Monday Oct 5, no due date.
		future := &data.Issues[0]
		futureID = future.ID
		future.DueDate, future.ParentID, future.Recurrence, future.NextOccurrenceAt = nil, nil, "weekly", midnight("2026-10-05")
		// An instance created at Monday Sep 28 00:00 (due that day); the next
		// one was scheduled for Oct 5 00:00.
		instance := &data.Issues[1]
		instanceID, series = instance.ID, "issue_legacy_series"
		due := "2026-09-28"
		instance.DueDate, instance.ParentID, instance.Recurrence, instance.NextOccurrenceAt = &due, nil, "FREQ=WEEKLY;BYDAY=MO", midnight("2026-10-05")
		instance.RecurrenceSeriesID, instance.RecurrenceOccurrence = series, due
		// A daily schedule whose occurrence passed while the server was down.
		overdue := &data.Issues[2]
		overdueID = overdue.ID
		overdue.DueDate, overdue.ParentID, overdue.Recurrence, overdue.NextOccurrenceAt = nil, nil, "daily", midnight("2026-09-30")
		return nil
	})
	created := f.generate(now)
	if len(created) != 2 {
		t.Fatalf("created %d instances, want 2: %#v", len(created), created)
	}
	// The original issue is now the instance due on its next occurrence date.
	future := f.issue(futureID)
	if future.Recurrence != "weekly" || optionalID(future.DueDate) != "2026-10-05" || !future.NextOccurrenceAt.Equal(time.Date(2026, 10, 6, 0, 1, 0, 0, time.UTC)) {
		t.Fatalf("legacy issue without due date = %q due %v next %v", future.Recurrence, optionalID(future.DueDate), future.NextOccurrenceAt)
	}
	// The old instance's due date passed: its successor is due Oct 5, exactly
	// the instance the old model would have created.
	successors := f.series(series)
	if len(successors) != 2 || successors[0].ID != instanceID || optionalID(successors[1].DueDate) != "2026-10-05" || successors[1].Recurrence != "FREQ=WEEKLY;BYDAY=MO" {
		t.Fatalf("legacy instance successors = %#v", successors)
	}
	if instance := f.issue(instanceID); instance.Recurrence != "" || instance.NextOccurrenceAt != nil {
		t.Fatalf("legacy instance keeps its schedule: %q %v", instance.Recurrence, instance.NextOccurrenceAt)
	}
	overdue := f.series(overdueID)
	if len(overdue) != 2 || optionalID(overdue[0].DueDate) != "2026-09-30" || overdue[0].Recurrence != "" || optionalID(overdue[1].DueDate) != "2026-10-01" || overdue[1].Recurrence != "daily" {
		t.Fatalf("overdue legacy series = %#v", overdue)
	}
	// Normalizing is a one-time upgrade: a restart rewrites nothing and
	// creates nothing more.
	version := f.issue(futureID).Version
	if again := f.generate(now.Add(time.Minute)); len(again) != 0 {
		t.Fatalf("duplicate generation: %#v", again)
	}
	f.repository.Close()
	f.open()
	if again := f.generate(now.Add(time.Hour)); len(again) != 0 {
		t.Fatalf("duplicate generation after restart: %#v", again)
	}
	if f.issue(futureID).Version != version {
		t.Fatal("normalized schedules must not be rewritten after a restart")
	}
	// Next week every series continues once.
	following := f.generate(time.Date(2026, 10, 6, 0, 1, 0, 0, time.UTC))
	dues := map[string]string{}
	for _, issue := range following {
		dues[issue.RecurrenceSeriesID] = optionalID(issue.DueDate)
	}
	if len(following) != 3 || dues[futureID] != "2026-10-12" || dues[series] != "2026-10-12" || dues[overdueID] != "2026-10-06" {
		t.Fatalf("following instances = %v", dues)
	}
	f.assertOneInstancePerDueDate()
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

// Archived recurring issues never fill the scheduler's due window: with more
// archived due schedules than one tick reads, live schedules still fire. A
// series whose team was archived ends instead of being retried every tick.
func TestRecurringIssueSchedulerSkipsArchivedSchedules(t *testing.T) {
	f := newRecurringFixture(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	var liveID, teamID, archivedTeamIssueID string
	f.mutate(func(data *domain.Bootstrap) error {
		template := data.Issues[0]
		archivedAt := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
		oldDue := "2026-08-01"
		oldNext := recurrenceCreationInstant(day(oldDue), time.UTC)
		for index := range 101 {
			issue := template
			issue.ID, issue.Identifier, issue.Number = fmt.Sprintf("issue_arch_%03d", index), fmt.Sprintf("ARCH-%d", index), 90000+index
			issue.ParentID, issue.SubIssueIDs, issue.DueDate = nil, []string{}, &oldDue
			issue.Recurrence, issue.NextOccurrenceAt, issue.ArchivedAt = "FREQ=WEEKLY;BYDAY=SA", &oldNext, &archivedAt
			data.Issues = append(data.Issues, issue)
		}
		live := &data.Issues[1]
		liveID, teamID = live.ID, live.Team.ID
		settings := teamSettings(data, teamID)
		settings.Timezone = "UTC"
		data.TeamSettings[teamID] = settings
		due := "2026-10-01"
		next := recurrenceCreationInstant(day(due), time.UTC)
		live.ParentID, live.DueDate, live.Recurrence, live.NextOccurrenceAt = nil, &due, "daily", &next
		// A recurring issue of a team that is archived afterwards.
		team := domain.Team{ID: "team_archived_recurring", Key: "ARC", Name: "Archived"}
		retired := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
		team.ArchivedAt = &retired
		data.Teams = append(data.Teams, team)
		other := template
		other.ID, other.Identifier, other.Number, other.Team = "issue_archived_team", "ARC-1", 95000, team
		other.ParentID, other.SubIssueIDs, other.DueDate = nil, []string{}, &due
		other.Recurrence, other.NextOccurrenceAt = "daily", &next
		archivedTeamIssueID = other.ID
		data.Issues = append(data.Issues, other)
		return nil
	})
	created := f.generate(now)
	if len(created) != 1 || created[0].RecurrenceSeriesID != liveID {
		t.Fatalf("live schedule starved by archived schedules: %#v", created)
	}
	if stopped := f.issue(archivedTeamIssueID); stopped.Recurrence != "" || stopped.NextOccurrenceAt != nil {
		t.Fatalf("archived team schedule kept: %q %v", stopped.Recurrence, stopped.NextOccurrenceAt)
	}
	// Archived schedules are left untouched (they resume when unarchived).
	if archived := f.issue("issue_arch_000"); archived.Recurrence == "" || archived.UpdatedAt.Equal(now) {
		t.Fatalf("archived schedule rewritten: %#v", archived.Recurrence)
	}
	ids, err := f.repository.RecurringIssueIDs(t.Context(), f.key, now.Add(24*time.Hour), 100)
	if err != nil || slices.ContainsFunc(ids, func(id string) bool { return strings.HasPrefix(id, "issue_arch_") || id == archivedTeamIssueID }) {
		t.Fatalf("due window = %v, %v", ids, err)
	}
}

// A stray nextOccurrenceAt on an issue without a schedule is cleared by the
// startup upgrade without crashing the scheduler.
func TestRecurringIssueNormalizationClearsStrayNextOccurrence(t *testing.T) {
	f := newRecurringFixture(t)
	var id string
	f.mutate(func(data *domain.Bootstrap) error {
		next := time.Date(2026, 10, 11, 0, 1, 0, 0, time.UTC)
		data.Issues[0].Recurrence, data.Issues[0].NextOccurrenceAt = "", &next
		id = data.Issues[0].ID
		return nil
	})
	if created := f.generate(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)); len(created) != 0 {
		t.Fatalf("created %#v", created)
	}
	if issue := f.issue(id); issue.NextOccurrenceAt != nil {
		t.Fatalf("stray next occurrence kept: %v", issue.NextOccurrenceAt)
	}
}

// Clearing the due date of a recurring issue is refused even when the
// unchanged (or a new) schedule is sent with it.
func TestApplyRecurrenceUpdateRefusesClearingDueDateWithSchedule(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for _, schedule := range []string{"weekly", "FREQ=MONTHLY"} {
		due := "2026-10-10"
		next := recurrenceCreationInstant(day(due), time.UTC)
		issue := &domain.Issue{Team: domain.Team{ID: "team"}, Recurrence: "weekly", DueDate: &due, NextOccurrenceAt: &next}
		issue.DueDate = nil // the update's dueDate:null is applied first
		value := schedule
		if err := applyRecurrenceUpdate(&domain.Bootstrap{}, issue, &value, nil, due, true, now, map[string]string{}); err == nil {
			t.Fatalf("clearing the due date with %q accepted: due %v next %v", schedule, optionalID(issue.DueDate), issue.NextOccurrenceAt)
		}
	}
}

// In time zones whose DST change skips local midnight, the next instance is
// still created after the due date has ended, and the startup upgrade leaves
// such schedules alone.
func TestRecurrenceCreationInstantWhenMidnightIsSkipped(t *testing.T) {
	santiago, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	for year := 2026; year <= 2028; year++ {
		for date := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC); date.Year() == year; date = date.AddDate(0, 0, 1) {
			instant := recurrenceCreationInstant(date, santiago)
			if got := civilDate(instant, santiago); !got.Equal(date.AddDate(0, 0, 1)) {
				t.Fatalf("creation after %s at %s (local %s)", date.Format("2006-01-02"), instant, instant.In(santiago))
			}
			due := date.Format("2006-01-02")
			issue := &domain.Issue{Recurrence: "daily", DueDate: &due, NextOccurrenceAt: &instant}
			if normalizeRecurrenceTiming(issue, santiago) {
				t.Fatalf("current schedule due %s rewritten", due)
			}
		}
	}
	// 2026-09-06 starts at 01:00 -03 in Santiago: create at 01:01.
	if got := recurrenceCreationInstant(day("2026-09-05"), santiago); !got.Equal(time.Date(2026, 9, 6, 4, 1, 0, 0, time.UTC)) {
		t.Fatalf("creation after 2026-09-05 = %s", got.In(santiago))
	}
}

// The scheduler finds an existing instance of the series through the indexed
// recurrenceInstance attribute, never a payload scan, however old the source's
// due date is.
func TestRecurringIssueScopeUsesTheInstanceIndex(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	due := "2025-01-06" // a Monday long ago
	next := recurrenceCreationInstant(day(due), time.UTC)
	source := domain.Issue{ID: "issue_1", RecurrenceSeriesID: "issue_series", Recurrence: "FREQ=WEEKLY", DueDate: &due, NextOccurrenceAt: &next}
	expanded := recurringInstanceScope([]domain.Issue{source}, source.ID, now)
	if len(expanded.IssueDataContains) != 0 || !expanded.IssueDataUpdatedSince.IsZero() {
		t.Fatalf("recurring scope scans payloads: %#v", expanded)
	}
	if len(expanded.IssueAttributes) != 1 || expanded.IssueAttributes[0].Column != "recurrenceInstance" {
		t.Fatalf("recurring scope attributes = %#v", expanded.IssueAttributes)
	}
	// Mondays on or after the team's today (Oct 3 in any time zone, or Oct 2/4):
	// the target is Monday Oct 5 for every offset.
	if values := expanded.IssueAttributes[0].Values; !slices.Equal(values, []string{"issue_series/2026-10-05"}) {
		t.Fatalf("instance candidates = %v", values)
	}
}
