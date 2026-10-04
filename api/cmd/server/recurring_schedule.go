package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Recurring issue schedules are stored on Issue.Recurrence as a small subset of
// iCalendar RRULE syntax so the value stays a single, human-readable string:
//
//	FREQ=DAILY[;INTERVAL=n]
//	FREQ=WEEKLY[;INTERVAL=n];BYDAY=MO,WE,FR          (weekdays = MO,TU,WE,TH,FR)
//	FREQ=MONTHLY[;INTERVAL=n];BYMONTHDAY=31           (-1 = last day; clamped to month end)
//	FREQ=MONTHLY[;INTERVAL=n];BYDAY=2TU               (1..4 or -1 = last weekday of month)
//	FREQ=YEARLY[;INTERVAL=n];BYMONTH=3;BYMONTHDAY=15  (or BYDAY=4TH with BYMONTH)
//
// The legacy preset values "daily", "weekly" and "monthly" (plus "weekdays",
// "biweekly" and "yearly") are still accepted. Presets that need a day are
// anchored on the first due date and normalised to the RRULE form.
// Occurrences are due dates (calendar dates in the team's timezone). The
// instance due on a date is replaced by the next one at 00:01 on the following
// day (recurrenceCreationInstant), when its due date has passed.

type recurrenceRule struct {
	Freq     string // DAILY, WEEKLY, MONTHLY, YEARLY
	Interval int
	ByDay    []time.Weekday // WEEKLY
	MonthDay int            // MONTHLY/YEARLY: 1..31, or -1 for the last day
	Ordinal  int            // MONTHLY/YEARLY with Weekday: 1..4, or -1 for last
	Weekday  time.Weekday
	Month    time.Month // YEARLY
}

var recurrenceWeekdayCodes = []string{"SU", "MO", "TU", "WE", "TH", "FR", "SA"}

const maxRecurrenceInterval = 99

func recurrenceWeekday(code string) (time.Weekday, bool) {
	index := slices.Index(recurrenceWeekdayCodes, code)
	return time.Weekday(index), index >= 0
}

// parseRecurrence validates a stored or submitted schedule. The returned rule
// may be incomplete (needsAnchor) for presets such as "weekly".
func parseRecurrence(value string) (recurrenceRule, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "daily":
		return recurrenceRule{Freq: "DAILY", Interval: 1}, nil
	case "weekdays":
		return recurrenceRule{Freq: "WEEKLY", Interval: 1, ByDay: []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}}, nil
	case "weekly":
		return recurrenceRule{Freq: "WEEKLY", Interval: 1}, nil
	case "biweekly":
		return recurrenceRule{Freq: "WEEKLY", Interval: 2}, nil
	case "monthly":
		return recurrenceRule{Freq: "MONTHLY", Interval: 1}, nil
	case "yearly":
		return recurrenceRule{Freq: "YEARLY", Interval: 1}, nil
	}
	rule := recurrenceRule{Interval: 1}
	upper := strings.TrimPrefix(strings.ToUpper(value), "RRULE:")
	if upper == "" {
		return rule, fmt.Errorf("%w: unknown recurrence", errInvalid)
	}
	seen := map[string]bool{}
	for _, part := range strings.Split(upper, ";") {
		key, raw, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || raw == "" || seen[key] {
			return rule, fmt.Errorf("%w: unknown recurrence", errInvalid)
		}
		seen[key] = true
		switch key {
		case "FREQ":
			if !slices.Contains([]string{"DAILY", "WEEKLY", "MONTHLY", "YEARLY"}, raw) {
				return rule, fmt.Errorf("%w: unknown recurrence frequency", errInvalid)
			}
			rule.Freq = raw
		case "INTERVAL":
			interval, err := strconv.Atoi(raw)
			if err != nil || interval < 1 || interval > maxRecurrenceInterval {
				return rule, fmt.Errorf("%w: recurrence interval must be 1-%d", errInvalid, maxRecurrenceInterval)
			}
			rule.Interval = interval
		case "BYMONTHDAY":
			day, err := strconv.Atoi(raw)
			if err != nil || day == 0 || day > 31 || day < -1 {
				return rule, fmt.Errorf("%w: invalid recurrence month day", errInvalid)
			}
			rule.MonthDay = day
		case "BYMONTH":
			month, err := strconv.Atoi(raw)
			if err != nil || month < 1 || month > 12 {
				return rule, fmt.Errorf("%w: invalid recurrence month", errInvalid)
			}
			rule.Month = time.Month(month)
		case "BYDAY":
			for _, item := range strings.Split(raw, ",") {
				item = strings.TrimSpace(item)
				if len(item) < 2 {
					return rule, fmt.Errorf("%w: invalid recurrence weekday", errInvalid)
				}
				weekday, ok := recurrenceWeekday(item[len(item)-2:])
				if !ok {
					return rule, fmt.Errorf("%w: invalid recurrence weekday", errInvalid)
				}
				if prefix := item[:len(item)-2]; prefix != "" {
					ordinal, err := strconv.Atoi(prefix)
					if err != nil || ordinal == 0 || ordinal > 4 || ordinal < -1 || rule.Ordinal != 0 {
						return rule, fmt.Errorf("%w: invalid recurrence weekday", errInvalid)
					}
					rule.Ordinal, rule.Weekday = ordinal, weekday
					continue
				}
				if !slices.Contains(rule.ByDay, weekday) {
					rule.ByDay = append(rule.ByDay, weekday)
				}
			}
		default:
			return rule, fmt.Errorf("%w: unsupported recurrence part %s", errInvalid, key)
		}
	}
	valid := true
	switch rule.Freq {
	case "DAILY":
		valid = len(rule.ByDay) == 0 && rule.Ordinal == 0 && rule.MonthDay == 0 && rule.Month == 0
	case "WEEKLY":
		valid = rule.Ordinal == 0 && rule.MonthDay == 0 && rule.Month == 0
	case "MONTHLY", "YEARLY":
		valid = len(rule.ByDay) == 0 && !(rule.Ordinal != 0 && rule.MonthDay != 0) && (rule.Freq == "YEARLY" || rule.Month == 0)
	default:
		valid = false
	}
	if !valid {
		return rule, fmt.Errorf("%w: unknown recurrence", errInvalid)
	}
	slices.SortFunc(rule.ByDay, func(a, b time.Weekday) int { return mondayIndex(a) - mondayIndex(b) })
	return rule, nil
}

