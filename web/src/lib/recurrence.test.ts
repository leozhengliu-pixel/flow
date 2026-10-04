import { describe, expect, it } from 'vitest'

import { defaultFirstDue, defaultRecurrenceFirstDue, describeRecurrence, describeRepeats, firstRecurrenceOnOrAfter, nextRecurrenceDue, parseRecurrence, recurrenceDate, recurrenceDueDate, recurrencePresetOptions, serializeRecurrence, serializeRecurrenceFrom, simpleRecurrence, toDateInput } from './recurrence'

const tuesday = new Date(2026, 8, 29)

describe('recurrence schedules', () => {
  it('keeps legacy presets and round-trips RRULE schedules', () => {
    expect(serializeRecurrence(parseRecurrence('daily', tuesday)!)).toBe('daily')
    expect(serializeRecurrence(parseRecurrence('weekdays', tuesday)!)).toBe('weekdays')
    expect(serializeRecurrence(parseRecurrence('weekly', tuesday)!)).toBe('FREQ=WEEKLY;BYDAY=TU')
    expect(serializeRecurrence(parseRecurrence('monthly', new Date(2026, 0, 31))!)).toBe('FREQ=MONTHLY;BYMONTHDAY=31')
    for (const value of ['FREQ=DAILY;INTERVAL=3', 'FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR', 'FREQ=MONTHLY;BYDAY=-1FR', 'FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=-1', 'FREQ=YEARLY;BYMONTH=11;BYDAY=4TH', 'FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=15']) {
      expect(serializeRecurrence(parseRecurrence(value, tuesday)!)).toBe(value)
    }
    expect(parseRecurrence('', tuesday)).toBeNull()
    expect(parseRecurrence('FREQ=HOURLY', tuesday)).toBeNull()
  })

  it('describes schedules like Linear', () => {
    const describe = (value: string) => describeRecurrence(value, { locale: 'en-US', anchor: tuesday })
    expect(describe('daily')).toBe('Every day')
    expect(describe('FREQ=DAILY;INTERVAL=3')).toBe('Every 3 days')
    expect(describe('weekdays')).toBe('Every weekday')
    expect(describe('weekly')).toBe('Weekly on Tue')
    expect(describe('FREQ=WEEKLY;INTERVAL=2;BYDAY=FR,MO')).toBe('Every 2 weeks on Mon, Fri')
    expect(describe('FREQ=MONTHLY;BYMONTHDAY=31')).toBe('Monthly on day 31')
    expect(describe('FREQ=MONTHLY;BYMONTHDAY=-1')).toBe('Monthly on the last day')
    expect(describe('FREQ=MONTHLY;BYDAY=2TU')).toBe('Monthly on the second Tuesday')
    expect(describe('FREQ=MONTHLY;INTERVAL=3;BYDAY=-1FR')).toBe('Every 3 months on the last Friday')
    expect(describe('FREQ=YEARLY;BYMONTH=3;BYMONTHDAY=15')).toBe('Yearly on Mar 15')
  })

  it('computes the first occurrence like the API, clamping month ends', () => {
    const first = (value: string, from: Date) => toDateInput(firstRecurrenceOnOrAfter(parseRecurrence(value, from)!, from))
    expect(first('FREQ=MONTHLY;BYMONTHDAY=31', new Date(2027, 1, 3))).toBe('2027-02-28')
    expect(first('FREQ=WEEKLY;BYDAY=MO,FR', tuesday)).toBe('2026-10-02')
    expect(first('FREQ=MONTHLY;BYDAY=-1FR', tuesday)).toBe('2026-10-30')
    expect(first('FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29', tuesday)).toBe('2027-02-28')
  })

  it('reads API instants in the team timezone', () => {
    // Team-local midnight in Tokyo is the previous day in UTC.
    expect(toDateInput(recurrenceDate('2026-10-04T15:00:00Z', 'Asia/Tokyo'))).toBe('2026-10-05')
    expect(toDateInput(recurrenceDate('2026-10-05'))).toBe('2026-10-05')
  })

  it('offers presets anchored on the first due date for the quick menu', () => {
    const values = recurrencePresetOptions(tuesday, text => text, 'en-US').map(option => [option.value, option.label])
    expect(values).toEqual([
      ['daily', 'Daily'],
      ['weekdays', 'Every weekday (Mon–Fri)'],
      ['FREQ=WEEKLY', 'Weekly on Tuesday'],
      ['FREQ=WEEKLY;INTERVAL=2', 'Every 2 weeks on Tuesday'],
      ['FREQ=MONTHLY', 'Monthly on day 29'],
      ['FREQ=YEARLY', 'Yearly on Sep 29'],
    ])
  })

  it('serializes Linear "repeats every N unit" schedules without day parts', () => {
    expect(simpleRecurrence('weekly', 1)).toBe('FREQ=WEEKLY')
    expect(simpleRecurrence('monthly', 3)).toBe('FREQ=MONTHLY;INTERVAL=3')
    expect(simpleRecurrence('daily', 0)).toBe('FREQ=DAILY')
    // A schedule on the first due date's own weekday/day stays simple; custom days keep the RRULE.
    expect(serializeRecurrenceFrom(parseRecurrence('FREQ=WEEKLY;BYDAY=TU', tuesday)!, tuesday)).toBe('FREQ=WEEKLY')
    expect(serializeRecurrenceFrom(parseRecurrence('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR', tuesday)!, tuesday)).toBe('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR')
    expect(serializeRecurrenceFrom(parseRecurrence('FREQ=YEARLY;BYMONTH=9;BYMONTHDAY=29', tuesday)!, tuesday)).toBe('FREQ=YEARLY')
    expect(serializeRecurrenceFrom(parseRecurrence('FREQ=MONTHLY;BYMONTHDAY=-1', tuesday)!, tuesday)).toBe('FREQ=MONTHLY;BYMONTHDAY=-1')
  })

  it('computes the next instance due date after a due date', () => {
    const next = (value: string, due: Date) => toDateInput(nextRecurrenceDue(parseRecurrence(value, due)!, due))
    expect(next('FREQ=WEEKLY', tuesday)).toBe('2026-10-06')
    expect(next('FREQ=WEEKLY;INTERVAL=2', tuesday)).toBe('2026-10-13')
    expect(next('FREQ=DAILY;INTERVAL=3', tuesday)).toBe('2026-10-02')
    expect(next('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR', tuesday)).toBe('2026-10-02')
    expect(next('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR', new Date(2026, 9, 2))).toBe('2026-10-12')
    expect(next('FREQ=MONTHLY', new Date(2027, 0, 31))).toBe('2027-02-28')
    expect(next('FREQ=MONTHLY;BYDAY=-1FR', tuesday)).toBe('2026-10-30')
    expect(next('FREQ=YEARLY', tuesday)).toBe('2027-09-29')
  })

  it('summarizes schedules as "Repeats every …" and reads legacy due dates', () => {
    expect(describeRepeats('FREQ=WEEKLY', { anchor: tuesday })).toBe('Repeats every week')
    expect(describeRepeats('FREQ=MONTHLY;INTERVAL=2', { anchor: tuesday })).toBe('Repeats every 2 months')
    expect(describeRepeats('weekdays', { anchor: tuesday, locale: 'en-US' })).toBe('Every weekday')
    expect(toDateInput(recurrenceDueDate({ dueDate: '2026-10-09' })!)).toBe('2026-10-09')
    expect(toDateInput(recurrenceDueDate({ nextOccurrenceAt: '2026-10-04T15:00:00Z' }, 'Asia/Tokyo')!)).toBe('2026-10-05')
    expect(recurrenceDueDate({})).toBeUndefined()
    expect(toDateInput(defaultFirstDue(tuesday))).toBe('2026-10-06')
  })

  it('picks the API\'s default first due date for a schedule set without one', () => {
    const saturday = new Date(2026, 9, 3)
    const first = (value: string) => toDateInput(defaultRecurrenceFirstDue(value, saturday)!)
    // First matching day from tomorrow: never a weekend for "every weekday".
    expect(first('daily')).toBe('2026-10-04')
    expect(first('weekdays')).toBe('2026-10-05')
    expect(first('FREQ=WEEKLY;BYDAY=MO,FR')).toBe('2026-10-05')
    // No day parts: one period from today, on today's weekday / day / date.
    expect(first('FREQ=WEEKLY')).toBe('2026-10-10')
    expect(first('weekly')).toBe('2026-10-10')
    expect(first('FREQ=WEEKLY;INTERVAL=2')).toBe('2026-10-17')
    expect(first('FREQ=MONTHLY')).toBe('2026-11-03')
    expect(first('FREQ=YEARLY')).toBe('2027-10-03')
    expect(defaultRecurrenceFirstDue('', saturday)).toBeUndefined()
  })
})
