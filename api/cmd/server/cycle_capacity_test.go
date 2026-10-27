package main

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func capacityFixture(settings domain.TeamSettings, cycles ...domain.Cycle) domain.Bootstrap {
	settings.TeamID = "team_a"
	return domain.Bootstrap{
		Users:        []domain.User{{ID: "user_a"}, {ID: "user_b"}, {ID: "app_bot", App: true}},
		Members:      []domain.WorkspaceMember{{User: domain.User{ID: "user_a"}, Status: "active"}, {User: domain.User{ID: "user_b"}, Status: "suspended"}},
		TeamMembers:  []domain.TeamMember{{TeamID: "team_a", UserID: "user_a"}, {TeamID: "team_a", UserID: "user_b"}, {TeamID: "team_a", UserID: "app_bot"}},
		TeamSettings: map[string]domain.TeamSettings{"team_a": settings},
		Cycles:       cycles,
	}
}

func capacityCycle(id string, number int, status string) domain.Cycle {
	start := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 14*number)
	return domain.Cycle{ID: id, Number: number, TeamID: "team_a", Status: status, StartsAt: start, EndsAt: start.AddDate(0, 0, 13)}
}

func done(cycleID string, estimate *float64) store.CycleCompletedIssue {
	return store.CycleCompletedIssue{CycleID: cycleID, Estimate: estimate}
}

func TestCycleCapacityVelocity(t *testing.T) {
	fibonacci := domain.TeamSettings{EstimateType: "fibonacci"}
	off := false
	upcoming := capacityCycle("c9", 9, "upcoming")
	for _, test := range []struct {
		name     string
		settings domain.TeamSettings
		cycles   []domain.Cycle
		issues   []store.CycleCompletedIssue
		want     float64
		source   string
	}{
		{name: "no completed cycle: 30 per active member (apps and suspended members excluded)", settings: fibonacci, cycles: []domain.Cycle{upcoming}, want: 30, source: "default"},
		{name: "one completed cycle", settings: fibonacci, cycles: []domain.Cycle{capacityCycle("c1", 1, "completed"), upcoming}, issues: []store.CycleCompletedIssue{done("c1", new(3.0)), done("c1", new(5.0))}, want: 8, source: "velocity"},
		{name: "the last three completed cycles", settings: fibonacci, cycles: []domain.Cycle{capacityCycle("c1", 1, "completed"), capacityCycle("c2", 2, "completed"), capacityCycle("c3", 3, "completed"), capacityCycle("c4", 4, "completed"), capacityCycle("c5", 5, "current"), upcoming},
			// c1 is outside the window; (2 + 3 + 8) / 3 = 4.33.
			issues: []store.CycleCompletedIssue{done("c1", new(100.0)), done("c2", new(2.0)), done("c3", new(3.0)), done("c4", new(8.0)), done("c5", new(13.0))}, want: 4.3, source: "velocity"},
		{name: "unestimated issues count as 1", settings: fibonacci, cycles: []domain.Cycle{capacityCycle("c1", 1, "completed"), upcoming}, issues: []store.CycleCompletedIssue{done("c1", nil), done("c1", new(2.0))}, want: 3, source: "velocity"},
		{name: "unestimated issues count as 0 when turned off", settings: domain.TeamSettings{EstimateType: "fibonacci", EstimateCountUnestimated: &off}, cycles: []domain.Cycle{capacityCycle("c1", 1, "completed"), upcoming}, issues: []store.CycleCompletedIssue{done("c1", nil), done("c1", new(2.0))}, want: 2, source: "velocity"},
		{name: "a 0 estimate is unestimated unless zero estimates are allowed", settings: fibonacci, cycles: []domain.Cycle{capacityCycle("c1", 1, "completed"), upcoming}, issues: []store.CycleCompletedIssue{done("c1", new(0.0)), done("c1", new(2.0))}, want: 3, source: "velocity"},
		{name: "zero estimates allowed", settings: domain.TeamSettings{EstimateType: "fibonacci", EstimateAllowZero: true}, cycles: []domain.Cycle{capacityCycle("c1", 1, "completed"), upcoming}, issues: []store.CycleCompletedIssue{done("c1", new(0.0)), done("c1", new(2.0))}, want: 2, source: "velocity"},
		{name: "teams without estimates count issues", settings: domain.TeamSettings{EstimateType: "notUsed"}, cycles: []domain.Cycle{capacityCycle("c1", 1, "completed"), upcoming}, issues: []store.CycleCompletedIssue{done("c1", new(5.0)), done("c1", nil)}, want: 2, source: "velocity"},
		{name: "completed cycles that completed nothing fall back to the default", settings: fibonacci, cycles: []domain.Cycle{capacityCycle("c1", 1, "completed"), upcoming}, want: 30, source: "default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := capacityFixture(test.settings, test.cycles...)
			applyCycleCapacities(&data, test.issues)
			for _, cycle := range data.Cycles {
				if cycle.Capacity != test.want || cycle.CapacityAutomatic != test.want || cycle.CapacitySource != test.source {
					t.Fatalf("%s: capacity=%v automatic=%v source=%q, want %v %q", cycle.ID, cycle.Capacity, cycle.CapacityAutomatic, cycle.CapacitySource, test.want, test.source)
				}
			}
		})
	}
	t.Run("an override wins and keeps the automatic value alongside", func(t *testing.T) {
		override := upcoming
		override.CapacityOverride = new(12)
		data := capacityFixture(fibonacci, override)
		applyCycleCapacities(&data, nil)
		if cycle := data.Cycles[0]; cycle.Capacity != 12 || cycle.CapacityAutomatic != 30 || cycle.CapacitySource != "override" {
			t.Fatalf("override cycle=%#v", cycle)
		}
	})
	t.Run("the default scales with active members and follows inherited estimates", func(t *testing.T) {
		data := capacityFixture(domain.TeamSettings{EstimateType: "notUsed", InheritIssueEstimation: true, ParentTeamID: "team_parent"}, capacityCycle("c1", 1, "completed"), upcoming)
		data.Members[1].Status = "active"
		data.TeamSettings["team_parent"] = domain.TeamSettings{TeamID: "team_parent", EstimateType: "fibonacci"}
		applyCycleCapacities(&data, []store.CycleCompletedIssue{done("c1", new(5.0))})
		if cycle := data.Cycles[1]; cycle.Capacity != 5 {
			t.Fatalf("inherited estimates: capacity=%v, want 5 points", cycle.Capacity)
		}
		empty := capacityFixture(domain.TeamSettings{EstimateType: "fibonacci"}, upcoming)
		empty.Members[1].Status = "active"
		applyCycleCapacities(&empty, nil)
		if cycle := empty.Cycles[0]; cycle.Capacity != 60 {
			t.Fatalf("two members: capacity=%v, want 60", cycle.Capacity)
		}
	})
}