func mondayIndex(day time.Weekday) int { return (int(day) + 6) % 7 }

func (rule recurrenceRule) needsAnchor() bool {
	switch rule.Freq {
	case "WEEKLY":
		return len(rule.ByDay) == 0
	case "MONTHLY":
		return rule.MonthDay == 0 && rule.Ordinal == 0
	case "YEARLY":
		return rule.Month == 0 || rule.MonthDay == 0 && rule.Ordinal == 0
	}
	return false
}

// anchored fills in the day a preset repeats on from an occurrence date.
func (rule recurrenceRule) anchored(date time.Time) recurrenceRule {
	switch rule.Freq {
	case "WEEKLY":
		if len(rule.ByDay) == 0 {
			rule.ByDay = []time.Weekday{date.Weekday()}
		}
	case "MONTHLY", "YEARLY":
		if rule.Freq == "YEARLY" && rule.Month == 0 {
			rule.Month = date.Month()
		}
		if rule.MonthDay == 0 && rule.Ordinal == 0 {
			rule.MonthDay = date.Day()
		}
	}
	return rule
}

func (rule recurrenceRule) String() string {
	parts := []string{"FREQ=" + rule.Freq}
	if rule.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(rule.Interval))
	}
	if rule.Month != 0 {
		parts = append(parts, "BYMONTH="+strconv.Itoa(int(rule.Month)))
	}
	if rule.MonthDay != 0 {
		parts = append(parts, "BYMONTHDAY="+strconv.Itoa(rule.MonthDay))
	}
	if len(rule.ByDay) > 0 {
		codes := make([]string, len(rule.ByDay))
		for i, day := range rule.ByDay {
			codes[i] = recurrenceWeekdayCodes[day]
		}
		parts = append(parts, "BYDAY="+strings.Join(codes, ","))
	}
	if rule.Ordinal != 0 {
		parts = append(parts, "BYDAY="+strconv.Itoa(rule.Ordinal)+recurrenceWeekdayCodes[rule.Weekday])
	}
	return strings.Join(parts, ";")
}

// civilDate truncates an instant to its calendar date in loc, represented as
// UTC midnight so date arithmetic never crosses a DST transition.
func civilDate(value time.Time, loc *time.Location) time.Time {
	local := value.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

// occurrenceInstant is the moment an occurrence date becomes due: local
// midnight in the team timezone.
func occurrenceInstant(date time.Time, loc *time.Location) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc).UTC()
}

// recurrenceCreationInstant is when the instance due on date has passed and
// the next one is created: 00:01 on the following day in the team timezone.
//
// In a time zone whose DST change skips local midnight (America/Santiago),
// 00:01 does not exist on that day and time.Date resolves it to the evening
// before, while the due date still runs; the instant then moves forward to
// one minute after the day actually begins.
func recurrenceCreationInstant(due time.Time, loc *time.Location) time.Time {
	day := time.Date(due.Year(), due.Month(), due.Day()+1, 0, 0, 0, 0, time.UTC)
	instant := time.Date(day.Year(), day.Month(), day.Day(), 0, 1, 0, 0, loc)
	for range 4 {
		if !civilDate(instant, loc).Before(day) {
			break
		}
		instant = instant.Add(time.Hour)
	}
	return instant.UTC()
}

// currentRecurrenceTiming reports whether next was written by the current
// timing model for an instance due on due: the creation instant after the due
// date, or a later 00:01 local (a schedule waiting for an existing instance's
// date to pass). Legacy schedules fired at local midnight.
func currentRecurrenceTiming(next, due time.Time, loc *time.Location) bool {
	expected := recurrenceCreationInstant(due, loc)
	if next.Equal(expected) {
		return true
	}
	local := next.In(loc)
	return next.After(expected) && local.Hour() == 0 && local.Minute() == 1 && local.Second() == 0
}

