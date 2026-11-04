import { describe, expect, it } from 'vitest'
import { parseNaturalDateInput } from './natural-date'

const now = new Date(2026, 8, 24, 10)
describe('shared typed dates', () => {
  it.each([['两周后', 'in 2 weeks'], ['下周', 'next Monday'], ['明年', 'next year'], ['下个月', 'next month'], ['二十天后', 'in 20 days'], ['5 周后', 'in 5 weeks'], ['下午 4 点', '4 pm']] as const)('parses %s across every date surface', (text, english) => {
    expect(parseNaturalDateInput(text, now)).toEqual(parseNaturalDateInput(english, now))
    expect(parseNaturalDateInput(text, now)!.getTime()).toBeGreaterThan(now.getTime())
  })
  it.each(['banana', '13 pm', 'February 30, 2027', '2026-02-30', 'tomorrow garbage'])('rejects invalid or partially parsed input %s', text => {
    expect(parseNaturalDateInput(text, now)).toBeUndefined()
  })
  it('keeps relative time and calendar arithmetic at month boundaries', () => {
    expect(parseNaturalDateInput('24h', now)).toEqual(new Date(2026, 8, 25, 10))
    expect(parseNaturalDateInput('in 2 months', now)).toEqual(new Date(2026, 10, 24, 10))
    expect(parseNaturalDateInput('tomorrow', now, { defaultHour: 9 })).toEqual(new Date(2026, 8, 25, 9))
    expect(parseNaturalDateInput('9am', now)).toEqual(new Date(2026, 8, 25, 9))
  })
})
