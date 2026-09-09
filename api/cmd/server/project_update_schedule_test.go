package main

import (
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestProjectUpdateScheduleValidationAndPersistence(t *testing.T) {
	schedule := domain.ProjectUpdateSchedule{Mode: "custom", FrequencyDays: 21, Weekday: 5, Hour: 14, Timezone: "Asia/Shanghai"}
	project := domain.Project{ID: "project", Status: domain.ProjectStatus{Type: "started"}}
	data := domain.Bootstrap{}
	if err := applyProjectUpdate(&data, &project, domain.ProjectMutationInput{UpdateSchedule: &schedule}); err != nil {
		t.Fatal(err)
	}
	if project.UpdateSchedule == nil || *project.UpdateSchedule != schedule || projectScheduleDays(project, 7) != 21 {
		t.Fatalf("schedule was not persisted: %#v", project.UpdateSchedule)
	}
	for _, invalid := range []domain.ProjectUpdateSchedule{
		{Mode: "other", FrequencyDays: 7, Timezone: "UTC"},
		{Mode: "custom", FrequencyDays: 8, Timezone: "UTC"},
		{Mode: "custom", FrequencyDays: 63, Timezone: "UTC"},
		{Mode: "custom", FrequencyDays: 7, Weekday: 7, Timezone: "UTC"},
		{Mode: "custom", FrequencyDays: 7, Hour: 24, Timezone: "UTC"},
		{Mode: "custom", FrequencyDays: 7, Timezone: "Invalid/Zone"},
	} {
		if validProjectUpdateSchedule(invalid) {
			t.Errorf("accepted invalid schedule: %#v", invalid)
		}
	}
	schedule.Mode = "never"
	if err := applyProjectUpdate(&data, &project, domain.ProjectMutationInput{UpdateSchedule: &schedule}); err != nil {
		t.Fatal(err)
	}
	if projectScheduleDays(project, 7) != 0 {
		t.Fatal("never should disable update reminders")
	}
	schedule.Mode = "default"
	project.UpdateSchedule = &schedule
	if projectScheduleDays(project, 14) != 14 {
		t.Fatal("default should inherit the workspace schedule")
	}
	project.Status.Type = "planned"
	if projectScheduleDays(project, 14) != 0 {
		t.Fatal("schedules should only apply to projects in progress")
	}
}

func TestProjectUpdateScheduleTimezoneAndDST(t *testing.T) {
	project := domain.Project{UpdateSchedule: &domain.ProjectUpdateSchedule{Mode: "custom", FrequencyDays: 7, Weekday: 5, Hour: 14, Timezone: "Asia/Shanghai"}}
	reference := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	if due := projectNextUpdateDue(project, reference, 7); !due.Equal(time.Date(2026, 9, 11, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("wrong weekly local time: %s", due)
	}
	project.UpdateSchedule.FrequencyDays = 21
	if due := projectNextUpdateDue(project, reference, 21); !due.Equal(time.Date(2026, 9, 25, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("wrong three-week schedule: %s", due)
	}
	project.UpdateSchedule = &domain.ProjectUpdateSchedule{Mode: "custom", FrequencyDays: 1, Hour: 9, Timezone: "America/New_York"}
	beforeDST := time.Date(2026, 3, 7, 15, 0, 0, 0, time.UTC)
	if due := projectNextUpdateDue(project, beforeDST, 1); !due.Equal(time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily reminders must follow the selected local hour across DST: %s", due)
	}
}
