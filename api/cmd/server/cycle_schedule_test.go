package main

import (
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scheduleFixture(cycles ...domain.Cycle) *domain.Bootstrap {
	return &domain.Bootstrap{
		Teams:         []domain.Team{{ID: "team_a"}},
		CycleSettings: map[string]domain.CycleSettings{"team_a": {Enabled: true, AutoCreate: true, DurationWeeks: 2, CooldownWeeks: 2, UpcomingCount: 2}},
		Cycles:        cycles,
	}
}

func scheduledCycle(id string, number int, status, start, end string) domain.Cycle {
	return domain.Cycle{ID: id, Number: number, TeamID: "team_a", Status: status, StartsAt: day(start), EndsAt: day(end)}
}

func cycleDates(t *testing.T, data *domain.Bootstrap, id string) (status, start, end string) {
	t.Helper()
	cycle, err := cycleByID(data, id)
	if err != nil {
		t.Fatalf("cycle %s missing", id)
	}
	return cycle.Status, cycle.StartsAt.Format("2006-01-02"), cycle.EndsAt.Format("2006-01-02")
}

func expectCycle(t *testing.T, data *domain.Bootstrap, id, status, start, end string) {
	t.Helper()
	if gotStatus, gotStart, gotEnd := cycleDates(t, data, id); gotStatus != status || gotStart != start || gotEnd != end {
		t.Fatalf("%s = %s %s..%s, want %s %s..%s", id, gotStatus, gotStart, gotEnd, status, start, end)
	}
}

// Automatic maintenance ends a cycle after its last day, leaves the cooldown without a current
// cycle and starts the next cycle on its planned start day.
func TestCycleMaintenanceHonorsCooldown(t *testing.T) {
	data := scheduleFixture(
		scheduledCycle("c1", 1, "current", "2026-10-19", "2026-11-01"),
		scheduledCycle("c2", 2, "upcoming", "2026-11-16", "2026-11-29"),
		scheduledCycle("c3", 3, "upcoming", "2026-12-14", "2026-12-27"),
	)
	lastDay := day("2026-11-01").Add(20 * time.Hour)
	if cycleScheduleDue(data, "team_a", lastDay) {
		t.Fatal("the cycle's last day counted as ended")
	}
	cooldown := day("2026-11-02").Add(time.Hour)
	if !cycleScheduleDue(data, "team_a", cooldown) {
		t.Fatal("an ended cycle was not due for maintenance")
	}
	advanceCycleSchedule(data, "team_a", cooldown)
	expectCycle(t, data, "c1", "completed", "2026-10-19", "2026-11-01")
	expectCycle(t, data, "c2", "upcoming", "2026-11-16", "2026-11-29")
	if currentCycle(data, "team_a") != nil {
		t.Fatal("a cycle started during the cooldown")
	}
	if cycleScheduleDue(data, "team_a", day("2026-11-10")) {
		t.Fatal("the cooldown kept asking for maintenance")
	}
	start := day("2026-11-16").Add(time.Hour)
	if !cycleScheduleDue(data, "team_a", start) {
		t.Fatal("the next cycle's start day was not due")
	}
	advanceCycleSchedule(data, "team_a", start)
	expectCycle(t, data, "c2", "current", "2026-11-16", "2026-11-29")
	expectCycle(t, data, "c3", "upcoming", "2026-12-14", "2026-12-27")
}

// Without a cooldown the next cycle starts the day after the last one ended.
func TestCycleMaintenanceWithoutCooldown(t *testing.T) {
	data := scheduleFixture(
		scheduledCycle("c1", 1, "current", "2026-10-19", "2026-11-01"),
		scheduledCycle("c2", 2, "upcoming", "2026-11-02", "2026-11-15"),
		scheduledCycle("c3", 3, "upcoming", "2026-11-16", "2026-11-29"),
	)
	data.CycleSettings["team_a"] = domain.CycleSettings{Enabled: true, AutoCreate: true, DurationWeeks: 2, UpcomingCount: 2}
	advanceCycleSchedule(data, "team_a", day("2026-11-02").Add(time.Hour))
	expectCycle(t, data, "c1", "completed", "2026-10-19", "2026-11-01")
	expectCycle(t, data, "c2", "current", "2026-11-02", "2026-11-15")
	expectCycle(t, data, data.Cycles[3].ID, "upcoming", "2026-11-30", "2026-12-13")
}

// The reference team's schedule on 2026-10-28: 2 week cycles with a 2 week cooldown.
func referenceSchedule(currentStatus string) *domain.Bootstrap {
	return scheduleFixture(
		scheduledCycle("c1", 1, currentStatus, "2026-10-28", "2026-11-15"),
		scheduledCycle("c2", 2, "upcoming", "2026-11-30", "2026-12-13"),
		scheduledCycle("c3", 3, "upcoming", "2026-12-28", "2027-01-10"),
	)
}

var referenceNow = day("2026-10-28").Add(8 * time.Hour)

func moveDates(t *testing.T, data *domain.Bootstrap, id, start, end string, shift bool) error {
	t.Helper()
	cycle, err := cycleByID(data, id)
	if err != nil {
		t.Fatal(err)
	}
	var startsAt, endsAt *time.Time
	if start != "" {
		value := day(start)
		startsAt = &value
	}
	if end != "" {
		value := day(end)
		endsAt = &value
	}
	return moveCycleDates(data, cycle, startsAt, endsAt, shift, referenceNow)
}

// Start cycle today (no current cycle): the start moves to today, the end and every other
// cycle stay.
func TestStartCycleTodayKeepsItsEnd(t *testing.T) {
	data := scheduleFixture(
		scheduledCycle("c1", 1, "upcoming", "2026-11-02", "2026-11-15"),
		scheduledCycle("c2", 2, "upcoming", "2026-11-30", "2026-12-13"),
	)
	startCycleOn(&data.Cycles[0], referenceNow)
	expectCycle(t, data, "c1", "current", "2026-10-28", "2026-11-15")
	expectCycle(t, data, "c2", "upcoming", "2026-11-30", "2026-12-13")
	if cycleScheduleDue(data, "team_a", referenceNow) || cycleScheduleDue(data, "team_a", day("2026-11-02")) {
		t.Fatal("starting early made the schedule due before the current cycle ended")
	}
	advanceCycleSchedule(data, "team_a", referenceNow)
	if len(data.Cycles) != 2 {
		t.Fatalf("starting early generated cycles: %#v", data.Cycles)
	}
	if !cycleScheduleDue(data, "team_a", day("2026-11-16")) {
		t.Fatal("the ended cycle was not due for maintenance")
	}
	advanceCycleSchedule(data, "team_a", day("2026-11-16"))
	if countUpcomingCycles(data, "team_a") != 2 {
		t.Fatal("scheduled maintenance did not restore the configured upcoming count")
	}
}

// expireCurrentCycle advances a fixture to the day after its current cycle ended.
func expireCurrentCycle(t *testing.T, repository *store.SQLiteStore, workspace, teamID string) {
	t.Helper()
	if err := repository.MutateWorkspace(t.Context(), workspace, "test.cycle_elapsed", teamID, nil, func(data *domain.Bootstrap) error {
		cycle := currentCycle(data, teamID)
		if cycle == nil {
			return errNotFound
		}
		cycle.EndsAt = cycleDay(time.Now()).AddDate(0, 0, -1)
		cycle.StartsAt = cycle.EndsAt.AddDate(0, 0, -13)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStartCycleTodayDoesNotTopUpOverHTTP(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(api)
	workspace := repository.Bootstrap().Workspace.URLKey
	expireCurrentCycle(t, repository, workspace, "team_test")
	api.maintainCycleSchedule(t.Context(), workspace)
	before := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if currentCycle(&before, "team_test") != nil || countUpcomingCycles(&before, "team_test") != 2 {
		t.Fatalf("expected a cooldown with two upcoming cycles: %#v", before.Cycles)
	}
	next := *earliestUpcomingCycle(&before, "team_test")
	started := requestJSON[domain.Cycle](t, handler, http.MethodPost, "/api/cycles/"+next.ID+"/start", nil, http.StatusOK)
	if started.Status != "current" || !started.StartsAt.Equal(cycleDay(time.Now())) || !started.EndsAt.Equal(next.EndsAt) {
		t.Fatalf("started cycle = %#v", started)
	}
	for range 2 {
		after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
		if len(after.Cycles) != len(before.Cycles) || countUpcomingCycles(&after, "team_test") != 1 {
			t.Fatalf("starting early topped up cycles: %#v", after.Cycles)
		}
		for _, original := range before.Cycles {
			cycle, err := cycleByID(&after, original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if cycle.ID != next.ID && !unchangedEdit(original, *cycle) {
				t.Fatalf("starting early changed another cycle: before=%#v after=%#v", original, *cycle)
			}
		}
	}
}

func TestStartCycleRequiresNextUpcomingAndNoCurrent(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	api := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(api)
	before := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	next := earliestUpcomingCycle(&before, "team_test")
	requestJSON[any](t, handler, http.MethodPost, "/api/cycles/"+next.ID+"/start", nil, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/cycles/"+next.ID, map[string]any{"status": "current"}, http.StatusBadRequest)
	after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !unchangedEdit(before.Cycles, after.Cycles) {
		t.Fatal("rejected start changed the team's cycles")
	}
	expireCurrentCycle(t, repository, before.Workspace.URLKey, "team_test")
	api.maintainCycleSchedule(t.Context(), before.Workspace.URLKey)
	for _, cycle := range before.Cycles {
		if cycle.TeamID == "team_test" && cycle.ID != next.ID {
			requestJSON[any](t, handler, http.MethodPost, "/api/cycles/"+cycle.ID+"/start", nil, http.StatusBadRequest)
		}
	}
}

// Moving the current cycle's end earlier changes only that cycle; it stays current and the
// cooldown before the next cycle grows.
func TestMovingCurrentCycleEndEarlier(t *testing.T) {
	data := referenceSchedule("current")
	if err := moveDates(t, data, "c1", "", "2026-10-28", false); err == nil {
		t.Fatal("a cycle ended on its first day")
	}
	if err := moveDates(t, data, "c1", "", "2026-10-29", false); err != nil {
		t.Fatal(err)
	}
	expectCycle(t, data, "c1", "current", "2026-10-28", "2026-10-29")
	expectCycle(t, data, "c2", "upcoming", "2026-11-30", "2026-12-13")
	expectCycle(t, data, "c3", "upcoming", "2026-12-28", "2027-01-10")
}

// An end moved past the next cycle's start moves that start to the next day; the next cycle
// keeps its end and the cycles after it stay.
func TestMovingEndIntoNextCycle(t *testing.T) {
	data := referenceSchedule("current")
	if err := moveDates(t, data, "c1", "", "2026-12-13", false); err == nil {
		t.Fatal("the cycle ended on the next cycle's last day")
	}
	if err := moveDates(t, data, "c1", "", "2026-12-04", false); err != nil {
		t.Fatal(err)
	}
	expectCycle(t, data, "c1", "current", "2026-10-28", "2026-12-04")
	expectCycle(t, data, "c2", "upcoming", "2026-12-05", "2026-12-13")
	expectCycle(t, data, "c3", "upcoming", "2026-12-28", "2027-01-10")
	if err := moveDates(t, data, "c1", "", "2026-12-12", false); err != nil {
		t.Fatal(err)
	}
	expectCycle(t, data, "c2", "upcoming", "2026-12-13", "2026-12-13")
}

// A start moved before the previous cycle's end moves that end to the day before.
func TestMovingStartIntoPreviousCycle(t *testing.T) {
	data := referenceSchedule("current")
	data.Cycles[0].EndsAt = day("2026-12-04")
	data.Cycles[1].StartsAt = day("2026-12-05")
	if err := moveDates(t, data, "c2", "2026-10-29", "", false); err == nil {
		t.Fatal("the previous cycle was left one day")
	}
	if err := moveDates(t, data, "c2", "2026-11-30", "", false); err != nil {
		t.Fatal(err)
	}
	expectCycle(t, data, "c1", "current", "2026-10-28", "2026-11-29")
	expectCycle(t, data, "c2", "upcoming", "2026-11-30", "2026-12-13")
	expectCycle(t, data, "c3", "upcoming", "2026-12-28", "2027-01-10")
}

// "Change this cycle's start date only" keeps the end; "Change this and future cycle start
// dates" keeps the length and moves every later cycle by the same days.
func TestMovingStartOnlyOrWithFutureCycles(t *testing.T) {
	data := referenceSchedule("current")
	if err := moveDates(t, data, "c2", "2026-12-07", "", true); err != nil {
		t.Fatal(err)
	}
	expectCycle(t, data, "c1", "current", "2026-10-28", "2026-11-15")
	expectCycle(t, data, "c2", "upcoming", "2026-12-07", "2026-12-20")
	expectCycle(t, data, "c3", "upcoming", "2027-01-04", "2027-01-17")
	if err := moveDates(t, data, "c2", "2026-12-09", "", false); err != nil {
		t.Fatal(err)
	}
	expectCycle(t, data, "c2", "upcoming", "2026-12-09", "2026-12-20")
	expectCycle(t, data, "c3", "upcoming", "2027-01-04", "2027-01-17")
}

// Only an upcoming cycle's start moves, and the current cycle cannot end before today.
func TestCycleDateMoveLimits(t *testing.T) {
	data := scheduleFixture(
		scheduledCycle("c1", 1, "current", "2026-10-19", "2026-11-01"),
		scheduledCycle("c2", 2, "upcoming", "2026-11-16", "2026-11-29"),
	)
	if err := moveDates(t, data, "c1", "2026-10-20", "", false); err == nil {
		t.Fatal("the current cycle's start moved")
	}
	if err := moveDates(t, data, "c1", "", "2026-10-27", false); err == nil {
		t.Fatal("the current cycle ended before today")
	}
	if err := moveDates(t, data, "c2", "2026-10-27", "", false); err == nil {
		t.Fatal("an upcoming cycle started before today")
	}
	expectCycle(t, data, "c1", "current", "2026-10-19", "2026-11-01")
	expectCycle(t, data, "c2", "upcoming", "2026-11-16", "2026-11-29")
}

// PATCH /api/cycles/{id} with shiftFollowing moves the later upcoming cycles too.
func TestCycleStartMoveShiftsFollowingOverHTTP(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	upcoming := func() []domain.Cycle {
		bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
		cycles := []domain.Cycle{}
		for _, cycle := range bootstrap.Cycles {
			if cycle.TeamID == "team_test" && cycle.Status == "upcoming" {
				cycles = append(cycles, cycle)
			}
		}
		slices.SortFunc(cycles, func(a, b domain.Cycle) int { return a.StartsAt.Compare(b.StartsAt) })
		return cycles
	}
	before := upcoming()
	if len(before) < 2 {
		t.Fatalf("upcoming cycles = %d", len(before))
	}
	moved := before[0].StartsAt.AddDate(0, 0, 7).Format("2006-01-02")
	requestJSON[domain.Cycle](t, handler, http.MethodPatch, "/api/cycles/"+before[0].ID, map[string]any{"startsAt": moved, "shiftFollowing": true}, http.StatusOK)
	after := upcoming()
	for index := range before {
		if !cycleDay(after[index].StartsAt).Equal(cycleDay(before[index].StartsAt).AddDate(0, 0, 7)) || !cycleDay(after[index].EndsAt).Equal(cycleDay(before[index].EndsAt).AddDate(0, 0, 7)) {
			t.Fatalf("cycle %d moved to %s..%s, want a week after %s..%s", after[index].Number, after[index].StartsAt, after[index].EndsAt, before[index].StartsAt, before[index].EndsAt)
		}
	}
}
