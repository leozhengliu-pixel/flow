import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import { agentItemsToMarkdown, useLinearAgentItems } from './use-linear-agent-items'
import type { AgentSession } from '@/types/flow'
import type { ApplicationActivity, ApplicationTask } from '@/lib/application-agents'

const session: AgentSession = {
  id: 's1',
  slugId: 'chat',
  userId: 'u1',
  title: 'Chat',
  favorite: false,
  location: 'page',
  issueIds: [],
  skillIds: [],
  messages: [
    { id: 'm1', role: 'user', content: 'Hi', createdAt: '2026-01-01T00:00:00Z' },
    { id: 'm2', role: 'assistant', content: 'Hello', createdAt: '2026-01-01T00:00:01Z' },
  ],
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:01Z',
}

const task: ApplicationTask = {
  id: 't1',
  issueId: 'i1',
  teamId: 'team',
  appUserId: 'app',
  creatorId: 'u1',
  status: 'complete',
  prompt: 'Do work',
  trigger: 'mention',
  version: 1,
  updatedAt: '2026-01-01T00:00:02Z',
}

const activities: ApplicationActivity[] = [
  {
    id: 'a1',
    sessionId: 'sess',
    actorId: 'app',
    type: 'response',
    body: 'Done',
    createdAt: '2026-01-01T00:00:03Z',
  },
]

describe('LS-0752 useLinearAgentItems', () => {
  it('merges session messages and task activities', () => {
    const { result } = renderHook(() =>
      useLinearAgentItems({
        sessions: [session],
        tasks: [task],
        activitiesByTaskId: { t1: activities },
      }),
    )
    expect(result.current.map(item => item.kind)).toEqual(['message', 'message', 'task', 'activity'])
    expect(agentItemsToMarkdown(result.current)).toContain('**user:** Hi')
    expect(agentItemsToMarkdown(result.current)).toContain('_response_: Done')
  })
})
