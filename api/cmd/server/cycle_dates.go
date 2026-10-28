package main

import (
	"fmt"
	"time"

	"flow/api/internal/domain"
)

// Moving a cycle's dates (Change cycle dates > Move start date… / Move end date…).
//
// Cycle dates are whole UTC days and EndsAt is the cycle's last day. A moved cycle keeps at
// least two days. Only the cycles next to it move to make room:
//   - a later end that reaches the next cycle moves the next cycle's start to the day after;
//     the next cycle keeps its end, so it can shrink to its last day;
//   - an earlier start that reaches the previous cycle moves the previous cycle's end to the
//     day before; the previous cycle keeps at least two days;
//   - a start moved with shiftFollowing keeps the cycle's length and moves every later upcoming
//     cycle by the same number of days; without it only the start moves.
//
// Only an upcoming cycle's start can move. A current cycle cannot end before today.

// cycleBefore orders a team's cycles by start day, then number.
func cycleBefore(a, b *domain.Cycle) bool {
	if left, right := cycleDay(a.StartsAt), cycleDay(b.StartsAt); !left.Equal(right) {
		return left.Before(right)
	}
	return a.Number < b.Number
}

// cycleNeighbors returns the team's cycles just before and just after cycle.
func cycleNeighbors(data *domain.Bootstrap, cycle *domain.Cycle) (previous, next *domain.Cycle) {
	for index := range data.Cycles {
		other := &data.Cycles[index]
		if other.TeamID != cycle.TeamID || other.ID == cycle.ID {
			continue
		}
		if cycleBefore(other, cycle) {
			if previous == nil || cycleBefore(previous, other) {
				previous = other
			}
		} else if next == nil || cycleBefore(other, next) {
			next = other
		}
	}
	return previous, next
}

func moveCycleDates(data *domain.Bootstrap, cycle *domain.Cycle, startsAt, endsAt *time.Time, shiftFollowing bool, now time.Time) error {
	if startsAt != nil && !cycleDay(*startsAt).Equal(cycleDay(cycle.StartsAt)) {
		if err := moveCycleStart(data, cycle, cycleDay(*startsAt), shiftFollowing, now); err != nil {
			return err
		}
	}
	if endsAt != nil && !cycleDay(*endsAt).Equal(cycleDay(cycle.EndsAt)) {
		_, next := cycleNeighbors(data, cycle)
		return moveCycleEnd(cycle, next, cycleDay(*endsAt), now)
	}
	return nil
}

func moveCycleStart(data *domain.Bootstrap, cycle *domain.Cycle, start time.Time, shiftFollowing bool, now time.Time) error {
	if cycle.Status != "upcoming" {
		return fmt.Errorf("%w: only an upcoming cycle's start date can move", errInvalid)
	}
	if start.Before(cycleDay(now)) {
		return fmt.Errorf("%w: a cycle cannot start before today", errInvalid)
	}
	previous, next := cycleNeighbors(data, cycle)
	if previous != nil {
		if previous.Status == "completed" {
			if !start.After(cycleDay(previous.EndsAt)) {
				return fmt.Errorf("%w: the cycle would start before the previous cycle ended", errInvalid)
			}
		} else if start.Before(cycleDay(previous.StartsAt).AddDate(0, 0, 2)) {
			return fmt.Errorf("%w: the previous cycle needs at least two days", errInvalid)
		}
	}
	oldStart, oldEnd := cycleDay(cycle.StartsAt), cycleDay(cycle.EndsAt)
	delta := start.Sub(oldStart)
	if shiftFollowing {
		for index := range data.Cycles {
			other := &data.Cycles[index]
			if other.TeamID == cycle.TeamID && other.ID != cycle.ID && other.Status == "upcoming" && cycleBefore(cycle, other) {
				other.StartsAt = cycleDay(other.StartsAt).Add(delta)
				other.EndsAt = cycleDay(other.EndsAt).Add(delta)
				other.UpdatedAt = now
			}
		}
		cycle.StartsAt, cycle.EndsAt = start, oldEnd.Add(delta)
	} else {
		cycle.StartsAt = start
		// A start moved past the cycle's last days keeps the cycle's length.
		if start.After(oldEnd.AddDate(0, 0, -1)) {
			cycle.EndsAt = start.Add(oldEnd.Sub(oldStart))
			if next != nil && next.Status == "upcoming" && !cycle.EndsAt.Before(cycleDay(next.StartsAt)) {
				length := cycleDay(next.EndsAt).Sub(cycleDay(next.StartsAt))
				next.StartsAt = cycle.EndsAt.AddDate(0, 0, 1)
				if next.EndsAt.Before(next.StartsAt) {
					next.EndsAt = next.StartsAt.Add(length)
				}
				next.UpdatedAt = now
			}
		}
	}
	if previous != nil && previous.Status != "completed" && !start.After(cycleDay(previous.EndsAt)) {
		previous.EndsAt = start.AddDate(0, 0, -1)
		previous.UpdatedAt = now
	}
	return nil
}

func moveCycleEnd(cycle, next *domain.Cycle, end time.Time, now time.Time) error {
	if end.Before(cycleDay(cycle.StartsAt).AddDate(0, 0, 1)) {
		return fmt.Errorf("%w: a cycle needs at least two days", errInvalid)
	}
	if cycle.Status == "current" && end.Before(cycleDay(now)) {
		return fmt.Errorf("%w: the current cycle cannot end before today", errInvalid)
	}
	if next != nil && next.Status != "completed" {
		if end.After(cycleDay(next.EndsAt).AddDate(0, 0, -1)) {
			return fmt.Errorf("%w: the cycle would end after the next cycle's last day", errInvalid)
		}
		if !end.Before(cycleDay(next.StartsAt)) {
			next.StartsAt = end.AddDate(0, 0, 1)
			next.UpdatedAt = now
		}
	}
	cycle.EndsAt = end
	return nil
}
