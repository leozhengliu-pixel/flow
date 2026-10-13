import { describe, expect, it } from 'vitest'
import { AGENT_SESSION_STATE_FILTERS, agentSessionStatesFor } from './agent-members'
import { matchesAgentSessionFilter } from '@/components/issue-explorer/issue-explorer-model'

describe('agent session filter: stale', () => {
  it('offers Stale for sessions whose agent went silent, separate from Active', () => {
    expect(AGENT_SESSION_STATE_FILTERS.map(option => option.id)).toContain('state:stale')
    expect(agentSessionStatesFor('state:stale')).toEqual(['stale'])
    const stale = { agentSessionId: 'task', agentSessionState: 'stale' as const }
    expect(matchesAgentSessionFilter(stale, ['state:stale'])).toBe(true)
    expect(matchesAgentSessionFilter(stale, ['state:active'])).toBe(false)
    expect(matchesAgentSessionFilter({ agentSessionId: 'task', agentSessionState: 'active' }, ['state:stale'])).toBe(false)
  })
})
