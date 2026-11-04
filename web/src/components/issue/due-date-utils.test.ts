import { describe, expect, it } from 'vitest'
import type { Cycle } from '@/types/flow'
import { cycleDueDateOptions } from './due-date-utils'

const cycle = (id: string, teamId: string, startsAt: string, endsAt: string) => ({ id, teamId, startsAt, endsAt }) as Cycle

describe('cycleDueDateOptions', () => {
  const now = Date.parse('2026-11-03T12:00:00Z')
  const cycles = [
    cycle('past', 'team', '2026-10-06T00:00:00Z', '2026-10-20T00:00:00Z'),
    cycle('current', 'team', '2026-10-21T00:00:00Z', '2026-11-04T00:00:00Z'),
    cycle('later', 'team', '2026-12-02T00:00:00Z', '2026-12-16T00:00:00Z'),
    cycle('next', 'team', '2026-11-04T00:00:00Z', '2026-11-18T00:00:00Z'),
    cycle('other-team', 'other', '2026-10-21T00:00:00Z', '2026-11-30T00:00:00Z'),
  ]

  it('lists the end of the current cycle and of the next one that starts, for the issue team only', () => {
    expect(cycleDueDateOptions(cycles, 'team', now, label => label)).toEqual([
      { label: 'End of this cycle', value: '2026-11-04' },
      { label: 'End of next cycle', value: '2026-11-18' },
    ])
  })

  it('leaves out a row whose cycle does not exist and translates the labels', () => {
    expect(cycleDueDateOptions([cycles[1]], 'team', now, label => `t:${label}`)).toEqual([{ label: 't:End of this cycle', value: '2026-11-04' }])
    expect(cycleDueDateOptions(cycles, 'none', now, label => label)).toEqual([])
  })
})
