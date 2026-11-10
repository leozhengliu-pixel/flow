import { describe, expect, it } from 'vitest'
import { unavailableDisplayProperties } from './display-property-availability'
import type { BootstrapData } from '@/types/flow'

const data = (overrides: Record<string, unknown> = {}) => ({
  teams: [{ id: 'a' }, { id: 'b' }],
  teamSettings: { a: { estimateType: 'fibonacci' }, b: {} },
  cycleSettings: { b: { enabled: true } },
  settings: {},
  workspaceSettings: { featureFlags: {} },
  releasePipelines: [],
  ...overrides,
}) as unknown as BootstrapData

describe('unavailableDisplayProperties', () => {
  it('leaves out what no team in scope has', () => {
    expect(unavailableDisplayProperties(data(), ['a'])).toEqual(['cycle', 'sla', 'customers', 'customerRevenue', 'myActivity'])
    expect(unavailableDisplayProperties(data(), ['b'])).toEqual(['estimate', 'sla', 'customers', 'customerRevenue', 'myActivity'])
  })

  it('counts every team when none is given', () => {
    expect(unavailableDisplayProperties(data())).toEqual(['sla', 'customers', 'customerRevenue', 'myActivity'])
  })

  it('offers SLA once it exists, Release with the feature, Customers once customers exist, and My activity date on the activity tab', () => {
    const rich = data({ settings: { sla: { enabled: true } }, customers: [{ id: 'c' }] })
    expect(unavailableDisplayProperties(rich, undefined, { activity: true })).toEqual([])
    expect(unavailableDisplayProperties(data({ workspaceSettings: { featureFlags: { releases: false } } }), ['a', 'b'])).toContain('release')
  })
})
