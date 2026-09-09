package main

import (
	"time"

	"flow/api/internal/domain"
)

func validProjectUpdateSchedule(schedule domain.ProjectUpdateSchedule) bool {
	if schedule.Mode != "default" && schedule.Mode != "custom" && schedule.Mode != "never" {
		return false
	}
	if schedule.FrequencyDays != 1 && (schedule.FrequencyDays < 7 || schedule.FrequencyDays > 56 || schedule.FrequencyDays%7 != 0) {
		return false
	}
	if schedule.Weekday < 0 || schedule.Weekday > 6 || schedule.Hour < 0 || schedule.Hour > 23 {
		return false
	}
	_, err := time.LoadLocation(schedule.Timezone)
	return schedule.Timezone != "" && err == nil
}

func projectScheduleDays(project domain.Project, fallback int) int {
	schedule := project.UpdateSchedule
	if schedule == nil {
		return projectCadenceDays(project.UpdateCadence, fallback)
	}
	if project.Status.Type != "started" || schedule.Mode == "never" {
		return 0
	}
	if schedule.Mode == "default" {
		return fallback
	}
	return schedule.FrequencyDays
}

func projectNextUpdateDue(project domain.Project, reference time.Time, cadence int) time.Time {
	schedule := project.UpdateSchedule
	if schedule == nil || schedule.Mode != "custom" {
		return reference.AddDate(0, 0, cadence)
	}
	zone, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		zone = time.UTC
	}
	local := reference.In(zone)
	next := time.Date(local.Year(), local.Month(), local.Day(), schedule.Hour, 0, 0, 0, zone)
	if cadence == 1 {
		if !next.After(local) {
			next = next.AddDate(0, 0, 1)
		}
		return next.UTC()
	}
	days := (schedule.Weekday - int(local.Weekday()) + 7) % 7
	next = next.AddDate(0, 0, days)
	if !next.After(local) {
		next = next.AddDate(0, 0, cadence)
	} else {
		next = next.AddDate(0, 0, cadence-7)
	}
	return next.UTC()
}
