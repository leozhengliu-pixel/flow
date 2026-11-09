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
    expect(unavailableDisplayProperties(data(), ['a'])).toEqual(['cycle', 'sla', 'release', 'myActivity', 'creator'])
    expect(unavailableDisplayProperties(data(), ['b'])).toEqual(['estimate', 'sla', 'release', 'myActivity', 'creator'])
  })

  it('counts every team when none is given', () => {
    expect(unavailableDisplayProperties(data())).toEqual(['sla', 'release', 'myActivity', 'creator'])
  })

  it('offers SLA and Release once they exist, and My activity date on the activity tab; Creator is a phone-only property', () => {
    const rich = data({ settings: { sla: { enabled: true } }, releasePipelines: [{ id: 'p' }] })
    expect(unavailableDisplayProperties(rich, undefined, { activity: true })).toEqual(['creator'])
    expect(unavailableDisplayProperties(data({ releasePipelines: [{ id: 'p' }], workspaceSettings: { featureFlags: { releases: false } } }), ['a', 'b'])).toContain('release')
  })
})
