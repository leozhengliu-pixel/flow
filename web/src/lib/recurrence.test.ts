import { describe, expect, it } from 'vitest'

import { describeRecurrence, firstRecurrenceOnOrAfter, parseRecurrence, recurrenceDate, recurrencePresetOptions, serializeRecurrence, toDateInput } from './recurrence'

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

  it('offers anchored presets for the quick menu', () => {
    const values = recurrencePresetOptions(tuesday, text => text, 'en-US').map(option => [option.value, option.label])
    expect(values).toEqual([
      ['daily', 'Daily'],
      ['weekdays', 'Every weekday (Mon–Fri)'],
      ['FREQ=WEEKLY;BYDAY=TU', 'Weekly on Tuesday'],
      ['FREQ=WEEKLY;INTERVAL=2;BYDAY=TU', 'Every 2 weeks on Tuesday'],
      ['FREQ=MONTHLY;BYMONTHDAY=29', 'Monthly on day 29'],
      ['FREQ=YEARLY;BYMONTH=9;BYMONTHDAY=29', 'Yearly on Sep 29'],
    ])
  })
})
