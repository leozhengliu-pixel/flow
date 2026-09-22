import { beforeEach, describe, expect, it } from 'vitest'
import { AgentComposeDraftTracker } from './agent-compose-draft-tracker'

describe('LS-0027 AgentComposeDraftTracker', () => {
  beforeEach(() => {
    AgentComposeDraftTracker.clear()
  })

  it('tracks live draft ids with ttl trim', () => {
    AgentComposeDraftTracker.add('draft-a')
    AgentComposeDraftTracker.add('draft-b')
    expect(AgentComposeDraftTracker.has('draft-a')).toBe(true)
    expect(AgentComposeDraftTracker.has('draft-b')).toBe(true)
    AgentComposeDraftTracker.remove('draft-a')
    expect(AgentComposeDraftTracker.has('draft-a')).toBe(false)
    expect(Object.keys(AgentComposeDraftTracker.readLiveEntries())).toEqual(['draft-b'])
  })
})
