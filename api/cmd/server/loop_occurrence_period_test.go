package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// The occurrence key follows the schedule's period in the loop's timezone: the ISO week for weekly schedules, the
// month for monthly ones. A manual run of a weekly or monthly loop is for the period of the most recent scheduled
// occurrence.
func TestLoopRunOccurrenceFollowsTheSchedulePeriod(t *testing.T) {
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	newYork, _ := time.LoadLocation("America/New_York")
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	loop := func(config map[string]any) domain.Loop {
		config["startDate"] = "2026-01-01"
		return domain.Loop{TriggerType: "schedule", CreatedAt: created, TriggerConfig: config}
	}
	friday := loop(map[string]any{"unit": "week", "interval": 1, "weekdays": []any{"fri"}, "time": "17:00", "timezone": "Asia/Shanghai"})
	thursdayUTC := loop(map[string]any{"unit": "week", "interval": 1, "weekdays": []any{"thu"}, "time": "09:00"})
	mondayEarly := loop(map[string]any{"unit": "week", "interval": 1, "weekdays": []any{"mon"}, "time": "00:30", "timezone": "Asia/Shanghai"})
	everyOtherWeek := loop(map[string]any{"unit": "week", "interval": 2, "time": "10:00", "timezone": "Asia/Shanghai"})
	monthly := loop(map[string]any{"unit": "month", "interval": 1, "time": "09:00", "timezone": "America/New_York"})
	monthlyShanghai := loop(map[string]any{"unit": "month", "interval": 1, "time": "00:30", "timezone": "Asia/Shanghai"})
	daily := loop(map[string]any{"unit": "day", "interval": 1, "time": "23:30", "timezone": "Asia/Shanghai"})
	future := domain.Loop{TriggerType: "schedule", CreatedAt: created, TriggerConfig: map[string]any{"unit": "week", "weekdays": []any{"fri"}, "time": "17:00", "timezone": "Asia/Shanghai", "startDate": "2027-06-01"}}
	scheduled := func(at time.Time) loopTrigger { return loopTrigger{Kind: "schedule", ScheduledAt: at} }
	manual := loopTrigger{Kind: "manual"}
	for _, item := range []struct {
		name    string
		loop    domain.Loop
		trigger loopTrigger
		now     time.Time
		want    string
	}{
		{"a scheduled Friday run is for its ISO week", friday, scheduled(time.Date(2026, 10, 30, 17, 0, 0, 0, shanghai)), time.Date(2026, 10, 30, 17, 1, 0, 0, shanghai), "2026-W44"},
		{"a late scheduled run keeps its week", friday, scheduled(time.Date(2026, 10, 30, 17, 0, 0, 0, shanghai)), time.Date(2026, 11, 2, 9, 0, 0, 0, shanghai), "2026-W44"},
		{"a manual run on Monday is for last Friday's week", friday, manual, time.Date(2026, 11, 2, 10, 0, 0, 0, shanghai), "2026-W44"},
		{"a manual run on Wednesday, before this week's Friday, is for last week's", friday, manual, time.Date(2026, 10, 28, 10, 0, 0, 0, shanghai), "2026-W43"},
		{"a manual run right after the Friday run is for this week", friday, manual, time.Date(2026, 10, 30, 17, 5, 0, 0, shanghai), "2026-W44"},
		{"the week is the loop's timezone's: Monday 00:30 in Shanghai is Sunday in UTC", mondayEarly, scheduled(time.Date(2026, 11, 2, 0, 30, 0, 0, shanghai)), time.Date(2026, 11, 2, 0, 31, 0, 0, shanghai), "2026-W45"},
		{"a manual run late on Sunday in UTC, already Monday in Shanghai after the run", mondayEarly, manual, time.Date(2026, 11, 1, 17, 0, 0, 0, time.UTC), "2026-W45"},
		{"a manual run early on Monday in Shanghai, before 00:30, is for last week", mondayEarly, manual, time.Date(2026, 11, 1, 16, 15, 0, 0, time.UTC), "2026-W44"},
		{"ISO weeks cross the year: Friday 1 January is in the last Thursday's week", thursdayUTC, manual, time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC), "2026-W53"},
		{"a two-week schedule's off week maps to its last occurrence", everyOtherWeek, manual, time.Date(2026, 1, 9, 12, 0, 0, 0, shanghai), "2026-W01"},
		{"a scheduled monthly run is for its month", monthly, scheduled(time.Date(2026, 11, 1, 9, 0, 0, 0, newYork)), time.Date(2026, 11, 1, 9, 1, 0, 0, newYork), "2026-11"},
		{"a manual run later in the month", monthly, manual, time.Date(2026, 11, 20, 12, 0, 0, 0, newYork), "2026-11"},
		{"a manual run on the 1st before the monthly run is for last month", monthly, manual, time.Date(2026, 12, 1, 8, 0, 0, 0, newYork), "2026-11"},
		{"a manual run on the 1st after it is for this month", monthly, manual, time.Date(2026, 12, 1, 10, 0, 0, 0, newYork), "2026-12"},
		{"the month is the loop's timezone's", monthlyShanghai, scheduled(time.Date(2026, 12, 1, 0, 30, 0, 0, shanghai)), time.Date(2026, 12, 1, 0, 31, 0, 0, shanghai), "2026-12"},
		{"a daily loop's manual run is for today", daily, manual, time.Date(2026, 10, 26, 16, 0, 0, 0, time.UTC), "2026-10-27"},
		{"before the first occurrence a manual run is for now's week", future, manual, time.Date(2026, 10, 28, 10, 0, 0, 0, shanghai), "2026-W44"},
	} {
		if got := loopRunOccurrence(item.loop, item.trigger, item.now); got != item.want {
			t.Errorf("%s: occurrence %q, want %q", item.name, got, item.want)
		}
	}
	for occurrence, want := range map[string]string{
		"2026-W44":      "week 2026-W44, 2026-10-26 to 2026-11-01",
		"2026-W53":      "week 2026-W53, 2026-12-28 to 2027-01-03",
		"2026-W01":      "week 2026-W01, 2025-12-29 to 2026-01-04",
		"2026-10":       "month 2026-10",
		"2026-10-27":    "2026-10-27",
		"2026-10-27T09": "2026-10-27T09",
	} {
		if got := loopOccurrenceLabel(occurrence); got != want {
			t.Errorf("label(%q) = %q, want %q", occurrence, got, want)
		}
	}
}

