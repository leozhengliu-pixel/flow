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

const (
	slaWorkWeekMonFri = "monFri"
	slaWorkWeekSunThu = "sunThu"
)

// slaWorkWeek is the workspace's business week for business-hour SLAs.
func slaWorkWeek(data *domain.Bootstrap) string {
	settings, _ := data.Settings["sla"].(map[string]any)
	if value, _ := settings["workWeek"].(string); value == slaWorkWeekSunThu {
		return slaWorkWeekSunThu
	}
	return slaWorkWeekMonFri
}

func nonWorkingDay(day time.Weekday, workWeek string) bool {
	if workWeek == slaWorkWeekSunThu {
		return day == time.Friday || day == time.Saturday
	}
	return day == time.Saturday || day == time.Sunday
}

func slaTimezone(data *domain.Bootstrap, issue domain.Issue) *time.Location {
	zone, err := time.LoadLocation(data.TeamSettings[issue.Team.ID].Timezone)
	if err != nil {
		return time.UTC
	}
	return zone
}

func businessDeadline(from time.Time, minutes int, zone *time.Location, workWeek string) time.Time {
	if minutes <= 0 {
		return from
	}
	current := from.In(zone)
	for day := 0; day < minutes/1440; day++ {
		current = businessDayOffset(current, 1, zone, workWeek)
	}
	current = current.Add(time.Duration(minutes%1440) * time.Minute)
	for nonWorkingDay(current.Weekday(), workWeek) {
		current = current.AddDate(0, 0, 1)
	}
	return current.UTC()
}

func businessDayOffset(from time.Time, days int, zone *time.Location, workWeek string) time.Time {
	current := from.In(zone)
	direction := 1
	if days < 0 {
		direction = -1
	}
	for remaining := absInt(days); remaining > 0; {
		current = current.AddDate(0, 0, direction)
		if !nonWorkingDay(current.Weekday(), workWeek) {
			remaining--
		}
	}
	return current
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func businessMinutes(from, to time.Time, zone *time.Location, workWeek string) int {
	if from.After(to) {
		return -businessMinutes(to, from, zone, workWeek)
	}
	current := from.In(zone)
	end := to.In(zone)
	duration := time.Duration(0)
	for current.Before(end) {
		start := time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, zone)
		close := start.AddDate(0, 0, 1)
		if !nonWorkingDay(current.Weekday(), workWeek) {
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
		return businessDeadline(now, minutes, slaTimezone(data, issue), slaWorkWeek(data))
	}
	return now.Add(time.Duration(minutes) * time.Minute)
}
