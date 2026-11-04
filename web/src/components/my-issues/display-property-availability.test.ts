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
    expect(unavailableDisplayProperties(data(), ['a'])).toEqual(['cycle', 'sla', 'release', 'myActivity'])
    expect(unavailableDisplayProperties(data(), ['b'])).toEqual(['estimate', 'sla', 'release', 'myActivity'])
  })

  it('counts every team when none is given', () => {
    expect(unavailableDisplayProperties(data())).toEqual(['sla', 'release', 'myActivity'])
  })

  it('offers SLA and Release once they exist, and My activity date on the activity tab', () => {
    const rich = data({ settings: { sla: { enabled: true } }, releasePipelines: [{ id: 'p' }] })
    expect(unavailableDisplayProperties(rich, undefined, { activity: true })).toEqual([])
    expect(unavailableDisplayProperties(data({ releasePipelines: [{ id: 'p' }], workspaceSettings: { featureFlags: { releases: false } } }), ['a', 'b'])).toContain('release')
  })
})
