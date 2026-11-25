import { describe, expect, it } from 'vitest'
import { locallyCreatedAgentSession } from '@/lib/scale-activate-a-agent-echo'

describe('scale-activate-a local Agent creation echoes', () => {
  it('keeps a locally created session owned by the initiating panel', () => {
    expect(locallyCreatedAgentSession({ type: 'agent.session_created', aggregateId: 'chat', clientId: 'this-page' }, 'this-page')).toBe('chat')
  })

  it('preserves remote creation, later updates, deletions and old-server signals', () => {
    expect(locallyCreatedAgentSession({ type: 'agent.session_created', aggregateId: 'remote', clientId: 'other-page' }, 'this-page')).toBeUndefined()
    expect(locallyCreatedAgentSession({ type: 'agent.session_updated', aggregateId: 'chat', clientId: 'this-page' }, 'this-page')).toBeUndefined()
    expect(locallyCreatedAgentSession({ type: 'agent.session_deleted', aggregateId: 'chat', clientId: 'this-page' }, 'this-page')).toBeUndefined()
    expect(locallyCreatedAgentSession({ type: 'agent.session_created', aggregateId: 'chat' }, 'this-page')).toBeUndefined()
    expect(locallyCreatedAgentSession({ type: 'agent.session_created', aggregateId: 'chat' }, '')).toBeUndefined()
  })
})
