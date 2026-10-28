package main

import (
	"testing"
	"time"

	"flow/api/internal/domain"
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

// Completing a cycle early ends it today and starts the next one today with its planned end,
// so the cycles after it keep their dates and the 2 week cooldown stays between them.
func TestCompletingCycleEarlyKeepsCooldowns(t *testing.T) {
	data := scheduleFixture(
		scheduledCycle("c1", 1, "current", "2026-10-19", "2026-11-01"),
		scheduledCycle("c2", 2, "upcoming", "2026-11-16", "2026-11-29"),
		scheduledCycle("c3", 3, "upcoming", "2026-12-14", "2026-12-27"),
	)
	now := day("2026-10-27").Add(15 * time.Hour)
	if err := transitionToCycle(data, &data.Cycles[1], now); err != nil {
		t.Fatal(err)
	}
	expectCycle(t, data, "c1", "completed", "2026-10-19", "2026-10-27")
	expectCycle(t, data, "c2", "current", "2026-10-27", "2026-11-29")
	expectCycle(t, data, "c3", "upcoming", "2026-12-14", "2026-12-27")
	if len(data.Cycles) != 4 {
		t.Fatalf("cycles = %d, want a new upcoming cycle", len(data.Cycles))
	}
	expectCycle(t, data, data.Cycles[3].ID, "upcoming", "2027-01-11", "2027-01-24")
}

// A cycle completed on the day it started covers that day; the next one starts the same day.
func TestCompletingCycleOnItsFirstDay(t *testing.T) {
	data := scheduleFixture(
		scheduledCycle("c1", 1, "current", "2026-10-27", "2026-11-09"),
		scheduledCycle("c2", 2, "upcoming", "2026-11-24", "2026-12-07"),
		scheduledCycle("c3", 3, "upcoming", "2026-12-22", "2027-01-04"),
	)
	if err := transitionToCycle(data, &data.Cycles[1], day("2026-10-27").Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	expectCycle(t, data, "c1", "completed", "2026-10-27", "2026-10-27")
	expectCycle(t, data, "c2", "current", "2026-10-27", "2026-12-07")
	expectCycle(t, data, "c3", "upcoming", "2026-12-22", "2027-01-04")
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