// Cycles generated in one go get distinct ids even when the clock reads the same value.
func TestGeneratedCycleIDsAreUnique(t *testing.T) {
	data := domain.Bootstrap{CycleSettings: map[string]domain.CycleSettings{"team_a": {DurationWeeks: 2}}}
	seen := map[string]bool{}
	for range 50 {
		cycle := appendFutureCycle(&data, "team_a", latestCycle(&data, "team_a"))
		if seen[cycle.ID] {
			t.Fatalf("duplicate cycle id %s", cycle.ID)
		}
		seen[cycle.ID] = true
	}
}

// The API computes capacity from completed cycles, takes an override from Edit
// cycle and returns to automatic when the override is cleared.
func TestCycleCapacityOverrideAndVelocityHTTP(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	var current domain.Cycle
	for _, cycle := range bootstrap.Cycles {
		if cycle.Status == "current" {
			current = cycle
		}
	}
	if current.ID == "" {
		t.Fatalf("no current cycle: %#v", bootstrap.Cycles)
	}
	teamID := current.TeamID
	completedState := ""
	for _, state := range bootstrap.States {
		if state.Type == "completed" && (state.TeamID == "" || state.TeamID == teamID) {
			completedState = state.ID
			break
		}
	}
	if completedState == "" {
		t.Fatal("no completed state")
	}
	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+teamID+"/settings", map[string]any{"estimateType": "fibonacci"}, http.StatusOK)
	upcomingOf := func(data domain.Bootstrap) domain.Cycle {
		var upcoming domain.Cycle
		for _, cycle := range data.Cycles {
			if cycle.TeamID == teamID && cycle.Status == "upcoming" && (upcoming.ID == "" || cycle.StartsAt.Before(upcoming.StartsAt)) {
				upcoming = cycle
			}
		}
		return upcoming
	}
	before := upcomingOf(requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK))
	if before.CapacitySource != "default" || before.Capacity <= 0 || before.Capacity != before.CapacityAutomatic || int(before.Capacity)%cycleCapacityPerMember != 0 {
		t.Fatalf("capacity before history: %#v", before)
	}
	// Cycle 1 completes 3 + 5 points (a canceled issue and an unfinished one do not count).
	for _, input := range []map[string]any{
		{"title": "Done three", "estimate": 3, "stateId": completedState},
		{"title": "Done five", "estimate": 5, "stateId": completedState},
		{"title": "Still open", "estimate": 8, "stateId": "state_todo"},
	} {
		input["cycleId"] = current.ID
		input["teamId"] = teamID
		requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", input, http.StatusCreated)
	}
	requestJSON[domain.Cycle](t, handler, http.MethodPost, "/api/cycles/"+current.ID+"/complete", nil, http.StatusOK)
	after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	next := upcomingOf(after)
	if next.Capacity != 8 || next.CapacitySource != "velocity" {
		t.Fatalf("capacity after one completed cycle: capacity=%v source=%q", next.Capacity, next.CapacitySource)
	}
	paged := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/issue-records/bootstrap", nil, http.StatusOK)
	if cycle := upcomingOf(paged); cycle.Capacity != 8 {
		t.Fatalf("paged bootstrap capacity=%v", cycle.Capacity)
	}
	public := requestJSON[domain.Cycle](t, handler, http.MethodGet, "/api/cycles/"+next.ID, nil, http.StatusOK)
	if public.Capacity != 8 || public.CapacityAutomatic != 8 {
		t.Fatalf("public cycle capacity=%v", public.Capacity)
	}

	// An override typed in Edit cycle wins; renaming keeps it; null clears it.
	overridden := requestJSON[domain.Cycle](t, handler, http.MethodPatch, "/api/cycles/"+next.ID, map[string]any{"capacity": 20}, http.StatusOK)
	if overridden.Capacity != 20 || overridden.CapacityOverride == nil || *overridden.CapacityOverride != 20 || overridden.CapacityAutomatic != 8 || overridden.CapacitySource != "override" {
		t.Fatalf("override: %#v", overridden)
	}
	if overridden.Resources == nil {
		t.Fatal("cycle update returned resources: null")
	}
	renamed := requestJSON[domain.Cycle](t, handler, http.MethodPatch, "/api/cycles/"+next.ID, map[string]any{"name": "Renamed"}, http.StatusOK)
	if renamed.Capacity != 20 || renamed.CapacitySource != "override" {
		t.Fatalf("rename dropped the override: %#v", renamed)
	}
	matrix := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/cycles/"+next.ID+"/capacity", nil, http.StatusOK)
	if matrix["capacity"] != 20.0 || matrix["capacityAutomatic"] != 8.0 || matrix["capacitySource"] != "override" {
		t.Fatalf("capacity matrix=%#v", matrix)
	}
	cleared := requestJSON[domain.Cycle](t, handler, http.MethodPatch, "/api/cycles/"+next.ID, map[string]any{"capacity": nil}, http.StatusOK)
	if cleared.Capacity != 8 || cleared.CapacityOverride != nil || cleared.CapacitySource != "velocity" {
		t.Fatalf("cleared override: %#v", cleared)
	}
	viaPut := requestJSON[domain.Cycle](t, handler, http.MethodPut, "/api/cycles/"+next.ID+"/capacity", map[string]any{"capacity": 15}, http.StatusOK)
	if viaPut.Capacity != 15 || viaPut.CapacitySource != "override" {
		t.Fatalf("PUT capacity: %#v", viaPut)
	}
	zeroed := requestJSON[domain.Cycle](t, handler, http.MethodPut, "/api/cycles/"+next.ID+"/capacity", map[string]any{"capacity": 0}, http.StatusOK)
	if zeroed.Capacity != 8 || zeroed.CapacityOverride != nil {
		t.Fatalf("PUT capacity 0: %#v", zeroed)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/cycles/"+next.ID, map[string]any{"capacity": -3}, http.StatusBadRequest)
}