// The report: a weekly loop run on Monday and again on Thursday of the same week appended its section twice. Now
// the second run's append is refused (the week's section exists) and the next week's run appends again.
func TestWeeklyLoopAppendsOncePerWeek(t *testing.T) {
	provider := &fakeLoopProvider{}
	srv, handler := newReliableLoopServer(t, provider)
	shanghai, _ := time.LoadLocation("Asia/Shanghai")
	original := "# 团队周报"
	document := loopTestDocument(t, handler, map[string]any{"title": "团队周报", "content": original})
	schedule := map[string]any{"interval": 1, "unit": "week", "weekdays": []any{"mon", "thu"}, "time": "09:00", "timezone": "Asia/Shanghai", "startDate": "2026-01-01"}
	loop, started := startTestRun(t, handler, "团队周报", "汇总开发团队本周工作，将周报追加到已有团队文档中。", map[string]any{"triggerConfig": schedule})
	waitForRun(t, handler, loop.ID, started.ID) // unscripted: writes nothing

	runAt := func(at time.Time, calls ...string) domain.LoopRun {
		t.Helper()
		provider.script(calls...)
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			run, err := srv.startLoopRun("test-workspace", loop.ID, loopTrigger{Kind: "schedule", ScheduledAt: at}, "")
			if err == nil {
				return waitForRun(t, handler, loop.ID, run.ID)
			}
			if !errors.Is(err, errConflict) || time.Now().After(deadline) {
				t.Fatal(err)
			}
		}
	}
	monday := "\n\n## 第 44 周\n\n- 完成登录修复"
	first := runAt(time.Date(2026, 10, 26, 9, 0, 0, 0, shanghai), loopAppendCall(document.ID, monday), `tool:finish_run {"status":"done","summary":"已追加本周周报。"}`)
	if first.Status != "completed" || first.Occurrence != "2026-W44" || first.Produced["document"] != 1 {
		t.Fatalf("Monday's run = %s %s %q occurrence %q produced %v", first.Status, first.FailureReason, first.Error, first.Occurrence, first.Produced)
	}
	// Thursday, same week: the same text and a reworded section are both refused.
	second := runAt(time.Date(2026, 10, 29, 9, 0, 0, 0, shanghai),
		loopAppendCall(document.ID, monday),
		loopAppendCall(document.ID, "\n\n## 第 44 周（周四）\n\n- 完成登录修复"),
		`tool:finish_run {"status":"done","summary":"本周周报已存在，未重复追加。"}`)
	if second.Occurrence != "2026-W44" || second.Status != "completed" || second.Produced["document"] != 0 {
		t.Fatalf("Thursday's run = %s %s %q occurrence %q produced %v", second.Status, second.FailureReason, second.Error, second.Occurrence, second.Produced)
	}
	for nth := range 2 {
		call := loopRunCall(second, "save_document", nth)
		if call.Status != "blocked" || !strings.Contains(call.Error, "already appended its section for week 2026-W44, 2026-10-26 to 2026-11-01") || call.Document == nil || call.Document.ExistingRunID != first.ID {
			t.Fatalf("Thursday's append %d = %+v document %+v", nth, call, call.Document)
		}
	}
	if content := loopTestDocumentContent(t, handler, document.ID); content != original+monday {
		t.Fatalf("the week's section is in the document once: %q", content)
	}
	// Next Monday is a new week.
	next := "\n\n## 第 45 周\n\n- 发布 2.0"
	third := runAt(time.Date(2026, 11, 2, 9, 0, 0, 0, shanghai), loopAppendCall(document.ID, next), `tool:finish_run {"status":"done","summary":"已追加本周周报。"}`)
	if third.Occurrence != "2026-W45" || third.Produced["document"] != 1 {
		t.Fatalf("next week's run = %s occurrence %q produced %v", third.Status, third.Occurrence, third.Produced)
	}
	if content := loopTestDocumentContent(t, handler, document.ID); content != original+monday+next {
		t.Fatalf("document after next week's run = %q", content)
	}

	// Run now (manual): the week of the most recent scheduled occurrence, which a scheduled run already appended.
	previous, ok := previousLoopRun(schedule, loop.CreatedAt, time.Now())
	if !ok {
		t.Fatal("the schedule has a recent occurrence")
	}
	year, week := previous.In(shanghai).ISOWeek()
	recent := runAt(previous, loopAppendCall(document.ID, "\n\n## 本周\n\n- 例行检查"), `tool:finish_run {"status":"done","summary":"已追加。"}`)
	provider.script(loopAppendCall(document.ID, "\n\n## 本周（手动）\n\n- 例行检查"), `tool:finish_run {"status":"done","summary":"本周周报已存在。"}`)
	manualRun := waitForRun(t, handler, loop.ID, startNextRun(t, handler, loop.ID).ID)
	want := loopRunOccurrence(loop, loopTrigger{Kind: "manual"}, time.Now())
	if recent.Occurrence != want || manualRun.Occurrence != want || want != fmt.Sprintf("%04d-W%02d", year, week) {
		t.Fatalf("manual run occurrence %q, scheduled %q, want %q", manualRun.Occurrence, recent.Occurrence, want)
	}
	if call := loopRunCall(manualRun, "save_document", 0); call.Status != "blocked" || !strings.Contains(call.Error, "already appended its section for week "+want) {
		t.Fatalf("the manual run's append = %+v", call)
	}
}
