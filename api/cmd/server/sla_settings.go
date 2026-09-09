package main

import (
	"flow/api/internal/domain"
	"time"
)

func slaEnabled(data *domain.Bootstrap) bool {
	settings, _ := data.Settings["sla"].(map[string]any)
	enabled, configured := settings["enabled"].(bool)
	return !configured || enabled
}
func slaTimezone(data *domain.Bootstrap, issue domain.Issue) *time.Location {
	zone, err := time.LoadLocation(data.TeamSettings[issue.Team.ID].Timezone)
	if err != nil {
		return time.UTC
	}
	return zone
}

func businessDeadline(from time.Time, minutes int, zone *time.Location) time.Time {
	if minutes <= 0 {
		return from
	}
	current := from.In(zone)
	remaining := time.Duration(max(0, minutes)) * time.Minute
	for {
		start := time.Date(current.Year(), current.Month(), current.Day(), 9, 0, 0, 0, zone)
		end := time.Date(current.Year(), current.Month(), current.Day(), 17, 0, 0, 0, zone)
		if current.Weekday() == time.Saturday || current.Weekday() == time.Sunday || !current.Before(end) {
			current = start.AddDate(0, 0, 1)
			continue
		}
		if current.Before(start) {
			current = start
		}
		available := end.Sub(current)
		if remaining <= available {
			return current.Add(remaining).UTC()
		}
		remaining -= available
		current = start.AddDate(0, 0, 1)
	}
}

func businessMinutes(from, to time.Time, zone *time.Location) int {
	if from.After(to) {
		return -businessMinutes(to, from, zone)
	}
	current := from.In(zone)
	end := to.In(zone)
	duration := time.Duration(0)
	for current.Before(end) {
		start := time.Date(current.Year(), current.Month(), current.Day(), 9, 0, 0, 0, zone)
		close := time.Date(current.Year(), current.Month(), current.Day(), 17, 0, 0, 0, zone)
		if current.Weekday() != time.Saturday && current.Weekday() != time.Sunday {
			left := current
			if left.Before(start) {
				left = start
			}
			right := end
			if right.After(close) {
				right = close
			}
			if right.After(left) {
				duration += right.Sub(left)
			}
		}
		current = start.AddDate(0, 0, 1)
	}
	return int(duration / time.Minute)
}

func slaDeadline(data *domain.Bootstrap, issue domain.Issue, rule domain.SLARule, now time.Time, minutes int) time.Time {
	if rule.BusinessHours {
		return businessDeadline(now, minutes, slaTimezone(data, issue))
	}
	return now.Add(time.Duration(minutes) * time.Minute)
}