// reanchored moves a single-day schedule that repeats on the previous due
// date's weekday / day of month / date onto the new due date, so "every week"
// keeps following the due date when it is edited. Multi-day and nth-weekday
// rules are kept as they are.
func (rule recurrenceRule) reanchored(previous, due time.Time) recurrenceRule {
	switch rule.Freq {
	case "WEEKLY":
		if len(rule.ByDay) == 1 && rule.ByDay[0] == previous.Weekday() {
			rule.ByDay = []time.Weekday{due.Weekday()}
		}
	case "MONTHLY":
		if rule.Ordinal == 0 && rule.MonthDay == previous.Day() {
			rule.MonthDay = due.Day()
		}
	case "YEARLY":
		if rule.Ordinal == 0 && rule.Month == previous.Month() && rule.MonthDay == previous.Day() {
			rule.Month, rule.MonthDay = due.Month(), due.Day()
		}
	}
	return rule
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// dayInMonth returns this rule's occurrence within the given month.
func (rule recurrenceRule) dayInMonth(year int, month time.Month) time.Time {
	last := daysInMonth(year, month)
	if rule.Ordinal != 0 {
		if rule.Ordinal < 0 {
			date := time.Date(year, month, last, 0, 0, 0, 0, time.UTC)
			return date.AddDate(0, 0, -((int(date.Weekday()) - int(rule.Weekday) + 7) % 7))
		}
		first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
		offset := (int(rule.Weekday) - int(first.Weekday()) + 7) % 7
		return first.AddDate(0, 0, offset+7*(rule.Ordinal-1))
	}
	day := rule.MonthDay
	if day < 0 || day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func monthStart(year int, month time.Month, add int) (int, time.Month) {
	value := time.Date(year, month+time.Month(add), 1, 0, 0, 0, 0, time.UTC)
	return value.Year(), value.Month()
}

// firstOnOrAfter is the earliest date >= date matching the rule's pattern. The
// interval only applies between consecutive occurrences.
func (rule recurrenceRule) firstOnOrAfter(date time.Time) time.Time {
	switch rule.Freq {
	case "WEEKLY":
		for offset := 0; offset < 7; offset++ {
			candidate := date.AddDate(0, 0, offset)
			if slices.Contains(rule.ByDay, candidate.Weekday()) {
				return candidate
			}
		}
	case "MONTHLY":
		if candidate := rule.dayInMonth(date.Year(), date.Month()); !candidate.Before(date) {
			return candidate
		}
		return rule.dayInMonth(monthStart(date.Year(), date.Month(), 1))
	case "YEARLY":
		for year := date.Year(); year <= date.Year()+8; year++ {
			if candidate := rule.dayInMonth(year, rule.Month); !candidate.Before(date) {
				return candidate
			}
		}
	}
	return date
}

// nextAfter is the occurrence following the occurrence on date, honouring the
// interval (every N days/weeks/months/years).
func (rule recurrenceRule) nextAfter(date time.Time) time.Time {
	interval := max(rule.Interval, 1)
	switch rule.Freq {
	case "DAILY":
		return date.AddDate(0, 0, interval)
	case "WEEKLY":
		weekStart := date.AddDate(0, 0, -mondayIndex(date.Weekday()))
		current := mondayIndex(date.Weekday())
		for _, day := range rule.ByDay {
			if mondayIndex(day) > current {
				return weekStart.AddDate(0, 0, mondayIndex(day))
			}
		}
		if len(rule.ByDay) == 0 {
			return date.AddDate(0, 0, 7*interval)
		}
		return weekStart.AddDate(0, 0, 7*interval+mondayIndex(rule.ByDay[0]))
	case "MONTHLY":
		if candidate := rule.dayInMonth(date.Year(), date.Month()); candidate.After(date) {
			return candidate
		}
		return rule.dayInMonth(monthStart(date.Year(), date.Month(), interval))
	case "YEARLY":
		if candidate := rule.dayInMonth(date.Year(), rule.Month); candidate.After(date) {
			return candidate
		}
		return rule.dayInMonth(date.Year()+interval, rule.Month)
	}
	return date.AddDate(0, 0, interval)
}

// resolveRecurrenceStart returns the canonical schedule and first occurrence
// date for a schedule starting at start (a calendar date). Presets without a
// fixed day repeat on the start date's weekday/day of month.
func resolveRecurrenceStart(value string, start time.Time) (string, time.Time, error) {
	rule, err := parseRecurrence(value)
	if err != nil {
		return "", time.Time{}, err
	}
	rule = rule.anchored(start)
	return rule.String(), rule.firstOnOrAfter(start), nil
}

func parseRecurrenceDate(value string, loc *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if date, err := time.Parse("2006-01-02", value); err == nil {
		return date, nil
	}
	instant, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid next occurrence", errInvalid)
	}
	return civilDate(instant, loc), nil
}

func teamLocation(timezone string) *time.Location {
	if location, err := time.LoadLocation(strings.TrimSpace(timezone)); err == nil && location != nil {
		return location
	}
	return time.UTC
}
